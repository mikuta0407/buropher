// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package htmldom

import (
	"errors"
	"strings"

	"golang.org/x/net/html/atom"

	html "github.com/mikuta0407/buropher/internal/textformat/htmldom/internal/h5"
)

// Redmine 7.0 は整形結果を Loofah.html5_fragment（Nokogiri::HTML5 = gumbo の HTML5 パーサ）で
// 解析し、Nokogiri の HTML5 直列化（html_standard_serialize）で文字列に戻す。
// 本ファイルはその解析・直列化を golang.org/x/net/html（WHATWG 準拠のパーサ）で再現する。

// Nokogiri::Gumbo の既定の上限（DEFAULT_MAX_TREE_DEPTH / DEFAULT_MAX_ATTRIBUTES）。
const (
	maxAttributes = 400
)

var (
	// ErrTreeTooDeep は木の深さが Nokogiri の上限（400）を超えたことを表す
	// （Nokogiri は ArgumentError "Document tree depth limit exceeded" を送出する）。
	ErrTreeTooDeep = html.ErrTreeTooDeep
	// ErrTooManyAttributes は 1 要素の属性数が Nokogiri の上限（400）を超えたことを表す。
	ErrTooManyAttributes = errors.New("htmldom: attributes per element limit exceeded")
)

// ParseHTML5Fragment は Loofah.html5_fragment(html)（文脈要素 body、scripting 無効、no-quirks）相当。
func ParseHTML5Fragment(src string) (*Node, error) {
	return ParseHTML5FragmentIn("body", "", src)
}

// ParseHTML5FragmentIn は ctx 要素（ns は "" / "svg" / "math"）の文脈で断片を解析する
// （Nokogiri の node.inner_html= / node.fragment(tags) 相当）。
func ParseHTML5FragmentIn(ctx, ns, src string) (*Node, error) {
	context := &html.Node{Type: html.ElementNode, Data: ctx, DataAtom: atom.Lookup([]byte(ctx)), Namespace: ns}
	nodes, err := html.ParseFragmentWithOptions(strings.NewReader(src), context, html.ParseOptionEnableScripting(false))
	if err != nil {
		if errors.Is(err, html.ErrTreeTooDeep) {
			return nil, ErrTreeTooDeep
		}
		return nil, err
	}
	frag := &Node{Type: FragmentNode}
	for _, n := range nodes {
		c, err := convertHTML5(n)
		if err != nil {
			return nil, err
		}
		if c != nil {
			frag.AppendChild(c)
		}
	}
	return frag, nil
}

// convertHTML5 は x/net/html のノードを htmldom のノードに変換する。
func convertHTML5(n *html.Node) (*Node, error) {
	var out *Node
	switch n.Type {
	case html.TextNode:
		return &Node{Type: TextNode, Data: n.Data}, nil
	case html.CommentNode:
		return &Node{Type: CommentNode, Data: n.Data}, nil
	case html.ElementNode:
		if len(n.Attr) > maxAttributes {
			return nil, ErrTooManyAttributes
		}
		out = &Node{Type: ElementNode, Data: n.Data, Namespace: n.Namespace}
		if len(n.Attr) > 0 {
			out.Attr = make([]Attr, len(n.Attr))
			for i, a := range n.Attr {
				name := a.Key
				if a.Namespace != "" {
					name = a.Namespace + ":" + a.Key
				}
				out.Attr[i] = Attr{Name: name, Value: a.Val}
			}
		}
	default:
		// DOCTYPE 等は断片に現れない
		return nil, nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		cc, err := convertHTML5(c)
		if err != nil {
			return nil, err
		}
		if cc != nil {
			out.AppendChild(cc)
		}
	}
	return out, nil
}

// html5VoidElements は Nokogiri の html_standard_serialize が終了タグを出力しない要素。
var html5VoidElements = map[string]bool{
	"area": true, "base": true, "basefont": true, "bgsound": true, "br": true, "col": true, "embed": true,
	"frame": true, "hr": true, "img": true, "input": true, "keygen": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// html5RawTextElements は子のテキストをエスケープせずに出力する要素。
var html5RawTextElements = map[string]bool{
	"style": true, "script": true, "xmp": true, "iframe": true, "noembed": true, "noframes": true,
	"plaintext": true, "noscript": true,
}

// RenderHTML5 は Loofah の HTML5 断片の to_s（Nokogiri の html_standard_serialize）相当。
func RenderHTML5(n *Node) string {
	var sb strings.Builder
	if n.Type == FragmentNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeHTML5(&sb, c)
		}
	} else {
		writeHTML5(&sb, n)
	}
	return sb.String()
}

// InnerHTML5 は子ノードを HTML5 の規則で直列化する。
func InnerHTML5(n *Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeHTML5(&sb, c)
	}
	return sb.String()
}

// isHTMLElement は HTML 名前空間の要素（Nokogiri で ns が NULL）かどうか。
func (n *Node) isHTMLElement(names map[string]bool) bool {
	return n != nil && n.Type == ElementNode && n.Namespace == "" && names[n.Data]
}

func writeHTML5(sb *strings.Builder, n *Node) {
	switch n.Type {
	case ElementNode:
		sb.WriteByte('<')
		sb.WriteString(n.Data)
		for _, a := range n.Attr {
			sb.WriteByte(' ')
			sb.WriteString(a.Name)
			sb.WriteString(`="`)
			escapeHTML5(sb, a.Value, true)
			sb.WriteByte('"')
		}
		sb.WriteByte('>')
		if n.isHTMLElement(html5VoidElements) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeHTML5(sb, c)
		}
		sb.WriteString("</")
		sb.WriteString(n.Data)
		sb.WriteByte('>')
	case TextNode:
		// libxml2 のテキストは C 文字列のため NUL 以降は出力されない
		s := n.Data
		if i := strings.IndexByte(s, 0); i >= 0 {
			s = s[:i]
		}
		if n.Parent.isHTMLElement(html5RawTextElements) {
			sb.WriteString(s)
		} else {
			escapeHTML5(sb, s, false)
		}
	case CDATANode:
		sb.WriteString("<![CDATA[")
		sb.WriteString(n.Data)
		sb.WriteString("]]>")
	case CommentNode:
		sb.WriteString("<!--")
		sb.WriteString(n.Data)
		sb.WriteString("-->")
	case PINode:
		sb.WriteString("<?")
		sb.WriteString(n.Data)
		sb.WriteByte('>')
	case FragmentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeHTML5(sb, c)
		}
	}
}

// escapeHTML5 は Nokogiri の output_escaped_string。
func escapeHTML5(sb *strings.Builder, s string, attr bool) {
	start := 0
	for i := 0; i < len(s); i++ {
		var rep string
		n := 1
		switch c := s[i]; {
		case c == '&':
			rep = "&amp;"
		case c == 0xC2 && i+1 < len(s) && s[i+1] == 0xA0:
			rep = "&nbsp;"
			n = 2
		case attr && c == '"':
			rep = "&quot;"
		case !attr && c == '<':
			rep = "&lt;"
		case !attr && c == '>':
			rep = "&gt;"
		default:
			continue
		}
		sb.WriteString(s[start:i])
		sb.WriteString(rep)
		i += n - 1
		start = i + 1
	}
	sb.WriteString(s[start:])
}

// EscapeHTML5Text は HTML5 直列化のテキストのエスケープ（& < > と U+00A0）。
func EscapeHTML5Text(s string) string {
	var sb strings.Builder
	escapeHTML5(&sb, s, false)
	return sb.String()
}

// SetInnerHTML5 は Nokogiri の node.inner_html = html（n の文脈で解析して子を置き換える）。
func (n *Node) SetInnerHTML5(src string) error {
	frag, err := ParseHTML5FragmentIn(n.Data, n.Namespace, src)
	if err != nil {
		return err
	}
	n.RemoveChildren()
	for _, c := range frag.Children() {
		n.AppendChild(c)
	}
	return nil
}

// ScrubTopDown は Loofah の Scrubber#traverse（direction: :top_down）。
// f が true（Loofah::Scrubber::STOP）を返すとその子孫は辿らない。
// 子の一覧は f の適用後に取得し、走査中の変更（置換・移動）の影響を受けない。
func (n *Node) ScrubTopDown(f func(*Node) bool) {
	if n.Type == FragmentNode {
		for _, c := range n.Children() {
			c.ScrubTopDown(f)
		}
		return
	}
	if f(n) {
		return
	}
	for _, c := range n.Children() {
		c.ScrubTopDown(f)
	}
}
