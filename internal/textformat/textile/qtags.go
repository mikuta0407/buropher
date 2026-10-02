// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

// QTAGS の正規表現 (redcloth3.rb:382-390) の専用マッチャ。
//
// 原典の正規表現は内容部 ([^\s].*?[^\s]) が遅延量指定子で行末まで走査するため、
// 閉じ記号の無い開始位置が多い長い行ではバックトラック型エンジンで O(n^2) になる
// (Ruby 3.3 はメモ化で回避している)。ここでは閉じ位置の候補を事前計算して、
// 正規表現と同じ結果 (最左・同じバックトラック順) を線形時間で求める。
// 正規表現版 (qtag.re) との一致はテストで検証している。

import (
	"strings"
	"unicode"
)

// qtagJoin は QTAGS_JOIN の選択肢 (正規表現内の順序どおり)。
var qtagJoin = []string{"**", "*", "??", "-", "__", "_", "%", "+", "^", "~"}

func isRubySpaceRune(r rune) bool {
	return r < 0x80 && isRubySpace(byte(r))
}

// isWordRune は [[:word:]] 相当。
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.IsMark(r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// isPunctRune は [[:punct:]] 相当。
func isPunctRune(r rune) bool {
	return unicode.IsPunct(r) || strings.ContainsRune("$+<=>^`|~", r)
}

func hasPrefixRunes(text []rune, i int, p string) bool {
	for _, r := range p {
		if i >= len(text) || text[i] != r {
			return false
		}
		i++
	}
	return true
}

// qtagGsub は text.gsub!(qtag_re) { ... } と同じ置換を行う。
func (rc *redcloth) qtagGsub(text string, q qtag) string {
	if !strings.Contains(text, q.rc) {
		return text
	}
	rs := []rune(text)
	n := len(rs)
	rcLen := len([]rune(q.rc))

	// closeAt[e]: 位置 e で「(?!--) rcq (JOIN|) (?=...)」が成立し、かつ rs[e-1] が非空白なら
	// 採用される oqa の長さ+1 (不成立なら 0)
	closeAt := make([]int, n+1)
	for e := 1; e <= n; e++ {
		if isRubySpaceRune(rs[e-1]) {
			continue
		}
		if hasPrefixRunes(rs, e, "--") || !hasPrefixRunes(rs, e, q.rc) {
			continue
		}
		f := e + rcLen
		look := func(i int) bool {
			if i >= n {
				return true
			}
			r := rs[i]
			return r == '\n' || isPunctRune(r) || r == '<' || isRubySpaceRune(r) || r == ')'
		}
		for _, j := range append(qtagJoin, "") {
			if hasPrefixRunes(rs, f, j) && look(f+len([]rune(j))) {
				closeAt[e] = len([]rune(j)) + 1
				break
			}
		}
	}
	// nextClose[i]: i 以上で closeAt が成立する最小位置 (なければ -1)
	nextClose := make([]int, n+2)
	nextClose[n+1] = -1
	for i := n; i >= 0; i-- {
		if closeAt[i] > 0 {
			nextClose[i] = i
		} else {
			nextClose[i] = nextClose[i+1]
		}
	}
	// nextNL[i]: i 以上で最初の改行位置 (なければ n)
	nextNL := make([]int, n+1)
	nextNL[n] = n
	for i := n - 1; i >= 0; i-- {
		if rs[i] == '\n' {
			nextNL[i] = i
		} else {
			nextNL[i] = nextNL[i+1]
		}
	}

	// 位置 c0 から始まる content の終端 e を求める (見つからなければ -1)
	content := func(c0 int) int {
		if c0 >= n {
			return -1
		}
		// 選択肢 1: [[:word:]] 1 文字
		if isWordRune(rs[c0]) && c0+1 <= n && closeAt[c0+1] > 0 {
			return c0 + 1
		}
		// 選択肢 2: [^\s].*?[^\s] (. は改行以外)
		if isRubySpaceRune(rs[c0]) || c0+2 > n {
			return -1
		}
		e := nextClose[c0+2]
		if e < 0 {
			return -1
		}
		// content (c0..e-1) の中間 (c0+1..e-2) に改行を含まないこと
		// (rs[c0] と rs[e-1] は非空白なので改行ではない)
		if nextNL[c0] < e-1 {
			return -1
		}
		return e
	}

	// 位置 q0 (sta の直後) からマッチを試みる
	tryAt := func(q0 int) (oqs string, c0, e int, ok bool) {
		if hasPrefixRunes(rs, q0, "--") {
			return "", 0, 0, false
		}
		for _, j := range append(qtagJoin, "") {
			if !hasPrefixRunes(rs, q0, j) {
				continue
			}
			p := q0 + len([]rune(j))
			if !hasPrefixRunes(rs, p, q.rc) {
				continue
			}
			c := p + rcLen
			if e := content(c); e >= 0 {
				return j, c, e, true
			}
		}
		return "", 0, 0, false
	}

	var out strings.Builder
	prev := 0
	p := 0
	for p < n {
		var sta string
		var q0, c0, e int
		var oqs string
		found := false
		// sta: ^ (ゼロ幅) を先に試し、次に [>\s(] の 1 文字
		if p == 0 || rs[p-1] == '\n' {
			if o, c, ee, ok := tryAt(p); ok {
				sta, q0, oqs, c0, e, found = "", p, o, c, ee, true
			}
		}
		if !found {
			r := rs[p]
			if r == '>' || r == '(' || isRubySpaceRune(r) {
				if o, c, ee, ok := tryAt(p + 1); ok {
					sta, q0, oqs, c0, e, found = string(r), p+1, o, c, ee, true
				}
			}
		}
		_ = q0
		if !found {
			p++
			continue
		}
		oqaLen := closeAt[e] - 1
		end := e + rcLen + oqaLen
		contentStr := string(rs[c0:e])
		oqa := string(rs[e+rcLen : end])

		out.WriteString(string(rs[prev:p]))
		out.WriteString(rc.qtagReplace(q, sta, oqs, contentStr, oqa))
		prev = end
		p = end
	}
	if prev == 0 {
		return text
	}
	out.WriteString(string(rs[prev:]))
	return out.String()
}

// qtagReplace は inline_textile_span のブロック本体 (redcloth3.rb:790-805)。
func (rc *redcloth) qtagReplace(q qtag, sta, oqs, content, oqa string) string {
	var atts *string
	if cm := match(reSpanAtts, content); cm != nil {
		atts = cm.opt(1)
		content = cm.s(2)
	}
	a := rc.shelve(rc.pba(atts, ""))
	return sta + oqs + "<" + q.ht + a + ">" + content + "</" + q.ht + ">" + oqa
}
