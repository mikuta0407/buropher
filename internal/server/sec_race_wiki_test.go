// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// wikiParentCycle は wiki_pages の parent_id をたどって循環があれば、その始点の id を返す（無ければ 0）。
func wikiParentCycle(t *testing.T, d *db.DB) int64 {
	t.Helper()
	var rows []struct {
		ID       int64         `db:"id"`
		ParentID sql.NullInt64 `db:"parent_id"`
	}
	if err := d.Select(context.Background(), &rows, `SELECT id, parent_id FROM wiki_pages`); err != nil {
		t.Fatal(err)
	}
	parent := map[int64]int64{}
	for _, r := range rows {
		if r.ParentID.Valid {
			parent[r.ID] = r.ParentID.Int64
		}
	}
	for id := range parent {
		seen := map[int64]bool{}
		for cur, ok := id, true; ok; cur, ok = parent[cur] {
			if seen[cur] {
				return id
			}
			seen[cur] = true
		}
	}
	return 0
}

// TestRaceWikiParentCycle は、2 つの Wiki ページを互いの親にする更新を同時に送っても
// 親子関係が循環しないことを確認する。親の循環の検査が保存のトランザクションの外でロックせずに
// 行われていたため、並行する更新の両方が検査を通って parent_id が循環した。
func TestRaceWikiParentCycle(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		var pages []struct {
			ID    int64  `db:"id"`
			Title string `db:"title"`
			Text  string `db:"text"`
		}
		if err := d.Select(ctx, &pages, `SELECT p.id, p.title, v.text FROM wiki_pages p
			JOIN wiki_page_versions v ON v.page_id = p.id AND v.version = p.current_version
			WHERE p.wiki_id = 1 ORDER BY p.id`); err != nil {
			t.Fatal(err)
		}
		if len(pages) < 4 {
			t.Fatalf("not enough wiki pages: %d", len(pages))
		}
		pages = pages[:len(pages)/2*2]
		reset := func() {
			if _, err := d.Exec(ctx, `UPDATE wiki_pages SET parent_id = NULL WHERE wiki_id = 1`); err != nil {
				t.Fatal(err)
			}
		}
		reset()
		for round := range 15 {
			raceParallel(len(pages), func(i int) {
				// (pages[0], pages[1]), (pages[2], pages[3]) ... の組で互いを親にする
				other := pages[i^1]
				body, _ := json.Marshal(map[string]any{"wiki_page": map[string]any{
					"parent_title": other.Title, "text": pages[i].Text,
				}})
				raceAPI(t, ts.URL, "PUT", "/projects/ecookbook/wiki/"+url.PathEscape(pages[i].Title)+".json", "admin", "admin", string(body))
			})
			if id := wikiParentCycle(t, d); id != 0 {
				t.Fatalf("round %d: parent_id cycle through wiki page #%d", round, id)
			}
			reset()
		}
	})
}
