package handler

// WatchersController（app/controllers/watchers_controller.rb）。
//
// ウォッチ対象（acts_as_watchable のモデル）は watchableTypes に object_type（クラス名の underscore）で登録する。
// 登録済み: issue / wiki / wiki_page / news / board / message / enabled_module。
// 新しい種類は registerWatchable で watchableType を登録する（watchers テーブルは watchable_kind で共通。
// object_type と watchable_kind が異なるのは enabled_module → project_module のみ）。
//
// サイドバーの部分テンプレートは、チケットが issues/_watchers（チケットのビューモデルを使う）、
// それ以外は共通の watchers/_watchers（dict "object_type" "id" "project"）。

import (
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// WatchersController。
var WatchersController = &Controller{Name: "watchers", MainMenu: true}

// routesIssueRelationsAndWatchers はチケットの関連・ウォッチャー・自動補完（IssueRelationsController,
// WatchersController, AutoCompletesController）のルートをまとめて登録する。
func (a *App) routesIssueRelationsAndWatchers(r Router) {
	a.routesAutoCompletes(r)
	a.routesWatchers(r)
	a.routesIssueRelations(r)
}

// routesWatchers は
//
//	post 'watchers/watch', :to => 'watchers#watch', :as => 'watch'
//	delete 'watchers/watch', :to => 'watchers#unwatch'
//	get 'watchers/new', :to => 'watchers#new', :as => 'new_watchers'
//	post 'watchers', :to => 'watchers#create'
//	post 'watchers/append', :to => 'watchers#append'
//	delete 'watchers', :to => 'watchers#destroy'
//	get 'watchers/autocomplete_for_mention', to: 'watchers#autocomplete_for_mention', via: [:get]
//	get 'watchers/autocomplete_for_user', :to => 'watchers#autocomplete_for_user'
//	post 'issues/:object_id/watchers', :to => 'watchers#create', :object_type => 'issue'
//	delete 'issues/:object_id/watchers/:user_id' => 'watchers#destroy', :object_type => 'issue'
func (a *App) routesWatchers(r Router) {
	// before_action :require_login, :find_watchables, :only => [:watch, :unwatch]
	watch := []ActionOption{RequireLogin(), Before(a.findWatchables)}
	a.Handle(r, http.MethodPost, "/watchers/watch", WatchersController, "watch", a.WatchersWatch, watch...)
	a.Handle(r, http.MethodDelete, "/watchers/watch", WatchersController, "unwatch", a.WatchersUnwatch, watch...)
	// before_action :find_project, :authorize, :only => [:new, :create, :append, :destroy, :autocomplete_for_user, :autocomplete_for_mention]
	// accept_api_auth :create, :destroy
	auth := []ActionOption{Before(a.findWatchersProject), Authorize()}
	api := append(slices.Clone(auth), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/watchers/new", WatchersController, "new", a.WatchersNew, auth...)
	a.Handle(r, http.MethodPost, "/watchers", WatchersController, "create", a.WatchersCreate, api...)
	a.Handle(r, http.MethodPost, "/watchers/append", WatchersController, "append", a.WatchersAppend, auth...)
	a.Handle(r, http.MethodDelete, "/watchers", WatchersController, "destroy", a.WatchersDestroy, api...)
	a.Handle(r, http.MethodGet, "/watchers/autocomplete_for_mention", WatchersController, "autocomplete_for_mention",
		a.WatchersAutocompleteForMention, auth...)
	a.Handle(r, http.MethodGet, "/watchers/autocomplete_for_user", WatchersController, "autocomplete_for_user",
		a.WatchersAutocompleteForUser, auth...)
	// :object_type => 'issue'（ルートの既定値。c.Params() は毎回作り直されるためリクエストのローカル値で持つ）
	issueAPI := append([]ActionOption{Before(func(c *Req) { c.setLocal(ctxWatchersObjectType, "issue") })}, api...)
	a.Handle(r, http.MethodPost, "/issues/{object_id}/watchers", WatchersController, "create", a.WatchersCreate, issueAPI...)
	a.Handle(r, http.MethodDelete, "/issues/{object_id}/watchers/{user_id}", WatchersController, "destroy", a.WatchersDestroy, issueAPI...)
}

// ---------------------------------------------------------------- ウォッチ対象の登録

// watchable はウォッチ対象のオブジェクト 1 件。
type watchable struct {
	// Type は object_type（クラス名の underscore。権限名・CSS・URL に使う。例 "issue"）。
	Type string
	// Kind は watchers.watchable_kind（Type と同じ。enabled_module のみ project_module）。
	Kind    string
	ID      int64
	Project *domain.Project
	// Data は種類ごとの値（issue なら *query.IssueRow）。
	Data any
}

// watchableType は acts_as_watchable のモデル 1 種類。
type watchableType struct {
	// Load は ids のうち存在するものを id 順に返す（klass.where(:id => ids).order(:id)）。
	Load func(a *App, c *Req, ids []int64) ([]*watchable, error)
	// Visible は visible?(user)。nil なら visible? を持たないモデル（enabled_module）で、
	// find_objects_from_params は project.visible? を見て、valid_watcher? は常に true。
	Visible func(a *App, c *Req, w *watchable, u *domain.User) (bool, error)
	// SetWatcher は set_watcher(principal, watching)（nil なら watchers テーブルを直接更新する）。
	SetWatcher func(a *App, c *Req, w *watchable, principalID int64, watching bool) error
	// WatchersPartial は watchers/_watchers の描画に使う部分テンプレートとローカル変数。
	WatchersPartial func(a *App, c *Req, w *watchable) (string, any, error)
}

var watchableTypes = map[string]*watchableType{}

// registerWatchable は object_type のウォッチ対象を登録する。
func registerWatchable(objectType string, t *watchableType) { watchableTypes[objectType] = t }

func init() {
	registerWatchable("issue", &watchableType{
		Load:            loadIssueWatchables,
		Visible:         issueWatchableVisible,
		SetWatcher:      setIssueWatcher,
		WatchersPartial: issueWatchersPartial,
	})
	// 共通の watchers/_watchers（watched.project で権限を見る）
	genericPartial := func(a *App, c *Req, w *watchable) (string, any, error) {
		return "watchers/watchers", map[string]any{"object_type": w.Type, "id": w.ID, "project": w.Project}, nil
	}
	// visible?(user) が user.allowed_to?(perm, project) のモデル
	// （News / Board / Message / Wiki / WikiPage。Message は user が nil なら false だが呼び出し側で nil は来ない）
	permVisible := func(perm string) func(a *App, c *Req, w *watchable, u *domain.User) (bool, error) {
		return func(a *App, c *Req, w *watchable, u *domain.User) (bool, error) {
			az := c.Authz()
			if u.ID != c.User.ID {
				az = authz.New(a.DB, u)
			}
			return az.AllowedTo(c.Ctx(), domain.Perm(perm), w.Project)
		}
	}
	for objectType, perm := range map[string]string{
		"news": "view_news", "board": "view_messages", "message": "view_messages",
		"wiki": "view_wiki_pages", "wiki_page": "view_wiki_pages", "enabled_module": "",
	} {
		t := &watchableType{
			Load: func(a *App, c *Req, ids []int64) ([]*watchable, error) {
				return loadWatchablesWithProject(a, c, objectType, ids)
			},
			WatchersPartial: genericPartial,
		}
		if perm != "" {
			t.Visible = permVisible(perm)
		}
		registerWatchable(objectType, t)
	}
}

// watchableKindOf は object_type → watchers.watchable_kind。
func watchableKindOf(objectType string) string {
	if objectType == "enabled_module" {
		return "project_module"
	}
	return objectType
}

// loadWatchablesWithProject は id → project_id の対応からウォッチ対象を作る（存在しない id は除く）。
func loadWatchablesWithProject(a *App, c *Req, objectType string, ids []int64) ([]*watchable, error) {
	var out []*watchable
	for _, id := range ids {
		pid, ok, err := repository.WatchableProjectID(c.Ctx(), a.DB, objectType, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		p, err := repository.GetProject(c.Ctx(), a.DB, pid)
		if err != nil {
			return nil, err
		}
		out = append(out, &watchable{Type: objectType, Kind: watchableKindOf(objectType), ID: id, Project: p})
	}
	return out, nil
}

func loadIssueWatchables(a *App, c *Req, ids []int64) ([]*watchable, error) {
	var out []*watchable
	for _, id := range ids {
		r, err := repository.ReadIssue(c.Ctx(), a.DB, id)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		p, err := repository.GetProject(c.Ctx(), a.DB, r.ProjectID)
		if err != nil {
			return nil, err
		}
		out = append(out, &watchable{Type: "issue", Kind: "issue", ID: id, Project: p, Data: issueRowFromRead(r)})
	}
	return out, nil
}

func issueWatchableVisible(a *App, c *Req, w *watchable, u *domain.User) (bool, error) {
	r := w.Data.(*query.IssueRow)
	az := c.Authz()
	if u.ID != c.User.ID {
		az = authz.New(a.DB, u)
	}
	return az.IssueVisible(c.Ctx(), &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
		StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, w.Project)
}

func setIssueWatcher(a *App, c *Req, w *watchable, principalID int64, watching bool) error {
	e := a.writeIssuesEnv(c, a.DB)
	iss, err := e.Find(c.Ctx(), w.ID)
	if err != nil || iss == nil {
		return err
	}
	return e.SetWatcher(c.Ctx(), iss, principalID, watching)
}

func issueWatchersPartial(a *App, c *Req, w *watchable) (string, any, error) {
	l := a.newIssueLookup(c)
	row := w.Data.(*query.IssueRow)
	l.addIssues([]*query.IssueRow{row})
	v := &issueShowView{l: l, M: l.model(row)}
	if l.err != nil {
		return "", nil, l.err
	}
	return "issues/watchers", map[string]any{"V": v}, nil
}

const (
	ctxWatchables         = "watchables"
	ctxWatchersObjectType = "watchers_object_type"
)

// watchersObjectType は params[:object_type]（ルートの既定値があればそれ）。
func (c *Req) watchersObjectType() (any, bool) {
	if v, ok := c.local(ctxWatchersObjectType).(string); ok {
		return v, true
	}
	return c.Params().Get("object_type")
}

func (c *Req) watchables() []*watchable {
	ws, _ := c.local(ctxWatchables).([]*watchable)
	return ws
}

// errUnknownWatchable は object_type が acts_as_watchable のモデルでない（find_objects_from_params が nil）。
var errUnknownWatchable = errors.New("unknown watchable type")

// findObjectsFromParams は find_objects_from_params（見えないものがあれば Unauthorized = deny_access で halted）。
func (a *App) findObjectsFromParams(c *Req) ([]*watchable, error) {
	ot, _ := c.watchersObjectType()
	t := watchableTypes[httpx.ValueString(ot)]
	if t == nil {
		return nil, errUnknownWatchable
	}
	v, _ := c.Params().Get("object_id")
	ids := idsFromParam(v)
	slices.Sort(ids)
	ws, err := t.Load(a, c, ids)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		var ok bool
		if t.Visible != nil {
			ok, err = t.Visible(a, c, w, c.User)
		} else {
			// visible? を持たなければ project.visible?
			ok, err = c.Authz().ProjectVisible(c.Ctx(), w.Project)
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			c.DenyAccess()
			return nil, nil
		}
	}
	return ws, nil
}

// findWatchables は find_watchables（無ければ 404）。
func (a *App) findWatchables(c *Req) {
	ws, err := a.findObjectsFromParams(c)
	if c.Halted() {
		return
	}
	if err != nil && !errors.Is(err, errUnknownWatchable) {
		a.internalError(c, "find watchables", err)
		return
	}
	if len(ws) == 0 {
		c.Render404("")
		return
	}
	c.setLocal(ctxWatchables, ws)
}

// findWatchersProject は WatchersController#find_project。
func (a *App) findWatchersProject(c *Req) {
	p := c.Params()
	ot, hasType := c.watchersObjectType()
	oid, hasID := p.Get("object_id")
	switch {
	case hasType && ot != nil && hasID && oid != nil:
		ws, err := a.findObjectsFromParams(c)
		if c.Halted() {
			return
		}
		if err != nil {
			// find_objects_from_params が nil を返すと @watchables.map で NoMethodError（500）
			a.internalError(c, "find watchables", err)
			return
		}
		c.setLocal(ctxWatchables, ws)
		projects := []*domain.Project{}
		for _, w := range ws {
			if !slices.ContainsFunc(projects, func(x *domain.Project) bool { return x.ID == w.Project.ID }) {
				projects = append(projects, w.Project)
			}
		}
		if len(projects) == 1 {
			c.Project = projects[0]
		} else {
			c.Projects = projects
		}
	case p.Has("project_id") && func() bool { v, _ := p.Get("project_id"); return v != nil }():
		// Project.visible.find_by_param(params[:project_id])（見つからなければ RecordNotFound）
		pr, err := c.lookupProject(p.String("project_id"))
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.Render404("")
			} else {
				a.internalError(c, "find project", err)
			}
			return
		}
		ok, err := c.Authz().ProjectVisible(c.Ctx(), pr)
		if err != nil {
			a.internalError(c, "project visible", err)
			return
		}
		if !ok {
			c.Render404("")
			return
		}
		c.Project = pr
	}
}

// watchersProjects は @projects（@project があればそれのみ）。
func (c *Req) watchersProjects() []*domain.Project {
	if c.Project != nil {
		return []*domain.Project{c.Project}
	}
	return c.Projects
}

// authorizeForWatchableType は authorize_for_watchable_type(action)（拒否なら 403 を描画して false）。
func (a *App) authorizeForWatchableType(c *Req, action string) bool {
	for _, w := range c.watchables() {
		if !c.AllowedTo(domain.Perm(action+"_"+w.Type+"_watchers"), w.Project) {
			c.Render403("")
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- 候補ユーザー

// validWatcher は valid_watcher?(user)（ユーザーならウォッチ対象が見えること）。
func (a *App) validWatcher(c *Req, w *watchable, u *domain.User) (bool, error) {
	t := watchableTypes[w.Type]
	if !u.Kind.IsUser() || t.Visible == nil {
		return true, nil
	}
	return t.Visible(a, c, w, u)
}

// visibleWatcherIDs は visible_watcher_users(User.current) の id。
func (a *App) visibleWatcherIDs(c *Req, w *watchable) ([]int64, error) {
	ws, err := repository.WatcherPrincipals(c.Ctx(), a.DB, w.Kind, w.ID, a.Settings.String("user_format"))
	if err != nil {
		return nil, err
	}
	all := c.AllowedTo(domain.Perm("view_"+w.Type+"_watchers"), w.Project)
	var ids []int64
	for _, x := range ws {
		id := principalID(x)
		if all || id == c.User.ID {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (a *App) principalVisibleCond(c *Req) (string, error) {
	return c.Authz().PrincipalVisibleCondition(c.Ctx())
}

// usersForNewWatcher は users_for_new_watcher。
func (a *App) usersForNewWatcher(c *Req) ([]*domain.User, error) {
	vis, err := a.principalVisibleCond(c)
	if err != nil {
		return nil, err
	}
	q := c.Params().String("q")
	o := repository.AssignableWatchersOptions{VisibleCond: vis, Like: q, UserFormat: a.Settings.String("user_format")}
	if rails.IsBlank(q) {
		switch {
		case c.Project != nil:
			o.ProjectIDs = []int64{c.Project.ID}
		case len(c.Projects) > 1:
			for _, p := range c.Projects {
				o.ProjectIDs = append(o.ProjectIDs, p.ID)
			}
		default:
			// scope が nil（Redmine では NoMethodError）
			return nil, nil
		}
	} else {
		o.Limit = 100
	}
	users, err := repository.AssignableWatchers(c.Ctx(), a.DB, o)
	if err != nil {
		return nil, err
	}
	ws := c.watchables()
	if len(ws) == 1 {
		ids, err := a.visibleWatcherIDs(c, ws[0])
		if err != nil {
			return nil, err
		}
		users = slices.DeleteFunc(users, func(u *domain.User) bool { return slices.Contains(ids, u.ID) })
	}
	for _, w := range ws {
		var out []*domain.User
		for _, u := range users {
			ok, err := a.validWatcher(c, w, u)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, u)
			}
		}
		users = out
	}
	return users, nil
}

// usersForMention は users_for_mention。
func (a *App) usersForMention(c *Req) ([]*domain.User, error) {
	vis, err := a.principalVisibleCond(c)
	if err != nil {
		return nil, err
	}
	q := c.Params().String("q")
	o := repository.AssignableWatchersOptions{VisibleCond: vis, Like: q, UsersOnly: true, UserFormat: a.Settings.String("user_format")}
	if rails.IsBlank(q) && c.Project != nil {
		o.ProjectIDs = []int64{c.Project.ID}
	} else {
		o.Limit = 10
	}
	users, err := repository.AssignableWatchers(c.Ctx(), a.DB, o)
	if err != nil {
		return nil, err
	}
	// object.respond_to?(:visible?) のときだけ絞り込む
	if ws := c.watchables(); len(ws) == 1 && watchableTypes[ws[0].Type].Visible != nil {
		var out []*domain.User
		for _, u := range users {
			ok, err := watchableTypes[ws[0].Type].Visible(a, c, ws[0], u)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, u)
			}
		}
		users = out
	}
	return users, nil
}

// ---------------------------------------------------------------- 描画用

// newWatchersData は watchers/_new のローカル変数（watchables, users）。
func (a *App) newWatchersData(c *Req, users []*domain.User) map[string]any {
	ws := c.watchables()
	data := map[string]any{"Users": users, "Watchables": ws, "Project": c.Project}
	objectType := ""
	var ids []int64
	if len(ws) > 0 {
		objectType = ws[0].Type
		for _, w := range ws {
			ids = append(ids, w.ID)
		}
		data["Title"] = c.L("permission_add_" + objectType + "_watchers")
		data["FormURL"] = "/watchers"
	} else {
		data["Title"] = c.L("permission_add_issue_watchers")
		data["FormURL"] = "/watchers/append"
	}
	data["ObjectType"] = objectType
	// url_for(:controller => 'watchers', :action => 'autocomplete_for_user', :object_type, :object_id, :project_id)
	h := rails.NewHash()
	if len(ws) > 0 {
		h.Set("object_type", objectType)
		h.Set("object_id", ids)
	}
	if c.Project != nil {
		h.Set("project_id", c.Project.Identifier)
	}
	u := "/watchers/autocomplete_for_user"
	if qs := helper.ToQuery(h); qs != "" {
		u += "?" + qs
	}
	data["AutocompleteURL"] = u
	return data
}

// watcherCSS は WatchersHelper#watcher_css(objects)。
func watcherCSS(ws []*watchable) string {
	id := "bulk"
	if len(ws) == 1 {
		id = strconv.FormatInt(ws[0].ID, 10)
	}
	return ws[0].Type + "-" + id + "-watcher"
}

// watcherLink は WatchersHelper#watcher_link(objects, user)。
func (a *App) watcherLink(c *Req, ws []*watchable, u *domain.User) (template.HTML, error) {
	if u == nil || !u.Logged() || len(ws) == 0 {
		return "", nil
	}
	ids := make([]int64, len(ws))
	for i, w := range ws {
		ids[i] = w.ID
	}
	watched, err := repository.AnyWatched(c.Ctx(), a.DB, ws[0].Kind, ids, u.ID)
	if err != nil {
		return "", err
	}
	css := watcherCSS(ws) + " icon icon-fav-off"
	icon, text, method := "watch", c.L("button_watch"), "post"
	if watched {
		css = watcherCSS(ws) + " icon icon-fav"
		icon, text, method = "unwatch", c.L("button_unwatch"), "delete"
	}
	var oid any = ws[0].ID
	if len(ws) > 1 {
		sorted := slices.Clone(ids)
		slices.Sort(sorted)
		oid = sorted
	}
	url := "/watchers/watch?" + helper.ToQuery(rails.NewHash("object_id", oid, "object_type", ws[0].Type))
	return rails.LinkTo(a.Helpers.Icon(c.Page(), icon, text, nil), url,
		rails.NewHash("remote", true, "method", method, "class", css)), nil
}

// setWatcherData は watchers/_set_watcher.js のローカル変数（watched, user）。
func (a *App) setWatcherData(c *Req, ws []*watchable, u *domain.User) (map[string]any, error) {
	link, err := a.watcherLink(c, ws, u)
	if err != nil {
		return nil, err
	}
	first := ws[0]
	if c.Project == nil {
		// watched.project（watchers/_watchers は watched.project で権限を見る）
		c.Project = first.Project
	}
	partial, locals, err := watchableTypes[first.Type].WatchersPartial(a, c, first)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Selector": "." + watcherCSS(ws), "WatcherLink": link,
		"WatchersPartial": partial, "WatchersLocals": locals}, nil
}

// renderWatcherText は render(:html => text, :status => :ok, :layout => true)（redirect_to_referer_or のブロック）。
func (a *App) redirectToRefererOrText(c *Req, text string) {
	if ref := c.R.Header.Get("Referer"); ref != "" {
		c.Redirect(ref)
		return
	}
	c.Render("watchers/text", map[string]any{"Text": text})
}

// setWatcherOn は watchable.set_watcher(principal, watching)。
func (a *App) setWatcherOn(c *Req, w *watchable, principalID int64, watching bool) error {
	if f := watchableTypes[w.Type].SetWatcher; f != nil {
		return f(a, c, w, principalID, watching)
	}
	if watching {
		return repository.AddWatcher(c.Ctx(), a.DB, w.Kind, w.ID, principalID)
	}
	return repository.RemoveWatcher(c.Ctx(), a.DB, w.Kind, w.ID, principalID)
}

// ---------------------------------------------------------------- アクション

// WatchersWatch は watchers#watch。
func (a *App) WatchersWatch(c *Req) { a.setWatcher(c, true) }

// WatchersUnwatch は watchers#unwatch。
func (a *App) WatchersUnwatch(c *Req) { a.setWatcher(c, false) }

// setWatcher は WatchersController#set_watcher(watchables, User.current, watching)。
func (a *App) setWatcher(c *Req, watching bool) {
	ws := c.watchables()
	for _, w := range ws {
		var err error
		if watching {
			// Watcher#validate_user（有効なユーザーのみ）
			if c.User.Active() {
				err = a.setWatcherOn(c, w, c.User.ID, true)
			}
		} else {
			err = a.setWatcherOn(c, w, c.User.ID, false)
		}
		if err != nil {
			a.internalError(c, "set watcher", err)
			return
		}
	}
	switch httpx.Negotiate(c.R, "html", "js") {
	case "html":
		text := "Watcher removed."
		if watching {
			text = "Watcher added."
		}
		a.redirectToRefererOrText(c, text)
	case "js":
		data, err := a.setWatcherData(c, ws, c.User)
		if err != nil {
			a.internalError(c, "set watcher", err)
			return
		}
		c.Render("watchers/_set_watcher", data, RenderOptions{Format: "js"})
	default:
		renderUnknownFormat(c)
	}
}

// WatchersNew は watchers#new（html は 404、js はモーダル）。
func (a *App) WatchersNew(c *Req) {
	switch httpx.Negotiate(c.R, "html", "js") {
	case "html":
		c.Render404("")
	case "js":
		users, err := a.usersForNewWatcher(c)
		if err != nil {
			a.internalError(c, "users for new watcher", err)
			return
		}
		c.Render("watchers/new", a.newWatchersData(c, users), RenderOptions{Format: "js"})
	default:
		renderUnknownFormat(c)
	}
}

// watcherUserIDsParam は create の user_ids（params[:watcher][:user_ids] || params[:watcher][:user_id]、無ければ params[:user_id]）。
func watcherUserIDsParam(p *httpx.Params) []int64 {
	if w, ok := p.Get("watcher"); ok && w != nil {
		if v, ok := p.Lookup("watcher", "user_ids"); ok && v != nil {
			return idsFromParam(v)
		}
		v, _ := p.Lookup("watcher", "user_id")
		return idsFromParam(v)
	}
	v, _ := p.Get("user_id")
	return idsFromParam(v)
}

// WatchersCreate は watchers#create。
func (a *App) WatchersCreate(c *Req) {
	if !a.authorizeForWatchableType(c, "add") {
		return
	}
	vis, err := a.principalVisibleCond(c)
	if err != nil {
		a.internalError(c, "principal visible", err)
		return
	}
	users, err := repository.AssignableWatchersByIDs(c.Ctx(), a.DB, vis, watcherUserIDsParam(c.Params()))
	if err != nil {
		a.internalError(c, "watchers", err)
		return
	}
	for _, u := range users {
		for _, w := range c.watchables() {
			ok, err := a.validWatcher(c, w, u)
			if err != nil {
				a.internalError(c, "valid watcher", err)
				return
			}
			if !ok {
				continue
			}
			if err := a.setWatcherOn(c, w, u.ID, true); err != nil {
				a.internalError(c, "add watcher", err)
				return
			}
		}
	}
	switch {
	case httpx.IsAPIRequest(c.R):
		c.RenderAPIOK()
		return
	}
	switch httpx.Negotiate(c.R, "html", "js") {
	case "html":
		a.redirectToRefererOrText(c, "Watcher added.")
	case "js":
		users, err := a.usersForNewWatcher(c)
		if err != nil {
			a.internalError(c, "users for new watcher", err)
			return
		}
		data := a.newWatchersData(c, users)
		if ws := c.watchables(); len(ws) == 1 {
			sw, err := a.setWatcherData(c, ws, c.User)
			if err != nil {
				a.internalError(c, "set watcher", err)
				return
			}
			data["SetWatcher"] = sw
		}
		c.Render("watchers/create", data, RenderOptions{Format: "js"})
	default:
		renderUnknownFormat(c)
	}
}

// WatchersAppend は watchers#append（新規チケットのフォームのウォッチャー一覧に追加する）。
func (a *App) WatchersAppend(c *Req) {
	var users []*domain.User
	p := c.Params()
	if w, ok := p.Get("watcher"); ok && w != nil {
		v, ok := p.Lookup("watcher", "user_ids")
		if !ok || v == nil {
			v, _ = p.Lookup("watcher", "user_id")
		}
		vis, err := a.principalVisibleCond(c)
		if err != nil {
			a.internalError(c, "principal visible", err)
			return
		}
		users, err = repository.AssignableWatchersByIDs(c.Ctx(), a.DB, vis, idsFromParam(v))
		if err != nil {
			a.internalError(c, "watchers", err)
			return
		}
	}
	if len(users) == 0 {
		httpx.Head(c.W, c.R, http.StatusOK)
		c.Halt()
		return
	}
	c.Render("watchers/append", map[string]any{"Users": users, "Checkboxes": watchersCheckboxes(a.newIssueLookup(c), users, func(int64) bool { return true })},
		RenderOptions{Format: "js"})
}

// WatchersDestroy は watchers#destroy。
func (a *App) WatchersDestroy(c *Req) {
	if !a.authorizeForWatchableType(c, "delete") {
		return
	}
	uid, err := strconv.ParseInt(c.Params().String("user_id"), 10, 64)
	if err != nil {
		c.Render404("")
		return
	}
	if _, err := repository.GetPrincipal(c.Ctx(), a.DB, uid); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "principal", err)
		}
		return
	}
	for _, w := range c.watchables() {
		if err := a.setWatcherOn(c, w, uid, false); err != nil {
			a.internalError(c, "remove watcher", err)
			return
		}
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderAPIOK()
		return
	}
	switch httpx.Negotiate(c.R, "html", "js") {
	case "html":
		a.redirectToRefererOrText(c, "Watcher removed.")
	case "js":
		data := map[string]any{}
		if ws := c.watchables(); len(ws) == 1 {
			sw, err := a.setWatcherData(c, ws, c.User)
			if err != nil {
				a.internalError(c, "set watcher", err)
				return
			}
			data["SetWatcher"] = sw
		}
		c.Render("watchers/destroy", data, RenderOptions{Format: "js"})
	default:
		renderUnknownFormat(c)
	}
}

// WatchersAutocompleteForUser は watchers#autocomplete_for_user（render :layout => false）。
func (a *App) WatchersAutocompleteForUser(c *Req) {
	users, err := a.usersForNewWatcher(c)
	if err != nil {
		a.internalError(c, "users for new watcher", err)
		return
	}
	c.Render("watchers/autocomplete_for_user", map[string]any{"Users": users}, RenderOptions{Layout: view.NoLayout})
}

// WatchersAutocompleteForMention は watchers#autocomplete_for_mention（render :json => format_users_json(users)）。
func (a *App) WatchersAutocompleteForMention(c *Req) {
	users, err := a.usersForMention(c)
	if err != nil {
		a.internalError(c, "users for mention", err)
		return
	}
	out := make([]any, 0, len(users))
	for _, u := range users {
		out = append(out, rails.NewHash("firstname", u.Firstname, "lastname", u.Lastname,
			"name", helper.PrincipalUserName(c.Page(), u), "login", u.Login))
	}
	renderJSON(c, out)
}

// renderJSON は render :json => v（ActiveSupport の to_json）。
func renderJSON(c *Req, v any) {
	httpx.SetContentType(c.W, "json", true)
	if httpx.ShouldVaryAccept(c.R) && c.W.Header().Get("Vary") == "" {
		c.W.Header().Add("Vary", "Accept")
	}
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(rails.ToJSON(v)))
	c.Halt()
}

// principalID は WatcherPrincipals の要素（*domain.User / *domain.Group）の id。
func principalID(v any) int64 {
	switch x := v.(type) {
	case *domain.User:
		return x.ID
	case *domain.Group:
		return x.ID
	}
	return 0
}
