package view

import (
	"fmt"
	"html/template"
	"path"
	"reflect"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Render は 1 回の描画（ビュー + 部分テンプレート + レイアウト）の状態を持つ。
// content_for のスロット、html_title の蓄積、cycle の状態などはここに保持される。
// テンプレート関数（RequestFuncs）からは *Render を通じてリクエスト情報にアクセスする。
type Render struct {
	engine *Engine
	// Ctx はリクエストコンテキスト。
	Ctx *Context
	// Assigns はビューに渡されたデータ（ERB のインスタンス変数相当）。部分テンプレートからは assigns 関数で参照する。
	Assigns any
	// Rails は ActionView ヘルパーの状態（CSRF トークン、cycle 等）。
	Rails *rails.View

	format string
	set    *ttemplate.Template
	names  map[string]bool
	slots  map[string]*strings.Builder
	titles []any
	body   string
	depth  int
}

// maxPartialDepth は部分テンプレートの最大入れ子（無限再帰の防止）。
const maxPartialDepth = 64

// NewRender は描画状態を作る。format が空なら "html"。
// 通常は Engine.Render を使うが、ヘルパーのテストや部分テンプレートだけの描画（XHR 応答など）に使える。
func (e *Engine) NewRender(ctx *Context, data any, format string) *Render {
	if ctx == nil {
		ctx = &Context{}
	}
	if format == "" {
		format = "html"
	}
	set, names := e.snapshot()
	r := &Render{engine: e, Ctx: ctx, Assigns: data, format: format, names: names, slots: map[string]*strings.Builder{}}
	r.Rails = newRailsView(ctx)
	clone, err := set.Clone()
	if err != nil {
		// Clone が失敗するのは実行後に Parse した場合のみで、ここでは起こらない
		panic(err)
	}
	r.set = clone.Funcs(r.funcMap())
	return r
}

func newDummyRender(e *Engine) *Render {
	ctx := &Context{}
	return &Render{engine: e, Ctx: ctx, format: "html", slots: map[string]*strings.Builder{}, Rails: newRailsView(ctx)}
}

func newRailsView(ctx *Context) *rails.View {
	v := rails.NewView(ctx.CSRFToken)
	v.ProtectAgainstForgery = ctx.CSRFToken != ""
	if ctx.AssetPath != nil {
		v.AssetPath = ctx.AssetPath
	}
	v.FormName = func(base string) string { return base + "-" + ctx.formNameSuffix() }
	v.Translate = func(key string) string { return ctx.translate(key) }
	// Rails 5 以降の既定値（config.action_view.embed_authenticity_token_in_remote_forms = false）:
	// remote: true の form_tag には authenticity_token を埋め込まない
	embed := false
	v.EmbedAuthenticityTokenInRemoteForms = &embed
	return v
}

// Format は描画中の形式（"html" / "js" / "text"）。
func (r *Render) Format() string { return r.format }

// T は翻訳（l 関数と同じ）。
func (r *Render) T(key string, args ...any) string { return r.Ctx.translate(key, args...) }

func (r *Render) has(name string) bool { return r.names[name] }

// resolveView はビュー名に形式を補う（"issues/show" → "issues/show.html"）。
func (r *Render) resolveView(name string) string {
	if r.has(name) {
		return name
	}
	return name + "." + r.format
}

// ResolvePartial は部分テンプレート名を実テンプレート名に解決する。
// "issues/attributes" → "issues/_attributes.<format>"（無ければ .html）。
// スラッシュを含まない名前はコントローラ名のディレクトリ、次いで application/ から探す（Rails と同じ）。
func (r *Render) ResolvePartial(name string) (string, error) {
	dir, base := path.Split(name)
	dirs := []string{dir}
	if dir == "" {
		dirs = []string{r.Ctx.Controller + "/", "application/"}
	}
	formats := []string{r.format}
	if r.format != "html" {
		formats = append(formats, "html")
	}
	for _, d := range dirs {
		for _, f := range formats {
			cand := d + "_" + base + "." + f
			if r.has(cand) {
				return cand, nil
			}
		}
	}
	return "", fmt.Errorf("view: 部分テンプレート %q が見つからない（format=%s）", name, r.format)
}

// renderTemplate はテンプレートを実行する（再帰の深さを管理）。
func (r *Render) renderTemplate(name string, data any) (string, error) {
	if !r.has(name) {
		return "", fmt.Errorf("view: テンプレート %q がない", name)
	}
	if r.depth >= maxPartialDepth {
		return "", fmt.Errorf("view: 部分テンプレートの入れ子が深すぎる（%s）", name)
	}
	r.depth++
	defer func() { r.depth-- }()
	return execute(r.set, name, data)
}

// Partial は render partial: name, locals: locals に相当する。
func (r *Render) Partial(name string, locals any) (template.HTML, error) {
	tname, err := r.ResolvePartial(name)
	if err != nil {
		return "", err
	}
	out, err := r.renderTemplate(tname, toLocals(locals))
	return template.HTML(out), err
}

// PartialIteration は collection 描画の <name>_iteration（Rails の ActionView::PartialIteration）。
type PartialIteration struct {
	Index int
	Size  int
}

// First は最初の要素か。
func (p PartialIteration) First() bool { return p.Index == 0 }

// Last は最後の要素か。
func (p PartialIteration) Last() bool { return p.Index == p.Size-1 }

// Collection は render partial: name, collection: items, as: as, locals: locals に相当する。
// as が空なら部分テンプレートの名前（"issues/issue" → issue）を変数名にする。
// 各要素の描画では <as>（要素）, <as>_counter（0 始まり）, <as>_iteration（PartialIteration）が locals に入る。
func (r *Render) Collection(name string, items any, as string, locals any) (template.HTML, error) {
	tname, err := r.ResolvePartial(name)
	if err != nil {
		return "", err
	}
	if as == "" {
		as = path.Base(name)
	}
	list := toList(items)
	var b strings.Builder
	base := toLocals(locals)
	for i, it := range list {
		l := make(map[string]any, len(base)+3)
		for k, v := range base {
			l[k] = v
		}
		l[as] = it
		l[as+"_counter"] = i
		l[as+"_iteration"] = PartialIteration{Index: i, Size: len(list)}
		out, err := r.renderTemplate(tname, l)
		if err != nil {
			return "", err
		}
		b.WriteString(out)
	}
	return template.HTML(b.String()), nil
}

// Capture は名前付きテンプレート（{{define}} したブロック）を描画して html_safe な値として返す。
// ERB の capture do ... end / content_for :x do ... end のブロック部分に相当する。
func (r *Render) Capture(name string, data any) (template.HTML, error) {
	out, err := r.renderTemplate(name, data)
	return template.HTML(out), err
}

// ContentFor はスロットに内容を追加する（html_safe でない値はエスケープされる）。
func (r *Render) ContentFor(name string, content any) {
	b := r.slots[name]
	if b == nil {
		b = &strings.Builder{}
		r.slots[name] = b
	}
	b.WriteString(string(rails.H(content)))
}

// Slot はスロットの内容を返す。
func (r *Render) Slot(name string) template.HTML {
	if b := r.slots[name]; b != nil {
		return template.HTML(b.String())
	}
	return ""
}

// HasContentFor は content_for?(name)。
func (r *Render) HasContentFor(name string) bool {
	b := r.slots[name]
	return b != nil && rails.IsPresent(b.String())
}

// AddTitle は html_title(*args)（タイトル要素の追加）。
func (r *Render) AddTitle(args ...any) { r.titles = append(r.titles, args...) }

// HTMLTitle は引数なしの html_title（Redmine の ApplicationHelper#html_title）。
func (r *Render) HTMLTitle() string {
	title := append([]any{}, r.titles...)
	if r.Ctx.Project != nil || r.Ctx.ProjectName != "" {
		title = append(title, r.Ctx.ProjectName)
	}
	if len(title) == 0 || rails.ToS(title[len(title)-1]) != r.Ctx.AppTitle {
		title = append(title, r.Ctx.AppTitle)
	}
	var parts []string
	for _, t := range title {
		if rails.IsPresent(t) {
			parts = append(parts, rails.ToS(t))
		}
	}
	return strings.Join(parts, " - ")
}

// BodyCSSClasses は Redmine の body_css_classes。
func (r *Render) BodyCSSClasses() string {
	c := r.Ctx
	var css []string
	if c.Theme != "" {
		css = append(css, "theme-"+strings.ReplaceAll(c.Theme, " ", "_"))
	}
	if c.ProjectIdentifier != "" {
		css = append(css, "project-"+c.ProjectIdentifier)
	}
	if c.HasMainMenu {
		css = append(css, "has-main-menu")
	}
	css = append(css, "controller-"+c.Controller, "action-"+c.Action)
	if c.AvatarsEnabled {
		css = append(css, "avatars-on")
	} else {
		css = append(css, "avatars-off")
	}
	if c.TextareaFont == "monospace" || c.TextareaFont == "proportional" {
		css = append(css, "textarea-"+c.TextareaFont)
	}
	return strings.Join(css, " ")
}

// RenderFlashMessages は Redmine の render_flash_messages。
func (r *Render) RenderFlashMessages() template.HTML {
	var b strings.Builder
	for _, f := range r.Ctx.Flash {
		var icon template.HTML
		if r.engine.opts.FlashIcon != nil {
			icon = r.engine.opts.FlashIcon(r, f.Type)
		}
		b.WriteString(string(rails.ContentTag("div", icon+template.HTML(f.Message),
			rails.NewHash("class", "flash "+f.Type, "id", "flash_"+f.Type))))
	}
	return template.HTML(b.String())
}

// CSRFMetaTags は csrf_meta_tags（CSRF 保護が無効なら空）。
func (r *Render) CSRFMetaTags() template.HTML {
	if r.Ctx.CSRFToken == "" {
		return ""
	}
	return rails.Tag("meta", rails.NewHash("name", "csrf-param", "content", "authenticity_token")) + "\n" +
		rails.Tag("meta", rails.NewHash("name", "csrf-token", "content", r.Ctx.CSRFToken))
}

// toLocals は部分テンプレートに渡す locals を map[string]any に変換する。
func toLocals(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return x
	case *rails.Hash:
		m := make(map[string]any, x.Len())
		for _, e := range x.Entries() {
			m[e.Key] = e.Value
		}
		return m
	}
	if h, ok := rails.ToHash(v); ok {
		return toLocals(h)
	}
	return map[string]any{"object": v}
}

func toList(v any) []any {
	if v == nil {
		return nil
	}
	if x, ok := v.([]any); ok {
		return x
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return []any{v}
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

// dict は "k", v, ... から map[string]any を作る（部分テンプレートの locals 用）。
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: 引数の数が奇数")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: キーが文字列でない: %v", pairs[i])
		}
		m[k] = pairs[i+1]
	}
	return m, nil
}

// funcMap はこの描画に束縛されたテンプレート関数群を返す。
func (r *Render) funcMap() ttemplate.FuncMap {
	fm := ttemplate.FuncMap{}
	for k, v := range r.Rails.FuncMap() {
		fm[k] = v
	}
	fm["dict"] = dict
	fm["list"] = func(xs ...any) []any { return xs }
	fm["partial"] = func(name string, locals ...any) (template.HTML, error) {
		var l any
		if len(locals) > 0 {
			l = locals[0]
		}
		return r.Partial(name, l)
	}
	fm["render_collection"] = func(name string, items any, locals ...any) (template.HTML, error) {
		var l any
		if len(locals) > 0 {
			l = locals[0]
		}
		return r.Collection(name, items, "", l)
	}
	fm["render_collection_as"] = func(name string, items any, as string, locals ...any) (template.HTML, error) {
		var l any
		if len(locals) > 0 {
			l = locals[0]
		}
		return r.Collection(name, items, as, l)
	}
	fm["capture"] = func(name string, data ...any) (template.HTML, error) {
		var d any = r.Assigns
		if len(data) > 0 {
			d = data[0]
		}
		return r.Capture(name, d)
	}
	fm["content_for"] = func(name string, content ...any) template.HTML {
		if len(content) == 0 {
			return r.Slot(name)
		}
		for _, c := range content {
			r.ContentFor(name, c)
		}
		return ""
	}
	fm["provide"] = func(name string, content ...any) template.HTML {
		for _, c := range content {
			r.ContentFor(name, c)
		}
		return ""
	}
	fm["has_content_for"] = r.HasContentFor
	fm["yield"] = func(name ...string) template.HTML {
		if len(name) == 0 || name[0] == "" {
			return template.HTML(r.body)
		}
		return r.Slot(name[0])
	}
	fm["html_title"] = func(args ...any) string {
		if len(args) > 0 {
			r.AddTitle(args...)
			return ""
		}
		return r.HTMLTitle()
	}
	fm["render_flash_messages"] = r.RenderFlashMessages
	fm["csrf_meta_tags"] = r.CSRFMetaTags
	fm["csrf_meta_tag"] = r.CSRFMetaTags
	fm["form_authenticity_token"] = func() string { return r.Ctx.CSRFToken }
	fm["body_css_classes"] = r.BodyCSSClasses
	fm["controller_name"] = func() string { return r.Ctx.Controller }
	fm["action_name"] = func() string { return r.Ctx.Action }
	fm["current_language"] = func() string { return r.Ctx.Locale }
	fm["current_user"] = func() any { return r.Ctx.User }
	fm["current_project"] = func() any { return r.Ctx.Project }
	fm["request_path"] = func() string { return r.Ctx.RequestPath }
	fm["assigns"] = func() any { return r.Assigns }
	fm["ctx"] = func() *Context { return r.Ctx }
	fm["l"] = func(key string, args ...any) string { return r.Ctx.translate(key, args...) }
	if r.engine != nil {
		for _, f := range r.engine.opts.RequestFuncs {
			for k, v := range f(r) {
				fm[k] = v
			}
		}
	}
	return fm
}
