package repository

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルはメール受信（MailHandler）だけが使う読み取り。

// ActiveUserIDsHavingMail は User.active.having_mail(addresses)（追加のメールアドレスも含め、
// 大文字小文字を区別しない。id 順）。
func ActiveUserIDsHavingMail(ctx context.Context, q db.Queryer, addresses []string) ([]int64, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(addresses))
	marks := make([]string, 0, len(addresses))
	for _, a := range addresses {
		args = append(args, strings.ToLower(strings.TrimSpace(a)))
		marks = append(marks, "?")
	}
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT p.id FROM principals p WHERE p.kind = 'user' AND p.status = 1
AND p.id IN (SELECT e.user_id FROM email_addresses e WHERE LOWER(e.address) IN (`+strings.Join(marks, ", ")+`))
ORDER BY p.id`, args...)
	return ids, err
}

// NewsCommentNewsID は Comment.find_by_id(id).commented（ニュースのコメントのニュース id）。無ければ ErrNotFound。
func NewsCommentNewsID(ctx context.Context, q db.Queryer, commentID int64) (int64, error) {
	var id int64
	if err := q.Get(ctx, &id, `SELECT news_id FROM news_comments WHERE id = ?`, commentID); err != nil {
		return 0, notFound(err)
	}
	return id, nil
}
