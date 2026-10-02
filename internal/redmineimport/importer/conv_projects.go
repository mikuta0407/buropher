package importer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/redmineimport/rubyyaml"
)

// ---------------------------------------------------------------- projects

// breakCycles は parent マップの循環を切る(循環内の最小 ID の親を 0 にする)。切った ID を返す。
func breakCycles(parent map[int64]int64) []int64 {
	var broken []int64
	state := map[int64]int{} // 0 未訪問, 1 訪問中, 2 済
	ids := make([]int64, 0, len(parent))
	for id := range parent {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, start := range ids {
		var path []int64
		cur := start
		for cur != 0 && state[cur] == 0 {
			state[cur] = 1
			path = append(path, cur)
			cur = parent[cur]
		}
		if cur != 0 && state[cur] == 1 {
			// path 内の cur 以降が循環
			minID := cur
			for i := len(path) - 1; i >= 0 && path[i] != cur; i-- {
				if path[i] < minID {
					minID = path[i]
				}
			}
			parent[minID] = 0
			broken = append(broken, minID)
		}
		for _, p := range path {
			state[p] = 2
		}
	}
	return broken
}

var reIdentifier = regexp.MustCompile(`^[a-z0-9_\-]+$`)

func (im *imp) importProjects() error {
	t := im.table("projects")
	rows, err := im.src.all("projects")
	if err != nil {
		return err
	}
	parent := map[int64]int64{}
	exists := map[int64]bool{}
	for _, r := range rows {
		exists[r.id()] = true
	}
	for _, r := range rows {
		id := r.id()
		p := r.ref("parent_id")
		if p != 0 && (!exists[p] || p == id) {
			t.repair(id, "parent_id refers to a missing project (or itself); set NULL")
			p = 0
		}
		parent[id] = p
	}
	for _, id := range breakCycles(parent) {
		t.repair(id, "cyclic parent_id; set NULL")
	}
	idents := map[string]bool{}
	ins := im.ins(t, "projects", "id", "parent_id", "name", "identifier", "description", "homepage", "is_public", "status",
		"inherit_members", "position", "default_version_id", "default_assigned_to_id", "default_issue_query_id", "created_at", "updated_at")
	for _, r := range rows {
		id := r.id()
		ident := strings.TrimSpace(r.str("identifier"))
		if ident == "" {
			ident = fmt.Sprintf("project-%d", id)
			t.repair(id, "identifier NULL/empty; generated project-<id>")
		} else if !reIdentifier.MatchString(ident) {
			t.repair(id, "identifier %q has characters Redmine no longer allows (kept)", ident)
		}
		if idents[ident] {
			ident = fmt.Sprintf("%s-%d", ident, id)
			t.repair(id, "duplicate identifier; renamed to <identifier>-<id>")
		}
		idents[ident] = true
		status := r.intOr("status", 1)
		switch status {
		case 1, 5, 9, 10:
		default:
			t.repair(id, "invalid status %d; set to 1", status)
			status = 1
		}
		var assignee any
		if a := r.ref("default_assigned_to_id"); a != 0 {
			if im.principal(a) {
				assignee = a
			} else {
				t.repair(id, "default_assigned_to_id refers to a missing principal; set NULL")
			}
		}
		if v := r.ref("default_version_id"); v != 0 {
			im.st.projectDefaultVersion[id] = v
		}
		if q := r.ref("default_issue_query_id"); q != 0 {
			im.st.projectDefaultQuery[id] = q
		}
		im.st.projects[id] = parent[id]
		if err := ins.add(id, nullIfZero(parent[id]), r.str("name"), ident, r.strNull("description", true), r.strNull("homepage", true),
			// 兄弟順は旧 lft の大小で保持する (position = lft。兄弟内の順序だけが意味を持つ)
			r.bool("is_public", true), status, r.bool("inherit_members", false), r.intOr("lft", 0), nil, assignee, nil,
			im.tsOr(t, id, r, "created_on", "updated_on"), im.tsOr(t, id, r, "updated_on", "created_on")); err != nil {
			return err
		}
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	// 閉包テーブル(自分自身 depth=0 を含む)
	cl := im.ins(t, "project_closure", "ancestor_id", "descendant_id", "depth")
	ids := make([]int64, 0, len(parent))
	for id := range parent {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		depth := int64(0)
		for cur := id; cur != 0; cur = parent[cur] {
			if err := cl.add(cur, id, depth); err != nil {
				return err
			}
			depth++
		}
	}
	_, err = cl.close()
	return err
}

// ---------------------------------------------------------------- enabled_modules / projects_trackers

var projectModuleNames = map[string]bool{
	"issue_tracking": true, "time_tracking": true, "news": true, "documents": true, "files": true,
	"wiki": true, "repository": true, "boards": true, "calendar": true, "gantt": true,
}

func (im *imp) importEnabledModules() error {
	t := im.table("enabled_modules")
	ins := im.ins(t, "project_modules", "id", "project_id", "name")
	seen := map[string]bool{}
	err := im.src.each("enabled_modules", func(r rec) error {
		id := r.id()
		p := r.ref("project_id")
		name := r.str("name")
		if !im.projectExists(p) {
			t.drop(id, "project_id NULL or missing")
			return nil
		}
		if !projectModuleNames[name] {
			t.drop(id, "unknown module %q (plugin)", name)
			return nil
		}
		k := fmt.Sprintf("%d/%s", p, name)
		if seen[k] {
			t.drop(id, "duplicate module")
			return nil
		}
		seen[k] = true
		im.st.modules.add(id)
		return ins.add(id, p, name)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importProjectsTrackers() error {
	t := im.table("projects_trackers")
	ins := im.ins(t, "project_trackers", "project_id", "tracker_id")
	seen := map[[2]int64]bool{}
	err := im.src.each("projects_trackers", func(r rec) error {
		p, tr := r.ref("project_id"), r.ref("tracker_id")
		key := fmt.Sprintf("%d-%d", p, tr)
		if !im.projectExists(p) {
			t.drop(key, "project_id refers to a missing project")
			return nil
		}
		if !im.st.trackers.has(tr) {
			t.drop(key, "tracker_id refers to a missing tracker")
			return nil
		}
		if seen[[2]int64{p, tr}] {
			t.drop(key, "duplicate")
			return nil
		}
		seen[[2]int64{p, tr}] = true
		im.st.projectTrackers[p] = append(im.st.projectTrackers[p], tr)
		return ins.add(p, tr)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- custom fields

var cfOwnerKinds = map[string]string{
	"IssueCustomField":             "issue",
	"ProjectCustomField":           "project",
	"TimeEntryCustomField":         "time_entry",
	"VersionCustomField":           "version",
	"DocumentCustomField":          "document",
	"UserCustomField":              "user",
	"GroupCustomField":             "group",
	"IssuePriorityCustomField":     "issue_priority",
	"TimeEntryActivityCustomField": "time_entry_activity",
	"DocumentCategoryCustomField":  "document_category",
}

var cfFormats = map[string]bool{
	"string": true, "text": true, "link": true, "int": true, "float": true, "date": true, "list": true,
	"bool": true, "enumeration": true, "user": true, "version": true, "attachment": true, "progressbar": true,
}

func (im *imp) importCustomFields() error {
	t := im.table("custom_fields")
	rows, err := im.src.all("custom_fields")
	if err != nil {
		return err
	}
	var kept []rec
	for _, r := range rows {
		id := r.id()
		if _, ok := cfOwnerKinds[r.str("type")]; !ok {
			t.drop(id, "unknown custom field type %q", r.str("type"))
			continue
		}
		if !cfFormats[r.str("field_format")] {
			t.drop(id, "unknown field format %q (plugin)", r.str("field_format"))
			continue
		}
		kept = append(kept, r)
	}
	pos := positions(t, kept, "position", func(r rec) any { return r.str("type") })
	names := newNamer(false)
	ins := im.ins(t, "custom_fields", "id", "owner_kind", "name", "description", "field_format", "regexp", "min_length", "max_length",
		"is_required", "is_for_all", "is_filter", "searchable", "default_value", "editable", "visible", "multiple", "position",
		"possible_values", "format_settings")
	for _, r := range kept {
		id := r.id()
		owner := cfOwnerKinds[r.str("type")]
		format := r.str("field_format")
		name := names.take(t, id, owner, r.str("name"), "Custom field %d")
		var pv any
		if v, derr := decodeYAML(r.Row["possible_values"]); derr != nil {
			t.repair(id, "unparseable possible_values YAML; set NULL")
		} else if v != nil {
			list := []string{}
			for _, s := range stringList(v) {
				if s != "" {
					list = append(list, s)
				}
			}
			pv = toJSON(list)
		}
		fs := "{}"
		if v, derr := decodeYAML(r.Row["format_store"]); derr != nil {
			t.repair(id, "unparseable format_store YAML; set {}")
		} else if m, ok := asMap(v); ok {
			out := make(rubyyaml.OrderedMap, 0, len(m))
			for _, it := range m {
				val := it.Value
				if it.Key == "user_role" || it.Key == "version_status" {
					list := []string{}
					for _, s := range stringList(val) {
						if s != "" {
							list = append(list, s)
						}
					}
					val = list
				}
				out = append(out, rubyyaml.MapItem{Key: it.Key, Value: val})
			}
			fs = toJSON(out)
		}
		var minL, maxL any
		if n, ok := r.int("min_length"); ok {
			minL = n
		}
		if n, ok := r.int("max_length"); ok {
			maxL = n
		}
		im.st.customFields[id] = &cfInfo{ownerKind: owner, format: format, multiple: r.bool("multiple", false)}
		if err := ins.add(id, owner, name, r.strNull("description", true), format, r.strNull("regexp", true), minL, maxL,
			r.bool("is_required", false), r.bool("is_for_all", false), r.bool("is_filter", false), r.bool("searchable", false),
			r.strNull("default_value", true), r.bool("editable", true), r.bool("visible", true), r.bool("multiple", false),
			pos[id], pv, fs); err != nil {
			return err
		}
	}
	_, err = ins.close()
	return err
}

func (im *imp) importCustomFieldEnumerations() error {
	t := im.table("custom_field_enumerations")
	ins := im.ins(t, "custom_field_enumerations", "id", "custom_field_id", "name", "active", "position")
	err := im.src.each("custom_field_enumerations", func(r rec) error {
		id := r.id()
		cf := r.ref("custom_field_id")
		if im.st.customFields[cf] == nil {
			t.drop(id, "custom_field_id refers to a missing custom field")
			return nil
		}
		im.st.cfEnumerations.add(id)
		return ins.add(id, cf, r.str("name"), r.bool("active", true), r.intOr("position", 1))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importCustomFieldJoins() error {
	for _, j := range []struct {
		table, col string
		exists     func(int64) bool
	}{
		{"custom_fields_projects", "project_id", im.projectExists},
		{"custom_fields_roles", "role_id", func(id int64) bool { _, ok := im.st.roles[id]; return ok }},
		{"custom_fields_trackers", "tracker_id", im.st.trackers.has},
	} {
		t := im.table(j.table)
		ins := im.ins(t, j.table, "custom_field_id", j.col)
		seen := map[[2]int64]bool{}
		err := im.src.each(j.table, func(r rec) error {
			cf, o := r.ref("custom_field_id"), r.ref(j.col)
			key := fmt.Sprintf("%d-%d", cf, o)
			if im.st.customFields[cf] == nil {
				t.drop(key, "custom_field_id refers to a missing custom field")
				return nil
			}
			if !j.exists(o) {
				t.drop(key, "%s refers to a missing row", j.col)
				return nil
			}
			if seen[[2]int64{cf, o}] {
				t.drop(key, "duplicate")
				return nil
			}
			seen[[2]int64{cf, o}] = true
			return ins.add(cf, o)
		})
		if err != nil {
			return err
		}
		if _, err := ins.close(); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- members

func (im *imp) importMembers() error {
	t := im.table("members")
	ins := im.ins(t, "members", "id", "project_id", "principal_id", "created_at")
	err := im.src.each("members", func(r rec) error {
		id := r.id()
		p, u := r.ref("project_id"), r.ref("user_id")
		if !im.projectExists(p) {
			t.drop(id, "project_id refers to a missing project")
			return nil
		}
		if !im.principal(u) {
			t.drop(id, "user_id refers to a missing principal")
			return nil
		}
		pair := [2]int64{u, p}
		if _, dup := im.st.memberByPair[pair]; dup {
			t.drop(id, "duplicate membership")
			return nil
		}
		im.st.memberByPair[pair] = id
		im.st.members[id] = pair
		if r.bool("mail_notification", false) {
			im.st.memberNotify = append(im.st.memberNotify, id)
		}
		return ins.add(id, p, u, im.tsOr(t, id, r, "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importMemberRoles() error {
	t := im.table("member_roles")
	rows, err := im.src.all("member_roles")
	if err != nil {
		return err
	}
	valid := map[int64]rec{}
	for _, r := range rows {
		id := r.id()
		if _, ok := im.st.members[r.ref("member_id")]; !ok {
			t.drop(id, "member_id refers to a missing member")
			continue
		}
		if _, ok := im.st.roles[r.ref("role_id")]; !ok {
			t.drop(id, "role_id refers to a missing role")
			continue
		}
		valid[id] = r
	}
	// 継承元が消えた派生行は連鎖的に破棄する
	for changed := true; changed; {
		changed = false
		for id, r := range valid {
			if inh := r.ref("inherited_from"); inh != 0 {
				if _, ok := valid[inh]; !ok || inh == id {
					t.drop(id, "inherited_from refers to a missing member role")
					delete(valid, id)
					changed = true
				}
			}
		}
	}
	ins := im.ins(t, "member_roles", "id", "member_id", "role_id", "inherited_from")
	seen := map[[3]int64]bool{}
	for _, r := range rows {
		id := r.id()
		if _, ok := valid[id]; !ok {
			continue
		}
		k := [3]int64{r.ref("member_id"), r.ref("role_id"), r.ref("inherited_from")}
		if seen[k] {
			t.drop(id, "duplicate member role")
			continue
		}
		seen[k] = true
		im.st.memberRoles.add(id)
		if err := ins.add(id, k[0], k[1], nullIfZero(k[2])); err != nil {
			return err
		}
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- versions / categories

var versionStatuses = map[string]bool{"open": true, "locked": true, "closed": true}
var versionSharings = map[string]bool{"none": true, "descendants": true, "hierarchy": true, "tree": true, "system": true}

func (im *imp) importVersions() error {
	t := im.table("versions")
	ins := im.ins(t, "versions", "id", "project_id", "name", "description", "effective_date", "wiki_page_title", "status", "sharing", "created_at", "updated_at")
	names := newNamer(false)
	err := im.src.each("versions", func(r rec) error {
		id := r.id()
		p := r.ref("project_id")
		if !im.projectExists(p) {
			t.drop(id, "project_id refers to a missing project")
			return nil
		}
		status := r.str("status")
		if !versionStatuses[status] {
			t.repair(id, "invalid status %q; set to open", status)
			status = "open"
		}
		sharing := r.str("sharing")
		if !versionSharings[sharing] {
			t.repair(id, "invalid sharing %q; set to none", sharing)
			sharing = "none"
		}
		name := names.take(t, id, p, r.str("name"), "Version %d")
		im.st.versions[id] = p
		return ins.add(id, p, name, r.strNull("description", true), im.dateNull(t, id, r, "effective_date"),
			r.strNull("wiki_page_title", true), status, sharing, im.tsOr(t, id, r, "created_on", "updated_on"), im.tsOr(t, id, r, "updated_on", "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importIssueCategories() error {
	t := im.table("issue_categories")
	ins := im.ins(t, "issue_categories", "id", "project_id", "name", "assigned_to_id")
	names := newNamer(false)
	err := im.src.each("issue_categories", func(r rec) error {
		id := r.id()
		p := r.ref("project_id")
		if !im.projectExists(p) {
			t.drop(id, "project_id refers to a missing project")
			return nil
		}
		var a any
		if u := r.ref("assigned_to_id"); u != 0 {
			if im.principal(u) {
				a = u
			} else {
				t.repair(id, "assigned_to_id refers to a missing principal; set NULL")
			}
		}
		name := names.take(t, id, p, r.str("name"), "Category %d")
		im.st.categories[id] = p
		return ins.add(id, p, name, a)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// updateProjectDefaults は projects.default_version_id を設定する(versions との循環参照のため後段)。
func (im *imp) updateProjectDefaults() error {
	t := im.table("projects")
	for pid, vid := range im.st.projectDefaultVersion {
		if _, ok := im.st.versions[vid]; !ok {
			t.repair(pid, "default_version_id refers to a missing version; set NULL")
			continue
		}
		if _, err := im.exec(`UPDATE projects SET default_version_id = ? WHERE id = ?`, vid, pid); err != nil {
			return err
		}
	}
	return nil
}

func (im *imp) updateProjectDefaultQuery() error {
	t := im.table("projects")
	for pid, qid := range im.st.projectDefaultQuery {
		if im.st.queries[qid] != "issue" {
			t.repair(pid, "default_issue_query_id refers to a missing issue query; set NULL")
			continue
		}
		if _, err := im.exec(`UPDATE projects SET default_issue_query_id = ? WHERE id = ?`, qid, pid); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- workflows

var workflowCoreFields = map[string]bool{
	"project_id": true, "tracker_id": true, "subject": true, "is_private": true, "assigned_to_id": true,
	"category_id": true, "fixed_version_id": true, "parent_issue_id": true, "start_date": true, "due_date": true,
	"estimated_hours": true, "done_ratio": true, "description": true, "priority_id": true,
}

var reDigits = regexp.MustCompile(`^\d+$`)

func (im *imp) importWorkflows() error {
	t := im.table("workflows")
	tins := im.ins(t, "workflow_transitions", "id", "tracker_id", "role_id", "old_status_id", "new_status_id", "author", "assignee")
	fins := im.ins(t, "workflow_field_rules", "id", "tracker_id", "role_id", "status_id", "core_field", "custom_field_id", "rule")
	seenT := map[string]bool{}
	seenF := map[string]bool{}
	err := im.src.each("workflows", func(r rec) error {
		id := r.id()
		tr, role := r.ref("tracker_id"), r.ref("role_id")
		if !im.st.trackers.has(tr) {
			t.drop(id, "tracker_id refers to a missing tracker")
			return nil
		}
		if _, ok := im.st.roles[role]; !ok {
			t.drop(id, "role_id refers to a missing role")
			return nil
		}
		oldS := r.intOr("old_status_id", 0)
		switch r.str("type") {
		case "WorkflowTransition":
			newS := r.ref("new_status_id")
			if oldS != 0 && !im.st.statuses.has(oldS) {
				t.drop(id, "old_status_id refers to a missing status")
				return nil
			}
			if !im.st.statuses.has(newS) {
				t.drop(id, "new_status_id refers to a missing status")
				return nil
			}
			author, assignee := r.bool("author", false), r.bool("assignee", false)
			k := fmt.Sprint(tr, role, oldS, newS, author, assignee)
			if seenT[k] {
				t.drop(id, "duplicate transition")
				return nil
			}
			seenT[k] = true
			return tins.add(id, tr, role, nullIfZero(oldS), newS, author, assignee)
		case "WorkflowPermission":
			if !im.st.statuses.has(oldS) {
				t.drop(id, "status (old_status_id) refers to a missing status")
				return nil
			}
			rule := r.str("rule")
			if rule != "readonly" && rule != "required" {
				t.drop(id, "invalid rule %q", rule)
				return nil
			}
			field := r.str("field_name")
			var core, cf any
			if reDigits.MatchString(field) {
				n, _ := toInt(field)
				if im.st.customFields[n] == nil {
					t.drop(id, "field_name refers to a missing custom field")
					return nil
				}
				cf = n
			} else if workflowCoreFields[field] {
				core = field
			} else {
				t.drop(id, "unknown field_name %q", field)
				return nil
			}
			k := fmt.Sprint(tr, role, oldS, core, cf)
			if seenF[k] {
				t.drop(id, "duplicate field rule")
				return nil
			}
			seenF[k] = true
			return fins.add(id, tr, role, oldS, core, cf, rule)
		default:
			t.drop(id, "unknown workflow type %q", r.str("type"))
			return nil
		}
	})
	if err != nil {
		return err
	}
	if _, err := tins.close(); err != nil {
		return err
	}
	_, err = fins.close()
	return err
}
