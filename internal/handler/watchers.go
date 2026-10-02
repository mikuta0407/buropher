package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
)

// WatchersController（app/controllers/watchers_controller.rb）。acts_as_watchable のモデル
// （issue / news / board / message / wiki / wiki_page / enabled_module）を object_type で扱う。
//
//	before_action :require_login, :find_watchables, :only => [:watch, :unwatch]
//	before_action :find_project, :authorize, :only => [:new, :create, :append, :destroy, :autocomplete_for_user, :autocomplete_for_mention]
//	accept_api_auth :create, :destroy
var WatchersController = &Controller{Name: "watchers", MainMenu: true}

// routesWatchers は watchers コントローラのルートを登録する。
func (a *App) routesWatchers(r Router) {
	w := WatchersController
	findWatchables := Before(a.findWatchablesRequired)
	project := []ActionOption{Before(a.watchersFindProject), Authorize()}
	a.Handle(r, http.MethodPost, "/watchers/watch", w, "watch", a.WatchersWatch, RequireLogin(), findWatchables)
	a.Handle(r, http.MethodDelete, "/watchers/watch", w, "unwatch", a.WatchersUnwatch, RequireLogin(), findWatchables)
	a.Handle(r, http.MethodGet, "/watchers/new", w, "new", a.WatchersNew, project...)
	a.Handle(r, http.MethodPost, "/watchers", w, "create", a.WatchersCreate, append(append([]ActionOption{}, project...), AcceptAPIAuth())...)
	a.Handle(r, http.MethodPost, "/watchers/append", w, "append", a.WatchersAppend, project...)
	a.Handle(r, http.MethodDelete, "/watchers", w, "destroy", a.WatchersDestroy, append(append([]ActionOption{}, project...), AcceptAPIAuth())...)
	a.Handle(r, http.MethodGet, "/watchers/autocomplete_for_mention", w, "autocomplete_for_mention", a.WatchersAutocompleteForMention, project...)
	a.Handle(r, http.MethodGet, "/watchers/autocomplete_for_user", w, "autocomplete_for_user", a.WatchersAutocompleteForUser, project...)
	// Specific routes for issue watchers API
	issue := func(c *Req) { c.setValue(objectTypeCtxKey{}, "issue") }
	a.Handle(r, http.MethodPost, "/issues/{object_id}/watchers", w, "create", a.WatchersCreate,
		append([]ActionOption{Before(issue)}, append(append([]ActionOption{}, project...), AcceptAPIAuth())...)...)
	a.Handle(r, http.MethodDelete, "/issues/{object_id}/watchers/{user_id}", w, "destroy", a.WatchersDestroy,
		append([]ActionOption{Before(issue)}, append(append([]ActionOption{}, project...), AcceptAPIAuth())...)...)
}

// watchable は acts_as_watchable のオブジェクト。
type watchable struct {
	// Type は object_type（クラス名の underscore）。
	Type string
	// Kind は watchers.watchable_kind。
	Kind    string
	ID      int64
	Project *domain.Project
}

// watchableQueries は object_type → プロジェクト id を引く SQL。
var watchableQueries = map[string]string{
	"issue":          `SELECT project_id FROM issues WHERE id = ?`,
	"news":           `SELECT project_id FROM news WHERE id = ?`,
	"board":          `SELECT project_id FROM boards WHERE id = ?`,
	"message":        `SELECT boards.project_id FROM messages JOIN boards ON boards.id = messages.board_id WHERE messages.id = ?`,
	"wiki":           `SELECT project_id FROM wikis WHERE id = ?`,
	"wiki_page":      `SELECT wikis.project_id FROM wiki_pages JOIN wikis ON wikis.id = wiki_pages.wiki_id WHERE wiki_pages.id = ?`,
	"enabled_module": `SELECT project_id FROM project_modules WHERE id = ?`,
}

func watchableKindOf(objectType string) string {
	if objectType == "enabled_module" {
		return "project_module"
	}
	return objectType
}

type watchersCtxKey struct{}

func (c *Req) watchables() []*watchable {
	ws, _ := c.value(watchersCtxKey{}).([]*watchable)
	return ws
}

// findObjectsFromParams は find_objects_from_params（見つからない種類なら nil。見えないものがあれば errUnauthorized）。
func (a *App) findObjectsFromParams(c *Req) ([]*watchable, error) {
	typ := c.objectTypeParam()
	query, ok := watchableQueries[typ]
	if !ok {
		return nil, nil
	}
	var ids []int64
	v, _ := c.Params().Get("object_id")
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if n := httpx.RubyToI(httpx.ValueString(e)); n > 0 {
				ids = append(ids, n)
			}
		}
	default:
		if n := httpx.RubyToI(httpx.ValueString(x)); n > 0 {
			ids = append(ids, n)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []*watchable
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var pid int64
		if err := a.DB.Get(c.Ctx(), &pid, query, id); err != nil {
			continue
		}
		p, err := repository.GetProject(c.Ctx(), a.DB, pid)
		if err != nil {
			return nil, err
		}
		out = append(out, &watchable{Type: typ, Kind: watchableKindOf(typ), ID: id, Project: p})
	}
	for _, w := range out {
		if !a.watchableVisible(c, w, c.User) {
			return nil, errUnauthorized
		}
	}
	return out, nil
}

var errUnauthorized = errors.New("unauthorized")

func newAuthorizerFor(a *App, u *domain.User) *authz.Authorizer { return authz.New(a.DB, u) }

// watchableVisible は watchable.visible?(user)（visible? を持たない enabled_module は project.visible?）。
func (a *App) watchableVisible(c *Req, w *watchable, u *domain.User) bool {
	if u == nil {
		return false
	}
	az := c.Authz()
	if u.ID != c.User.ID {
		az = newAuthorizerFor(a, u)
	}
	switch w.Type {
	case "issue":
		st := redmine.NewDBStore(c.Ctx(), a.DB, az)
		is, err := st.VisibleIssue(w.ID)
		return err == nil && is != nil
	case "enabled_module":
		ok, err := az.ProjectVisible(c.Ctx(), w.Project)
		return err == nil && ok
	}
	perm := map[string]string{"news": "view_news", "board": "view_messages", "message": "view_messages",
		"wiki": "view_wiki_pages", "wiki_page": "view_wiki_pages"}[w.Type]
	ok, err := az.AllowedTo(c.Ctx(), domain.Perm(perm), w.Project)
	return err == nil && ok
}

// findWatchablesRequired は find_watchables（無ければ 404）。
func (a *App) findWatchablesRequired(c *Req) {
	ws, err := a.findObjectsFromParams(c)
	if errors.Is(err, errUnauthorized) {
		c.DenyAccess()
		return
	}
	if err != nil {
		a.internalError(c, "find watchables", err)
		return
	}
	if len(ws) == 0 {
		c.Render404("")
		return
	}
	c.setValue(watchersCtxKey{}, ws)
}

// watchersFindProject は WatchersController#find_project。
func (a *App) watchersFindProject(c *Req) {
	p := c.Params()
	if c.objectTypeParam() != "" && p.Present("object_id") {
		ws, err := a.findObjectsFromParams(c)
		if errors.Is(err, errUnauthorized) {
			c.DenyAccess()
			return
		}
		if err != nil {
			a.internalError(c, "find watchables", err)
			return
		}
		c.setValue(watchersCtxKey{}, ws)
		var projects []*domain.Project
		seen := map[int64]bool{}
		for _, w := range ws {
			if !seen[w.Project.ID] {
				seen[w.Project.ID] = true
				projects = append(projects, w.Project)
			}
		}
		if len(projects) == 1 {
			c.Project = projects[0]
		} else if len(projects) > 1 {
			c.Projects = projects
		}
		return
	}
	if p.Present("project_id") {
		// Project.visible.find_by_param(params[:project_id])
		pr, err := c.lookupProject(p.String("project_id"))
		if err == nil {
			if ok, _ := c.Authz().ProjectVisible(c.Ctx(), pr); ok {
				c.Project = pr
			}
		}
	}
}

// setWatcher は set_watcher(watchables, user, watching)。
func (a *App) setWatcher(c *Req, watching bool) {
	ws := c.watchables()
	err := a.withTx(c, func(tx *db.Tx) error {
		for _, w := range ws {
			var err error
			if watching {
				err = repository.AddWatcher(c.Ctx(), tx, w.Kind, w.ID, c.User.ID)
			} else {
				err = repository.RemoveWatcher(c.Ctx(), tx, w.Kind, w.ID, c.User.ID)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.internalError(c, "set watcher", err)
		return
	}
	switch httpx.Negotiate(c.R, "html", "js") {
	case "js":
		a.renderSetWatcher(c, ws, "watchers/_set_watcher")
	case "html":
		text := "Watcher removed."
		if watching {
			text = "Watcher added."
		}
		a.redirectToRefererOrText(c, text)
	default:
		respondNotAcceptable(c)
	}
}

// redirectToRefererOrText は redirect_to_referer_or { render(:html => text, :status => :ok, :layout => true) }。
func (a *App) redirectToRefererOrText(c *Req, text string) {
	if ref := c.R.Header.Get("Referer"); ref != "" {
		c.Redirect(ref)
		return
	}
	c.Render("watchers/text", map[string]any{"Text": text})
}

// setWatcherData は watchers/_set_watcher（watcher_link の差し替えとサイドバーの更新）の値。
func (a *App) setWatcherData(ws []*watchable) map[string]any {
	first := ws[0]
	id := strconv.FormatInt(first.ID, 10)
	if len(ws) > 1 {
		id = "bulk"
	}
	var ids []int64
	for _, w := range ws {
		ids = append(ids, w.ID)
	}
	return map[string]any{"Watched": ws, "First": first, "CSS": first.Type + "-" + id + "-watcher", "IDs": ids}
}

func (a *App) renderSetWatcher(c *Req, ws []*watchable, tmpl string) {
	c.Render(tmpl, a.setWatcherData(ws), RenderOptions{Format: "js", Layout: view.NoLayout})
}

// WatchersWatch は watchers#watch。
func (a *App) WatchersWatch(c *Req) { a.setWatcher(c, true) }

// WatchersUnwatch は watchers#unwatch。
func (a *App) WatchersUnwatch(c *Req) { a.setWatcher(c, false) }

// usersForNewWatcher は users_for_new_watcher。
func (a *App) usersForNewWatcher(c *Req) ([]any, error) {
	ws := c.watchables()
	cond, err := c.Authz().PrincipalVisibleCondition(c.Ctx())
	if err != nil {
		return nil, err
	}
	o := repository.WatcherCandidateOptions{VisibleCond: cond, Like: c.Params().String("q"), UserFormat: a.Settings.String("user_format")}
	if httpx.IsBlank(c.Params().String("q")) {
		switch {
		case c.Project != nil:
			o.ProjectIDs = []int64{c.Project.ID}
		case len(c.Projects) > 1:
			for _, p := range c.Projects {
				o.ProjectIDs = append(o.ProjectIDs, p.ID)
			}
		default:
			return nil, nil
		}
	} else {
		o.Limit = 100
	}
	users, err := repository.AssignableWatchers(c.Ctx(), a.DB, o)
	if err != nil {
		return nil, err
	}
	if len(ws) == 1 {
		// users -= watchable_object.visible_watcher_users
		visible, err := a.visibleWatcherIDs(c, ws[0])
		if err != nil {
			return nil, err
		}
		var kept []any
		for _, u := range users {
			if !visible[principalID(u)] {
				kept = append(kept, u)
			}
		}
		users = kept
	}
	var out []any
	for _, u := range users {
		ok := true
		for _, w := range ws {
			if !a.validWatcher(c, w, u) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, u)
		}
	}
	return out, nil
}

func principalID(v any) int64 {
	switch x := v.(type) {
	case *domain.User:
		return x.ID
	case *domain.Group:
		return x.ID
	}
	return 0
}

// visibleWatcherIDs は visible_watcher_users（view_<type>_watchers が無ければ自分だけ）の id。
func (a *App) visibleWatcherIDs(c *Req, w *watchable) (map[int64]bool, error) {
	ws, err := repository.WatcherPrincipals(c.Ctx(), a.DB, w.Kind, w.ID, a.Settings.String("user_format"))
	if err != nil {
		return nil, err
	}
	all := c.AllowedTo(domain.Perm("view_"+w.Type+"_watchers"), w.Project)
	out := map[int64]bool{}
	for _, x := range ws {
		id := principalID(x)
		if all || id == c.User.ID {
			out[id] = true
		}
	}
	return out, nil
}

// validWatcher は valid_watcher?(user)（ユーザーならオブジェクトが見えること）。
func (a *App) validWatcher(c *Req, w *watchable, principal any) bool {
	u, ok := principal.(*domain.User)
	if !ok || w.Type == "enabled_module" {
		return true
	}
	return a.watchableVisible(c, w, u)
}

// authorizeForWatchableType は authorize_for_watchable_type(action)。
func (a *App) authorizeForWatchableType(c *Req, action string) bool {
	for _, w := range c.watchables() {
		if !c.AllowedTo(domain.Perm(action+"_"+w.Type+"_watchers"), w.Project) {
			c.Render403("")
			return false
		}
	}
	return true
}

// WatchersNew は watchers#new（JS のみ。HTML は 404）。
func (a *App) WatchersNew(c *Req) {
	if httpx.Negotiate(c.R, "html", "js") != "js" {
		c.Render404("")
		return
	}
	users, err := a.usersForNewWatcher(c)
	if err != nil {
		a.internalError(c, "users for new watcher", err)
		return
	}
	c.Render("watchers/new", a.newWatcherData(c, users), RenderOptions{Format: "js", Layout: view.NoLayout})
}

func (a *App) newWatcherData(c *Req, users []any) map[string]any {
	ws := c.watchables()
	// url_for(:controller => 'watchers', :action => 'autocomplete_for_user', :object_type, :object_id, :project_id)
	q := url.Values{}
	if len(ws) > 0 {
		q.Set("object_type", ws[0].Type)
		for _, w := range ws {
			q.Add("object_id[]", strconv.FormatInt(w.ID, 10))
		}
	}
	if c.Project != nil {
		q.Set("project_id", c.Project.Identifier)
	}
	ac := "/watchers/autocomplete_for_user"
	if len(q) > 0 {
		ac += "?" + q.Encode()
	}
	locals := map[string]any{"watchables": ws, "users": users, "has_watchables": len(ws) > 0, "autocomplete_url": ac}
	data := map[string]any{"Watchables": ws, "Users": users, "Locals": locals, "Single": len(ws) == 1}
	if len(ws) > 0 {
		locals["first"] = ws[0]
		for k, v := range a.setWatcherData(ws) {
			data[k] = v
		}
	}
	return data
}

// WatchersCreate は watchers#create。
func (a *App) WatchersCreate(c *Req) {
	if !a.authorizeForWatchableType(c, "add") {
		return
	}
	p := c.Params()
	var raw []string
	if w := p.Map("watcher"); w != nil {
		if w.Has("user_ids") {
			raw = w.Strings("user_ids")
		} else {
			raw = []string{w.String("user_id")}
		}
	} else {
		raw = p.Strings("user_id")
	}
	var ids []int64
	for _, s := range raw {
		if n := httpx.RubyToI(s); n > 0 {
			ids = append(ids, n)
		}
	}
	ws := c.watchables()
	if len(ids) > 0 {
		cond, err := c.Authz().PrincipalVisibleCondition(c.Ctx())
		if err != nil {
			a.internalError(c, "principal visible", err)
			return
		}
		users, err := repository.AssignableWatchers(c.Ctx(), a.DB, repository.WatcherCandidateOptions{VisibleCond: cond, IDs: ids, UserFormat: a.Settings.String("user_format")})
		if err != nil {
			a.internalError(c, "assignable watchers", err)
			return
		}
		err = a.withTx(c, func(tx *db.Tx) error {
			for _, u := range users {
				for _, w := range ws {
					if a.validWatcher(c, w, u) {
						if err := repository.AddWatcher(c.Ctx(), tx, w.Kind, w.ID, principalID(u)); err != nil {
							return err
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			a.internalError(c, "add watchers", err)
			return
		}
	}
	switch httpx.Negotiate(c.R, "html", "js", "xml", "json") {
	case "html":
		a.redirectToRefererOrText(c, "Watcher added.")
	case "js":
		users, err := a.usersForNewWatcher(c)
		if err != nil {
			a.internalError(c, "users for new watcher", err)
			return
		}
		c.Render("watchers/create", a.newWatcherData(c, users), RenderOptions{Format: "js", Layout: view.NoLayout})
	case "xml", "json":
		c.RenderAPIOK()
	default:
		respondNotAcceptable(c)
	}
}

// WatchersAppend は watchers#append（チケット作成フォームのウォッチャーのチェックボックスを追加する）。
func (a *App) WatchersAppend(c *Req) {
	var users []any
	if w := c.Params().Map("watcher"); w != nil {
		raw := w.Strings("user_ids")
		if !w.Has("user_ids") {
			raw = []string{w.String("user_id")}
		}
		var ids []int64
		for _, s := range raw {
			if n := httpx.RubyToI(s); n > 0 {
				ids = append(ids, n)
			}
		}
		if len(ids) > 0 {
			cond, err := c.Authz().PrincipalVisibleCondition(c.Ctx())
			if err != nil {
				a.internalError(c, "principal visible", err)
				return
			}
			if users, err = repository.AssignableWatchers(c.Ctx(), a.DB, repository.WatcherCandidateOptions{VisibleCond: cond, IDs: ids, UserFormat: a.Settings.String("user_format")}); err != nil {
				a.internalError(c, "assignable watchers", err)
				return
			}
		}
	}
	if len(users) == 0 {
		httpx.Head(c.W, c.R, http.StatusOK)
		c.Halt()
		return
	}
	c.Render("watchers/append", map[string]any{"Users": users}, RenderOptions{Format: "js", Layout: view.NoLayout})
}

// WatchersDestroy は watchers#destroy。
func (a *App) WatchersDestroy(c *Req) {
	if !a.authorizeForWatchableType(c, "delete") {
		return
	}
	uid, ok := paramInt64(c, "user_id")
	if !ok {
		c.Render404("")
		return
	}
	if _, err := repository.GetPrincipal(c.Ctx(), a.DB, uid); err != nil {
		a.notFoundOr500(c, "find principal", err)
		return
	}
	ws := c.watchables()
	err := a.withTx(c, func(tx *db.Tx) error {
		for _, w := range ws {
			if err := repository.RemoveWatcher(c.Ctx(), tx, w.Kind, w.ID, uid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.internalError(c, "remove watcher", err)
		return
	}
	switch httpx.Negotiate(c.R, "html", "js", "xml", "json") {
	case "html":
		a.redirectToRefererOrText(c, "Watcher removed.")
	case "js":
		data := map[string]any{}
		if len(ws) == 1 {
			data = a.setWatcherData(ws)
		}
		data["Single"] = len(ws) == 1
		c.Render("watchers/destroy", data, RenderOptions{Format: "js", Layout: view.NoLayout})
	case "xml", "json":
		c.RenderAPIOK()
	default:
		respondNotAcceptable(c)
	}
}

// WatchersAutocompleteForUser は watchers#autocomplete_for_user。
func (a *App) WatchersAutocompleteForUser(c *Req) {
	users, err := a.usersForNewWatcher(c)
	if err != nil {
		a.internalError(c, "users for new watcher", err)
		return
	}
	c.Render("watchers/autocomplete_for_user", map[string]any{"Users": users}, RenderOptions{Layout: view.NoLayout})
}

// WatchersAutocompleteForMention は watchers#autocomplete_for_mention（JSON）。
func (a *App) WatchersAutocompleteForMention(c *Req) {
	cond, err := c.Authz().PrincipalVisibleCondition(c.Ctx())
	if err != nil {
		a.internalError(c, "principal visible", err)
		return
	}
	o := repository.WatcherCandidateOptions{VisibleCond: cond, Like: c.Params().String("q"), UserFormat: a.Settings.String("user_format")}
	if httpx.IsBlank(c.Params().String("q")) && c.Project != nil {
		o.ProjectIDs = []int64{c.Project.ID}
	} else {
		o.Limit = 10
	}
	principals, err := repository.AssignableWatchers(c.Ctx(), a.DB, o)
	if err != nil {
		a.internalError(c, "mention candidates", err)
		return
	}
	ws := c.watchables()
	type entry struct {
		Firstname string `json:"firstname"`
		Lastname  string `json:"lastname"`
		Name      string `json:"name"`
		Login     string `json:"login"`
	}
	out := []entry{}
	page := c.Page()
	for _, x := range principals {
		u, ok := x.(*domain.User)
		if !ok {
			continue
		}
		if len(ws) == 1 && ws[0].Type != "enabled_module" && !a.watchableVisible(c, ws[0], u) {
			continue
		}
		out = append(out, entry{Firstname: u.Firstname, Lastname: u.Lastname, Name: page.UserName(u), Login: u.Login})
	}
	b, _ := json.Marshal(out)
	c.Halt()
	c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(b)
}
