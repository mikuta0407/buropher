// compat は本物の Redmine（参照）と buropher（候補）のレスポンスを正規化して比較する互換テストハーネス。
//
// 使い方:
//
//	go run ./tools/compat diff     -ref http://127.0.0.1:3998 -cand http://127.0.0.1:3000 [scenario...]
//	go run ./tools/compat snapshot -ref http://127.0.0.1:3998 [scenario...]
//	go run ./tools/compat check    -cand http://127.0.0.1:3000 [scenario...]
//	go run ./tools/compat fetch    -base http://127.0.0.1:3998 -user admin /issues/1
//
// シナリオ省略時は testdata/compat/scenarios/*.yml を使う。詳細は tools/compat/README.md。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/tools/compat/internal/client"
	"github.com/mikuta0407/buropher/tools/compat/internal/normalize"
	"github.com/mikuta0407/buropher/tools/compat/internal/report"
	"github.com/mikuta0407/buropher/tools/compat/internal/scenario"
	"github.com/mikuta0407/buropher/tools/compat/internal/udiff"
)

const (
	defaultScenarioGlob = "testdata/compat/scenarios/*.yml"
	defaultGoldenDir    = "testdata/compat/golden"
	defaultAllowlist    = "testdata/compat/allowlist.yml"
	defaultOutDir       = "compat-report"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	code := 0
	switch os.Args[1] {
	case "diff":
		code, err = runCompare(os.Args[1], os.Args[2:])
	case "check":
		code, err = runCompare(os.Args[1], os.Args[2:])
	case "snapshot":
		code, err = runSnapshot(os.Args[2:])
	case "fetch":
		err = runFetch(os.Args[2:])
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		usage(os.Stderr)
		code = 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "compat:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: compat <command> [flags] [scenario files...]

commands:
  diff      参照 (-ref) と候補 (-cand) を取得して比較する
  check     候補 (-cand) をゴールデンファイルと比較する（Ruby 不要、CI 向け）
  snapshot  参照 (-ref) の正規化済み出力をゴールデンファイルとして保存する
  fetch     1 URL を取得して正規化結果を標準出力に出す（デバッグ用）

各コマンドの -h でフラグ一覧を表示。
`)
}

// commonFlags は複数コマンドで共通のフラグ。
type commonFlags struct {
	ref, cand string
	resetRef  string
	resetCand string
	golden    string
	allowlist string
	out       string
	run       string
	timeout   time.Duration
	context   int
	verbose   bool
}

func (c *commonFlags) register(fs *flag.FlagSet, cmd string) {
	if cmd == "diff" || cmd == "snapshot" {
		fs.StringVar(&c.ref, "ref", envOr("COMPAT_REF", "http://127.0.0.1:3998"), "参照 Redmine のベース URL")
		fs.StringVar(&c.resetRef, "reset-ref", os.Getenv("COMPAT_RESET_REF"), "参照側の取得前に実行するシェルコマンド（例: tools/compat/redmine-ref.sh reset）")
	}
	if cmd == "diff" || cmd == "check" {
		fs.StringVar(&c.cand, "cand", os.Getenv("COMPAT_CAND"), "候補（buropher）のベース URL")
		fs.StringVar(&c.resetCand, "reset-cand", os.Getenv("COMPAT_RESET_CAND"), "候補側の取得前に実行するシェルコマンド")
		fs.StringVar(&c.allowlist, "allowlist", defaultAllowlist, "許容差分リスト")
		fs.StringVar(&c.out, "out", defaultOutDir, "レポート出力ディレクトリ")
		fs.IntVar(&c.context, "context", 3, "unified diff のコンテキスト行数")
	}
	if cmd == "check" || cmd == "snapshot" {
		fs.StringVar(&c.golden, "golden", defaultGoldenDir, "ゴールデンファイルのディレクトリ")
	}
	fs.StringVar(&c.run, "run", "", "実行するケースを \"<scenario>/<case id>\" の正規表現で絞り込む")
	fs.DurationVar(&c.timeout, "timeout", 60*time.Second, "1 リクエストのタイムアウト")
	fs.BoolVar(&c.verbose, "v", false, "ケースごとの結果を表示する")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// loadCases はシナリオファイル群を読み込んでケースに展開する。
func loadCases(files []string, run string) ([]scenario.Case, error) {
	if len(files) == 0 {
		m, err := filepath.Glob(defaultScenarioGlob)
		if err != nil {
			return nil, err
		}
		files = m
	}
	if len(files) == 0 {
		return nil, errors.New("シナリオファイルがありません")
	}
	var re *regexp.Regexp
	if run != "" {
		var err error
		if re, err = regexp.Compile(run); err != nil {
			return nil, fmt.Errorf("-run: %w", err)
		}
	}
	var all []scenario.Case
	names := map[string]string{}
	for _, f := range files {
		sf, err := scenario.Load(f)
		if err != nil {
			return nil, err
		}
		if prev, dup := names[sf.Name]; dup {
			return nil, fmt.Errorf("scenario name %q is used by both %s and %s", sf.Name, prev, f)
		}
		names[sf.Name] = f
		cs, err := sf.Cases()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, c := range cs {
			if re != nil && !re.MatchString(c.Scenario+"/"+c.ID) {
				continue
			}
			all = append(all, c)
		}
	}
	return all, nil
}

// document はレスポンスを比較用テキスト（ヘッダ部 + 正規化済み本文）に変換する。
func document(c scenario.Case, resp *client.Response, base string) (string, normalize.Format, error) {
	n, err := normalize.New(c.Normalize, base)
	if err != nil {
		return "", "", err
	}
	format := c.Format
	if format == "" {
		format = normalize.DetectFormat(resp.ContentType)
	}
	mt := ""
	if resp.ContentType != "" {
		if m, _, err := mime.ParseMediaType(resp.ContentType); err == nil {
			mt = m
		} else {
			mt = resp.ContentType
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "status: %d\n", resp.Status)
	fmt.Fprintf(&b, "content-type: %s\n", mt)
	if resp.Location != "" {
		fmt.Fprintf(&b, "location: %s\n", n.String(resp.Location))
	}
	b.WriteString("\n")
	if len(resp.Body) > 0 {
		body, err := n.Normalize(format, resp.Body)
		if err != nil {
			return "", "", err
		}
		b.WriteString(body)
	}
	return b.String(), format, nil
}

func fetchDoc(t *client.Target, c scenario.Case) (string, normalize.Format, int, error) {
	resp, err := t.Do(c)
	if err != nil {
		return "", "", 0, err
	}
	doc, format, err := document(c, resp, t.BaseString())
	return doc, format, resp.Status, err
}

func newResult(c scenario.Case) report.Result {
	return report.Result{Scenario: c.Scenario, ID: c.ID, Method: c.Method, Path: c.Path, User: c.User}
}

// goldenPath は既存のゴールデンファイルを探す（拡張子は保存時のフォーマットによる）。
func goldenPath(dir string, c scenario.Case) (string, bool) {
	for _, ext := range []string{"html", "json", "xml", "txt"} {
		p := filepath.Join(dir, c.Scenario, c.ID+"."+ext)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return filepath.Join(dir, c.Scenario, c.ID+".txt"), false
}

// fetched は 1 ケース分の取得結果。
type fetched struct {
	doc    string
	format normalize.Format
	name   string // diff ヘッダに出す名前
	err    error
}

// runReset はターゲットのパス開始前に実行するリセットコマンドを走らせる。
func runReset(label, command string) error {
	if command == "" {
		return nil
	}
	fmt.Fprintf(os.Stderr, "compat: reset %s: %s\n", label, command)
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("reset %s: %w", label, err)
	}
	return nil
}

// fetchAll はターゲットに対して全ケースを順に取得する。
// Redmine は GET でも状態が変わる（最近使ったプロジェクト等）ため、
// 参照・候補それぞれに同じ順序でリクエスト列を流してから比較する。
func fetchAll(t *client.Target, side string, cases []scenario.Case) []fetched {
	out := make([]fetched, len(cases))
	for i, c := range cases {
		if c.Skip != "" {
			continue
		}
		doc, format, status, err := fetchDoc(t, c)
		if err == nil && side == "ref" && c.Expect != 0 && status != c.Expect {
			err = fmt.Errorf("status %d, expected %d", status, c.Expect)
		}
		out[i] = fetched{doc: doc, format: format, name: side + "/" + c.Scenario + "/" + c.ID + "." + format.Ext(), err: err}
	}
	return out
}

func runCompare(cmd string, args []string) (int, error) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var cf commonFlags
	cf.register(fs, cmd)
	_ = fs.Parse(args)
	if cf.cand == "" {
		return 2, errors.New("-cand (または COMPAT_CAND) が必要です")
	}
	cases, err := loadCases(fs.Args(), cf.run)
	if err != nil {
		return 2, err
	}
	al, err := report.LoadAllowlist(cf.allowlist)
	if err != nil {
		return 2, err
	}
	cand, err := client.New(cf.cand, cf.timeout)
	if err != nil {
		return 2, err
	}

	// 参照側: diff なら参照サーバから、check ならゴールデンファイルから
	var want []fetched
	refName := "golden:" + cf.golden
	if cmd == "diff" {
		ref, err := client.New(cf.ref, cf.timeout)
		if err != nil {
			return 2, err
		}
		refName = cf.ref
		if err := runReset("ref", cf.resetRef); err != nil {
			return 2, err
		}
		want = fetchAll(ref, "ref", cases)
	} else {
		want = make([]fetched, len(cases))
		for i, c := range cases {
			if c.Skip != "" {
				continue
			}
			p, ok := goldenPath(cf.golden, c)
			if !ok {
				want[i].err = fmt.Errorf("golden file not found: %s（snapshot を実行してください）", p)
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return 2, err
			}
			want[i] = fetched{doc: string(data), name: p}
		}
	}
	if err := runReset("cand", cf.resetCand); err != nil {
		return 2, err
	}
	got := fetchAll(cand, "cand", cases)

	rep := &report.Report{Title: "buropher compat " + cmd, Reference: refName, Candidate: cf.cand, Started: time.Now()}
	for i, c := range cases {
		res := newResult(c)
		switch {
		case c.Skip != "":
			res.Status, res.Message = report.Skipped, c.Skip
		case want[i].err != nil:
			res.Status, res.Message = report.Error, "reference: "+want[i].err.Error()
		case got[i].err != nil:
			res.Status, res.Message = report.Error, "candidate: "+got[i].err.Error()
		default:
			writeNormalized(cf.out, "cand", c, got[i].format, got[i].doc)
			if cmd == "diff" {
				writeNormalized(cf.out, "ref", c, want[i].format, want[i].doc)
			}
			// 製品名（Redmine / Buropher）は比較の直前に両側を同じ形にそろえる（normalize.Brand）
			res.Diff = udiff.Unified(normalize.Brand(want[i].doc), normalize.Brand(got[i].doc), want[i].name, got[i].name, cf.context)
			if res.Diff == "" {
				res.Status = report.Pass
			} else if reason, ok := al.Allowed(c.Scenario, c.ID, res.Diff); ok {
				res.Status, res.Message = report.Allowed, reason
			} else {
				res.Status = report.Fail
			}
		}
		rep.Results = append(rep.Results, res)
		logResult(cf.verbose, res)
	}
	if err := rep.WriteDir(cf.out); err != nil {
		return 2, err
	}
	fmt.Println(rep.SummaryLine())
	fmt.Printf("report: %s\n", filepath.Join(cf.out, "summary.md"))
	if rep.Failed() {
		for _, r := range rep.Results {
			if r.Status == report.Fail || r.Status == report.Error {
				fmt.Printf("  %s %s/%s %s\n", strings.ToUpper(string(r.Status)), r.Scenario, r.ID, r.Message)
			}
		}
		return 1, nil
	}
	return 0, nil
}

func writeNormalized(out, side string, c scenario.Case, f normalize.Format, doc string) {
	p := filepath.Join(out, "normalized", side, c.Scenario, c.ID+"."+f.Ext())
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
		_ = os.WriteFile(p, []byte(doc), 0o644)
	}
}

func logResult(verbose bool, r report.Result) {
	if !verbose && r.Status != report.Fail && r.Status != report.Error {
		return
	}
	msg := ""
	if r.Message != "" {
		msg = " (" + r.Message + ")"
	}
	fmt.Fprintf(os.Stderr, "%-7s %s/%s%s\n", strings.ToUpper(string(r.Status)), r.Scenario, r.ID, msg)
}

func runSnapshot(args []string) (int, error) {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs, "snapshot")
	_ = fs.Parse(args)
	cases, err := loadCases(fs.Args(), cf.run)
	if err != nil {
		return 2, err
	}
	ref, err := client.New(cf.ref, cf.timeout)
	if err != nil {
		return 2, err
	}
	if err := runReset("ref", cf.resetRef); err != nil {
		return 2, err
	}
	written := map[string]bool{}
	scenarios := map[string]bool{}
	failed := 0
	for _, c := range cases {
		scenarios[c.Scenario] = true
		if c.Skip != "" {
			// スキップ中のケースの既存ゴールデンは残す
			if p, ok := goldenPath(cf.golden, c); ok {
				written[p] = true
			}
			continue
		}
		doc, format, status, err := fetchDoc(ref, c)
		if err == nil && c.Expect != 0 && status != c.Expect {
			err = fmt.Errorf("status %d, expected %d", status, c.Expect)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR   %s/%s: %v\n", c.Scenario, c.ID, err)
			failed++
			continue
		}
		p := filepath.Join(cf.golden, c.Scenario, c.ID+"."+format.Ext())
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return 2, err
		}
		// 拡張子が変わった場合に備えて同 ID の旧ファイルを消す
		if old, ok := goldenPath(cf.golden, c); ok && old != p {
			_ = os.Remove(old)
		}
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			return 2, err
		}
		written[p] = true
		if cf.verbose {
			fmt.Fprintf(os.Stderr, "WROTE   %s (status %d)\n", p, status)
		}
	}
	// 絞り込みなしのときはシナリオから消えたケースのゴールデンを削除
	removed := 0
	if cf.run == "" && failed == 0 {
		var names []string
		for s := range scenarios {
			names = append(names, s)
		}
		sort.Strings(names)
		for _, s := range names {
			m, _ := filepath.Glob(filepath.Join(cf.golden, s, "*"))
			for _, p := range m {
				if !written[p] {
					_ = os.Remove(p)
					removed++
				}
			}
		}
	}
	fmt.Printf("snapshot: wrote=%d removed=%d errors=%d dir=%s\n", len(written), removed, failed, cf.golden)
	if failed > 0 {
		return 1, nil
	}
	return 0, nil
}

func runFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	base := fs.String("base", envOr("COMPAT_REF", "http://127.0.0.1:3998"), "ベース URL")
	user := fs.String("user", scenario.Anonymous, "ユーザー名（anonymous で未ログイン）")
	password := fs.String("password", "", "パスワード（省略時はユーザー名と同じ）")
	method := fs.String("method", "GET", "HTTP メソッド")
	format := fs.String("format", "", "html/json/xml/text（省略時は Content-Type から判定）")
	auth := fs.String("auth", "", "session/basic/none（省略時は自動）")
	raw := fs.Bool("raw", false, "正規化せずに本文を出力する")
	var headers headerFlags
	fs.Var(&headers, "H", "追加のリクエストヘッダー（\"Name: value\"。複数指定可。例: -H 'X-Requested-With: XMLHttpRequest'）")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: compat fetch [flags] /path")
	}
	pw := *password
	if pw == "" {
		pw = *user
	}
	sf := &scenario.File{
		Name:  "fetch",
		Users: map[string]scenario.Credential{*user: {Login: *user, Password: pw}},
		Requests: []scenario.Request{{Path: fs.Arg(0), Method: *method, Format: normalize.Format(*format), Auth: *auth, Users: []string{*user},
			Headers: headers.m}},
	}
	cs, err := sf.Cases()
	if err != nil {
		return err
	}
	t, err := client.New(*base, 60*time.Second)
	if err != nil {
		return err
	}
	resp, err := t.Do(cs[0])
	if err != nil {
		return err
	}
	if *raw {
		_, err = os.Stdout.Write(resp.Body)
		return err
	}
	doc, _, err := document(cs[0], resp, t.BaseString())
	if err != nil {
		return err
	}
	_, err = io.WriteString(os.Stdout, doc)
	return err
}

// headerFlags は fetch の -H（"Name: value" の繰り返し）。
type headerFlags struct{ m map[string]string }

func (h *headerFlags) String() string { return "" }

func (h *headerFlags) Set(v string) error {
	name, value, ok := strings.Cut(v, ":")
	if !ok {
		return fmt.Errorf("invalid header %q", v)
	}
	if h.m == nil {
		h.m = map[string]string{}
	}
	h.m[strings.TrimSpace(name)] = strings.TrimSpace(value)
	return nil
}
