// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"
	ttemplate "text/template"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/mimetype"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/scm"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは RepositoriesHelper（app/helpers/repositories_helper.rb）と、リポジトリの画面で使う
// ApplicationHelper#link_to_revision / to_path_param / format_changeset_comments、IconsHelper#file_icon /
// scm_change_icon、およびリポジトリのルート（config/routes.rb）の URL 生成。

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			// repo_url "action" project repository path rev [key value ...]（url_for(:controller => 'repositories', ...)）
			"repo_url": func(action string, project *domain.Project, repo *domain.Repository, path, rev string, kv ...any) string {
				return RepositoryURL(project, repo, action, path, rev, queryPairs(kv)...)
			},
			// link_to_revision(revision, repository, :text => ..., :accesskey => ...)
			"link_to_revision": func(rev any, project *domain.Project, repo *domain.Repository, args ...any) html {
				return LinkToRevision(pg(), rev, project, repo, optHash(args))
			},
			"format_revision": func(rev any) string { return FormatRevision(rev) },
			"to_path_param":   func(p string) string { return ToPathParam(p) },
			// str_truncate は String#truncate(n)。
			"str_truncate": func(s string, n int) string { return rails.StringTruncate(s, n, "...", nil) },
			// repository_breadcrumbs は repositories/_breadcrumbs のリンク列（separator で連結）。
			"repository_breadcrumbs": func(project *domain.Project, repo *domain.Repository, path, kind, rev string) html {
				return RepositoryBreadcrumbs(project, repo, path, kind, rev)
			},
			// repositories_sidebar_links は show のサイドバー（リポジトリの切り替えリンク）。
			"repositories_sidebar_links": func(project *domain.Project, repos []*domain.Repository, cur *domain.Repository) html {
				p := pg()
				links := make([]string, len(repos))
				for i, r := range repos {
					name := r.Identifier
					if strings.TrimSpace(name) == "" {
						if r.IsDefault {
							name = p.l("field_repository_is_default")
						} else {
							name = r.SCMName()
						}
					}
					class := "repository"
					if cur != nil && r.ID == cur.ID {
						class += " selected"
					}
					links[i] = string(rails.LinkTo(name, RepositoryURL(project, r, "show", "", ""), rails.NewHash("class", class)))
				}
				return html(strings.Join(links, "<br />"))
			},
			// link_to_issue は link_to_issue(issue)（参照用のチケット）。
			"link_to_issue": func(is *redmine.Issue) html {
				return d.WikiRenderer(pg()).LinkToIssue(is, redmine.LinkToIssueOptions{})
			},
			// revision_links は parents / children の link_to_revision(p, @repository, :text => format_revision(p)) を ", " で連結。
			"revision_links": func(css []*domain.Changeset, project *domain.Project, repo *domain.Repository) html {
				p := pg()
				out := make([]string, len(css))
				for i, cs := range css {
					out[i] = string(LinkToRevision(p, cs, project, repo, nil))
				}
				return html(strings.Join(out, ", "))
			},
			// other_format_link_caption は OtherFormatsBuilder#link_to_with_query_parameters(name, url, :caption => caption)。
			"other_format_link_caption": func(name, url, caption string) html {
				return rails.ContentTag("span", rails.LinkTo(caption, url, rails.NewHash("class", strings.ToLower(name), "rel", "nofollow")), nil)
			},
			// json_string は to_json（文字列。< > & は \u エスケープ）。
			"json_string": func(s string) string {
				b, _ := json.Marshal(s)
				return string(b)
			},
			// committer_user_options は options_from_collection_for_select(@users, 'id', 'name', user_id.to_i)。
			"committer_user_options": func(users []*domain.User, selected int64) html {
				p := pg()
				var b strings.Builder
				for i, u := range users {
					if i > 0 {
						b.WriteString("\n")
					}
					attrs := rails.NewHash("value", u.ID)
					if u.ID == selected {
						attrs = rails.NewHash("selected", "selected", "value", u.ID)
					}
					b.WriteString(string(rails.ContentTag("option", p.UserName(u), attrs)))
				}
				return html(b.String())
			},
			// scm_select_tag は RepositoriesHelper#scm_select_tag。
			// 意図的な差異: buropher は Git のみ対応（D-15）のため、選択肢は空欄と Git だけ。
			"scm_select_tag": func(newRecord bool, newURL string) html {
				p := pg()
				opts := []any{[]any{"--- " + p.l("actionview_instancetag_blank_option") + " ---", ""}, []any{"Git", "Git"}}
				return rails.SelectTag("repository_scm", rails.OptionsForSelect(opts, "Git"),
					rails.NewHash("disabled", !newRecord, "data", rails.NewHash("remote", true, "method", "get", "url", newURL)))
			},
			// encoding_choices は [nil] + Setting::ENCODINGS。
			"encoding_choices": func() []any {
				out := []any{nil}
				for _, e := range settings.Encodings {
					out = append(out, e)
				}
				return out
			},
			// prepend_blank は [''] + list。
			"prepend_blank": func(list []string) []any {
				out := []any{""}
				for _, s := range list {
					out = append(out, s)
				}
				return out
			},
			"file_icon": func(e *scm.Entry, name string) html {
				return d.fileIcon(pg(), e, name)
			},
			"scm_change_icon": func(action string, name any) html {
				return d.spriteIcon(pg(), scmChangeIconName(action), name, rails.NewHash("size", 14))
			},
			"truncate_at_line_break": func(text string) string { return TruncateAtLineBreak(text, 255) },
			"with_leading_slash":     func(p string) string { return scm.WithLeadingSlash(p) },
			"changeset_author": func(c *domain.Changeset) any {
				if c.User != nil {
					return c.User
				}
				return c.CommitterName()
			},
			"format_changeset_comments": func(c *domain.Changeset, project *domain.Project) html {
				p := pg()
				return d.textilizable(p, c.Comments, rails.NewHash("project", project, "formatting", p.settingBool("commit_logs_formatting")))
			},
			"render_changes_tree": func(tree *ChangesTree, project *domain.Project, repo *domain.Repository, rev string) html {
				return d.renderChangesTree(pg(), tree, project, repo, rev)
			},
			"distance_of_time_in_words_now": func(t time.Time) string { return pg().Loc.DistanceOfTimeInWords(t, pg().now()) },
			"replace_invalid_utf8":          func(s string) string { return scm.ReplaceInvalidUTF8(s) },
			"mime_css_class_of":             func(name string) string { return mimetype.CSSClassOf(name) },
			// render_entry_pagination は RepositoriesHelper#render_pagination（ファイル間の前後リンク）。
			"render_entry_pagination": func(p *pagination.Paginator, entries []*scm.Entry, project *domain.Project, repo *domain.Repository, rev string) html {
				if p == nil {
					return ""
				}
				return paginationLinksEach(pg(), p, false, func(text string, params *rails.Hash, opts *rails.Hash) html {
					n := int(httpx.RubyToI(rails.ToS(params.Get(p.PageParam))))
					if n < 1 || n > len(entries) {
						return ""
					}
					path := scm.ReplaceInvalidUTF8(entries[n-1].Path)
					return rails.LinkTo(text, RepositoryURL(project, repo, "entry", ToPathParam(path), rev), nil)
				})
			},
			// hexdigest は ActiveSupport::Digest.hexdigest（Redmine の設定では MD5）。
			"hexdigest": func(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) },
		}
	})
}

// SyntaxHighlightLines は syntax_highlight_lines(filename, content)（行ごとの HTML）。
func SyntaxHighlightLines(filename, content string) []string {
	return syntaxHighlightLines(filename, content)
}

// queryPairs は repo_url の可変引数（key, value, ...）を組にする。
func queryPairs(kv []any) [][2]string {
	var out [][2]string
	for i := 0; i+1 < len(kv); i += 2 {
		v := rails.ToS(kv[i+1])
		if kv[i+1] == nil {
			continue
		}
		out = append(out, [2]string{rails.ToS(kv[i]), v})
	}
	return out
}

// reRevConstraint は routes.rb の :constraints => {:rev => /[a-z0-9\.\-_]+/}。
var reRevConstraint = regexp.MustCompile(`\A[a-z0-9\.\-_]+\z`)

// reSegment は既定のセグメントの制約（/ . ? を含まない）。
var reSegment = regexp.MustCompile(`\A[^/.?]+\z`)

// RepositoryURL は url_for(:controller => 'repositories', :action => action, :id => project,
// :repository_id => repo.identifier_param, :path => path, :rev => rev, ...) の生成規則
// （config/routes.rb のリポジトリのルートを Rails と同じ優先順位で選ぶ）。
// path は to_path_param 済みの値、query はそれ以外のパラメータ（キーの昇順でクエリにする）。
func RepositoryURL(project *domain.Project, repo *domain.Repository, action, path, rev string, query ...[2]string) string {
	base := "/projects/" + escapeSegment(project.Identifier) + "/repository/" + escapeSegment(repo.IdentifierParam())
	q := append([][2]string(nil), query...)
	revOK := rev != "" && reRevConstraint.MatchString(rev)
	withPath := func(u string) string {
		if path != "" {
			u += "/" + escapePathGlob(path)
		}
		return u
	}
	var u string
	switch action {
	case "show", "browse":
		switch {
		case revOK:
			u = withPath(base + "/revisions/" + escapeSegment(rev) + "/" + action)
		case action == "browse":
			u = withPath(base + "/browse")
			if rev != "" {
				q = append(q, [2]string{"rev", rev})
			}
		case path != "":
			u = withPath(base + "/show")
			if rev != "" {
				q = append(q, [2]string{"rev", rev})
			}
		default:
			u = base
			if rev != "" {
				q = append(q, [2]string{"rev", rev})
			}
		}
	case "entry", "raw", "annotate":
		if revOK {
			u = withPath(base + "/revisions/" + escapeSegment(rev) + "/" + action)
		} else {
			u = withPath(base + "/" + action)
			if rev != "" {
				q = append(q, [2]string{"rev", rev})
			}
		}
	case "changes":
		u = withPath(base + "/changes")
		if rev != "" {
			q = append(q, [2]string{"rev", rev})
		}
	case "diff":
		if revOK {
			u = withPath(base + "/revisions/" + escapeSegment(rev) + "/diff")
		} else {
			u = withPath(base + "/diff")
			if rev != "" {
				q = append(q, [2]string{"rev", rev})
			}
		}
	case "revision":
		if rev != "" && reSegment.MatchString(rev) {
			u = base + "/revisions/" + escapeSegment(rev)
		} else {
			u = base + "/revision"
			if rev != "" {
				q = append(q, [2]string{"rev", rev})
			}
		}
		if path != "" {
			q = append(q, [2]string{"path", path})
		}
	case "revisions":
		u = base + "/revisions"
	case "stats":
		u = base + "/statistics"
	case "graph":
		u = base + "/graph"
	case "fetch_changesets":
		u = base + "/fetch_changesets"
	default:
		u = base + "/" + action
	}
	if len(q) == 0 {
		return u
	}
	sort.SliceStable(q, func(i, j int) bool { return q[i][0] < q[j][0] })
	parts := make([]string, len(q))
	for i, kv := range q {
		parts[i] = cgiEscape(kv[0]) + "=" + cgiEscape(kv[1])
	}
	return u + "?" + strings.Join(parts, "&")
}

// escapePathGlob は Journey の escape_path（グロブ *path。/ を残す）。
func escapePathGlob(s string) string {
	const keep = "-._~!$&'()*+,;=:@/"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// cgiEscape は CGI.escape（to_query の値。空白は +）。
func cgiEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ToPathParam は ApplicationHelper#to_path_param（/ と \ で分け、空の要素を除いて / で連結）。
func ToPathParam(path string) string {
	var parts []string
	for _, p := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// FormatRevision は RepositoriesHelper#format_revision。
func FormatRevision(rev any) string {
	switch x := rev.(type) {
	case *domain.Changeset:
		return x.FormatIdentifier()
	case *scm.Revision:
		return x.FormatIdentifier()
	case string:
		return x
	}
	return rails.ToS(rev)
}

// revisionIdentifier は revision.respond_to?(:identifier) ? revision.identifier : revision。
func revisionIdentifier(rev any) string {
	switch x := rev.(type) {
	case *domain.Changeset:
		return x.Identifier()
	case *scm.Revision:
		return x.Identifier
	}
	return rails.ToS(rev)
}

// LinkToRevision は ApplicationHelper#link_to_revision(revision, repository, options)。
func LinkToRevision(p *Page, rev any, project *domain.Project, repo *domain.Repository, opts *rails.Hash) template.HTML {
	text := FormatRevision(rev)
	if opts != nil {
		if v := opts.Get("text"); v != nil {
			text = rails.ToS(v)
		}
	}
	var accesskey any
	if opts != nil {
		accesskey = opts.Get("accesskey")
	}
	return rails.LinkTo(rails.H(text), RepositoryURL(project, repo, "revision", "", revisionIdentifier(rev)),
		rails.NewHash("title", p.l("label_revision_id", FormatRevision(rev)), "accesskey", accesskey))
}

// TruncateAtLineBreak は truncate_at_line_break(text, length)（gsub(%r{^(.{length}[^\n]*)\n.+$}m, '\\1...')）。
func TruncateAtLineBreak(text string, length int) string {
	re := regexp.MustCompile(`(?s)^(.{` + fmt.Sprint(length) + `}[^\n]*)\n.+$`)
	// Ruby の ^ は行頭にも一致するため、各行頭から試す
	loc := -1
	for i := 0; i <= len(text); i++ {
		if i != 0 && text[i-1] != '\n' {
			continue
		}
		if m := re.FindStringSubmatchIndex(text[i:]); m != nil && m[0] == 0 {
			loc = i
			break
		}
	}
	if loc < 0 {
		return text
	}
	m := re.FindStringSubmatch(text[loc:])
	return text[:loc] + m[1] + "..."
}

// RepositoryBreadcrumbs は repositories/_breadcrumbs のリンク部分。
func RepositoryBreadcrumbs(project *domain.Project, repo *domain.Repository, path, kind, rev string) template.HTML {
	dirs := strings.Split(path, "/")
	// Ruby の String#split は末尾の空要素を除く
	for len(dirs) > 0 && dirs[len(dirs)-1] == "" {
		dirs = dirs[:len(dirs)-1]
	}
	filename := ""
	if kind == "file" && len(dirs) > 0 {
		filename = dirs[len(dirs)-1]
		dirs = dirs[:len(dirs)-1]
	}
	root := repo.Identifier
	if strings.TrimSpace(root) == "" {
		root = "root"
	}
	crumbs := []string{string(rails.LinkTo(root, RepositoryURL(project, repo, "show", "", rev), nil))}
	linkPath := ""
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if linkPath != "" {
			linkPath += "/"
		}
		linkPath += dir
		crumbs = append(crumbs, string(rails.LinkTo(dir, RepositoryURL(project, repo, "show", ToPathParam(linkPath), rev), nil)))
	}
	if filename != "" {
		crumbs = append(crumbs, string(rails.LinkTo(filename, RepositoryURL(project, repo, "entry", ToPathParam(linkPath+"/"+filename), rev), nil)))
	}
	return template.HTML(strings.Join(crumbs, `<span class="separator">/</span>`))
}

// IconForMimeType は IconsHelper#icon_for_mime_type（ハンドラで HTML を組み立てる箇所用）。
func IconForMimeType(mime string) string { return iconForMimeType(mime) }

// iconForMimeType は IconsHelper#icon_for_mime_type（MIME タイプ "type/subtype" からアイコン名を決める）。
func iconForMimeType(mime string) string {
	switch mime {
	case "text/x-c", "text/x-csharp", "text/x-java", "text/x-php", "text/x-ruby", "text/xml", "text/css", "text/html",
		"application/pdf", "application/zip", "application/gzip", "application/javascript":
		return strings.ReplaceAll(mime, "/", "-")
	}
	// mime.to_s.split('/')
	parts := strings.Split(mime, "/")
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	switch parts[0] {
	case "audio":
		return "file-music"
	case "image":
		return "photo"
	case "text":
		if sub == "markdown" || sub == "plain" || sub == "x-textile" {
			return "text-plain"
		}
	case "video":
		return "movie"
	default:
		// Microsoft Office Open XML の文書（プレビューできない旧形式 .doc/.xls/.ppt は含めない）
		switch mime {
		case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
			return "file-type-ppt"
		case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
			return "file-type-xls"
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
			return "file-type-docx"
		}
	}
	return "file"
}

// fileIcon は IconsHelper#file_icon(entry, name)。
func (d *Deps) fileIcon(p *Page, e *scm.Entry, name string) html {
	if e.IsDir() {
		return d.spriteIcon(p, "folder", name, nil)
	}
	return d.spriteIcon(p, iconForMimeType(mimetype.Of(name)), name, nil)
}

// scmChangeIconName は scm_change_icon のアイコン名。
func scmChangeIconName(action string) string {
	switch action {
	case "A":
		return "add"
	case "D":
		return "circle-minus"
	}
	return "circle-dot-filled"
}

// ChangesTree は render_changeset_changes の木（ディレクトリ → 子、ファイル → 変更）。
type ChangesTree struct {
	// Children はパス（"/dir/file"）ごとの子。
	Children map[string]*ChangesTree
	// Change はファイルの変更（ディレクトリなら nil）。
	Change *domain.ChangesetFile
}

// BuildChangesTree は render_changeset_changes の前処理（移動・コピーの判定と木の構築）。
// changes は filechanges.limit(1000).reorder('path')、all は filechanges 全体。
func BuildChangesTree(changes, all []*domain.ChangesetFile) *ChangesTree {
	root := &ChangesTree{}
	for _, c0 := range changes {
		c := *c0
		switch c.Action {
		case "A":
			if c.FromPath != "" {
				c.Action = "C"
				for _, x := range all {
					if x.Action == "D" && x.Path == c.FromPath {
						c.Action = "R"
						break
					}
				}
			}
		case "D":
			skip := false
			for _, x := range all {
				if x.FromPath == c.Path {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		p := root
		path := ""
		for _, dir := range strings.Split(c.Path, "/") {
			if strings.TrimSpace(dir) == "" {
				continue
			}
			path += "/" + dir
			if p.Children == nil {
				p.Children = map[string]*ChangesTree{}
			}
			n, ok := p.Children[path]
			if !ok {
				n = &ChangesTree{}
				p.Children[path] = n
			}
			p = n
		}
		cc := c
		p.Change = &cc
	}
	return root
}

// renderChangesTree は render_changes_tree(tree[:s])。
func (d *Deps) renderChangesTree(p *Page, tree *ChangesTree, project *domain.Project, repo *domain.Repository, rev string) html {
	if tree == nil || tree.Children == nil {
		return ""
	}
	var out strings.Builder
	out.WriteString("<ul>")
	keys := make([]string, 0, len(tree.Children))
	for k := range tree.Children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, file := range keys {
		node := tree.Children[file]
		base := rails.EscapeString(file)
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if node.Children != nil {
			text := rails.LinkTo(d.spriteIcon(p, "folder-open", template.HTML(base), nil),
				RepositoryURL(project, repo, "show", ToPathParam(file), rev), nil)
			out.WriteString("<li class='change folder'>")
			out.WriteString(string(text))
			out.WriteString(string(d.renderChangesTree(p, node, project, repo, rev)))
			out.WriteString("</li>")
		} else if c := node.Change; c != nil {
			pathParam := ToPathParam(c.Path)
			text := template.HTML(base)
			if c.Action != "D" {
				text = rails.LinkTo(d.spriteIcon(p, scmChangeIconName(c.Action), template.HTML(base), rails.NewHash("size", 14)),
					RepositoryURL(project, repo, "entry", pathParam, rev), nil)
			}
			if strings.TrimSpace(c.Revision) != "" {
				text += template.HTML(" - " + rails.EscapeString(c.Revision))
			}
			if c.Action == "M" {
				text += " (" + rails.LinkTo(p.l("label_diff"), RepositoryURL(project, repo, "diff", pathParam, rev), nil) + ") "
			}
			if strings.TrimSpace(c.FromPath) != "" {
				text += " " + rails.ContentTag("span", c.FromPath, rails.NewHash("class", "copied-from"))
			}
			out.WriteString("<li class='change change-" + c.Action + "'>")
			out.WriteString(string(text))
			out.WriteString("</li>")
		}
	}
	out.WriteString("</ul>")
	return html(out.String())
}
