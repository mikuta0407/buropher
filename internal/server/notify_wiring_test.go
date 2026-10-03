package server_test

// 画面操作（HTTP）から通知の配送（メール / Discord DM）までの結合テスト。
// チケット・ニュース・コメント・文書・ファイル・フォーラム・アカウント系の各操作が notify.Service に
// つながっていることを、受信者と（Redmine の期待値があるものは）内容で確かめる。

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/totp"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/mail/smtptest"
	"github.com/mikuta0407/buropher/internal/server"
)

// wiringEnv は通知環境と、そのサーバに HTTP でつなぐテストサーバ。
type wiringEnv struct {
	*notifyEnv
	ts *httptest.Server
}

func newWiringEnv(t *testing.T, mutate ...func(cfg *config.Config, o *server.Options)) *wiringEnv {
	t.Helper()
	myFreezeClock(t)
	e := newNotifyEnv(t, mutate...)
	return &wiringEnv{notifyEnv: e, ts: newTestHTTP(t, e.srv)}
}

// form は GET したページの CSRF トークンを付けて POST する。
func (w *wiringEnv) form(c *http.Client, page, target string, v url.Values) *http.Response {
	w.t.Helper()
	_, body := get(w.t, c, w.ts.URL+page)
	v.Set("authenticity_token", csrfToken(w.t, body))
	res, _ := post(w.t, c, w.ts.URL+target, v)
	return res
}

// recipients は送られたメールの宛先（1 通ごとに "a,b"。整列済み）。
func recipients(ms []*mail.Message) []string {
	var out []string
	for _, m := range ms {
		out = append(out, strings.Join(m.To, ","))
	}
	sort.Strings(out)
	return out
}

func goldenRecipients(gc goldenCase) []string {
	var out []string
	for _, gm := range gc.Mails {
		out = append(out, strings.Join(gm.To, ","))
	}
	sort.Strings(out)
	return out
}

func expectRecipients(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: recipients = %v, want %v", what, got, want)
	}
}

// TestNotifyWiringIssuesSMTP はチケットの作成・更新（画面）で SMTP に Redmine と同じ受信者のメールが届くことを確かめる。
func TestNotifyWiringIssuesSMTP(t *testing.T) {
	gs := loadMailGoldens(t)
	smtp := &smtptest.Server{}
	if err := smtp.Start(); err != nil {
		t.Fatal(err)
	}
	defer smtp.Close()
	host, port := smtp.Addr()
	w := newWiringEnv(t, func(cfg *config.Config, o *server.Options) {
		o.MailSender = nil
		cfg.Mail = config.Mail{DeliveryMethod: "smtp", SMTP: config.SMTP{Address: host, Port: port}}
	})
	c := login(t, w.ts, "jsmith", "jsmith")

	// 作成: issue_add_1（eCookbook の Bug、作成者 jsmith、担当者なし、優先度 Low）と同じ条件 → 同じ受信者
	res := w.form(c, "/projects/ecookbook/issues/new", "/projects/ecookbook/issues", url.Values{
		"issue[tracker_id]": {"1"}, "issue[subject]": {"Wired notification"}, "issue[priority_id]": {"4"}, "issue[status_id]": {"1"},
	})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("create issue: status %d", res.StatusCode)
	}
	w.run()
	var got []string
	for _, m := range smtp.Messages() {
		got = append(got, strings.Join(m.To, ","))
		if !strings.Contains(m.Data, "] (New) Wired notification") || !strings.Contains(m.Data, "Message-ID: <redmine.issue-") {
			t.Errorf("unexpected issue_add mail:\n%s", m.Data)
		}
	}
	sort.Strings(got)
	expectRecipients(t, "issue_add", got, goldenRecipients(gs["issue_add_1"]))

	// 更新: e2e_issue_update（Redmine で同じ更新をしたときのメール）と同じ受信者
	before := len(smtp.Messages())
	res = w.form(c, "/issues/1", "/issues/1", url.Values{"_method": {"patch"},
		"issue[status_id]": {"2"}, "issue[notes]": {"Changed the status\n\nwith *notes*"}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("update issue: status %d", res.StatusCode)
	}
	w.run()
	got = nil
	for _, m := range smtp.Messages()[before:] {
		got = append(got, strings.Join(m.To, ","))
		if !strings.Contains(m.Data, "Subject: [eCookbook - Bug #1] (Assigned) Cannot print recipes") {
			t.Errorf("unexpected issue_edit mail:\n%s", m.Data)
		}
	}
	sort.Strings(got)
	expectRecipients(t, "issue_edit", got, goldenRecipients(gs["e2e_issue_update"]))
	var sent int
	w.d.Get(w.ctx, &sent, `SELECT COUNT(*) FROM notification_deliveries WHERE status = 'sent' AND channel = 'email'`)
	if sent != 4 {
		t.Errorf("sent deliveries = %d, want 4", sent)
	}
}

// TestNotifyWiringIssueUpdateContent は画面からのチケット更新のメールが Redmine の出力（e2e_issue_update）と一致することを確かめる。
func TestNotifyWiringIssueUpdateContent(t *testing.T) {
	gs := loadMailGoldens(t)
	w := newWiringEnv(t)
	c := login(t, w.ts, "jsmith", "jsmith")
	w.form(c, "/issues/1", "/issues/1", url.Values{"_method": {"patch"},
		"issue[status_id]": {"2"}, "issue[notes]": {"Changed the status\n\nwith *notes*"}})
	w.run()
	compareCase(t, gs["e2e_issue_update"], w.sender.Messages())

	// 通知イベントに issue_updated が無く、issue_note_added だけならノート付きの更新のみ通知する
	w.setEvents("issue_note_added")
	w.sender.Clear()
	w.form(c, "/issues/1", "/issues/1", url.Values{"_method": {"patch"}, "issue[done_ratio]": {"50"}})
	w.run()
	if n := len(w.sender.Messages()); n != 0 {
		t.Fatalf("update without notes sent %d mails", n)
	}
	w.form(c, "/issues/1", "/issues/1", url.Values{"_method": {"patch"}, "issue[notes]": {"note only"}})
	w.run()
	if n := len(w.sender.Messages()); n == 0 {
		t.Fatal("note was not notified")
	}
}

// TestNotifyWiringLogOnlyWithoutDelivery はメールも Discord も無効ならチケットの通知を積まないことを確かめる。
func TestNotifyWiringLogOnlyWithoutDelivery(t *testing.T) {
	w := newWiringEnv(t, func(cfg *config.Config, o *server.Options) { o.MailSender = nil })
	c := login(t, w.ts, "jsmith", "jsmith")
	w.form(c, "/issues/1", "/issues/1", url.Values{"_method": {"patch"}, "issue[notes]": {"hello"}})
	var n int
	w.d.Get(w.ctx, &n, `SELECT COUNT(*) FROM jobs`)
	if n != 0 {
		t.Fatalf("jobs = %d, want 0", n)
	}
}

// TestNotifyWiringContent はニュース・コメント・文書・ファイル・フォーラムの投稿でメールが送られることを確かめる。
func TestNotifyWiringContent(t *testing.T) {
	gs := loadMailGoldens(t)
	w := newWiringEnv(t)
	w.setEvents("news_added", "news_comment_added", "document_added", "file_added", "message_posted")
	c := login(t, w.ts, "jsmith", "jsmith")
	check := func(what string, golden string, subject string) {
		t.Helper()
		w.run()
		ms := w.sender.Messages()
		expectRecipients(t, what, recipients(ms), goldenRecipients(gs[golden]))
		for _, m := range ms {
			if m.Subject != subject {
				t.Errorf("%s: subject = %q, want %q", what, m.Subject, subject)
			}
		}
		w.sender.Clear()
	}

	// ニュース（news_added_1 と同じプロジェクト → 同じ受信者）
	res := w.form(c, "/projects/ecookbook/news/new", "/projects/ecookbook/news", url.Values{
		"news[title]": {"Wired news"}, "news[summary]": {"s"}, "news[description]": {"body"}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("create news: %d", res.StatusCode)
	}
	check("news_added", "news_added_1", "[eCookbook] News: Wired news")
	var newsID int64
	w.d.Get(w.ctx, &newsID, `SELECT MAX(id) FROM news`)
	nid := itoa64(newsID)

	// コメント
	w.form(c, "/news/"+nid, "/news/"+nid+"/comments", url.Values{"comment[comments]": {"nice"}})
	check("news_comment_added", "news_comment_added_1", "Re: [eCookbook] News: Wired news")

	// 文書
	res = w.form(c, "/projects/ecookbook/documents/new", "/projects/ecookbook/documents", url.Values{
		"document[category_id]": {"1"}, "document[title]": {"Wired doc"}, "document[description]": {"d"}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("create document: %d", res.StatusCode)
	}
	check("document_added", "document_added_1", "[eCookbook] New document: Wired doc")
	var docID int64
	w.d.Get(w.ctx, &docID, `SELECT MAX(id) FROM documents`)

	// 文書への添付の追加は document_added で判定する（file_added が無くても送る）
	w.setEvents("document_added")
	tok := w.uploadToken(c, "/documents/"+itoa64(docID), "doc.txt")
	w.form(c, "/documents/"+itoa64(docID), "/documents/"+itoa64(docID)+"/add_attachment",
		url.Values{"attachments[1][token]": {tok}})
	check("attachments_added (document)", "attachments_added_document", "[eCookbook] New file: doc.txt")

	// ファイル（files#create は file_added で判定する）
	tok = w.uploadToken(c, "/projects/ecookbook/files/new", "file.zip")
	w.form(c, "/projects/ecookbook/files/new", "/projects/ecookbook/files", url.Values{"attachments[1][token]": {tok}})
	w.run()
	if n := len(w.sender.Messages()); n != 0 {
		t.Fatalf("files#create without file_added sent %d mails", n)
	}
	w.setEvents("file_added", "message_posted")
	tok = w.uploadToken(c, "/projects/ecookbook/files/new", "file2.zip")
	w.form(c, "/projects/ecookbook/files/new", "/projects/ecookbook/files", url.Values{"attachments[1][token]": {tok}})
	check("attachments_added (project)", "attachments_added_project", "[eCookbook] New file: file2.zip")

	// フォーラム: 新しいトピック（掲示板 1。message_posted_1 の受信者のうち、トピック 1 のウォッチャーの admin を除く）
	res = w.form(c, "/boards/1/topics/new", "/boards/1/topics/new", url.Values{
		"message[subject]": {"Wired topic"}, "message[content]": {"hello"}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("create topic: %d", res.StatusCode)
	}
	w.run()
	topic := itoa64(w.maxID("messages"))
	ms := w.sender.Messages()
	expectRecipients(t, "message_posted (topic)", recipients(ms), []string{"dlopper@somenet.foo", "jsmith@somenet.foo"})
	if ms[0].Subject != "[eCookbook - Help - msg"+topic+"] Wired topic" {
		t.Errorf("topic subject = %q", ms[0].Subject)
	}
	w.sender.Clear()
	// トピック 1 への返信（message_posted_2 と同じトピック → 同じ受信者）
	w.form(c, "/boards/1/topics/1", "/boards/1/topics/1/replies", url.Values{
		"reply[subject]": {"RE: First post"}, "reply[content]": {"reply"}})
	check("message_posted (reply)", "message_posted_2", "[eCookbook - Help - msg1] RE: First post")
}

func (w *wiringEnv) maxID(table string) int64 {
	var id int64
	w.d.Get(w.ctx, &id, `SELECT MAX(id) FROM `+table)
	return id
}

var uploadTokenRe = regexp.MustCompile(`input\.token'\)\.val\('([^']+)'\)`)

// uploadToken は uploads.js で一時ファイルを上げてトークンを返す。
func (w *wiringEnv) uploadToken(c *http.Client, page, filename string) string {
	w.t.Helper()
	_, body := get(w.t, c, w.ts.URL+page)
	m := csrfMetaTokenRe.FindStringSubmatch(body)
	if m == nil {
		w.t.Fatalf("csrf-token meta not found on %s", page)
	}
	hdr := map[string]string{"X-CSRF-Token": m[1], "X-Requested-With": "XMLHttpRequest", "Accept": "text/javascript, application/javascript"}
	_, js := upload(w.t, c, w.ts.URL+"/uploads.js?attachment_id=1&filename="+url.QueryEscape(filename), "application/octet-stream", "hello", hdr)
	t := uploadTokenRe.FindStringSubmatch(js)
	if t == nil {
		w.t.Fatalf("upload token not found:\n%s", js)
	}
	return t[1]
}

// TestNotifyWiringDiscord はメール配送が無効でも、Discord を連携したユーザーには画面操作の通知が DM で届くことを確かめる。
func TestNotifyWiringDiscord(t *testing.T) {
	myFreezeClock(t)
	e, fake := newDiscordEnvWith(t, func(cfg *config.Config, o *server.Options) { o.MailSender = mail.NullSender{} })
	w := &wiringEnv{notifyEnv: e, ts: newTestHTTP(t, e.srv)}
	e.linkDiscord(2, "d-jsmith", "discord")
	c := login(t, w.ts, "dlopper", "foo")
	res := w.form(c, "/issues/2", "/issues/2", url.Values{"_method": {"patch"}, "issue[notes]": {"ping @jsmith"}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("update issue: %d", res.StatusCode)
	}
	w.run()
	dms := fake.SentTo("d-jsmith")
	if len(dms) != 1 {
		t.Fatalf("discord DMs to jsmith = %d, want 1", len(dms))
	}
	embed := dms[0].Body["embeds"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(embed["title"].(string), "[eCookbook - Feature request #2]") {
		t.Errorf("embed title = %v", embed["title"])
	}
	if len(w.sender.Messages()) != 0 {
		t.Errorf("mail sent although mail delivery is disabled")
	}
}

// TestNotifyWiringAccountMails はパスワード再発行・自己登録・有効化依頼・2 要素認証のメールが Redmine と同じ内容で届くことを確かめる。
func TestNotifyWiringAccountMails(t *testing.T) {
	gs := loadMailGoldens(t)
	w := newWiringEnv(t)
	anon := newClient(t)

	// パスワード再発行（トークン以外は lost_password と同じ）
	w.form(anon, "/account/lost_password", "/account/lost_password", url.Values{"mail": {"jsmith@somenet.foo"}})
	w.run()
	var tok string
	if err := w.d.Get(w.ctx, &tok, `SELECT value FROM tokens WHERE user_id = 2 AND action = 'recovery'`); err != nil {
		t.Fatal(err)
	}
	ms := w.sender.Messages()
	if len(ms) != 1 {
		t.Fatalf("lost_password mails = %d", len(ms))
	}
	replaceIn(ms[0], tok, strings.Repeat("a", 64))
	compareMail(t, gs["lost_password"].Mails[0], ms[0])
	w.sender.Clear()

	// 自己登録（管理者による有効化）→ 有効な管理者へ有効化依頼（ログイン名以外は account_activation_request と同じ）
	w.set("self_registration", "2")
	w.form(newClient(t), "/account/register", "/account/register", url.Values{"user[login]": {"reg1"},
		"user[password]": {"reg1pass1"}, "user[password_confirmation]": {"reg1pass1"}, "user[firstname]": {"R"},
		"user[lastname]": {"One"}, "user[mail]": {"reg1@example.net"}, "user[custom_field_values][4]": {"01234"}})
	w.run()
	ms = w.sender.Messages()
	expectRecipients(t, "account_activation_request", recipients(ms), goldenRecipients(gs["account_activation_request"]))
	replaceIn(ms[0], "(reg1)", "(dlopper)")
	compareMail(t, gs["account_activation_request"].Mails[0], ms[0])
	w.sender.Clear()

	// メールによる有効化 → 登録メール（宛先とトークン以外は register と同じ）
	w.set("self_registration", "1")
	w.form(newClient(t), "/account/register", "/account/register", url.Values{"user[login]": {"reg2"},
		"user[password]": {"reg2pass1"}, "user[password_confirmation]": {"reg2pass1"}, "user[firstname]": {"R"},
		"user[lastname]": {"Two"}, "user[mail]": {"reg2@example.net"}, "user[custom_field_values][4]": {"01234"}})
	w.run()
	if err := w.d.Get(w.ctx, &tok, `SELECT t.value FROM tokens t JOIN user_accounts u ON u.principal_id = t.user_id WHERE u.login = 'reg2' AND t.action = 'register'`); err != nil {
		t.Fatal(err)
	}
	ms = w.sender.Messages()
	if len(ms) != 1 || strings.Join(ms[0].To, ",") != "reg2@example.net" {
		t.Fatalf("register mails = %v", recipients(ms))
	}
	replaceIn(ms[0], tok, strings.Repeat("b", 64))
	ms[0].To = []string{"dlopper@somenet.foo"}
	compareMail(t, gs["register"].Mails[0], ms[0])
	w.sender.Clear()

	// 2 要素認証の有効化 → セキュリティ通知（本人宛て。送信者・IP アドレス入り）
	c := login(t, w.ts, "jsmith", "jsmith")
	w.form(c, "/my/twofa/select_scheme", "/my/twofa/totp/activate/init", url.Values{})
	_, page := get(t, c, w.ts.URL+"/my/twofa/totp/activate/confirm")
	m := authTotpKeyRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("TOTP key not found")
	}
	key := strings.ReplaceAll(m[1], " ", "")
	w.form(c, "/my/twofa/totp/activate/confirm", "/my/twofa/totp/activate", url.Values{"twofa_code": {totp.Now(key, frozenTime)}})
	w.run()
	ms = w.sender.Messages()
	if len(ms) != 1 || strings.Join(ms[0].To, ",") != "jsmith@somenet.foo" || ms[0].Subject != "[Redmine] Security notification" {
		t.Fatalf("twofa mails = %v", recipients(ms))
	}
	for _, s := range []string{"Two-factor authentication successfully enabled using Authenticator app.", "User: jsmith", "IP address: 127.0.0.1", "http://localhost:3000/my/account"} {
		if !strings.Contains(ms[0].Text, s) {
			t.Errorf("twofa mail does not contain %q:\n%s", s, ms[0].Text)
		}
	}
}

func replaceIn(m *mail.Message, old, new string) {
	m.Text = strings.ReplaceAll(m.Text, old, new)
	m.HTML = strings.ReplaceAll(m.HTML, old, new)
	m.Subject = strings.ReplaceAll(m.Subject, old, new)
}

// TestNotifyWiringImports は CSV インポートの通知（チケットは settings['notifications'] に従う、
// ユーザーは notifications なら Mailer.deliver_account_information）を確かめる。
func TestNotifyWiringImports(t *testing.T) {
	gs := loadMailGoldens(t)
	w := newWiringEnv(t)
	jsmith := login(t, w.ts, "jsmith", "jsmith")
	_, ids := runImport(t, jsmith, w.ts, w.d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon, issueImportMapping)
	w.run()
	if len(ids) != 3 || len(w.sender.Messages()) != 0 {
		t.Fatalf("import without notifications: issues=%d mails=%d", len(ids), len(w.sender.Messages()))
	}
	_, ids = runImport(t, jsmith, w.ts, w.d, "IssueImport", "import_issues.csv", "issues",
		merge(utf8Semicolon, map[string]string{"notifications": "1"}), issueImportMapping)
	w.run()
	adds := 0
	for _, m := range w.sender.Messages() {
		if strings.HasPrefix(m.MessageID, "redmine.issue-") {
			adds++
		}
	}
	if len(ids) != 3 || adds == 0 {
		t.Fatalf("import with notifications: issues=%d issue_add mails=%d", len(ids), adds)
	}
	w.sender.Clear()

	admin := login(t, w.ts, "admin", "admin")
	mapping := map[string]string{"login": "1", "firstname": "2", "lastname": "3", "mail": "4", "language": "5", "admin": "6",
		"password": "8", "must_change_passwd": "9", "status": "10", "cf_4": "11"}
	_, uids := runImport(t, admin, w.ts, w.d, "UserImport", "import_users.csv", "principals",
		merge(utf8Semicolon, map[string]string{"notifications": "1"}), mapping)
	w.run()
	// user2 は language = ja なので件名は日本語
	subjects := map[string]bool{gs["account_information"].Mails[0].Subject: true, "Redmine アカウント登録の確認": true}
	var info []string
	for _, m := range w.sender.Messages() {
		if subjects[m.Subject] {
			info = append(info, strings.Join(m.To, ","))
		}
	}
	sort.Strings(info)
	expectRecipients(t, "account_information", info, []string{"user1@somenet.foo", "user2@somenet.foo", "user3@somenet.foo"})
	if len(uids) != 3 {
		t.Fatalf("users = %d", len(uids))
	}
}

// コミットメッセージのキーワードによるチケット更新（定期取り込み・sys・リポジトリ画面）も、チケットの画面操作と
// 同じ通知先（メールが有効なら notify.Service）に配送される（App.Notifier はテスト用の上書きで本番では nil）。
func TestSCMServiceUsesIssueNotifier(t *testing.T) {
	e := newNotifyEnv(t)
	if got := e.srv.App().SCMService().Notifier; got != e.svc {
		t.Fatalf("scm notifier = %#v, want notify.Service", got)
	}
}
