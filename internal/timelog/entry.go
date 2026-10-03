// Package timelog は Redmine の工数（app/models/time_entry.rb, time_entry_activity.rb の一部）の
// ドメインロジック（safe_attributes=、検証、保存、可視性・編集可否）を移植する。
//
//	env := &timelog.Env{Q: tx, Settings: st, User: u, Az: az, Loc: loc}
//	e, _ := env.New(ctx, project, issue)        // TimeEntry.new(project:, issue:, author:, spent_on:)
//	env.SafeAssign(ctx, e, params)               // safe_attributes=
//	ok, _ := env.Save(ctx, e)                    // 検証 + 保存
package timelog

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/validation"
)

// Env は工数操作の実行環境（User.current・DB・設定）。
type Env struct {
	Q        db.Queryer
	Settings *settings.Settings
	// User は User.current。
	User *domain.User
	// Az は User.current の Authorizer。
	Az *authz.Authorizer
	// Loc はエラーメッセージの翻訳。
	Loc *i18n.Localizer
	// Now は現在時刻（nil なら clock.Now）。
	Now func() time.Time

	iss      *issues.Env
	projects map[int64]*domain.Project
	cfs      []*domain.CustomFieldInfo
	cfFull   map[int64]*customfield.CustomField
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return clock.Now().UTC()
}

// Today は User.current.today（ユーザーのタイムゾーンでの今日）。
func (e *Env) Today() time.Time {
	t := e.now()
	if e.Loc != nil && e.Loc.Location != nil {
		t = t.In(e.Loc.Location)
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// WithQ は DB ハンドルを q に差し替えた Env を返す（トランザクション内で使う。チケットの Env は作り直す）。
func (e *Env) WithQ(q db.Queryer) *Env {
	c := *e
	c.Q = q
	c.iss = nil
	return &c
}

// Issues はチケットの Env（共有）。
func (e *Env) Issues() *issues.Env {
	if e.iss == nil {
		e.iss = issues.NewEnv(e.Q, e.Settings, e.User)
		e.iss.Now = e.Now
	}
	return e.iss
}

// Project は Project.find_by_id（キャッシュ付き。無ければ nil）。
func (e *Env) Project(ctx context.Context, id *int64) (*domain.Project, error) {
	if id == nil {
		return nil, nil
	}
	if e.projects == nil {
		e.projects = map[int64]*domain.Project{}
	}
	if p, ok := e.projects[*id]; ok {
		return p, nil
	}
	p, err := repository.GetProject(ctx, e.Q, *id)
	if errors.Is(err, repository.ErrNotFound) {
		p, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.projects[*id] = p
	return p, nil
}

func (e *Env) allowed(ctx context.Context, u *domain.User, perm string, p *domain.Project) (bool, error) {
	az := e.Az
	if u != nil && (az == nil || az.User().ID != u.ID) {
		az = authz.New(e.Q, u)
	}
	return az.AllowedTo(ctx, domain.Perm(perm), p)
}

// Entry は編集中の工数（TimeEntry のインスタンス。未設定の属性は nil）。
type Entry struct {
	ID         int64
	ProjectID  *int64
	UserID     *int64
	AuthorID   *int64
	IssueID    *int64
	ActivityID *int64
	Hours      *float64
	Comments   *string
	SpentOn    *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time

	// HoursBeforeTypeCast / SpentOnBeforeTypeCast は代入された生の値（文字列）。
	HoursBeforeTypeCast   any
	IssueIDBeforeTypeCast any
	SpentOnBeforeTypeCast any
	hoursFromUser         bool

	// CFValues は custom_field_values（custom_field_id → 値。cfOrder の順で扱う）。
	CFValues map[int64][]string
	cfOrder  []int64
	cfWas    map[int64][]string

	orig           *domain.TimeEntry
	invalidIssueID bool
	invalidUserID  bool

	// Errors は検証エラー。
	Errors *validation.Errors
}

// NewRecord は new_record?。
func (t *Entry) NewRecord() bool { return t.orig == nil }

// Orig は保存済みの値（新規なら nil）。
func (t *Entry) Orig() *domain.TimeEntry { return t.orig }

func ptr[T any](v T) *T { return &v }

func eqID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func origID(o *domain.TimeEntry, f func(*domain.TimeEntry) int64) *int64 {
	if o == nil {
		return nil
	}
	v := f(o)
	if v == 0 {
		return nil
	}
	return &v
}

// IssueIDChanged などは *_changed?。
func (t *Entry) IssueIDChanged() bool {
	if t.orig == nil {
		return t.IssueID != nil
	}
	return !eqID(t.IssueID, t.orig.IssueID)
}

// ProjectIDChanged は project_id_changed?。
func (t *Entry) ProjectIDChanged() bool {
	return !eqID(t.ProjectID, origID(t.orig, func(o *domain.TimeEntry) int64 { return o.ProjectID }))
}

// UserIDChanged は user_id_changed?。
func (t *Entry) UserIDChanged() bool {
	return !eqID(t.UserID, origID(t.orig, func(o *domain.TimeEntry) int64 { return o.UserID }))
}

// ActivityIDChanged は activity_id_changed?。
func (t *Entry) ActivityIDChanged() bool {
	return !eqID(t.ActivityID, origID(t.orig, func(o *domain.TimeEntry) int64 { return o.ActivityID }))
}

// HoursChanged は hours_changed?。
func (t *Entry) HoursChanged() bool {
	if t.orig == nil {
		return t.Hours != nil
	}
	return t.Hours == nil || *t.Hours != t.orig.Hours
}

// SpentOnChanged は spent_on_changed?。
func (t *Entry) SpentOnChanged() bool {
	if t.orig == nil {
		return t.SpentOn != nil
	}
	return t.SpentOn == nil || !t.SpentOn.Equal(t.orig.SpentOn)
}

func (t *Entry) commentsChanged() bool {
	if t.orig == nil {
		return t.Comments != nil
	}
	a, b := t.Comments, t.orig.Comments
	if a == nil || b == nil {
		return (a == nil) != (b == nil)
	}
	return *a != *b
}

// Changed は changed?（属性またはカスタム値が変わったか）。
func (t *Entry) Changed() bool {
	return t.orig == nil || t.IssueIDChanged() || t.ProjectIDChanged() || t.UserIDChanged() || t.ActivityIDChanged() ||
		t.HoursChanged() || t.SpentOnChanged() || t.commentsChanged() || !eqID(t.AuthorID, origID(t.orig, func(o *domain.TimeEntry) int64 { return o.AuthorID }))
}

// RoundedHours は TimeEntry#hours（分単位に丸めた値。nil なら nil）。
func (t *Entry) RoundedHours() *float64 {
	if t.Hours == nil {
		return nil
	}
	return ptr(domain.RoundedHours(*t.Hours))
}

// FromRecord は保存済みの工数を Entry にする（カスタム値は Load で読む）。
func FromRecord(r *domain.TimeEntry) *Entry {
	o := *r
	t := &Entry{ID: r.ID, ProjectID: ptr(r.ProjectID), UserID: ptr(r.UserID), AuthorID: ptr(r.AuthorID),
		IssueID: r.IssueID, ActivityID: ptr(r.ActivityID), Hours: ptr(r.Hours), Comments: r.Comments,
		SpentOn: ptr(r.SpentOn), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, orig: &o, Errors: validation.New("time_entry")}
	return t
}

// Find は TimeEntry.find（無ければ repository.ErrNotFound）。
func (e *Env) Find(ctx context.Context, id int64) (*Entry, error) {
	r, err := repository.TimeEntryByID(ctx, e.Q, id)
	if err != nil {
		return nil, err
	}
	t := FromRecord(r)
	if err := e.loadCustomValues(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// Reload は time_entry.reload。
func (e *Env) Reload(ctx context.Context, t *Entry) (*Entry, error) { return e.Find(ctx, t.ID) }

// New は TimeEntry.new(project:, issue:, author: User.current, spent_on: User.current.today)
// （作業分類の既定値も設定する）。
func (e *Env) New(ctx context.Context, project *domain.Project, issueID *int64) (*Entry, error) {
	t := &Entry{Errors: validation.New("time_entry")}
	if project != nil {
		t.ProjectID = ptr(project.ID)
	}
	t.IssueID = issueID
	if e.User != nil && e.User.ID != 0 {
		t.AuthorID = ptr(e.User.ID)
	}
	t.SpentOn = ptr(e.Today())
	aid, err := e.DefaultActivityID(ctx, e.User, project)
	if err != nil {
		return nil, err
	}
	t.ActivityID = aid
	if err := e.loadCustomValues(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// SetUser は user = User.current（create のみ）。
func (t *Entry) SetUser(u *domain.User) {
	if u != nil && u.ID != 0 {
		t.UserID = ptr(u.ID)
	}
}

// ---------------------------------------------------------------- 作業分類

// AvailableActivities は TimeEntryActivity.available_activities(project)。
func (e *Env) AvailableActivities(ctx context.Context, project *domain.Project) ([]*domain.Enumeration, error) {
	var pid *int64
	if project != nil {
		pid = ptr(project.ID)
	}
	return repository.TimelogAvailableActivities(ctx, e.Q, pid)
}

// DefaultActivity は TimeEntryActivity.default(project)。
func (e *Env) DefaultActivity(ctx context.Context, project *domain.Project) (*domain.Enumeration, error) {
	def, err := repository.TimelogDefaultActivity(ctx, e.Q)
	if err != nil || def == nil || project == nil {
		return def, err
	}
	acts, err := repository.TimelogProjectActivities(ctx, e.Q, project.ID, false)
	if err != nil {
		return nil, err
	}
	if len(acts) == 0 || slices.ContainsFunc(acts, func(a *domain.Enumeration) bool { return a.ID == def.ID }) {
		return def, nil
	}
	for _, a := range acts {
		if a.ParentID != nil && *a.ParentID == def.ID {
			return a, nil
		}
	}
	return nil, nil
}

// DefaultActivityID は TimeEntryActivity.default_activity_id(user, project)。
func (e *Env) DefaultActivityID(ctx context.Context, user *domain.User, project *domain.Project) (*int64, error) {
	avail, err := e.AvailableActivities(ctx, project)
	if err != nil || len(avail) == 0 {
		return nil, err
	}
	if len(avail) == 1 {
		return ptr(avail[0].ID), nil
	}
	match := func(ids []int64) *int64 {
		for _, id := range ids {
			for _, a := range avail {
				if a.ID == id || (a.ParentID != nil && *a.ParentID == id) {
					return ptr(a.ID)
				}
			}
		}
		return nil
	}
	if project != nil && user != nil && user.ID != 0 {
		ids, err := repository.TimelogRoleDefaultActivityIDs(ctx, e.Q, user.ID, project.ID)
		if err != nil {
			return nil, err
		}
		if aid := match(ids); aid != nil {
			return aid, nil
		}
		pd, err := e.DefaultActivity(ctx, project)
		if err != nil {
			return nil, err
		}
		if pd != nil {
			if aid := match([]int64{pd.ID}); aid != nil {
				return aid, nil
			}
		}
	}
	gd, err := repository.TimelogDefaultActivity(ctx, e.Q)
	if err != nil {
		return nil, err
	}
	if gd != nil {
		if aid := match([]int64{gd.ID}); aid != nil {
			return aid, nil
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------- カスタムフィールド

// CustomFields は TimeEntryCustomField.sorted。
func (e *Env) CustomFields(ctx context.Context) ([]*domain.CustomFieldInfo, error) {
	if e.cfs == nil {
		cfs, err := repository.CustomFieldInfosByKind(ctx, e.Q, "time_entry")
		if err != nil {
			return nil, err
		}
		e.cfs = cfs
		if e.cfs == nil {
			e.cfs = []*domain.CustomFieldInfo{}
		}
	}
	return e.cfs, nil
}

func (e *Env) fullCF(ctx context.Context, id int64) (*customfield.CustomField, error) {
	if e.cfFull == nil {
		cfs, err := customfield.ListByKind(ctx, e.Q, customfield.OwnerKind("time_entry"))
		if err != nil {
			return nil, err
		}
		e.cfFull = map[int64]*customfield.CustomField{}
		for _, cf := range cfs {
			e.cfFull[cf.ID] = cf
		}
	}
	return e.cfFull[id], nil
}

func (e *Env) loadCustomValues(ctx context.Context, t *Entry) error {
	cfs, err := e.CustomFields(ctx)
	if err != nil {
		return err
	}
	stored := map[int64][]string{}
	if t.ID != 0 {
		if stored, err = repository.CustomValues(ctx, e.Q, "time_entry", t.ID); err != nil {
			return err
		}
	}
	t.CFValues = map[int64][]string{}
	t.cfWas = map[int64][]string{}
	t.cfOrder = nil
	for _, cf := range cfs {
		t.cfOrder = append(t.cfOrder, cf.ID)
		if vs, ok := stored[cf.ID]; ok {
			t.CFValues[cf.ID] = vs
		} else if t.ID == 0 && cf.DefaultValue != nil && *cf.DefaultValue != "" {
			t.CFValues[cf.ID] = []string{*cf.DefaultValue}
		} else {
			t.CFValues[cf.ID] = nil
		}
		t.cfWas[cf.ID] = slices.Clone(t.CFValues[cf.ID])
	}
	return nil
}

// VisibleCustomFieldValues は visible_custom_field_values(user)（= editable_custom_field_values）。
func (e *Env) VisibleCustomFieldValues(ctx context.Context, t *Entry, u *domain.User) ([]*domain.CustomFieldValue, error) {
	cfs, err := e.CustomFields(ctx)
	if err != nil {
		return nil, err
	}
	p, err := e.Project(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	var out []*domain.CustomFieldValue
	for _, cf := range cfs {
		if !slices.Contains(t.cfOrder, cf.ID) {
			continue
		}
		ok, err := e.cfVisible(ctx, cf, p, u)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, &domain.CustomFieldValue{Field: cf, Values: t.CFValues[cf.ID]})
		}
	}
	return out, nil
}

// CustomFieldValues は custom_field_values（全件）。
func (e *Env) CustomFieldValues(ctx context.Context, t *Entry) ([]*domain.CustomFieldValue, error) {
	cfs, err := e.CustomFields(ctx)
	if err != nil {
		return nil, err
	}
	var out []*domain.CustomFieldValue
	for _, cf := range cfs {
		if slices.Contains(t.cfOrder, cf.ID) {
			out = append(out, &domain.CustomFieldValue{Field: cf, Values: t.CFValues[cf.ID]})
		}
	}
	return out, nil
}

// cfVisible は custom_field.visible_by?(project, user)。
func (e *Env) cfVisible(ctx context.Context, cf *domain.CustomFieldInfo, p *domain.Project, u *domain.User) (bool, error) {
	full, err := e.fullCF(ctx, cf.ID)
	if err != nil || full == nil {
		return false, err
	}
	az := e.Az
	if u != nil && (az == nil || az.User().ID != u.ID) {
		az = authz.New(e.Q, u)
	}
	return customfield.VisibleBy(ctx, az, full, p)
}

// ---------------------------------------------------------------- 可視性・編集可否

// Visible は visible?(user)。
func (e *Env) Visible(ctx context.Context, t *Entry, u *domain.User) (bool, error) {
	p, err := e.Project(ctx, t.ProjectID)
	if err != nil {
		return false, err
	}
	az := e.Az
	if u != nil && (az == nil || az.User().ID != u.ID) {
		az = authz.New(e.Q, u)
	}
	return az.AllowedToWith(ctx, domain.Perm("view_time_entries"), p, func(r *domain.Role, user *domain.User) bool {
		switch r.TimeEntriesVisibility {
		case domain.TimeEntriesVisibilityAll:
			return true
		case domain.TimeEntriesVisibilityOwn:
			return t.UserID != nil && *t.UserID == user.ID
		}
		return false
	})
}

// EditableBy は editable_by?(usr)。
func (e *Env) EditableBy(ctx context.Context, t *Entry, u *domain.User) (bool, error) {
	ok, err := e.Visible(ctx, t, u)
	if err != nil || !ok {
		return false, err
	}
	p, err := e.Project(ctx, t.ProjectID)
	if err != nil {
		return false, err
	}
	if t.UserID != nil && u != nil && *t.UserID == u.ID {
		ok, err := e.allowed(ctx, u, "edit_own_time_entries", p)
		if err != nil || ok {
			return ok, err
		}
	}
	return e.allowed(ctx, u, "edit_time_entries", p)
}

// AssignableUsers は assignable_users（log_time を持つ有効なメンバーのユーザー（sorted）+ User.current）。
func (e *Env) AssignableUsers(ctx context.Context, t *Entry) ([]*domain.User, error) {
	var users []*domain.User
	if t.ProjectID != nil {
		ids, err := repository.TimelogLogTimeMemberUserIDs(ctx, e.Q, *t.ProjectID)
		if err != nil {
			return nil, err
		}
		m, err := repository.UsersByIDs(ctx, e.Q, ids)
		if err != nil {
			return nil, err
		}
		for _, u := range m {
			if u.Kind == domain.KindUser {
				users = append(users, u)
			}
		}
		SortUsers(users, e.userFormat())
	}
	if e.User != nil && e.User.Logged() && !slices.ContainsFunc(users, func(u *domain.User) bool { return u.ID == e.User.ID }) {
		users = append(users, e.User)
	}
	return users, nil
}

func (e *Env) userFormat() string {
	if e.Settings == nil {
		return "firstname_lastname"
	}
	return e.Settings.String("user_format")
}

// SortUsers は User.sorted（Setting.user_format の並び。同値は id 順）。
func SortUsers(users []*domain.User, format string) {
	key := func(u *domain.User) []string {
		switch format {
		case "firstname":
			return []string{u.Firstname}
		case "lastname_firstname", "lastnamefirstname", "lastname_comma_firstname":
			return []string{u.Lastname, u.Firstname}
		case "lastname":
			return []string{u.Lastname}
		case "username":
			return []string{u.Login}
		}
		return []string{u.Firstname, u.Lastname}
	}
	slices.SortStableFunc(users, func(a, b *domain.User) int {
		ka, kb := key(a), key(b)
		for i := range ka {
			if c := strings.Compare(ka[i], kb[i]); c != 0 {
				return c
			}
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
}

// ---------------------------------------------------------------- safe_attributes=

// Attrs は safe_attributes= に渡す属性（params[:time_entry]）。値は文字列・数値・nil・配列・ハッシュ。
type Attrs interface {
	Get(key string) (any, bool)
	Keys() []string
}

var intRe = regexp.MustCompile(`^\s*[+-]?\d+`)

// castInt は ActiveModel::Type::Integer#cast（非数値の文字列・空は nil）。
func castInt(v any) *int64 {
	switch x := v.(type) {
	case nil:
		return nil
	case int64:
		return &x
	case int:
		return ptr(int64(x))
	case float64:
		return ptr(int64(x))
	case bool:
		if x {
			return ptr(int64(1))
		}
		return ptr(int64(0))
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return ptr(int64(f))
		}
		if m := intRe.FindString(s); m != "" && regexp.MustCompile(`^\s*[+-]?\d+(\.\d+)?\s*$`).MatchString(s) {
			n, _ := strconv.ParseInt(strings.TrimSpace(m), 10, 64)
			return &n
		}
		return nil
	}
	return nil
}

func toS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return issues.RubyFloatToS(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

var dateRe = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})`)

// castDate は ActiveModel::Type::Date#cast（解釈できなければ nil）。
func castDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return nil
	}
	return &t
}

// SetHours は hours=（文字列なら to_hours。解釈できなければ Float 型のキャストに任せる）。
func (t *Entry) SetHours(v any) {
	t.HoursBeforeTypeCast = v
	t.hoursFromUser = true
	switch x := v.(type) {
	case nil:
		t.Hours = nil
	case float64:
		t.Hours = &x
	case int64:
		t.Hours = ptr(float64(x))
	case string:
		if f, ok := issues.ToHours(x); ok {
			t.Hours = &f
			t.HoursBeforeTypeCast = f
			return
		}
		// Float 型のキャスト（"".presence → nil、それ以外は to_f）
		s := strings.TrimSpace(x)
		if x == "" || s == "" && x == "" {
			t.Hours = nil
			return
		}
		if strings.TrimSpace(x) == "" {
			// 空白のみは presence で nil にならないが to_f は 0.0
			t.Hours = ptr(0.0)
			return
		}
		t.Hours = ptr(rubyToF(x))
	default:
		t.Hours = nil
	}
}

var toFRe = regexp.MustCompile(`^\s*[+-]?(\d[\d_]*)?(\.\d+)?([eE][+-]?\d+)?`)

// rubyToF は String#to_f。
func rubyToF(s string) float64 {
	m := toFRe.FindString(s)
	m = strings.ReplaceAll(strings.TrimSpace(m), "_", "")
	f, _ := strconv.ParseFloat(m, 64)
	return f
}

// SetSpentOn は spent_on=。
func (t *Entry) SetSpentOn(v any) {
	t.SpentOnBeforeTypeCast = v
	s := toS(v)
	if s == "" {
		t.SpentOn = nil
		return
	}
	t.SpentOn = castDate(s)
}

// SafeAssign は safe_attributes=(attrs, user)。
func (e *Env) SafeAssign(ctx context.Context, t *Entry, attrs Attrs, user *domain.User) error {
	if attrs == nil {
		return nil
	}
	if user == nil {
		user = e.User
	}
	_, projectGiven := attrs.Get("project_id")
	projectBlank := true
	for _, k := range attrs.Keys() {
		v, _ := attrs.Get(k)
		switch k {
		case "project_id":
			t.ProjectID = castInt(v)
			projectBlank = strings.TrimSpace(toS(v)) == ""
		case "issue_id":
			t.IssueID = castInt(v)
			t.IssueIDBeforeTypeCast = v
		case "user_id":
			t.UserID = castInt(v)
		case "activity_id":
			t.ActivityID = castInt(v)
		case "hours":
			t.SetHours(v)
		case "comments":
			if v == nil {
				t.Comments = nil
			} else {
				t.Comments = ptr(toS(v))
			}
		case "spent_on":
			t.SetSpentOn(v)
		case "custom_field_values":
			if m, ok := v.(Attrs); ok {
				for _, ck := range m.Keys() {
					cv, _ := m.Get(ck)
					id, err := strconv.ParseInt(ck, 10, 64)
					if err != nil || !slices.Contains(t.cfOrder, id) {
						continue
					}
					t.CFValues[id] = cfInput(cv)
				}
			}
		case "custom_fields":
			if list, ok := v.([]any); ok {
				for _, item := range list {
					m, ok := item.(Attrs)
					if !ok {
						continue
					}
					idv, _ := m.Get("id")
					id := castInt(idv)
					if id == nil || !slices.Contains(t.cfOrder, *id) {
						continue
					}
					val, _ := m.Get("value")
					t.CFValues[*id] = cfInput(val)
				}
			}
		}
	}
	_ = projectGiven
	if t.IssueIDChanged() && t.IssueID != nil {
		iss, err := e.Issues().Find(ctx, *t.IssueID)
		if err != nil {
			return err
		}
		if iss != nil {
			vis, err := e.Issues().Visible(ctx, iss, user)
			if err != nil {
				return err
			}
			ip, err := e.Project(ctx, ptr(iss.ProjectID))
			if err != nil {
				return err
			}
			can, err := e.allowed(ctx, user, "log_time", ip)
			if err != nil {
				return err
			}
			if vis && can {
				if projectBlank && !eqID(ptr(iss.ProjectID), t.ProjectID) {
					t.ProjectID = ptr(iss.ProjectID)
				}
				t.invalidIssueID = false
			} else {
				t.invalidIssueID = true
			}
		}
	}
	p, err := e.Project(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	if t.UserIDChanged() && !eqID(t.UserID, t.AuthorID) {
		ok, err := e.allowed(ctx, user, "log_time_for_other_users", p)
		if err != nil {
			return err
		}
		t.invalidUserID = !ok
	} else {
		t.invalidUserID = false
	}
	// 編集できないカスタム値を除く
	var keep []int64
	vals, err := e.VisibleCustomFieldValues(ctx, t, user)
	if err != nil {
		return err
	}
	for _, v := range vals {
		keep = append(keep, v.Field.ID)
	}
	t.cfOrder = slices.DeleteFunc(t.cfOrder, func(id int64) bool { return !slices.Contains(keep, id) })
	return nil
}

func cfInput(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		out := []string{}
		for _, e := range x {
			out = append(out, toS(e))
		}
		return out
	case []string:
		return x
	}
	return []string{toS(v)}
}

// ---------------------------------------------------------------- 検証

func (e *Env) setting(name string) string {
	if e.Settings == nil {
		return ""
	}
	return e.Settings.String(name)
}

func (e *Env) settingBool(name string) bool {
	return e.Settings != nil && e.Settings.Bool(name)
}

func (e *Env) requiredFields() []string {
	if e.Settings == nil {
		return nil
	}
	return e.Settings.Strings("timelog_required_fields")
}

// Validate は valid?（before_validation の set_project_if_nil / set_author_if_nil を含む）。
func (e *Env) Validate(ctx context.Context, t *Entry) error {
	t.Errors = validation.New("time_entry")
	var iss *issues.Issue
	if t.IssueID != nil {
		var err error
		if iss, err = e.Issues().Find(ctx, *t.IssueID); err != nil {
			return err
		}
	}
	project, err := e.Project(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	// before_validation
	if iss != nil && project == nil {
		t.ProjectID = ptr(iss.ProjectID)
		if project, err = e.Project(ctx, t.ProjectID); err != nil {
			return err
		}
	}
	if t.AuthorID == nil && e.User != nil && e.User.ID != 0 {
		t.AuthorID = ptr(e.User.ID)
	}
	errs := t.Errors
	// validates_presence_of
	if t.AuthorID == nil {
		errs.Add("author_id", "blank")
	}
	if t.UserID == nil {
		errs.Add("user_id", "blank")
	}
	if t.ActivityID == nil {
		errs.Add("activity_id", "blank")
	}
	if t.ProjectID == nil {
		errs.Add("project_id", "blank")
	}
	if t.Hours == nil {
		errs.Add("hours", "blank")
	}
	if t.SpentOn == nil {
		errs.Add("spent_on", "blank")
	}
	req := e.requiredFields()
	if slices.Contains(req, "issue_id") && t.IssueID == nil {
		errs.Add("issue_id", "blank")
	}
	if slices.Contains(req, "comments") && strings.TrimSpace(t.CommentsString()) == "" {
		errs.Add("comments", "blank")
	}
	// validates_numericality_of :hours, allow_nil: true, message: :invalid
	if t.Hours != nil && t.hoursFromUser {
		if s, ok := t.HoursBeforeTypeCast.(string); ok {
			if _, ok := numericString(s); !ok {
				errs.Add("hours", "invalid")
			}
		}
	}
	if t.Comments != nil && len([]rune(*t.Comments)) > 1024 {
		errs.Add("comments", "too_long", "count", 1024)
	}
	// validates :spent_on, date: true
	if s, ok := t.SpentOnBeforeTypeCast.(string); ok && strings.TrimSpace(s) != "" {
		if !regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}( 00:00:00)?\z`).MatchString(s) || t.SpentOn == nil {
			errs.Add("spent_on", "not_a_date")
		}
	}
	// validate_time_entry
	if t.Hours != nil {
		h := domain.RoundedHours(*t.Hours)
		if h < 0 {
			errs.Add("hours", "invalid")
		}
		if h == 0 && t.HoursChanged() && !e.settingBool("timelog_accept_0_hours") {
			errs.Add("hours", "invalid")
		}
		maxHours := rubyToF(e.setting("timelog_max_hours_per_day"))
		if t.HoursChanged() && maxHours > 0 {
			logged := 0.0
			if t.UserID != nil && t.SpentOn != nil {
				if logged, err = repository.TimeEntryOtherHours(ctx, e.Q, *t.UserID, *t.SpentOn, t.ID); err != nil {
					return err
				}
			}
			if logged+h > maxHours {
				errs.AddMessage("base", e.Loc.L("error_exceeds_maximum_hours_per_day",
					map[string]any{"logged_hours": e.Loc.FormatHours(logged), "max_hours": e.Loc.FormatHours(maxHours)}))
			}
		}
	}
	if project == nil {
		errs.Add("project_id", "invalid")
	}
	if t.invalidUserID || (t.UserIDChanged() && !eqID(t.UserID, t.AuthorID) && !e.userAssignable(ctx, t)) {
		errs.Add("user_id", "invalid")
	}
	if (t.IssueID != nil && iss == nil) || (iss != nil && (project == nil || project.ID != iss.ProjectID)) || t.invalidIssueID {
		errs.Add("issue_id", "invalid")
	}
	if t.ActivityIDChanged() && project != nil {
		acts, err := repository.TimelogProjectActivities(ctx, e.Q, project.ID, false)
		if err != nil {
			return err
		}
		if t.ActivityID == nil || !slices.ContainsFunc(acts, func(a *domain.Enumeration) bool { return a.ID == *t.ActivityID }) {
			errs.Add("activity_id", "inclusion")
		}
	}
	if t.SpentOn != nil && t.SpentOnChanged() && t.UserID != nil {
		if !e.settingBool("timelog_accept_future_dates") && t.SpentOn.After(e.userToday(ctx, *t.UserID)) {
			errs.AddMessage("base", e.Loc.L("error_spent_on_future_date"))
		}
	}
	if !e.settingBool("timelog_accept_closed_issues") && iss != nil {
		closed, err := e.Issues().Closed(ctx, iss)
		if err != nil {
			return err
		}
		was, err := e.Issues().WasClosed(ctx, iss)
		if err != nil {
			return err
		}
		if closed && was {
			errs.AddMessage("base", e.Loc.L("error_spent_on_closed_issue"))
		}
	}
	// acts_as_customizable の validate_custom_field_values
	if t.NewRecord() || t.customValuesChanged() {
		for _, id := range t.cfOrder {
			full, err := e.fullCF(ctx, id)
			if err != nil {
				return err
			}
			if full == nil {
				continue
			}
			var val any
			vs := t.CFValues[id]
			if full.Multiple {
				arr := make([]any, len(vs))
				for i, s := range vs {
					arr[i] = s
				}
				val = arr
			} else if len(vs) > 0 {
				val = vs[0]
			}
			cenv := &customfield.Env{T: e.Loc.L, CurrentUserID: e.userID()}
			for _, msg := range customfield.ValidateFieldValue(cenv, full, val) {
				errs.AddMessage("base", full.Name+" "+msg)
			}
		}
	}
	return nil
}

func (e *Env) userID() int64 {
	if e.User == nil {
		return 0
	}
	return e.User.ID
}

// userToday は user.today（ユーザーのタイムゾーン）。
func (e *Env) userToday(ctx context.Context, userID int64) time.Time {
	t := e.now()
	pref, err := repository.GetUserPreference(ctx, e.Q, userID)
	var loc *time.Location
	if err == nil && pref != nil {
		loc = i18n.UserLocation(pref.TimeZone)
	}
	if loc == nil {
		loc = time.Local
	}
	t = t.In(loc)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (t *Entry) customValuesChanged() bool {
	for _, id := range t.cfOrder {
		if !slices.Equal(t.CFValues[id], t.cfWas[id]) {
			return true
		}
	}
	return false
}

func (e *Env) userAssignable(ctx context.Context, t *Entry) bool {
	if t.UserID == nil {
		return false
	}
	users, err := e.AssignableUsers(ctx, t)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(users, func(u *domain.User) bool { return u.ID == *t.UserID })
}

var numericRe = regexp.MustCompile(`\A\s*[+-]?\d+(\.\d+)?([eE][+-]?\d+)?\s*\z`)

// numericString は NumericalityValidator の数値判定（Kernel.Float 相当）。
func numericString(s string) (float64, bool) {
	if !numericRe.MatchString(s) {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// CommentsString は comments（nil なら空文字列）。
func (t *Entry) CommentsString() string {
	if t.Comments == nil {
		return ""
	}
	return *t.Comments
}

// ---------------------------------------------------------------- 保存・削除

// Save は save（検証して保存。検証エラーなら false）。
func (e *Env) Save(ctx context.Context, t *Entry) (bool, error) {
	if err := e.Validate(ctx, t); err != nil {
		return false, err
	}
	if t.Errors.Any() {
		return false, nil
	}
	r := &domain.TimeEntry{ID: t.ID, ProjectID: *t.ProjectID, UserID: *t.UserID, AuthorID: *t.AuthorID,
		IssueID: t.IssueID, Hours: *t.Hours, Comments: t.Comments, ActivityID: *t.ActivityID, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
	r.SetSpentOn(*t.SpentOn)
	now := e.now()
	write := func(q db.Queryer) error {
		if t.NewRecord() {
			if err := repository.TimeEntryInsert(ctx, q, r, now); err != nil {
				return err
			}
		} else {
			touch := t.Changed()
			if err := repository.TimeEntryUpdate(ctx, q, r, now, touch); err != nil {
				return err
			}
		}
		for _, id := range t.cfOrder {
			if !t.NewRecord() && slices.Equal(t.CFValues[id], t.cfWas[id]) {
				continue
			}
			vals := t.CFValues[id]
			if len(vals) == 0 {
				vals = []string{""}
			}
			if err := repository.SetCustomValues(ctx, q, "time_entry", r.ID, id, vals); err != nil {
				return err
			}
		}
		return nil
	}
	// 行とカスタム値は 1 トランザクションで保存する（e.Q が *db.DB のとき。呼び出し側のトランザクションならそのまま）
	var err error
	if d, ok := e.Q.(*db.DB); ok {
		err = d.WithTx(ctx, func(tx *db.Tx) error { return write(tx) })
	} else {
		err = write(e.Q)
	}
	if err != nil {
		return false, err
	}
	t.ID, t.CreatedAt, t.UpdatedAt = r.ID, r.CreatedAt, r.UpdatedAt
	o := *r
	t.orig = &o
	for _, id := range t.cfOrder {
		t.cfWas[id] = slices.Clone(t.CFValues[id])
	}
	return true, nil
}

// Destroy は destroy。
func (e *Env) Destroy(ctx context.Context, t *Entry) error {
	return repository.TimeEntryDelete(ctx, e.Q, t.ID)
}
