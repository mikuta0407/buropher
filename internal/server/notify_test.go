package server_test

// 通知の結合テスト: イベント → 受信者計算 → チャネル決定 → ジョブ → 描画 → 配送（SMTP / 偽 Discord API）。
// メールは Redmine の期待値（testdata/mail/mails.json）と受信者・内容を比べる。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/discord/discordtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/mail/smtptest"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

type notifyEnv struct {
	t      *testing.T
	ctx    context.Context
	srv    *server.Server
	d      *db.DB
	svc    *notify.Service
	sender *mail.TestSender
	now    time.Time
}

// newNotifyEnv はフィクスチャ投入済み DB と、メールを TestSender に送るサーバ。
func newNotifyEnv(t *testing.T, mutate ...func(cfg *config.Config, o *server.Options)) *notifyEnv {
	t.Helper()
	saved := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = saved })
	ctx := context.Background()
	d := dbtest.New(t)
	if err := testfixtures.LoadContext(ctx, d, frozenTime, testfixtures.All()...); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Storage.AttachmentsPath = t.TempDir()
	env := &notifyEnv{t: t, ctx: ctx, d: d, sender: &mail.TestSender{}, now: frozenTime}
	opts := server.Options{TempDir: t.TempDir(), Now: func() time.Time { return env.now }, MailSender: env.sender}
	for _, m := range mutate {
		m(cfg, &opts)
	}
	srv, err := server.New(cfg, d, opts)
	if err != nil {
		t.Fatal(err)
	}
	env.srv, env.svc = srv, srv.Notify()
	return env
}

func (e *notifyEnv) set(name, value string) {
	e.t.Helper()
	if err := e.srv.App().Settings.Set(e.ctx, name, value); err != nil {
		e.t.Fatal(err)
	}
}

func (e *notifyEnv) setEvents(evs ...string) {
	e.t.Helper()
	if err := e.srv.App().Settings.Set(e.ctx, "notified_events", evs); err != nil {
		e.t.Fatal(err)
	}
}

// run は積まれたジョブを処理する。
func (e *notifyEnv) run() int {
	e.t.Helper()
	n, err := e.srv.Jobs().RunPending(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *notifyEnv) user(id int64) *domain.User {
	u, err := repository.GetUser(e.ctx, e.d, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// compareCase は送られたメールを受信者で対応づけて期待値と比べる。
func compareCase(t *testing.T, gc goldenCase, got []*mail.Message) {
	t.Helper()
	key := func(to []string) string { return strings.Join(to, ",") }
	var gotKeys, wantKeys []string
	byTo := map[string]*mail.Message{}
	for _, m := range got {
		gotKeys = append(gotKeys, key(m.To))
		byTo[key(m.To)] = m
	}
	for _, gm := range gc.Mails {
		wantKeys = append(wantKeys, key(gm.To))
	}
	sort.Strings(gotKeys)
	sort.Strings(wantKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("%s: recipients = %v, want %v", gc.Case, gotKeys, wantKeys)
	}
	for _, gm := range gc.Mails {
		compareMail(t, gm, byTo[key(gm.To)])
	}
}

// TestNotifyEventsMatchRedmine はイベントごとの受信者と内容を Redmine の Mailer.deliver_* と比べる。
func TestNotifyEventsMatchRedmine(t *testing.T) {
	gs := loadMailGoldens(t)
	cases := []struct {
		name string
		fire func(e *notifyEnv)
	}{
		{"news_added_1", func(e *notifyEnv) { e.svc.NewsAdded(e.ctx, 1) }},
		{"news_comment_added_1", func(e *notifyEnv) { e.svc.NewsCommentAdded(e.ctx, 1) }},
		{"document_added_1", func(e *notifyEnv) { e.svc.DocumentAdded(e.ctx, 1, e.user(2)) }},
		{"attachments_added_project", func(e *notifyEnv) { e.svc.AttachmentsAdded(e.ctx, []int64{8, 22}) }},
		{"attachments_added_version", func(e *notifyEnv) { e.svc.AttachmentsAdded(e.ctx, []int64{9}) }},
		{"attachments_added_document", func(e *notifyEnv) { e.svc.AttachmentsAdded(e.ctx, []int64{2}) }},
		{"message_posted_1", func(e *notifyEnv) { e.svc.MessagePosted(e.ctx, 1) }},
		{"message_posted_2", func(e *notifyEnv) { e.svc.MessagePosted(e.ctx, 2) }},
		{"wiki_content_added_1", func(e *notifyEnv) { e.svc.WikiContentAdded(e.ctx, 1, 0, e.user(1)) }},
		{"wiki_content_updated_1", func(e *notifyEnv) { e.svc.WikiContentUpdated(e.ctx, 1, 0, e.user(1), "old text") }},
		{"account_activation_request", func(e *notifyEnv) { e.svc.AccountActivationRequest(e.ctx, 3) }},
		{"account_information", func(e *notifyEnv) { e.svc.AccountInformation(e.ctx, e.user(2), "pAsSwoRd") }},
		{"lost_password", func(e *notifyEnv) {
			e.svc.LostPassword(e.ctx, e.user(2), strings.Repeat("a", 64), "")
			e.svc.LostPassword(e.ctx, e.user(2), strings.Repeat("a", 64), "other@example.net")
		}},
		{"security_notification_password", func(e *notifyEnv) { e.svc.PasswordUpdated(e.ctx, e.user(2), e.user(2), "10.1.2.3") }},
		{"settings_updated", func(e *notifyEnv) {
			e.svc.SettingsUpdated(e.ctx, e.user(1), []string{"host_name", "login_required"}, "127.0.0.1")
		}},
		{"reminders_7", func(e *notifyEnv) { mustReminders(e, notify.ReminderOptions{Days: 7}) }},
		{"reminders_42", func(e *notifyEnv) { mustReminders(e, notify.ReminderOptions{Days: 42}) }},
		{"reminders_42_user3", func(e *notifyEnv) { mustReminders(e, notify.ReminderOptions{Days: 42, Users: []int64{3}}) }},
		{"issue_add_2", func(e *notifyEnv) { e.issueNotification(issues.NotifyIssueAdd, 2, 0) }},
		{"issue_edit_3", func(e *notifyEnv) { e.issueNotification(issues.NotifyIssueEdit, 2, 3) }},
		{"issue_edit_4", func(e *notifyEnv) { e.issueNotification(issues.NotifyIssueEdit, 6, 4) }},
	}
	e := newNotifyEnv(t)
	e.setEvents("issue_added", "issue_updated", "news_added", "news_comment_added", "document_added", "file_added",
		"message_posted", "wiki_content_added", "wiki_content_updated")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e.t = t
			e.sender.Clear()
			c.fire(e)
			e.run()
			compareCase(t, gs[c.name], e.sender.Messages())
		})
	}
}

func mustReminders(e *notifyEnv, o notify.ReminderOptions) {
	if _, err := e.svc.Reminders(e.ctx, o); err != nil {
		e.t.Fatal(err)
	}
}

// issueNotification は internal/issues の受信者計算で通知を作って積む（Mailer.deliver_issue_add / edit）。
func (e *notifyEnv) issueNotification(event string, issueID, journalID int64) {
	e.t.Helper()
	env := issues.NewEnv(e.d, e.srv.App().Settings, e.user(1))
	iss, err := env.Load(e.ctx, issueID)
	if err != nil {
		e.t.Fatal(err)
	}
	var users []*domain.User
	if event == issues.NotifyIssueAdd {
		a, _ := env.NotifiedUsers(e.ctx, iss)
		b, _ := env.NotifiedWatchers(e.ctx, iss)
		users = append(a, b...)
	} else {
		j, err := env.FindJournal(e.ctx, journalID)
		if err != nil {
			e.t.Fatal(err)
		}
		a, _ := env.JournalNotifiedUsers(e.ctx, j, iss)
		b, _ := env.JournalNotifiedWatchers(e.ctx, j, iss)
		users = append(a, b...)
	}
	var ids []int64
	for _, u := range users {
		if !slices.Contains(ids, u.ID) {
			ids = append(ids, u.ID)
		}
	}
	if err := e.svc.Enqueue(e.ctx, issues.Notification{Event: event, IssueID: issueID, JournalID: journalID, Recipients: ids}); err != nil {
		e.t.Fatal(err)
	}
}

// TestNotifyIssueUpdateEndToEnd はチケットを更新すると通知がジョブに積まれ、SMTP で配送されることを確かめる
// （Redmine で同じ更新をしたときのメール = e2e_issue_update と比べる）。
func TestNotifyIssueUpdateEndToEnd(t *testing.T) {
	gs := loadMailGoldens(t)
	smtp := &smtptest.Server{}
	if err := smtp.Start(); err != nil {
		t.Fatal(err)
	}
	defer smtp.Close()
	host, port := smtp.Addr()
	e := newNotifyEnv(t, func(cfg *config.Config, o *server.Options) {
		o.MailSender = nil
		cfg.Mail = config.Mail{DeliveryMethod: "smtp", SMTP: config.SMTP{Address: host, Port: port}}
	})
	cur := e.user(2)
	env := issues.NewEnv(e.d, e.srv.App().Settings, cur)
	env.Notifier = e.svc
	env.Now = func() time.Time { return frozenTime }
	iss, err := env.Load(e.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.InitJournal(e.ctx, iss, cur, "Changed the status\n\nwith *notes*"); err != nil {
		t.Fatal(err)
	}
	if err := env.SafeAssign(e.ctx, iss, issues.Params{"status_id": "2"}, cur); err != nil {
		t.Fatal(err)
	}
	ok, res, err := env.Save(e.ctx, iss)
	if err != nil || !ok {
		t.Fatalf("save: %v %v", ok, err)
	}
	if err := env.Dispatch(e.ctx, res.Notifications); err != nil {
		t.Fatal(err)
	}
	var pending int
	e.d.Get(e.ctx, &pending, `SELECT COUNT(*) FROM jobs WHERE kind = 'notify.email' AND state = 'pending'`)
	if pending != 2 {
		t.Fatalf("pending email jobs = %d, want 2", pending)
	}
	e.run()
	msgs := smtp.Messages()
	if len(msgs) != 2 {
		t.Fatalf("smtp received %d messages", len(msgs))
	}
	var got []string
	for _, m := range msgs {
		got = append(got, strings.Join(m.To, ","))
		if !strings.Contains(m.Data, "Message-ID: <redmine.journal-") || !strings.Contains(m.Data, "Subject: [eCookbook - Bug #1] (Assigned) Cannot print recipes") {
			t.Errorf("unexpected message:\n%s", m.Data)
		}
	}
	sort.Strings(got)
	var want []string
	for _, gm := range gs["e2e_issue_update"].Mails {
		want = append(want, strings.Join(gm.To, ","))
	}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("smtp recipients = %v, want %v", got, want)
	}
	// 配送ログ
	var sent int
	e.d.Get(e.ctx, &sent, `SELECT COUNT(*) FROM notification_deliveries WHERE status = 'sent' AND channel = 'email' AND event = 'issue_updated'`)
	if sent != 2 {
		t.Errorf("sent deliveries = %d", sent)
	}
	// 描画内容も Redmine と同じ（描画は同じなので TestSender で確認）
	e2 := newNotifyEnv(t)
	env2 := issues.NewEnv(e2.d, e2.srv.App().Settings, e2.user(2))
	env2.Notifier = e2.svc
	env2.Now = func() time.Time { return frozenTime }
	iss2, _ := env2.Load(e2.ctx, 1)
	env2.InitJournal(e2.ctx, iss2, e2.user(2), "Changed the status\n\nwith *notes*")
	env2.SafeAssign(e2.ctx, iss2, issues.Params{"status_id": "2"}, e2.user(2))
	_, res2, err := env2.Save(e2.ctx, iss2)
	if err != nil {
		t.Fatal(err)
	}
	env2.Dispatch(e2.ctx, res2.Notifications)
	e2.run()
	compareCase(t, gs["e2e_issue_update"], e2.sender.Messages())
}

// TestNotifyAdminTestEmail は管理画面のテストメール（admin#test_email）が SMTP で同期送信されることを確かめる。
func TestNotifyAdminTestEmail(t *testing.T) {
	smtp := &smtptest.Server{User: "u", Password: "p"}
	if err := smtp.Start(); err != nil {
		t.Fatal(err)
	}
	defer smtp.Close()
	host, port := smtp.Addr()
	e := newNotifyEnv(t, func(cfg *config.Config, o *server.Options) {
		o.MailSender = nil
		cfg.Mail = config.Mail{DeliveryMethod: "smtp", SMTP: config.SMTP{Address: host, Port: port, UserName: "u", Password: "p", Authentication: "login"}}
	})
	if err := e.svc.TestEmail(e.ctx, e.user(1)); err != nil {
		t.Fatal(err)
	}
	ms := smtp.Messages()
	if len(ms) != 1 || ms[0].To[0] != "admin@somenet.foo" || ms[0].Auth != "LOGIN" || !strings.Contains(ms[0].Data, "Subject: Redmine test") {
		t.Fatalf("messages = %+v", ms)
	}
}

// discordEnv は Discord 通知を有効にした環境（偽 Discord API）。
func newDiscordEnv(t *testing.T) (*notifyEnv, *discordtest.Server) {
	return newDiscordEnvWith(t)
}

// newDiscordEnvWith は newDiscordEnv に設定の変更を加えたもの。
func newDiscordEnvWith(t *testing.T, mutate ...func(cfg *config.Config, o *server.Options)) (*notifyEnv, *discordtest.Server) {
	fake := discordtest.New()
	t.Cleanup(fake.Close)
	fake.BotToken, fake.ClientID, fake.ClientSecret = "bot-token", "client-1", "secret-1"
	e := newNotifyEnv(t, func(cfg *config.Config, o *server.Options) {
		cfg.Discord.APIBase = fake.URL
		cfg.Discord.AuthorizeURL = fake.URL + "/oauth2/authorize"
		cfg.Discord.Enabled = true
		for _, m := range mutate {
			m(cfg, o)
		}
	})
	e.set("buropher_discord_enabled", "1")
	box := e.srv.App().Secrets
	tok, _ := box.Seal("bot-token")
	sec, _ := box.Seal("secret-1")
	e.set("buropher_discord_bot_token", tok)
	e.set("buropher_discord_client_id", "client-1")
	e.set("buropher_discord_client_secret", sec)
	e.set("buropher_discord_guild_id", "guild-1")
	return e, fake
}

func (e *notifyEnv) linkDiscord(userID int64, discordID, channels string) {
	e.t.Helper()
	if err := repository.LinkDiscordIdentity(e.ctx, e.d, userID, discordID, "", e.now); err != nil {
		e.t.Fatal(err)
	}
	if err := repository.SetNotificationChannels(e.ctx, e.d, userID, channels, true); err != nil {
		e.t.Fatal(err)
	}
}

// TestNotifyDiscordDelivery は Discord / メール / 両方のチャネル選択と DM の内容を確かめる。
func TestNotifyDiscordDelivery(t *testing.T) {
	e, fake := newDiscordEnv(t)
	e.linkDiscord(2, "d-jsmith", "discord")
	e.linkDiscord(3, "d-dlopper", "both")
	e.issueNotification(issues.NotifyIssueEdit, 1, 1)
	e.run()
	// jsmith は Discord のみ、dlopper は両方
	var mailTo []string
	for _, m := range e.sender.Messages() {
		mailTo = append(mailTo, m.To...)
	}
	if !slices.Equal(mailTo, []string{"dlopper@somenet.foo"}) {
		t.Errorf("mail recipients = %v", mailTo)
	}
	js := fake.SentTo("d-jsmith")
	if len(js) != 1 || len(fake.SentTo("d-dlopper")) != 1 {
		t.Fatalf("discord messages: jsmith=%d dlopper=%d", len(js), len(fake.SentTo("d-dlopper")))
	}
	body := js[0].Body
	embed := body["embeds"].([]any)[0].(map[string]any)
	if embed["title"] != "[eCookbook - Bug #1] Cannot print recipes" || embed["url"] != "http://localhost:3000/issues/1#change-1" {
		t.Errorf("embed = %v", embed)
	}
	if body["content"] != "Issue #1 has been updated by Redmine Admin." || embed["description"] != "Journal notes" {
		t.Errorf("content = %v, description = %v", body["content"], embed["description"])
	}
	fields := embed["fields"].([]any)
	if v := fields[0].(map[string]any)["value"]; v != "• Status changed from New to Assigned\n• % Done changed from 40 to 30" {
		t.Errorf("fields = %v", fields)
	}
	// DM チャンネルはキャッシュされる
	e.issueNotification(issues.NotifyIssueEdit, 1, 2)
	e.run()
	if fake.DMCreates != 2 {
		t.Errorf("DM channels created %d times, want 2 (cached per user)", fake.DMCreates)
	}
	// 日本語のユーザーには日本語で送る
	e.d.Exec(e.ctx, `UPDATE user_accounts SET language = 'ja' WHERE principal_id = 2`)
	e.issueNotification(issues.NotifyIssueEdit, 1, 1)
	e.run()
	js = fake.SentTo("d-jsmith")
	last := js[len(js)-1].Body
	if last["content"] != "チケット #1 を Redmine Admin さんが更新しました。" {
		t.Errorf("ja content = %v", last["content"])
	}
	// 配送ログ
	var n int
	e.d.Get(e.ctx, &n, `SELECT COUNT(*) FROM notification_deliveries WHERE channel = 'discord' AND status = 'sent' AND recipient = 'd-jsmith'`)
	if n != 3 {
		t.Errorf("discord deliveries for jsmith = %d", n)
	}
}

// TestNotifyDiscordRateLimit は 429 でジョブを retry_after 後に延期し、その後に送ることを確かめる。
func TestNotifyDiscordRateLimit(t *testing.T) {
	e, fake := newDiscordEnv(t)
	e.linkDiscord(2, "d-jsmith", "discord")
	fake.RateLimitNext, fake.RetryAfter = 1, 2.5
	e.svc.Enqueue(e.ctx, issues.Notification{Event: issues.NotifyIssueAdd, IssueID: 1, Recipients: []int64{2}})
	e.run()
	if len(fake.SentTo("d-jsmith")) != 0 {
		t.Fatal("sent despite rate limit")
	}
	var j struct {
		State    string  `db:"state"`
		Attempts int     `db:"attempts"`
		RunAt    db.Time `db:"run_at"`
	}
	if err := e.d.Get(e.ctx, &j, `SELECT state, attempts, run_at FROM jobs WHERE kind = 'notify.discord'`); err != nil {
		t.Fatal(err)
	}
	if j.State != "pending" || j.Attempts != 0 || !j.RunAt.Equal(frozenTime.Add(2500*time.Millisecond)) {
		t.Fatalf("job after 429 = %+v", j)
	}
	e.now = frozenTime.Add(3 * time.Second)
	e.run()
	if len(fake.SentTo("d-jsmith")) != 1 {
		t.Fatal("not sent after retry_after")
	}
}

// TestNotifyDiscordCannotDMFallback は 50007（DM 不可）でメールに切り替え、閾値に達したら本人に知らせて
// 以後はメールで送ることを確かめる。
func TestNotifyDiscordCannotDMFallback(t *testing.T) {
	e, fake := newDiscordEnv(t)
	e.set("buropher_discord_failure_threshold", "2")
	e.linkDiscord(2, "d-jsmith", "discord")
	fake.CannotDM["d-jsmith"] = true
	fire := func() {
		e.svc.Enqueue(e.ctx, issues.Notification{Event: issues.NotifyIssueAdd, IssueID: 1, Recipients: []int64{2}})
		e.run()
	}
	fire()
	ms := e.sender.Messages()
	if len(ms) != 1 || ms[0].Subject != "[eCookbook - Bug #1] (New) Cannot print recipes" {
		t.Fatalf("fallback mail = %v", ms)
	}
	chs, _ := e.svc.ChannelsFor(e.ctx, 2)
	if !slices.Equal(chs, []string{"discord"}) {
		t.Fatalf("channels after 1 failure = %v", chs)
	}
	fire()
	ms = e.sender.Messages()
	if len(ms) != 3 {
		t.Fatalf("mails after 2nd failure = %d", len(ms))
	}
	var notice *mail.Message
	for _, m := range ms {
		if strings.Contains(m.Subject, "Discord notifications have been switched to email") {
			notice = m
		}
	}
	if notice == nil || !strings.Contains(notice.Text, "Cannot send messages to this user") {
		t.Fatalf("fallback notice not sent: %v", ms)
	}
	chs, _ = e.svc.ChannelsFor(e.ctx, 2)
	if !slices.Equal(chs, []string{"email"}) {
		t.Fatalf("channels after threshold = %v", chs)
	}
	var failed int
	e.d.Get(e.ctx, &failed, `SELECT COUNT(*) FROM notification_deliveries WHERE channel = 'discord' AND status = 'failed'`)
	if failed != 2 {
		t.Errorf("failed deliveries = %d", failed)
	}
	// テスト DM に成功すると再開する
	delete(fake.CannotDM, "d-jsmith")
	cfg := e.svc.DiscordConfig()
	if err := e.svc.SendDM(e.ctx, cfg, 2, "d-jsmith", &notify.DiscordMessage{Title: "test"}); err != nil {
		t.Fatal(err)
	}
	repository.ResetDiscordDMFailures(e.ctx, e.d, 2)
	chs, _ = e.svc.ChannelsFor(e.ctx, 2)
	if !slices.Equal(chs, []string{"discord"}) {
		t.Fatalf("channels after reset = %v", chs)
	}
}

// TestDiscordLinkFlow はマイアカウントからの連携（OAuth2 → 保存 → Guild 参加 → テスト DM）と解除を確かめる。
func TestDiscordLinkFlow(t *testing.T) {
	e, fake := newDiscordEnv(t)
	fake.Codes["code-1"] = discordtest.User{ID: "d-jsmith", Username: "jsmith_d"}
	ts := newTestHTTP(t, e.srv)
	c := login(t, ts, "jsmith", "jsmith")
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	res, err := c.Get(ts.URL + "/my/discord/link")
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != 302 || !strings.HasPrefix(loc.String(), fake.URL+"/oauth2/authorize?") {
		t.Fatalf("link redirect = %d %s", res.StatusCode, loc)
	}
	q := loc.Query()
	if q.Get("client_id") != "client-1" || q.Get("scope") != "identify guilds.join" || q.Get("redirect_uri") != "http://localhost:3000/my/discord/callback" {
		t.Fatalf("authorize query = %v", q)
	}
	// 不正な state は拒否
	res, _ = c.Get(ts.URL + "/my/discord/callback?code=code-1&state=bad")
	res.Body.Close()
	if id, _ := repository.GetDiscordIdentity(e.ctx, e.d, 2); id != nil {
		t.Fatal("linked with invalid state")
	}
	res, _ = c.Get(ts.URL + "/my/discord/link")
	state := mustQuery(t, res.Header.Get("Location"), "state")
	res, err = c.Get(ts.URL + "/my/discord/callback?code=code-1&state=" + state)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/my/account") {
		t.Fatalf("callback = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	id, _ := repository.GetDiscordIdentity(e.ctx, e.d, 2)
	if id == nil || id.Subject != "d-jsmith" {
		t.Fatalf("identity = %+v", id)
	}
	if !slices.Equal(fake.GuildAdds, []string{"guild-1/d-jsmith"}) {
		t.Errorf("guild adds = %v", fake.GuildAdds)
	}
	if len(fake.SentTo("d-jsmith")) != 1 {
		t.Errorf("test DM not sent")
	}
	// マイアカウントに連携状態と送信先の選択が出る
	_, body := get(t, c, ts.URL+"/my/account")
	if !strings.Contains(body, `name="pref[notification_channel]"`) || !strings.Contains(body, "Linked as jsmith_d") {
		t.Fatalf("my/account does not show discord preferences")
	}
	// 送信先を保存
	tok := csrfToken(t, body)
	res, _ = post(t, c, ts.URL+"/my/account", url.Values{"_method": {"put"}, "authenticity_token": {tok},
		"user[firstname]": {"John"}, "user[lastname]": {"Smith"}, "user[mail]": {"jsmith@somenet.foo"},
		"pref[notification_channel]": {"both"}})
	ns, _ := repository.GetNotificationSetting(e.ctx, e.d, 2, "only_my_events", true)
	if ns.Channels != "both" {
		t.Fatalf("channels = %q (status %d)", ns.Channels, res.StatusCode)
	}
	// 別ユーザーが同じ Discord アカウントを連携しようとすると拒否
	c2 := login(t, ts, "dlopper", "foo")
	c2.CheckRedirect = c.CheckRedirect
	res, _ = c2.Get(ts.URL + "/my/discord/link")
	state = mustQuery(t, res.Header.Get("Location"), "state")
	c2.Get(ts.URL + "/my/discord/callback?code=code-1&state=" + state)
	if id, _ := repository.GetDiscordIdentity(e.ctx, e.d, 3); id != nil {
		t.Fatal("same discord account linked twice")
	}
	// テスト DM と解除
	_, body = get(t, c, ts.URL+"/my/account")
	post(t, c, ts.URL+"/my/discord/test", url.Values{"authenticity_token": {csrfToken(t, body)}})
	if len(fake.SentTo("d-jsmith")) != 2 {
		t.Errorf("test DM via my/discord/test not sent")
	}
	post(t, c, ts.URL+"/my/discord/unlink", url.Values{"authenticity_token": {csrfToken(t, body)}})
	if id, _ := repository.GetDiscordIdentity(e.ctx, e.d, 2); id != nil {
		t.Fatal("not unlinked")
	}
}

// TestDiscordAdminSettings は管理画面のプラグイン一覧と Discord 通知の設定画面を確かめる。
func TestDiscordAdminSettings(t *testing.T) {
	e, _ := newDiscordEnv(t)
	ts := newTestHTTP(t, e.srv)
	c := login(t, ts, "admin", "admin")
	_, body := get(t, c, ts.URL+"/admin/plugins")
	if !strings.Contains(body, `<tr id="plugin-buropher_discord">`) || !strings.Contains(body, `href="/settings/plugin/buropher_discord"`) {
		t.Fatalf("plugins page:\n%s", body)
	}
	_, body = get(t, c, ts.URL+"/settings/plugin/buropher_discord")
	if !strings.Contains(body, `name="settings[bot_token]"`) || strings.Contains(body, "bot-token") {
		t.Fatal("settings page must have the bot token field but not show the token")
	}
	post(t, c, ts.URL+"/settings/plugin/buropher_discord", url.Values{"authenticity_token": {csrfToken(t, body)},
		"settings[enabled]": {"1"}, "settings[client_id]": {"client-2"}, "settings[bot_token]": {""},
		"settings[default_channel]": {"both"}, "settings[guild_id]": {"g2"}, "settings[join_guild]": {"0"}, "settings[failure_threshold]": {"5"}})
	cfg := e.svc.DiscordConfig()
	if cfg.ClientID != "client-2" || cfg.BotToken != "bot-token" || cfg.GuildID != "g2" || cfg.JoinGuild {
		t.Fatalf("config = %+v", cfg)
	}
	if v := e.srv.App().Settings.String("buropher_discord_bot_token"); !strings.HasPrefix(v, "sb1:") {
		t.Errorf("bot token must be stored encrypted: %q", v)
	}
	if e.srv.App().Settings.String("buropher_default_notification_channel") != "both" {
		t.Error("default channel not saved")
	}
	// 一般ユーザーは開けない
	c2 := login(t, ts, "jsmith", "jsmith")
	res, _ := get(t, c2, ts.URL+"/settings/plugin/buropher_discord")
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin status = %d", res.StatusCode)
	}
}

func newTestHTTP(t *testing.T, srv *server.Server) *httptest.Server {
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func mustQuery(t *testing.T, u, key string) string {
	t.Helper()
	p, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	return p.Query().Get(key)
}
