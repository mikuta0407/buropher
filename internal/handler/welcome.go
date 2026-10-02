package handler

// WelcomeController（app/controllers/welcome_controller.rb）。
var WelcomeController = &Controller{Name: "welcome", MainMenu: false}

// WelcomeIndex は welcome#index（GET /）。
func (a *App) WelcomeIndex(c *Req) {
	news, err := a.Users.LatestNews(c.Ctx(), c.User, 5)
	if err != nil {
		a.logger().Error("latest news", "err", err)
	}
	c.Render("welcome/index", map[string]any{
		"News":    news,
		"AtomKey": c.User.AtomKey(),
	})
}
