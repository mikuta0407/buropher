package repository

// ガントチャート（Redmine::Helpers::Gantt）が読む値。

import (
	"context"
	"strconv"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// GanttProjects は ids のプロジェクトとその祖先のうち visibleCond（Project.visible の条件。projects を参照）に
// 合うもの（Gantt#projects。並びはツリー順）。
func GanttProjects(ctx context.Context, q db.Queryer, ids []int64, visibleCond string) ([]*domain.Project, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	where := "projects.id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id IN (" + idsCSV(ids) + "))"
	if visibleCond != "" {
		where += " AND (" + visibleCond + ")"
	}
	return LoadProjects(ctx, q, where)
}

// GanttRelations は ids どうしの blocks / precedes の関連（IssueRelation.where(...)。id 順）。
func GanttRelations(ctx context.Context, q db.Queryer, ids []int64) ([]*IssueRelation, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	in := idsCSV(ids)
	var rows []*IssueRelation
	err := q.Select(ctx, &rows, `SELECT id, issue_from_id, issue_to_id, relation_type, delay FROM issue_relations
WHERE issue_from_id IN (`+in+`) AND issue_to_id IN (`+in+`) AND relation_type IN ('blocks', 'precedes') ORDER BY id`)
	return rows, err
}

// GanttProjectDates は Project#start_date / Project#due_date（チケット・共有バージョン・共有バージョンのチケットの最小 / 最大）。
func GanttProjectDates(ctx context.Context, q db.Queryer, p *domain.Project) (start, due *time.Time, err error) {
	shared := `SELECT versions.id FROM versions JOIN projects ON projects.id = versions.project_id WHERE ` + SharedVersionsCondition(p)
	id := strconv.FormatInt(p.ID, 10)
	var r struct {
		S1 db.NullDate `db:"s1"`
		S2 db.NullDate `db:"s2"`
		S3 db.NullDate `db:"s3"`
		D1 db.NullDate `db:"d1"`
		D2 db.NullDate `db:"d2"`
		D3 db.NullDate `db:"d3"`
	}
	err = q.Get(ctx, &r, `SELECT
  (SELECT MIN(start_date) FROM issues WHERE project_id = `+id+`) AS s1,
  (SELECT MIN(effective_date) FROM versions WHERE id IN (`+shared+`)) AS s2,
  (SELECT MIN(start_date) FROM issues WHERE fixed_version_id IN (`+shared+`)) AS s3,
  (SELECT MAX(due_date) FROM issues WHERE project_id = `+id+`) AS d1,
  (SELECT MAX(effective_date) FROM versions WHERE id IN (`+shared+`)) AS d2,
  (SELECT MAX(due_date) FROM issues WHERE fixed_version_id IN (`+shared+`)) AS d3`)
	if err != nil {
		return nil, nil, err
	}
	pick := func(less bool, ds ...db.NullDate) *time.Time {
		var out *time.Time
		for _, d := range ds {
			if !d.Valid {
				continue
			}
			t := d.Date.Time
			if out == nil || (less && t.Before(*out)) || (!less && t.After(*out)) {
				out = &t
			}
		}
		return out
	}
	return pick(true, r.S1, r.S2, r.S3), pick(false, r.D1, r.D2, r.D3), nil
}

// ProjectHasIssuesOrVersions は project.issues.exists? || project.versions.exists?。
func ProjectHasIssuesOrVersions(ctx context.Context, q db.Queryer, projectID int64) (bool, error) {
	ok, err := exists(ctx, q, `SELECT 1 FROM issues WHERE project_id = ?`, projectID)
	if err != nil || ok {
		return ok, err
	}
	return exists(ctx, q, `SELECT 1 FROM versions WHERE project_id = ?`, projectID)
}

// VersionStartDate は Version#start_date（fixed_issues.minimum('start_date')）。
func VersionStartDate(ctx context.Context, q db.Queryer, versionID int64) (*time.Time, error) {
	var d db.NullDate
	if err := q.Get(ctx, &d, `SELECT MIN(start_date) FROM issues WHERE fixed_version_id = ?`, versionID); err != nil {
		return nil, err
	}
	if !d.Valid {
		return nil, nil
	}
	t := d.Date.Time
	return &t, nil
}

func idsCSV(ids []int64) string {
	b := make([]byte, 0, len(ids)*4)
	for i, id := range ids {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, id, 10)
	}
	return string(b)
}
