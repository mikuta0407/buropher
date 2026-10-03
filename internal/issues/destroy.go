// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"errors"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
)

// ---------------------------------------------------------------- 添付ファイル

// attachSavedAttachments は attach_saved_attachments (+ attachment_added のジャーナル記録)。
func (e *Env) attachSavedAttachments(ctx context.Context, iss *Issue) error {
	for _, id := range iss.attachIDs {
		var filename string
		if err := e.Q.Get(ctx, &filename, `SELECT filename FROM attachments WHERE id = ?`, id); err != nil {
			if isNoRows(err) {
				continue
			}
			return err
		}
		if _, err := e.Q.Exec(ctx, `UPDATE attachments SET container_kind = 'issue', container_id = ? WHERE id = ?`, iss.ID, id); err != nil {
			return err
		}
		if iss.currentJournal != nil && !iss.IsCopy() {
			iss.currentJournal.JournalizeAttachment(id, filename, true)
		}
	}
	iss.attachIDs = nil
	return nil
}

// deleteSelectedAttachments は delete_selected_attachments (+ attachment_removed のジャーナル記録)。
// ディスク上のファイルの削除は添付ファイルの保存層の責務 (ここでは行を削除するだけ)。
func (e *Env) deleteSelectedAttachments(ctx context.Context, iss *Issue) error {
	if len(iss.deletedAttachmentIDs) == 0 {
		return nil
	}
	for _, id := range iss.deletedAttachmentIDs {
		var filename string
		if err := e.Q.Get(ctx, &filename, `SELECT filename FROM attachments WHERE id = ? AND container_kind = 'issue' AND container_id = ?`, id, iss.ID); err != nil {
			if isNoRows(err) {
				continue
			}
			return err
		}
		if _, err := e.Q.Exec(ctx, `DELETE FROM attachments WHERE id = ?`, id); err != nil {
			return err
		}
		if iss.currentJournal != nil {
			iss.currentJournal.JournalizeAttachment(id, filename, false)
		}
	}
	iss.deletedAttachmentIDs = nil
	return nil
}

// ---------------------------------------------------------------- 削除

// TimeEntriesTodo は削除するチケットに工数がある場合の扱い (IssuesController#destroy の params[:todo])。
type TimeEntriesTodo string

const (
	// TimeEntriesDestroy は工数も削除する。
	TimeEntriesDestroy TimeEntriesTodo = "destroy"
	// TimeEntriesNullify は工数のチケットを外す。
	TimeEntriesNullify TimeEntriesTodo = "nullify"
	// TimeEntriesReassign は工数を別のチケットに付け替える。
	TimeEntriesReassign TimeEntriesTodo = "reassign"
)

// DestroyOptions は DestroyIssues のオプション。
type DestroyOptions struct {
	Todo TimeEntriesTodo
	// ReassignToID は Todo = reassign のときの付け替え先 (ProjectID のチケット)。
	ReassignToID int64
	// ProjectID は付け替え先を探すプロジェクト (@project)。0 なら付け替え不可。
	ProjectID int64
}

// 削除時のエラー (IssuesController#destroy のフラッシュメッセージ)。
var (
	// ErrTimeEntriesTodoRequired は工数があるのに扱いが指定されていない (確認画面を表示する)。
	ErrTimeEntriesTodoRequired = errors.New("issues: time entries exist; todo is required")
	// ErrTimeEntryIssueRequired は Setting.timelog_required_fields に issue_id があり nullify できない。
	ErrTimeEntryIssueRequired = errors.New("issues: field_issue blank")
	// ErrReassignTargetNotFound は付け替え先が見つからない (error_issue_not_found_in_project)。
	ErrReassignTargetNotFound = errors.New("issues: error_issue_not_found_in_project")
	// ErrReassignToDeleted は付け替え先が削除対象 (error_cannot_reassign_time_entries_to_an_issue_about_to_be_deleted)。
	ErrReassignToDeleted = errors.New("issues: error_cannot_reassign_time_entries_to_an_issue_about_to_be_deleted")
)

// TimeEntriesHours は削除対象 (子孫を含む) の工数合計 (@hours)。
func (e *Env) TimeEntriesHours(ctx context.Context, ids []int64) (float64, []int64, error) {
	all, err := e.selfAndDescendantIDsOf(ctx, ids)
	if err != nil {
		return 0, nil, err
	}
	var h float64
	if len(all) > 0 {
		var v *float64
		if err := e.Q.Get(ctx, &v, `SELECT SUM(hours) FROM time_entries WHERE issue_id IN (`+inIDs(all)+`)`); err != nil {
			return 0, nil, err
		}
		if v != nil {
			h = *v
		}
	}
	return h, all, nil
}

func (e *Env) selfAndDescendantIDsOf(ctx context.Context, ids []int64) ([]int64, error) {
	var out []int64
	for _, id := range ids {
		root, path, ok, err := e.dbPath(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var sub []int64
		if err := e.Q.Select(ctx, &sub, `SELECT id FROM issues WHERE root_id = ? AND hier_path LIKE ? ORDER BY id`, root, path+"%"); err != nil {
			return nil, err
		}
		for _, s := range sub {
			if !containsID(out, s) {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// DestroyIssues は IssuesController#destroy の本体: 工数の扱いを処理してからチケット (と子孫) を削除する。
// 権限 (deletable?) の確認は呼び出し側で行うこと。
func (e *Env) DestroyIssues(ctx context.Context, ids []int64, opts DestroyOptions) (*SaveResult, error) {
	st := &saveState{result: &SaveResult{}}
	err := e.inTx(ctx, func() error {
		hours, all, err := e.TimeEntriesHours(ctx, ids)
		if err != nil {
			return err
		}
		if hours > 0 {
			switch opts.Todo {
			case TimeEntriesDestroy:
			case TimeEntriesNullify:
				if e.Settings != nil && containsString(e.Settings.Strings("timelog_required_fields"), "issue_id") {
					return ErrTimeEntryIssueRequired
				}
				if _, err := e.Q.Exec(ctx, `UPDATE time_entries SET issue_id = NULL WHERE issue_id IN (`+inIDs(all)+`)`); err != nil {
					return err
				}
			case TimeEntriesReassign:
				var target *Issue
				if opts.ProjectID != 0 {
					t, err := e.Find(ctx, opts.ReassignToID)
					if err != nil {
						return err
					}
					if t != nil && t.ProjectID == opts.ProjectID {
						target = t
					}
				}
				if target == nil {
					return ErrReassignTargetNotFound
				}
				if containsID(all, target.ID) {
					return ErrReassignToDeleted
				}
				if _, err := e.Q.Exec(ctx, `UPDATE time_entries SET issue_id = ?, project_id = ? WHERE issue_id IN (`+inIDs(all)+`)`,
					target.ID, target.ProjectID); err != nil {
					return err
				}
			default:
				return ErrTimeEntriesTodoRequired
			}
		}
		for _, id := range ids {
			iss, err := e.Find(ctx, id)
			if err != nil {
				return err
			}
			if iss == nil {
				// 親と一緒に削除済み
				continue
			}
			if err := e.destroy(ctx, iss, false, st); err != nil {
				return err
			}
		}
		return e.runCommitCallbacks(ctx, st)
	})
	if err != nil {
		return nil, err
	}
	return st.result, nil
}

// Destroy は issue.destroy (子孫も削除する)。
func (e *Env) Destroy(ctx context.Context, iss *Issue) (*SaveResult, error) {
	st := &saveState{result: &SaveResult{}}
	err := e.inTx(ctx, func() error {
		if err := e.destroy(ctx, iss, false, st); err != nil {
			return err
		}
		return e.runCommitCallbacks(ctx, st)
	})
	if err != nil {
		return nil, err
	}
	return st.result, nil
}

// destroy は destroy (before_destroy: destroy_children、依存レコードの削除、after_destroy: 親の再計算)。
func (e *Env) destroy(ctx context.Context, iss *Issue, withoutNestedSet bool, st *saveState) error {
	// 既に削除済み・古い場合は読み直す
	fresh, err := e.Find(ctx, iss.ID)
	if err != nil {
		return err
	}
	if fresh == nil {
		return nil
	}
	children, err := e.Children(ctx, fresh)
	if err != nil {
		return err
	}
	for _, c := range children {
		if err := e.destroy(ctx, c, true, st); err != nil {
			return err
		}
	}
	id := iss.ID
	// 工数のカスタム値 (TimeEntry の acts_as_customizable)
	if _, err := e.Q.Exec(ctx, `DELETE FROM custom_values WHERE customized_kind = 'time_entry' AND customized_id IN
(SELECT id FROM time_entries WHERE issue_id = ?)`, id); err != nil {
		return err
	}
	// 添付ファイル型カスタム値の添付
	if _, err := e.Q.Exec(ctx, `DELETE FROM attachments WHERE container_kind = 'custom_value' AND container_id IN
(SELECT cv.id FROM custom_values cv JOIN custom_fields cf ON cf.id = cv.custom_field_id
 WHERE cv.customized_kind = 'issue' AND cv.customized_id = ? AND cf.field_format = 'attachment')`, id); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ?`,
		`DELETE FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = ?`,
		`DELETE FROM attachments WHERE container_kind = 'issue' AND container_id = ?`,
		`DELETE FROM reactions WHERE reactable_kind = 'issue' AND reactable_id = ?`,
		`DELETE FROM reactions WHERE reactable_kind = 'journal' AND reactable_id IN (SELECT id FROM issue_journals WHERE issue_id = ?)`,
		`DELETE FROM time_entries WHERE issue_id = ?`,
		`DELETE FROM issue_relations WHERE issue_from_id = ? OR issue_to_id = ?`,
		`DELETE FROM issue_journals WHERE issue_id = ?`,
	} {
		args := []any{id}
		if n := countPlaceholders(q); n == 2 {
			args = append(args, id)
		}
		if _, err := e.Q.Exec(ctx, q, args...); err != nil {
			return err
		}
	}
	// 子の削除で親 (= このチケット) が再計算・保存されている可能性があるので DB の parent_id を使う
	parentID := fresh.ParentID
	if _, err := e.Q.Exec(ctx, `DELETE FROM issues WHERE id = ?`, id); err != nil {
		return err
	}
	rec := st.addIssue(iss)
	rec.destroyed = true
	rec.destroyedParentID = parentID
	rec.withoutNestedSet = withoutNestedSet
	if parentID != nil {
		if err := e.recalculateAttributesFor(ctx, *parentID, st); err != nil {
			return err
		}
	}
	return nil
}

func countPlaceholders(q string) int {
	n := 0
	for _, c := range q {
		if c == '?' {
			n++
		}
	}
	return n
}

func formatInt(id int64) string { return strconv.FormatInt(id, 10) }

var _ = db.In
