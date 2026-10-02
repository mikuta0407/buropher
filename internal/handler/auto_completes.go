package handler

// AutoCompletesController（app/controllers/auto_completes_controller.rb）の issues / wiki_pages。

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// AutoCompletesController。
var AutoCompletesController = &Controller{Name: "auto_completes", MainMenu: true}

// routesAutoCompletes は
//
//	match '/issues/auto_complete', :to => 'auto_completes#issues', :via => :get, :as => 'auto_complete_issues'
//	match '/wiki_pages/auto_complete', :to => 'auto_completes#wiki_pages', :via => :get, :as => 'auto_complete_wiki_pages'
func (a *App) routesAutoCompletes(r Router) {
	// before_action :find_project
	a.Handle(r, http.MethodGet, "/issues/auto_complete", AutoCompletesController, "issues", a.AutoCompletesIssues,
		Before(a.findAutoCompleteProject))
	a.Handle(r, http.MethodGet, "/wiki_pages/auto_complete", AutoCompletesController, "wiki_pages", a.AutoCompletesWikiPages,
		Before(a.findAutoCompleteProject))
}

// AutoCompletesWikiPages は auto_completes#wiki_pages（JSON 配列 {id, label, value}。新しい順に 10 件）。
// Wiki が無い（プロジェクト未指定を含む）か view_wiki_pages が無ければ []。
func (a *App) AutoCompletesWikiPages(c *Req) {
	out := make([]any, 0, 10)
	if c.Project == nil || !c.AllowedTo(domain.Perm("view_wiki_pages"), c.Project) {
		renderJSON(c, out)
		return
	}
	wiki, err := repository.FindWiki(c.Ctx(), a.DB, c.Project.ID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.internalError(c, "auto complete wiki", err)
			return
		}
		renderJSON(c, out)
		return
	}
	q := strings.TrimSpace(c.Params().String("q"))
	pages, err := repository.AutoCompleteWikiPages(c.Ctx(), a.DB, wiki.ID, q, 10)
	if err != nil {
		a.internalError(c, "auto complete wiki", err)
		return
	}
	for _, p := range pages {
		out = append(out, rails.NewHash("id", p.ID, "label", rubyTruncate(p.Title, 255), "value", p.Title))
	}
	renderJSON(c, out)
}

// findAutoCompleteProject は AutoCompletesController#find_project（project_id があれば Project.find。無ければ 404）。
func (a *App) findAutoCompleteProject(c *Req) {
	if id := c.Params().String("project_id"); !rails.IsBlank(id) {
		c.FindProject(id)
	}
}

var autoCompleteIDRe = regexp.MustCompile(`\A#?(\d+)\z`)

// AutoCompletesIssues は auto_completes#issues（JSON 配列 {id, label, value}）。
func (a *App) AutoCompletesIssues(c *Req) {
	p := c.Params()
	qv, ok := p.Get("q")
	if !ok || qv == nil {
		qv, _ = p.Get("term")
	}
	q := strings.TrimSpace(rubyToS(qv))
	status := p.String("status")
	issueID := p.String("issue_id")

	vis, err := c.Authz().IssueVisibleCondition(c.Ctx(), issueVisOpts())
	if err != nil {
		a.internalError(c, "issue visible", err)
		return
	}
	scope := repository.AutoCompleteIssuesScope{Scope: p.String("scope"), VisibleCond: vis}
	if c.Project != nil {
		scope.ProjectID = c.Project.ID
	}
	if status != "" {
		open := status == "o"
		scope.Open = &open
	}
	if issueID != "" {
		scope.HasExclude = true
		scope.ExcludeID = rubyStringToI(issueID)
	}
	var list []*repository.AutoCompleteIssue
	if q != "" {
		if m := autoCompleteIDRe.FindStringSubmatch(q); m != nil {
			// scope.find_by(:id => $1.to_i)
			found, err := repository.AutoCompleteIssues(c.Ctx(), a.DB, scope, "issues.id = ?", []any{rubyStringToI(m[1])}, 1)
			if err != nil {
				a.internalError(c, "auto complete", err)
				return
			}
			list = append(list, found...)
		}
		like, args := query.TokenizedLikeCondition(a.DB.Dialect(), "issues.subject", q)
		rows, err := repository.AutoCompleteIssues(c.Ctx(), a.DB, scope, like, args, 10)
		if err != nil {
			a.internalError(c, "auto complete", err)
			return
		}
		list = append(list, rows...)
	} else {
		rows, err := repository.AutoCompleteIssues(c.Ctx(), a.DB, scope, "", nil, 10)
		if err != nil {
			a.internalError(c, "auto complete", err)
			return
		}
		list = rows
	}
	out := make([]any, 0, len(list))
	for _, r := range list {
		out = append(out, rails.NewHash("id", r.ID,
			"label", r.Tracker+" #"+strconv.FormatInt(r.ID, 10)+": "+rubyTruncate(r.Subject, 255),
			"value", r.ID))
	}
	renderJSON(c, out)
}

// rubyToS は params の値の to_s（配列・ハッシュは空扱い）。
func rubyToS(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// rubyStringToI は String#to_i（先頭の数字列。無ければ 0）。
func rubyStringToI(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	neg := false
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		neg = s[0] == '-'
		s = s[1:]
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			if r == '_' {
				continue
			}
			break
		}
		n = n*10 + int64(r-'0')
	}
	if neg {
		return -n
	}
	return n
}

// rubyTruncate は String#truncate(length)（省略記号 "..." を含めて length 文字）。
func rubyTruncate(s string, length int) string {
	r := []rune(s)
	if len(r) <= length {
		return s
	}
	return string(r[:length-3]) + "..."
}
