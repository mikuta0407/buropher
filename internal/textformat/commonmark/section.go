package commonmark

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

// ErrStaleSection は更新対象のセクションが編集開始後に変更されていたことを表す
// （Redmine::WikiFormatting::StaleSectionError）。
var ErrStaleSection = errors.New("commonmark: stale section")

// SectionHash は ActiveSupport::Digest.hexdigest（Redmine の既定は MD5）。
func SectionHash(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// GetSection は Redmine::WikiFormatting::SectionHelper#get_section。
// index 番目（1 始まり）の見出しから始まるセクションの本文とそのハッシュを返す。
func GetSection(text string, index int) (string, string) {
	s := extractSections(text, index)[1]
	return s, SectionHash(s)
}

// UpdateSection は SectionHelper#update_section。hash が空でなく現在のセクションの
// ハッシュと異なる場合は ErrStaleSection を返す。
func UpdateSection(text string, index int, update, hash string) (string, error) {
	t := extractSections(text, index)
	if strings.TrimSpace(hash) != "" && hash != SectionHash(t[1]) {
		return "", ErrStaleSection
	}
	if !isBlank(t[1]) {
		t[1] = update
	}
	var parts []string
	for _, s := range t {
		if !isBlank(s) {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// isBlank は Ruby の String#blank?（空白文字のみ）。
func isBlank(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' ||
			r == 0x85 || r == 0xA0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200A) ||
			r == 0x2028 || r == 0x2029 || r == 0x202F || r == 0x205F || r == 0x3000
	}) == ""
}

var (
	reFencePart  = regexp.MustCompile("\\A(~{3,}|`{3,})(\\s*\\S+)?[ \\t\\n\\v\\f\\r]*(?:\\n|\\z)")
	reATXPart    = regexp.MustCompile(`\A(#+) .+`)
	reSetextPart = regexp.MustCompile(`\A.+\r?\n\r?(=+|-+)[ \t\n\v\f\r]*(?:\n|\z)`)
)

// rubySpace は Ruby の \s（[ \t\r\n\f\v]）。
func rubySpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'
}

// splitSections は Ruby の
// text.split(/(^(?:(?!\s*$|#).+\r?\n\r?(?:=+|-+)|#+ .+|(?:~~~|```).*)\s*$)/)
// と同じ分割（区切り文字列も結果に含む）を行う。
func splitSections(text string) []string {
	var parts []string
	last := 0
	pos := 0
	for pos <= len(text) {
		// pos は行頭
		if end := matchSectionDelimiter(text, pos); end > pos {
			parts = append(parts, text[last:pos], text[pos:end])
			last = end
			// 次の行頭へ
			if end < len(text) && text[end] == '\n' {
				pos = end + 1
			} else if end > 0 && text[end-1] == '\n' {
				pos = end
			} else {
				nl := strings.IndexByte(text[end:], '\n')
				if nl < 0 {
					break
				}
				pos = end + nl + 1
			}
			continue
		}
		nl := strings.IndexByte(text[pos:], '\n')
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	parts = append(parts, text[last:])
	// Ruby の split は末尾の空要素を取り除く
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// lineEnd は pos を含む行の終端（改行の位置またはテキスト末尾）。
func lineEnd(text string, pos int) int {
	if i := strings.IndexByte(text[pos:], '\n'); i >= 0 {
		return pos + i
	}
	return len(text)
}

// trailingSpaceEnd は "\s*$" の一致終端（貪欲で、行末で終わる最長位置）。失敗時は -1。
func trailingSpaceEnd(text string, q int) int {
	k := q
	for k < len(text) && rubySpace(text[k]) {
		k++
	}
	for ; k >= q; k-- {
		if k == len(text) || text[k] == '\n' {
			return k
		}
	}
	return -1
}

// matchSectionDelimiter は行頭 pos で区切りに一致した場合その終端を返す（不一致は -1）。
func matchSectionDelimiter(text string, pos int) int {
	if pos >= len(text) {
		return -1
	}
	le := lineEnd(text, pos)
	line := text[pos:le]
	// (a) Setext 見出し: (?!\s*$|#).+\r?\n\r?(?:=+|-+)
	if line != "" && line[0] != '#' && strings.TrimLeft(line, " \t\r\f\v") != "" && le < len(text) {
		q := le + 1
		if q < len(text) && text[q] == '\r' {
			q++
		}
		if q < len(text) && (text[q] == '=' || text[q] == '-') {
			ch := text[q]
			r := q
			for r < len(text) && text[r] == ch {
				r++
			}
			// 貪欲な =+ / -+ のあと \s*$（後戻りは同じ結果になる）
			for k := r; k > q; k-- {
				if e := trailingSpaceEnd(text, k); e >= 0 {
					return e
				}
			}
		}
	}
	// (b) ATX 見出し: #+ .+
	if strings.HasPrefix(line, "#") {
		i := 0
		for i < len(line) && line[i] == '#' {
			i++
		}
		if i < len(line) && line[i] == ' ' && i+1 < len(line) {
			if e := trailingSpaceEnd(text, le); e >= 0 {
				return e
			}
		}
	}
	// (c) フェンス: (?:~~~|```).*
	if strings.HasPrefix(line, "~~~") || strings.HasPrefix(line, "```") {
		if e := trailingSpaceEnd(text, le); e >= 0 {
			return e
		}
	}
	return -1
}

// extractSections は SectionHelper#extract_sections。
func extractSections(text string, index int) [3]string {
	var sections [3]strings.Builder
	offset := 0
	i := 0
	l := 1
	insidePre := false
	for _, part := range splitSections(text) {
		level := 0
		if m := reFencePart.FindStringSubmatchIndex(part); m != nil {
			if !insidePre {
				insidePre = true
			} else if m[4] < 0 {
				insidePre = false
			}
		} else if insidePre {
			// 何もしない
		} else if m := reATXPart.FindStringSubmatch(part); m != nil {
			level = len(m[1])
		} else if m := reSetextPart.FindStringSubmatch(part); m != nil {
			if strings.Contains(m[1], "=") {
				level = 1
			} else {
				level = 2
			}
		}
		if level > 0 {
			i++
			if offset == 0 && i == index {
				offset = 1
				l = level
			} else if offset == 1 && i > index && level <= l {
				offset = 2
			}
		}
		sections[offset].WriteString(part)
	}
	var out [3]string
	for k := range sections {
		out[k] = strings.Trim(sections[k].String(), " \t\n\v\f\r\x00")
	}
	return out
}
