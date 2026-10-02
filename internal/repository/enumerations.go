package repository

import (
	"context"
	"fmt"

	"github.com/mikuta0407/buropher/internal/db"
)

// ComputePriorityPositionNames は IssuePriority.compute_position_names の移植。
// 有効な優先度を position 順に並べ、既定（なければ中央）を基準に CSS 用の名前を付ける。
func ComputePriorityPositionNames(ctx context.Context, q db.Queryer) error {
	var ps []struct {
		ID        int64 `db:"id"`
		Position int   `db:"position"`
	}
	if err := q.Select(ctx, &ps, `SELECT id, position FROM issue_priorities WHERE active = ? ORDER BY position`, true); err != nil {
		return err
	}
	if len(ps) == 0 {
		return nil
	}
	// default_or_middle: 既定（無効なものも含む）があればそれ、なければ有効なものの中央
	def := ps[(len(ps)-1)/2].Position
	var defPos []int
	if err := q.Select(ctx, &defPos, `SELECT position FROM issue_priorities WHERE is_default = ? ORDER BY id LIMIT 1`, true); err != nil {
		return err
	}
	if len(defPos) > 0 {
		def = defPos[0]
	}
	for i, p := range ps {
		var name string
		switch {
		case p.Position == def:
			name = "default"
		case p.Position < def:
			if i == 0 {
				name = "lowest"
			} else {
				name = fmt.Sprintf("low%d", i+1)
			}
		default:
			if i == len(ps)-1 {
				name = "highest"
			} else {
				name = fmt.Sprintf("high%d", len(ps)-i)
			}
		}
		if _, err := q.Exec(ctx, `UPDATE issue_priorities SET position_name = ? WHERE id = ?`, name, p.ID); err != nil {
			return err
		}
	}
	return nil
}
