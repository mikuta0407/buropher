// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// SaveResult は保存の結果 (コミット後に行う通知)。
type SaveResult struct {
	// Notifications はコミット後にキューへ積む通知 (Mailer.deliver_issue_add / deliver_issue_edit)。
	Notifications []Notification
}

// saveState は 1 回の (入れ子の保存を含む) 保存処理で共有する状態。
type saveState struct {
	// records はトランザクションに参加したレコード (保存開始順)。コミット後コールバックの実行順。
	records []*commitRecord
	result  *SaveResult
}

type commitRecord struct {
	issue   *Issue
	journal *Journal
	// journalIssue はジャーナルの journalized (同じインスタンス)。
	journalIssue *Issue
	created      bool
	// destroyedParentID は削除されたチケットの親 (create_parent_issue_journal 用)。
	destroyed         bool
	destroyedParentID *int64
	withoutNestedSet  bool
}

func (st *saveState) addIssue(iss *Issue) *commitRecord {
	for _, r := range st.records {
		if r.issue == iss {
			return r
		}
	}
	r := &commitRecord{issue: iss}
	st.records = append(st.records, r)
	return r
}

var errRollback = errors.New("issues: rollback")

// Save は issue.save (検証付き)。検証エラーなら false を返し iss.Errors に理由を設定する。
// 楽観ロックの衝突は ErrStale。成功時の SaveResult.Notifications はコミット後に Dispatch すること。
func (e *Env) Save(ctx context.Context, iss *Issue) (bool, *SaveResult, error) {
	return e.saveTop(ctx, iss, true)
}

// SaveWithoutValidation は issue.save(validate: false)。
func (e *Env) SaveWithoutValidation(ctx context.Context, iss *Issue) (*SaveResult, error) {
	_, res, err := e.saveTop(ctx, iss, false)
	return res, err
}

func (e *Env) saveTop(ctx context.Context, iss *Issue, validate bool) (bool, *SaveResult, error) {
	st := &saveState{result: &SaveResult{}}
	ok := false
	err := e.inTx(ctx, func() error {
		return e.savepoint(ctx, func() error {
			var err error
			ok, err = e.save(ctx, iss, validate, st)
			if err != nil {
				return err
			}
			if !ok {
				return errRollback
			}
			return e.runCommitCallbacks(ctx, st)
		})
	})
	if errors.Is(err, errRollback) {
		return false, st.result, nil
	}
	if err != nil {
		return false, nil, err
	}
	return ok, st.result, nil
}

var savepointSeq int

// savepoint は fn をセーブポイント内で実行し、エラーならセーブポイントまで戻す。
func (e *Env) savepoint(ctx context.Context, fn func() error) error {
	savepointSeq++
	name := "issues_sp_" + strconv.Itoa(savepointSeq)
	if _, err := e.Q.Exec(ctx, "SAVEPOINT "+name); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := e.Q.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); rerr != nil {
			return errors.Join(err, rerr)
		}
		_, _ = e.Q.Exec(ctx, "RELEASE SAVEPOINT "+name)
		return err
	}
	_, err := e.Q.Exec(ctx, "RELEASE SAVEPOINT "+name)
	return err
}

// save は 1 チケットの保存 (コールバック込み)。入れ子の保存は同じ st を使う。
func (e *Env) save(ctx context.Context, iss *Issue, validate bool, st *saveState) (bool, error) {
	rec := st.addIssue(iss)
	if validate {
		ok, err := e.Validate(ctx, iss)
		if err != nil || !ok {
			return false, err
		}
	}
	isNew := iss.NewRecord()

	// ---- before_save
	// set_parent_id
	iss.ParentID = iss.parentIssueIDValue()
	// attach_saved_attachments (保存済みチケットは即座に紐付けてジャーナルに記録)
	if !isNew {
		if err := e.attachSavedAttachments(ctx, iss); err != nil {
			return false, err
		}
	}
	if err := e.closeDuplicates(ctx, iss, st); err != nil {
		return false, err
	}
	// update_done_ratio_from_issue_status
	if e.useStatusForDoneRatio() {
		s, err := e.StatusOf(ctx, iss)
		if err != nil {
			return false, err
		}
		if s != nil && s.DefaultDoneRatio != nil {
			iss.DoneRatio = *s.DefaultDoneRatio
		}
	}
	// force_updated_on_change
	if iss.Changed() || (iss.currentJournal != nil && !iss.currentJournal.NotesAndDetailsEmpty()) {
		iss.UpdatedAt = e.now()
		if isNew {
			iss.CreatedAt = iss.UpdatedAt
		}
	}
	// update_closed_on
	if closing, err := e.Closing(ctx, iss); err != nil {
		return false, err
	} else if closing {
		t := iss.UpdatedAt
		iss.ClosedAt = &t
	}

	// ---- before_create / before_update + INSERT / UPDATE
	if isNew {
		if err := e.insertIssue(ctx, iss); err != nil {
			return false, err
		}
		rec.created = true
	} else {
		if iss.AttrChanged("parent_id") {
			if err := e.moveSubtree(ctx, iss, iss.ParentID); err != nil {
				return false, err
			}
			// moveSubtree が DB の root_id / hier_path を更新済み
			iss.orig.RootID, iss.orig.HierPath = iss.RootID, iss.HierPath
		}
		if err := e.updateIssue(ctx, iss); err != nil {
			return false, err
		}
	}
	// changes_applied
	prev := *iss.orig0()
	iss.saved = &prev
	iss.savedNew = isNew
	cur := iss.Issue
	iss.orig = &cur

	// ---- after_create (autosave 関連)
	if isNew {
		if err := e.saveNewWatchers(ctx, iss); err != nil {
			return false, err
		}
		if err := e.attachSavedAttachments(ctx, iss); err != nil {
			return false, err
		}
		if err := e.saveCopiedAttachments(ctx, iss); err != nil {
			return false, err
		}
	}

	// ---- after_save
	touch, err := e.saveCustomFieldValues(ctx, iss)
	if err != nil {
		return false, err
	}
	if touch && !iss.savedAnyChange() {
		if err := e.touch(ctx, iss); err != nil {
			return false, err
		}
	}
	if err := e.parseIssueMentions(ctx, iss); err != nil {
		return false, err
	}
	if !isNew && iss.SavedChangeTo("project_id") {
		ok, err := e.afterProjectChange(ctx, iss, st)
		if err != nil || !ok {
			return false, err
		}
	}
	if err := e.rescheduleFollowingIssues(ctx, iss, st); err != nil {
		return false, err
	}
	if iss.SavedChangeTo("parent_id") {
		if err := e.updateNestedSetAttributesOnParentChange(ctx, iss, st); err != nil {
			return false, err
		}
	}
	iss.parentIssueSet, iss.parentIssue = false, nil
	if iss.ParentID != nil {
		if err := e.recalculateAttributesFor(ctx, *iss.ParentID, st); err != nil {
			return false, err
		}
	}
	if err := e.deleteSelectedAttachments(ctx, iss); err != nil {
		return false, err
	}
	if iss.currentJournal != nil {
		if _, err := e.saveJournal(ctx, iss.currentJournal, iss, st); err != nil {
			return false, err
		}
	}
	if err := e.afterCreateFromCopy(ctx, iss, st); err != nil {
		return false, err
	}
	return true, nil
}

// orig0 は保存前の DB 上の値 (新規なら列の既定値)。
func (iss *Issue) orig0() *domain.Issue {
	if iss.orig == nil {
		return &domain.Issue{}
	}
	return iss.orig
}

// savedAnyChange は saved_changes? (直前の保存で列が変わったか)。
func (iss *Issue) savedAnyChange() bool {
	if iss.saved == nil {
		return false
	}
	if iss.savedNew {
		return true
	}
	for _, a := range allColumns {
		if !attrEqual(a, iss.saved, &iss.Issue) {
			return true
		}
	}
	return false
}

func (e *Env) issueArgs(iss *Issue) []any {
	// description は nil なら NULL、"" は "" のまま保存する (D-17)
	var desc any
	if iss.Description != nil {
		desc = *iss.Description
	}
	var sd, dd db.NullDate
	if iss.StartDate != nil {
		sd = db.NewNullDate(db.DateOf(*iss.StartDate))
	}
	if iss.DueDate != nil {
		dd = db.NewNullDate(db.DateOf(*iss.DueDate))
	}
	var closed db.NullTime
	if iss.ClosedAt != nil {
		closed = db.NewNullTime(*iss.ClosedAt)
	}
	return []any{iss.ProjectID, iss.TrackerID, iss.StatusID, iss.PriorityID, iss.AuthorID, iss.AssignedToID, iss.CategoryID,
		iss.FixedVersionID, iss.ParentID, iss.Subject, desc, sd, dd, iss.DoneRatio, iss.EstimatedHours, iss.IsPrivate,
		db.NewTime(iss.CreatedAt), db.NewTime(iss.UpdatedAt), closed}
}

// insertIssue は INSERT と入れ子集合の設定 (add_to_nested_set / add_as_root)。
func (e *Env) insertIssue(ctx context.Context, iss *Issue) error {
	if iss.CreatedAt.IsZero() {
		iss.CreatedAt = e.now()
	}
	if iss.UpdatedAt.IsZero() {
		iss.UpdatedAt = iss.CreatedAt
	}
	var rootID int64
	parentPath := ""
	if iss.ParentID != nil {
		r, p, ok, err := e.dbPath(ctx, *iss.ParentID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("issues: parent issue %d not found", *iss.ParentID)
		}
		rootID, parentPath = r, p
	}
	args := append(e.issueArgs(iss), rootID, iss.LockVersion)
	id, err := e.Q.InsertReturningID(ctx, `INSERT INTO issues (project_id, tracker_id, status_id, priority_id, author_id,
  assigned_to_id, category_id, fixed_version_id, parent_id, subject, description, start_date, due_date, done_ratio,
  estimated_hours, is_private, created_at, updated_at, closed_at, root_id, lock_version, hier_path)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')`, args...)
	if err != nil {
		return err
	}
	iss.ID = id
	if rootID == 0 {
		// add_as_root: Redmine はハッシュ形式の update_all で root_id / lft / rgt を設定するため、
		// 楽観ロックが有効なモデルでは lock_version も 1 進む (DB 上の値。Ruby のインスタンスは古いままだが
		// ここでは DB と揃える)。
		iss.RootID, iss.HierPath = id, hierPathFor("", id)
		iss.LockVersion++
		_, err = e.Q.Exec(ctx, `UPDATE issues SET root_id = ?, hier_path = ?, lock_version = ? WHERE id = ?`,
			iss.RootID, iss.HierPath, iss.LockVersion, id)
		return err
	}
	iss.RootID, iss.HierPath = rootID, hierPathFor(parentPath, id)
	_, err = e.Q.Exec(ctx, `UPDATE issues SET root_id = ?, hier_path = ? WHERE id = ?`, iss.RootID, iss.HierPath, id)
	return err
}

// updateIssue は UPDATE (楽観ロック付き)。変更が無ければ何もしない。
func (e *Env) updateIssue(ctx context.Context, iss *Issue) error {
	changed := false
	for _, a := range allColumns {
		if a == "root_id" || a == "hier_path" {
			continue
		}
		if iss.AttrChanged(a) {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	lock := iss.LockVersion
	args := append(e.issueArgs(iss), lock+1, iss.ID, lock)
	res, err := e.Q.Exec(ctx, `UPDATE issues SET project_id = ?, tracker_id = ?, status_id = ?, priority_id = ?, author_id = ?,
  assigned_to_id = ?, category_id = ?, fixed_version_id = ?, parent_id = ?, subject = ?, description = ?, start_date = ?,
  due_date = ?, done_ratio = ?, estimated_hours = ?, is_private = ?, created_at = ?, updated_at = ?, closed_at = ?,
  lock_version = ? WHERE id = ? AND lock_version = ?`, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStale
	}
	iss.LockVersion = lock + 1
	return nil
}

// touch は updated_on を現在時刻にして lock_version を進める (ActiveRecord#touch)。
func (e *Env) touch(ctx context.Context, iss *Issue) error {
	now := e.now()
	res, err := e.Q.Exec(ctx, `UPDATE issues SET updated_at = ?, lock_version = ? WHERE id = ? AND lock_version = ?`,
		db.NewTime(now), iss.LockVersion+1, iss.ID, iss.LockVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrStale
	}
	iss.UpdatedAt = now
	iss.LockVersion++
	iss.orig.UpdatedAt, iss.orig.LockVersion = iss.UpdatedAt, iss.LockVersion
	return nil
}

// closeDuplicates は close_duplicates (before_save): クローズ時にこのチケットを重複とするチケットも閉じる。
func (e *Env) closeDuplicates(ctx context.Context, iss *Issue, st *saveState) error {
	if e.Settings != nil && !e.Settings.Bool("close_duplicate_issues") {
		return nil
	}
	closing, err := e.Closing(ctx, iss)
	if err != nil || !closing || iss.ID == 0 {
		return err
	}
	var ids []int64
	if err := e.Q.Select(ctx, &ids, `SELECT issue_from_id FROM issue_relations WHERE issue_to_id = ? AND relation_type = 'duplicates' ORDER BY id`, iss.ID); err != nil {
		return err
	}
	for _, id := range ids {
		dup, err := e.Find(ctx, id)
		if err != nil {
			return err
		}
		if dup == nil {
			continue
		}
		if c, err := e.Closed(ctx, dup); err != nil {
			return err
		} else if c {
			continue
		}
		if j := iss.currentJournal; j != nil {
			u, err := e.UserByID(ctx, j.UserID)
			if err != nil {
				return err
			}
			dj, err := e.InitJournal(ctx, dup, u, j.Notes)
			if err != nil {
				return err
			}
			dj.PrivateNotes = j.PrivateNotes
		}
		dup.StatusID = iss.StatusID
		if _, err := e.save(ctx, dup, false, st); err != nil {
			return err
		}
	}
	return nil
}

// afterProjectChange は after_project_change: 工数のプロジェクト、関連、同じプロジェクトにいた子の移動。
func (e *Env) afterProjectChange(ctx context.Context, iss *Issue, st *saveState) (bool, error) {
	if _, err := e.Q.Exec(ctx, `UPDATE time_entries SET project_id = ? WHERE issue_id = ?`, iss.ProjectID, iss.ID); err != nil {
		return false, err
	}
	if e.Settings == nil || !e.Settings.Bool("cross_project_issue_relations") {
		if _, err := e.Q.Exec(ctx, `DELETE FROM issue_relations WHERE issue_from_id = ? OR issue_to_id = ?`, iss.ID, iss.ID); err != nil {
			return false, err
		}
	}
	children, err := e.Children(ctx, iss)
	if err != nil {
		return false, err
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return false, err
	}
	for _, child := range children {
		if child.ProjectID != iss.saved.ProjectID {
			continue
		}
		if err := e.SetProject(ctx, child, p, true); err != nil {
			return false, err
		}
		ok, err := e.save(ctx, child, true, st)
		if err != nil {
			return false, err
		}
		if !ok {
			msgs := child.Errors.FullMessages(e.translator())
			iss.Errors.AddMessage("base", e.translator()("error_move_of_child_not_possible",
				map[string]any{"child": "#" + strconv.FormatInt(child.ID, 10), "errors": joinComma(msgs)}))
			return false, nil
		}
	}
	return true, nil
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// rescheduleFollowingIssues は reschedule_following_issues: 日付が変わったら後続チケットを再スケジュールする。
func (e *Env) rescheduleFollowingIssues(ctx context.Context, iss *Issue, st *saveState) error {
	if !iss.SavedChangeTo("start_date") && !iss.SavedChangeTo("due_date") {
		return nil
	}
	rels, err := e.relationsFrom(ctx, iss.ID)
	if err != nil {
		return err
	}
	for _, r := range rels {
		if err := e.setIssueToDates(ctx, r, iss, iss.currentJournal, st); err != nil {
			return err
		}
	}
	return nil
}

// updateNestedSetAttributesOnParentChange は親変更後の処理: 部分木の無効になった関連の削除と旧親の再計算。
func (e *Env) updateNestedSetAttributesOnParentChange(ctx context.Context, iss *Issue, st *saveState) error {
	ids, err := e.SelfAndDescendantIDs(ctx, iss)
	if err != nil {
		return err
	}
	for _, id := range ids {
		rels, err := e.relationsOf(ctx, id)
		if err != nil {
			return err
		}
		for _, r := range rels {
			ok, err := e.relationValid(ctx, r)
			if err != nil {
				return err
			}
			if !ok {
				if err := e.destroyRelation(ctx, r, st); err != nil {
					return err
				}
			}
		}
	}
	if iss.saved.ParentID != nil {
		return e.recalculateAttributesFor(ctx, *iss.saved.ParentID, st)
	}
	return nil
}

// runCommitCallbacks はコミット後のコールバック (create_parent_issue_journal, add_auto_watcher,
// send_notification, Journal#send_notification) をトランザクションに参加した順に実行する。
func (e *Env) runCommitCallbacks(ctx context.Context, st *saveState) error {
	for i := 0; i < len(st.records); i++ {
		r := st.records[i]
		switch {
		case r.issue != nil:
			if err := e.createParentIssueJournal(ctx, r, st); err != nil {
				return err
			}
			if r.created && !r.destroyed {
				if err := e.addAutoWatcher(ctx, r.issue); err != nil {
					return err
				}
				if err := e.sendIssueAddNotification(ctx, r.issue, st); err != nil {
					return err
				}
			}
		case r.journal != nil:
			if err := e.sendJournalNotification(ctx, r.journal, r.journalIssue, st); err != nil {
				return err
			}
		}
	}
	return nil
}

// createParentIssueJournal は create_parent_issue_journal: 親チケットに child_id の変更を記録する。
func (e *Env) createParentIssueJournal(ctx context.Context, r *commitRecord, st *saveState) error {
	iss := r.issue
	var oldParent, newParent *int64
	switch {
	case r.destroyed:
		if r.withoutNestedSet {
			return nil
		}
		oldParent = r.destroyedParentID
	default:
		if !iss.SavedChangeTo("parent_id") {
			return nil
		}
		oldParent, newParent = iss.saved.ParentID, iss.ParentID
	}
	cur, err := e.currentUser(ctx)
	if err != nil {
		return err
	}
	child := strconv.FormatInt(iss.ID, 10)
	for k, pid := range []*int64{oldParent, newParent} {
		if pid == nil {
			continue
		}
		p, err := e.Find(ctx, *pid)
		if err != nil {
			return err
		}
		if p == nil {
			continue
		}
		if ok, err := e.Visible(ctx, p, cur); err != nil {
			return err
		} else if !ok {
			continue
		}
		j, err := e.InitJournal(ctx, p, cur, "")
		if err != nil {
			return err
		}
		if k == 0 {
			j.AddAttributeDetail("child_id", &child, nil)
		} else {
			j.AddAttributeDetail("child_id", nil, &child)
		}
		// 別トランザクション (コミット後) の保存。そのコミット後コールバックは直後に実行される
		sub := &saveState{result: st.result}
		if _, err := e.save(ctx, p, true, sub); err != nil {
			if errors.Is(err, ErrStale) {
				continue
			}
			return err
		}
		if err := e.runCommitCallbacks(ctx, sub); err != nil {
			return err
		}
	}
	return nil
}

// addAutoWatcher は add_auto_watcher (作成者を自動ウォッチャーにする)。
func (e *Env) addAutoWatcher(ctx context.Context, iss *Issue) error {
	a, err := e.UserByID(ctx, iss.AuthorID)
	if err != nil || a == nil || !a.Active() {
		return err
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return err
	}
	if ok, err := e.allowedTo(ctx, a, "add_issue_watchers", p); err != nil || !ok {
		return err
	}
	aw, err := e.autoWatchOn(ctx, a.ID)
	if err != nil || !slices.Contains(aw, "issue_created") {
		return err
	}
	ids, err := e.WatcherIDs(ctx, iss)
	if err != nil || containsID(ids, a.ID) {
		return err
	}
	return e.AddWatcher(ctx, iss, a.ID)
}

// translator はメッセージの翻訳 (e.Translate。未設定ならキーと変数を連結して返す)。
func (e *Env) translator() domain.Translator {
	if e.Translate != nil {
		return e.Translate
	}
	return func(key string, args ...any) string {
		s := key
		if len(args) > 0 {
			if m, ok := args[0].(map[string]any); ok {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				for _, k := range keys {
					s += fmt.Sprintf(" %s=%v", k, m[k])
				}
			}
		}
		return s
	}
}
