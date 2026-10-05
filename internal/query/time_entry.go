// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
)

// timeEntryKind は TimeEntryQuery。
type timeEntryKind struct{}

// timeEntryVisibleCondition は TimeEntry.visible_condition(user, options)。
func timeEntryVisibleCondition(ctx context.Context, a *authz.Authorizer, opts authz.ConditionOptions) (string, error) {
	return a.AllowedToCondition(ctx, "view_time_entries", opts, func(role *domain.Role, user *domain.User) string {
		switch {
		case role.TimeEntriesVisibility == domain.TimeEntriesVisibilityAll:
			return ""
		case role.TimeEntriesVisibility == domain.TimeEntriesVisibilityOwn && user.ID != 0 && user.Logged():
			return "time_entries.user_id = " + itoa(user.ID)
		}
		return "1=0"
	})
}

func (timeEntryKind) queriedTable() string    { return "time_entries" }
func (timeEntryKind) customizedTable() string { return "time_entries" }
func (timeEntryKind) customizedKind() string  { return "time_entry" }
func (timeEntryKind) viewPermission() string  { return "view_time_entries" }

func (timeEntryKind) defaultFilters() *Filters {
	f := NewFilters()
	f.Set("spent_on", Filter{Operator: "*", Values: []string{}})
	return f
}

func (timeEntryKind) defaultSortCriteria() SortCriteria     { return SortCriteria{{"spent_on", "desc"}} }
func (timeEntryKind) availableDisplayTypes(*Query) []string { return []string{"list"} }
func (timeEntryKind) defaultDisplayType(*Query) string      { return "list" }

func (timeEntryKind) defaultColumnNames(q *Query) []string {
	cols := q.env.settingHashStrings("time_entry_list_defaults", "column_names")
	if q.Project != nil {
		return cols
	}
	return unionStrings([]string{"project"}, cols)
}

func (timeEntryKind) defaultTotalableNames(q *Query) []string {
	return q.env.settingHashStrings("time_entry_list_defaults", "totalable_names")
}

func (timeEntryKind) columnFor(field string) (string, string) {
	return "time_entries", renameTimestamp(field)
}

func (timeEntryKind) initializeAvailableFilters(ctx context.Context, q *Query) error {
	e := q.env
	attrOfIssue := func(key string) string {
		return e.l("label_attribute_of_issue", map[string]any{"name": e.l(key)})
	}
	q.addAvailableFilter("spent_on", filterOpt{Type: "date_past"})
	if q.Project == nil {
		q.addAvailableFilter("project_id", filterOpt{Type: "list", ValuesFunc: q.projectValues})
	}
	leaf, err := q.projectIsLeaf(ctx)
	if err != nil {
		return err
	}
	if q.Project != nil && !leaf {
		q.addAvailableFilter("subproject_id", filterOpt{Type: "list_subprojects", ValuesFunc: q.subprojectValues})
	}
	q.addAvailableFilter("issue_id", filterOpt{Type: "tree", Label: "label_issue"})
	q.addAvailableFilter("issue.tracker_id", filterOpt{Type: "list", Name: attrOfIssue("field_tracker"),
		ValuesFunc: func(ctx context.Context) ([]Option, error) {
			ts, err := q.trackers(ctx)
			if err != nil {
				return nil, err
			}
			out := []Option{}
			for _, t := range ts {
				out = append(out, Option{Label: t.Name, Value: itoa(t.ID)})
			}
			return out, nil
		}})
	q.addAvailableFilter("issue.parent_id", filterOpt{Type: "tree", Name: attrOfIssue("field_parent_issue")})
	q.addAvailableFilter("issue.status_id", filterOpt{Type: "list", Name: attrOfIssue("field_status"), ValuesFunc: q.issueStatusesValues})
	q.addAvailableFilter("issue.fixed_version_id", filterOpt{Type: "list", Name: attrOfIssue("field_fixed_version"), ValuesFunc: q.fixedVersionValues})
	if q.Project != nil {
		q.addAvailableFilter("issue.category_id", filterOpt{Type: "list_optional", Name: attrOfIssue("field_category"), ValuesFunc: q.categoryValues})
	}
	q.addAvailableFilter("issue.subject", filterOpt{Type: "text", Name: attrOfIssue("field_subject")})
	q.addAvailableFilter("user_id", filterOpt{Type: "list_optional", ValuesFunc: q.authorValues})
	q.addAvailableFilter("user.group", filterOpt{Type: "list_optional", ValuesFunc: q.groupValues,
		Name: e.l("label_attribute_of_user", map[string]any{"name": e.l("label_group")})})
	q.addAvailableFilter("user.role", filterOpt{Type: "list_optional", ValuesFunc: q.roleValues,
		Name: e.l("label_attribute_of_user", map[string]any{"name": e.l("field_role")})})
	q.addAvailableFilter("author_id", filterOpt{Type: "list_optional", ValuesFunc: q.authorValues})
	av, err := q.timeEntryActivityValues(ctx)
	if err != nil {
		return err
	}
	q.addAvailableFilter("activity_id", filterOpt{Type: "list", Values: av})
	if q.Project == nil || !leaf {
		q.addAvailableFilter("project.status", filterOpt{Type: "list", ValuesFunc: q.projectStatusesValues,
			Name: e.l("label_attribute_of_project", map[string]any{"name": e.l("field_status")})})
	}
	q.addAvailableFilter("comments", filterOpt{Type: "text"})
	q.addAvailableFilter("hours", filterOpt{Type: "hour"})
	tcfs, err := q.visibleFilterCustomFields(ctx, "custom_fields.owner_kind = 'time_entry'")
	if err != nil {
		return err
	}
	if err := q.addCustomFieldsFilters(ctx, tcfs, ""); err != nil {
		return err
	}
	if err := q.addAssociationsCustomFieldsFilters(ctx, "project"); err != nil {
		return err
	}
	where, args, err := q.issueCustomFieldsWhere(ctx)
	if err != nil {
		return err
	}
	icfs, err := q.visibleFilterCustomFields(ctx, where, args...)
	if err != nil {
		return err
	}
	if err := q.addCustomFieldsFilters(ctx, icfs, "issue"); err != nil {
		return err
	}
	return q.addAssociationsCustomFieldsFilters(ctx, "user")
}

func (timeEntryKind) availableColumns(ctx context.Context, q *Query) ([]*Column, error) {
	uf := q.env.setting("user_format")
	cols := []*Column{
		newColumn("project", ColumnPlain, colOpt{sortable: []string{"projects.name"}, groupable: true, groupSQL: "time_entries.project_id", groupAssoc: true}),
		newColumn("spent_on", ColumnPlain, colOpt{sortable: []string{"time_entries.spent_on", "time_entries.created_at"}, defaultOrder: "desc", groupable: true, groupSQL: "time_entries.spent_on"}),
		newColumn("created_on", ColumnTimestamp, colOpt{sortable: []string{"time_entries.created_at"}, defaultOrder: "desc", groupable: q.timestampGroupable(), groupSQL: q.timestampToDate("time_entries.created_at")}),
		newColumn("tweek", ColumnPlain, colOpt{sortable: []string{"time_entries.tyear", "time_entries.tweek"}, caption: "label_week"}),
		newColumn("author", ColumnPlain, colOpt{sortable: customfield.UserOrderFields("users", uf)}),
		newColumn("user", ColumnPlain, colOpt{sortable: customfield.UserOrderFields("users", uf), groupable: true, groupSQL: "time_entries.user_id", groupAssoc: true}),
		newColumn("activity", ColumnPlain, colOpt{sortable: []string{"time_entry_activities.position"}, groupable: true, groupSQL: "time_entries.activity_id", groupAssoc: true}),
		newColumn("issue", ColumnPlain, colOpt{sortable: []string{"issues.id"}, groupable: true, groupSQL: "time_entries.issue_id", groupAssoc: true}),
		{Name: "issue.tracker", Kind: ColumnAssociation, Association: "issue", Attribute: "tracker", Inline: true, CaptionKey: "field_tracker", Sortable: []string{"trackers.position"}},
		{Name: "issue.parent", Kind: ColumnAssociation, Association: "issue", Attribute: "parent", Inline: true, CaptionKey: "field_parent_issue",
			Sortable: []string{"issues.root_id", "issues.hier_path ASC"}, DefaultOrder: "desc"},
		{Name: "issue.status", Kind: ColumnAssociation, Association: "issue", Attribute: "status", Inline: true, CaptionKey: "field_status", Sortable: []string{"issue_statuses.position"}},
		{Name: "issue.category", Kind: ColumnAssociation, Association: "issue", Attribute: "category", Inline: true, CaptionKey: "field_category", Sortable: []string{"issue_categories.name"}},
		{Name: "issue.fixed_version", Kind: ColumnAssociation, Association: "issue", Attribute: "fixed_version", Inline: true, CaptionKey: "field_fixed_version", Sortable: customfield.VersionOrderFields("versions")},
		newColumn("comments", ColumnPlain, colOpt{}),
		newColumn("hours", ColumnPlain, colOpt{sortable: []string{"time_entries.hours"}, totalable: true}),
	}
	tcfs, err := q.visibleCustomFields(ctx, "custom_fields.owner_kind = 'time_entry'")
	if err != nil {
		return nil, err
	}
	for _, cf := range tcfs {
		cols = append(cols, q.newCustomFieldColumn(cf, nil))
	}
	where, args, err := q.issueCustomFieldsWhere(ctx)
	if err != nil {
		return nil, err
	}
	icfs, err := q.visibleCustomFields(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	f := false
	for _, cf := range icfs {
		cols = append(cols, q.newAssocCustomFieldColumn("issue", cf, &f))
	}
	pcfs, err := q.visibleCustomFields(ctx, "custom_fields.owner_kind = 'project'")
	if err != nil {
		return nil, err
	}
	for _, cf := range pcfs {
		cols = append(cols, q.newAssocCustomFieldColumn("project", cf, nil))
	}
	return cols, nil
}

func (timeEntryKind) baseScope(ctx context.Context, q *Query) (string, frag, error) {
	vis, err := timeEntryVisibleCondition(ctx, q.env.Auth, authz.ConditionOptions{})
	if err != nil {
		return "", frag{}, err
	}
	ivis, err := q.env.Auth.IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return "", frag{}, err
	}
	st, err := q.statement(ctx)
	if err != nil {
		return "", frag{}, err
	}
	from := "time_entries INNER JOIN projects ON projects.id = time_entries.project_id" +
		" INNER JOIN principals users ON users.id = time_entries.user_id" +
		teActivityJoin + teIssueJoinPrefix + ivis + ")"
	return from, joinFrags(" AND ", raw("("+vis+")"), paren(st)), nil
}

// base_scope の includes(:activity) と left_join_issue の結合 (teIssueJoinPrefix は FROM の末尾に置く)。
const (
	teActivityJoin    = " LEFT OUTER JOIN time_entry_activities ON time_entry_activities.id = time_entries.activity_id"
	teIssueJoinPrefix = " LEFT OUTER JOIN issues ON issues.id = time_entries.issue_id AND ("
)

// pruneJoins は件数・合計で参照されていない活動・チケットの LEFT OUTER JOIN を除く。
// どちらも主キーへの結合なので作業時間の行は増減しない。
func (timeEntryKind) pruneJoins(from, used string) string {
	if !strings.Contains(used, "issues.") && !strings.Contains(used, "issues ") {
		if i := strings.Index(from, teIssueJoinPrefix); i >= 0 {
			from = from[:i]
		}
	}
	if !strings.Contains(used, "time_entry_activities") {
		from = strings.Replace(from, teActivityJoin, "", 1)
	}
	return from
}

func (timeEntryKind) joinsForOrderStatement(ctx context.Context, q *Query, order string) ([]string, error) {
	joins, err := q.cfJoinsForOrder(ctx, order)
	if err != nil {
		return nil, err
	}
	if strings.Contains(order, "issue_statuses") {
		joins = append(joins, "LEFT OUTER JOIN issue_statuses ON issue_statuses.id = issues.status_id")
	}
	if strings.Contains(order, "trackers") {
		joins = append(joins, "LEFT OUTER JOIN trackers ON trackers.id = issues.tracker_id")
	}
	if strings.Contains(order, "issue_categories") {
		joins = append(joins, "LEFT OUTER JOIN issue_categories ON issue_categories.id = issues.category_id")
	}
	if strings.Contains(order, "versions") {
		joins = append(joins, "LEFT OUTER JOIN versions ON versions.id = issues.fixed_version_id")
	}
	return joins, nil
}

func (timeEntryKind) sqlForSpecialField(ctx context.Context, q *Query, field, operator string, v []string) (frag, bool, error) {
	var f frag
	var err error
	switch field {
	case "issue_id":
		switch operator {
		case "=":
			f = raw("time_entries.issue_id = " + itoa(customfield.RubyToI(first(v))))
		case "~":
			id := customfield.RubyToI(first(v))
			_, path, ok, err := q.issueTreeInfo(ctx, id)
			if err != nil {
				return frag{}, true, err
			}
			if !ok {
				f = raw("1=0")
				break
			}
			var ids []int64
			if err := q.env.Q.Select(ctx, &ids, `SELECT id FROM issues WHERE hier_path LIKE ? ORDER BY id`, path+"%"); err != nil {
				return frag{}, true, err
			}
			if len(ids) == 0 {
				f = raw("1=0")
			} else {
				f = raw("time_entries.issue_id IN (" + idList(ids) + ")")
			}
		case "!*":
			f = raw("time_entries.issue_id IS NULL")
		case "*":
			f = raw("time_entries.issue_id IS NOT NULL")
		}
	case "issue.fixed_version_id":
		vids := atoiList(v)
		var ids []int64
		if len(vids) > 0 {
			if err := q.env.Q.Select(ctx, &ids, `SELECT id FROM issues WHERE fixed_version_id IN (`+idList(vids)+`) ORDER BY id`); err != nil {
				return frag{}, true, err
			}
		}
		switch operator {
		case "=":
			if len(ids) > 0 {
				f = raw("time_entries.issue_id IN (" + idList(ids) + ")")
			} else {
				f = raw("1=0")
			}
		case "!":
			if len(ids) > 0 {
				f = raw("time_entries.issue_id NOT IN (" + idList(ids) + ")")
			} else {
				f = raw("1=1")
			}
		}
	case "issue.parent_id":
		switch operator {
		case "=":
			pids := uniqDigits(first(v))
			var ids []int64
			if len(pids) > 0 {
				if err := q.env.Q.Select(ctx, &ids, `SELECT id FROM issues WHERE parent_id IN (`+idList(pids)+`) ORDER BY id`); err != nil {
					return frag{}, true, err
				}
			}
			if len(ids) > 0 {
				f = raw("time_entries.issue_id IN (" + idList(ids) + ")")
			} else {
				f = raw("1=0")
			}
		case "~":
			root, path, ok, err := q.issueTreeInfo(ctx, customfield.RubyToI(first(v)))
			if err != nil {
				return frag{}, true, err
			}
			var ids []int64
			if ok {
				if err := q.env.Q.Select(ctx, &ids, `SELECT id FROM issues WHERE root_id = ? AND hier_path LIKE ? AND hier_path <> ? ORDER BY id`, root, path+"%", path); err != nil {
					return frag{}, true, err
				}
			}
			if len(ids) > 0 {
				f = raw("time_entries.issue_id IN (" + idList(ids) + ")")
			} else {
				f = raw("1=0")
			}
		default:
			f, err = q.sqlForField(ctx, "parent_id", operator, v, "issues", "parent_id", false)
		}
	case "activity_id":
		ids := idList(atoiList(v))
		if operator == "=" {
			f = raw("(time_entry_activities.id IN (" + ids + ") OR time_entry_activities.parent_id IN (" + ids + "))")
		} else {
			f = raw("(time_entry_activities.id NOT IN (" + ids + ") AND (time_entry_activities.parent_id IS NULL OR time_entry_activities.parent_id NOT IN (" + ids + ")))")
		}
	case "issue.tracker_id":
		f, err = q.sqlForField(ctx, "tracker_id", operator, v, "issues", "tracker_id", false)
	case "issue.status_id":
		f, err = q.sqlForField(ctx, "status_id", operator, v, "issues", "status_id", false)
	case "issue.category_id":
		f, err = q.sqlForField(ctx, "category_id", operator, v, "issues", "category_id", false)
	case "issue.subject":
		f, err = q.sqlForField(ctx, "subject", operator, v, "issues", "subject", false)
	case "project.status":
		f, err = q.sqlForField(ctx, field, operator, v, "projects", "status", false)
	case "user.group":
		var groups []int64
		switch operator {
		case "*":
			groups, err = q.givableGroupIDs(ctx)
			operator = "="
		case "!*":
			groups, err = q.givableGroupIDs(ctx)
			operator = "!"
		default:
			groups, err = q.existingGroups(ctx, v)
		}
		if err != nil {
			return frag{}, true, err
		}
		members, merr := q.groupMembersAndSelf(ctx, groups, false)
		if merr != nil {
			return frag{}, true, merr
		}
		f, err = q.sqlForField(ctx, "user_id", operator, members, "time_entries", "user_id", false)
		f = f.wrap("(", ")")
	case "user.role":
		f = sqlForRole("time_entries", "user_id", operator, v, true)
	default:
		return frag{}, false, nil
	}
	return f, true, err
}
