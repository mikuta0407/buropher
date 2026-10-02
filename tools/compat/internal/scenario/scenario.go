// Package scenario は互換テストのシナリオファイル（YAML / TXT）を読み込み、
// URL × ユーザーのテストケースへ展開する。
package scenario

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mikuta0407/buropher/tools/compat/internal/normalize"
)

// Anonymous は未ログインを表すユーザー名。
const Anonymous = "anonymous"

// Credential はログイン情報。
type Credential struct {
	Login    string `yaml:"login"`
	Password string `yaml:"password"`
}

// Request はシナリオ中の 1 リクエスト定義。
type Request struct {
	// ID はケース ID の接頭辞（省略時はメソッドとパスから生成）。
	ID     string `yaml:"id"`
	Path   string `yaml:"path"`
	Method string `yaml:"method"`
	// Users はこのリクエストを実行するユーザー一覧（"anonymous" で未ログイン）。
	Users []string `yaml:"users"`
	// User は Users の単数形ショートカット。
	User string `yaml:"user"`
	// Format は html/json/xml/text。省略時は Content-Type から判定。
	Format normalize.Format `yaml:"format"`
	// Auth は session（ログインフォーム+Cookie）/ basic（HTTP Basic 認証）/ none。
	// 省略時は .json/.xml などの API 形式なら basic、それ以外は session。
	Auth string `yaml:"auth"`
	// Form は application/x-www-form-urlencoded で送るフィールド（session 認証時は CSRF トークンを自動付与）。
	Form map[string]string `yaml:"form"`
	// Body は生のリクエストボディ（ContentType と併用）。
	Body        string            `yaml:"body"`
	ContentType string            `yaml:"content_type"`
	Headers     map[string]string `yaml:"headers"`
	// ExpectStatus が非 0 なら参照側のステータスがこれと一致することを確認する。
	ExpectStatus int `yaml:"expect_status"`
	// Normalize はこのリクエストに追加で適用する正規化設定。
	Normalize normalize.Config `yaml:"normalize"`
	// Skip が空でなければ実行しない（理由を書く）。
	Skip string `yaml:"skip"`
}

// File はシナリオファイル全体。
type File struct {
	Name string `yaml:"name"`
	// Users はユーザー名 → 認証情報。未定義のユーザーは login=password=ユーザー名とみなす。
	Users     map[string]Credential `yaml:"users"`
	Headers   map[string]string     `yaml:"headers"`
	Normalize normalize.Config      `yaml:"normalize"`
	Requests  []Request             `yaml:"requests"`
}

// Case は 1 回の HTTP 取得（リクエスト × ユーザー）。
type Case struct {
	Scenario  string
	ID        string
	User      string
	Cred      Credential
	Method    string
	Path      string
	Format    normalize.Format
	Auth      string
	Form      map[string]string
	Body      string
	CType     string
	Headers   map[string]string
	Expect    int
	Normalize normalize.Config
	Skip      string
}

// Load はシナリオファイルを読み込む。拡張子 .txt は簡易形式として扱う。
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f *File
	if strings.HasSuffix(path, ".txt") {
		f, err = parseTXT(data)
	} else {
		f = &File{}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		err = dec.Decode(f)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Name == "" {
		base := filepath.Base(path)
		f.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return f, nil
}

// parseTXT は 1 行 1 リクエストの簡易形式を読む:
//
//	[METHOD] /path [user1,user2] [format=json]
//
// "#" 以降はコメント。ユーザー省略時は anonymous。
func parseTXT(data []byte) (*File, error) {
	f := &File{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	ln := 0
	for sc.Scan() {
		ln++
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		r := Request{}
		if !strings.HasPrefix(fields[0], "/") {
			r.Method = strings.ToUpper(fields[0])
			fields = fields[1:]
		}
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
			return nil, fmt.Errorf("line %d: path must start with /", ln)
		}
		r.Path = fields[0]
		for _, fld := range fields[1:] {
			if v, ok := strings.CutPrefix(fld, "format="); ok {
				r.Format = normalize.Format(v)
			} else if v, ok := strings.CutPrefix(fld, "auth="); ok {
				r.Auth = v
			} else {
				r.Users = append(r.Users, strings.Split(fld, ",")...)
			}
		}
		f.Requests = append(f.Requests, r)
	}
	return f, sc.Err()
}

var reUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// pathID はパスからファイル名に使える ID を作る。
func pathID(method, path string) string {
	s := strings.TrimPrefix(path, "/")
	s = reUnsafe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "root"
	}
	if method != "GET" {
		s = strings.ToLower(method) + "_" + s
	}
	return s
}

// isAPIPath は API 形式（.json/.xml 等）のパスかを判定する。
func isAPIPath(path string, format normalize.Format) bool {
	if format == normalize.FormatJSON || format == normalize.FormatXML {
		return true
	}
	p := path
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	return strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".xml")
}

// Cases はシナリオをテストケースへ展開する。
func (f *File) Cases() ([]Case, error) {
	var out []Case
	seen := map[string]bool{}
	for i, r := range f.Requests {
		if r.Path == "" || !strings.HasPrefix(r.Path, "/") {
			return nil, fmt.Errorf("requests[%d]: path must start with /", i)
		}
		method := strings.ToUpper(r.Method)
		if method == "" {
			method = "GET"
		}
		users := r.Users
		if r.User != "" {
			users = append(users, r.User)
		}
		if len(users) == 0 {
			users = []string{Anonymous}
		}
		base := r.ID
		if base == "" {
			base = pathID(method, r.Path)
		}
		for _, u := range users {
			auth := r.Auth
			if auth == "" {
				if isAPIPath(r.Path, r.Format) {
					auth = "basic"
				} else {
					auth = "session"
				}
			}
			if u == Anonymous {
				auth = "none"
			}
			cred, ok := f.Users[u]
			if !ok {
				cred = Credential{Login: u, Password: u}
			}
			if cred.Login == "" {
				cred.Login = u
			}
			id := base + "__" + u
			if seen[id] {
				return nil, fmt.Errorf("duplicate case id %q (set id: explicitly)", id)
			}
			seen[id] = true
			headers := map[string]string{}
			for k, v := range f.Headers {
				headers[k] = v
			}
			for k, v := range r.Headers {
				headers[k] = v
			}
			out = append(out, Case{
				Scenario:  f.Name,
				ID:        id,
				User:      u,
				Cred:      cred,
				Method:    method,
				Path:      r.Path,
				Format:    r.Format,
				Auth:      auth,
				Form:      r.Form,
				Body:      r.Body,
				CType:     r.ContentType,
				Headers:   headers,
				Expect:    r.ExpectStatus,
				Normalize: f.Normalize.Merge(r.Normalize),
				Skip:      r.Skip,
			})
		}
	}
	return out, nil
}
