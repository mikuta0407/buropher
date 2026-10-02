package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはファイル（FilesController: プロジェクト・バージョンの添付）用の読み込み。

type versionRow struct {
	ID            int64          `db:"id"`
	ProjectID     int64          `db:"project_id"`
	Name          string         `db:"name"`
	Description   sql.NullString `db:"description"`
	EffectiveDate db.NullDate    `db:"effective_date"`
	WikiPageTitle sql.NullString `db:"wiki_page_title"`
	Status        string         `db:"status"`
	Sharing       string         `db:"sharing"`
	CreatedAt     db.Time        `db:"created_at"`
	UpdatedAt     db.Time        `db:"updated_at"`
}

func (r *versionRow) version() *domain.Version {
	v := &domain.Version{ID: r.ID, ProjectID: r.ProjectID, Name: r.Name, Status: r.Status, Sharing: r.Sharing,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if r.Description.Valid {
		s := r.Description.String
		v.Description = &s
	}
	if r.WikiPageTitle.Valid {
		s := r.WikiPageTitle.String
		v.WikiPageTitle = &s
	}
	if r.EffectiveDate.Valid {
		t := time.Date(r.EffectiveDate.Date.Year(), r.EffectiveDate.Date.Month(), r.EffectiveDate.Date.Day(), 0, 0, 0, 0, time.UTC)
		v.EffectiveDate = &t
	}
	return v
}

// ProjectOwnVersions は project.versions（自プロジェクトのバージョン。並びは id 順。
// 呼び出し側で domain.CompareVersions により並べる）。
func ProjectOwnVersions(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.Version, error) {
	var rows []versionRow
	if err := q.Select(ctx, &rows, `SELECT id, project_id, name, description, effective_date, wiki_page_title, status, sharing,
  created_at, updated_at FROM versions WHERE project_id = ? ORDER BY id`, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.Version, len(rows))
	for i := range rows {
		out[i] = rows[i].version()
	}
	return out, nil
}

// ProjectOwnVersion は @project.versions.find_by_id(id)。
func ProjectOwnVersion(ctx context.Context, q db.Queryer, projectID, id int64) (*domain.Version, error) {
	var r versionRow
	if err := q.Get(ctx, &r, `SELECT id, project_id, name, description, effective_date, wiki_page_title, status, sharing,
  created_at, updated_at FROM versions WHERE project_id = ? AND id = ?`, projectID, id); err != nil {
		return nil, notFound(err)
	}
	return r.version(), nil
}

// SortedContainerAttachments は container.attachments を order（検証済みの ORDER BY 断片の列）で並べて返す
// （FilesController#index の includes(:attachments).reorder(sort_clause)）。author を読み込む。
func SortedContainerAttachments(ctx context.Context, q db.Queryer, kind string, id int64, order []string) ([]*domain.Attachment, error) {
	ob := "attachments.created_at ASC, attachments.id ASC"
	if len(order) > 0 {
		ob = strings.Join(order, ", ") + ", attachments.id ASC"
	}
	var rows []attachmentRow
	if err := q.Select(ctx, &rows, attachmentSelect+` WHERE attachments.container_kind = ? AND attachments.container_id = ? ORDER BY `+ob, kind, id); err != nil {
		return nil, err
	}
	return preloadAttachmentAuthors(ctx, q, rows)
}
