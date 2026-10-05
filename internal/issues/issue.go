// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// NewBlank は Issue.new (優先度の既定値とウォッチャー空のみ設定した新規チケット)。
func (e *Env) NewBlank(ctx context.Context) (*Issue, error) {
	iss := &Issue{}
	p, err := e.DefaultPriority(ctx)
	if err != nil {
		return nil, err
	}
	if p != nil {
		iss.PriorityID = p.ID
	}
	return iss, nil
}

// New は新規チケットを既定値付きで作る (IssuesController#build_new_issue_from_params の既定値部分)。
//   - 優先度: IssuePriority.default
//   - project= (既定バージョンの設定を含む)
//   - トラッカー: trackerID (0 ならユーザが利用できる先頭のトラッカー) と、その既定ステータス
//   - 作成者: author (nil なら User.current)
//   - 開始日: Setting.default_issue_start_date_to_creation_date なら今日
//   - 期日: Setting.default_issue_due_date_offset があれば今日 + オフセット日数
//
// カテゴリ・プロジェクトの既定担当者は保存時 (default_assign) に設定される。
func (e *Env) New(ctx context.Context, p *domain.Project, trackerID int64, author *domain.User) (*Issue, error) {
	iss, err := e.NewBlank(ctx)
	if err != nil {
		return nil, err
	}
	if p != nil {
		if err := e.SetProject(ctx, iss, p, false); err != nil {
			return nil, err
		}
	}
	if author == nil {
		if author, err = e.currentUser(ctx); err != nil {
			return nil, err
		}
	}
	iss.AuthorID = author.ID
	if e.Settings != nil && e.Settings.Bool("default_issue_start_date_to_creation_date") {
		iss.StartDate = ptrTime(e.today())
	}
	if e.Settings != nil {
		if days, ok := e.Settings.DefaultIssueDueDateOffsetInDays(); ok {
			iss.DueDate = ptrTime(e.today().AddDate(0, 0, days))
		}
	}
	if trackerID != 0 {
		if err := e.SetTrackerID(ctx, iss, trackerID); err != nil {
			return nil, err
		}
	} else if p != nil {
		ts, err := e.AllowedTargetTrackers(ctx, iss, author)
		if err != nil {
			return nil, err
		}
		if len(ts) > 0 {
			if err := e.SetTracker(ctx, iss, ts[0]); err != nil {
				return nil, err
			}
		}
	}
	return iss, nil
}

// ---------------------------------------------------------------- セッター (副作用付き)

// SetStatusID は status_id= (値が変わるときだけ status を引き直す。存在しなければ nil)。
func (e *Env) SetStatusID(ctx context.Context, iss *Issue, id int64) error {
	if id == iss.StatusID {
		return nil
	}
	s, err := e.Status(ctx, id)
	if err != nil {
		return err
	}
	if s == nil {
		iss.StatusID = 0
	} else {
		iss.StatusID = s.ID
	}
	return nil
}

// SetTrackerID は tracker_id= (存在しない id なら tracker = nil)。
func (e *Env) SetTrackerID(ctx context.Context, iss *Issue, id int64) error {
	if id == iss.TrackerID {
		return nil
	}
	t, err := e.Tracker(ctx, id)
	if err != nil {
		return err
	}
	return e.SetTracker(ctx, iss, t)
}

// SetTracker は tracker= (ステータスの付け替えとカスタムフィールド値の再割当てを行う)。
func (e *Env) SetTracker(ctx context.Context, iss *Issue, t *domain.Tracker) error {
	was, err := e.TrackerOf(ctx, iss)
	if err != nil {
		return err
	}
	if t == nil {
		iss.TrackerID = 0
	} else {
		iss.TrackerID = t.ID
	}
	if !sameTracker(t, was) {
		switch {
		case was != nil && iss.StatusID == was.DefaultStatusID:
			iss.StatusID = 0
		case iss.StatusID != 0 && t != nil:
			ids, err := e.trackerIssueStatusIDs(ctx, t.ID)
			if err != nil {
				return err
			}
			if !containsID(ids, iss.StatusID) {
				iss.StatusID = 0
			}
		}
		if err := e.reassignCustomFieldValues(ctx, iss); err != nil {
			return err
		}
	}
	if iss.StatusID == 0 {
		ds, err := e.DefaultStatus(ctx, iss)
		if err != nil {
			return err
		}
		if ds != nil {
			iss.StatusID = ds.ID
		}
	}
	return nil
}

func sameTracker(a, b *domain.Tracker) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// SetProjectID は project_id= (存在しない id なら project = nil)。
func (e *Env) SetProjectID(ctx context.Context, iss *Issue, id int64) error {
	if id == iss.ProjectID {
		return nil
	}
	p, err := e.Project(ctx, id)
	if err != nil {
		return err
	}
	return e.SetProject(ctx, iss, p, false)
}

// SetProject は project=(project, keep_tracker)。
func (e *Env) SetProject(ctx context.Context, iss *Issue, p *domain.Project, keepTracker bool) error {
	was, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return err
	}
	if p == nil {
		iss.ProjectID = 0
	} else {
		iss.ProjectID = p.ID
	}
	if was != nil && p != nil && was.ID != p.ID {
		trackers, err := e.ProjectTrackers(ctx, p)
		if err != nil {
			return err
		}
		if !keepTracker && !containsTracker(trackers, iss.TrackerID) {
			var first *domain.Tracker
			if len(trackers) > 0 {
				first = trackers[0]
			}
			if err := e.SetTracker(ctx, iss, first); err != nil {
				return err
			}
		}
		if iss.CategoryID != nil {
			cat, err := e.Category(ctx, iss.CategoryID)
			if err != nil {
				return err
			}
			iss.CategoryID = nil
			if cat != nil {
				var id int64
				err := e.Q.Get(ctx, &id, `SELECT id FROM issue_categories WHERE project_id = ? AND name = ? ORDER BY id LIMIT 1`, p.ID, cat.Name)
				if err == nil {
					iss.CategoryID = &id
				} else if !isNoRows(err) {
					return err
				}
			}
		}
		if iss.NewRecord() && iss.AssignedToID != nil {
			users, err := e.AssignableUsers(ctx, iss)
			if err != nil {
				return err
			}
			if !containsPrincipal(users, *iss.AssignedToID) {
				iss.AssignedToID = nil
			}
		}
		if iss.FixedVersionID != nil {
			v, err := e.Version(ctx, iss.FixedVersionID)
			if err != nil {
				return err
			}
			if v != nil && v.ProjectID != p.ID {
				shared, err := e.sharedVersionIDs(ctx, p)
				if err != nil {
					return err
				}
				if !containsID(shared, v.ID) {
					iss.FixedVersionID = nil
				}
			} else if v == nil {
				iss.FixedVersionID = nil
			}
		}
		parent, err := e.parentOf(ctx, iss)
		if err != nil {
			return err
		}
		ok, err := e.ValidParentProject(ctx, iss, parent)
		if err != nil {
			return err
		}
		if !ok {
			_ = e.SetParentIssueID(ctx, iss, "") // 空文字は常に成功する
		}
		if err := e.reassignCustomFieldValues(ctx, iss); err != nil {
			return err
		}
	}
	if iss.NewRecord() && iss.FixedVersionID == nil && p != nil && p.DefaultVersionID != nil {
		var n int
		if err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM versions JOIN projects ON projects.id = versions.project_id
WHERE versions.status = 'open' AND versions.id = ? AND `+repository.SharedVersionsCondition(p), *p.DefaultVersionID); err != nil {
			return err
		}
		if n > 0 {
			iss.FixedVersionID = ptrInt64(*p.DefaultVersionID)
		}
	}
	return nil
}

func containsTracker(ts []*domain.Tracker, id int64) bool {
	for _, t := range ts {
		if t.ID == id {
			return true
		}
	}
	return false
}

var newlineRe = regexp.MustCompile(`\r\n|\n|\r`)

// SetDescription は description= (改行を CRLF に正規化する)。
func (iss *Issue) SetDescription(s *string) {
	if s == nil {
		iss.Description = nil
		return
	}
	v := newlineRe.ReplaceAllString(*s, "\r\n")
	iss.Description = &v
}

// SetEstimatedHoursString は estimated_hours= に文字列を代入する (String#to_hours で解釈)。
func (iss *Issue) SetEstimatedHoursString(s string) {
	iss.estimatedHoursRaw = ""
	if strings.TrimSpace(s) == "" {
		iss.EstimatedHours = nil
		return
	}
	if f, ok := ToHours(s); ok {
		iss.EstimatedHours = &f
		return
	}
	// 解釈できない文字列は数値検証で invalid になる (Rails の数値キャストは nil)
	iss.EstimatedHours = nil
	iss.estimatedHoursRaw = s
}

// SetEstimatedHours は estimated_hours= (数値)。
func (iss *Issue) SetEstimatedHours(f *float64) {
	iss.estimatedHoursRaw = ""
	iss.EstimatedHours = f
}

// SetStartDateString は start_date= に文字列を代入する (不正な日付は検証エラーになる)。
func (iss *Issue) SetStartDateString(s string) {
	iss.StartDate, iss.startDateRaw = parseDateInput(s)
}

// SetDueDateString は due_date= に文字列を代入する。
func (iss *Issue) SetDueDateString(s string) {
	iss.DueDate, iss.dueDateRaw = parseDateInput(s)
}

// SetStartDate は start_date= (日付)。
func (iss *Issue) SetStartDate(t *time.Time) {
	iss.startDateRaw = ""
	if t != nil {
		d := dateOnly(*t)
		t = &d
	}
	iss.StartDate = t
}

// SetDueDate は due_date= (日付)。
func (iss *Issue) SetDueDate(t *time.Time) {
	iss.dueDateRaw = ""
	if t != nil {
		d := dateOnly(*t)
		t = &d
	}
	iss.DueDate = t
}

var parentIDRe = regexp.MustCompile(`^#?(\d+)$`)

// SetParentIssueID は parent_issue_id= ("12" / "#12" / "" を受け付ける。存在しない id は不正値として保持)。
func (e *Env) SetParentIssueID(ctx context.Context, iss *Issue, arg string) error {
	s := strings.TrimSpace(arg)
	iss.parentIssueSet = true
	if s == "" {
		iss.parentIssue = nil
		iss.invalidParentIssueID = ""
		return nil
	}
	if m := parentIDRe.FindStringSubmatch(s); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		p, err := e.Find(ctx, id)
		if err != nil {
			return err
		}
		if p != nil {
			iss.parentIssue = p
			iss.invalidParentIssueID = ""
			return nil
		}
	}
	iss.parentIssue = nil
	iss.invalidParentIssueID = arg
	return nil
}

// SetParentIssue は parent_issue_id= にチケットを直接指定する (nil で親を外す)。
func (iss *Issue) SetParentIssue(p *Issue) {
	iss.parentIssueSet = true
	iss.parentIssue = p
	iss.invalidParentIssueID = ""
}

// ParentIssueID は parent_issue_id (不正値の場合はその文字列、未設定は "")。
func (iss *Issue) ParentIssueID() string {
	if iss.invalidParentIssueID != "" {
		return iss.invalidParentIssueID
	}
	if iss.parentIssueSet {
		if iss.parentIssue == nil {
			return ""
		}
		return strconv.FormatInt(iss.parentIssue.ID, 10)
	}
	if iss.ParentID != nil {
		return strconv.FormatInt(*iss.ParentID, 10)
	}
	return ""
}

// parentIssueIDValue は parent_issue_id の数値 (未設定・不正なら nil)。
func (iss *Issue) parentIssueIDValue() *int64 {
	if iss.invalidParentIssueID != "" {
		return nil
	}
	if iss.parentIssueSet {
		if iss.parentIssue == nil {
			return nil
		}
		return ptrInt64(iss.parentIssue.ID)
	}
	return iss.ParentID
}

// parentOf は issue.parent (parent_id のチケット)。
func (e *Env) parentOf(ctx context.Context, iss *Issue) (*Issue, error) {
	if iss.ParentID == nil {
		return nil, nil
	}
	return e.Find(ctx, *iss.ParentID)
}

// ---------------------------------------------------------------- 状態の判定

// Closed は closed?。
func (e *Env) Closed(ctx context.Context, iss *Issue) (bool, error) {
	s, err := e.StatusOf(ctx, iss)
	if err != nil || s == nil {
		return false, err
	}
	return s.IsClosed, nil
}

// WasClosed は was_closed?。
func (e *Env) WasClosed(ctx context.Context, iss *Issue) (bool, error) {
	s, err := e.StatusWas(ctx, iss)
	if err != nil || s == nil {
		return false, err
	}
	return s.IsClosed, nil
}

// Reopening は reopening? / reopened?。
func (e *Env) Reopening(ctx context.Context, iss *Issue) (bool, error) {
	if iss.NewRecord() || !iss.AttrChanged("status_id") {
		return false, nil
	}
	c, err := e.Closed(ctx, iss)
	if err != nil || c {
		return false, err
	}
	return e.WasClosed(ctx, iss)
}

// Closing は closing?。
func (e *Env) Closing(ctx context.Context, iss *Issue) (bool, error) {
	if iss.NewRecord() {
		return e.Closed(ctx, iss)
	}
	if !iss.AttrChanged("status_id") {
		return false, nil
	}
	c, err := e.Closed(ctx, iss)
	if err != nil || !c {
		return false, err
	}
	w, err := e.WasClosed(ctx, iss)
	return !w, err
}

// DoneRatio は done_ratio (ステータスで進捗を決める設定ならステータスの既定進捗率)。
func (e *Env) DoneRatio(ctx context.Context, iss *Issue) (int, error) {
	if e.useStatusForDoneRatio() {
		s, err := e.StatusOf(ctx, iss)
		if err != nil {
			return 0, err
		}
		if s != nil && s.DefaultDoneRatio != nil {
			return *s.DefaultDoneRatio, nil
		}
	}
	return iss.DoneRatio, nil
}

func (e *Env) useStatusForDoneRatio() bool {
	return e.Settings != nil && e.Settings.String("issue_done_ratio") == "issue_status"
}

// Overdue は overdue?。
func (e *Env) Overdue(ctx context.Context, iss *Issue) (bool, error) {
	if iss.DueDate == nil || !iss.DueDate.Before(e.today()) {
		return false, nil
	}
	c, err := e.Closed(ctx, iss)
	return !c, err
}

// BehindSchedule は behind_schedule?。
func (e *Env) BehindSchedule(ctx context.Context, iss *Issue) (bool, error) {
	if iss.StartDate == nil || iss.DueDate == nil {
		return false, nil
	}
	dr, err := e.DoneRatio(ctx, iss)
	if err != nil {
		return false, err
	}
	days := int(iss.DueDate.Sub(*iss.StartDate).Hours()/24) + 1
	// Ruby は Rational (日数) * done_ratio / 100 の floor
	n := days * dr
	off := n / 100
	if n < 0 && n%100 != 0 {
		off--
	}
	done := iss.StartDate.AddDate(0, 0, off)
	return !done.After(e.today()), nil
}

// Duration は duration (日数)。
func (iss *Issue) Duration() int {
	if iss.StartDate == nil || iss.DueDate == nil {
		return 0
	}
	return int(iss.DueDate.Sub(*iss.StartDate).Hours() / 24)
}

// WorkingDuration は working_duration。
func (e *Env) WorkingDuration(iss *Issue) int {
	if iss.StartDate == nil || iss.DueDate == nil {
		return 0
	}
	return e.WorkingDays(*iss.StartDate, *iss.DueDate)
}

// DisabledCoreFields は disabled_core_fields。
func (e *Env) DisabledCoreFields(ctx context.Context, iss *Issue) ([]string, error) {
	t, err := e.TrackerOf(ctx, iss)
	if err != nil || t == nil {
		return nil, err
	}
	return t.DisabledCoreFields, nil
}

// JournalizedAttributeNames は journalized_attribute_names。
func (e *Env) JournalizedAttributeNames(ctx context.Context, iss *Issue) ([]string, error) {
	dis, err := e.DisabledCoreFields(ctx, iss)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range journalizedColumns {
		if !containsString(dis, a) {
			out = append(out, a)
		}
	}
	return out, nil
}

func containsString(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

// DatesDerived は dates_derived?。
func (e *Env) DatesDerived(ctx context.Context, iss *Issue) (bool, error) {
	return e.derived(ctx, iss, "parent_issue_dates")
}

// PriorityDerived は priority_derived?。
func (e *Env) PriorityDerived(ctx context.Context, iss *Issue) (bool, error) {
	return e.derived(ctx, iss, "parent_issue_priority")
}

// DoneRatioDerived は done_ratio_derived?。
func (e *Env) DoneRatioDerived(ctx context.Context, iss *Issue) (bool, error) {
	return e.derived(ctx, iss, "parent_issue_done_ratio")
}

func (e *Env) derived(ctx context.Context, iss *Issue, setting string) (bool, error) {
	leaf, err := e.Leaf(ctx, iss)
	if err != nil || leaf {
		return false, err
	}
	v := "derived"
	if e.Settings != nil {
		v = e.Settings.String(setting)
	}
	return v == "derived", nil
}

// ---------------------------------------------------------------- 権限

// AttributesEditable は attributes_editable?(user)。
func (e *Env) AttributesEditable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	ok, err := e.userTrackerPermission(ctx, iss, u, "edit_issues")
	if err != nil || ok {
		return ok, err
	}
	ok, err = e.userTrackerPermission(ctx, iss, u, "edit_own_issues")
	return ok && iss.AuthorID == u.ID && u.ID != 0, err
}

// Editable は editable?(user)。
func (e *Env) Editable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	ok, err := e.AttributesEditable(ctx, iss, u)
	if err != nil || ok {
		return ok, err
	}
	return e.NotesAddable(ctx, iss, u)
}

// NotesAddable は notes_addable?(user)。
func (e *Env) NotesAddable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	return e.userTrackerPermission(ctx, iss, u, "add_issue_notes")
}

// Deletable は deletable?(user)。
func (e *Env) Deletable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	return e.userTrackerPermission(ctx, iss, u, "delete_issues")
}

// AttachmentsAddable は attachments_addable?(user)。
func (e *Env) AttachmentsAddable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	return e.Editable(ctx, iss, u)
}

// AttachmentsEditable は attachments_editable?(user) / attachments_deletable?(user)。
func (e *Env) AttachmentsEditable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	v, err := e.Visible(ctx, iss, u)
	if err != nil || !v {
		return false, err
	}
	return e.AttributesEditable(ctx, iss, u)
}

// AttachmentsDeletable は attachments_deletable?(user)。
func (e *Env) AttachmentsDeletable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	return e.AttachmentsEditable(ctx, iss, u)
}

// TimeLoggable は time_loggable?(user)。
func (e *Env) TimeLoggable(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return false, err
	}
	ok, err := e.allowedTo(ctx, u, "log_time", p)
	if err != nil || !ok {
		return false, err
	}
	if e.Settings == nil || e.Settings.Bool("timelog_accept_closed_issues") {
		return true, nil
	}
	c, err := e.Closed(ctx, iss)
	return !c, err
}

func (e *Env) userTrackerPermission(ctx context.Context, iss *Issue, u *domain.User, perm string) (bool, error) {
	if u == nil {
		return false, nil
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return false, err
	}
	return e.authz(u).UserTrackerPermission(ctx, p, iss.TrackerID, perm)
}

// Visible は visible?(user)。
func (e *Env) Visible(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return false, err
		}
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return false, err
	}
	if p == nil {
		return false, nil
	}
	return e.authz(u).IssueVisible(ctx, &iss.Issue, p)
}

// CSSClasses は css_classes(user)。
func (e *Env) CSSClasses(ctx context.Context, iss *Issue, u *domain.User) (string, error) {
	pri, err := e.PriorityOf(ctx, iss)
	if err != nil {
		return "", err
	}
	pc := ""
	if pri != nil {
		pn := ""
		if pri.PositionName != nil {
			pn = *pri.PositionName
		}
		pc = "priority-" + strconv.FormatInt(pri.ID, 10) + " priority-" + pn
	}
	s := "issue tracker-" + strconv.FormatInt(iss.TrackerID, 10) + " status-" + strconv.FormatInt(iss.StatusID, 10) + " " + pc
	if c, err := e.Closed(ctx, iss); err != nil {
		return "", err
	} else if c {
		s += " closed"
	}
	if o, err := e.Overdue(ctx, iss); err != nil {
		return "", err
	} else if o {
		s += " overdue"
	}
	if iss.ParentID != nil {
		s += " child"
	}
	if leaf, err := e.Leaf(ctx, iss); err != nil {
		return "", err
	} else if !leaf {
		s += " parent"
	}
	if iss.IsPrivate {
		s += " private"
	}
	if b, err := e.BehindSchedule(ctx, iss); err != nil {
		return "", err
	} else if b {
		s += " behind-schedule"
	}
	if u != nil && u.Logged() {
		if iss.AuthorID == u.ID {
			s += " created-by-me"
		}
		if iss.AssignedToID != nil && *iss.AssignedToID == u.ID {
			s += " assigned-to-me"
		}
		gids, err := e.groupIDs(ctx, u)
		if err != nil {
			return "", err
		}
		if iss.AssignedToID != nil && containsID(gids, *iss.AssignedToID) {
			s += " assigned-to-my-group"
		}
	}
	return s, nil
}

// String は to_s ("Tracker #id: subject")。
func (e *Env) String(ctx context.Context, iss *Issue) string {
	t, _ := e.TrackerOf(ctx, iss)
	name := ""
	if t != nil {
		name = t.Name
	}
	return name + " #" + strconv.FormatInt(iss.ID, 10) + ": " + iss.Subject
}
