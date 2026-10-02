package issues

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// frozenNow は参照環境の Redmine (COMPAT_FROZEN_TIME) と同じ固定時刻。
var frozenNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// tc は 1 テスト分の環境 (全フィクスチャを frozenNow 基準で投入した DB)。
type tc struct {
	t   testing.TB
	ctx context.Context
	d   *db.DB
	st  *settings.Settings
	// cur は User.current (0 は匿名)。
	cur int64
	now time.Time
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
	return &tc{t: t, ctx: ctx, d: d, st: st, now: frozenNow}
}

func (c *tc) must(err error) {
	c.t.Helper()
	if err != nil {
		c.t.Fatal(err)
	}
}

// env は User.current = c.cur の Env (DB に直接、操作ごとにトランザクション)。
func (c *tc) env() *Env {
	c.t.Helper()
	var u *domain.User
	if c.cur != 0 {
		u = c.user(c.cur)
	}
	e := NewEnv(c.d, c.st, u)
	now := c.now
	e.Now = func() time.Time { return now }
	return e
}

// as は User.current を設定する (with_current_user)。
func (c *tc) as(id int64) *tc { c.cur = id; return c }

func (c *tc) user(id int64) *domain.User {
	c.t.Helper()
	u, err := repository.GetUser(c.ctx, c.d, id)
	c.must(err)
	return u
}

func (c *tc) project(id int64) *domain.Project {
	c.t.Helper()
	p, err := repository.GetProject(c.ctx, c.d, id)
	c.must(err)
	return p
}

func (c *tc) issue(id int64) *Issue {
	c.t.Helper()
	iss, err := c.env().Load(c.ctx, id)
	c.must(err)
	return iss
}

func (c *tc) exec(q string, args ...any) {
	c.t.Helper()
	_, err := c.d.Exec(c.ctx, q, args...)
	c.must(err)
}

func (c *tc) setting(name string, v any) {
	c.t.Helper()
	c.must(c.st.Set(c.ctx, name, v))
}

func (c *tc) count(q string, args ...any) int {
	c.t.Helper()
	var n int
	c.must(c.d.Get(c.ctx, &n, q, args...))
	return n
}

func (c *tc) ids(q string, args ...any) []int64 {
	c.t.Helper()
	var ids []int64
	c.must(c.d.Select(c.ctx, &ids, q, args...))
	return ids
}

// newIssue は Issue.new(attrs) (属性の代入まで)。
func (c *tc) newIssue(e *Env, attrs Params) *Issue {
	c.t.Helper()
	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	c.must(e.AssignAttributes(c.ctx, iss, attrs))
	return iss
}

// generate は Issue.generate (未保存。project 1、先頭トラッカー、作成者 2、通知なし)。
func (c *tc) generate(e *Env, attrs Params) *Issue {
	c.t.Helper()
	iss := c.newIssue(e, attrs)
	iss.SetNotify(false)
	if iss.ProjectID == 0 {
		c.must(e.SetProject(c.ctx, iss, c.project(1), false))
	}
	if iss.TrackerID == 0 {
		ts, err := e.ProjectTrackers(c.ctx, c.project(iss.ProjectID))
		c.must(err)
		if len(ts) > 0 {
			c.must(e.SetTracker(c.ctx, iss, ts[0]))
		}
	}
	if strings.TrimSpace(iss.Subject) == "" {
		iss.Subject = "Generated"
	}
	if iss.AuthorID == 0 {
		iss.AuthorID = 2
	}
	return iss
}

// generateSaved は Issue.generate! (保存して読み直す)。
func (c *tc) generateSaved(e *Env, attrs Params) *Issue {
	c.t.Helper()
	iss := c.generate(e, attrs)
	c.saveOK(e, iss)
	return c.reload(e, iss)
}

// generateWithDescendants は Issue.generate_with_descendants!。
func (c *tc) generateWithDescendants(e *Env, attrs Params) *Issue {
	c.t.Helper()
	iss := c.generateSaved(e, attrs)
	child := c.generateSaved(e, Params{"project_id": iss.ProjectID, "subject": "Child1", "parent_issue_id": iss.ID})
	c.generateSaved(e, Params{"project_id": iss.ProjectID, "subject": "Child2", "parent_issue_id": iss.ID})
	c.generateSaved(e, Params{"project_id": iss.ProjectID, "subject": "Child11", "parent_issue_id": child.ID})
	return c.reload(e, iss)
}

func (c *tc) reload(e *Env, iss *Issue) *Issue {
	c.t.Helper()
	c.must(e.Reload(c.ctx, iss))
	return iss
}

// save は issue.save。
func (c *tc) save(e *Env, iss *Issue) bool {
	c.t.Helper()
	ok, _, err := e.Save(c.ctx, iss)
	c.must(err)
	return ok
}

// saveOK は issue.save! (失敗ならテスト失敗)。
func (c *tc) saveOK(e *Env, iss *Issue) *SaveResult {
	c.t.Helper()
	ok, res, err := e.Save(c.ctx, iss)
	c.must(err)
	if !ok {
		c.t.Fatalf("save failed: %v", iss.Errors.List)
	}
	return res
}

func (c *tc) valid(e *Env, iss *Issue) bool {
	c.t.Helper()
	ok, err := e.Validate(c.ctx, iss)
	c.must(err)
	return ok
}

// addWorkflowPermission は WorkflowPermission.create!。
func (c *tc) addWorkflowPermission(status, tracker, role int64, field, rule string) {
	c.t.Helper()
	var core, cf any
	if n := castID(field); n != nil && digitsRe.MatchString(field) {
		cf = *n
	} else {
		core = field
	}
	c.exec(`INSERT INTO workflow_field_rules (tracker_id, role_id, status_id, core_field, custom_field_id, rule) VALUES (?, ?, ?, ?, ?, ?)`,
		tracker, role, status, core, cf, rule)
}

// addTransition は WorkflowTransition.create!。
func (c *tc) addTransition(tracker, role int64, old, new int64, author, assignee bool) {
	c.t.Helper()
	var o any
	if old != 0 {
		o = old
	}
	c.exec(`INSERT INTO workflow_transitions (tracker_id, role_id, old_status_id, new_status_id, author, assignee) VALUES (?, ?, ?, ?, ?, ?)`,
		tracker, role, o, new, author, assignee)
}

var cfSeq atomic.Int64

// cfAttrs は IssueCustomField.create! / generate! の属性。
type cfAttrs struct {
	// ID は明示する場合のみ (0 なら採番)。
	ID             int64
	Name           string
	Format         string
	IsForAll       bool
	IsRequired     bool
	Visible        *bool
	Multiple       bool
	Trackers       []int64 // nil なら全トラッカー
	Projects       []int64
	Roles          []int64
	DefaultValue   string
	PossibleValues []string
	Editable       *bool
	Regexp         string
	MaxLength      int
}

// createCF はチケット用カスタムフィールドを作る。
func (c *tc) createCF(a cfAttrs) int64 {
	c.t.Helper()
	if a.Name == "" {
		a.Name = fmt.Sprintf("Custom field %d", cfSeq.Add(1))
	}
	if a.Format == "" {
		a.Format = "string"
	}
	visible := true
	if a.Visible != nil {
		visible = *a.Visible
	}
	editable := true
	if a.Editable != nil {
		editable = *a.Editable
	}
	var dv, pv, re any
	if a.DefaultValue != "" {
		dv = a.DefaultValue
	}
	if a.PossibleValues != nil {
		parts := make([]string, len(a.PossibleValues))
		for i, s := range a.PossibleValues {
			parts[i] = fmt.Sprintf("%q", s)
		}
		pv = "[" + strings.Join(parts, ",") + "]"
	}
	if a.Regexp != "" {
		re = a.Regexp
	}
	var ml any
	if a.MaxLength > 0 {
		ml = a.MaxLength
	}
	var pos int
	c.must(c.d.Get(c.ctx, &pos, `SELECT COALESCE(MAX(position), 0) + 1 FROM custom_fields WHERE owner_kind = 'issue'`))
	id, err := c.d.InsertReturningID(c.ctx, `INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, is_required, visible,
  multiple, default_value, possible_values, editable, regexp, max_length, position) VALUES ('issue', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Format, a.IsForAll, a.IsRequired, visible, a.Multiple, dv, pv, editable, re, ml, pos)
	c.must(err)
	if a.ID != 0 && a.ID != id {
		c.exec(`UPDATE custom_fields SET id = ? WHERE id = ?`, a.ID, id)
		id = a.ID
	}
	trackers := a.Trackers
	if trackers == nil {
		trackers = c.ids(`SELECT id FROM trackers ORDER BY id`)
	}
	for _, t := range trackers {
		c.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`, id, t)
	}
	for _, p := range a.Projects {
		c.exec(`INSERT INTO custom_fields_projects (custom_field_id, project_id) VALUES (?, ?)`, id, p)
	}
	for _, r := range a.Roles {
		c.exec(`INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (?, ?)`, id, r)
	}
	return id
}

func boolp(b bool) *bool { return &b }

// removePermission は Role#remove_permission!。
func (c *tc) removePermission(role int64, perms ...string) {
	c.t.Helper()
	for _, p := range perms {
		c.exec(`DELETE FROM role_permissions WHERE role_id = ? AND permission = ?`, role, p)
	}
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

// setPermissionTrackers は Role#set_permission_trackers(perm, trackers) (nil = 全トラッカー)。
func (c *tc) setPermissionTrackers(role int64, perm string, trackers []int64) {
	c.t.Helper()
	c.exec(`DELETE FROM role_permission_trackers WHERE role_id = ? AND permission = ?`, role, perm)
	c.exec(`UPDATE role_permissions SET all_trackers = ? WHERE role_id = ? AND permission = ?`, trackers == nil, role, perm)
	for _, t := range trackers {
		c.exec(`INSERT INTO role_permission_trackers (role_id, permission, tracker_id) VALUES (?, ?, ?)`, role, perm, t)
	}
}

// setCoreFields は Tracker#core_fields= + save!。
func (c *tc) setCoreFields(tracker int64, fields ...string) {
	c.t.Helper()
	var dis []string
	for _, f := range domain.TrackerCoreFields {
		if !slices.Contains(fields, f) {
			dis = append(dis, f)
		}
	}
	js := "[]"
	if len(dis) > 0 {
		js = `["` + strings.Join(dis, `","`) + `"]`
	}
	c.exec(`UPDATE trackers SET disabled_core_fields = ? WHERE id = ?`, js, tracker)
}

// addMember は Member.create!(principal, project, roles)。
func (c *tc) addMember(principal, project int64, roles ...int64) {
	c.t.Helper()
	_, err := repository.CreateMember(c.ctx, c.d, project, principal, roles)
	c.must(err)
}

// generateUser は User.generate!。
func (c *tc) generateUser(a testfixtures.UserAttrs) int64 {
	c.t.Helper()
	id := testfixtures.GenerateUserWith(c.t, c.d, a)
	return id
}

func (c *tc) generateGroup() int64 {
	c.t.Helper()
	return testfixtures.GenerateGroup(c.t, c.d)
}

// addRelation は IssueRelation.create! (検証付き)。
func (c *tc) addRelation(e *Env, from, to *Issue, typ string, delay *int) *Relation {
	c.t.Helper()
	r := &Relation{From: from, To: to}
	r.RelationType = typ
	r.Delay = delay
	ok, _, err := e.CreateRelation(c.ctx, r)
	c.must(err)
	if !ok {
		c.t.Fatalf("relation create failed: %v", r.Errors.List)
	}
	return r
}

func intp(i int) *int { return &i }

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func datep(s string) *time.Time { t := date(s); return &t }

func today() time.Time { return dateOnly(frozenNow) }

func daysFromNow(n int) time.Time { return today().AddDate(0, 0, n) }

func sp(s string) *string { return &s }

// errorsOn は errors[attr] (キーまたはメッセージ)。
func errorsOn(iss *Issue, attr string) []string { return iss.Errors.On(attr) }

func hasError(iss *Issue, attr, key string) bool { return slices.Contains(iss.Errors.On(attr), key) }

func statusIDs(ss []*domain.IssueStatus) []int64 {
	out := make([]int64, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

func eqIDs(t testing.TB, want, got []int64, msg ...any) {
	t.Helper()
	if !slices.Equal(want, got) {
		t.Errorf("%v: want %v, got %v", msg, want, got)
	}
}

func cfVal(c *tc, e *Env, iss *Issue, cfID int64) CFValue {
	c.t.Helper()
	v, _, err := e.CustomFieldValue(c.ctx, iss, cfID)
	c.must(err)
	return v
}

func journals(c *tc, issueID int64) []*Journal {
	c.t.Helper()
	js, err := c.env().Journals(c.ctx, issueID)
	c.must(err)
	return js
}

func lastJournal(c *tc, issueID int64) *Journal {
	c.t.Helper()
	js := journals(c, issueID)
	if len(js) == 0 {
		return nil
	}
	return js[len(js)-1]
}
