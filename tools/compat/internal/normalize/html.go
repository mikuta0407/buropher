package normalize

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/andybalholm/cascadia"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// 中身を整形せずそのまま出力する要素（空白に意味がある / 生テキスト）。
var rawElements = map[atom.Atom]bool{
	atom.Pre:      true,
	atom.Textarea: true,
	atom.Script:   true,
	atom.Style:    true,
}

// 1 行に収める単一テキスト子要素の最大長。
const inlineTextMax = 120

// HTML は HTML 文書を正規化された 1 要素 1 行のテキストへ変換する。
func (n *Normalizer) HTML(body []byte) (string, error) {
	doc, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("html parse: %w", err)
	}
	if err := n.applyDOMRules(doc); err != nil {
		return "", err
	}
	var b strings.Builder
	n.renderChildren(&b, doc, 0)
	return b.String(), nil
}

// applyDOMRules は CSRF トークン置換・セレクタによる削除/マスクを DOM に適用する。
func (n *Normalizer) applyDOMRules(doc *xhtml.Node) error {
	// CSRF トークン: meta[name=csrf-token] と hidden input[name=authenticity_token]。
	// 収集したトークン文字列は文書中の他の場所（インライン JS 等）でも置換する。
	tokens := map[string]bool{}
	for _, sel := range []struct{ css, attr string }{
		{`meta[name="csrf-token"]`, "content"},
		{`input[name="authenticity_token"]`, "value"},
	} {
		for _, el := range cascadia.MustCompile(sel.css).MatchAll(doc) {
			for i := range el.Attr {
				if el.Attr[i].Key == sel.attr {
					if el.Attr[i].Val != "" {
						tokens[el.Attr[i].Val] = true
					}
					el.Attr[i].Val = PlaceholderCSRF
				}
			}
		}
	}

	// Redmine の form_tag_html はフォームに "<id|form>-<hex8>" のランダムな name を付ける。
	// 値と同じ文字列が他所で参照されても揃うよう、置換表に登録する。
	formNames := map[string]string{}
	for _, el := range cascadia.MustCompile(`form[name]`).MatchAll(doc) {
		for i := range el.Attr {
			if el.Attr[i].Key != "name" {
				continue
			}
			if m := reRandomFormName.FindStringSubmatch(el.Attr[i].Val); m != nil {
				formNames[el.Attr[i].Val] = m[1] + "-RANDOM"
				el.Attr[i].Val = m[1] + "-RANDOM"
			}
		}
	}

	for _, s := range n.cfg.StripSelectors {
		sel, err := cascadia.Compile(s)
		if err != nil {
			return fmt.Errorf("strip_selectors %q: %w", s, err)
		}
		for _, el := range sel.MatchAll(doc) {
			if el.Parent != nil {
				el.Parent.RemoveChild(el)
			}
		}
	}
	for _, s := range n.cfg.MaskTextSelectors {
		sel, err := cascadia.Compile(s)
		if err != nil {
			return fmt.Errorf("mask_text_selectors %q: %w", s, err)
		}
		for _, el := range sel.MatchAll(doc) {
			for c := el.FirstChild; c != nil; {
				next := c.NextSibling
				el.RemoveChild(c)
				c = next
			}
			el.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: PlaceholderMasked})
		}
	}
	for _, m := range n.cfg.MaskAttrs {
		sel, err := cascadia.Compile(m.Selector)
		if err != nil {
			return fmt.Errorf("mask_attrs %q: %w", m.Selector, err)
		}
		for _, el := range sel.MatchAll(doc) {
			for i := range el.Attr {
				if el.Attr[i].Key == m.Attr {
					el.Attr[i].Val = PlaceholderMasked
				}
			}
		}
	}

	// 文字列レベルの置換を全テキスト・属性値に適用
	var walk func(*xhtml.Node)
	walk = func(x *xhtml.Node) {
		switch x.Type {
		case xhtml.TextNode, xhtml.CommentNode:
			x.Data = n.String(replaceTokens(replaceMap(x.Data, formNames), tokens))
		case xhtml.ElementNode:
			for i := range x.Attr {
				x.Attr[i].Val = n.String(replaceTokens(replaceMap(x.Attr[i].Val, formNames), tokens))
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return nil
}

// ランダムなフォーム name（form_tag_html）
var reRandomFormName = regexp.MustCompile(`^(.*)-[0-9a-f]{8}$`)

func replaceMap(s string, m map[string]string) string {
	for from, to := range m {
		if strings.Contains(s, from) {
			s = strings.ReplaceAll(s, from, to)
		}
	}
	return s
}

func replaceTokens(s string, tokens map[string]bool) string {
	for t := range tokens {
		if strings.Contains(s, t) {
			s = strings.ReplaceAll(s, t, PlaceholderCSRF)
		}
	}
	return s
}

// collapseSpace は連続する空白を 1 個のスペースに圧縮する。
func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\f':
			space = true
		default:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

// textValue はテキストノードの正規化後の値を返す（空なら出力しない）。
func (n *Normalizer) textValue(s string) string {
	c := collapseSpace(s)
	if strings.TrimSpace(c) == "" {
		return ""
	}
	if !val(n.cfg.PreserveEdgeSpace) {
		c = strings.TrimSpace(c)
	}
	return c
}

func (n *Normalizer) renderChildren(b *strings.Builder, x *xhtml.Node, depth int) {
	for c := x.FirstChild; c != nil; c = c.NextSibling {
		n.renderNode(b, c, depth)
	}
}

func indent(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("  ")
	}
}

func (n *Normalizer) renderNode(b *strings.Builder, x *xhtml.Node, depth int) {
	switch x.Type {
	case xhtml.DocumentNode:
		n.renderChildren(b, x, depth)
	case xhtml.DoctypeNode:
		indent(b, depth)
		fmt.Fprintf(b, "<!DOCTYPE %s>\n", x.Data)
	case xhtml.CommentNode:
		if val(n.cfg.KeepComments) {
			indent(b, depth)
			fmt.Fprintf(b, "<!--%s-->\n", x.Data)
		}
	case xhtml.TextNode:
		if t := n.textValue(x.Data); t != "" {
			indent(b, depth)
			b.WriteString(html.EscapeString(t))
			b.WriteByte('\n')
		}
	case xhtml.ElementNode:
		n.renderElement(b, x, depth)
	}
}

func (n *Normalizer) startTag(x *xhtml.Node) string {
	attrs := make([]xhtml.Attribute, len(x.Attr))
	copy(attrs, x.Attr)
	sort.SliceStable(attrs, func(i, j int) bool {
		if attrs[i].Namespace != attrs[j].Namespace {
			return attrs[i].Namespace < attrs[j].Namespace
		}
		return attrs[i].Key < attrs[j].Key
	})
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(x.Data)
	for _, a := range attrs {
		v := a.Val
		if a.Key == "class" {
			f := strings.Fields(v)
			if val(n.cfg.SortClasses) {
				sort.Strings(f)
			}
			v = strings.Join(f, " ")
		}
		b.WriteByte(' ')
		if a.Namespace != "" {
			b.WriteString(a.Namespace)
			b.WriteByte(':')
		}
		b.WriteString(a.Key)
		b.WriteString(`="`)
		b.WriteString(html.EscapeString(v))
		b.WriteByte('"')
	}
	b.WriteByte('>')
	return b.String()
}

func isVoid(x *xhtml.Node) bool {
	switch x.DataAtom {
	case atom.Area, atom.Base, atom.Br, atom.Col, atom.Embed, atom.Hr, atom.Img, atom.Input,
		atom.Link, atom.Meta, atom.Source, atom.Track, atom.Wbr:
		return true
	}
	return x.DataAtom == 0 && x.Data == "param"
}

func (n *Normalizer) renderElement(b *strings.Builder, x *xhtml.Node, depth int) {
	indent(b, depth)
	b.WriteString(n.startTag(x))
	if isVoid(x) {
		b.WriteByte('\n')
		return
	}
	end := "</" + x.Data + ">"

	// 生テキスト要素: 中身をそのまま 1 ブロックとして出力
	if rawElements[x.DataAtom] && x.Namespace == "" {
		var inner strings.Builder
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if x.DataAtom == atom.Pre {
				_ = xhtml.Render(&inner, c)
			} else if c.Type == xhtml.TextNode {
				if x.DataAtom == atom.Textarea {
					inner.WriteString(html.EscapeString(c.Data))
				} else {
					inner.WriteString(c.Data)
				}
			}
		}
		s := strings.ReplaceAll(inner.String(), "\r\n", "\n")
		if x.DataAtom == atom.Script || x.DataAtom == atom.Style {
			// スクリプト/スタイルは行末空白と前後の空行のみ除去
			lines := strings.Split(strings.TrimSpace(s), "\n")
			for i, l := range lines {
				lines[i] = strings.TrimRight(l, " \t")
			}
			s = strings.Join(lines, "\n")
		}
		b.WriteString(s)
		b.WriteString(end)
		b.WriteByte('\n')
		return
	}

	// 出力対象の子ノードを数える
	var kids []*xhtml.Node
	for c := x.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.ElementNode:
			kids = append(kids, c)
		case xhtml.TextNode:
			if n.textValue(c.Data) != "" {
				kids = append(kids, c)
			}
		case xhtml.CommentNode:
			if val(n.cfg.KeepComments) {
				kids = append(kids, c)
			}
		}
	}
	if len(kids) == 0 {
		b.WriteString(end)
		b.WriteByte('\n')
		return
	}
	if len(kids) == 1 && kids[0].Type == xhtml.TextNode {
		t := html.EscapeString(n.textValue(kids[0].Data))
		if len(t) <= inlineTextMax && !strings.Contains(t, "\n") {
			b.WriteString(t)
			b.WriteString(end)
			b.WriteByte('\n')
			return
		}
	}
	b.WriteByte('\n')
	for _, c := range kids {
		n.renderNode(b, c, depth+1)
	}
	indent(b, depth)
	b.WriteString(end)
	b.WriteByte('\n')
}
