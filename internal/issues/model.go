// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// Issue は編集中のチケット (Redmine の Issue インスタンス)。
//
// 埋め込みの domain.Issue が現在値 (属性)。orig は DB 上の値 (Rails の *_in_database / *_was)
// で、新規レコードでは nil。保存後は saved に直前の保存での変更前の値が入る
// (saved_change_to_* / *_before_last_save)。
type Issue struct {
	domain.Issue

	orig  *domain.Issue
	saved *domain.Issue // 直前の保存前の値 (保存していなければ nil)
	// savedNew は直前の保存が INSERT だったか (saved_change_to_id?)。
	savedNew bool

	// 不正な入力値 (validates :date / numericality 用の before_type_cast)。
	startDateRaw      string
	dueDateRaw        string
	estimatedHoursRaw string

	// parent_issue_id= の状態 (@parent_issue / @invalid_parent_issue_id)。
	parentIssueSet       bool
	parentIssue          *Issue
	invalidParentIssueID string

	// カスタムフィールド値。cfv は custom_field_values (遅延計算)、cvRows は DB の custom_values。
	cfv        []*CustomFieldValue
	cvRows     []cvRow
	cfvChanged bool
	// builtValues は行の無いフィールドについて組み立てた CustomValue の値 (custom_values.build、既定値か nil)。
	builtValues map[int64]CFValue

	currentJournal  *Journal
	attributesSetBy *domain.User
	notifyOff       bool

	// 新規作成時のウォッチャー (watcher_user_ids=)。
	watcherUserIDs []int64
	// 保存時に添付する既存添付ファイル (saved_attachments) と削除する添付ファイル。
	attachIDs            []int64
	deletedAttachmentIDs []int64

	// コピー元 (copy_from)。
	copiedFrom             *Issue
	copyOptions            CopyOptions
	copiedAttachments      []int64
	afterCreateFromCopyRun bool

	// TransitionWarning は new_statuses_allowed_to で状態遷移が制限された理由 (i18n キー)。
	TransitionWarning string

	// Errors は直近の Validate / Save の検証エラー。
	Errors domain.ValidationErrors

	// mentionedUserIDs は直前の保存で新たにメンションされたユーザ。
	mentionedUserIDs []int64

	// soonestStartStub はテスト用に soonest_start を固定する (issue.stubs(:soonest_start))。
	soonestStartStub *time.Time
}

// NewRecord は new_record?。
func (i *Issue) NewRecord() bool { return i.orig == nil }

// Persisted は persisted?。
func (i *Issue) Persisted() bool { return i.orig != nil }

// Orig は DB 上の値 (新規なら nil)。
func (i *Issue) Orig() *domain.Issue { return i.orig }

// CurrentJournal は current_journal。
func (i *Issue) CurrentJournal() *Journal { return i.currentJournal }

// ClearJournal は clear_journal。
func (i *Issue) ClearJournal() { i.currentJournal = nil }

// SetNotify は notify=。
func (i *Issue) SetNotify(v bool) { i.notifyOff = !v }

// Notify は notify?。
func (i *Issue) Notify() bool { return !i.notifyOff }

// CopiedFrom はコピー元 (copy? でなければ nil)。
func (i *Issue) CopiedFrom() *Issue { return i.copiedFrom }

// IsCopy は copy?。
func (i *Issue) IsCopy() bool { return i.copiedFrom != nil }

// WatcherUserIDs は新規チケットに設定するウォッチャー (watcher_user_ids)。
func (i *Issue) WatcherUserIDs() []int64 { return i.watcherUserIDs }

// SetWatcherUserIDs は watcher_user_ids= (新規チケットのみ。重複除去)。
func (i *Issue) SetWatcherUserIDs(ids []int64) {
	var out []int64
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	i.watcherUserIDs = out
}

// AttachSaved は保存時にコンテナとして紐付ける既存 (アップロード済み) の添付ファイルを登録する
// (save_attachments の結果 saved_attachments)。
func (i *Issue) AttachSaved(attachmentIDs ...int64) {
	i.attachIDs = append(i.attachIDs, attachmentIDs...)
}

// SetDeletedAttachmentIDs は deleted_attachment_ids=。
func (i *Issue) SetDeletedAttachmentIDs(ids []int64) { i.deletedAttachmentIDs = ids }

// SavedChangeTo は saved_change_to_<attr>? (直前の保存でその列が変わったか)。
func (i *Issue) SavedChangeTo(attr string) bool {
	if i.saved == nil {
		return false
	}
	if attr == "id" {
		return i.savedNew
	}
	return !attrEqual(attr, i.saved, &i.Issue)
}

// BeforeLastSave は直前の保存前の値 (*_before_last_save)。保存していなければ nil。
func (i *Issue) BeforeLastSave() *domain.Issue { return i.saved }

// Changed は changed? (DB 上の値と異なる列があるか)。新規は常に true。
func (i *Issue) Changed() bool {
	if i.orig == nil {
		return true
	}
	return len(i.ChangedAttributes()) > 0
}

// ChangedAttributes は changed (DB 上の値と異なる列名)。
func (i *Issue) ChangedAttributes() []string {
	var out []string
	for _, a := range allColumns {
		if i.AttrChanged(a) {
			out = append(out, a)
		}
	}
	return out
}

// AttrChanged は <attr>_changed?。
func (i *Issue) AttrChanged(attr string) bool {
	if i.orig == nil {
		var zero domain.Issue
		return !attrEqual(attr, &zero, &i.Issue)
	}
	return !attrEqual(attr, i.orig, &i.Issue)
}

// allColumns は issues の列 (Redmine の Issue.column_names の順。lft/rgt の代わりに hier_path)。
var allColumns = []string{"id", "tracker_id", "project_id", "subject", "description", "due_date", "category_id",
	"status_id", "assigned_to_id", "priority_id", "fixed_version_id", "author_id", "lock_version", "created_on",
	"updated_on", "start_date", "done_ratio", "estimated_hours", "parent_id", "root_id", "hier_path", "is_private", "closed_on"}

// journalizedColumns は journalized_attribute_names の候補 (列順)。
var journalizedColumns = []string{"tracker_id", "project_id", "subject", "description", "due_date", "category_id",
	"status_id", "assigned_to_id", "priority_id", "fixed_version_id", "author_id", "start_date", "done_ratio",
	"estimated_hours", "parent_id", "is_private"}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func eqTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// attrEqual は 2 つの Issue の列 attr が等しいか。
func attrEqual(attr string, a, b *domain.Issue) bool {
	switch attr {
	case "id":
		return a.ID == b.ID
	case "tracker_id":
		return a.TrackerID == b.TrackerID
	case "project_id":
		return a.ProjectID == b.ProjectID
	case "subject":
		return a.Subject == b.Subject
	case "description":
		return eqPtr(a.Description, b.Description)
	case "due_date":
		return eqTime(a.DueDate, b.DueDate)
	case "category_id":
		return eqPtr(a.CategoryID, b.CategoryID)
	case "status_id":
		return a.StatusID == b.StatusID
	case "assigned_to_id":
		return eqPtr(a.AssignedToID, b.AssignedToID)
	case "priority_id":
		return a.PriorityID == b.PriorityID
	case "fixed_version_id":
		return eqPtr(a.FixedVersionID, b.FixedVersionID)
	case "author_id":
		return a.AuthorID == b.AuthorID
	case "lock_version":
		return a.LockVersion == b.LockVersion
	case "created_on":
		return a.CreatedAt.Equal(b.CreatedAt)
	case "updated_on":
		return a.UpdatedAt.Equal(b.UpdatedAt)
	case "start_date":
		return eqTime(a.StartDate, b.StartDate)
	case "done_ratio":
		return a.DoneRatio == b.DoneRatio
	case "estimated_hours":
		return eqPtr(a.EstimatedHours, b.EstimatedHours)
	case "parent_id":
		return eqPtr(a.ParentID, b.ParentID)
	case "root_id":
		return a.RootID == b.RootID
	case "hier_path":
		return a.HierPath == b.HierPath
	case "is_private":
		return a.IsPrivate == b.IsPrivate
	case "closed_on":
		return eqTime(a.ClosedAt, b.ClosedAt)
	}
	return true
}

// ---------------------------------------------------------------- カスタムフィールド値

// CFValue は Ruby の custom_field_value.value (nil / String / Array)。
type CFValue struct {
	isArray bool
	s       *string
	a       []string
}

// NilValue は nil。
func NilValue() CFValue { return CFValue{} }

// StrValue は文字列値。
func StrValue(s string) CFValue { return CFValue{s: &s} }

// ArrayValue は配列値。
func ArrayValue(a ...string) CFValue { return CFValue{isArray: true, a: slices.Clone(a)} }

// IsArray は値が配列か。
func (v CFValue) IsArray() bool { return v.isArray }

// IsNil は値が nil か。
func (v CFValue) IsNil() bool { return !v.isArray && v.s == nil }

// String は to_s (配列は先頭要素、nil は "")。
func (v CFValue) String() string {
	if v.isArray {
		if len(v.a) == 0 {
			return ""
		}
		return v.a[0]
	}
	if v.s == nil {
		return ""
	}
	return *v.s
}

// Strings は Array.wrap(value) (nil は空)。
func (v CFValue) Strings() []string {
	if v.isArray {
		return slices.Clone(v.a)
	}
	if v.s == nil {
		return nil
	}
	return []string{*v.s}
}

// Blank は blank? (配列は要素がすべて空でも false: Ruby の Array#blank? は empty? のみ)。
func (v CFValue) Blank() bool {
	if v.isArray {
		return len(v.a) == 0
	}
	return v.s == nil || strings.TrimSpace(*v.s) == ""
}

// Present は value_present? (配列はいずれかが空でない)。
func (v CFValue) Present() bool {
	if v.isArray {
		return slices.ContainsFunc(v.a, func(s string) bool { return strings.TrimSpace(s) != "" })
	}
	return !v.Blank()
}

// Equal は Ruby の ==。
func (v CFValue) Equal(o CFValue) bool {
	if v.isArray != o.isArray {
		return false
	}
	if v.isArray {
		return slices.Equal(v.a, o.a)
	}
	return eqPtr(v.s, o.s)
}

func (v CFValue) clone() CFValue {
	if v.isArray {
		return ArrayValue(v.a...)
	}
	if v.s == nil {
		return CFValue{}
	}
	return StrValue(*v.s)
}

// CustomFieldValue は CustomFieldValue (カスタムフィールドと値の組)。
type CustomFieldValue struct {
	Field    *customfield.CustomField
	Value    CFValue
	ValueWas CFValue
}

// CustomFieldID は custom_field_id。
func (c *CustomFieldValue) CustomFieldID() int64 { return c.Field.ID }

// cvRow は custom_values の 1 行。
type cvRow struct {
	ID      int64          `db:"id"`
	FieldID int64          `db:"custom_field_id"`
	Value   sql.NullString `db:"value"`
}

// ---------------------------------------------------------------- 読み込み

type issueRow struct {
	ID             int64           `db:"id"`
	ProjectID      int64           `db:"project_id"`
	TrackerID      int64           `db:"tracker_id"`
	StatusID       int64           `db:"status_id"`
	PriorityID     int64           `db:"priority_id"`
	AuthorID       int64           `db:"author_id"`
	AssignedToID   sql.NullInt64   `db:"assigned_to_id"`
	CategoryID     sql.NullInt64   `db:"category_id"`
	FixedVersionID sql.NullInt64   `db:"fixed_version_id"`
	ParentID       sql.NullInt64   `db:"parent_id"`
	RootID         int64           `db:"root_id"`
	HierPath       string          `db:"hier_path"`
	Subject        string          `db:"subject"`
	Description    sql.NullString  `db:"description"`
	StartDate      db.NullDate     `db:"start_date"`
	DueDate        db.NullDate     `db:"due_date"`
	DoneRatio      int             `db:"done_ratio"`
	EstimatedHours sql.NullFloat64 `db:"estimated_hours"`
	IsPrivate      bool            `db:"is_private"`
	LockVersion    int             `db:"lock_version"`
	CreatedAt      db.Time         `db:"created_at"`
	UpdatedAt      db.Time         `db:"updated_at"`
	ClosedAt       db.NullTime     `db:"closed_at"`
}

const issueCols = `issues.id, issues.project_id, issues.tracker_id, issues.status_id, issues.priority_id, issues.author_id,
  issues.assigned_to_id, issues.category_id, issues.fixed_version_id, issues.parent_id, issues.root_id, issues.hier_path,
  issues.subject, issues.description, issues.start_date, issues.due_date, issues.done_ratio, issues.estimated_hours,
  issues.is_private, issues.lock_version, issues.created_at, issues.updated_at, issues.closed_at`

func nullID(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func nullDate(n db.NullDate) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Date.Time
	return &t
}

func (r *issueRow) issue() domain.Issue {
	is := domain.Issue{
		ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID, StatusID: r.StatusID, PriorityID: r.PriorityID,
		AuthorID: r.AuthorID, AssignedToID: nullID(r.AssignedToID), CategoryID: nullID(r.CategoryID),
		FixedVersionID: nullID(r.FixedVersionID), ParentID: nullID(r.ParentID), RootID: r.RootID, HierPath: r.HierPath,
		Subject: r.Subject, StartDate: nullDate(r.StartDate), DueDate: nullDate(r.DueDate), DoneRatio: r.DoneRatio,
		IsPrivate: r.IsPrivate, LockVersion: r.LockVersion, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
		ClosedAt: r.ClosedAt.Ptr(),
	}
	if r.Description.Valid {
		s := r.Description.String
		is.Description = &s
	}
	if r.EstimatedHours.Valid {
		f := r.EstimatedHours.Float64
		is.EstimatedHours = &f
	}
	return is
}

// Find は id のチケットを読み込む (Issue.find_by_id。無ければ nil, nil)。
func (e *Env) Find(ctx context.Context, id int64) (*Issue, error) {
	iss, err := e.Load(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return iss, err
}

// Load は id のチケットを読み込む (Issue.find。無ければ ErrNotFound)。
func (e *Env) Load(ctx context.Context, id int64) (*Issue, error) {
	var r issueRow
	if err := e.Q.Get(ctx, &r, `SELECT `+issueCols+` FROM issues WHERE issues.id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	iss := &Issue{Issue: r.issue()}
	o := iss.Issue
	iss.orig = &o
	if err := e.Q.Select(ctx, &iss.cvRows, `SELECT id, custom_field_id, value FROM custom_values
WHERE customized_kind = 'issue' AND customized_id = ? ORDER BY id`, id); err != nil {
		return nil, err
	}
	return iss, nil
}

// LoadMany は where 条件 (issues を参照) のチケットを id 順で読み込む (カスタム値なし。必要時に読み込む)。
func (e *Env) LoadMany(ctx context.Context, where string, args ...any) ([]*Issue, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []issueRow
	if err := e.Q.Select(ctx, &rows, `SELECT `+issueCols+` FROM issues WHERE `+where+` ORDER BY issues.id`, args...); err != nil {
		return nil, err
	}
	out := make([]*Issue, len(rows))
	byID := make(map[int64]*Issue, len(rows))
	ids := make([]string, len(rows))
	for k := range rows {
		iss := &Issue{Issue: rows[k].issue()}
		o := iss.Issue
		iss.orig = &o
		out[k] = iss
		byID[iss.ID] = iss
		ids[k] = strconv.FormatInt(iss.ID, 10)
	}
	// カスタム値はまとめて読み込む (チケットごとに id 順)
	for len(ids) > 0 {
		n := min(len(ids), 500)
		var cvs []struct {
			cvRow
			CustomizedID int64 `db:"customized_id"`
		}
		if err := e.Q.Select(ctx, &cvs, `SELECT id, custom_field_id, value, customized_id FROM custom_values
WHERE customized_kind = 'issue' AND customized_id IN (`+strings.Join(ids[:n], ",")+`) ORDER BY id`); err != nil {
			return nil, err
		}
		ids = ids[n:]
		for _, cv := range cvs {
			if iss := byID[cv.CustomizedID]; iss != nil {
				iss.cvRows = append(iss.cvRows, cv.cvRow)
			}
		}
	}
	return out, nil
}

// Reload は DB から読み直す (reload)。current_journal は保持する。
func (e *Env) Reload(ctx context.Context, iss *Issue) error {
	fresh, err := e.Load(ctx, iss.ID)
	if err != nil {
		return err
	}
	j := iss.currentJournal
	copiedFrom, copyOpts, handled := iss.copiedFrom, iss.copyOptions, iss.afterCreateFromCopyRun
	*iss = *fresh
	iss.currentJournal = j
	iss.copiedFrom, iss.copyOptions, iss.afterCreateFromCopyRun = copiedFrom, copyOpts, handled
	return nil
}

// ---------------------------------------------------------------- 関連の取得

// ProjectOf は issue.project。
func (e *Env) ProjectOf(ctx context.Context, iss *Issue) (*domain.Project, error) {
	return e.Project(ctx, iss.ProjectID)
}

// TrackerOf は issue.tracker。
func (e *Env) TrackerOf(ctx context.Context, iss *Issue) (*domain.Tracker, error) {
	return e.Tracker(ctx, iss.TrackerID)
}

// StatusOf は issue.status。
func (e *Env) StatusOf(ctx context.Context, iss *Issue) (*domain.IssueStatus, error) {
	return e.Status(ctx, iss.StatusID)
}

// PriorityOf は issue.priority。
func (e *Env) PriorityOf(ctx context.Context, iss *Issue) (*domain.Enumeration, error) {
	return e.Priority(ctx, iss.PriorityID)
}

// Category は id のカテゴリ (無ければ nil)。
func (e *Env) Category(ctx context.Context, id *int64) (*domain.IssueCategory, error) {
	if id == nil {
		return nil, nil
	}
	var r struct {
		ID           int64         `db:"id"`
		ProjectID    int64         `db:"project_id"`
		Name         string        `db:"name"`
		AssignedToID sql.NullInt64 `db:"assigned_to_id"`
	}
	if err := e.Q.Get(ctx, &r, `SELECT id, project_id, name, assigned_to_id FROM issue_categories WHERE id = ?`, *id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &domain.IssueCategory{ID: r.ID, ProjectID: r.ProjectID, Name: r.Name, AssignedToID: nullID(r.AssignedToID)}, nil
}

// ProjectCategoryIDs は project.issue_category_ids (名前順)。
func (e *Env) ProjectCategoryIDs(ctx context.Context, projectID int64) ([]int64, error) {
	var ids []int64
	err := e.Q.Select(ctx, &ids, `SELECT id FROM issue_categories WHERE project_id = ? ORDER BY name, id`, projectID)
	return ids, err
}

type versionRow struct {
	ID            int64          `db:"id"`
	ProjectID     int64          `db:"project_id"`
	Name          string         `db:"name"`
	Description   sql.NullString `db:"description"`
	EffectiveDate db.NullDate    `db:"effective_date"`
	WikiPageTitle sql.NullString `db:"wiki_page_title"`
	Status        string         `db:"status"`
	Sharing       string         `db:"sharing"`
	CreatedAt     db.Time        `db:"created_at"`
	UpdatedAt     db.Time        `db:"updated_at"`
}

const versionCols = `versions.id, versions.project_id, versions.name, versions.description, versions.effective_date,
  versions.wiki_page_title, versions.status, versions.sharing, versions.created_at, versions.updated_at`

func (r *versionRow) version() *domain.Version {
	v := &domain.Version{ID: r.ID, ProjectID: r.ProjectID, Name: r.Name, EffectiveDate: nullDate(r.EffectiveDate),
		Status: r.Status, Sharing: r.Sharing, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if r.Description.Valid {
		s := r.Description.String
		v.Description = &s
	}
	if r.WikiPageTitle.Valid {
		s := r.WikiPageTitle.String
		v.WikiPageTitle = &s
	}
	return v
}

// Version は id のバージョン (無ければ nil)。
func (e *Env) Version(ctx context.Context, id *int64) (*domain.Version, error) {
	if id == nil {
		return nil, nil
	}
	var r versionRow
	if err := e.Q.Get(ctx, &r, `SELECT `+versionCols+` FROM versions WHERE id = ?`, *id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r.version(), nil
}
