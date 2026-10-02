// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package handler

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/mimetype"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/scm"
	"github.com/mikuta0407/buropher/internal/scmsync"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは RepositoriesController の閲覧系アクション（show / browse / changes / revisions / revision /
// entry / raw / annotate / diff / stats / graph）。

// helperRepositoryURL は helper.RepositoryURL。
func helperRepositoryURL(p *domain.Project, repo *domain.Repository, action, path, rev string, query ...[2]string) string {
	return helper.RepositoryURL(p, repo, action, path, rev, query...)
}

// findChangesetByName は @repository.find_changeset_by_name(name)（ユーザーを読み込む）。
func (a *App) findChangesetByName(c *Req, repo *domain.Repository, name string) (*domain.Changeset, error) {
	cs, err := scmsync.FindChangesetByName(c.Ctx(), a.DB, repo, name)
	if err != nil || cs == nil {
		return cs, err
	}
	return cs, a.loadChangesetUsers(c, []*domain.Changeset{cs})
}

// loadChangesetUsers は changeset.user を読み込む（preload(:user)）。
func (a *App) loadChangesetUsers(c *Req, css []*domain.Changeset) error {
	var ids []int64
	for _, cs := range css {
		if cs.UserID != nil {
			ids = append(ids, *cs.UserID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	users, _, err := repository.PrincipalsByIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		return err
	}
	for _, cs := range css {
		if cs.UserID != nil {
			cs.User = users[*cs.UserID]
		}
	}
	return nil
}

// repoEntries は @repository.entries(path, rev)（Git は report_last_commit に従い、各エントリのチェンジセットを読み込む）。
func (a *App) repoEntries(c *Req, s *repoState, path, rev string) ([]*repoEntry, bool, error) {
	es, ok := s.git.Entries(c.Ctx(), path, rev, s.repo.ReportLastCommit())
	if !ok {
		return nil, false, nil
	}
	out := make([]*repoEntry, len(es))
	for i, e := range es {
		re := &repoEntry{Entry: e, userName: c.Page().UserName}
		if e.Lastrev != nil && e.Lastrev.Identifier != "" {
			cs, err := a.findChangesetByName(c, s.repo, e.Lastrev.Identifier)
			if err != nil {
				return nil, false, err
			}
			re.Changeset = cs
		}
		out[i] = re
	}
	return out, true, nil
}

// repoEntry は Entry と entry.changeset。
type repoEntry struct {
	*scm.Entry
	Changeset *domain.Changeset
	userName  func(*domain.User) string
}

// Author は Entry#author（changeset があればその author.to_s、無ければ lastrev の author の "<" より前）。
func (e *repoEntry) Author() string {
	if e.Changeset != nil {
		if e.Changeset.User != nil {
			return e.userName(e.Changeset.User)
		}
		return e.Changeset.CommitterName()
	}
	if e.Lastrev != nil {
		s := scm.ReplaceInvalidUTF8(e.Lastrev.Author)
		if i := strings.Index(s, "<"); i >= 0 {
			s = s[:i]
		}
		return s
	}
	return ""
}

// TrID は ActiveSupport::Digest.hexdigest(entry.path)（Redmine の設定では MD5）。
func (e *repoEntry) TrID() string { return fmt.Sprintf("%x", md5.Sum([]byte(e.Path))) }

// latestChangesets は Repository::Git#latest_changesets(path, rev, limit)。
func (a *App) latestChangesets(c *Req, s *repoState, path, rev string, limit int) ([]*domain.Changeset, error) {
	revs := s.git.Revisions(c.Ctx(), path, "", rev, scm.RevisionsOptions{Limit: limit})
	if len(revs) == 0 {
		return nil, nil
	}
	ids := make([]string, len(revs))
	for i, r := range revs {
		ids[i] = r.Scmid
	}
	css, err := repository.ChangesetsByScmids(c.Ctx(), a.DB, s.repo.ID, ids)
	if err != nil {
		return nil, err
	}
	return css, a.loadChangesetUsers(c, css)
}

// repoCommonData はリポジトリの画面に共通するビューのデータ（_navigation / _breadcrumbs）。
func (a *App) repoCommonData(c *Req, s *repoState) map[string]any {
	ctx := c.Ctx()
	return map[string]any{
		"Repository":       s.repo,
		"Path":             s.path,
		"Rev":              s.rev,
		"RevTo":            s.revTo,
		"Branches":         s.git.BranchNames(ctx),
		"Tags":             s.git.Tags(ctx),
		"ManageRepository": c.AllowedTo(domain.Perm("manage_repository"), c.Project),
		"Autofetch":        a.Settings.Bool("autofetch_changesets"),
		"ActionName":       c.Action,
	}
}

// RepositoriesShow は repositories#show / browse（ルートの一覧・最新のリビジョン。XHR はツリーの展開）。
func (a *App) RepositoriesShow(c *Req) { a.repositoriesShow(c, nil) }

func (a *App) repositoriesShow(c *Req, entry *scm.Entry) {
	s := c.repoState()
	ctx := c.Ctx()
	if c.Project.Active() && a.Settings.Bool("autofetch_changesets") && s.path == "" {
		if err := a.scmService().Fetch(ctx, s.repo, c.User); err != nil {
			a.renderCommandFailed(c, err)
			return
		}
	}
	entries, ok, err := a.repoEntries(c, s, s.path, s.rev)
	if err != nil {
		a.internalError(c, "repository entries", err)
		return
	}
	cs, err := a.findChangesetByName(c, s.repo, s.rev)
	if err != nil {
		a.internalError(c, "find changeset", err)
		return
	}
	s.changeset = cs
	data := a.repoCommonData(c, s)
	data["Entries"] = entries
	data["Changeset"] = cs
	data["Entry"] = entry
	data["Depth"] = int(httpx.RubyToI(c.Params().String("depth")))
	data["ParentID"] = c.Params().String("parent_id")
	if httpx.IsXHR(c.R) {
		if !ok {
			httpx.Head(c.W, c.R, http.StatusOK)
			c.Halt()
			return
		}
		c.Render("repositories/_dir_list_content", data, RenderOptions{Layout: view.NoLayout})
		return
	}
	if !ok {
		a.showErrorNotFound(c)
		return
	}
	changesets, err := a.latestChangesets(c, s, s.path, s.rev, 10)
	if err != nil {
		a.internalError(c, "latest changesets", err)
		return
	}
	repos, err := repository.ProjectScmRepositories(ctx, a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "repositories", err)
		return
	}
	data["Changesets"] = changesets
	data["Revisions"], err = a.revisionsData(c, s, changesets, s.path)
	if err != nil {
		a.internalError(c, "revisions", err)
		return
	}
	data["Repositories"] = sortRepositories(repos)
	key := c.AtomKey()
	var kv [][2]string
	if key != "" {
		kv = append(kv, [2]string{"key", key})
	}
	data["AtomDiscoveryURL"] = httpx.RequestBaseURL(c.R) + urlroot.Path(helperRepositoryURL(c.Project, s.repo, "revisions", "", "", kv...))
	data["AtomURL"] = withAtomFormat(helperRepositoryURL(c.Project, s.repo, "revisions", "", "", kv...))
	c.Render("repositories/show", data)
}

// withAtomFormat は URL のパス部に .atom を付ける（format: 'atom'）。
func withAtomFormat(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i] + ".atom" + u[i:]
	}
	return u + ".atom"
}

// sortRepositories は @project.repositories.sort（既定のリポジトリが先、次に identifier）。
func sortRepositories(repos []*domain.Repository) []*domain.Repository {
	out := append([]*domain.Repository(nil), repos...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && repoLess(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func repoLess(a, b *domain.Repository) bool {
	switch {
	case a.IsDefault:
		return !b.IsDefault
	case b.IsDefault:
		return false
	}
	return a.Identifier < b.Identifier
}

// revisionRow は _revisions の 1 行。
type revisionRow struct {
	*domain.Changeset
	LineNum int
}

// revisionsData は _revisions のデータ（差分のラジオボタン・リビジョングラフ）。
func (a *App) revisionsData(c *Req, s *repoState, revisions []*domain.Changeset, path string) (map[string]any, error) {
	showDiff := len(revisions) > 1 && c.AllowedTo(domain.Perm("browse_repository"), c.Project)
	showGraph := s.repo.SupportsRevisionGraph() && path == ""
	rows := make([]*revisionRow, len(revisions))
	for i, cs := range revisions {
		rows[i] = &revisionRow{Changeset: cs, LineNum: i + 1}
	}
	data := map[string]any{
		"Path": path, "Revisions": rows, "ShowDiff": showDiff, "ShowGraph": showGraph && len(revisions) > 0,
		"Count": len(revisions),
		// form_tag の url_for は :rev を指定しないため、ルートの :rev を引き継ぐ（Rails の recall）
		"DiffURL": helperRepositoryURL(c.Project, s.repo, "diff", helper.ToPathParam(path), chi.URLParam(c.R, "rev")),
	}
	if showGraph && len(revisions) > 0 {
		js, space, err := a.indexCommits(c, s, revisions)
		if err != nil {
			return nil, err
		}
		data["GraphJSON"] = template.HTML(js)
		data["GraphSpace"] = space
		data["IDStyle"] = "padding-left:" + strconv.Itoa((space+1)*20) + "px"
	}
	return data, nil
}

// graphCommit は index_commits の 1 件。
type graphCommit struct {
	parents []string
	rdmid   int
	refs    *string
	scmid   string
	href    string
	space   *int
}

// indexCommits は RepositoriesHelper#index_commits（リビジョングラフの JSON と幅）。
func (a *App) indexCommits(c *Req, s *repoState, commits []*domain.Changeset) (string, int, error) {
	ids := make([]int64, len(commits))
	for i, cs := range commits {
		ids[i] = cs.ID
	}
	parents, err := repository.ChangesetParentScmids(c.Ctx(), a.DB, ids)
	if err != nil {
		return "", 0, err
	}
	heads := s.git.Branches(c.Ctx())
	refs := map[string][]string{}
	for _, h := range heads {
		refs[h.Scmid] = append(refs[h.Scmid], h.Name)
	}
	byScmid := map[string]*graphCommit{}
	var order []string
	for i := len(commits) - 1; i >= 0; i-- {
		cs := commits[i]
		gc := &graphCommit{parents: parents[cs.ID], rdmid: len(commits) - 1 - i, scmid: cs.Scmid,
			href: urlroot.Path(helperRepositoryURL(c.Project, s.repo, "revision", "", cs.Scmid))}
		if gc.parents == nil {
			gc.parents = []string{}
		}
		if r, ok := refs[cs.Scmid]; ok {
			j := strings.Join(r, " ")
			gc.refs = &j
		}
		if _, dup := byScmid[cs.Scmid]; !dup {
			order = append(order, cs.Scmid)
		}
		byScmid[cs.Scmid] = gc
	}
	space := -1
	matched := false
	for _, h := range heads {
		if _, ok := byScmid[h.Scmid]; ok {
			base := -1
			if matched {
				base = space
			}
			space = indexHead(base+1, h.Scmid, byScmid)
			matched = true
		}
	}
	if !matched {
		space = indexHead(0, commits[0].Scmid, byScmid)
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range order {
		gc := byScmid[k]
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonString(k))
		b.WriteString(`:{"parent_scmids":[`)
		for j, p := range gc.parents {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(jsonString(p))
		}
		b.WriteString(`],"rdmid":` + strconv.Itoa(gc.rdmid) + `,"refs":`)
		if gc.refs == nil {
			b.WriteString("null")
		} else {
			b.WriteString(jsonString(*gc.refs))
		}
		b.WriteString(`,"scmid":` + jsonString(gc.scmid) + `,"href":` + jsonString(gc.href))
		if gc.space != nil {
			b.WriteString(`,"space":` + strconv.Itoa(*gc.space))
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return b.String(), space, nil
}

// indexHead は RepositoriesHelper#index_head。
func indexHead(space int, scmid string, byScmid map[string]*graphCommit) int {
	type item struct {
		space int
		c     *graphCommit
	}
	stack := []item{{space, byScmid[scmid]}}
	maxSpace := space
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sp, commit := it.space, it.c
		if commit.space == nil {
			v := sp
			commit.space = &v
		}
		sp--
		for _, ps := range commit.parents {
			pc := byScmid[ps]
			if pc != nil && pc.space == nil {
				sp++
				stack = append([]item{{sp, pc}}, stack...)
			}
		}
		if maxSpace < sp {
			maxSpace = sp
		}
	}
	return maxSpace
}

// RepositoriesChanges は repositories#changes（ファイル・ディレクトリの履歴）。
func (a *App) RepositoriesChanges(c *Req) {
	s := c.repoState()
	entry := s.git.Entry(c.Ctx(), s.path, s.rev)
	if entry == nil {
		a.showErrorNotFound(c)
		return
	}
	changesets, err := a.latestChangesets(c, s, s.path, s.rev, a.Settings.Int("repository_log_display_limit"))
	if err != nil {
		a.internalError(c, "latest changesets", err)
		return
	}
	cs, err := a.findChangesetByName(c, s.repo, s.rev)
	if err != nil {
		a.internalError(c, "find changeset", err)
		return
	}
	s.changeset = cs
	data := a.repoCommonData(c, s)
	data["Entry"] = entry
	data["Changeset"] = cs
	data["Changesets"] = changesets
	if len(changesets) > 0 {
		if data["Revisions"], err = a.revisionsData(c, s, changesets, s.path); err != nil {
			a.internalError(c, "revisions", err)
			return
		}
	}
	data["Tabs"] = a.linkToFunctionsTabs(c, s, entry)
	c.Render("repositories/changes", data)
}

// linkToFunctionsTabs は _link_to_functions のタブ（ファイルのときだけ）。
func (a *App) linkToFunctionsTabs(c *Req, s *repoState, entry *scm.Entry) []helper.Tab {
	if entry == nil || entry.Kind != "file" {
		return nil
	}
	p := helper.ToPathParam(s.path)
	return []helper.Tab{
		{Name: "entry", Label: "button_view", URL: helperRepositoryURL(c.Project, s.repo, "entry", p, s.rev)},
		{Name: "changes", Label: "label_history", URL: helperRepositoryURL(c.Project, s.repo, "changes", p, s.rev)},
		{Name: "annotate", Label: "button_annotate", URL: helperRepositoryURL(c.Project, s.repo, "annotate", p, s.rev)},
	}
}

// RepositoriesRevisions は repositories#revisions（ページ分割した一覧。atom はフィード）。
func (a *App) RepositoriesRevisions(c *Req) {
	s := c.repoState()
	ctx := c.Ctx()
	count, err := repository.CountChangesets(ctx, a.DB, s.repo.ID)
	if err != nil {
		a.internalError(c, "count changesets", err)
		return
	}
	pages := pagination.New(count, c.PerPageOption(), c.Params().String("page"))
	changesets, err := repository.ListChangesets(ctx, a.DB, s.repo.ID, pages.PerPage, pages.Offset())
	if err != nil {
		a.internalError(c, "changesets", err)
		return
	}
	if err := a.loadChangesetUsers(c, changesets); err != nil {
		a.internalError(c, "changeset users", err)
		return
	}
	switch httpx.Format(c.R) {
	case "atom":
		ids := make([]string, len(changesets))
		for i, cs := range changesets {
			ids[i] = strconv.FormatInt(cs.ID, 10)
		}
		var events []*activity.Event
		if len(ids) > 0 {
			loader := &activity.Loader{Q: a.DB, Auth: c.Authz(), Loc: c.Loc}
			events, err = loader.Changesets(ctx, "changesets.id IN ("+strings.Join(ids, ",")+")", nil, "")
			if err != nil {
				a.internalError(c, "changeset events", err)
				return
			}
		}
		a.renderFeed(c, events, c.Project.Name+": "+c.L("label_revision_plural"))
		return
	case "", "html":
	default:
		respondNotAcceptable(c)
		return
	}
	data := a.repoCommonData(c, s)
	data["Pages"] = pages
	data["Count"] = count
	if data["Revisions"], err = a.revisionsData(c, s, changesets, ""); err != nil {
		a.internalError(c, "revisions", err)
		return
	}
	key := c.AtomKey()
	qp := c.Page().QueryParameters().Except("page", "key")
	if key != "" {
		qp.Set("key", key)
	}
	base := httpx.RequestBaseURL(c.R)
	path := helperRepositoryURL(c.Project, s.repo, "revisions", "", "")
	data["AtomDiscoveryURL"] = base + helper.URLWithQuery(path+".atom", qp)
	var kv [][2]string
	if key != "" {
		kv = append(kv, [2]string{"key", key})
	}
	data["AtomURL"] = withAtomFormat(helperRepositoryURL(c.Project, s.repo, "revisions", "", "", kv...))
	opts := RenderOptions{}
	if httpx.IsXHR(c.R) {
		opts.Layout = view.NoLayout
	}
	c.Render("repositories/revisions", data, opts)
}

// changesetData は revision / diff の _changeset のデータ。
func (a *App) changesetData(c *Req, s *repoState) (map[string]any, error) {
	ctx := c.Ctx()
	cs := s.changeset
	if err := a.loadChangesetUsers(c, []*domain.Changeset{cs}); err != nil {
		return nil, err
	}
	prev, err := repository.PreviousChangeset(ctx, a.DB, cs)
	if err != nil {
		return nil, err
	}
	next, err := repository.NextChangeset(ctx, a.DB, cs)
	if err != nil {
		return nil, err
	}
	parents, err := repository.ChangesetParents(ctx, a.DB, cs.ID)
	if err != nil {
		return nil, err
	}
	children, err := repository.ChangesetChildren(ctx, a.DB, cs.ID)
	if err != nil {
		return nil, err
	}
	files, err := repository.ChangesetFiles(ctx, a.DB, cs.ID, false, 0)
	if err != nil {
		return nil, err
	}
	data, err := a.relatedIssuesData(c)
	if err != nil {
		return nil, err
	}
	data["Previous"] = prev
	data["Next"] = next
	data["Parents"] = parents
	data["Children"] = children
	data["BrowseRepository"] = c.AllowedTo(domain.Perm("browse_repository"), c.Project)
	tabs := []helper.Tab{{Name: "revision", Label: "label_change_plural",
		URL: helperRepositoryURL(c.Project, s.repo, "revision", "", cs.Identifier())}}
	if c.Action == "diff" || len(files) > 0 {
		tabs = append(tabs, helper.Tab{Name: "diff", Label: "label_view_diff",
			URL: helperRepositoryURL(c.Project, s.repo, "diff", "", cs.Identifier())})
	}
	data["ChangesetTabs"] = tabs
	if c.Action == "revision" {
		changes, err := repository.ChangesetFiles(ctx, a.DB, cs.ID, true, 1000)
		if err != nil {
			return nil, err
		}
		data["ChangesTree"] = helper.BuildChangesTree(changes, files)
	}
	return data, nil
}

// relatedIssuesData は _related_issues のデータ。
func (a *App) relatedIssuesData(c *Req) (map[string]any, error) {
	s := c.repoState()
	ctx := c.Ctx()
	ids, err := repository.ChangesetIssueIDs(ctx, a.DB, s.changeset.ID)
	if err != nil {
		return nil, err
	}
	st := redmine.NewDBStore(ctx, a.DB, c.Authz())
	var issues []*redmine.Issue
	for _, id := range ids {
		is, err := st.VisibleIssue(id)
		if err != nil {
			return nil, err
		}
		if is != nil {
			issues = append(issues, is)
		}
	}
	manage := c.AllowedTo(domain.Perm("manage_related_issues"), c.Project)
	return map[string]any{
		"Repository":       s.repo,
		"Changeset":        s.changeset,
		"Rev":              s.rev,
		"RelatedIssues":    issues,
		"ManageRelated":    manage,
		"ShowRelated":      len(issues) > 0 || manage,
		"AutoCompletePath": urlroot.Path("/issues/auto_complete?project_id=" + rails.URLEncode(c.Project.Identifier) + "&scope=all"),
	}, nil
}

// RepositoriesRevision は repositories#revision（チェンジセットの画面）。
func (a *App) RepositoriesRevision(c *Req) {
	s := c.repoState()
	data, err := a.changesetData(c, s)
	if err != nil {
		a.internalError(c, "changeset", err)
		return
	}
	c.Render("repositories/revision", data)
}

// RepositoriesEntry は repositories#entry（ファイルの表示）。
func (a *App) RepositoriesEntry(c *Req) { a.entryAndRaw(c, false) }

// RepositoriesRaw は repositories#raw（ファイルのダウンロード）。
func (a *App) RepositoriesRaw(c *Req) { a.entryAndRaw(c, true) }

// isEntryTextData は is_entry_text_data?（拡張子がテキスト、またはバイナリでない）。
func isEntryTextData(content []byte, path string) bool {
	if mimetype.IsType("text", path) {
		return true
	}
	return !scm.IsBinary(content)
}

// entryAndRaw は entry_and_raw(is_raw)。
func (a *App) entryAndRaw(c *Req, isRaw bool) {
	s := c.repoState()
	ctx := c.Ctx()
	entry := s.git.Entry(ctx, s.path, s.rev)
	if entry == nil {
		a.showErrorNotFound(c)
		return
	}
	if entry.IsDir() {
		a.repositoriesShow(c, entry)
		return
	}
	if isRaw {
		content, ok := s.git.Cat(ctx, s.path, s.rev)
		if !ok {
			a.showErrorNotFound(c)
			return
		}
		name := s.path
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		h := c.W.Header()
		typ := mimetype.Of(s.path)
		switch typ {
		case "":
			typ = "application/octet-stream"
		case "application/javascript":
			typ = "text/plain"
		}
		disposition := "attachment"
		if mimetype.Of(s.path) == "application/pdf" {
			disposition = "inline"
		}
		h.Set("Content-Type", typ)
		h.Set("Content-Disposition", httpx.ContentDisposition(disposition, name))
		h.Set("Content-Transfer-Encoding", "binary")
		c.Halt()
		c.W.WriteHeader(http.StatusOK)
		if c.R.Method != http.MethodHead {
			_, _ = c.W.Write(content)
		}
		return
	}
	data := a.repoCommonData(c, s)
	data["Entry"] = entry
	// ファイル間の前後リンク
	parentPath := ""
	if i := strings.LastIndex(s.path, "/"); i >= 0 {
		parentPath = s.path[:i]
	}
	if siblings, ok, err := a.repoEntries(c, s, parentPath, s.rev); err != nil {
		a.internalError(c, "entries", err)
		return
	} else if ok {
		var files []*scm.Entry
		for _, e := range siblings {
			if !e.IsDir() {
				files = append(files, e.Entry)
			}
		}
		for i, e := range files {
			if e.Name == entry.Name {
				data["Paginator"] = pagination.New(len(files), 1, i+1)
				data["PageEntries"] = files
				break
			}
		}
	}
	maxSize := int64(a.Settings.Int("file_max_size_displayed")) * 1024
	kind := ""
	if entry.Size == nil || *entry.Size <= maxSize {
		content, ok := s.git.Cat(ctx, s.path, s.rev)
		if !ok {
			a.showErrorNotFound(c)
			return
		}
		if int64(len(content)) <= maxSize && isEntryTextData(content, s.path) {
			text := strings.ReplaceAll(string(content), "\r\n", "\n")
			data["Content"] = scm.ToUTF8BySetting(text, a.Settings.String("repositories_encodings"))
			kind = "file"
		}
	}
	rawURL := helperRepositoryURL(c.Project, s.repo, "raw", helper.ToPathParam(s.path), s.rev)
	data["RawURL"] = rawURL
	switch {
	case mimetype.IsType("image", s.path):
		kind = "image"
	case mimetype.Of(s.path) == "text/x-textile":
		kind = "markup"
		data["Markup"] = a.markupToHTML(c, "textile", stringOrEmpty(data["Content"]))
	case mimetype.Of(s.path) == "text/markdown":
		kind = "markup"
		data["Markup"] = a.markupToHTML(c, "common_mark", stringOrEmpty(data["Content"]))
	case kind == "file":
	default:
		kind = "other"
		switch {
		case mimetype.IsType("video", s.path):
			data["MediaKind"] = "video"
		case mimetype.IsType("audio", s.path):
			data["MediaKind"] = "audio"
		}
	}
	data["Kind"] = kind
	cs, err := a.findChangesetByName(c, s.repo, s.rev)
	if err != nil {
		a.internalError(c, "find changeset", err)
		return
	}
	s.changeset = cs
	data["Changeset"] = cs
	data["Tabs"] = a.linkToFunctionsTabs(c, s, entry)
	c.Render("repositories/entry", data)
}

func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

// annotateLine は annotate の 1 行。
type annotateLine struct {
	Num      int
	HTML     template.HTML
	Blank    bool
	Revision *scm.Revision
	// Changed は revision && revision != previous_revision（リビジョン・作成者を表示する）。
	Changed bool
	// BlocChange は previous_revision && revision && revision != previous_revision（bloc-change クラス）。
	BlocChange bool
	Color      int
	Author     string
	// PreviousURL は「この変更以前の注釈」のリンク（無ければ ""）。
	PreviousURL string
}

// RepositoriesAnnotate は repositories#annotate。
func (a *App) RepositoriesAnnotate(c *Req) {
	s := c.repoState()
	ctx := c.Ctx()
	entry := s.git.Entry(ctx, s.path, s.rev)
	if entry == nil {
		a.showErrorNotFound(c)
		return
	}
	data := a.repoCommonData(c, s)
	data["Entry"] = entry
	ann := s.git.Annotate(ctx, s.path, s.rev)
	switch {
	case ann == nil || ann.Empty():
		data["ErrorMessage"] = c.L("error_scm_annotate")
	case annotateSize(ann) > a.Settings.Int("file_max_size_displayed")*1024:
		data["ErrorMessage"] = c.L("error_scm_annotate_big_text_file")
	default:
		hasPrevious := ann.HasPrevious()
		content := scm.ToUTF8BySetting(ann.Content(), a.Settings.String("repositories_encodings"))
		hl := helper.SyntaxHighlightLines(s.path, content)
		colors := map[string]int{}
		var prev *scm.Revision
		lines := make([]*annotateLine, len(hl))
		for i, h := range hl {
			l := &annotateLine{Num: i + 1, HTML: template.HTML(h), Blank: h == "\n" || h == "\r\n"}
			var rev *scm.Revision
			if i < len(ann.Revisions) {
				rev = ann.Revisions[i]
			}
			l.Revision = rev
			if rev != nil {
				key := rev.Identifier
				if key == "" {
					key = rev.Revision
				}
				if _, ok := colors[key]; !ok {
					colors[key] = len(colors) % 12
				}
				l.Color = colors[key]
				l.BlocChange = prev != nil && !rev.Equal(prev)
				if prev == nil || !rev.Equal(prev) {
					l.Changed = true
					author := scm.ToUTF8(rev.Author, s.repo.RepoLogEncoding())
					if j := strings.Index(author, "<"); j >= 0 {
						author = author[:j]
					}
					l.Author = author
					if hasPrevious && i < len(ann.Previous) && ann.Previous[i] != "" {
						f := strings.Fields(ann.Previous[i])
						p := s.path
						if len(f) > 1 {
							p = f[1]
						}
						l.PreviousURL = helperRepositoryURL(c.Project, s.repo, "annotate", helper.ToPathParam(p), f[0])
					}
				}
			}
			lines[i] = l
			prev = rev
		}
		data["Lines"] = lines
		data["HasPrevious"] = hasPrevious
	}
	cs, err := a.findChangesetByName(c, s.repo, s.rev)
	if err != nil {
		a.internalError(c, "find changeset", err)
		return
	}
	s.changeset = cs
	data["Changeset"] = cs
	data["Tabs"] = a.linkToFunctionsTabs(c, s, entry)
	c.Render("repositories/annotate", data)
}

// annotateSize は @annotate.lines.sum(&:size)（文字数）。
func annotateSize(a *scm.Annotate) int {
	n := 0
	for _, l := range a.Lines {
		n += len([]rune(l))
	}
	return n
}

// RepositoriesDiff は repositories#diff（.diff は unified diff のダウンロード）。
func (a *App) RepositoriesDiff(c *Req) {
	s := c.repoState()
	ctx := c.Ctx()
	format := httpx.Format(c.R)
	if format == "" {
		format = c.Params().String("format")
	}
	if format == "diff" {
		diff, ok := s.git.Diff(ctx, s.path, s.rev, s.revTo)
		if !ok {
			a.showErrorNotFound(c)
			return
		}
		filename := "changeset_r" + s.rev
		if s.revTo != "" {
			filename += "_r" + s.revTo
		}
		h := c.W.Header()
		h.Set("Content-Type", "text/x-patch")
		h.Set("Content-Disposition", httpx.ContentDisposition("attachment", filename+".diff"))
		h.Set("Content-Transfer-Encoding", "binary")
		c.Halt()
		c.W.WriteHeader(http.StatusOK)
		if c.R.Method != http.MethodHead {
			_, _ = c.W.Write([]byte(strings.Join(diff, "")))
		}
		return
	}
	if format != "" && format != "html" {
		c.Render404("")
		return
	}
	// params[:type] || User.current.pref[:diff_type] || 'inline'
	prefType := ""
	if c.User.Logged() {
		if v, err := repository.UserPrefExtra(ctx, a.DB, c.User.ID, "diff_type"); err == nil && v != nil {
			prefType = httpx.ValueString(v)
		}
	}
	diffType := c.Params().String("type")
	if diffType == "" {
		diffType = prefType
	}
	if diffType != "inline" && diffType != "sbs" {
		diffType = "inline"
	}
	if c.User.Logged() && diffType != prefType {
		if err := repository.SetUserPrefExtra(ctx, a.DB, c.User.ID, "diff_type", diffType); err != nil {
			a.logger().Error("save diff type", "err", err)
		}
	}
	diff, ok := s.git.Diff(ctx, s.path, s.rev, s.revTo)
	if !ok {
		a.showErrorNotFound(c)
		return
	}
	cs, err := a.findChangesetByName(c, s.repo, s.rev)
	if err != nil {
		a.internalError(c, "find changeset", err)
		return
	}
	var csTo *domain.Changeset
	if s.revTo != "" {
		if csTo, err = a.findChangesetByName(c, s.repo, s.revTo); err != nil {
			a.internalError(c, "find changeset", err)
			return
		}
	}
	s.changeset = cs
	data := a.repoCommonData(c, s)
	if cs != nil && csTo == nil {
		cd, err := a.changesetData(c, s)
		if err != nil {
			a.internalError(c, "changeset", err)
			return
		}
		for k, v := range cd {
			data[k] = v
		}
	}
	data["Changeset"] = cs
	data["ChangesetTo"] = csTo
	formatRevs := ""
	if csTo != nil {
		formatRevs += csTo.FormatIdentifier() + ":"
	}
	if cs != nil {
		formatRevs += cs.FormatIdentifier()
	}
	data["DiffFormatRevisions"] = formatRevs
	data["DiffType"] = diffType
	enc := a.Settings.String("repositories_encodings")
	conv := make([]string, len(diff))
	for i, l := range diff {
		conv[i] = scm.ToUTF8BySetting(l, enc)
	}
	data["Diff"] = strings.Join(conv, "")
	_, hasRevTo := c.Params().Get("rev_to")
	data["HasRevToParam"] = hasRevTo
	data["RevToParam"] = c.Params().String("rev_to")
	data["DiffFormURL"] = helperRepositoryURL(c.Project, s.repo, "diff", helper.ToPathParam(s.path), s.rev)
	data["UnifiedDiffURL"] = a.unifiedDiffURL(c, s)
	c.Render("repositories/diff", data)
}

// unifiedDiffURL は other_formats_links の f.link_to_with_query_parameters 'Diff', { path: nil }
// （クエリを保ったまま format: 'diff'、path: nil。rev はルートの値を引き継ぐ）。
func (a *App) unifiedDiffURL(c *Req, s *repoState) string {
	qp := c.Page().QueryParameters().Except("page", "format", "path")
	u := helperRepositoryURL(c.Project, s.repo, "diff", "", chi.URLParam(c.R, "rev"))
	return helper.URLWithQuery(withAtomFormatExt(u, ".diff"), qp)
}

// withAtomFormatExt は URL のパス部に拡張子を付ける。
func withAtomFormatExt(u, ext string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i] + ext + u[i:]
	}
	return u + ext
}

// RepositoriesStats は repositories#stats。
func (a *App) RepositoriesStats(c *Req) {
	s := c.repoState()
	c.Render("repositories/stats", map[string]any{
		"Repository":   s.repo,
		"MonthURL":     helperRepositoryURL(c.Project, s.repo, "graph", "", "", [2]string{"graph", "commits_per_month"}),
		"AuthorURL":    helperRepositoryURL(c.Project, s.repo, "graph", "", "", [2]string{"graph", "commits_per_author"}),
		"BackURL":      helperRepositoryURL(c.Project, s.repo, "show", "", ""),
		"RevisionsTxt": jsonString(c.L("label_revision_plural")),
		"ChangesTxt":   jsonString(c.L("label_change_plural")),
		"MonthTitle":   jsonString(c.L("label_commits_per_month")),
		"AuthorTitle":  jsonString(c.L("label_commits_per_author")),
	})
}

// RepositoriesGraph は repositories#graph（グラフの JSON）。
func (a *App) RepositoriesGraph(c *Req) {
	s := c.repoState()
	var data map[string]any
	var err error
	switch c.Params().String("graph") {
	case "commits_per_month":
		data, err = a.graphCommitsPerMonth(c, s.repo)
	case "commits_per_author":
		data, err = a.graphCommitsPerAuthor(c, s.repo)
	default:
		c.Render404("")
		return
	}
	if err != nil {
		a.internalError(c, "graph", err)
		return
	}
	labels, _ := json.Marshal(data["labels"])
	commits, _ := json.Marshal(data["commits"])
	changes, _ := json.Marshal(data["changes"])
	body := `{"labels":` + string(labels) + `,"commits":` + string(commits) + `,"changes":` + string(changes) + `}`
	c.Halt()
	c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(body))
}

// graphCommitsPerMonth は graph_commits_per_month（直近 12 か月のコミット数・変更数）。
func (a *App) graphCommitsPerMonth(c *Req, repo *domain.Repository) (map[string]any, error) {
	dateTo := a.userToday(c)
	// date_from = date_to << 11 の月の 1 日
	dateFrom := time.Date(dateTo.Year(), dateTo.Month()-11, 1, 0, 0, 0, 0, time.UTC)
	commits := make([]int, 12)
	changes := make([]int, 12)
	idx := func(d time.Time) int { return ((int(dateTo.Month())-int(d.Month()))%12 + 12) % 12 }
	cs, err := repository.ChangesetCountsByDate(c.Ctx(), a.DB, repo.ID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	for _, r := range cs {
		commits[idx(r.Date.Time)] += r.Count
	}
	ch, err := repository.ChangeCountsByDate(c.Ctx(), a.DB, repo.ID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	for _, r := range ch {
		changes[idx(r.Date.Time)] += r.Count
	}
	labels := make([]string, 12)
	for m := 0; m < 12; m++ {
		labels[11-m] = c.Loc.MonthName(((int(dateTo.Month())-1-m)%12+12)%12 + 1)
	}
	return map[string]any{"labels": labels, "commits": reverseInts(commits), "changes": reverseInts(changes)}, nil
}

func reverseInts(xs []int) []int {
	out := make([]int, len(xs))
	for i, x := range xs {
		out[len(xs)-1-i] = x
	}
	return out
}

var reMailInName = regexp.MustCompile(`<.+@.+>`)

// graphCommitsPerAuthor は graph_commits_per_author（stats_by_author。10 件に満たなければ空で埋める）。
func (a *App) graphCommitsPerAuthor(c *Req, repo *domain.Repository) (map[string]any, error) {
	ctx := c.Ctx()
	commits, err := repository.ChangesetCountsByAuthor(ctx, a.DB, repo.ID)
	if err != nil {
		return nil, err
	}
	changes, err := repository.ChangeCountsByAuthor(ctx, a.DB, repo.ID)
	if err != nil {
		return nil, err
	}
	uids, err := repository.ChangesetUserIDs(ctx, a.DB, repo.ID)
	if err != nil {
		return nil, err
	}
	users, _, err := repository.PrincipalsByIDs(ctx, a.DB, uids)
	if err != nil {
		return nil, err
	}
	page := c.Page()
	names := map[int64]string{}
	for id, u := range users {
		names[id] = page.UserName(u)
	}
	var order []string
	stats := map[string][2]int{}
	add := func(rows []repository.AuthorCount, idx int) {
		for _, r := range rows {
			name := r.Committer.String
			if n, ok := names[r.UserID.Int64]; ok && r.UserID.Valid {
				name = n
			}
			v, ok := stats[name]
			if !ok {
				order = append(order, name)
			}
			v[idx] += r.Count
			stats[name] = v
		}
	}
	add(commits, 0)
	add(changes, 1)
	fields := make([]string, 0, 10)
	var cdata, chdata []int
	for _, n := range order {
		fields = append(fields, n)
		cdata = append(cdata, stats[n][0])
		chdata = append(chdata, stats[n][1])
	}
	for len(fields) < 10 {
		fields = append(fields, "")
	}
	for len(cdata) < 10 {
		cdata = append(cdata, 0)
	}
	for len(chdata) < 10 {
		chdata = append(chdata, 0)
	}
	labels := make([]string, len(fields))
	for i, f := range fields {
		labels[len(fields)-1-i] = reMailInName.ReplaceAllString(f, "")
	}
	return map[string]any{"labels": labels, "commits": reverseInts(cdata), "changes": reverseInts(chdata)}, nil
}
