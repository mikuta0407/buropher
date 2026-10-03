// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはプロジェクト設定画面のタブ（バージョン・カテゴリ・リポジトリ・フォーラム・作業分類）で使う
// 関連の読み書き。
//
// TODO(dedupe): チケットのドメインサービスの移植で domain.Version / domain.IssueCategory が入ったら、
// ここの *Info 型をそちらに置き換える。

// VersionInfo は versions 行（設定画面の一覧用。プロジェクト名付き）。
type VersionInfo struct {
	ID                int64
	ProjectID         int64
	ProjectName       string
	ProjectIdentifier string
	Name              string
	Description       string
	EffectiveDate     *time.Time
	WikiPageTitle     string
	Status            string
	Sharing           string
	// ProjectHasWiki は version.project.wiki が存在するか。
	ProjectHasWiki bool
}

// ProjectSharedVersions は project.shared_versions.status(status).like(name).sorted。
// status が空なら全ステータス、name が空なら名前で絞らない。
func ProjectSharedVersions(ctx context.Context, q db.Queryer, p *domain.Project, status, name string) ([]*VersionInfo, error) {
	where := SharedVersionsCondition(p)
	var args []any
	if status != "" {
		where += " AND versions.status = ?"
		args = append(args, status)
	}
	if strings.TrimSpace(name) != "" {
		where += ` AND LOWER(versions.name) LIKE LOWER(?) ESCAPE '\'`
		args = append(args, "%"+likeEscapeSQL(strings.TrimSpace(name))+"%")
	}
	return loadVersions(ctx, q, where, "versions.effective_date IS NULL, versions.effective_date, versions.name, versions.id", args...)
}

// ProjectOpenSharedVersions は project.shared_versions.open（id 順）。
func ProjectOpenSharedVersions(ctx context.Context, q db.Queryer, p *domain.Project) ([]*VersionInfo, error) {
	return loadVersions(ctx, q, SharedVersionsCondition(p)+" AND versions.status = 'open'", "versions.id")
}

// GetVersionInfo は id のバージョン。
func GetVersionInfo(ctx context.Context, q db.Queryer, id int64) (*VersionInfo, error) {
	vs, err := loadVersions(ctx, q, "versions.id = ?", "versions.id", id)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, ErrNotFound
	}
	return vs[0], nil
}

func likeEscapeSQL(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func loadVersions(ctx context.Context, q db.Queryer, where, order string, args ...any) ([]*VersionInfo, error) {
	var rows []struct {
		ID            int64          `db:"id"`
		ProjectID     int64          `db:"project_id"`
		ProjectName   string         `db:"project_name"`
		Identifier    string         `db:"identifier"`
		Name          string         `db:"name"`
		Description   sql.NullString `db:"description"`
		EffectiveDate db.NullDate    `db:"effective_date"`
		WikiPageTitle sql.NullString `db:"wiki_page_title"`
		Status        string         `db:"status"`
		Sharing       string         `db:"sharing"`
		HasWiki       bool           `db:"has_wiki"`
	}
	if err := q.Select(ctx, &rows, `SELECT versions.id, versions.project_id, projects.name AS project_name, projects.identifier,
  versions.name, versions.description, versions.effective_date, versions.wiki_page_title, versions.status, versions.sharing,
  EXISTS (SELECT 1 FROM wikis WHERE wikis.project_id = versions.project_id) AS has_wiki
FROM versions JOIN projects ON projects.id = versions.project_id
WHERE `+where+` ORDER BY `+order, args...); err != nil {
		return nil, err
	}
	out := make([]*VersionInfo, len(rows))
	for i, r := range rows {
		v := &VersionInfo{ID: r.ID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, ProjectIdentifier: r.Identifier,
			Name: r.Name, Description: r.Description.String, WikiPageTitle: r.WikiPageTitle.String, Status: r.Status,
			Sharing: r.Sharing, ProjectHasWiki: r.HasWiki}
		if r.EffectiveDate.Valid {
			t := r.EffectiveDate.Date.Time
			v.EffectiveDate = &t
		}
		out[i] = v
	}
	return out, nil
}

// IssueCategoryInfo は issue_categories 行。
type IssueCategoryInfo struct {
	ID           int64
	ProjectID    int64
	Name         string
	AssignedToID *int64
}

// ProjectIssueCategories は project.issue_categories（名前順）。
func ProjectIssueCategories(ctx context.Context, q db.Queryer, projectID int64) ([]*IssueCategoryInfo, error) {
	return loadIssueCategories(ctx, q, "project_id = ?", projectID)
}

// GetIssueCategory は id のカテゴリ。
func GetIssueCategory(ctx context.Context, q db.Queryer, id int64) (*IssueCategoryInfo, error) {
	cs, err := loadIssueCategories(ctx, q, "id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, ErrNotFound
	}
	return cs[0], nil
}

func loadIssueCategories(ctx context.Context, q db.Queryer, where string, args ...any) ([]*IssueCategoryInfo, error) {
	var rows []struct {
		ID           int64         `db:"id"`
		ProjectID    int64         `db:"project_id"`
		Name         string        `db:"name"`
		AssignedToID sql.NullInt64 `db:"assigned_to_id"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, project_id, name, assigned_to_id FROM issue_categories WHERE `+where+` ORDER BY name, id`, args...); err != nil {
		return nil, err
	}
	out := make([]*IssueCategoryInfo, len(rows))
	for i, r := range rows {
		out[i] = &IssueCategoryInfo{ID: r.ID, ProjectID: r.ProjectID, Name: r.Name, AssignedToID: nullID(r.AssignedToID)}
	}
	return out, nil
}

// IssueCategoryNameTaken は validates_uniqueness_of :name, :scope => [:project_id]（大文字小文字を区別）。
func IssueCategoryNameTaken(ctx context.Context, q db.Queryer, projectID int64, name string, excludeID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM issue_categories WHERE project_id = ? AND name = ? AND id <> ?`, projectID, name, excludeID)
}

// SaveIssueCategory はカテゴリを保存する（ID == 0 なら作成）。
func SaveIssueCategory(ctx context.Context, q db.Queryer, c *IssueCategoryInfo) error {
	if c.ID == 0 {
		id, err := q.InsertReturningID(ctx, `INSERT INTO issue_categories (project_id, name, assigned_to_id) VALUES (?, ?, ?)`,
			c.ProjectID, c.Name, c.AssignedToID)
		if err != nil {
			return err
		}
		c.ID = id
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE issue_categories SET name = ?, assigned_to_id = ? WHERE id = ?`, c.Name, c.AssignedToID, c.ID)
	return err
}

// IssueCategoryIssueCount は category.issues.size。
func IssueCategoryIssueCount(ctx context.Context, q db.Queryer, id int64) (int, error) {
	return CountWhere(ctx, q, "issues", "category_id = ?", id)
}

// DestroyIssueCategory は IssueCategory#destroy(reassign_to)（チケットのカテゴリを付け替えてから削除）。
// TODO: Redmine は付け替えを Issue.where(...).update_all で行い、ジャーナルを残さない（同じ挙動）。
func DestroyIssueCategory(ctx context.Context, q db.Queryer, id int64, reassignTo *int64) error {
	if _, err := q.Exec(ctx, `UPDATE issues SET category_id = ? WHERE category_id = ?`, reassignTo, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM issue_categories WHERE id = ?`, id)
	return err
}

// RepositoryInfo は repositories 行（設定画面の一覧用）。
type RepositoryInfo struct {
	ID         int64
	Identifier string
	IsDefault  bool
	SCM        string
	URL        string
}

// ProjectRepositoriesForSettings は project.repositories.sort（既定を先頭、次に識別子順。設定画面用）。
func ProjectRepositoriesForSettings(ctx context.Context, q db.Queryer, projectID int64) ([]*RepositoryInfo, error) {
	var rows []struct {
		ID         int64          `db:"id"`
		Identifier sql.NullString `db:"identifier"`
		IsDefault  bool           `db:"is_default"`
		SCM        string         `db:"scm"`
		URL        string         `db:"url"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, identifier, is_default, scm, url FROM repositories WHERE project_id = ?
ORDER BY is_default DESC, identifier, id`, projectID); err != nil {
		return nil, err
	}
	out := make([]*RepositoryInfo, len(rows))
	for i, r := range rows {
		out[i] = &RepositoryInfo{ID: r.ID, Identifier: r.Identifier.String, IsDefault: r.IsDefault, SCM: r.SCM, URL: r.URL}
	}
	return out, nil
}

// BoardInfo は boards 行（設定画面の一覧用）。
type BoardInfo struct {
	ID          int64
	ProjectID   int64
	ParentID    *int64
	Name        string
	Description string
	Position    int
}

// ProjectBoards は project.boards（position 順）。
func ProjectBoards(ctx context.Context, q db.Queryer, projectID int64) ([]*BoardInfo, error) {
	var rows []struct {
		ID          int64         `db:"id"`
		ProjectID   int64         `db:"project_id"`
		ParentID    sql.NullInt64 `db:"parent_id"`
		Name        string        `db:"name"`
		Description string        `db:"description"`
		Position    int           `db:"position"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, project_id, parent_id, name, description, position FROM boards WHERE project_id = ? ORDER BY position, id`, projectID); err != nil {
		return nil, err
	}
	out := make([]*BoardInfo, len(rows))
	for i, r := range rows {
		out[i] = &BoardInfo{ID: r.ID, ProjectID: r.ProjectID, ParentID: nullID(r.ParentID), Name: r.Name, Description: r.Description, Position: r.Position}
	}
	return out, nil
}

// ---------------------------------------------------------------- 作業分類（プロジェクト別）

// ProjectActivities は project.activities(include_inactive)（システムの作業分類のうち上書きされていないもの +
// プロジェクト別の作業分類、position 順）。
func ProjectActivities(ctx context.Context, q db.Queryer, projectID int64, includeInactive bool) ([]*domain.Enumeration, error) {
	where := `(project_id IS NULL OR project_id = ?)
  AND id NOT IN (SELECT parent_id FROM time_entry_activities WHERE project_id = ? AND parent_id IS NOT NULL)`
	if !includeInactive {
		where += ` AND active = ` + q.Dialect().BoolLiteral(true)
	}
	var rows []enumerationRow
	if err := q.Select(ctx, &rows, enumerationSelect("TimeEntryActivity")+` WHERE `+where+` ORDER BY position, id`, projectID, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.Enumeration, len(rows))
	for i := range rows {
		out[i] = rows[i].enumeration("TimeEntryActivity")
	}
	return out, nil
}

// ProjectTimeEntryActivities は project.time_entry_activities（プロジェクト別の上書き行）。
func ProjectTimeEntryActivities(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.Enumeration, error) {
	var rows []enumerationRow
	if err := q.Select(ctx, &rows, enumerationSelect("TimeEntryActivity")+` WHERE project_id = ? ORDER BY position, id`, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.Enumeration, len(rows))
	for i := range rows {
		out[i] = rows[i].enumeration("TimeEntryActivity")
	}
	return out, nil
}

// NamedID は (name, id) の組（pluck(:name, :id)）。
type NamedID struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

// PublicIssueQueries は IssueQuery.only_public の全体クエリとプロジェクトのクエリ（id 順）。
func PublicIssueQueries(ctx context.Context, q db.Queryer, projectID int64) (global, current []NamedID, err error) {
	if err = q.Select(ctx, &global, `SELECT id, name FROM queries WHERE kind = 'issue' AND visibility = 2 AND project_id IS NULL ORDER BY id`); err != nil {
		return
	}
	err = q.Select(ctx, &current, `SELECT id, name FROM queries WHERE kind = 'issue' AND visibility = 2 AND project_id = ? ORDER BY id`, projectID)
	return
}

// ProjectAssignableUserIDs は project.assignable_users の id（有効なユーザー（設定でグループも）で、
// assignable なロールのメンバー）。
func ProjectAssignableUserIDs(ctx context.Context, q db.Queryer, projectID int64, groups bool) ([]int64, error) {
	kinds := "'user'"
	if groups {
		kinds = "'user', 'group'"
	}
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT DISTINCT p.id FROM principals p
JOIN members m ON m.principal_id = p.id JOIN member_roles mr ON mr.member_id = m.id JOIN roles r ON r.id = mr.role_id
WHERE p.status = 1 AND p.kind IN (`+kinds+`) AND m.project_id = ? AND r.assignable = `+q.Dialect().BoolLiteral(true)+`
ORDER BY p.id`, projectID)
	return ids, err
}

// ReassignTimeEntryActivity は time_entries.where(activity_id: from).update_all(activity_id: to)（プロジェクト内）。
func ReassignTimeEntryActivity(ctx context.Context, q db.Queryer, projectID, from, to int64) error {
	_, err := q.Exec(ctx, `UPDATE time_entries SET activity_id = ? WHERE project_id = ? AND activity_id = ?`, to, projectID, from)
	return err
}
