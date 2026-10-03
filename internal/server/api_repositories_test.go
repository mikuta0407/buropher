// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/repositories_test.rb の移植。
// 関連チケットの追加・削除は SCM にアクセスしないため、フィクスチャのリポジトリ（Subversion）でも確認できる。

import (
	"net/http"
	"strings"
	"testing"
)

// repoChangesetIssues はチェンジセット 103 に関連付いたチケット id（昇順、カンマ区切り）。
func repoChangesetIssues(t *testing.T, f *contentFixture) string {
	t.Helper()
	return f.str(t, `SELECT COALESCE(group_concat(issue_id, ','), '') FROM (SELECT issue_id FROM changesets_issues WHERE changeset_id = 103 ORDER BY issue_id)`)
}

func TestAPIRepositories(t *testing.T) {
	for _, format := range []string{"xml", "json"} {
		for _, issueID := range []string{"2", "#2"} {
			name := "POST /projects/:id/repository/:repository_id/revisions/:rev/issues." + format + " should add related issue"
			if strings.HasPrefix(issueID, "#") {
				name = "POST /projects/:id/repository/:repository_id/revisions/:rev/issues." + format + " should accept issue_id with sharp"
			}
			t.Run(name, func(t *testing.T) {
				f := newContentFixture(t)
				if s := repoChangesetIssues(t, f); s != "" {
					t.Fatalf("initial issues %q", s)
				}
				res := apiCall(t, f.ts, http.MethodPost, "/projects/1/repository/10/revisions/4/issues."+format, cFormType, cForm("issue_id", issueID), apiCreds("jsmith"))
				res.expectStatus(t, 204)
				if s := repoChangesetIssues(t, f); s != "2" {
					t.Errorf("issues %q", s)
				}
			})
		}

		t.Run("POST /projects/:id/repository/:repository_id/revisions/:rev/issues."+format+" with invalid issue_id", func(t *testing.T) {
			f := newContentFixture(t)
			res := apiCall(t, f.ts, http.MethodPost, "/projects/1/repository/10/revisions/4/issues."+format, cFormType, cForm("issue_id", "9999"), apiCreds("jsmith"))
			res.expectStatus(t, 422)
			if format == "xml" {
				assertXMLText(t, res.XML(t), "errors error", "Issue is invalid")
			} else if !strings.Contains(res.Body, `"Issue is invalid"`) {
				t.Errorf("body %s", res.Body)
			}
			if s := repoChangesetIssues(t, f); s != "" {
				t.Errorf("issues %q", s)
			}
		})

		t.Run("DELETE /projects/:id/repository/:repository_id/revisions/:rev/issues/:issue_id."+format+" should remove related issue", func(t *testing.T) {
			f := newContentFixture(t)
			f.exec(t, `INSERT INTO changesets_issues (changeset_id, issue_id) VALUES (103, 1), (103, 2)`)
			res := apiCall(t, f.ts, http.MethodDelete, "/projects/1/repository/10/revisions/4/issues/2."+format, "", "", apiCreds("jsmith"))
			res.expectStatus(t, 204)
			if s := repoChangesetIssues(t, f); s != "1" {
				t.Errorf("issues %q", s)
			}
		})
	}
}
