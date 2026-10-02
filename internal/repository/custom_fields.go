package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type customFieldRow struct {
	ID             int64          `db:"id"`
	OwnerKind      string         `db:"owner_kind"`
	Name           string         `db:"name"`
	Description    sql.NullString `db:"description"`
	FieldFormat    string         `db:"field_format"`
	Regexp         sql.NullString `db:"regexp"`
	MinLength      sql.NullInt64  `db:"min_length"`
	MaxLength      sql.NullInt64  `db:"max_length"`
	IsRequired     bool           `db:"is_required"`
	IsForAll       bool           `db:"is_for_all"`
	IsFilter       bool           `db:"is_filter"`
	Searchable     bool           `db:"searchable"`
	DefaultValue   sql.NullString `db:"default_value"`
	Editable       bool           `db:"editable"`
	Visible        bool           `db:"visible"`
	Multiple       bool           `db:"multiple"`
	Position       int            `db:"position"`
	PossibleValues sql.NullString `db:"possible_values"`
	FormatSettings sql.NullString `db:"format_settings"`
}

const customFieldCols = `id, owner_kind, name, description, field_format, regexp, min_length, max_length, is_required,
  is_for_all, is_filter, searchable, default_value, editable, visible, multiple, position, possible_values, format_settings`

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func nullIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func (r *customFieldRow) customField() *domain.CustomField {
	cf := &domain.CustomField{
		ID: r.ID, OwnerKind: r.OwnerKind, Name: r.Name, Description: nullStrPtr(r.Description),
		FieldFormat: r.FieldFormat, Regexp: nullStrPtr(r.Regexp), MinLength: nullIntPtr(r.MinLength),
		MaxLength: nullIntPtr(r.MaxLength), IsRequired: r.IsRequired, IsForAll: r.IsForAll, IsFilter: r.IsFilter,
		Searchable: r.Searchable, DefaultValue: nullStrPtr(r.DefaultValue), Editable: r.Editable,
		Visible: r.Visible, Multiple: r.Multiple, Position: r.Position,
	}
	if r.PossibleValues.Valid {
		_ = json.Unmarshal([]byte(r.PossibleValues.String), &cf.PossibleValues)
	}
	if r.FormatSettings.Valid {
		_ = json.Unmarshal([]byte(r.FormatSettings.String), &cf.FormatSettings)
	}
	if cf.PossibleValues == nil {
		cf.PossibleValues = []string{}
	}
	return cf
}

// LoadCustomFields は custom_fields を where 条件で読み込み、ロール・トラッカー・プロジェクトの関連を付ける。
// 並びは (owner_kind の CUSTOM_FIELDS_TABS 順ではなく) position, id。
func LoadCustomFields(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.CustomField, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []customFieldRow
	if err := q.Select(ctx, &rows, `SELECT `+customFieldCols+` FROM custom_fields WHERE `+where+` ORDER BY position, id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.CustomField, len(rows))
	byID := make(map[int64]*domain.CustomField, len(rows))
	ids := make([]int64, len(rows))
	for i := range rows {
		cf := rows[i].customField()
		out[i] = cf
		byID[cf.ID] = cf
		ids[i] = cf.ID
	}
	for _, chunk := range chunkIDs(ids) {
		for _, rel := range []struct {
			table, col string
			set        func(cf *domain.CustomField, id int64)
		}{
			{"custom_fields_roles", "role_id", func(cf *domain.CustomField, id int64) { cf.RoleIDs = append(cf.RoleIDs, id) }},
			{"custom_fields_trackers", "tracker_id", func(cf *domain.CustomField, id int64) { cf.TrackerIDs = append(cf.TrackerIDs, id) }},
			{"custom_fields_projects", "project_id", func(cf *domain.CustomField, id int64) { cf.ProjectIDs = append(cf.ProjectIDs, id) }},
		} {
			query, a, err := db.In(`SELECT custom_field_id AS cf, `+rel.col+` AS ref FROM `+rel.table+` WHERE custom_field_id IN (?) ORDER BY custom_field_id, `+rel.col, chunk)
			if err != nil {
				return nil, err
			}
			var links []struct {
				CF  int64 `db:"cf"`
				Ref int64 `db:"ref"`
			}
			if err := q.Select(ctx, &links, query, a...); err != nil {
				return nil, err
			}
			for _, l := range links {
				rel.set(byID[l.CF], l.Ref)
			}
		}
	}
	return out, nil
}

// ListCustomFields は CustomField.all（position, id 順）。
func ListCustomFields(ctx context.Context, q db.Queryer) ([]*domain.CustomField, error) {
	return LoadCustomFields(ctx, q, "")
}

// CustomFieldsByOwner は owner_kind のカスタムフィールド（position 順）。
func CustomFieldsByOwner(ctx context.Context, q db.Queryer, owner string) ([]*domain.CustomField, error) {
	return LoadCustomFields(ctx, q, `owner_kind = ?`, owner)
}

// GetCustomField は CustomField.find(id)。
func GetCustomField(ctx context.Context, q db.Queryer, id int64) (*domain.CustomField, error) {
	cfs, err := LoadCustomFields(ctx, q, `id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(cfs) == 0 {
		return nil, ErrNotFound
	}
	return cfs[0], nil
}

// TrackersCustomFields は trackers.map(&:custom_fields).flatten.uniq.sort（position 順）。
func TrackersCustomFields(ctx context.Context, q db.Queryer, trackerIDs []int64) ([]*domain.CustomField, error) {
	if len(trackerIDs) == 0 {
		return nil, nil
	}
	where, args, err := db.In(`owner_kind = 'issue' AND id IN (SELECT custom_field_id FROM custom_fields_trackers WHERE tracker_id IN (?))`, trackerIDs)
	if err != nil {
		return nil, err
	}
	return LoadCustomFields(ctx, q, where, args...)
}

// IssueCustomFieldProjectCounts は IssueCustomField.where(is_for_all: false).joins(:projects).group(:custom_field_id).count。
func IssueCustomFieldProjectCounts(ctx context.Context, q db.Queryer) (map[int64]int, error) {
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

// CustomFieldNameTaken は validates_uniqueness_of :name, :scope => :type（大文字小文字を区別）。
func CustomFieldNameTaken(ctx context.Context, q db.Queryer, owner, name string, exceptID int64) (bool, error) {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM custom_fields WHERE owner_kind = ? AND name = ? AND id <> ?`, owner, name, exceptID); err != nil {
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

// SaveCustomField はカスタムフィールドの行と関連（ロール・トラッカー・プロジェクト）を保存する。
// cf.ID == 0 なら作成して id を設定する。position の調整（acts_as_positioned）は呼び出し側で行う。
// IsIssue でない種類ではトラッカー・プロジェクトを書き換えない。
func SaveCustomField(ctx context.Context, q db.Queryer, cf *domain.CustomField) error {
	pv, err := jsonOrNil(cf.PossibleValues)
	if err != nil {
		return err
	}
	fsMap := cf.FormatSettings
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
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append([]any{cf.OwnerKind}, args...)...)
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
	if cf.OwnerKind == domain.CFOwnerIssue {
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

// MaxCustomFieldPosition は同じ owner_kind 内の position の最大値（acts_as_positioned の末尾挿入用）。
func MaxCustomFieldPosition(ctx context.Context, q db.Queryer, owner string) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COALESCE(MAX(position), 0) FROM custom_fields WHERE owner_kind = ?`, owner)
	return n, err
}

// ShiftCustomFieldPositions は acts_as_positioned の位置の詰め・空け:
// owner_kind 内で position が [from, to] の範囲の行を delta だけずらす（exceptID は除く）。
func ShiftCustomFieldPositions(ctx context.Context, q db.Queryer, owner string, from, to, delta int, exceptID int64) error {
	_, err := q.Exec(ctx, `UPDATE custom_fields SET position = position + ? WHERE owner_kind = ? AND position >= ? AND position <= ? AND id <> ?`,
		delta, owner, from, to, exceptID)
	return err
}

// DeleteCustomField はカスタムフィールドと値を削除する（has_many :custom_values, :dependent => :delete_all。
// 選択肢・関連・ワークフロー規則は FK の CASCADE で消える）。
func DeleteCustomField(ctx context.Context, q db.Queryer, id int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM custom_values WHERE custom_field_id = ?`, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM custom_fields WHERE id = ?`, id)
	return err
}

// DeleteDuplicateCustomValues は CustomField#handle_multiplicity_change（複数値をやめたとき、
// 同じ対象の値のうち id が最大のもの以外を削除する）。
func DeleteDuplicateCustomValues(ctx context.Context, q db.Queryer, cfID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM custom_values WHERE custom_field_id = ? AND EXISTS (SELECT 1 FROM custom_values cve
  WHERE cve.custom_field_id = custom_values.custom_field_id AND cve.customized_kind = custom_values.customized_kind
  AND cve.customized_id = custom_values.customized_id AND cve.id > custom_values.id)`, cfID)
	return err
}

// ---------------------------------------------------------------- custom_field_enumerations

type cfEnumRow struct {
	ID            int64  `db:"id"`
	CustomFieldID int64  `db:"custom_field_id"`
	Name          string `db:"name"`
	Active        bool   `db:"active"`
	Position      int    `db:"position"`
}

// CustomFieldEnumerations は custom_field.enumerations（position 順）。activeOnly なら .active。
func CustomFieldEnumerations(ctx context.Context, q db.Queryer, cfID int64, activeOnly bool) ([]*domain.CustomFieldEnumeration, error) {
	where := `custom_field_id = ?`
	if activeOnly {
		where += ` AND active = ` + q.Dialect().BoolLiteral(true)
	}
	var rows []cfEnumRow
	if err := q.Select(ctx, &rows, `SELECT id, custom_field_id, name, active, position FROM custom_field_enumerations WHERE `+where+` ORDER BY position, id`, cfID); err != nil {
		return nil, err
	}
	out := make([]*domain.CustomFieldEnumeration, len(rows))
	for i, r := range rows {
		out[i] = &domain.CustomFieldEnumeration{ID: r.ID, CustomFieldID: r.CustomFieldID, Name: r.Name, Active: r.Active, Position: r.Position}
	}
	return out, nil
}

// CreateCustomFieldEnumeration は選択肢を作成する（before_create :set_position で末尾）。
func CreateCustomFieldEnumeration(ctx context.Context, q db.Queryer, e *domain.CustomFieldEnumeration) error {
	if err := q.Get(ctx, &e.Position, `SELECT COALESCE(MAX(position), 0) + 1 FROM custom_field_enumerations WHERE custom_field_id = ?`, e.CustomFieldID); err != nil {
		return err
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO custom_field_enumerations (custom_field_id, name, active, position) VALUES (?, ?, ?, ?)`,
		e.CustomFieldID, e.Name, e.Active, e.Position)
	e.ID = id
	return err
}

// UpdateCustomFieldEnumeration は選択肢の名前・有効・位置を更新する。
func UpdateCustomFieldEnumeration(ctx context.Context, q db.Queryer, e *domain.CustomFieldEnumeration) error {
	_, err := q.Exec(ctx, `UPDATE custom_field_enumerations SET name = ?, active = ?, position = ? WHERE id = ?`, e.Name, e.Active, e.Position, e.ID)
	return err
}

// CustomFieldEnumerationObjectsCount は CustomFieldEnumeration#objects_count。
func CustomFieldEnumerationObjectsCount(ctx context.Context, q db.Queryer, e *domain.CustomFieldEnumeration) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM custom_values WHERE custom_field_id = ? AND value = ?`, e.CustomFieldID, itoa64(e.ID))
	return n, err
}

// DestroyCustomFieldEnumeration は CustomFieldEnumeration#destroy(reassign_to)。
func DestroyCustomFieldEnumeration(ctx context.Context, q db.Queryer, e *domain.CustomFieldEnumeration, reassignTo *domain.CustomFieldEnumeration) error {
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
