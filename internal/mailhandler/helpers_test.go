package mailhandler

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// frozenNow は参照環境の Redmine と同じ固定時刻。
var frozenNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// recNotifier はチケットの通知を記録する（ActionMailer::Base.deliveries の代わり）。
type recNotifier struct {
	mu sync.Mutex
	ns []issues.Notification
}

func (n *recNotifier) Enqueue(_ context.Context, x issues.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ns = append(n.ns, x)
	return nil
}

// deliveries は配信されるメールの数（受信者ごとに 1 通）。
func (n *recNotifier) deliveries() int {
	c := 0
	for _, x := range n.ns {
		c += len(x.Recipients)
	}
	return c
}

type recContent struct{ actions []string }

func (n *recContent) Notify(_ context.Context, action string, _ any) { n.actions = append(n.actions, action) }

type accountMail struct {
	user     *domain.User
	password string
}

type recAccountMailer struct{ mails []accountMail }

func (m *recAccountMailer) AccountInformation(_ context.Context, u *domain.User, pw string) error {
	m.mails = append(m.mails, accountMail{u, pw})
	return nil
}

// tc は 1 テスト分の環境。
type tc struct {
	t        testing.TB
	ctx      context.Context
	d        *db.DB
	st       *settings.Settings
	h        *Handler
	notifier *recNotifier
	content  *recContent
	mailer   *recAccountMailer
	logs     *bytes.Buffer
}

func setup(t testing.TB) *tc {
	t.Helper()
	d := dbtest.New(t)
	testfixtures.LoadAt(t, d, frozenNow, testfixtures.All()...)
	ctx := context.Background()
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	c := &tc{t: t, ctx: ctx, d: d, st: st, notifier: &recNotifier{}, content: &recContent{}, mailer: &recAccountMailer{},
		logs: &bytes.Buffer{}}
	// Setting.notified_events = Redmine::Notifiable.all.collect(&:name)
	c.set("notified_events", []any{"issue_added", "issue_updated", "issue_note_added", "issue_status_updated",
		"issue_assigned_to_updated", "issue_priority_updated", "issue_fixed_version_updated", "issue_attachment_added",
		"news_added", "news_comment_added", "document_added", "file_added", "message_posted", "wiki_content_added",
		"wiki_content_updated"})
	store := &attachments.Store{Root: t.TempDir(), Settings: st, Now: func() time.Time { return frozenNow }}
	c.h = &Handler{DB: d, Settings: st, Bundle: i18n.Default(), Attachments: store, Now: func() time.Time { return frozenNow },
		Logger:        slog.New(slog.NewTextHandler(io.MultiWriter(c.logs), nil)),
		IssueNotifier: c.notifier, ContentNotifier: c.content, AccountMailer: c.mailer}
	return c
}

func (c *tc) must(err error) {
	c.t.Helper()
	if err != nil {
		c.t.Fatal(err)
	}
}

func (c *tc) set(name string, v any) {
	c.t.Helper()
	c.must(c.st.Set(c.ctx, name, v))
}

func (c *tc) exec(q string, args ...any) {
	c.t.Helper()
	_, err := c.d.Exec(c.ctx, q, args...)
	c.must(err)
}

func (c *tc) count(q string, args ...any) int {
	c.t.Helper()
	var n int
	c.must(c.d.Get(c.ctx, &n, q, args...))
	return n
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "mail_handler", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// submit は submit_email(filename, options) { |raw| ... }。
func (c *tc) submit(name string, o Options, edit ...func(string) string) any {
	c.t.Helper()
	raw := string(readFixture(c.t, name))
	for _, f := range edit {
		raw = f(raw)
	}
	obj, err := c.h.Receive(c.ctx, []byte(raw), o)
	c.must(err)
	return obj
}

func (c *tc) env() *issues.Env {
	e := issues.NewEnv(c.d, c.st, nil)
	e.Now = func() time.Time { return frozenNow }
	return e
}

// issue は再読み込みしたチケット（issue.reload）。
func (c *tc) issue(id int64) *issues.Issue {
	c.t.Helper()
	iss, err := c.env().Load(c.ctx, id)
	c.must(err)
	return iss
}

// assertIssue は obj が保存済みのチケットであることを確かめ、再読み込みして返す。
func (c *tc) assertIssue(obj any) *issues.Issue {
	c.t.Helper()
	iss, ok := obj.(*issues.Issue)
	if !ok || iss == nil {
		c.t.Fatalf("expected Issue, got %T (%v)\nlogs:\n%s", obj, obj, c.logs.String())
	}
	if iss.NewRecord() || iss.ID == 0 {
		c.t.Fatalf("issue not saved")
	}
	return c.issue(iss.ID)
}

func (c *tc) assertJournal(obj any) *issues.Journal {
	c.t.Helper()
	j, ok := obj.(*issues.Journal)
	if !ok || j == nil {
		c.t.Fatalf("expected Journal, got %T (%v)\nlogs:\n%s", obj, obj, c.logs.String())
	}
	return j
}

func (c *tc) userByLogin(login string) *domain.User {
	c.t.Helper()
	u, err := repository.FindUserByLogin(c.ctx, c.d, login)
	c.must(err)
	return u
}

func (c *tc) userByMail(mail string) *domain.User {
	c.t.Helper()
	u, err := repository.FindUserByMail(c.ctx, c.d, mail)
	c.must(err)
	return u
}

func (c *tc) idByName(table, name string) int64 {
	c.t.Helper()
	var id int64
	c.must(c.d.Get(c.ctx, &id, `SELECT id FROM `+table+` WHERE name = ? ORDER BY id LIMIT 1`, name))
	return id
}

func (c *tc) attachments(kind string, id int64) []*domain.Attachment {
	c.t.Helper()
	as, err := repository.ContainerAttachmentList(c.ctx, c.d, kind, id)
	c.must(err)
	return as
}

func (c *tc) watcherIDs(iss *issues.Issue) []int64 {
	c.t.Helper()
	ids, err := c.env().WatcherIDs(c.ctx, iss)
	c.must(err)
	return ids
}

// addPermission は Role#add_permission!。
func (c *tc) addPermission(role int64, perms ...string) {
	c.t.Helper()
	for _, p := range perms {
		if c.count(`SELECT COUNT(*) FROM role_permissions WHERE role_id = ? AND permission = ?`, role, p) == 0 {
			c.exec(`INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`, role, p)
		}
	}
}

// removePermissionAll は Role.all.each {|r| r.remove_permission! perm}。
func (c *tc) removePermissionAll(perm string) {
	c.t.Helper()
	c.exec(`DELETE FROM role_permissions WHERE permission = ?`, perm)
}

// anonymousRole は Role.anonymous の id。
func (c *tc) anonymousRole() int64 {
	c.t.Helper()
	var id int64
	c.must(c.d.Get(c.ctx, &id, `SELECT id FROM roles WHERE builtin = ?`, domain.RoleBuiltinAnonymous))
	return id
}
