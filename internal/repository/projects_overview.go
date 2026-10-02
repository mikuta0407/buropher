package repository

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはプロジェクトの概要（projects#show）・一覧で使う集計と関連の読み込み。
// 可視性の SQL は呼び出し側（authz）が作って渡す（repository は authz に依存しない規約）。

// IssueCountsByTracker は Issue.visible(.open).where(cond).group(:tracker).count。
// visible / cond は issues と projects を参照する WHERE 句断片（空なら条件なし）。
func IssueCountsByTracker(ctx context.Context, q db.Queryer, visible, cond string, openOnly bool) (map[int64]int64, error) {
	where := andConds(visible, cond)
	if openOnly {
		where = andConds(where, "issue_statuses.is_closed = "+q.Dialect().BoolLiteral(false))
	}
	var rows []struct {
		TrackerID int64 `db:"tracker_id"`
		N         int64 `db:"n"`
	}
	if err := q.Select(ctx, &rows, `SELECT issues.tracker_id, COUNT(*) AS n FROM issues
JOIN projects ON projects.id = issues.project_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
WHERE `+where+` GROUP BY issues.tracker_id`); err != nil {
		return nil, err
	}
	out := map[int64]int64{}
	for _, r := range rows {
		out[r.TrackerID] = r.N
	}
	return out, nil
}

// SumIssueEstimatedHours は Issue.visible.where(cond).sum(:estimated_hours)。
func SumIssueEstimatedHours(ctx context.Context, q db.Queryer, visible, cond string) (float64, error) {
	var v sql.NullFloat64
	err := q.Get(ctx, &v, `SELECT SUM(issues.estimated_hours) FROM issues JOIN projects ON projects.id = issues.project_id
WHERE `+andConds(visible, cond))
	return v.Float64, err
}

// SumTimeEntryHours は TimeEntry.visible.where(cond).sum(:hours)。
// visible / cond は time_entries と projects を参照する WHERE 句断片。
func SumTimeEntryHours(ctx context.Context, q db.Queryer, visible, cond string) (float64, error) {
	var v sql.NullFloat64
	err := q.Get(ctx, &v, `SELECT SUM(time_entries.hours) FROM time_entries JOIN projects ON projects.id = time_entries.project_id
WHERE `+andConds(visible, cond))
	return v.Float64, err
}

func andConds(conds ...string) string {
	var parts []string
	for _, c := range conds {
		if strings.TrimSpace(c) != "" {
			parts = append(parts, "("+c+")")
		}
	}
	if len(parts) == 0 {
		return "1=1"
	}
	return strings.Join(parts, " AND ")
}

// RolledUpTrackers は Project#rolled_up_trackers(include_subprojects).visible の移植。
// visible は Tracker.visible の条件（trackers と projects を参照する。空なら全て）で、
// project_trackers 経由で結合したプロジェクトに対して評価される。
func RolledUpTrackers(ctx context.Context, q db.Queryer, p *domain.Project, withSubprojects bool, visible string) ([]*domain.Tracker, error) {
	// Rails は rolled_up_trackers と visible の joins(:projects) を 1 つの結合にまとめるため、
	// 両方の条件は同じプロジェクト行に対して評価される（EXISTS の相関サブクエリで表す）。
	scope := "projects.id = " + strconv.FormatInt(p.ID, 10)
	if withSubprojects {
		scope = "projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + strconv.FormatInt(p.ID, 10) + ")"
	}
	where := `EXISTS (SELECT 1 FROM project_trackers JOIN projects ON projects.id = project_trackers.project_id
  WHERE project_trackers.tracker_id = trackers.id AND ` + scope + ` AND projects.status <> ` + strconv.Itoa(domain.ProjectStatusArchived) + `
  AND EXISTS (SELECT 1 FROM project_modules rem WHERE rem.project_id = projects.id AND rem.name = 'issue_tracking')
  AND (` + andConds(visible) + `))`
	return loadTrackersWhere(ctx, q, where)
}

func loadTrackersWhere(ctx context.Context, q db.Queryer, where string) ([]*domain.Tracker, error) {
	var rows []trackerRow
	if err := q.Select(ctx, &rows, `SELECT id, name, description, position, is_in_roadmap, default_status_id, disabled_core_fields
FROM trackers WHERE `+where+` ORDER BY position, id`); err != nil {
		return nil, err
	}
	out := make([]*domain.Tracker, len(rows))
	for i := range rows {
		out[i] = rows[i].tracker()
	}
	return out, nil
}

// ProjectTrackers は project.trackers（position 順）。
func ProjectTrackers(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.Tracker, error) {
	return loadTrackersWhere(ctx, q, `id IN (SELECT tracker_id FROM project_trackers WHERE project_id = `+strconv.FormatInt(projectID, 10)+`)`)
}

// ProjectNews は project.news.limit(n).reorder(created_on DESC)（project / author を preload）。
func ProjectNews(ctx context.Context, q db.Queryer, projectID int64, limit int) ([]*domain.News, error) {
	var rows []newsRow
	if err := q.Select(ctx, &rows, `SELECT news.id, news.project_id, news.title, news.summary, news.description, news.author_id,
  news.comments_count, news.created_at
FROM news WHERE news.project_id = ?
ORDER BY news.created_at DESC, news.id DESC `+q.Dialect().LimitOffset(limit, 0), projectID); err != nil {
		return nil, err
	}
	return preloadNews(ctx, q, rows)
}

// MemberPrincipal はメンバー一覧の 1 件（principal とロール）。
type MemberPrincipal struct {
	Member *domain.Member
	// User はユーザーの場合（AnonymousUser を含む）。
	User *domain.User
	// Group はグループ（組込グループを含む）の場合。
	Group *domain.Group
}

// Principal は principal の共通部分。
func (m *MemberPrincipal) Principal() *domain.Principal {
	if m.User != nil {
		return &m.User.Principal
	}
	if m.Group != nil {
		return &m.Group.Principal
	}
	return nil
}

// ProjectMembersWithPrincipals は project.memberships（principal 付き、members.id 順）。
// activeOnly なら Member.active（principal が有効なもの）に絞る。
func ProjectMembersWithPrincipals(ctx context.Context, q db.Queryer, projectID int64, activeOnly bool) ([]*MemberPrincipal, error) {
	where := `m.project_id = ?`
	if activeOnly {
		where += ` AND m.principal_id IN (SELECT id FROM principals WHERE status = ` + strconv.Itoa(domain.StatusActive) + `)`
	}
	members, err := loadMembers(ctx, q, where, projectID)
	if err != nil {
		return nil, err
	}
	return attachPrincipals(ctx, q, members)
}

func attachPrincipals(ctx context.Context, q db.Queryer, members []*domain.Member) ([]*MemberPrincipal, error) {
	ids := make([]int64, len(members))
	for i, m := range members {
		ids[i] = m.PrincipalID
	}
	users, groups, err := PrincipalsByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := make([]*MemberPrincipal, 0, len(members))
	for _, m := range members {
		mp := &MemberPrincipal{Member: m, User: users[m.PrincipalID], Group: groups[m.PrincipalID]}
		out = append(out, mp)
	}
	return out, nil
}

// PrincipalsByIDs は id のユーザーとグループを読み込む。
func PrincipalsByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*domain.User, map[int64]*domain.Group, error) {
	users := map[int64]*domain.User{}
	groups := map[int64]*domain.Group{}
	ids = uniqIDs(ids)
	for _, chunk := range chunkIDs(ids) {
		query, args, err := db.In(userSelect+` WHERE p.id IN (?)`, chunk)
		if err != nil {
			return nil, nil, err
		}
		var rows []userRow
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, nil, err
		}
		for i := range rows {
			r := &rows[i]
			if domain.PrincipalKind(r.Kind).IsGroup() {
				groups[r.ID] = &domain.Group{Principal: r.principal()}
			} else {
				users[r.ID] = r.user()
			}
		}
	}
	return users, groups, nil
}

// GetMemberWithPrincipal は id のメンバー（principal 付き）。
func GetMemberWithPrincipal(ctx context.Context, q db.Queryer, id int64) (*MemberPrincipal, error) {
	m, err := GetMember(ctx, q, id)
	if err != nil {
		return nil, err
	}
	mps, err := attachPrincipals(ctx, q, []*domain.Member{m})
	if err != nil {
		return nil, err
	}
	return mps[0], nil
}

// ProjectMemberCount は project.members.count（有効なユーザーのメンバー数）。
func ProjectMemberCount(ctx context.Context, q db.Queryer, projectID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM members m JOIN principals p ON p.id = m.principal_id
WHERE m.project_id = ? AND p.kind = 'user' AND p.status = ?`, projectID, domain.StatusActive)
	return n, err
}

// CountWhere は SELECT COUNT(*) FROM table WHERE where。
func CountWhere(ctx context.Context, q db.Queryer, table, where string, args ...any) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM `+table+` WHERE `+where, args...)
	return n, err
}

// ProjectIssueCustomFieldIDs は project.issue_custom_field_ids（custom_fields_projects の issue CF、position 順）。
func ProjectIssueCustomFieldIDs(ctx context.Context, q db.Queryer, projectID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT cf.id FROM custom_fields cf JOIN custom_fields_projects cfp ON cfp.custom_field_id = cf.id
WHERE cfp.project_id = ? AND cf.owner_kind = 'issue' ORDER BY cf.position, cf.id`, projectID)
	return ids, err
}

// SetProjectIssueCustomFields は project.issue_custom_field_ids=。
func SetProjectIssueCustomFields(ctx context.Context, q db.Queryer, projectID int64, ids []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM custom_fields_projects WHERE project_id = ?
  AND custom_field_id IN (SELECT id FROM custom_fields WHERE owner_kind = 'issue')`, projectID); err != nil {
		return err
	}
	for _, id := range uniqIDs(ids) {
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM custom_fields WHERE id = ? AND owner_kind = 'issue'`, id); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if _, err := q.Exec(ctx, `INSERT INTO custom_fields_projects (custom_field_id, project_id) VALUES (?, ?)`, id, projectID); err != nil {
			return err
		}
	}
	return nil
}

// SetProjectTrackers は project.tracker_ids=。
func SetProjectTrackers(ctx context.Context, q db.Queryer, projectID int64, ids []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM project_trackers WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	for _, id := range uniqIDs(ids) {
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM trackers WHERE id = ?`, id); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if _, err := q.Exec(ctx, `INSERT INTO project_trackers (project_id, tracker_id) VALUES (?, ?)`, projectID, id); err != nil {
			return err
		}
	}
	return nil
}

// ProjectIdentifierTaken は validates_uniqueness_of :identifier（大文字小文字を区別）。
func ProjectIdentifierTaken(ctx context.Context, q db.Queryer, identifier string, excludeID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM projects WHERE identifier = ? AND id <> ?`, identifier, excludeID)
}

// LastProjectIdentifier は Project.order('id DESC').first.identifier（無ければ ""）。
func LastProjectIdentifier(ctx context.Context, q db.Queryer) (string, bool, error) {
	var ids []string
	if err := q.Select(ctx, &ids, `SELECT identifier FROM projects ORDER BY id DESC `+q.Dialect().LimitOffset(1, 0)); err != nil {
		return "", false, err
	}
	if len(ids) == 0 {
		return "", false, nil
	}
	return ids[0], true, nil
}

// ProjectCustomFieldInfos は ProjectCustomField（position 順）。
func ProjectCustomFieldInfos(ctx context.Context, q db.Queryer) ([]*domain.CustomFieldInfo, error) {
	return CustomFieldInfosByKind(ctx, q, "project")
}

// VisibleIDs は ids のうち cond（projects を参照）を満たすプロジェクトの id。
func VisibleProjectIDsAmong(ctx context.Context, q db.Queryer, ids []int64, cond string) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []int64
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	err := q.Select(ctx, &out, `SELECT id FROM projects WHERE id IN (`+strings.Join(parts, ",")+`) AND (`+andConds(cond)+`)`)
	slices.Sort(out)
	return out, err
}

// UserProjectsAll は user.projects（アーカイブされていないメンバーシップのプロジェクト、ツリー順）。
func UserProjectsAll(ctx context.Context, q db.Queryer, userID int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.status <> `+strconv.Itoa(domain.ProjectStatusArchived)+
		` AND projects.id IN (SELECT project_id FROM members WHERE principal_id = ?)`, userID)
}

// ProjectEnabledModules は project.enabled_modules（id 順）。
func ProjectEnabledModules(ctx context.Context, q db.Queryer, projectID int64) ([]domain.EnabledModule, error) {
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, name FROM project_modules WHERE project_id = ? ORDER BY id`, projectID); err != nil {
		return nil, err
	}
	out := make([]domain.EnabledModule, len(rows))
	for i, r := range rows {
		out[i] = domain.EnabledModule{ID: r.ID, ProjectID: projectID, Name: r.Name}
	}
	return out, nil
}

// MemberOfMemberRole は member_roles.id のメンバー（継承元の判定に使う）。
func MemberOfMemberRole(ctx context.Context, q db.Queryer, memberRoleID int64) (*domain.Member, error) {
	return oneMember(loadMembers(ctx, q, `m.id IN (SELECT member_id FROM member_roles WHERE id = ?)`, memberRoleID))
}

// ReapplyInheritedMemberRoles は remove_inherited_member_roles + add_inherited_member_roles。
// Redmine は新規作成時（parent_id と inherit_members の両方が変化）に update_inherited_members と
// 親変更時のコールバックで継承メンバーを 2 回作り直すため、id の採番もそれに合わせる。
func ReapplyInheritedMemberRoles(ctx context.Context, q db.Queryer, p *domain.Project) error {
	if err := removeInheritedMemberRoles(ctx, q, p.ID); err != nil {
		return err
	}
	return addInheritedMemberRoles(ctx, q, p)
}

// EnsureProjectWiki は EnabledModule#module_enabled（wiki モジュールを有効にしたら Wiki を作る）。
func EnsureProjectWiki(ctx context.Context, q db.Queryer, projectID int64) error {
	ok, err := ProjectHasWiki(ctx, q, projectID)
	if err != nil || ok {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO wikis (project_id, start_page) VALUES (?, 'Wiki')`, projectID)
	return err
}
