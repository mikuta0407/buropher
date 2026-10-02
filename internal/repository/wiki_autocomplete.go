package repository

import (
	"context"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// AutoCompleteWikiPages は auto_completes#wiki_pages の
// wiki.pages.reorder(id: :desc)[.where("LOWER(title) LIKE LOWER(?)", "%#{q}%")].limit(limit)。
// term が空なら絞り込まない（Redmine と同じく LIKE のワイルドカードはエスケープしない）。
func AutoCompleteWikiPages(ctx context.Context, q db.Queryer, wikiID int64, term string, limit int) ([]*domain.WikiPage, error) {
	where := "WHERE wiki_pages.wiki_id = ?"
	args := []any{wikiID}
	if term != "" {
		where += " AND LOWER(wiki_pages.title) LIKE LOWER(?)"
		args = append(args, "%"+term+"%")
	}
	return selectWikiPages(ctx, q, where+" ORDER BY wiki_pages.id DESC LIMIT "+strconv.Itoa(limit), args...)
}
