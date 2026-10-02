package repository

import (
	"context"
	"database/sql"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは文書（Document）の読み書き。

type documentRow struct {
	ID          int64          `db:"id"`
	ProjectID   int64          `db:"project_id"`
	CategoryID  int64          `db:"category_id"`
	Title       string         `db:"title"`
	Description sql.NullString `db:"description"`
	CreatedAt   db.Time        `db:"created_at"`
}

func (r *documentRow) document() *domain.Document {
	return &domain.Document{ID: r.ID, ProjectID: r.ProjectID, CategoryID: r.CategoryID, Title: r.Title,
		Description: r.Description.String, CreatedAt: r.CreatedAt.Time}
}

const documentSelect = `SELECT id, project_id, category_id, title, description, created_at FROM documents`

// GetDocument は Document.find(id)（category を読み込む）。
func GetDocument(ctx context.Context, q db.Queryer, id int64) (*domain.Document, error) {
	var r documentRow
	if err := q.Get(ctx, &r, documentSelect+` WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	d := r.document()
	if err := loadDocumentCategories(ctx, q, []*domain.Document{d}); err != nil {
		return nil, err
	}
	return d, nil
}

// ProjectDocuments は project.documents.includes(:attachments, :category)（id 順）。
func ProjectDocuments(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.Document, error) {
	var rows []documentRow
	if err := q.Select(ctx, &rows, documentSelect+` WHERE project_id = ? ORDER BY id`, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.Document, len(rows))
	for i := range rows {
		out[i] = rows[i].document()
	}
	if err := loadDocumentCategories(ctx, q, out); err != nil {
		return nil, err
	}
	for _, d := range out {
		atts, err := ContainerAttachmentList(ctx, q, domain.AttachmentContainerDocument, d.ID)
		if err != nil {
			return nil, err
		}
		d.Attachments = atts
	}
	return out, nil
}

func loadDocumentCategories(ctx context.Context, q db.Queryer, docs []*domain.Document) error {
	if len(docs) == 0 {
		return nil
	}
	cats, err := ListEnumerations(ctx, q, domain.EnumDocumentCategory, false)
	if err != nil {
		return err
	}
	byID := map[int64]*domain.Enumeration{}
	for _, c := range cats {
		byID[c.ID] = c
	}
	for _, d := range docs {
		d.Category = byID[d.CategoryID]
	}
	return nil
}

// DocumentCategoryExists は category_id の DocumentCategory があるか（belongs_to の検証）。
func DocumentCategoryExists(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM document_categories WHERE id = ?`, id)
	return n > 0, err
}

// DefaultDocumentCategoryID は DocumentCategory.default（is_default の最初の行、無ければ position 順の
// 最初の行。どちらも無ければ 0）。
func DefaultDocumentCategoryID(ctx context.Context, q db.Queryer) (int64, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT id FROM document_categories ORDER BY is_default DESC, position, id`); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return ids[0], nil
}

// InsertDocument は documents 行を作成して d.ID を設定する。
func InsertDocument(ctx context.Context, q db.Queryer, d *domain.Document) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO documents (project_id, category_id, title, description, created_at) VALUES (?, ?, ?, ?, ?)`,
		d.ProjectID, d.CategoryID, d.Title, d.Description, db.NewTime(d.CreatedAt))
	if err != nil {
		return err
	}
	d.ID = id
	return nil
}

// UpdateDocument は category_id / title / description を保存する。
func UpdateDocument(ctx context.Context, q db.Queryer, d *domain.Document) error {
	_, err := q.Exec(ctx, `UPDATE documents SET category_id = ?, title = ?, description = ? WHERE id = ?`,
		d.CategoryID, d.Title, d.Description, d.ID)
	return err
}

// DeleteDocument は documents 行とカスタム値を削除する（添付は呼び出し側が DeleteContainerAttachments で消す）。
func DeleteDocument(ctx context.Context, q db.Queryer, id int64) error {
	if err := DeleteCustomValues(ctx, q, "document", id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM documents WHERE id = ?`, id)
	return err
}
