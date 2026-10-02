package query

import (
	"context"
	"encoding/json"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
)

// kindImpl はクエリ種別 (Query のサブクラス) ごとの振る舞い。
type kindImpl interface {
	// queriedTable は statement で参照するテーブル名 (queried_table_name。UserQuery は principals の別名 users)。
	queriedTable() string
	// customizedTable / customizedKind は CF フィルタの EXISTS で使う customized_class のテーブルと種別。
	customizedTable() string
	customizedKind() string
	viewPermission() string
	defaultFilters() *Filters
	initializeAvailableFilters(ctx context.Context, q *Query) error
	availableColumns(ctx context.Context, q *Query) ([]*Column, error)
	defaultColumnNames(q *Query) []string
	defaultTotalableNames(q *Query) []string
	defaultSortCriteria() SortCriteria
	availableDisplayTypes(q *Query) []string
	defaultDisplayType(q *Query) string
	// columnFor は通常フィールド (sql_for_field(field, ..., queried_table_name, field)) の列。
	columnFor(field string) (table, column string)
	// sqlForSpecialField は sql_for_<field>_field。handled = false なら通常フィールド扱い。
	sqlForSpecialField(ctx context.Context, q *Query, field, operator string, v []string) (frag, bool, error)
	// joinsForOrderStatement は joins_for_order_statement (CF の JOIN を含む)。
	joinsForOrderStatement(ctx context.Context, q *Query, order string) ([]string, error)
	// baseScope は base_scope の FROM/JOIN 句と WHERE 句 (statement を含む)。
	baseScope(ctx context.Context, q *Query) (from string, where frag, err error)
}

// renameTimestamp は Redmine の *_on 列名を新スキーマの *_at にする。
func renameTimestamp(field string) string {
	switch field {
	case "created_on":
		return "created_at"
	case "updated_on":
		return "updated_at"
	case "closed_on":
		return "closed_at"
	case "last_login_on":
		return "last_login_at"
	case "passwd_changed_on":
		return "password_changed_at"
	}
	return field
}

// ---------------------------------------------------------------- 共通の補助 (トラッカー・ステータス・バージョン)

// trackerInfo はトラッカーの id・名前・無効なコアフィールド。
type trackerInfo struct {
	ID       int64
	Name     string
	Disabled []string
}

// trackers は Query#trackers: (project ? project.rolled_up_trackers : Tracker.all).visible.sorted。
func (q *Query) trackers(ctx context.Context) ([]trackerInfo, error) {
	cond, err := q.env.Auth.AllowedToCondition(ctx, "view_issues", authz.ConditionOptions{}, func(role *domain.Role, _ *domain.User) string {
		if role.PermissionsAllTrackers("view_issues") {
			return ""
		}
		if ids := role.PermissionsTrackerIDs("view_issues"); len(ids) > 0 {
			return "trackers.id IN (" + idList(ids) + ")"
		}
		return "1=0"
	})
	if err != nil {
		return nil, err
	}
	where := cond
	if q.Project != nil {
		where = "(" + cond + ") AND projects.status <> " + itoa(domain.ProjectStatusArchived) +
			" AND EXISTS (SELECT 1 FROM project_modules em WHERE em.project_id = projects.id AND em.name = 'issue_tracking')" +
			" AND projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + itoa(q.Project.ID) + ")"
	}
	var rows []struct {
		ID       int64  `db:"id"`
		Name     string `db:"name"`
		Disabled string `db:"disabled_core_fields"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT trackers.id, trackers.name, trackers.disabled_core_fields FROM trackers
WHERE trackers.id IN (SELECT pt.tracker_id FROM project_trackers pt JOIN projects ON projects.id = pt.project_id WHERE `+where+`)
ORDER BY trackers.position, trackers.id`); err != nil {
		return nil, err
	}
	out := make([]trackerInfo, len(rows))
	for i, r := range rows {
		out[i] = trackerInfo{ID: r.ID, Name: r.Name}
		_ = json.Unmarshal([]byte(r.Disabled), &out[i].Disabled)
	}
	return out, nil
}

// disabledCoreFields は Tracker.disabled_core_fields(trackers) (全トラッカーで無効なフィールド)。
func disabledCoreFields(ts []trackerInfo) []string {
	if len(ts) == 0 {
		return nil
	}
	out := ts[0].Disabled
	for _, t := range ts[1:] {
		var keep []string
		for _, f := range out {
			if contains(t.Disabled, f) {
				keep = append(keep, f)
			}
		}
		out = keep
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// sharedVersionsCondition は project.shared_versions の WHERE 句 (versions と projects を参照)。
func (q *Query) sharedVersionsCondition(ctx context.Context, p *domain.Project) (string, error) {
	pid := itoa(p.ID)
	root := p.ID
	if p.ParentID != nil {
		var r int64
		if err := q.env.Q.Get(ctx, &r, `SELECT ancestor_id FROM project_closure WHERE descendant_id = ? ORDER BY depth DESC LIMIT 1`, p.ID); err != nil {
			return "", err
		}
		root = r
	}
	return "(projects.id = " + pid + " OR (projects.status <> " + itoa(domain.ProjectStatusArchived) + " AND (" +
		" versions.sharing = 'system'" +
		" OR (projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + itoa(root) + ") AND versions.sharing = 'tree')" +
		" OR (projects.id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id = " + pid + " AND depth > 0) AND versions.sharing IN ('hierarchy', 'descendants'))" +
		" OR (projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + pid + " AND depth > 0) AND versions.sharing = 'hierarchy')" +
		")))", nil
}

// versionScopeCondition は (project ? project.shared_versions : Version.visible) の WHERE 句。
func (q *Query) versionScopeCondition(ctx context.Context) (string, error) {
	if q.Project != nil {
		return q.sharedVersionsCondition(ctx, q.Project)
	}
	return q.env.Auth.AllowedToCondition(ctx, "view_issues", authz.ConditionOptions{}, nil)
}

// customFieldsForIssues は issue_custom_fields (project ? project.rolled_up_custom_fields : IssueCustomField.sorted) の WHERE 句。
func (q *Query) issueCustomFieldsWhere(ctx context.Context) (string, []any, error) {
	t := q.env.boolLit(true)
	if q.Project == nil {
		return "custom_fields.owner_kind = 'issue'", nil, nil
	}
	leaf, err := q.projectIsLeaf(ctx)
	if err != nil {
		return "", nil, err
	}
	if leaf {
		return "custom_fields.owner_kind = 'issue' AND (custom_fields.is_for_all = " + t +
			" OR custom_fields.id IN (SELECT DISTINCT cfp.custom_field_id FROM custom_fields_projects cfp WHERE cfp.project_id = " + itoa(q.Project.ID) + "))", nil, nil
	}
	return "custom_fields.owner_kind = 'issue' AND (custom_fields.is_for_all = " + t +
		" OR EXISTS (SELECT 1 FROM custom_fields_projects cfp JOIN projects p ON p.id = cfp.project_id" +
		" WHERE cfp.custom_field_id = custom_fields.id AND p.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + itoa(q.Project.ID) + ")))", nil, nil
}

// visibleCustomFields は scope.visible (where は custom_fields を参照する条件)。
func (q *Query) visibleCustomFields(ctx context.Context, where string, args ...any) ([]*customfield.CustomField, error) {
	vis, err := customfield.VisibleCondition(ctx, q.env.Auth)
	if err != nil {
		return nil, err
	}
	return customfield.Load(ctx, q.env.Q, "("+vis+") AND ("+where+")", args...)
}

// allowedTo は User.current.allowed_to?(perm, project, global: true)。
func (q *Query) allowedTo(ctx context.Context, perm string, project *domain.Project, global bool) (bool, error) {
	if project != nil {
		return q.env.Auth.AllowedTo(ctx, domain.Perm(perm), project)
	}
	if global {
		return q.env.Auth.AllowedToGlobally(ctx, domain.Perm(perm), nil)
	}
	return false, nil
}

// givableGroupIDs は Group.givable の id。
func (q *Query) givableGroupIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := q.env.Q.Select(ctx, &ids, `SELECT id FROM principals WHERE kind = 'group' ORDER BY id`)
	return ids, err
}

// groupMemberIDs は group.user_ids (+ グループ自身)。
func (q *Query) groupMembersAndSelf(ctx context.Context, groupIDs []int64, includeSelf bool) ([]string, error) {
	set := map[int64]bool{}
	for _, gid := range groupIDs {
		var uids []int64
		if err := q.env.Q.Select(ctx, &uids, `SELECT user_id FROM group_users WHERE group_id = ?`, gid); err != nil {
			return nil, err
		}
		for _, u := range uids {
			set[u] = true
		}
		if includeSelf {
			set[gid] = true
		}
	}
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sortInt64(ids)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = itoa(id)
	}
	return out, nil
}

// existingGroups は Group.where(:id => value) (givable / 組込を問わずグループ種別のもの)。
func (q *Query) existingGroups(ctx context.Context, values []string) ([]int64, error) {
	var ids []int64
	for _, v := range values {
		if id, ok := parseID(v); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var out []int64
	err := q.env.Q.Select(ctx, &out, `SELECT id FROM principals WHERE kind IN ('group', 'group_anonymous', 'group_non_member') AND id IN (`+idList(ids)+`) ORDER BY id`)
	return out, err
}

func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
