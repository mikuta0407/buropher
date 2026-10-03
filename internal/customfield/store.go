package customfield

import (
	"context"
	"encoding/json"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルはカスタムフィールド・選択肢の保存と集計（CustomField#save の永続化部分、acts_as_positioned、
// has_many :custom_values, :dependent => :delete_all、CustomFieldEnumeration）。
// customfield は authz に依存し authz は repository に依存するため、SQL は repository ではなくここに置く。

// TrackersCustomFields は trackers.map(&:custom_fields).flatten.uniq.sort（position 順）。
func TrackersCustomFields(ctx context.Context, q db.Queryer, trackerIDs []int64) ([]*CustomField, error) {
	if len(trackerIDs) == 0 {
		return nil, nil
	}
	where, args, err := db.In(`custom_fields.owner_kind = 'issue' AND custom_fields.id IN (SELECT custom_field_id FROM custom_fields_trackers WHERE tracker_id IN (?))`, trackerIDs)
	if err != nil {
		return nil, err
	}
	return Load(ctx, q, where, args...)
}

// IssueProjectCounts は IssueCustomField.where(is_for_all: false).joins(:projects).group(:custom_field_id).count。
func IssueProjectCounts(ctx context.Context, q db.Queryer) (map[int64]int, error) {
	var rows []struct {
		ID int64 `db:"id"`
		N  int   `db:"n"`
	}
	if err := q.Select(ctx, &rows, `SELECT cfp.custom_field_id AS id, COUNT(*) AS n FROM custom_fields cf
  INNER JOIN custom_fields_projects cfp ON cfp.custom_field_id = cf.id
  WHERE cf.owner_kind = 'issue' AND cf.is_for_all = ? GROUP BY cfp.custom_field_id`, false); err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(rows))
	for _, r := range rows {
		out[r.ID] = r.N
	}
	return out, nil
}

// NameTaken は validates_uniqueness_of :name, :scope => :type（大文字小文字を区別）。
func NameTaken(ctx context.Context, q db.Queryer, kind OwnerKind, name string, exceptID int64) (bool, error) {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM custom_fields WHERE owner_kind = ? AND name = ? AND id <> ?`, string(kind), name, exceptID); err != nil {
		return false, err
	}
	return n > 0, nil
}

func jsonOrNil(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Save はカスタムフィールドの行と関連（ロール・トラッカー・プロジェクト）を保存する。
// cf.ID == 0 なら作成して id を設定する。position の調整（acts_as_positioned）は呼び出し側で行う。
// IsIssue でない種類ではトラッカー・プロジェクトを書き換えない。
func Save(ctx context.Context, q db.Queryer, cf *CustomField) error {
	pv, err := jsonOrNil(cf.PossibleValues)
	if err != nil {
		return err
	}
	fsMap := cf.Settings
	if fsMap == nil {
		fsMap = map[string]any{}
	}
	fs, err := json.Marshal(fsMap)
	if err != nil {
		return err
	}
	args := []any{cf.Name, cf.Description, cf.FieldFormat, cf.Regexp, cf.MinLength, cf.MaxLength, cf.IsRequired,
		cf.IsForAll, cf.IsFilter, cf.Searchable, cf.DefaultValue, cf.Editable, cf.Visible, cf.Multiple, cf.Position, pv, string(fs)}
	if cf.ID == 0 {
		id, err := q.InsertReturningID(ctx, `INSERT INTO custom_fields (owner_kind, name, description, field_format, regexp, min_length, max_length,
  is_required, is_for_all, is_filter, searchable, default_value, editable, visible, multiple, position, possible_values, format_settings)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append([]any{string(cf.OwnerKind)}, args...)...)
		if err != nil {
			return err
		}
		cf.ID = id
	} else {
		if _, err := q.Exec(ctx, `UPDATE custom_fields SET name = ?, description = ?, field_format = ?, regexp = ?, min_length = ?,
  max_length = ?, is_required = ?, is_for_all = ?, is_filter = ?, searchable = ?, default_value = ?, editable = ?, visible = ?,
  multiple = ?, position = ?, possible_values = ?, format_settings = ? WHERE id = ?`, append(args, cf.ID)...); err != nil {
			return err
		}
	}
	if err := replaceLinks(ctx, q, "custom_fields_roles", "role_id", "roles", cf.ID, cf.RoleIDs); err != nil {
		return err
	}
	if cf.OwnerKind == KindIssue {
		if err := replaceLinks(ctx, q, "custom_fields_trackers", "tracker_id", "trackers", cf.ID, cf.TrackerIDs); err != nil {
			return err
		}
		if err := replaceLinks(ctx, q, "custom_fields_projects", "project_id", "projects", cf.ID, cf.ProjectIDs); err != nil {
			return err
		}
	}
	return nil
}

// replaceLinks は habtm の関連を置き換える（存在しない相手の id は無視する。Rails の *_ids= は未発見で例外になるが、
// 画面からは存在する id しか送られない）。
func replaceLinks(ctx context.Context, q db.Queryer, table, col, refTable string, cfID int64, ids []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM `+table+` WHERE custom_field_id = ?`, cfID); err != nil {
		return err
	}
	for _, id := range uniqIDs(ids) {
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM `+refTable+` WHERE id = ?`, id); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if _, err := q.Exec(ctx, `INSERT INTO `+table+` (custom_field_id, `+col+`) VALUES (?, ?)`, cfID, id); err != nil {
			return err
		}
	}
	return nil
}

// MaxPosition は同じ owner_kind 内の position の最大値（acts_as_positioned の末尾挿入用）。
func MaxPosition(ctx context.Context, q db.Queryer, kind OwnerKind) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COALESCE(MAX(position), 0) FROM custom_fields WHERE owner_kind = ?`, string(kind))
	return n, err
}

// InsertPosition は acts_as_positioned#insert_position（同じ種類で position 以降を 1 つ後ろへ）。
func InsertPosition(ctx context.Context, q db.Queryer, kind OwnerKind, position int, id int64) error {
	_, err := q.Exec(ctx, `UPDATE custom_fields SET position = position + 1 WHERE owner_kind = ? AND position >= ? AND id <> ?`, string(kind), position, id)
	return err
}

// RemovePosition は acts_as_positioned#remove_position（以前の position 以降を 1 つ前へ）。
func RemovePosition(ctx context.Context, q db.Queryer, kind OwnerKind, previous int, id int64) error {
	_, err := q.Exec(ctx, `UPDATE custom_fields SET position = position - 1 WHERE owner_kind = ? AND position >= ? AND id <> ?`, string(kind), previous, id)
	return err
}

// ShiftPositions は acts_as_positioned#shift_positions（[min, max] の他の行を offset ずらし、
// 更新件数が max - min でなければ reset_positions_in_list で 1 から振り直す）。
func ShiftPositions(ctx context.Context, q db.Queryer, kind OwnerKind, id int64, from, to int) error {
	var offset int
	if from > to {
		offset = 1
	} else if from < to {
		offset = -1
	} else {
		offset = 0
	}
	lo, hi := min(from, to), max(from, to)
	res, err := q.Exec(ctx, `UPDATE custom_fields SET position = position + ? WHERE owner_kind = ? AND id <> ? AND position BETWEEN ? AND ?`,
		offset, string(kind), id, lo, hi)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if int(n) != hi-lo {
		var ids []int64
		if err := q.Select(ctx, &ids, `SELECT id FROM custom_fields WHERE owner_kind = ? ORDER BY position, id`, string(kind)); err != nil {
			return err
		}
		for i, rid := range ids {
			if _, err := q.Exec(ctx, `UPDATE custom_fields SET position = ? WHERE id = ?`, i+1, rid); err != nil {
				return err
			}
		}
	}
	return nil
}

// Delete はカスタムフィールドと値を削除する（has_many :custom_values, :dependent => :delete_all。
// 選択肢・関連・ワークフロー規則は FK の CASCADE で消える）。
func Delete(ctx context.Context, q db.Queryer, id int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM custom_values WHERE custom_field_id = ?`, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM custom_fields WHERE id = ?`, id)
	return err
}

// DeleteDuplicateValues は CustomField#handle_multiplicity_change（複数値をやめたとき、
// 同じ対象の値のうち id が最大のもの以外を削除する）。
func DeleteDuplicateValues(ctx context.Context, q db.Queryer, cfID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM custom_values WHERE custom_field_id = ? AND EXISTS (SELECT 1 FROM custom_values cve
  WHERE cve.custom_field_id = custom_values.custom_field_id AND cve.customized_kind = custom_values.customized_kind
  AND cve.customized_id = custom_values.customized_id AND cve.id > custom_values.id)`, cfID)
	return err
}

// ---------------------------------------------------------------- 選択肢（custom_field_enumerations）

type cfEnumRow struct {
	ID            int64  `db:"id"`
	CustomFieldID int64  `db:"custom_field_id"`
	Name          string `db:"name"`
	Active        bool   `db:"active"`
	Position      int    `db:"position"`
}

// Enumerations は custom_field.enumerations（position 順）。activeOnly なら .active。
func Enumerations(ctx context.Context, q db.Queryer, cfID int64, activeOnly bool) ([]*Enumeration, error) {
	where := `custom_field_id = ?`
	if activeOnly {
		where += ` AND active = ` + q.Dialect().BoolLiteral(true)
	}
	var rows []cfEnumRow
	if err := q.Select(ctx, &rows, `SELECT id, custom_field_id, name, active, position FROM custom_field_enumerations WHERE `+where+` ORDER BY position, id`, cfID); err != nil {
		return nil, err
	}
	out := make([]*Enumeration, len(rows))
	for i, r := range rows {
		out[i] = &Enumeration{ID: r.ID, CustomFieldID: r.CustomFieldID, Name: r.Name, Active: r.Active, Position: r.Position}
	}
	return out, nil
}

// CreateEnumeration は選択肢を作成する（before_create :set_position で末尾）。
func CreateEnumeration(ctx context.Context, q db.Queryer, e *Enumeration) error {
	if err := q.Get(ctx, &e.Position, `SELECT COALESCE(MAX(position), 0) + 1 FROM custom_field_enumerations WHERE custom_field_id = ?`, e.CustomFieldID); err != nil {
		return err
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO custom_field_enumerations (custom_field_id, name, active, position) VALUES (?, ?, ?, ?)`,
		e.CustomFieldID, e.Name, e.Active, e.Position)
	e.ID = id
	return err
}

// UpdateEnumeration は選択肢の名前・有効・位置を更新する。
func UpdateEnumeration(ctx context.Context, q db.Queryer, e *Enumeration) error {
	_, err := q.Exec(ctx, `UPDATE custom_field_enumerations SET name = ?, active = ?, position = ? WHERE id = ?`, e.Name, e.Active, e.Position, e.ID)
	return err
}

// EnumerationObjectsCount は CustomFieldEnumeration#objects_count。
func EnumerationObjectsCount(ctx context.Context, q db.Queryer, e *Enumeration) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM custom_values WHERE custom_field_id = ? AND value = ?`, e.CustomFieldID, itoa64(e.ID))
	return n, err
}

// DestroyEnumeration は CustomFieldEnumeration#destroy(reassign_to)。
func DestroyEnumeration(ctx context.Context, q db.Queryer, e *Enumeration, reassignTo *Enumeration) error {
	if reassignTo != nil {
		if _, err := q.Exec(ctx, `UPDATE custom_values SET value = ? WHERE custom_field_id = ? AND value = ?`,
			itoa64(reassignTo.ID), e.CustomFieldID, itoa64(e.ID)); err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `DELETE FROM custom_field_enumerations WHERE id = ?`, e.ID)
	return err
}

func itoa64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func uniqIDs(ids []int64) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
