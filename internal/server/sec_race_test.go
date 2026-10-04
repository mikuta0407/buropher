// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/server"
)

// このファイルは並行リクエストによる競合状態（同時の親変更・セッション失効中のログイン・
// 複数プロセス間の設定キャッシュなど）を実際の HTTP で再現するテスト。
// SQLite と、BUROPHER_TEST_PG_DSN があれば PostgreSQL（READ COMMITTED で書き込みが直列化されない）で実行する。

// raceForEachDB は SQLite と PostgreSQL（DSN があれば）の空の DB でサブテストを実行する。
func raceForEachDB(t *testing.T, fn func(t *testing.T, d *db.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, dbtest.NewSQLite(t)) })
	t.Run("postgres", func(t *testing.T) { fn(t, dbtest.NewPostgres(t)) })
}

// raceParallel は n 個のゴルーチンを開始の合図で一斉に走らせ、すべての終了を待つ。
func raceParallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn(i)
		}()
	}
	close(start)
	wg.Wait()
}

// raceAPI は HTTP Basic 認証付きの API リクエストを送り、ステータスと本文を返す。
func raceAPI(t *testing.T, base, method, path, user, pw, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	req.SetBasicAuth(user, pw)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	c := &http.Client{Timeout: 60 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		t.Errorf("%s %s: %v", method, path, err)
		return 0, ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// raceParentCycle は parent_id をたどって循環があれば、その始点の id を返す（無ければ 0）。
func raceParentCycle(t *testing.T, d *db.DB) int64 {
	t.Helper()
	var rows []struct {
		ID       int64         `db:"id"`
		ParentID sql.NullInt64 `db:"parent_id"`
	}
	if err := d.Select(context.Background(), &rows, `SELECT id, parent_id FROM issues`); err != nil {
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

// TestRaceIssueParentCycle は、2 つのチケットを互いの親にする更新を同時に送っても
// 親子関係が循環しないことを確認する。PostgreSQL では検証（親が自分の子孫でないこと）と更新の間に
// 他のトランザクションの更新が入り込めたため循環が作られ、以後どちらかを保存するたびに
// 親の再計算が無限に再帰した（スタック溢れでプロセスごと落ちる DoS）。
func TestRaceIssueParentCycle(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		var ids []int64
		if err := d.Select(ctx, &ids, `SELECT id FROM issues WHERE project_id = 1 AND parent_id IS NULL ORDER BY id`); err != nil {
			t.Fatal(err)
		}
		if len(ids) < 4 {
			t.Fatalf("not enough root issues: %v", ids)
		}
		ids = ids[:len(ids)/2*2]
		for round := range 15 {
			raceParallel(len(ids), func(i int) {
				// (ids[0], ids[1]), (ids[2], ids[3]) ... の組で互いを親にする
				other := ids[i^1]
				raceAPI(t, ts.URL, "PUT", fmt.Sprintf("/issues/%d.json", ids[i]), "admin", "admin",
					fmt.Sprintf(`{"issue":{"parent_issue_id":%d}}`, other))
			})
			if id := raceParentCycle(t, d); id != 0 {
				t.Fatalf("round %d: parent_id cycle through issue #%d", round, id)
			}
			// 次の回のために親子関係を解く
			for _, id := range ids {
				if _, err := d.Exec(ctx, `UPDATE issues SET parent_id = NULL, root_id = ?, hier_path = ? WHERE id = ?`,
					id, fmt.Sprintf("%010d/", id), id); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

// TestIssueParentCycleSaveTerminates は、既に parent_id が循環しているデータ（修正前の並行更新で
// 作られた破損）でも、チケットの保存が親の再計算で無限に再帰せずに終わることを確認する。
func TestIssueParentCycleSaveTerminates(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		// #1 と #2 を互いの親にする（hier_path は保存時の値のまま）
		for _, q := range []string{
			`UPDATE issues SET parent_id = 2, root_id = 2, hier_path = '0000000002/0000000001/' WHERE id = 1`,
			`UPDATE issues SET parent_id = 1, root_id = 1, hier_path = '0000000001/0000000002/' WHERE id = 2`,
		} {
			if _, err := d.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan int, 1)
		go func() {
			code, _ := raceAPI(t, ts.URL, "PUT", "/issues/1.json", "admin", "admin", `{"issue":{"notes":"touch"}}`)
			done <- code
		}()
		select {
		case code := <-done:
			if code == 0 {
				t.Fatal("request failed")
			}
		case <-time.After(30 * time.Second):
			t.Fatal("saving an issue in a parent_id cycle did not terminate")
		}
	})
}

// TestSettingsChangeSeenByOtherProcess は、同じ DB を使う別のプロセス（ここでは同じ DB に繋いだ
// 2 つ目のサーバ）で設定を変えると、次のリクエストから反映されることを確認する（Setting.check_cache）。
// 修正前は設定のキャッシュを起動時にしか読まないため、REST API の無効化やログイン必須化が
// 他のプロセスでは再起動まで効かず、無効にしたはずの API キー認証が通り続けた。
func TestSettingsChangeSeenByOtherProcess(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		srvA, _ := newFixtureServerOn(t, d)
		cfg := config.Default()
		cfg.Server.SecretKey = "test-secret"
		cfg.Storage.AttachmentsPath = t.TempDir()
		srvB, err := server.New(cfg, d, server.Options{TempDir: t.TempDir(), Now: func() time.Time { return frozenTime }})
		if err != nil {
			t.Fatal(err)
		}
		tsB := httptest.NewServer(srvB.Handler())
		t.Cleanup(tsB.Close)
		if code, _ := raceAPI(t, tsB.URL, "GET", "/issues.json", "jsmith", "jsmith", ""); code != 200 {
			t.Fatalf("API before change: %d", code)
		}
		ctx := context.Background()
		// プロセス A で REST API を無効にし、ログインを必須にする
		if err := srvA.App().Settings.Set(ctx, "rest_api_enabled", "0"); err != nil {
			t.Fatal(err)
		}
		if err := srvA.App().Settings.Set(ctx, "login_required", "1"); err != nil {
			t.Fatal(err)
		}
		if code, _ := raceAPI(t, tsB.URL, "GET", "/issues.json", "jsmith", "jsmith", ""); code != 403 {
			t.Errorf("API on the other process after disabling REST API: %d, want 403", code)
		}
		res, _ := get(t, newClient(t), tsB.URL+"/projects")
		if res.StatusCode != 302 || !strings.Contains(res.Header.Get("Location"), "/login") {
			t.Errorf("anonymous /projects on the other process after login_required: %d %s", res.StatusCode, res.Header.Get("Location"))
		}
		// 同じマイクロ秒内の連続した変更も検出する（版が必ず進む）
		for _, v := range []string{"1", "0"} {
			if err := srvA.App().Settings.Set(ctx, "login_required", v); err != nil {
				t.Fatal(err)
			}
		}
		res, _ = get(t, newClient(t), tsB.URL+"/projects")
		if res.StatusCode != 200 {
			t.Errorf("anonymous /projects after login_required was turned off again: %d", res.StatusCode)
		}
	})
}

// TestRaceAdditionalEmailLimit は、追加メールアドレスの作成を並列に送っても max_additional_emails を
// 超えて作れないことを確認する（PostgreSQL では件数の確認と追加の間に他のトランザクションの追加が入り込めた）。
func TestRaceAdditionalEmailLimit(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		srv, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM email_addresses WHERE user_id = 2`); err != nil {
			t.Fatal(err)
		}
		// 既存の件数 = 上限（あと 1 件だけ追加できる）
		if err := srv.App().Settings.Set(ctx, "max_additional_emails", fmt.Sprint(n)); err != nil {
			t.Fatal(err)
		}
		c := login(t, ts, "jsmith", "jsmith")
		_, body := get(t, c, ts.URL+"/my/account")
		token := csrfToken(t, body)
		raceParallel(10, func(i int) {
			res, err := c.PostForm(ts.URL+"/users/2/email_addresses", url.Values{"authenticity_token": {token},
				"email_address[address]": {fmt.Sprintf("race%d@example.net", i)}})
			if err == nil {
				res.Body.Close()
			}
		})
		var after int
		if err := d.Get(ctx, &after, `SELECT COUNT(*) FROM email_addresses WHERE user_id = 2`); err != nil {
			t.Fatal(err)
		}
		if after > n+1 {
			t.Fatalf("%d addresses after parallel creation, limit allows %d", after, n+1)
		}
	})
}
