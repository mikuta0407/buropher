// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// rolesForWorkflow は roles_for_workflow(user): 管理者は全ロール、それ以外はプロジェクトでのロール。
// ワークフローを考慮するロール (add_issues / edit_issues を持つ) のみ。
func (e *Env) rolesForWorkflow(ctx context.Context, iss *Issue, u *domain.User) ([]*domain.Role, error) {
	var roles []*domain.Role
	// OAuth では admin スコープが無ければ管理者として扱わない (IsAdmin。OAuth 以外では AdminFlag と同じ)
	if u.IsAdmin() {
		rs, err := repository.ListRoles(ctx, e.Q)
		if err != nil {
			return nil, err
		}
		roles = rs
	} else {
		p, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return nil, err
		}
		rs, err := e.authz(u).RolesForProject(ctx, p)
		if err != nil {
			return nil, err
		}
		roles = rs
	}
	var out []*domain.Role
	for _, r := range roles {
		if !r.ConsiderWorkflow() {
			continue
		}
		// OAuth のトークンは add_issues / edit_issues のどちらもスコープに無ければワークフローのロールにしない
		// (notes だけのスコープでステータスを変更できないように。admin スコープは全権。domain.User.OAuthScopeAllows 参照)
		if !u.IsAdmin() && !(r.HasPermission("add_issues") && u.OAuthScopeAllows("add_issues")) &&
			!(r.HasPermission("edit_issues") && u.OAuthScopeAllows("edit_issues")) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func roleIDs(rs []*domain.Role) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// WorkflowRuleByAttribute は workflow_rule_by_attribute(user)。
// 属性名 (コア列名またはカスタムフィールド id の文字列) → "readonly" / "required"。
func (e *Env) WorkflowRuleByAttribute(ctx context.Context, iss *Issue, u *domain.User) (map[string]string, error) {
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return nil, err
		}
	}
	roles, err := e.rolesForWorkflow(ctx, iss, u)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	if len(roles) == 0 {
		return result, nil
	}
	q, args, err := db.In(`SELECT role_id, core_field, custom_field_id, rule FROM workflow_field_rules
WHERE tracker_id = ? AND status_id = ? AND role_id IN (?) ORDER BY id`, iss.TrackerID, iss.StatusID, roleIDs(roles))
	if err != nil {
		return nil, err
	}
	var wps []struct {
		RoleID        int64   `db:"role_id"`
		CoreField     *string `db:"core_field"`
		CustomFieldID *int64  `db:"custom_field_id"`
		Rule          string  `db:"rule"`
	}
	if err := e.Q.Select(ctx, &wps, q, args...); err != nil {
		return nil, err
	}
	if len(wps) == 0 {
		return result, nil
	}
	rules := map[string]map[int64]string{}
	var order []string
	add := func(name string, roleID int64, rule string) {
		if rules[name] == nil {
			rules[name] = map[int64]string{}
			order = append(order, name)
		}
		rules[name][roleID] = rule
	}
	for _, wp := range wps {
		name := ""
		if wp.CoreField != nil {
			name = *wp.CoreField
		} else if wp.CustomFieldID != nil {
			name = strconv.FormatInt(*wp.CustomFieldID, 10)
		}
		add(name, wp.RoleID, wp.Rule)
	}
	// 非表示のカスタムフィールドで、閲覧ロールに含まれないロールは読み取り専用
	var fr []struct {
		FieldID int64 `db:"custom_field_id"`
		RoleID  int64 `db:"role_id"`
	}
	if err := e.Q.Select(ctx, &fr, `SELECT cfr.custom_field_id, cfr.role_id FROM custom_fields cf
JOIN custom_fields_roles cfr ON cfr.custom_field_id = cf.id
WHERE cf.owner_kind = 'issue' AND cf.visible = ? ORDER BY cf.id, cfr.role_id`, false); err != nil {
		return nil, err
	}
	fieldsWithRoles := map[int64][]int64{}
	var fieldOrder []int64
	for _, x := range fr {
		if _, ok := fieldsWithRoles[x.FieldID]; !ok {
			fieldOrder = append(fieldOrder, x.FieldID)
		}
		fieldsWithRoles[x.FieldID] = append(fieldsWithRoles[x.FieldID], x.RoleID)
	}
	for _, r := range roles {
		for _, fid := range fieldOrder {
			if !slices.Contains(fieldsWithRoles[fid], r.ID) {
				add(strconv.FormatInt(fid, 10), r.ID, "readonly")
			}
		}
	}
	for _, name := range order {
		rs := rules[name]
		if len(rs) < len(roles) {
			continue
		}
		var uniq []string
		for _, r := range rs {
			if !slices.Contains(uniq, r) {
				uniq = append(uniq, r)
			}
		}
		if len(uniq) == 1 {
			result[name] = uniq[0]
		} else {
			result[name] = "required"
		}
	}
	return result, nil
}

// ReadOnlyAttributeNames は read_only_attribute_names(user)。
func (e *Env) ReadOnlyAttributeNames(ctx context.Context, iss *Issue, u *domain.User) ([]string, error) {
	return e.attributeNamesWithRule(ctx, iss, u, "readonly")
}

// RequiredAttributeNames は required_attribute_names(user)。
func (e *Env) RequiredAttributeNames(ctx context.Context, iss *Issue, u *domain.User) ([]string, error) {
	return e.attributeNamesWithRule(ctx, iss, u, "required")
}

// RequiredAttribute は required_attribute?(name, user)。
func (e *Env) RequiredAttribute(ctx context.Context, iss *Issue, name string, u *domain.User) (bool, error) {
	names, err := e.RequiredAttributeNames(ctx, iss, u)
	return slices.Contains(names, name), err
}

func (e *Env) attributeNamesWithRule(ctx context.Context, iss *Issue, u *domain.User, rule string) ([]string, error) {
	m, err := e.WorkflowRuleByAttribute(ctx, iss, u)
	if err != nil {
		return nil, err
	}
	var out []string
	for k, v := range m {
		if v == rule {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out, nil
}

// newStatusesAllowed は IssueStatus.new_statuses_allowed(status, roles, tracker, author, assignee)。
func (e *Env) newStatusesAllowed(ctx context.Context, status *domain.IssueStatus, roles []*domain.Role, tracker *domain.Tracker, author, assignee bool) ([]*domain.IssueStatus, error) {
	if len(roles) == 0 || tracker == nil {
		return nil, nil
	}
	cond := "wt.old_status_id IS NULL"
	qargs := []any{tracker.ID}
	if status != nil {
		cond = "wt.old_status_id = ?"
		qargs = append(qargs, status.ID)
	}
	qargs = append(qargs, roleIDs(roles))
	extra := ""
	if !(author && assignee) {
		if author || assignee {
			extra = " AND (wt.author = ? OR wt.assignee = ?)"
			qargs = append(qargs, author, assignee)
		} else {
			extra = " AND wt.author = ? AND wt.assignee = ?"
			qargs = append(qargs, false, false)
		}
	}
	q, a, err := db.In(`SELECT DISTINCT wt.new_status_id FROM workflow_transitions wt
WHERE wt.tracker_id = ? AND `+cond+` AND wt.role_id IN (?)`+extra, qargs...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := e.Q.Select(ctx, &ids, q, a...); err != nil {
		return nil, err
	}
	all, err := e.Statuses(ctx)
	if err != nil {
		return nil, err
	}
	var out []*domain.IssueStatus
	for _, s := range all {
		if slices.Contains(ids, s.ID) {
			out = append(out, s)
		}
	}
	return out, nil
}

// StatusWas は status_was (新規なら nil)。
func (e *Env) StatusWas(ctx context.Context, iss *Issue) (*domain.IssueStatus, error) {
	if iss.orig == nil {
		return nil, nil
	}
	return e.Status(ctx, iss.orig.StatusID)
}

// DefaultStatus は default_status (トラッカーの既定ステータス)。
func (e *Env) DefaultStatus(ctx context.Context, iss *Issue) (*domain.IssueStatus, error) {
	t, err := e.TrackerOf(ctx, iss)
	if err != nil || t == nil {
		return nil, err
	}
	return e.Status(ctx, t.DefaultStatusID)
}

// NewStatusesAllowedTo は new_statuses_allowed_to(user, include_default)。
// 遷移が制限された場合は iss.TransitionWarning に理由 (i18n キー) を設定する。
func (e *Env) NewStatusesAllowedTo(ctx context.Context, iss *Issue, u *domain.User, includeDefault bool) ([]*domain.IssueStatus, error) {
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return nil, err
		}
	}
	var initial *domain.IssueStatus
	var err error
	switch {
	case iss.NewRecord():
	case iss.AttrChanged("tracker_id"):
		var n int
		if err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM trackers WHERE id = ? AND default_status_id = ?`, iss.orig.TrackerID, iss.orig.StatusID); err != nil {
			return nil, err
		}
		if n > 0 {
			initial, err = e.DefaultStatus(ctx, iss)
		} else {
			ids, err2 := e.trackerIssueStatusIDs(ctx, iss.TrackerID)
			if err2 != nil {
				return nil, err2
			}
			if slices.Contains(ids, iss.orig.StatusID) {
				initial, err = e.Status(ctx, iss.orig.StatusID)
			} else {
				initial, err = e.DefaultStatus(ctx, iss)
			}
		}
	default:
		initial, err = e.StatusWas(ctx, iss)
	}
	if err != nil {
		return nil, err
	}
	initialAssigned := iss.AssignedToID
	if iss.AttrChanged("assigned_to_id") {
		if iss.orig != nil {
			initialAssigned = iss.orig.AssignedToID
		} else {
			initialAssigned = nil
		}
	}
	assigneeOK := false
	if initialAssigned != nil {
		if u.ID == *initialAssigned {
			assigneeOK = true
		} else {
			gids, err := e.groupIDs(ctx, u)
			if err != nil {
				return nil, err
			}
			assigneeOK = slices.Contains(gids, *initialAssigned)
		}
	}
	roles, err := e.rolesForWorkflow(ctx, iss, u)
	if err != nil {
		return nil, err
	}
	tracker, err := e.TrackerOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	isAuthor := iss.AuthorID != 0 && iss.AuthorID == u.ID && u.ID != 0
	statuses, err := e.newStatusesAllowed(ctx, initial, roles, tracker, isAuthor, assigneeOK)
	if err != nil {
		return nil, err
	}
	if len(statuses) > 0 && initial != nil {
		statuses = append(statuses, initial)
	}
	if includeDefault || (iss.NewRecord() && len(statuses) == 0) {
		if ds, err := e.DefaultStatus(ctx, iss); err != nil {
			return nil, err
		} else if ds != nil {
			statuses = append(statuses, ds)
		}
	}
	statuses = uniqSortStatuses(statuses)
	closable, warn, err := e.closable(ctx, iss)
	if err != nil {
		return nil, err
	}
	if !closable {
		iss.TransitionWarning = warn
		statuses = slices.DeleteFunc(statuses, func(s *domain.IssueStatus) bool { return s.IsClosed })
	}
	reopenable, warn, err := e.reopenable(ctx, iss)
	if err != nil {
		return nil, err
	}
	if !reopenable {
		iss.TransitionWarning = warn
		statuses = slices.DeleteFunc(statuses, func(s *domain.IssueStatus) bool { return !s.IsClosed })
	}
	return statuses, nil
}

func uniqSortStatuses(ss []*domain.IssueStatus) []*domain.IssueStatus {
	var out []*domain.IssueStatus
	for _, s := range ss {
		if s != nil && !slices.ContainsFunc(out, func(o *domain.IssueStatus) bool { return o.ID == s.ID }) {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b *domain.IssueStatus) int { return a.Position - b.Position })
	return out
}

// closable は closable? (未完了の子孫がある / ブロックされているなら false と理由)。
func (e *Env) closable(ctx context.Context, iss *Issue) (bool, string, error) {
	if iss.Persisted() {
		var n int
		if err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM issues JOIN issue_statuses s ON s.id = issues.status_id
WHERE issues.root_id = ? AND issues.hier_path LIKE ? AND issues.id <> ? AND s.is_closed = ?`,
			iss.orig.RootID, iss.orig.HierPath+"%", iss.ID, false); err != nil {
			return false, "", err
		}
		if n > 0 {
			return false, "notice_issue_not_closable_by_open_tasks", nil
		}
	}
	blocked, err := e.Blocked(ctx, iss)
	if err != nil {
		return false, "", err
	}
	if blocked {
		return false, "notice_issue_not_closable_by_blocking_issue", nil
	}
	return true, "", nil
}

// reopenable は reopenable? (クローズ済みの祖先があれば false と理由)。
func (e *Env) reopenable(ctx context.Context, iss *Issue) (bool, string, error) {
	anc, err := e.ancestorIDs(ctx, iss)
	if err != nil {
		return false, "", err
	}
	if len(anc) == 0 {
		return true, "", nil
	}
	q, args, err := db.In(`SELECT COUNT(*) FROM issues JOIN issue_statuses s ON s.id = issues.status_id
WHERE issues.id IN (?) AND s.is_closed = ?`, anc, true)
	if err != nil {
		return false, "", err
	}
	var n int
	if err := e.Q.Get(ctx, &n, q, args...); err != nil {
		return false, "", err
	}
	if n > 0 {
		return false, "notice_issue_not_reopenable_by_closed_parent_issue", nil
	}
	return true, "", nil
}

// Blocked は blocked? (未完了のチケットからブロックされている)。
func (e *Env) Blocked(ctx context.Context, iss *Issue) (bool, error) {
	if iss.ID == 0 {
		return false, nil
	}
	var n int
	err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM issue_relations r JOIN issues f ON f.id = r.issue_from_id
JOIN issue_statuses s ON s.id = f.status_id
WHERE r.issue_to_id = ? AND r.relation_type = 'blocks' AND s.is_closed = ?`, iss.ID, false)
	return n > 0, err
}
