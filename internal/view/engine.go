package view

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Options はエンジンの設定。
type Options struct {
	// FS はテンプレートのルート（"issues/show.html.tmpl" などを含む）。
	FS fs.FS
	// Reload が真なら描画のたびに FS の変更（更新時刻・サイズ・ファイル構成）を確認し、
	// 変更があれば再読み込みする（開発モード用）。
	Reload bool
	// Funcs はリクエストに依存しない追加のテンプレート関数（i18n や Redmine ヘルパーなど）。
	// 解析時に関数名が必要なため、リクエスト依存の関数も名前だけはここか RequestFuncs で登録すること。
	Funcs template.FuncMap
	// RequestFuncs はリクエストごとに束縛されるテンプレート関数を返す関数群。
	// 引数の *Render から Context・部分テンプレート描画などにアクセスできる。
	RequestFuncs []func(r *Render) template.FuncMap
	// StatementFuncs は「文」として扱う関数名（行に単独で置かれたら ERB の <% %> と同様に行ごと消える）。
	// content_for / provide / html_title（引数あり）/ reset_cycle は既定で登録済み。
	StatementFuncs map[string]StatementFunc
	// DefaultLayout は HTML 形式の既定レイアウト名（"base" → layouts/base.html）。空なら "base"。
	DefaultLayout string
	// SkipFuncCheck が真なら解析時に関数の存在を検査しない（構文だけを検査するサニティチェック用）。
	// 未登録の関数を使うテンプレートは実行時にエラーになる。
	SkipFuncCheck bool
	// FlashIcon は render_flash_messages で各メッセージの前に置くアイコン（Redmine の notice_icon）。
	FlashIcon func(r *Render, kind string) rails.HTML
}

// Engine はテンプレート集合を保持する。複数のゴルーチンから同時に使える。
type Engine struct {
	opts  Options
	stmts map[string]StatementFunc

	mu        sync.RWMutex
	set       *template.Template
	names     map[string]bool // 登録済みテンプレート名
	signature string          // Reload 用の FS 状態
}

// defaultStatementFuncs は既定の「文」関数。
var defaultStatementFuncs = map[string]StatementFunc{
	"content_for": StatementWithArgsAfterName,
	"provide":     StatementAlways,
	"html_title":  StatementWithArgs,
	"reset_cycle": StatementAlways,
	// ブロック付きヘルパーの終端（ERB の <% end %> の位置に置き、行単独なら行ごと消える）
	"end_form": StatementAlways,
	"end_tag":  StatementAlways,
}

// StatementWithArgsAfterName は第 1 引数（名前）の後にさらに引数がある場合だけ文として扱う
// （content_for "sidebar" X は設定＝文、content_for "sidebar" は内容の出力）。
const StatementWithArgsAfterName StatementFunc = 100

// New はエンジンを作成し、テンプレートを読み込む。
func New(opts Options) (*Engine, error) {
	if opts.FS == nil {
		return nil, fmt.Errorf("view: Options.FS が未設定")
	}
	if opts.DefaultLayout == "" {
		opts.DefaultLayout = "base"
	}
	e := &Engine{opts: opts, stmts: map[string]StatementFunc{}}
	for k, v := range defaultStatementFuncs {
		e.stmts[k] = v
	}
	for k, v := range opts.StatementFuncs {
		e.stmts[k] = v
	}
	if err := e.load(); err != nil {
		return nil, err
	}
	return e, nil
}

// baseFuncs は解析時に必要な関数名をすべて含む FuncMap（リクエスト依存の関数はダミー）。
func (e *Engine) baseFuncs() template.FuncMap {
	fm := genericFuncs()
	// リクエスト依存の関数名をダミーの Render で収集する
	dummy := newDummyRender(e)
	for k, v := range dummy.funcMap() {
		fm[k] = v
	}
	for k, v := range e.opts.Funcs {
		fm[k] = v
	}
	fm[escapeFuncName] = erbEscape
	fm[rawFuncName] = erbRaw
	return fm
}

const rawFuncName = "_erb_raw"

// templateFiles は FS 内の *.tmpl を列挙する。
func (e *Engine) templateFiles() ([]string, string, error) {
	var files []string
	var sig strings.Builder
	err := fs.WalkDir(e.opts.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".tmpl") {
			return nil
		}
		files = append(files, p)
		if e.opts.Reload {
			if info, err := d.Info(); err == nil {
				fmt.Fprintf(&sig, "%s:%d:%d;", p, info.ModTime().UnixNano(), info.Size())
			}
		}
		return nil
	})
	sort.Strings(files)
	return files, sig.String(), err
}

// load はすべてのテンプレートを解析して集合を作り直す。
func (e *Engine) load() error {
	files, sig, err := e.templateFiles()
	if err != nil {
		return fmt.Errorf("view: テンプレートの列挙に失敗: %w", err)
	}
	funcs := e.baseFuncs()
	set := template.New("").Funcs(funcs)
	names := map[string]bool{}
	owner := map[string]string{}
	for _, file := range files {
		src, err := fs.ReadFile(e.opts.FS, file)
		if err != nil {
			return fmt.Errorf("view: %s: %w", file, err)
		}
		name := strings.TrimSuffix(file, ".tmpl")
		trimmed, err := ERBTrim(string(src), e.stmts)
		if err != nil {
			return fmt.Errorf("view: %s: %w", file, err)
		}
		trees, err := e.parseFile(name, trimmed, funcs)
		if err != nil {
			return fmt.Errorf("view: %w", err)
		}
		escaper := escapeFuncName
		if strings.HasSuffix(name, ".text") {
			escaper = rawFuncName
		}
		for _, tt := range trees {
			if prev, dup := owner[tt.Name]; dup {
				return fmt.Errorf("view: テンプレート %q が %s と %s で重複定義されている", tt.Name, prev, file)
			}
			owner[tt.Name] = file
			addEscaper(tt, escaper)
			if _, err := set.AddParseTree(tt.Name, tt); err != nil {
				return fmt.Errorf("view: %s: %w", file, err)
			}
			names[tt.Name] = true
		}
	}
	e.mu.Lock()
	e.set, e.names, e.signature = set, names, sig
	e.mu.Unlock()
	return nil
}

// reloadIfChanged は Reload モードで FS が変化していれば再読み込みする。
func (e *Engine) reloadIfChanged() error {
	if !e.opts.Reload {
		return nil
	}
	_, sig, err := e.templateFiles()
	if err != nil {
		return err
	}
	e.mu.RLock()
	same := sig == e.signature
	e.mu.RUnlock()
	if same {
		return nil
	}
	return e.load()
}

// Has はテンプレート名（"issues/show.html"）が存在するかを返す。
func (e *Engine) Has(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.names[name]
}

// Names は登録済みのテンプレート名（define を含む）をソートして返す。
func (e *Engine) Names() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.names))
	for n := range e.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// snapshot は現在のテンプレート集合と名前表を返す。
func (e *Engine) snapshot() (*template.Template, map[string]bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.set, e.names
}

// RenderOptions は描画のオプション。
type RenderOptions struct {
	// Format は "html"（既定）/ "js" / "text" など。テンプレート名の拡張子部分になる。
	Format string
	// Layout はレイアウト名（"base" → layouts/base.<format>）。
	// 空文字列なら既定（html は Options.DefaultLayout、それ以外はなし）、NoLayout ならレイアウトなし。
	Layout string
}

// NoLayout はレイアウトを使わないことを示す RenderOptions.Layout の値。
const NoLayout = "-"

// Render はビュー name（"issues/show" または "issues/show.html"）を描画する。
// data はビューとレイアウトの両方に渡される（ERB のインスタンス変数に相当）。
func (e *Engine) Render(ctx *Context, name string, data any, opts RenderOptions) ([]byte, error) {
	if err := e.reloadIfChanged(); err != nil {
		return nil, err
	}
	r := e.NewRender(ctx, data, opts.Format)
	body, err := r.renderTemplate(r.resolveView(name), data)
	if err != nil {
		return nil, err
	}
	layout := opts.Layout
	if layout == "" && r.format == "html" {
		layout = e.opts.DefaultLayout
	}
	if layout == "" || layout == NoLayout {
		return []byte(body), nil
	}
	r.body = body
	lname := "layouts/" + layout + "." + r.format
	if !r.has(lname) {
		return nil, fmt.Errorf("view: レイアウト %q がない", lname)
	}
	out, err := r.renderTemplate(lname, data)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// execute はテンプレートを実行して文字列を返す。
func execute(set *template.Template, name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// builtinNames は text/template の組み込み関数名（解析時の存在検査用）。
var builtinNames = map[string]any{
	"and": true, "call": true, "html": true, "index": true, "slice": true, "js": true, "len": true, "not": true,
	"or": true, "print": true, "printf": true, "println": true, "urlquery": true,
	"eq": true, "ge": true, "gt": true, "le": true, "lt": true, "ne": true,
}

// parseFile は 1 ファイルを解析し、ファイル内で定義されたすべての木（define を含む）を返す。
func (e *Engine) parseFile(name, src string, funcs template.FuncMap) ([]*parse.Tree, error) {
	treeSet := map[string]*parse.Tree{}
	t := parse.New(name)
	if e.opts.SkipFuncCheck {
		t.Mode |= parse.SkipFuncCheck
	}
	if _, err := t.Parse(src, "", "", treeSet, builtinNames, map[string]any(funcs)); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(treeSet))
	for n := range treeSet {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*parse.Tree, 0, len(names))
	for _, n := range names {
		out = append(out, treeSet[n])
	}
	return out, nil
}
