// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

// test/unit/token_test.rb、project_test.rb（shared_versions）、lib/redmine/project_jump_box_test.rb の移植。

import (
	"errors"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func (e *env) tokenCount(userID int64, action string) int {
	return e.count(`SELECT COUNT(*) FROM tokens WHERE user_id = ? AND action = ?`, userID, action)
}

func TestTokens(t *testing.T) {
	withFixtures(t, func(e *env) {
		hex40 := regexp.MustCompile(`\A[0-9a-f]{40}\z`)
		tok, err := repository.CreateToken(e.ctx, e.d, 2, repository.TokenFeeds)
		e.must(err)
		if !hex40.MatchString(tok.Value) {
			t.Errorf("token value %q", tok.Value)
		}

		// test_create_should_remove_existing_tokens: max_instances 1 は古いものを消す
		_, err = repository.CreateToken(e.ctx, e.d, 2, repository.TokenFeeds)
		e.must(err)
		if n := e.tokenCount(2, repository.TokenFeeds); n != 1 {
			t.Errorf("feeds tokens = %d", n)
		}
		// test_create_session_token_should_keep_last_10_tokens: autologin は 10 個まで
		for range 12 {
			_, err := repository.CreateToken(e.ctx, e.d, 2, repository.TokenAutologin)
			e.must(err)
		}
		if n := e.tokenCount(2, repository.TokenAutologin); n != 10 {
			t.Errorf("autologin tokens = %d", n)
		}

		// find_active_user / find_token
		now := time.Now()
		u, err := repository.FindActiveTokenUser(e.ctx, e.d, repository.TokenFeeds, tok.Value, 0, now)
		if !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("replaced token still valid: %v %v", u, err)
		}
		k, err := repository.AtomKey(e.ctx, e.d, 2)
		e.must(err)
		u, err = repository.FindActiveTokenUser(e.ctx, e.d, repository.TokenFeeds, k, 0, now)
		e.must(err)
		if u.ID != 2 {
			t.Errorf("user = %d", u.ID)
		}
		if k2, _ := repository.AtomKey(e.ctx, e.d, 2); k2 != k {
			t.Error("atom key changed")
		}
		// 別アクション・不正なキー
		for _, c := range []struct{ action, key string }{
			{repository.TokenAPI, k}, {repository.TokenFeeds, k + "x"}, {repository.TokenFeeds, "a b"}, {"", k},
		} {
			if _, err := repository.FindToken(e.ctx, e.d, c.action, c.key, 0, now); !errors.Is(err, repository.ErrNotFound) {
				t.Errorf("FindToken(%q, %q) = %v", c.action, c.key, err)
			}
		}
		// test_find_token_should_return_nil_with_expired_token
		e.must(exec(e, `UPDATE tokens SET created_at = ? WHERE value = ?`, now.AddDate(0, 0, -2).UTC().Format("2006-01-02T15:04:05.000000Z"), k))
		if _, err := repository.FindToken(e.ctx, e.d, repository.TokenFeeds, k, 1, now); !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("expired token found: %v", err)
		}
		if _, err := repository.FindToken(e.ctx, e.d, repository.TokenFeeds, k, 3, now); err != nil {
			t.Errorf("token within validity: %v", err)
		}
		// test_find_active_user_should_return_nil_for_locked_user
		e.must(exec(e, `UPDATE principals SET status = 3 WHERE id = 2`))
		if _, err := repository.FindActiveTokenUser(e.ctx, e.d, repository.TokenFeeds, k, 0, now); !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("locked user found: %v", err)
		}

		// test_destroy_expired_should_not_destroy_feeds_and_api_tokens / autologin は期限で消える
		e.must(exec(e, `UPDATE tokens SET created_at = ? WHERE user_id = 2`, now.AddDate(0, 0, -40).UTC().Format("2006-01-02T15:04:05.000000Z")))
		n, err := repository.DestroyExpiredTokens(e.ctx, e.d, now, 30)
		e.must(err)
		if n != 10 || e.tokenCount(2, repository.TokenFeeds) != 1 {
			t.Errorf("destroyed %d, feeds left %d", n, e.tokenCount(2, repository.TokenFeeds))
		}
	})
}

func exec(e *env, q string, args ...any) error {
	_, err := e.d.Exec(e.ctx, q, args...)
	return err
}

func TestSharedVersions(t *testing.T) {
	withFixtures(t, func(e *env) {
		ids := func(pid int64) []int64 {
			got, err := repository.SharedVersionIDs(e.ctx, e.d, e.project(pid))
			e.must(err)
			return got
		}
		// test_shared_versions
		if got := ids(1); !slices.Equal(got, []int64{1, 2, 3, 4, 6, 7}) {
			t.Errorf("parent shared versions = %v", got)
		}
		// test_shared_versions_*_sharing: 5 の子=6、兄弟=3、ルート=1、ルートの兄弟=2
		for _, c := range []struct {
			sharing                          string
			self, child, root, sibling, rsib bool
		}{
			{"none", true, false, false, false, false},
			{"descendants", true, true, false, false, false},
			{"hierarchy", true, true, true, false, false},
			{"tree", true, true, true, true, false},
			{"system", true, true, true, true, true},
		} {
			id, err := e.d.InsertReturningID(e.ctx, `INSERT INTO versions (project_id, name, sharing, created_at, updated_at) VALUES (5, ?, ?, ?, ?)`,
				c.sharing+"_sharing", c.sharing, "2026-01-01T00:00:00.000000Z", "2026-01-01T00:00:00.000000Z")
			e.must(err)
			for _, x := range []struct {
				pid  int64
				want bool
			}{{5, c.self}, {6, c.child}, {1, c.root}, {3, c.sibling}, {2, c.rsib}} {
				if got := slices.Contains(ids(x.pid), id); got != x.want {
					t.Errorf("%s: project %d includes = %v, want %v", c.sharing, x.pid, got, x.want)
				}
			}
			e.must(exec(e, `DELETE FROM versions WHERE id = ?`, id))
		}
		// test_shared_versions_should_ignore_archived_subprojects
		e.must(exec(e, `UPDATE projects SET status = 9 WHERE id = 3`))
		if slices.Contains(ids(1), 4) {
			t.Error("archived subproject version shared")
		}
		ok, err := repository.ProjectHasRolledUpVersions(e.ctx, e.d, e.project(3))
		e.must(err)
		if ok {
			t.Error("archived project has rolled up versions")
		}
		ok, err = repository.ProjectHasRolledUpVersions(e.ctx, e.d, e.project(1))
		e.must(err)
		if !ok {
			t.Error("rolled up versions missing")
		}
	})
}

func TestProjectMenuConditions(t *testing.T) {
	withFixtures(t, func(e *env) {
		check := func(name string, f func(int64) (bool, error), want map[int64]bool) {
			for pid, w := range want {
				got, err := f(pid)
				e.must(err)
				if got != w {
					t.Errorf("%s(%d) = %v, want %v", name, pid, got, w)
				}
			}
		}
		check("wiki", func(id int64) (bool, error) { return repository.ProjectHasWiki(e.ctx, e.d, id) },
			map[int64]bool{1: true, 2: true, 3: false, 5: true})
		check("boards", func(id int64) (bool, error) { return repository.ProjectHasBoards(e.ctx, e.d, id) },
			map[int64]bool{1: true, 2: true, 3: false})
		check("repositories", func(id int64) (bool, error) { return repository.ProjectHasRepositories(e.ctx, e.d, id) },
			map[int64]bool{1: true, 2: true, 3: false})

		// Issue.allowed_target_trackers: 管理者は全トラッカー、メンバーは add_issues のロール次第
		trackers := func(uid, pid int64) []int64 {
			u, err := repository.GetUser(e.ctx, e.d, uid)
			e.must(err)
			got, err := authz.New(e.d, u).AllowedTargetTrackerIDs(e.ctx, e.project(pid), 0)
			e.must(err)
			return got
		}
		all, err := repository.ProjectTrackerIDs(e.ctx, e.d, 1)
		e.must(err)
		if got := trackers(1, 1); !slices.Equal(got, all) || len(all) == 0 {
			t.Errorf("admin trackers = %v, want %v", got, all)
		}
		if got := trackers(2, 1); !slices.Equal(got, all) {
			t.Errorf("manager trackers = %v, want %v", got, all)
		}
		// 匿名（非メンバーの組込ロールに add_issues が無い）
		anon, err := repository.AnonymousUser(e.ctx, e.d)
		e.must(err)
		got, err := authz.New(e.d, anon).AllowedTargetTrackerIDs(e.ctx, e.project(2), 0)
		e.must(err)
		if len(got) != 0 {
			t.Errorf("anonymous trackers on private project = %v", got)
		}
		// トラッカー制限: ロール 1 の add_issues をトラッカー 2 のみに
		e.must(repository.SetRolePermissions(e.ctx, e.d, 1, permissionsOf(e, 1), map[string][]int64{"add_issues": {2}}))
		if got := trackers(2, 1); !slices.Equal(got, []int64{2}) {
			t.Errorf("restricted trackers = %v", got)
		}
	})
}

func permissionsOf(e *env, roleID int64) []string {
	r, err := repository.GetRole(e.ctx, e.d, roleID)
	e.must(err)
	return r.Permissions
}

func TestProjectJumpBox(t *testing.T) {
	withFixtures(t, func(e *env) {
		recents := func(uid int64) []int64 {
			ids, err := repository.RecentProjectIDs(e.ctx, e.d, uid)
			e.must(err)
			return ids
		}
		const uid = 2
		// test_should_store_recent_projects
		for _, pid := range []int64{1, 2, 3, 1, 5} {
			e.must(repository.ProjectUsed(e.ctx, e.d, uid, pid, 3))
		}
		if got := recents(uid); !slices.Equal(got, []int64{5, 1, 3}) {
			t.Errorf("recents = %v", got)
		}
		// test_should_not_include_bookmark_in_recently_used_list
		e.must(repository.BookmarkProject(e.ctx, e.d, uid, 1))
		if got := recents(uid); !slices.Equal(got, []int64{5, 3}) {
			t.Errorf("recents after bookmark = %v", got)
		}
		e.must(repository.ProjectUsed(e.ctx, e.d, uid, 1, 3))
		if got := recents(uid); !slices.Equal(got, []int64{5, 3}) {
			t.Errorf("bookmarked project added to recents: %v", got)
		}
		bm, err := repository.BookmarkedProjectIDs(e.ctx, e.d, uid)
		e.must(err)
		if !slices.Equal(bm, []int64{1}) {
			t.Errorf("bookmarks = %v", bm)
		}
		e.must(repository.DeleteProjectBookmark(e.ctx, e.d, uid, 1))
		bm, err = repository.BookmarkedProjectIDs(e.ctx, e.d, uid)
		e.must(err)
		if len(bm) != 0 {
			t.Errorf("bookmarks after delete = %v", bm)
		}
		// 件数は pref.recently_used_projects に従う
		e.must(repository.ProjectUsed(e.ctx, e.d, uid, 2, 1))
		if got := recents(uid); !slices.Equal(got, []int64{2}) {
			t.Errorf("recents with limit 1 = %v", got)
		}
		// 個人設定: 行が無いユーザーは既定値
		p, err := repository.GetUserPreference(e.ctx, e.d, 8)
		e.must(err)
		if *p != *domain.DefaultUserPreference(8) {
			t.Errorf("default preference = %+v", p)
		}
	})
}
