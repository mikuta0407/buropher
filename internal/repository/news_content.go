package repository

import (
	"context"
	"database/sql"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはニュース（News）とそのコメント（Comment）の読み書き。

const newsSelect = `SELECT news.id, news.project_id, news.title, news.summary, news.description, news.author_id,
  news.comments_count, news.created_at FROM news JOIN projects ON projects.id = news.project_id`

// GetNews は News.find(id)（project と author を読み込む）。
func GetNews(ctx context.Context, q db.Queryer, id int64) (*domain.News, error) {
	var rows []newsRow
	if err := q.Select(ctx, &rows, newsSelect+` WHERE news.id = ?`, id); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	out, err := preloadNews(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// newsScope は (@project ? @project.news.visible : News.visible) の WHERE 句。
func newsScope(visible string, projectID int64) (string, []any) {
	if visible == "" {
		visible = "1=1"
	}
	where := "(" + visible + ")"
	var args []any
	if projectID != 0 {
		where += " AND news.project_id = ?"
		args = append(args, projectID)
	}
	return where, args
}

// CountNews は scope.count。projectID が 0 なら全プロジェクト。
func CountNews(ctx context.Context, q db.Queryer, visible string, projectID int64) (int, error) {
	where, args := newsScope(visible, projectID)
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM news JOIN projects ON projects.id = news.project_id WHERE `+where, args...)
	return n, err
}

// ListNews は scope.includes(:author, :project).order(created_on DESC).limit.offset。
func ListNews(ctx context.Context, q db.Queryer, visible string, projectID int64, limit, offset int) ([]*domain.News, error) {
	where, args := newsScope(visible, projectID)
	var rows []newsRow
	// 同時刻は id の降順（LatestNews と同じ。参照環境の SQLite の走査順）
	if err := q.Select(ctx, &rows, newsSelect+` WHERE `+where+` ORDER BY news.created_at DESC, news.id DESC `+
		q.Dialect().LimitOffset(limit, offset), args...); err != nil {
		return nil, err
	}
	return preloadNews(ctx, q, rows)
}

// InsertNews は news 行を作成して n.ID を設定する。
func InsertNews(ctx context.Context, q db.Queryer, n *domain.News) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO news (project_id, title, summary, description, author_id, comments_count, created_at)
VALUES (?, ?, ?, ?, ?, 0, ?)`, n.ProjectID, n.Title, nullStr(n.Summary), nullStr(n.Description), n.AuthorID, db.NewTime(n.CreatedAt))
	if err != nil {
		return err
	}
	n.ID = id
	return nil
}

// UpdateNews は title / summary / description を保存する。
func UpdateNews(ctx context.Context, q db.Queryer, n *domain.News) error {
	_, err := q.Exec(ctx, `UPDATE news SET title = ?, summary = ?, description = ? WHERE id = ?`,
		n.Title, nullStr(n.Summary), nullStr(n.Description), n.ID)
	return err
}

// DeleteNews は news 行を削除する（コメントは FK の CASCADE。コメントのリアクション・
// ニュースのリアクション・ウォッチャーもここで消す。添付は呼び出し側が DeleteContainerAttachments で消す）。
func DeleteNews(ctx context.Context, q db.Queryer, id int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM reactions WHERE reactable_kind = 'comment' AND reactable_id IN (SELECT id FROM news_comments WHERE news_id = ?)`, id); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM reactions WHERE reactable_kind = 'news' AND reactable_id = ?`, id); err != nil {
		return err
	}
	if err := DeleteWatchers(ctx, q, "news", []int64{id}); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM news WHERE id = ?`, id)
	return err
}

type commentRow struct {
	ID        int64          `db:"id"`
	NewsID    int64          `db:"news_id"`
	AuthorID  int64          `db:"author_id"`
	Content   sql.NullString `db:"content"`
	CreatedAt db.Time        `db:"created_at"`
	UpdatedAt db.Time        `db:"updated_at"`
}

// NewsComments は news.comments（created_on 順。author を読み込む）。
func NewsComments(ctx context.Context, q db.Queryer, newsID int64) ([]*domain.Comment, error) {
	var rows []commentRow
	if err := q.Select(ctx, &rows, `SELECT id, news_id, author_id, content, created_at, updated_at FROM news_comments
WHERE news_id = ? ORDER BY created_at, id`, newsID); err != nil {
		return nil, err
	}
	out := make([]*domain.Comment, len(rows))
	var uids []int64
	for i, r := range rows {
		out[i] = &domain.Comment{ID: r.ID, NewsID: r.NewsID, AuthorID: r.AuthorID, Content: r.Content.String,
			CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
		uids = append(uids, r.AuthorID)
	}
	if len(out) == 0 {
		return out, nil
	}
	users, err := UsersByIDs(ctx, q, uids)
	if err != nil {
		return nil, err
	}
	for _, c := range out {
		c.Author = users[c.AuthorID]
	}
	return out, nil
}

// InsertComment はコメントを作成し、news.comments_count を増やす（counter_cache）。
func InsertComment(ctx context.Context, q db.Queryer, c *domain.Comment) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO news_comments (news_id, author_id, content, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		c.NewsID, c.AuthorID, nullStr(c.Content), db.NewTime(c.CreatedAt), db.NewTime(c.UpdatedAt))
	if err != nil {
		return err
	}
	c.ID = id
	_, err = q.Exec(ctx, `UPDATE news SET comments_count = comments_count + 1 WHERE id = ?`, c.NewsID)
	return err
}

// DeleteComment はニュースのコメントを削除し、comments_count を減らす。無ければ ErrNotFound。
func DeleteComment(ctx context.Context, q db.Queryer, newsID, commentID int64) error {
	res, err := q.Exec(ctx, `DELETE FROM news_comments WHERE id = ? AND news_id = ?`, commentID, newsID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := q.Exec(ctx, `DELETE FROM reactions WHERE reactable_kind = 'comment' AND reactable_id = ?`, commentID); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE news SET comments_count = comments_count - 1 WHERE id = ?`, newsID)
	return err
}

// ProjectModuleID は project.enabled_module(name) の id（無ければ ErrNotFound）。
func ProjectModuleID(ctx context.Context, q db.Queryer, projectID int64, name string) (int64, error) {
	var id int64
	if err := q.Get(ctx, &id, `SELECT id FROM project_modules WHERE project_id = ? AND name = ?`, projectID, name); err != nil {
		return 0, notFound(err)
	}
	return id, nil
}
