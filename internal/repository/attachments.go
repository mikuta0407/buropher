// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

// 添付ファイル（Attachment モデル）の読み書き。ディスク上のファイル操作は internal/attachments が行う。
// テキスト整形用の軽量な読み込み（RefAttachment）は wikitext.go にある。

import (
	"context"
	"database/sql"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type attachmentRow struct {
	ID            int64          `db:"id"`
	ContainerKind sql.NullString `db:"container_kind"`
	ContainerID   sql.NullInt64  `db:"container_id"`
	Filename      string         `db:"filename"`
	DiskDirectory sql.NullString `db:"disk_directory"`
	DiskFilename  string         `db:"disk_filename"`
	Filesize      int64          `db:"filesize"`
	ContentType   sql.NullString `db:"content_type"`
	Digest        sql.NullString `db:"digest"`
	DigestAlgo    sql.NullString `db:"digest_algo"`
	Downloads     int            `db:"downloads"`
	AuthorID      int64          `db:"author_id"`
	Description   sql.NullString `db:"description"`
	CreatedAt     db.Time        `db:"created_at"`
}

const attachmentSelect = `SELECT attachments.id, attachments.container_kind, attachments.container_id, attachments.filename,
  attachments.disk_directory, attachments.disk_filename, attachments.filesize, attachments.content_type,
  attachments.digest, attachments.digest_algo, attachments.downloads, attachments.author_id,
  attachments.description, attachments.created_at
FROM attachments`

func (r *attachmentRow) attachment() *domain.Attachment {
	a := &domain.Attachment{
		ID: r.ID, ContainerKind: r.ContainerKind.String, Filename: r.Filename,
		DiskDirectory: r.DiskDirectory.String, DiskFilename: r.DiskFilename, Filesize: r.Filesize,
		ContentType: r.ContentType.String, Digest: r.Digest.String, DigestAlgo: r.DigestAlgo.String,
		Downloads: r.Downloads, AuthorID: r.AuthorID, Description: r.Description.String, DescriptionNull: !r.Description.Valid, CreatedOn: r.CreatedAt.Time,
	}
	if r.ContainerID.Valid {
		id := r.ContainerID.Int64
		a.ContainerID = &id
	}
	return a
}

// nullStr は空文字列を NULL にする（content_type・digest・disk_directory など、Redmine でも空にならず nil になる列用）。
func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// attachmentDescriptionArg は description の保存値（DescriptionNull で空なら NULL、それ以外は "" もそのまま。D-17）。
func attachmentDescriptionArg(a *domain.Attachment) sql.NullString {
	return sql.NullString{String: a.Description, Valid: !a.DescriptionNull || a.Description != ""}
}

// attachmentContainerArgs は container_kind / container_id の値（未紐付けなら両方 NULL）。
func attachmentContainerArgs(a *domain.Attachment) (sql.NullString, sql.NullInt64) {
	if a.ContainerID == nil || a.ContainerKind == "" {
		return sql.NullString{}, sql.NullInt64{}
	}
	return sql.NullString{String: a.ContainerKind, Valid: true}, sql.NullInt64{Int64: *a.ContainerID, Valid: true}
}

// InsertAttachment は添付ファイルの行を作成し、a.ID を設定する。CreatedOn がゼロなら db.Now()。
// digest が空なら digest / digest_algo は NULL。
func InsertAttachment(ctx context.Context, q db.Queryer, a *domain.Attachment) error {
	if a.CreatedOn.IsZero() {
		a.CreatedOn = db.Now().Time
	}
	kind, cid := attachmentContainerArgs(a)
	algo := a.DigestAlgo
	if a.Digest == "" {
		algo = ""
	} else if algo == "" {
		algo = "sha256"
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO attachments (container_kind, container_id, filename, disk_directory,
  disk_filename, filesize, content_type, digest, digest_algo, downloads, author_id, description, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		kind, cid, a.Filename, nullStr(a.DiskDirectory), a.DiskFilename, a.Filesize, nullStr(a.ContentType),
		nullStr(a.Digest), nullStr(algo), a.Downloads, a.AuthorID, attachmentDescriptionArg(a), db.NewTime(a.CreatedOn))
	if err != nil {
		return err
	}
	a.ID = id
	a.DigestAlgo = algo
	return nil
}

// UpdateAttachment は container・filename・description・content_type を保存する
// （attachment.save / container.attachments << attachment）。
func UpdateAttachment(ctx context.Context, q db.Queryer, a *domain.Attachment) error {
	kind, cid := attachmentContainerArgs(a)
	_, err := q.Exec(ctx, `UPDATE attachments SET container_kind = ?, container_id = ?, filename = ?, description = ?, content_type = ?
WHERE id = ?`, kind, cid, a.Filename, attachmentDescriptionArg(a), nullStr(a.ContentType), a.ID)
	return err
}

// UpdateAttachmentDiskfile は disk_directory / disk_filename を変更する（update_columns。重複ファイルの再利用）。
func UpdateAttachmentDiskfile(ctx context.Context, q db.Queryer, id int64, dir, filename string) error {
	_, err := q.Exec(ctx, `UPDATE attachments SET disk_directory = ?, disk_filename = ? WHERE id = ?`, nullStr(dir), filename, id)
	return err
}

// IncrementAttachmentDownloads は Attachment#increment_download。
func IncrementAttachmentDownloads(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `UPDATE attachments SET downloads = downloads + 1 WHERE id = ?`, id)
	return err
}

// GetAttachment は Attachment.find(id)（author を読み込む）。
func GetAttachment(ctx context.Context, q db.Queryer, id int64) (*domain.Attachment, error) {
	var r attachmentRow
	if err := q.Get(ctx, &r, attachmentSelect+` WHERE attachments.id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	out, err := preloadAttachmentAuthors(ctx, q, []attachmentRow{r})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// FindUnattachedAttachment は Attachment.find_by_token の検索部分（id と digest が一致し container が無いもの）。
// トークンの解析は attachments.ParseToken。
func FindUnattachedAttachment(ctx context.Context, q db.Queryer, id int64, digest string) (*domain.Attachment, error) {
	var r attachmentRow
	if err := q.Get(ctx, &r, attachmentSelect+` WHERE attachments.id = ? AND attachments.digest = ? AND attachments.container_id IS NULL`, id, digest); err != nil {
		return nil, notFound(err)
	}
	out, err := preloadAttachmentAuthors(ctx, q, []attachmentRow{r})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// ContainerAttachmentList は container.attachments（acts_as_attachable の関連。created_on, id の昇順）。
// author を読み込む。
func ContainerAttachmentList(ctx context.Context, q db.Queryer, kind string, id int64) ([]*domain.Attachment, error) {
	var rows []attachmentRow
	if err := q.Select(ctx, &rows, attachmentSelect+` WHERE attachments.container_kind = ? AND attachments.container_id = ?
ORDER BY attachments.created_at ASC, attachments.id ASC`, kind, id); err != nil {
		return nil, err
	}
	return preloadAttachmentAuthors(ctx, q, rows)
}

// AttachmentsSharingDiskfile は disk_filename が同じで id が異なる添付の件数
// （Attachment#delete_from_disk の判定。Attachment.where("disk_filename = ? AND id <> ?")）。
func AttachmentsSharingDiskfile(ctx context.Context, q db.Queryer, diskFilename string, excludeID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM attachments WHERE disk_filename = ? AND id <> ?`, diskFilename, excludeID)
	return n, err
}

// FindReusableAttachment は reuse_existing_file_if_possible の検索
// （digest・filesize が同じで disk_filename が異なる最後の添付。無ければ ErrNotFound）。
func FindReusableAttachment(ctx context.Context, q db.Queryer, digest string, filesize int64, diskFilename string) (*domain.Attachment, error) {
	var r attachmentRow
	if err := q.Get(ctx, &r, attachmentSelect+` WHERE attachments.digest = ? AND attachments.filesize = ? AND attachments.disk_filename <> ?
ORDER BY attachments.id DESC LIMIT 1`, digest, filesize, diskFilename); err != nil {
		return nil, notFound(err)
	}
	return r.attachment(), nil
}

// DeleteAttachment は添付ファイルの行を削除する（ディスク上のファイルは attachments.Store.DeleteFromDisk）。
func DeleteAttachment(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `DELETE FROM attachments WHERE id = ?`, id)
	return err
}

// DeleteContainerAttachments は containers（kind, ids）の添付の行をすべて削除し、削除した添付を返す
// （has_many :attachments, dependent: :destroy）。ディスク上のファイルはコミット後に
// attachments.Store.DeleteFromDisk(ctx, q, deleted...) で消す（after_commit :delete_from_disk）。
func DeleteContainerAttachments(ctx context.Context, q db.Queryer, kind string, ids []int64) ([]*domain.Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*domain.Attachment
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(attachmentSelect+` WHERE attachments.container_kind = ? AND attachments.container_id IN (?) ORDER BY attachments.id`, kind, chunk)
		if err != nil {
			return nil, err
		}
		var rows []attachmentRow
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for i := range rows {
			out = append(out, rows[i].attachment())
		}
		query, args, err = db.In(`DELETE FROM attachments WHERE container_kind = ? AND container_id IN (?)`, kind, chunk)
		if err != nil {
			return nil, err
		}
		if _, err := q.Exec(ctx, query, args...); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AttachmentContainerProject は添付のコンテナが属するプロジェクトの id（Attachment#project = container.project）。
// コンテナが無い・未対応の種別なら ErrNotFound。
func AttachmentContainerProject(ctx context.Context, q db.Queryer, kind string, id int64) (int64, error) {
	var query string
	switch kind {
	case domain.AttachmentContainerProject:
		query = `SELECT id FROM projects WHERE id = ?`
	case domain.AttachmentContainerIssue:
		query = `SELECT project_id FROM issues WHERE id = ?`
	case domain.AttachmentContainerVersion:
		query = `SELECT project_id FROM versions WHERE id = ?`
	case domain.AttachmentContainerNews:
		query = `SELECT project_id FROM news WHERE id = ?`
	case domain.AttachmentContainerDocument:
		query = `SELECT project_id FROM documents WHERE id = ?`
	case domain.AttachmentContainerWikiPage:
		query = `SELECT wikis.project_id FROM wiki_pages JOIN wikis ON wikis.id = wiki_pages.wiki_id WHERE wiki_pages.id = ?`
	case domain.AttachmentContainerMessage:
		query = `SELECT boards.project_id FROM messages JOIN boards ON boards.id = messages.board_id WHERE messages.id = ?`
	default:
		return 0, ErrNotFound
	}
	var pid int64
	if err := q.Get(ctx, &pid, query, id); err != nil {
		return 0, notFound(err)
	}
	return pid, nil
}

func preloadAttachmentAuthors(ctx context.Context, q db.Queryer, rows []attachmentRow) ([]*domain.Attachment, error) {
	out := make([]*domain.Attachment, len(rows))
	uids := make([]int64, len(rows))
	for i := range rows {
		out[i] = rows[i].attachment()
		uids[i] = rows[i].AuthorID
	}
	if len(out) == 0 {
		return out, nil
	}
	users, err := UsersByIDs(ctx, q, uids)
	if err != nil {
		return nil, err
	}
	for _, a := range out {
		a.Author = users[a.AuthorID]
	}
	return out, nil
}
