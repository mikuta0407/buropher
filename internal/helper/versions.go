package helper

// このファイルは VersionsController（handler/versions*.go）が使う ApplicationHelper の公開版。

import (
	"html/template"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// VersionTextilizable は textilizable(text)（バージョンの wiki ページの本文）。
func (d *Deps) VersionTextilizable(p *Page, text string, obj TextObject) template.HTML {
	return d.textilizable(p, text, rails.NewHash("object", obj))
}
