package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Page はこのリクエストの helper.Page を作る。
func (c *Req) Page() *helper.Page {
	a := c.App
	p := &helper.Page{
		Request:            c.R,
		Settings:           a.Settings,
		Loc:                c.Loc,
		User:               c.User,
		Project:            c.Project,
		Controller:         c.Controller.Name,
		Action:             c.Action,
		MainMenu:           c.Controller.MainMenu,
		DefaultSearchScope: c.Controller.DefaultSearchScope,
		Question:           c.Question,
		QuestionSet:        c.QuestionSet,
		DB:                 a.DB,
		Now:                a.now,
		Logger:             a.logger(),
		PreviewAttachments: c.Attachments,
	}
	if c.User != nil {
		p.Pref = c.Pref()
		p.Authz = c.Authz
	}
	if c.Controller.MenuItem != nil {
		p.MenuItem = c.Controller.MenuItem(c.Action)
	}
	return p
}

// ViewContext はビューの描画コンテキスト（view.Context）を組み立てる。
func (c *Req) ViewContext() *view.Context {
	a := c.App
	page := c.Page()
	ctx := &view.Context{
		Locale:         c.Loc.Lang,
		T:              translator(c.Loc),
		RequestPath:    c.R.URL.Path,
		Controller:     c.Controller.Name,
		Action:         c.Action,
		AppTitle:       a.Settings.String("app_title"),
		AvatarsEnabled: a.Settings.Bool("gravatar_enabled"),
		FormNameSuffix: a.FormNameSuffix,
		Values:         map[string]any{helper.PageKey: page},
	}
	if a.Assets != nil {
		ctx.AssetPath = func(kind, source string) string {
			switch kind {
			case "javascript":
				return a.Assets.JavascriptPath(source)
			case "stylesheet":
				return a.Assets.StylesheetPath(source)
			}
			return a.Assets.AssetPath(source)
		}
	}
	if s := c.Session(); s != nil {
		ctx.CSRFToken = s.CSRFToken()
	}
	if c.User != nil {
		ctx.User = c.User
		ctx.TextareaFont = page.Pref.TextareaFont
	}
	if c.Project != nil {
		ctx.Project = c.Project
		ctx.ProjectName = c.Project.Name
		ctx.ProjectIdentifier = c.Project.Identifier
	}
	if t := a.Helpers.CurrentTheme(page); t != nil {
		ctx.Theme = t.Name
	}
	ctx.HasMainMenu = page.DisplayMainMenu(c.Project)
	// render_flash_messages は String の値のみ表示する
	for _, e := range c.Flash().Entries() {
		ctx.Flash = append(ctx.Flash, view.Flash{Type: e.Key, Message: e.Value})
	}
	return ctx
}

// translator は i18n.Localizer を view.Translator に変換する（rails.Hash の引数は補間変数として渡す）。
func translator(l *i18n.Localizer) view.Translator {
	return func(key string, args ...any) string {
		for i, a := range args {
			if h, ok := a.(*rails.Hash); ok {
				v := i18n.Vars{}
				for _, e := range h.Entries() {
					v[e.Key] = e.Value
				}
				args[i] = v
			}
		}
		return l.L(key, args...)
	}
}

// RenderOptions は Render のオプション。
type RenderOptions struct {
	Status int
	// Layout はレイアウト名（空なら既定の base、view.NoLayout でなし）。
	Layout string
	Format string
}

// Render は render :template => name（data はインスタンス変数）。
func (c *Req) Render(name string, data any, opts ...RenderOptions) {
	var o RenderOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Status == 0 {
		o.Status = http.StatusOK
	}
	out, err := c.App.Views.Render(c.ViewContext(), name, data, view.RenderOptions{Format: o.Format, Layout: o.Layout})
	c.halted = true
	if err != nil {
		c.App.logger().Error("render failed", "template", name, "err", err)
		c.renderInternalError()
		return
	}
	format := o.Format
	if format == "" {
		format = "html"
	}
	httpx.SetContentType(c.W, format, true)
	if httpx.ShouldVaryAccept(c.R) && c.W.Header().Get("Vary") == "" {
		c.W.Header().Add("Vary", "Accept")
	}
	c.W.WriteHeader(o.Status)
	_, _ = c.W.Write(out)
}

// renderInternalError はテンプレート描画に失敗したときの 500（public/500.html 相当）。
func (c *Req) renderInternalError() {
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.W.WriteHeader(http.StatusInternalServerError)
	_, _ = c.W.Write(InternalErrorPage)
}

// InternalErrorPage は public/500.html の内容（server が設定する）。
var InternalErrorPage = []byte("<!DOCTYPE html><html><body><h1>Internal error</h1></body></html>")

// RenderError は ApplicationController#render_error。
func (c *Req) RenderError(status int, message string) {
	c.App.Errors.RenderError(c.W, c.R, status, message)
	c.halted = true
}

// Render403 は render_403。
func (c *Req) Render403(message string) {
	c.Project = nil
	if message == "" {
		message = c.L("notice_not_authorized")
	}
	c.RenderError(http.StatusForbidden, message)
}

// Render404 は render_404。
func (c *Req) Render404(message string) {
	if message == "" {
		message = c.L("notice_file_not_found")
	}
	c.RenderError(http.StatusNotFound, message)
}

// ErrorPage は httpx.ErrorPage の実装（common/error.html を base レイアウトで描画する）。
func (a *App) ErrorPage() httpx.ErrorPage {
	return httpx.ErrorPageFunc(func(w http.ResponseWriter, r *http.Request, status int, message string, layout bool) {
		c := ReqOf(r)
		if c == nil || c.Loc == nil || c.User == nil {
			c = a.anonymousReq(w, r)
		}
		c.W = w
		opts := RenderOptions{Status: status}
		if !layout {
			opts.Layout = view.NoLayout
		}
		data := map[string]any{"Status": status, "Message": message}
		if p := c.ArchivedProject; p != nil {
			data["ArchivedProject"] = p
			data["UnarchivePath"] = "/projects/" + p.Identifier + "/unarchive"
		}
		c.Render("common/error", data, opts)
	})
}
