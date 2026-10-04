// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package textile は Redmine 6.1.2 の Textile フォーマッタ
// (Redmine::WikiFormatting::Textile::Formatter = RedCloth3 + Redmine の拡張) の移植である。
//
// Format が返す HTML は Redmine の Formatter#to_html と同一 (バイト単位) になることを目標にしている。
// Redmine リンク ([[Wiki]], #123 など) やマクロ ({{toc}} など) の展開、
// サニタイズは呼び出し側 (textilizable 相当) の責務であり、本パッケージでは行わない。
package textile

import (
	"strconv"
	"strings"
)

// Highlighter はシンタックスハイライタ。言語 lang がサポートされていなければ ok=false を返す。
// サポートされていれば code をハイライトした HTML を返す
// (Redmine::SyntaxHighlighting.language_supported? / highlight_by_language 相当)。
type Highlighter func(lang, code string) (html string, ok bool)

// Options は Format のオプション。
type Options struct {
	// Highlight は <code class="lang"> 内のコードのハイライタ。nil なら全言語を非サポート扱いにする。
	Highlight Highlighter
}

// Format は Textile ソースを HTML に変換する (Formatter.new(src).to_html 相当)。
func Format(src string, opts *Options) string {
	// 不正な UTF-8 は U+FFFD にする（extractSections と同じ理由）
	src = strings.ToValidUTF8(src, "�")
	rc := newFormatter(opts)
	return rc.toHTML(src)
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

var reNotextileTag = rx(`</?notextile>`)

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
	text = rc.smoothOfftags(text)

	text = rc.retrieve(text)
	text = strings.ReplaceAll(text, userRedshMarker, ":redsh#")

	text = gsub(reNotextileTag, text, func(md) string { return "" })
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

var reCodeClass = rxm(`<code` + reS + `+class=(?:"([^"]+)"|'([^']+)')>` + reS + `?(.*)`)

// smooth_offtags (formatter.rb:103-127): コードハイライト対応版
func (rc *redcloth) smoothOfftags(text string) string {
	if len(rc.preList) == 0 {
		return text
	}
	return gsub(reRedpre, text, func(m md) string {
		n, err := strconv.Atoi(m.s(1))
		if err != nil || n >= len(rc.preList) {
			return ""
		}
		content := rc.preList[n]
		// この正規表現は rip_offtags が生成するデータにマッチしなければならない
		if cm := match(reCodeClass, content); cm != nil {
			language := cm.s(1)
			if !cm.ok(1) {
				language = cm.s(2)
			}
			code := cm.s(3)
			// 拡張開発向けに元の言語名を残す
			langattr := ""
			if !blank(language) {
				langattr = ` data-language="` + htmlEscapeERB(language) + `"`
			}
			if html, ok := rc.highlightCode(language, strings.ReplaceAll(code, "x%x%", "&")); ok {
				content = `<code class="` + htmlEscapeERB(language) + ` syntaxhl"` + langattr + `>` + html
			} else {
				content = "<code" + langattr + ">" + htmlEscapeERB(code)
			}
		}
		return content
	})
}

func (rc *redcloth) highlightCode(lang, code string) (string, bool) {
	if rc.highlight == nil {
		return "", false
	}
	return rc.highlight(lang, code)
}
