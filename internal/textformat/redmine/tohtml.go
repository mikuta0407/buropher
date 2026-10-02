// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package redmine

// ToHTML は Redmine::WikiFormatting.to_html(r.TextFormatting, text)（マクロ・Redmine リンクの解決をしない
// 書式変換のみ。添付ファイルの Markdown / Textile のプレビュー（common/_markup）に使う）。
func (r *Renderer) ToHTML(text string) string { return r.toHTML(text) }
