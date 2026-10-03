// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

// Ruby (Onigmo) の正規表現・文字列操作の意味論を Go で再現するための補助関数群。
//
// RedCloth3 は Ruby の正規表現 (後読み・後方参照・行アンカー) に強く依存しているため、
// .NET 互換エンジンの github.com/dlclark/regexp2 を用いる。Ruby と .NET の差異は
// 以下のように吸収する:
//
//   - Ruby の ^ / $ は常に行アンカー → 全パターンに Multiline を付ける
//   - Ruby の /m (ドットが改行にマッチ) → Singleline
//   - Ruby の \w \s \d は ASCII のみ → 明示的な ASCII 文字クラスに書き換える
//   - Ruby の \b は Unicode の単語文字 (L/M/N/Pc) 基準 → 後読み・先読みで自前実装
//   - POSIX ブラケット ([[:word:]] 等) は Unicode 版の Onigmo 定義に合わせて展開

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/dlclark/regexp2"
)

// MatchTimeout は本パッケージ（と redmine パッケージ）の正規表現 1 回の照合の上限時間。
// RedCloth 由来の正規表現には遅延量指定子（.+? 等）が多く、閉じ記号の無い入力では
// 入力長の 2 乗の時間がかかる（例: "\n@a" を数千回繰り返した本文で数十秒）。
// 利用者が書いた本文で CPU を占有されないよう、照合ごとに上限を設ける。
const MatchTimeout = time.Second

// ErrMatchTimeout は照合が MatchTimeout を超えたときに補助関数が panic する値。
// textilizable 側で recover して装飾なしのテキストに切り替える。
var ErrMatchTimeout = errors.New("textformat: regexp match timeout")

// compile は MatchTimeout 付きで正規表現をコンパイルする。
func compile(p string, opts regexp2.RegexOptions) *regexp2.Regexp {
	re := regexp2.MustCompile(p, opts)
	re.MatchTimeout = MatchTimeout
	return re
}

// rxFail は照合エラー（regexp2 では時間切れのみ）で panic する。
// regexp2 のエラーには入力全体が含まれるため、短い番兵値に置き換える。
func rxFail() {
	panic(ErrMatchTimeout)
}

// 文字クラス断片 (ブラケットの内側に埋め込む用)
const (
	// Ruby \s (ASCII 空白)
	spIn = ` \t\n\v\f\r`
	// Ruby \w (ASCII 単語文字)
	wIn = `a-zA-Z0-9_`
)

// ブラケット付きの文字クラス
const (
	reS  = `[` + spIn + `]`
	reNS = `[^` + spIn + `]`
	reW  = `[` + wIn + `]`
	reNW = `[^` + wIn + `]`
)

var (
	// Onigmo の Other_Alphabetic 相当 (Alphabetic = L + Nl + Other_Alphabetic)
	otherAlphaIn = rangeTableClass(unicode.Other_Alphabetic)
	// [[:word:]] / \p{Word}: Alphabetic | Mark | Decimal_Number | Connector_Punctuation
	wordIn = `\p{L}\p{Nl}\p{M}\p{Nd}\p{Pc}` + otherAlphaIn
	// [[:alnum:]]: Alphabetic | Decimal_Number
	alnumIn = `\p{L}\p{Nl}\p{Nd}` + otherAlphaIn
	// [[:punct:]]: Punctuation + ASCII の記号 9 文字 (Ruby 2.4+)
	punctIn = `\p{P}\$\+<=>\^` + "`" + `\|~`
	// \b 判定用の単語文字 (Onigmo の \b は Number 全体も単語扱い)
	bWordCls = `[\p{L}\p{M}\p{N}\p{Pc}]`
	// Ruby の \b 相当
	reB = `(?:(?<=` + bWordCls + `)(?!` + bWordCls + `)|(?<!` + bWordCls + `)(?=` + bWordCls + `))`
)

// rangeTableClass は unicode.RangeTable を正規表現の文字クラス内側表現に変換する。
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

// rx は Ruby の正規表現 (オプションなし) 相当をコンパイルする。
func rx(p string) *regexp2.Regexp {
	return compile(p, regexp2.Multiline)
}

// rxm は Ruby の /m 付き正規表現相当をコンパイルする。
func rxm(p string) *regexp2.Regexp {
	return compile(p, regexp2.Multiline|regexp2.Singleline)
}

// rxi は Ruby の /i 付き正規表現相当をコンパイルする。
func rxi(p string) *regexp2.Regexp {
	return compile(p, regexp2.Multiline|regexp2.IgnoreCase)
}

// rxmi は Ruby の /mi 付き正規表現相当をコンパイルする。
func rxmi(p string) *regexp2.Regexp {
	return compile(p, regexp2.Multiline|regexp2.Singleline|regexp2.IgnoreCase)
}

// md は Ruby の MatchData 相当の薄いラッパ。
type md struct {
	m *regexp2.Match
}

// s は i 番目のグループ文字列を返す (未マッチなら空文字列)。
func (x md) s(i int) string {
	g := x.m.GroupByNumber(i)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return g.String()
}

// ok は i 番目のグループがマッチに参加したかを返す (Ruby で nil でないか)。
func (x md) ok(i int) bool {
	g := x.m.GroupByNumber(i)
	return g != nil && len(g.Captures) > 0
}

// opt は i 番目のグループを *string で返す (未参加なら nil)。
func (x md) opt(i int) *string {
	if !x.ok(i) {
		return nil
	}
	s := x.s(i)
	return &s
}

// all はマッチ全体 ($&) を返す。
func (x md) all() string {
	return x.m.String()
}

// match は Ruby の =~ 相当。マッチしなければ nil。
func match(re *regexp2.Regexp, s string) *md {
	m, err := re.FindStringMatch(s)
	if err != nil {
		rxFail()
	}
	if m == nil {
		return nil
	}
	return &md{m}
}

// matches は Ruby の match? 相当。
func matches(re *regexp2.Regexp, s string) bool {
	ok, err := re.MatchString(s)
	if err != nil {
		rxFail()
	}
	return ok
}

// gsub は Ruby の gsub (ブロック付き) 相当。
func gsub(re *regexp2.Regexp, s string, f func(m md) string) string {
	out, err := re.ReplaceFunc(s, func(m regexp2.Match) string { return f(md{&m}) }, -1, -1)
	if err != nil {
		rxFail()
	}
	return out
}

// gsubB は Ruby の gsub! の戻り値 (置換が起きたか) も返す版。
func gsubB(re *regexp2.Regexp, s string, f func(m md) string) (string, bool) {
	changed := false
	out := gsub(re, s, func(m md) string {
		changed = true
		return f(m)
	})
	return out, changed
}

// sub は Ruby の sub (最初の 1 件のみ置換) 相当。置換が起きたかも返す。
func sub(re *regexp2.Regexp, s string, f func(m md) string) (string, bool) {
	changed := false
	out, err := re.ReplaceFunc(s, func(m regexp2.Match) string {
		changed = true
		return f(md{&m})
	}, -1, 1)
	if err != nil {
		rxFail()
	}
	return out, changed
}

// scan は Ruby の String#scan 相当 (各マッチを返す)。
func scan(re *regexp2.Regexp, s string) []md {
	var res []md
	m, err := re.FindStringMatch(s)
	for err == nil && m != nil {
		res = append(res, md{m})
		m, err = re.FindNextMatch(m)
	}
	if err != nil {
		rxFail()
	}
	return res
}

// splitRe は Ruby の String#split(regexp) 相当 (キャプチャなし・limit なし)。
// 末尾の空要素は取り除かれ、空文字列は空配列になる。
func splitRe(re *regexp2.Regexp, s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var parts []string
	prev := 0
	m, err := re.FindStringMatch(s)
	for err == nil && m != nil {
		if m.Length == 0 {
			// 空マッチ (本パッケージでは使わない) は位置 0 以外で 1 文字分割
			if m.Index == 0 || m.Index >= len(runes) {
				m, err = re.FindNextMatch(m)
				continue
			}
		}
		parts = append(parts, string(runes[prev:m.Index]))
		prev = m.Index + m.Length
		m, err = re.FindNextMatch(m)
	}
	if err != nil {
		rxFail()
	}
	parts = append(parts, string(runes[prev:]))
	return trimTrailingEmpty(parts)
}

// splitStr は Ruby の String#split(string) 相当 (区切りは " " 以外を想定)。
func splitStr(s, sep string) []string {
	if s == "" {
		return nil
	}
	return trimTrailingEmpty(strings.Split(s, sep))
}

func trimTrailingEmpty(parts []string) []string {
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// eachLine は Ruby の String#each_line 相当 (改行を含めて行に分ける)。
func eachLine(s string) []string {
	var lines []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// rubyStrip は Ruby の String#strip 相当 (NUL と ASCII 空白を除去)。
func rubyStrip(s string) string {
	return strings.Trim(s, "\x00\t\n\v\f\r ")
}

// blank は ActiveSupport の String#blank? 相当。
func blank(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// htmlEscapeERB は ERB::Util.html_escape / CGI.escapeHTML 相当。
func htmlEscapeERB(s string) string {
	return erbReplacer.Replace(s)
}

var erbReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)
