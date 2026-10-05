// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Redmine 7.0 #37480: プロジェクトのメンバーの CSV 出力（test_index_with_csv_format_should_export_csv）。
func TestMembersIndexCSV(t *testing.T) {
	ts, _ := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	res, body := get(t, jsmith, ts.URL+"/projects/5/memberships.csv")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv; header=present") {
		t.Errorf("content type %q", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="private-child-members.csv"`) {
		t.Errorf("content disposition %q", cd)
	}
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(body, "\xef\xbb\xbf"), "\n"), "\n")
	want := []string{
		"User or Group,Type,Role,Project",
		"John Smith,User,Manager,Private child of eCookbook",
		"A Team,Group,Manager,Private child of eCookbook",
		"A Team,Group,Developer,Private child of eCookbook",
		"User Misc,User,Manager,Private child of eCookbook",
		"User Misc,User,Developer,Private child of eCookbook",
		"Redmine Admin,User,Manager,Private child of eCookbook",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("csv:\n%s", body)
	}
	// HTML は 406 のまま
	if res, _ := get(t, jsmith, ts.URL+"/projects/5/memberships"); res.StatusCode != 406 {
		t.Errorf("html: status %d", res.StatusCode)
	}
	// メンバーを見る権限が無ければ見られない
	if res, _ := get(t, newClient(t), ts.URL+"/projects/5/memberships.csv"); res.StatusCode == 200 {
		t.Error("anonymous should not export private project members")
	}
}

// Redmine 7.0 #44013: プロジェクト設定のメンバー一覧はグループにもリンクする
// （test_settings_members_should_link_to_principals）。CSV 出力のリンクも出す。
func TestSettingsMembersLinkToPrincipals(t *testing.T) {
	ts, d := newFixtureServer(t)
	// User.add_to_project(Group.find(10), Project.find(1))
	mastersExec(t, d, `INSERT INTO members (principal_id, project_id, created_at) VALUES (10, 1, '2026-01-15T12:00:00.000000Z')`)
	mid := mastersInt(t, d, `SELECT id FROM members WHERE principal_id = 10 AND project_id = 1`)
	mastersExec(t, d, `INSERT INTO member_roles (member_id, role_id) VALUES (?, 1)`, mid)
	jsmith := login(t, ts, "jsmith", "jsmith")
	_, body := get(t, jsmith, ts.URL+"/projects/ecookbook/settings/members")
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	assertXMLText(t, doc, `tr#member-1 td.name a[href="/users/2"]`, "John Smith")
	assertXMLText(t, doc, `tr#member-`+uitoa(int(mid))+` td.name a.group[href="/groups/10"]`, "A Team")
	assertXMLCount(t, doc, `p.other-formats a.csv[href="/projects/ecookbook/settings/members.csv"]`, 1)
	assertXMLCount(t, doc, `form#csv-export-form[action="/projects/1/memberships.csv"] select[name=encoding]`, 1)
}
