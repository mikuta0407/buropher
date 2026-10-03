package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは Project#copy の各 copy_*（members / versions / issue_categories / queries / boards /
// wiki / documents）の移植。
//
// copy_issues は internal/issues（Env.CopyProjectIssues）にある。
//
// TODO: 添付ファイルの複製（プロジェクト・バージョン・Wiki ページ・文書の attachments.copy）。

// CopyCounts は projects/copy の各項目の件数（@source_project.members.count など）。
type CopyCounts struct {
	Members, Versions, IssueCategories, Issues, Queries, Documents, Boards, WikiPages int
}

// ProjectCopyCounts は複製元の件数。
func ProjectCopyCounts(ctx context.Context, q db.Queryer, projectID int64) (CopyCounts, error) {
	var c CopyCounts
	var err error
	if c.Members, err = ProjectMemberCount(ctx, q, projectID); err != nil {
		return c, err
	}
	for _, x := range []struct {
		dst   *int
		table string
		where string
	}{
		{&c.Versions, "versions", "project_id = ?"},
		{&c.IssueCategories, "issue_categories", "project_id = ?"},
		{&c.Issues, "issues", "project_id = ?"},
		{&c.Queries, "queries", "project_id = ?"},
		{&c.Documents, "documents", "project_id = ?"},
		{&c.Boards, "boards", "project_id = ?"},
		{&c.WikiPages, "wiki_pages", "wiki_id IN (SELECT id FROM wikis WHERE project_id = ?)"},
	} {
		if *x.dst, err = CountWhere(ctx, q, x.table, x.where, projectID); err != nil {
			return c, err
		}
	}
	return c, nil
}

// CopyProjectMembers は copy_members（ユーザー → グループの順、継承でないロールのみ）。
func CopyProjectMembers(ctx context.Context, q db.Queryer, srcID, dstID int64) error {
	members, err := ProjectMembersWithPrincipals(ctx, q, srcID, false)
	if err != nil {
		return err
	}
	var ordered []*MemberPrincipal
	for _, m := range members {
		if m.User != nil {
			ordered = append(ordered, m)
		}
	}
	for _, m := range members {
		if m.User == nil {
			ordered = append(ordered, m)
		}
	}
	for _, m := range ordered {
		var roles []int64
		for _, mr := range m.Member.MemberRoles {
			if !mr.Inherited() {
				roles = append(roles, mr.RoleID)
			}
		}
		if len(roles) == 0 {
			continue
		}
		if _, err := CreateMember(ctx, q, dstID, m.Member.PrincipalID, roles); err != nil &&
			!errors.Is(err, ErrMemberTaken) && !errors.Is(err, ErrInvalidMemberRole) && !errors.Is(err, ErrMemberRoleEmpty) {
			return err
		}
	}
	return nil
}

// CopyProjectVersions は copy_versions（バージョン名でチケットを付け替える場合の対応表を返す）。
func CopyProjectVersions(ctx context.Context, q db.Queryer, srcID, dstID int64) (map[int64]int64, error) {
	var rows []struct {
		ID            int64          `db:"id"`
		Name          string         `db:"name"`
		Description   sql.NullString `db:"description"`
		EffectiveDate db.NullDate    `db:"effective_date"`
		WikiPageTitle sql.NullString `db:"wiki_page_title"`
		Status        string         `db:"status"`
		Sharing       string         `db:"sharing"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, name, description, effective_date, wiki_page_title, status, sharing
FROM versions WHERE project_id = ? ORDER BY id`, srcID); err != nil {
		return nil, err
	}
	out := map[int64]int64{}
	now := db.Now()
	for _, r := range rows {
		id, err := q.InsertReturningID(ctx, `INSERT INTO versions (project_id, name, description, effective_date, wiki_page_title, status, sharing, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, dstID, r.Name, r.Description, r.EffectiveDate, r.WikiPageTitle, r.Status, r.Sharing, now, now)
		if err != nil {
			return nil, err
		}
		out[r.ID] = id
	}
	return out, nil
}

// CopyProjectIssueCategories は copy_issue_categories。
func CopyProjectIssueCategories(ctx context.Context, q db.Queryer, srcID, dstID int64) error {
	_, err := q.Exec(ctx, `INSERT INTO issue_categories (project_id, name, assigned_to_id)
SELECT ?, name, assigned_to_id FROM issue_categories WHERE project_id = ? ORDER BY name, id`, dstID, srcID)
	return err
}

// CopyProjectQueries は copy_queries（既定のチケットクエリも付け替える）。
func CopyProjectQueries(ctx context.Context, q db.Queryer, src *domain.Project, dstID int64) error {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT id FROM queries WHERE project_id = ? ORDER BY id`, src.ID); err != nil {
		return err
	}
	for _, id := range ids {
		newID, err := q.InsertReturningID(ctx, `INSERT INTO queries (kind, project_id, user_id, name, description, visibility, filters,
  column_names, sort_criteria, group_by, totalable_names, display_type, options)
SELECT kind, ?, user_id, name, description, visibility, filters, column_names, sort_criteria, group_by, totalable_names, display_type, options
FROM queries WHERE id = ?`, dstID, id)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO queries_roles (query_id, role_id)
SELECT ?, role_id FROM queries_roles WHERE query_id = ? AND EXISTS (SELECT 1 FROM queries WHERE id = ? AND visibility = 1)`, newID, id, id); err != nil {
			return err
		}
		if src.DefaultIssueQueryID != nil && *src.DefaultIssueQueryID == id {
			if _, err := q.Exec(ctx, `UPDATE projects SET default_issue_query_id = ? WHERE id = ?`, newID, dstID); err != nil {
				return err
			}
		}
	}
	return nil
}

// CopyProjectBoards は copy_boards（parent_id は Redmine と同じく複製元の値のまま）。
func CopyProjectBoards(ctx context.Context, q db.Queryer, srcID, dstID int64) error {
	_, err := q.Exec(ctx, `INSERT INTO boards (project_id, parent_id, name, description, position)
SELECT ?, parent_id, name, description, position FROM boards WHERE project_id = ? ORDER BY position, id`, dstID, srcID)
	return err
}

// CopyProjectDocuments は copy_documents。
func CopyProjectDocuments(ctx context.Context, q db.Queryer, srcID, dstID int64) error {
	_, err := q.Exec(ctx, `INSERT INTO documents (project_id, category_id, title, description, created_at)
SELECT ?, category_id, title, description, created_at FROM documents WHERE project_id = ? ORDER BY id`, dstID, srcID)
	return err
}

// CopyProjectWiki は copy_wiki（各ページの現在の内容だけを複製し、親子関係を再現する）。
func CopyProjectWiki(ctx context.Context, q db.Queryer, srcID, dstID int64) error {
	var wiki struct {
		ID        int64  `db:"id"`
		StartPage string `db:"start_page"`
	}
	err := q.Get(ctx, &wiki, `SELECT id, start_page FROM wikis WHERE project_id = ?`, srcID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	var dstWiki int64
	err = q.Get(ctx, &dstWiki, `SELECT id FROM wikis WHERE project_id = ?`, dstID)
	if errors.Is(err, sql.ErrNoRows) {
		dstWiki, err = q.InsertReturningID(ctx, `INSERT INTO wikis (project_id, start_page) VALUES (?, ?)`, dstID, wiki.StartPage)
	} else if err == nil {
		_, err = q.Exec(ctx, `UPDATE wikis SET start_page = ? WHERE id = ?`, wiki.StartPage, dstWiki)
	}
	if err != nil {
		return err
	}
	var pages []struct {
		ID             int64         `db:"id"`
		Title          string        `db:"title"`
		ParentID       sql.NullInt64 `db:"parent_id"`
		Protected      bool          `db:"protected"`
		CurrentVersion int           `db:"current_version"`
	}
	if err := q.Select(ctx, &pages, `SELECT id, title, parent_id, protected, current_version FROM wiki_pages WHERE wiki_id = ? ORDER BY id`, wiki.ID); err != nil {
		return err
	}
	now := db.Now()
	mapping := map[int64]int64{}
	for _, p := range pages {
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM wiki_page_versions WHERE page_id = ? AND version = ?`, p.ID, p.CurrentVersion); err != nil {
			return err
		}
		if n == 0 {
			// Skip pages without content
			continue
		}
		newID, err := q.InsertReturningID(ctx, `INSERT INTO wiki_pages (wiki_id, title, protected, current_version, created_at) VALUES (?, ?, ?, ?, ?)`,
			dstWiki, p.Title, p.Protected, p.CurrentVersion, now)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO wiki_page_versions (page_id, version, author_id, text, comments, updated_at)
SELECT ?, version, author_id, text, comments, ? FROM wiki_page_versions WHERE page_id = ? AND version = ?`, newID, now, p.ID, p.CurrentVersion); err != nil {
			return err
		}
		mapping[p.ID] = newID
	}
	for _, p := range pages {
		if p.ParentID.Valid {
			child, ok1 := mapping[p.ID]
			parent, ok2 := mapping[p.ParentID.Int64]
			if ok1 && ok2 {
				if _, err := q.Exec(ctx, `UPDATE wiki_pages SET parent_id = ? WHERE id = ?`, parent, child); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
