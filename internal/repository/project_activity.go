// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// LastActivityConds は Redmine::Activity::Fetcher の各イベント種別の可視性条件
// （呼び出し側が authz で作る。projects ほか各テーブルを参照する SQL）。空文字の種別は対象外。
type LastActivityConds struct {
	// Issues は Issue.visible（issues / projects）。
	Issues string
	// JournalNotes は Journal.visible_notes_condition（issue_journals / projects）。Issues と AND される。
	JournalNotes string
	// Changesets は Changeset.visible（view_changesets）。
	Changesets string
	// News は News.visible（view_news）。
	News string
	// Documents は Document.visible / view_documents。
	Documents string
	// Files は view_files。
	Files string
	// WikiEdits は view_wiki_edits。
	WikiEdits string
	// Messages は Message.visible（view_messages）。
	Messages string
	// TimeEntries は TimeEntry.visible。
	TimeEntries string
}

// LastActivityByProject は Project.load_last_activity_date の移植: 可視なイベントの
// プロジェクトごとの最新日時（Fetcher#events(nil, nil, :last_by_project => true).to_h）。
func LastActivityByProject(ctx context.Context, q db.Queryer, c LastActivityConds) (map[int64]time.Time, error) {
	type src struct {
		cond string
		sql  string
	}
	var srcs []src
	add := func(cond, sql string) {
		if cond != "" {
			srcs = append(srcs, src{cond, sql})
		}
	}
	add(c.Issues, `SELECT projects.id AS pid, MAX(issues.created_at) AS t FROM issues JOIN projects ON projects.id = issues.project_id
WHERE (`+c.Issues+`) GROUP BY projects.id`)
	if c.Issues != "" {
		jn := c.JournalNotes
		if jn == "" {
			jn = "1=1"
		}
		srcs = append(srcs, src{c.Issues, `SELECT projects.id AS pid, MAX(issue_journals.created_at) AS t FROM issue_journals
JOIN issues ON issues.id = issue_journals.issue_id JOIN projects ON projects.id = issues.project_id
WHERE (EXISTS (SELECT 1 FROM issue_journal_details d WHERE d.journal_id = issue_journals.id AND d.property = 'attr' AND d.prop_key = 'status_id')
  OR (issue_journals.notes IS NOT NULL AND issue_journals.notes <> ''))
  AND (` + c.Issues + `) AND (` + jn + `) GROUP BY projects.id`})
	}
	add(c.Changesets, `SELECT projects.id AS pid, MAX(changesets.committed_at) AS t FROM changesets
JOIN repositories ON repositories.id = changesets.repository_id JOIN projects ON projects.id = repositories.project_id
WHERE (`+c.Changesets+`) GROUP BY projects.id`)
	add(c.News, `SELECT projects.id AS pid, MAX(news.created_at) AS t FROM news JOIN projects ON projects.id = news.project_id
WHERE (`+c.News+`) GROUP BY projects.id`)
	add(c.Documents, `SELECT projects.id AS pid, MAX(documents.created_at) AS t FROM documents JOIN projects ON projects.id = documents.project_id
WHERE (`+c.Documents+`) GROUP BY projects.id`)
	add(c.Documents, `SELECT projects.id AS pid, MAX(attachments.created_at) AS t FROM attachments
JOIN documents ON attachments.container_kind = 'document' AND documents.id = attachments.container_id
JOIN projects ON projects.id = documents.project_id
WHERE (`+c.Documents+`) GROUP BY projects.id`)
	add(c.Files, `SELECT projects.id AS pid, MAX(attachments.created_at) AS t FROM attachments
LEFT JOIN versions ON attachments.container_kind = 'version' AND versions.id = attachments.container_id
JOIN projects ON versions.project_id = projects.id OR (attachments.container_kind = 'project' AND attachments.container_id = projects.id)
WHERE attachments.container_kind IN ('version', 'project') AND (`+c.Files+`) GROUP BY projects.id`)
	add(c.WikiEdits, `SELECT projects.id AS pid, MAX(wiki_page_versions.updated_at) AS t FROM wiki_page_versions
JOIN wiki_pages ON wiki_pages.id = wiki_page_versions.page_id JOIN wikis ON wikis.id = wiki_pages.wiki_id
JOIN projects ON projects.id = wikis.project_id
WHERE (`+c.WikiEdits+`) GROUP BY projects.id`)
	add(c.Messages, `SELECT projects.id AS pid, MAX(messages.created_at) AS t FROM messages
JOIN boards ON boards.id = messages.board_id JOIN projects ON projects.id = boards.project_id
WHERE (`+c.Messages+`) GROUP BY projects.id`)
	add(c.TimeEntries, `SELECT projects.id AS pid, MAX(time_entries.created_at) AS t FROM time_entries
JOIN projects ON projects.id = time_entries.project_id
WHERE (`+c.TimeEntries+`) GROUP BY projects.id`)

	out := map[int64]time.Time{}
	for _, s := range srcs {
		var rows []struct {
			PID int64       `db:"pid"`
			T   db.NullTime `db:"t"`
		}
		if err := q.Select(ctx, &rows, s.sql); err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.T.Valid {
				continue
			}
			if cur, ok := out[r.PID]; !ok || r.T.Time.After(cur) {
				out[r.PID] = r.T.Time
			}
		}
	}
	return out, nil
}
