package helper

// このファイルはガントチャート（handler の gantts.go / Redmine::Helpers::Gantt）がハンドラ側で
// HTML を組み立てるときに使う、AvatarsHelper の関数の公開版。

import (
	"html/template"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Avatar は avatar(principal, options)（グループは group_avatar）。テンプレートの外から使う。
func (d *Deps) Avatar(p *Page, u *domain.User, opts *rails.Hash) template.HTML {
	if u == nil {
		return ""
	}
	rv := rails.NewView("")
	rv.AssetPath = func(_, source string) string { return d.assetPath(source) }
	r := &view.Render{Ctx: &view.Context{}, Rails: rv}
	if u.Kind.IsGroup() {
		// group_avatar: GravatarHelper::DEFAULT_OPTIONS.except(:default, :rating, :ssl).merge(options)
		o := opts.Clone()
		cls := "avatar"
		if c := o.Get("class"); c != nil {
			cls += " " + rails.ToS(c)
		}
		o.Set("class", "group-avatar "+cls)
		return d.imageTag(r, p, "group.png", rails.NewHash("size", 24, "alt", "", "title", "", "class", "gravatar").Update(o))
	}
	return d.avatar(r, p, u, opts)
}
