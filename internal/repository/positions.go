package repository

import (
	"context"

	"github.com/mikuta0407/buropher/internal/db"
)

// PositionScope は Redmine::Acts::Positioned（acts_as_positioned）の position_scope。
// Table の行のうち Where（空なら全行）に一致する範囲で position を連番に保つ。
type PositionScope struct {
	Table string
	Where string
	Args  []any
}

func (s PositionScope) cond() string {
	if s.Where == "" {
		return "1=1"
	}
	return s.Where
}

func (s PositionScope) args(extra ...any) []any {
	return append(append([]any{}, s.Args...), extra...)
}

// NextPosition は set_default_position（新規行の既定位置 = 最大値 + 1）。
func NextPosition(ctx context.Context, q db.Queryer, s PositionScope) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COALESCE(MAX(position), 0) + 1 FROM `+s.Table+` WHERE `+s.cond(), s.args()...)
	return n, err
}

// InsertPosition は insert_position（新規行の挿入: position 以上の他の行を 1 つ後ろへずらす）。
func InsertPosition(ctx context.Context, q db.Queryer, s PositionScope, id int64, position int) error {
	_, err := q.Exec(ctx, `UPDATE `+s.Table+` SET position = position + 1 WHERE `+s.cond()+` AND position >= ? AND id <> ?`,
		s.args(position, id)...)
	return err
}

// RemovePosition は remove_position（削除: 後ろの行を 1 つ前へ詰める）。
func RemovePosition(ctx context.Context, q db.Queryer, s PositionScope, id int64, position int) error {
	_, err := q.Exec(ctx, `UPDATE `+s.Table+` SET position = position - 1 WHERE `+s.cond()+` AND position >= ? AND id <> ?`,
		s.args(position, id)...)
	return err
}

// ShiftPositions は shift_positions（既存行の移動 old → new。間の行を 1 つずらし、
// 更新件数が合わなければ reset_positions_in_list で振り直す）。
func ShiftPositions(ctx context.Context, q db.Queryer, s PositionScope, id int64, oldPos, newPos int) error {
	if oldPos == newPos {
		return nil
	}
	offset := 1
	if oldPos < newPos {
		offset = -1
	}
	lo, hi := min(oldPos, newPos), max(oldPos, newPos)
	res, err := q.Exec(ctx, `UPDATE `+s.Table+` SET position = position + ? WHERE `+s.cond()+` AND id <> ? AND position BETWEEN ? AND ?`,
		append([]any{offset}, s.args(id, lo, hi)...)...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if int(n) != hi-lo {
		return ResetPositions(ctx, q, s)
	}
	return nil
}

// ResetPositions は reset_positions_in_list（position, id の順に 1 から振り直す）。
func ResetPositions(ctx context.Context, q db.Queryer, s PositionScope) error {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT id FROM `+s.Table+` WHERE `+s.cond()+` ORDER BY position, id`, s.args()...); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := q.Exec(ctx, `UPDATE `+s.Table+` SET position = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return nil
}

// UpdatePosition は after_save の update_position（スコープ不変の場合）。
// oldPos が nil なら新規作成（insert_position）、そうでなければ shift_positions。
func UpdatePosition(ctx context.Context, q db.Queryer, s PositionScope, id int64, oldPos *int, newPos int) error {
	if oldPos == nil {
		return InsertPosition(ctx, q, s, id, newPos)
	}
	return ShiftPositions(ctx, q, s, id, *oldPos, newPos)
}
