package issues

import (
	"context"
	"database/sql"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/domain"
)

// SaveJournal は Journal#save を単独で実行する (チケットは保存しない)。ノートも詳細も空なら保存せず false。
// 保存した場合はコミット後の通知 (Journal#send_notification) を返す。
func (e *Env) SaveJournal(ctx context.Context, iss *Issue, j *Journal) (bool, *SaveResult, error) {
	st := &saveState{result: &SaveResult{}}
	ok := false
	err := e.inTx(ctx, func() error {
		var err error
		if ok, err = e.saveJournal(ctx, j, iss, st); err != nil {
			return err
		}
		return e.runCommitCallbacks(ctx, st)
	})
	if err != nil {
		return false, nil, err
	}
	return ok, st.result, nil
}

// PriorAssignedTo は prior_assigned_to (履歴上、現在の担当者の直前の担当者。無ければ nil)。
func (e *Env) PriorAssignedTo(ctx context.Context, iss *Issue) (*domain.Principal, error) {
	var ids []sql.NullString
	if err := e.Q.Select(ctx, &ids, `SELECT d.old_value FROM issue_journals j JOIN issue_journal_details d ON d.journal_id = j.id
WHERE j.issue_id = ? AND d.prop_key = 'assigned_to_id' AND d.old_value IS NOT NULL ORDER BY j.id DESC LIMIT 1`, iss.ID); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return e.Principal(ctx, rubyToI(ids[0].String))
}

// JournalAttachmentIDs は Journal#attachments の id (追加された添付ファイルのうち存在するもの、詳細の順)。
func (e *Env) JournalAttachmentIDs(ctx context.Context, j *Journal) ([]int64, error) {
	var ids []int64
	for _, d := range j.AllDetails() {
		if d.Property == "attachment" && d.Value != nil && *d.Value != "" {
			if id, err := strconv.ParseInt(d.PropKey, 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var existing []int64
	if err := e.Q.Select(ctx, &existing, `SELECT id FROM attachments WHERE id IN (`+inIDs(ids)+`)`); err != nil {
		return nil, err
	}
	var out []int64
	for _, id := range ids {
		if slices.Contains(existing, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// LastUpdatedBy は last_updated_by (最後のジャーナルのユーザ id。無ければ 0)。
func (e *Env) LastUpdatedBy(ctx context.Context, iss *Issue) (int64, error) {
	var ids []int64
	err := e.Q.Select(ctx, &ids, `SELECT user_id FROM issue_journals WHERE issue_id = ? ORDER BY id DESC LIMIT 1`, iss.ID)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// LastNotes は last_notes (user に見える最後のノート)。
func (e *Env) LastNotes(ctx context.Context, iss *Issue, u *domain.User) (string, error) {
	cond, err := e.JournalVisibleNotesCondition(ctx, u)
	if err != nil {
		return "", err
	}
	var notes []sql.NullString
	if err := e.Q.Select(ctx, &notes, `SELECT issue_journals.notes FROM issue_journals JOIN issues ON issues.id = issue_journals.issue_id
JOIN projects ON projects.id = issues.project_id
WHERE issue_journals.issue_id = ? AND issue_journals.notes IS NOT NULL AND issue_journals.notes <> '' AND `+cond+`
ORDER BY issue_journals.id DESC LIMIT 1`, iss.ID); err != nil {
		return "", err
	}
	if len(notes) == 0 {
		return "", nil
	}
	return notes[0].String, nil
}

// UpdateVersionsFromSharingChange は Issue.update_versions_from_sharing_change(version):
// 共有範囲の変更で使えなくなったバージョンをチケットから外す (ジャーナル付き)。
func (e *Env) UpdateVersionsFromSharingChange(ctx context.Context, versionID int64) (*SaveResult, error) {
	return e.updateVersions(ctx, `issues.fixed_version_id = ?`, versionID)
}

// UpdateVersionsFromHierarchyChange は Issue.update_versions_from_hierarchy_change(project):
// プロジェクトの移動後、共有されなくなったバージョンをチケットから外す。
func (e *Env) UpdateVersionsFromHierarchyChange(ctx context.Context, projectID int64) (*SaveResult, error) {
	var ids []int64
	if err := e.Q.Select(ctx, &ids, `SELECT descendant_id FROM project_closure WHERE ancestor_id = ? ORDER BY descendant_id`, projectID); err != nil {
		return nil, err
	}
	in := inIDs(ids)
	return e.updateVersions(ctx, `versions.project_id IN (`+in+`) OR issues.project_id IN (`+in+`)`)
}

// updateVersions は Issue.update_versions(conditions)。
func (e *Env) updateVersions(ctx context.Context, cond string, args ...any) (*SaveResult, error) {
	res := &SaveResult{}
	err := e.inTx(ctx, func() error {
		var ids []int64
		if err := e.Q.Select(ctx, &ids, `SELECT issues.id FROM issues JOIN projects ON projects.id = issues.project_id
JOIN versions ON versions.id = issues.fixed_version_id
WHERE issues.fixed_version_id IS NOT NULL AND issues.project_id <> versions.project_id AND versions.sharing <> 'system'
AND (`+cond+`) ORDER BY issues.id`, args...); err != nil {
			return err
		}
		cur, err := e.currentUser(ctx)
		if err != nil {
			return err
		}
		// プロジェクトの階層・共有が変わっているのでキャッシュを捨てる
		e.projects = nil
		for _, id := range ids {
			for retried := false; ; retried = true {
				iss, err := e.Find(ctx, id)
				if err != nil {
					return err
				}
				if iss == nil {
					break
				}
				p, err := e.ProjectOf(ctx, iss)
				if err != nil {
					return err
				}
				shared, err := e.sharedVersionIDs(ctx, p)
				if err != nil {
					return err
				}
				if containsID(shared, *iss.FixedVersionID) {
					break
				}
				if _, err := e.InitJournal(ctx, iss, cur, ""); err != nil {
					return err
				}
				iss.FixedVersionID = nil
				st := &saveState{result: res}
				ok, err := e.save(ctx, iss, true, st)
				if err == ErrStale && !retried {
					continue
				}
				if err != nil {
					return err
				}
				if ok {
					if err := e.runCommitCallbacks(ctx, st); err != nil {
						return err
					}
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
