package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/permission"
)

// ErrInvalidParent は Project#validate_parent の違反 (自身・子孫への移動、非アクティブな親)。
var ErrInvalidParent = errors.New("repository: invalid parent project")

type projectRow struct {
	ID                  int64          `db:"id"`
	ParentID            sql.NullInt64  `db:"parent_id"`
	Name                string         `db:"name"`
	Identifier          string         `db:"identifier"`
	Description         sql.NullString `db:"description"`
	Homepage            sql.NullString `db:"homepage"`
	IsPublic            bool           `db:"is_public"`
	Status              int            `db:"status"`
	InheritMembers      bool           `db:"inherit_members"`
	DefaultVersionID    sql.NullInt64  `db:"default_version_id"`
	DefaultAssignedToID sql.NullInt64  `db:"default_assigned_to_id"`
	DefaultIssueQueryID sql.NullInt64  `db:"default_issue_query_id"`
	CreatedAt           db.Time        `db:"created_at"`
	UpdatedAt           db.Time        `db:"updated_at"`
}

func nullID(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func (r *projectRow) project() *domain.Project {
	return &domain.Project{
		ID: r.ID, ParentID: nullID(r.ParentID), Name: r.Name, Identifier: r.Identifier,
		Description: r.Description.String, Homepage: r.Homepage.String, IsPublic: r.IsPublic,
		Status: r.Status, InheritMembers: r.InheritMembers,
		DefaultVersionID: nullID(r.DefaultVersionID), DefaultAssignedToID: nullID(r.DefaultAssignedToID),
		DefaultIssueQueryID: nullID(r.DefaultIssueQueryID),
		CreatedAt:           r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
		EnabledModuleNames: []string{},
	}
}

const projectCols = `projects.id, projects.parent_id, projects.name, projects.identifier, projects.description,
  projects.homepage, projects.is_public, projects.status, projects.inherit_members, projects.default_version_id,
  projects.default_assigned_to_id, projects.default_issue_query_id, projects.created_at, projects.updated_at`

// LoadProjects は where 条件 (projects を参照可) のプロジェクトを有効モジュール付きで読み込む。
// 並びはツリー順 (Redmine の lft 順, Project.sorted)。
func LoadProjects(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Project, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []projectRow
	if err := q.Select(ctx, &rows, `SELECT `+projectCols+` FROM projects WHERE `+where, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Project, len(rows))
	byID := make(map[int64]*domain.Project, len(rows))
	ids := make([]int64, len(rows))
	for i := range rows {
		out[i] = rows[i].project()
		byID[out[i].ID] = out[i]
		ids[i] = out[i].ID
	}
	for _, chunk := range chunkIDs(ids) {
		query, a, err := db.In(`SELECT project_id, name FROM project_modules WHERE project_id IN (?) ORDER BY id`, chunk)
		if err != nil {
			return nil, err
		}
		var ms []struct {
			ProjectID int64  `db:"project_id"`
			Name      string `db:"name"`
		}
		if err := q.Select(ctx, &ms, query, a...); err != nil {
			return nil, err
		}
		for _, m := range ms {
			p := byID[m.ProjectID]
			p.EnabledModuleNames = append(p.EnabledModuleNames, m.Name)
		}
	}
	if err := SortProjectsByTree(ctx, q, out); err != nil {
		return nil, err
	}
	return out, nil
}

func oneProject(ps []*domain.Project, err error) (*domain.Project, error) {
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, ErrNotFound
	}
	return ps[0], nil
}

// GetProject は id のプロジェクトを返す。
func GetProject(ctx context.Context, q db.Queryer, id int64) (*domain.Project, error) {
	return oneProject(LoadProjects(ctx, q, `projects.id = ?`, id))
}

// FindProjectByIdentifier は識別子でプロジェクトを返す。
func FindProjectByIdentifier(ctx context.Context, q db.Queryer, identifier string) (*domain.Project, error) {
	return oneProject(LoadProjects(ctx, q, `projects.identifier = ?`, identifier))
}

var digitsOnly = regexp.MustCompile(`^\d*$`)

// FindProject は Project.find(param) の移植: 数字のみなら id、それ以外は識別子で探す。
func FindProject(ctx context.Context, q db.Queryer, param string) (*domain.Project, error) {
	if digitsOnly.MatchString(param) {
		id, err := strconv.ParseInt(param, 10, 64)
		if err != nil {
			return nil, ErrNotFound
		}
		return GetProject(ctx, q, id)
	}
	return FindProjectByIdentifier(ctx, q, param)
}

// ListProjects は全プロジェクトをツリー順で返す。
func ListProjects(ctx context.Context, q db.Queryer) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, "")
}

// ProjectAncestors は祖先をルートから親の順で返す (Project#ancestors)。
func ProjectAncestors(ctx context.Context, q db.Queryer, id int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id = ? AND depth > 0)`, id)
}

// ProjectSelfAndAncestors は Project#self_and_ancestors。
func ProjectSelfAndAncestors(ctx context.Context, q db.Queryer, id int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id = ?)`, id)
}

// ProjectDescendants は子孫をツリー順で返す (Project#descendants)。
func ProjectDescendants(ctx context.Context, q db.Queryer, id int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ? AND depth > 0)`, id)
}

// ProjectSelfAndDescendants は Project#self_and_descendants。
func ProjectSelfAndDescendants(ctx context.Context, q db.Queryer, id int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)`, id)
}

// ProjectChildren は子プロジェクトをツリー順 (名前順) で返す (Project#children)。
func ProjectChildren(ctx context.Context, q db.Queryer, id int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.parent_id = ?`, id)
}

// ProjectSelfAndDescendantIDs は自身と子孫の id を返す。
func ProjectSelfAndDescendantIDs(ctx context.Context, q db.Queryer, id int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT descendant_id FROM project_closure WHERE ancestor_id = ? ORDER BY depth, descendant_id`, id)
	return ids, err
}

// IsProjectLeaf は Project#leaf? (子が無ければ true)。
func IsProjectLeaf(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM projects WHERE parent_id = ?`, id)
	return n == 0, err
}

// ---------------------------------------------------------------- ツリー順 (lft 相当)

// NestedSetValue は Redmine の入れ子集合の値 (lft, rgt) に相当する。
type NestedSetValue struct{ Lft, Rgt int }

type treeNode struct {
	id       int64
	parentID int64 // 0 = ルート
	name     string
}

// ProjectNestedSet は全プロジェクトについて、Redmine の ProjectNestedSet が保持する
// lft/rgt に相当する値を計算して返す。
//
// Redmine は兄弟を「名前の昇順」に挿入する (project_nested_set.rb の target_lft) が、比較は DB の
// 照合順序に依存し (MySQL / PG の既定ロケールでは大文字小文字をほぼ無視、SQLite はバイト順)、
// 既存行の並びも操作履歴に依存する。buropher は lft を持たないため、次の決定的な規則で並べる:
//  1. 名前を小文字化して比較 (MySQL / PG 既定照合に近い)
//  2. 同じなら名前のバイト順
//  3. 同名なら id の降順 (Redmine では後から追加・移動した方が同名兄弟の前に入るため)
func ProjectNestedSet(ctx context.Context, q db.Queryer) (map[int64]NestedSetValue, error) {
	var rows []struct {
		ID       int64         `db:"id"`
		ParentID sql.NullInt64 `db:"parent_id"`
		Name     string        `db:"name"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, parent_id, name FROM projects`); err != nil {
		return nil, err
	}
	nodes := make([]treeNode, len(rows))
	for i, r := range rows {
		nodes[i] = treeNode{id: r.ID, parentID: r.ParentID.Int64, name: r.Name}
	}
	return computeNestedSet(nodes), nil
}

func computeNestedSet(nodes []treeNode) map[int64]NestedSetValue {
	children := map[int64][]treeNode{}
	exists := map[int64]bool{}
	for _, n := range nodes {
		exists[n.id] = true
	}
	for _, n := range nodes {
		p := n.parentID
		if !exists[p] {
			p = 0
		}
		children[p] = append(children[p], n)
	}
	for _, cs := range children {
		slices.SortFunc(cs, func(a, b treeNode) int {
			if c := strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); c != 0 {
				return c
			}
			if c := strings.Compare(a.name, b.name); c != 0 {
				return c
			}
			// 同名は id 降順
			switch {
			case a.id > b.id:
				return -1
			case a.id < b.id:
				return 1
			}
			return 0
		})
	}
	out := make(map[int64]NestedSetValue, len(nodes))
	counter := 0
	var walk func(id int64)
	walk = func(id int64) {
		for _, c := range children[id] {
			if _, done := out[c.id]; done {
				continue // 循環防止
			}
			counter++
			lft := counter
			out[c.id] = NestedSetValue{Lft: lft}
			walk(c.id)
			counter++
			out[c.id] = NestedSetValue{Lft: lft, Rgt: counter}
		}
	}
	walk(0)
	return out
}

// SortProjectsByTree は projects をツリー順 (Redmine の lft 順) に並べ替える。
func SortProjectsByTree(ctx context.Context, q db.Queryer, projects []*domain.Project) error {
	if len(projects) < 2 {
		return nil
	}
	ns, err := ProjectNestedSet(ctx, q)
	if err != nil {
		return err
	}
	slices.SortStableFunc(projects, func(a, b *domain.Project) int {
		la, lb := ns[a.ID].Lft, ns[b.ID].Lft
		if la != lb {
			return la - lb
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return nil
}

// ---------------------------------------------------------------- 書き込み

// CreateProjectOptions はプロジェクト作成時の関連データ。
type CreateProjectOptions struct {
	// EnabledModules は有効にするモジュール名 (Project#enabled_module_names=)。
	EnabledModules []string
	// TrackerIDs はプロジェクトのトラッカー。
	TrackerIDs []int64
}

// CreateProject はプロジェクトを作成し、閉包テーブル・モジュール・トラッカーを登録する。
// 親があり inherit_members の場合は親のメンバーを継承ロールとして実体化する
// (after_save add_inherited_member_roles)。入力の検証 (識別子の書式など) は呼び出し側で行う。
func CreateProject(ctx context.Context, q db.Queryer, p *domain.Project, opt CreateProjectOptions) error {
	if p.Status == 0 {
		p.Status = domain.ProjectStatusActive
	}
	if p.ParentID != nil {
		parent, err := GetProject(ctx, q, *p.ParentID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return ErrInvalidParent
			}
			return err
		}
		if !parent.Active() {
			return ErrInvalidParent
		}
	}
	now := db.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now.Time
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now.Time
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO projects (parent_id, name, identifier, description, homepage, is_public, status,
  inherit_members, default_version_id, default_assigned_to_id, default_issue_query_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ParentID, p.Name, p.Identifier, nullString(p.Description), nullString(p.Homepage), p.IsPublic, p.Status,
		p.InheritMembers, p.DefaultVersionID, p.DefaultAssignedToID, p.DefaultIssueQueryID,
		db.NewTime(p.CreatedAt), db.NewTime(p.UpdatedAt))
	if err != nil {
		return err
	}
	p.ID = id
	if err := insertClosure(ctx, q, id, p.ParentID); err != nil {
		return err
	}
	if err := SetEnabledModules(ctx, q, id, opt.EnabledModules); err != nil {
		return err
	}
	for _, t := range uniqIDs(opt.TrackerIDs) {
		if _, err := q.Exec(ctx, `INSERT INTO project_trackers (project_id, tracker_id) VALUES (?, ?)`, id, t); err != nil {
			return err
		}
	}
	p.EnabledModuleNames = enabledNames(opt.EnabledModules)
	if p.ParentID != nil {
		return addInheritedMemberRoles(ctx, q, p)
	}
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func insertClosure(ctx context.Context, q db.Queryer, id int64, parentID *int64) error {
	if _, err := q.Exec(ctx, `INSERT INTO project_closure (ancestor_id, descendant_id, depth) VALUES (?, ?, 0)`, id, id); err != nil {
		return err
	}
	if parentID == nil {
		return nil
	}
	_, err := q.Exec(ctx, `INSERT INTO project_closure (ancestor_id, descendant_id, depth)
SELECT ancestor_id, ?, depth + 1 FROM project_closure WHERE descendant_id = ?`, id, *parentID)
	return err
}

// UpdateProject はプロジェクトの属性を保存する (識別子以外)。
// 親の変更時は閉包テーブルを付け替え、継承メンバーを再計算する
// (after_save remove_inherited_member_roles / add_inherited_member_roles)。
// inherit_members の変更時は update_inherited_members を実行する。
//
// TODO: after_update update_versions_from_hierarchy_change
// (Issue.update_versions_from_hierarchy_change) はチケット移植時に追加する。
func UpdateProject(ctx context.Context, q db.Queryer, p *domain.Project) error {
	old, err := GetProject(ctx, q, p.ID)
	if err != nil {
		return err
	}
	parentChanged := !sameID(old.ParentID, p.ParentID)
	if parentChanged && p.ParentID != nil {
		parent, err := GetProject(ctx, q, *p.ParentID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return ErrInvalidParent
			}
			return err
		}
		// move_possible?: 自身または子孫へは移動できない
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM project_closure WHERE ancestor_id = ? AND descendant_id = ?`, p.ID, parent.ID); err != nil {
			return err
		}
		if n > 0 || !parent.Active() {
			return ErrInvalidParent
		}
	}
	p.UpdatedAt = db.Now().Time
	if _, err := q.Exec(ctx, `UPDATE projects SET parent_id = ?, name = ?, description = ?, homepage = ?, is_public = ?, status = ?,
  inherit_members = ?, default_version_id = ?, default_assigned_to_id = ?, default_issue_query_id = ?, updated_at = ? WHERE id = ?`,
		p.ParentID, p.Name, nullString(p.Description), nullString(p.Homepage), p.IsPublic, p.Status, p.InheritMembers,
		p.DefaultVersionID, p.DefaultAssignedToID, p.DefaultIssueQueryID, db.NewTime(p.UpdatedAt), p.ID); err != nil {
		return err
	}
	if parentChanged {
		if err := moveClosure(ctx, q, p.ID, p.ParentID); err != nil {
			return err
		}
	}
	// update_inherited_members
	if old.InheritMembers != p.InheritMembers && p.ParentID != nil {
		if p.InheritMembers {
			if err := removeInheritedMemberRoles(ctx, q, p.ID); err != nil {
				return err
			}
			if err := addInheritedMemberRoles(ctx, q, p); err != nil {
				return err
			}
		} else {
			if err := removeInheritedMemberRoles(ctx, q, p.ID); err != nil {
				return err
			}
		}
	}
	if parentChanged {
		if err := removeInheritedMemberRoles(ctx, q, p.ID); err != nil {
			return err
		}
		if err := addInheritedMemberRoles(ctx, q, p); err != nil {
			return err
		}
	}
	return nil
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// moveClosure は id のサブツリーを newParent の下へ付け替える。
func moveClosure(ctx context.Context, q db.Queryer, id int64, newParent *int64) error {
	// サブツリー外の祖先との行を削除
	if _, err := q.Exec(ctx, `DELETE FROM project_closure
WHERE descendant_id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)
  AND ancestor_id NOT IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)`, id, id); err != nil {
		return err
	}
	if newParent == nil {
		return nil
	}
	_, err := q.Exec(ctx, `INSERT INTO project_closure (ancestor_id, descendant_id, depth)
SELECT a.ancestor_id, d.descendant_id, a.depth + d.depth + 1
FROM project_closure a, project_closure d
WHERE a.descendant_id = ? AND d.ancestor_id = ?`, *newParent, id)
	return err
}

// removeInheritedMemberRoles は Project#remove_inherited_member_roles の移植:
// このプロジェクトのメンバーロールのうち、同プロジェクト外 (親プロジェクト) から継承した行を削除する。
func removeInheritedMemberRoles(ctx context.Context, q db.Queryer, projectID int64) error {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT mr.id FROM member_roles mr JOIN members m ON m.id = mr.member_id
WHERE m.project_id = ? AND mr.inherited_from IS NOT NULL
  AND mr.inherited_from NOT IN (SELECT mr2.id FROM member_roles mr2 JOIN members m2 ON m2.id = mr2.member_id WHERE m2.project_id = ?)
ORDER BY mr.id`, projectID, projectID); err != nil {
		return err
	}
	for _, id := range ids {
		if err := DestroyMemberRole(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

// addInheritedMemberRoles は Project#add_inherited_member_roles の移植:
// inherit_members で親がある場合、親の全メンバーのロールを継承ロールとして追加する。
func addInheritedMemberRoles(ctx context.Context, q db.Queryer, p *domain.Project) error {
	if !p.InheritMembers || p.ParentID == nil {
		return nil
	}
	parentMembers, err := ProjectMemberships(ctx, q, *p.ParentID)
	if err != nil {
		return err
	}
	for _, pm := range parentMembers {
		var add []newMemberRole
		for _, mr := range pm.MemberRoles {
			from := mr.ID
			add = append(add, newMemberRole{roleID: mr.RoleID, inheritedFrom: &from})
		}
		if len(add) == 0 {
			continue
		}
		if _, err := findOrCreateMemberWithRoles(ctx, q, p.ID, pm.PrincipalID, add); err != nil {
			return err
		}
	}
	return nil
}

func enabledNames(names []string) []string {
	out := []string{}
	for _, n := range names {
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// SetEnabledModules は Project#enabled_module_names= の移植。既存のモジュール行 (id) は維持し、
// 不要な行を削除、新しいモジュールを追加する。未知のモジュール名はエラー。
func SetEnabledModules(ctx context.Context, q db.Queryer, projectID int64, names []string) error {
	names = enabledNames(names)
	known := permission.AvailableProjectModules()
	for _, n := range names {
		if !slices.Contains(known, n) {
			return fmt.Errorf("repository: unknown project module %q", n)
		}
	}
	var cur []string
	if err := q.Select(ctx, &cur, `SELECT name FROM project_modules WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	for _, c := range cur {
		if !slices.Contains(names, c) {
			if err := DisableModule(ctx, q, projectID, c); err != nil {
				return err
			}
		}
	}
	for _, n := range names {
		if !slices.Contains(cur, n) {
			if err := EnableModule(ctx, q, projectID, n); err != nil {
				return err
			}
		}
	}
	return nil
}

// EnableModule は Project#enable_module! (有効なら何もしない)。
func EnableModule(ctx context.Context, q db.Queryer, projectID int64, name string) error {
	_, err := q.Exec(ctx, q.Dialect().Upsert("project_modules", []string{"project_id", "name"}, []string{"project_id", "name"}, nil), projectID, name)
	return err
}

// DisableModule は Project#disable_module!。ニュースモジュールのウォッチャ (watchable_kind='project_module') も削除する。
func DisableModule(ctx context.Context, q db.Queryer, projectID int64, name string) error {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT id FROM project_modules WHERE project_id = ? AND name = ?`, projectID, name); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := q.Exec(ctx, `DELETE FROM watchers WHERE watchable_kind = 'project_module' AND watchable_id = ?`, id); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM project_modules WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// ArchiveProject は Project#archive の移植。子孫のバージョンが子孫以外のプロジェクトの
// チケットで使用されていれば false を返して何もしない。成功時は自身と子孫を archived にする。
func ArchiveProject(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM issues
WHERE issues.fixed_version_id IN (SELECT v.id FROM versions v WHERE v.project_id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?))
  AND issues.project_id NOT IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)`, id, id); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	_, err := q.Exec(ctx, `UPDATE projects SET status = ?, updated_at = ? WHERE id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)`,
		domain.ProjectStatusArchived, db.Now(), id)
	return err == nil, err
}

// UnarchiveProject は Project#unarchive の移植: 祖先にクローズ済みがあれば closed、
// そうでなければ active にする (自身とアーカイブ済みの祖先)。
func UnarchiveProject(ctx context.Context, q db.Queryer, id int64) error {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM projects WHERE status = ? AND id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id = ? AND depth > 0)`,
		domain.ProjectStatusClosed, id); err != nil {
		return err
	}
	st := domain.ProjectStatusActive
	if n > 0 {
		st = domain.ProjectStatusClosed
	}
	// update_all なので updated_at は変えない
	_, err := q.Exec(ctx, `UPDATE projects SET status = ? WHERE status = ? AND id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id = ?)`,
		st, domain.ProjectStatusArchived, id)
	return err
}

// CloseProject は Project#close (自身と子孫の active → closed)。
func CloseProject(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `UPDATE projects SET status = ? WHERE status = ? AND id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)`,
		domain.ProjectStatusClosed, domain.ProjectStatusActive, id)
	return err
}

// ReopenProject は Project#reopen (自身と子孫の closed → active)。
func ReopenProject(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `UPDATE projects SET status = ? WHERE status = ? AND id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)`,
		domain.ProjectStatusActive, domain.ProjectStatusClosed, id)
	return err
}
