package repository

import (
	"context"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルは工数レポート（TimeReport）の条件値の表示に使う関連の読み込み。

// TimelogNames は table（issue_statuses / trackers / issue_categories / time_entry_activities）の id → name。
func TimelogNames(ctx context.Context, q db.Queryer, table string, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	switch table {
	case "issue_statuses", "trackers", "issue_categories", "time_entry_activities":
	default:
		return out, nil
	}
	var rows []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, name FROM `+table+` WHERE id IN (`+int64List(ids)+`)`); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// TimelogVersionRef はバージョン（link_to_version 用）。
type TimelogVersionRef struct {
	ID          int64       `db:"id"`
	Name        string      `db:"name"`
	ProjectID   int64       `db:"project_id"`
	ProjectName string      `db:"project_name"`
	Date        db.NullDate `db:"effective_date"`
}

// TimelogVersions は id → バージョン。
func TimelogVersions(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*TimelogVersionRef, error) {
	out := map[int64]*TimelogVersionRef{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []TimelogVersionRef
	if err := q.Select(ctx, &rows, `SELECT versions.id, versions.name, versions.project_id, projects.name AS project_name, versions.effective_date
FROM versions JOIN projects ON projects.id = versions.project_id WHERE versions.id IN (`+int64List(ids)+`)`); err != nil {
		return nil, err
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

// TimelogReportCustomFieldIDs は TimeReport#load_available_criteria のカスタムフィールド
// （TimeEntryCustomField.visible + ProjectCustomField.visible + Issue（project nil なら for_all、
// それ以外は project.all_issue_custom_fields（sorted））.visible + TimeEntryActivityCustomField.visible）の id。
// visible は custom_fields を参照する可視条件。
func TimelogReportCustomFieldIDs(ctx context.Context, q db.Queryer, projectID *int64, visible string) ([]int64, error) {
	var out []int64
	add := func(where, order string, args ...any) error {
		var ids []int64
		if err := q.Select(ctx, &ids, `SELECT id FROM custom_fields WHERE `+where+` AND (`+visible+`) ORDER BY `+order, args...); err != nil {
			return err
		}
		out = append(out, ids...)
		return nil
	}
	if err := add(`owner_kind = 'time_entry'`, `id`); err != nil {
		return nil, err
	}
	if err := add(`owner_kind = 'project'`, `id`); err != nil {
		return nil, err
	}
	if projectID == nil {
		if err := add(`owner_kind = 'issue' AND is_for_all = ?`, `id`, true); err != nil {
			return nil, err
		}
	} else {
		if err := add(`owner_kind = 'issue' AND (is_for_all = ? OR id IN (SELECT custom_field_id FROM custom_fields_projects WHERE project_id = ?))`,
			`position, id`, true, *projectID); err != nil {
			return nil, err
		}
	}
	if err := add(`owner_kind = 'time_entry_activity'`, `id`); err != nil {
		return nil, err
	}
	return out, nil
}
