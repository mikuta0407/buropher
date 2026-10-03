// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

// textilizable（Redmine リンク・Wiki リンク・マクロの解決）が参照するデータの読み込み。
// 可視性の条件（Issue.visible / Project.allowed_to_condition 等）は authz が作る SQL 断片を
// cond 引数で受け取る（repository は authz に依存できないため）。
// 型名の Ref は「テキスト中の参照を解決するための最小限の列」を表す。

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// RefIssue はチケットリンク（link_to_issue / Issue#css_classes）に必要な列。
type RefIssue struct {
	ID              int64          `db:"id"`
	ProjectID       int64          `db:"project_id"`
	ProjectName     string         `db:"project_name"`
	TrackerID       int64          `db:"tracker_id"`
	TrackerName     string         `db:"tracker_name"`
	StatusID        int64          `db:"status_id"`
	StatusName      string         `db:"status_name"`
	StatusClosed    bool           `db:"status_closed"`
	PriorityID      int64          `db:"priority_id"`
	PriorityPosName sql.NullString `db:"priority_position_name"`
	Subject         string         `db:"subject"`
	AuthorID        int64          `db:"author_id"`
	AssignedToID    sql.NullInt64  `db:"assigned_to_id"`
	ParentID        sql.NullInt64  `db:"parent_id"`
	HasChildren     bool           `db:"has_children"`
	IsPrivate       bool           `db:"is_private"`
	StartDate       db.NullDate    `db:"start_date"`
	DueDate         db.NullDate    `db:"due_date"`
	DoneRatio       int            `db:"done_ratio"`
}

// FindVisibleIssue は Issue.visible.find_by_id(id)。cond は IssueVisibleCondition の結果。
func FindVisibleIssue(ctx context.Context, q db.Queryer, id int64, cond string) (*RefIssue, error) {
	var r RefIssue
	err := q.Get(ctx, &r, `SELECT issues.id, issues.project_id, projects.name AS project_name,
  issues.tracker_id, trackers.name AS tracker_name, issues.status_id, issue_statuses.name AS status_name,
  issue_statuses.is_closed AS status_closed, issues.priority_id, issue_priorities.position_name AS priority_position_name,
  issues.subject, issues.author_id, issues.assigned_to_id, issues.parent_id,
  EXISTS (SELECT 1 FROM issues c WHERE c.parent_id = issues.id) AS has_children,
  issues.is_private, issues.start_date, issues.due_date, issues.done_ratio
FROM issues
JOIN projects ON projects.id = issues.project_id
JOIN trackers ON trackers.id = issues.tracker_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
JOIN issue_priorities ON issue_priorities.id = issues.priority_id
WHERE issues.id = ? AND (`+condOr(cond)+`)`, id)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

func condOr(cond string) string {
	if cond == "" {
		return "1=1"
	}
	return cond
}

// FindProjectsWhere は条件に合うプロジェクトを id 順で返す（find_by 系の「最初の 1 件」用）。
func FindProjectWhere(ctx context.Context, q db.Queryer, where string, args ...any) (*domain.Project, error) {
	var id int64
	if err := q.Get(ctx, &id, `SELECT projects.id FROM projects WHERE `+where+` ORDER BY projects.id LIMIT 1`, args...); err != nil {
		return nil, notFound(err)
	}
	return GetProject(ctx, q, id)
}

// RefWiki は wikis の行。
type RefWiki struct {
	ID        int64  `db:"id"`
	ProjectID int64  `db:"project_id"`
	StartPage string `db:"start_page"`
}

// FindWikiByProject は Project#wiki。
func FindWikiByProject(ctx context.Context, q db.Queryer, projectID int64) (*RefWiki, error) {
	var w RefWiki
	if err := q.Get(ctx, &w, `SELECT id, project_id, start_page FROM wikis WHERE project_id = ?`, projectID); err != nil {
		return nil, notFound(err)
	}
	return &w, nil
}

// RefWikiPage は wiki_pages の行（現在の版の更新日時・本文の有無を含む）。
type RefWikiPage struct {
	ID         int64         `db:"id"`
	WikiID     int64         `db:"wiki_id"`
	ProjectID  int64         `db:"project_id"`
	Title      string        `db:"title"`
	ParentID   sql.NullInt64 `db:"parent_id"`
	HasContent bool          `db:"has_content"`
	UpdatedOn  db.NullTime   `db:"updated_on"`
}

const refWikiPageSelect = `SELECT wiki_pages.id, wiki_pages.wiki_id, wikis.project_id, wiki_pages.title, wiki_pages.parent_id,
  (v.id IS NOT NULL) AS has_content, v.updated_at AS updated_on
FROM wiki_pages
JOIN wikis ON wikis.id = wiki_pages.wiki_id
LEFT JOIN wiki_page_versions v ON v.page_id = wiki_pages.id AND v.version = wiki_pages.current_version`

// FindWikiPageByTitle は wiki.pages.find_by("LOWER(title) = LOWER(?)", title)。
func FindWikiPageByTitle(ctx context.Context, q db.Queryer, wikiID int64, title string) (*RefWikiPage, error) {
	var p RefWikiPage
	if err := q.Get(ctx, &p, refWikiPageSelect+` WHERE wiki_pages.wiki_id = ? AND LOWER(wiki_pages.title) = LOWER(?) ORDER BY wiki_pages.id LIMIT 1`, wikiID, title); err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// GetWikiPage は id の Wiki ページ。
func GetWikiPage(ctx context.Context, q db.Queryer, id int64) (*RefWikiPage, error) {
	var p RefWikiPage
	if err := q.Get(ctx, &p, refWikiPageSelect+` WHERE wiki_pages.id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// FindWikiRedirect は wiki.redirects.where("LOWER(title) = LOWER(?)", title).first の転送先ページ
// （WikiRedirect#target_page = Wiki.find(redirects_to_wiki_id).find_page(redirects_to, with_redirect: false)）。
func FindWikiRedirect(ctx context.Context, q db.Queryer, wikiID int64, title string) (*RefWikiPage, error) {
	var r struct {
		RedirectsTo   string `db:"redirects_to"`
		RedirectsWiki int64  `db:"redirects_to_wiki_id"`
	}
	if err := q.Get(ctx, &r, `SELECT redirects_to, redirects_to_wiki_id FROM wiki_redirects
WHERE wiki_id = ? AND LOWER(title) = LOWER(?) ORDER BY id LIMIT 1`, wikiID, title); err != nil {
		return nil, notFound(err)
	}
	return FindWikiPageByTitle(ctx, q, r.RedirectsWiki, r.RedirectsTo)
}

// WikiPageChildren は page.children（title 順）。
func WikiPageChildren(ctx context.Context, q db.Queryer, pageID int64) ([]*RefWikiPage, error) {
	var rows []*RefWikiPage
	if err := q.Select(ctx, &rows, refWikiPageSelect+` WHERE wiki_pages.parent_id = ? ORDER BY wiki_pages.title`, pageID); err != nil {
		return nil, err
	}
	return rows, nil
}

// WikiPageText は page.content.text（本文が無ければ ok=false）。
func WikiPageText(ctx context.Context, q db.Queryer, pageID int64) (string, bool, error) {
	var s string
	err := q.Get(ctx, &s, `SELECT v.text FROM wiki_pages p JOIN wiki_page_versions v ON v.page_id = p.id AND v.version = p.current_version WHERE p.id = ?`, pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return s, true, nil
}

// RecentWikiPages は recent_pages マクロの検索（content.updated_on >= since を新しい順、limit <= 0 なら無制限）。
func RecentWikiPages(ctx context.Context, q db.Queryer, projectIDs []int64, since time.Time, limit int) ([]*RefWikiPage, error) {
	if len(projectIDs) == 0 {
		return nil, nil
	}
	query, args, err := db.In(refWikiPageSelect+` WHERE wikis.project_id IN (?) AND v.updated_at >= ?
ORDER BY v.updated_at DESC, wiki_pages.id DESC`, projectIDs, db.NewTime(since))
	if err != nil {
		return nil, err
	}
	// JOIN :content は INNER JOIN なので本文の無いページは除く（v.updated_at >= ? で除外される）
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	var rows []*RefWikiPage
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

// RefAttachment は添付ファイル（リンク・サムネイル用）。
type RefAttachment struct {
	ID          int64          `db:"id"`
	Filename    string         `db:"filename"`
	Description sql.NullString `db:"description"`
	ContentType sql.NullString `db:"content_type"`
	Filesize    int64          `db:"filesize"`
	CreatedAt   db.Time        `db:"created_at"`
}

// ContainerAttachments は container.attachments（container_kind, container_id）。並びは id 順。
func ContainerAttachments(ctx context.Context, q db.Queryer, kind string, id int64) ([]*RefAttachment, error) {
	var rows []*RefAttachment
	if err := q.Select(ctx, &rows, `SELECT id, filename, description, content_type, filesize, created_at FROM attachments
WHERE container_kind = ? AND container_id = ? ORDER BY id`, kind, id); err != nil {
		return nil, err
	}
	return rows, nil
}

// AttachmentsByIDs は id 指定の添付（:attachments オプション用）。
func AttachmentsByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*RefAttachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`SELECT id, filename, description, content_type, filesize, created_at FROM attachments WHERE id IN (?) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	var rows []*RefAttachment
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

// FindAttachmentByToken は Attachment.find_by_token（未紐付けの添付のみ）。
func FindAttachmentByToken(ctx context.Context, q db.Queryer, id int64, digest string) (*RefAttachment, error) {
	var r RefAttachment
	if err := q.Get(ctx, &r, `SELECT id, filename, description, content_type, filesize, created_at FROM attachments
WHERE id = ? AND digest = ? AND container_id IS NULL`, id, digest); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// RefRepository はリポジトリ。
type RefRepository struct {
	ID         int64          `db:"id"`
	ProjectID  int64          `db:"project_id"`
	SCM        string         `db:"scm"`
	Identifier sql.NullString `db:"identifier"`
	IsDefault  bool           `db:"is_default"`
}

// ProjectRepositories は project.repositories（id 順）。
func ProjectRepositories(ctx context.Context, q db.Queryer, projectID int64) ([]*RefRepository, error) {
	var rows []*RefRepository
	if err := q.Select(ctx, &rows, `SELECT id, project_id, scm, identifier, is_default FROM repositories WHERE project_id = ? ORDER BY id`, projectID); err != nil {
		return nil, err
	}
	return rows, nil
}

// RefChangeset はチェンジセット。
type RefChangeset struct {
	ID       int64          `db:"id"`
	Revision string         `db:"revision"`
	Scmid    sql.NullString `db:"scmid"`
	Comments sql.NullString `db:"comments"`
}

// FindVisibleChangeset は Changeset.visible.find_by_repository_id_and_revision。cond は view_changesets の条件。
func FindVisibleChangeset(ctx context.Context, q db.Queryer, repoID int64, revision, cond string) (*RefChangeset, error) {
	var r RefChangeset
	if err := q.Get(ctx, &r, `SELECT changesets.id, changesets.revision, changesets.scmid, changesets.comments FROM changesets
JOIN repositories ON repositories.id = changesets.repository_id JOIN projects ON projects.id = repositories.project_id
WHERE changesets.repository_id = ? AND changesets.revision = ? AND (`+condOr(cond)+`) ORDER BY changesets.id LIMIT 1`, repoID, revision); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// FindVisibleChangesetByScmidPrefix は Changeset.visible.where("repository_id = ? AND scmid LIKE ?", id, "prefix%").first。
func FindVisibleChangesetByScmidPrefix(ctx context.Context, q db.Queryer, repoID int64, prefix, cond string) (*RefChangeset, error) {
	var r RefChangeset
	if err := q.Get(ctx, &r, `SELECT changesets.id, changesets.revision, changesets.scmid, changesets.comments FROM changesets
JOIN repositories ON repositories.id = changesets.repository_id JOIN projects ON projects.id = repositories.project_id
WHERE changesets.repository_id = ? AND changesets.scmid LIKE ? AND (`+condOr(cond)+`) ORDER BY changesets.id LIMIT 1`, repoID, prefix+"%"); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// RefNamed は id・名前・プロジェクトだけを持つ参照（文書・バージョン・フォーラム・ニュース）。
type RefNamed struct {
	ID        int64  `db:"id"`
	ProjectID int64  `db:"project_id"`
	Name      string `db:"name"`
}

// 参照の種類ごとの SELECT（projects を JOIN 済み。name 列に表示名を出す）。
var refNamedSelect = map[string]string{
	"document": `SELECT documents.id, documents.project_id, documents.title AS name FROM documents JOIN projects ON projects.id = documents.project_id`,
	"version":  `SELECT versions.id, versions.project_id, versions.name AS name FROM versions JOIN projects ON projects.id = versions.project_id`,
	"board":    `SELECT boards.id, boards.project_id, boards.name AS name FROM boards JOIN projects ON projects.id = boards.project_id`,
	"news":     `SELECT news.id, news.project_id, news.title AS name FROM news JOIN projects ON projects.id = news.project_id`,
}

var refNamedTable = map[string]string{"document": "documents", "version": "versions", "board": "boards", "news": "news"}
var refNamedColumn = map[string]string{"document": "title", "version": "name", "board": "name", "news": "title"}

// FindVisibleNamed は Document/Version/Board/News.visible.find_by_id(id)。kind は document|version|board|news。
func FindVisibleNamed(ctx context.Context, q db.Queryer, kind string, id int64, cond string) (*RefNamed, error) {
	var r RefNamed
	if err := q.Get(ctx, &r, refNamedSelect[kind]+` WHERE `+refNamedTable[kind]+`.id = ? AND (`+condOr(cond)+`)`, id); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// FindVisibleNamedByName は project.documents.visible.find_by_title(name) 等（プロジェクト内で名前が完全一致する最初の 1 件）。
func FindVisibleNamedByName(ctx context.Context, q db.Queryer, kind string, projectID int64, name, cond string) (*RefNamed, error) {
	var r RefNamed
	t := refNamedTable[kind]
	if err := q.Get(ctx, &r, refNamedSelect[kind]+` WHERE `+t+`.project_id = ? AND `+t+`.`+refNamedColumn[kind]+` = ? AND (`+condOr(cond)+`) ORDER BY `+t+`.id LIMIT 1`, projectID, name); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// RefMessage はフォーラムのメッセージ。
type RefMessage struct {
	ID       int64         `db:"id"`
	BoardID  int64         `db:"board_id"`
	ParentID sql.NullInt64 `db:"parent_id"`
	Subject  string        `db:"subject"`
}

// FindVisibleMessage は Message.visible.find_by_id(id)。cond は view_messages の条件。
func FindVisibleMessage(ctx context.Context, q db.Queryer, id int64, cond string) (*RefMessage, error) {
	var r RefMessage
	if err := q.Get(ctx, &r, `SELECT messages.id, messages.board_id, messages.parent_id, messages.subject FROM messages
JOIN boards ON boards.id = messages.board_id JOIN projects ON projects.id = boards.project_id
WHERE messages.id = ? AND (`+condOr(cond)+`)`, id); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// FindVisibleUserWhere は User.visible.find_by(where)（principals.kind = 'user' に限る）。
// cond は PrincipalVisibleCondition の結果（principals を参照）。
func FindVisibleUserWhere(ctx context.Context, q db.Queryer, cond, where string, args ...any) (*domain.User, error) {
	var id int64
	if err := q.Get(ctx, &id, `SELECT principals.id FROM principals LEFT JOIN user_accounts ON user_accounts.principal_id = principals.id
WHERE principals.kind = 'user' AND (`+condOr(cond)+`) AND (`+where+`) ORDER BY principals.id LIMIT 1`, args...); err != nil {
		return nil, notFound(err)
	}
	return GetUser(ctx, q, id)
}

// LowerLoginCondition は "LOWER(login) = ?" の条件（user_accounts を参照）。
func LowerLoginCondition() string { return "LOWER(user_accounts.login) = ?" }

// RefObject は textilizable の :object に渡すオブジェクトの解決結果。
type RefObject struct {
	ProjectID     int64
	JournalizedID int64 // journal のときのチケット id
	PageID        int64 // wiki_content のときのページ id
	Title         string
}

// ResolveObject は種類と id から object のプロジェクト等を求める（テスト・プレビュー用）。
func ResolveObject(ctx context.Context, q db.Queryer, kind string, id int64) (*RefObject, error) {
	o := &RefObject{}
	var err error
	switch kind {
	case "issue":
		err = q.Get(ctx, &o.ProjectID, `SELECT project_id FROM issues WHERE id = ?`, id)
	case "journal":
		var r struct {
			IssueID   int64 `db:"issue_id"`
			ProjectID int64 `db:"project_id"`
		}
		err = q.Get(ctx, &r, `SELECT j.issue_id, i.project_id FROM issue_journals j JOIN issues i ON i.id = j.issue_id WHERE j.id = ?`, id)
		o.JournalizedID, o.ProjectID = r.IssueID, r.ProjectID
	case "wiki_content", "wiki_page":
		var p *RefWikiPage
		p, err = GetWikiPage(ctx, q, id)
		if err == nil {
			o.ProjectID, o.PageID, o.Title = p.ProjectID, p.ID, p.Title
		}
	case "news":
		err = q.Get(ctx, &o.ProjectID, `SELECT project_id FROM news WHERE id = ?`, id)
	case "document":
		err = q.Get(ctx, &o.ProjectID, `SELECT project_id FROM documents WHERE id = ?`, id)
	case "version":
		err = q.Get(ctx, &o.ProjectID, `SELECT project_id FROM versions WHERE id = ?`, id)
	case "message":
		err = q.Get(ctx, &o.ProjectID, `SELECT b.project_id FROM messages m JOIN boards b ON b.id = m.board_id WHERE m.id = ?`, id)
	case "project":
		o.ProjectID = id
	default:
		return nil, errors.New("repository: unknown object kind " + strings.TrimSpace(kind))
	}
	if err != nil {
		return nil, notFound(err)
	}
	return o, nil
}
