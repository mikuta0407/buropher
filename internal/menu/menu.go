// Package menu は Redmine::MenuManager（lib/redmine/menu_manager.rb）と
// lib/redmine/preparation.rb のメニュー定義の移植。
//
// Redmine と同じ順序・同じ HTML（<ul><li><a class=".." href="..">..</a></li>..</ul>）を出力する。
package menu

import (
	"html/template"
	"strings"
)

// Project はメニュー描画に必要なプロジェクト情報。
type Project interface {
	// Identifier は URL に使う識別子（Project#to_param）。
	Identifier() string
}

// Env はメニュー描画時の環境（現在のユーザー・設定・i18n 等）。
type Env interface {
	// L は l(key) に相当する。
	L(key string) string
	// LOrHumanize は l_or_humanize(name, prefix: "label_") に相当する。
	LOrHumanize(name, prefix string) string
	LoggedIn() bool
	Admin() bool
	// AllowedTo は User.current.allowed_to?(permission, project)。
	AllowedTo(permission string, p Project) bool
	// AllowedToAction は User.current.allowed_to?({controller:, action:}, project)。
	AllowedToAction(controller, action string, p Project) bool
	// AllowedToGlobally は User.current.allowed_to?(permission, nil, global: true)。
	AllowedToGlobally(permission string) bool
	// ModuleEnabledInVisibleProject は EnabledModule.exists?(project: Project.visible, name: module)。
	ModuleEnabledInVisibleProject(module string) bool
	// Setting は Setting[name] の文字列値。
	Setting(name string) string
	// SpriteIcon は sprite_icon(icon, label) の HTML。
	SpriteIcon(icon string, label template.HTML) template.HTML
	// CurrentMenuItem はコントローラ/アクションから決まる選択中メニュー名。
	CurrentMenuItem() string

	// プロジェクトメニューの表示条件
	SharedVersionsAny(p Project) bool
	RolledUpVersionsAny(p Project) bool
	AllowedTargetTrackersAny(p Project) bool
	HasWiki(p Project) bool
	BoardsAny(p Project) bool
	RepositoriesExist(p Project) bool
}

// PermissionMode は MenuItem の :permission オプションの状態。
type PermissionMode int

const (
	// PermFromURL は :permission 未指定（URL のコントローラ/アクションで判定）。
	PermFromURL PermissionMode = iota
	// PermNamed は :permission => :name。
	PermNamed
	// PermNone は :permission => nil（判定しない）。
	PermNone
)

// Attr は順序付き HTML 属性。
type Attr struct{ Name, Value string }

// Item は MenuItem。
type Item struct {
	Name string
	// Controller/Action は URL が Hash の場合の値（権限判定とリンク先に使う）。
	Controller, Action string
	// URL はリンク先を返す。nil なら URL なし（子メニューの親）。
	URL            func(e Env, p Project) string
	PermMode       PermissionMode
	Permission     string
	Cond           func(e Env, p Project) bool
	Caption        string // i18n キー。空なら l_or_humanize(name, prefix: 'label_')
	CaptionLiteral string // 文字列キャプション（' + ' など）
	Icon           string
	HTML           []Attr // :html（class は末尾に name.dasherize が連結される）
	Last           bool
	Children       []*Item
}

// Menu はメニューのルート。
type Menu struct {
	Name  string
	Items []*Item
	last  int
}

// Push は Mapper#push（:first/:before/:after は未使用のため :last のみ対応）。
func (m *Menu) Push(it *Item, parent string) {
	if parent != "" {
		for _, p := range m.Items {
			if p.Name == parent {
				p.Children = append(p.Children, it)
				return
			}
		}
	}
	if it.Last {
		m.Items = append(m.Items, it)
		m.last++
		return
	}
	pos := len(m.Items) - m.last
	m.Items = append(m.Items[:pos], append([]*Item{it}, m.Items[pos:]...)...)
}

// Find は名前で項目を探す（ルート直下と子）。
func (m *Menu) Find(name string) *Item {
	for _, it := range m.Items {
		if it.Name == name {
			return it
		}
		for _, c := range it.Children {
			if c.Name == name {
				return c
			}
		}
	}
	return nil
}

// Allowed は MenuItem#allowed? の移植。
func (it *Item) Allowed(e Env, p Project) bool {
	if it.URL == nil {
		ok := false
		for _, c := range it.Children {
			if c.Allowed(e, p) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	} else if p != nil { // Redmine: elsif user && project（User.current は常に存在）
		switch it.PermMode {
		case PermNamed:
			if !e.AllowedTo(it.Permission, p) {
				return false
			}
		case PermFromURL:
			if it.Controller != "" && !e.AllowedToAction(it.Controller, it.Action, p) {
				return false
			}
		}
	}
	if it.Cond != nil && !it.Cond(e, p) {
		return false
	}
	return true
}

// caption は MenuItem#caption の移植。
func (it *Item) caption(e Env) string {
	switch {
	case it.CaptionLiteral != "":
		return it.CaptionLiteral
	case it.Caption != "":
		return e.L(it.Caption)
	default:
		return e.LOrHumanize(it.Name, "label_")
	}
}

// htmlAttrs は MenuItem#html_options（selected 時は class に ' selected' を追加）。
func (it *Item) htmlAttrs(selected bool) []Attr {
	attrs := make([]Attr, 0, len(it.HTML)+1)
	class := ""
	hasClass := false
	for _, a := range it.HTML {
		if a.Name == "class" {
			hasClass = true
			class = a.Value
			continue
		}
		attrs = append(attrs, a)
	}
	dash := strings.ReplaceAll(it.Name, "_", "-")
	if class != "" {
		class += " " + dash
	} else {
		class = dash
	}
	if selected {
		class += " selected"
	}
	// Ruby の Hash では既存キーへの代入は位置を保ち、新規キーは末尾に追加される。
	if hasClass {
		out := make([]Attr, 0, len(it.HTML))
		for _, a := range it.HTML {
			if a.Name == "class" {
				a.Value = class
			}
			out = append(out, a)
		}
		return out
	}
	return append(attrs, Attr{"class", class})
}

// Render は render_menu の移植。表示項目がなければ空文字を返す。
func (m *Menu) Render(e Env, p Project) template.HTML {
	var b strings.Builder
	for _, it := range m.Items {
		if it.Allowed(e, p) {
			b.WriteString(renderNode(e, it, p))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return template.HTML("<ul>" + b.String() + "</ul>")
}

// HasItems は display_main_menu? の children.present? 判定用。
func (m *Menu) HasItems() bool { return len(m.Items) > 0 }

func renderNode(e Env, it *Item, p Project) string {
	selected := e.CurrentMenuItem() == it.Name
	if len(it.Children) == 0 {
		return "<li>" + renderSingle(e, it, p, selected) + "</li>"
	}
	// render_menu_node_with_children: 要素を "\n" で連結する
	parts := []string{"<li>", renderSingle(e, it, p, selected)}
	var cb strings.Builder
	for _, c := range it.Children {
		if c.Allowed(e, p) {
			cb.WriteString(renderNode(e, c, p))
		}
	}
	if cb.Len() > 0 {
		parts = append(parts, `<ul class="menu-children">`+cb.String()+"</ul>")
	}
	parts = append(parts, "</li>")
	return strings.Join(parts, "\n")
}

func renderSingle(e Env, it *Item, p Project, selected bool) string {
	attrs := it.htmlAttrs(selected)
	url := ""
	if it.URL != nil {
		url = it.URL(e, p)
	}
	if url == "" {
		url = "#"
		// reverse_merge!(:onclick => 'return false;') は replace({onclick: ...}.merge(self)) なので、
		// onclick は（既存の値を保ったまま）常に先頭のキーになる
		onclick := Attr{"onclick", "return false;"}
		rest := make([]Attr, 0, len(attrs))
		for _, a := range attrs {
			if a.Name == "onclick" {
				onclick.Value = a.Value
				continue
			}
			rest = append(rest, a)
		}
		attrs = append([]Attr{onclick}, rest...)
	}
	label := template.HTML(template.HTMLEscapeString(it.caption(e)))
	if it.Icon != "" {
		label = e.SpriteIcon(it.Icon, label)
	}
	return linkTo(label, url, attrs)
}

// linkTo は Rails の link_to（:method 対応）と同じ属性順で <a> を出力する。
func linkTo(label template.HTML, url string, attrs []Attr) string {
	var out []Attr
	method := ""
	for _, a := range attrs {
		if a.Name == "method" {
			method = a.Value
			continue
		}
		out = append(out, a)
	}
	if method != "" && method != "get" {
		relIdx := -1
		for i, a := range out {
			if a.Name == "rel" {
				relIdx = i
			}
		}
		if relIdx < 0 {
			out = append(out, Attr{"rel", "nofollow"})
		} else if !strings.Contains(out[relIdx].Value, "nofollow") {
			out[relIdx].Value = strings.TrimLeft(out[relIdx].Value+" nofollow", " ")
		}
		out = append(out, Attr{"data-method", method})
	}
	out = append(out, Attr{"href", url})
	var b strings.Builder
	b.WriteString("<a")
	for _, a := range out {
		b.WriteString(" " + a.Name + `="` + escapeAttr(a.Value) + `"`)
	}
	b.WriteString(">" + string(label) + "</a>")
	return b.String()
}

// escapeAttr は ERB::Util.html_escape と同じ置換を行う。
func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}
