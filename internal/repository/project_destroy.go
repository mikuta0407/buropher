// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// ScheduleProjectDeletion は DestroyProjectJob.schedule の前半:
// プロジェクトと子孫を即座に削除予約（STATUS_SCHEDULED_FOR_DELETION）にする。
func ScheduleProjectDeletion(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `UPDATE projects SET status = ? WHERE id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?)`,
		domain.ProjectStatusScheduledForDeletion, id)
	return err
}

// DestroyProject は Project#destroy（子孫を含む）の移植。外部キーの CASCADE で消えない
// ポリモーフィックな関連（カスタム値・ウォッチャー・添付・リアクション）を先に削除し、
// 作業時間（作業分類への RESTRICT 参照があるため）を消してから projects 行を削除する。
//
// 削除した添付の行を返す（実ファイルはコミット後に呼び出し側が Store.DeleteFromDisk で消す）。
func DestroyProject(ctx context.Context, q db.Queryer, id int64) ([]*domain.Attachment, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT descendant_id FROM project_closure WHERE ancestor_id = ? ORDER BY depth DESC`, id); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrNotFound
	}
	parts := make([]string, len(ids))
	for i, x := range ids {
		parts[i] = strconv.FormatInt(x, 10)
	}
	in := "(" + strings.Join(parts, ",") + ")"
	issues := `SELECT id FROM issues WHERE project_id IN ` + in
	journals := `SELECT j.id FROM issue_journals j WHERE j.issue_id IN (` + issues + `)`
	versions := `SELECT id FROM versions WHERE project_id IN ` + in
	news := `SELECT id FROM news WHERE project_id IN ` + in
	comments := `SELECT id FROM news_comments WHERE news_id IN (` + news + `)`
	boards := `SELECT id FROM boards WHERE project_id IN ` + in
	messages := `SELECT id FROM messages WHERE board_id IN (` + boards + `)`
	wikis := `SELECT id FROM wikis WHERE project_id IN ` + in
	pages := `SELECT id FROM wiki_pages WHERE wiki_id IN (` + wikis + `)`
	documents := `SELECT id FROM documents WHERE project_id IN ` + in
	timeEntries := `SELECT id FROM time_entries WHERE project_id IN ` + in
	modules := `SELECT id FROM project_modules WHERE project_id IN ` + in
	activities := `SELECT id FROM time_entry_activities WHERE project_id IN ` + in
	stmts := []string{
		// カスタム値（カスタム値への添付を先に）
		`DELETE FROM attachments WHERE container_kind = 'custom_value' AND container_id IN (SELECT id FROM custom_values WHERE
  (customized_kind = 'issue' AND customized_id IN (` + issues + `)) OR (customized_kind = 'project' AND customized_id IN ` + in + `)
  OR (customized_kind = 'version' AND customized_id IN (` + versions + `)) OR (customized_kind = 'document' AND customized_id IN (` + documents + `))
  OR (customized_kind = 'time_entry' AND customized_id IN (` + timeEntries + `)))`,
		`DELETE FROM custom_values WHERE customized_kind = 'issue' AND customized_id IN (` + issues + `)`,
		`DELETE FROM custom_values WHERE customized_kind = 'project' AND customized_id IN ` + in,
		`DELETE FROM custom_values WHERE customized_kind = 'version' AND customized_id IN (` + versions + `)`,
		`DELETE FROM custom_values WHERE customized_kind = 'document' AND customized_id IN (` + documents + `)`,
		`DELETE FROM custom_values WHERE customized_kind = 'time_entry' AND customized_id IN (` + timeEntries + `)`,
		`DELETE FROM custom_values WHERE customized_kind = 'enumeration' AND customized_id IN (` + activities + `)`,
		// ウォッチャー
		`DELETE FROM watchers WHERE watchable_kind = 'issue' AND watchable_id IN (` + issues + `)`,
		`DELETE FROM watchers WHERE watchable_kind = 'news' AND watchable_id IN (` + news + `)`,
		`DELETE FROM watchers WHERE watchable_kind = 'board' AND watchable_id IN (` + boards + `)`,
		`DELETE FROM watchers WHERE watchable_kind = 'message' AND watchable_id IN (` + messages + `)`,
		`DELETE FROM watchers WHERE watchable_kind = 'wiki' AND watchable_id IN (` + wikis + `)`,
		`DELETE FROM watchers WHERE watchable_kind = 'wiki_page' AND watchable_id IN (` + pages + `)`,
		`DELETE FROM watchers WHERE watchable_kind = 'project_module' AND watchable_id IN (` + modules + `)`,
		// 添付
		`DELETE FROM attachments WHERE container_kind = 'issue' AND container_id IN (` + issues + `)`,
		`DELETE FROM attachments WHERE container_kind = 'project' AND container_id IN ` + in,
		`DELETE FROM attachments WHERE container_kind = 'version' AND container_id IN (` + versions + `)`,
		`DELETE FROM attachments WHERE container_kind = 'wiki_page' AND container_id IN (` + pages + `)`,
		`DELETE FROM attachments WHERE container_kind = 'message' AND container_id IN (` + messages + `)`,
		`DELETE FROM attachments WHERE container_kind = 'news' AND container_id IN (` + news + `)`,
		`DELETE FROM attachments WHERE container_kind = 'document' AND container_id IN (` + documents + `)`,
		// リアクション
		`DELETE FROM reactions WHERE reactable_kind = 'issue' AND reactable_id IN (` + issues + `)`,
		`DELETE FROM reactions WHERE reactable_kind = 'journal' AND reactable_id IN (` + journals + `)`,
		`DELETE FROM reactions WHERE reactable_kind = 'news' AND reactable_id IN (` + news + `)`,
		`DELETE FROM reactions WHERE reactable_kind = 'message' AND reactable_id IN (` + messages + `)`,
		`DELETE FROM reactions WHERE reactable_kind = 'comment' AND reactable_id IN (` + comments + `)`,
		// 作業時間（作業分類への RESTRICT 参照）
		`DELETE FROM time_entries WHERE project_id IN ` + in,
		`DELETE FROM time_entries WHERE issue_id IN (` + issues + `)`,
		// チケット（親子・関連は CASCADE / 遅延制約）
		`DELETE FROM issues WHERE project_id IN ` + in,
		`DELETE FROM projects WHERE id IN ` + in,
	}
	var rows []attachmentRow
	if err := q.Select(ctx, &rows, attachmentSelect+` WHERE (attachments.container_kind = 'issue' AND attachments.container_id IN (`+issues+`))
OR (attachments.container_kind = 'project' AND attachments.container_id IN `+in+`)
OR (attachments.container_kind = 'version' AND attachments.container_id IN (`+versions+`))
OR (attachments.container_kind = 'wiki_page' AND attachments.container_id IN (`+pages+`))
OR (attachments.container_kind = 'message' AND attachments.container_id IN (`+messages+`))
OR (attachments.container_kind = 'news' AND attachments.container_id IN (`+news+`))
OR (attachments.container_kind = 'document' AND attachments.container_id IN (`+documents+`))
ORDER BY attachments.id`); err != nil {
		return nil, err
	}
	atts := make([]*domain.Attachment, len(rows))
	for i := range rows {
		atts[i] = rows[i].attachment()
	}
	for _, s := range stmts {
		if _, err := q.Exec(ctx, s); err != nil {
			return nil, err
		}
	}
	return atts, nil
}
