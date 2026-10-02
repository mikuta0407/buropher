package helper

// ニュース・文書・ファイル・フォーラムの画面で使うヘルパー
// （ReactionsHelper / BoardsHelper / WatchersHelper#watcher_link（種類の対応付き）/ ApplicationHelper の一部）。

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			// reaction_button(object)。kind は reactions.reactable_kind（news / comment / message / journal / issue）。
			"reaction_button": func(kind string, id int64) html { return d.reactionButton(pg(), kind, id) },
			// toggle_link(name, id, :focus => ..., :scroll => ...)
			"toggle_link": func(name any, id string, args ...any) html { return toggleLink(name, id, optHash(args)) },
			// link_to_if_authorized(name, {:controller, :action ...}, html_options)（URL は呼び出し側で組み立てる）
			"link_to_if_authorized": func(name any, ctrl, action, url string, args ...any) html {
				p := pg()
				if !p.AllowedTo(domain.ControllerAction(ctrl, action), p.Project) {
					return ""
				}
				return rails.LinkTo(name, url, optHash(args))
			},
			"link_to_message":           func(m *domain.Message, args ...any) html { return linkToMessage(m, optHash(args)) },
			"board_message_url":         boardMessageURL,
			"boards_options_for_select": boardsOptionsForSelect,
			// watcher_link(object, User.current)。objectType は object_type（news / board / message / enabled_module ...）。
			"content_watcher_link": func(objectType string, id int64) html { return d.contentWatcherLink(pg(), objectType, id) },
			"truncate_lines":       func(s string) string { return truncateLines(s, 250) },
			"project_tree_options_for_select": func(projects []*domain.Project, selected any) html {
				return projectTreeOptionsForSelect(pg(), projects, selected)
			},
			"quote_reply_button": func(url string, iconOnly bool) html { return d.quoteReplyButton(pg(), url, iconOnly) },
			"news_index_path":   func(project any) string { return newsIndexPath(toProject(project)) },
			"news_preview_path": newsPreviewPath,
			"news_atom_path": func(project any, key string) string {
				u := newsIndexPath(toProject(project)) + ".atom"
				if key != "" {
					u += "?key=" + url.QueryEscape(key)
				}
				return u
			},
			"news_text_object": func(n *domain.News) *redmine.Object {
				return &redmine.Object{Kind: "news", ID: n.ID, Project: n.Project}
			},
			"comment_text_area": func(name, method, value string, opts *rails.Hash) html {
				return commentTextArea(name, method, value, opts)
			},
			"user_allowed_to": func(perm string, project any) bool {
				p := pg()
				return p.AllowedTo(domain.Perm(perm), toProject(project))
			},
			"user_allowed_to_globally": func(perm string) bool {
				p := pg()
				a := p.authorizer()
				if a == nil {
					return p.admin()
				}
				ok, err := a.AllowedToGlobally(p.ctx(), domain.Perm(perm), nil)
				return err == nil && ok
			},
		}
	})
}

// newsIndexPath は project_news_index_path(project) / news_index_path。
func newsIndexPath(p *domain.Project) string {
	if p == nil {
		return "/news"
	}
	return "/projects/" + p.Identifier + "/news"
}

// newsPreviewPath は preview_news_path(:project_id => @project, :id => @news)。
func newsPreviewPath(project any, n *domain.News) string {
	q := url.Values{}
	if n != nil && n.ID != 0 {
		q.Set("id", strconv.FormatInt(n.ID, 10))
	}
	if p := toProject(project); p != nil {
		q.Set("project_id", p.Identifier)
	}
	if len(q) == 0 {
		return "/news/preview"
	}
	return "/news/preview?" + q.Encode()
}

// commentTextArea は text_area(object_name, method, options)（モデルなし。値は value）。
func commentTextArea(name, method, value string, opts *rails.Hash) html {
	o := rails.NewHash()
	if opts != nil {
		o.Update(opts)
	}
	o.Set("name", name+"["+method+"]")
	if _, ok := o.Lookup("id"); !ok {
		o.Set("id", name+"_"+method)
	}
	return rails.ContentTag("textarea", rails.H(value), o)
}

// ---------------------------------------------------------------- ReactionsHelper

// reactableClass は object.class.name（reactions_path の object_type）。
var reactableClass = map[string]string{
	"news": "News", "comment": "Comment", "message": "Message", "journal": "Journal", "issue": "Issue",
}

// reactionButton は ReactionsHelper#reaction_button(object)。オブジェクトは現在のプロジェクトに属し、
// 表示中の画面で見えている（visible?）前提。
func (d *Deps) reactionButton(p *Page, kind string, id int64) html {
	if !p.settingBool("reactions_enabled") || p.DB == nil {
		return ""
	}
	var cond string
	if a := p.authorizer(); a != nil {
		c, err := a.PrincipalVisibleCondition(p.ctx())
		if err != nil {
			p.logError("reaction_button", err)
			return ""
		}
		cond = c
	}
	var uid int64
	if p.logged() {
		uid = p.User.ID
	}
	m, err := repository.ReactionDetails(p.ctx(), p.DB, kind, []int64{id}, cond, uid)
	if err != nil {
		p.logError("reaction_button", err)
		return ""
	}
	det := m[id]
	if det == nil {
		det = &repository.ReactionDetail{}
	}
	count := det.Count()
	var names []string
	for i, u := range det.VisibleUsers {
		if i >= 10 {
			break
		}
		names = append(names, p.userName(u, ""))
	}
	var tooltip any
	if count > 0 {
		if others := count - len(names); others > 0 {
			names = append(names, p.l("reaction_text_x_other_users", map[string]any{"count": others}))
		}
		tooltip = toSentence(p, names)
	}
	var label any
	if count > 0 {
		label = count
	}
	cls := func(extra ...string) string {
		parts := append([]string{"icon", "reaction-button"}, extra...)
		if count > 0 {
			parts = append(parts, "has-reactions")
		}
		return strings.Join(parts, " ")
	}
	editable := p.logged() && p.Project != nil && p.Project.Active()
	class := reactableClass[kind]
	sid := strconv.FormatInt(id, 10)
	var inner html
	switch {
	case editable && det.UserReactionID != 0:
		inner = rails.LinkTo(d.spriteIcon(p, "thumb-up-filled", label, rails.NewHash("style", "filled")),
			"/reactions/"+strconv.FormatInt(det.UserReactionID, 10)+"?object_id="+sid+"&object_type="+class,
			rails.NewHash("remote", true, "method", "delete", "class", cls("reacted"), "title", tooltip))
	case editable:
		inner = rails.LinkTo(d.spriteIcon(p, "thumb-up", label, nil),
			"/reactions?object_id="+sid+"&object_type="+class,
			rails.NewHash("remote", true, "method", "post", "class", cls(), "title", tooltip))
	default:
		inner = rails.ContentTag("span", d.spriteIcon(p, "thumb-up", label, nil), rails.NewHash("class", cls("readonly"), "title", tooltip))
	}
	return rails.ContentTag("span", inner, rails.NewHash("class", "reaction-button-wrapper",
		"data", rails.NewHash("reaction-button-id", "reaction_"+kind+"_"+sid)))
}

// toSentence は Array#to_sentence(locale: I18n.locale)。
func toSentence(p *Page, words []string) string {
	conn := func(key, def string) string {
		k := "support.array." + key
		if s := p.l(k); s != "" && s != k && !strings.HasPrefix(s, "translation missing") {
			return s
		}
		return def
	}
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + conn("two_words_connector", " and ") + words[1]
	}
	return strings.Join(words[:len(words)-1], conn("words_connector", ", ")) + conn("last_word_connector", ", and ") + words[len(words)-1]
}

// ---------------------------------------------------------------- ApplicationHelper

// toggleLink は ApplicationHelper#toggle_link。
func toggleLink(name any, id string, opts *rails.Hash) html {
	onclick := "$('#" + id + "').toggle(); "
	focus := rails.ToS(opts.Get("focus"))
	if focus != "" {
		onclick += "$('#" + focus + ":visible').focus(); "
	} else {
		onclick += "this.blur(); "
	}
	if truthy(opts.Get("scroll")) {
		onclick += "$(window).scrollTop($('#" + focus + "').position().top); "
	}
	onclick += "return false;"
	return rails.LinkTo(name, "#", rails.NewHash("onclick", onclick))
}

// boardMessageURL は board_message_path(board_id, id, :r => r, :anchor => anchor)（link_to_message の URL）。
func boardMessageURL(m *domain.Message) string {
	u := "/boards/" + strconv.FormatInt(m.BoardID, 10) + "/topics/" + strconv.FormatInt(m.RootID(), 10)
	if m.ParentID != nil {
		u += "?r=" + strconv.FormatInt(m.ID, 10) + "#message-" + strconv.FormatInt(m.ID, 10)
	}
	return u
}

// linkToMessage は ApplicationHelper#link_to_message(message)。
func linkToMessage(m *domain.Message, htmlOpts *rails.Hash) html {
	if m == nil {
		return ""
	}
	return rails.LinkTo(rails.StringTruncate(m.Subject, 60, "...", nil), boardMessageURL(m), htmlOpts)
}

// truncateLines は ApplicationHelper#truncate_lines（250 文字を超える行の途中で切り「...」を付ける）。
func truncateLines(s string, length int) string {
	re := regexp.MustCompile(`(?s)\A(.{` + strconv.Itoa(length) + `}.*?)(?m:$)`)
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1] + "..."
	}
	return s
}

// projectTreeOptionsForSelect は ApplicationHelper#project_tree_options_for_select(projects, :selected => selected)。
func projectTreeOptionsForSelect(p *Page, projects []*domain.Project, selected any) html {
	if len(projects) == 0 || p.DB == nil {
		return ""
	}
	ns, err := repository.ProjectNestedSet(p.ctx(), p.DB)
	if err != nil {
		p.logError("project_tree", err)
		return ""
	}
	byID := map[int64]*domain.Project{}
	jps := make([]JumpProject, len(projects))
	for i, pr := range projects {
		v := ns[pr.ID]
		jps[i] = JumpProject{ID: pr.ID, Name: pr.Name, Identifier: pr.Identifier, Lft: v.Lft, Rgt: v.Rgt}
		byID[pr.ID] = pr
	}
	var selID int64
	if sp := toProject(selected); sp != nil {
		selID = sp.ID
	}
	var b strings.Builder
	ProjectTree(jps, func(jp JumpProject, level int) {
		prefix := ""
		if level > 0 {
			prefix = strings.Repeat("&nbsp;", 2*level) + "&#187; "
		}
		opts := rails.NewHash("value", jp.ID)
		if jp.ID == selID {
			opts.Set("selected", "selected")
		}
		b.WriteString(string(rails.ContentTag("option", html(prefix)+rails.H(jp.Name), opts)))
	})
	return html(b.String())
}

// ---------------------------------------------------------------- BoardsHelper

// boardsOptionsForSelect は BoardsHelper#boards_options_for_select（[ラベル, id] の配列）。
func boardsOptionsForSelect(boards []*domain.Board) []any {
	var out []any
	for _, bl := range domain.BoardTree(boards) {
		label := ""
		if bl.Level > 0 {
			label = strings.Repeat("&nbsp;", 2*bl.Level) + "&#187; "
		}
		out = append(out, []any{html(label) + rails.H(bl.Board.Name), bl.Board.ID})
	}
	return out
}

// ---------------------------------------------------------------- WatchersHelper

// watchableKind は object_type（クラス名の underscore）→ watchers.watchable_kind。
func watchableKind(objectType string) string {
	if objectType == "enabled_module" {
		return "project_module"
	}
	return objectType
}

// contentWatcherLink は WatchersHelper#watcher_link(object, User.current)（object_type と DB の種類の対応付き）。
func (d *Deps) contentWatcherLink(p *Page, objectType string, id int64) html {
	if !p.logged() || p.DB == nil {
		return ""
	}
	watched, err := repository.WatchedBy(p.ctx(), p.DB, watchableKind(objectType), id, p.User.ID)
	if err != nil {
		p.logError("watcher_link", err)
		return ""
	}
	css := objectType + "-" + strconv.FormatInt(id, 10) + "-watcher"
	icon, text, method := "watch", p.l("button_watch"), "post"
	if watched {
		css += " icon icon-fav"
		icon, text, method = "unwatch", p.l("button_unwatch"), "delete"
	} else {
		css += " icon icon-fav-off"
	}
	url := "/watchers/watch?object_id=" + strconv.FormatInt(id, 10) + "&object_type=" + objectType
	return rails.LinkTo(d.spriteIcon(p, icon, text, nil), url, rails.NewHash("remote", true, "method", method, "class", css))
}

// ---------------------------------------------------------------- Redmine::QuoteReply::Helper

// quoteReplyButton は quote_reply_button(url:, icon_only:)。
func (d *Deps) quoteReplyButton(p *Page, url string, iconOnly bool) html {
	label := p.l("button_quote")
	cls := "icon icon-quote"
	if iconOnly {
		cls = "icon-only icon-quote"
	}
	opts := rails.NewHash("data", rails.NewHash("action", "quote-reply#quote", "quote_reply_url_param", url,
		"quote_reply_text_formatting_param", p.setting("text_formatting")), "class", cls)
	if iconOnly {
		opts.Set("title", label)
	}
	return rails.LinkTo(d.spriteIcon(p, "quote-filled", label, rails.NewHash("icon_only", iconOnly, "style", "filled")), "#", opts)
}

var _ = authz.ConditionOptions{}
