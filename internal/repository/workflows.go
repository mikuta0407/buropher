package repository

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// WorkflowCountKey は WorkflowTransition.group(:tracker_id, :role_id).count のキー。
type WorkflowCountKey struct{ TrackerID, RoleID int64 }

// WorkflowTransitionCounts は WorkflowTransition.group(:tracker_id, :role_id).count。
func WorkflowTransitionCounts(ctx context.Context, q db.Queryer) (map[WorkflowCountKey]int, error) {
	var rows []struct {
		TrackerID int64 `db:"tracker_id"`
		RoleID    int64 `db:"role_id"`
		N         int   `db:"n"`
	}
	if err := q.Select(ctx, &rows, `SELECT tracker_id, role_id, COUNT(*) AS n FROM workflow_transitions GROUP BY tracker_id, role_id`); err != nil {
		return nil, err
	}
	out := make(map[WorkflowCountKey]int, len(rows))
	for _, r := range rows {
		out[WorkflowCountKey{r.TrackerID, r.RoleID}] = r.N
	}
	return out, nil
}

type workflowTransitionRow struct {
	ID          int64         `db:"id"`
	TrackerID   int64         `db:"tracker_id"`
	RoleID      int64         `db:"role_id"`
	OldStatusID sql.NullInt64 `db:"old_status_id"`
	NewStatusID int64         `db:"new_status_id"`
	Author      bool          `db:"author"`
	Assignee    bool          `db:"assignee"`
}

// WorkflowTransitions は WorkflowTransition.where(:tracker_id => trackerIDs, :role_id => roleIDs)（id 順）。
func WorkflowTransitions(ctx context.Context, q db.Queryer, trackerIDs, roleIDs []int64) ([]*domain.WorkflowTransition, error) {
	if len(trackerIDs) == 0 || len(roleIDs) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`SELECT id, tracker_id, role_id, old_status_id, new_status_id, author, assignee
  FROM workflow_transitions WHERE tracker_id IN (?) AND role_id IN (?) ORDER BY id`, trackerIDs, roleIDs)
	if err != nil {
		return nil, err
	}
	var rows []workflowTransitionRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.WorkflowTransition, len(rows))
	for i, r := range rows {
		out[i] = &domain.WorkflowTransition{ID: r.ID, TrackerID: r.TrackerID, RoleID: r.RoleID,
			OldStatusID: r.OldStatusID.Int64, NewStatusID: r.NewStatusID, Author: r.Author, Assignee: r.Assignee}
	}
	return out, nil
}

// WorkflowUsedStatusIDs は WorkflowsController#find_statuses の
// WorkflowTransition.where(tracker_id, role_id).where('old_status_id <> new_status_id').pluck(:old_status_id, :new_status_id)
// （新規チケットの遷移は Redmine では old_status_id = 0 として比較されるため COALESCE で揃える）。
func WorkflowUsedStatusIDs(ctx context.Context, q db.Queryer, trackerIDs, roleIDs []int64) ([]int64, error) {
	if len(trackerIDs) == 0 || len(roleIDs) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`SELECT DISTINCT COALESCE(old_status_id, 0) AS o, new_status_id AS n FROM workflow_transitions
  WHERE tracker_id IN (?) AND role_id IN (?) AND COALESCE(old_status_id, 0) <> new_status_id`, trackerIDs, roleIDs)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		O int64 `db:"o"`
		N int64 `db:"n"`
	}
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	var out []int64
	for _, r := range rows {
		for _, id := range []int64{r.O, r.N} {
			if id != 0 && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// TransitionChange は replace_transitions に渡す 1 件（params[:transitions][old][new][rule] = transition）。
type TransitionChange struct {
	OldStatusID int64
	NewStatusID int64
	// Rule は "always" / "author" / "assignee"。
	Rule string
	// Enabled は transition が "1"（true）なら true。
	Enabled bool
}

type wfRecord struct {
	domain.WorkflowTransition
	destroyed bool
}

// ReplaceWorkflowTransitions は WorkflowTransition.replace_transitions(trackers, roles, transitions)。
// changes は params の順序で並べること（同じキーの扱いが Redmine と同じ順序になる）。
func ReplaceWorkflowTransitions(ctx context.Context, q db.Queryer, trackerIDs, roleIDs []int64, changes []TransitionChange) error {
	cur, err := WorkflowTransitions(ctx, q, trackerIDs, roleIDs)
	if err != nil {
		return err
	}
	records := make([]*wfRecord, len(cur))
	for i, w := range cur {
		records[i] = &wfRecord{WorkflowTransition: *w}
	}
	destroy := func(r *wfRecord) error {
		r.destroyed = true
		if r.ID == 0 {
			return nil
		}
		_, err := q.Exec(ctx, `DELETE FROM workflow_transitions WHERE id = ?`, r.ID)
		return err
	}
	save := func(r *wfRecord) error {
		if r.ID == 0 {
			id, err := q.InsertReturningID(ctx, `INSERT INTO workflow_transitions (tracker_id, role_id, old_status_id, new_status_id, author, assignee)
  VALUES (?, ?, ?, ?, ?, ?)`, r.TrackerID, r.RoleID, nullIfZeroID(r.OldStatusID), r.NewStatusID, r.Author, r.Assignee)
			r.ID = id
			return err
		}
		_, err := q.Exec(ctx, `UPDATE workflow_transitions SET author = ?, assignee = ? WHERE id = ?`, r.Author, r.Assignee, r.ID)
		return err
	}
	statusExists := map[int64]bool{}
	exists := func(id int64) (bool, error) {
		if v, ok := statusExists[id]; ok {
			return v, nil
		}
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM issue_statuses WHERE id = ?`, id); err != nil {
			return false, err
		}
		statusExists[id] = n > 0
		return n > 0, nil
	}
	for _, ch := range changes {
		for _, tr := range trackerIDs {
			for _, role := range roleIDs {
				var w []*wfRecord
				for _, r := range records {
					if r.OldStatusID == ch.OldStatusID && r.NewStatusID == ch.NewStatusID &&
						r.TrackerID == tr && r.RoleID == role && !r.destroyed {
						if ch.Rule == "always" {
							if !r.Author && !r.Assignee {
								w = append(w, r)
							}
						} else if r.Author || r.Assignee {
							w = append(w, r)
						}
					}
				}
				if len(w) > 1 {
					for _, r := range w[1:] {
						if err := destroy(r); err != nil {
							return err
						}
					}
				}
				var rec *wfRecord
				if len(w) > 0 {
					rec = w[0]
				}
				if ch.Enabled {
					changed := false
					if rec == nil {
						// validates_presence_of :new_status（存在しないステータスへの遷移は保存されない）
						ok, err := exists(ch.NewStatusID)
						if err != nil {
							return err
						}
						if ch.OldStatusID != 0 {
							okOld, err := exists(ch.OldStatusID)
							if err != nil {
								return err
							}
							ok = ok && okOld
						}
						if !ok {
							continue
						}
						rec = &wfRecord{WorkflowTransition: domain.WorkflowTransition{
							OldStatusID: ch.OldStatusID, NewStatusID: ch.NewStatusID, TrackerID: tr, RoleID: role}}
						records = append(records, rec)
						changed = true
					}
					if ch.Rule == "author" && !rec.Author {
						rec.Author = true
						changed = true
					}
					if ch.Rule == "assignee" && !rec.Assignee {
						rec.Assignee = true
						changed = true
					}
					if changed {
						if err := save(rec); err != nil {
							return err
						}
					}
				} else if rec != nil {
					switch ch.Rule {
					case "always":
						if err := destroy(rec); err != nil {
							return err
						}
					case "author":
						if rec.Assignee {
							if rec.Author {
								rec.Author = false
								if err := save(rec); err != nil {
									return err
								}
							}
						} else if err := destroy(rec); err != nil {
							return err
						}
					case "assignee":
						if rec.Author {
							if rec.Assignee {
								rec.Assignee = false
								if err := save(rec); err != nil {
									return err
								}
							}
						} else if err := destroy(rec); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func nullIfZeroID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

type workflowFieldRuleRow struct {
	ID            int64          `db:"id"`
	TrackerID     int64          `db:"tracker_id"`
	RoleID        int64          `db:"role_id"`
	StatusID      int64          `db:"status_id"`
	CoreField     sql.NullString `db:"core_field"`
	CustomFieldID sql.NullInt64  `db:"custom_field_id"`
	Rule          string         `db:"rule"`
}

func (r workflowFieldRuleRow) rule() *domain.WorkflowFieldRule {
	name := r.CoreField.String
	if r.CustomFieldID.Valid {
		name = strconv.FormatInt(r.CustomFieldID.Int64, 10)
	}
	return &domain.WorkflowFieldRule{ID: r.ID, TrackerID: r.TrackerID, RoleID: r.RoleID, StatusID: r.StatusID, FieldName: name, Rule: r.Rule}
}

// WorkflowFieldRules は WorkflowPermission.where(:tracker_id => trackerIDs, :role_id => roleIDs)（id 順）。
func WorkflowFieldRules(ctx context.Context, q db.Queryer, trackerIDs, roleIDs []int64) ([]*domain.WorkflowFieldRule, error) {
	if len(trackerIDs) == 0 || len(roleIDs) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`SELECT id, tracker_id, role_id, status_id, core_field, custom_field_id, rule
  FROM workflow_field_rules WHERE tracker_id IN (?) AND role_id IN (?) ORDER BY id`, trackerIDs, roleIDs)
	if err != nil {
		return nil, err
	}
	var rows []workflowFieldRuleRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.WorkflowFieldRule, len(rows))
	for i, r := range rows {
		out[i] = r.rule()
	}
	return out, nil
}

// WorkflowRulesByStatusID は WorkflowPermission.rules_by_status_id(trackers, roles):
// status_id → field_name → rule の配列（id 順）。
func WorkflowRulesByStatusID(ctx context.Context, q db.Queryer, trackerIDs, roleIDs []int64) (map[int64]map[string][]string, error) {
	rules, err := WorkflowFieldRules(ctx, q, trackerIDs, roleIDs)
	if err != nil {
		return nil, err
	}
	out := map[int64]map[string][]string{}
	for _, r := range rules {
		if out[r.StatusID] == nil {
			out[r.StatusID] = map[string][]string{}
		}
		out[r.StatusID][r.FieldName] = append(out[r.StatusID][r.FieldName], r.Rule)
	}
	return out, nil
}

// PermissionChange は replace_permissions に渡す 1 件（params[:permissions][status_id][field] = rule）。
type PermissionChange struct {
	StatusID string
	Field    string
	Rule     string
}

var digitsRe = regexp.MustCompile(`^\d+$`)

// ReplaceWorkflowPermissions は WorkflowPermission.replace_permissions(trackers, roles, permissions)。
// 既存の規則を削除し、rule が空でなければ全トラッカー × ロールに作成する。
// 検証（rule が readonly / required、ステータスが存在、field_name が標準フィールドか数字）に
// 失敗した行は Redmine の create と同じく黙って保存しない。
func ReplaceWorkflowPermissions(ctx context.Context, q db.Queryer, trackerIDs, roleIDs []int64, changes []PermissionChange) error {
	if len(trackerIDs) == 0 || len(roleIDs) == 0 {
		return nil
	}
	for _, ch := range changes {
		statusID, err := strconv.ParseInt(ch.StatusID, 10, 64)
		if err != nil {
			// Ruby の where(:old_status_id => "x") は 0 等にキャストされ何も一致しない
			continue
		}
		var core, cf any
		var cond string
		var condArg any
		switch {
		case digitsRe.MatchString(ch.Field):
			n, _ := strconv.ParseInt(ch.Field, 10, 64)
			cf, cond, condArg = n, "custom_field_id = ?", n
		case slices.Contains(domain.TrackerCoreFieldsAll, ch.Field):
			core, cond, condArg = ch.Field, "core_field = ?", ch.Field
		default:
			// 不正な field_name は保存されない（既存の行もない）
			continue
		}
		query, args, err := db.In(`DELETE FROM workflow_field_rules WHERE tracker_id IN (?) AND role_id IN (?) AND status_id = ? AND `+cond,
			trackerIDs, roleIDs, statusID, condArg)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, query, args...); err != nil {
			return err
		}
		if ch.Rule != domain.WorkflowRuleReadonly && ch.Rule != domain.WorkflowRuleRequired {
			// rule.present? でなければ削除のみ。不正な rule は validates_inclusion_of で保存されない。
			continue
		}
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM issue_statuses WHERE id = ?`, statusID); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if cf != nil {
			if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM custom_fields WHERE id = ?`, cf); err != nil {
				return err
			}
			if n == 0 {
				// TODO(schema): Redmine は存在しないカスタムフィールド ID も保存するが、FK のため保存しない
				continue
			}
		}
		for _, tr := range trackerIDs {
			for _, role := range roleIDs {
				if _, err := q.Exec(ctx, `INSERT INTO workflow_field_rules (tracker_id, role_id, status_id, core_field, custom_field_id, rule)
  VALUES (?, ?, ?, ?, ?, ?)`, tr, role, statusID, core, cf, ch.Rule); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CopyWorkflow は WorkflowRule.copy_one(source_tracker, source_role, target_tracker, target_role):
// 対象の遷移・フィールド規則を削除し、コピー元の行を複製する。コピー元と対象が同じなら何もしない。
func CopyWorkflow(ctx context.Context, q db.Queryer, srcTracker, srcRole, dstTracker, dstRole int64) error {
	if srcTracker == dstTracker && srcRole == dstRole {
		return nil
	}
	for _, t := range []string{"workflow_transitions", "workflow_field_rules"} {
		if _, err := q.Exec(ctx, `DELETE FROM `+t+` WHERE tracker_id = ? AND role_id = ?`, dstTracker, dstRole); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `INSERT INTO workflow_transitions (tracker_id, role_id, old_status_id, new_status_id, author, assignee)
  SELECT ?, ?, old_status_id, new_status_id, author, assignee FROM workflow_transitions WHERE tracker_id = ? AND role_id = ? ORDER BY id`,
		dstTracker, dstRole, srcTracker, srcRole); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `INSERT INTO workflow_field_rules (tracker_id, role_id, status_id, core_field, custom_field_id, rule)
  SELECT ?, ?, status_id, core_field, custom_field_id, rule FROM workflow_field_rules WHERE tracker_id = ? AND role_id = ? ORDER BY id`,
		dstTracker, dstRole, srcTracker, srcRole)
	return err
}

// TrackersByIDs は Tracker.where(:id => ids)（position 順。存在しない id は無視）。
func TrackersByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.Tracker, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`SELECT `+trackerCols+` FROM trackers WHERE id IN (?) ORDER BY position, id`, uniqIDs(ids))
	if err != nil {
		return nil, err
	}
	var rows []trackerRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Tracker, len(rows))
	for i := range rows {
		out[i] = rows[i].tracker()
	}
	return out, nil
}

// IssueStatusesByIDs は IssueStatus.where(:id => ids).sorted。
func IssueStatusesByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.IssueStatus, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`SELECT `+issueStatusCols+` FROM issue_statuses WHERE id IN (?) ORDER BY position, id`, uniqIDs(ids))
	if err != nil {
		return nil, err
	}
	var rows []issueStatusRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.IssueStatus, len(rows))
	for i := range rows {
		out[i] = rows[i].status()
	}
	return out, nil
}
