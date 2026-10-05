// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package fixtures

import "strings"

// EscapeAttrAngles は Nokogiri が直列化した HTML の属性値に含まれる "<" と ">" を
// &lt; / &gt; に置き換える（buropher は属性値の山括弧もエスケープするため、
// Redmine の出力をこの形に揃えてから比較する）。
func EscapeAttrAngles(s string) string {
	if !strings.Contains(s, `="`) {
		return s
	}
	var b strings.Builder
	inTag, inQuote := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case !inTag:
			if c == '<' && strings.HasPrefix(s[i:], "<!--") {
				end := strings.Index(s[i+4:], "-->")
				if end < 0 {
					b.WriteString(s[i:])
					return b.String()
				}
				b.WriteString(s[i : i+4+end+3])
				i += 4 + end + 2
				continue
			}
			if c == '<' && i+1 < len(s) && isASCIILetter(s[i+1]) {
				inTag = true
			}
			b.WriteByte(c)
		case inQuote:
			switch c {
			case '"':
				inQuote = false
				b.WriteByte(c)
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			default:
				b.WriteByte(c)
			}
		default:
			switch c {
			case '"':
				inQuote = true
			case '>':
				inTag = false
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
