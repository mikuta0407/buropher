package server_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/clock"
)

// このファイルはマイページ・個人設定（MyController）の参照 Redmine との比較テストと、書き込みの振る舞いのテスト。
//
// testdata/my/* は参照 Redmine 6.1.2 の出力を正規化し、HTML は <title>・ページ固有の head・#main だけを
// 抜き出したもの（asNormalize。プロジェクトジャンプボックスは比較しない）。
//   - GET のみのケースは共有の参照環境（http://127.0.0.1:3998）から:
//     BUROPHER_MY_GOLDEN_REF=http://127.0.0.1:3998 go test -run TestMyPagesMatchRedmine ./internal/server
//   - ブロックを追加した後のケースは reset 直後の専用参照環境から（状態を変えるため共有環境は使わない）:
//     BUROPHER_MY_GOLDEN_WRITE_REF=http://127.0.0.1:4020 go test -run TestMyPageBlocksMatchRedmine ./internal/server
//
// どちらも先に /my/account.json を取得して API キーを作る（共有の参照環境では既に作られていることがあるため。
// 固定時刻なのでどちらも「1 分未満前に作成」と表示される）。
// time_zone_select は Casablanca の基準オフセットが評価時刻で変わるため比較から外す（myNormalize）。

var myCases = []asCase{
	{"admin", "/my/page", "page_admin.html"},
	{"jsmith", "/my/page", "page_jsmith.html"},
	{"dlopper", "/my/page", "page_dlopper.html"},
	{"jsmith", "/my", "my_jsmith.html"},
	{"admin", "/my/account", "account_admin.html"},
	{"jsmith", "/my/account", "account_jsmith.html"},
	{"dlopper", "/my/account", "account_dlopper.html"},
	{"jsmith", "/my/password", "password_jsmith.html"},
	{"jsmith", "/my/account/destroy", "destroy_jsmith.html"},
	{"jsmith", "/my/api_key", "api_key_jsmith.html"},
}

var (
	myTimeZoneRe = regexp.MustCompile(`(?s)<p><label for="pref_time_zone">.*?</select></p>`)
	myKeyRe      = regexp.MustCompile(`[0-9a-f]{40}`)
)

func myNormalize(body, base string) string {
	s := asNormalize(body, base)
	s = myTimeZoneRe.ReplaceAllString(s, "<!-- time_zone -->")
	return myKeyRe.ReplaceAllString(s, "KEY")
}

// myFreezeClock はトークンの作成日時（db.Now）も参照環境の固定時刻にする。
func myFreezeClock(t *testing.T) {
	clock.Set(frozenTime)
	t.Cleanup(func() { clock.Set(time.Time{}) })
}

// myPrime は各ユーザーの API キーを作る（/my/account.json）。
func myPrime(t *testing.T, base string, clients map[string]*http.Client) {
	t.Helper()
	for _, u := range []string{"admin", "jsmith", "dlopper"} {
		if status, _ := asFetch(t, base, clients, u, "/my/account.json"); status != 200 {
			t.Fatalf("%s /my/account.json: %d", u, status)
		}
	}
}

func myCompare(t *testing.T, dir, golden, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(dir, golden))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var a, b string
			if i < len(gl) {
				a = gl[i]
			}
			if i < len(wl) {
				b = wl[i]
			}
			if a != b {
				t.Fatalf("line %d differs\n got: %q\nwant: %q", i+1, a, b)
			}
		}
	}
}

// TestMyPagesMatchRedmine はマイページ・個人設定の画面が参照 Redmine と一致することを確認する。
func TestMyPagesMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "my")
	if ref := os.Getenv("BUROPHER_MY_GOLDEN_REF"); ref != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		clients := map[string]*http.Client{}
		myPrime(t, ref, clients)
		for _, tc := range myCases {
			status, body := asFetch(t, ref, clients, tc.user, tc.path)
			if status != 200 {
				t.Fatalf("ref %s %s: status %d", tc.user, tc.path, status)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.golden), []byte(myNormalize(body, ref)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	myFreezeClock(t)
	ts, _ := newFixtureServer(t)
	clients := map[string]*http.Client{}
	myPrime(t, ts.URL, clients)
	for _, tc := range myCases {
		t.Run(tc.golden, func(t *testing.T) {
			status, body := asFetch(t, ts.URL, clients, tc.user, tc.path)
			if status != 200 {
				t.Fatalf("status %d", status)
			}
			myCompare(t, dir, tc.golden, myNormalize(body, ts.URL))
		})
	}
}

// mySend はログイン済みクライアントで CSRF トークン付きのフォームを送る（xhr なら JS 応答を要求する）。
func mySend(t *testing.T, c *http.Client, base, method, path string, form url.Values, xhr bool) (*http.Response, string) {
	t.Helper()
	_, page := get(t, c, base+"/my/account")
	m := regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("csrf token not found")
	}
	if form == nil {
		form = url.Values{}
	}
	form.Set("authenticity_token", m[1])
	if method != http.MethodPost {
		form.Set("_method", strings.ToLower(method))
	}
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if xhr {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "text/javascript, application/javascript, */*; q=0.01")
	}
	return do(t, c, req), ""
}

// myAddBlocks は jsmith のマイページにすべての種類のブロックを追加する（参照環境と候補で同じ手順）。
func myAddBlocks(t *testing.T, c *http.Client, base string) {
	t.Helper()
	for _, b := range []string{"issueswatched", "issuesupdatedbyme", "issuequery", "news", "calendar", "documents", "timelog", "activity", "issuequery__1"} {
		res, _ := mySend(t, c, base, http.MethodPost, "/my/add_block", url.Values{"block": {b}}, true)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("add_block %s: %d", b, res.StatusCode)
		}
	}
	res, _ := mySend(t, c, base, http.MethodPost, "/my/page", url.Values{
		"settings[issuequery__1][query_id]": {"6"},
		"settings[issueswatched][columns][]": {"tracker", "subject", "priority", "assigned_to", "due_date", "done_ratio",
			"updated_on", "fixed_version", "estimated_hours", "author", "category", "start_date", "created_on", "is_private"},
	}, true)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("update_page: %d", res.StatusCode)
	}
}

// TestMyPageBlocksMatchRedmine はすべての種類のブロックを追加したマイページが参照 Redmine と一致することを確認する。
func TestMyPageBlocksMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "my")
	const golden = "page_all_blocks_jsmith.html"
	if ref := os.Getenv("BUROPHER_MY_GOLDEN_WRITE_REF"); ref != "" {
		clients := map[string]*http.Client{}
		myPrime(t, ref, clients)
		asFetch(t, ref, clients, "jsmith", "/my/account")
		myAddBlocks(t, clients["jsmith"], ref)
		status, body := asFetch(t, ref, clients, "jsmith", "/my/page")
		if status != 200 {
			t.Fatalf("ref: status %d", status)
		}
		if err := os.WriteFile(filepath.Join(dir, golden), []byte(myNormalize(body, ref)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	myFreezeClock(t)
	ts, _ := newFixtureServer(t)
	clients := map[string]*http.Client{}
	myPrime(t, ts.URL, clients)
	asFetch(t, ts.URL, clients, "jsmith", "/my/account")
	myAddBlocks(t, clients["jsmith"], ts.URL)
	status, body := asFetch(t, ts.URL, clients, "jsmith", "/my/page")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	myCompare(t, dir, golden, myNormalize(body, ts.URL))
}

// TestMyWriteBehavior はブロック操作・個人設定・パスワード変更・キーの再生成・アカウント削除の振る舞いを確認する。
func TestMyWriteBehavior(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	layout := func(uid int) string {
		var s *string
		if err := d.Get(ctx, &s, `SELECT my_page_layout FROM user_preferences WHERE user_id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		if s == nil {
			return ""
		}
		return *s
	}

	t.Run("blocks", func(t *testing.T) {
		c := login(t, ts, "dlopper", "foo")
		res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/add_block", url.Values{"block": {"news"}}, true)
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
			t.Fatalf("add_block: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
		}
		// dlopper のフィクスチャのレイアウト（left: latestnews, documents / right: issuesassignedtome / top: calendar）
		if got := layout(3); got != `{"left":["latestnews","documents"],"right":["issuesassignedtome"],"top":["news","calendar"]}` {
			t.Errorf("layout after add: %s", got)
		}
		// 上限に達したブロック・未知のブロックは 422
		for _, b := range []string{"news", "unknown", ""} {
			if res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/add_block", url.Values{"block": {b}}, true); res.StatusCode != 422 {
				t.Errorf("add_block %q: %d", b, res.StatusCode)
			}
		}
		// issuequery は 3 つまで（issuequery, issuequery__1, issuequery__2）
		for _, b := range []string{"issuequery", "issuequery__1", "issuequery__2"} {
			if res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/add_block", url.Values{"block": {b}}, false); res.StatusCode != 302 {
				t.Errorf("add_block %s: %d", b, res.StatusCode)
			}
		}
		if res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/add_block", url.Values{"block": {"issuequery__3"}}, false); res.StatusCode != 422 {
			t.Errorf("4th issuequery: %d", res.StatusCode)
		}
		// 並べ替え（存在しないブロックは無視、移動元のグループからは消える）
		res, _ = mySend(t, c, ts.URL, http.MethodPost, "/my/order_blocks", url.Values{"group": {"left"}, "blocks[]": {"news", "issuesassignedtome", "bogus"}}, true)
		if res.StatusCode != 200 {
			t.Fatalf("order_blocks: %d", res.StatusCode)
		}
		if got := layout(3); got != `{"left":["news","issuesassignedtome"],"right":[],"top":["issuequery__2","issuequery__1","issuequery","calendar"]}` {
			t.Errorf("layout after order: %s", got)
		}
		// 設定の保存（未使用のブロックの設定は保存しない）
		res, _ = mySend(t, c, ts.URL, http.MethodPost, "/my/page", url.Values{
			"settings[issuequery][query_id]": {"5"}, "settings[timelog][days]": {"3"},
		}, true)
		if res.StatusCode != 200 {
			t.Fatalf("update_page: %d", res.StatusCode)
		}
		var settings string
		if err := d.Get(ctx, &settings, `SELECT my_page_settings FROM user_preferences WHERE user_id = 3`); err != nil {
			t.Fatal(err)
		}
		if settings != `{"issuequery":{"query_id":"5"}}` {
			t.Errorf("settings: %s", settings)
		}
		_, body := get(t, c, ts.URL+"/my/page")
		if !strings.Contains(body, `<a href="/issues?query_id=5">Open issues by priority and tracker</a>`) {
			t.Error("saved query block not rendered")
		}
		// 削除（HTML はリダイレクト）
		if res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/remove_block", url.Values{"block": {"issuequery"}}, false); res.StatusCode != 302 {
			t.Errorf("remove_block: %d", res.StatusCode)
		}
		if strings.Contains(layout(3), `"issuequery"`) {
			t.Error("block not removed")
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		res, _ := get(t, newClient(t), ts.URL+"/my/page")
		if res.StatusCode != 302 || !strings.Contains(res.Header.Get("Location"), "/login?back_url=") {
			t.Errorf("anonymous /my/page: %d %s", res.StatusCode, res.Header.Get("Location"))
		}
	})

	t.Run("account", func(t *testing.T) {
		c := login(t, ts, "dlopper", "foo")
		res, _ := mySend(t, c, ts.URL, http.MethodPut, "/my/account", url.Values{
			"user[firstname]": {"Dave2"}, "user[mail]": {"dave2@somenet.foo"}, "pref[comments_sorting]": {"desc"},
			"pref[no_self_notified]": {"0"},
		}, false)
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/my/account") {
			t.Fatalf("account update: %d %s", res.StatusCode, res.Header.Get("Location"))
		}
		if n := uQueryString(t, d, `SELECT firstname FROM principals WHERE id = 3`); n != "Dave2" {
			t.Errorf("firstname: %s", n)
		}
		if s := uQueryString(t, d, `SELECT comments_sorting FROM user_preferences WHERE user_id = 3`); s != "desc" {
			t.Errorf("comments_sorting: %s", s)
		}
		// 個人設定の保存で未使用のブロック設定は消える（clear_unused_block_settings）
		res, _ = mySend(t, c, ts.URL, http.MethodPut, "/my/account", url.Values{"user[firstname]": {""}}, false)
		if res.StatusCode != 200 {
			t.Errorf("invalid account update: %d", res.StatusCode)
		}
	})

	t.Run("password", func(t *testing.T) {
		c1 := login(t, ts, "rhill", "foo")
		c2 := login(t, ts, "rhill", "foo")
		res, _ := mySend(t, c1, ts.URL, http.MethodPost, "/my/password", url.Values{
			"password": {"foo"}, "new_password": {"newpassword1"}, "new_password_confirmation": {"newpassword1"},
		}, false)
		if res.StatusCode != 302 {
			t.Fatalf("password: %d", res.StatusCode)
		}
		// 変更したセッションは続き、他のセッションは無効になる
		if res, _ := get(t, c1, ts.URL+"/my/account"); res.StatusCode != 200 {
			t.Errorf("own session after password change: %d", res.StatusCode)
		}
		if res, _ := get(t, c2, ts.URL+"/my/account"); res.StatusCode != 302 {
			t.Errorf("other session after password change: %d", res.StatusCode)
		}
		login(t, ts, "rhill", "newpassword1")
	})

	t.Run("keys", func(t *testing.T) {
		c := login(t, ts, "jsmith", "jsmith")
		key := func(action string) string {
			var s string
			_ = d.Get(ctx, &s, `SELECT value FROM tokens WHERE user_id = 2 AND action = ?`, action)
			return s
		}
		get(t, c, ts.URL+"/my/page") // atom キーを作る
		old := key("feeds")
		if res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/atom_key", nil, false); res.StatusCode != 302 {
			t.Errorf("reset_atom_key: %d", res.StatusCode)
		}
		if k := key("feeds"); k == "" || k == old {
			t.Errorf("atom key not reset: %q -> %q", old, k)
		}
		if res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/api_key", nil, false); res.StatusCode != 302 {
			t.Errorf("reset_api_key: %d", res.StatusCode)
		}
		api := key("api")
		if api == "" {
			t.Fatal("api key not created")
		}
		_, body := get(t, c, ts.URL+"/my/api_key")
		if !strings.Contains(body, "<pre>"+api+"</pre>") {
			t.Error("show_api_key does not show the key")
		}
	})

	t.Run("destroy", func(t *testing.T) {
		// 他に有効な管理者がいない管理者は削除できない
		admin := login(t, ts, "admin", "admin")
		if res, _ := mySend(t, admin, ts.URL, http.MethodPost, "/my/account/destroy", url.Values{"confirm": {"1"}}, false); res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/my/account") {
			t.Errorf("admin destroy: %d %s", res.StatusCode, res.Header.Get("Location"))
		}
		c := login(t, ts, "miscuser8", "foo")
		if res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/account/destroy", url.Values{}, false); res.StatusCode != 200 {
			t.Errorf("destroy without confirm: %d", res.StatusCode)
		}
		res, _ := mySend(t, c, ts.URL, http.MethodPost, "/my/account/destroy", url.Values{"confirm": {"1"}}, false)
		if res.StatusCode != 302 {
			t.Fatalf("destroy: %d", res.StatusCode)
		}
		if n := uQueryInt(t, d, `SELECT COUNT(*) FROM principals WHERE id = 8`); n != 0 {
			t.Errorf("user not deleted (redirected to %s)", res.Header.Get("Location"))
		}
	})
}
