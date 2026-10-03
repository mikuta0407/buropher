// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/csvexport"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// UsersController（app/controllers/users_controller.rb）。layout 'admin'（show は base）。
var UsersController = &Controller{Name: "users", MainMenu: false}

// routesUsers は users コントローラのルートを登録する。
func (a *App) routesUsers(r Router) {
	adm := RequireAdmin()
	api := AcceptAPIAuth()
	// resources :users do collection { delete 'bulk_destroy'; post :bulk_lock; post :bulk_unlock } end
	a.Handle(r, http.MethodDelete, "/users/bulk_destroy", UsersController, "bulk_destroy", a.UsersBulkDestroy, adm)
	a.Handle(r, http.MethodPost, "/users/bulk_lock", UsersController, "bulk_lock", a.UsersBulkLock, adm)
	a.Handle(r, http.MethodPost, "/users/bulk_unlock", UsersController, "bulk_unlock", a.UsersBulkUnlock, adm)
	a.Handle(r, http.MethodGet, "/users", UsersController, "index", a.UsersIndex, api, adm)
	a.Handle(r, http.MethodPost, "/users", UsersController, "create", a.UsersCreate, api, adm)
	a.Handle(r, http.MethodGet, "/users/new", UsersController, "new", a.UsersNew, adm)
	a.Handle(r, http.MethodGet, "/users/{id}/edit", UsersController, "edit", a.UsersEdit, adm, a.findUser(true))
	a.Handle(r, http.MethodGet, "/users/{id}", UsersController, "show", a.UsersShow, api, a.findUser(false))
	for _, m := range []string{http.MethodPatch, http.MethodPut} {
		a.Handle(r, m, "/users/{id}", UsersController, "update", a.UsersUpdate, api, adm, a.findUser(true))
	}
	a.Handle(r, http.MethodDelete, "/users/{id}", UsersController, "destroy", a.UsersDestroy, api, adm, a.findUser(true))
}

// userCtxKey は find_user の結果（@user）を Req に持たせるためのキー。
type userCtxKey struct{}

// findUser は UsersController#find_user(logged)。
func (a *App) findUser(logged bool) ActionOption {
	return Before(func(c *Req) {
		id := c.Params().String("id")
		var u *domain.User
		var err error
		if id == "current" {
			if !c.RequireLogin() {
				return
			}
			u = c.User
		} else {
			n, perr := strconv.ParseInt(id, 10, 64)
			if perr != nil {
				c.Render404("")
				return
			}
			if logged {
				u, err = repository.GetLoggedUser(c.Ctx(), a.DB, n)
			} else {
				u, err = repository.GetUser(c.Ctx(), a.DB, n)
			}
		}
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				a.logger().Error("find user", "err", err)
			}
			c.Render404("")
			return
		}
		c.setValue(userCtxKey{}, u)
	})
}

// adminLayoutXHR は layout 'admin'（XHR なら layout なし）。
func adminLayoutXHR(c *Req) RenderOptions {
	if httpx.IsXHR(c.R) {
		return RenderOptions{Layout: view.NoLayout}
	}
	return RenderOptions{Layout: "admin"}
}

// ---------------------------------------------------------------- index

// UsersIndex は users#index（GET /users(.:format)）。
func (a *App) UsersIndex(c *Req) {
	format := httpx.Format(c.R)
	ctx := c.Ctx()
	q, env, err := a.retrieveUserQuery(c, format != "csv")
	if err != nil {
		a.serverError(c, err)
		return
	}
	if httpx.IsAPIRequest(c.R) || format == "csv" {
		// API backwards compatibility: handle legacy filter parameters
		if name := c.Params().String("name"); strings.TrimSpace(name) != "" {
			_ = q.AddFilter(ctx, "name", "~", []string{name})
		}
		if g := c.Params().String("group_id"); strings.TrimSpace(g) != "" {
			_ = q.AddFilter(ctx, "is_member_of_group", "=", []string{g})
		}
	}
	errMsgs, err := q.Errors(ctx)
	if err != nil {
		a.serverError(c, err)
		return
	}
	view := &userQueryView{q: q, env: env, c: c, app: a}
	if len(errMsgs) > 0 {
		switch {
		case httpx.IsAPIRequest(c.R):
			c.RenderAPIErrors(errMsgs...)
		case format == "csv":
			httpx.Head(c.W, c.R, http.StatusUnprocessableEntity)
			c.Halt()
		default:
			c.Render("users/index", a.userIndexData(c, view, nil, nil, 0, errMsgs), adminLayoutXHR(c))
		}
		return
	}
	count64, err := q.Count(ctx)
	if err != nil {
		a.serverError(c, err)
		return
	}
	count := int(count64)
	switch {
	case httpx.IsAPIRequest(c.R):
		offset, limit := c.APIOffsetAndLimit()
		users, err := a.queryUsers(c, q, limit, offset)
		if err != nil {
			a.serverError(c, err)
			return
		}
		cfs, err := a.loadPrincipalCustomValues(c, "user", users)
		if err != nil {
			a.serverError(c, err)
			return
		}
		c.RenderAPI(0, func(b apibuilder.Builder) {
			b.Array("users", c.APIMeta(apibuilder.A("total_count", count, "offset", offset, "limit", limit)), func() {
				for _, u := range users {
					b.Object("user", func() {
						a.userAPIFields(c, b, u, cfs[u.ID])
					})
				}
			})
		})
	case format == "csv":
		users, err := a.queryUsers(c, q, 0, 0)
		if err != nil {
			a.serverError(c, err)
			return
		}
		if err := a.prepareUserView(c, view, users); err != nil {
			a.serverError(c, err)
			return
		}
		view.csv = true
		a.sendUsersCSV(c, view, users)
	default:
		limit := c.PerPageOption()
		pages := pagination.New(count, limit, c.Params().String("page"))
		users, err := a.queryUsers(c, q, pages.PerPage, pages.Offset())
		if err != nil {
			a.serverError(c, err)
			return
		}
		if err := a.prepareUserView(c, view, users); err != nil {
			a.serverError(c, err)
			return
		}
		c.Render("users/index", a.userIndexData(c, view, users, pages, count, nil), adminLayoutXHR(c))
	}
}

// queryUsers は query.results_scope.limit(limit).offset(offset)（limit 0 は全件）。
func (a *App) queryUsers(c *Req, q *query.Query, limit, offset int) ([]*domain.User, error) {
	ids, err := q.IDs(c.Ctx(), query.ListOptions{Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	users, err := repository.UsersWhereIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*domain.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	out := make([]*domain.User, 0, len(ids))
	for _, id := range ids {
		if u := byID[id]; u != nil {
			out = append(out, u)
		}
	}
	return out, nil
}

// prepareUserView は列の表示に必要なカスタム値・認証方式を読み込む。
func (a *App) prepareUserView(c *Req, v *userQueryView, users []*domain.User) error {
	cfs, err := a.loadPrincipalCustomValues(c, "user", users)
	if err != nil {
		return err
	}
	v.cvs = cfs
	srcs, err := repository.ListAuthSources(c.Ctx(), a.DB)
	if err != nil {
		return err
	}
	v.srcs = map[int64]string{}
	for _, s := range srcs {
		v.srcs[s.ID] = s.Name
	}
	return nil
}

// serverError は予期しないエラー（500）。
func (a *App) serverError(c *Req, err error) {
	a.logger().Error("internal error", "controller", c.Controller.Name, "action", c.Action, "err", err)
	c.RenderError(http.StatusInternalServerError, err.Error())
}

// userIndexData は users/index のデータ。
func (a *App) userIndexData(c *Req, v *userQueryView, users []*domain.User, pages *pagination.Paginator, count int, errs []string) map[string]any {
	return map[string]any{
		"Query":        v,
		"Users":        users,
		"Pages":        pages,
		"Count":        count,
		"QueryErrors":  errs,
		"CanImport":    c.AllowedToGlobally(domain.Perm("import_users")),
		"CanSaveQuery": c.AllowedToGlobally(domain.Perm("save_queries")),
		"BackURL":      helper.URLWithQuery(c.R.URL.Path, pageQueryParameters(c)),
	}
}

// sendUsersCSV は query_to_csv（Redmine::Export::CSV）で /users.csv を返す。
func (a *App) sendUsersCSV(c *Req, v *userQueryView, users []*domain.User) {
	p := c.Params()
	w := csvexport.New(csvexport.Options{
		Separator:       firstNonEmpty(p.String("field_separator"), c.L("general_csv_separator")),
		Encoding:        p.String("encoding"),
		DefaultEncoding: c.L("general_csv_encoding"),
	})
	cols, err := v.q.Columns(c.Ctx())
	if err != nil {
		a.serverError(c, err)
		return
	}
	var head []string
	for _, col := range cols {
		head = append(head, v.ColumnCaption(col))
	}
	w.Strings(head...)
	for _, u := range users {
		var row []csvexport.Field
		for _, col := range cols {
			row = append(row, csvexport.S(v.Value(col, u)))
		}
		w.Row(row...)
	}
	name := p.String("query_name")
	if name == "" || name == "_" {
		name = "users"
	}
	c.halted = true
	c.W.Header().Set("Content-Type", "text/csv; header=present")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+strings.ToLower(name)+`.csv"; filename*=UTF-8''`+strings.ToLower(name)+".csv")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(w.Bytes())
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- show

// UsersShow は users#show（GET /users/:id(.:format)、layout base）。
func (a *App) UsersShow(c *Req) {
	u := c.value(userCtxKey{}).(*domain.User)
	ctx := c.Ctx()
	cond, err := c.Authz().PrincipalVisibleCondition(ctx)
	if err != nil {
		a.serverError(c, err)
		return
	}
	visible, err := repository.PrincipalVisible(ctx, a.DB, cond, u.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if !visible {
		c.Render404("")
		return
	}
	// show projects based on current user visibility
	projCond, err := c.Authz().VisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		a.serverError(c, err)
		return
	}
	memberships, err := a.membershipRows(c, u.ID, projCond, false)
	if err != nil {
		a.serverError(c, err)
		return
	}
	tree := append([]*membershipRow(nil), memberships...)
	if err := a.setTreeLevels(c, tree); err != nil {
		a.serverError(c, err)
		return
	}
	issueCond, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		a.serverError(c, err)
		return
	}
	groupIDs, err := repository.UserGroupIDs(ctx, a.DB, u.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	assigned := append([]int64{u.ID}, groupIDs...)
	counts := map[string]int{}
	for _, x := range []struct {
		key      string
		assigned []int64
		author   int64
		open     bool
	}{
		{"assigned_total", assigned, 0, false}, {"assigned_open", assigned, 0, true},
		{"reported_total", nil, u.ID, false}, {"reported_open", nil, u.ID, true},
	} {
		if x.author == 0 && len(x.assigned) == 0 {
			continue
		}
		n, err := repository.CountIssues(ctx, a.DB, issueCond, x.assigned, x.author, x.open)
		if err != nil {
			a.serverError(c, err)
			return
		}
		counts[x.key] = n
	}
	cfs, err := a.loadPrincipalCustomValues(c, "user", []*domain.User{u})
	if err != nil {
		a.serverError(c, err)
		return
	}
	if httpx.IsAPIRequest(c.R) {
		a.renderUserShowAPI(c, u, memberships, cfs[u.ID], 0)
		return
	}
	pref, err := repository.GetUserPreference(ctx, a.DB, u.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	var groups []*domain.Group
	if c.User.ID == u.ID || c.User.IsAdmin() {
		for _, gid := range groupIDs {
			if g, err := repository.GetGroup(ctx, a.DB, gid); err == nil {
				groups = append(groups, g)
			}
		}
	}
	var emails []string
	if !pref.HideMail {
		emails = append(emails, u.Mail)
		addrs, err := repository.UserEmailAddresses(ctx, a.DB, u.ID, true)
		if err != nil {
			a.serverError(c, err)
			return
		}
		for _, ad := range addrs {
			emails = append(emails, ad.Address)
		}
	}
	assignedParam := make([]string, len(assigned))
	for i, id := range assigned {
		assignedParam[i] = itoa(id)
	}
	// Redmine::Activity::Fetcher.new(User.current, :author => @user).events(nil, nil, :limit => 10)
	eventsByDay, err := a.userActivityEvents(c, u)
	if err != nil {
		a.serverError(c, err)
		return
	}
	var activityURL string
	atomParams := rails.NewHash("user_id", u.ID)
	if len(eventsByDay) > 0 {
		activityURL = helper.URLWithQuery("/activity", rails.NewHash("user_id", u.ID, "from", eventsByDay[0].Day.Format("2006-01-02")))
		if k := c.AtomKey(); k != "" {
			atomParams.Set("key", k)
		}
	}
	c.Render("users/show", map[string]any{
		"User":            u,
		"Pref":            pref,
		"Emails":          emails,
		"CustomValues":    cfs[u.ID],
		"Counts":          counts,
		"AssignedToIDs":   strings.Join(assignedParam, "|"),
		"Memberships":     tree,
		"Groups":          groups,
		"EventsByDay":     eventsByDay,
		"ActivityURL":     activityURL,
		"ActivityAtomURL": helper.URLWithQuery("/activity.atom", atomParams),
		// url_for(:controller => 'activities', :action => 'index', :user_id => @user, :format => :atom, :key => ...)
		// は :id => nil を指定しないため現在のパスパラメータ id が残り /projects/:id/activity.atom になる（Redmine と同じ）
		"ActivityFeedURL": httpx.RequestBaseURL(c.R) + urlroot.Path(helper.URLWithQuery("/projects/"+url.PathEscape(c.Params().String("id"))+"/activity.atom", atomParams)),
	})
}

// renderUserShowAPI は users/show.api.rsb。
func (a *App) renderUserShowAPI(c *Req, u *domain.User, memberships []*membershipRow, cvs []principalCustomValue, status int) {
	ctx := c.Ctx()
	cur := c.User
	self := cur.ID == u.ID && cur.Logged()
	pref, _ := repository.GetUserPreference(ctx, a.DB, u.ID)
	var apiKey string
	if cur.IsAdmin() || (self && !cur.AuthorizedByOAuth()) {
		var err error
		if apiKey, err = repository.APIKey(ctx, a.DB, u.ID); err != nil {
			a.serverError(c, err)
			return
		}
	}
	var groups []*domain.Group
	if cur.IsAdmin() && c.IncludeInAPIResponse("groups") {
		ids, _ := repository.UserGroupIDs(ctx, a.DB, u.ID)
		for _, id := range ids {
			if g, err := repository.GetGroup(ctx, a.DB, id); err == nil {
				groups = append(groups, g)
			}
		}
	}
	var src *domain.AuthSource
	if u.AuthSourceID != nil {
		srcs, _ := repository.ListAuthSources(ctx, a.DB)
		for i := range srcs {
			if srcs[i].ID == *u.AuthSourceID {
				src = &srcs[i]
			}
		}
	}
	c.RenderAPI(status, func(b apibuilder.Builder) {
		b.Object("user", func() {
			b.Value("id", u.ID)
			b.Value("login", u.Login)
			if cur.IsAdmin() || self {
				b.Value("admin", u.IsAdmin())
			}
			b.Value("firstname", u.Firstname)
			b.Value("lastname", u.Lastname)
			if cur.IsAdmin() || pref == nil || !pref.HideMail {
				b.Value("mail", nilIfEmpty(u.Mail))
			}
			b.Value("created_on", u.CreatedAt)
			b.Value("updated_on", u.UpdatedAt)
			b.Value("last_login_on", u.LastLoginAt)
			b.Value("passwd_changed_on", u.PasswordChangedAt)
			if u.Mail != "" && a.Settings.Bool("gravatar_enabled") {
				b.Value("avatar_url", gravatarAPIURL(u.Mail, a.Settings.String("gravatar_default")))
			}
			if cur.IsAdmin() || self {
				b.Value("twofa_scheme", nilIfEmpty(u.TwofaScheme))
			}
			if apiKey != "" {
				b.Value("api_key", apiKey)
			}
			if cur.IsAdmin() {
				b.Value("status", u.Status)
			}
			renderAPICustomValues(b, cvs)
			if cur.IsAdmin() && c.IncludeInAPIResponse("auth_source") && src != nil {
				b.Object("auth_source", func() {
					b.Value("id", src.ID)
					b.Value("name", src.Name)
				})
			}
			if cur.IsAdmin() && c.IncludeInAPIResponse("groups") {
				b.Array("groups", nil, func() {
					for _, g := range groups {
						b.Attrs("group", apibuilder.A("id", g.ID, "name", g.Name))
					}
				})
			}
			if c.IncludeInAPIResponse("memberships") && memberships != nil {
				renderAPIMemberships(c, b, memberships)
			}
		})
	})
}

// userAPIFields は users/index.api.rsb の 1 ユーザー分。
func (a *App) userAPIFields(c *Req, b apibuilder.Builder, u *domain.User, cvs []principalCustomValue) {
	b.Value("id", u.ID)
	b.Value("login", u.Login)
	b.Value("admin", u.IsAdmin())
	b.Value("firstname", u.Firstname)
	b.Value("lastname", u.Lastname)
	b.Value("mail", nilIfEmpty(u.Mail))
	b.Value("created_on", u.CreatedAt)
	b.Value("updated_on", u.UpdatedAt)
	b.Value("last_login_on", u.LastLoginAt)
	b.Value("passwd_changed_on", u.PasswordChangedAt)
	if a.Settings.Bool("gravatar_enabled") {
		b.Value("avatar_url", gravatarAPIURL(u.Mail, a.Settings.String("gravatar_default")))
	}
	b.Value("twofa_scheme", nilIfEmpty(u.TwofaScheme))
	b.Value("status", u.Status)
	if c.IncludeInAPIResponse("auth_source") && u.AuthSourceID != nil {
		srcs, _ := repository.ListAuthSources(c.Ctx(), a.DB)
		for _, s := range srcs {
			if s.ID == *u.AuthSourceID {
				b.Object("auth_source", func() {
					b.Value("id", s.ID)
					b.Value("name", s.Name)
				})
			}
		}
	}
	renderAPICustomValues(b, cvs)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// renderAPIMemberships は users/show.api.rsb・groups/show.api.rsb の memberships 配列。
func renderAPIMemberships(c *Req, b apibuilder.Builder, memberships []*membershipRow) {
	b.Array("memberships", nil, func() {
		for _, m := range memberships {
			if m.Project == nil {
				continue
			}
			b.Object("membership", func() {
				b.Value("id", m.Member.ID)
				b.Attrs("project", apibuilder.A("id", m.Project.ID, "name", m.Project.Name))
				b.Array("roles", nil, func() {
					for _, mr := range m.Member.MemberRoles {
						role := m.roleByID[mr.RoleID]
						if role == nil {
							continue
						}
						attrs := apibuilder.A("id", role.ID, "name", roleDisplayName(c, role))
						if mr.Inherited() {
							attrs = append(attrs, apibuilder.KV{Key: "inherited", Value: true})
						}
						b.Attrs("role", attrs)
					}
				})
			})
		}
	})
}

// roleDisplayName は Role#name（組込ロールは翻訳）。
func roleDisplayName(c *Req, r *domain.Role) string {
	switch r.Builtin {
	case domain.RoleBuiltinNonMember:
		return c.L("label_role_non_member")
	case domain.RoleBuiltinAnonymous:
		return c.L("label_role_anonymous")
	}
	return r.Name
}

// ---------------------------------------------------------------- new / create

// userFormData は users/new・users/edit のデータ。
func (a *App) userFormData(c *Req, m *userModel) (map[string]any, error) {
	ctx := c.Ctx()
	srcs, err := repository.ListAuthSources(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"User":        m,
		"AuthSources": srcs,
		"SendInfo":    c.Params().Present("send_information"),
	}
	// メール通知の選択肢（メンバーシップが無ければ selected を除く）と通知対象のプロジェクト
	var projects []*domain.Project
	if !m.newRecord {
		ms, err := repository.Memberships(ctx, a.DB, m.ID)
		if err != nil {
			return nil, err
		}
		for _, mem := range ms {
			if p, err := repository.GetProject(ctx, a.DB, mem.ProjectID); err == nil {
				projects = append(projects, p)
			}
		}
	}
	data["Projects"] = projects
	var mailOpts [][2]string
	for _, o := range domain.ValidNotificationOptions(len(projects) > 0) {
		mailOpts = append(mailOpts, [2]string{c.L(o.Label), o.Value})
	}
	data["MailOptions"] = mailOpts
	high, err := repository.HighPriorityAfterDefault(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	data["HighPriority"] = strings.ToLower(high)
	// 既定のクエリ
	for _, k := range []struct{ kind, key string }{{"issue", "IssueQueryOptions"}, {"project", "ProjectQueryOptions"}} {
		qs, err := repository.GlobalQueryOptions(ctx, a.DB, k.kind)
		if err != nil {
			return nil, err
		}
		var public, mine []any
		for _, q := range qs {
			if q.Visibility == domain.QueryVisibilityPublic {
				public = append(public, []any{q.Name, q.ID})
			} else if q.UserID != nil && *q.UserID == m.ID && !m.newRecord {
				mine = append(mine, []any{q.Name, q.ID})
			}
		}
		label := c.L("label_default_queries.for_this_user")
		if m.ID == c.User.ID && !m.newRecord {
			label = c.L("label_my_queries")
		}
		if public == nil {
			public = []any{}
		}
		if mine == nil {
			mine = []any{}
		}
		data[k.key] = []any{[]any{c.L("label_default_queries.for_all_users"), public}, []any{label, mine}}
	}
	// グループ（Group.givable.sort）
	groups, err := repository.ListGroups(ctx, a.DB, false)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(groups, func(i, j int) bool {
		x, y := strings.ToLower(groups[i].Name), strings.ToLower(groups[j].Name)
		if x != y {
			return x < y
		}
		return groups[i].Name < groups[j].Name
	})
	data["Groups"] = groups
	if !m.newRecord {
		rows, err := a.membershipRows(c, m.ID, "", true)
		if err != nil {
			return nil, err
		}
		data["Memberships"] = rows
		data["Principal"] = m.User
		data["PrincipalBase"] = "/users/" + itoa(m.ID)
		tabs := []helper.Tab{
			{Name: "general", Partial: "users/general", Label: "label_general"},
			{Name: "memberships", Partial: "users/memberships", Label: "label_project_plural"},
		}
		if len(groups) > 0 {
			tabs = append(tabs[:1], append([]helper.Tab{{Name: "groups", Partial: "users/groups", Label: "label_group_plural"}}, tabs[1:]...)...)
		}
		data["Tabs"] = tabs
		n, err := repository.CountUserEmailAddresses(ctx, a.DB, m.ID)
		if err != nil {
			return nil, err
		}
		data["ShowEmailsLink"] = n > 1 || a.Settings.Int("max_additional_emails") > 0
	}
	data["Twofa"] = a.Settings.String("twofa") != "0"
	data["ForceDefaultLanguage"] = a.Settings.Bool("force_default_language_for_loggedin")
	data["PasswordMinLength"] = a.Settings.String("password_min_length")
	var classes []string
	for _, k := range a.Settings.Strings("password_required_char_classes") {
		classes = append(classes, c.L("label_password_char_class_"+k))
	}
	data["PasswordCharClasses"] = strings.Join(classes, ", ")
	return data, nil
}

// UsersNew は users#new（GET /users/new）。
func (a *App) UsersNew(c *Req) {
	m := a.newUserModel(c)
	m.assignSafeAttributes(c.Params().Map("user"), c.User)
	data, err := a.userFormData(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.NoStore()
	c.Render("users/new", data, adminLayoutXHR(c))
}

// UsersCreate は users#create（POST /users(.:format)）。
func (a *App) UsersCreate(c *Req) {
	m := a.newUserModel(c)
	m.AdminFlag = false
	up := c.Params().Map("user")
	m.assignSafeAttributes(up, c.User)
	if m.AuthSourceID == nil && up != nil {
		// @user.password = params[:user][:password]（キーが無ければ nil）
		if v, ok := up.Get("password"); ok && v != nil {
			pw := httpx.ValueString(v)
			m.password = &pw
		}
		if v, ok := up.Get("password_confirmation"); ok && v != nil {
			conf := httpx.ValueString(v)
			m.passwordConfirmation = &conf
		}
	}
	m.assignPref(c.Params().Map("pref"))
	ok, err := a.saveUser(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if ok {
		if c.Params().Present("send_information") {
			a.deliverAccountInformation(c, m)
		}
		if httpx.IsAPIRequest(c.R) {
			c.W.Header().Set("Location", httpx.RequestBaseURL(c.R)+urlroot.Path("/users/"+itoa(m.ID)))
			u, err := repository.GetUser(c.Ctx(), a.DB, m.ID)
			if err != nil {
				a.serverError(c, err)
				return
			}
			cvs, err := a.loadPrincipalCustomValues(c, "user", []*domain.User{u})
			if err != nil {
				a.serverError(c, err)
				return
			}
			a.renderUserShowAPI(c, u, nil, cvs[u.ID], http.StatusCreated)
			return
		}
		link := rails.LinkTo(m.Login, "/users/"+itoa(m.ID), nil)
		c.Flash().SetNotice(c.L("notice_user_successful_create", map[string]any{"id": string(link)}))
		if c.Params().Present("continue") {
			c.Redirect(helper.URLWithQuery("/users/new", rails.NewHash("user", rails.NewHash("generate_password", m.generatePassword))))
		} else {
			c.Redirect("/users/" + itoa(m.ID) + "/edit")
		}
		return
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderValidationErrors(m.errors)
		return
	}
	m.password, m.passwordConfirmation = nil, nil
	data, err := a.userFormData(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.NoStore()
	c.Render("users/new", data, adminLayoutXHR(c))
}

// deliverAccountInformation は Mailer.deliver_account_information(user, password)（password は @user.password）。
func (a *App) deliverAccountInformation(c *Req, m *userModel) {
	pw := ""
	if m.password != nil {
		pw = *m.password
	}
	u := *m.User
	u.Mail = m.mail
	a.Notify.AccountInformation(c.Ctx(), &u, pw)
}

// ---------------------------------------------------------------- edit / update

// UsersEdit は users#edit（GET /users/:id/edit）。
func (a *App) UsersEdit(c *Req) {
	u := c.value(userCtxKey{}).(*domain.User)
	m, err := a.loadUserModel(c, u)
	if err != nil {
		a.serverError(c, err)
		return
	}
	a.renderUserEdit(c, m, http.StatusOK)
}

func (a *App) renderUserEdit(c *Req, m *userModel, status int) {
	data, err := a.userFormData(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.NoStore()
	opts := adminLayoutXHR(c)
	opts.Status = status
	c.Render("users/edit", data, opts)
}

// UsersUpdate は users#update（PATCH/PUT /users/:id(.:format)）。
func (a *App) UsersUpdate(c *Req) {
	u := c.value(userCtxKey{}).(*domain.User)
	m, err := a.loadUserModel(c, u)
	if err != nil {
		a.serverError(c, err)
		return
	}
	up := c.Params().Map("user")
	updatingPassword := false
	if up != nil && up.Present("password") && (m.AuthSourceID == nil || !up.Present("auth_source_id")) {
		updatingPassword = true
		pw := up.String("password")
		m.password = &pw
		if _, ok := up.Get("password_confirmation"); ok {
			conf := up.String("password_confirmation")
			m.passwordConfirmation = &conf
		}
	}
	m.assignSafeAttributes(up, c.User)
	wasActivated := m.orig.Status == domain.StatusRegistered && m.Status == domain.StatusActive
	m.assignPref(c.Params().Map("pref"))
	ok, err := a.saveUser(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if ok {
		if err := repository.SaveUserPreferenceDetail(c.Ctx(), a.DB, m.pref); err != nil {
			a.serverError(c, err)
			return
		}
		u := *m.User
		u.Mail = m.mail
		if updatingPassword {
			a.Notify.PasswordUpdated(c.Ctx(), &u, c.User, c.remoteIP())
		}
		if wasActivated {
			a.Notify.AccountActivated(c.Ctx(), &u)
		} else if m.Active() && c.Params().Present("send_information") && m.ID != c.User.ID {
			a.deliverAccountInformation(c, m)
		}
		if httpx.IsAPIRequest(c.R) {
			c.RenderAPIOK()
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		httpx.RedirectToRefererOr(c.W, c.R, "/users/"+itoa(m.ID)+"/edit", 0)
		c.Halt()
		return
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderValidationErrors(m.errors)
		return
	}
	m.password, m.passwordConfirmation = nil, nil
	a.renderUserEdit(c, m, http.StatusOK)
}

// ---------------------------------------------------------------- destroy

// ownAccountDeletable は User#own_account_deletable?。
func (a *App) ownAccountDeletable(c *Req, u *domain.User) bool {
	if !a.Settings.Bool("unsubscribe") {
		return false
	}
	if !u.IsAdmin() {
		return true
	}
	ok, err := repository.ActiveAdminExistsExcept(c.Ctx(), a.DB, u.ID)
	return err == nil && ok
}

// UsersDestroy は users#destroy（DELETE /users/:id(.:format)）。確認が無ければ確認画面を表示する。
func (a *App) UsersDestroy(c *Req) {
	u := c.value(userCtxKey{}).(*domain.User)
	if u.ID == c.User.ID && !a.ownAccountDeletable(c, u) {
		c.RenderError(http.StatusUnprocessableEntity, "")
		return
	}
	p := c.Params()
	if httpx.IsAPIRequest(c.R) || p.Present("lock") || (p.Has("confirm") && p.String("confirm") == u.Login) {
		if p.Present("lock") {
			if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
				if err := repository.SetUsersStatus(c.Ctx(), tx, []int64{u.ID}, domain.StatusLocked); err != nil {
					return err
				}
				return repository.DeleteUserTokensByActions(c.Ctx(), tx, u.ID, "recovery", "autologin", "session")
			}); err != nil {
				a.serverError(c, err)
				return
			}
			c.Flash().SetNotice(c.L("notice_successful_update"))
		} else {
			if err := a.destroyUser(c, u.ID); err != nil {
				a.serverError(c, err)
				return
			}
			c.Flash().SetNotice(c.L("notice_successful_delete"))
		}
		if httpx.IsAPIRequest(c.R) {
			c.RenderAPIOK()
			return
		}
		c.RedirectBackOrDefault("/users", false)
		return
	}
	c.Render("users/destroy", map[string]any{"User": u}, adminLayoutXHR(c))
}

// destroyUser は User#destroy（参照を匿名ユーザーへ付け替えてから削除）。
func (a *App) destroyUser(c *Req, id int64) error {
	anon := a.anonymous(c.Ctx())
	u, _ := repository.GetUser(c.Ctx(), a.DB, id)
	if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		return repository.DestroyUser(c.Ctx(), tx, id, anon.ID)
	}); err != nil {
		return err
	}
	// after_destroy :deliver_security_notification
	a.notifyUserDestroyed(c, u)
	return nil
}

// UsersBulkDestroy は users#bulk_destroy（DELETE /users/bulk_destroy）。
func (a *App) UsersBulkDestroy(c *Req) {
	users, err := repository.LoggedUsersByIDs(c.Ctx(), a.DB, idsFromParam(c.Params().Slice("ids")), c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if len(users) == 0 {
		c.Render404("")
		return
	}
	if c.Params().String("confirm") == c.L("general_text_Yes") {
		for _, u := range users {
			if err := a.destroyUser(c, u.ID); err != nil {
				a.serverError(c, err)
				return
			}
		}
		c.Flash().SetNotice(c.L("notice_successful_delete"))
		c.Redirect("/users")
		return
	}
	ids := make([]any, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	c.Render("users/bulk_destroy", map[string]any{"Users": users, "IDsQuery": helper.ToQuery(rails.NewHash("ids", ids))}, adminLayoutXHR(c))
}

// UsersBulkLock は users#bulk_lock（POST /users/bulk_lock）。
func (a *App) UsersBulkLock(c *Req) { a.bulkUpdateStatus(c, domain.StatusLocked) }

// UsersBulkUnlock は users#bulk_unlock（POST /users/bulk_unlock）。
func (a *App) UsersBulkUnlock(c *Req) { a.bulkUpdateStatus(c, domain.StatusActive) }

// bulkUpdateStatus は UsersController#bulk_update_status（update_all のためコールバックなし）。
func (a *App) bulkUpdateStatus(c *Req, status int) {
	users, err := repository.LoggedUsersByIDs(c.Ctx(), a.DB, idsFromParam(c.Params().Slice("ids")), c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if len(users) == 0 {
		c.Render404("")
		return
	}
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if err := repository.SetUsersStatus(c.Ctx(), tx, ids, status); err != nil {
			return err
		}
		if status != domain.StatusLocked {
			return nil
		}
		// buropher 独自（セキュリティ）: Redmine の update_all はコールバックを通らずセッション等が残り、
		// ロック解除で以前のセッション・自動ログインが復活する。users#destroy の lock と同じく破棄する。
		for _, id := range ids {
			if err := repository.DeleteUserTokensByActions(c.Ctx(), tx, id, "recovery", "autologin", "session"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		a.serverError(c, err)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.Redirect("/users")
}
