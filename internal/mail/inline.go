package mail

// HTML メールの CSS インライン化（Redmine の Mailer が include する Roadie::Rails::Automatic の移植）。
//
// roadie は Nokogiri（libxml2 の HTML パーサ）で文書を解析し、<style> の規則を各要素の style 属性へ
// 展開して libxml2 の HTML シリアライザで出力する。ここでは golang.org/x/net/html で <body> の中身を
// 解析し、cascadia でセレクタを照合して、libxml2 と同じ規則（空要素・真偽属性・URI 属性のエスケープ・
// 引用符の選択）で出力する。期待値は testdata/roadie_golden.json（gen_roadie.rb で roadie から生成）。

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// URLOptions は roadie の url_options（相対 URL を絶対 URL にする基準）。
type URLOptions struct {
	Protocol string // "http" / "https"
	Host     string
	Port     int // 0 なら省略
}

func (o URLOptions) root() string {
	scheme := o.Protocol
	if scheme == "" {
		scheme = "http"
	}
	if i := strings.IndexFunc(scheme, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	}); i >= 0 {
		scheme = scheme[:i]
	}
	s := scheme + "://" + o.Host
	if o.Port != 0 {
		s += ":" + strconv.Itoa(o.Port)
	}
	return s
}

// generateURL は Roadie::UrlGenerator#generate_url。
func (o URLOptions) generateURL(path string) string {
	root := o.root()
	switch {
	case path == "":
		return root
	case strings.HasPrefix(path, "#"):
		return path
	case regexp.MustCompile(`^//\w`).MatchString(path):
		scheme := o.Protocol
		if scheme == "" {
			scheme = "http"
		}
		return scheme + ":" + path
	}
	if u, err := url.Parse(path); err == nil && u.Scheme != "" {
		return path
	} else if err != nil && regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`).MatchString(path) {
		return path
	}
	if strings.HasPrefix(path, "/") {
		return root + path
	}
	return root + "/" + path
}

// cssDecl は宣言 1 件（StyleProperty）。
type cssDecl struct {
	prop, value string
	important   bool
	specificity int
}

func (d cssDecl) String() string {
	v := d.value
	if d.important {
		v += " !important"
	}
	return d.prop + ":" + v
}

// cssBlock はセレクタ 1 個分の規則（StyleBlock）。
type cssBlock struct {
	selector string
	decls    []cssDecl
	media    string
}

func (b cssBlock) String() string {
	ds := make([]string, len(b.decls))
	for i, d := range b.decls {
		ds[i] = d.String()
	}
	return b.selector + "{" + strings.Join(ds, ";") + "}"
}

var (
	cssCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	nonIDRe      = regexp.MustCompile(`(?i)(?:\.\w+)|\[(?:\w+)|(?::(?:link|visited|active|hover|focus|lang|target|enabled|disabled|checked|indeterminate|root|nth-child|nth-last-child|nth-of-type|nth-last-of-type|first-child|last-child|first-of-type|last-of-type|only-child|only-of-type|empty|contains))`)
	elementsRe   = regexp.MustCompile(`(?i)(?:(?:^|[\s+>~]+)\w+|:{1,2}(?:after|before|first-letter|first-line|selection))`)
)

// specificity は CssParser.calculate_specificity（"#{b}#{c}#{d}".to_i）。
func specificity(sel string) int {
	b := strings.Count(sel, "#")
	c := len(nonIDRe.FindAllString(sel, -1))
	d := len(elementsRe.FindAllString(sel, -1))
	n, _ := strconv.Atoi(fmt.Sprintf("0%d%d%d", b, c, d))
	return n
}

// parseCSS は単純な CSS（@media なし）を StyleBlock の列にする（CssParser + Roadie::Stylesheet）。
func parseCSS(css string) []cssBlock {
	css = cssCommentRe.ReplaceAllString(css, "")
	var out []cssBlock
	for {
		open := strings.Index(css, "{")
		if open < 0 {
			break
		}
		closeIdx := strings.Index(css[open:], "}")
		if closeIdx < 0 {
			break
		}
		sels := css[:open]
		body := css[open+1 : open+closeIdx]
		css = css[open+closeIdx+1:]
		var decls []string
		for _, d := range strings.Split(body, ";") {
			if strings.TrimSpace(d) != "" {
				decls = append(decls, d)
			}
		}
		for _, sel := range strings.Split(sels, ",") {
			sel = strings.Join(strings.Fields(sel), " ")
			if sel == "" {
				continue
			}
			sp := specificity(sel)
			b := cssBlock{selector: sel, media: "all"}
			for _, d := range decls {
				i := strings.Index(d, ":")
				if i < 0 {
					continue
				}
				prop := strings.ToLower(strings.TrimSpace(d[:i]))
				val := strings.TrimSpace(d[i+1:])
				imp := false
				if j := strings.Index(strings.ToLower(val), "!important"); j >= 0 {
					imp = true
					val = strings.TrimSpace(val[:j])
				}
				b.decls = append(b.decls, cssDecl{prop: prop, value: val, important: imp, specificity: sp})
			}
			out = append(out, b)
		}
	}
	return out
}

var badPseudo = []string{":active", ":focus", ":hover", ":link", ":target", ":visited",
	":-ms-input-placeholder", ":-moz-placeholder", ":before", ":after", ":enabled", ":disabled", ":checked", ":host", ":root"}

// inlinable は Roadie::Selector#inlinable?。
func inlinable(sel string) bool {
	if strings.Contains(sel, "::") || strings.HasPrefix(sel, "@") {
		return false
	}
	for _, p := range badPseudo {
		if strings.Contains(sel, p) {
			return false
		}
	}
	return true
}

var (
	styleElemRe = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	preNLRe     = regexp.MustCompile(`(?i)(<(?:pre|textarea|listing)\b[^>]*>)\n`)
	ctMetaRe    = regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']?content-type`)
)

// InlineCSS は Redmine のメールレイアウト（layouts/mailer.html）で描画した HTML に roadie と同じ変換
// （<style> の規則のインライン化、Content-Type の meta 追加、相対 URL の絶対化、libxml2 形式での出力）を行う。
func InlineCSS(src string, opts URLOptions) (string, error) {
	lower := strings.ToLower(src)
	hi := strings.Index(lower, "<head>")
	he := strings.Index(lower, "</head>")
	bi := strings.Index(lower, "<body")
	be := strings.LastIndex(lower, "</body>")
	if hi < 0 || he < hi || bi < he || be < bi {
		return "", fmt.Errorf("mail: unexpected layout")
	}
	bodyOpenEnd := strings.Index(src[bi:], ">")
	if bodyOpenEnd < 0 {
		return "", fmt.Errorf("mail: unexpected layout")
	}
	headInner := src[hi+len("<head>") : he]
	var blocks []cssBlock
	for _, m := range styleElemRe.FindAllStringSubmatch(headInner, -1) {
		blocks = append(blocks, parseCSS(strings.TrimSpace(m[1]))...)
	}
	headRest := styleElemRe.ReplaceAllString(headInner, "")
	bodyInner := src[bi+bodyOpenEnd+1 : be]
	afterBody := src[be+len("</body>"):]
	if i := strings.Index(strings.ToLower(afterBody), "</html>"); i >= 0 {
		afterBody = afterBody[:i]
	}

	// <body> の中身を body 要素の文脈で解析する（libxml2 は <pre> 直後の改行を残すので、
	// html5 パーサが 1 つ消す分をあらかじめ足しておく）
	bodyInner = preNLRe.ReplaceAllString(bodyInner, "$1\n\n")
	annotated, attrOrders := annotateStartTags(bodyInner)
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(annotated), body)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		body.AppendChild(n)
	}
	restoreSourceStructure(body, attrOrders)

	type elemStyle struct {
		decls []struct {
			d   cssDecl
			seq int
		}
	}
	styles := map[*html.Node]*elemStyle{}
	var order []*html.Node
	var extra []cssBlock
	seq := 0
	for _, b := range blocks {
		if !inlinable(b.selector) {
			extra = append(extra, b)
			continue
		}
		sel, err := cascadia.Compile(b.selector)
		if err != nil {
			continue
		}
		for _, n := range sel.MatchAll(body) {
			es := styles[n]
			if es == nil {
				es = &elemStyle{}
				styles[n] = es
				order = append(order, n)
			}
			for _, d := range b.decls {
				es.decls = append(es.decls, struct {
					d   cssDecl
					seq int
				}{d, seq})
				seq++
			}
		}
	}
	for _, n := range order {
		es := styles[n]
		ds := es.decls
		sort.SliceStable(ds, func(i, j int) bool {
			a, b := ds[i].d, ds[j].d
			if a.important != b.important {
				return !a.important
			}
			if a.specificity != b.specificity {
				return a.specificity < b.specificity
			}
			return ds[i].seq < ds[j].seq
		})
		strs := make([]string, len(ds))
		for i, d := range ds {
			strs[i] = d.d.String()
		}
		strs = dedupeKeepLast(strs)
		v := strings.Join(strs, ";")
		if old, ok := getAttr(n, "style"); ok {
			v += ";" + old
		}
		setAttr(n, "style", v)
	}
	// Roadie::UrlRewriter（a[href], img[src], *[style] の要素）
	var rewrite func(n *html.Node)
	rewrite = func(n *html.Node) {
		if n.Type == html.ElementNode {
			_, hasHref := getAttr(n, "href")
			_, hasSrc := getAttr(n, "src")
			st, hasStyle := getAttr(n, "style")
			isA, isImg := n.Data == "a", n.Data == "img"
			if (isA && hasHref) || (isImg && hasSrc) || hasStyle {
				if hasStyle {
					setAttr(n, "style", rewriteCSSURLs(st, opts))
				}
				if isA {
					h, _ := getAttr(n, "href")
					setAttr(n, "href", opts.generateURL(h))
				} else if isImg {
					s, _ := getAttr(n, "src")
					setAttr(n, "src", opts.generateURL(s))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rewrite(c)
		}
	}
	rewrite(body)

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html>")
	sb.WriteString(src[strings.Index(lower, "<html>")+len("<html>") : hi])
	sb.WriteString("<head>")
	sb.WriteString(headRest)
	if !ctMetaRe.MatchString(headRest) {
		sb.WriteString(`<meta http-equiv="Content-Type" content="text/html; charset=UTF-8">`)
	}
	if len(extra) > 0 {
		ss := make([]string, len(extra))
		for i, b := range extra {
			ss[i] = b.String()
		}
		sb.WriteString("<style>" + strings.Join(ss, "\n") + "</style>")
	}
	sb.WriteString("</head>")
	sb.WriteString(src[he+len("</head>") : bi])
	serializeNode(&sb, body)
	sb.WriteString(afterBody)
	sb.WriteString("</html>\n")
	return sb.String(), nil
}

// orderAttr は元の属性順を復元するために開始タグへ一時的に付ける属性名。
const orderAttr = "data-buropher-attr-order"

// annotateStartTags は各開始タグに通し番号の属性を付け、元の属性名の順序を記録する。
// x/net/html は書式要素（a, b, strong ...）の属性を並べ替え、また html5 の規則で要素を補う
// （tbody、対応のない </p> の空 p）が、libxml2 はどちらもしないため、解析後に restoreSourceStructure で戻す。
func annotateStartTags(src string) (string, [][]string) {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	var orders [][]string
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := string(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			b.WriteString(raw)
			continue
		}
		tok := z.Token()
		keys := make([]string, 0, len(tok.Attr))
		for _, a := range tok.Attr {
			keys = append(keys, a.Key)
		}
		i := strings.LastIndex(raw, ">")
		if i > 0 && raw[i-1] == '/' {
			i--
		}
		if i < 0 {
			b.WriteString(raw)
			continue
		}
		fmt.Fprintf(&b, "%s %s=\"%d\"%s", raw[:i], orderAttr, len(orders), raw[i:])
		orders = append(orders, keys)
	}
	return b.String(), orders
}

// restoreSourceStructure は annotateStartTags の属性を外して属性順を戻し、元のタグに無かった
// tbody を展開し、元のタグに無かった空の p を取り除く。
func restoreSourceStructure(n *html.Node, orders [][]string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		restoreSourceStructure(c, orders)
		c = next
	}
	if n.Type != html.ElementNode || n.Parent == nil {
		return
	}
	v, ok := getAttr(n, orderAttr)
	if !ok {
		switch n.Data {
		case "tbody":
			for c := n.FirstChild; c != nil; {
				next := c.NextSibling
				n.RemoveChild(c)
				n.Parent.InsertBefore(c, n)
				c = next
			}
			n.Parent.RemoveChild(n)
		case "p":
			if n.FirstChild == nil {
				n.Parent.RemoveChild(n)
			}
		}
		return
	}
	idx, _ := strconv.Atoi(v)
	var attrs []html.Attribute
	byKey := map[string]html.Attribute{}
	for _, a := range n.Attr {
		if a.Key != orderAttr {
			byKey[strings.ToLower(a.Key)] = a
		}
	}
	if idx < len(orders) {
		for _, k := range orders[idx] {
			if a, ok := byKey[strings.ToLower(k)]; ok {
				attrs = append(attrs, a)
				delete(byKey, strings.ToLower(k))
			}
		}
	}
	for _, a := range n.Attr {
		if _, ok := byKey[strings.ToLower(a.Key)]; ok && a.Key != orderAttr {
			attrs = append(attrs, a)
		}
	}
	n.Attr = attrs
}

func dedupeKeepLast(in []string) []string {
	last := map[string]int{}
	for i, s := range in {
		last[s] = i
	}
	if len(last) == len(in) {
		return in
	}
	var out []string
	for i, s := range in {
		if last[s] == i {
			out = append(out, s)
		}
	}
	return out
}

var cssURLRe = regexp.MustCompile(`url\(((?:["']|%22)?)([^(]*(?:\([^)]*\))*[^(]+?)((?:["']|%22)?)\)`)

func rewriteCSSURLs(css string, opts URLOptions) string {
	return cssURLRe.ReplaceAllStringFunc(css, func(m string) string {
		sm := cssURLRe.FindStringSubmatch(m)
		if sm[1] != sm[3] {
			return m
		}
		return "url(" + sm[1] + opts.generateURL(sm[2]) + sm[3] + ")"
	})
}

func getAttr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// libxml2 の HTML 4 の空要素。
var voidElems = map[string]bool{"area": true, "base": true, "basefont": true, "br": true, "col": true, "embed": true,
	"frame": true, "hr": true, "img": true, "input": true, "isindex": true, "link": true, "meta": true, "param": true}

// libxml2 の htmlIsBooleanAttr。
var boolAttrs = map[string]bool{"checked": true, "compact": true, "declare": true, "defer": true, "disabled": true,
	"ismap": true, "multiple": true, "nohref": true, "noresize": true, "noshade": true, "nowrap": true, "readonly": true, "selected": true}

func isURIAttr(elem, key string) bool {
	switch key {
	case "href", "action", "src":
		return true
	case "name":
		return elem == "a"
	}
	return false
}

// serializeNode は libxml2 の htmlNodeDumpOutput（書式化なし）と同じ形で出力する。
func serializeNode(sb *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		if p := n.Parent; p != nil && p.Type == html.ElementNode && (p.Data == "script" || p.Data == "style") {
			sb.WriteString(n.Data)
			return
		}
		escapeText(sb, n.Data)
		return
	case html.CommentNode:
		sb.WriteString("<!--" + n.Data + "-->")
		return
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			serializeNode(sb, c)
		}
		return
	case html.ElementNode:
	default:
		return
	}
	name := strings.ToLower(n.Data)
	sb.WriteString("<" + name)
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if a.Namespace != "" {
			key = a.Namespace + ":" + key
		}
		sb.WriteString(" " + key)
		if boolAttrs[key] {
			continue
		}
		v := a.Val
		if isURIAttr(name, key) {
			v = escapeURIAttr(strings.TrimLeft(v, " \t\n\r"))
		}
		writeQuotedAttr(sb, v)
	}
	sb.WriteString(">")
	if voidElems[name] {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		serializeNode(sb, c)
	}
	sb.WriteString("</" + name + ">")
}

func escapeText(sb *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		default:
			sb.WriteRune(r)
		}
	}
}

// escapeURIAttr は URI 属性値の非 ASCII・空白・制御文字を %XX にする。
func escapeURIAttr(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 || c <= 0x20 || c == 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// writeQuotedAttr は属性値を引用符で囲んで出力する（" だけを含むなら ' で囲み、そうでなければ " で囲んで &quot;）。
func writeQuotedAttr(sb *strings.Builder, v string) {
	quote := byte('"')
	if strings.Contains(v, `"`) && !strings.Contains(v, "'") {
		quote = '\''
	}
	sb.WriteString("=")
	sb.WriteByte(quote)
	for _, r := range v {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			if quote == '"' {
				sb.WriteString("&quot;")
			} else {
				sb.WriteRune(r)
			}
		case '\n':
			sb.WriteString("&#10;")
		case '\r':
			sb.WriteString("&#13;")
		case '\t':
			sb.WriteString("&#9;")
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte(quote)
}
