package mailhandler

// test/unit/mail_handler_test.rb の移植（新しいチケットの作成）。

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

func ptrVal[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestAddIssueWithSpecificOverrides(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{AllowOverride: []string{
		"status", "start_date", "due_date", "assigned_to", "fixed_version", "estimated_hours", "done_ratio", "parent_issue"}}))
	if iss.ProjectID != 2 {
		t.Errorf("project = %d", iss.ProjectID)
	}
	if iss.TrackerID != 1 {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
	if iss.Subject != "New ticket on a given project" {
		t.Errorf("subject = %q", iss.Subject)
	}
	jsmith := c.userByLogin("jsmith")
	if iss.AuthorID != jsmith.ID {
		t.Errorf("author = %d", iss.AuthorID)
	}
	if iss.StatusID != c.idByName("issue_statuses", "Resolved") {
		t.Errorf("status = %d", iss.StatusID)
	}
	desc := ptrVal(iss.Description).(string)
	if !strings.Contains(desc, "Lorem ipsum dolor sit amet, consectetuer adipiscing elit.") {
		t.Errorf("description = %q", desc)
	}
	if iss.StartDate == nil || iss.StartDate.Format("2006-01-02") != "2010-01-01" {
		t.Errorf("start_date = %v", iss.StartDate)
	}
	if iss.DueDate == nil || iss.DueDate.Format("2006-01-02") != "2010-12-31" {
		t.Errorf("due_date = %v", iss.DueDate)
	}
	if iss.AssignedToID == nil || *iss.AssignedToID != jsmith.ID {
		t.Errorf("assigned_to = %v", ptrVal(iss.AssignedToID))
	}
	if iss.FixedVersionID == nil || *iss.FixedVersionID != c.idByName("versions", "Alpha") {
		t.Errorf("fixed_version = %v", ptrVal(iss.FixedVersionID))
	}
	if iss.EstimatedHours == nil || *iss.EstimatedHours != 2.5 {
		t.Errorf("estimated_hours = %v", ptrVal(iss.EstimatedHours))
	}
	if iss.DoneRatio != 30 {
		t.Errorf("done_ratio = %d", iss.DoneRatio)
	}
	if iss.ParentID == nil || *iss.ParentID != 4 {
		t.Errorf("parent = %v", ptrVal(iss.ParentID))
	}
	// キーワードは本文から除かれる
	for _, re := range []string{`(?im)^Project:`, `(?im)^Status:`, `(?im)^Start Date:`} {
		if regexp.MustCompile(re).MatchString(desc) {
			t.Errorf("description matches %s", re)
		}
	}
}

func TestAddIssueWithAllOverrides(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{AllowOverride: []string{"all"}}))
	if iss.ProjectID != 2 || iss.TrackerID != 1 || iss.StatusID != c.idByName("issue_statuses", "Resolved") {
		t.Errorf("project/tracker/status = %d/%d/%d", iss.ProjectID, iss.TrackerID, iss.StatusID)
	}
	if iss.StartDate == nil || iss.StartDate.Format("2006-01-02") != "2010-01-01" || iss.DueDate == nil ||
		iss.DueDate.Format("2006-01-02") != "2010-12-31" {
		t.Errorf("dates = %v %v", iss.StartDate, iss.DueDate)
	}
	if iss.AssignedToID == nil || *iss.AssignedToID != 2 {
		t.Errorf("assigned_to = %v", ptrVal(iss.AssignedToID))
	}
	if iss.FixedVersionID == nil || *iss.FixedVersionID != c.idByName("versions", "Alpha") {
		t.Errorf("fixed_version = %v", ptrVal(iss.FixedVersionID))
	}
	if iss.EstimatedHours == nil || *iss.EstimatedHours != 2.5 || iss.DoneRatio != 30 {
		t.Errorf("estimated/done = %v %d", ptrVal(iss.EstimatedHours), iss.DoneRatio)
	}
	if iss.ParentID == nil || *iss.ParentID != 4 {
		t.Errorf("parent = %v", ptrVal(iss.ParentID))
	}
}

func TestAddIssueWithoutOverridesShouldIgnoreAttributes(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_transitions`)
	c.exec(`DELETE FROM workflow_field_rules`)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{}))
	if iss.ProjectID != 2 || iss.Subject != "New ticket on a given project" || iss.AuthorID != 2 {
		t.Errorf("project/subject/author = %d %q %d", iss.ProjectID, iss.Subject, iss.AuthorID)
	}
	if iss.TrackerID != 1 {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
	if iss.StatusID != c.idByName("issue_statuses", "New") {
		t.Errorf("status = %d", iss.StatusID)
	}
	if iss.StartDate != nil && iss.StartDate.Format("2006-01-02") == "2010-01-01" {
		t.Errorf("start_date = %v", iss.StartDate)
	}
	if iss.DueDate != nil || iss.AssignedToID != nil || iss.FixedVersionID != nil || iss.EstimatedHours != nil || iss.ParentID != nil {
		t.Errorf("unexpected attributes: %v %v %v %v %v", iss.DueDate, ptrVal(iss.AssignedToID), ptrVal(iss.FixedVersionID),
			ptrVal(iss.EstimatedHours), ptrVal(iss.ParentID))
	}
	if iss.DoneRatio != 0 {
		t.Errorf("done_ratio = %d", iss.DoneRatio)
	}
}

func TestAddIssueToProjectSpecifiedBySubaddress(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_project_given_by_to_header.eml", Options{
		Issue: map[string]string{"tracker": "Support request"}, ProjectFromSubaddress: "redmine@somenet.foo"}))
	if iss.ProjectID != 2 {
		t.Errorf("project = %d", iss.ProjectID)
	}
	if iss.TrackerID != c.idByName("trackers", "Support request") {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
}

func TestAddIssueWithDefaultTracker(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{Issue: map[string]string{"tracker": "Support request"}}))
	if iss.TrackerID != c.idByName("trackers", "Support request") {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
}

func TestAddIssueWithDefaultVersion(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{Issue: map[string]string{"fixed_version": "Alpha"}}))
	if iss.FixedVersionID == nil || *iss.FixedVersionID != c.idByName("versions", "Alpha") {
		t.Errorf("fixed_version = %v", ptrVal(iss.FixedVersionID))
	}
}

func TestAddIssueWithDefaultAssignedTo(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{Issue: map[string]string{"assigned_to": "jsmith"}}))
	if iss.AssignedToID == nil || *iss.AssignedToID != 2 {
		t.Errorf("assigned_to = %v", ptrVal(iss.AssignedToID))
	}
}

func TestAddIssueWithStatusOverride(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{AllowOverride: []string{"status"}}))
	if iss.ProjectID != 2 || iss.StatusID != c.idByName("issue_statuses", "Resolved") {
		t.Errorf("project/status = %d/%d", iss.ProjectID, iss.StatusID)
	}
}

func TestAddIssueShouldAcceptIsPrivateAttribute(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{Issue: map[string]string{"is_private": "1"}}))
	if !iss.IsPrivate {
		t.Error("issue should be private")
	}
}

func TestAddIssueWithGroupAssignment(t *testing.T) {
	c := setup(t)
	c.set("issue_group_assignment", "1")
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{AllowOverride: []string{"assigned_to"}},
		func(s string) string { return strings.ReplaceAll(s, "Assigned to: John Smith", "Assigned to: B Team") }))
	if iss.AssignedToID == nil || *iss.AssignedToID != 11 {
		t.Errorf("assigned_to = %v", ptrVal(iss.AssignedToID))
	}
}

func TestAddIssueWithPartialAttributesOverride(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_with_attributes.eml", Options{Issue: map[string]string{"priority": "High"},
		AllowOverride: []string{"tracker"}}))
	if iss.Subject != "New ticket on a given project" || iss.AuthorID != 2 || iss.ProjectID != 2 {
		t.Errorf("subject/author/project = %q %d %d", iss.Subject, iss.AuthorID, iss.ProjectID)
	}
	if iss.TrackerID != c.idByName("trackers", "Feature request") {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
	if iss.CategoryID != nil {
		t.Errorf("category = %v", *iss.CategoryID)
	}
	if iss.PriorityID != c.idByName("issue_priorities", "High") {
		t.Errorf("priority = %d", iss.PriorityID)
	}
	if !strings.Contains(ptrVal(iss.Description).(string), "Lorem ipsum dolor sit amet, consectetuer adipiscing elit.") {
		t.Error("description")
	}
}

func TestAddIssueWithSpacesBetweenAttributeAndSeparator(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_with_spaces_between_attribute_and_separator.eml",
		Options{AllowOverride: []string{"tracker,category,priority"}}))
	if iss.TrackerID != c.idByName("trackers", "Feature request") {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
	if iss.CategoryID == nil || *iss.CategoryID != c.idByName("issue_categories", "Stock management") {
		t.Errorf("category = %v", ptrVal(iss.CategoryID))
	}
	if iss.PriorityID != c.idByName("issue_priorities", "Urgent") {
		t.Errorf("priority = %d", iss.PriorityID)
	}
}

func TestAddIssueWithAttachmentToSpecificProject(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_with_attachment.eml", Options{Issue: map[string]string{"project": "onlinestore"}}))
	if iss.Subject != "Ticket created by email with attachment" || iss.AuthorID != 2 || iss.ProjectID != 2 {
		t.Errorf("subject/author/project = %q %d %d", iss.Subject, iss.AuthorID, iss.ProjectID)
	}
	if d := ptrVal(iss.Description); d != "This is  a new ticket with attachments" {
		t.Errorf("description = %q", d)
	}
	as := c.attachments("issue", iss.ID)
	if len(as) != 1 {
		t.Fatalf("attachments = %d", len(as))
	}
	if as[0].Filename != "Paella.jpg" || as[0].ContentType != "image/jpeg" || as[0].Filesize != 10790 {
		t.Errorf("attachment = %q %q %d", as[0].Filename, as[0].ContentType, as[0].Filesize)
	}
}

func TestAddIssueWithCustomFields(t *testing.T) {
	c := setup(t)
	// IssueCustomField.generate!(:field_format => 'list', :name => 'OS', :multiple => true, ...)
	id := c.createIssueCustomField("OS", "list", true, true, []string{"Linux", "Windows", "Mac OS X"})
	iss := c.assertIssue(c.submit("ticket_with_custom_fields.eml", Options{Issue: map[string]string{"project": "onlinestore"},
		AllowOverride: []string{"database", "Searchable_field", "OS"}}))
	if iss.Subject != "New ticket with custom field values" {
		t.Errorf("subject = %q", iss.Subject)
	}
	if v := c.customValues(iss.ID, 1); !slices.Equal(v, []string{"PostgreSQL"}) {
		t.Errorf("cf1 = %v", v)
	}
	if v := c.customValues(iss.ID, 2); !slices.Equal(v, []string{"Value for a custom field"}) {
		t.Errorf("cf2 = %v", v)
	}
	v := c.customValues(iss.ID, id)
	slices.Sort(v)
	if !slices.Equal(v, []string{"Mac OS X", "Windows"}) {
		t.Errorf("OS = %v", v)
	}
	if regexp.MustCompile(`(?im)^searchable field:`).MatchString(ptrVal(iss.Description).(string)) {
		t.Error("keyword not removed")
	}
}

func TestAddIssueWithVersionCustomFields(t *testing.T) {
	c := setup(t)
	id := c.createIssueCustomField("Affected version", "version", false, true, nil)
	iss := c.assertIssue(c.submit("ticket_with_custom_fields.eml", Options{Issue: map[string]string{"project": "ecookbook"},
		AllowOverride: []string{"affected version"}}, func(s string) string { return s + "Affected version: 1.0\n" }))
	if v := c.customValues(iss.ID, id); !slices.Equal(v, []string{"2"}) {
		t.Errorf("affected version = %v", v)
	}
}

func TestAddIssueShouldMatchAssigneeOnDisplayName(t *testing.T) {
	c := setup(t)
	uid := c.createUser("foobar", "Foo Bar", "Foo Baz", "foobar@example.net")
	c.addMember(uid, 2, 1)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{AllowOverride: []string{"assigned_to"}},
		func(s string) string {
			return regexp.MustCompile(`(?m)^Assigned to.*$`).ReplaceAllString(s, "Assigned to: Foo Bar Foo baz")
		}))
	if iss.AssignedToID == nil || *iss.AssignedToID != uid {
		t.Errorf("assigned_to = %v", ptrVal(iss.AssignedToID))
	}
}

func TestAddIssueShouldSetDefaultStartDate(t *testing.T) {
	c := setup(t)
	c.set("default_issue_start_date_to_creation_date", "1")
	iss := c.assertIssue(c.submit("ticket_with_cc.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	if iss.StartDate == nil || iss.StartDate.Format("2006-01-02") != frozenNow.Format("2006-01-02") {
		t.Errorf("start_date = %v", iss.StartDate)
	}
}

func TestAddIssueShouldAddCcAsWatchers(t *testing.T) {
	c := setup(t)
	u := c.userByMail("dlopper@somenet.foo")
	iss := c.assertIssue(c.submit("ticket_with_cc.eml", Options{Issue: map[string]string{"project": "ecookbook"}}))
	ids := c.watcherIDs(iss)
	if !slices.Equal(ids, []int64{u.ID}) {
		t.Errorf("watchers = %v", ids)
	}
}

func TestAddIssueFromAdditionalEmailAddress(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE email_addresses SET address = 'mainaddress@somenet.foo' WHERE user_id = 2 AND is_default = ?`, true)
	c.exec(`INSERT INTO email_addresses (user_id, address, is_default, notify, created_at, updated_at) VALUES (2, 'jsmith@somenet.foo', ?, ?, ?, ?)`,
		false, false, db.NewTime(frozenNow), db.NewTime(frozenNow))
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{}))
	if iss.AuthorID != 2 {
		t.Errorf("author = %d", iss.AuthorID)
	}
}

func TestAddIssueByUnknownUser(t *testing.T) {
	c := setup(t)
	before := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`)
	if obj := c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "ecookbook"}}); obj != nil {
		t.Errorf("got %T", obj)
	}
	if after := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`); after != before {
		t.Errorf("user count %d -> %d", before, after)
	}
}

func TestAddIssueByAnonymousUser(t *testing.T) {
	c := setup(t)
	c.addPermission(c.anonymousRole(), "add_issues", "add_issue_watchers")
	before := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`)
	iss := c.assertIssue(c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "ecookbook"},
		UnknownUser: "accept"}))
	anon := c.count(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)
	if iss.AuthorID != int64(anon) {
		t.Errorf("author = %d (anonymous %d)", iss.AuthorID, anon)
	}
	u := c.userByMail("dlopper@somenet.foo")
	if ids := c.watcherIDs(iss); !slices.Equal(ids, []int64{u.ID}) {
		t.Errorf("watchers = %v", ids)
	}
	if after := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`); after != before {
		t.Errorf("user count %d -> %d", before, after)
	}
}

func TestAddIssueByAnonymousUserWithNoFromAddress(t *testing.T) {
	c := setup(t)
	c.addPermission(c.anonymousRole(), "add_issues")
	iss := c.assertIssue(c.submit("ticket_by_empty_user.eml", Options{Issue: map[string]string{"project": "ecookbook"},
		UnknownUser: "accept"}))
	anon := c.count(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)
	if iss.AuthorID != int64(anon) {
		t.Errorf("author = %d", iss.AuthorID)
	}
}

func TestAddIssueByAnonymousUserOnPrivateProject(t *testing.T) {
	c := setup(t)
	c.addPermission(c.anonymousRole(), "add_issues")
	before := c.count(`SELECT COUNT(*) FROM issues`)
	if obj := c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "onlinestore"},
		UnknownUser: "accept"}); obj != nil {
		t.Errorf("got %T", obj)
	}
	if after := c.count(`SELECT COUNT(*) FROM issues`); after != before {
		t.Errorf("issue count %d -> %d", before, after)
	}
}

func TestAddIssueByAnonymousUserOnPrivateProjectWithoutPermissionCheck(t *testing.T) {
	c := setup(t)
	before := c.count(`SELECT COUNT(*) FROM issues`)
	iss := c.assertIssue(c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "onlinestore"},
		NoPermissionCheck: "1", UnknownUser: "accept"}))
	anon := c.count(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)
	if iss.AuthorID != int64(anon) {
		t.Errorf("author = %d", iss.AuthorID)
	}
	if after := c.count(`SELECT COUNT(*) FROM issues`); after != before+1 {
		t.Errorf("issue count %d -> %d", before, after)
	}
}

func TestNoIssueOnClosedProjectWithoutPermissionCheck(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE projects SET status = 5 WHERE id = 2`)
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "onlinestore"},
		NoPermissionCheck: "1", UnknownUser: "accept"})
	if after := c.count(`SELECT COUNT(*) FROM issues`); after != before {
		t.Errorf("issue count %d -> %d", before, after)
	}
}

func TestNoIssueOnClosedProjectWithoutIssueTrackingModule(t *testing.T) {
	c := setup(t)
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "subproject2"},
		NoPermissionCheck: "1", UnknownUser: "accept"})
	if after := c.count(`SELECT COUNT(*) FROM issues`); after != before {
		t.Errorf("issue count %d -> %d", before, after)
	}
}

func TestAddIssueByCreatedUser(t *testing.T) {
	c := setup(t)
	c.set("default_language", "en")
	before := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`)
	iss := c.assertIssue(c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "ecookbook"},
		UnknownUser: "create"}))
	if after := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`); after != before+1 {
		t.Errorf("user count %d -> %d", before, after)
	}
	u := c.userByMail("john.doe@somenet.foo")
	if iss.AuthorID != u.ID || !u.Active() || u.Firstname != "John" || u.Lastname != "Doe" {
		t.Errorf("author = %d %+v", iss.AuthorID, u.Principal)
	}
	// アカウント情報のメール
	if len(c.mailer.mails) != 1 {
		t.Fatalf("account mails = %d", len(c.mailer.mails))
	}
	m := c.mailer.mails[0]
	if m.user.ID != u.ID || m.password == "" {
		t.Errorf("account mail = %+v", m)
	}
	ok, err := passwordVerify(u.PasswordHash, m.password)
	if err != nil || !ok {
		t.Errorf("password does not match: %v", err)
	}
}

func TestAddIssueShouldSendNotification(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{AllowOverride: []string{"all"}}))
	if iss.ParentID == nil || *iss.ParentID != 4 {
		t.Fatalf("parent = %v", ptrVal(iss.ParentID))
	}
	if n := c.notifier.deliveries(); n != 2 {
		t.Errorf("deliveries = %d (%+v)", n, c.notifier.ns)
	}
	var issueIDs []int64
	for _, x := range c.notifier.ns {
		issueIDs = append(issueIDs, x.IssueID)
	}
	if !slices.Contains(issueIDs, iss.ID) || !slices.Contains(issueIDs, 4) {
		t.Errorf("notified issues = %v", issueIDs)
	}
}

func TestCreatedUserShouldBeAddedToGroups(t *testing.T) {
	c := setup(t)
	g1 := c.createGroup("Group mail 1")
	g2 := c.createGroup("Group mail 2")
	c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "ecookbook"}, UnknownUser: "create",
		DefaultGroup: "Group mail 1,Group mail 2"})
	var uid int64
	c.must(c.d.Get(c.ctx, &uid, `SELECT MAX(id) FROM principals WHERE kind = 'user'`))
	var gids []int64
	c.must(c.d.Select(c.ctx, &gids, `SELECT group_id FROM group_users WHERE user_id = ? ORDER BY group_id`, uid))
	if !slices.Equal(gids, []int64{g1, g2}) {
		t.Errorf("groups = %v (want %d %d)", gids, g1, g2)
	}
}

func TestCreatedUserShouldNotReceiveAccountInformationWithNoAccountInfoOption(t *testing.T) {
	c := setup(t)
	before := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`)
	c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "ecookbook"}, UnknownUser: "create",
		NoAccountNotice: "1"})
	if after := c.count(`SELECT COUNT(*) FROM principals WHERE kind = 'user'`); after != before+1 {
		t.Errorf("user count %d -> %d", before, after)
	}
	if len(c.mailer.mails) != 0 {
		t.Errorf("account mails = %d", len(c.mailer.mails))
	}
	// 新しいチケットの通知の 2 通だけ
	if n := c.notifier.deliveries(); n != 2 {
		t.Errorf("deliveries = %d", n)
	}
}

func TestCreatedUserShouldHaveMailNotificationToNoneWithNoNotificationOption(t *testing.T) {
	c := setup(t)
	c.submit("ticket_by_unknown_user.eml", Options{Issue: map[string]string{"project": "ecookbook"}, UnknownUser: "create",
		NoNotification: "1"})
	var mn string
	c.must(c.d.Get(c.ctx, &mn, `SELECT mail_notification FROM user_notification_settings WHERE user_id = (SELECT MAX(id) FROM principals WHERE kind = 'user')`))
	if mn != "none" {
		t.Errorf("mail_notification = %q", mn)
	}
}

func TestAddIssueWithoutFromHeader(t *testing.T) {
	c := setup(t)
	c.addPermission(c.anonymousRole(), "add_issues")
	if obj := c.submit("ticket_without_from_header.eml", Options{}); obj != nil {
		t.Errorf("got %T", obj)
	}
}

func TestAddIssueWithInvalidAttributes(t *testing.T) {
	c := setup(t)
	c.set("default_issue_start_date_to_creation_date", "0")
	iss := c.assertIssue(c.submit("ticket_with_invalid_attributes.eml", Options{AllowOverride: []string{"tracker,category,priority"}}))
	if iss.AssignedToID != nil || iss.StartDate != nil || iss.DueDate != nil || iss.DoneRatio != 0 || iss.ParentID != nil {
		t.Errorf("unexpected: %v %v %v %d %v", ptrVal(iss.AssignedToID), iss.StartDate, iss.DueDate, iss.DoneRatio, ptrVal(iss.ParentID))
	}
	if iss.PriorityID != c.idByName("issue_priorities", "Normal") {
		t.Errorf("priority = %d", iss.PriorityID)
	}
}

func TestAddIssueWithInvalidProjectShouldBeAssignedToDefaultProject(t *testing.T) {
	c := setup(t)
	iss := c.assertIssue(c.submit("ticket_on_given_project.eml", Options{Issue: map[string]string{"project": "ecookbook"},
		AllowOverride: []string{"project"}}, func(s string) string {
		return regexp.MustCompile(`(?m)^Project:.+$`).ReplaceAllString(s, "Project: invalid")
	}))
	if iss.ProjectID != 1 {
		t.Errorf("project = %d", iss.ProjectID)
	}
}

func TestAddIssueWithPrivateKeyword(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE user_accounts SET language = 'fr' WHERE principal_id = 2`)
	// MemberRole.create! member_id: 3, role_id: 1
	c.exec(`INSERT INTO member_roles (member_id, role_id) VALUES (3, 1)`)
	iss := c.assertIssue(c.submit("ticket_with_localized_private_flag.eml", Options{AllowOverride: []string{"is_private,tracker,category,priority"}}))
	if iss.Subject != "New ticket on a given project" || !iss.IsPrivate {
		t.Errorf("subject/private = %q %v", iss.Subject, iss.IsPrivate)
	}
}

func TestAddIssueWithLocalizedAttributes(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE user_accounts SET language = 'fr' WHERE principal_id = 2`)
	iss := c.assertIssue(c.submit("ticket_with_localized_attributes.eml", Options{AllowOverride: []string{"tracker,category,priority"}}))
	if iss.Subject != "New ticket on a given project" || iss.AuthorID != 2 || iss.ProjectID != 2 {
		t.Errorf("subject/author/project = %q %d %d", iss.Subject, iss.AuthorID, iss.ProjectID)
	}
	if iss.TrackerID != c.idByName("trackers", "Feature request") {
		t.Errorf("tracker = %d", iss.TrackerID)
	}
	if iss.CategoryID == nil || *iss.CategoryID != c.idByName("issue_categories", "Stock management") {
		t.Errorf("category = %v", ptrVal(iss.CategoryID))
	}
	if iss.PriorityID != c.idByName("issue_priorities", "Urgent") {
		t.Errorf("priority = %d", iss.PriorityID)
	}
	if !strings.Contains(ptrVal(iss.Description).(string), "Lorem ipsum dolor sit amet, consectetuer adipiscing elit.") {
		t.Error("description")
	}
}

func TestAddIssueWithJapaneseKeywords(t *testing.T) {
	c := setup(t)
	tid := c.createTracker("開発")
	c.exec(`INSERT INTO project_trackers (project_id, tracker_id) VALUES (1, ?)`, tid)
	iss := c.assertIssue(c.submit("japanese_keywords_iso_2022_jp.eml", Options{Issue: map[string]string{"project": "ecookbook"},
		AllowOverride: []string{"tracker"}}))
	if iss.TrackerID != tid {
		t.Errorf("tracker = %d (want %d)", iss.TrackerID, tid)
	}
}
