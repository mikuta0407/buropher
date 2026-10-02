package query

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// フィルタの選択肢 (UI 用。Redmine の *_values メソッド)。

func isNoRows(err error) bool { return errors.Is(err, db.ErrNoRows) || errors.Is(err, sql.ErrNoRows) }

// principalInfo は選択肢の作成に必要なプリンシパルの属性。
type principalInfo struct {
	ID        int64          `db:"id"`
	Kind      string         `db:"kind"`
	Status    int            `db:"status"`
	Firstname string         `db:"firstname"`
	Lastname  string         `db:"lastname"`
	Name      string         `db:"name"`
	Login     sql.NullString `db:"login"`
}

// displayName は User#name (Setting.user_format) / Group#name。
func displayName(p principalInfo, format string) string {
	if p.Kind != string(domain.KindUser) && p.Kind != string(domain.KindAnonymousUser) {
		return p.Name
	}
	first, last := p.Firstname, p.Lastname
	firstRune := func(s string) string {
		for _, r := range s {
			return string(r)
		}
		return ""
	}
	switch format {
	case "firstname_lastinitial":
		return first + " " + firstRune(last) + "."
	case "firstinitial_lastname":
		var parts []string
		for _, w := range strings.Fields(first) {
			parts = append(parts, firstRune(w)+".")
		}
		return strings.Join(parts, " ") + " " + last
	case "firstname":
		return first
	case "lastname_firstname":
		return last + " " + first
	case "lastnamefirstname":
		return last + first
	case "lastname_comma_firstname":
		return last + ", " + first
	case "lastname":
		return last
	case "username":
		return p.Login.String
	}
	return first + " " + last
}

// statusLabel は l("status_#{User::LABEL_BY_STATUS[status]}")。
func (q *Query) statusLabel(status int) string {
	switch status {
	case domain.StatusActive:
		return q.env.l("status_active")
	case domain.StatusRegistered:
		return q.env.l("status_registered")
	case domain.StatusLocked:
		return q.env.l("status_locked")
	}
	return q.env.l("status_anon")
}

// allProjects は Project.visible をツリー順で返す。
func (q *Query) allProjects(ctx context.Context) ([]*domain.Project, error) {
	cond, err := q.env.Auth.VisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return nil, err
	}
	ps, err := repository.LoadProjects(ctx, q.env.Q, cond)
	if err != nil {
		return nil, err
	}
	ns, err := q.nestedSet(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(ps, func(a, b *domain.Project) int {
		if la, lb := ns[a.ID].Lft, ns[b.ID].Lft; la != lb {
			return la - lb
		}
		return int(a.ID - b.ID)
	})
	return ps, nil
}

// allProjectsValues は all_projects_values (ツリーの深さを "--" で表す)。
func (q *Query) allProjectsValues(ctx context.Context) ([]Option, error) {
	ps, err := q.allProjects(ctx)
	if err != nil {
		return nil, err
	}
	var out []Option
	var ancestors []*domain.Project
	isDescendant := func(p, anc *domain.Project) (bool, error) {
		var n int
		err := q.env.Q.Get(ctx, &n, `SELECT COUNT(*) FROM project_closure WHERE ancestor_id = ? AND descendant_id = ? AND depth > 0`, anc.ID, p.ID)
		return n > 0, err
	}
	for _, p := range ps {
		for len(ancestors) > 0 {
			ok, err := isDescendant(p, ancestors[len(ancestors)-1])
			if err != nil {
				return nil, err
			}
			if ok {
				break
			}
			ancestors = ancestors[:len(ancestors)-1]
		}
		prefix := ""
		if level := len(ancestors); level > 0 {
			prefix = strings.Repeat("--", level) + " "
		}
		out = append(out, Option{Label: prefix + p.Name, Value: itoa(p.ID)})
		ancestors = append(ancestors, p)
	}
	return out, nil
}

// projectValues は project_values (<< 自分のプロジェクト >> / << ブックマーク >> + 全プロジェクト)。
func (q *Query) projectValues(ctx context.Context) ([]Option, error) {
	var out []Option
	if q.env.User().Logged() {
		ids, err := q.env.Auth.ProjectIDs(ctx)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			out = append(out, Option{Label: "<< " + strings.ToLower(q.env.l("label_my_projects")) + " >>", Value: "mine"})
		}
		bm, err := q.env.BookmarkedProjectIDs(ctx)
		if err != nil {
			return nil, err
		}
		if len(bm) > 0 {
			out = append(out, Option{Label: "<< " + strings.ToLower(q.env.l("label_my_bookmarks")) + " >>", Value: "bookmarks"})
		}
	}
	all, err := q.allProjectsValues(ctx)
	return append(out, all...), err
}

// subprojectValues は subproject_values (可視な子孫プロジェクト)。
func (q *Query) subprojectValues(ctx context.Context) ([]Option, error) {
	cond, err := q.env.Auth.VisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT projects.id, projects.name FROM projects JOIN project_closure pc ON pc.descendant_id = projects.id
WHERE pc.ancestor_id = ? AND pc.depth > 0 AND (`+cond+`) ORDER BY projects.id`, q.Project.ID); err != nil {
		return nil, err
	}
	out := make([]Option, len(rows))
	for i, r := range rows {
		out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
	}
	return out, nil
}

// principals は Query#principals (プロジェクト (と子孫) または全可視プロジェクトのメンバー)。
func (q *Query) principals(ctx context.Context) ([]principalInfo, error) {
	var projectIDs []int64
	if q.Project != nil {
		projectIDs = []int64{q.Project.ID}
		leaf, err := q.projectIsLeaf(ctx)
		if err != nil {
			return nil, err
		}
		if !leaf {
			cond, err := q.env.Auth.VisibleCondition(ctx, authz.ConditionOptions{})
			if err != nil {
				return nil, err
			}
			var ids []int64
			if err := q.env.Q.Select(ctx, &ids, `SELECT projects.id FROM projects JOIN project_closure pc ON pc.descendant_id = projects.id
WHERE pc.ancestor_id = ? AND pc.depth > 0 AND (`+cond+`)`, q.Project.ID); err != nil {
				return nil, err
			}
			projectIDs = append(projectIDs, ids...)
		}
	} else {
		ps, err := q.allProjects(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			projectIDs = append(projectIDs, p.ID)
		}
	}
	if len(projectIDs) == 0 {
		return nil, nil
	}
	vis, err := q.env.Auth.PrincipalVisibleCondition(ctx)
	if err != nil {
		return nil, err
	}
	var rows []principalInfo
	if err := q.env.Q.Select(ctx, &rows, `SELECT principals.id, principals.kind, principals.status, principals.firstname, principals.lastname,
  principals.name, ua.login FROM principals LEFT JOIN user_accounts ua ON ua.principal_id = principals.id
WHERE principals.status IN (1, 3) AND principals.kind NOT IN ('group_anonymous', 'group_non_member')
  AND principals.id IN (SELECT DISTINCT principal_id FROM members WHERE project_id IN (`+idList(projectIDs)+`)) AND (`+vis+`)`); err != nil {
		return nil, err
	}
	uf := q.env.setting("user_format")
	slices.SortStableFunc(rows, func(a, b principalInfo) int { return comparePrincipals(a, b, uf) })
	return rows, nil
}

// comparePrincipals は Principal#<=> (同種は名前の大文字小文字無視比較、ユーザがグループより先)。
func comparePrincipals(a, b principalInfo, uf string) int {
	ga, gb := !isUserKind(a.Kind), !isUserKind(b.Kind)
	if ga != gb {
		if ga {
			return 1
		}
		return -1
	}
	return strings.Compare(strings.ToLower(displayName(a, uf)), strings.ToLower(displayName(b, uf)))
}

func isUserKind(k string) bool {
	return k == string(domain.KindUser) || k == string(domain.KindAnonymousUser)
}

func (q *Query) principalOptions(ps []principalInfo) []Option {
	uf := q.env.setting("user_format")
	sorted := slices.Clone(ps)
	slices.SortStableFunc(sorted, func(a, b principalInfo) int {
		if a.Status != b.Status {
			return a.Status - b.Status
		}
		return comparePrincipals(a, b, uf)
	})
	out := make([]Option, len(sorted))
	for i, p := range sorted {
		out[i] = Option{Label: displayName(p, uf), Value: itoa(p.ID), Group: q.statusLabel(p.Status)}
	}
	return out
}

// authorValues は author_values (<< 自分 >> + メンバーのユーザ + 匿名ユーザ)。
func (q *Query) authorValues(ctx context.Context) ([]Option, error) {
	var out []Option
	if q.env.User().Logged() {
		out = append(out, Option{Label: "<< " + q.env.l("label_me") + " >>", Value: "me"})
	}
	ps, err := q.principals(ctx)
	if err != nil {
		return nil, err
	}
	var users []principalInfo
	for _, p := range ps {
		if isUserKind(p.Kind) {
			users = append(users, p)
		}
	}
	out = append(out, q.principalOptions(users)...)
	anon, err := repository.AnonymousUser(ctx, q.env.Q)
	if err != nil {
		return nil, err
	}
	return append(out, Option{Label: q.env.l("label_user_anonymous"), Value: itoa(anon.ID)}), nil
}

// assignedToValues は assigned_to_values (グループへの割り当てが有効ならグループも含む)。
func (q *Query) assignedToValues(ctx context.Context) ([]Option, error) {
	var out []Option
	if q.env.User().Logged() {
		out = append(out, Option{Label: "<< " + q.env.l("label_me") + " >>", Value: "me"})
	}
	ps, err := q.principals(ctx)
	if err != nil {
		return nil, err
	}
	if !q.env.settingBool("issue_group_assignment") {
		ps = slices.DeleteFunc(slices.Clone(ps), func(p principalInfo) bool { return !isUserKind(p.Kind) })
	}
	return append(out, q.principalOptions(ps)...), nil
}

// watcherValues は watcher_values。
func (q *Query) watcherValues(ctx context.Context) ([]Option, error) {
	out := []Option{{Label: "<< " + q.env.l("label_me") + " >>", Value: "me"}}
	ok, err := q.allowedTo(ctx, "view_issue_watchers", q.Project, true)
	if err != nil || !ok {
		return out, err
	}
	ps, err := q.principals(ctx)
	if err != nil {
		return nil, err
	}
	return append(out, q.principalOptions(ps)...), nil
}

// groupValues は Group.givable.visible の [name, id]。
func (q *Query) groupValues(ctx context.Context) ([]Option, error) {
	vis, err := q.env.Auth.PrincipalVisibleCondition(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT id, name FROM principals WHERE kind = 'group' AND (`+vis+`) ORDER BY id`); err != nil {
		return nil, err
	}
	out := make([]Option, len(rows))
	for i, r := range rows {
		out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
	}
	return out, nil
}

// roleValues は Role.givable の [name, id]。
func (q *Query) roleValues(ctx context.Context) ([]Option, error) {
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT id, name FROM roles WHERE builtin = 0 ORDER BY position, id`); err != nil {
		return nil, err
	}
	out := make([]Option, len(rows))
	for i, r := range rows {
		out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
	}
	return out, nil
}

// priorityValues は IssuePriority の [name, id] (Enumeration の default_scope は position 順)。
func (q *Query) priorityValues(ctx context.Context) ([]Option, error) {
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT id, name FROM issue_priorities ORDER BY position, id`); err != nil {
		return nil, err
	}
	out := make([]Option, len(rows))
	for i, r := range rows {
		out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
	}
	return out, nil
}

// issueStatusesValues は issue_statuses_values (プロジェクトでは rolled_up_statuses)。
func (q *Query) issueStatusesValues(ctx context.Context) ([]Option, error) {
	where := "1=1"
	if q.Project != nil {
		where = `id IN (SELECT old_status_id FROM workflow_transitions WHERE tracker_id IN (` + q.rolledUpTrackersSQL() + `) AND old_status_id IS NOT NULL AND old_status_id <> new_status_id
UNION SELECT new_status_id FROM workflow_transitions WHERE tracker_id IN (` + q.rolledUpTrackersSQL() + `) AND (old_status_id IS NULL OR old_status_id <> new_status_id))`
	}
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT id, name FROM issue_statuses WHERE `+where+` ORDER BY position, id`); err != nil {
		return nil, err
	}
	out := make([]Option, len(rows))
	for i, r := range rows {
		out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
	}
	return out, nil
}

// rolledUpTrackersSQL は project.rolled_up_trackers の id を返す副問い合わせ。
func (q *Query) rolledUpTrackersSQL() string {
	return `SELECT pt.tracker_id FROM project_trackers pt JOIN projects p ON p.id = pt.project_id
WHERE p.status <> 9 AND EXISTS (SELECT 1 FROM project_modules em WHERE em.project_id = p.id AND em.name = 'issue_tracking')
AND p.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ` + itoa(q.Project.ID) + `)`
}

// projectStatusesValues は project_statuses_values (ProjectAdminQuery はアーカイブ・削除予約も含む)。
func (q *Query) projectStatusesValues(ctx context.Context) ([]Option, error) {
	out := []Option{
		{Label: q.env.l("project_status_active"), Value: "1"},
		{Label: q.env.l("project_status_closed"), Value: "5"},
	}
	if q.Kind == KindProjectAdmin {
		out = append(out, Option{Label: q.env.l("project_status_archived"), Value: "9"},
			Option{Label: q.env.l("project_status_scheduled_for_deletion"), Value: "10"})
	}
	return out, nil
}

// categoryValues は project.issue_categories の [name, id]。
func (q *Query) categoryValues(ctx context.Context) ([]Option, error) {
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT id, name FROM issue_categories WHERE project_id = ? ORDER BY name, id`, q.Project.ID); err != nil {
		return nil, err
	}
	out := make([]Option, len(rows))
	for i, r := range rows {
		out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
	}
	return out, nil
}

// versionInfo は選択肢用のバージョンの属性。
type versionInfo struct {
	ID            int64       `db:"id"`
	Name          string      `db:"name"`
	Status        string      `db:"status"`
	EffectiveDate db.NullDate `db:"effective_date"`
	ProjectName   string      `db:"project_name"`
}

// fixedVersionValues は fixed_version_values (Version.sort_by_status の順)。
func (q *Query) fixedVersionValues(ctx context.Context) ([]Option, error) {
	cond, err := q.versionScopeCondition(ctx)
	if err != nil {
		return nil, err
	}
	return q.versionOptions(ctx, cond)
}

func (q *Query) versionOptions(ctx context.Context, cond string, args ...any) ([]Option, error) {
	var vs []versionInfo
	if err := q.env.Q.Select(ctx, &vs, `SELECT versions.id, versions.name, versions.status, versions.effective_date, projects.name AS project_name
FROM versions JOIN projects ON projects.id = versions.project_id WHERE `+cond, args...); err != nil {
		return nil, err
	}
	sortVersionsByStatus(vs)
	out := make([]Option, len(vs))
	for i, v := range vs {
		out[i] = Option{Label: v.ProjectName + " - " + v.Name, Value: itoa(v.ID), Group: q.env.l("version_status_" + v.Status)}
	}
	return out, nil
}

// sortVersionsByStatus は Version.sort_by_status (ステータスの降順、同じなら Version#<=>)。
func sortVersionsByStatus(vs []versionInfo) {
	slices.SortStableFunc(vs, func(a, b versionInfo) int {
		if a.Status != b.Status {
			return strings.Compare(b.Status, a.Status)
		}
		return compareVersions(a, b)
	})
}

func compareVersions(a, b versionInfo) int {
	switch {
	case a.EffectiveDate.Valid && b.EffectiveDate.Valid:
		if !a.EffectiveDate.Date.Equal(b.EffectiveDate.Date.Time) {
			return a.EffectiveDate.Date.Compare(b.EffectiveDate.Date.Time)
		}
	case a.EffectiveDate.Valid:
		return -1
	case b.EffectiveDate.Valid:
		return 1
	}
	if a.Name == b.Name {
		return int(a.ID - b.ID)
	}
	return strings.Compare(a.Name, b.Name)
}

// customFieldFilterValues はカスタムフィールドのフィルタの選択肢 (query_filter_values)。
func (q *Query) customFieldFilterValues(ctx context.Context, cf *customfield.CustomField) ([]Option, error) {
	switch cf.FieldFormat {
	case "list":
		out := make([]Option, len(cf.PossibleValues))
		for i, v := range cf.PossibleValues {
			out[i] = Option{Label: v, Value: v}
		}
		return out, nil
	case "bool":
		return []Option{{Label: q.env.l("general_text_Yes"), Value: "1"}, {Label: q.env.l("general_text_No"), Value: "0"}}, nil
	case "enumeration":
		var rows []struct {
			ID   int64  `db:"id"`
			Name string `db:"name"`
		}
		if err := q.env.Q.Select(ctx, &rows, `SELECT id, name FROM custom_field_enumerations WHERE custom_field_id = ? AND active = ? ORDER BY position, id`, cf.ID, true); err != nil {
			return nil, err
		}
		out := make([]Option, len(rows))
		for i, r := range rows {
			out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
		}
		return out, nil
	case "user":
		return q.authorValues(ctx)
	case "version":
		var cond string
		var err error
		if q.Project != nil {
			cond, err = q.sharedVersionsCondition(ctx, q.Project)
		} else {
			var vis string
			vis, err = q.env.Auth.AllowedToCondition(ctx, "view_issues", authz.ConditionOptions{}, nil)
			cond = "(" + vis + ") AND versions.sharing = 'system'"
		}
		if err != nil {
			return nil, err
		}
		return q.versionOptions(ctx, cond)
	}
	return nil, nil
}

// timeEntryActivityValues は TimeEntryQuery の activity_id の選択肢
// ((project ? project.activities : TimeEntryActivity.shared) の [name, (parent_id || id)])。
func (q *Query) timeEntryActivityValues(ctx context.Context) ([]Option, error) {
	where := "project_id IS NULL"
	var args []any
	if q.Project != nil {
		where = "(project_id IS NULL OR project_id = ?) AND id NOT IN (SELECT parent_id FROM time_entry_activities WHERE project_id = ? AND parent_id IS NOT NULL) AND active = ?"
		args = []any{q.Project.ID, q.Project.ID, true}
	}
	var rows []struct {
		ID       int64         `db:"id"`
		Name     string        `db:"name"`
		ParentID sql.NullInt64 `db:"parent_id"`
	}
	if err := q.env.Q.Select(ctx, &rows, `SELECT id, name, parent_id FROM time_entry_activities WHERE `+where+` ORDER BY position, id`, args...); err != nil {
		return nil, err
	}
	out := make([]Option, len(rows))
	for i, r := range rows {
		v := r.ID
		if r.ParentID.Valid {
			v = r.ParentID.Int64
		}
		out[i] = Option{Label: r.Name, Value: itoa(v)}
	}
	return out, nil
}

// FilterJSON は available_filters_as_json の 1 項目。
type FilterJSON struct {
	Type   string     `json:"type"`
	Name   string     `json:"name"`
	Remote bool       `json:"remote,omitempty"`
	Values [][]string `json:"values,omitempty"`
}

// AvailableFiltersAsJSON は available_filters_as_json (フィルタ UI 用)。
// remote なフィルタは、そのフィルタが設定されている場合のみ値を含める。
func (q *Query) AvailableFiltersAsJSON(ctx context.Context) ([]string, map[string]FilterJSON, error) {
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]FilterJSON{}
	for _, def := range af.Defs() {
		j := FilterJSON{Type: def.Type, Name: def.Name, Remote: def.Remote}
		if q.HasFilter(def.Field) || !def.Remote {
			vals, err := def.LoadValues(ctx)
			if err != nil {
				return nil, nil, err
			}
			if vals != nil {
				present := map[string]bool{}
				for _, o := range vals {
					present[o.Value] = true
				}
				// find_<field>_filter_values: 選択済みで選択肢に無いプリンシパルを補う
				if def.Field == "assigned_to_id" || def.Field == "author_id" {
					var missing []int64
					for _, v := range q.ValuesFor(def.Field) {
						if id, ok := parseID(v); ok && !present[v] {
							missing = append(missing, id)
						}
					}
					if len(missing) > 0 {
						extra, err := q.findPrincipalValues(ctx, missing)
						if err != nil {
							return nil, nil, err
						}
						vals = append(slices.Clone(vals), extra...)
					}
				}
				j.Values = make([][]string, len(vals))
				for i, o := range vals {
					if o.Group != "" {
						j.Values[i] = []string{o.Label, o.Value, o.Group}
					} else {
						j.Values[i] = []string{o.Label, o.Value}
					}
				}
			}
		}
		out[def.Field] = j
	}
	return af.Keys(), out, nil
}

// findPrincipalValues は find_assigned_to_id_filter_values (Principal.visible.where(:id => values))。
func (q *Query) findPrincipalValues(ctx context.Context, ids []int64) ([]Option, error) {
	vis, err := q.env.Auth.PrincipalVisibleCondition(ctx)
	if err != nil {
		return nil, err
	}
	var rows []principalInfo
	if err := q.env.Q.Select(ctx, &rows, `SELECT principals.id, principals.kind, principals.status, principals.firstname, principals.lastname,
  principals.name, ua.login FROM principals LEFT JOIN user_accounts ua ON ua.principal_id = principals.id
WHERE principals.id IN (`+idList(ids)+`) AND (`+vis+`) ORDER BY principals.id`); err != nil {
		return nil, err
	}
	uf := q.env.setting("user_format")
	out := make([]Option, len(rows))
	for i, r := range rows {
		out[i] = Option{Label: displayName(r, uf), Value: itoa(r.ID)}
	}
	return out, nil
}
