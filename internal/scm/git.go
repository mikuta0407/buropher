// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultGitCommand は GIT_BIN の既定値。
const DefaultGitCommand = "git"

// gitDefaultBranchNames は GIT_DEFAULT_BRANCH_NAMES。
var gitDefaultBranchNames = []string{"main", "master"}

// Git は GitAdapter（1 リポジトリ分）。
type Git struct {
	// Command は git の実行ファイル（空なら "git"）。
	Command string
	// URL はリポジトリのパス（repositories.url）。
	URL string
	// RootURL は repositories.root_url（空なら URL を使う）。
	RootURL string
	// PathEncoding は repositories.path_encoding（空なら UTF-8）。
	PathEncoding string

	branches     []Branch
	branchesDone bool
	branchesNil  bool
	tags         []string
	tagsDone     bool
	tagsNil      bool
}

// NewGit は GitAdapter.new(url, root_url, login, password, path_encoding)。
func NewGit(command, url, rootURL, pathEncoding string) *Git {
	if strings.TrimSpace(pathEncoding) == "" {
		pathEncoding = "UTF-8"
	}
	return &Git{Command: command, URL: url, RootURL: rootURL, PathEncoding: pathEncoding}
}

func (g *Git) command() string {
	if g.Command == "" {
		return DefaultGitCommand
	}
	return g.Command
}

// ---------------------------------------------------------------- バージョン

var versionCache sync.Map // command -> []int

var reVersion = regexp.MustCompile(`\A(.*?)((\d+\.)+\d+)`)

// ClientVersion は client_version（git --version の数値部。取得できなければ nil）。
func ClientVersion(command string) []int {
	if command == "" {
		command = DefaultGitCommand
	}
	if v, ok := versionCache.Load(command); ok {
		return v.([]int)
	}
	var ver []int
	out, err := exec.Command(command, "--version").Output()
	if err == nil {
		if m := reVersion.FindStringSubmatch(string(out)); m != nil {
			for _, s := range strings.Split(m[2], ".") {
				n, _ := strconv.Atoi(s)
				ver = append(ver, n)
			}
		}
	}
	versionCache.Store(command, ver)
	return ver
}

// ClientAvailable は client_available（バージョンが取れたか）。
func ClientAvailable(command string) bool { return len(ClientVersion(command)) > 0 }

// ClientVersionString は client_version_string。
func ClientVersionString(command string) string {
	v := ClientVersion(command)
	if len(v) == 0 {
		return "Unknown version"
	}
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ".")
}

// versionAbove は client_version_above?(v)（Ruby の配列比較 <=> >= 0）。
func versionAbove(have, want []int) bool {
	for i := 0; i < len(have) && i < len(want); i++ {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return len(have) >= len(want)
}

func (g *Git) versionAbove(want ...int) bool { return versionAbove(ClientVersion(g.command()), want) }

// ---------------------------------------------------------------- 実行

// gitCmd は git_cmd（--git-dir と -c オプションを付けて実行し、標準出力を返す）。
// 0 以外の終了は ErrCommandAborted。stdin が nil でなければ標準入力に渡す。
func (g *Git) gitCmd(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	repo := g.RootURL
	if repo == "" {
		repo = g.URL
	}
	full := []string{"--git-dir", repo}
	if g.versionAbove(1, 7, 2) {
		full = append(full, "-c", "core.quotepath=false", "-c", "log.decorate=no")
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, g.command(), full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		// Redmine はシェル経由で起動するため、コマンドが見つからない場合も終了コード 127 で ScmCommandAborted になる
		return out.Bytes(), ErrCommandAborted
	}
	return out.Bytes(), nil
}

// toRepo は scm_iconv(@path_encoding, 'UTF-8', s)。
func (g *Git) toRepo(s string) string {
	out, ok := Iconv(g.PathEncoding, "UTF-8", s)
	if !ok {
		return ""
	}
	return out
}

// fromRepo は scm_iconv('UTF-8', @path_encoding, s)。
func (g *Git) fromRepo(s string) string {
	out, ok := Iconv("UTF-8", g.PathEncoding, s)
	if !ok {
		return ""
	}
	return out
}

func splitLinesKeep(b []byte) []string {
	var out []string
	r := bufio.NewReader(bytes.NewReader(b))
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			out = append(out, line)
		}
		if err != nil {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------- ブランチ・タグ

var reBranch = regexp.MustCompile(`\s*(\*?)\s*(.*?)\s*([0-9a-f]{40}).*$`)

// Branches は branches（名前順。失敗時は nil）。
func (g *Git) Branches(ctx context.Context) []Branch {
	if g.branchesDone {
		if g.branchesNil {
			return nil
		}
		return g.branches
	}
	g.branchesDone = true
	out, err := g.gitCmd(ctx, []string{"branch", "--no-color", "--verbose", "--no-abbrev"}, nil)
	if err != nil {
		g.branchesNil = true
		return nil
	}
	bs := []Branch{}
	for _, line := range splitLinesKeep(out) {
		m := reBranch.FindStringSubmatch(strings.TrimRight(line, "\n"))
		if m == nil {
			continue
		}
		bs = append(bs, Branch{Name: g.fromRepo(m[2]), Scmid: m[3], IsDefault: m[1] == "*"})
	}
	sort.SliceStable(bs, func(i, j int) bool { return bs[i].Name < bs[j].Name })
	g.branches = bs
	return bs
}

// BranchNames はブランチ名の一覧。
func (g *Git) BranchNames(ctx context.Context) []string {
	bs := g.Branches(ctx)
	if bs == nil {
		return nil
	}
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}

// Tags は tags（名前順。失敗時は nil）。
func (g *Git) Tags(ctx context.Context) []string {
	if g.tagsDone {
		if g.tagsNil {
			return nil
		}
		return g.tags
	}
	g.tagsDone = true
	out, err := g.gitCmd(ctx, []string{"tag"}, nil)
	if err != nil {
		g.tagsNil = true
		return nil
	}
	lines := splitLinesKeep(out)
	sort.Strings(lines)
	tags := []string{}
	for _, t := range lines {
		tags = append(tags, g.fromRepo(strings.TrimSpace(t)))
	}
	g.tags = tags
	return tags
}

// DefaultBranch は default_branch（* の付いたブランチ、main / master、先頭の順）。
func (g *Git) DefaultBranch(ctx context.Context) string {
	bs := g.Branches(ctx)
	if len(bs) == 0 {
		return ""
	}
	for _, b := range bs {
		if b.IsDefault {
			return b.Name
		}
	}
	for _, b := range bs {
		for _, n := range gitDefaultBranchNames {
			if b.Name == n {
				return b.Name
			}
		}
	}
	return bs[0].Name
}

// ---------------------------------------------------------------- エントリ

// Entry は entry（パスの親ディレクトリを ls-tree して名前で探す。ルートは dir）。見つからなければ nil。
func (g *Git) Entry(ctx context.Context, path, identifier string) *Entry {
	parts := splitPath(path)
	if len(parts) == 0 {
		return &Entry{Path: "", Kind: "dir"}
	}
	searchPath := strings.Join(parts[:len(parts)-1], "/")
	searchName := parts[len(parts)-1]
	es, ok := g.Entries(ctx, searchPath, identifier, false)
	if !ok {
		return nil
	}
	for _, e := range es {
		if e.Name == searchName {
			return e
		}
	}
	return nil
}

// splitPath は path.to_s.split(%r{[\/\\]}).select {|n| !n.blank?}。
func splitPath(path string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

var reLsTree = regexp.MustCompile(`^\d+\s+(\w+)\s+([0-9a-f]{40})\s+([0-9-]+)\t(.+)$`)

// Entries は entries(path, identifier, :report_last_commit => ...)。失敗時は ok=false（Ruby の nil）。
func (g *Git) Entries(ctx context.Context, path, identifier string, reportLastCommit bool) ([]*Entry, bool) {
	p := g.toRepo(path)
	if identifier == "" {
		identifier = "HEAD"
	}
	out, err := g.gitCmd(ctx, []string{"ls-tree", "-l", g.toRepo(identifier) + ":" + p}, nil)
	if err != nil {
		return nil, false
	}
	var es []*Entry
	seen := map[string]bool{}
	for _, line := range splitLinesKeep(out) {
		m := reLsTree.FindStringSubmatch(strings.TrimRight(line, "\n"))
		if m == nil {
			continue
		}
		typ, size, name := m[1], m[3], m[4]
		fullPath := name
		if p != "" {
			fullPath = p + "/" + name
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		e := &Entry{Name: g.fromRepo(name), Path: g.fromRepo(fullPath), Kind: "file"}
		if typ == "tree" {
			e.Kind = "dir"
		} else {
			n := rubyToI(size)
			e.Size = &n
		}
		if reportLastCommit {
			e.Lastrev = g.Lastrev(ctx, fullPath, identifier)
		} else {
			e.Lastrev = &Revision{}
		}
		es = append(es, e)
	}
	return SortEntriesByName(es), true
}

// rubyToI は String#to_i（先頭の整数部）。
func rubyToI(s string) int64 {
	s = strings.TrimSpace(s)
	end := 0
	if end < len(s) && (s[end] == '-' || s[end] == '+') {
		end++
	}
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.ParseInt(s[:end], 10, 64)
	return n
}

// Lastrev は lastrev(path, rev)（path はリポジトリの文字コードのまま）。
func (g *Git) Lastrev(ctx context.Context, path, rev string) *Revision {
	args := []string{"log", "--no-color", "--encoding=UTF-8", "--date=iso", "--pretty=fuller", "--no-merges", "-n", "1"}
	if g.versionAbove(2, 9) {
		args = append(args, "--no-renames")
	}
	if rev != "" {
		args = append(args, rev)
	}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := g.gitCmd(ctx, args, nil)
	if err != nil {
		return nil
	}
	lines := splitLinesKeep(out)
	if len(lines) < 5 {
		return nil
	}
	f := strings.Fields(lines[0])
	am := reLastrevAuthor.FindStringSubmatch(strings.TrimRight(lines[1], "\n"))
	cm := reLastrevDate.FindStringSubmatch(strings.TrimRight(lines[4], "\n"))
	if len(f) < 2 || am == nil || cm == nil {
		return nil
	}
	t, ok := parseGitTime(cm[1])
	if !ok {
		return nil
	}
	return &Revision{Identifier: f[1], Scmid: f[1], Author: am[1], Time: t}
}

var (
	reLastrevAuthor = regexp.MustCompile(`Author:\s+(.*)$`)
	reLastrevDate   = regexp.MustCompile(`CommitDate:\s+(.*)$`)
)

// parseGitTime は Time.parse（--date=iso の "2007-12-14 09:22:52 +0000"）。
func parseGitTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range []string{"2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05 Z0700", time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------- ログ

// RevisionsOptions は revisions の options。
type RevisionsOptions struct {
	Reverse  bool
	Limit    int
	Includes []string
	Excludes []string
}

var (
	reCommit    = regexp.MustCompile(`^commit ([0-9a-f]{40})(( [0-9a-f]{40})*)$`)
	reHeader    = regexp.MustCompile(`^(\w+):\s*(.*)$`)
	reRawChange = regexp.MustCompile(`^:\d+\s+\d+\s+[0-9a-f.]+\s+[0-9a-f.]+\s+(\w)\t(.+)$`)
	reRawCopy   = regexp.MustCompile(`^:\d+\s+\d+\s+[0-9a-f.]+\s+[0-9a-f.]+\s+(\w)\d+\s+(\S+)\t(.+)$`)
)

// Revisions は revisions(path, identifier_from, identifier_to, options)。失敗時はそれまでに解析できたもの。
func (g *Git) Revisions(ctx context.Context, path, from, to string, o RevisionsOptions) []*Revision {
	args := []string{"log", "--no-color", "--encoding=UTF-8", "--raw", "--date=iso", "--pretty=fuller", "--parents", "--stdin"}
	if g.versionAbove(2, 9) {
		args = append(args, "--no-renames")
	}
	if o.Reverse {
		args = append(args, "--reverse")
	}
	if o.Limit > 0 {
		args = append(args, "-n", strconv.Itoa(o.Limit))
	}
	if path != "" {
		args = append(args, "--", g.toRepo(path))
	}
	var revs []string
	if from != "" || to != "" {
		s := ""
		if from != "" {
			s += g.toRepo(from) + ".."
		}
		if to != "" {
			s += g.toRepo(to)
		}
		revs = append(revs, s)
	} else {
		revs = append(revs, o.Includes...)
		for _, r := range o.Excludes {
			revs = append(revs, "^"+r)
		}
	}
	out, _ := g.gitCmd(ctx, args, []byte(strings.Join(revs, "\n")+"\n"))
	return g.parseLog(out)
}

type logState struct {
	commit, author, date string
	desc                 strings.Builder
	parents              []string
	hasDesc              bool
}

func (g *Git) parseLog(out []byte) []*Revision {
	var result []*Revision
	var files []Change
	cs := &logState{}
	parsing := 0 // 0: ヘッダ, 1: 説明, 2: ファイル
	flush := func() {
		t, _ := parseGitTime(cs.date)
		rev := &Revision{Identifier: cs.commit, Scmid: cs.commit, Author: cs.author, Time: t,
			Message: cs.desc.String(), Paths: files, Parents: cs.parents}
		result = append(result, rev)
	}
	for _, raw := range splitLinesKeep(out) {
		line := strings.TrimSuffix(raw, "\n")
		chomp := strings.TrimSuffix(line, "\r")
		if m := reCommit.FindStringSubmatch(line); m != nil {
			if parsing == 1 || parsing == 2 {
				parsing = 0
				flush()
				cs = &logState{}
				files = nil
			}
			cs.commit = m[1]
			if ps := strings.TrimSpace(m[2]); ps != "" {
				cs.parents = strings.Split(ps, " ")
			}
			continue
		}
		switch {
		case parsing == 0 && reHeader.MatchString(line):
			m := reHeader.FindStringSubmatch(line)
			switch m[1] {
			case "Author":
				cs.author = m[2]
			case "CommitDate":
				cs.date = m[2]
			}
		case parsing == 0 && chomp == "":
			parsing = 1
			cs.hasDesc = true
		case (parsing == 1 || parsing == 2) && reRawChange.MatchString(line):
			m := reRawChange.FindStringSubmatch(line)
			parsing = 2
			files = append(files, Change{Action: m[1], Path: g.fromRepo(m[2])})
		case (parsing == 1 || parsing == 2) && reRawCopy.MatchString(line):
			m := reRawCopy.FindStringSubmatch(line)
			parsing = 2
			files = append(files, Change{Action: m[1], Path: g.fromRepo(m[3])})
		case parsing == 1 && chomp == "":
			parsing = 2
		case parsing == 1:
			// line[4..-1]（先頭 4 文字の字下げを除く。改行は残す）
			if len(raw) > 4 {
				cs.desc.WriteString(raw[4:])
			} else if len(raw) == 4 {
				cs.desc.WriteString("")
			}
		}
	}
	if cs.commit != "" {
		flush()
	}
	return result
}

// ---------------------------------------------------------------- 差分・blame・内容

// Diff は diff(path, identifier_from, identifier_to)（行の配列。失敗時は ok=false）。
func (g *Git) Diff(ctx context.Context, path, from, to string) ([]string, bool) {
	var args []string
	if to != "" {
		args = []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", to, from}
	} else {
		args = []string{"show", "--no-color", "--no-ext-diff", "--no-textconv", from}
	}
	// --no-ext-diff / --no-textconv: リポジトリの設定（diff.external・textconv ドライバ）でコマンドを実行させない
	if g.versionAbove(2, 9) {
		args = append(args, "--no-renames")
	}
	if path != "" {
		args = append(args, "--", g.toRepo(path))
	}
	out, err := g.gitCmd(ctx, args, nil)
	if err != nil {
		return nil, false
	}
	return splitLinesKeep(out), true
}

var (
	reBlameCommit   = regexp.MustCompile(`^([0-9a-f]{39,40})\s.*`)
	reBlameAuthor   = regexp.MustCompile(`^author (.+)`)
	reBlamePrevious = regexp.MustCompile(`^previous (.+)`)
	reBlameLine     = regexp.MustCompile(`^\t(.*)`)
)

// Annotate は annotate(path, identifier)。バイナリや失敗時は nil。
func (g *Git) Annotate(ctx context.Context, path, identifier string) *Annotate {
	if strings.TrimSpace(identifier) == "" {
		identifier = "HEAD"
	}
	// --no-textconv: git blame は既定で textconv ドライバを使う（リポジトリの設定でコマンドを実行させない）
	out, err := g.gitCmd(ctx, []string{"blame", "--no-textconv", "--encoding=UTF-8", "-p", g.toRepo(identifier), "--", g.toRepo(path)}, nil)
	if err != nil {
		return nil
	}
	if IsBinary(out) {
		return nil
	}
	a := &Annotate{}
	id := ""
	authors := map[string]string{}
	prevs := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case reBlameCommit.MatchString(line):
			id = reBlameCommit.FindStringSubmatch(line)[1]
		case reBlameAuthor.MatchString(line):
			authors[id] = strings.TrimSpace(reBlameAuthor.FindStringSubmatch(line)[1])
		case reBlamePrevious.MatchString(line):
			prevs[id] = strings.TrimSpace(reBlamePrevious.FindStringSubmatch(line)[1])
		case reBlameLine.MatchString(line):
			a.Lines = append(a.Lines, reBlameLine.FindStringSubmatch(line)[1])
			a.Revisions = append(a.Revisions, &Revision{Identifier: id, Revision: id, Scmid: id, Author: authors[id]})
			a.Previous = append(a.Previous, prevs[id])
			id = ""
		}
	}
	return a
}

// Cat は cat(path, identifier)（失敗時は ok=false）。
func (g *Git) Cat(ctx context.Context, path, identifier string) ([]byte, bool) {
	if identifier == "" {
		identifier = "HEAD"
	}
	out, err := g.gitCmd(ctx, []string{"show", "--no-color", g.toRepo(identifier) + ":" + g.toRepo(path)}, nil)
	if err != nil {
		return nil, false
	}
	return out, true
}

// ValidName は valid_name?（ブランチ・タグとして存在する名前か）。
func (g *Git) ValidName(ctx context.Context, name string) bool {
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "refs/heads/") ||
		strings.HasPrefix(name, "refs/remotes/") || name == "HEAD" {
		return false
	}
	_, err := g.gitCmd(ctx, []string{"show-ref", "--heads", "--tags", "--quiet", "--", name}, nil)
	return err == nil
}
