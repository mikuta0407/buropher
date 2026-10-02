package handler

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/search"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// SearchController（app/controllers/search_controller.rb）。
var SearchController = &Controller{Name: "search", MainMenu: true}

// routesSearch は search コントローラのルートを登録する。
func (a *App) routesSearch(r Router) {
	opts := []ActionOption{FindOptionalProjectByID(), AuthorizeGlobal(), AcceptAPIAuth()}
	// get 'projects/:id/search', :controller => 'search', :action => 'index', :as => 'project_search'
	a.Handle(r, http.MethodGet, "/projects/{id}/search", SearchController, "index", a.SearchIndex, opts...)
	// get 'search', :controller => 'search', :action => 'index'
	a.Handle(r, http.MethodGet, "/search", SearchController, "index", a.SearchIndex, opts...)
}

var reQuickJump = regexp.MustCompile(`(?m)^#?(\d+)$`)

// SearchIndex は search#index（GET /search, /projects/:id/search, .json / .xml）。
func (a *App) SearchIndex(c *Req) {
	ctx := c.Ctx()
	p := c.Params()
	question := strings.TrimSpace(p.String("q"))
	allWords := true
	if _, ok := p.Get("all_words"); ok {
		allWords = p.Present("all_words")
	}
	titlesOnly := false
	if _, ok := p.Get("titles_only"); ok {
		titlesOnly = p.Present("titles_only")
	}
	attachments := "0"
	if p.Present("attachments") {
		attachments = p.String("attachments")
	}
	openIssues := false
	if _, ok := p.Get("open_issues"); ok {
		openIssues = p.Present("open_issues")
	}
	api := httpx.IsAPIRequest(c.R)
	var offset, limit int
	offsetSet := false
	if f := httpx.Format(c.R); f == "xml" || f == "json" {
		offset, limit = c.APIOffsetAndLimit()
		offsetSet = true
	} else {
		limit = int(settings.RubyToI(a.Settings.String("search_results_per_page")))
		if limit == 0 {
			limit = 10
		}
	}

	// quick jump to an issue
	if !api {
		if m := reQuickJump.FindStringSubmatch(question); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			vis, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{})
			if err != nil {
				a.serverError(c, err)
				return
			}
			var n int
			if err := a.DB.Get(ctx, &n, `SELECT COUNT(*) FROM issues INNER JOIN projects ON projects.id = issues.project_id
WHERE issues.id = ? AND (`+vis+`)`, id); err != nil {
				a.serverError(c, err)
				return
			}
			if n > 0 {
				c.Redirect("/issues/" + strconv.FormatInt(id, 10))
				return
			}
		}
	}

	// projects_to_search（nil = 全プロジェクト）
	var projects []int64
	var single *domain.Project
	switch p.String("scope") {
	case "all":
	case "my_projects":
		ps, err := repository.LoadProjects(ctx, a.DB, "projects.status <> ? AND projects.id IN (SELECT project_id FROM members WHERE principal_id = ?)",
			domain.ProjectStatusArchived, c.User.ID)
		if err != nil {
			a.serverError(c, err)
			return
		}
		projects = []int64{}
		for _, pr := range ps {
			projects = append(projects, pr.ID)
		}
	case "bookmarks":
		ids, err := repository.BookmarkedProjectIDs(ctx, a.DB, c.User.ID)
		if err != nil {
			a.serverError(c, err)
			return
		}
		projects = append([]int64{}, ids...)
	case "subprojects":
		if c.Project != nil {
			ps, err := repository.ProjectSelfAndDescendants(ctx, a.DB, c.Project.ID)
			if err != nil {
				a.serverError(c, err)
				return
			}
			projects = []int64{}
			for _, pr := range ps {
				projects = append(projects, pr.ID)
			}
		}
	default:
		if c.Project != nil {
			single = c.Project
			projects = []int64{c.Project.ID}
		}
	}

	objectTypes := search.AvailableSearchTypes()
	if single != nil {
		// don't search projects / only show what the user is allowed to view
		var ts []string
		for _, t := range objectTypes {
			if t == "projects" {
				continue
			}
			if c.AllowedTo(domain.Perm("view_"+t), single) {
				ts = append(ts, t)
			}
		}
		objectTypes = ts
	}
	var scope []string
	for _, t := range objectTypes {
		if p.Present(t) {
			scope = append(scope, t)
		}
	}
	if len(scope) == 0 {
		scope = objectTypes
	}

	f := search.NewFetcher(a.DB, c.Authz(), c.Loc, question, scope, projects, search.Options{
		AllWords: allWords, TitlesOnly: titlesOnly, Attachments: attachments, OpenIssues: openIssues,
	})
	var results []*activity.Event
	var resultCount int
	var countByType map[string]int
	var pages *pagination.Paginator
	hasResults := false
	if len(f.Tokens()) > 0 {
		var err error
		if resultCount, err = f.ResultCount(ctx); err != nil {
			a.serverError(c, err)
			return
		}
		if countByType, err = f.ResultCountByType(ctx); err != nil {
			a.serverError(c, err)
			return
		}
		pages = pagination.New(resultCount, limit, p.String("page"))
		if !offsetSet {
			offset = pages.Offset()
		}
		if results, err = f.Results(ctx, offset, pages.PerPage); err != nil {
			a.serverError(c, err)
			return
		}
		hasResults = true
	} else {
		question = ""
	}
	c.Question = question

	if api {
		c.RenderAPI(0, func(b apibuilder.Builder) {
			b.Array("results", c.APIMeta(apibuilder.A("total_count", nilIfZero(resultCount, hasResults), "offset", offset, "limit", limit)), func() {
				base := httpx.RequestBaseURL(c.R)
				for _, e := range results {
					b.Object("result", func() {
						b.Value("id", e.ID)
						b.Value("title", e.Title)
						b.Value("type", e.Type)
						b.Value("url", base+e.URL)
						if e.DescriptionNull {
							b.Value("description", nil)
						} else {
							b.Value("description", e.Description)
						}
						b.Value("datetime", e.Datetime)
					})
				}
			})
		})
		return
	}

	// project_select_tag の選択肢
	var scopeOptions []any
	scopeOptions = append(scopeOptions, []any{c.L("label_project_all"), "all"})
	if c.User.Logged() {
		var n int
		if err := a.DB.Get(ctx, &n, `SELECT COUNT(*) FROM members INNER JOIN projects ON projects.id = members.project_id
WHERE members.principal_id = ? AND projects.status <> ?`, c.User.ID, domain.ProjectStatusArchived); err != nil {
			a.serverError(c, err)
			return
		}
		if n > 0 {
			scopeOptions = append(scopeOptions, []any{c.L("label_my_projects"), "my_projects"})
		}
		bm, err := repository.BookmarkedProjectIDs(ctx, a.DB, c.User.ID)
		if err != nil {
			a.serverError(c, err)
			return
		}
		if len(bm) > 0 {
			scopeOptions = append(scopeOptions, []any{c.L("label_my_bookmarks"), "bookmarks"})
		}
	}
	if c.Project != nil {
		var n int
		if err := a.DB.Get(ctx, &n, `SELECT COUNT(*) FROM projects WHERE status = ?
AND id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ? AND depth > 0)`, domain.ProjectStatusActive, c.Project.ID); err != nil {
			a.serverError(c, err)
			return
		}
		if n > 0 {
			scopeOptions = append(scopeOptions, []any{c.L("label_and_its_subprojects", i18n.Vars{"value": c.Project.Name}), "subprojects"})
		}
		scopeOptions = append(scopeOptions, []any{c.Project.Name, ""})
	}
	scopeSet := map[string]bool{}
	for _, t := range scope {
		scopeSet[t] = true
	}
	path := strings.TrimSuffix(c.R.URL.Path, ".html")
	data := map[string]any{
		"Question":      question,
		"AllWords":      allWords,
		"TitlesOnly":    titlesOnly,
		"Attachments":   attachments,
		"OpenIssues":    openIssues,
		"ObjectTypes":   objectTypes,
		"Scope":         scopeSet,
		"ScopeSize":     len(scope),
		"ScopeOptions":  scopeOptions,
		"HasResults":    hasResults,
		"Results":       results,
		"ResultCount":   resultCount,
		"CountByType":   countByType,
		"Tokens":        f.Tokens(),
		"Pages":         pages,
		"Path":          path,
		"ShowIssuesBtn": countByType["issues"] > 0 && attachments == "0",
	}
	if hasResults {
		data["ResultsByType"] = a.Helpers.RenderResultsByType(c.Page(), path, countByType, f.TypeOrder())
		data["IssuesFilterPath"] = issuesFilterPath(c, question, p.String("scope"), allWords, titlesOnly, openIssues)
	}
	var ropts []RenderOptions
	if httpx.IsXHR(c.R) {
		ropts = append(ropts, RenderOptions{Layout: view.NoLayout})
	}
	c.Render("search/index", data, ropts...)
}

// nilIfZero は @result_count（トークンが無ければ nil）。
func nilIfZero(n int, present bool) any {
	if !present {
		return nil
	}
	return n
}

// issuesFilterPath は SearchHelper#issues_filter_path。
func issuesFilterPath(c *Req, question, projectsScope string, allWords, titlesOnly, openIssues bool) string {
	field := "any_searchable"
	if titlesOnly {
		field = "subject"
	}
	statusOp := "*"
	if openIssues {
		statusOp = "o"
	}
	fieldOp := "*~"
	if allWords {
		fieldOp = "~"
	}
	f := []any{"status_id", field}
	op := rails.NewHash("status_id", statusOp, field, fieldOp)
	v := rails.NewHash(field, []any{question})
	params := rails.NewHash("set_filter", 1)
	var projectID any
	switch projectsScope {
	case "all":
	case "my_projects":
		f = append(f, "project_id")
		op.Set("project_id", "=")
		v.Set("project_id", []any{"mine"})
	case "bookmarks":
		f = append(f, "project_id")
		op.Set("project_id", "=")
		v.Set("project_id", []any{"bookmarks"})
	case "subprojects":
		f = append(f, "subproject_id")
		op.Set("subproject_id", "*")
		if c.Project != nil {
			projectID = c.Project.ID
		}
	default:
		if c.Project != nil {
			f = append(f, "subproject_id")
			op.Set("subproject_id", "!*")
			projectID = c.Project.ID
		}
	}
	params.Set("f", f).Set("op", op).Set("v", v).Set("sort", "updated_on:desc")
	if projectID != nil {
		params.Set("project_id", projectID)
	}
	return helper.URLWithQuery("/issues", params)
}
