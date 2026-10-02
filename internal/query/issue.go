package query

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
)

// issueKind は IssueQuery。
type issueKind struct{}

// estimatedRemainingHoursSQL は IssueQuery::ESTIMATED_REMAINING_HOURS_SQL。
const estimatedRemainingHoursSQL = "COALESCE(issues.estimated_hours, 0) * (100 - COALESCE(issues.done_ratio, 0)) / 100"

// RelationType は IssueRelation::TYPES の 1 項目。
type RelationType struct {
	Name    string
	Label   string // :name の i18n キー
	SymName string
	Order   int
	Sym     string
	Reverse string
}

// RelationTypes は IssueRelation::TYPES (定義順)。
var RelationTypes = []RelationType{
	{Name: "relates", Label: "label_relates_to", SymName: "label_relates_to", Order: 1, Sym: "relates"},
	{Name: "duplicates", Label: "label_duplicates", SymName: "label_duplicated_by", Order: 2, Sym: "duplicated"},
	{Name: "duplicated", Label: "label_duplicated_by", SymName: "label_duplicates", Order: 3, Sym: "duplicates", Reverse: "duplicates"},
	{Name: "blocks", Label: "label_blocks", SymName: "label_blocked_by", Order: 4, Sym: "blocked"},
	{Name: "blocked", Label: "label_blocked_by", SymName: "label_blocks", Order: 5, Sym: "blocks", Reverse: "blocks"},
	{Name: "precedes", Label: "label_precedes", SymName: "label_follows", Order: 6, Sym: "follows"},
	{Name: "follows", Label: "label_follows", SymName: "label_precedes", Order: 7, Sym: "precedes", Reverse: "precedes"},
	{Name: "copied_to", Label: "label_copied_to", SymName: "label_copied_from", Order: 8, Sym: "copied_from"},
	{Name: "copied_from", Label: "label_copied_from", SymName: "label_copied_to", Order: 9, Sym: "copied_to", Reverse: "copied_to"},
}

func relationType(name string) *RelationType {
	for i := range RelationTypes {
		if RelationTypes[i].Name == name {
			return &RelationTypes[i]
		}
	}
	return nil
}

func (issueKind) queriedTable() string    { return "issues" }
func (issueKind) customizedTable() string { return "issues" }
func (issueKind) customizedKind() string  { return "issue" }
func (issueKind) viewPermission() string  { return "view_issues" }

func (issueKind) defaultFilters() *Filters {
	f := NewFilters()
	f.Set("status_id", Filter{Operator: "o", Values: []string{""}})
	return f
}

func (issueKind) defaultSortCriteria() SortCriteria     { return SortCriteria{{"id", "desc"}} }
func (issueKind) availableDisplayTypes(*Query) []string { return []string{"list"} }
func (issueKind) defaultDisplayType(q *Query) string    { return "list" }
func (issueKind) defaultTotalableNames(q *Query) []string {
	return q.env.settingStrings("issue_list_default_totals")
}

func (issueKind) defaultColumnNames(q *Query) []string {
	cols := q.env.settingStrings("issue_list_default_columns")
	if q.Project != nil {
		return cols
	}
	return unionStrings([]string{"project"}, cols)
}

func (issueKind) columnFor(field string) (string, string) { return "issues", renameTimestamp(field) }

func (k issueKind) initializeAvailableFilters(ctx context.Context, q *Query) error {
	e := q.env
	q.addAvailableFilter("status_id", filterOpt{Type: "list_status", ValuesFunc: q.issueStatusesValues})
	if q.Project == nil {
		q.addAvailableFilter("project_id", filterOpt{Type: "list", ValuesFunc: q.projectValues})
	}
	trackers, err := q.trackers(ctx)
	if err != nil {
		return err
	}
	tv := []Option{}
	for _, t := range trackers {
		tv = append(tv, Option{Label: t.Name, Value: itoa(t.ID)})
	}
	q.addAvailableFilter("tracker_id", filterOpt{Type: "list_with_history", Values: tv})
	pv, err := q.priorityValues(ctx)
	if err != nil {
		return err
	}
	q.addAvailableFilter("priority_id", filterOpt{Type: "list_with_history", Values: pv})
	q.addAvailableFilter("author_id", filterOpt{Type: "list", ValuesFunc: q.authorValues})
	q.addAvailableFilter("author.group", filterOpt{Type: "list", ValuesFunc: q.groupValues,
		Name: e.l("label_attribute_of_author", map[string]any{"name": e.l("label_group")})})
	q.addAvailableFilter("author.role", filterOpt{Type: "list", ValuesFunc: q.roleValues,
		Name: e.l("label_attribute_of_author", map[string]any{"name": e.l("field_role")})})
	q.addAvailableFilter("assigned_to_id", filterOpt{Type: "list_optional_with_history", ValuesFunc: q.assignedToValues})
	q.addAvailableFilter("member_of_group", filterOpt{Type: "list_optional", ValuesFunc: q.groupValues})
	q.addAvailableFilter("assigned_to_role", filterOpt{Type: "list_optional", ValuesFunc: q.roleValues})
	q.addAvailableFilter("fixed_version_id", filterOpt{Type: "list_optional_with_history", ValuesFunc: q.fixedVersionValues})
	q.addAvailableFilter("fixed_version.due_date", filterOpt{Type: "date",
		Name: e.l("label_attribute_of_fixed_version", map[string]any{"name": e.l("field_effective_date")})})
	q.addAvailableFilter("fixed_version.status", filterOpt{Type: "list",
		Name:   e.l("label_attribute_of_fixed_version", map[string]any{"name": e.l("field_status")}),
		Values: q.versionStatusOptions()})
	if q.Project != nil {
		q.addAvailableFilter("category_id", filterOpt{Type: "list_optional_with_history", ValuesFunc: q.categoryValues})
	}
	q.addAvailableFilter("subject", filterOpt{Type: "text"})
	q.addAvailableFilter("description", filterOpt{Type: "text"})
	q.addAvailableFilter("notes", filterOpt{Type: "text"})
	q.addAvailableFilter("created_on", filterOpt{Type: "date_past"})
	q.addAvailableFilter("updated_on", filterOpt{Type: "date_past"})
	q.addAvailableFilter("closed_on", filterOpt{Type: "date_past"})
	q.addAvailableFilter("start_date", filterOpt{Type: "date"})
	q.addAvailableFilter("due_date", filterOpt{Type: "date"})
	q.addAvailableFilter("estimated_hours", filterOpt{Type: "float"})
	if ok, err := q.allowedTo(ctx, "view_time_entries", q.Project, true); err != nil {
		return err
	} else if ok {
		q.addAvailableFilter("spent_time", filterOpt{Type: "float", Label: "label_spent_time"})
	}
	q.addAvailableFilter("done_ratio", filterOpt{Type: "integer"})
	if ok, err := q.canSetPrivate(ctx); err != nil {
		return err
	} else if ok {
		q.addAvailableFilter("is_private", filterOpt{Type: "list",
			Values: []Option{{Label: e.l("general_text_yes"), Value: "1"}, {Label: e.l("general_text_no"), Value: "0"}}})
	}
	q.addAvailableFilter("attachment", filterOpt{Type: "text", Name: e.l("label_attachment")})
	q.addAvailableFilter("attachment_description", filterOpt{Type: "text", Name: e.l("label_attachment_description")})
	if e.User().Logged() {
		q.addAvailableFilter("watcher_id", filterOpt{Type: "list", ValuesFunc: q.watcherValues})
	}
	q.addAvailableFilter("updated_by", filterOpt{Type: "list", ValuesFunc: q.authorValues})
	q.addAvailableFilter("last_updated_by", filterOpt{Type: "list", ValuesFunc: q.authorValues})
	leaf, err := q.projectIsLeaf(ctx)
	if err != nil {
		return err
	}
	if q.Project != nil && !leaf {
		q.addAvailableFilter("subproject_id", filterOpt{Type: "list_subprojects", ValuesFunc: q.subprojectValues})
	}
	if q.Project == nil || !leaf {
		q.addAvailableFilter("project.status", filterOpt{Type: "list", ValuesFunc: q.projectStatusesValues,
			Name: e.l("label_attribute_of_project", map[string]any{"name": e.l("field_status")})})
	}
	where, args, err := q.issueCustomFieldsWhere(ctx)
	if err != nil {
		return err
	}
	cfs, err := q.visibleFilterCustomFields(ctx, where, args...)
	if err != nil {
		return err
	}
	if err := q.addCustomFieldsFilters(ctx, cfs, ""); err != nil {
		return err
	}
	if err := q.addAssociationsCustomFieldsFilters(ctx, "project", "author", "assigned_to", "fixed_version"); err != nil {
		return err
	}
	for _, rt := range RelationTypes {
		q.addAvailableFilter(rt.Name, filterOpt{Type: "relation", Label: rt.Label, ValuesFunc: q.allProjectsValues})
	}
	q.addAvailableFilter("parent_id", filterOpt{Type: "tree", Label: "field_parent_issue"})
	q.addAvailableFilter("child_id", filterOpt{Type: "tree", Label: "label_subtask_plural"})
	q.addAvailableFilter("issue_id", filterOpt{Type: "integer", Label: "label_issue"})
	q.addAvailableFilter("any_searchable", filterOpt{Type: "search"})
	for _, f := range disabledCoreFields(trackers) {
		q.deleteAvailableFilter(f)
	}
	return nil
}

// canSetPrivate は set_issues_private / set_own_issues_private のいずれかをグローバルに持つ。
func (q *Query) canSetPrivate(ctx context.Context) (bool, error) {
	ok, err := q.allowedTo(ctx, "set_issues_private", nil, true)
	if err != nil || ok {
		return ok, err
	}
	return q.allowedTo(ctx, "set_own_issues_private", nil, true)
}

var reIssuesWord = regexp.MustCompile(`\bissues\b`)

func (k issueKind) availableColumns(ctx context.Context, q *Query) ([]*Column, error) {
	uf := q.env.setting("user_format")
	vis, err := q.env.Auth.IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return nil, err
	}
	subtasksVis := reIssuesWord.ReplaceAllString(vis, "subtasks")
	cols := []*Column{
		newColumn("id", ColumnPlain, colOpt{sortable: []string{"issues.id"}, defaultOrder: "desc", frozen: true}),
		newColumn("project", ColumnPlain, colOpt{sortable: []string{"projects.name"}, groupable: true, groupSQL: "issues.project_id", groupAssoc: true}),
		newColumn("tracker", ColumnPlain, colOpt{sortable: []string{"trackers.position"}, groupable: true, groupSQL: "issues.tracker_id", groupAssoc: true}),
		newColumn("parent", ColumnPlain, colOpt{sortable: []string{"issues.root_id", "issues.hier_path ASC"}, defaultOrder: "desc", caption: "field_parent_issue"}),
		{Name: "parent.subject", Kind: ColumnAssociation, Association: "parent", Attribute: "subject", Inline: true, CaptionKey: "field_parent_issue_subject"},
		newColumn("status", ColumnPlain, colOpt{sortable: []string{"issue_statuses.position"}, groupable: true, groupSQL: "issues.status_id", groupAssoc: true}),
		newColumn("priority", ColumnPlain, colOpt{sortable: []string{"issue_priorities.position"}, defaultOrder: "desc", groupable: true, groupSQL: "issues.priority_id", groupAssoc: true}),
		newColumn("subject", ColumnPlain, colOpt{sortable: []string{"issues.subject"}}),
		newColumn("author", ColumnPlain, colOpt{sortable: customfield.UserOrderFields("authors", uf), groupable: true, groupSQL: "issues.author_id", groupAssoc: true}),
		newColumn("assigned_to", ColumnPlain, colOpt{sortable: customfield.UserOrderFields("users", uf), groupable: true, groupSQL: "issues.assigned_to_id", groupAssoc: true}),
		newColumn("watcher_users", ColumnWatcher, colOpt{caption: "label_issue_watchers"}),
		newColumn("updated_on", ColumnTimestamp, colOpt{sortable: []string{"issues.updated_at"}, defaultOrder: "desc", groupable: true, groupSQL: q.timestampToDate("issues.updated_at")}),
		newColumn("category", ColumnPlain, colOpt{sortable: []string{"issue_categories.name"}, groupable: true, groupSQL: "issues.category_id", groupAssoc: true}),
		newColumn("fixed_version", ColumnPlain, colOpt{sortable: customfield.VersionOrderFields("versions"), groupable: true, groupSQL: "issues.fixed_version_id", groupAssoc: true}),
		newColumn("start_date", ColumnPlain, colOpt{sortable: []string{"issues.start_date"}, groupable: true, groupSQL: "issues.start_date"}),
		newColumn("due_date", ColumnPlain, colOpt{sortable: []string{"issues.due_date"}, groupable: true, groupSQL: "issues.due_date"}),
		newColumn("estimated_hours", ColumnPlain, colOpt{sortable: []string{"issues.estimated_hours"}, totalable: true}),
		newColumn("estimated_remaining_hours", ColumnPlain, colOpt{sortable: []string{estimatedRemainingHoursSQL}, totalable: true}),
		newColumn("total_estimated_hours", ColumnPlain, colOpt{defaultOrder: "desc", sortable: []string{
			"COALESCE((SELECT SUM(estimated_hours) FROM issues subtasks WHERE " + subtasksVis +
				" AND subtasks.root_id = issues.root_id AND subtasks.hier_path LIKE (issues.hier_path || '%')), 0)"}}),
		newColumn("done_ratio", ColumnPlain, colOpt{sortable: []string{"issues.done_ratio"}, groupable: true, groupSQL: "issues.done_ratio"}),
		newColumn("created_on", ColumnTimestamp, colOpt{sortable: []string{"issues.created_at"}, defaultOrder: "desc", groupable: true, groupSQL: q.timestampToDate("issues.created_at")}),
		newColumn("closed_on", ColumnTimestamp, colOpt{sortable: []string{"issues.closed_at"}, defaultOrder: "desc", groupable: true, groupSQL: q.timestampToDate("issues.closed_at")}),
		newColumn("last_updated_by", ColumnPlain, colOpt{sortable: customfield.UserOrderFields("last_journal_user", uf)}),
		newColumn("relations", ColumnPlain, colOpt{caption: "label_related_issues"}),
		newColumn("attachments", ColumnPlain, colOpt{caption: "label_attachment_plural"}),
		newColumn("description", ColumnPlain, colOpt{notInline: true}),
		newColumn("last_notes", ColumnPlain, colOpt{caption: "label_last_notes", notInline: true}),
	}
	cols[0].Caption = "#"
	where, args, err := q.issueCustomFieldsWhere(ctx)
	if err != nil {
		return nil, err
	}
	cfs, err := q.visibleCustomFields(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	for _, cf := range cfs {
		cols = append(cols, q.newCustomFieldColumn(cf, nil))
	}
	if ok, err := q.allowedTo(ctx, "view_time_entries", q.Project, true); err != nil {
		return nil, err
	} else if ok {
		teVis, err := q.timeEntryVisibleCondition(ctx)
		if err != nil {
			return nil, err
		}
		idx := slices.IndexFunc(cols, func(c *Column) bool { return c.Name == "total_estimated_hours" })
		spent := newColumn("spent_hours", ColumnPlain, colOpt{defaultOrder: "desc", caption: "label_spent_time", totalable: true,
			sortable: []string{"COALESCE((SELECT SUM(hours) FROM time_entries JOIN projects ON projects.id = time_entries.project_id" +
				" WHERE (" + teVis + ") AND time_entries.issue_id = issues.id), 0)"}})
		total := newColumn("total_spent_hours", ColumnPlain, colOpt{defaultOrder: "desc", caption: "label_total_spent_time",
			sortable: []string{"COALESCE((SELECT SUM(hours) FROM time_entries JOIN projects ON projects.id = time_entries.project_id" +
				" JOIN issues subtasks ON subtasks.id = time_entries.issue_id" +
				" WHERE (" + teVis + ") AND subtasks.root_id = issues.root_id AND subtasks.hier_path LIKE (issues.hier_path || '%')), 0)"}})
		if idx >= 0 {
			cols = slices.Insert(cols, idx+1, spent, total)
		} else {
			// Ruby の insert(-1, ...) / insert(0, ...) の挙動
			cols = append(cols[:len(cols)-1], spent, cols[len(cols)-1])
			cols = slices.Insert(cols, 0, total)
		}
	}
	if ok, err := q.canSetPrivate(ctx); err != nil {
		return nil, err
	} else if ok {
		cols = append(cols, newColumn("is_private", ColumnPlain, colOpt{sortable: []string{"issues.is_private"}, groupable: true, groupSQL: "issues.is_private"}))
	}
	trackers, err := q.trackers(ctx)
	if err != nil {
		return nil, err
	}
	var disabled []string
	for _, f := range disabledCoreFields(trackers) {
		disabled = append(disabled, strings.TrimSuffix(f, "_id"))
	}
	if slices.Contains(disabled, "estimated_hours") {
		disabled = append(disabled, "total_estimated_hours", "estimated_remaining_hours")
	}
	cols = slices.DeleteFunc(cols, func(c *Column) bool { return slices.Contains(disabled, c.Name) })
	return cols, nil
}

// timeEntryVisibleCondition は TimeEntry.visible_condition(User.current)。
func (q *Query) timeEntryVisibleCondition(ctx context.Context) (string, error) {
	return timeEntryVisibleCondition(ctx, q.env.Auth, authz.ConditionOptions{})
}

func (issueKind) baseScope(ctx context.Context, q *Query) (string, frag, error) {
	vis, err := q.env.Auth.IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return "", frag{}, err
	}
	st, err := q.statement(ctx)
	if err != nil {
		return "", frag{}, err
	}
	from := "issues INNER JOIN projects ON projects.id = issues.project_id INNER JOIN issue_statuses ON issue_statuses.id = issues.status_id"
	return from, joinFrags(" AND ", raw("("+vis+")"), paren(st)), nil
}

func (issueKind) joinsForOrderStatement(ctx context.Context, q *Query, order string) ([]string, error) {
	joins, err := q.cfJoinsForOrder(ctx, order)
	if err != nil {
		return nil, err
	}
	if order == "" {
		return joins, nil
	}
	if strings.Contains(order, "authors") {
		joins = append(joins, "LEFT OUTER JOIN principals authors ON authors.id = issues.author_id")
	}
	if strings.Contains(order, "users") {
		joins = append(joins, "LEFT OUTER JOIN principals users ON users.id = issues.assigned_to_id")
	}
	if strings.Contains(order, "last_journal_user") {
		notes, err := q.visibleNotesCondition(ctx, "issue_journals")
		if err != nil {
			return nil, err
		}
		joins = append(joins, "LEFT OUTER JOIN issue_journals ON issue_journals.id = (SELECT MAX(issue_journals.id) FROM issue_journals"+
			" WHERE issue_journals.issue_id = issues.id AND "+notes+")"+
			" LEFT OUTER JOIN principals last_journal_user ON last_journal_user.id = issue_journals.user_id")
	}
	if strings.Contains(order, "versions") {
		joins = append(joins, "LEFT OUTER JOIN versions ON versions.id = issues.fixed_version_id")
	}
	if strings.Contains(order, "issue_categories") {
		joins = append(joins, "LEFT OUTER JOIN issue_categories ON issue_categories.id = issues.category_id")
	}
	if strings.Contains(order, "trackers") {
		joins = append(joins, "LEFT OUTER JOIN trackers ON trackers.id = issues.tracker_id")
	}
	if strings.Contains(order, "issue_priorities") {
		joins = append(joins, "LEFT OUTER JOIN issue_priorities ON issue_priorities.id = issues.priority_id")
	}
	return joins, nil
}

// ---------------------------------------------------------------- IssueQuery 固有のフィルタ SQL

func (k issueKind) sqlForSpecialField(ctx context.Context, q *Query, field, operator string, v []string) (frag, bool, error) {
	if rt := relationType(field); rt != nil {
		f, err := q.sqlForRelations(field, operator, v, false)
		return f, true, err
	}
	var f frag
	var err error
	switch field {
	case "notes":
		f, err = q.sqlForNotes(ctx, field, operator, v)
	case "updated_by":
		f, err = q.sqlForUpdatedBy(ctx, field, operator, v)
	case "last_updated_by":
		f, err = q.sqlForLastUpdatedBy(ctx, field, operator, v)
	case "spent_time":
		f = sqlForSpentTime(operator, v)
	case "watcher_id":
		f, err = q.sqlForWatcherID(ctx, field, operator, v)
	case "member_of_group":
		f, err = q.sqlForMemberOfGroup(ctx, operator, v)
	case "assigned_to_role":
		f = sqlForRole("issues", "assigned_to_id", operator, v, true)
	case "author.group":
		f, err = q.sqlForAuthorGroup(ctx, operator, v)
	case "author.role":
		f = sqlForRole("issues", "author_id", operator, v, false)
	case "fixed_version.status":
		f, err = q.sqlForFixedVersionAttr(ctx, field, operator, v, "status", operator == "!")
	case "fixed_version.due_date":
		f, err = q.sqlForFixedVersionAttr(ctx, field, operator, v, "effective_date", operator == "!*")
	case "is_private":
		f = q.sqlForIsPrivate(operator, v)
	case "attachment":
		f = q.sqlForAttachment(operator, v)
	case "attachment_description":
		f = q.sqlForAttachmentDescription(operator, v)
	case "parent_id":
		f, err = q.sqlForParentID(ctx, operator, v)
	case "child_id":
		f, err = q.sqlForChildID(ctx, operator, v)
	case "updated_on":
		switch operator {
		case "!*":
			f = raw("issues.updated_at = issues.created_at")
		case "*":
			f = raw("issues.updated_at > issues.created_at")
		default:
			f, err = q.sqlForField(ctx, "updated_on", operator, v, "issues", "updated_at", false)
		}
	case "issue_id":
		if operator == "=" {
			ids := reDigits.FindAllString(first(v), -1)
			if len(ids) == 0 {
				f = raw("1=0")
			} else {
				f = raw("issues.id IN (" + strings.Join(trimZeros(ids), ",") + ")")
			}
		} else {
			f, err = q.sqlForField(ctx, "id", operator, v, "issues", "id", false)
		}
	case "project.status":
		f, err = q.sqlForField(ctx, field, operator, v, "projects", "status", false)
	case "any_searchable":
		f, err = q.sqlForAnySearchable(ctx, operator, v)
	default:
		return frag{}, false, nil
	}
	return f, true, err
}

var reDigits = regexp.MustCompile(`\d+`)

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// trimZeros は "007" のような数字列を整数表記にする (to_i)。
func trimZeros(ids []string) []string {
	out := make([]string, len(ids))
	for i, s := range ids {
		out[i] = itoa(customfield.RubyToI(s))
	}
	return out
}

// uniqDigits は value.first.to_s.scan(/\d+/).map(&:to_i).uniq。
func uniqDigits(s string) []int64 {
	var out []int64
	for _, m := range reDigits.FindAllString(s, -1) {
		n := customfield.RubyToI(m)
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func (q *Query) sqlForNotes(ctx context.Context, field, operator string, v []string) (frag, error) {
	inner, err := q.sqlForField(ctx, field, strings.TrimPrefix(operator, "!"), v, "issue_journals", "notes", false)
	if err != nil {
		return frag{}, err
	}
	notes, err := q.visibleNotesCondition(ctx, "issue_journals")
	if err != nil {
		return frag{}, err
	}
	e := "EXISTS"
	if strings.HasPrefix(operator, "!") {
		e = "NOT EXISTS"
	}
	return concat(raw(e+" (SELECT 1 FROM issue_journals WHERE issue_journals.issue_id=issues.id AND ("), inner, raw(") AND ("+notes+"))")), nil
}

func (q *Query) sqlForUpdatedBy(ctx context.Context, field, operator string, v []string) (frag, error) {
	neg := ""
	if operator == "!" {
		neg = "NOT"
	}
	inner, err := q.sqlForField(ctx, field, "=", v, "issue_journals", "user_id", false)
	if err != nil {
		return frag{}, err
	}
	notes, err := q.visibleNotesCondition(ctx, "issue_journals")
	if err != nil {
		return frag{}, err
	}
	return concat(raw(neg+" EXISTS (SELECT 1 FROM issue_journals WHERE issue_journals.issue_id=issues.id AND ("), inner, raw(") AND ("+notes+"))")), nil
}

func (q *Query) sqlForLastUpdatedBy(ctx context.Context, field, operator string, v []string) (frag, error) {
	neg := ""
	if operator == "!" {
		neg = "NOT"
	}
	inner, err := q.sqlForField(ctx, field, "=", v, "sj", "user_id", false)
	if err != nil {
		return frag{}, err
	}
	notes, err := q.visibleNotesCondition(ctx, "issue_journals")
	if err != nil {
		return frag{}, err
	}
	return concat(raw(neg+" EXISTS (SELECT 1 FROM issue_journals sj WHERE sj.issue_id=issues.id AND ("), inner,
		raw(") AND sj.id IN (SELECT MAX(issue_journals.id) FROM issue_journals WHERE issue_journals.issue_id=issues.id AND ("+notes+")))")), nil
}

func sqlForSpentTime(operator string, v []string) frag {
	f1 := customfield.RubyToF(first(v))
	var f2 float64
	if len(v) > 1 {
		f2 = customfield.RubyToF(v[1])
	}
	const sub = "COALESCE((SELECT ROUND(CAST(SUM(hours) AS DECIMAL(30,3)), 2) FROM time_entries WHERE issue_id = issues.id), 0) "
	switch operator {
	case "=", ">=", "<=":
		return sqlf(sub+operator+" ?", f1)
	case "><":
		return sqlf(sub+"BETWEEN ? AND ?", f1, f2)
	case "*":
		return raw(sub + "> 0")
	case "!*":
		return raw(sub + "= 0")
	}
	return frag{}
}

func (q *Query) sqlForWatcherID(ctx context.Context, field, operator string, v []string) (frag, error) {
	u := q.env.User()
	meIDs := []int64{0, u.ID}
	gids, err := q.env.Auth.GroupIDs(ctx)
	if err != nil {
		return frag{}, err
	}
	meIDs = append(meIDs, gids...)
	var me, others []string
	for _, id := range v {
		if slices.Contains(meIDs, customfield.RubyToI(id)) {
			me = append(me, id)
		} else {
			others = append(others, id)
		}
	}
	meSQL, err := q.sqlForField(ctx, field, "=", me, "watchers", "principal_id", false)
	if err != nil {
		return frag{}, err
	}
	var sub frag
	if len(others) > 0 {
		perm, err := q.env.Auth.AllowedToCondition(ctx, "view_issue_watchers", authz.ConditionOptions{}, nil)
		if err != nil {
			return frag{}, err
		}
		othersSQL, err := q.sqlForField(ctx, field, "=", others, "watchers", "principal_id", false)
		if err != nil {
			return frag{}, err
		}
		sub = concat(raw("SELECT issues.id FROM issues INNER JOIN watchers ON issues.id = watchers.watchable_id AND watchers.watchable_kind = 'issue' "+
			"LEFT OUTER JOIN projects ON projects.id = issues.project_id WHERE ("), meSQL, raw(") OR ("+perm+" AND "), othersSQL, raw(")"))
	} else {
		sub = concat(raw("SELECT watchers.watchable_id FROM watchers WHERE watchers.watchable_kind='issue' AND "), meSQL)
	}
	op := "NOT IN"
	if operator == "=" {
		op = "IN"
	}
	return concat(raw("issues.id "+op+" ("), sub, raw(")")), nil
}

func (q *Query) sqlForMemberOfGroup(ctx context.Context, operator string, v []string) (frag, error) {
	var groups []int64
	var err error
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
		return frag{}, err
	}
	members, err := q.groupMembersAndSelf(ctx, groups, true)
	if err != nil {
		return frag{}, err
	}
	f, err := q.sqlForField(ctx, "assigned_to_id", operator, members, "issues", "assigned_to_id", false)
	return f.wrap("(", ")"), err
}

func (q *Query) sqlForAuthorGroup(ctx context.Context, operator string, v []string) (frag, error) {
	var groups []int64
	var err error
	if len(v) == 0 {
		groups, err = q.givableGroupIDs(ctx)
	} else {
		groups, err = q.existingGroups(ctx, v)
	}
	if err != nil {
		return frag{}, err
	}
	members, err := q.groupMembersAndSelf(ctx, groups, true)
	if err != nil {
		return frag{}, err
	}
	f, err := q.sqlForField(ctx, "author_id", operator, members, "issues", "author_id", false)
	return f.wrap("(", ")"), err
}

// sqlForRole は sql_for_assigned_to_role_field / sql_for_author_role_field / sql_for_user_role_field。
// withAny は "*" / "!*" (メンバーかどうか) を扱う。
func sqlForRole(table, column, operator string, v []string, withAny bool) frag {
	switch operator {
	case "*", "!*":
		if !withAny {
			break
		}
		sw, nl := "", ""
		if operator == "!*" {
			sw, nl = "NOT", table+"."+column+" IS NULL OR"
		}
		return raw("(" + nl + " " + sw + " EXISTS (SELECT 1 FROM members WHERE " + table + ".project_id = members.project_id AND members.principal_id = " + table + "." + column + "))")
	case "=", "!":
		var ids []int64
		for _, s := range v {
			if n, ok := parseID(s); ok {
				ids = append(ids, n)
			}
		}
		roleCond := "1=0"
		if len(ids) > 0 {
			roleCond = "member_roles.role_id IN (" + idList(ids) + ")"
		}
		sw, nl := "", ""
		if operator == "!" {
			sw, nl = "NOT", table+"."+column+" IS NULL OR"
		}
		return raw("(" + nl + " " + sw + " EXISTS (SELECT 1 FROM members inner join member_roles on members.id = member_roles.member_id" +
			" WHERE " + table + ".project_id = members.project_id AND members.principal_id = " + table + "." + column + " AND " + roleCond + "))")
	}
	if !withAny {
		// author.role の "*" 等は Redmine では =/! 以外も同じ式 (role_cond) になる
		return sqlForRole(table, column, "=", v, false)
	}
	return frag{}
}

func (q *Query) sqlForFixedVersionAttr(ctx context.Context, field, operator string, v []string, column string, nullOK bool) (frag, error) {
	where, err := q.sqlForField(ctx, field, operator, v, "versions", column, false)
	if err != nil {
		return frag{}, err
	}
	scope, err := q.versionScopeCondition(ctx)
	if err != nil {
		return frag{}, err
	}
	var ids []int64
	if err := q.env.Q.Select(ctx, &ids, "SELECT versions.id FROM versions INNER JOIN projects ON projects.id = versions.project_id WHERE ("+scope+") AND ("+where.SQL+") ORDER BY versions.id", where.Args...); err != nil {
		return frag{}, err
	}
	nl := ""
	if nullOK {
		nl = "issues.fixed_version_id IS NULL OR"
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = itoa(id)
	}
	in, err := q.sqlForField(ctx, "fixed_version_id", "=", strs, "issues", "fixed_version_id", false)
	if err != nil {
		return frag{}, err
	}
	return concat(raw("("+nl+" "), in, raw(")")), nil
}

func (q *Query) sqlForIsPrivate(operator string, v []string) frag {
	op := "NOT IN"
	if operator == "=" {
		op = "IN"
	}
	var lits []string
	for _, s := range v {
		l := q.env.boolLit(s != "0")
		if !slices.Contains(lits, l) {
			lits = append(lits, l)
		}
	}
	return raw("issues.is_private " + op + " (" + strings.Join(lits, ",") + ")")
}

const issueAttachments = "SELECT 1 FROM attachments a WHERE a.container_kind = 'issue' AND a.container_id = issues.id"

func (q *Query) sqlForAttachment(operator string, v []string) frag {
	switch operator {
	case "*", "!*":
		e := "EXISTS"
		if operator == "!*" {
			e = "NOT EXISTS"
		}
		return raw(e + " (" + issueAttachments + ")")
	case "~", "!~", "*~":
		c := q.sqlContains("a.filename", first(v), containsOpts{anyWord: operator == "*~"})
		e := "EXISTS"
		if operator == "!~" {
			e = "NOT EXISTS"
		}
		return concat(raw(e+" ("+issueAttachments+" AND ("), c, raw("))"))
	case "^", "$":
		c := q.sqlContains("a.filename", first(v), containsOpts{startsWith: operator == "^", endsWith: operator == "$"})
		return concat(raw("EXISTS ("+issueAttachments+" AND ("), c, raw("))"))
	}
	return frag{}
}

func (q *Query) sqlForAttachmentDescription(operator string, v []string) frag {
	cond := "a.description IS NOT NULL AND a.description <> ''"
	var c frag
	switch operator {
	case "*":
		c = raw(cond)
	case "!*":
		c = raw("NOT (" + cond + ")")
	case "~", "!~", "*~":
		pre := ""
		if operator != "~" {
			pre = cond + " AND "
		}
		c = concat(raw(pre), q.sqlContains("a.description", first(v), containsOpts{notMatch: operator == "!~", anyWord: operator == "*~"}))
	case "^", "$":
		c = q.sqlContains("a.description", first(v), containsOpts{startsWith: operator == "^", endsWith: operator == "$"})
	default:
		c = raw("1=0")
	}
	return concat(raw("EXISTS ("+issueAttachments+" AND ("), c, raw("))"))
}

// issueTreeInfo は (root_id, hier_path)。
func (q *Query) issueTreeInfo(ctx context.Context, id int64) (int64, string, bool, error) {
	var row struct {
		RootID   int64  `db:"root_id"`
		HierPath string `db:"hier_path"`
	}
	err := q.env.Q.Get(ctx, &row, `SELECT root_id, hier_path FROM issues WHERE id = ?`, id)
	if err != nil {
		if isNoRows(err) {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}
	return row.RootID, row.HierPath, true, nil
}

func (q *Query) sqlForParentID(ctx context.Context, operator string, v []string) (frag, error) {
	switch operator {
	case "=":
		ids := uniqDigits(first(v))
		if len(ids) == 0 {
			return raw("1=0"), nil
		}
		return raw("issues.parent_id IN (" + idList(ids) + ")"), nil
	case "~":
		var conds []string
		for _, id := range uniqDigits(first(v)) {
			root, path, ok, err := q.issueTreeInfo(ctx, id)
			if err != nil {
				return frag{}, err
			}
			if ok {
				// 子孫 (lft > x.lft AND rgt < x.rgt)
				conds = append(conds, "(issues.root_id = "+itoa(root)+" AND issues.hier_path LIKE '"+path+"%' AND issues.hier_path <> '"+path+"')")
			}
		}
		if len(conds) == 0 {
			return raw("1=0"), nil
		}
		return raw("(" + strings.Join(conds, " OR ") + ")"), nil
	case "!*":
		return raw("issues.parent_id IS NULL"), nil
	case "*":
		return raw("issues.parent_id IS NOT NULL"), nil
	}
	return frag{}, nil
}

func (q *Query) sqlForChildID(ctx context.Context, operator string, v []string) (frag, error) {
	switch operator {
	case "=":
		childIDs := uniqDigits(first(v))
		if len(childIDs) == 0 {
			return raw("1=0"), nil
		}
		var ids []int64
		if err := q.env.Q.Select(ctx, &ids, `SELECT DISTINCT parent_id FROM issues WHERE id IN (`+idList(childIDs)+`) AND parent_id IS NOT NULL ORDER BY parent_id`); err != nil {
			return frag{}, err
		}
		if len(ids) == 0 {
			return raw("1=0"), nil
		}
		return raw("issues.id IN (" + idList(ids) + ")"), nil
	case "~":
		root, path, ok, err := q.issueTreeInfo(ctx, customfield.RubyToI(first(v)))
		if err != nil {
			return frag{}, err
		}
		if !ok {
			return raw("1=0"), nil
		}
		// 祖先 (lft < x.lft AND rgt > x.rgt)
		return raw("issues.root_id = " + itoa(root) + " AND '" + path + "' LIKE (issues.hier_path || '%') AND issues.hier_path <> '" + path + "'"), nil
	case "!*":
		// 葉 (rgt - lft = 1)
		return raw("NOT EXISTS (SELECT 1 FROM issues children WHERE children.parent_id = issues.id)"), nil
	case "*":
		return raw("EXISTS (SELECT 1 FROM issues children WHERE children.parent_id = issues.id)"), nil
	}
	return frag{}, nil
}

// sqlForRelations は sql_for_relations。
func (q *Query) sqlForRelations(field, operator string, v []string, reverse bool) (frag, error) {
	rt := relationType(field)
	relType := field
	join, target := "issue_from_id", "issue_to_id"
	if rt.Reverse != "" || reverse {
		if rt.Reverse != "" {
			relType = rt.Reverse
		}
		join, target = target, join
	}
	rel := "'" + relType + "'"
	var sql string
	switch operator {
	case "*", "!*":
		op := "IN"
		if operator == "!*" {
			op = "NOT IN"
		}
		sql = "issues.id " + op + " (SELECT DISTINCT issue_relations." + join + " FROM issue_relations WHERE issue_relations.relation_type = " + rel + ")"
	case "=", "!":
		ids := uniqDigits(first(v))
		if len(ids) == 0 {
			sql = "1=0"
		} else {
			op := "IN"
			if operator == "!" {
				op = "NOT IN"
			}
			sql = "issues.id " + op + " (SELECT DISTINCT issue_relations." + join + " FROM issue_relations WHERE issue_relations.relation_type = " + rel +
				" AND issue_relations." + target + " IN (" + idList(ids) + "))"
		}
	case "=p", "=!p", "!p":
		op, comp := "IN", "="
		if operator == "!p" {
			op = "NOT IN"
		}
		if operator == "=!p" {
			comp = "<>"
		}
		sql = "issues.id " + op + " (SELECT DISTINCT issue_relations." + join + " FROM issue_relations, issues relissues WHERE issue_relations.relation_type = " + rel +
			" AND issue_relations." + target + " = relissues.id AND relissues.project_id " + comp + " " + itoa(customfield.RubyToI(first(v))) + ")"
	case "*o", "!o":
		op := "IN"
		if operator == "!o" {
			op = "NOT IN"
		}
		sql = "issues.id " + op + " (SELECT DISTINCT issue_relations." + join + " FROM issue_relations, issues relissues WHERE issue_relations.relation_type = " + rel +
			" AND issue_relations." + target + " = relissues.id AND relissues.status_id IN (SELECT id FROM issue_statuses WHERE is_closed = " + q.env.boolLit(false) + "))"
	}
	if rt.Sym == field && !reverse {
		rev, err := q.sqlForRelations(field, operator, v, true)
		if err != nil {
			return frag{}, err
		}
		sep := " OR "
		if slices.Contains([]string{"!", "!*", "!p", "!o"}, operator) {
			sep = " AND "
		}
		sql = sql + sep + rev.SQL
	}
	return raw("(" + sql + ")"), nil
}
