package helper

// このファイルは VersionsController（handler/versions*.go）が使う ApplicationHelper の公開版。

import "html/template"

// VersionTextilizable は textilizable(text)（バージョンの wiki ページの本文）。
func (d *Deps) VersionTextilizable(p *Page, text string) template.HTML { return d.textilizable(p, text) }
