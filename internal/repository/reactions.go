package repository

import (
	"context"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// ReactionDetail は Reaction::Detail（見えるユーザーと自分のリアクション）。
type ReactionDetail struct {
	// VisibleUsers はリアクションしたユーザーのうち見えるもの（reactions.id の降順）。
	VisibleUsers []*domain.User
	// UserReactionID は自分のリアクションの id（無ければ 0）。
	UserReactionID int64
}

// Count は reaction_count。
func (d *ReactionDetail) Count() int { return len(d.VisibleUsers) }

// ReactionDetails は Reaction.build_detail_map_for(reactables, user)。
// visibleCond は authz の PrincipalVisibleCondition（principals を参照する SQL）。
func ReactionDetails(ctx context.Context, q db.Queryer, kind string, ids []int64, visibleCond string, userID int64) (map[int64]*ReactionDetail, error) {
	out := map[int64]*ReactionDetail{}
	if len(ids) == 0 {
		return out, nil
	}
	if visibleCond == "" {
		visibleCond = "1=1"
	}
	query, args, err := db.In(`SELECT reactions.id, reactions.reactable_id, reactions.user_id FROM reactions
JOIN principals ON principals.id = reactions.user_id
WHERE reactions.reactable_kind = ? AND reactions.reactable_id IN (?) AND (`+visibleCond+`)
ORDER BY reactions.id DESC`, kind, uniqIDs(ids))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID          int64 `db:"id"`
		ReactableID int64 `db:"reactable_id"`
		UserID      int64 `db:"user_id"`
	}
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	var uids []int64
	for _, r := range rows {
		uids = append(uids, r.UserID)
	}
	users, err := UsersByIDs(ctx, q, uids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		d := out[r.ReactableID]
		if d == nil {
			d = &ReactionDetail{}
			out[r.ReactableID] = d
		}
		if u := users[r.UserID]; u != nil {
			d.VisibleUsers = append(d.VisibleUsers, u)
		}
		if r.UserID == userID && userID != 0 {
			d.UserReactionID = r.ID
		}
	}
	return out, nil
}

// ReactableTarget はリアクションの対象（種類と id）の所属プロジェクトと、ジャーナルの場合のチケット・非公開注記。
type ReactableTarget struct {
	ProjectID    int64
	IssueID      int64
	PrivateNotes bool
}

// FindReactable は object_type.constantize.find(object_id) の所属を引く（無ければ ErrNotFound）。
func FindReactable(ctx context.Context, q db.Queryer, kind string, id int64) (*ReactableTarget, error) {
	t := &ReactableTarget{}
	var err error
	switch kind {
	case "issue":
		err = q.Get(ctx, &t.ProjectID, `SELECT project_id FROM issues WHERE id = ?`, id)
		t.IssueID = id
	case "journal":
		var row struct {
			IssueID      int64 `db:"issue_id"`
			ProjectID    int64 `db:"project_id"`
			PrivateNotes bool  `db:"private_notes"`
		}
		err = q.Get(ctx, &row, `SELECT j.issue_id, i.project_id, j.private_notes FROM issue_journals j JOIN issues i ON i.id = j.issue_id WHERE j.id = ?`, id)
		t.IssueID, t.ProjectID, t.PrivateNotes = row.IssueID, row.ProjectID, row.PrivateNotes
	case "news":
		err = q.Get(ctx, &t.ProjectID, `SELECT project_id FROM news WHERE id = ?`, id)
	case "comment":
		err = q.Get(ctx, &t.ProjectID, `SELECT n.project_id FROM news_comments c JOIN news n ON n.id = c.news_id WHERE c.id = ?`, id)
	case "message":
		err = q.Get(ctx, &t.ProjectID, `SELECT b.project_id FROM messages m JOIN boards b ON b.id = m.board_id WHERE m.id = ?`, id)
	default:
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, notFound(err)
	}
	return t, nil
}

// FindOrCreateReaction は reactions.find_or_create_by!(user: user)。
func FindOrCreateReaction(ctx context.Context, q db.Queryer, kind string, id, userID int64, now time.Time) error {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM reactions WHERE reactable_kind = ? AND reactable_id = ? AND user_id = ?`, kind, id, userID); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := q.Exec(ctx, `INSERT INTO reactions (reactable_kind, reactable_id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		kind, id, userID, db.NewTime(now), db.NewTime(now))
	return err
}

// DeleteUserReaction は reactions.by(user).find_by(id: reactionID)&.destroy。
func DeleteUserReaction(ctx context.Context, q db.Queryer, kind string, id, userID, reactionID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM reactions WHERE id = ? AND reactable_kind = ? AND reactable_id = ? AND user_id = ?`, reactionID, kind, id, userID)
	return err
}
