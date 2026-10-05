// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package scrubber は Redmine 7.0 で Textile と CommonMark が共用する Loofah スクラバの移植。
//
//   - Redmine::WikiFormatting::CopypreScrubber
//   - Redmine::WikiFormatting::TablesortScrubber
//   - Redmine::WikiFormatting::InlineAttachmentsScrubber
//   - Redmine::WikiFormatting::HiresImagesScrubber
//   - Redmine::WikiFormatting::SyntaxHighlight#process
//
// 各関数は Loofah::Scrubber#scrub に相当し、Loofah::Scrubber::STOP を返す場合は true を返す。
package scrubber

import (
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// Options はスクラバの設定。
type Options struct {
	// IconsPath はアイコンスプライト SVG の URL（asset_path('icons.svg') 相当）。空なら "/assets/icons.svg"。
	IconsPath string
	// Translate は I18n.t 相当（button_copy 等）。nil または空文字列なら英語の既定値を使う。
	Translate func(key string) string
	// TablesortEnabled は Setting.wiki_tablesort_enabled?。
	TablesortEnabled bool
	// FindAttachment は本文に添付された画像を探す（InlineAttachmentsScrubber の find_attachment）。
	// 見つかればダウンロード URL と説明を返す。nil なら何もしない。
	FindAttachment func(filename string) (url, description string, ok bool)
}

func (o *Options) iconsPath() string {
	if o == nil || o.IconsPath == "" {
		return "/assets/icons.svg"
	}
	return o.IconsPath
}

func (o *Options) t(key, def string) string {
	if o != nil && o.Translate != nil {
		if s := o.Translate(key); s != "" {
			return s
		}
	}
	return def
}

// SpriteIcon は sprite_icon(icon, size: 18) の svg 要素（ラベルなし）を作る。
func SpriteIcon(iconsPath, icon string) *htmldom.Node {
	svg := htmldom.NewElement("svg", htmldom.Attr{Name: "class", Value: "s18 icon-svg"}, htmldom.Attr{Name: "aria-hidden", Value: "true"})
	svg.Namespace = "svg"
	use := htmldom.NewElement("use", htmldom.Attr{Name: "href", Value: iconsPath + "#icon--" + icon})
	use.Namespace = "svg"
	svg.AppendChild(use)
	return svg
}

// CopyPre は CopypreScrubber（pre をコピー用ボタン付きの div.pre-wrapper で包む）。
func CopyPre(n *htmldom.Node, o *Options) {
	if !n.IsElement("pre") {
		return
	}
	n.SetAttr("data-clipboard-target", "pre")
	wrapper := htmldom.NewElement("div",
		htmldom.Attr{Name: "class", Value: "pre-wrapper"},
		htmldom.Attr{Name: "data-controller", Value: "clipboard"})
	if n.Parent != nil {
		n.InsertBefore(wrapper)
	}
	wrapper.AppendChild(n)
	button := htmldom.NewElement("a",
		htmldom.Attr{Name: "class", Value: "copy-pre-content-link icon-only"},
		htmldom.Attr{Name: "title", Value: o.t("button_copy", "Copy")},
		htmldom.Attr{Name: "data-action", Value: "clipboard#copyPre"})
	button.AppendChild(SpriteIcon(o.iconsPath(), "copy-pre-content"))
	n.InsertBefore(button)
}

// Tablesort は TablesortScrubber（見出し行を持つ 3 行以上の表を並べ替え可能にする）。
func Tablesort(n *htmldom.Node, o *Options) {
	if o == nil || !o.TablesortEnabled || !n.IsElement("table") {
		return
	}
	rows := n.FindAll(func(c *htmldom.Node) bool { return c.IsElement("tr") })
	if len(rows) < 3 {
		return
	}
	tr := rows[0]
	if len(tr.FindAll(func(c *htmldom.Node) bool { return c.IsElement("th") })) == 0 {
		return
	}
	n.SetAttr("data-controller", "tablesort")
	tr.SetAttr("data-sort-method", "none")
	for _, td := range tr.FindAll(func(c *htmldom.Node) bool { return c.IsElement("td") }) {
		td.SetAttr("data-sort-method", "none")
	}
}

var reInlineImage = regexp.MustCompile(`(?is)\A([^/"]+?\.(?:avif|bmp|gif|jpg|jpeg|jpe|png|webp))\z`)

// InlineAttachments は InlineAttachmentsScrubber（添付ファイル名の画像をダウンロード URL に置き換える）。
func InlineAttachments(n *htmldom.Node, o *Options) {
	if o == nil || o.FindAttachment == nil || !n.IsElement("img") {
		return
	}
	src := n.AttrVal("src")
	if blank(src) {
		return
	}
	m := reInlineImage.FindStringSubmatch(src)
	if m == nil {
		return
	}
	url, desc, ok := o.FindAttachment(m[1])
	if !ok {
		return
	}
	n.SetAttr("src", url)
	desc = strings.ReplaceAll(desc, `"`, "")
	if !blank(desc) && blank(n.AttrVal("alt")) {
		n.SetAttr("title", desc)
		n.SetAttr("alt", desc)
	}
	n.SetAttr("loading", "lazy")
}

var reHiresFilename = regexp.MustCompile(`(?i)@(\dx)\.(?:bmp|gif|jpg|jpe|jpeg|png)\z`)

// HiresImages は HiresImagesScrubber（name@2x.png のような画像に srcset を付ける）。
func HiresImages(n *htmldom.Node) {
	if !n.IsElement("img") {
		return
	}
	src := n.AttrVal("src")
	if blank(src) || !strings.Contains(src, "@") {
		return
	}
	m := reHiresFilename.FindStringSubmatch(src)
	if m == nil {
		return
	}
	n.SetAttr("srcset", src+" "+m[1])
}

// blank は Ruby の blank?（空または空白のみ）。
func blank(s string) bool {
	return strings.TrimLeft(s, " \t\n\v\f\r                 　") == ""
}

// Run は scrubbers を Loofah のスクラバ連鎖として断片に適用する
// （各ノードに順に適用し、STOP を返したら残りと子孫を飛ばし、ノードが外されたら残りを飛ばす）。
func Run(frag *htmldom.Node, scrubbers ...func(*htmldom.Node) bool) {
	frag.ScrubTopDown(func(n *htmldom.Node) bool {
		for _, s := range scrubbers {
			if s(n) {
				return true
			}
			if n.Parent == nil {
				return false
			}
		}
		return false
	})
}
