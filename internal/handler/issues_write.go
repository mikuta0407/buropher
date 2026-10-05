// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// IssuesController（app/controllers/issues_controller.rb）の作成・更新系アクション:
// new（update_form の XHR・コピーを含む）/ create / edit（update_form）/ update と REST API の create / update。

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/view/rails"
	"github.com/mikuta0407/buropher/internal/webhook"
)

// routesIssuesWrite は issues コントローラの作成・更新系のルートを登録する。
//
//	get 'projects/:project_id/issues/:copy_from/copy', :to => 'issues#new'
//	resources :projects { resources :issues, :only => [:index, :new, :create]; post 'issues/new' }
//	resources :issues { member { patch 'edit' } }（new / create / edit / update）
//	post '/issues/new', :to => 'issues#new'
func (a *App) routesIssuesWrite(r Router) {
	// before_action :find_optional_project, :only => [:index, :new, :create]
	// before_action :build_new_issue_from_params, :only => [:new, :create]
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/projects/{project_id}/issues/new", IssuesController, "new", a.IssuesNew, FindOptionalProject())
		a.Handle(r, m, "/issues/new", IssuesController, "new", a.IssuesNew, FindOptionalProject())
	}
	a.Handle(r, http.MethodGet, "/projects/{project_id}/issues/{copy_from}/copy", IssuesController, "new", a.IssuesNew, FindOptionalProject())
	a.Handle(r, http.MethodPost, "/projects/{project_id}/issues", IssuesController, "create", a.IssuesCreate,
		FindOptionalProject(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/issues", IssuesController, "create", a.IssuesCreate, FindOptionalProject(), AcceptAPIAuth())
	// before_action :find_issue, :only => [:show, :edit, :update, :issue_tab]
	// before_action :authorize, :except => [:index, :new, :create]
	a.Handle(r, http.MethodGet, "/issues/{id}/edit", IssuesController, "edit", a.IssuesEdit, Before(a.findIssue), Authorize())
	a.Handle(r, http.MethodPatch, "/issues/{id}/edit", IssuesController, "edit", a.IssuesEdit, Before(a.findIssue), Authorize())
	for _, m := range []string{http.MethodPut, http.MethodPatch} {
		a.Handle(r, m, "/issues/{id}", IssuesController, "update", a.IssuesUpdate, Before(a.findIssue), Authorize(), AcceptAPIAuth())
	}
}

// ---------------------------------------------------------------- params

// issueParamsOf は params[:issue]（*httpx.Params）を issues.Params（プレーンな map）にする。
func issueParamsOf(p *httpx.Params) issues.Params {
	if p == nil {
		return nil
	}
	out := issues.Params{}
	p.Each(func(k string, v any) { out[k] = plainIssueParam(k, v) })
	return out
}

func plainIssueParam(key string, v any) any {
	switch x := v.(type) {
	case *httpx.Params:
		m := map[string]any{}
		x.Each(func(k string, v2 any) { m[k] = plainIssueParam(k, v2) })
		return m
	case []any:
		if key == "custom_fields" {
			var list []map[string]any
			for _, e := range x {
				if ep, ok := e.(*httpx.Params); ok {
					m := map[string]any{}
					ep.Each(func(k string, v2 any) { m[k] = plainIssueParam(k, v2) })
					list = append(list, m)
				}
			}
			return list
		}
		ss := make([]string, 0, len(x))
		for _, e := range x {
			switch y := e.(type) {
			case string:
				ss = append(ss, y)
			case *httpx.Params, []any:
				return x
			default:
				ss = append(ss, rails.ToS(y))
			}
		}
		return ss
	}
	return v
}

// timeEntryParamsPresent は params[:time_entry][:hours].present? || params[:time_entry][:comments].present?。
func timeEntryParamsPresent(p *httpx.Params) bool {
	return p != nil && (rails.IsPresent(p.String("hours")) || rails.IsPresent(p.String("comments")))
}

// timelogEnv は User.current の工数の Env。
func (a *App) timelogEnv(c *Req) *timelog.Env {
	return &timelog.Env{Q: a.DB, Settings: a.Settings, User: c.User, Az: c.Authz(), Loc: c.Loc, Now: a.now}
}

// attachmentsParam は params[:attachments] || (params[:issue] && params[:issue][:uploads])。
func attachmentsParam(c *Req) any {
	if v, ok := c.Params().Get("attachments"); ok && v != nil {
		return v
	}
	if is := c.Params().Map("issue"); is != nil {
		if v, ok := is.Get("uploads"); ok {
			return v
		}
	}
	return nil
}

// attachmentsParamSize は attachments.size（Hash / Array の要素数。それ以外は 0）。
func attachmentsParamSize(v any) int {
	switch x := v.(type) {
	case *httpx.Params:
		return x.Len()
	case []any:
		return len(x)
	}
	return 0
}

// ---------------------------------------------------------------- new / create

// issueNewState は build_new_issue_from_params が設定するインスタンス変数。
type issueNewState struct {
	env *issues.Env
	iss *issues.Issue

	copyFrom        *issues.Issue
	linkCopy        bool
	copyAttachments bool
	copySubtasks    bool
	copyWatchers    bool

	allowed []*domain.IssueStatus
	// saved は save_attachments の結果（フォームの再表示で saved_attachments を出す）。
	saved *attachments.SaveResult
}

// issuesNewTabController は「新しいチケット」タブが有効なときの new / create（current_menu_item が :new_issue）。
var issuesNewTabController = &Controller{Name: "issues", MainMenu: true, DefaultSearchScope: "issues",
	MenuItem: func(string) string { return "new_issue" }}

// buildNewIssueFromParams は IssuesController#build_new_issue_from_params（失敗時は描画済みで nil）。
func (a *App) buildNewIssueFromParams(c *Req) *issueNewState {
	if a.Settings.String("new_item_menu_tab") == "1" {
		c.Controller = issuesNewTabController
	}
	ctx := c.Ctx()
	env := a.writeIssuesEnv(c, a.DB)
	st := &issueNewState{env: env}
	fail := func(what string, err error) *issueNewState {
		a.internalError(c, what, err)
		return nil
	}
	iss, err := env.NewBlank(ctx)
	if err != nil {
		return fail("new issue", err)
	}
	st.iss = iss
	get := c.R.Method == http.MethodGet || c.R.Method == http.MethodHead
	if cf := c.Params().String("copy_from"); c.Params().Has("copy_from") {
		if _, err := env.InitJournal(ctx, iss, c.User, ""); err != nil {
			return fail("init journal", err)
		}
		// Issue.visible.find(params[:copy_from])
		var src *issues.Issue
		if id, ok := strictID(cf); ok {
			if src, err = env.Find(ctx, id); err != nil {
				return fail("copy from", err)
			}
		}
		if src != nil {
			vis, err := env.Visible(ctx, src, c.User)
			if err != nil {
				return fail("copy from visible", err)
			}
			if !vis {
				src = nil
			}
		}
		if src == nil {
			c.Render404("")
			return nil
		}
		srcProject, err := env.ProjectOf(ctx, src)
		if err != nil {
			return fail("copy from project", err)
		}
		if !c.AllowedTo(domain.Perm("copy_issues"), srcProject) {
			c.DenyAccess()
			return nil
		}
		st.copyFrom = src
		st.linkCopy = a.settingYesNoAsk("link_copied_issue", c.Params().String("link_copy")) || get
		st.copyAttachments = a.settingYesNoAsk("copy_attachments_on_issue_copy", c.Params().String("copy_attachments")) || get
		st.copySubtasks = rails.IsPresent(c.Params().String("copy_subtasks")) || get
		st.copyWatchers = c.Project != nil && c.AllowedTo(domain.Perm("add_issue_watchers"), c.Project)
		if err := env.CopyFrom(ctx, iss, src, issues.CopyOptions{NoAttachments: !st.copyAttachments, NoSubtasks: !st.copySubtasks,
			NoWatchers: !st.copyWatchers, NoLink: !st.linkCopy}); err != nil {
			return fail("copy from", err)
		}
		pid := ""
		if src.ParentID != nil {
			pid = strconv.FormatInt(*src.ParentID, 10)
		}
		if err := env.SetParentIssueID(ctx, iss, pid); err != nil {
			return fail("parent issue", err)
		}
	}
	// @issue.project = @project
	if err := env.SetProject(ctx, iss, c.Project, false); err != nil {
		return fail("set project", err)
	}
	if get && iss.ProjectID == 0 {
		ps, err := env.AllowedTargetProjects(ctx, iss, c.User, "*")
		if err != nil {
			return fail("allowed target projects", err)
		}
		if len(ps) > 0 {
			if err := env.SetProject(ctx, iss, ps[0], false); err != nil {
				return fail("set project", err)
			}
		}
	}
	if iss.AuthorID == 0 {
		iss.AuthorID = c.User.ID
	}
	if iss.StartDate == nil && a.Settings.Bool("default_issue_start_date_to_creation_date") {
		t := a.newIssueLookup(c).userToday()
		iss.SetStartDate(&t)
	}

	attrs := issueParamsOf(c.Params().Map("issue"))
	if attrs == nil {
		attrs = issues.Params{}
	}
	if c.Action == "new" && c.Params().Has("was_default_status") {
		if sid, ok := attrs["status_id"]; ok && rails.ToS(sid) == c.Params().String("was_default_status") {
			delete(attrs, "status_id")
		}
	}
	if c.Action == "new" && c.Params().String("form_update_triggered_by") == "issue_project_id" {
		delete(attrs, "fixed_version_id")
	}
	if v, ok := attrs["assigned_to_id"]; ok && rails.ToS(v) == "me" {
		attrs["assigned_to_id"] = strconv.FormatInt(c.User.ID, 10)
	}
	if err := env.SafeAssign(ctx, iss, attrs, c.User); err != nil {
		return fail("safe assign", err)
	}

	if iss.ProjectID != 0 {
		p, err := env.ProjectOf(ctx, iss)
		if err != nil {
			return fail("project", err)
		}
		if iss.TrackerID == 0 {
			ts, err := env.AllowedTargetTrackers(ctx, iss, c.User)
			if err != nil {
				return fail("allowed trackers", err)
			}
			if len(ts) > 0 {
				if err := env.SetTracker(ctx, iss, ts[0]); err != nil {
					return fail("set tracker", err)
				}
			}
		}
		if iss.TrackerID == 0 {
			pts, err := env.ProjectTrackers(ctx, p)
			if err != nil {
				return fail("project trackers", err)
			}
			if len(pts) > 0 {
				c.RenderError(http.StatusForbidden, c.L("error_no_tracker_allowed_for_new_issue_in_project"))
			} else {
				c.RenderError(http.StatusInternalServerError, c.L("error_no_tracker_in_project"))
			}
			return nil
		}
		if s, err := env.StatusOf(ctx, iss); err != nil {
			return fail("status", err)
		} else if s == nil {
			c.RenderError(http.StatusInternalServerError, c.L("error_no_default_issue_status"))
			return nil
		}
	} else if get {
		c.RenderError(http.StatusForbidden, c.L("error_no_projects_with_tracker_allowed_for_new_issue"))
		return nil
	}
	if iss.ProjectID != 0 {
		allowed, err := env.NewStatusesAllowedTo(ctx, iss, c.User, false)
		if err != nil {
			return fail("allowed statuses", err)
		}
		st.allowed = allowed
	}
	return st
}

// strictID は Issue.find(id) の id（数字のみ）。
func strictID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// settingYesNoAsk は link_copy? / copy_attachments?（設定が 'yes' / 'no' / 'ask'）。
func (a *App) settingYesNoAsk(name, param string) bool {
	switch a.Settings.String(name) {
	case "yes":
		return true
	case "ask":
		return param == "1"
	}
	return false
}

// IssuesNew は IssuesController#new（html / js の update_form）。
func (a *App) IssuesNew(c *Req) {
	st := a.buildNewIssueFromParams(c)
	if st == nil {
		return
	}
	switch formatOf(c) {
	case "js":
		a.renderIssueNewJS(c, st)
	case "html":
		opts := RenderOptions{}
		if httpx.IsXHR(c.R) {
			opts.Layout = noLayout
		}
		a.renderIssueNew(c, st, opts)
	default:
		renderUnknownFormat(c)
	}
}

// newFormData は new.html.erb / new.js.erb / _form のデータ。
func (a *App) newFormData(c *Req, st *issueNewState) (map[string]any, *issueLookup) {
	l := a.newIssueLookup(c)
	m := l.modelFor(st.iss)
	v := &issueShowView{l: l, M: m, AllowedStatuses: st.allowed}
	f := a.newIssueEditForm(c, l, v)
	f.saved = st.saved
	f.ErrorMessages = st.iss.Errors.FullMessages(c.Loc.L)
	data := map[string]any{"V": v, "F": f, "M": m}
	if st.copyFrom != nil {
		data["CopyFromParam"] = c.Params().String("copy_from")
		data["AskLinkCopy"] = a.Settings.String("link_copied_issue") == "ask"
		data["LinkCopy"] = st.linkCopy
		hasAtt := len(l.issueAttachments(st.copyFrom.ID)) > 0
		data["AskCopyAttachments"] = a.Settings.String("copy_attachments_on_issue_copy") == "ask" && hasAtt
		data["CopyAttachments"] = st.copyAttachments
		leaf, err := st.env.Leaf(c.Ctx(), st.copyFrom)
		l.fail(err)
		data["AskCopySubtasks"] = !leaf
		data["CopySubtasks"] = st.copySubtasks
	}
	is := c.Params().Map("issue")
	data["ShowFollow"] = c.Params().Has("back_url") && is != nil && is.Has("parent_issue_id")
	return data, l
}

func (a *App) renderIssueNew(c *Req, st *issueNewState, opts RenderOptions) {
	data, l := a.newFormData(c, st)
	if l.err != nil {
		a.internalError(c, "issue new", l.err)
		return
	}
	c.Render("issues/new", data, opts)
}

func (a *App) renderIssueNewJS(c *Req, st *issueNewState) {
	data, l := a.newFormData(c, st)
	trig := c.Params().String("form_update_triggered_by")
	data["TriggeredBy"] = trig
	if trig == "issue_category_id" {
		// escape_javascript(@issue.category.try(:assigned_to).try(:name)).presence || '&nbsp;'
		html := template.HTML("&nbsp;")
		if cat, err := st.env.Category(c.Ctx(), st.iss.CategoryID); err != nil {
			l.fail(err)
		} else if cat != nil && cat.ProjectID == st.iss.ProjectID && cat.AssignedToID != nil {
			// チケットのプロジェクトのカテゴリに限る
			if u := l.principal(*cat.AssignedToID); u != nil {
				if n := l.principalName(u); n != "" {
					// <%= escape_javascript(name) %> は ERB が HTML エスケープする（名前は .html() に渡る）
					html = template.HTML(rails.EscapeString(rails.EscapeJavascriptString(n)))
				}
			}
		}
		data["CategoryAssigneeHTML"] = html
	}
	if l.err != nil {
		a.internalError(c, "issue new js", l.err)
		return
	}
	c.Render("issues/new", data, RenderOptions{Format: "js"})
}

// IssuesCreate は IssuesController#create（html / api）。
func (a *App) IssuesCreate(c *Req) {
	st := a.buildNewIssueFromParams(c)
	if st == nil {
		return
	}
	ctx := c.Ctx()
	iss, env := st.iss, st.env
	api := httpx.IsAPIRequest(c.R)
	// unless User.current.allowed_to?(:add_issues, @issue.project, :global => true)
	var p *domain.Project
	if iss.ProjectID != 0 {
		var err error
		if p, err = env.ProjectOf(ctx, iss); err != nil {
			a.internalError(c, "project", err)
			return
		}
	}
	allowed := false
	if p != nil {
		allowed = c.AllowedTo(domain.Perm("add_issues"), p)
	} else {
		ok, err := c.Authz().AllowedToGlobally(ctx, domain.Perm("add_issues"), nil)
		if err != nil {
			a.internalError(c, "authorize", err)
			return
		}
		allowed = ok
	}
	if !allowed {
		c.DenyAccess()
		return
	}
	// @issue.save_attachments(params[:attachments] || params[:issue][:uploads])
	res, err := a.AttachmentStore.SaveAttachments(ctx, a.DB, attachmentsParam(c), c.User, c.Loc)
	if err != nil {
		a.internalError(c, "save attachments", err)
		return
	}
	st.saved = res
	for _, f := range res.Files {
		iss.AttachSaved(f.ID)
	}
	saved, sres, err := a.saveNewIssue(c, env, iss, res)
	if err != nil {
		a.internalError(c, "create issue", err)
		return
	}
	if !saved {
		iss.DetachSavedAttachments()
		if api {
			c.RenderAPIErrors(iss.Errors.FullMessages(c.Loc.L)...)
			return
		}
		if iss.ProjectID == 0 {
			c.RenderError(http.StatusUnprocessableEntity, "")
			return
		}
		// 保存に失敗したときは issue.project 等を表示用に読み直さずそのまま再描画する
		a.renderIssueNew(c, st, RenderOptions{})
		return
	}
	a.dispatchIssueNotifications(c, sres)
	if api {
		row, err := issueRowByID(c, a, iss.ID)
		if err != nil {
			a.internalError(c, "reload issue", err)
			return
		}
		c.setLocal(ctxIssue, row)
		c.W.Header().Set("Location", issueURL(c, iss.ID))
		a.issuesShowAPIStatus(c, http.StatusCreated)
		return
	}
	if w := res.WarningNotSaved(c.Loc); w != "" {
		c.Flash().SetWarning(w)
	}
	link := rails.LinkTo("#"+strconv.FormatInt(iss.ID, 10), "/issues/"+strconv.FormatInt(iss.ID, 10), rails.NewHash("title", iss.Subject))
	c.Flash().SetNotice(c.L("notice_issue_successful_create", map[string]any{"id": string(link)}))
	a.redirectAfterCreate(c, iss)
}

// saveNewIssue は @issue.save（未解決の添付トークンは warn_about_failed_attachments の検証エラー）。
func (a *App) saveNewIssue(c *Req, env *issues.Env, iss *issues.Issue, res *attachments.SaveResult) (bool, *issues.SaveResult, error) {
	ctx := c.Ctx()
	if msg := res.FailedMessage(c.Loc); msg != "" {
		if _, err := env.Validate(ctx, iss); err != nil {
			return false, nil, err
		}
		iss.Errors.List = append([]domain.ValidationError{{Attr: "base", Message: msg}}, iss.Errors.List...)
		return false, nil, nil
	}
	var ok bool
	var sres *issues.SaveResult
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		if err := persistSavedAttachments(c, tx, res); err != nil {
			return err
		}
		var err error
		ok, sres, err = env.WithQ(tx).Save(ctx, iss)
		if errors.Is(err, issues.ErrStale) {
			ok, err = false, nil
		}
		if err == nil && !ok {
			return errIssueRollback
		}
		return err
	})
	if errors.Is(err, errIssueRollback) {
		return false, nil, nil
	}
	return ok, sres, err
}

// persistSavedAttachments は saved_attachments の filename / description / content_type の変更を保存する
// （attach_saved_attachments の attachments << attachment。コンテナの紐付けは issues パッケージが行う）。
func persistSavedAttachments(c *Req, q db.Queryer, res *attachments.SaveResult) error {
	if res == nil {
		return nil
	}
	for _, f := range res.Files {
		if err := repository.UpdateAttachment(c.Ctx(), q, f); err != nil {
			return err
		}
	}
	return nil
}

// redirectAfterCreate は IssuesController#redirect_after_create。
func (a *App) redirectAfterCreate(c *Req, iss *issues.Issue) {
	switch {
	case c.Params().Has("continue"):
		// url_params = {:issue => {:tracker_id, :parent_issue_id(, :project_id)}.compact, :back_url => ...}（to_query はソート済み）
		var parts []string
		add := func(k, v string) { parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v)) }
		if !c.Params().Present("project_id") {
			add("issue[project_id]", strconv.FormatInt(iss.ProjectID, 10))
		}
		if pid := iss.ParentIssueID(); pid != "" {
			add("issue[parent_issue_id]", pid)
		}
		add("issue[tracker_id]", strconv.FormatInt(iss.TrackerID, 10))
		if b := c.Params().String("back_url"); rails.IsPresent(b) {
			add("back_url", b)
		}
		slices.Sort(parts)
		path := "/issues/new"
		if c.Params().Present("project_id") {
			p, err := a.writeIssuesEnv(c, a.DB).ProjectOf(c.Ctx(), iss)
			if err != nil || p == nil {
				a.internalError(c, "project", err)
				return
			}
			path = "/projects/" + p.Identifier + "/issues/new"
		}
		if len(parts) > 0 {
			path += "?" + strings.Join(parts, "&")
		}
		c.Redirect(path)
	case c.Params().Has("follow"):
		c.Redirect("/issues/" + strconv.FormatInt(iss.ID, 10))
	default:
		c.RedirectBackOrDefault("/issues/"+strconv.FormatInt(iss.ID, 10), false)
	}
}

// ---------------------------------------------------------------- edit / update

// issueEditState は update_issue_from_params が設定するインスタンス変数。
type issueEditState struct {
	env *issues.Env
	iss *issues.Issue
	// tl / te / teParams は工数の Env・@time_entry・params[:time_entry]。
	tl       *timelog.Env
	te       *timelog.Entry
	teParams *httpx.Params
	allowed  []*domain.IssueStatus
	saved    *attachments.SaveResult

	conflict         bool
	conflictJournals []*issues.Journal
}

// updateIssueFromParams は IssuesController#update_issue_from_params（false なら描画・リダイレクト済み）。
func (a *App) updateIssueFromParams(c *Req) *issueEditState {
	ctx := c.Ctx()
	env := a.writeIssuesEnv(c, a.DB)
	row := c.currentIssue()
	iss, err := env.Load(ctx, row.ID)
	if err != nil {
		a.internalError(c, "load issue", err)
		return nil
	}
	st := &issueEditState{env: env, iss: iss}
	// @time_entry = TimeEntry.new(:issue => @issue, :project => @issue.project)
	st.tl = a.timelogEnv(c)
	st.teParams = c.Params().Map("time_entry")
	p, err := env.ProjectOf(ctx, iss)
	if err != nil {
		a.internalError(c, "project", err)
		return nil
	}
	issueID := iss.ID
	if st.te, err = st.tl.New(ctx, p, &issueID); err != nil {
		a.internalError(c, "time entry", err)
		return nil
	}
	if st.teParams != nil {
		if err := st.tl.SafeAssign(ctx, st.te, st.teParams, c.User); err != nil {
			a.internalError(c, "time entry", err)
			return nil
		}
	}
	if _, err := env.InitJournal(ctx, iss, c.User, ""); err != nil {
		a.internalError(c, "init journal", err)
		return nil
	}
	attrs := issueParamsOf(c.Params().Map("issue"))
	if attrs != nil {
		if v, ok := attrs["assigned_to_id"]; ok && rails.ToS(v) == "me" {
			attrs["assigned_to_id"] = strconv.FormatInt(c.User.ID, 10)
		}
		switch c.Params().String("conflict_resolution") {
		case "overwrite":
			delete(attrs, "lock_version")
		case "add_notes":
			sliced := issues.Params{}
			for _, k := range []string{"notes", "private_notes"} {
				if v, ok := attrs[k]; ok {
					sliced[k] = v
				}
			}
			attrs = sliced
		case "cancel":
			c.Redirect("/issues/" + strconv.FormatInt(iss.ID, 10))
			return nil
		}
	}
	attrs = issues.ReplaceNoneValuesWithBlank(attrs)
	if err := env.SafeAssign(ctx, iss, attrs, c.User); err != nil {
		a.internalError(c, "safe assign", err)
		return nil
	}
	if st.allowed, err = env.NewStatusesAllowedTo(ctx, iss, c.User, false); err != nil {
		a.internalError(c, "allowed statuses", err)
		return nil
	}
	return st
}

// editFormData は edit.html.erb / edit.js.erb / _edit のデータ。
func (a *App) editFormData(c *Req, st *issueEditState) (map[string]any, *issueLookup) {
	l := a.newIssueLookup(c)
	m := l.modelFor(st.iss)
	v := &issueShowView{l: l, M: m, AllowedStatuses: st.allowed}
	f := a.newIssueEditForm(c, l, v)
	f.saved = st.saved
	if f.TimeEntry != nil {
		f.TimeEntry.setInput(st.te)
	}
	f.ErrorMessages = st.iss.Errors.FullMessages(c.Loc.L)
	if st.te != nil {
		f.ErrorMessages = append(f.ErrorMessages, st.te.Errors.FullMessages(c.Loc)...)
	}
	f.Conflict = st.conflict
	for _, j := range st.conflictJournals {
		l.preloadPrincipals([]int64{j.UserID})
		f.ConflictJournals = append(f.ConflictJournals, &journalView{Journal: j, l: l, issue: m, User: l.principal(j.UserID)})
	}
	trackerWas := ""
	if t := l.tracker(st.iss.TrackerIDWas()); t != nil {
		trackerWas = t.Name
	}
	data := map[string]any{"V": v, "F": f, "M": m, "TrackerWas": trackerWas,
		"CanLogTime": c.AllowedTo(domain.Perm("log_time"), m.Project)}
	return data, l
}

// IssuesEdit は IssuesController#edit（html / js の update_form）。
func (a *App) IssuesEdit(c *Req) {
	st := a.updateIssueFromParams(c)
	if st == nil {
		return
	}
	data, l := a.editFormData(c, st)
	if l.err != nil {
		a.internalError(c, "issue edit", l.err)
		return
	}
	switch formatOf(c) {
	case "js":
		c.Render("issues/edit", data, RenderOptions{Format: "js"})
	case "html":
		c.Render("issues/edit", data)
	default:
		renderUnknownFormat(c)
	}
}

// errStaleIssue は save_issue_with_child_records の ActiveRecord::StaleObjectError。
var errStaleIssue = errors.New("stale issue")

// errIssueRollback は ActiveRecord::Rollback。
var errIssueRollback = errors.New("issue rollback")

// IssuesUpdate は IssuesController#update（html / api）。
func (a *App) IssuesUpdate(c *Req) {
	st := a.updateIssueFromParams(c)
	if st == nil {
		return
	}
	ctx := c.Ctx()
	iss, env := st.iss, st.env
	api := httpx.IsAPIRequest(c.R)
	atts := attachmentsParam(c)
	addable, err := env.AttachmentsAddable(ctx, iss, c.User)
	if err != nil {
		a.internalError(c, "attachments addable", err)
		return
	}
	var res *attachments.SaveResult
	if addable {
		if res, err = a.AttachmentStore.SaveAttachments(ctx, a.DB, atts, c.User, c.Loc); err != nil {
			a.internalError(c, "save attachments", err)
			return
		}
		st.saved = res
		for _, f := range res.Files {
			iss.AttachSaved(f.ID)
		}
	} else if n := attachmentsParamSize(atts); n > 0 {
		c.Flash().SetWarning(attachments.WarningNotSaved(c.Loc, n))
	}

	saved, sres, err := a.saveIssueWithChildRecords(c, st, res)
	if errors.Is(err, errStaleIssue) {
		iss.DetachSavedAttachments()
		st.conflict = true
		if lj := c.Params().String("last_journal_id"); c.Params().Has("last_journal_id") {
			js, err := env.JournalsAfter(ctx, iss, customRubyToI(lj))
			if err != nil {
				a.internalError(c, "journals after", err)
				return
			}
			p, _ := env.ProjectOf(ctx, iss)
			viewPrivate := c.AllowedTo(domain.Perm("view_private_notes"), p)
			for _, j := range js {
				if j.PrivateNotes && !viewPrivate {
					continue
				}
				st.conflictJournals = append(st.conflictJournals, j)
			}
		}
		err = nil
	}
	if err != nil {
		a.internalError(c, "update issue", err)
		return
	}
	if saved {
		a.dispatchIssueNotifications(c, sres)
		if w := res.WarningNotSaved(c.Loc); w != "" {
			c.Flash().SetWarning(w)
		}
		if j := iss.CurrentJournal(); j != nil && j.Persisted() && !c.Params().Has("no_flash") {
			c.Flash().SetNotice(c.L("notice_successful_update"))
		}
		if api {
			c.RenderAPIOK()
			return
		}
		c.Flash().Set(flashPrevNextKey, previousAndNextIssueIDsParam(c))
		c.RedirectBackOrDefault("/issues/"+strconv.FormatInt(iss.ID, 10), false)
		return
	}
	if api {
		c.RenderAPIErrors(iss.Errors.FullMessages(c.Loc.L)...)
		return
	}
	data, l := a.editFormData(c, st)
	if l.err != nil {
		a.internalError(c, "issue edit", l.err)
		return
	}
	c.Render("issues/edit", data)
}

func customRubyToI(s string) int64 { return httpx.RubyToI(s) }

// previousAndNextIssueIDsParam は previous_and_next_issue_ids_params（flash 用のクエリ文字列。issues_show.go と同じ形式）。
func previousAndNextIssueIDsParam(c *Req) string {
	v := url.Values{}
	for _, k := range []string{"prev_issue_id", "next_issue_id", "issue_position", "issue_count"} {
		v.Set(k, c.Params().String(k))
	}
	return "prev_issue_id=" + url.QueryEscape(v.Get("prev_issue_id")) + "&next_issue_id=" + url.QueryEscape(v.Get("next_issue_id")) +
		"&issue_position=" + url.QueryEscape(v.Get("issue_position")) + "&issue_count=" + url.QueryEscape(v.Get("issue_count"))
}

// saveIssueWithChildRecords は IssuesController#save_issue_with_child_records（作業時間の記録とチケットの保存を
// 1 トランザクションで行う）。楽観ロックの衝突は errStaleIssue。
func (a *App) saveIssueWithChildRecords(c *Req, st *issueEditState, res *attachments.SaveResult) (bool, *issues.SaveResult, error) {
	ctx := c.Ctx()
	iss := st.iss
	p, err := st.env.ProjectOf(ctx, iss)
	if err != nil {
		return false, nil, err
	}
	logTime := timeEntryParamsPresent(st.teParams) && c.AllowedTo(domain.Perm("log_time"), p)
	var saved bool
	var sres *issues.SaveResult
	err = a.DB.WithTx(ctx, func(tx *db.Tx) error {
		env := st.env.WithQ(tx)
		var extra []domain.ValidationError
		if logTime {
			te := st.te
			pid, iid, uid := iss.ProjectID, iss.ID, c.User.ID
			te.ProjectID, te.IssueID, te.AuthorID = &pid, &iid, &uid
			te.SetUser(c.User)
			today := st.tl.Today()
			te.SpentOn = &today
			tl := st.tl.WithQ(tx)
			if err := tl.SafeAssign(ctx, te, st.teParams, c.User); err != nil {
				return err
			}
			ok, err := tl.Save(ctx, te)
			if err != nil {
				return err
			}
			if !ok {
				// has_many :time_entries の関連の検証（validate_collection_association）
				extra = append(extra, domain.ValidationError{Attr: "time_entries", Key: "invalid"})
			}
		}
		if msg := res.FailedMessage(c.Loc); msg != "" {
			extra = append(extra, domain.ValidationError{Attr: "base", Message: msg})
		}
		if len(extra) > 0 {
			if _, err := env.Validate(ctx, iss); err != nil {
				return err
			}
			iss.Errors.List = append(extra, iss.Errors.List...)
			return errIssueRollback
		}
		if err := persistSavedAttachments(c, tx, res); err != nil {
			return err
		}
		ok, r, err := env.Save(ctx, iss)
		if errors.Is(err, issues.ErrStale) {
			return errStaleIssue
		}
		if err != nil {
			return err
		}
		if !ok {
			return errIssueRollback
		}
		saved, sres = true, r
		return nil
	})
	if err == nil && saved && logTime && st.te != nil && st.te.ID != 0 {
		// 作業時間の after_create_commit
		a.triggerWebhookByID(c, webhook.TypeTimeEntry, webhook.ActionCreated, st.te.ID)
	}
	if errors.Is(err, errIssueRollback) {
		return false, nil, nil
	}
	return saved, sres, err
}
