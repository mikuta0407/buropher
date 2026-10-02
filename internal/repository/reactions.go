package repository

import (
	"context"

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
