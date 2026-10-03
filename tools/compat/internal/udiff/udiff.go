// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package udiff は行単位の Myers 差分と unified diff 形式の出力を提供する。
package udiff

import (
	"fmt"
	"strings"
)

// OpKind は編集操作の種類。
type OpKind int

const (
	Equal OpKind = iota
	Delete
	Insert
)

// Op は 1 行分の編集操作。
type Op struct {
	Kind OpKind
	Line string
}

// maxD を超える編集距離の場合は全置換として扱う（メモリ保護）。
const maxD = 4000

// Lines は文字列を行に分割する（末尾の改行は区切りとして扱う）。
func Lines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// Diff は a から b への行単位の編集列を返す。
func Diff(a, b []string) []Op {
	// 共通の先頭・末尾を除去してから Myers を適用
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var ops []Op
	for _, l := range a[:pre] {
		ops = append(ops, Op{Equal, l})
	}
	ops = append(ops, myers(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, Op{Equal, l})
	}
	return ops
}

func replaceAll(a, b []string) []Op {
	ops := make([]Op, 0, len(a)+len(b))
	for _, l := range a {
		ops = append(ops, Op{Delete, l})
	}
	for _, l := range b {
		ops = append(ops, Op{Insert, l})
	}
	return ops
}

func myers(a, b []string) []Op {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return replaceAll(a, b)
	}
	max := n + m
	if max > 2*maxD {
		max = 2 * maxD
	}
	off := max + 1
	v := make([]int, 2*max+4)
	var trace [][]int
	found := false
	for d := 0; d <= max && !found; d++ {
		// d ステップ目開始時点の v の該当範囲を保存
		snap := make([]int, 2*d+3)
		copy(snap, v[off-d-1:off+d+2])
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		return replaceAll(a, b)
	}
	// バックトラック
	var rev []Op
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		snap := trace[d]
		get := func(k int) int { return snap[k+d+1] }
		k := x - y
		var prevK int
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		var prevX int
		if d == 0 {
			prevX = 0
		} else {
			prevX = get(prevK)
		}
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Op{Equal, a[x]})
		}
		if d > 0 {
			if x == prevX {
				y--
				rev = append(rev, Op{Insert, b[y]})
			} else {
				x--
				rev = append(rev, Op{Delete, a[x]})
			}
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, Op{Equal, a[x]})
	}
	ops := make([]Op, len(rev))
	for i := range rev {
		ops[i] = rev[len(rev)-1-i]
	}
	return ops
}

// Unified は unified diff 形式の文字列を返す。差分がなければ空文字列。
func Unified(a, b, nameA, nameB string, context int) string {
	ops := Diff(Lines(a), Lines(b))
	changed := false
	for _, o := range ops {
		if o.Kind != Equal {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", nameA, nameB)

	// 各 op の a/b 行番号（1 始まり）
	type pos struct{ ai, bi int }
	ps := make([]pos, len(ops)+1)
	ai, bi := 1, 1
	for i, o := range ops {
		ps[i] = pos{ai, bi}
		switch o.Kind {
		case Equal:
			ai++
			bi++
		case Delete:
			ai++
		case Insert:
			bi++
		}
	}
	ps[len(ops)] = pos{ai, bi}

	i := 0
	for i < len(ops) {
		// 次の変更を探す
		for i < len(ops) && ops[i].Kind == Equal {
			i++
		}
		if i >= len(ops) {
			break
		}
		start := max(i-context, 0)
		// ハンクの終わり: 変更後に context*2 を超える Equal が続くまで
		end := i
		for end < len(ops) {
			if ops[end].Kind != Equal {
				end++
				continue
			}
			j := end
			for j < len(ops) && ops[j].Kind == Equal {
				j++
			}
			if j >= len(ops) || j-end > 2*context {
				end = min(end+context, len(ops))
				break
			}
			end = j
		}
		var na, nb int
		for _, o := range ops[start:end] {
			if o.Kind != Insert {
				na++
			}
			if o.Kind != Delete {
				nb++
			}
		}
		sa, sb := ps[start].ai, ps[start].bi
		if na == 0 {
			sa--
		}
		if nb == 0 {
			sb--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", sa, na, sb, nb)
		for _, o := range ops[start:end] {
			switch o.Kind {
			case Equal:
				out.WriteString(" ")
			case Delete:
				out.WriteString("-")
			case Insert:
				out.WriteString("+")
			}
			out.WriteString(o.Line)
			out.WriteByte('\n')
		}
		i = end
	}
	return out.String()
}
