package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// MyController（app/controllers/my_controller.rb）。main_menu = false。
//
// before_action :require_login（全アクション）。password は check_password_change / check_twofa_activation を省く。
// TODO(sudo): require_sudo_mode（:account の PUT, reset_atom_key, reset_api_key, show_api_key, destroy）。
// Redmine の既定（config の sudo_mode 無効）では何もしないため未実装。
var MyController = &Controller{Name: "my", MainMenu: false}

// routesMy は my コントローラのルートを登録する。
func (a *App) routesMy(r Router) {
	login := RequireLogin()
	// match 'my/account', :via => [:get, :put]（accept_api_auth :account）
	a.Handle(r, http.MethodGet, "/my/account", MyController, "account", a.MyAccount, AcceptAPIAuth(), login)
	a.Handle(r, http.MethodPut, "/my/account", MyController, "account", a.MyAccount, AcceptAPIAuth(), login)
	// match 'my/account/destroy', :via => [:get, :post], :as => :delete_my_account
	a.Handle(r, http.MethodGet, "/my/account/destroy", MyController, "destroy", a.MyDestroy, login)
	a.Handle(r, http.MethodPost, "/my/account/destroy", MyController, "destroy", a.MyDestroy, login)
	// match 'my/page', :via => :get / post 'my/page', :to => 'my#update_page'
	a.Handle(r, http.MethodGet, "/my/page", MyController, "page", a.MyPage, login)
	a.Handle(r, http.MethodPost, "/my/page", MyController, "update_page", a.MyUpdatePage, login)
	// match 'my', :action => 'index', :via => :get
	a.Handle(r, http.MethodGet, "/my", MyController, "index", a.MyIndex, login)
	// get 'my/api_key' / post 'my/api_key' / post 'my/atom_key'
	a.Handle(r, http.MethodGet, "/my/api_key", MyController, "show_api_key", a.MyShowAPIKey, login)
	a.Handle(r, http.MethodPost, "/my/api_key", MyController, "reset_api_key", a.MyResetAPIKey, login)
	a.Handle(r, http.MethodPost, "/my/atom_key", MyController, "reset_atom_key", a.MyResetAtomKey, login)
	// match 'my/password', :via => [:get, :post]
	pwOpts := []ActionOption{Skip(FilterPasswordChange, FilterTwofaActivation), login}
	a.Handle(r, http.MethodGet, "/my/password", MyController, "password", a.MyPassword, pwOpts...)
	a.Handle(r, http.MethodPost, "/my/password", MyController, "password", a.MyPassword, pwOpts...)
	// match 'my/add_block' / 'my/remove_block' / 'my/order_blocks', :via => :post
	a.Handle(r, http.MethodPost, "/my/add_block", MyController, "add_block", a.MyAddBlock, login)
	a.Handle(r, http.MethodPost, "/my/remove_block", MyController, "remove_block", a.MyRemoveBlock, login)
	a.Handle(r, http.MethodPost, "/my/order_blocks", MyController, "order_blocks", a.MyOrderBlocks, login)
}

// ---------------------------------------------------------------- account

// myAccountData は my/account のデータ（users/_mail_notifications・_preferences・_auto_watch_on と共通）。
func (a *App) myAccountData(c *Req, m *userModel) (map[string]any, error) {
	data, err := a.userFormData(c, m)
	if err != nil {
		return nil, err
	}
	data["ChangePasswordAllowed"] = a.changePasswordAllowed(c, m.User)
	data["RestAPIEnabled"] = a.Settings.Bool("rest_api_enabled")
	data["TwofaSchemes"] = []string{"totp"}
	// avatar_edit_link（Setting.gravatar_enabled? のときアバターをアバターサーバへのリンクにする）
	data["GravatarEnabled"] = a.Settings.Bool("gravatar_enabled")
	data["AvatarServerURL"] = "https://www.gravatar.com"
	data["TwofaActive"] = m.TwofaScheme != ""
	if m.TwofaScheme != "" {
		n, err := repository.CountTwofaBackupCodes(c.Ctx(), a.DB, m.ID)
		if err != nil {
			return nil, err
		}
		data["TwofaBackupCodes"] = n > 0
	}
	var editable []any
	for _, cv := range m.customValues {
		if cv.Field.Editable {
			editable = append(editable, customFieldTagWithLabel("user", cv, m.errors.Include(cv.Field.Name)))
		}
	}
	data["EditableCustomFieldTags"] = editable
	if err := a.mySidebarData(c, m.User, data); err != nil {
		return nil, err
	}
	return data, nil
}

// mySidebarData は my/_sidebar のデータ。
func (a *App) mySidebarData(c *Req, u *domain.User, data map[string]any) error {
	ctx := c.Ctx()
	data["SidebarUser"] = u
	// buropher 拡張: OIDC 認証方式があれば外部 ID 連携へのリンクを出す
	data["SSOEnabled"] = len(a.oidcSources(c)) > 0
	data["OwnAccountDeletable"] = a.ownAccountDeletable(c, u)
	atom, err := repository.UserToken(ctx, a.DB, u.ID, repository.TokenFeeds)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if atom != nil {
		data["AtomTokenAge"] = c.Loc.DistanceOfTimeInWords(a.now(), atom.CreatedAt)
	}
	data["RestAPIEnabled"] = a.Settings.Bool("rest_api_enabled")
	api, err := repository.UserToken(ctx, a.DB, u.ID, repository.TokenAPI)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if api != nil {
		data["APITokenAge"] = c.Loc.DistanceOfTimeInWords(a.now(), api.CreatedAt)
	}
	return nil
}

// changePasswordAllowed は User#change_password_allowed?（外部認証のユーザーは変更できない）。
// TODO(auth): AuthSource#allow_password_changes?（LDAP は false）。
func (a *App) changePasswordAllowed(c *Req, u *domain.User) bool {
	return u.AuthSourceID == nil
}

// MyAccount は my#account（GET / PUT /my/account(.:format)）。
func (a *App) MyAccount(c *Req) {
	u, err := repository.GetUser(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	m, err := a.loadUserModel(c, u)
	if err != nil {
		a.serverError(c, err)
		return
	}
	api := httpx.IsAPIRequest(c.R)
	if c.R.Method == http.MethodPut {
		m.assignSafeAttributes(c.Params().Map("user"), c.User)
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
			// before_save :clear_unused_block_settings（pref.save のたびに未使用ブロックの設定を消す）
			if err := a.clearUnusedMyPageSettings(c, m.ID); err != nil {
				a.serverError(c, err)
				return
			}
			// TODO(mail): User#deliver_security_notification（メールアドレス変更時のセキュリティ通知）
			if api {
				c.RenderAPIOK()
				return
			}
			c.Flash().SetNotice(c.L("notice_account_updated"))
			c.Redirect("/my/account")
			return
		}
		if api {
			c.RenderValidationErrors(m.errors)
			return
		}
		// Redmine では @user と User.current が同じオブジェクトのため、保存に失敗した値が
		// レイアウト（アバターの頭文字など）にも表れる
		c.User = m.User
	} else if api {
		a.renderMyAccountAPI(c, m.User)
		return
	}
	data, err := a.myAccountData(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("my/account", data)
}

// renderMyAccountAPI は my/account.api.rsb。
func (a *App) renderMyAccountAPI(c *Req, u *domain.User) {
	apiKey, err := repository.APIKey(c.Ctx(), a.DB, u.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	cvs, err := a.loadPrincipalCustomValues(c, "user", []*domain.User{u})
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.RenderAPI(0, func(b apibuilder.Builder) {
		b.Object("user", func() {
			b.Value("id", u.ID)
			b.Value("login", u.Login)
			b.Value("admin", u.IsAdmin())
			b.Value("firstname", u.Firstname)
			b.Value("lastname", u.Lastname)
			b.Value("mail", nilIfEmpty(u.Mail))
			b.Value("created_on", u.CreatedAt)
			b.Value("last_login_on", u.LastLoginAt)
			b.Value("api_key", apiKey)
			renderAPICustomValues(b, cvs[u.ID])
		})
	})
}

// ---------------------------------------------------------------- destroy

// MyDestroy は my#destroy（GET / POST /my/account/destroy）。
func (a *App) MyDestroy(c *Req) {
	u := c.User
	if !a.ownAccountDeletable(c, u) {
		c.Redirect("/my/account")
		return
	}
	if c.R.Method == http.MethodPost && c.Params().Present("confirm") {
		if err := a.destroyUser(c, u.ID); err != nil {
			a.serverError(c, err)
			return
		}
		a.logoutUser(c)
		c.Flash().SetNotice(c.L("notice_account_deleted"))
		c.Redirect("/")
		return
	}
	c.Render("my/destroy", map[string]any{"User": u})
}

// ---------------------------------------------------------------- password

// MyPassword は my#password（GET / POST /my/password）。
func (a *App) MyPassword(c *Req) {
	u, err := repository.GetUser(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if !a.changePasswordAllowed(c, u) {
		c.Flash().SetError(c.L("notice_can_t_change_password"))
		c.Redirect("/my/account")
		return
	}
	m, err := a.loadUserModel(c, u)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if c.R.Method == http.MethodPost {
		p := c.Params()
		current := p.String("password")
		ok, verr := password.Verify(u.PasswordHash, current)
		switch {
		case verr != nil || !ok || current == "":
			c.Flash().Now("error", c.L("notice_account_wrong_password"))
		case current == p.String("new_password"):
			c.Flash().Now("error", c.L("notice_new_password_must_be_different"))
		default:
			pw, conf := p.String("new_password"), p.String("new_password_confirmation")
			m.password, m.passwordConfirmation = &pw, &conf
			m.MustChangePassword = false
			saved, err := a.saveUser(c, m)
			if err != nil {
				a.serverError(c, err)
				return
			}
			if saved {
				// パスワード変更で全セッション（session トークン）が破棄されたので、
				// 現在のセッションは新しい ID で続ける（session[:tk] = generate_session_token）。
				if s := c.Session(); s != nil {
					s.Renew()
				}
				// TODO(mail): Mailer.deliver_password_updated(@user, User.current)（セキュリティ通知）
				a.logger().Info("password updated mail (not delivered: mail delivery is not implemented)", "user", u.Login)
				c.Flash().SetNotice(c.L("notice_account_password_updated"))
				c.Redirect("/my/account")
				return
			}
		}
	}
	c.NoStore()
	data := map[string]any{
		"User":               m,
		"MustChangePassword": a.mustChangePassword(u),
		"PasswordMinLength":  a.Settings.String("password_min_length"),
	}
	var classes []string
	for _, k := range a.Settings.Strings("password_required_char_classes") {
		classes = append(classes, c.L("label_password_char_class_"+k))
	}
	data["PasswordCharClasses"] = strings.Join(classes, ", ")
	if err := a.mySidebarData(c, u, data); err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("my/password", data)
}

// ---------------------------------------------------------------- keys

// MyResetAtomKey は my#reset_atom_key（POST /my/atom_key）。
func (a *App) MyResetAtomKey(c *Req) {
	if err := a.resetUserKey(c, repository.TokenFeeds); err != nil {
		a.serverError(c, err)
		return
	}
	c.Flash().SetNotice(c.L("notice_feeds_access_key_reseted"))
	c.Redirect("/my/account")
}

// MyResetAPIKey は my#reset_api_key（POST /my/api_key）。
func (a *App) MyResetAPIKey(c *Req) {
	if err := a.resetUserKey(c, repository.TokenAPI); err != nil {
		a.serverError(c, err)
		return
	}
	c.Flash().SetNotice(c.L("notice_api_access_key_reseted"))
	c.Redirect("/my/account")
}

// resetUserKey はトークンを破棄して作り直す（User#atom_key / api_key は無ければ作成する）。
func (a *App) resetUserKey(c *Req, action string) error {
	if err := repository.DeleteUserTokens(c.Ctx(), a.DB, c.User.ID, action); err != nil {
		return err
	}
	_, err := repository.CreateToken(c.Ctx(), a.DB, c.User.ID, action)
	return err
}

// MyShowAPIKey は my#show_api_key（GET /my/api_key(.js)）。
func (a *App) MyShowAPIKey(c *Req) {
	key, err := repository.APIKey(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	data := map[string]any{"APIKey": key}
	if httpx.Format(c.R) == "js" || httpx.IsXHR(c.R) {
		c.Render("my/show_api_key", data, RenderOptions{Format: "js", Layout: view.NoLayout})
		return
	}
	c.Render("my/show_api_key", data)
}
