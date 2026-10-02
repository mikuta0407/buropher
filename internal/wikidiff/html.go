// SPDX-License-Identifier: GPL-2.0-or-later

package wikidiff

import "strings"

// isRubySpace は Ruby (Onigmo) の /\s/ ([ \t\n\v\f\r]) に一致するかを返す。
func isRubySpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// SplitWords は Redmine::Helpers::Diff の単語分割を移植したもの。
// Ruby の `text.split(/(\s+)/).select {|w| w != ' '}` と同じ結果を返す。
// (キャプチャした空白も要素に含まれ、末尾の空文字列は除去され、
// 先頭が空白の場合は先頭に空文字列が残る。ちょうど " " の要素は除去される。)
func SplitWords(text string) []string {
	var out []string
	add := func(s string) {
		if s != " " {
			out = append(out, s)
		}
	}
	if text == "" {
		return out
	}
	i := 0
	if isRubySpace(text[0]) {
		// 先頭のマッチ前の空フィールドは保持される
		add("")
	}
	for i < len(text) {
		j := i
		sp := isRubySpace(text[i])
		for j < len(text) && isRubySpace(text[j]) == sp {
			j++
		}
		add(text[i:j])
		i = j
	}
	return out
}

// htmlEscape は ERB::Util.html_escape (CGI.escapeHTML) 相当のエスケープを行う。
var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)

func htmlEscape(s string) string { return htmlEscaper.Replace(s) }

// WordDiffHTML は `Redmine::Helpers::Diff.new(textTo, textFrom).to_html` を移植したもの。
// textFrom から textTo への単語単位の差分を、追加語を <span class="diff_in">、
// 削除語を <span class="diff_out"> で囲んだ HTML 断片として返す。
// (WikiDiff.new(content_to, content_from) も同じ処理である。)
func WordDiffHTML(textTo, textFrom string) string {
	toWords := SplitWords(textTo)
	fromWords := SplitWords(textFrom)
	diffs := DiffStrings(fromWords, toWords)

	// Ruby の nil 要素 (配列の穴) は safe_join で "" になるため "" で表す。
	words := make([]string, len(toWords))
	for i, w := range toWords {
		words[i] = htmlEscape(w)
	}
	ensure := func(i int) {
		for len(words) <= i {
			words = append(words, "")
		}
	}

	wordsAdd, wordsDel, dels, delOff := 0, 0, 0, 0
	for _, hunk := range diffs {
		addAt, addTo, delAt := noIndex, noIndex, noIndex
		var deleted strings.Builder
		for _, ch := range hunk {
			pos := ch.Pos
			if ch.Sign == '+' {
				if addAt == noIndex {
					addAt = pos + dels
				}
				addTo = pos + dels
				wordsAdd++
			} else {
				if delAt == noIndex {
					delAt = pos
				}
				if deleted.Len() > 0 {
					deleted.WriteByte(' ')
				}
				deleted.WriteString(ch.Elem)
				wordsDel++
			}
		}
		if addAt != noIndex {
			ensure(addAt)
			words[addAt] = `<span class="diff_in">` + words[addAt]
			ensure(addTo)
			words[addTo] = words[addTo] + `</span>`
		}
		if delAt != noIndex {
			span := `<span class="diff_out">` + htmlEscape(deleted.String()) + `</span>`
			words = rubyInsert(words, delAt-delOff+dels+wordsAdd, span)
			dels++
			delOff += wordsDel
			wordsDel = 0
		}
	}
	return strings.Join(words, " ")
}

// rubyInsert は Ruby の Array#insert(idx, obj) を再現する。
// 長さを超える位置には nil ("" で表現) を詰め、負の位置は末尾からとして扱う。
// (Ruby で IndexError になる範囲外の負値は先頭に挿入する。)
func rubyInsert(s []string, idx int, v string) []string {
	if idx < 0 {
		idx += len(s) + 1
		if idx < 0 {
			idx = 0
		}
	}
	for len(s) < idx {
		s = append(s, "")
	}
	s = append(s, "")
	copy(s[idx+1:], s[idx:])
	s[idx] = v
	return s
}
