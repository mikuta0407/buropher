// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package textile は Redmine 7.0 の Textile フォーマッタ
// (Redmine::WikiFormatting::Textile::Formatter = RedCloth3 + Redmine の拡張 + Loofah スクラバ) の移植である。
//
// Format が返す HTML は Redmine の Formatter#to_html と同一 (バイト単位) になることを目標にしている。
// Redmine 7.0 (#43643) は RedCloth3 の出力を Loofah.html5_fragment (Nokogiri の HTML5 パーサ) で解析し、
// スクラバ (Copypre → SyntaxHighlight → Tablesort → InlineAttachments → HiresImages) を適用してから
// 直列化する。
// Redmine リンク ([[Wiki]], #123 など) やマクロ ({{toc}} など) の展開は
// 呼び出し側 (textilizable 相当) の責務であり、本パッケージでは行わない。
package textile

import (
	"strings"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
	"github.com/mikuta0407/buropher/internal/textformat/scrubber"
)

// Highlighter はシンタックスハイライタ。言語 lang がサポートされていなければ ok=false を返す。
// サポートされていれば code をハイライトした HTML を返す
// (Redmine::SyntaxHighlighting.language_supported? / highlight_by_language 相当)。
type Highlighter func(lang, code string) (html string, ok bool)

// Options は Format のオプション。
type Options struct {
	// Highlight は <code class="lang"> 内のコードのハイライタ。nil なら全言語を非サポート扱いにする。
	Highlight Highlighter
	// Scrub は共用スクラバ (コピー用ボタン・表の並べ替え・添付画像) の設定。
	Scrub scrubber.Options
}

// Format は Textile ソースを HTML に変換する (Formatter.new(src).to_html 相当)。
// HTML5 パーサの上限 (木の深さ 400 等) を超えた場合はエスケープしたテキストを返す (FormatE を参照)。
func Format(src string, opts *Options) string {
	out, err := FormatE(src, opts)
	if err != nil {
		return htmldom.EscapeHTML5Text(src)
	}
	return out
}

// FormatE は Format と同じだが、HTML5 パーサの上限を超えた場合にエラーを返す
// (Redmine では Nokogiri が ArgumentError を送出し、ページ全体がエラーになる)。
func FormatE(src string, opts *Options) (string, error) {
	// 不正な UTF-8 は U+FFFD にする（extractSections と同じ理由）
	src = strings.ToValidUTF8(src, "�")
	rc := newFormatter(opts)
	frag, err := htmldom.ParseHTML5Fragment(rc.toHTML(src))
	if err != nil {
		return "", err
	}
	var so *scrubber.Options
	if opts != nil {
		so = &opts.Scrub
	}
	scrubber.Run(frag,
		func(n *htmldom.Node) bool { scrubber.CopyPre(n, so); return false },
		func(n *htmldom.Node) bool { return rc.syntaxHighlight(n) },
		func(n *htmldom.Node) bool { scrubber.Tablesort(n, so); return false },
		func(n *htmldom.Node) bool { scrubber.InlineAttachments(n, so); return false },
		func(n *htmldom.Node) bool { scrubber.HiresImages(n); return false },
	)
	return htmldom.RenderHTML5(frag), nil
}

// syntaxHighlight は Textile::SyntaxHighlightScrubber (<pre><code class="foo"> をハイライトする)。
func (rc *redcloth) syntaxHighlight(node *htmldom.Node) bool {
	if !node.IsElement("code") {
		return false
	}
	lang := node.AttrVal("class")
	if blank(lang) {
		return false
	}
	text := node.Text()
	text = strings.TrimPrefix(text, "\n")
	// Redmine::WikiFormatting::SyntaxHighlight#process
	if !node.HasAttr("data-language") {
		node.SetAttr("data-language", lang)
	}
	if html, ok := rc.highlightCode(lang, text); ok {
		if err := node.SetInnerHTML5(html); err == nil {
			node.SetAttr("class", lang+" syntaxhl")
		}
	} else {
		node.RemoveAttr("class")
		// 多重防御: 非対応の言語では子をエスケープしたテキストに置き換える
		node.SetText(text)
	}
	return true
}

func newFormatter(opts *Options) *redcloth {
	// Formatter#initialize (formatter.rb:35-40)
	rc := &redcloth{
		hardBreaks:   true,
		filterStyles: false,
	}
	if opts != nil {
		rc.highlight = opts.Highlight
	}
	return rc
}

type blockRule func(rc *redcloth, text string) (string, bool)
type inlineRule func(rc *redcloth, text string) string

// Formatter::RULES = [:textile, :block_markdown_rule, :inline_auto_link, :inline_auto_mailto,
// :inline_restore_redmine_links] を展開したもののうち block_ で始まるもの (formatter.rb:33)
var blockRules = []blockRule{
	(*redcloth).blockTextileTable,
	(*redcloth).blockTextileLists,
	(*redcloth).blockTextilePrefix,
	(*redcloth).blockMarkdownRule,
}

// 同 inline_ で始まるもの (この後に glyphs_textile が続く)
var inlineRules = []inlineRule{
	(*redcloth).inlineTextileImage,
	(*redcloth).inlineTextileCode,
	(*redcloth).inlineTextileSpan,
	(*redcloth).inlineTextileLink,
	func(_ *redcloth, t string) string { return autoLink(t) },
	func(_ *redcloth, t string) string { return autoMailto(t) },
	func(_ *redcloth, t string) string { return restoreRedmineLinks(t) },
}

var reNotextileTag = rx(`</?notextile>|<(?=</?notextile>)`)

// userRedshMarker は利用者の入力中の ":redsh#"（shelve の目印）を retrieve から隠すための置き換え。
const userRedshMarker = ":redsh\uFFFF#"

// toHTML は RedCloth3#to_html (redcloth3.rb:269-320) に Formatter#to_html の規則を与えたもの。
func (rc *redcloth) toHTML(src string) string {
	text := strings.ToValidUTF8(src, "�")
	rc.shelf = nil
	// 利用者が書いた ":redsh#N:" は shelve の目印と同じ形のため、retrieve の対象にならない形にしておき、
	// retrieve の後で元に戻す（retrieve が展開するのは shelve が置いた目印だけ）。
	text = strings.ReplaceAll(text, ":redsh#", userRedshMarker)

	// 標準的なクリーンアップ
	text = incomingEntities(text)
	text = cleanWhiteSpace(text)

	// 処理開始
	rc.preList = nil
	text = rc.ripOfftags(text, true, true)
	text = noTextile(text)
	text = removeHTMLComments(text)
	text = escapeHTMLTags(text)
	// hard_break と blocks より前に行う必要がある
	text = rc.blockTextileQuotes(text)
	text = rc.hardBreak(text)
	// refs: Formatter の規則には refs_ が含まれないので何もしない
	text = rc.blocks(text, false)
	text = rc.inline(text)
	text = rc.smoothOfftagsPlain(text)

	text = rc.retrieve(text)
	text = strings.ReplaceAll(text, userRedshMarker, ":redsh#")

	// <notextile> を取り除く。直前の "<" は &lt; にする（"<<notextile>/notextile>script>" のように
	// 取り除いた後に新しいタグができるのを防ぐ。Redmine 7.0.1 #44308 "Frankenstein tag"）
	text = gsub(reNotextileTag, text, func(m md) string {
		if m.all() == "<" {
			return "&lt;"
		}
		return ""
	})
	text = strings.ReplaceAll(text, "x%x%", "&#38;")
	return rubyStrip(text)
}

// inline (redcloth3.rb:1131-1137)
func (rc *redcloth) inline(text string) string {
	for _, r := range inlineRules {
		text = r(rc, text)
	}
	return rc.glyphsTextile(text, 0)
}

var reHardBreak = rx(`(.)\n(?!\n|\Z| *([#*=]+(` + reS + `|$)|[{|]))`)

// hard_break (formatter.rb:99-101): RedCloth r128 のパッチ版
func (rc *redcloth) hardBreak(text string) string {
	if !rc.hardBreaks {
		return text
	}
	return gsub(reHardBreak, text, func(m md) string { return m.s(1) + "<br />" })
}

func (rc *redcloth) highlightCode(lang, code string) (string, bool) {
	if rc.highlight == nil {
		return "", false
	}
	return rc.highlight(lang, code)
}
