package redmine

import "html/template"

// ToHTML は Redmine::WikiFormatting.to_html(Setting.text_formatting, text)（整形のみ。Redmine リンク・
// マクロ・添付の解決は行わない。メールの emails_header / emails_footer に使う）。
func (r *Renderer) ToHTML(text string) template.HTML {
	return template.HTML(r.toHTML(text))
}
