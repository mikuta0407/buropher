// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package wikidiff

import "strings"

// Version は注釈対象となる Wiki コンテンツの 1 版を表す。
type Version struct {
	// Version は版番号。
	Version int
	// AuthorID は作成者 ID。0 は作成者不明 (Ruby の nil) を表す。
	AuthorID int64
	// Text は本文。
	Text string
}

// Line は注釈付きの 1 行を表す。WikiAnnotate#lines の [version, author, text] に対応する。
type Line struct {
	// Version はその行を最後に変更した版番号。
	Version int
	// AuthorID はその版の作成者 ID (HasAuthor が false の場合は 0)。
	AuthorID int64
	// HasAuthor は作成者が判明しているか (Ruby で author が nil でないか)。
	HasAuthor bool
	// Text は行の内容。
	Text string
}

// SplitLines は Ruby の `text.split(/\r?\n/)` を再現する (末尾の空文字列は除去される)。
func SplitLines(text string) []string {
	parts := strings.Split(text, "\n")
	for i := 0; i < len(parts)-1; i++ {
		parts[i] = strings.TrimSuffix(parts[i], "\r")
	}
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// posNil は positions 配列における Ruby の nil を表す。
const posNil = -2

// Annotate は WikiAnnotate.new(content).lines を移植したもの。
// current は最新 (注釈対象) の版、previous は与えた版の直前の版を返す関数で、
// 直前の版がなければ false を返す。
//
// Ruby 版と同様に、最後に辿った版が 1 の場合のみ作成者不明の行へその版の作成者を補う
// (履歴が削除されて最古の版が 1 より大きい場合は作成者不明のままとなる)。
func Annotate(current Version, previous func(v Version) (Version, bool)) []Line {
	type annLine struct {
		version    int
		hasVersion bool
		author     int64
		text       string
	}
	currentLines := SplitLines(current.Text)
	lines := make([]annLine, len(currentLines))
	positions := make([]int, len(currentLines))
	for i, t := range currentLines {
		lines[i].text = t
		positions[i] = i
	}
	posAt := func(i int) int {
		if i < 0 || i >= len(positions) {
			return posNil
		}
		return positions[i]
	}

	for {
		prev, ok := previous(current)
		if !ok {
			break
		}
		var d []Change
		for _, hunk := range DiffStrings(SplitLines(prev.Text), SplitLines(current.Text)) {
			d = append(d, hunk...)
		}
		for _, c := range d {
			p := posAt(c.Pos)
			if c.Sign == '+' && p != posNil && p != -1 {
				if !lines[p].hasVersion {
					lines[p].version = current.Version
					lines[p].hasVersion = true
					lines[p].author = current.AuthorID
				}
			}
		}
		for _, c := range d {
			if c.Sign == '-' {
				// positions.insert(line, -1)
				for len(positions) < c.Pos {
					positions = append(positions, posNil)
				}
				positions = append(positions, 0)
				copy(positions[c.Pos+1:], positions[c.Pos:])
				positions[c.Pos] = -1
			} else {
				// positions[line] = nil
				for len(positions) <= c.Pos {
					positions = append(positions, posNil)
				}
				positions[c.Pos] = posNil
			}
		}
		// positions.compact!
		compacted := positions[:0]
		for _, p := range positions {
			if p != posNil {
				compacted = append(compacted, p)
			}
		}
		positions = compacted

		// すべての行に注釈が付いたら終了
		done := true
		for _, l := range lines {
			if !l.hasVersion {
				done = false
				break
			}
		}
		if done {
			break
		}
		current = prev
	}

	out := make([]Line, len(lines))
	for i, l := range lines {
		if !l.hasVersion {
			l.version = current.Version
		}
		if l.author == 0 && current.Version == 1 {
			l.author = current.AuthorID
		}
		out[i] = Line{Version: l.version, AuthorID: l.author, HasAuthor: l.author != 0, Text: l.text}
	}
	return out
}
