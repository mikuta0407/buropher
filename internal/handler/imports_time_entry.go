// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"context"
	"errors"
	"html/template"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/csvimport"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/webhook"
)

// このファイルは TimeEntryImport（app/models/time_entry_import.rb）の移植。

// timeEntryImportState は TimeEntryImport のリクエスト内キャッシュ。
type timeEntryImportState struct {
	env      *timelog.Env
	projects []*domain.Project
	loaded   bool
	// projectsCache は issue_project の @projects_cache。
	projectsCache map[int64]*domain.Project
}

func (m *importModel) teState() *timeEntryImportState {
	if m.te == nil {
		env := &timelog.Env{Q: m.a.DB, Settings: m.a.Settings, User: m.c.User, Az: m.c.Authz(), Loc: m.c.Loc, Now: m.a.now}
		m.te = &timeEntryImportState{env: env, projectsCache: map[int64]*domain.Project{}}
	}
	return m.te
}

// teAllowedTargetProjects は allowed_target_projects（Project.allowed_to(user, :log_time).order(:lft)）。
func (m *importModel) teAllowedTargetProjects() ([]*domain.Project, error) {
	st := m.teState()
	if st.loaded {
		return st.projects, nil
	}
	ps, err := m.allowedProjects("log_time")
	if err != nil {
		return nil, err
	}
	ns, err := repository.ProjectNestedSet(m.c.Ctx(), m.a.DB)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ps, func(i, j int) bool { return ns[ps[i].ID].Lft < ns[ps[j].ID].Lft })
	st.projects, st.loaded = ps, true
	return ps, nil
}

// teProject は TimeEntryImport#project。
func (m *importModel) teProject() (*domain.Project, error) {
	ps, err := m.teAllowedTargetProjects()
	if err != nil {
		return nil, err
	}
	return importFindProject(ps, m.mappingValue("project_id")), nil
}

// teAllowedTargetActivities は allowed_target_activities（project.activities。sorted）。
func (m *importModel) teAllowedTargetActivities() ([]*domain.Enumeration, error) {
	p, err := m.teProject()
	if err != nil {
		return nil, err
	}
	if p == nil {
		// project が nil なら Redmine は NoMethodError になる
		return nil, nil
	}
	return m.teState().env.AvailableActivities(m.c.Ctx(), p)
}

// teActivity は TimeEntryImport#activity（mapping['activity'] が "value:<id>" のとき）。
func (m *importModel) teActivity() (*domain.Enumeration, error) {
	mm := importValueRe.FindStringSubmatch(importToS(m.mappingValue("activity")))
	if mm == nil {
		return nil, nil
	}
	id, _ := strconv.ParseInt(mm[1], 10, 64)
	acts, err := m.teAllowedTargetActivities()
	if err != nil {
		return nil, err
	}
	for _, a := range acts {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, nil
}

// teUserValue は user_value（mapping['user'] が "value:<id>" のとき）。
func (m *importModel) teUserValue() *int64 {
	mm := importValueRe.FindStringSubmatch(importToS(m.mappingValue("user")))
	if mm == nil {
		return nil
	}
	id, _ := strconv.ParseInt(mm[1], 10, 64)
	return &id
}

// teAllowedTargetUsers は allowed_target_users（log_time を持つ有効なメンバー（メンバー順）+ User.current）。
func (m *importModel) teAllowedTargetUsers() ([]*domain.User, error) {
	ctx := m.c.Ctx()
	var users []*domain.User
	p, err := m.teProject()
	if err != nil {
		return nil, err
	}
	if p != nil {
		var ids []int64
		if err := m.a.DB.Select(ctx, &ids, `SELECT members.principal_id FROM members JOIN principals p ON p.id = members.principal_id
WHERE members.project_id = ? AND p.kind = 'user' AND p.status = ? ORDER BY members.id`, p.ID, domain.StatusActive); err != nil {
			return nil, err
		}
		um, err := repository.UsersByIDs(ctx, m.a.DB, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			u := um[id]
			if u == nil {
				continue
			}
			ok, err := authz.New(m.a.DB, u).AllowedTo(ctx, domain.Perm("log_time"), p)
			if err != nil {
				return nil, err
			}
			if ok {
				users = append(users, u)
			}
		}
	}
	cur := m.c.User
	if cur.Logged() && !slices.ContainsFunc(users, func(u *domain.User) bool { return u.ID == cur.ID }) {
		users = append(users, cur)
	}
	return users, nil
}

// teAllowedTo は user.allowed_to?(perm, project)。
func (m *importModel) teAllowedTo(perm string) bool {
	p, err := m.teProject()
	if err != nil || p == nil {
		return false
	}
	ok, err := authz.New(m.a.DB, m.user).AllowedTo(m.c.Ctx(), domain.Perm(perm), p)
	return err == nil && ok
}

// timeEntryBuildAndSave は TimeEntryImport#build_object と object.save。
func (m *importModel) timeEntryBuildAndSave(ctx context.Context, row csvimport.Row, item *repository.ImportItem) (importResult, error) {
	st := m.teState()
	env := st.env
	project, err := m.teProject()
	if err != nil {
		return importResult{}, err
	}
	t, err := env.New(ctx, nil, nil)
	if err != nil {
		return importResult{}, err
	}
	t.AuthorID = &m.user.ID

	var activityID any
	if act, err := m.teActivity(); err != nil {
		return importResult{}, err
	} else if act != nil {
		activityID = act.ID
	} else if name := m.rowValue(row, "activity"); name != nil {
		acts, err := m.teAllowedTargetActivities()
		if err != nil {
			return importResult{}, err
		}
		for _, a := range acts {
			if importNamed(a.Name, *name) {
				activityID = a.ID
				break
			}
		}
	}

	var userID any
	if m.teAllowedTo("log_time_for_other_users") {
		if uv := m.teUserValue(); uv != nil {
			userID = *uv
		} else if name := m.rowValue(row, "user"); name != nil {
			users, err := m.teAllowedTargetUsers()
			if err != nil {
				return importResult{}, err
			}
			if p := detectByKeyword(m.keywordPrincipalsFromUsers(users), *name); p != nil {
				userID = p.ID
			}
		}
	} else {
		userID = m.user.ID
	}

	attrs := httpx.NewParams()
	attrs.Set("activity_id", activityID)
	attrs.Set("author_id", m.user.ID)
	attrs.Set("user_id", userID)
	attrs.Set("spent_on", strPtrAny(m.rowDate(row, "spent_on")))
	attrs.Set("hours", strPtrAny(m.rowValue(row, "hours")))
	attrs.Set("comments", strPtrAny(m.rowValue(row, "comments")))

	var objProject *domain.Project
	if issueID := m.rowValue(row, "issue_id"); issueID != nil {
		attrs.Set("issue_id", *issueID)
		if objProject, err = m.teIssueProject(ctx, *issueID); err != nil {
			return importResult{}, err
		}
	} else {
		var pid any
		if project != nil {
			pid = project.ID
		}
		attrs.Set("project_id", pid)
		objProject = project
	}
	t.ProjectID = nil
	if objProject != nil {
		id := objProject.ID
		t.ProjectID = &id
	}

	cfs, err := env.CustomFieldValues(ctx, t)
	if err != nil {
		return importResult{}, err
	}
	cfAttrs := httpx.NewParams()
	for _, v := range cfs {
		key := "cf_" + strconv.FormatInt(v.Field.ID, 10)
		var value *string
		if v.Field.FieldFormat == "date" {
			value = m.rowDate(row, key)
		} else {
			value = m.rowValue(row, key)
		}
		if value == nil {
			continue
		}
		full, err := customfield.Get(ctx, m.a.DB, v.Field.ID)
		if err != nil {
			return importResult{}, err
		}
		if full == nil {
			continue
		}
		cz := &customfield.Customized{Kind: "time_entry"}
		if objProject != nil {
			cz.ProjectID, cz.ProjectIdentifier = objProject.ID, objProject.Identifier
		}
		val := m.cfValueFromKeyword(ctx, full, *value, cz)
		switch x := val.(type) {
		case []string:
			arr := make([]any, len(x))
			for i, s := range x {
				arr[i] = s
			}
			cfAttrs.Set(strconv.FormatInt(v.Field.ID, 10), arr)
		default:
			cfAttrs.Set(strconv.FormatInt(v.Field.ID, 10), x)
		}
	}
	attrs.Set("custom_field_values", cfAttrs)
	if err := env.SafeAssign(ctx, t, attrs, m.user); err != nil {
		return importResult{}, err
	}
	saved, err := env.Save(ctx, t)
	if err != nil {
		return importResult{}, err
	}
	if !saved {
		return importResult{obj: t, message: strings.Join(t.Errors.FullMessages(m.c.Loc), "\n")}, nil
	}
	m.a.TriggerWebhook(ctx, webhook.TypeTimeEntry, webhook.ActionCreated, t.ID)
	return importResult{obj: t, persisted: true, objID: t.ID}, nil
}

// teIssueProject は issue_project(issue_id)（チケットのプロジェクトが allowed_target_projects に含まれる場合のみ）。
func (m *importModel) teIssueProject(ctx context.Context, issueID string) (*domain.Project, error) {
	id := rubyStrToI(issueID)
	var pid int64
	err := m.a.DB.Get(ctx, &pid, `SELECT project_id FROM issues WHERE id = ? LIMIT 1`, id)
	if errors.Is(err, db.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := m.teState()
	if p, ok := st.projectsCache[pid]; ok {
		return p, nil
	}
	ps, err := m.teAllowedTargetProjects()
	if err != nil {
		return nil, err
	}
	var found *domain.Project
	for _, p := range ps {
		if p.ID == pid {
			found = p
		}
	}
	if found != nil {
		st.projectsCache[pid] = found
	}
	return found, nil
}

// cfsByKind は TimeEntryCustomField.all / UserCustomField.all（id 順）。
func (m *importModel) cfsByKind(kind string) ([]importCF, error) {
	cfs, err := customfield.ListByKind(m.c.Ctx(), m.a.DB, customfield.OwnerKind(kind))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(cfs, func(i, j int) bool { return cfs[i].ID < cfs[j].ID })
	out := make([]importCF, 0, len(cfs))
	for _, cf := range cfs {
		out = append(out, importCF{ID: cf.ID, Name: cf.Name, IsRequired: cf.IsRequired, Format: cf.FieldFormat})
	}
	return out, nil
}

// importTimeEntryMappingData は _time_entries_fields_mapping の値。
func (m *importModel) importTimeEntryMappingData(data map[string]any) error {
	ps, err := m.teAllowedTargetProjects()
	if err != nil {
		return err
	}
	p, err := m.teProject()
	if err != nil {
		return err
	}
	acts, err := m.teAllowedTargetActivities()
	if err != nil {
		return err
	}
	var actValues []any
	for _, a := range acts {
		actValues = append(actValues, []any{a.Name, a.ID})
	}
	data["Projects"] = ps
	data["Project"] = p
	data["ActivityValues"] = actValues
	canOther := m.c.User != nil && p != nil && m.c.AllowedTo(domain.Perm("log_time_for_other_users"), p)
	data["LogTimeForOtherUsers"] = canOther
	if canOther {
		users, err := m.teAllowedTargetUsers()
		if err != nil {
			return err
		}
		format := m.a.Settings.String("user_format")
		var vals []any
		for _, u := range users {
			vals = append(vals, []any{u.Name(format), u.ID})
		}
		data["UserValues"] = vals
		data["UserDefault"] = "value:" + strconv.FormatInt(m.c.User.ID, 10)
	}
	return nil
}

// importTimeEntryRow は _time_entries_saved_objects の 1 行。
type importTimeEntryRow struct {
	Project  *domain.Project
	User     *domain.User
	Activity string
	Issue    template.HTML
	SpentOn  string
	Hours    string
	Comments string
}

// importSavedTimeEntries は saved_objects（TimeEntry.where(:id => ids).order(:id)）の表示用の行。
func (a *App) importSavedTimeEntries(c *Req, ids []int64) ([]importTimeEntryRow, error) {
	ctx := c.Ctx()
	env := &timelog.Env{Q: a.DB, Settings: a.Settings, User: c.User, Az: c.Authz(), Loc: c.Loc, Now: a.now}
	l := a.newIssueLookup(c)
	l.loadMasters()
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	var issueIDs []int64
	var entries []*timelog.Entry
	for _, id := range sorted {
		t, err := env.Find(ctx, id)
		if err != nil {
			continue
		}
		entries = append(entries, t)
		if t.IssueID != nil {
			issueIDs = append(issueIDs, *t.IssueID)
		}
	}
	l.preloadIssues(issueIDs)
	l.preloadChildren(issueIDs)
	var rows []importTimeEntryRow
	for _, t := range entries {
		r := importTimeEntryRow{}
		if t.ProjectID != nil {
			r.Project = l.project(*t.ProjectID)
		}
		if t.UserID != nil {
			r.User = l.principal(*t.UserID)
		}
		if t.ActivityID != nil {
			if e, err := repository.GetEnumeration(ctx, a.DB, *t.ActivityID); err == nil && e != nil {
				r.Activity = e.Name
			}
		}
		if t.IssueID != nil {
			if is := l.issue(*t.IssueID); is != nil {
				r.Issue = l.linkToIssue(is, redmine.LinkToIssueOptions{})
			}
		}
		if t.SpentOn != nil {
			r.SpentOn = c.Loc.FormatDate(*t.SpentOn)
		}
		if h := t.RoundedHours(); h != nil {
			r.Hours = c.Loc.LHoursShort(*h)
		}
		if t.Comments != nil {
			r.Comments = *t.Comments
		}
		rows = append(rows, r)
	}
	return rows, l.err
}
