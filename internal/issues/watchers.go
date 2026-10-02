package issues

import (
	"context"

	"github.com/mikuta0407/buropher/internal/domain"
)

// WatcherIDs は保存済みチケットのウォッチャー (watcher_user_ids、id 順)。新規なら watcher_user_ids=。
func (e *Env) WatcherIDs(ctx context.Context, iss *Issue) ([]int64, error) {
	if iss.NewRecord() {
		return iss.watcherUserIDs, nil
	}
	var ids []int64
	err := e.Q.Select(ctx, &ids, `SELECT principal_id FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = ? ORDER BY id`, iss.ID)
	return ids, err
}

// directlyWatchedBy は Watcher.any_watched?([issue], user) (グループ経由は含まない)。
func (e *Env) directlyWatchedBy(ctx context.Context, issueID, principalID int64) (bool, error) {
	var n int
	err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = ? AND principal_id = ?`, issueID, principalID)
	return n > 0, err
}

// WatchedBy は watched_by?(principal) (ユーザなら所属グループ経由も含む)。
func (e *Env) WatchedBy(ctx context.Context, iss *Issue, u *domain.User) (bool, error) {
	ids, err := e.WatcherIDs(ctx, iss)
	if err != nil {
		return false, err
	}
	want := []int64{u.ID}
	gids, err := e.groupIDs(ctx, u)
	if err != nil {
		return false, err
	}
	want = append(want, gids...)
	for _, id := range ids {
		if containsID(want, id) {
			return true, nil
		}
	}
	return false, nil
}

// AddWatcher は add_watcher(principal) (保存済みなら即座に挿入、新規なら watcher_user_ids に追加)。
func (e *Env) AddWatcher(ctx context.Context, iss *Issue, principalID int64) error {
	if iss.NewRecord() {
		if !containsID(iss.watcherUserIDs, principalID) {
			iss.watcherUserIDs = append(iss.watcherUserIDs, principalID)
		}
		return nil
	}
	ok, err := e.validWatcherPrincipal(ctx, principalID)
	if err != nil || !ok {
		return err
	}
	w, err := e.directlyWatchedBy(ctx, iss.ID, principalID)
	if err != nil || w {
		return err
	}
	_, err = e.Q.Exec(ctx, `INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', ?, ?)`, iss.ID, principalID)
	return err
}

// validWatcherPrincipal は Watcher#validate_user (有効なユーザか、組込でないグループ)。
func (e *Env) validWatcherPrincipal(ctx context.Context, id int64) (bool, error) {
	p, err := e.Principal(ctx, id)
	if err != nil || p == nil {
		return false, err
	}
	switch p.Kind {
	case domain.KindUser:
		return p.Active(), nil
	case domain.KindGroup:
		return true, nil
	}
	return false, nil
}

// RemoveWatcher は remove_watcher(principal)。
func (e *Env) RemoveWatcher(ctx context.Context, iss *Issue, principalID int64) error {
	if iss.NewRecord() {
		var out []int64
		for _, id := range iss.watcherUserIDs {
			if id != principalID {
				out = append(out, id)
			}
		}
		iss.watcherUserIDs = out
		return nil
	}
	_, err := e.Q.Exec(ctx, `DELETE FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = ? AND principal_id = ?`, iss.ID, principalID)
	return err
}

// SetWatcher は set_watcher(user, watching)。
func (e *Env) SetWatcher(ctx context.Context, iss *Issue, principalID int64, watching bool) error {
	if watching {
		return e.AddWatcher(ctx, iss, principalID)
	}
	return e.RemoveWatcher(ctx, iss, principalID)
}

// saveNewWatchers は新規作成時に watcher_user_ids を保存する (has_many :through の autosave)。
func (e *Env) saveNewWatchers(ctx context.Context, iss *Issue) error {
	for _, id := range iss.watcherUserIDs {
		p, err := e.Principal(ctx, id)
		if err != nil {
			return err
		}
		if p == nil {
			continue
		}
		if _, err := e.Q.Exec(ctx, `INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', ?, ?)`, iss.ID, id); err != nil {
			return err
		}
	}
	return nil
}

// VisibleWatcherUsers は visible_watcher_users(user) (view_issue_watchers が無ければ自分のみ)。
func (e *Env) VisibleWatcherUsers(ctx context.Context, iss *Issue, u *domain.User) ([]*PrincipalRef, error) {
	ids, err := e.WatcherIDs(ctx, iss)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	ok, err := e.allowedTo(ctx, u, "view_issue_watchers", p)
	if err != nil {
		return nil, err
	}
	if !ok {
		if !containsID(ids, u.ID) {
			return nil, nil
		}
		ids = []int64{u.ID}
	}
	ps, err := e.principalRefs(ctx, `p.id IN (`+inIDs(ids)+`)`)
	if err != nil {
		return nil, err
	}
	// watcher_users の並び (watchers の id 順) に合わせる
	byID := map[int64]*PrincipalRef{}
	for _, x := range ps {
		byID[x.ID] = x
	}
	var out []*PrincipalRef
	for _, id := range ids {
		if x := byID[id]; x != nil {
			out = append(out, x)
		}
	}
	return out, nil
}

// AddableWatcherUsers は addable_watcher_users (プロジェクトの担当可能なウォッチャー候補 - 既存ウォッチャー、閲覧できないユーザを除く)。
func (e *Env) AddableWatcherUsers(ctx context.Context, iss *Issue) ([]*PrincipalRef, error) {
	p, err := e.ProjectOf(ctx, iss)
	if err != nil || p == nil {
		return nil, err
	}
	// project.principals.assignable_watchers: 有効なユーザ / グループ (グループは常に、ユーザは有効なもの)
	ps, err := e.principalRefs(ctx, `p.id IN (SELECT principal_id FROM members WHERE project_id = ?) AND
((p.kind = 'user' AND p.status = 1) OR p.kind = 'group')`, p.ID)
	if err != nil {
		return nil, err
	}
	e.SortPrincipals(ps)
	ids, err := e.WatcherIDs(ctx, iss)
	if err != nil {
		return nil, err
	}
	var out []*PrincipalRef
	for _, x := range ps {
		if containsID(ids, x.ID) {
			continue
		}
		if x.Kind == domain.KindUser {
			u, err := e.UserByID(ctx, x.ID)
			if err != nil {
				return nil, err
			}
			ok, err := e.Visible(ctx, iss, u)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, x)
	}
	return out, nil
}
