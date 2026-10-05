// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"fmt"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

// recompute は導出値(position_name)を再計算する。
// カウンタキャッシュ(comments_count 等)は Redmine が画面に表示する値なので、
// 実件数とずれていても移行元の値をそのまま保持する(表示の互換を優先)。
func (im *imp) recompute() error {
	if err := repository.ComputePriorityPositionNames(im.ctx, im.tx); err != nil {
		return err
	}
	// boards.last_message_id / messages.last_reply_id は参照先メッセージが取り込まれていれば移行元の値を設定する
	for _, f := range im.lastIDs {
		if !im.st.messages.has(f.ref) {
			continue
		}
		if _, err := im.exec(`UPDATE `+f.table+` SET `+f.col+` = ? WHERE id = ?`, f.ref, f.id); err != nil {
			return err
		}
	}
	return nil
}

// lastIDFix は後から設定する last_*_id。
type lastIDFix struct {
	table, col string
	id, ref    int64
}

// idTables は id 主キー(採番)を持つテーブル。
var idTables = []string{
	"auth_sources", "principals", "email_addresses", "auth_source_group_mappings", "user_identities", "tokens",
	"twofa_backup_codes", "issue_statuses", "trackers", "projects", "project_modules", "issue_priorities",
	"document_categories", "time_entry_activities", "roles", "members", "member_roles", "custom_fields",
	"custom_field_enumerations", "custom_values", "workflow_transitions", "workflow_field_rules", "versions",
	"issue_categories", "issues", "issue_relations", "issue_journals", "issue_journal_details", "watchers",
	"time_entries", "wikis", "wiki_pages", "wiki_page_versions", "wiki_redirects", "boards", "messages", "news",
	"news_comments", "documents", "reactions", "attachments", "queries", "legacy_settings", "repositories",
	"changesets", "changeset_files", "imports", "import_items", "oauth_applications", "oauth_access_grants",
	"oauth_access_tokens", "jobs", "notification_deliveries", "webhooks",
}

func (im *imp) resetSequences() error {
	for _, t := range idTables {
		if err := im.tx.Dialect().ResetSequence(im.ctx, im.tx, t); err != nil {
			return fmt.Errorf("reset sequence %s: %w", t, err)
		}
	}
	// 破棄した行の ID を再利用しないよう、移行元テーブルの最大 ID まで採番を進める
	// (Redmine 上で次に作られるはずの ID と揃う)。
	for target, src := range sequenceSources {
		var max int64
		if err := im.src.each(src, func(r rec) error {
			if id := r.id(); id > max {
				max = id
			}
			return nil
		}); err != nil {
			return err
		}
		if max == 0 {
			continue
		}
		if err := im.tx.Dialect().AdvanceSequence(im.ctx, im.tx, target, max); err != nil {
			return fmt.Errorf("advance sequence %s: %w", target, err)
		}
	}
	return nil
}

// sequenceSources は採番を移行元の最大 ID まで進めるテーブル (移行先 → 移行元)。
// enumerations は 3 テーブルで ID 空間を共有するので全て enumerations の最大値に揃える。
var sequenceSources = map[string]string{
	"principals": "users", "auth_sources": "auth_sources", "email_addresses": "email_addresses",
	"tokens": "tokens", "issue_statuses": "issue_statuses", "trackers": "trackers", "projects": "projects",
	"project_modules": "enabled_modules", "issue_priorities": "enumerations", "document_categories": "enumerations",
	"time_entry_activities": "enumerations", "roles": "roles", "members": "members", "member_roles": "member_roles",
	"custom_fields": "custom_fields", "custom_field_enumerations": "custom_field_enumerations",
	"custom_values": "custom_values", "workflow_transitions": "workflows", "workflow_field_rules": "workflows",
	"versions": "versions", "issue_categories": "issue_categories", "issues": "issues",
	"issue_relations": "issue_relations", "issue_journals": "journals", "issue_journal_details": "journal_details",
	"watchers": "watchers", "time_entries": "time_entries", "wikis": "wikis", "wiki_pages": "wiki_pages",
	"wiki_redirects": "wiki_redirects", "boards": "boards", "messages": "messages", "news": "news",
	"news_comments": "comments", "documents": "documents", "reactions": "reactions", "attachments": "attachments",
	"queries": "queries", "repositories": "repositories", "changesets": "changesets", "changeset_files": "changes",
	"oauth_applications": "oauth_applications", "webhooks": "webhooks",
}

// polyChecks はポリモーフィック参照の孤児検出(種別 → 参照先テーブル)。
var polyChecks = []struct {
	table, kindCol, idCol string
	targets               map[string][]string
}{
	{"attachments", "container_kind", "container_id", map[string][]string{
		"issue": {"issues"}, "project": {"projects"}, "version": {"versions"}, "wiki_page": {"wiki_pages"},
		"message": {"messages"}, "news": {"news"}, "document": {"documents"}, "custom_value": {"custom_values"}}},
	{"custom_values", "customized_kind", "customized_id", map[string][]string{
		"issue": {"issues"}, "project": {"projects"}, "time_entry": {"time_entries"}, "version": {"versions"},
		"document": {"documents"}, "principal": {"principals"},
		"enumeration": {"issue_priorities", "time_entry_activities", "document_categories"}}},
	{"watchers", "watchable_kind", "watchable_id", map[string][]string{
		"issue": {"issues"}, "news": {"news"}, "board": {"boards"}, "message": {"messages"}, "wiki": {"wikis"},
		"wiki_page": {"wiki_pages"}, "project_module": {"project_modules"}}},
	{"reactions", "reactable_kind", "reactable_id", map[string][]string{
		"issue": {"issues"}, "journal": {"issue_journals"}, "news": {"news"}, "message": {"messages"}, "comment": {"news_comments"}}},
}

func (im *imp) checks() error {
	// 1. 外部キー
	fk := Check{Name: "foreign keys", OK: true}
	switch im.tx.Dialect().Name() {
	case db.SQLite:
		rows, err := im.tx.Query(im.ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		for rows.Next() {
			var table, parent string
			var rowid, fkid any
			if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
				rows.Close()
				return err
			}
			fk.OK = false
			if len(fk.Detail) < 20 {
				fk.Detail = append(fk.Detail, fmt.Sprintf("%s rowid %v -> %s", table, rowid, parent))
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	case db.Postgres:
		if _, err := im.tx.Exec(im.ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
			fk.OK = false
			fk.Detail = append(fk.Detail, err.Error())
			im.rep.Checks = append(im.rep.Checks, fk)
			return nil // トランザクションは中断済み。以降のチェックは行わない
		}
		if _, err := im.tx.Exec(im.ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
			return err
		}
	}
	im.rep.Checks = append(im.rep.Checks, fk)

	// 2. ポリモーフィック孤児
	poly := Check{Name: "polymorphic references", OK: true}
	for _, pc := range polyChecks {
		for kind, targets := range pc.targets {
			q := fmt.Sprintf("SELECT COUNT(*) FROM %s x WHERE x.%s = ?", pc.table, pc.kindCol)
			for _, t := range targets {
				q += fmt.Sprintf(" AND NOT EXISTS (SELECT 1 FROM %s y WHERE y.id = x.%s)", t, pc.idCol)
			}
			var n int64
			if err := im.tx.Get(im.ctx, &n, q, kind); err != nil {
				return err
			}
			if n > 0 {
				poly.OK = false
				poly.Detail = append(poly.Detail, fmt.Sprintf("%s: %d orphan %s rows", pc.table, n, kind))
			}
		}
		var n int64
		kinds := make([]any, 0, len(pc.targets))
		ph := ""
		for k := range pc.targets {
			kinds = append(kinds, k)
			if ph != "" {
				ph += ", "
			}
			ph += "?"
		}
		if err := im.tx.Get(im.ctx, &n, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND %s NOT IN (%s)", pc.table, pc.kindCol, pc.kindCol, ph), kinds...); err != nil {
			return err
		}
		if n > 0 {
			poly.OK = false
			poly.Detail = append(poly.Detail, fmt.Sprintf("%s: %d rows with unknown kind", pc.table, n))
		}
	}
	im.rep.Checks = append(im.rep.Checks, poly)

	// 3. 階層(issues の root_id / hier_path、project_closure)
	hier := Check{Name: "hierarchies", OK: true}
	for _, c := range []struct{ name, q string }{
		{"issues whose root_id differs from the parent's", `SELECT COUNT(*) FROM issues c JOIN issues p ON p.id = c.parent_id WHERE c.root_id <> p.root_id`},
		{"issues whose hier_path does not extend the parent's", `SELECT COUNT(*) FROM issues c JOIN issues p ON p.id = c.parent_id WHERE substr(c.hier_path, 1, length(p.hier_path)) <> p.hier_path OR length(c.hier_path) <> length(p.hier_path) + 11`},
		{"root issues with root_id <> id", `SELECT COUNT(*) FROM issues WHERE parent_id IS NULL AND root_id <> id`},
		{"projects without a self closure row", `SELECT COUNT(*) FROM projects p WHERE NOT EXISTS (SELECT 1 FROM project_closure c WHERE c.ancestor_id = p.id AND c.descendant_id = p.id AND c.depth = 0)`},
		{"projects whose parent closure row is missing", `SELECT COUNT(*) FROM projects p WHERE p.parent_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM project_closure c WHERE c.ancestor_id = p.parent_id AND c.descendant_id = p.id AND c.depth = 1)`},
	} {
		var n int64
		if err := im.tx.Get(im.ctx, &n, c.q); err != nil {
			return err
		}
		if n > 0 {
			hier.OK = false
			hier.Detail = append(hier.Detail, fmt.Sprintf("%d %s", n, c.name))
		}
	}
	im.rep.Checks = append(im.rep.Checks, hier)

	// 4. member_roles の継承行(グループ所属・inherit_members から期待される派生行との差分。警告のみ)
	missingGroup, missingInherit, unexpected, err := im.memberRoleInheritance()
	if err != nil {
		return err
	}
	if missingGroup+missingInherit+unexpected > 0 {
		im.rep.warnf("member_roles inheritance differs from what Redmine would derive: %d missing group-derived rows, %d missing inherit_members rows, %d unexplained inherited rows (kept as is)",
			missingGroup, missingInherit, unexpected)
	}
	return nil
}

func (im *imp) memberRoleInheritance() (missingGroup, missingInherit, unexpected int64, err error) {
	q1 := `SELECT COUNT(*) FROM member_roles mr
		JOIN members m ON m.id = mr.member_id
		JOIN group_users gu ON gu.group_id = m.principal_id
		WHERE NOT EXISTS (SELECT 1 FROM member_roles x JOIN members um ON um.id = x.member_id
			WHERE um.principal_id = gu.user_id AND um.project_id = m.project_id AND x.role_id = mr.role_id AND x.inherited_from = mr.id)`
	if err = im.tx.Get(im.ctx, &missingGroup, q1); err != nil {
		return
	}
	q2 := `SELECT COUNT(*) FROM member_roles mr
		JOIN members m ON m.id = mr.member_id
		JOIN projects c ON c.parent_id = m.project_id AND c.inherit_members = ?
		WHERE NOT EXISTS (SELECT 1 FROM member_roles x JOIN members cm ON cm.id = x.member_id
			WHERE cm.principal_id = m.principal_id AND cm.project_id = c.id AND x.role_id = mr.role_id AND x.inherited_from = mr.id)`
	if err = im.tx.Get(im.ctx, &missingInherit, q2, true); err != nil {
		return
	}
	q3 := `SELECT COUNT(*) FROM member_roles x
		JOIN members xm ON xm.id = x.member_id
		JOIN member_roles s ON s.id = x.inherited_from
		JOIN members sm ON sm.id = s.member_id
		WHERE NOT (
			(sm.project_id = xm.project_id AND EXISTS (SELECT 1 FROM group_users gu WHERE gu.group_id = sm.principal_id AND gu.user_id = xm.principal_id))
			OR (sm.principal_id = xm.principal_id AND EXISTS (SELECT 1 FROM projects c WHERE c.id = xm.project_id AND c.parent_id = sm.project_id AND c.inherit_members = ?))
		)`
	err = im.tx.Get(im.ctx, &unexpected, q3, true)
	return
}
