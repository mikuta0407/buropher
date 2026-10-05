// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/redmineimport/rubyyaml"
)

// namer は (スコープ, 名前) の一意性を保ち、衝突したら "<name> (<id>)" に改名する。
type namer struct {
	seen  map[string]bool
	lower bool
}

func newNamer(lower bool) *namer { return &namer{seen: map[string]bool{}, lower: lower} }

func (n *namer) key(scope any, name string) string {
	if n.lower {
		name = strings.ToLower(name)
	}
	return fmt.Sprint(scope) + "\x00" + name
}

// take は一意な名前を返す。改名した場合は t に記録する。
func (n *namer) take(t *TableReport, id int64, scope any, name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf(fallback, id)
		t.repair(id, "empty name; generated")
	}
	if n.seen[n.key(scope, name)] {
		base := name
		name = fmt.Sprintf("%s (%d)", base, id)
		for i := 2; n.seen[n.key(scope, name)]; i++ {
			name = fmt.Sprintf("%s (%d-%d)", base, id, i)
		}
		t.repair(id, "duplicate name; renamed to \"<name> (<id>)\"")
	}
	n.seen[n.key(scope, name)] = true
	return name
}

// positions は position 列の値を返す。NULL の行は既存の最大値の後ろへ id 順に採番する。
func positions(t *TableReport, rows []rec, col string, scope func(rec) any) map[int64]int64 {
	out := map[int64]int64{}
	maxPos := map[any]int64{}
	for _, r := range rows {
		if p, ok := r.int(col); ok {
			s := any(nil)
			if scope != nil {
				s = scope(r)
			}
			if p > maxPos[s] {
				maxPos[s] = p
			}
			out[r.id()] = p
		}
	}
	sorted := append([]rec(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].id() < sorted[j].id() })
	for _, r := range sorted {
		if _, ok := out[r.id()]; ok {
			continue
		}
		s := any(nil)
		if scope != nil {
			s = scope(r)
		}
		maxPos[s]++
		out[r.id()] = maxPos[s]
		t.repair(r.id(), "%s NULL; appended", col)
	}
	return out
}

// orderBy は position 順(同順位は id 順)に並べた ID を返す。
func orderBy(rows []rec, pos map[int64]int64) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.id())
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if pos[ids[i]] != pos[ids[j]] {
			return pos[ids[i]] < pos[ids[j]]
		}
		return ids[i] < ids[j]
	})
	return ids
}

// ---------------------------------------------------------------- issue_statuses

func (im *imp) importIssueStatuses() error {
	t := im.table("issue_statuses")
	rows, err := im.src.all("issue_statuses")
	if err != nil {
		return err
	}
	pos := positions(t, rows, "position", nil)
	names := newNamer(false)
	ins := im.ins(t, "issue_statuses", "id", "name", "description", "is_closed", "position", "default_done_ratio")
	for _, r := range rows {
		id := r.id()
		var ratio any
		if n, ok := r.int("default_done_ratio"); ok {
			if n < 0 || n > 100 {
				t.repair(id, "default_done_ratio out of range; set NULL")
			} else {
				ratio = n
			}
		}
		name := names.take(t, id, nil, r.str("name"), "Status %d")
		im.st.statuses.add(id)
		if err := ins.add(id, name, r.strNull("description", false), r.bool("is_closed", false), pos[id], ratio); err != nil {
			return err
		}
	}
	im.st.statusOrder = orderBy(rows, pos)
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- trackers

// coreFields は Tracker::CORE_FIELDS(ビット順)。
var coreFields = []string{"assigned_to_id", "category_id", "fixed_version_id", "parent_issue_id",
	"start_date", "due_date", "estimated_hours", "done_ratio", "description", "priority_id"}

func (im *imp) importTrackers() error {
	t := im.table("trackers")
	rows, err := im.src.all("trackers")
	if err != nil {
		return err
	}
	pos := positions(t, rows, "position", nil)
	names := newNamer(false)
	ins := im.ins(t, "trackers", "id", "name", "description", "position", "is_in_roadmap", "private_by_default", "default_status_id", "disabled_core_fields")
	var kept []rec
	for _, r := range rows {
		id := r.id()
		ds := r.ref("default_status_id")
		if !im.st.statuses.has(ds) {
			if len(im.st.statusOrder) == 0 {
				t.drop(id, "no issue statuses exist for default_status_id")
				continue
			}
			ds = im.st.statusOrder[0]
			t.repair(id, "default_status_id missing; set to the first status")
		}
		bits := r.intOr("fields_bits", 0)
		disabled := []string{}
		for i, f := range coreFields {
			if bits&(1<<i) != 0 {
				disabled = append(disabled, f)
			}
		}
		if bits>>len(coreFields) != 0 {
			t.repair(id, "unknown fields_bits ignored")
		}
		name := names.take(t, id, nil, r.str("name"), "Tracker %d")
		im.st.trackers.add(id)
		im.st.trackerDefault[id] = ds
		kept = append(kept, r)
		if err := ins.add(id, name, r.strNull("description", false), pos[id], r.bool("is_in_roadmap", true), r.bool("private_by_default", false), ds, toJSON(disabled)); err != nil {
			return err
		}
	}
	im.st.trackerOrder = orderBy(kept, pos)
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- enumerations

func (im *imp) importEnumerations() error {
	t := im.table("enumerations")
	rows, err := im.src.all("enumerations")
	if err != nil {
		return err
	}
	byType := map[string][]rec{}
	for _, r := range rows {
		switch typ := r.str("type"); typ {
		case "IssuePriority", "DocumentCategory":
			if !r.isNull("project_id") || !r.isNull("parent_id") {
				t.repair(r.id(), "%s with project_id/parent_id; treated as system-wide", typ)
			}
			byType[typ] = append(byType[typ], r)
		case "TimeEntryActivity":
			im.st.pendingActivities = append(im.st.pendingActivities, r)
		default:
			t.drop(r.id(), "unknown enumeration type %q", typ)
		}
	}
	for _, spec := range []struct {
		typ, table string
		set        idset
		fallback   string
	}{
		{"IssuePriority", "issue_priorities", im.st.priorities, "Priority %d"},
		{"DocumentCategory", "document_categories", im.st.docCategories, "Category %d"},
	} {
		rs := byType[spec.typ]
		pos := positions(t, rs, "position", nil)
		names := newNamer(false)
		cols := []string{"id", "name", "position", "is_default", "active"}
		if spec.typ == "IssuePriority" {
			cols = append(cols, "position_name")
		}
		ins := im.ins(t, spec.table, cols...)
		var def int64
		for _, r := range rs {
			id := r.id()
			name := names.take(t, id, spec.typ, r.str("name"), spec.fallback)
			isDef := r.bool("is_default", false)
			if isDef && def == 0 {
				def = id
			}
			vals := []any{id, name, pos[id], isDef, r.bool("active", true)}
			if spec.typ == "IssuePriority" {
				vals = append(vals, nil)
			}
			spec.set.add(id)
			if err := ins.add(vals...); err != nil {
				return err
			}
		}
		order := orderBy(rs, pos)
		if def == 0 && len(order) > 0 {
			def = order[0]
		}
		if spec.typ == "IssuePriority" {
			im.st.priorityOrder = order
			im.st.defaultPriority = def
		} else {
			im.st.defaultDocCategory = def
		}
		if _, err := ins.close(); err != nil {
			return err
		}
	}
	return nil
}

// importActivities は TimeEntryActivity(プロジェクト上書きを含む)を取り込む(projects の後)。
func (im *imp) importActivities() error {
	t := im.table("enumerations")
	rows := im.st.pendingActivities
	byID := map[int64]rec{}
	for _, r := range rows {
		byID[r.id()] = r
	}
	pos := positions(t, rows, "position", func(r rec) any { return r.ref("project_id") })
	names := newNamer(false)
	ins := im.ins(t, "time_entry_activities", "id", "name", "position", "is_default", "active", "project_id", "parent_id")
	// システム活動を先に、上書き行を後に処理する
	sort.SliceStable(rows, func(i, j int) bool {
		pi, pj := rows[i].ref("project_id") != 0, rows[j].ref("project_id") != 0
		if pi != pj {
			return !pi
		}
		return rows[i].id() < rows[j].id()
	})
	for _, r := range rows {
		id := r.id()
		project, parent := r.ref("project_id"), r.ref("parent_id")
		if project != 0 && !im.projectExists(project) {
			t.drop(id, "project override for a missing project")
			if parent != 0 {
				im.st.activityRemap[id] = parent
			}
			continue
		}
		if project != 0 {
			if pa, ok := im.st.activities[parent]; !ok || pa != 0 {
				// 親(システム活動)がない上書き行はシステム活動として残す
				t.repair(id, "project override without a valid parent; kept as an inactive system activity")
				project, parent = 0, 0
				r.Row["active"] = false
			}
		} else if parent != 0 {
			t.repair(id, "parent_id without project_id; cleared")
			parent = 0
		}
		name := names.take(t, id, project, r.str("name"), "Activity %d")
		im.st.activities[id] = project
		isDef := r.bool("is_default", false)
		if project == 0 && isDef && im.st.defaultActivity == 0 {
			im.st.defaultActivity = id
		}
		if err := ins.add(id, name, pos[id], isDef, r.bool("active", true), nullIfZero(project), nullIfZero(parent)); err != nil {
			return err
		}
	}
	if im.st.defaultActivity == 0 {
		var sys []rec
		for _, r := range rows {
			if im.st.activities[r.id()] == 0 {
				if _, ok := im.st.activities[r.id()]; ok {
					sys = append(sys, r)
				}
			}
		}
		if o := orderBy(sys, pos); len(o) > 0 {
			im.st.defaultActivity = o[0]
		}
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	// roles.default_time_entry_activity_id
	rt := im.table("roles")
	for rid, aid := range im.st.roleDefaultActivity {
		if _, ok := im.st.activities[aid]; !ok {
			rt.repair(rid, "default_time_entry_activity_id refers to a missing activity; set NULL")
			continue
		}
		if _, err := im.exec(`UPDATE roles SET default_time_entry_activity_id = ? WHERE id = ?`, aid, rid); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- roles

var rePermission = regexp.MustCompile(`:([a-z0-9_]+)`)

var visibilityValues = map[string][]string{
	"issues_visibility":       {"default", "all", "own"},
	"users_visibility":        {"members_of_visible_projects", "all"},
	"time_entries_visibility": {"all", "own"},
}

func (im *imp) importRoles() error {
	t := im.table("roles")
	rows, err := im.src.all("roles")
	if err != nil {
		return err
	}
	pos := positions(t, rows, "position", nil)
	names := newNamer(false)
	ins := im.ins(t, "roles", "id", "name", "position", "assignable", "builtin", "issues_visibility", "users_visibility",
		"time_entries_visibility", "all_roles_managed", "default_time_entry_activity_id")
	pins := im.ins(t, "role_permissions", "role_id", "permission", "all_trackers", "position")
	tins := im.ins(t, "role_permission_trackers", "role_id", "permission", "tracker_id")
	type permRow struct {
		role     int64
		perm     string
		pos      int
		all      bool
		trackers []int64
	}
	var perms []permRow
	builtins := map[int64]bool{}
	for _, r := range rows {
		id := r.id()
		builtin := r.intOr("builtin", 0)
		if builtin < 0 || builtin > 2 {
			t.repair(id, "invalid builtin %d; set to 0", builtin)
			builtin = 0
		}
		if builtin != 0 {
			if builtins[builtin] {
				t.repair(id, "duplicate built-in role; converted to a normal role")
				builtin = 0
			} else {
				builtins[builtin] = true
			}
		}
		vis := map[string]string{}
		for col, allowed := range visibilityValues {
			v := r.str(col)
			ok := false
			for _, a := range allowed {
				ok = ok || v == a
			}
			if !ok {
				t.repair(id, "invalid %s %q; set to %q", col, v, allowed[0])
				v = allowed[0]
			}
			vis[col] = v
		}
		name := names.take(t, id, nil, r.str("name"), "Role %d")
		if a := r.ref("default_time_entry_activity_id"); a != 0 {
			im.st.roleDefaultActivity[id] = a
		}
		im.st.roles[id] = builtin
		if err := ins.add(id, name, pos[id], r.bool("assignable", true), builtin, vis["issues_visibility"], vis["users_visibility"],
			vis["time_entries_visibility"], r.bool("all_roles_managed", true), nil); err != nil {
			return err
		}
		// permissions(Redmine の PermissionsAttributeCoder と同じく正規表現で抽出)
		var settingsMap rubyyaml.OrderedMap
		if v, derr := decodeYAML(r.Row["settings"]); derr != nil {
			t.repair(id, "unparseable settings YAML; tracker restrictions ignored")
		} else {
			settingsMap, _ = asMap(v)
		}
		allTrackers, _ := settingsMap.Get("permissions_all_trackers")
		allMap, _ := asMap(allTrackers)
		trackerIDs, _ := settingsMap.Get("permissions_tracker_ids")
		idsMap, _ := asMap(trackerIDs)
		seen := map[string]bool{}
		for _, m := range rePermission.FindAllStringSubmatch(r.str("permissions"), -1) {
			p := m[1]
			if seen[p] {
				continue
			}
			seen[p] = true
			// 未知の権限（廃止された権限・プラグインの権限）も Redmine と同じく保持する。
			// Redmine は roles.permissions をそのまま API に出し、権限判定・画面では無視する
			// （ロールの編集画面から保存すると消える）。
			// position は YAML 配列内の順（Redmine は保存順のまま API 等に出す）
			pr := permRow{role: id, perm: p, pos: len(seen), all: true}
			if v, ok := allMap.Get(p); ok {
				if s, _ := toStr(rubyyaml.Plain(v)); s == "0" {
					pr.all = false
				}
			}
			if !pr.all {
				tv, _ := idsMap.Get(p)
				tseen := map[int64]bool{}
				for _, s := range stringList(tv) {
					if s == "" {
						continue
					}
					tid, ok := toInt(s)
					if !ok || !im.st.trackers.has(tid) {
						t.repair(id, "permissions_tracker_ids: missing tracker %q dropped", s)
						continue
					}
					if !tseen[tid] {
						tseen[tid] = true
						pr.trackers = append(pr.trackers, tid)
					}
				}
			}
			perms = append(perms, pr)
		}
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	for _, p := range perms {
		if err := pins.add(p.role, p.perm, p.all, p.pos); err != nil {
			return err
		}
	}
	if _, err := pins.close(); err != nil {
		return err
	}
	for _, p := range perms {
		for _, tid := range p.trackers {
			if err := tins.add(p.role, p.perm, tid); err != nil {
				return err
			}
		}
	}
	_, err = tins.close()
	return err
}

func (im *imp) importRolesManagedRoles() error {
	t := im.table("roles_managed_roles")
	ins := im.ins(t, "roles_managed_roles", "role_id", "managed_role_id")
	seen := map[[2]int64]bool{}
	err := im.src.each("roles_managed_roles", func(r rec) error {
		a, b := r.ref("role_id"), r.ref("managed_role_id")
		key := fmt.Sprintf("%d-%d", a, b)
		if _, ok := im.st.roles[a]; !ok {
			t.drop(key, "role_id refers to a missing role")
			return nil
		}
		if _, ok := im.st.roles[b]; !ok {
			t.drop(key, "managed_role_id refers to a missing role")
			return nil
		}
		if seen[[2]int64{a, b}] {
			t.drop(key, "duplicate")
			return nil
		}
		seen[[2]int64{a, b}] = true
		return ins.add(a, b)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}
