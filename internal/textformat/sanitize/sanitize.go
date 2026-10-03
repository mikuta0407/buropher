// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package sanitize は Redmine の CommonMark 用サニタイズ処理を移植したもの。
//
//   - Redmine::WikiFormatting::CommonMark::SanitizationFilter
//     （html-pipeline の SanitizationFilter の allowlist を Redmine がカスタマイズしたもの。
//     実体は Sanitize gem 6.1 の Sanitize.clean_node!）
//   - Redmine::WikiFormatting::CommonMark::ExternalLinksFilter
//   - Redmine::WikiFormatting::HtmlSanitizer（上記 2 つの組み合わせ）
//
// DOM は htmldom（libxml2 互換）を用いるため、Nokogiri 上で動く元実装と
// 同じ木に対して同じ順序で変換が適用される。
package sanitize

import (
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// allowedElements は html-pipeline の ALLOWLIST の要素に Redmine が input と u を加えたもの。
var allowedElements = toSet(
	"h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8", "br", "b", "i", "strong", "em", "a", "pre", "code", "img", "tt",
	"div", "ins", "del", "sup", "sub", "p", "ol", "ul", "table", "thead", "tbody", "tfoot", "blockquote",
	"dl", "dt", "dd", "kbd", "q", "samp", "var", "hr", "ruby", "rt", "rp", "li", "tr", "td", "th", "s", "strike", "summary",
	"details", "caption", "figure", "figcaption",
	"abbr", "bdo", "cite", "dfn", "mark", "small", "span", "time", "wbr",
	"input", "u",
)

// allAttributes は全要素で許可する属性（Redmine は name を除き style を加える）。
var allAttributes = []string{
	"abbr", "accept", "accept-charset",
	"accesskey", "action", "align", "alt",
	"aria-describedby", "aria-hidden", "aria-label", "aria-labelledby",
	"axis", "border", "cellpadding", "cellspacing", "char",
	"charoff", "charset", "checked",
	"clear", "cols", "colspan", "color",
	"compact", "coords", "datetime", "dir",
	"disabled", "enctype", "for", "frame",
	"headers", "height", "hreflang",
	"hspace", "ismap", "label", "lang",
	"maxlength", "media", "method",
	"multiple", "nohref", "noshade",
	"nowrap", "open", "progress", "prompt", "readonly", "rel", "rev",
	"role", "rows", "rowspan", "rules", "scope",
	"selected", "shape", "size", "span",
	"start", "summary", "tabindex", "target",
	"title", "type", "usemap", "valign", "value",
	"vspace", "width", "itemprop", "style",
}

var elementAttributes = func() map[string]map[string]bool {
	per := map[string][]string{
		"a":          {"href", "name", "id"},
		"img":        {"src", "longdesc"},
		"div":        {"itemscope", "itemtype", "class"},
		"blockquote": {"cite"},
		"del":        {"cite"},
		"ins":        {"cite"},
		"q":          {"cite"},
		"code":       {"class"},
		"p":          {"class"},
		"li":         {"id", "class"},
		"input":      {"class", "type"},
		"ul":         {"class"},
	}
	m := map[string]map[string]bool{}
	for el, attrs := range per {
		set := toSet(attrs...)
		for _, a := range allAttributes {
			set[a] = true
		}
		m[el] = set
	}
	m[""] = toSet(allAttributes...)
	return m
}()

// protocols は属性ごとに許可する URL スキーム（"" は相対 URL）。a[href] は Redmine が別途判定する。
var protocols = map[string]map[string][]string{
	"blockquote": {"cite": {"http", "https", ""}},
	"del":        {"cite": {"http", "https", ""}},
	"ins":        {"cite": {"http", "https", ""}},
	"q":          {"cite": {"http", "https", ""}},
	"img":        {"src": {"http", "https", ""}, "longdesc": {"http", "https", ""}},
}

// whitespaceElements は Sanitize の既定設定: 除去時に前後へ空白を入れる要素。
var whitespaceElements = toSet(
	"address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "footer",
	"h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup", "hr", "li", "nav", "ol", "p", "pre", "section", "ul",
)

// removeContents は html-pipeline の設定（既定値を上書きし script のみ）。
var removeContents = toSet("script")

// allowedCSSProperties は Redmine の ALLOWED_CSS_PROPERTIES。
var allowedCSSProperties = toSet(
	"color", "background-color",
	"width", "min-width", "max-width",
	"height", "min-height", "max-height",
	"padding", "padding-left", "padding-right", "padding-top", "padding-bottom",
	"margin", "margin-left", "margin-right", "margin-top", "margin-bottom",
	"border", "border-left", "border-right", "border-top", "border-bottom", "border-radius", "border-style", "border-collapse", "border-spacing",
	"font", "font-style", "font-variant", "font-weight", "font-stretch", "font-size", "line-height", "font-family",
	"text-align",
	"float",
)

func toSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

var (
	reCodeLanguage = regexp.MustCompile(`\Alanguage-\S+\z`)
	reAlertDiv     = regexp.MustCompile(`\Amarkdown-alert markdown-alert-[a-z]+\z`)
	reFnRef        = regexp.MustCompile(`\Afnref(-\d+){1,2}\z`)
	reFn           = regexp.MustCompile(`\Afn-\d+\z`)
	// Sanitize::REGEX_PROTOCOL（Ruby の \s は ASCII 空白）
	reProtocol = regexp.MustCompile(`(?i)\A[ \t\r\n\f\v]*([^/#]*?)(?::|&#0*58|&#x0*3a)`)
	reScheme   = regexp.MustCompile(`\A[a-z][a-z0-9+.\-]*\z`)
)

// rubyStrip は Ruby の String#strip（空白と NUL を除去）。
func rubyStrip(s string) string {
	return strings.Trim(s, " \t\n\v\f\r\x00")
}

// URIWithLinkSafeScheme は Redmine::Helpers::URL#uri_with_link_safe_scheme?。
func URIWithLinkSafeScheme(uri string) bool {
	m := reProtocol.FindStringSubmatch(uri)
	if m == nil {
		return true
	}
	scheme := strings.ToLower(m[1])
	if !reScheme.MatchString(scheme) {
		return false
	}
	switch scheme {
	case "data", "javascript", "vbscript":
		return false
	}
	return true
}

// transformer は Sanitize の transformer（ノードを受け取り木を変更する）。
type transformer func(n *htmldom.Node)

// Redmine / html-pipeline のカスタム transformer（定義順に適用される）。
var customTransformers = []transformer{
	// html-pipeline: ul/ol の外にある li は子で置き換える
	func(n *htmldom.Node) {
		if n.IsElement("li") && !n.HasAncestor(func(p *htmldom.Node) bool { return p.IsElement("ul", "ol") }) {
			n.ReplaceWithChildren()
		}
	},
	// html-pipeline: table の外にある表要素は子で置き換える
	func(n *htmldom.Node) {
		if n.IsElement("thead", "tbody", "tfoot", "tr", "td", "th") &&
			!n.HasAncestor(func(p *htmldom.Node) bool { return p.IsElement("table") }) {
			n.ReplaceWithChildren()
		}
	},
	// code の class は language-xxx のみ
	func(n *htmldom.Node) {
		if !n.IsElement("code") || !n.HasAttr("class") {
			return
		}
		if !reCodeLanguage.MatchString(n.AttrVal("class")) {
			n.RemoveAttr("class")
		}
	},
	// div/p の class はアラート用のみ
	func(n *htmldom.Node) {
		switch {
		case n.IsElement("div"):
			if !reAlertDiv.MatchString(n.AttrVal("class")) {
				n.RemoveAttr("class")
			}
		case n.IsElement("p"):
			if n.AttrVal("class") != "markdown-alert-title" {
				n.RemoveAttr("class")
			}
		}
	},
	// a の id は脚注参照（fnref-N）のみ
	func(n *htmldom.Node) {
		if !n.IsElement("a") || !n.HasAttr("id") {
			return
		}
		if reFnRef.MatchString(n.AttrVal("id")) {
			return
		}
		n.RemoveAttr("id")
	},
	// li の id は脚注（fn-N）、class は task-list-item のみ
	func(n *htmldom.Node) {
		if !n.IsElement("li") {
			return
		}
		if n.HasAttr("id") && !reFn.MatchString(n.AttrVal("id")) {
			n.RemoveAttr("id")
		}
		if n.HasAttr("class") && n.AttrVal("class") != "task-list-item" {
			n.RemoveAttr("class")
		}
	},
	// input はタスクリストのチェックボックスのみ
	func(n *htmldom.Node) {
		if !n.IsElement("input") {
			return
		}
		if n.AttrVal("type") == "checkbox" && n.AttrVal("class") == "task-list-item-checkbox" {
			return
		}
		n.ReplaceWithChildren()
	},
	// ul の class は contains-task-list のみ
	func(n *htmldom.Node) {
		if !n.IsElement("ul") {
			return
		}
		if n.AttrVal("class") == "contains-task-list" {
			return
		}
		n.RemoveAttr("class")
	},
	// a[href] のスキーム判定（Redmine 独自）
	func(n *htmldom.Node) {
		if !n.IsElement("a") || !n.HasAttr("href") {
			return
		}
		v := rubyStrip(n.AttrVal("href"))
		n.SetAttr("href", v)
		if !(v != "" && URIWithLinkSafeScheme(v)) {
			n.RemoveAttr("href")
		}
	},
}

// cleanElement は Sanitize::Transformers::CleanElement。
func cleanElement(n *htmldom.Node) {
	if n.Type != htmldom.ElementNode {
		return
	}
	name := strings.ToLower(n.Data)
	if !allowedElements[name] && n.Parent != nil {
		if whitespaceElements[name] {
			n.InsertBefore(htmldom.NewText(" "))
			if n.FirstChild != nil {
				n.InsertAfter(htmldom.NewText(" "))
			}
		}
		if n.FirstChild != nil && !removeContents[name] {
			for _, c := range n.Children() {
				n.InsertBefore(c)
			}
		}
		n.Unlink()
		return
	}

	allow := elementAttributes[name]
	if allow == nil {
		allow = elementAttributes[""]
	}
	protos := protocols[name]
	var kept []htmldom.Attr
	for _, a := range n.Attr {
		an := strings.ToLower(a.Name)
		if !allow[an] {
			continue
		}
		if ps, ok := protos[an]; ok {
			if m := reProtocol.FindStringSubmatch(a.Value); m != nil {
				if !contains(ps, strings.ToLower(m[1])) {
					continue
				}
			} else if !contains(ps, "") {
				continue
			}
			a.Value = rubyStrip(a.Value)
			a.NoValue = false
		}
		if an == "action" || an == "href" || an == "src" || (name == "a" && an == "name") {
			a.Value = strings.NewReplacer(" ", "%20", `"`, "%22").Replace(a.Value)
			a.NoValue = false
		}
		kept = append(kept, a)
	}
	n.Attr = kept
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// cleanCSSAttribute は Sanitize::Transformers::CSS::CleanAttribute。
func cleanCSSAttribute(n *htmldom.Node) {
	if n.Type != htmldom.ElementNode || !n.HasAttr("style") {
		return
	}
	css := cleanProperties(n.AttrVal("style"), allowedCSSProperties)
	if rubyStrip(css) == "" {
		n.RemoveAttr("style")
	} else {
		n.SetAttr("style", css)
	}
}

func transformNode(n *htmldom.Node) {
	for _, t := range customTransformers {
		t(n)
	}
	cleanElement(n)
	// CleanComment
	if n.Type == htmldom.CommentNode {
		n.Unlink()
	}
	cleanCSSAttribute(n)
	// CleanDoctype: DTD ノードは作らないため不要
	// CleanCDATA
	if n.Type == htmldom.CDATANode {
		n.Type = htmldom.TextNode
	}
}

// traverse は Sanitize#traverse（変換中にノードが移動しても続行できる走査）。
func traverse(node *htmldom.Node, f func(*htmldom.Node)) {
	f(node)
	child := node.FirstChild
	for child != nil {
		prev := child.PrevSibling
		traverse(child, f)
		if child.Parent == node {
			child = child.NextSibling
		} else if prev != nil {
			child = prev.NextSibling
		} else {
			child = node.FirstChild
		}
	}
}

// Node はサニタイズを木に直接適用する（SanitizationFilter#call 相当）。
func Node(frag *htmldom.Node) {
	traverse(frag, transformNode)
}

// ExternalLinks は ExternalLinksFilter（外部リンクに class="external"、
// mailto に class="email"、target 付き外部リンクに rel="noopener" を付与）。
func ExternalLinks(frag *htmldom.Node) {
	for _, a := range frag.FindAll(func(n *htmldom.Node) bool { return n.IsElement("a") }) {
		href, ok := a.GetAttr("href")
		if !ok {
			continue
		}
		if strings.HasPrefix(href, "/") || strings.HasPrefix(href, "#") || !strings.Contains(href, ":") {
			continue
		}
		scheme := uriScheme(href)
		if scheme == "" {
			continue
		}
		cls := "external"
		if scheme == "mailto" {
			cls = "email"
		}
		if k := a.AttrVal("class"); strings.TrimSpace(k) != "" {
			cls = k + " " + cls
		}
		a.SetAttr("class", cls)
		if t := a.AttrVal("target"); strings.TrimSpace(t) != "" && scheme != "mailto" {
			rel := strings.Fields(a.AttrVal("rel"))
			rel = append(rel, "noopener")
			a.SetAttr("rel", strings.Join(rel, " "))
		}
	}
}

// HTML は Redmine::WikiFormatting::HtmlSanitizer.call（サニタイズ＋外部リンク処理）。
func HTML(html string) string {
	frag := htmldom.ParseFragment(html)
	Node(frag)
	ExternalLinks(frag)
	return htmldom.Render(frag)
}
