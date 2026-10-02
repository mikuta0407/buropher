package testfixtures

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

func ts(c *loadCtx, r row, k string) db.Time { return db.NewTime(r.time(k, c.now)) }

func nts(r row, k string) db.NullTime {
	if !r.has(k) || r.str(k) == "" {
		return db.NullTime{}
	}
	return db.NewNullTime(r.time(k, time.Time{}))
}

func loadIssueStatuses(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO issue_statuses (id, name, description, is_closed, position, default_done_ratio) VALUES (?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.str("name"), r.nstr("description"), r.bool("is_closed", false), r.int("position", 1), r.nint("default_done_ratio")); err != nil {
			return err
		}
	}
	return nil
}

// trackerCoreFields は Tracker::CORE_FIELDS (fields_bits のビット順)。
var trackerCoreFields = []string{"assigned_to_id", "category_id", "fixed_version_id", "parent_issue_id",
	"start_date", "due_date", "estimated_hours", "done_ratio", "description", "priority_id"}

func loadTrackers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		bits := r.int("fields_bits", 0)
		disabled := []string{}
		for i, f := range trackerCoreFields {
			if bits&(1<<i) != 0 {
				disabled = append(disabled, f)
			}
		}
		dj, _ := json.Marshal(disabled)
		if err := c.exec(`INSERT INTO trackers (id, name, description, position, is_in_roadmap, default_status_id, disabled_core_fields) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.str("name"), r.nstr("description"), r.int("position", 1), r.bool("is_in_roadmap", true),
			r.int("default_status_id", 0), string(dj)); err != nil {
			return err
		}
	}
	return nil
}

func loadEnumerations(c *loadCtx, rows []row) error {
	for _, r := range rows {
		id := r.int("id", 0)
		common := []any{id, r.str("name"), r.int("position", 1), r.bool("is_default", false), r.bool("active", true)}
		var err error
		switch r.str("type") {
		case "IssuePriority":
			err = c.exec(`INSERT INTO issue_priorities (id, name, position, is_default, active, position_name) VALUES (?, ?, ?, ?, ?, ?)`,
				append(common, r.nstr("position_name"))...)
		case "TimeEntryActivity":
			err = c.exec(`INSERT INTO time_entry_activities (id, name, position, is_default, active, project_id, parent_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				append(common, r.nint("project_id"), r.nint("parent_id"))...)
		case "DocumentCategory":
			err = c.exec(`INSERT INTO document_categories (id, name, position, is_default, active) VALUES (?, ?, ?, ?, ?)`, common...)
		default:
			// type = Enumeration 等の異常行は新スキーマに行き先が無いので捨てる
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func loadUsers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		id := r.int("id", 0)
		kind := domain.KindFromRedmineType(r.str("type"))
		if kind == "" {
			return fmt.Errorf("%s: unknown users.type %q", r.label, r.str("type"))
		}
		c.kinds[id] = string(kind)
		first, last, name := r.str("firstname"), r.str("lastname"), ""
		if kind.IsGroup() {
			first, last, name = "", "", r.str("lastname")
		}
		if err := c.exec(`INSERT INTO principals (id, kind, status, firstname, lastname, name, twofa_required, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, string(kind), r.int("status", 1), first, last, name, r.bool("twofa_required", false),
			ts(c, r, "created_on"), ts(c, r, "updated_on")); err != nil {
			return err
		}
		if !kind.IsUser() {
			continue
		}
		var hash any
		if h := r.str("hashed_password"); h != "" {
			if salt := r.str("salt"); salt != "" {
				hash = password.RedmineHash(salt, h)
			} else {
				hash = "redmine-sha1-nosalt$$" + h
			}
		}
		if err := c.exec(`INSERT INTO user_accounts (principal_id, login, password_hash, password_changed_at, must_change_password, admin,
  language, auth_source_id, last_login_at, twofa_scheme, twofa_totp_key, twofa_totp_last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.str("login"), hash, nts(r, "passwd_changed_on"), r.bool("must_change_passwd", false), r.bool("admin", false),
			r.nstr("language"), r.nint("auth_source_id"), nts(r, "last_login_on"), r.nstr("twofa_scheme"), r.nstr("twofa_totp_key"),
			r.nint("twofa_totp_last_used_at")); err != nil {
			return err
		}
		if err := c.exec(`INSERT INTO user_notification_settings (user_id, mail_notification) VALUES (?, ?)`,
			id, r.nstr("mail_notification")); err != nil {
			return err
		}
	}
	return nil
}

func loadEmailAddresses(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO email_addresses (id, user_id, address, is_default, notify, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("user_id", 0), r.str("address"), r.bool("is_default", false), r.bool("notify", true),
			ts(c, r, "created_on"), ts(c, r, "updated_on")); err != nil {
			return err
		}
	}
	return nil
}

func loadGroupsUsers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO group_users (group_id, user_id) VALUES (?, ?)`, r.int("group_id", 0), r.int("user_id", 0)); err != nil {
			return err
		}
	}
	return nil
}

func loadProjects(c *loadCtx, rows []row) error {
	parents := map[int64]int64{}
	for _, r := range rows {
		id := r.int("id", 0)
		if p := r.int("parent_id", 0); p != 0 {
			parents[id] = p
		}
		if v := r.int("default_version_id", 0); v != 0 && c.loaded["versions"] {
			c.projectDefaultVersions[id] = v
		}
		var assigned any
		if a := r.int("default_assigned_to_id", 0); a != 0 && c.loaded["users"] {
			assigned = a
		}
		// 兄弟順は fixtures の lft で保持する (position = lft)
		if err := c.exec(`INSERT INTO projects (id, parent_id, name, identifier, description, homepage, is_public, status, inherit_members,
  position, default_assigned_to_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.nint("parent_id"), r.str("name"), r.str("identifier"), r.nstr("description"), r.nstr("homepage"),
			r.bool("is_public", true), r.int("status", domain.ProjectStatusActive), r.bool("inherit_members", false),
			r.int("lft", 0), assigned, ts(c, r, "created_on"), ts(c, r, "updated_on")); err != nil {
			return err
		}
	}
	// 閉包テーブル (自身 depth=0 を含む)
	for _, r := range rows {
		id := r.int("id", 0)
		cur, depth := id, 0
		seen := map[int64]bool{}
		for {
			if err := c.exec(`INSERT INTO project_closure (ancestor_id, descendant_id, depth) VALUES (?, ?, ?)`, cur, id, depth); err != nil {
				return err
			}
			seen[cur] = true
			p, ok := parents[cur]
			if !ok || seen[p] {
				break
			}
			cur, depth = p, depth+1
		}
	}
	return nil
}

func loadEnabledModules(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO project_modules (id, project_id, name) VALUES (?, ?, ?)`, r.int("id", 0), r.int("project_id", 0), r.str("name")); err != nil {
			return err
		}
	}
	return nil
}

func loadProjectsTrackers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO project_trackers (project_id, tracker_id) VALUES (?, ?)`, r.int("project_id", 0), r.int("tracker_id", 0)); err != nil {
			return err
		}
	}
	return nil
}

func loadVersions(c *loadCtx, rows []row) error {
	for _, r := range rows {
		status := r.str("status")
		if status == "" {
			status = "open"
		}
		sharing := r.str("sharing")
		if sharing == "" {
			sharing = "none"
		}
		if err := c.exec(`INSERT INTO versions (id, project_id, name, description, effective_date, wiki_page_title, status, sharing, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), r.str("name"), r.nstr("description"), r.date("effective_date"),
			r.nstr("wiki_page_title"), status, sharing, ts(c, r, "created_on"), ts(c, r, "updated_on")); err != nil {
			return err
		}
	}
	return nil
}

func loadIssueCategories(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO issue_categories (id, project_id, name, assigned_to_id) VALUES (?, ?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), r.str("name"), r.nint("assigned_to_id")); err != nil {
			return err
		}
	}
	return nil
}

// permRe は Role::PermissionsAttributeCoder.load と同じ抽出規則。
var permRe = regexp.MustCompile(`:([a-z0-9_]+)`)

// ParsePermissions は roles.permissions の YAML 文字列から権限名を取り出す (重複除去・順序維持)。
func ParsePermissions(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range permRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// roleSettings は roles.settings (store) の内容。
type roleSettings struct {
	PermissionsAllTrackers map[string]any   `yaml:"permissions_all_trackers"`
	PermissionsTrackerIDs  map[string][]any `yaml:"permissions_tracker_ids"`
}

func loadRoles(c *loadCtx, rows []row) error {
	for _, r := range rows {
		id := r.int("id", 0)
		iv := r.str("issues_visibility")
		if iv == "" {
			iv = domain.IssuesVisibilityDefault
		}
		uv := r.str("users_visibility")
		if uv == "" {
			uv = domain.UsersVisibilityMembersOfVisibleProjects
		}
		tv := r.str("time_entries_visibility")
		if tv == "" {
			tv = domain.TimeEntriesVisibilityAll
		}
		if err := c.exec(`INSERT INTO roles (id, name, position, assignable, builtin, issues_visibility, users_visibility, time_entries_visibility, all_roles_managed)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.str("name"), r.int("position", 1), r.bool("assignable", true), r.int("builtin", 0), iv, uv, tv,
			r.bool("all_roles_managed", true)); err != nil {
			return err
		}
		var st roleSettings
		if s := r.str("settings"); s != "" {
			if err := yaml.Unmarshal([]byte(s), &st); err != nil {
				return fmt.Errorf("%s: settings: %w", r.label, err)
			}
		}
		for _, p := range ParsePermissions(r.str("permissions")) {
			all := fmt.Sprint(st.PermissionsAllTrackers[p]) != "0"
			if err := c.exec(`INSERT INTO role_permissions (role_id, permission, all_trackers) VALUES (?, ?, ?)`, id, p, all); err != nil {
				return err
			}
			if all {
				continue
			}
			seen := map[string]bool{}
			for _, t := range st.PermissionsTrackerIDs[p] {
				ts := strings.TrimSpace(fmt.Sprint(t))
				if ts == "" || seen[ts] {
					continue
				}
				seen[ts] = true
				if err := c.exec(`INSERT INTO role_permission_trackers (role_id, permission, tracker_id) VALUES (?, ?, ?)`, id, p, ts); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func loadMembers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		id, pid, uid := r.int("id", 0), r.int("project_id", 0), r.int("user_id", 0)
		if err := c.exec(`INSERT INTO members (id, project_id, principal_id, created_at) VALUES (?, ?, ?, ?)`,
			id, pid, uid, ts(c, r, "created_on")); err != nil {
			return err
		}
		if r.bool("mail_notification", false) && domain.PrincipalKind(c.kinds[uid]).IsUser() {
			if err := c.exec(`INSERT INTO user_notified_projects (user_id, project_id) VALUES (?, ?)`, uid, pid); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadMemberRoles(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO member_roles (id, member_id, role_id, inherited_from) VALUES (?, ?, ?, ?)`,
			r.int("id", 0), r.int("member_id", 0), r.int("role_id", 0), r.nint("inherited_from")); err != nil {
			return err
		}
	}
	return nil
}

func loadIssues(c *loadCtx, rows []row) error {
	parents := map[int64]int64{}
	for _, r := range rows {
		if p := r.int("parent_id", 0); p != 0 {
			parents[r.int("id", 0)] = p
		}
	}
	for _, r := range rows {
		id := r.int("id", 0)
		// ルートからの経路
		path := []int64{id}
		for cur := id; ; {
			p, ok := parents[cur]
			if !ok || len(path) > 1000 {
				break
			}
			path = append([]int64{p}, path...)
			cur = p
		}
		var hp strings.Builder
		for _, x := range path {
			fmt.Fprintf(&hp, "%010d/", x)
		}
		if err := c.exec(`INSERT INTO issues (id, project_id, tracker_id, status_id, priority_id, author_id, assigned_to_id, category_id,
  fixed_version_id, parent_id, root_id, hier_path, subject, description, start_date, due_date, done_ratio, estimated_hours,
  is_private, lock_version, created_at, updated_at, closed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.int("project_id", 0), r.int("tracker_id", 0), r.int("status_id", 0), r.int("priority_id", 0),
			r.int("author_id", 0), r.nint("assigned_to_id"), r.nint("category_id"), r.nint("fixed_version_id"),
			r.nint("parent_id"), path[0], hp.String(), r.str("subject"), r.nstr("description"),
			r.date("start_date"), r.date("due_date"), r.int("done_ratio", 0), r.float("estimated_hours"),
			r.bool("is_private", false), r.int("lock_version", 0), ts(c, r, "created_on"), ts(c, r, "updated_on"),
			nts(r, "closed_on")); err != nil {
			return err
		}
	}
	return nil
}

func loadWorkflows(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if t := r.str("type"); t != "" && t != "WorkflowTransition" {
			return fmt.Errorf("%s: workflow type %q is not supported", r.label, t)
		}
		var old any
		if o := r.int("old_status_id", 0); o != 0 {
			old = o
		}
		if err := c.exec(`INSERT INTO workflow_transitions (id, tracker_id, role_id, old_status_id, new_status_id, author, assignee) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("tracker_id", 0), r.int("role_id", 0), old, r.int("new_status_id", 0),
			r.bool("author", false), r.bool("assignee", false)); err != nil {
			return err
		}
	}
	return nil
}

var watchableKinds = map[string]string{
	"Issue": "issue", "News": "news", "Board": "board", "Message": "message",
	"Wiki": "wiki", "WikiPage": "wiki_page", "EnabledModule": "project_module",
}

func loadWatchers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		kind, ok := watchableKinds[r.str("watchable_type")]
		if !ok {
			return fmt.Errorf("%s: unknown watchable_type %q", r.label, r.str("watchable_type"))
		}
		args := []any{kind, r.int("watchable_id", 0), r.int("user_id", 0)}
		q := `INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES (?, ?, ?)`
		if id, ok := r.id(); ok {
			q = `INSERT INTO watchers (id, watchable_kind, watchable_id, principal_id) VALUES (?, ?, ?, ?)`
			args = append([]any{id}, args...)
		}
		if err := c.exec(q, args...); err != nil {
			return err
		}
	}
	return nil
}
