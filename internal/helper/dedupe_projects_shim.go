package helper

import "html/template"

// このファイルは projects ブランチ（helper/projects.go）にある関数の一時的な複製。
// TODO(dedupe): projects ブランチが master に入ったらこのファイルを削除する。

// SpriteIconHTML は sprite_icon(name, label)（ハンドラで HTML を組み立てる場合に使う）。
func (d *Deps) SpriteIconHTML(p *Page, name string, label any) template.HTML {
	return d.spriteIcon(p, name, label, nil)
}
