package repository

import (
	"context"
	"database/sql"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type newsRow struct {
	ID            int64          `db:"id"`
	ProjectID     int64          `db:"project_id"`
	Title         string         `db:"title"`
	Summary       sql.NullString `db:"summary"`
	Description   sql.NullString `db:"description"`
	AuthorID      int64          `db:"author_id"`
	CommentsCount int            `db:"comments_count"`
	CreatedAt     db.Time        `db:"created_at"`
}

func (r *newsRow) news() *domain.News {
	return &domain.News{
		ID: r.ID, ProjectID: r.ProjectID, Title: r.Title, Summary: r.Summary.String, Description: r.Description.String,
		AuthorID: r.AuthorID, CommentsCount: r.CommentsCount, CreatedAt: r.CreatedAt.Time,
	}
}

// LatestNews は News.latest(user, count) の移植: 可視なニュースを新しい順に count 件、
// project と author を preload して返す。
//
// visible は News.visible の条件で、呼び出し側が
// authz.Authorizer.AllowedToCondition(ctx, "view_news", ...) で作った projects 参照の SQL を渡す
// (repository は authz に依存しないため、可視性の SQL は引数で受け取るのが規約)。
func LatestNews(ctx context.Context, q db.Queryer, visible string, count int) ([]*domain.News, error) {
	if visible == "" {
		visible = "1=1"
	}
	var rows []newsRow
	// 同時刻のニュースは id の降順 (参照環境の SQLite が created_on の索引を逆順に走査する順)
	if err := q.Select(ctx, &rows, `SELECT news.id, news.project_id, news.title, news.summary, news.description, news.author_id,
  news.comments_count, news.created_at
FROM news JOIN projects ON projects.id = news.project_id
WHERE `+visible+`
ORDER BY news.created_at DESC, news.id DESC `+q.Dialect().LimitOffset(count, 0)); err != nil {
		return nil, err
	}
	return preloadNews(ctx, q, rows)
}

// preloadNews は project / author を読み込む。
func preloadNews(ctx context.Context, q db.Queryer, rows []newsRow) ([]*domain.News, error) {
	out := make([]*domain.News, len(rows))
	var pids, uids []int64
	for i := range rows {
		out[i] = rows[i].news()
		pids = append(pids, rows[i].ProjectID)
		uids = append(uids, rows[i].AuthorID)
	}
	if len(out) == 0 {
		return out, nil
	}
	projects, err := ProjectsByIDs(ctx, q, pids)
	if err != nil {
		return nil, err
	}
	users, err := UsersByIDs(ctx, q, uids)
	if err != nil {
		return nil, err
	}
	for _, n := range out {
		n.Project = projects[n.ProjectID]
		n.Author = users[n.AuthorID]
	}
	return out, nil
}
