// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

// ToHTML は Redmine::WikiFormatting.to_html(r.TextFormatting, text)（マクロ・Redmine リンクの解決をしない
// 書式変換のみ。添付ファイルの Markdown / Textile のプレビュー（common/_markup）や、メールの
// emails_header / emails_footer に使う）。
// 正規表現の照合時間切れでは装飾なしのテキストを返す。
func (r *Renderer) ToHTML(text string) (out string) {
	defer func() {
		// recover は defer された関数から直接呼ぶ必要がある
		if rec := recover(); rec != nil {
			out = string(fallbackOnMatchTimeout(rec, text))
		}
	}()
	return r.toHTML(text)
}
