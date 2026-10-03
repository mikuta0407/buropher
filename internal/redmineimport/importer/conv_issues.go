// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"fmt"
	"strings"
	"time"
)

// hierPath は ID 列(ルート → 自身)から hier_path を作る。
func hierPath(chain []int64) string {
	var sb strings.Builder
	for _, id := range chain {
		fmt.Fprintf(&sb, "%010d/", id)
	}
	return sb.String()
}

func (im *imp) importIssues() error {
	t := im.table("issues")
	// 1 周目: 取り込める行と親子関係
	parent := map[int64]int64{}
	oldRoot := map[int64]int64{}
	err := im.src.each("issues", func(r rec) error {
		id := r.id()
		if !im.projectExists(r.ref("project_id")) {
			return nil
		}
		parent[id] = r.ref("parent_id")
		oldRoot[id] = r.ref("root_id")
		return nil
	})
	if err != nil {
		return err
	}
	for id, p := range parent {
		if p != 0 {
			if _, ok := parent[p]; !ok || p == id {
				t.repair(id, "parent_id refers to a missing issue; set NULL")
				parent[id] = 0
			}
		}
	}
	for _, id := range breakCycles(parent) {
		t.repair(id, "cyclic parent_id; set NULL")
	}
	chainOf := func(id int64) []int64 {
		var rev []int64
		for cur := id; cur != 0; cur = parent[cur] {
			rev = append(rev, cur)
		}
		for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
			rev[i], rev[j] = rev[j], rev[i]
		}
		return rev
	}

	ins := im.ins(t, "issues", "id", "project_id", "tracker_id", "status_id", "priority_id", "author_id", "assigned_to_id",
		"category_id", "fixed_version_id", "parent_id", "root_id", "hier_path", "subject", "description", "start_date", "due_date",
		"done_ratio", "estimated_hours", "is_private", "lock_version", "created_at", "updated_at", "closed_at")
	err = im.src.each("issues", func(r rec) error {
		id := r.id()
		project := r.ref("project_id")
		if !im.projectExists(project) {
			t.drop(id, "project_id refers to a missing project")
			return nil
		}
		tracker := r.ref("tracker_id")
		if !im.st.trackers.has(tracker) {
			alt := int64(0)
			if pt := im.st.projectTrackers[project]; len(pt) > 0 {
				alt = pt[0]
			} else if len(im.st.trackerOrder) > 0 {
				alt = im.st.trackerOrder[0]
			}
			if alt == 0 {
				return fmt.Errorf("issue %d: no tracker available", id)
			}
			t.repair(id, "tracker_id refers to a missing tracker; set to the project's first tracker")
			tracker = alt
		}
		status := r.ref("status_id")
		if !im.st.statuses.has(status) {
			t.repair(id, "status_id refers to a missing status; set to the tracker's default status")
			status = im.st.trackerDefault[tracker]
		}
		priority := r.ref("priority_id")
		if !im.st.priorities.has(priority) {
			if im.st.defaultPriority == 0 {
				return fmt.Errorf("issue %d: no issue priority available", id)
			}
			t.repair(id, "priority_id refers to a missing priority; set to the default priority")
			priority = im.st.defaultPriority
		}
		author := im.authorOr(t, id, "author_id", r.ref("author_id"))
		var assignee, category, version any
		if a := r.ref("assigned_to_id"); a != 0 {
			if im.principal(a) {
				assignee = a
			} else {
				t.repair(id, "assigned_to_id refers to a missing principal; set NULL")
			}
		}
		if c := r.ref("category_id"); c != 0 {
			if _, ok := im.st.categories[c]; ok {
				category = c
			} else {
				t.repair(id, "category_id refers to a missing category; set NULL")
			}
		}
		if v := r.ref("fixed_version_id"); v != 0 {
			if _, ok := im.st.versions[v]; ok {
				version = v
			} else {
				t.repair(id, "fixed_version_id refers to a missing version; set NULL")
			}
		}
		chain := chainOf(id)
		root := chain[0]
		if oldRoot[id] != root {
			t.repair(id, "root_id inconsistent with parent chain; recomputed")
		}
		ratio := r.intOr("done_ratio", 0)
		if ratio < 0 || ratio > 100 {
			t.repair(id, "done_ratio out of range; clamped")
			ratio = max(0, min(100, ratio))
		}
		var est any
		if v := r.Row["estimated_hours"]; v != nil {
			if f, ok := toFloat(v); ok {
				est = f
			} else {
				t.repair(id, "estimated_hours invalid; set NULL")
			}
		}
		im.st.issues[id] = project
		return ins.add(id, project, tracker, status, priority, author, assignee, category, version,
			nullIfZero(parent[id]), root, hierPath(chain), r.str("subject"), r.strNull("description", false),
			im.dateNull(t, id, r, "start_date"), im.dateNull(t, id, r, "due_date"), ratio, est,
			r.bool("is_private", false), r.intOr("lock_version", 0),
			im.tsOr(t, id, r, "created_on", "updated_on"), im.tsOr(t, id, r, "updated_on", "created_on"), im.tsNull(t, id, r, "closed_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// relationReverse は Redmine が保存時に正方向へ直す関係型(IssueRelation::TYPES の :reverse)。
var relationReverse = map[string]string{
	"duplicated":  "duplicates",
	"blocked":     "blocks",
	"follows":     "precedes",
	"copied_from": "copied_to",
}

var relationTypes = map[string]bool{
	"relates": true, "duplicates": true, "duplicated": true, "blocks": true, "blocked": true,
	"precedes": true, "follows": true, "copied_to": true, "copied_from": true,
}

func (im *imp) importIssueRelations() error {
	t := im.table("issue_relations")
	ins := im.ins(t, "issue_relations", "id", "issue_from_id", "issue_to_id", "relation_type", "delay")
	seen := map[[2]int64]bool{}
	err := im.src.each("issue_relations", func(r rec) error {
		id := r.id()
		from, to := r.ref("issue_from_id"), r.ref("issue_to_id")
		typ := r.str("relation_type")
		if _, ok := im.st.issues[from]; !ok {
			t.drop(id, "issue_from_id refers to a missing issue")
			return nil
		}
		if _, ok := im.st.issues[to]; !ok {
			t.drop(id, "issue_to_id refers to a missing issue")
			return nil
		}
		if !relationTypes[typ] {
			t.drop(id, "unknown relation_type %q", typ)
			return nil
		}
		if from == to {
			t.drop(id, "relation to itself")
			return nil
		}
		if rev, ok := relationReverse[typ]; ok {
			from, to, typ = to, from, rev
			t.repair(id, "reverse relation type normalized")
		} else if typ == "relates" && from > to {
			from, to = to, from
			t.repair(id, "relates ordered by issue id")
		}
		if seen[[2]int64{from, to}] {
			t.drop(id, "duplicate relation between the same issues")
			return nil
		}
		seen[[2]int64{from, to}] = true
		var delay any
		if d, ok := r.int("delay"); ok {
			delay = d
		}
		return ins.add(id, from, to, typ, delay)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importJournals() error {
	t := im.table("journals")
	ins := im.ins(t, "issue_journals", "id", "issue_id", "user_id", "notes", "private_notes", "created_at", "updated_at", "updated_by_id")
	err := im.src.each("journals", func(r rec) error {
		id := r.id()
		if typ := r.str("journalized_type"); typ != "Issue" {
			t.drop(id, "journalized_type %q is not Issue (plugin)", typ)
			return nil
		}
		issue := r.ref("journalized_id")
		if _, ok := im.st.issues[issue]; !ok {
			t.drop(id, "journalized_id refers to a missing issue")
			return nil
		}
		user := im.authorOr(t, id, "user_id", r.ref("user_id"))
		var upd any
		if u := r.ref("updated_by_id"); u != 0 {
			if im.principal(u) {
				upd = u
			} else {
				t.repair(id, "updated_by_id refers to a missing user; set NULL")
			}
		}
		im.st.journals.add(id)
		return ins.add(id, issue, user, r.strNull("notes", false), r.bool("private_notes", false),
			im.tsOr(t, id, r, "created_on"), im.tsNull(t, id, r, "updated_on"), upd)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importJournalDetails() error {
	t := im.table("journal_details")
	ins := im.ins(t, "issue_journal_details", "id", "journal_id", "property", "prop_key", "custom_field_id", "old_value", "value")
	err := im.src.each("journal_details", func(r rec) error {
		id := r.id()
		j := r.ref("journal_id")
		if !im.st.journals.has(j) {
			t.drop(id, "journal_id refers to a missing (or non-issue) journal")
			return nil
		}
		prop := r.str("property")
		key := r.str("prop_key")
		var cf any
		switch prop {
		case "attr", "attachment", "relation":
		case "cf":
			n, ok := toInt(key)
			if !ok {
				t.drop(id, "cf detail with non-numeric prop_key %q", key)
				return nil
			}
			cf = n
		default:
			t.drop(id, "unknown property %q (plugin)", prop)
			return nil
		}
		return ins.add(id, j, prop, key, cf, r.strNull("old_value", false), r.strNull("value", false))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importTimeEntries() error {
	t := im.table("time_entries")
	ins := im.ins(t, "time_entries", "id", "project_id", "user_id", "author_id", "issue_id", "hours", "comments", "activity_id",
		"spent_on", "tyear", "tmonth", "tweek", "created_at", "updated_at")
	err := im.src.each("time_entries", func(r rec) error {
		id := r.id()
		project := r.ref("project_id")
		if !im.projectExists(project) {
			t.drop(id, "project_id refers to a missing project")
			return nil
		}
		user := im.authorOr(t, id, "user_id", r.ref("user_id"))
		author := r.ref("author_id")
		if !im.principal(author) {
			if author == 0 {
				t.repair(id, "author_id NULL; set to user_id")
			} else {
				t.repair(id, "author_id refers to a missing user; set to user_id")
			}
			author = user
		}
		var issue any
		if i := r.ref("issue_id"); i != 0 {
			if _, ok := im.st.issues[i]; ok {
				issue = i
			} else {
				t.repair(id, "issue_id refers to a missing issue; set NULL")
			}
		}
		act := r.ref("activity_id")
		if _, ok := im.st.activities[act]; !ok {
			if p, ok := im.st.activityRemap[act]; ok {
				if _, ok := im.st.activities[p]; ok {
					act = p
					t.repair(id, "activity_id refers to a dropped project activity; set to its parent")
				}
			}
		}
		if _, ok := im.st.activities[act]; !ok {
			if im.st.defaultActivity == 0 {
				t.drop(id, "activity_id refers to a missing activity and no default activity exists")
				return nil
			}
			act = im.st.defaultActivity
			t.repair(id, "activity_id refers to a missing activity; set to the default activity")
		}
		hours, ok := toFloat(r.Row["hours"])
		if !ok {
			hours = 0
			t.repair(id, "hours invalid; set to 0")
		}
		spent, ok := toDate(r.Row["spent_on"])
		if !ok {
			s, _ := im.tz.ts(r.Row["created_on"])
			if len(s) >= 10 {
				spent = s[:10]
			} else {
				spent = im.nowStr[:10]
			}
			t.repair(id, "spent_on invalid; derived from created_on")
		}
		d, _ := time.Parse("2006-01-02", spent)
		_, week := d.ISOWeek()
		im.st.timeEntries.add(id)
		return ins.add(id, project, user, author, issue, hours, r.strNull("comments", false), act, spent,
			d.Year(), int(d.Month()), week, im.tsOr(t, id, r, "created_on", "updated_on"), im.tsOr(t, id, r, "updated_on", "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}
