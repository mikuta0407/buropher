// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package commonmark は Redmine 6.1.2 の CommonMark 整形
// （Redmine::WikiFormatting::CommonMark::Formatter）を移植したもの。
//
// Redmine は commonmarker（Rust の comrak 0.41）で Markdown を HTML にし、
// html-pipeline のフィルタ群（Sanitization → SyntaxHighlight → FixupAutoLinks →
// ExternalLinks → AlertsIcons）で後処理する。本パッケージは goldmark で構文解析し、
// comrak と同じ規則で HTML を生成したうえで、libxml2 互換 DOM（htmldom）上で
// 同じ順序のフィルタを適用する。
//
// Redmine リンク（#123, [[Wiki]] 等）とマクロは別レイヤ（parse_redmine_links 相当）で
// 処理するため、ここでは扱わない（テキストはそのまま出力される）。
package commonmark

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/text"

	"github.com/mikuta0407/buropher/internal/textformat/highlight"
	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
	"github.com/mikuta0407/buropher/internal/textformat/sanitize"
)

// Options は整形オプション。
type Options struct {
	// DisableHardBreaks が true の場合、単一改行を <br> にしない
	// （Redmine の common_mark_enable_hardbreaks: false 相当。既定は有効）。
	DisableHardBreaks bool
	// IconsPath はアイコンスプライト SVG の URL（asset_path('icons.svg') 相当）。
	// 空の場合は "/assets/icons.svg"。
	IconsPath string
	// Translate は I18n.t 相当（label_alert_note 等）。nil または空文字列を返すと既定値を使う。
	Translate func(key string) string
}

// Format は Markdown テキストを Redmine と同じ HTML に変換する。
func Format(src string, opts Options) string {
	html := MarkdownToHTML(src, !opts.DisableHardBreaks)
	frag := htmldom.ParseFragment(html)
	sanitize.Node(frag)
	SyntaxHighlightFilter(frag)
	FixupAutoLinksFilter(frag)
	sanitize.ExternalLinks(frag)
	AlertsIconsFilter(frag, opts)
	return htmldom.Render(frag)
}

// MarkdownToHTML は MarkdownFilter 相当（\r を除去し comrak 互換の HTML を生成、末尾空白を除去）。
func MarkdownToHTML(src string, hardbreaks bool) string {
	src = strings.ReplaceAll(src, "\r", "")
	b := []byte(src)
	// comrak と同様に NUL を U+FFFD に置き換える
	if strings.IndexByte(src, 0) >= 0 {
		b = []byte(strings.ReplaceAll(src, "\x00", "�"))
	}
	b = capContainerDepth(b)
	p := newParser()
	doc := p.Parse(text.NewReader(b))
	r := &renderer{src: b, hardbreaks: hardbreaks}
	r.render(doc)
	return strings.TrimRight(r.buf.String(), " \t\n\v\f\r\x00")
}

// maxContainerMarkers は 1 行の先頭に並べられるコンテナ（引用 ">"・リスト項目 "- " "1. " など）の印の数の上限。
const maxContainerMarkers = 100

// capContainerDepth は行頭のコンテナの印が maxContainerMarkers 個を超える行で、超えた最初の印の前に
// "\" を挿入してリテラルにする（それ以降の入れ子を作らせない）。
// goldmark はブロックの入れ子 1 段ごとに行頭からの桁位置（LineOffset）や水平線の判定で行の残りを
// 走査するため、"- " や ">" を 1 行に数万個並べると行の長さの 2 乗の時間がかかる
// （"- " * 20000 で約 3 秒、長くすると数分）。現実の文書に 100 段の入れ子は無いので出力は変わらない。
func capContainerDepth(b []byte) []byte {
	var out []byte // 書き換えが必要になるまで nil（元の b をそのまま返す）
	last := 0
	for lineStart := 0; lineStart < len(b); {
		lineEnd := bytes.IndexByte(b[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(b)
		} else {
			lineEnd += lineStart
		}
		if p := excessContainerMarker(b[lineStart:lineEnd]); p >= 0 {
			out = append(out, b[last:lineStart+p]...)
			out = append(out, '\\')
			last = lineStart + p
		}
		lineStart = lineEnd + 1
	}
	if out == nil {
		return b
	}
	return append(out, b[last:]...)
}

// excessContainerMarker は行頭の印を数え、maxContainerMarkers 個を超えた印をリテラルにするために
// "\" を挿入すべき位置を返す（超えなければ -1）。
func excessContainerMarker(line []byte) int {
	isBlank := func(i int) bool { return i >= len(line) || line[i] == ' ' || line[i] == '\t' }
	if len(line) <= maxContainerMarkers || isThematicBreakLine(line) {
		return -1
	}
	n := 0
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == ' ' || c == '\t':
			i++
			continue
		case c == '>':
			if n++; n > maxContainerMarkers {
				return i
			}
			i++
		case (c == '-' || c == '+' || c == '*') && isBlank(i+1):
			if n++; n > maxContainerMarkers {
				return i
			}
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(line) && j-i < 9 && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			if j >= len(line) || (line[j] != '.' && line[j] != ')') || !isBlank(j+1) {
				return -1
			}
			if n++; n > maxContainerMarkers {
				return j
			}
			i = j + 1
		default:
			return -1
		}
	}
	return -1
}

// isThematicBreakLine は行が空白と 1 種類の水平線の記号（- * _）だけでできているか
// （"- - - ..." を長く書いた水平線を書き換えないため）。
func isThematicBreakLine(line []byte) bool {
	var mark byte
	for _, c := range line {
		switch {
		case c == ' ' || c == '\t':
		case mark == 0 && (c == '-' || c == '*' || c == '_'):
			mark = c
		case c != mark:
			return false
		}
	}
	return mark != 0
}

var reLanguageClass = regexp.MustCompile(`\Alanguage-(\S+)\z`)

// SyntaxHighlightFilter は pre > code.language-xxx を Rouge 互換でハイライトする。
func SyntaxHighlightFilter(frag *htmldom.Node) {
	codes := frag.FindAll(func(n *htmldom.Node) bool {
		return n.IsElement("code") && n.Parent != nil && n.Parent.IsElement("pre")
	})
	for _, node := range codes {
		cls := node.AttrVal("class")
		if strings.TrimSpace(cls) == "" {
			continue
		}
		m := reLanguageClass.FindStringSubmatch(cls)
		if m == nil {
			continue
		}
		lang := m[1]
		txt := node.Text()
		if !node.HasAttr("data-language") {
			node.SetAttr("data-language", lang)
		}
		if highlight.LanguageSupported(lang) {
			nodes := highlight.Nodes(txt, lang)
			node.RemoveChildren()
			for _, c := range nodes {
				node.AppendChild(c)
			}
			node.SetAttr("class", lang+" syntaxhl")
		} else {
			node.RemoveAttr("class")
		}
	}
}

var (
	reUserLinkPrefix = regexp.MustCompile(`(@|user:)\z`)
	reHiresImage     = regexp.MustCompile(`.+@\dx\.(bmp|gif|jpg|jpe|jpeg|png)\z`)
)

// FixupAutoLinksFilter はユーザー参照や高解像度画像名の誤った mailto 自動リンクを元に戻す。
func FixupAutoLinksFilter(frag *htmldom.Node) {
	for _, a := range frag.FindAll(func(n *htmldom.Node) bool { return n.IsElement("a") }) {
		href, ok := a.GetAttr("href")
		if !ok || !strings.HasPrefix(href, "mailto:") {
			continue
		}
		p := a.PrevSibling
		if (p != nil && p.Type == htmldom.TextNode && reUserLinkPrefix.MatchString(p.Data)) ||
			reHiresImage.MatchString(a.Text()) {
			a.ReplaceWith(htmldom.NewText(a.Text()))
		}
	}
}

// alertIcons はアラート種別とアイコン名の対応（ALERT_TYPE_TO_ICON_NAME）。
var alertIcons = map[string]string{
	"note":      "help",
	"tip":       "bulb",
	"warning":   "warning",
	"caution":   "alert-circle",
	"important": "message-report",
}

var reAlertType = regexp.MustCompile(`markdown-alert-(\w+)`)

// AlertsIconsFilter はアラートのタイトルを翻訳し、アイコンを挿入する。
func AlertsIconsFilter(frag *htmldom.Node, opts Options) {
	titles := frag.FindAll(func(n *htmldom.Node) bool {
		return n.IsElement("p") && hasClass(n, "markdown-alert-title")
	})
	for _, node := range titles {
		parent := node.Parent
		if parent == nil || parent.Type != htmldom.ElementNode {
			continue
		}
		pc, ok := parent.GetAttr("class")
		if !ok {
			continue
		}
		m := reAlertType.FindStringSubmatch(pc)
		if m == nil {
			continue
		}
		alertType := m[1]
		icon, ok := alertIcons[alertType]
		if !ok {
			continue
		}
		if _, known := alertIcons[strings.ToLower(node.Text())]; known {
			label := ""
			if opts.Translate != nil {
				label = opts.Translate("label_alert_" + alertType)
			}
			if label == "" {
				label = alertDefaultTitle(alertType)
			}
			node.SetText(label)
		}
		first := node.FirstChild
		if first == nil {
			continue
		}
		iconsPath := opts.IconsPath
		if iconsPath == "" {
			iconsPath = "/assets/icons.svg"
		}
		svg := htmldom.NewElement("svg", htmldom.Attr{Name: "class", Value: "s18 icon-svg"}, htmldom.Attr{Name: "aria-hidden", Value: "true"})
		svg.AppendChild(htmldom.NewElement("use", htmldom.Attr{Name: "href", Value: iconsPath + "#icon--" + icon}))
		span := htmldom.NewElement("span", htmldom.Attr{Name: "class", Value: "icon-label"})
		span.AppendChild(htmldom.NewText(node.Text()))
		first.ReplaceWith(svg, span)
	}
}

// hasClass は CSS のクラスセレクタ相当の判定。
func hasClass(n *htmldom.Node, cls string) bool {
	for _, c := range strings.Fields(n.AttrVal("class")) {
		if c == cls {
			return true
		}
	}
	return false
}
