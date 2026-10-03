// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

// ToHTML は Redmine::WikiFormatting.to_html(r.TextFormatting, text)（マクロ・Redmine リンクの解決をしない
// 書式変換のみ。添付ファイルの Markdown / Textile のプレビュー（common/_markup）や、メールの
// emails_header / emails_footer に使う）。
func (r *Renderer) ToHTML(text string) string { return r.toHTML(text) }
