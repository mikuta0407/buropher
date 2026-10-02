package mailhandler

// test/unit/mail_handler_test.rb の移植（返信・無視するメール・ユーザー作成の補助）。

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func TestShouldIgnoreEmailsFromLockedUsers(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE principals SET status = 3 WHERE id = 2`)
	before := c.count(`SELECT COUNT(*) FROM issues`)
	if obj := c.submit("ticket_on_given_project.eml", Options{}); obj != nil {
		t.Errorf("got %T", obj)
	}
	if after := c.count(`SELECT COUNT(*) FROM issues`); after != before {
		t.Errorf("issue count %d -> %d", before, after)
	}
	if !strings.Contains(c.logs.String(), "ignoring email from non-active user") {
		t.Error("dispatch should not be called")
	}
}

func TestShouldIgnoreEmailsFromEmissionAddress(t *testing.T) {
	c := setup(t)
	c.addPermission(c.anonymousRole(), "add_issues")
	for _, addr := range []string{"redmine@example.net", "Redmine <redmine@example.net>", "redmine@example.net (Redmine)"} {
		c.set("mail_from", addr)
		before := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`)
		if obj := c.submit("ticket_from_emission_address.eml", Options{Issue: map[string]string{"project": "ecookbook"}, UnknownUser: "create"}); obj != nil {
			t.Errorf("%s: got %T", addr, obj)
		}
		if after := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`); after != before {
			t.Errorf("%s: user count %d -> %d", addr, before, after)
		}
	}
}

func TestShouldIgnoreAutoRepliedEmails(t *testing.T) {
	c := setup(t)
	for _, header := range []string{"Auto-Submitted: auto-replied", "Auto-Submitted: Auto-Replied",
		"Auto-Submitted: auto-generated", "X-Autoreply: yes"} {
		raw := header + "\n" + string(readFixture(t, "ticket_on_given_project.eml"))
		before := c.count(`SELECT COUNT(*) FROM issues`)
		obj, err := c.h.Receive(c.ctx, []byte(raw), Options{})
		c.must(err)
		if obj != nil {
			t.Errorf("email with %s header was not ignored", header)
		}
		if after := c.count(`SELECT COUNT(*) FROM issues`); after != before {
			t.Errorf("%s: issue count %d -> %d", header, before, after)
		}
	}
}

func TestShouldNotIgnoreAutoSubmittedHeadersNotDefinedInRFC3834(t *testing.T) {
	c := setup(t)
	raw := "Auto-Submitted: auto-forwarded\n" + string(readFixture(t, "ticket_on_given_project.eml"))
	before := c.count(`SELECT COUNT(*) FROM issues`)
	obj, err := c.h.Receive(c.ctx, []byte(raw), Options{})
	c.must(err)
	if obj == nil {
		t.Error("email with auto-forwarded header was ignored")
	}
	if after := c.count(`SELECT COUNT(*) FROM issues`); after != before+1 {
		t.Errorf("issue count %d -> %d", before, after)
	}
}

func TestAddIssueShouldSendEmailNotification(t *testing.T) {
	c := setup(t)
	c.set("notified_events", []any{"issue_added"})
	c.assertIssue(c.submit("ticket_on_given_project.eml", Options{}))
	if n := c.notifier.deliveries(); n != 1 {
		t.Errorf("deliveries = %d", n)
	}
}

func TestUpdateIssue(t *testing.T) {
	c := setup(t)
	j := c.assertJournal(c.submit("ticket_reply.eml", Options{}))
	if j.UserID != 2 || j.IssueID != 2 {
		t.Errorf("user/issue = %d/%d", j.UserID, j.IssueID)
	}
	if !strings.Contains(j.Notes, "This is reply") {
		t.Errorf("notes = %q", j.Notes)
	}
	if j.PrivateNotes {
		t.Error("private_notes")
	}
	if iss := c.issue(2); iss.TrackerID != c.idByName("trackers", "Feature request") {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
}

func replaceSubject(subject string) func(string) string {
	return func(s string) string {
		return regexp.MustCompile(`(?m)^Subject:.*$`).ReplaceAllLiteralString(s, "Subject: "+subject)
	}
}

func TestUpdateIssueShouldAcceptIssueIDAfterSpaceInsideBrackets(t *testing.T) {
	c := setup(t)
	j := c.assertJournal(c.submit("ticket_reply_with_status.eml", Options{}, replaceSubject("Re: [Feature request #2] Add ingredients categories")))
	if j.IssueID != 2 {
		t.Errorf("issue = %d", j.IssueID)
	}
}

func TestUpdateIssueShouldAcceptIssueIDInsideBrackets(t *testing.T) {
	c := setup(t)
	j := c.assertJournal(c.submit("ticket_reply_with_status.eml", Options{}, replaceSubject("Re: [#2] Add ingredients categories")))
	if j.IssueID != 2 {
		t.Errorf("issue = %d", j.IssueID)
	}
}

func TestUpdateIssueShouldIgnoreBogusIssueIDsInSubject(t *testing.T) {
	c := setup(t)
	j := c.assertJournal(c.submit("ticket_reply_with_status.eml", Options{},
		replaceSubject("Re: [12345#1][bogus#1][Feature request #2] Add ingredients categories")))
	if j.IssueID != 2 {
		t.Errorf("issue = %d", j.IssueID)
	}
}

func TestUpdateIssueWithAttributeChanges(t *testing.T) {
	c := setup(t)
	j := c.assertJournal(c.submit("ticket_reply_with_status.eml", Options{AllowOverride: []string{"status", "assigned_to",
		"start_date", "due_date", "float field"}}))
	iss := c.issue(j.IssueID)
	if j.UserID != 2 || j.IssueID != 2 || !strings.Contains(j.Notes, "This is reply") {
		t.Errorf("journal = %d %d %q", j.UserID, j.IssueID, j.Notes)
	}
	if iss.TrackerID != c.idByName("trackers", "Feature request") || iss.StatusID != c.idByName("issue_statuses", "Resolved") {
		t.Errorf("tracker/status = %d/%d", iss.TrackerID, iss.StatusID)
	}
	if iss.StartDate == nil || iss.StartDate.Format("2006-01-02") != "2010-01-01" || iss.DueDate == nil ||
		iss.DueDate.Format("2006-01-02") != "2010-12-31" {
		t.Errorf("dates = %v %v", iss.StartDate, iss.DueDate)
	}
	if iss.AssignedToID == nil || *iss.AssignedToID != 2 {
		t.Errorf("assigned_to = %v", ptrVal(iss.AssignedToID))
	}
	var cfID int64
	c.must(c.d.Get(c.ctx, &cfID, `SELECT id FROM custom_fields WHERE name = 'Float field'`))
	if v := c.customValues(iss.ID, cfID); !slices.Equal(v, []string{"52.6"}) {
		t.Errorf("float field = %v", v)
	}
	for _, re := range []string{`(?im)^Status:`, `(?im)^Start Date:`} {
		if regexp.MustCompile(re).MatchString(j.Notes) {
			t.Errorf("notes match %s", re)
		}
	}
}

func TestUpdateIssueWithAttachment(t *testing.T) {
	c := setup(t)
	journals := c.count(`SELECT COUNT(*) FROM issue_journals`)
	details := c.count(`SELECT COUNT(*) FROM issue_journal_details`)
	atts := c.count(`SELECT COUNT(*) FROM attachments`)
	issuesN := c.count(`SELECT COUNT(*) FROM issues`)
	c.submit("ticket_with_attachment.eml", Options{}, replaceSubject("Re: [Cookbook - Feature #2] (New) Add ingredients categories"))
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != journals+1 || c.count(`SELECT COUNT(*) FROM issue_journal_details`) != details+1 ||
		c.count(`SELECT COUNT(*) FROM attachments`) != atts+1 || c.count(`SELECT COUNT(*) FROM issues`) != issuesN {
		t.Fatalf("counts changed unexpectedly\n%s", c.logs.String())
	}
	var jid int64
	c.must(c.d.Get(c.ctx, &jid, `SELECT MAX(id) FROM issue_journals`))
	var issueID int64
	c.must(c.d.Get(c.ctx, &issueID, `SELECT issue_id FROM issue_journals WHERE id = ?`, jid))
	if issueID != 2 {
		t.Errorf("issue = %d", issueID)
	}
	var rows []struct {
		Property string  `db:"property"`
		Value    *string `db:"value"`
	}
	c.must(c.d.Select(c.ctx, &rows, `SELECT property, value FROM issue_journal_details WHERE journal_id = ?`, jid))
	if len(rows) != 1 || rows[0].Property != "attachment" || rows[0].Value == nil || *rows[0].Value != "Paella.jpg" {
		t.Errorf("details = %+v", rows)
	}
}

func TestUpdateIssueShouldDiscardAllChangesOnValidationFailure(t *testing.T) {
	c := setup(t)
	// Issue.any_instance.stubs(:valid?).returns(false) の代わりに、必須のカスタムフィールドで検証を失敗させる
	id := c.createIssueCustomField("Required for mail", "string", false, true, nil)
	c.exec(`UPDATE custom_fields SET is_required = ? WHERE id = ?`, true, id)
	journals := c.count(`SELECT COUNT(*) FROM issue_journals`)
	details := c.count(`SELECT COUNT(*) FROM issue_journal_details`)
	atts := c.count(`SELECT COUNT(*) FROM attachments`)
	obj := c.submit("ticket_with_attachment.eml", Options{}, replaceSubject("Re: [Cookbook - Feature #2] (New) Add ingredients categories"))
	if obj != nil {
		t.Errorf("got %T", obj)
	}
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != journals || c.count(`SELECT COUNT(*) FROM issue_journal_details`) != details ||
		c.count(`SELECT COUNT(*) FROM attachments`) != atts {
		t.Error("changes were not discarded")
	}
}

func TestUpdateIssueShouldSendEmailNotification(t *testing.T) {
	c := setup(t)
	c.assertJournal(c.submit("ticket_reply.eml", Options{}))
	if n := c.notifier.deliveries(); n != 3 {
		t.Errorf("deliveries = %d (%+v)", n, c.notifier.ns)
	}
}

func TestUpdateIssueShouldNotSetDefaults(t *testing.T) {
	c := setup(t)
	j := c.assertJournal(c.submit("ticket_reply.eml", Options{Issue: map[string]string{"tracker": "Support request", "priority": "High"}}))
	if !strings.Contains(j.Notes, "This is reply") {
		t.Errorf("notes = %q", j.Notes)
	}
	iss := c.issue(j.IssueID)
	if iss.TrackerID != c.idByName("trackers", "Feature request") || iss.PriorityID != c.idByName("issue_priorities", "Normal") {
		t.Errorf("tracker/priority = %d/%d", iss.TrackerID, iss.PriorityID)
	}
}

func TestUpdateIssueShouldAddCcAsWatchers(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM watchers`)
	before := c.count(`SELECT COUNT(*) FROM watchers`)
	if obj := c.submit("issue_update_with_cc.eml", Options{}); obj == nil {
		t.Fatalf("not received\n%s", c.logs.String())
	}
	if after := c.count(`SELECT COUNT(*) FROM watchers`); after != before+1 {
		t.Errorf("watchers %d -> %d", before, after)
	}
	u := c.userByMail("dlopper@somenet.foo")
	if ids := c.watcherIDs(c.issue(2)); !slices.Equal(ids, []int64{u.ID}) {
		t.Errorf("watchers = %v", ids)
	}
}

func TestUpdateIssueShouldNotAddCcAsWatchersIfAlreadyWatching(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM watchers`)
	u := c.userByMail("dlopper@somenet.foo")
	c.exec(`INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', 2, ?)`, u.ID)
	before := c.count(`SELECT COUNT(*) FROM watchers`)
	if obj := c.submit("issue_update_with_cc.eml", Options{}); obj == nil {
		t.Fatal("not received")
	}
	if after := c.count(`SELECT COUNT(*) FROM watchers`); after != before {
		t.Errorf("watchers %d -> %d", before, after)
	}
}

func replaceInReplyTo(v string) func(string) string {
	return func(s string) string {
		return regexp.MustCompile(`(?m)^In-Reply-To:.*$`).ReplaceAllLiteralString(s, "In-Reply-To: "+v)
	}
}

func TestReplyingToAPrivateNoteShouldAddReplyAsPrivate(t *testing.T) {
	c := setup(t)
	pid, err := c.d.InsertReturningID(c.ctx, `INSERT INTO issue_journals (issue_id, user_id, notes, private_notes, created_at, updated_at) VALUES (1, 2, 'Private notes', ?, ?, ?)`,
		true, dbNow(), dbNow())
	c.must(err)
	before := c.count(`SELECT COUNT(*) FROM issue_journals`)
	j := c.assertJournal(c.submit("ticket_reply.eml", Options{}, replaceInReplyTo("<redmine.journal-"+strconv.FormatInt(pid, 10)+".20060719210421@osiris>")))
	if !strings.Contains(j.Notes, "This is reply") || !j.PrivateNotes {
		t.Errorf("journal = %q private=%v", j.Notes, j.PrivateNotes)
	}
	if after := c.count(`SELECT COUNT(*) FROM issue_journals`); after != before+1 {
		t.Errorf("journals %d -> %d", before, after)
	}
}

func TestReplyToANonexistentIssue(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM issue_journal_details WHERE journal_id IN (SELECT id FROM issue_journals WHERE issue_id = 2)`)
	c.exec(`DELETE FROM issue_journals WHERE issue_id = 2`)
	c.exec(`DELETE FROM issue_relations WHERE issue_from_id = 2 OR issue_to_id = 2`)
	c.exec(`DELETE FROM time_entries WHERE issue_id = 2`)
	c.exec(`DELETE FROM issues WHERE id = 2`)
	issuesN := c.count(`SELECT COUNT(*) FROM issues`)
	journals := c.count(`SELECT COUNT(*) FROM issue_journals`)
	if obj := c.submit("ticket_reply_with_status.eml", Options{}); obj != nil {
		t.Errorf("got %T", obj)
	}
	if c.count(`SELECT COUNT(*) FROM issues`) != issuesN || c.count(`SELECT COUNT(*) FROM issue_journals`) != journals {
		t.Error("counts changed")
	}
}

func TestReplyToAnIssueWithoutPermission(t *testing.T) {
	c := setup(t)
	c.removePermissionAll("add_issue_notes")
	journals := c.count(`SELECT COUNT(*) FROM issue_journals`)
	if obj := c.submit("ticket_reply_with_status.eml", Options{}); obj != nil {
		t.Errorf("got %T", obj)
	}
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != journals {
		t.Error("journal created")
	}
}

func (c *tc) lastJournalIDOfIssue2() int64 {
	var id int64
	c.must(c.d.Get(c.ctx, &id, `SELECT MAX(id) FROM issue_journals WHERE issue_id = 2`))
	c.exec(`DELETE FROM issue_journal_details WHERE journal_id = ?`, id)
	c.exec(`DELETE FROM issue_journals WHERE id = ?`, id)
	return id
}

func TestReplyToANonexitentJournal(t *testing.T) {
	c := setup(t)
	jid := c.lastJournalIDOfIssue2()
	journals := c.count(`SELECT COUNT(*) FROM issue_journals`)
	if obj := c.submit("ticket_reply.eml", Options{}, replaceInReplyTo("<redmine.journal-"+strconv.FormatInt(jid, 10)+".20060719210421@osiris>")); obj != nil {
		t.Errorf("got %T", obj)
	}
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != journals {
		t.Error("journal created")
	}
}

func TestReplyToANonexitentJournalWithSubjectFallback(t *testing.T) {
	c := setup(t)
	jid := c.lastJournalIDOfIssue2()
	journals := c.count(`SELECT COUNT(*) FROM issue_journals`)
	j := c.assertJournal(c.submit("ticket_reply.eml", Options{},
		replaceInReplyTo("<redmine.journal-"+strconv.FormatInt(jid, 10)+".20060719210421@osiris>"),
		replaceSubject("Re: [Feature request #2] Add ingredients categories")))
	if j.IssueID != 2 {
		t.Errorf("issue = %d", j.IssueID)
	}
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != journals+1 {
		t.Error("journal not created")
	}
}

func (c *tc) assertMessage(obj any) *domain.Message {
	c.t.Helper()
	m, ok := obj.(*domain.Message)
	if !ok || m == nil || m.ID == 0 {
		c.t.Fatalf("expected saved Message, got %T %v\n%s", obj, obj, c.logs.String())
	}
	got, err := repository.GetMessage(c.ctx, c.d, m.ID)
	c.must(err)
	return got
}

func TestReplyToAMessage(t *testing.T) {
	c := setup(t)
	m := c.assertMessage(c.submit("message_reply.eml", Options{}))
	if m.Subject != "Reply via email" {
		t.Errorf("subject = %q", m.Subject)
	}
	// メッセージ #2 への返信は、その親のトピック #1 への返信になる
	if m.ParentID == nil || *m.ParentID != 1 {
		t.Errorf("parent = %v", ptrVal(m.ParentID))
	}
}

func TestReplyToAMessageBySubject(t *testing.T) {
	c := setup(t)
	m := c.assertMessage(c.submit("message_reply_by_subject.eml", Options{}))
	if m.Subject != "Reply to the first post" {
		t.Errorf("subject = %q", m.Subject)
	}
	if m.ParentID == nil || *m.ParentID != 1 {
		t.Errorf("parent = %v", ptrVal(m.ParentID))
	}
}

func TestReplyToALockedTopic(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE messages SET locked = ? WHERE id = 1`, true)
	before := c.count(`SELECT replies_count FROM messages WHERE id = 1`)
	if _, ok := c.submit("message_reply_by_subject.eml", Options{}).(*domain.Message); ok {
		t.Error("got Message")
	}
	if after := c.count(`SELECT replies_count FROM messages WHERE id = 1`); after != before {
		t.Errorf("replies_count %d -> %d", before, after)
	}
}

func TestReplyToANonexistentTopic(t *testing.T) {
	c := setup(t)
	_, err := repository.DeleteMessage(c.ctx, c.d, 2)
	c.must(err)
	before := c.count(`SELECT COUNT(*) FROM messages`)
	if obj := c.submit("message_reply_by_subject.eml", Options{}); obj != nil {
		t.Errorf("got %T", obj)
	}
	if after := c.count(`SELECT COUNT(*) FROM messages`); after != before {
		t.Errorf("messages %d -> %d", before, after)
	}
}

func TestReplyToATopicWithoutPermission(t *testing.T) {
	c := setup(t)
	c.removePermissionAll("add_messages")
	before := c.count(`SELECT COUNT(*) FROM messages`)
	if obj := c.submit("message_reply_by_subject.eml", Options{}); obj != nil {
		t.Errorf("got %T", obj)
	}
	if after := c.count(`SELECT COUNT(*) FROM messages`); after != before {
		t.Errorf("messages %d -> %d", before, after)
	}
}

func (c *tc) assertComment(obj any) *domain.Comment {
	c.t.Helper()
	m, ok := obj.(*domain.Comment)
	if !ok || m == nil || m.ID == 0 {
		c.t.Fatalf("expected saved Comment, got %T %v\n%s", obj, obj, c.logs.String())
	}
	return m
}

func TestReplyToANews(t *testing.T) {
	c := setup(t)
	m := c.assertComment(c.submit("news_reply.eml", Options{}))
	var row struct {
		NewsID  int64  `db:"news_id"`
		Content string `db:"content"`
	}
	c.must(c.d.Get(c.ctx, &row, `SELECT news_id, content FROM news_comments WHERE id = ?`, m.ID))
	if row.NewsID != 1 || row.Content != "This is a reply to a news." {
		t.Errorf("comment = %+v", row)
	}
}

func TestReplyToANewsComment(t *testing.T) {
	c := setup(t)
	m := c.assertComment(c.submit("news_comment_reply.eml", Options{}))
	var row struct {
		NewsID  int64  `db:"news_id"`
		Content string `db:"content"`
	}
	c.must(c.d.Get(c.ctx, &row, `SELECT news_id, content FROM news_comments WHERE id = ?`, m.ID))
	if row.NewsID != 1 || row.Content != "This is a reply to a comment." {
		t.Errorf("comment = %+v", row)
	}
}

func TestReplyToANonexistantNews(t *testing.T) {
	c := setup(t)
	c.must(repository.DeleteNews(c.ctx, c.d, 1))
	before := c.count(`SELECT COUNT(*) FROM news_comments`)
	if obj := c.submit("news_reply.eml", Options{}); obj != nil {
		t.Errorf("news_reply: got %T", obj)
	}
	if obj := c.submit("news_comment_reply.eml", Options{}); obj != nil {
		t.Errorf("news_comment_reply: got %T", obj)
	}
	if after := c.count(`SELECT COUNT(*) FROM news_comments`); after != before {
		t.Errorf("comments %d -> %d", before, after)
	}
}

func TestReplyToANewsWithoutPermission(t *testing.T) {
	c := setup(t)
	c.removePermissionAll("comment_news")
	before := c.count(`SELECT COUNT(*) FROM news_comments`)
	if obj := c.submit("news_reply.eml", Options{}); obj != nil {
		t.Errorf("news_reply: got %T", obj)
	}
	if obj := c.submit("news_comment_reply.eml", Options{}); obj != nil {
		t.Errorf("news_comment_reply: got %T", obj)
	}
	if after := c.count(`SELECT COUNT(*) FROM news_comments`); after != before {
		t.Errorf("comments %d -> %d", before, after)
	}
}

func TestNewUserFromAttributesShouldReturnValidUser(t *testing.T) {
	c := setup(t)
	cases := []struct{ addr, name, login, first, last string }{
		{"jsmith@example.net", "", "jsmith@example.net", "jsmith", "-"},
		{"jsmith@example.net", "John", "jsmith@example.net", "John", "-"},
		{"jsmith@example.net", "John Smith", "jsmith@example.net", "John", "Smith"},
		{"jsmith@example.net", "John Paul Smith", "jsmith@example.net", "John", "Paul Smith"},
		{"jsmith@example.net", "AVeryLongFirstnameThatExceedsTheMaximumLength Smith", "jsmith@example.net", "AVeryLongFirstnameThatExceedsT", "Smith"},
		{"jsmith@example.net", "John AVeryLongLastnameThatExceedsTheMaximumLength", "jsmith@example.net", "John", "AVeryLongLastnameThatExceedsTh"},
	}
	for _, tt := range cases {
		u, err := c.h.NewUserFromAttributes(context.Background(), c.d, tt.addr, tt.name)
		c.must(err)
		if u.Mail != tt.addr || u.Login != tt.login || u.Firstname != tt.first || u.Lastname != tt.last || u.MailNotification != "only_my_events" {
			t.Errorf("%q %q: got %+v", tt.addr, tt.name, u)
		}
	}
}

func TestNewUserFromAttributesShouldUseDefaultLoginIfInvalid(t *testing.T) {
	c := setup(t)
	u, err := c.h.NewUserFromAttributes(context.Background(), c.d, "foo+bar@example.net", "")
	c.must(err)
	if !regexp.MustCompile(`^user[a-f0-9]+$`).MatchString(u.Login) {
		t.Errorf("login = %q", u.Login)
	}
	if u.Mail != "foo+bar@example.net" {
		t.Errorf("mail = %q", u.Mail)
	}
}

func TestNewUserWithUTF8EncodedFullnameShouldBeDecoded(t *testing.T) {
	c := setup(t)
	before := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`)
	c.submit("fullname_of_sender_as_utf8_encoded.eml", Options{Issue: map[string]string{"project": "ecookbook"}, UnknownUser: "create"})
	if after := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`); after != before+1 {
		t.Fatalf("user count %d -> %d", before, after)
	}
	u := c.userByMail("foo@example.org")
	if u.Firstname != "Ää" || u.Lastname != "Öö" {
		t.Errorf("name = %q %q", u.Firstname, u.Lastname)
	}
}

func TestNewUserWithFullnameInParentheses(t *testing.T) {
	c := setup(t)
	before := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`)
	c.submit("fullname_of_sender_in_parentheses.eml", Options{Issue: map[string]string{"project": "ecookbook"}, UnknownUser: "create"})
	if after := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`); after != before+1 {
		t.Fatalf("user count %d -> %d", before, after)
	}
	u := c.userByMail("jdoe@example.net")
	if u.Firstname != "John" || u.Lastname != "Doe" {
		t.Errorf("name = %q %q", u.Firstname, u.Lastname)
	}
}

func TestExtractOptionsFromEnvShouldReturnOptions(t *testing.T) {
	env := map[string]string{"tracker": "defect", "project": "foo", "unknown_user": "create", "no_notification": "1"}
	o := ExtractOptionsFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if len(o.Issue) != 2 || o.Issue["tracker"] != "defect" || o.Issue["project"] != "foo" {
		t.Errorf("issue = %v", o.Issue)
	}
	if o.UnknownUser != "create" || o.NoNotification != "1" || o.AllowOverride != nil || o.NoPermissionCheck != "" {
		t.Errorf("options = %+v", o)
	}
}

func TestSafeReceiveShouldRescueExceptionsAndReturnFalse(t *testing.T) {
	c := setup(t)
	c.d.Close()
	if obj := c.h.SafeReceive(c.ctx, readFixture(t, "ticket_on_given_project.eml"), Options{}); obj != nil {
		t.Errorf("got %T", obj)
	}
}
