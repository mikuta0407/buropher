// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// sqlLeakMarkers は内部エラーの文面に現れる語（応答本文に出てはならない）。
var sqlLeakMarkers = []string{"no such table", "SQL", "sqlite", "document_categories", "custom_values", "statement invalid"}

func assertNoLeak(t *testing.T, what, body string) {
	t.Helper()
	for _, m := range sqlLeakMarkers {
		if strings.Contains(body, m) {
			t.Errorf("%s: response leaks internal error text %q:\n%s", what, m, body)
		}
	}
}

// TestServerErrorDoesNotLeakDBError は想定外の DB エラー（serverError）の 500 が
// エラー文（SQL・テーブル名・ドライバ名）を利用者に見せないことを確認する（Rails の public/500.html 相当）。
func TestServerErrorDoesNotLeakDBError(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	if _, err := d.Exec(context.Background(), `ALTER TABLE document_categories RENAME TO document_categories_x`); err != nil {
		t.Fatal(err)
	}
	res, body := get(t, admin, ts.URL+"/admin")
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("/admin: status %d, want 500", res.StatusCode)
	}
	assertNoLeak(t, "/admin", body)
}

// TestQueryStatementErrorDoesNotLeakSQL はプロジェクトのクエリの実行エラーが Redmine と同じく
// error_query_statement_invalid の文言だけを表示し、SQL のエラー文を見せないことを確認する。
func TestQueryStatementErrorDoesNotLeakSQL(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	if _, err := d.Exec(context.Background(), `ALTER TABLE custom_values RENAME TO custom_values_x`); err != nil {
		t.Fatal(err)
	}
	res, body := get(t, admin, ts.URL+"/admin/projects?set_filter=1&f[]=cf_3&op[cf_3]=*")
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("/admin/projects: status %d, want 500\n%s", res.StatusCode, body)
	}
	assertNoLeak(t, "/admin/projects", body)
	if !strings.Contains(body, "An error occurred while executing the query and has been logged.") {
		t.Errorf("/admin/projects: error_query_statement_invalid message missing:\n%s", body)
	}
}

// TestHealthzDoesNotLeakDBError は認証なしの /healthz が DB の障害時にエラー文を返さないことを確認する。
func TestHealthzDoesNotLeakDBError(t *testing.T) {
	ts, d := newFixtureServer(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", res.StatusCode)
	}
	if got := string(b); got != "db: unavailable" {
		t.Errorf("body %q, want %q", got, "db: unavailable")
	}
}
