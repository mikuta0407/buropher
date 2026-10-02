package handler

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
)

// ActivitiesController（app/controllers/activities_controller.rb）。menu_item :activity。
var ActivitiesController = &Controller{Name: "activities", MainMenu: true,
	MenuItem: func(string) string { return "activity" }}

// routesActivities は activities コントローラのルートを登録する。
func (a *App) routesActivities(r Router) {
	opts := []ActionOption{FindOptionalProjectByID(), AuthorizeGlobal(), AcceptAtomAuth()}
	// get 'projects/:id/activity', :to => 'activities#index', :as => :project_activity
	a.Handle(r, http.MethodGet, "/projects/{id}/activity", ActivitiesController, "index", a.ActivitiesIndex, opts...)
	// get '/activity', :to => 'activities#index'
	a.Handle(r, http.MethodGet, "/activity", ActivitiesController, "index", a.ActivitiesIndex, opts...)
}

// userToday は User.current.today（タイムゾーン未設定ならサーバのローカル日付）。日付は UTC の 0 時で表す。
func (a *App) userToday(c *Req) time.Time {
	return dateOnly(i18n.UserTime(a.now(), c.Loc.Location))
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

var reParamDate = regexp.MustCompile(`^\s*(\d{4})-(\d{1,2})-(\d{1,2})`)

// parseParamDate は String#to_date（YYYY-MM-DD 形式のみ。解析できなければ false）。
func parseParamDate(s string) (time.Time, bool) {
	m := reParamDate.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

// findVisibleActiveUser は User.visible.active.find(id)。
func (a *App) findVisibleActiveUser(c *Req, idParam string) (*domain.User, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idParam), 10, 64)
	if err != nil || id <= 0 {
		// Rails の find は to_i するので "2abc" は 2 になる
		id = int64(httpx.RubyToI(idParam))
		if id <= 0 {
			return nil, repository.ErrNotFound
		}
	}
	u, err := repository.FindActiveUser(c.Ctx(), a.DB, id)
	if err != nil {
		return nil, err
	}
	cond, err := c.Authz().PrincipalVisibleCondition(c.Ctx())
	if err != nil {
		return nil, err
	}
	ok, err := repository.PrincipalVisible(c.Ctx(), a.DB, cond, u.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, repository.ErrNotFound
	}
	return u, nil
}

// ActivitiesIndex は activities#index（GET /activity, /projects/:id/activity, .atom）。
func (a *App) ActivitiesIndex(c *Req) {
	ctx := c.Ctx()
	p := c.Params()
	days := int(settings.RubyToI(a.Settings.String("activity_days_default")))
	var dateTo time.Time
	if v, ok := p.Get("from"); ok {
		if s, ok := v.(string); ok {
			if d, ok := parseParamDate(s); ok {
				dateTo = d.AddDate(0, 0, 1)
			}
		}
	}
	today := a.userToday(c)
	if dateTo.IsZero() {
		dateTo = today.AddDate(0, 0, 1)
	}
	dateFrom := dateTo.AddDate(0, 0, -days)
	withSubprojects := a.Settings.Bool("display_subprojects_issues")
	if _, ok := p.Get("with_subprojects"); ok {
		withSubprojects = p.String("with_subprojects") == "1"
	}
	var author *domain.User
	if p.Present("user_id") {
		u, err := a.findVisibleActiveUser(c, p.String("user_id"))
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.Render404("")
				return
			}
			a.serverError(c, err)
			return
		}
		author = u
	}
	f, err := activity.NewFetcher(ctx, a.DB, c.Authz(), c.Loc, activity.Options{
		Project: c.Project, WithSubprojects: withSubprojects, Author: author,
	})
	if err != nil {
		a.serverError(c, err)
		return
	}
	f.ScopeSelect(func(t string) bool { _, ok := p.Get("show_" + t); return ok })
	if len(f.Scope()) > 0 {
		if p.Present("submit") && c.User.Logged() {
			if err := repository.SetUserPrefExtra(ctx, a.DB, c.User.ID, "activity_scope", f.Scope()); err != nil {
				a.serverError(c, err)
				return
			}
		}
	} else if author == nil {
		var saved []string
		if c.User.Logged() {
			if saved, err = repository.UserPrefExtraStrings(ctx, a.DB, c.User.ID, "activity_scope"); err != nil {
				a.serverError(c, err)
				return
			}
		}
		var scope []string
		for _, t := range saved {
			if slices.Contains(f.EventTypes(), t) && !slices.Contains(scope, t) {
				scope = append(scope, t)
			}
		}
		if len(scope) > 0 {
			f.SetScope(scope)
		} else {
			f.SetScopeDefault()
		}
	} else {
		f.SetScopeAll()
	}

	if httpx.Format(c.R) == "atom" {
		events, err := f.Events(ctx, nil, nil, int(settings.RubyToI(a.Settings.String("feeds_limit"))))
		if err != nil {
			a.serverError(c, err)
			return
		}
		title := c.L("label_activity")
		switch {
		case author != nil:
			title = c.Page().UserName(author)
		case len(f.Scope()) == 1:
			title = c.L("label_" + helper.ActivitySingularize(f.Scope()[0]) + "_plural")
		}
		prefix := a.Settings.String("app_title")
		if c.Project != nil {
			prefix = c.Project.Name
		}
		a.renderFeed(c, events, prefix+": "+title)
		return
	}
	if httpx.Format(c.R) != "html" && httpx.Format(c.R) != "" {
		c.RenderError(http.StatusNotAcceptable, "")
		return
	}

	events, err := f.Events(ctx, &dateFrom, &dateTo, 0)
	if err != nil {
		a.serverError(c, err)
		return
	}
	env, err := query.NewEnv(ctx, a.DB, c.User, a.Settings)
	if err != nil {
		a.serverError(c, err)
		return
	}
	env.L = c.Loc
	env.Now = a.now
	users, err := query.ActiveUsers(ctx, env, c.Project)
	if err != nil {
		a.serverError(c, err)
		return
	}
	// activity_authors_options_for_select
	var authorOptions []any
	if c.User.Logged() {
		authorOptions = append(authorOptions, []any{"<< " + c.L("label_me") + " >>", c.User.ID})
	}
	for _, u := range users {
		authorOptions = append(authorOptions, []any{u.Name, u.ID})
	}
	hasSubprojects := false
	if c.Project != nil {
		cond, err := c.Authz().VisibleCondition(ctx, authz.ConditionOptions{})
		if err != nil {
			a.serverError(c, err)
			return
		}
		ds, err := repository.LoadProjects(ctx, a.DB, "projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ? AND depth > 0) AND ("+cond+")", c.Project.ID)
		if err != nil {
			a.serverError(c, err)
			return
		}
		hasSubprojects = len(ds) > 0
	}
	scopeSet := map[string]bool{}
	for _, t := range f.Scope() {
		scopeSet[t] = true
	}
	qp := c.Page().QueryParameters()
	path := strings.TrimSuffix(c.R.URL.Path, ".html")
	link := func(from time.Time) string {
		q := qp.Clone()
		q.Set("from", from.Format("2006-01-02"))
		return helper.URLWithQuery(path, q)
	}
	fd := func(t time.Time) string { return c.Loc.FormatDate(t) }
	dateRange := func(from, to time.Time) string {
		return c.L("label_date_from_to", i18n.Vars{"start": fd(from), "end": fd(to)})
	}
	atomKey := c.AtomKey()
	// link_to_with_query_parameters 'Atom', 'from' => nil, :key => User.current.atom_key
	atomParams := qp.Except("page", "format", "from", "key")
	if atomKey != "" {
		atomParams.Set("key", atomKey)
	}
	// auto_discovery_link_tag(:atom, :params => query_parameters.merge(:from => nil, :key => atom_key), :format => 'atom')
	discParams := qp.Except("from", "key")
	if atomKey != "" {
		discParams.Set("key", atomKey)
	}
	var ropts []RenderOptions
	if httpx.IsXHR(c.R) {
		ropts = append(ropts, RenderOptions{Layout: view.NoLayout})
	}
	c.Render("activities/index", map[string]any{
		"Author":         author,
		"Days":           days,
		"DateTo":         dateTo,
		"DateFrom":       dateFrom,
		"LastDate":       dateTo.AddDate(0, 0, -1),
		"EventsByDay":    helper.GroupActivityEvents(events, c.Loc.Location),
		"EventTypes":     f.EventTypes(),
		"Scope":          scopeSet,
		"AuthorOptions":  authorOptions,
		"WithSubproject": withSubprojects,
		"HasSubprojects": hasSubprojects,
		"Subtitle":       dateRange(dateFrom, dateTo.AddDate(0, 0, -1)),
		"PrevURL":        link(dateTo.AddDate(0, 0, -days-1)),
		"PrevTitle":      dateRange(dateTo.AddDate(0, 0, -2*days), dateTo.AddDate(0, 0, -days-1)),
		"ShowNext":       !dateTo.After(today),
		"NextURL":        link(dateTo.AddDate(0, 0, days-1)),
		"NextTitle":      dateRange(dateTo, dateTo.AddDate(0, 0, days-1)),
		"AtomURL":        helper.URLWithQuery(path+".atom", atomParams),
		"AtomFeedURL":    httpx.RequestBaseURL(c.R) + helper.URLWithQuery(path+".atom", discParams),
		"FormAction":     path,
	}, ropts...)
}

// userActivityEvents は UsersController#show の最近の活動
// （Redmine::Activity::Fetcher.new(User.current, :author => @user).events(nil, nil, :limit => 10)）。
func (a *App) userActivityEvents(c *Req, u *domain.User) ([]helper.ActivityDay, error) {
	f, err := activity.NewFetcher(c.Ctx(), a.DB, c.Authz(), c.Loc, activity.Options{Author: u})
	if err != nil {
		return nil, err
	}
	events, err := f.Events(c.Ctx(), nil, nil, 10)
	if err != nil {
		return nil, err
	}
	return helper.GroupActivityEvents(events, c.Loc.Location), nil
}

