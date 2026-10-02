package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type trackerRow struct {
	ID                 int64          `db:"id"`
	Name               string         `db:"name"`
	Description        sql.NullString `db:"description"`
	Position           int            `db:"position"`
	IsInRoadmap        bool           `db:"is_in_roadmap"`
	DefaultStatusID    int64          `db:"default_status_id"`
	DisabledCoreFields string         `db:"disabled_core_fields"`
}

func (r *trackerRow) tracker() *domain.Tracker {
	t := &domain.Tracker{ID: r.ID, Name: r.Name, Position: r.Position, IsInRoadmap: r.IsInRoadmap,
		DefaultStatusID: r.DefaultStatusID, DisabledCoreFields: []string{}}
	if r.Description.Valid {
		s := r.Description.String
		t.Description = &s
	}
	_ = json.Unmarshal([]byte(r.DisabledCoreFields), &t.DisabledCoreFields)
	return t
}

// TrackerPositionScope は Tracker の acts_as_positioned（スコープなし）。
var TrackerPositionScope = PositionScope{Table: "trackers"}

const trackerCols = `id, name, description, position, is_in_roadmap, default_status_id, disabled_core_fields`

// ListTrackers は Tracker.sorted（position 順）を返す。
func ListTrackers(ctx context.Context, q db.Queryer) ([]*domain.Tracker, error) {
	var rows []trackerRow
	if err := q.Select(ctx, &rows, `SELECT `+trackerCols+` FROM trackers ORDER BY position, id`); err != nil {
		return nil, err
	}
	out := make([]*domain.Tracker, len(rows))
	for i := range rows {
		out[i] = rows[i].tracker()
	}
	return out, nil
}

// GetTracker は id のトラッカーを project_ids / custom_field_ids 付きで返す。
func GetTracker(ctx context.Context, q db.Queryer, id int64) (*domain.Tracker, error) {
	var r trackerRow
	if err := q.Get(ctx, &r, `SELECT `+trackerCols+` FROM trackers WHERE id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t := r.tracker()
	if err := q.Select(ctx, &t.ProjectIDs, `SELECT project_id FROM project_trackers WHERE tracker_id = ? ORDER BY project_id`, id); err != nil {
		return nil, err
	}
	ids, err := TrackerCustomFieldIDs(ctx, q, id)
	if err != nil {
		return nil, err
	}
	t.CustomFieldIDs = ids
	return t, nil
}

// TrackerCustomFieldIDs は Tracker#custom_field_ids（IssueCustomField のみ）。
func TrackerCustomFieldIDs(ctx context.Context, q db.Queryer, trackerID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT cft.custom_field_id FROM custom_fields_trackers cft
  JOIN custom_fields cf ON cf.id = cft.custom_field_id AND cf.owner_kind = 'issue'
  WHERE cft.tracker_id = ? ORDER BY cft.custom_field_id`, trackerID)
	return ids, err
}

// TrackerNameTaken は validates_uniqueness_of :name（大文字小文字を区別）。excludeID は自分自身。
func TrackerNameTaken(ctx context.Context, q db.Queryer, name string, excludeID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM trackers WHERE name = ? AND id <> ?`, name, excludeID)
	return n > 0, err
}

// SaveTracker はトラッカーの行を保存する（t.ID == 0 なら作成して id を設定する）。
// position の調整（acts_as_positioned）は呼び出し側で行う。
func SaveTracker(ctx context.Context, q db.Queryer, t *domain.Tracker) error {
	if t.DisabledCoreFields == nil {
		t.DisabledCoreFields = []string{}
	}
	dj, err := json.Marshal(t.DisabledCoreFields)
	if err != nil {
		return err
	}
	if t.ID == 0 {
		id, err := q.InsertReturningID(ctx, `INSERT INTO trackers (name, description, position, is_in_roadmap, default_status_id, disabled_core_fields)
  VALUES (?, ?, ?, ?, ?, ?)`, t.Name, t.Description, t.Position, t.IsInRoadmap, t.DefaultStatusID, string(dj))
		if err != nil {
			return err
		}
		t.ID = id
		return nil
	}
	_, err = q.Exec(ctx, `UPDATE trackers SET name = ?, description = ?, position = ?, is_in_roadmap = ?, default_status_id = ?,
  disabled_core_fields = ? WHERE id = ?`, t.Name, t.Description, t.Position, t.IsInRoadmap, t.DefaultStatusID, string(dj), t.ID)
	return err
}

// SetTrackerProjects は Tracker#project_ids=。
func SetTrackerProjects(ctx context.Context, q db.Queryer, trackerID int64, projectIDs []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM project_trackers WHERE tracker_id = ?`, trackerID); err != nil {
		return err
	}
	for _, p := range uniqIDs(projectIDs) {
		if _, err := q.Exec(ctx, `INSERT INTO project_trackers (project_id, tracker_id)
  SELECT id, ? FROM projects WHERE id = ?`, trackerID, p); err != nil {
			return err
		}
	}
	return nil
}

// SetTrackerCustomFields は Tracker#custom_field_ids=（IssueCustomField のみ対象）。
func SetTrackerCustomFields(ctx context.Context, q db.Queryer, trackerID int64, cfIDs []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM custom_fields_trackers WHERE tracker_id = ?
  AND custom_field_id IN (SELECT id FROM custom_fields WHERE owner_kind = 'issue')`, trackerID); err != nil {
		return err
	}
	for _, c := range uniqIDs(cfIDs) {
		if _, err := q.Exec(ctx, `INSERT INTO custom_fields_trackers (custom_field_id, tracker_id)
  SELECT id, ? FROM custom_fields WHERE id = ? AND owner_kind = 'issue'`, trackerID, c); err != nil {
			return err
		}
	}
	return nil
}

// TrackerIssuesExist は tracker.issues.any?。
func TrackerIssuesExist(ctx context.Context, q db.Queryer, trackerID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM issues WHERE tracker_id = ?`, trackerID)
	return n > 0, err
}

// ProjectsWithTrackerIssues は Project.joins(:issues).where(issues: {tracker_id:}).sorted.distinct。
func ProjectsWithTrackerIssues(ctx context.Context, q db.Queryer, trackerID int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.id IN (SELECT project_id FROM issues WHERE tracker_id = ?)`, trackerID)
}

// DestroyTracker はトラッカーを削除し、後ろのトラッカーの position を詰める
// （workflow_rules / project_trackers / custom_fields_trackers は FK の CASCADE で消える）。
func DestroyTracker(ctx context.Context, q db.Queryer, t *domain.Tracker) error {
	if _, err := q.Exec(ctx, `DELETE FROM trackers WHERE id = ?`, t.ID); err != nil {
		return err
	}
	return RemovePosition(ctx, q, TrackerPositionScope, t.ID, t.Position)
}

// TrackerIDsWithWorkflow はワークフロー（遷移・フィールド権限）を持つトラッカーの id 集合
// （tracker.workflow_rules.exists?）。
func TrackerIDsWithWorkflow(ctx context.Context, q db.Queryer) (map[int64]bool, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT DISTINCT tracker_id FROM workflow_transitions
  UNION SELECT DISTINCT tracker_id FROM workflow_field_rules`); err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// RoleIDsWithWorkflow はワークフローを持つロールの id 集合（role.workflow_rules.exists?）。
func RoleIDsWithWorkflow(ctx context.Context, q db.Queryer) (map[int64]bool, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT DISTINCT role_id FROM workflow_transitions
  UNION SELECT DISTINCT role_id FROM workflow_field_rules`); err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// StatusIDsInWorkflow は遷移の old/new に使われているステータスの id 集合
// （WorkflowTransition.where('old_status_id = ? OR new_status_id = ?').exists?）。
func StatusIDsInWorkflow(ctx context.Context, q db.Queryer) (map[int64]bool, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT DISTINCT old_status_id FROM workflow_transitions WHERE old_status_id IS NOT NULL
  UNION SELECT DISTINCT new_status_id FROM workflow_transitions`); err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// CopyWorkflowRules は WorkflowRule.copy(source_tracker, source_role, target_trackers, target_roles)。
// srcTracker / srcRole の nil は「対象と同じ」、dstTrackers が空なら全トラッカー、
// dstRoles が空ならワークフローを考慮するロール（Role#consider_workflow?）すべて。
// 対象ごとに既存のルールを削除してから元のルールを複製する（WorkflowRule.copy_one）。
func CopyWorkflowRules(ctx context.Context, q db.Queryer, srcTracker, srcRole *int64, dstTrackers, dstRoles []int64) error {
	if srcTracker == nil && srcRole == nil {
		return errors.New("repository: source_tracker or source_role must be specified")
	}
	if len(dstTrackers) == 0 {
		if err := q.Select(ctx, &dstTrackers, `SELECT id FROM trackers ORDER BY position, id`); err != nil {
			return err
		}
	}
	if len(dstRoles) == 0 {
		if err := q.Select(ctx, &dstRoles, `SELECT id FROM roles WHERE id IN
  (SELECT role_id FROM role_permissions WHERE permission IN ('add_issues', 'edit_issues')) ORDER BY id`); err != nil {
			return err
		}
	}
	for _, dt := range dstTrackers {
		for _, dr := range dstRoles {
			st, sr := dt, dr
			if srcTracker != nil {
				st = *srcTracker
			}
			if srcRole != nil {
				sr = *srcRole
			}
			if st == dt && sr == dr {
				continue
			}
			if _, err := q.Exec(ctx, `DELETE FROM workflow_transitions WHERE tracker_id = ? AND role_id = ?`, dt, dr); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `DELETE FROM workflow_field_rules WHERE tracker_id = ? AND role_id = ?`, dt, dr); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `INSERT INTO workflow_transitions (tracker_id, role_id, old_status_id, new_status_id, author, assignee)
  SELECT ?, ?, old_status_id, new_status_id, author, assignee FROM workflow_transitions WHERE tracker_id = ? AND role_id = ? ORDER BY id`,
				dt, dr, st, sr); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `INSERT INTO workflow_field_rules (tracker_id, role_id, status_id, core_field, custom_field_id, rule)
  SELECT ?, ?, status_id, core_field, custom_field_id, rule FROM workflow_field_rules WHERE tracker_id = ? AND role_id = ? ORDER BY id`,
				dt, dr, st, sr); err != nil {
				return err
			}
		}
	}
	return nil
}
