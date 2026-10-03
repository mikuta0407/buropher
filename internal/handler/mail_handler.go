package handler

import (
	"context"
	"crypto/subtle"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/notify"
	"html/template"
	"net/http"

	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/mailhandler"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

// このファイルは MailHandlerController（app/controllers/mail_handler_controller.rb）の移植。
// rdm-mailhandler.rb などから受信メールを POST で受け取る（Setting.mail_handler_api_enabled と
// mail_handler_api_key で保護する）。
//
// ActionController::Base を継承しているため ApplicationController の before_action（ログイン要求・
// 言語設定など）は動かさず、verify_authenticity_token も行わない（skip_before_action）。

// MailHandlerController は mail_handler コントローラ。
var MailHandlerController = &Controller{Name: "mail_handler"}

func (a *App) routesMailHandler(r Router) {
	// get 'mail_handler', :to => 'mail_handler#new' / post 'mail_handler', :to => 'mail_handler#index'
	httpx.Route(r, http.MethodGet, "/mail_handler", a.mailHandlerCheckCredential(a.MailHandlerNew))
	httpx.Route(r, http.MethodPost, "/mail_handler", a.mailHandlerCheckCredential(a.MailHandlerIndex))
}

// MailHandler はメール受信の処理（mailhandler.Handler）を App の依存から組み立てる。
// CLI・定期取得（サーバーの外側）からも使う。
func (a *App) MailHandler() *mailhandler.Handler {
	h := &mailhandler.Handler{
		DB: a.DB, Settings: a.Settings, Bundle: a.Bundle, Attachments: a.AttachmentStore,
		Now: a.Now, Logger: a.Logger, IssueNotifier: a.issueNotifier(),
	}
	if m, ok := a.Mailer.(mailhandler.AccountMailer); ok {
		h.AccountMailer = m
	} else if a.Notify != nil {
		h.AccountMailer = notifyAccountInformation{a.Notify}
	}
	h.ContentNotifier = mailContentNotifier{a: a}
	return h
}

// mailContentNotifier はコンテンツの通知フック（a.notify）を mailhandler.ContentNotifier として使う。
// 受信メールで作られるのはフォーラム返信（message_posted）とニュースへのコメント（news_comment_added）で、
// どちらも通知イベント名とメーラーのアクション名が同じ。
type mailContentNotifier struct{ a *App }

func (n mailContentNotifier) Notify(ctx context.Context, action string, obj any) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return
	}
	n.a.notify(n.a.anonymousReq(nil, r), action, action, obj)
}

// mailHandlerCheckCredential は check_credential（WS が無効か API キーが違えば 403）。
func (a *App) mailHandlerCheckCredential(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := httpx.ParamsOf(r).String("key")
		apiKey := a.Settings.String("mail_handler_api_key")
		if !a.Settings.Bool("mail_handler_api_enabled") || subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) != 1 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Access denied. Incoming emails WS is disabled or key is invalid."))
			return
		}
		next(w, httpx.SkipCSRF(r))
	}
}

// MailHandlerIndex は mail_handler#index（受信したら 201、受け付けなければ 422）。
func (a *App) MailHandlerIndex(w http.ResponseWriter, r *http.Request) {
	p := httpx.ParamsOf(r)
	o := mailhandler.Options{Issue: map[string]string{}}
	if m := p.Map("issue"); m != nil {
		// params.permit(issue: [:project, :status, :tracker, :category, :priority, :assigned_to, :fixed_version, :is_private])
		for _, k := range []string{"project", "status", "tracker", "category", "priority", "assigned_to", "fixed_version", "is_private"} {
			if v, ok := m.StringOK(k); ok {
				o.Issue[k] = v
			}
		}
	}
	if v, ok := p.StringOK("allow_override"); ok {
		o.AllowOverride = []string{v}
	}
	o.UnknownUser = p.String("unknown_user")
	o.DefaultGroup = p.String("default_group")
	o.NoAccountNotice = p.String("no_account_notice")
	o.NoNotification = p.String("no_notification")
	o.NoPermissionCheck = p.String("no_permission_check")
	o.ProjectFromSubaddress = p.String("project_from_subaddress")
	email := p.String("email")
	w.Header().Set("Content-Type", "text/html")
	if a.MailHandler().SafeReceive(r.Context(), []byte(email), o) != nil {
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
}

// mailHandlerNewTemplate は app/views/mail_handler/new.html.erb（レイアウトなし）。
var mailHandlerNewTemplate = template.Must(template.New("mail_handler/new").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8" />
<style>
  label {display:block;margin:0.5em;}
</style>
</head>
<body>
<h1>Buropher Mail Handler</h1>

<form enctype="multipart/form-data" action="{{.Action}}" accept-charset="UTF-8" method="post"><input type="hidden" name="authenticity_token" value="{{.Token}}" autocomplete="off" />
  <input type="hidden" name="key" id="key" value="{{.Key}}" autocomplete="off" />

  <fieldset>
    <legend>Raw Email</legend>
    <label><textarea name="email" id="email" style="width:95%; height:400px;">
</textarea></label>
  </fieldset>

  <fieldset>
    <legend>Options</legend>
    <label>unknown_user: <select name="unknown_user" id="unknown_user"><option value=""></option>
<option value="ignore">ignore</option>
<option value="accept">accept</option>
<option value="create">create</option></select></label>
    <label>default_group: <input type="text" name="default_group" id="default_group" /></label>
    <label>no_account_notice: <input type="checkbox" name="no_account_notice" id="no_account_notice" value="1" /></label>
    <label>no_notification: <input type="checkbox" name="no_notification" id="no_notification" value="1" /></label>
    <label>no_permission_check: <input type="checkbox" name="no_permission_check" id="no_permission_check" value="1" /></label>
  </fieldset>

  <fieldset>
    <legend>Issue attributes options</legend>
    <label>project: <input type="text" name="issue[project]" id="issue_project" /></label>
    <label>status: <input type="text" name="issue[status]" id="issue_status" /></label>
    <label>tracker: <input type="text" name="issue[tracker]" id="issue_tracker" /></label>
    <label>category: <input type="text" name="issue[category]" id="issue_category" /></label>
    <label>priority: <input type="text" name="issue[priority]" id="issue_priority" /></label>
    <label>assigned_to: <input type="text" name="issue[assigned_to]" id="issue_assigned_to" /></label>
    <label>fixed_version: <input type="text" name="issue[fixed_version]" id="issue_fixed_version" /></label>
    <label>private: <input type="checkbox" name="issue[private]" id="issue_private" value="1" /></label>
    <label>allow_override: <input type="text" name="allow_override" id="allow_override" /></label>
  </fieldset>

  <p><input type="submit" name="commit" value="Submit Email" data-disable-with="Submit Email" /></p>
</form>
</body>
</html>
`))

// MailHandlerNew は mail_handler#new（受信メールを手で投入するフォーム）。
func (a *App) MailHandlerNew(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]string{"Key": httpx.ParamsOf(r).String("key"), "Token": httpx.CSRFToken(r), "Action": urlroot.Path("/mail_handler")}
	if err := mailHandlerNewTemplate.Execute(w, data); err != nil {
		a.logger().Error("render mail_handler/new", "err", err)
	}
}

// notifyAccountInformation は notify.Service の AccountInformation を mailhandler.AccountMailer として使う
// （受信メールで作成したユーザーへのアカウント情報メール）。
type notifyAccountInformation struct{ s *notify.Service }

func (n notifyAccountInformation) AccountInformation(ctx context.Context, user *domain.User, password string) error {
	n.s.AccountInformation(ctx, user, password)
	return nil
}
