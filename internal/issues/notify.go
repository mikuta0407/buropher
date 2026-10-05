// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/dlclark/regexp2"

	"github.com/mikuta0407/buropher/internal/domain"
)

// 通知イベント (Mailer のメール種別)。
const (
	NotifyIssueAdd  = "issue_add"
	NotifyIssueEdit = "issue_edit"
)

// Notification はコミット後にキューへ積む通知 1 件 (Mailer.deliver_issue_add / deliver_issue_edit)。
type Notification struct {
	// Event は NotifyIssueAdd / NotifyIssueEdit。
	Event     string
	IssueID   int64
	JournalID int64 // issue_edit のみ
	// AuthorID は通知の起因ユーザ (issue_add はチケット作成者、issue_edit はジャーナルのユーザ)。
	AuthorID int64
	// Recipients は Redmine が配信するユーザ (deliver_* の users。受信者ごとに 1 通)。
	Recipients []int64
	// SelfExcluded は作成者が no_self_notified のため Recipients から除いた場合 true
	// (Redmine は mail の To から作成者のアドレスを除く)。
	SelfExcluded bool
}

// Notifier は通知をキューへ積む先 (メール・Discord 等の配送はこのパッケージの外)。
type Notifier interface {
	Enqueue(ctx context.Context, n Notification) error
}

// Dispatch はコミット後に通知を Notifier へ渡す (Notifier が nil なら何もしない)。
func (e *Env) Dispatch(ctx context.Context, ns []Notification) error {
	if e.Notifier == nil {
		return nil
	}
	for _, n := range ns {
		if err := e.Notifier.Enqueue(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// notificationSettings は user_notification_settings (行が無い / NULL は Setting.default_notification_option)。
type notificationSettings struct {
	MailNotification string `db:"mail_notification"`
	NoSelfNotified   bool   `db:"no_self_notified"`
	HighPriority     bool   `db:"notify_about_high_priority_issues"`
}

func (e *Env) notificationSettings(ctx context.Context, userID int64) (*notificationSettings, error) {
	var rows []struct {
		MailNotification *string `db:"mail_notification"`
		NoSelfNotified   bool    `db:"no_self_notified"`
		HighPriority     bool    `db:"notify_about_high_priority_issues"`
	}
	if err := e.Q.Select(ctx, &rows, `SELECT mail_notification, no_self_notified, notify_about_high_priority_issues
FROM user_notification_settings WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	def := "only_my_events"
	if e.Settings != nil {
		def = e.Settings.String("default_notification_option")
	}
	ns := &notificationSettings{MailNotification: def, NoSelfNotified: true}
	if e.Settings != nil {
		ns.NoSelfNotified = e.Settings.Bool("default_users_no_self_notified")
	}
	if len(rows) > 0 {
		if rows[0].MailNotification != nil {
			ns.MailNotification = *rows[0].MailNotification
		}
		ns.NoSelfNotified = rows[0].NoSelfNotified
		ns.HighPriority = rows[0].HighPriority
	}
	return ns, nil
}

// previousAssignee は previous_assignee (保存前の担当者。保存後は直前の保存前の値)。
func (iss *Issue) previousAssigneeID() *int64 {
	if iss.orig != nil && iss.AttrChanged("assigned_to_id") {
		return iss.orig.AssignedToID
	}
	if iss.saved != nil {
		return iss.saved.AssignedToID
	}
	return nil
}

// expandPrincipals はグループを所属ユーザに展開する (重複除去、出現順)。
func (e *Env) expandPrincipals(ctx context.Context, ids []int64, activeGroupUsers bool) ([]int64, error) {
	var out []int64
	for _, id := range ids {
		p, err := e.Principal(ctx, id)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		if p.Kind.IsGroup() {
			q := `SELECT gu.user_id FROM group_users gu JOIN principals p ON p.id = gu.user_id WHERE gu.group_id = ?`
			if activeGroupUsers {
				q += ` AND p.status = 1`
			}
			var us []int64
			if err := e.Q.Select(ctx, &us, q+` ORDER BY gu.user_id`, id); err != nil {
				return nil, err
			}
			for _, u := range us {
				if !containsID(out, u) {
					out = append(out, u)
				}
			}
			continue
		}
		if !containsID(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// notifyAbout は User#notify_about?(issue)。
func (e *Env) notifyAbout(ctx context.Context, u *domain.User, ns *notificationSettings, iss *Issue) (bool, error) {
	switch ns.MailNotification {
	case "all":
		return true, nil
	case "", "none":
		return false, nil
	}
	isAuthor := iss.AuthorID == u.ID
	assigned, err := e.isOrBelongsTo(ctx, u, iss.AssignedToID)
	if err != nil {
		return false, err
	}
	prev, err := e.isOrBelongsTo(ctx, u, iss.previousAssigneeID())
	if err != nil {
		return false, err
	}
	switch ns.MailNotification {
	case "selected", "only_my_events":
		return isAuthor || assigned || prev, nil
	case "only_assigned":
		return assigned || prev, nil
	case "only_owner":
		return isAuthor, nil
	case "only_my_watches":
		return e.WatchedBy(ctx, iss, u)
	}
	return false, nil
}

// NotifiedUsers は Issue#notified_users (作成者・担当者・前担当者、プロジェクトの通知対象、高優先度。閲覧できるユーザのみ)。
func (e *Env) NotifiedUsers(ctx context.Context, iss *Issue) ([]*domain.User, error) {
	var base []int64
	for _, id := range []*int64{ptrIfNonZero(iss.AuthorID), iss.AssignedToID, iss.previousAssigneeID()} {
		if id != nil && !containsID(base, *id) {
			base = append(base, *id)
		}
	}
	ids, err := e.expandPrincipals(ctx, base, false)
	if err != nil {
		return nil, err
	}
	var notified []int64
	for _, id := range ids {
		u, err := e.UserByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if u == nil || !u.Active() {
			continue
		}
		ns, err := e.notificationSettings(ctx, id)
		if err != nil {
			return nil, err
		}
		ok, err := e.notifyAbout(ctx, u, ns, iss)
		if err != nil {
			return nil, err
		}
		if ok {
			notified = append(notified, id)
		}
	}
	// project.notified_users
	def := "only_my_events"
	if e.Settings != nil {
		def = e.Settings.String("default_notification_option")
	}
	var pu []int64
	if err := e.Q.Select(ctx, &pu, `SELECT m.principal_id FROM members m JOIN principals p ON p.id = m.principal_id
LEFT JOIN user_notification_settings ns ON ns.user_id = p.id
WHERE m.project_id = ? AND p.kind = 'user' AND p.status = 1 AND
(EXISTS (SELECT 1 FROM user_notified_projects unp WHERE unp.user_id = p.id AND unp.project_id = m.project_id)
 OR COALESCE(ns.mail_notification, ?) = 'all') ORDER BY m.id`, iss.ProjectID, def); err != nil {
		return nil, err
	}
	notified = append(notified, pu...)
	pri, err := e.PriorityOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	if high, err := e.PriorityHigh(ctx, pri); err != nil {
		return nil, err
	} else if high {
		var hp []int64
		if err := e.Q.Select(ctx, &hp, `SELECT m.principal_id FROM members m JOIN principals p ON p.id = m.principal_id
JOIN user_notification_settings ns ON ns.user_id = p.id
WHERE m.project_id = ? AND p.kind = 'user' AND p.status = 1 AND ns.notify_about_high_priority_issues = ? ORDER BY m.id`,
			iss.ProjectID, true); err != nil {
			return nil, err
		}
		notified = append(notified, hp...)
	}
	var out []*domain.User
	var seen []int64
	for _, id := range notified {
		if containsID(seen, id) {
			continue
		}
		seen = append(seen, id)
		u, err := e.UserByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if u == nil {
			continue
		}
		ok, err := e.Visible(ctx, iss, u)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, u)
		}
	}
	return out, nil
}

func ptrIfNonZero(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// NotifiedWatchers は notified_watchers (有効なウォッチャー、グループは有効な所属ユーザに展開、
// メールアドレスが無い・通知 none を除き、閲覧できるユーザのみ)。
func (e *Env) NotifiedWatchers(ctx context.Context, iss *Issue) ([]*domain.User, error) {
	ids, err := e.WatcherIDs(ctx, iss)
	if err != nil {
		return nil, err
	}
	var active []int64
	for _, id := range ids {
		p, err := e.Principal(ctx, id)
		if err != nil {
			return nil, err
		}
		if p != nil && p.Status == domain.StatusActive {
			active = append(active, id)
		}
	}
	exp, err := e.expandPrincipals(ctx, active, true)
	if err != nil {
		return nil, err
	}
	return e.filterMailable(ctx, iss, exp)
}

// filterMailable はメールアドレスが無い・通知 none のユーザと、チケットを閲覧できないユーザを除く。
func (e *Env) filterMailable(ctx context.Context, iss *Issue, ids []int64) ([]*domain.User, error) {
	var out []*domain.User
	for _, id := range ids {
		u, err := e.UserByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if u == nil || strings.TrimSpace(u.Mail) == "" {
			continue
		}
		ns, err := e.notificationSettings(ctx, id)
		if err != nil {
			return nil, err
		}
		if ns.MailNotification == "none" {
			continue
		}
		ok, err := e.Visible(ctx, iss, u)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// NotifiedMentions は notified_mentions (直前の保存で新たにメンションされたユーザ)。
func (e *Env) NotifiedMentions(ctx context.Context, iss *Issue) ([]*domain.User, error) {
	return e.filterMailable(ctx, iss, iss.mentionedUserIDs)
}

// mentionPattern は Redmine::Acts::Mentionable::MENTION_PATTERN。
var mentionPattern = regexp2.MustCompile(`(?:^|\W)@([A-Za-z0-9_\-@\.]*?)(?=(?=[\p{P}\p{S}][^A-Za-z0-9_\/])|\s|[\p{P}\p{S}]?$)`, regexp2.IgnoreCase|regexp2.Multiline)

var (
	quotedRe     = regexp.MustCompile(`(?s)\r\n(?:>\s)+(.*?)\r\n`)
	textilePreRe = regexp.MustCompile(`(?s)<pre>(.*?)</pre>`)
	cmCodeRe     = regexp.MustCompile("(?s)(~~~|```)(.*?)(~~~|```)")
)

// scanForMentionedUsers は scan_for_mentioned_users (ログイン名の一覧)。
func (e *Env) scanForMentionedUsers(content *string) []string {
	if content == nil {
		return nil
	}
	c := quotedRe.ReplaceAllString(*content, "")
	tf := ""
	if e.Settings != nil {
		tf = e.Settings.String("text_formatting")
	}
	switch tf {
	case "textile":
		c = textilePreRe.ReplaceAllString(c, "")
	case "common_mark":
		c = cmCodeRe.ReplaceAllString(c, "")
	}
	var out []string
	m, _ := mentionPattern.FindStringMatch(c)
	for m != nil {
		out = append(out, m.GroupByNumber(1).String())
		m, _ = mentionPattern.FindNextMatch(m)
	}
	return out
}

// mentionedUsers は get_mentioned_users(old, new): 新たに現れたログイン名の、User.current に見える有効なユーザ。
func (e *Env) mentionedUsers(ctx context.Context, old, cur *string) ([]int64, error) {
	prev := e.scanForMentionedUsers(old)
	var logins []string
	for _, l := range e.scanForMentionedUsers(cur) {
		if !slices.Contains(prev, l) {
			logins = append(logins, l)
		}
	}
	if len(logins) == 0 {
		return nil, nil
	}
	u, err := e.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	vis, err := e.authz(u).PrincipalVisibleCondition(ctx)
	if err != nil {
		return nil, err
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(logins)), ",")
	args := make([]any, len(logins))
	for i, l := range logins {
		args[i] = l
	}
	var ids []int64
	err = e.Q.Select(ctx, &ids, `SELECT principals.id FROM principals JOIN user_accounts ua ON ua.principal_id = principals.id
WHERE principals.kind = 'user' AND principals.status = 1 AND ua.login IN (`+ph+`) AND `+vis+` ORDER BY principals.id`, args...)
	return ids, err
}

// parseIssueMentions は parse_mentions (description が変わったときのみ)。
func (e *Env) parseIssueMentions(ctx context.Context, iss *Issue) error {
	if !iss.SavedChangeTo("description") {
		return nil
	}
	ids, err := e.mentionedUsers(ctx, iss.saved.Description, iss.Description)
	if err != nil {
		return err
	}
	iss.mentionedUserIDs = ids
	return nil
}

func notifiedEvents(e *Env) []string {
	if e.Settings == nil {
		return []string{"issue_added", "issue_updated"}
	}
	return e.Settings.Strings("notified_events")
}

func userIDs(us []*domain.User) []int64 {
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = u.ID
	}
	return out
}

func unionIDs(lists ...[]int64) []int64 {
	var out []int64
	for _, l := range lists {
		for _, id := range l {
			if !containsID(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// sendIssueAddNotification は send_notification (issue_added)。
func (e *Env) sendIssueAddNotification(ctx context.Context, iss *Issue, st *saveState) error {
	if !iss.Notify() || !slices.Contains(notifiedEvents(e), "issue_added") {
		return nil
	}
	nu, err := e.NotifiedUsers(ctx, iss)
	if err != nil {
		return err
	}
	nw, err := e.NotifiedWatchers(ctx, iss)
	if err != nil {
		return err
	}
	nm, err := e.NotifiedMentions(ctx, iss)
	if err != nil {
		return err
	}
	n := Notification{Event: NotifyIssueAdd, IssueID: iss.ID, AuthorID: iss.AuthorID,
		Recipients: unionIDs(userIDs(nu), userIDs(nw), userIDs(nm))}
	if err := e.excludeSelf(ctx, &n); err != nil {
		return err
	}
	st.result.Notifications = append(st.result.Notifications, n)
	return nil
}

// excludeSelf は作成者が no_self_notified なら受信者から除く (Mailer#mail)。
func (e *Env) excludeSelf(ctx context.Context, n *Notification) error {
	if n.AuthorID == 0 || !containsID(n.Recipients, n.AuthorID) {
		return nil
	}
	a, err := e.UserByID(ctx, n.AuthorID)
	if err != nil || a == nil || !a.Logged() {
		return err
	}
	ns, err := e.notificationSettings(ctx, a.ID)
	if err != nil {
		return err
	}
	if ns.NoSelfNotified {
		n.Recipients = slices.DeleteFunc(n.Recipients, func(id int64) bool { return id == n.AuthorID })
		n.SelfExcluded = true
	}
	return nil
}

// JournalNotifiedUsers は Journal#notified_users (非公開ノートなら view_private_notes を持つユーザのみ)。
func (e *Env) JournalNotifiedUsers(ctx context.Context, j *Journal, iss *Issue) ([]*domain.User, error) {
	us, err := e.NotifiedUsers(ctx, iss)
	if err != nil {
		return nil, err
	}
	return e.selectJournalVisibleUsers(ctx, j, iss, us)
}

// JournalNotifiedWatchers は Journal#notified_watchers。
func (e *Env) JournalNotifiedWatchers(ctx context.Context, j *Journal, iss *Issue) ([]*domain.User, error) {
	us, err := e.NotifiedWatchers(ctx, iss)
	if err != nil {
		return nil, err
	}
	return e.selectJournalVisibleUsers(ctx, j, iss, us)
}

func (e *Env) selectJournalVisibleUsers(ctx context.Context, j *Journal, iss *Issue, us []*domain.User) ([]*domain.User, error) {
	if !j.PrivateNotes {
		return us, nil
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	var out []*domain.User
	for _, u := range us {
		ok, err := e.allowedTo(ctx, u, "view_private_notes", p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// sendJournalNotification は Journal#send_notification (issue_edit)。
func (e *Env) sendJournalNotification(ctx context.Context, j *Journal, iss *Issue, st *saveState) error {
	if j.notifyOff {
		return nil
	}
	ev := notifiedEvents(e)
	has := func(s string) bool { return slices.Contains(ev, s) }
	attachmentAdded := false
	for _, d := range j.Details {
		if d.Property == "attachment" && d.Value != nil {
			attachmentAdded = true
		}
	}
	newStatus := false
	if v := j.NewValueFor("status_id"); v != nil {
		if s, err := e.Status(ctx, rubyToI(*v)); err != nil {
			return err
		} else if s != nil {
			newStatus = true
		}
	}
	if !(has("issue_updated") ||
		(has("issue_note_added") && strings.TrimSpace(j.Notes) != "") ||
		(has("issue_status_updated") && newStatus) ||
		(has("issue_assigned_to_updated") && j.DetailForAttribute("assigned_to_id") != nil) ||
		(has("issue_priority_updated") && j.NewValueFor("priority_id") != nil) ||
		(has("issue_fixed_version_updated") && j.DetailForAttribute("fixed_version_id") != nil) ||
		(has("issue_attachment_added") && attachmentAdded)) {
		return nil
	}
	nu, err := e.JournalNotifiedUsers(ctx, j, iss)
	if err != nil {
		return err
	}
	nw, err := e.JournalNotifiedWatchers(ctx, j, iss)
	if err != nil {
		return err
	}
	jm, err := e.filterMailable(ctx, iss, e.journalMentions(ctx, j))
	if err != nil {
		return err
	}
	jm, err = e.selectJournalVisibleUsers(ctx, j, iss, jm)
	if err != nil {
		return err
	}
	im, err := e.NotifiedMentions(ctx, iss)
	if err != nil {
		return err
	}
	all := unionIDs(userIDs(nu), userIDs(nw), userIDs(jm), userIDs(im))
	var recips []int64
	for _, id := range all {
		if j.HasNotes() {
			recips = append(recips, id)
			continue
		}
		u, err := e.UserByID(ctx, id)
		if err != nil {
			return err
		}
		vd, err := e.VisibleDetails(ctx, j, iss, u)
		if err != nil {
			return err
		}
		if len(vd) > 0 {
			recips = append(recips, id)
		}
	}
	n := Notification{Event: NotifyIssueEdit, IssueID: iss.ID, JournalID: j.ID, AuthorID: j.UserID, Recipients: recips}
	if err := e.excludeSelf(ctx, &n); err != nil {
		return err
	}
	st.result.Notifications = append(st.result.Notifications, n)
	return nil
}

// journalMentions は Journal の parse_mentions (作成時のノート)。
func (e *Env) journalMentions(ctx context.Context, j *Journal) []int64 {
	if strings.TrimSpace(j.Notes) == "" {
		return nil
	}
	n := j.Notes
	ids, err := e.mentionedUsers(ctx, nil, &n)
	if err != nil {
		return nil
	}
	return ids
}
