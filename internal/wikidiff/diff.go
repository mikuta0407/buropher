// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package wikidiff は Redmine 6.1.2 の Wiki 差分・注釈 (annotate) 処理を
// 純 Go で移植したものである。Ruby 版と入力に対して同一の結果を返すことを目的とする。
//
// 移植元:
//   - lib/redmine/string_array_diff/diff.rb, diffable.rb
//     (Lars Christensen による LCS ベースの配列 diff) → [DiffStrings]
//   - lib/redmine/helpers/diff.rb (Redmine::Helpers::Diff, WikiDiff の基底)
//     → [SplitWords], [WordDiffHTML]
//   - app/models/wiki_annotate.rb (WikiAnnotate) → [Annotate]
//
// 公開 API:
//
//	func DiffStrings(from, to []string) [][]Change
//	func SplitWords(text string) []string
//	func WordDiffHTML(textTo, textFrom string) string
//	func SplitLines(text string) []string
//	func Annotate(current Version, previous func(v Version) (Version, bool)) []Line
//
// Ruby 実装の癖 (LCS の先頭刈り込みループの条件が `bstart <= afinish` になっている点、
// replacenextlarger が上限指定時に push しても上限値を返す点、thresh[k-1] の k=0 で
// 負インデックスになる点など) もそのまま再現している。
package wikidiff

// Change は diff の 1 要素を表す。Ruby の [sign, position, element] に対応する。
type Change struct {
	// Sign は '+' (追加) または '-' (削除)。
	Sign byte
	// Pos は '-' なら from 側、'+' なら to 側のインデックス。
	Pos int
	// Elem は対象要素。
	Elem string
}

// lcsLink は Diff.lcs の links 要素 ([prev, aindex, bindex]) に対応する。
type lcsLink struct {
	prev   *lcsLink
	aindex int
	bindex int
}

// noIndex は Ruby の nil に相当するインデックス値。
const noIndex = -1

// lcs は Redmine::StringArrayDiff::Diff.lcs の移植。
// 戻り値は a のインデックスに対応する b のインデックス (未対応は noIndex) で、
// 長さは Ruby の mvector.length と一致する。
func lcs(a, b []string) []int {
	var mvector []int
	set := func(i, v int) {
		for len(mvector) <= i {
			mvector = append(mvector, noIndex)
		}
		mvector[i] = v
	}

	astart, bstart := 0, 0
	afinish, bfinish := len(a)-1, len(b)-1

	// 先頭の共通要素を刈り込む (Ruby 版の条件 bstart <= afinish をそのまま再現)
	for astart <= afinish && bstart <= afinish && bstart < len(b) && a[astart] == b[bstart] {
		set(astart, bstart)
		astart++
		bstart++
	}

	// 末尾の共通要素を刈り込む
	for astart <= afinish && bstart <= bfinish && a[afinish] == b[bfinish] {
		set(afinish, bfinish)
		afinish--
		bfinish--
	}

	// reverse_hash(bstart..bfinish)
	bmatches := make(map[string][]int)
	for i := bstart; i <= bfinish; i++ {
		bmatches[b[i]] = append(bmatches[b[i]], i)
	}

	if !withinMatchBudget(a[astart:afinish+1], bmatches) {
		// 一致の組が多すぎる（同じ行の繰り返し）。先頭・末尾の共通部分だけを一致とし、中間はすべて変更とする
		bmatches = nil
	}
	var thresh []int
	var links []*lcsLink
	getLink := func(i int) *lcsLink {
		if i < 0 || i >= len(links) {
			return nil
		}
		return links[i]
	}

	for aindex := astart; aindex <= afinish; aindex++ {
		idxs, ok := bmatches[a[aindex]]
		if !ok {
			continue
		}
		k := noIndex
		for j := len(idxs) - 1; j >= 0; j-- {
			bindex := idxs[j]
			if k != noIndex && thresh[k] > bindex && rubyAt(thresh, k-1) < bindex {
				thresh[k] = bindex
			} else {
				k = replaceNextLarger(&thresh, bindex, k)
			}
			if k != noIndex {
				var prev *lcsLink
				if k != 0 {
					prev = getLink(k - 1)
				}
				for len(links) <= k {
					links = append(links, nil)
				}
				links[k] = &lcsLink{prev: prev, aindex: aindex, bindex: bindex}
			}
		}
	}

	if len(thresh) > 0 {
		for link := getLink(len(thresh) - 1); link != nil; link = link.prev {
			set(link.aindex, link.bindex)
		}
	}
	return mvector
}

// rubyAt は Ruby の配列参照 (負インデックスは末尾から) を再現する。
// 呼び出し側で範囲内であることが保証されている前提。
func rubyAt(s []int, i int) int {
	if i < 0 {
		i += len(s)
	}
	return s[i]
}

// replaceNextLarger は Diffable#replacenextlarger の移植。
// high が noIndex の場合は Ruby の nil (= self.length) として扱う。
// 戻り値 noIndex は Ruby の nil を表す。
func replaceNextLarger(thresh *[]int, value, high int) int {
	s := *thresh
	if high == noIndex {
		high = len(s)
	}
	if len(s) == 0 || value > s[len(s)-1] {
		*thresh = append(s, value)
		return high
	}
	// 置換位置を二分探索する
	low := 0
	for low < high {
		index := (high + low) / 2
		found := s[index]
		if value == found {
			return noIndex
		}
		if value > found {
			low = index + 1
		} else {
			high = index
		}
	}
	for len(s) <= low {
		s = append(s, 0)
	}
	s[low] = value
	*thresh = s
	return low
}

// DiffStrings は Ruby の `from.diff(to).diffs` を移植したもの。
// 連続した変更をまとめたハンク ([[sign, pos, elem], ...]) の列を返す。
// 差分がなければ空 (nil) を返す。
func DiffStrings(from, to []string) [][]Change {
	mvector := lcs(from, to)
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
		if bline != noIndex {
			for bi < bline {
				cur = append(cur, Change{Sign: '+', Pos: bi, Elem: to[bi]})
				bi++
			}
			match()
			bi++
		} else {
			cur = append(cur, Change{Sign: '-', Pos: ai, Elem: from[ai]})
		}
		ai++
	}
	for ai < len(from) {
		cur = append(cur, Change{Sign: '-', Pos: ai, Elem: from[ai]})
		ai++
	}
	for bi < len(to) {
		cur = append(cur, Change{Sign: '+', Pos: bi, Elem: to[bi]})
		bi++
	}
	match()
	return diffs
}

// maxMatchPairs は LCS の計算で調べる一致の組の数の上限（textdiff.MaxMatchPairs と同じ理由）。
// 同じ行を繰り返したページの差分・注釈で二乗の時間がかかるのを防ぐ。
const maxMatchPairs = 10_000_000

// withinMatchBudget は a の各要素について b 側の一致位置の数を合計し、maxMatchPairs 以下か。
func withinMatchBudget(a []string, bmatches map[string][]int) bool {
	total := 0
	for _, s := range a {
		total += len(bmatches[s])
		if total > maxMatchPairs {
			return false
		}
	}
	return true
}
