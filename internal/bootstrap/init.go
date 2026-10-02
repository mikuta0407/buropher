// Package bootstrap は `buropher init`（初期データ投入と管理者作成）を実装する。
//
// Redmine の 001_setup マイグレーション（管理者・組込みロール）、
// 組込みプリンシパルの遅延作成（User.anonymous, GroupBuiltin.load_instance）、
// Redmine::DefaultData::Loader.load をまとめて 1 トランザクションで行う。
package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/defaultdata"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
)

// Options は初期化オプション。
type Options struct {
	Language      string // 既定データの言語（例 "ja"）。空なら "en"
	AdminLogin    string
	AdminPassword string
	AdminEmail    string
	// SkipDefaultData は既定データ（ロール・トラッカー等）を投入しない。
	SkipDefaultData bool
	// SkipWorkflow は既定ワークフローを作らない。
	SkipWorkflow bool
}

// ErrAlreadyInitialized は既に初期化済み。
var ErrAlreadyInitialized = errors.New("bootstrap: database is already initialized")

// Init は DB を初期化する。マイグレーションは呼び出し側で済ませておくこと。
func Init(ctx context.Context, d *db.DB, opt Options) error {
	if opt.Language == "" {
		opt.Language = "en"
	}
	if opt.AdminLogin == "" {
		opt.AdminLogin = "admin"
	}
	if opt.AdminEmail == "" {
		opt.AdminEmail = "admin@example.net"
	}
	if opt.AdminPassword == "" {
		return errors.New("bootstrap: admin password is required")
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM principals`); err != nil {
		return err
	}
	if n > 0 {
		return ErrAlreadyInitialized
	}
	bundle := i18n.Default()
	l := func(key string) string { return bundle.T(opt.Language, key, nil) }
	hash, err := password.Hash(opt.AdminPassword)
	if err != nil {
		return err
	}
	return d.WithTx(ctx, func(tx *db.Tx) error {
		now := db.Now()
		// 組込みプリンシパル
		if _, err := tx.InsertReturningID(ctx, `INSERT INTO principals (kind, status, firstname, lastname, created_at, updated_at) VALUES ('anonymous_user', 0, '', 'Anonymous', ?, ?)`, now, now); err != nil {
			return fmt.Errorf("anonymous user: %w", err)
		}
		for _, g := range []struct{ kind, name string }{{"group_anonymous", "GroupAnonymous"}, {"group_non_member", "GroupNonMember"}} {
			if _, err := tx.InsertReturningID(ctx, `INSERT INTO principals (kind, status, name, created_at, updated_at) VALUES (?, 1, ?, ?, ?)`, g.kind, g.name, now, now); err != nil {
				return fmt.Errorf("builtin group: %w", err)
			}
		}
		// 管理者（Redmine の 001_setup と同じ氏名）
		adminID, err := tx.InsertReturningID(ctx, `INSERT INTO principals (kind, status, firstname, lastname, created_at, updated_at) VALUES ('user', 1, 'Redmine', 'Admin', ?, ?)`, now, now)
		if err != nil {
			return fmt.Errorf("admin: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_accounts (principal_id, login, password_hash, password_changed_at, admin, language) VALUES (?, ?, ?, ?, ?, ?)`,
			adminID, opt.AdminLogin, hash, now, true, opt.Language); err != nil {
			return fmt.Errorf("admin account: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO email_addresses (user_id, address, is_default, notify, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			adminID, opt.AdminEmail, true, true, now, now); err != nil {
			return fmt.Errorf("admin email: %w", err)
		}
		// 組込みロール（Role.non_member / Role.anonymous）
		roleIDs := map[int]int64{}
		for _, r := range []struct {
			builtin  int
			name     string
			position int
		}{{1, "Non member", 1}, {2, "Anonymous", 2}} {
			id, err := tx.InsertReturningID(ctx, `INSERT INTO roles (name, position, builtin) VALUES (?, ?, ?)`, r.name, r.position, r.builtin)
			if err != nil {
				return fmt.Errorf("builtin role: %w", err)
			}
			roleIDs[r.builtin] = id
		}
		if opt.SkipDefaultData {
			return nil
		}
		return loadDefaultData(ctx, tx, defaultdata.Build(l, !opt.SkipWorkflow), roleIDs)
	})
}

func loadDefaultData(ctx context.Context, tx *db.Tx, p *defaultdata.Plan, builtinRoles map[int]int64) error {
	var roleIDs []int64
	for _, r := range p.Roles {
		var id int64
		var err error
		if r.Builtin != 0 {
			id = builtinRoles[r.Builtin]
		} else {
			id, err = tx.InsertReturningID(ctx, `INSERT INTO roles (name, position, issues_visibility, users_visibility) VALUES (?, ?, ?, ?)`,
				r.Name, r.Position, r.IssuesVisibility, r.UsersVisibility)
			if err != nil {
				return fmt.Errorf("role %s: %w", r.Name, err)
			}
			roleIDs = append(roleIDs, id)
		}
		for _, perm := range r.Permissions {
			if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`, id, perm); err != nil {
				return fmt.Errorf("role permission: %w", err)
			}
		}
	}
	statusIDs := make([]int64, len(p.Statuses))
	for i, s := range p.Statuses {
		id, err := tx.InsertReturningID(ctx, `INSERT INTO issue_statuses (name, is_closed, position) VALUES (?, ?, ?)`, s.Name, s.IsClosed, s.Position)
		if err != nil {
			return fmt.Errorf("status: %w", err)
		}
		statusIDs[i] = id
	}
	trackerIDs := make([]int64, len(p.Trackers))
	for i, t := range p.Trackers {
		id, err := tx.InsertReturningID(ctx, `INSERT INTO trackers (name, default_status_id, is_in_roadmap, position) VALUES (?, ?, ?, ?)`,
			t.Name, statusIDs[t.DefaultStatus], t.IsInRoadmap, t.Position)
		if err != nil {
			return fmt.Errorf("tracker: %w", err)
		}
		trackerIDs[i] = id
	}
	for _, w := range p.Transitions {
		if _, err := tx.Exec(ctx, `INSERT INTO workflow_transitions (tracker_id, role_id, old_status_id, new_status_id) VALUES (?, ?, ?, ?)`,
			trackerIDs[w.Tracker], roleIDs[w.Role], statusIDs[w.OldStatus], statusIDs[w.NewStatus]); err != nil {
			return fmt.Errorf("workflow: %w", err)
		}
	}
	for table, es := range map[string][]defaultdata.Enumeration{
		"issue_priorities": p.Priorities, "document_categories": p.DocumentCategories, "time_entry_activities": p.Activities,
	} {
		for _, e := range es {
			if _, err := tx.Exec(ctx, `INSERT INTO `+table+` (name, position, is_default) VALUES (?, ?, ?)`, e.Name, e.Position, e.IsDefault); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
		}
	}
	if err := repository.ComputePriorityPositionNames(ctx, tx); err != nil {
		return err
	}
	for _, q := range p.Queries {
		filters := map[string]any{}
		for _, f := range q.Filters {
			filters[f.Field] = map[string]any{"operator": f.Operator, "values": f.Values}
		}
		fj, _ := json.Marshal(filters)
		var sort, tot any
		if q.SortCriteria != nil {
			b, _ := json.Marshal(q.SortCriteria)
			sort = string(b)
		}
		if q.TotalableNames != nil {
			b, _ := json.Marshal(q.TotalableNames)
			tot = string(b)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO queries (kind, name, visibility, filters, sort_criteria, totalable_names) VALUES (?, ?, ?, ?, ?, ?)`,
			q.Kind, q.Name, q.Visibility, string(fj), sort, tot); err != nil {
			return fmt.Errorf("query: %w", err)
		}
	}
	ids := make([]any, 0, len(p.DefaultProjectsTrackers))
	for _, i := range p.DefaultProjectsTrackers {
		ids = append(ids, fmt.Sprint(trackerIDs[i]))
	}
	st, err := settings.New(ctx, repository.SettingsStore{DB: tx})
	if err != nil {
		return err
	}
	return st.Set(ctx, "default_projects_tracker_ids", ids)
}
