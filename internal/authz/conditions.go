// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package authz

import (
	"context"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/permission"
)

// ConditionOptions は Project.allowed_to_condition のオプション。
type ConditionOptions struct {
	// SkipPreCondition はモジュール有効の確認を省く (:skip_pre_condition)。
	SkipPreCondition bool
	// Project は条件をこのプロジェクト (と WithSubprojects なら子孫) に限定する (:project)。
	Project *domain.Project
	// WithSubprojects は Project の子孫も含める (:with_subprojects)。
	WithSubprojects bool
	// Member は自分がメンバーのプロジェクトに限定する (:member)。
	Member bool
}

// ConditionFilter は allowed_to_condition のブロックに相当する。ロールごとの追加 SQL 条件を返す。
// "" を返すと追加条件なし (Ruby の nil)。
type ConditionFilter func(role *domain.Role, user *domain.User) string

// idList は整数 id を "1,2,3" 形式にする。
func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// ProjectCondition は Project#project_condition(with_subprojects) の移植。
// lft/rgt の代わりに閉包テーブルを使う。
func ProjectCondition(p *domain.Project, withSubprojects bool) string {
	id := strconv.FormatInt(p.ID, 10)
	if withSubprojects {
		return "projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + id + ")"
	}
	return "projects.id = " + id
}

// AllowedToCondition は Project.allowed_to_condition(user, permission, options) の移植。
// projects テーブル (名前 projects) を参照する SQL の WHERE 句断片を返す。
// 値はすべて SQL に埋め込まれる (整数 id と固定文字列のみ) のでプレースホルダ引数は無い。
func (a *Authorizer) AllowedToCondition(ctx context.Context, perm string, opts ConditionOptions, filter ConditionFilter) (string, error) {
	d := a.q.Dialect()
	pd := permission.Get(perm)
	var base string
	if pd != nil && pd.Read {
		base = "projects.status IN (" + strconv.Itoa(domain.ProjectStatusActive) + ", " + strconv.Itoa(domain.ProjectStatusClosed) + ")"
	} else {
		base = "projects.status = " + strconv.Itoa(domain.ProjectStatusActive)
	}
	if !opts.SkipPreCondition && pd != nil && pd.Module != "" {
		base += " AND EXISTS (SELECT 1 AS one FROM project_modules em" +
			" WHERE em.project_id = projects.id" +
			" AND em.name='" + pd.Module + "')"
	}
	if opts.Project != nil {
		base = "(" + ProjectCondition(opts.Project, opts.WithSubprojects) + ") AND (" + base + ")"
	}
	if a.user.IsAdmin() {
		return base, nil
	}
	action := domain.Perm(perm)
	type stmt struct {
		role *domain.Role
		sql  string
	}
	var stmts []stmt
	if !opts.Member {
		role, err := a.BuiltinRole(ctx)
		if err != nil {
			return "", err
		}
		if role.AllowedTo(action, nil) {
			s := "projects.is_public = " + d.BoolLiteral(true)
			if a.user.ID != 0 {
				gid, err := a.builtinGroup(ctx)
				if err != nil {
					return "", err
				}
				s = "(" + s + " AND projects.id NOT IN " +
					"(SELECT project_id FROM members " +
					"WHERE principal_id IN (" + idList([]int64{a.user.ID, gid}) + ")))"
			}
			stmts = append(stmts, stmt{role, s})
		}
	}
	pibr, err := a.ProjectIDsByRole(ctx)
	if err != nil {
		return "", err
	}
	for _, rp := range pibr {
		if rp.Role.AllowedTo(action, nil) && len(rp.ProjectIDs) > 0 {
			stmts = append(stmts, stmt{rp.Role, "projects.id IN (" + idList(rp.ProjectIDs) + ")"})
		}
	}
	if len(stmts) == 0 {
		return "1=0", nil
	}
	parts := make([]string, len(stmts))
	for i, s := range stmts {
		parts[i] = s.sql
		if filter != nil {
			if extra := filter(s.role, a.user); extra != "" {
				parts[i] = "(" + s.sql + " AND (" + extra + "))"
			}
		}
	}
	return "((" + base + ") AND (" + strings.Join(parts, " OR ") + "))", nil
}

// VisibleCondition は Project.visible_condition(user, options) (= allowed_to_condition(:view_project))。
func (a *Authorizer) VisibleCondition(ctx context.Context, opts ConditionOptions) (string, error) {
	return a.AllowedToCondition(ctx, "view_project", opts, nil)
}

// IssueVisibleCondition は Issue.visible_condition(user, options) の移植。
// issues と projects (名前 issues, projects) を参照する WHERE 句断片を返す。
func (a *Authorizer) IssueVisibleCondition(ctx context.Context, opts ConditionOptions) (string, error) {
	d := a.q.Dialect()
	var userIDs string
	if a.user.ID != 0 && a.user.Logged() {
		gids, err := a.GroupIDs(ctx)
		if err != nil {
			return "", err
		}
		userIDs = idList(append([]int64{a.user.ID}, gids...))
	}
	return a.AllowedToCondition(ctx, "view_issues", opts, func(role *domain.Role, user *domain.User) string {
		var sql string
		if user.ID != 0 && user.Logged() {
			uid := strconv.FormatInt(user.ID, 10)
			switch role.IssuesVisibility {
			case domain.IssuesVisibilityAll:
				sql = "1=1"
			case domain.IssuesVisibilityDefault:
				sql = "(issues.is_private = " + d.BoolLiteral(false) + " " +
					"OR issues.author_id = " + uid + " " +
					"OR issues.assigned_to_id IN (" + userIDs + "))"
			case domain.IssuesVisibilityOwn:
				sql = "(issues.author_id = " + uid + " OR " +
					"issues.assigned_to_id IN (" + userIDs + "))"
			default:
				sql = "1=0"
			}
		} else {
			sql = "(issues.is_private = " + d.BoolLiteral(false) + ")"
		}
		if !role.PermissionsAllTrackers("view_issues") {
			if tids := role.PermissionsTrackerIDs("view_issues"); len(tids) > 0 {
				sql = "(" + sql + " AND issues.tracker_id IN (" + idList(tids) + "))"
			} else {
				sql = "1=0"
			}
		}
		return sql
	})
}

// IssueVisible は Issue#visible?(user) の移植。
func (a *Authorizer) IssueVisible(ctx context.Context, issue *domain.Issue, p *domain.Project) (bool, error) {
	var assignedOK bool
	if issue.AssignedToID != nil {
		ok, err := a.IsOrBelongsTo(ctx, *issue.AssignedToID)
		if err != nil {
			return false, err
		}
		assignedOK = ok
	}
	return a.AllowedToWith(ctx, domain.Perm("view_issues"), p, func(role *domain.Role, user *domain.User) bool {
		var visible bool
		if user.Logged() {
			switch role.IssuesVisibility {
			case domain.IssuesVisibilityAll:
				visible = true
			case domain.IssuesVisibilityDefault:
				visible = !issue.IsPrivate || issue.AuthorID == user.ID || assignedOK
			case domain.IssuesVisibilityOwn:
				visible = issue.AuthorID == user.ID || assignedOK
			}
		} else {
			visible = !issue.IsPrivate
		}
		if !role.PermissionsAllTrackers("view_issues") {
			visible = visible && role.PermissionsTrackerIDsInclude("view_issues", issue.TrackerID)
		}
		return visible
	})
}

// UserTrackerPermission は Issue#user_tracker_permission?(user, permission) の移植。
// プロジェクトが非アクティブなら read 権限以外は false。管理者は true。
// それ以外は権限を持つロールのいずれかが全トラッカーまたは trackerID を許可していれば true。
func (a *Authorizer) UserTrackerPermission(ctx context.Context, p *domain.Project, trackerID int64, perm string) (bool, error) {
	if p != nil && !p.Active() {
		pd := permission.Get(perm)
		if pd == nil || !pd.Read {
			return false, nil
		}
	}
	if a.user.IsAdmin() {
		return true, nil
	}
	roles, err := a.RolesForProject(ctx, p)
	if err != nil {
		return false, err
	}
	for _, r := range roles {
		if r.HasPermission(perm) && (r.PermissionsAllTrackers(perm) || r.PermissionsTrackerIDsInclude(perm, trackerID)) {
			return true, nil
		}
	}
	return false, nil
}

// PrincipalVisibleCondition は Principal.visible(user) スコープの移植。
// principals テーブル (名前 principals) を参照する WHERE 句断片を返す。
//   - 管理者: 全件 ("1=1")
//   - メンバーシップのロール (無ければ組込ロール) に users_visibility = 'all' があれば有効な全件
//   - それ以外: 有効なもののうち自身と可視プロジェクトのメンバー
//
// 結果はこの Authorizer (1 リクエスト) の間キャッシュする (ユーザー名のリンクごとに呼ばれるため)。
func (a *Authorizer) PrincipalVisibleCondition(ctx context.Context) (string, error) {
	if a.pvcLoaded {
		return a.principalVisCond, nil
	}
	c, err := a.principalVisibleCondition(ctx)
	if err != nil {
		return "", err
	}
	a.principalVisCond, a.pvcLoaded = c, true
	return c, nil
}

func (a *Authorizer) principalVisibleCondition(ctx context.Context) (string, error) {
	if a.user.IsAdmin() {
		return "1=1", nil
	}
	active := "principals.status = " + strconv.Itoa(domain.StatusActive)
	viewAll := false
	var n int
	if a.user.ID != 0 {
		if err := a.q.Get(ctx, &n, `SELECT COUNT(*) FROM members m JOIN projects ON projects.id = m.project_id
WHERE m.principal_id = ? AND projects.status <> ?`, a.user.ID, domain.ProjectStatusArchived); err != nil {
			return "", err
		}
	}
	if n > 0 {
		var c int
		if err := a.q.Get(ctx, &c, `SELECT COUNT(*) FROM members m JOIN projects ON projects.id = m.project_id
JOIN member_roles mr ON mr.member_id = m.id JOIN roles r ON r.id = mr.role_id
WHERE m.principal_id = ? AND projects.status <> ? AND r.users_visibility = ?`,
			a.user.ID, domain.ProjectStatusArchived, domain.UsersVisibilityAll); err != nil {
			return "", err
		}
		viewAll = c > 0
	} else {
		br, err := a.BuiltinRole(ctx)
		if err != nil {
			return "", err
		}
		viewAll = br.UsersVisibility == domain.UsersVisibilityAll
	}
	if viewAll {
		return active, nil
	}
	vp, err := a.VisibleProjectIDs(ctx)
	if err != nil {
		return "", err
	}
	// Rails は空配列の IN (?) を IN (NULL) に展開する
	inList := "NULL"
	if len(vp) > 0 {
		inList = idList(vp)
	}
	uid := "NULL"
	if a.user.ID != 0 {
		uid = strconv.FormatInt(a.user.ID, 10)
	}
	return "(" + active + " AND (principals.id = " + uid +
		" OR principals.id IN (SELECT principal_id FROM members WHERE project_id IN (" + inList + "))))", nil
}
