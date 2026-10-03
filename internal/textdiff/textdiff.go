// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package textdiff は Redmine::StringArrayDiff（lib/redmine/string_array_diff）と
// Redmine::Helpers::Diff（単語単位の差分の HTML。チケットの説明の差分表示に使う）の移植。
//
// 差分アルゴリズムは Redmine の実装（diff.rb の LCS）をそのまま移しており、
// 先頭の共通部分の刈り込みの条件（bstart <= afinish）など元の挙動も保っている。
package textdiff

import (
	"html"
	"regexp"
	"strings"
)

// Change は 1 件の変更（'+' / '-'、位置、要素）。
type Change struct {
	Op   byte
	Pos  int
	Elem string
}

// lcs は Diff.lcs(a, b)（a の各位置に対応する b の位置。対応が無ければ -1）。
func lcs(a, b []string) []int {
	astart, bstart := 0, 0
	afinish, bfinish := len(a)-1, len(b)-1
	mvector := map[int]int{}
	maxIdx := -1
	set := func(i, v int) {
		mvector[i] = v
		if i > maxIdx {
			maxIdx = i
		}
	}
	// 先頭の共通部分（Redmine の実装どおり bstart <= afinish で比較する）
	for astart <= afinish && bstart <= afinish && bstart < len(b) && a[astart] == b[bstart] {
		set(astart, bstart)
		astart++
		bstart++
	}
	// 末尾の共通部分
	for astart <= afinish && bstart <= bfinish && a[afinish] == b[bfinish] {
		set(afinish, bfinish)
		afinish--
		bfinish--
	}
	bmatches := map[string][]int{}
	for i := bstart; i <= bfinish; i++ {
		bmatches[b[i]] = append(bmatches[b[i]], i)
	}
	if !withinMatchBudget(a[astart:afinish+1], bmatches) {
		// 一致の組が多すぎる（同じ要素の繰り返し）。先頭・末尾の共通部分だけを一致とし、中間はすべて変更とする
		bmatches = nil
	}
	var thresh []int
	type link struct {
		prev   *link
		ai, bi int
	}
	var links []*link
	for aindex := astart; aindex <= afinish; aindex++ {
		bs, ok := bmatches[a[aindex]]
		if !ok {
			continue
		}
		k := -1 // nil
		for j := len(bs) - 1; j >= 0; j-- {
			bindex := bs[j]
			if k > 0 && thresh[k] > bindex && thresh[k-1] < bindex {
				thresh[k] = bindex
			} else {
				k = replaceNextLarger(&thresh, bindex, k)
			}
			if k >= 0 {
				var prev *link
				if k != 0 {
					prev = links[k-1]
				}
				for len(links) <= k {
					links = append(links, nil)
				}
				links[k] = &link{prev: prev, ai: aindex, bi: bindex}
			}
		}
	}
	if len(thresh) > 0 {
		for l := links[len(thresh)-1]; l != nil; l = l.prev {
			set(l.ai, l.bi)
		}
	}
	out := make([]int, maxIdx+1)
	for i := range out {
		out[i] = -1
	}
	for i, v := range mvector {
		out[i] = v
	}
	return out
}

// replaceNextLarger は Diffable#replacenextlarger（high が -1 なら nil）。nil の結果は -1。
func replaceNextLarger(arr *[]int, value, high int) int {
	if high < 0 {
		high = len(*arr)
	}
	a := *arr
	if len(a) == 0 || value > a[len(a)-1] {
		*arr = append(a, value)
		return high
	}
	low := 0
	for low < high {
		index := (high + low) / 2
		found := a[index]
		if value == found {
			return -1
		}
		if value > found {
			low = index + 1
		} else {
			high = index
		}
	}
	a[low] = value
	return low
}

// Diff は Redmine::StringArrayDiff::Diff.new(a, b).diffs。
func Diff(a, b []string) [][]Change {
	mvector := lcs(a, b)
	var diffs [][]Change
	var cur []Change
	match := func() {
		if len(cur) > 0 {
			diffs = append(diffs, cur)
		}
		cur = nil
	}
	ai, bi := 0, 0
	for ai < len(mvector) {
		bline := mvector[ai]
		if bline >= 0 {
			for bi < bline {
				cur = append(cur, Change{'+', bi, b[bi]})
				bi++
			}
			match()
			bi++
		} else {
			cur = append(cur, Change{'-', ai, a[ai]})
		}
		ai++
	}
	for ai < len(a) {
		cur = append(cur, Change{'-', ai, a[ai]})
		ai++
	}
	for bi < len(b) {
		cur = append(cur, Change{'+', bi, b[bi]})
		bi++
	}
	match()
	return diffs
}

var wsRe = regexp.MustCompile(`\s+`)

// splitWords は content.to_s.split(/(\s+)/).select {|w| w != ' '}。
func splitWords(s string) []string {
	var out []string
	last := 0
	for _, loc := range wsRe.FindAllStringIndex(s, -1) {
		out = append(out, s[last:loc[0]], s[loc[0]:loc[1]])
		last = loc[1]
	}
	out = append(out, s[last:])
	// String#split は末尾の空文字列を除く
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	res := out[:0]
	for _, w := range out {
		if w != " " {
			res = append(res, w)
		}
	}
	return res
}

// htmlReplacer は h のエスケープ（呼び出しごとに Replacer を作ると長文で遅い）。
var htmlReplacer = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "'", "&#39;", "<", "&lt;", ">", "&gt;")

func h(s string) string { return htmlReplacer.Replace(s) }

var _ = html.EscapeString

// ToHTML は Redmine::Helpers::Diff.new(contentTo, contentFrom).to_html。
func ToHTML(contentTo, contentFrom string) string {
	words := splitWords(contentTo)
	from := splitWords(contentFrom)
	diffs := Diff(from, words)
	escaped := make([]string, len(words))
	for i, w := range words {
		escaped[i] = h(w)
	}
	out := &gapList{rest: escaped}
	wordsAdd, wordsDel, dels, delOff := 0, 0, 0, 0
	var deleted strings.Builder
	for _, d := range diffs {
		addAt, addTo, delAt := -1, -1, -1
		deleted.Reset()
		for _, ch := range d {
			pos := ch.Pos
			if ch.Op == '+' {
				if addAt < 0 {
					addAt = pos + dels
				}
				addTo = pos + dels
				wordsAdd++
			} else {
				if delAt < 0 {
					delAt = pos
				}
				if deleted.Len() > 0 {
					deleted.WriteByte(' ')
				}
				deleted.WriteString(ch.Elem)
				wordsDel++
			}
		}
		if addAt >= 0 {
			out.set(addAt, `<span class="diff_in">`+out.get(addAt))
			out.set(addTo, out.get(addTo)+`</span>`)
		}
		if delAt >= 0 {
			idx := delAt - delOff + dels + wordsAdd
			ins := `<span class="diff_out">` + h(deleted.String()) + `</span>`
			if idx < 0 {
				idx += out.len() + 1
			}
			// Ruby の Array#insert は末尾より先なら nil で埋める（insert が処理する）
			out.insert(idx, ins)
			dels++
			delOff += wordsDel
			wordsDel = 0
		}
	}
	return out.join(" ")
}

// gapList は Array#insert を繰り返す ToHTML 用の列。挿入位置はほぼ単調に増えるため、
// 挿入位置より前を res、後ろを rest に分けて持ち、各挿入を償却 O(1) にする
// （単純な slice への挿入では変更の多い長文で二乗の時間がかかる）。
type gapList struct {
	res, rest []string
}

func (g *gapList) len() int { return len(g.res) + len(g.rest) }

func (g *gapList) get(i int) string {
	if i < len(g.res) {
		return g.res[i]
	}
	return g.rest[i-len(g.res)]
}

func (g *gapList) set(i int, v string) {
	if i < len(g.res) {
		g.res[i] = v
		return
	}
	g.rest[i-len(g.res)] = v
}

// insert は Array#insert(idx, v)（0 <= idx。末尾より先なら "" で埋める）。
func (g *gapList) insert(idx int, v string) {
	if idx < len(g.res) {
		// 稀な後戻り: 通常の slice 挿入
		g.res = append(g.res[:idx], append([]string{v}, g.res[idx:]...)...)
		return
	}
	n := min(idx-len(g.res), len(g.rest))
	g.res = append(g.res, g.rest[:n]...)
	g.rest = g.rest[n:]
	for len(g.res) < idx {
		g.res = append(g.res, "")
	}
	g.res = append(g.res, v)
}

func (g *gapList) join(sep string) string {
	return strings.Join(append(g.res, g.rest...), sep)
}

// MaxMatchPairs は LCS の計算で調べる一致の組の数の上限。Hunt-Szymanski 法は一致の組の数に比例する
// 時間がかかり、同じ行・単語を繰り返した文章では二乗に膨らむ（数百 KB の説明でサーバーを数分止められる）。
// 上限を超えたら中間部分の対応付けを諦める（差分は正しいが最小ではなくなる）。
const MaxMatchPairs = 10_000_000

// withinMatchBudget は a の各要素について b 側の一致位置の数を合計し、MaxMatchPairs 以下か。
func withinMatchBudget(a []string, bmatches map[string][]int) bool {
	total := 0
	for _, s := range a {
		total += len(bmatches[s])
		if total > MaxMatchPairs {
			return false
		}
	}
	return true
}
