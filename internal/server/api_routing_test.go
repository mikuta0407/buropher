// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/api_routing_test.rb の移植。
// Redmine::ApiTest::Routing#should_route と同じく、各ルートを .json と .xml の両方で確認する
// （controller#action は App.RouteTable、パスパラメータはルータの Find が設定する URL パラメータで確認）。

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// apiRoutingCases は "METHOD /path" → controller#action と、期待するパスパラメータの値。
var apiRoutingCases = []struct {
	req, want string
	params    []string
}{
	// attachments
	{"GET /attachments/1", "attachments#show", []string{"1"}},
	{"PATCH /attachments/1", "attachments#update", []string{"1"}},
	{"DELETE /attachments/1", "attachments#destroy", []string{"1"}},
	{"POST /uploads", "attachments#upload", nil},
	// custom_fields
	{"GET /custom_fields", "custom_fields#index", nil},
	// enumerations
	{"GET /enumerations/issue_priorities", "enumerations#index", []string{"issue_priorities"}},
	// files
	{"GET /projects/foo/files", "files#index", []string{"foo"}},
	{"POST /projects/foo/files", "files#create", []string{"foo"}},
	// groups
	{"GET /groups", "groups#index", nil},
	{"POST /groups", "groups#create", nil},
	{"GET /groups/1", "groups#show", []string{"1"}},
	{"PUT /groups/1", "groups#update", []string{"1"}},
	{"DELETE /groups/1", "groups#destroy", []string{"1"}},
	// group_users
	{"POST /groups/567/users", "groups#add_users", []string{"567"}},
	{"DELETE /groups/567/users/12", "groups#remove_user", []string{"567", "12"}},
	// issue_categories
	{"GET /projects/foo/issue_categories", "issue_categories#index", []string{"foo"}},
	{"POST /projects/foo/issue_categories", "issue_categories#create", []string{"foo"}},
	{"GET /issue_categories/1", "issue_categories#show", []string{"1"}},
	{"PUT /issue_categories/1", "issue_categories#update", []string{"1"}},
	{"DELETE /issue_categories/1", "issue_categories#destroy", []string{"1"}},
	// issue_relations
	{"GET /issues/1/relations", "issue_relations#index", []string{"1"}},
	{"POST /issues/1/relations", "issue_relations#create", []string{"1"}},
	{"GET /relations/23", "issue_relations#show", []string{"23"}},
	{"DELETE /relations/23", "issue_relations#destroy", []string{"23"}},
	// issue_statuses
	{"GET /issue_statuses", "issue_statuses#index", nil},
	// issues
	{"GET /issues", "issues#index", nil},
	{"POST /issues", "issues#create", nil},
	{"GET /issues/64", "issues#show", []string{"64"}},
	{"PUT /issues/64", "issues#update", []string{"64"}},
	{"DELETE /issues/64", "issues#destroy", []string{"64"}},
	// issue_watchers（object_type => 'issue' はルートの既定値）
	{"POST /issues/12/watchers", "watchers#create", []string{"12"}},
	{"DELETE /issues/12/watchers/3", "watchers#destroy", []string{"12", "3"}},
	// memberships
	{"GET /projects/5234/memberships", "members#index", []string{"5234"}},
	{"POST /projects/5234/memberships", "members#create", []string{"5234"}},
	{"GET /memberships/5234", "members#show", []string{"5234"}},
	{"PUT /memberships/5234", "members#update", []string{"5234"}},
	{"DELETE /memberships/5234", "members#destroy", []string{"5234"}},
	// news
	{"GET /news", "news#index", nil},
	{"GET /projects/567/news", "news#index", []string{"567"}},
	// projects
	{"GET /projects", "projects#index", nil},
	{"POST /projects", "projects#create", nil},
	{"GET /projects/1", "projects#show", []string{"1"}},
	{"PUT /projects/1", "projects#update", []string{"1"}},
	{"DELETE /projects/1", "projects#destroy", []string{"1"}},
	// queries
	{"GET /queries", "queries#index", nil},
	// repositories
	{"POST /projects/1/repository/2/revisions/3/issues", "repositories#add_related_issue", []string{"1", "2", "3"}},
	{"DELETE /projects/1/repository/2/revisions/3/issues/4", "repositories#remove_related_issue", []string{"1", "2", "3", "4"}},
	// roles
	{"GET /roles", "roles#index", nil},
	{"GET /roles/2", "roles#show", []string{"2"}},
	// time_entries
	{"GET /time_entries", "timelog#index", nil},
	{"POST /time_entries", "timelog#create", nil},
	{"GET /time_entries/1", "timelog#show", []string{"1"}},
	{"PUT /time_entries/1", "timelog#update", []string{"1"}},
	{"DELETE /time_entries/1", "timelog#destroy", []string{"1"}},
	// trackers
	{"GET /trackers", "trackers#index", nil},
	// users
	{"GET /users", "users#index", nil},
	{"POST /users", "users#create", nil},
	{"GET /users/44", "users#show", []string{"44"}},
	{"GET /users/current", "users#show", []string{"current"}},
	{"PUT /users/44", "users#update", []string{"44"}},
	{"DELETE /users/44", "users#destroy", []string{"44"}},
	// versions
	{"GET /projects/foo/versions", "versions#index", []string{"foo"}},
	{"POST /projects/foo/versions", "versions#create", []string{"foo"}},
	{"GET /versions/1", "versions#show", []string{"1"}},
	{"PUT /versions/1", "versions#update", []string{"1"}},
	{"DELETE /versions/1", "versions#destroy", []string{"1"}},
	// wiki
	{"GET /projects/567/wiki/index", "wiki#index", []string{"567"}},
	{"GET /projects/567/wiki/my_page", "wiki#show", []string{"567", "my_page"}},
	{"GET /projects/1/wiki/my_page/2", "wiki#show", []string{"1", "my_page", "2"}},
	{"PUT /projects/567/wiki/my_page", "wiki#update", []string{"567", "my_page"}},
	{"DELETE /projects/567/wiki/my_page", "wiki#destroy", []string{"567", "my_page"}},
}

func TestAPIRouting(t *testing.T) {
	srv, _, _ := newFixtureServerFull(t)
	finder, ok := srv.Handler().(routeFinder)
	if !ok {
		t.Fatal("router does not implement Find")
	}
	type key struct{ method, pattern string }
	table := map[key]string{}
	for _, e := range srv.App().RouteTable() {
		table[key{e.Method, e.Pattern}] = e.Controller + "#" + e.Action
	}
	for _, tc := range apiRoutingCases {
		method, path, _ := strings.Cut(tc.req, " ")
		for _, f := range []string{"json", "xml"} {
			t.Run(tc.req+"."+f, func(t *testing.T) {
				rctx := chi.NewRouteContext()
				pattern := finder.Find(rctx, method, path+"."+f)
				if pattern == "" {
					t.Fatalf("unrouted (want %s)", tc.want)
				}
				if !strings.HasSuffix(pattern, ".{format}") {
					t.Errorf("pattern %s has no format", pattern)
				}
				if got := table[key{method, strings.TrimSuffix(pattern, ".{format}")}]; got != tc.want {
					t.Errorf("%s routes to %q, want %s", pattern, got, tc.want)
				}
				vals := map[string]string{}
				for i, k := range rctx.URLParams.Keys {
					vals[k] = rctx.URLParams.Values[i]
				}
				if vals["format"] != f {
					t.Errorf("format = %q", vals["format"])
				}
				var got []string
				for i, k := range rctx.URLParams.Keys {
					if k != "format" && k != "*" {
						got = append(got, rctx.URLParams.Values[i])
					}
				}
				if !slices.Equal(got, tc.params) {
					t.Errorf("params %v, want %v", got, tc.params)
				}
			})
		}
	}
}
