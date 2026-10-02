package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Routes は config/routes.rb のうち実装済みのルートを登録する。
// r はセッション等のミドルウェアを適用済みのルータ（ルーティング後の CSRF 検証は Handle が行う）。
func (a *App) Routes(r chi.Router) {
	// root :to => 'welcome#index'
	a.Handle(r, http.MethodGet, "/", WelcomeController, "index", a.WelcomeIndex)

	// match 'login', :to => 'account#login', :as => 'signin', :via => [:get, :post]
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/login", AccountController, "login", a.AccountLogin,
			Skip(FilterLoginRequired, FilterPasswordChange))
		// match 'logout', :to => 'account#logout', :as => 'signout', :via => [:get, :post]
		a.Handle(r, m, "/logout", AccountController, "logout", a.AccountLogout,
			Skip(FilterLoginRequired, FilterPasswordChange, FilterTwofaActivation))
	}
}
