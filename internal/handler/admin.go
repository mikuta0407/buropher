package handler

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

// AdminController（app/controllers/admin_controller.rb）のうち projects 以外のアクション
// （index / plugins / info / default_configuration / test_email）。projects は projects_admin.go。
// layout 'admin'、main_menu false、menu_item :plugins / :info（menu.CurrentMenuItem の対応表で決まる）。
var AdminController = AdminProjectsController

// routesAdmin は admin コントローラ（projects 以外）のルートを登録する。
//
//	get 'admin', :to => 'admin#index'
//	get 'admin/plugins', :to => 'admin#plugins'
//	get 'admin/info', :to => 'admin#info'
//	post 'admin/test_email', :to => 'admin#test_email', :as => 'test_email'
//	post 'admin/default_configuration', :to => 'admin#default_configuration'
func (a *App) routesAdmin(r Router) {
	a.Handle(r, http.MethodGet, "/admin", AdminController, "index", a.AdminIndex, RequireAdmin())
	a.Handle(r, http.MethodGet, "/admin/plugins", AdminController, "plugins", a.AdminPlugins, RequireAdmin())
	a.Handle(r, http.MethodGet, "/admin/info", AdminController, "info", a.AdminInfo, RequireAdmin())
	a.Handle(r, http.MethodPost, "/admin/test_email", AdminController, "test_email", a.AdminTestEmail, RequireAdmin())
	a.Handle(r, http.MethodPost, "/admin/default_configuration", AdminController, "default_configuration", a.AdminDefaultConfiguration, RequireAdmin())
}

// AdminIndex は admin#index（@no_configuration_data = Redmine::DefaultData::Loader::no_data?）。
func (a *App) AdminIndex(c *Req) {
	noData, err := repository.NoConfigurationData(c.Ctx(), a.DB)
	if err != nil {
		c.RenderError(http.StatusInternalServerError, err.Error())
		return
	}
	c.renderAdmin("admin/index", map[string]any{"NoConfigurationData": noData}, false)
}

// AdminPlugins は admin#plugins。buropher はプラグインに対応しないため常に空（@plugins = []）。
func (a *App) AdminPlugins(c *Req) {
	c.renderAdmin("admin/plugins", map[string]any{"Plugins": []any{}}, false)
}

// AdminDefaultConfiguration は admin#default_configuration（POST。既定の設定データを投入する）。
func (a *App) AdminDefaultConfiguration(c *Req) {
	// Loader.load(lang): データがあれば言語を変えずに DataAlreadyLoaded、無ければ set_language_if_valid
	// （以降の flash もその言語になる）
	if empty, err := repository.NoConfigurationData(c.Ctx(), a.DB); err == nil && empty {
		c.Loc.SetLanguageIfValid(c.Params().String("lang"))
	}
	if err := bootstrap.LoadDefaultData(c.Ctx(), a.DB, c.Loc.Lang); err != nil {
		c.Flash().SetError(c.L("error_can_t_load_default_data", html.EscapeString(err.Error())))
	} else {
		if err := a.Settings.Reload(c.Ctx()); err != nil {
			a.logger().Error("reload settings", "err", err)
		}
		c.Flash().SetNotice(c.L("notice_default_data_loaded"))
	}
	c.Redirect("/admin")
}

// TestEmailSender は Mailer.deliver_test_email(user) の差し込み口（メール送信の移植で設定する）。
// nil なら送信できないものとしてエラーを表示する。
var TestEmailSender func(c *Req, to string) error

// AdminTestEmail は admin#test_email。
func (a *App) AdminTestEmail(c *Req) {
	mail := c.User.Mail
	var err error
	if TestEmailSender == nil {
		// 意図的な差異: メール送信が未実装の間は常に失敗として扱う
		err = fmt.Errorf("email delivery is not configured")
	} else {
		err = TestEmailSender(c, mail)
	}
	if err != nil {
		c.Flash().SetError(c.L("notice_email_error", html.EscapeString(err.Error())))
	} else {
		c.Flash().SetNotice(c.L("notice_email_sent", html.EscapeString(mail)))
	}
	c.Redirect("/settings?tab=notifications")
}

// adminInfoCheck は admin/info の確認項目 1 件（[label, result]）。
type adminInfoCheck struct {
	Label  string
	Result bool
}

// AdminInfo は admin#info。
//
// 意図的な差異: Redmine の環境情報（Ruby / Rails / ImageMagick 等）の代わりに buropher の
// バージョン・Go のバージョン・DB アダプタなどを表示する（レイアウトと CSS クラスは Redmine と同じ）。
func (a *App) AdminInfo(c *Req) {
	ctx := c.Ctx()
	changed := true
	if hashes, err := repository.DefaultAdminPasswordHashes(ctx, a.DB); err == nil {
		for _, h := range hashes {
			if ok, _ := password.Verify(h, "admin"); ok {
				changed = false
			}
		}
	}
	writable := false
	if a.AttachmentStore != nil && a.AttachmentStore.Root != "" {
		writable = dirWritable(a.AttachmentStore.Root)
	}
	migrated := true
	if st, err := db.Status(ctx, a.DB); err == nil {
		for _, s := range st {
			if !s.Applied {
				migrated = false
			}
		}
	} else {
		migrated = false
	}
	checks := []adminInfoCheck{
		{c.L("text_default_administrator_account_changed"), changed},
		{c.L("text_file_repository_writable"), writable},
		{c.L("text_all_migrations_have_been_run"), migrated},
	}
	c.renderAdmin("admin/info", map[string]any{
		"VersionedName": "buropher " + a.version(),
		"Checklist":     checks,
		"Environment":   a.environmentInfo(),
	}, false)
}

func (a *App) version() string {
	if a.Version != "" {
		return a.Version
	}
	return "dev"
}

// dirWritable は File.writable?(dir)（一時ファイルを作れるか）。
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".writable-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(filepath.Clean(name))
	return true
}

// environmentInfo は Redmine::Info.environment に相当する buropher の環境情報。
func (a *App) environmentInfo() string {
	adapter := "unknown"
	if a.DB != nil {
		switch a.DB.Dialect().Name() {
		case db.SQLite:
			adapter = "SQLite"
		case db.Postgres:
			adapter = "PostgreSQL"
		}
	}
	theme := "Default"
	if id := a.Settings.String("ui_theme"); id != "" {
		theme = id
		if a.Assets != nil {
			for _, t := range a.Assets.Themes() {
				if t.ID == id {
					theme = t.Name
				}
			}
		}
	}
	var b strings.Builder
	row := func(k, v string) { fmt.Fprintf(&b, "  %-30s %s\n", k, v) }
	b.WriteString("Environment:\n")
	row("buropher version", a.version())
	row("Go version", runtime.Version())
	row("OS/Arch", runtime.GOOS+"/"+runtime.GOARCH)
	row("Database adapter", adapter)
	row("Mailer delivery", "none")
	b.WriteString("Redmine settings:\n")
	row("Redmine theme", theme)
	b.WriteString("SCM:\n")
	if ok, v := gitCommandVersion(context.Background()); ok {
		row("Git", v)
	} else {
		row("Git", "")
	}
	b.WriteString("Redmine plugins:\n")
	b.WriteString("  no plugin installed")
	return b.String()
}
