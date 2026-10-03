// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package unifieddiff は Redmine::UnifiedDiff / Redmine::DiffTable / Redmine::Diff
// （lib/redmine/unified_diff.rb, diff_table.rb, diff.rb）の移植。unified diff を解析して
// ファイルごとの表（インライン表示 inline / 左右表示 sbs）にする。
// 添付ファイルの差分の表示（attachments#show）と、リポジトリの差分表示で使う。
package unifieddiff

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Diff は差分の 1 行（Redmine::Diff）。行番号は無ければ 0。
type Diff struct {
	NbLineLeft    int
	LineLeft      string
	NbLineRight   int
	LineRight     string
	TypeDiffRight string
	TypeDiffLeft  string
	// Offsets は変更部分の [開始, 終了]（終了は負の添字。nil なら強調なし）。
	Offsets []int
}

// NbLineLeftString は nb_line_left（0 なら ""）。
func (d *Diff) NbLineLeftString() string { return nbString(d.NbLineLeft) }

// NbLineRightString は nb_line_right（0 なら ""）。
func (d *Diff) NbLineRightString() string { return nbString(d.NbLineRight) }

func nbString(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// TypeDiff は type_diff。
func (d *Diff) TypeDiff() string {
	if d.TypeDiffRight == "diff_in" {
		return d.TypeDiffRight
	}
	return d.TypeDiffLeft
}

// Line は line。
func (d *Diff) Line() string {
	if d.TypeDiffRight == "diff_in" {
		return d.LineRight
	}
	return d.LineLeft
}

// HTMLLineLeft は html_line_left。
func (d *Diff) HTMLLineLeft() string { return lineToHTML(d.LineLeft, d.Offsets) }

// HTMLLineRight は html_line_right。
func (d *Diff) HTMLLineRight() string { return lineToHTML(d.LineRight, d.Offsets) }

// HTMLLine は html_line。
func (d *Diff) HTMLLine() string { return lineToHTML(d.Line(), d.Offsets) }

// escapeHTML は CGI.escapeHTML（& < > " '）。
func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}

var _ = html.EscapeString

// rsub は Ruby の str[a..b]（文字単位。負の添字は末尾から。範囲外は ""）。
func rsub(r []rune, a, b int) string {
	n := len(r)
	if a < 0 {
		a += n
	}
	if b < 0 {
		b += n
	}
	if a < 0 || a > n {
		return ""
	}
	if b >= n {
		b = n - 1
	}
	if b < a {
		return ""
	}
	return string(r[a : b+1])
}

func lineToHTML(line string, offsets []int) string {
	if offsets == nil {
		return escapeHTML(line)
	}
	r := []rune(line)
	var s strings.Builder
	if offsets[0] != 0 {
		s.WriteString(escapeHTML(rsub(r, 0, offsets[0]-1)))
	}
	s.WriteString("<span>" + escapeHTML(rsub(r, offsets[0], offsets[1])) + "</span>")
	if offsets[1] != -1 {
		s.WriteString(escapeHTML(rsub(r, offsets[1]+1, -1)))
	}
	return s.String()
}

// Line は each_line の 1 要素（spacing は前の行と連続していないとき真）。
type Line struct {
	Spacing bool
	*Diff
}

// Table はファイル 1 つ分の差分（Redmine::DiffTable）。
type Table struct {
	Diffs            []*Diff
	FileName         string
	PreviousFileName string

	typ, style           string
	parsing              bool
	added, removed       int
	lineNumL, lineNumR   int
	gitDiff, hasFileName bool
}

var (
	reFileHeader = regexp.MustCompile(`^(---|\+\+\+) (.*)$`)
	reHunk       = regexp.MustCompile(`^@@ (\+|\-)(\d+)(,\d+)? (\+|\-)(\d+)(,\d+)? @@`)
	reEnd        = regexp.MustCompile(`^[^\+\-\s@\\]`)
	reGitA       = regexp.MustCompile(`^(a/|/dev/null)`)
	reGitB       = regexp.MustCompile(`^(b/|/dev/null)`)
	reSvnRev     = regexp.MustCompile(`\t+\(.*\)$`)
)

func newTable(typ, style string) *Table { return &Table{typ: typ, style: style} }

// addLine は add_line（差分の終わりなら false）。
func (t *Table) addLine(line string) bool {
	if !t.parsing {
		if m := reFileHeader.FindStringSubmatch(line); m != nil {
			t.setFileName(m[2])
		} else if m := reHunk.FindStringSubmatch(line); m != nil {
			t.lineNumL, _ = strconv.Atoi(m[2])
			t.lineNumR, _ = strconv.Atoi(m[5])
			t.parsing = true
		}
		return true
	}
	if reEnd.MatchString(line) {
		t.parsing = false
		return false
	}
	if m := reHunk.FindStringSubmatch(line); m != nil {
		t.lineNumL, _ = strconv.Atoi(m[2])
		t.lineNumR, _ = strconv.Atoi(m[5])
		return true
	}
	t.parseLine(line)
	return true
}

// Lines は each_line。
func (t *Table) Lines() []Line {
	var out []Line
	prevL, prevR := 0, 0
	for _, d := range t.Diffs {
		spacing := prevL != 0 && prevR != 0 && d.NbLineLeft != prevL+1 && d.NbLineRight != prevR+1
		out = append(out, Line{Spacing: spacing, Diff: d})
		if d.NbLineLeft > 0 {
			prevL = d.NbLineLeft
		}
		if d.NbLineRight > 0 {
			prevR = d.NbLineRight
		}
	}
	return out
}

func (t *Table) setFileName(arg string) {
	bothGit := false
	if !t.hasFileName {
		if reGitA.MatchString(arg) {
			t.gitDiff = true
		}
	} else {
		bothGit = t.gitDiff && reGitB.MatchString(arg)
	}
	switch {
	case bothGit:
		if t.hasFileName && arg == "/dev/null" {
			t.FileName = strings.TrimPrefix(t.FileName, "a/")
		} else {
			if t.FileName != "/dev/null" {
				t.PreviousFileName = strings.TrimPrefix(t.FileName, "a/")
			}
			t.FileName = strings.TrimPrefix(arg, "b/")
			if t.PreviousFileName == t.FileName {
				t.PreviousFileName = ""
			}
		}
	case t.style == "Subversion":
		t.FileName = reSvnRev.ReplaceAllString(arg, "")
	default:
		t.FileName = arg
	}
	t.hasFileName = true
}

func (t *Table) diffForAddedLine() *Diff {
	if t.typ == "sbs" && t.removed > 0 && t.added < t.removed {
		return t.Diffs[len(t.Diffs)-(t.removed-t.added)]
	}
	d := &Diff{}
	t.Diffs = append(t.Diffs, d)
	return d
}

func rest(line string) string {
	_, size := utf8.DecodeRuneInString(line)
	return line[size:]
}

func (t *Table) parseLine(line string) {
	switch {
	case strings.HasPrefix(line, "+"):
		d := t.diffForAddedLine()
		d.LineRight = rest(line)
		d.NbLineRight = t.lineNumR
		d.TypeDiffRight = "diff_in"
		t.lineNumR++
		t.added++
	case strings.HasPrefix(line, "-"):
		d := &Diff{LineLeft: rest(line), NbLineLeft: t.lineNumL, TypeDiffLeft: "diff_out"}
		t.Diffs = append(t.Diffs, d)
		t.lineNumL++
		t.removed++
	default:
		t.writeOffsets()
		if r, _ := utf8.DecodeRuneInString(line); line != "" && unicode.IsSpace(r) && r < utf8.RuneSelf {
			l := rest(line)
			t.Diffs = append(t.Diffs, &Diff{LineRight: l, NbLineRight: t.lineNumR, LineLeft: l, NbLineLeft: t.lineNumL})
			t.lineNumL++
			t.lineNumR++
		}
		// "\" で始まる行（\ No newline at end of file）と空行は表示しない
	}
}

func (t *Table) writeOffsets() {
	if t.added > 0 && t.added == t.removed {
		n := len(t.Diffs)
		for i := 0; i < t.added; i++ {
			line := t.Diffs[n-(1+i)]
			removed := line
			if t.typ != "sbs" {
				removed = t.Diffs[n-(1+t.added+i)]
			}
			off := offsets(removed.LineLeft, line.LineRight)
			removed.Offsets, line.Offsets = off, off
		}
	}
	t.added, t.removed = 0, 0
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func offsets(left, right string) []int {
	if blank(left) || blank(right) || left == right {
		return nil
	}
	l, r := []rune(left), []rune(right)
	maxLen := min(len(l), len(r))
	start := 0
	for start < maxLen && l[start] == r[start] {
		start++
	}
	end := -1
	for end >= -(maxLen-start) && l[len(l)+end] == r[len(r)+end] {
		end--
	}
	if start == 0 && end == -1 {
		return nil
	}
	return []int{start, end}
}

// ReplaceInvalidUTF8 は Redmine::CodesetUtil.replace_invalid_utf8（UTF-8 として不正なバイトを 1 バイトずつ "?" にする）。
func ReplaceInvalidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteByte('?')
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// UnifiedDiff は Redmine::UnifiedDiff（ファイルごとの Table の列）。
type UnifiedDiff struct {
	Tables    []*Table
	DiffType  string
	Truncated bool
}

var reGitFooterNum = regexp.MustCompile(`^[0-9]`)

// Parse は UnifiedDiff.new(diff, :type => typ, :style => style, :max_lines => maxLines)。
// toUTF8 は Redmine::CodesetUtil.to_utf8_by_setting（nil なら不正なバイトを "?" にする）。
func Parse(diff, typ, style string, maxLines int, toUTF8 func(string) string) *UnifiedDiff {
	if typ == "" {
		typ = "inline"
	}
	if toUTF8 == nil {
		toUTF8 = ReplaceInvalidUTF8
	}
	lines := splitLines(diff)
	// git の末尾（"-- " と版数）を除く
	if n := len(lines); n > 1 && strings.HasPrefix(lines[n-2], "--") && reGitFooterNum.MatchString(lines[n-1]) {
		lines = lines[:n-2]
	}
	u := &UnifiedDiff{DiffType: typ}
	t := newTable(typ, style)
	count := 0
	for _, raw := range lines {
		line := toUTF8(raw)
		if !t.addLine(line) {
			if len(t.Diffs) > 0 {
				u.Tables = append(u.Tables, t)
			}
			t = newTable(typ, style)
		}
		count++
		if maxLines > 0 && count > maxLines {
			u.Truncated = true
			break
		}
	}
	if len(t.Diffs) > 0 {
		u.Tables = append(u.Tables, t)
	}
	return u
}

// splitLines は String#split("\n")（末尾の空要素は除く）。
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
