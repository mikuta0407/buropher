// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import "github.com/yuin/goldmark/text"

// inlineLinkFollows は現在位置から off バイト先の '(' が comrak の
// handle_close_bracket でインラインリンクの宛先部分として受理されるかを返す
// （読み位置は戻す）。
func inlineLinkFollows(block text.Reader, off int) bool {
	l, pos := block.Position()
	defer block.SetPosition(l, pos)
	var s []byte
	for {
		line, _ := block.PeekLine()
		if line == nil {
			break
		}
		s = append(s, line...)
		block.AdvanceLine()
	}
	if off >= len(s) || s[off] != '(' {
		return false
	}
	p := spaceChars(s, off+1)
	if p >= len(s) {
		return false
	}
	n, ok := manualScanLinkURL(s[p:])
	if !ok {
		return false
	}
	endURL := p + n
	startTitle := spaceChars(s, endURL)
	endTitle := startTitle
	if startTitle != endURL {
		if t, ok := scanLinkTitle(s[startTitle:]); ok {
			endTitle += t
		}
	}
	endAll := spaceChars(s, endTitle)
	return endAll < len(s) && s[endAll] == ')'
}

func spaceChars(s []byte, p int) int {
	for p < len(s) && (s[p] == ' ' || s[p] == '\t' || s[p] == '\v' || s[p] == '\f' || s[p] == '\r' || s[p] == '\n') {
		p++
	}
	return p
}
