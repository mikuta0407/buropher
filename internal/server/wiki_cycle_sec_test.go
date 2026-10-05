// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestWikiParentCycleTerminates は Wiki ページの親子関係が循環していても（同時の親変更などで生じうる）、
// {{child_pages}} の描画が終わること（修正前は無限再帰でプロセスごと落ちた）。
func TestWikiParentCycleTerminates(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// Another_page の子 Child_1 を Another_page の親にして循環させる
	if _, err := d.Exec(ctx, `UPDATE wiki_pages SET parent_id = (SELECT id FROM wiki_pages WHERE wiki_id = 1 AND title = 'Child_1')
		WHERE wiki_id = 1 AND title = 'Another_page'`); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "admin", "admin")
	c.Timeout = 30 * time.Second
	res, body := post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page/preview", wikiForm(t, c, ts, "content[text]", "{{child_pages(Another_page)}}"))
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "pages-hierarchy") {
		t.Errorf("child_pages preview: status %d", res.StatusCode)
	}
}
