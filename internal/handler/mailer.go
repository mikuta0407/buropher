package handler

// Mailer（app/models/mailer.rb）の移植。notify.Renderer を実装し、受信者を User.current・受信者の言語を
// current_language にした描画用のリクエスト（Mailer#process）で app/views/mailer/*.erb 相当の
// web/templates/mailer/*.tmpl を描画する。HTML は roadie と同じく CSS をインライン化する（mail.InlineCSS）。

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	netmail "net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/brand"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// MailerController はメール描画用のコントローラ（controller_name = mailer）。
var MailerController = &Controller{Name: "mailer", MainMenu: false}

// discardWriter はメール描画用リクエストの捨て先の ResponseWriter。
type discardWriter struct{ h http.Header }

func (w *discardWriter) Header() http.Header {
	if w.h == nil {
		w.h = http.Header{}
	}
	return w.h
}
func (w *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *discardWriter) WriteHeader(int)             {}

// mailURLOptions は Mailer.default_url_options（Setting.host_name / protocol）。
func (a *App) mailURLOptions() httpx.URLOptions {
	return httpx.URLOptionsFromSettings(a.Settings.String("protocol"), a.Settings.String("host_name"))
}

// newBackgroundReq はリクエスト外（ジョブ）で user を User.current として描画するための Req を作る。
// URL は Mailer.default_url_options（Setting.host_name / protocol）で作る。
func (a *App) newBackgroundReq(ctx context.Context, user *domain.User, ctrl *Controller, action string) *Req {
	o := a.mailURLOptions()
	host := o.Host
	if o.Port != "" {
		host += ":" + o.Port
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/", nil)
	r.Host = host
	r.RemoteAddr = "127.0.0.1:0"
	if strings.EqualFold(o.Protocol, "https") {
		r.TLS = &tls.ConnectionState{}
	}
	c := a.newReq(&discardWriter{}, r, ctrl, action)
	if user == nil {
		user = a.anonymous(ctx)
	}
	c.SetUser(user)
	c.MailBaseURL = o.BaseURL()
	c.Loc = a.mailLocalizer(ctx, user)
	return c
}

// mailLocalizer は Mailer#process の言語設定（ログイン済みならユーザーの言語、なければ既定の言語）と
// User.current のタイムゾーン。
func (a *App) mailLocalizer(ctx context.Context, user *domain.User) *i18n.Localizer {
	lang := ""
	if user.Logged() {
		lang = a.Bundle.FindLanguage(user.Language)
	}
	if lang == "" {
		lang = a.Settings.String("default_language")
	}
	is := i18n.Settings{
		DateFormat:      a.Settings.String("date_format"),
		TimeFormat:      a.Settings.String("time_format"),
		TimespanFormat:  a.Settings.String("timespan_format"),
		StartOfWeek:     a.Settings.String("start_of_week"),
		DefaultLanguage: a.Settings.String("default_language"),
	}
	var loc *time.Location
	if user.Logged() {
		if p, err := repository.GetUserPreference(ctx, a.DB, user.ID); err == nil && p.TimeZone != "" {
			loc = i18n.UserLocation(p.TimeZone)
		}
	}
	l := a.Bundle.NewLocalizer("en", is, loc)
	l.SetLanguageIfValid(lang)
	return l
}

// mailer は 1 通分の描画状態（Mailer のインスタンス）。
type mailer struct {
	a    *App
	ctx  context.Context
	c    *Req
	p    *notify.Payload
	user *domain.User // @user（受信者。Mailer の第 1 引数）
	// author は @author（From の表示名・X-Redmine-Sender・自分の変更を通知しない判定に使う）。
	author  *domain.User
	headers []mail.Header
	// messageID / references は Message-ID / References のトークン（< > なし）。
	messageID  string
	references []string
	base       string
	data       map[string]any
	// discord は Discord の DM の描画のために呼んだ（宛先の解決と本文の描画を行わず、件名とデータだけ作る）。
	discord bool
	subject string
}

// tokenObject は Mailer.token_for の対象（クラス名・id・created_on/updated_on）。
type tokenObject struct {
	class string
	id    int64
	at    time.Time
}

// newMailer は 1 通分の描画状態を作る（受信者が削除されていれば nil）。
func (a *App) newMailer(ctx context.Context, p *notify.Payload) (*mailer, error) {
	var user *domain.User
	if p.UserID != 0 {
		u, err := repository.GetUser(ctx, a.DB, p.UserID)
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		user = u
	}
	m := &mailer{a: a, ctx: ctx, p: p, user: user, data: map[string]any{}}
	m.c = a.newBackgroundReq(ctx, user, MailerController, p.Kind)
	m.base = m.c.MailBaseURL
	return m, nil
}

// RenderMail は notify.Renderer#RenderMail（Mailer の各アクション）。
func (a *App) RenderMail(ctx context.Context, p *notify.Payload) (*mail.Message, error) {
	m, err := a.newMailer(ctx, p)
	if m == nil || err != nil {
		return nil, err
	}
	return m.run()
}

// run は Payload.Kind のアクションを実行する。
func (m *mailer) run() (*mail.Message, error) {
	p := m.p
	switch p.Kind {
	case notify.KindIssueAdd:
		return m.issueAdd()
	case notify.KindIssueEdit:
		return m.issueEdit()
	case notify.KindReminder:
		return m.reminder()
	case notify.KindDocumentAdded:
		return m.documentAdded()
	case notify.KindAttachmentsAdded:
		return m.attachmentsAdded()
	case notify.KindNewsAdded:
		return m.newsAdded()
	case notify.KindNewsCommentAdded:
		return m.newsCommentAdded()
	case notify.KindMessagePosted:
		return m.messagePosted()
	case notify.KindWikiContentAdded, notify.KindWikiContentUpdated:
		return m.wikiContent(p.Kind == notify.KindWikiContentUpdated)
	case notify.KindAccountInformation:
		return m.accountInformation()
	case notify.KindAccountActivationRequest:
		return m.accountActivationRequest()
	case notify.KindAccountActivated:
		return m.accountActivated()
	case notify.KindLostPassword:
		return m.lostPassword()
	case notify.KindRegister:
		return m.register()
	case notify.KindSecurityNotification:
		return m.securityNotification()
	case notify.KindSettingsUpdated:
		return m.settingsUpdated()
	case notify.KindTestEmail:
		return m.testEmail()
	case notify.KindDiscordFallback:
		return m.discordFallback()
	}
	return nil, fmt.Errorf("mailer: unknown mail %q", p.Kind)
}

// l は l(key, args...)（受信者の言語）。
func (m *mailer) l(key string, args ...any) string { return m.c.L(key, args...) }

// url は url_for の完全 URL（path は "/" 始まり）。
func (m *mailer) url(path string) string { return m.base + path }

// redmineHeaders は redmine_headers（nil・空の値は compact で除かれる）。
func (m *mailer) redmineHeaders(kv ...any) {
	for i := 0; i+1 < len(kv); i += 2 {
		k := kv[i].(string)
		var v string
		switch x := kv[i+1].(type) {
		case nil:
			continue
		case string:
			v = x
		case *string:
			if x == nil {
				continue
			}
			v = *x
		case int64:
			v = strconv.FormatInt(x, 10)
		case int:
			v = strconv.Itoa(x)
		default:
			v = fmt.Sprint(x)
		}
		m.brandHeader(k, v)
	}
}

// brandHeader は X-Buropher-<k> ヘッダを設定する。mail.redmine_compat_headers（既定 true）のときは
// Redmine 互換の X-Redmine-<k> も同じ値で設定し、X-Buropher-<k> をその直後に置く。
func (m *mailer) brandHeader(k, v string) {
	if !m.a.MailOmitRedmineHeaders {
		m.setHeader("X-Redmine-"+k, v)
	}
	m.setHeader("X-Buropher-"+k, v)
}

func (m *mailer) setHeader(name, value string) {
	for i, h := range m.headers {
		if strings.EqualFold(h.Name, name) {
			m.headers[i].Value = value
			return
		}
	}
	m.headers = append(m.headers, mail.Header{Name: name, Value: value})
}

func (m *mailer) header(name string) string {
	for _, h := range m.headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// tokenFor は Mailer.token_for(object, user)。
func (m *mailer) tokenFor(o tokenObject, user *domain.User) string {
	parts := []string{m.a.messageIDPrefix(), o.class + "-" + strconv.FormatInt(o.id, 10), o.at.UTC().Format("20060102150405")}
	if user != nil {
		parts = append(parts, strconv.FormatInt(user.ID, 10))
	}
	host := mailFromHost(m.a.Settings.String("mail_from"))
	if host == "" {
		h, _ := os.Hostname()
		host = h + "." + m.a.messageIDPrefix()
	}
	return strings.Join(parts, ".") + "@" + host
}

// messageIDPrefix は Message-ID / References の接頭辞（config の mail.message_id_prefix。既定 "redmine"）。
func (a *App) messageIDPrefix() string {
	if a.MessageIDPrefix != "" {
		return a.MessageIDPrefix
	}
	return "redmine"
}

var mailFromHostRe = regexp.MustCompile(`(?m)^.*@|>`)

// mailFromHost は Setting.mail_from.to_s.strip.gsub(%r{^.*@|>}, ”)。
func mailFromHost(s string) string {
	return mailFromHostRe.ReplaceAllString(strings.TrimSpace(s), "")
}

func (m *mailer) messageIDFor(o tokenObject) { m.messageID = m.tokenFor(o, m.user) }

func (m *mailer) referencesFor(o tokenObject) {
	m.references = append(m.references, m.tokenFor(o, m.user))
}

// userName は User#name（Setting.user_format）。
func (m *mailer) userName(u *domain.User) string {
	if u == nil {
		return ""
	}
	if u.Kind == domain.KindAnonymousUser {
		return m.l("label_user_anonymous")
	}
	return u.Name(m.a.Settings.String("user_format"))
}

// principalName は Principal#to_s（グループは名前）。
func (m *mailer) principalName(u *domain.User) string {
	if u == nil {
		return ""
	}
	if u.Kind.IsGroup() {
		return u.Principal.Name
	}
	return m.userName(u)
}

func (m *mailer) getUser(id int64) *domain.User {
	if id == 0 {
		return nil
	}
	u, err := repository.GetUser(m.ctx, m.a.DB, id)
	if err != nil {
		return nil
	}
	return u
}

// mailTo は Mailer#mail の宛先（User は email_addresses、文字列はそのまま。文字列が先）。
func (m *mailer) mailTo(users []*domain.User, addrs []string) ([]string, error) {
	out := append([]string(nil), addrs...)
	for _, u := range users {
		if u == nil {
			continue
		}
		as, err := repository.NotifyEmailAddresses(m.ctx, m.a.DB, u.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
	}
	return out, nil
}

// finish は Mailer#mail（共通ヘッダ・From・List-Id・自分の変更の除外・本文の描画）。宛先が無ければ nil。
func (m *mailer) finish(to []string, subject, view string) (*mail.Message, error) {
	a := m.a
	st := a.Settings
	if m.discord {
		return m.finishDiscord(subject)
	}
	mailFrom := st.String("mail_from")
	var from, listID string
	if addr, err := netmail.ParseAddress(mailFrom); err == nil {
		name := addr.Name
		if name == "" {
			if m.author != nil && m.author.Logged() {
				name = m.userName(m.author)
			} else {
				name = st.String("app_title")
			}
		}
		from = mail.FormatAddress(name, addr.Address)
		fromAddr := strings.ReplaceAll(addr.Address, "@", ".")
		if p := m.header("X-Buropher-Project"); p != "" {
			listID = "<" + p + "." + fromAddr + ">"
		} else {
			listID = "<" + fromAddr + ">"
		}
	} else {
		from = mailFrom
		listID = "<" + strings.ReplaceAll(from, "@", ".") + ">"
	}
	// 自分の変更を通知しない（no_self_notified）作成者のアドレスを除く
	if m.author != nil && m.author.Logged() {
		ns, err := repository.GetNotificationSetting(m.ctx, a.DB, m.author.ID, st.String("default_notification_option"), st.Bool("default_users_no_self_notified"))
		if err != nil {
			return nil, err
		}
		if ns.NoSelfNotified {
			mine, err := repository.AllEmailAddresses(m.ctx, a.DB, m.author.ID)
			if err != nil {
				return nil, err
			}
			var kept []string
			for _, t := range to {
				drop := false
				for _, x := range mine {
					if t == x {
						drop = true
					}
				}
				if !drop {
					kept = append(kept, t)
				}
			}
			to = kept
		}
		m.redmineHeaders("Sender", m.author.Login)
	}
	if len(to) == 0 {
		return nil, nil
	}
	m.setHeader("X-Mailer", brand.Name)
	m.brandHeader("Host", st.String("host_name"))
	m.brandHeader("Site", st.String("app_title"))
	m.setHeader("X-Auto-Response-Suppress", "All")
	m.setHeader("Auto-Submitted", "auto-generated")
	m.setHeader("List-Id", listID)
	msg := &mail.Message{From: from, To: to, Subject: subject, Headers: m.headers, Date: a.now()}
	if m.messageID != "" {
		msg.MessageID = m.messageID
	}
	if len(m.references) > 0 {
		refs := make([]string, len(m.references))
		for i, r := range m.references {
			refs[i] = "<" + r + ">"
		}
		msg.References = strings.Join(refs, " ")
	}
	text, html, err := m.render(view)
	if err != nil {
		return nil, err
	}
	msg.Text, msg.HTML = text, html
	return msg, nil
}

// render は mailer/<view> を text（と plain_text_mail でなければ html）で描画する。
func (m *mailer) render(name string) (string, string, error) {
	a := m.a
	st := a.Settings
	data := m.data
	data["EmailsHeader"] = st.String("emails_header")
	data["EmailsFooter"] = st.String("emails_footer")
	vc := m.c.ViewContext()
	text, err := a.Views.Render(vc, "mailer/"+name, data, view.RenderOptions{Format: "text", Layout: "mailer"})
	if err != nil {
		return "", "", fmt.Errorf("mailer: render %s.text: %w", name, err)
	}
	if st.Bool("plain_text_mail") {
		return string(text), "", nil
	}
	r := a.Helpers.WikiRenderer(m.c.Page())
	if h := st.String("emails_header"); strings.TrimSpace(h) != "" {
		data["EmailsHeaderHTML"] = template.HTML(r.ToHTML(h))
	}
	if f := st.String("emails_footer"); strings.TrimSpace(f) != "" {
		data["EmailsFooterHTML"] = template.HTML(r.ToHTML(f))
	}
	html, err := a.Views.Render(m.c.ViewContext(), "mailer/"+name, data, view.RenderOptions{Format: "html", Layout: "mailer"})
	if err != nil {
		return "", "", fmt.Errorf("mailer: render %s.html: %w", name, err)
	}
	o := a.mailURLOptions()
	port, _ := strconv.Atoi(o.Port)
	inlined, err := mail.InlineCSS(string(html), mail.URLOptions{Protocol: o.Protocol, Host: o.Host, Port: port})
	if err != nil {
		return "", "", err
	}
	return string(text), inlined, nil
}

// linkTo は link_to(text, url)（text はエスケープする）。
func linkTo(text, url string) template.HTML {
	return template.HTML(`<a href="` + template.HTMLEscapeString(url) + `">` + template.HTMLEscapeString(text) + `</a>`)
}

// testEmail は Mailer#test_email。
func (m *mailer) testEmail() (*mail.Message, error) {
	m.data["URL"] = m.url("/")
	to, err := m.mailTo([]*domain.User{m.user}, nil)
	if err != nil {
		return nil, err
	}
	return m.finish(to, brand.Name+" test", "test_email")
}
