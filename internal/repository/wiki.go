package repository

// Wiki（WikiController / WikisController）の読み書き。textilizable 用の参照（RefWiki 等）は wikitext.go。
//
// Redmine の wiki_contents（最新版）と wiki_content_versions（履歴）は wiki_page_versions に統合されており、
// WikiContent は wiki_pages.current_version の版の行、楽観ロック（locking_column = version）は
// wiki_pages.current_version の比較で行う。

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// ErrStaleObject は楽観ロックの競合（ActiveRecord::StaleObjectError）。
var ErrStaleObject = errors.New("repository: stale object")

type wikiRow struct {
	ID        int64  `db:"id"`
	ProjectID int64  `db:"project_id"`
	StartPage string `db:"start_page"`
}

// FindWiki は Project#wiki（無ければ ErrNotFound）。
func FindWiki(ctx context.Context, q db.Queryer, projectID int64) (*domain.Wiki, error) {
	var r wikiRow
	if err := q.Get(ctx, &r, `SELECT id, project_id, start_page FROM wikis WHERE project_id = ?`, projectID); err != nil {
		return nil, notFound(err)
	}
	return &domain.Wiki{ID: r.ID, ProjectID: r.ProjectID, StartPage: r.StartPage}, nil
}

// GetWiki は id の Wiki。
func GetWiki(ctx context.Context, q db.Queryer, id int64) (*domain.Wiki, error) {
	var r wikiRow
	if err := q.Get(ctx, &r, `SELECT id, project_id, start_page FROM wikis WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return &domain.Wiki{ID: r.ID, ProjectID: r.ProjectID, StartPage: r.StartPage}, nil
}

// UpdateWikiStartPage は wiki.update_attribute :start_page。
func UpdateWikiStartPage(ctx context.Context, q db.Queryer, wikiID int64, title string) error {
	_, err := q.Exec(ctx, `UPDATE wikis SET start_page = ? WHERE id = ?`, title, wikiID)
	return err
}

type wikiPageRow struct {
	ID             int64         `db:"id"`
	WikiID         int64         `db:"wiki_id"`
	Title          string        `db:"title"`
	ParentID       sql.NullInt64 `db:"parent_id"`
	Protected      bool          `db:"protected"`
	CurrentVersion int           `db:"current_version"`
	CreatedAt      db.Time       `db:"created_at"`
	UpdatedOn      db.NullTime   `db:"updated_on"`
}

func (r *wikiPageRow) page() *domain.WikiPage {
	p := &domain.WikiPage{
		ID: r.ID, WikiID: r.WikiID, Title: r.Title, ParentID: nullID(r.ParentID), Protected: r.Protected,
		CurrentVersion: r.CurrentVersion, CreatedAt: r.CreatedAt.Time, HasContent: r.UpdatedOn.Valid,
	}
	if r.UpdatedOn.Valid {
		p.UpdatedOn = r.UpdatedOn.Time
	}
	return p
}

const wikiPageSelect = `SELECT wiki_pages.id, wiki_pages.wiki_id, wiki_pages.title, wiki_pages.parent_id, wiki_pages.protected,
  wiki_pages.current_version, wiki_pages.created_at, v.updated_at AS updated_on
FROM wiki_pages
LEFT JOIN wiki_page_versions v ON v.page_id = wiki_pages.id AND v.version = wiki_pages.current_version`

func selectWikiPages(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.WikiPage, error) {
	var rows []wikiPageRow
	if err := q.Select(ctx, &rows, wikiPageSelect+" "+where, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.WikiPage, len(rows))
	for i := range rows {
		out[i] = rows[i].page()
	}
	return out, nil
}

func oneWikiPage(ps []*domain.WikiPage, err error) (*domain.WikiPage, error) {
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, ErrNotFound
	}
	return ps[0], nil
}

// FindWikiPage は wiki.pages.find_by("LOWER(title) = LOWER(?)", title)（リダイレクトは見ない）。
func FindWikiPage(ctx context.Context, q db.Queryer, wikiID int64, title string) (*domain.WikiPage, error) {
	return oneWikiPage(selectWikiPages(ctx, q, `WHERE wiki_pages.wiki_id = ? AND LOWER(wiki_pages.title) = LOWER(?) ORDER BY wiki_pages.id LIMIT 1`, wikiID, title))
}

// GetWikiPageByID は id の Wiki ページ（wiki_id が 0 でなければそのWiki内に限る: wiki.pages.find_by_id）。
func GetWikiPageByID(ctx context.Context, q db.Queryer, wikiID, id int64) (*domain.WikiPage, error) {
	if wikiID == 0 {
		return oneWikiPage(selectWikiPages(ctx, q, `WHERE wiki_pages.id = ?`, id))
	}
	return oneWikiPage(selectWikiPages(ctx, q, `WHERE wiki_pages.id = ? AND wiki_pages.wiki_id = ?`, id, wikiID))
}

// FindWikiRedirectTarget は wiki.redirects.where("LOWER(title) = LOWER(?)", title).first.target_page。
// リダイレクトが無ければ ErrNotFound、転送先が無ければ (nil, nil)。
func FindWikiRedirectTarget(ctx context.Context, q db.Queryer, wikiID int64, title string) (*domain.WikiPage, error) {
	var r struct {
		RedirectsTo   string `db:"redirects_to"`
		RedirectsWiki int64  `db:"redirects_to_wiki_id"`
	}
	if err := q.Get(ctx, &r, `SELECT redirects_to, redirects_to_wiki_id FROM wiki_redirects
WHERE wiki_id = ? AND LOWER(title) = LOWER(?) ORDER BY id LIMIT 1`, wikiID, title); err != nil {
		return nil, notFound(err)
	}
	p, err := FindWikiPage(ctx, q, r.RedirectsWiki, r.RedirectsTo)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return p, err
}

// WikiPages は wiki.pages.with_updated_on（LOWER(title) 順）。
func WikiPages(ctx context.Context, q db.Queryer, wikiID int64) ([]*domain.WikiPage, error) {
	return selectWikiPages(ctx, q, `WHERE wiki_pages.wiki_id = ? ORDER BY LOWER(wiki_pages.title), wiki_pages.id`, wikiID)
}

// WikiPageChildPages は page.children（title 順）。
func WikiPageChildPages(ctx context.Context, q db.Queryer, pageID int64) ([]*domain.WikiPage, error) {
	return selectWikiPages(ctx, q, `WHERE wiki_pages.parent_id = ? ORDER BY wiki_pages.title, wiki_pages.id`, pageID)
}

// WikiPagesByIDs は id 指定のページ（id 順）。
func WikiPagesByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.WikiPage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`WHERE wiki_pages.id IN (?) ORDER BY wiki_pages.id`, ids)
	if err != nil {
		return nil, err
	}
	return selectWikiPages(ctx, q, query, args...)
}

// WikiPageDescendantIDs は page.descendants の id（幅優先。自身は含まない）。
func WikiPageDescendantIDs(ctx context.Context, q db.Queryer, pageID int64) ([]int64, error) {
	var out []int64
	frontier := []int64{pageID}
	seen := map[int64]bool{pageID: true}
	for len(frontier) > 0 {
		query, args, err := db.In(`SELECT id FROM wiki_pages WHERE parent_id IN (?) ORDER BY title, id`, frontier)
		if err != nil {
			return nil, err
		}
		var ids []int64
		if err := q.Select(ctx, &ids, query, args...); err != nil {
			return nil, err
		}
		frontier = frontier[:0]
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
				frontier = append(frontier, id)
			}
		}
	}
	return out, nil
}

// WikiPageAncestors は page.ancestors（親から根の順）。
func WikiPageAncestors(ctx context.Context, q db.Queryer, page *domain.WikiPage) ([]*domain.WikiPage, error) {
	var out []*domain.WikiPage
	seen := map[int64]bool{}
	pid := page.ParentID
	for pid != nil && !seen[*pid] {
		seen[*pid] = true
		p, err := GetWikiPageByID(ctx, q, 0, *pid)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
		pid = p.ParentID
	}
	return out, nil
}

// ---- 版（WikiContent / WikiContentVersion）

type wikiVersionRow struct {
	ID        int64          `db:"id"`
	PageID    int64          `db:"page_id"`
	Version   int            `db:"version"`
	AuthorID  sql.NullInt64  `db:"author_id"`
	Text      string         `db:"text"`
	Comments  sql.NullString `db:"comments"`
	UpdatedAt db.Time        `db:"updated_at"`
}

func (r *wikiVersionRow) version() *domain.WikiContentVersion {
	v := &domain.WikiContentVersion{
		ID: r.ID, PageID: r.PageID, Version: r.Version, AuthorID: nullID(r.AuthorID), Text: r.Text,
		UpdatedOn: r.UpdatedAt.Time,
	}
	if r.Comments.Valid {
		s := r.Comments.String
		v.Comments = &s
	}
	return v
}

func selectWikiVersions(ctx context.Context, q db.Queryer, cols, where string, args ...any) ([]*domain.WikiContentVersion, error) {
	var rows []wikiVersionRow
	if err := q.Select(ctx, &rows, `SELECT `+cols+` FROM wiki_page_versions `+where, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.WikiContentVersion, len(rows))
	var authorIDs []int64
	for i := range rows {
		out[i] = rows[i].version()
		if out[i].AuthorID != nil {
			authorIDs = append(authorIDs, *out[i].AuthorID)
		}
	}
	if len(authorIDs) > 0 {
		users, err := UsersByIDs(ctx, q, authorIDs)
		if err != nil {
			return nil, err
		}
		for _, v := range out {
			if v.AuthorID != nil {
				v.Author = users[*v.AuthorID]
			}
		}
	}
	return out, nil
}

const wikiVersionCols = `id, page_id, version, author_id, text, comments, updated_at`

// 本文を読まない列（history の select("id, author_id, comments, updated_on, version")）。
const wikiVersionColsNoText = `id, page_id, version, author_id, '' AS text, comments, updated_at`

func oneWikiVersion(vs []*domain.WikiContentVersion, err error) (*domain.WikiContentVersion, error) {
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, ErrNotFound
	}
	return vs[0], nil
}

// WikiPageVersion は content.versions.find_by_version(version)。
func WikiPageVersion(ctx context.Context, q db.Queryer, pageID int64, version int) (*domain.WikiContentVersion, error) {
	return oneWikiVersion(selectWikiVersions(ctx, q, wikiVersionCols, `WHERE page_id = ? AND version = ?`, pageID, version))
}

// WikiPreviousVersion は WikiContentVersion#previous。
func WikiPreviousVersion(ctx context.Context, q db.Queryer, pageID int64, version int) (*domain.WikiContentVersion, error) {
	return oneWikiVersion(selectWikiVersions(ctx, q, wikiVersionCols, `WHERE page_id = ? AND version < ? ORDER BY version DESC LIMIT 1`, pageID, version))
}

// WikiNextVersion は WikiContentVersion#next。
func WikiNextVersion(ctx context.Context, q db.Queryer, pageID int64, version int) (*domain.WikiContentVersion, error) {
	return oneWikiVersion(selectWikiVersions(ctx, q, wikiVersionCols, `WHERE page_id = ? AND version > ? ORDER BY version ASC LIMIT 1`, pageID, version))
}

// WikiAllVersions は content.versions（version 昇順、本文込み）。
func WikiAllVersions(ctx context.Context, q db.Queryer, pageID int64) ([]*domain.WikiContentVersion, error) {
	return selectWikiVersions(ctx, q, wikiVersionCols, `WHERE page_id = ? ORDER BY version`, pageID)
}

// WikiVersionsCount は content.versions.count。
func WikiVersionsCount(ctx context.Context, q db.Queryer, pageID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM wiki_page_versions WHERE page_id = ?`, pageID)
	return n, err
}

// WikiVersionsPage は history の版一覧（version 降順、本文なし）。
func WikiVersionsPage(ctx context.Context, q db.Queryer, pageID int64, limit, offset int) ([]*domain.WikiContentVersion, error) {
	return selectWikiVersions(ctx, q, wikiVersionColsNoText, `WHERE page_id = ? ORDER BY version DESC LIMIT ? OFFSET ?`, pageID, limit, offset)
}

// ---- 書き込み

// CreateWikiPage はページを作成し id を設定する（本文なし: current_version = 0）。
func CreateWikiPage(ctx context.Context, q db.Queryer, p *domain.WikiPage, now time.Time) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO wiki_pages (wiki_id, title, parent_id, protected, current_version, created_at)
VALUES (?, ?, ?, ?, 0, ?)`, p.WikiID, p.Title, p.ParentID, p.Protected, db.NewTime(now))
	if err != nil {
		return err
	}
	p.ID = id
	p.CreatedAt = db.NewTime(now).Time
	return nil
}

// UpdateWikiPageAttrs はページの title / wiki_id / parent_id / protected を保存する。
func UpdateWikiPageAttrs(ctx context.Context, q db.Queryer, p *domain.WikiPage) error {
	_, err := q.Exec(ctx, `UPDATE wiki_pages SET wiki_id = ?, title = ?, parent_id = ?, protected = ? WHERE id = ?`,
		p.WikiID, p.Title, p.ParentID, p.Protected, p.ID)
	return err
}

// SetWikiPageParent は update_attribute(:parent_id)。
func SetWikiPageParent(ctx context.Context, q db.Queryer, pageID int64, parentID *int64) error {
	_, err := q.Exec(ctx, `UPDATE wiki_pages SET parent_id = ? WHERE id = ?`, parentID, pageID)
	return err
}

// SetWikiPageProtected は update_attribute :protected。
func SetWikiPageProtected(ctx context.Context, q db.Queryer, pageID int64, protected bool) error {
	_, err := q.Exec(ctx, `UPDATE wiki_pages SET protected = ? WHERE id = ?`, protected, pageID)
	return err
}

// AddWikiContentVersion は WikiContent の保存（新しい版の作成）。lockVersion はフォームの版
// （locking_column の比較値）で、現在の current_version と異なれば ErrStaleObject を返す。
// 新しい版の番号は lockVersion + 1（新規ページは 1）。
func AddWikiContentVersion(ctx context.Context, q db.Queryer, pageID int64, lockVersion int, authorID *int64, text string, comments *string, now time.Time) (int, error) {
	newVersion := lockVersion + 1
	res, err := q.Exec(ctx, `UPDATE wiki_pages SET current_version = ? WHERE id = ? AND current_version = ?`, newVersion, pageID, lockVersion)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, err
	} else if n == 0 {
		return 0, ErrStaleObject
	}
	// 履歴を削除した後の再作成などで同じ版が残っていれば置き換える
	if _, err := q.Exec(ctx, `DELETE FROM wiki_page_versions WHERE page_id = ? AND version = ?`, pageID, newVersion); err != nil {
		return 0, err
	}
	if _, err := q.Exec(ctx, `INSERT INTO wiki_page_versions (page_id, version, author_id, text, comments, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		pageID, newVersion, authorID, text, comments, db.NewTime(now)); err != nil {
		return 0, err
	}
	return newVersion, nil
}

// DeleteWikiVersion は WikiContentVersion#destroy（after_destroy :page_update_after_destroy を含む）。
// 最新版を消した場合は残る最大の版を最新にし、版が無くなればページを削除する（pageDeleted = true）。
func DeleteWikiVersion(ctx context.Context, q db.Queryer, pageID int64, version int) (pageDeleted bool, err error) {
	if _, err := q.Exec(ctx, `DELETE FROM wiki_page_versions WHERE page_id = ? AND version = ?`, pageID, version); err != nil {
		return false, err
	}
	var latest sql.NullInt64
	if err := q.Get(ctx, &latest, `SELECT MAX(version) FROM wiki_page_versions WHERE page_id = ?`, pageID); err != nil {
		return false, err
	}
	if !latest.Valid {
		return true, nil
	}
	_, err = q.Exec(ctx, `UPDATE wiki_pages SET current_version = ? WHERE id = ?`, latest.Int64, pageID)
	return false, err
}

// DeleteWikiPageRow はページ行を削除する（版は ON DELETE CASCADE、子ページは parent_id が NULL になる）。
// 添付・ウォッチャー・リダイレクトの削除は呼び出し側で行う。
func DeleteWikiPageRow(ctx context.Context, q db.Queryer, pageID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM wiki_pages WHERE id = ?`, pageID)
	return err
}

// DeleteWikiRedirectsTo は WikiPage#delete_redirects（このページへのリダイレクトを削除）。
func DeleteWikiRedirectsTo(ctx context.Context, q db.Queryer, wikiID int64, title string) error {
	_, err := q.Exec(ctx, `DELETE FROM wiki_redirects WHERE redirects_to_wiki_id = ? AND redirects_to = ?`, wikiID, title)
	return err
}

// WikiHandleRename は WikiPage#handle_rename_or_move のリダイレクト処理。
func WikiHandleRename(ctx context.Context, q db.Queryer, oldWikiID int64, oldTitle string, newWikiID int64, newTitle string, createRedirect bool, now time.Time) error {
	// 旧タイトルを指すリダイレクトを新タイトルへ付け替える（自分自身を指すものは削除）
	type redir struct {
		ID     int64  `db:"id"`
		WikiID int64  `db:"wiki_id"`
		Title  string `db:"title"`
	}
	var rs []redir
	if err := q.Select(ctx, &rs, `SELECT id, wiki_id, title FROM wiki_redirects WHERE redirects_to = ? AND redirects_to_wiki_id = ? ORDER BY id`, oldTitle, oldWikiID); err != nil {
		return err
	}
	for _, r := range rs {
		if r.Title == newTitle && r.WikiID == newWikiID {
			if _, err := q.Exec(ctx, `DELETE FROM wiki_redirects WHERE id = ?`, r.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := q.Exec(ctx, `UPDATE wiki_redirects SET redirects_to = ?, redirects_to_wiki_id = ? WHERE id = ?`, newTitle, newWikiID, r.ID); err != nil {
			return err
		}
	}
	// 新タイトルのリダイレクトを削除
	if _, err := q.Exec(ctx, `DELETE FROM wiki_redirects WHERE wiki_id = ? AND title = ?`, newWikiID, newTitle); err != nil {
		return err
	}
	if createRedirect {
		if _, err := q.Exec(ctx, `INSERT INTO wiki_redirects (wiki_id, title, redirects_to, redirects_to_wiki_id, created_at) VALUES (?, ?, ?, ?, ?)`,
			oldWikiID, oldTitle, newTitle, newWikiID, db.NewTime(now)); err != nil {
			return err
		}
	}
	return nil
}

// WikiTitleTaken は validates_uniqueness_of :title, :scope => :wiki_id, :case_sensitive => false。
func WikiTitleTaken(ctx context.Context, q db.Queryer, wikiID int64, title string, exceptID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM wiki_pages WHERE wiki_id = ? AND LOWER(title) = LOWER(?) AND id <> ?`, wikiID, title, exceptID)
	return n > 0, err
}

// DeleteWiki は Wiki#destroy（ページ・版・リダイレクトを含む）。ページの添付・ウォッチャーは呼び出し側で削除する。
func DeleteWiki(ctx context.Context, q db.Queryer, wikiID int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM wiki_redirects WHERE wiki_id = ? OR redirects_to_wiki_id = ?`, wikiID, wikiID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE wiki_pages SET parent_id = NULL WHERE wiki_id = ?`, wikiID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM wiki_pages WHERE wiki_id = ?`, wikiID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM watchers WHERE watchable_kind = 'wiki' AND watchable_id = ?`, wikiID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM wikis WHERE id = ?`, wikiID)
	return err
}

// WikiPageIDs は wiki のページ id（id 順）。
func WikiPageIDs(ctx context.Context, q db.Queryer, wikiID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT id FROM wiki_pages WHERE wiki_id = ? ORDER BY id`, wikiID)
	return ids, err
}

// ProjectsWithWikiAllowed は Project.allowed_to(:rename_wiki_pages).joins(:wiki) の (プロジェクト, wiki id)。
// cond は authz の AllowedToCondition の結果。
func ProjectsWithWiki(ctx context.Context, q db.Queryer, cond string) ([]*domain.Project, map[int64]int64, error) {
	var rows []struct {
		ProjectID int64 `db:"project_id"`
		WikiID    int64 `db:"wiki_id"`
	}
	if err := q.Select(ctx, &rows, `SELECT projects.id AS project_id, wikis.id AS wiki_id FROM projects JOIN wikis ON wikis.project_id = projects.id
WHERE `+condOr(cond)+` ORDER BY projects.id`); err != nil {
		return nil, nil, err
	}
	wikis := map[int64]int64{}
	var ids []int64
	for _, r := range rows {
		wikis[r.ProjectID] = r.WikiID
		ids = append(ids, r.ProjectID)
	}
	if len(ids) == 0 {
		return nil, wikis, nil
	}
	query, args, err := db.In(`projects.id IN (?)`, ids)
	if err != nil {
		return nil, nil, err
	}
	ps, err := LoadProjects(ctx, q, query, args...)
	return ps, wikis, err
}

// ---- ウォッチャー（acts_as_watchable）

// Watchers は watchable の watcher_users（ユーザーの名前順は呼び出し側で並べる。ここでは watchers.id 順）。
func Watchers(ctx context.Context, q db.Queryer, kind string, id int64) ([]*domain.User, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT principal_id FROM watchers WHERE watchable_kind = ? AND watchable_id = ? ORDER BY id`, kind, id); err != nil {
		return nil, err
	}
	users, err := UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	var out []*domain.User
	for _, uid := range ids {
		if u := users[uid]; u != nil {
			out = append(out, u)
		}
	}
	return out, nil
}

// WatchedBy は watched_by?(user)。
func WatchedBy(ctx context.Context, q db.Queryer, kind string, id, userID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = ? AND watchable_id = ? AND principal_id = ?`, kind, id, userID)
	return n > 0, err
}

// DeleteWatchers は acts_as_watchable の dependent: :delete_all。
func DeleteWatchers(ctx context.Context, q db.Queryer, kind string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	query, args, err := db.In(`DELETE FROM watchers WHERE watchable_kind = ? AND watchable_id IN (?)`, kind, ids)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, query, args...)
	return err
}

// AddWatcher は add_watcher（既にあれば何もしない）。
func AddWatcher(ctx context.Context, q db.Queryer, kind string, id, userID int64) error {
	ok, err := WatchedBy(ctx, q, kind, id, userID)
	if err != nil || ok {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES (?, ?, ?)`, kind, id, userID)
	return err
}
