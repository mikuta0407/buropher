package htmldom

import (
	"strings"
	"unicode"
)

// Render は Nokogiri の DocumentFragment#to_s（各子ノードの to_html の連結）相当。
// libxml2 の htmlNodeDumpFormatOutput（format=1）を移植している。
func Render(n *Node) string {
	var sb strings.Builder
	if n.Type == FragmentNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			dumpNode(&sb, c)
		}
	} else {
		dumpNode(&sb, n)
	}
	return sb.String()
}

// InnerHTML は子ノードを直列化する（Nokogiri の inner_html）。
func InnerHTML(n *Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		dumpNode(&sb, c)
	}
	return sb.String()
}

func isTextLike(n *Node) bool { return n.Type == TextNode }

// newlineAllowedIn は libxml2 の「parent->name != NULL && parent->name[0] != 'p'」判定。
// 断片（名前なし）の場合は改行を入れない。
func newlineAllowedIn(parent *Node) bool {
	if parent == nil || parent.Type != ElementNode {
		return false
	}
	return parent.Data != "" && parent.Data[0] != 'p'
}

func dumpNode(sb *strings.Builder, root *Node) {
	var rec func(cur *Node)
	rec = func(cur *Node) {
		switch cur.Type {
		case ElementNode:
			info, known := tagLookup(cur.Data)
			sb.WriteByte('<')
			sb.WriteString(cur.Data)
			for _, a := range cur.Attr {
				dumpAttr(sb, cur, a)
			}
			if known && info.empty {
				sb.WriteByte('>')
			} else if cur.FirstChild == nil {
				if known && info.saveEndTag != 0 && cur.Data != "html" && cur.Data != "body" {
					sb.WriteByte('>')
				} else {
					sb.WriteString("></")
					sb.WriteString(cur.Data)
					sb.WriteByte('>')
				}
			} else {
				sb.WriteByte('>')
				if known && !info.isInline && !isTextLike(cur.FirstChild) &&
					cur.FirstChild != cur.LastChild && cur.Data[0] != 'p' {
					sb.WriteByte('\n')
				}
				for c := cur.FirstChild; c != nil; c = c.NextSibling {
					rec(c)
				}
				if known && !info.isInline && !isTextLike(cur.LastChild) &&
					cur.FirstChild != cur.LastChild && cur.Data[0] != 'p' {
					sb.WriteByte('\n')
				}
				sb.WriteString("</")
				sb.WriteString(cur.Data)
				sb.WriteByte('>')
			}
			if cur.NextSibling != nil && known && !info.isInline {
				if !isTextLike(cur.NextSibling) && newlineAllowedIn(cur.Parent) {
					sb.WriteByte('\n')
				}
			}
		case TextNode:
			if cur.Parent != nil && cur.Parent.Type == ElementNode &&
				(strings.EqualFold(cur.Parent.Data, "script") || strings.EqualFold(cur.Parent.Data, "style")) {
				sb.WriteString(cur.Data)
			} else {
				sb.WriteString(EscapeText(cur.Data))
			}
		case CDATANode:
			sb.WriteString(cur.Data)
		case CommentNode:
			sb.WriteString("<!--")
			sb.WriteString(cur.Data)
			sb.WriteString("-->")
		case PINode:
			sb.WriteString("<?")
			sb.WriteString(cur.Data)
			if cur.HasPIContent {
				sb.WriteByte(' ')
				sb.WriteString(cur.PIContent)
			}
			sb.WriteByte('>')
		case FragmentNode:
			for c := cur.FirstChild; c != nil; c = c.NextSibling {
				rec(c)
			}
		}
	}
	rec(root)
}

// EscapeText は xmlEncodeEntitiesReentrant（HTML 文書、attr=0）相当。
func EscapeText(s string) string {
	return encodeEntities(s, false)
}

func encodeEntities(s string, attr bool) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '<':
			if attr && strings.HasPrefix(s[i:], "<!--") {
				if end := strings.Index(s[i:], "-->"); end >= 0 {
					sb.WriteString(s[i : i+end+3])
					i += end + 2
					continue
				}
			}
			sb.WriteString("&lt;")
		case c == '>':
			sb.WriteString("&gt;")
		case c == '&':
			if attr && i+1 < len(s) && s[i+1] == '{' && strings.IndexByte(s[i:], '}') >= 0 {
				end := strings.IndexByte(s[i:], '}')
				sb.WriteString(s[i : i+end+1])
				i += end
				continue
			}
			sb.WriteString("&amp;")
		case (c >= 0x20 && c < 0x80) || c == '\n' || c == '\t' || c == '\r':
			sb.WriteByte(c)
		case c >= 0x80:
			sb.WriteByte(c)
		default:
			// 制御文字（IS_BYTE_CHAR でないもの）は出力しない
		}
	}
	return sb.String()
}

func dumpAttr(sb *strings.Builder, el *Node, a Attr) {
	sb.WriteByte(' ')
	sb.WriteString(a.Name)
	if a.NoValue || isBooleanAttr(a.Name) {
		return
	}
	value := encodeEntities(a.Value, true)
	sb.WriteByte('=')
	lname := strings.ToLower(a.Name)
	if lname == "href" || lname == "action" || lname == "src" || (lname == "name" && strings.EqualFold(el.Data, "a")) {
		value = strings.TrimLeft(value, " \t\n\r")
		value = uriEscape(value)
	}
	writeQuoted(sb, value)
}

// uriEscape は xmlURIEscapeStr(value, "\"#$%&+,/:;<=>?@[\\]^`{|}")。
func uriEscape(s string) string {
	const keep = "\"#$%&+,/:;<=>?@[\\]^`{|}"
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '@' || isUnreserved(c) || strings.IndexByte(keep, c) >= 0 {
			sb.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		sb.WriteByte('%')
		sb.WriteByte(hex[c>>4])
		sb.WriteByte(hex[c&0xF])
	}
	return sb.String()
}

func isUnreserved(c byte) bool {
	return isASCIILetter(c) || isASCIIDigit(c) || c == '-' || c == '_' || c == '.' || c == '!' ||
		c == '~' || c == '*' || c == '\'' || c == '(' || c == ')'
}

// writeQuoted は xmlOutputBufferWriteQuotedString。
func writeQuoted(sb *strings.Builder, s string) {
	if strings.IndexByte(s, '"') >= 0 {
		if strings.IndexByte(s, '\'') >= 0 {
			sb.WriteByte('"')
			sb.WriteString(strings.ReplaceAll(s, `"`, "&quot;"))
			sb.WriteByte('"')
		} else {
			sb.WriteByte('\'')
			sb.WriteString(s)
			sb.WriteByte('\'')
		}
		return
	}
	sb.WriteByte('"')
	sb.WriteString(s)
	sb.WriteByte('"')
}

// ---- XML 1.0 の文字クラス（htmlParseNameComplex 用の近似） ----

func isLetter(c rune) bool {
	if c < 0x80 {
		return isASCIILetter(byte(c))
	}
	return unicode.IsLetter(c) || unicode.Is(unicode.Nl, c)
}

func isDigit(c rune) bool {
	if c < 0x80 {
		return isASCIIDigit(byte(c))
	}
	return unicode.IsDigit(c)
}

func isCombining(c rune) bool {
	return unicode.In(c, unicode.Mn, unicode.Mc, unicode.Me)
}

func isExtender(c rune) bool {
	switch c {
	case 0x00B7, 0x02D0, 0x02D1, 0x0387, 0x0640, 0x0E46, 0x0EC6, 0x3005:
		return true
	}
	return (c >= 0x3031 && c <= 0x3035) || (c >= 0x309D && c <= 0x309E) || (c >= 0x30FC && c <= 0x30FE)
}
