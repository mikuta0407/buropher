package handler

import (
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは工数一覧（timelog/_list）の行の描画（QueriesHelper#column_content / csv_content の
// TimeEntryQuery 部分）と、関連（プロジェクト・ユーザー・作業分類・チケット）の読み込み。

// teLookup は工数の関連レコードのキャッシュ。
type teLookup struct {
	a          *App
	c          *Req
	projects   map[int64]*domain.Project
	users      map[int64]*domain.User
	activities map[int64]*domain.Enumeration
	// issues は存在するチケット（可視性を問わない）、visibleIssues は User.current に見えるもの。
	issues        map[int64]*repository.RefIssue
	visibleIssues map[int64]bool
	extras        map[int64]*repository.TimelogIssueExtra
	// cvs は customized_kind → 所有者 id → custom_field_id → 値。
	cvs map[string]map[int64]map[int64][]string
}

// newTELookup は entries の関連を読み込む。
func (a *App) newTELookup(c *Req, entries []*timelog.Entry) (*teLookup, error) {
	ctx := c.Ctx()
	l := &teLookup{a: a, c: c, cvs: map[string]map[int64]map[int64][]string{}}
	var pids, uids, aids, iids []int64
	for _, t := range entries {
		if t.ProjectID != nil {
			pids = append(pids, *t.ProjectID)
		}
		if t.UserID != nil {
			uids = append(uids, *t.UserID)
		}
		if t.AuthorID != nil {
			uids = append(uids, *t.AuthorID)
		}
		if t.ActivityID != nil {
			aids = append(aids, *t.ActivityID)
		}
		if t.IssueID != nil {
			iids = append(iids, *t.IssueID)
		}
	}
	var err error
	if l.projects, err = repository.ProjectsByIDs(ctx, a.DB, pids); err != nil {
		return nil, err
	}
	if l.users, err = repository.UsersByIDs(ctx, a.DB, uids); err != nil {
		return nil, err
	}
	if l.activities, err = repository.TimelogActivitiesByIDs(ctx, a.DB, aids); err != nil {
		return nil, err
	}
	if l.issues, err = repository.TimelogRefIssues(ctx, a.DB, iids, ""); err != nil {
		return nil, err
	}
	var parentIDs []int64
	for _, is := range l.issues {
		if is.ParentID.Valid {
			parentIDs = append(parentIDs, is.ParentID.Int64)
		}
	}
	if len(parentIDs) > 0 {
		ps, err := repository.TimelogRefIssues(ctx, a.DB, parentIDs, "")
		if err != nil {
			return nil, err
		}
		for id, is := range ps {
			if l.issues[id] == nil {
				l.issues[id] = is
			}
		}
	}
	l.visibleIssues = map[int64]bool{}
	if len(l.issues) > 0 {
		cond, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{})
		if err != nil {
			return nil, err
		}
		var all []int64
		for id := range l.issues {
			all = append(all, id)
		}
		vis, err := repository.TimelogRefIssues(ctx, a.DB, all, cond)
		if err != nil {
			return nil, err
		}
		for id := range vis {
			l.visibleIssues[id] = true
		}
	}
	if l.extras, err = repository.TimelogIssueExtras(ctx, a.DB, iids); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *teLookup) project(id *int64) *domain.Project {
	if id == nil {
		return nil
	}
	return l.projects[*id]
}

func (l *teLookup) user(id *int64) *domain.User {
	if id == nil {
		return nil
	}
	return l.users[*id]
}

func (l *teLookup) issueExists(id int64) bool { return l.issues[id] != nil }

func (l *teLookup) page() *helper.Page { return l.c.Page() }

// linkToIssue は link_to_issue(issue, opts)。
func (l *teLookup) linkToIssue(is *repository.RefIssue, o redmine.LinkToIssueOptions) template.HTML {
	return l.a.Helpers.WikiRenderer(l.page()).LinkToIssue(is, o)
}

// formatIssue は format_object(issue)（見えなければ "#id"）。
func (l *teLookup) formatIssue(id int64, html bool) any {
	is := l.issues[id]
	if is == nil {
		return ""
	}
	if !l.visibleIssues[id] {
		return "#" + strconv.FormatInt(id, 10)
	}
	if html {
		return l.linkToIssue(is, redmine.LinkToIssueOptions{})
	}
	return is.TrackerName + " #" + strconv.FormatInt(id, 10) + ": " + is.Subject
}

// formatUser は format_object(user)。
func (l *teLookup) formatUser(u *domain.User, html bool) any {
	if u == nil {
		return ""
	}
	if html {
		return l.a.Helpers.LinkToPrincipal(l.page(), u, "")
	}
	return helper.PrincipalName(l.page(), u)
}

// formatProject は format_object(project)。
func (l *teLookup) formatProject(p *domain.Project, html bool) any {
	if p == nil {
		return ""
	}
	if html {
		return helper.LinkToProject(p)
	}
	return p.Name
}

// formatVersion は format_object(version)（link_to_version）。
func (l *teLookup) formatVersion(x *repository.TimelogIssueExtra, html bool) any {
	if x == nil || !x.FixedVersionID.Valid {
		return ""
	}
	name := x.VersionName.String
	if !html {
		return x.VersionProjectName.String + " - " + name
	}
	if l.c.Project == nil || l.c.Project.ID != x.VersionProjectID.Int64 {
		name = x.VersionProjectName.String + " - " + name
	}
	var title any
	if x.VersionDate.Valid {
		title = helper.FormatDate(l.page(), x.VersionDate.Date.Time)
	}
	return rails.LinkTo(name, "/versions/"+strconv.FormatInt(x.FixedVersionID.Int64, 10), rails.NewHash("title", title))
}

// ---------------------------------------------------------------- カスタムフィールド列

func (l *teLookup) loadCFValues(kind string, ids []int64) error {
	if _, ok := l.cvs[kind]; ok {
		return nil
	}
	m, err := repository.TimelogCustomValues(l.c.Ctx(), l.a.DB, kind, ids)
	if err != nil {
		return err
	}
	l.cvs[kind] = m
	return nil
}

// prepareColumns は列に必要なカスタム値を読み込む。
func (l *teLookup) prepareColumns(cols []*query.Column, entries []*timelog.Entry) error {
	for _, col := range cols {
		switch col.Kind {
		case query.ColumnCustomField:
			var ids []int64
			for _, t := range entries {
				ids = append(ids, t.ID)
			}
			if err := l.loadCFValues("time_entry", ids); err != nil {
				return err
			}
		case query.ColumnAssocCF:
			var ids []int64
			kind := col.Association
			for _, t := range entries {
				switch kind {
				case "issue":
					if t.IssueID != nil {
						ids = append(ids, *t.IssueID)
					}
				case "project":
					if t.ProjectID != nil {
						ids = append(ids, *t.ProjectID)
					}
				case "user":
					if t.UserID != nil {
						ids = append(ids, *t.UserID)
					}
				}
			}
			if kind == "user" {
				kind = "principal"
			}
			if err := l.loadCFValues(kind, ids); err != nil {
				return err
			}
		}
	}
	return nil
}

// cfValuesOf は列の値（カスタム値の文字列。所有者が無ければ ok = false）。
func (l *teLookup) cfValuesOf(col *query.Column, t *timelog.Entry) ([]string, bool) {
	var kind string
	var id *int64
	switch col.Kind {
	case query.ColumnCustomField:
		kind, id = "time_entry", &t.ID
	case query.ColumnAssocCF:
		switch col.Association {
		case "issue":
			kind, id = "issue", t.IssueID
		case "project":
			kind, id = "project", t.ProjectID
		case "user":
			kind, id = "principal", t.UserID
		}
	}
	if id == nil {
		return nil, false
	}
	m := l.cvs[kind][*id]
	if m == nil {
		return nil, true
	}
	return m[col.CustomField.ID], true
}

// formatCF は format_object(custom_value) を値ごとに行い ", " で連結する。
func (l *teLookup) formatCF(cf *customfield.CustomField, vals []string, html bool) any {
	env := &customfield.Env{T: l.c.L, CurrentUserID: l.c.User.ID,
		FormatObject: func(v any, h bool) any { return l.formatPlain(v, h) }}
	f := customfield.FindFormat(cf.FieldFormat)
	var raw any
	if cf.Multiple {
		arr := make([]any, len(vals))
		for i, v := range vals {
			arr[i] = v
		}
		raw = arr
	} else if len(vals) > 0 {
		raw = vals[0]
	}
	out := f.FormattedValue(env, cf, raw, nil, html)
	if h, ok := out.(rails.HTML); ok {
		return template.HTML(h)
	}
	return l.formatPlain(out, html)
}

// formatPlain は format_object（キャスト済みの値）。
func (l *teLookup) formatPlain(v any, html bool) any {
	switch x := v.(type) {
	case nil:
		return ""
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			s := l.formatPlain(e, html)
			if html {
				parts = append(parts, string(rails.H(s)))
			} else {
				parts = append(parts, rails.ToS(s))
			}
		}
		if html {
			return template.HTML(strings.Join(parts, ", "))
		}
		return strings.Join(parts, ", ")
	case bool:
		if x {
			return l.c.L("general_text_Yes")
		}
		return l.c.L("general_text_No")
	case time.Time:
		return helper.FormatDate(l.page(), x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', 2, 64)
	case customfield.Option:
		return x.Label
	}
	return rails.ToS(v)
}

// ---------------------------------------------------------------- column_content

// columnContent は column_content(column, entry)。
func (l *teLookup) columnContent(col *query.Column, t *timelog.Entry) any {
	return l.columnValue(col, t, true)
}

// csvContent は csv_content(column, entry)。
func (l *teLookup) csvContent(col *query.Column, t *timelog.Entry) string {
	return rails.ToS(l.columnValue(col, t, false))
}

func (l *teLookup) columnValue(col *query.Column, t *timelog.Entry, html bool) any {
	page := l.page()
	var issue *repository.RefIssue
	if t.IssueID != nil {
		issue = l.issues[*t.IssueID]
	}
	switch col.Kind {
	case query.ColumnCustomField, query.ColumnAssocCF:
		vals, ok := l.cvValues(col, t)
		if !ok {
			return ""
		}
		return l.formatCF(col.CustomField, vals, html)
	}
	switch col.Name {
	case "project":
		return l.formatProject(l.project(t.ProjectID), html)
	case "spent_on":
		if t.SpentOn == nil {
			return ""
		}
		return helper.FormatDate(page, *t.SpentOn)
	case "created_on":
		return helper.FormatTime(page, t.CreatedAt, true)
	case "tweek":
		if t.SpentOn == nil {
			return ""
		}
		_, w := t.SpentOn.ISOWeek()
		return strconv.Itoa(w)
	case "author":
		return l.formatUser(l.user(t.AuthorID), html)
	case "user":
		return l.formatUser(l.user(t.UserID), html)
	case "activity":
		if t.ActivityID != nil {
			if a := l.activities[*t.ActivityID]; a != nil {
				return a.Name
			}
		}
		return ""
	case "issue":
		if t.IssueID == nil {
			return ""
		}
		return l.formatIssue(*t.IssueID, html)
	case "issue.tracker":
		if issue == nil {
			return ""
		}
		return issue.TrackerName
	case "issue.status":
		if issue == nil {
			return ""
		}
		return issue.StatusName
	case "issue.parent":
		if issue == nil || !issue.ParentID.Valid {
			return ""
		}
		pid := issue.ParentID.Int64
		parent := l.issues[pid]
		if parent == nil {
			return ""
		}
		if !html {
			if l.visibleIssues[pid] {
				return parent.TrackerName + " #" + strconv.FormatInt(pid, 10) + ": " + parent.Subject
			}
			return "#" + strconv.FormatInt(pid, 10)
		}
		if l.visibleIssues[pid] {
			return l.linkToIssue(parent, redmine.LinkToIssueOptions{NoSubject: true})
		}
		return "#" + strconv.FormatInt(pid, 10)
	case "issue.category":
		if issue == nil {
			return ""
		}
		if x := l.extras[issue.ID]; x != nil && x.CategoryName.Valid {
			return x.CategoryName.String
		}
		return ""
	case "issue.fixed_version":
		if issue == nil {
			return ""
		}
		return l.formatVersion(l.extras[issue.ID], html)
	case "comments":
		return t.CommentsString()
	case "hours":
		h := t.RoundedHours()
		if h == nil {
			return ""
		}
		if html {
			return l.c.Loc.FormatHours(*h)
		}
		return strings.ReplaceAll(strconv.FormatFloat(*h, 'f', 2, 64), ".", l.c.L("general_csv_decimal_separator"))
	}
	return ""
}

func (l *teLookup) cvValues(col *query.Column, t *timelog.Entry) ([]string, bool) {
	return l.cfValuesOf(col, t)
}

// groupValue は group_by_column.group_value(entry) を GroupKey と format_object 済みの名前にする。
func (l *teLookup) groupValue(col *query.Column, t *timelog.Entry) (query.GroupKey, any) {
	switch col.Kind {
	case query.ColumnCustomField:
		vals, _ := l.cfValuesOf(col, t)
		if len(vals) == 0 || vals[0] == "" {
			return query.GroupKey{Null: true}, nil
		}
		s := vals[0]
		cast := col.CustomField.CastValue(&s)
		if cast == nil {
			return query.GroupKey{Null: true}, nil
		}
		return query.GroupKey{Value: teFormatCast(cast)}, l.formatCF(col.CustomField, vals, true)
	}
	id := func(p *int64) query.GroupKey {
		if p == nil {
			return query.GroupKey{Null: true}
		}
		return query.GroupKey{Value: strconv.FormatInt(*p, 10)}
	}
	switch col.Name {
	case "project":
		return id(t.ProjectID), l.formatProject(l.project(t.ProjectID), true)
	case "user":
		return id(t.UserID), l.formatUser(l.user(t.UserID), true)
	case "activity":
		var name any
		if t.ActivityID != nil && l.activities[*t.ActivityID] != nil {
			name = l.activities[*t.ActivityID].Name
		}
		return id(t.ActivityID), name
	case "issue":
		if t.IssueID == nil {
			return query.GroupKey{Null: true}, nil
		}
		return id(t.IssueID), l.formatIssue(*t.IssueID, true)
	case "spent_on":
		if t.SpentOn == nil {
			return query.GroupKey{Null: true}, nil
		}
		return query.GroupKey{Value: t.SpentOn.Format("2006-01-02")}, helper.FormatDate(l.page(), *t.SpentOn)
	}
	return query.GroupKey{Null: true}, nil
}

func teFormatCast(v any) string {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		s := strconv.FormatFloat(x, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	case string:
		return x
	}
	return rails.ToS(v)
}
