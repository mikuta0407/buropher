package handler

// チケットの参照系画面で使う、関連レコード（プロジェクト・トラッカー・ステータス・優先度・ユーザー・
// カテゴリ・バージョン・チケット）のリクエスト内キャッシュと、行の HTML 部品（link_to_issue 等）。

import (
	"context"
	"database/sql"
	"html/template"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// issueLookup は 1 リクエスト内の関連レコードのキャッシュ。
type issueLookup struct {
	a    *App
	c    *Req
	ctx  context.Context
	page *helper.Page
	err  error

	projects   map[int64]*domain.Project
	trackers   map[int64]*domain.Tracker
	trackerOrd []*domain.Tracker
	statuses   map[int64]*domain.IssueStatus
	statusOrd  []*domain.IssueStatus
	priorities map[int64]*domain.Enumeration
	prioOrd    []*domain.Enumeration
	principals map[int64]*domain.User
	categories map[int64]*repository.IssueCategory
	versions   map[int64]*repository.Version
	issues     map[int64]*query.IssueRow
	children   map[int64]bool
	visible    map[int64]bool
	cfs        map[int64]*customfield.CustomField
	cfVisible  map[[2]int64]bool
	attachs    map[int64]*repository.ReadAttachment
	relations  map[int64]*repository.IssueRelation
	vVisible   map[int64]bool
	ie         *issues.Env
}

func (a *App) newIssueLookup(c *Req) *issueLookup {
	return &issueLookup{a: a, c: c, ctx: c.Ctx(), page: c.Page(),
		principals: map[int64]*domain.User{}, categories: map[int64]*repository.IssueCategory{},
		versions: map[int64]*repository.Version{}, issues: map[int64]*query.IssueRow{}, children: map[int64]bool{},
		visible: map[int64]bool{}, cfVisible: map[[2]int64]bool{}, attachs: map[int64]*repository.ReadAttachment{},
		relations: map[int64]*repository.IssueRelation{}, vVisible: map[int64]bool{}}
}

// fail は最初のエラーを記録する（描画中のエラーは最後にまとめて扱う）。
func (l *issueLookup) fail(err error) {
	if err != nil && l.err == nil {
		l.err = err
		l.a.logger().Error("issue lookup", "err", err)
	}
}

func (l *issueLookup) L(key string, args ...any) string { return l.c.L(key, args...) }

func (l *issueLookup) loadMasters() {
	if l.projects != nil {
		return
	}
	l.projects = map[int64]*domain.Project{}
	ps, err := repository.ListProjects(l.ctx, l.a.DB)
	l.fail(err)
	for _, p := range ps {
		l.projects[p.ID] = p
	}
	l.trackers = map[int64]*domain.Tracker{}
	ts, err := repository.ListTrackers(l.ctx, l.a.DB)
	l.fail(err)
	l.trackerOrd = ts
	for _, t := range ts {
		l.trackers[t.ID] = t
	}
	l.statuses = map[int64]*domain.IssueStatus{}
	ss, err := repository.ListIssueStatuses(l.ctx, l.a.DB)
	l.fail(err)
	l.statusOrd = ss
	for _, s := range ss {
		l.statuses[s.ID] = s
	}
	l.priorities = map[int64]*domain.Enumeration{}
	es, err := repository.ListEnumerations(l.ctx, l.a.DB, domain.EnumIssuePriority, false)
	l.fail(err)
	l.prioOrd = es
	for _, e := range es {
		l.priorities[e.ID] = e
	}
}

func (l *issueLookup) project(id int64) *domain.Project {
	l.loadMasters()
	return l.projects[id]
}

func (l *issueLookup) tracker(id int64) *domain.Tracker {
	l.loadMasters()
	if t := l.trackers[id]; t != nil {
		return t
	}
	return &domain.Tracker{ID: id}
}

func (l *issueLookup) status(id int64) *domain.IssueStatus {
	l.loadMasters()
	if s := l.statuses[id]; s != nil {
		return s
	}
	return &domain.IssueStatus{ID: id}
}

func (l *issueLookup) priority(id int64) *domain.Enumeration {
	l.loadMasters()
	if p := l.priorities[id]; p != nil {
		return p
	}
	return &domain.Enumeration{ID: id}
}

// preloadPrincipals は ids のプリンシパルを読み込む。
func (l *issueLookup) preloadPrincipals(ids []int64) {
	var miss []int64
	for _, id := range ids {
		if _, ok := l.principals[id]; !ok && id != 0 {
			miss = append(miss, id)
		}
	}
	if len(miss) == 0 {
		return
	}
	m, err := repository.PrincipalsAsUsersByIDs(l.ctx, l.a.DB, miss)
	l.fail(err)
	for _, id := range miss {
		l.principals[id] = m[id]
	}
}

func (l *issueLookup) principal(id int64) *domain.User {
	if id == 0 {
		return nil
	}
	l.preloadPrincipals([]int64{id})
	return l.principals[id]
}

func (l *issueLookup) principalPtr(id *int64) *domain.User {
	if id == nil {
		return nil
	}
	return l.principal(*id)
}

func (l *issueLookup) category(id *int64) *repository.IssueCategory {
	if id == nil {
		return nil
	}
	if c, ok := l.categories[*id]; ok {
		return c
	}
	m, err := repository.IssueCategoriesByIDs(l.ctx, l.a.DB, []int64{*id})
	l.fail(err)
	l.categories[*id] = m[*id]
	return m[*id]
}

func (l *issueLookup) preloadVersions(ids []int64) {
	var miss []int64
	for _, id := range ids {
		if _, ok := l.versions[id]; !ok {
			miss = append(miss, id)
		}
	}
	if len(miss) == 0 {
		return
	}
	m, err := repository.VersionsByIDs(l.ctx, l.a.DB, miss)
	l.fail(err)
	for _, id := range miss {
		l.versions[id] = m[id]
	}
}

func (l *issueLookup) version(id *int64) *repository.Version {
	if id == nil {
		return nil
	}
	l.preloadVersions([]int64{*id})
	return l.versions[*id]
}

// preloadIssues は ids のチケットを読み込む（親・関連の相手など）。
func (l *issueLookup) preloadIssues(ids []int64) {
	var miss []int64
	for _, id := range ids {
		if _, ok := l.issues[id]; !ok {
			miss = append(miss, id)
		}
	}
	if len(miss) == 0 {
		return
	}
	m, err := repository.ReadIssuesByIDs(l.ctx, l.a.DB, miss)
	l.fail(err)
	for _, id := range miss {
		if r := m[id]; r != nil {
			l.issues[id] = issueRowFromRead(r)
		} else {
			l.issues[id] = nil
		}
	}
}

// addIssues は既に読み込んだ行をキャッシュに入れる。
func (l *issueLookup) addIssues(rows []*query.IssueRow) {
	for _, r := range rows {
		l.issues[r.ID] = r
	}
}

func (l *issueLookup) issue(id int64) *query.IssueRow {
	l.preloadIssues([]int64{id})
	return l.issues[id]
}

// preloadChildren は ids の leaf? 判定を読み込む。
func (l *issueLookup) preloadChildren(ids []int64) {
	var miss []int64
	for _, id := range ids {
		if _, ok := l.children[id]; !ok {
			miss = append(miss, id)
		}
	}
	if len(miss) == 0 {
		return
	}
	m, err := repository.IssueIDsWithChildren(l.ctx, l.a.DB, miss)
	l.fail(err)
	for _, id := range miss {
		l.children[id] = m[id]
	}
}

// hasChildren は !issue.leaf?。
func (l *issueLookup) hasChildren(id int64) bool {
	l.preloadChildren([]int64{id})
	return l.children[id]
}

// issueVisible は issue.visible?（User.current）。
func (l *issueLookup) issueVisible(r *query.IssueRow) bool {
	if r == nil {
		return false
	}
	if v, ok := l.visible[r.ID]; ok {
		return v
	}
	p := l.project(r.ProjectID)
	ok := false
	if p != nil {
		var err error
		ok, err = l.c.Authz().IssueVisible(l.ctx, &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
			StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, p)
		l.fail(err)
	}
	l.visible[r.ID] = ok
	return ok
}

// markVisible はクエリで取得した（可視であることが分かっている）チケットを記録する。
func (l *issueLookup) markVisible(rows []*query.IssueRow) {
	for _, r := range rows {
		l.visible[r.ID] = true
	}
}

// versionVisible は Version#visible?（user.allowed_to?(:view_issues, project)）。
func (l *issueLookup) versionVisible(v *repository.Version) bool {
	if ok, done := l.vVisible[v.ID]; done {
		return ok
	}
	ok := false
	if p := l.project(v.ProjectID); p != nil {
		ok = l.c.AllowedTo(domain.Perm("view_issues"), p)
	}
	l.vVisible[v.ID] = ok
	return ok
}

// customFields はチケットの CF（id → CF）。
func (l *issueLookup) customField(id int64) *customfield.CustomField {
	if l.cfs == nil {
		l.cfs = map[int64]*customfield.CustomField{}
		cfs, err := customfield.Load(l.ctx, l.a.DB, "1=1")
		l.fail(err)
		for _, cf := range cfs {
			l.cfs[cf.ID] = cf
		}
	}
	return l.cfs[id]
}

// cfVisibleBy は custom_field.visible_by?(project, User.current)。
func (l *issueLookup) cfVisibleBy(cf *customfield.CustomField, p *domain.Project) bool {
	var pid int64
	if p != nil {
		pid = p.ID
	}
	k := [2]int64{cf.ID, pid}
	if v, ok := l.cfVisible[k]; ok {
		return v
	}
	ok, err := customfield.VisibleBy(l.ctx, l.c.Authz(), cf, p)
	l.fail(err)
	l.cfVisible[k] = ok
	return ok
}

// ---------------------------------------------------------------- 変換

// issueRowFromRead は repository の行を query.IssueRow にする。
func issueRowFromRead(r *repository.ReadIssueRow) *query.IssueRow {
	row := &query.IssueRow{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID, StatusID: r.StatusID, PriorityID: r.PriorityID,
		AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, CategoryID: r.CategoryID, FixedVersionID: r.FixedVersionID,
		ParentID: r.ParentID, RootID: r.RootID, HierPath: r.HierPath, Subject: r.Subject,
		DoneRatio: r.DoneRatio, EstimatedHours: r.EstimatedHours, IsPrivate: r.IsPrivate, LockVersion: r.LockVersion,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, ClosedAt: r.ClosedAt.Ptr()}
	if r.Description != nil {
		row.Description = *r.Description
	}
	if r.StartDate.Valid {
		t := r.StartDate.Date.Time
		row.StartDate = &t
	}
	if r.DueDate.Valid {
		t := r.DueDate.Date.Time
		row.DueDate = &t
	}
	return row
}

// refIssue は link_to_issue / css_classes 用の redmine.Issue。
func (l *issueLookup) refIssue(r *query.IssueRow) *redmine.Issue {
	st := l.status(r.StatusID)
	pr := l.priority(r.PriorityID)
	is := &redmine.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID, TrackerName: l.tracker(r.TrackerID).Name,
		StatusID: r.StatusID, StatusName: st.Name, StatusClosed: st.IsClosed, PriorityID: r.PriorityID,
		Subject: r.Subject, AuthorID: r.AuthorID, HasChildren: l.hasChildren(r.ID), IsPrivate: r.IsPrivate,
		DoneRatio: l.doneRatio(r)}
	if p := l.project(r.ProjectID); p != nil {
		is.ProjectName = p.Name
	}
	if pr.PositionName != nil {
		is.PriorityPosName = sql.NullString{String: *pr.PositionName, Valid: true}
	}
	if r.AssignedToID != nil {
		is.AssignedToID = sql.NullInt64{Int64: *r.AssignedToID, Valid: true}
	}
	if r.ParentID != nil {
		is.ParentID = sql.NullInt64{Int64: *r.ParentID, Valid: true}
	}
	if r.StartDate != nil {
		is.StartDate = db.NullDate{Date: db.Date{Time: *r.StartDate}, Valid: true}
	}
	if r.DueDate != nil {
		is.DueDate = db.NullDate{Date: db.Date{Time: *r.DueDate}, Valid: true}
	}
	return is
}

// doneRatio は Issue#done_ratio（Setting.issue_done_ratio == 'issue_status' ならステータスの既定値）。
func (l *issueLookup) doneRatio(r *query.IssueRow) int {
	if l.a.Settings.String("issue_done_ratio") == "issue_status" {
		if st := l.status(r.StatusID); st.DefaultDoneRatio != nil {
			return *st.DefaultDoneRatio
		}
	}
	return r.DoneRatio
}

func (l *issueLookup) renderer() *redmine.Renderer { return l.a.Helpers.WikiRenderer(l.page) }

// cssClasses は issue.css_classes。
func (l *issueLookup) cssClasses(r *query.IssueRow) string {
	return l.renderer().IssueCSSClasses(l.refIssue(r))
}

// linkToIssue は link_to_issue(issue, options)。
func (l *issueLookup) linkToIssue(r *query.IssueRow, o redmine.LinkToIssueOptions) template.HTML {
	return l.renderer().LinkToIssue(l.refIssue(r), o)
}

// ---------------------------------------------------------------- HTML 部品

func (l *issueLookup) icon(name string, label any, opts ...any) template.HTML {
	var h *rails.Hash
	if len(opts) > 0 {
		h = rails.NewHash(opts...)
	}
	return l.a.Helpers.Icon(l.page, name, label, h)
}

// linkToPrincipal は link_to_principal(principal)。
func (l *issueLookup) linkToPrincipal(u *domain.User) template.HTML {
	return l.a.Helpers.LinkToPrincipal(l.page, u, "")
}

func (l *issueLookup) principalName(u *domain.User) string {
	return helper.PrincipalUserName(l.page, u)
}

// linkToVersion は link_to_version(version)。
func (l *issueLookup) linkToVersion(v *repository.Version) template.HTML {
	if v == nil {
		return ""
	}
	name := l.formatVersionName(v)
	// format_date(nil) は nil（title 属性を出さない）
	var title any
	if v.EffectiveDate.Valid {
		title = l.formatDate(v.EffectiveDate.Date.Time)
	}
	if !l.versionVisible(v) {
		return rails.H(name)
	}
	return rails.LinkTo(name, "/versions/"+strconv.FormatInt(v.ID, 10), rails.NewHash("title", title))
}

// formatVersionName は format_version_name(version)。
func (l *issueLookup) formatVersionName(v *repository.Version) string {
	if l.c.Project != nil && l.c.Project.ID == v.ProjectID {
		return v.Name
	}
	return v.ProjectName + " - " + v.Name
}

func (l *issueLookup) formatDate(t time.Time) string { return l.c.Loc.FormatDate(t) }

func (l *issueLookup) formatTime(t time.Time) string { return l.c.Loc.FormatTime(t, true) }

func (l *issueLookup) formatHours(v *float64) string {
	if v == nil {
		return ""
	}
	return l.c.Loc.FormatHours(*v)
}

func (l *issueLookup) yesNo(b bool) string {
	if b {
		return l.L("general_text_Yes")
	}
	return l.L("general_text_No")
}

// timeEntryVisibleCondition は TimeEntry.visible の条件（time_entries・projects を参照）。
func (l *issueLookup) timeEntryVisibleCondition() string {
	cond, err := l.c.Authz().AllowedToCondition(l.ctx, "view_time_entries", authz.ConditionOptions{}, func(role *domain.Role, user *domain.User) string {
		switch role.TimeEntriesVisibility {
		case domain.TimeEntriesVisibilityAll:
			return ""
		case domain.TimeEntriesVisibilityOwn:
			return "time_entries.user_id = " + strconv.FormatInt(user.ID, 10)
		}
		return "1=0"
	})
	l.fail(err)
	if cond == "" {
		return "1=0"
	}
	return cond
}

// sortPrincipals は Principal#<=>（ユーザーが先、名前の大文字小文字を無視した比較）で並べる。
func (l *issueLookup) sortPrincipals(us []*domain.User) {
	slices.SortStableFunc(us, func(a, b *domain.User) int {
		ag, bg := a.Kind.IsGroup(), b.Kind.IsGroup()
		if ag != bg {
			if ag {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(l.principalName(a)), strings.ToLower(l.principalName(b)))
	})
}
