// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package redmine

// Ruby (Onigmo) の正規表現の意味論を regexp2 で再現する補助関数（textile パッケージの rx.go と同じ方針）。
//
//   - Ruby の ^ / $ は常に行アンカー → Multiline を付ける
//   - Ruby の /m → Singleline、/i → IgnoreCase
//   - Ruby の \s \w \d は ASCII のみ → 明示的な文字クラスで書く
//   - [[:punct:]] / \p{Word} は Onigmo の Unicode 定義に展開する

import (
	"strings"
	"unicode"

	"github.com/dlclark/regexp2"
)

const (
	// Ruby \s（ASCII 空白）
	spIn = ` \t\n\v\f\r`
)

var (
	otherAlphaIn = rangeTableClass(unicode.Other_Alphabetic)
	// \p{Word}: Alphabetic | Mark | Decimal_Number | Connector_Punctuation
	wordIn = `\p{L}\p{Nl}\p{M}\p{Nd}\p{Pc}` + otherAlphaIn
	// [[:punct:]]: Punctuation + ASCII の記号 9 文字（Ruby 2.4+）
	punctIn = `\p{P}\$\+<=>\^` + "`" + `\|~`
)

func rangeTableClass(t *unicode.RangeTable) string {
	var b strings.Builder
	emit := func(lo, hi, stride rune) {
		if stride == 1 {
			b.WriteRune(lo)
			if hi > lo {
				b.WriteByte('-')
				b.WriteRune(hi)
			}
			return
		}
		for r := lo; r <= hi; r += stride {
			b.WriteRune(r)
		}
	}
	for _, r := range t.R16 {
		emit(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	for _, r := range t.R32 {
		emit(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	return b.String()
}

func rx(p string) *regexp2.Regexp   { return regexp2.MustCompile(p, regexp2.Multiline) }
func rxm(p string) *regexp2.Regexp  { return regexp2.MustCompile(p, regexp2.Multiline|regexp2.Singleline) }
func rxi(p string) *regexp2.Regexp  { return regexp2.MustCompile(p, regexp2.Multiline|regexp2.IgnoreCase) }
func rxmi(p string) *regexp2.Regexp { return regexp2.MustCompile(p, regexp2.Multiline|regexp2.Singleline|regexp2.IgnoreCase) }

// md は Ruby の MatchData 相当。
type md struct{ m *regexp2.Match }

func (x md) s(i int) string {
	g := x.m.GroupByNumber(i)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return g.String()
}

func (x md) ok(i int) bool {
	g := x.m.GroupByNumber(i)
	return g != nil && len(g.Captures) > 0
}

// n は名前付きグループの値（未参加なら ok=false）。
func (x md) n(name string) (string, bool) {
	g := x.m.GroupByName(name)
	if g == nil || len(g.Captures) == 0 {
		return "", false
	}
	return g.String(), true
}

func (x md) ns(name string) string { s, _ := x.n(name); return s }

func (x md) all() string { return x.m.String() }

func match(re *regexp2.Regexp, s string) *md {
	m, err := re.FindStringMatch(s)
	if err != nil {
		panic(err)
	}
	if m == nil {
		return nil
	}
	return &md{m}
}

func matches(re *regexp2.Regexp, s string) bool {
	ok, err := re.MatchString(s)
	if err != nil {
		panic(err)
	}
	return ok
}

func gsub(re *regexp2.Regexp, s string, f func(m md) string) string {
	out, err := re.ReplaceFunc(s, func(m regexp2.Match) string { return f(md{&m}) }, -1, -1)
	if err != nil {
		panic(err)
	}
	return out
}

func scan(re *regexp2.Regexp, s string) []md {
	var res []md
	m, err := re.FindStringMatch(s)
	for err == nil && m != nil {
		res = append(res, md{m})
		m, err = re.FindNextMatch(m)
	}
	if err != nil {
		panic(err)
	}
	return res
}

// rubyStrip は String#strip（NUL と ASCII 空白を除去）。
func rubyStrip(s string) string { return strings.Trim(s, "\x00\t\n\v\f\r ") }

// blank は String#blank?。
func blank(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

var erbReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

// h は ERB::Util.html_escape / CGI.escapeHTML。
func h(s string) string { return erbReplacer.Replace(s) }
