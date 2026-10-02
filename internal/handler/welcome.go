package handler

import (
	"net/http"

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
