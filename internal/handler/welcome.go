package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// WelcomeController（app/controllers/welcome_controller.rb）。
var WelcomeController = &Controller{Name: "welcome", MainMenu: false}

// routesWelcome は welcome コントローラのルートを登録する。
func (a *App) routesWelcome(r Router) {
	// root :to => 'welcome#index'
	a.Handle(r, http.MethodGet, "/", WelcomeController, "index", a.WelcomeIndex)
	// get 'robots.:format', :to => 'welcome#robots', :constraints => {:format => 'txt'}
	// skip_before_action :check_if_login_required, only: [:robots]
	a.Handle(r.With(forceFormat("text")), http.MethodGet, "/robots.txt", WelcomeController, "robots", a.WelcomeRobots,
		Skip(FilterLoginRequired))
}

// WelcomeRobots は welcome#robots（welcome/robots.text.erb）。匿名ユーザーに見えるプロジェクトの
// リポジトリ・チケット・活動と、クロールさせたくない画面を Disallow にする。
func (a *App) WelcomeRobots(c *Req) {
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	if a.Settings.Bool("login_required") {
		b.WriteString("Disallow: /\n")
	} else {
		// @projects = Project.visible(User.anonymous)（並びの指定なし = 主キー順）
		cond, err := authz.New(a.DB, a.anonymous(c.Ctx())).VisibleCondition(c.Ctx(), authz.ConditionOptions{})
		if err != nil {
			a.internalError(c, "robots", err)
			return
		}
		projects, err := repository.LoadProjects(c.Ctx(), a.DB, cond)
		if err != nil {
			a.internalError(c, "robots", err)
			return
		}
		sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
		for _, p := range projects {
			for _, id := range []string{p.Identifier, strconv.FormatInt(p.ID, 10)} {
				b.WriteString("Disallow: /projects/" + id + "/repository\n")
				b.WriteString("Disallow: /projects/" + id + "/issues\n")
				b.WriteString("Disallow: /projects/" + id + "/activity\n")
			}
		}
		for _, l := range []string{"/issues/gantt", "/issues/calendar", "/activity", "/search",
			"/issues?*sort=", "/issues?*query_id=", "/issues?*set_filter=", "/issues/*.pdf$", "/projects/*.pdf$",
			"/login", "/account/register", "/account/lost_password"} {
			b.WriteString("Disallow: " + l + "\n")
		}
	}
	c.W.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(b.String()))
	c.Halt()
}

// WelcomeIndex は welcome#index（GET /）。
func (a *App) WelcomeIndex(c *Req) {
	news, err := a.latestNews(c, 5)
	if err != nil {
		a.logger().Error("latest news", "err", err)
	}
	c.Render("welcome/index", map[string]any{
		"News":    news,
		"AtomKey": c.AtomKey(),
	})
}

// latestNews は News.latest(User.current, count)。
func (a *App) latestNews(c *Req, count int) ([]*domain.News, error) {
	cond, err := c.Authz().AllowedToCondition(c.Ctx(), "view_news", authz.ConditionOptions{}, nil)
	if err != nil {
		return nil, err
	}
	return repository.LatestNews(c.Ctx(), a.DB, cond, count)
}
