// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
)

// このファイルはニュース・コメント・文書・ファイル・フォーラム・メッセージ・ウォッチャー・添付
// （NewsController ほか）の画面・API を参照 Redmine（http://127.0.0.1:3998）の出力と比較し、
// 書き込み系の振る舞いを確認する。testdata/content/*.html は参照の `compat fetch -raw` の出力から
// <div id="main"> ... <div id="footer"> を切り出し、ベース URL を伏せたもの（*.json / *.xml は全体）。

var (
	contentFormNameRe = regexp.MustCompile(`name="([a-z_-]+?)-[0-9a-f]{8}"`)
	contentCSRFRe     = regexp.MustCompile(`value="[A-Za-z0-9_-]{40,}"`)
	contentMetaCSRFRe = regexp.MustCompile(`<meta name="csrf-token" content="([A-Za-z0-9_-]+)"`)
)

func normalizeContent(s, base string) string {
	s = strings.ReplaceAll(s, base, "{{BASE}}")
	s = feedKeyRe.ReplaceAllString(s, "key=KEY")
	s = contentCSRFRe.ReplaceAllString(s, `value="{{CSRF}}"`)
	s = contentFormNameRe.ReplaceAllString(s, `name="$1-RANDOM"`)
	s = digestRe.ReplaceAllString(s, "-DIGEST.$1")
	return imageDigestRe.ReplaceAllString(s, "-DIGEST.$1")
}

func contentMain(s string) string {
	i := strings.Index(s, `<div id="main"`)
	j := strings.Index(s, `<div id="footer">`)
	if i < 0 || j < i {
		return s
	}
	return s[i:j]
}

// compareContentGolden は got と testdata/content/<name> を行単位で比較する。
// CONTENT_DUMP が設定されていれば取得した本文をそのディレクトリに書き出す。
func compareContentGolden(t *testing.T, name, got, base string) {
	t.Helper()
	got = normalizeContent(got, base)
	if dir := os.Getenv("CONTENT_DUMP"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(got), 0o644)
	}
	b, err := os.ReadFile("testdata/content/" + name)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeContent(string(b), "{{BASE}}")
	if got == want {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var a, w string
		if i < len(gl) {
			a = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if a != w {
			t.Fatalf("%s: line %d differs\n got: %q\nwant: %q", name, i+1, a, w)
		}
	}
}

// loadReactionFixtures は test/fixtures/reactions.yml の行を投入する（internal/testfixtures は reactions を扱わない）。
func loadReactionFixtures(t *testing.T, d *db.DB) {
	t.Helper()
	rows := []struct {
		kind     string
		id, user int64
	}{
		{"issue", 1, 1}, {"issue", 1, 2}, {"issue", 1, 3}, {"journal", 1, 2}, {"issue", 6, 2},
		{"journal", 4, 2}, {"news", 1, 1}, {"comment", 1, 2}, {"message", 7, 2}, {"news", 3, 2},
	}
	ts := db.NewTime(frozenTime)
	for i, r := range rows {
		if _, err := d.Exec(context.Background(), `INSERT INTO reactions (id, reactable_kind, reactable_id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			i+1, r.kind, r.id, r.user, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
}

// apiClient は HTTP Basic 認証で API を呼ぶクライアント。
type apiClient struct{ user, password string }

func (a apiClient) get(t *testing.T, u string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.SetBasicAuth(a.user, a.password)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return res, string(b)
}

// TestContentPagesMatchRedmine はニュース・文書・ファイル・フォーラムの画面と API が参照と一致することを確認する。
func TestContentPagesMatchRedmine(t *testing.T) {
	ts, d := newFixtureServer(t)
	loadReactionFixtures(t, d)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	anon := newClient(t)
	cases := []struct {
		client *http.Client
		path   string
		golden string
		whole  bool
	}{
		{admin, "/projects/ecookbook/news", "news_index_admin.html", false},
		{anon, "/news/1", "news_1_anonymous.html", false},
		{admin, "/news/1", "news_1_admin.html", false},
		{admin, "/projects/ecookbook/documents", "documents_admin.html", false},
		{admin, "/documents/1", "document_1_admin.html", false},
		{admin, "/projects/ecookbook/files", "files_admin.html", false},
		{admin, "/projects/ecookbook/boards", "boards_admin.html", false},
		{admin, "/projects/ecookbook/boards/1", "board_1_admin.html", false},
		{admin, "/boards/1/topics/1", "topic_1_admin.html", false},
		{jsmith, "/boards/1/topics/1/edit", "topic_1_edit_jsmith.html", false},
		{admin, "/news.json", "news_admin.json", true},
		{admin, "/news/1.xml?include=comments", "news_1_admin.xml", true},
		{admin, "/projects/ecookbook/files.json", "files_admin.json", true},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			var res *http.Response
			var body string
			if tc.whole {
				// API 形式はセッションを使わない（参照は HTTP Basic 認証で取得）
				res, body = apiClient{"admin", "admin"}.get(t, ts.URL+tc.path)
			} else {
				res, body = get(t, tc.client, ts.URL+tc.path)
			}
			if res.StatusCode != 200 {
				t.Fatalf("%s: status %d", tc.path, res.StatusCode)
			}
			if !tc.whole {
				body = contentMain(body)
			}
			compareContentGolden(t, tc.golden, body, ts.URL)
		})
	}
}

// contentForm は CSRF トークン付きのフォーム値。
func contentForm(t *testing.T, c *http.Client, ts *httptest.Server, kv ...string) url.Values {
	t.Helper()
	_, body := get(t, c, ts.URL+"/projects/ecookbook/news")
	m := contentMetaCSRFRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("csrf-token not found")
	}
	v := url.Values{"authenticity_token": {m[1]}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func contentCount(t *testing.T, d *db.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func expectContentRedirect(t *testing.T, res *http.Response, body, suffix string) {
	t.Helper()
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), suffix) {
		t.Fatalf("status %d location %q (want ...%s)\n%s", res.StatusCode, res.Header.Get("Location"), suffix, contentMain(body))
	}
}

// TestNewsAndComments はニュースの作成・更新・削除とコメントの追加・削除を確認する。
func TestNewsAndComments(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	dlopper := login(t, ts, "dlopper", "foo")

	// 検証エラー（title / description 必須）
	res, body := post(t, jsmith, ts.URL+"/projects/ecookbook/news", contentForm(t, jsmith, ts, "news[title]", "", "news[description]", ""))
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Title cannot be blank</li>") || !strings.Contains(body, "<li>Description cannot be blank</li>") {
		t.Fatalf("invalid create: %d", res.StatusCode)
	}

	// アップロード済みの添付（トークン）付きで作成
	_, up := upload(t, newClient(t), ts.URL+"/uploads.json?filename=hello.txt", "application/octet-stream", "hello", map[string]string{"basic": "jsmith"})
	token := regexp.MustCompile(`"token":"([^"]+)"`).FindStringSubmatch(up)[1]
	res, body = post(t, jsmith, ts.URL+"/projects/ecookbook/news", contentForm(t, jsmith, ts,
		"news[title]", "Release", "news[summary]", "S", "news[description]", "D", "attachments[1][token]", token))
	expectContentRedirect(t, res, body, "/projects/ecookbook/news")
	id := contentCount(t, d, `SELECT MAX(id) FROM news`)
	if n := contentCount(t, d, `SELECT COUNT(*) FROM attachments WHERE container_kind = 'news' AND container_id = ?`, id); n != 1 {
		t.Errorf("attachments: %d", n)
	}
	if n := contentCount(t, d, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'news' AND watchable_id = ? AND principal_id = 2`, id); n != 1 {
		t.Errorf("author not watching: %d", n)
	}
	_, body = get(t, jsmith, ts.URL+"/projects/ecookbook/news")
	if !strings.Contains(body, "Successful creation.") {
		t.Error("flash notice missing")
	}

	// 更新（manage_news を持たない rhill は 403）
	rhill := login(t, ts, "rhill", "foo")
	res, _ = post(t, rhill, ts.URL+"/news/1", contentForm(t, rhill, ts, "_method", "put", "news[title]", "x"))
	if res.StatusCode != 403 {
		t.Errorf("update by rhill: %d", res.StatusCode)
	}
	res, body = post(t, jsmith, ts.URL+"/news/1", contentForm(t, jsmith, ts, "_method", "patch", "news[title]", "Updated"))
	expectContentRedirect(t, res, body, "/news/1")

	// コメント（counter_cache）
	before := contentCount(t, d, `SELECT comments_count FROM news WHERE id = 1`)
	res, body = post(t, dlopper, ts.URL+"/news/1/comments", contentForm(t, dlopper, ts, "comment[comments]", "Nice"))
	expectContentRedirect(t, res, body, "/news/1")
	if n := contentCount(t, d, `SELECT comments_count FROM news WHERE id = 1`); n != before+1 {
		t.Errorf("comments_count %d, want %d", n, before+1)
	}
	cid := contentCount(t, d, `SELECT MAX(id) FROM news_comments`)
	res, body = post(t, jsmith, ts.URL+"/news/1/comments/"+itoa64(cid), contentForm(t, jsmith, ts, "_method", "delete"))
	expectContentRedirect(t, res, body, "/news/1")
	if n := contentCount(t, d, `SELECT comments_count FROM news WHERE id = 1`); n != before {
		t.Errorf("comments_count after destroy %d, want %d", n, before)
	}

	// 削除（コメント・添付・ウォッチャーも消える）
	res, body = post(t, jsmith, ts.URL+"/news/"+itoa64(id), contentForm(t, jsmith, ts, "_method", "delete"))
	expectContentRedirect(t, res, body, "/projects/ecookbook/news")
	for _, q := range []string{
		`SELECT COUNT(*) FROM news WHERE id = ?`,
		`SELECT COUNT(*) FROM attachments WHERE container_kind = 'news' AND container_id = ?`,
		`SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'news' AND watchable_id = ?`,
	} {
		if n := contentCount(t, d, q, id); n != 0 {
			t.Errorf("%s: %d", q, n)
		}
	}

	// API
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/projects/ecookbook/news.json", strings.NewReader(`{"news":{"title":""}}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("jsmith", "jsmith")
	apiRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readUnbranded(apiRes.Body)
	apiRes.Body.Close()
	if apiRes.StatusCode != 422 || string(b) != `{"errors":["Title cannot be blank","Description cannot be blank"]}` {
		t.Errorf("api invalid create: %d %s", apiRes.StatusCode, b)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// newFixtureServerWithFiles は newFixtureServer に加えて添付ファイルの保存先を返す。
func newFixtureServerWithFiles(t *testing.T) (*httptest.Server, *db.DB, string) {
	t.Helper()
	var root string
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { root = a.AttachmentStore.Root })
	return ts, d, root
}

// TestDocumentsAndFiles は文書の作成・添付の追加・削除と、ファイルの追加を確認する。
func TestDocumentsAndFiles(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	api := map[string]string{"basic": "jsmith"}
	tok := func(name, body string) string {
		_, up := upload(t, newClient(t), ts.URL+"/uploads.json?filename="+name, "application/octet-stream", body, api)
		return regexp.MustCompile(`"token":"([^"]+)"`).FindStringSubmatch(up)[1]
	}

	res, body := post(t, jsmith, ts.URL+"/projects/ecookbook/documents", contentForm(t, jsmith, ts,
		"document[category_id]", "2", "document[title]", "Guide", "attachments[1][token]", tok("a.txt", "aaa")))
	expectContentRedirect(t, res, body, "/projects/ecookbook/documents")
	id := contentCount(t, d, `SELECT MAX(id) FROM documents`)
	res, body = post(t, jsmith, ts.URL+"/documents/"+itoa64(id)+"/add_attachment", contentForm(t, jsmith, ts, "attachments[1][token]", tok("b.txt", "bbb")))
	expectContentRedirect(t, res, body, "/documents/"+itoa64(id))
	if n := contentCount(t, d, `SELECT COUNT(*) FROM attachments WHERE container_kind = 'document' AND container_id = ?`, id); n != 2 {
		t.Fatalf("document attachments: %d", n)
	}
	_, body = get(t, jsmith, ts.URL+"/projects/ecookbook/documents?sort_by=title")
	if !strings.Contains(body, `<h3 class="group-name">G</h3>`) {
		t.Error("title group missing")
	}
	res, body = post(t, jsmith, ts.URL+"/documents/"+itoa64(id), contentForm(t, jsmith, ts, "_method", "delete"))
	expectContentRedirect(t, res, body, "/projects/ecookbook/documents")
	if n := contentCount(t, d, `SELECT COUNT(*) FROM attachments WHERE container_kind = 'document' AND container_id = ?`, id); n != 0 {
		t.Errorf("attachments left: %d", n)
	}

	// ファイル（バージョンに追加）
	res, body = post(t, jsmith, ts.URL+"/projects/ecookbook/files", contentForm(t, jsmith, ts, "version_id", "2", "attachments[1][token]", tok("c.zip", "ccc")))
	expectContentRedirect(t, res, body, "/projects/ecookbook/files")
	if n := contentCount(t, d, `SELECT COUNT(*) FROM attachments WHERE container_kind = 'version' AND container_id = 2`); n != 1 {
		t.Errorf("version files: %d", n)
	}
	res, body = post(t, jsmith, ts.URL+"/projects/ecookbook/files", contentForm(t, jsmith, ts))
	if res.StatusCode != 200 || !strings.Contains(body, "File is invalid") {
		t.Errorf("files create without file: %d", res.StatusCode)
	}
}

// TestBoardsAndMessages はトピックの作成・返信・編集（移動・ロック）・削除とカウンターを確認する。
func TestBoardsAndMessages(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	dlopper := login(t, ts, "dlopper", "foo")

	res, body := post(t, jsmith, ts.URL+"/boards/1/topics/new", contentForm(t, jsmith, ts, "message[subject]", "Topic", "message[content]", "Body"))
	topic := contentCount(t, d, `SELECT MAX(id) FROM messages`)
	expectContentRedirect(t, res, body, "/boards/1/topics/"+itoa64(topic))
	res, body = post(t, dlopper, ts.URL+"/boards/1/topics/"+itoa64(topic)+"/replies", contentForm(t, dlopper, ts, "reply[subject]", "RE: Topic", "reply[content]", "Reply"))
	reply := contentCount(t, d, `SELECT MAX(id) FROM messages`)
	expectContentRedirect(t, res, body, "/boards/1/topics/"+itoa64(topic)+"?r="+itoa64(reply))
	if n := contentCount(t, d, `SELECT replies_count FROM messages WHERE id = ?`, topic); n != 1 {
		t.Errorf("replies_count: %d", n)
	}
	if n := contentCount(t, d, `SELECT last_reply_id FROM messages WHERE id = ?`, topic); n != reply {
		t.Errorf("last_reply_id: %d", n)
	}
	if n := contentCount(t, d, `SELECT last_message_id FROM boards WHERE id = 1`); n != reply {
		t.Errorf("last_message_id: %d", n)
	}
	// 作成者はトピックをウォッチする
	if n := contentCount(t, d, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'message' AND watchable_id = ? AND principal_id IN (2, 3)`, topic); n != 2 {
		t.Errorf("watchers: %d", n)
	}

	// ロックと別のフォーラムへの移動
	res, body = post(t, jsmith, ts.URL+"/boards/1/topics/"+itoa64(topic)+"/edit", contentForm(t, jsmith, ts,
		"message[subject]", "Topic", "message[content]", "Body", "message[locked]", "1", "message[board_id]", "2"))
	expectContentRedirect(t, res, body, "/boards/2/topics/"+itoa64(topic))
	if n := contentCount(t, d, `SELECT COUNT(*) FROM messages WHERE board_id = 2`); n != 2 {
		t.Errorf("moved messages: %d", n)
	}
	if n := contentCount(t, d, `SELECT messages_count FROM boards WHERE id = 2`); n != 2 {
		t.Errorf("board 2 messages_count: %d", n)
	}
	// ロックされたトピックには返信できない（flash は成功のまま、返信は作られない）
	before := contentCount(t, d, `SELECT COUNT(*) FROM messages`)
	post(t, dlopper, ts.URL+"/boards/2/topics/"+itoa64(topic)+"/replies", contentForm(t, dlopper, ts, "reply[subject]", "x", "reply[content]", "y"))
	if n := contentCount(t, d, `SELECT COUNT(*) FROM messages`); n != before {
		t.Errorf("reply to locked topic created")
	}
	// 他人のメッセージは編集できない
	res, _ = post(t, dlopper, ts.URL+"/boards/2/topics/"+itoa64(topic)+"/edit", contentForm(t, dlopper, ts, "message[subject]", "x", "message[content]", "y"))
	if res.StatusCode != 403 {
		t.Errorf("edit by dlopper: %d", res.StatusCode)
	}
	// 返信の削除（トピックへ戻る）とトピックの削除
	res, body = post(t, jsmith, ts.URL+"/boards/2/topics/"+itoa64(reply)+"/destroy", contentForm(t, jsmith, ts))
	expectContentRedirect(t, res, body, "/boards/2/topics/"+itoa64(topic)+"?r="+itoa64(reply))
	if n := contentCount(t, d, `SELECT replies_count FROM messages WHERE id = ?`, topic); n != 0 {
		t.Errorf("replies_count after destroy: %d", n)
	}
	res, body = post(t, jsmith, ts.URL+"/boards/2/topics/"+itoa64(topic)+"/destroy", contentForm(t, jsmith, ts))
	expectContentRedirect(t, res, body, "/projects/ecookbook/boards/2")
	if n := contentCount(t, d, `SELECT topics_count FROM boards WHERE id = 2`); n != 0 {
		t.Errorf("topics_count after destroy: %d", n)
	}

	// 引用（XHR の JS）
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/boards/1/topics/quote/1.js", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	qres, err := jsmith.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	qb, _ := readUnbranded(qres.Body)
	qres.Body.Close()
	want := "$('#message_subject').val(\"RE: First post\");\n$('#message_content').val(\"Redmine Admin wrote:\\n> This is the very first post\\n> in the forum\\n\\n\");\n"
	if qres.StatusCode != 200 || !strings.HasPrefix(string(qb), want) {
		t.Errorf("quote: %d\n%s", qres.StatusCode, qb)
	}
}

// TestWatchersWatchUnwatch は watchers#watch / unwatch（JS の応答とウォッチャーの行）を確認する。
func TestWatchersWatchUnwatch(t *testing.T) {
	ts, d := newFixtureServer(t)
	dlopper := login(t, ts, "dlopper", "foo")
	send := func(method string) (int, string) {
		form := contentForm(t, dlopper, ts)
		req, _ := http.NewRequest(method, ts.URL+"/watchers/watch?object_type=board&object_id=1", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "text/javascript")
		res, err := dlopper.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := readUnbranded(res.Body)
		return res.StatusCode, string(b)
	}
	status, body := send(http.MethodPost)
	if status != 200 || !strings.Contains(body, `$(".board-1-watcher").each(function(){$(this).replaceWith("<a class=\"board-1-watcher icon icon-fav\"`) {
		t.Fatalf("watch: %d\n%s", status, body)
	}
	if n := contentCount(t, d, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'board' AND watchable_id = 1 AND principal_id = 3`); n != 1 {
		t.Errorf("watcher not added")
	}
	status, body = send(http.MethodDelete)
	if status != 200 || !strings.Contains(body, `icon-fav-off`) {
		t.Fatalf("unwatch: %d\n%s", status, body)
	}
	if n := contentCount(t, d, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'board' AND watchable_id = 1 AND principal_id = 3`); n != 0 {
		t.Errorf("watcher not removed")
	}
	// 匿名はログインへ
	res, _ := post(t, newClient(t), ts.URL+"/watchers/watch?object_type=board&object_id=1", url.Values{})
	if res.StatusCode != 422 && res.StatusCode != 302 {
		t.Errorf("anonymous watch: %d", res.StatusCode)
	}
}

// writeAttachmentFile はフィクスチャの添付のディスク上のファイルを作る。
func writeAttachmentFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAttachmentsShowThumbnailDestroy は添付の表示（テキスト・画像・差分）・サムネイル・更新 API・
// チケットの添付の削除（ジャーナルへの記録）を確認する。
func TestAttachmentsShowThumbnailDestroy(t *testing.T) {
	ts, d, root := newFixtureServerWithFiles(t)
	admin := login(t, ts, "admin", "admin")
	writeAttachmentFile(t, root, "2006/07/060719210727_source.rb", []byte("# The Greeter class\nclass Greeter\nend\n"))
	writeAttachmentFile(t, root, "2006/07/060719210727_changeset_utf8.diff",
		[]byte("--- a/foo.rb\n+++ b/foo.rb\n@@ -1,3 +1,3 @@\n context\n-old line\n+new line\n end\n"))
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 400; x++ {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255})
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	writeAttachmentFile(t, root, "2010/11/101123161450_testfile_1.png", buf.Bytes())

	_, body := get(t, admin, ts.URL+"/attachments/4")
	if !strings.Contains(body, `<span class="nc">Greeter</span>`) || !strings.Contains(body, `<tr id="L3">`) {
		t.Errorf("text preview:\n%s", contentMain(body))
	}
	_, body = get(t, admin, ts.URL+"/attachments/14?type=sbs")
	if !strings.Contains(body, `<td class="line-code diff_out">`) || !strings.Contains(body, `<div><span>old</span> line</div>`) {
		t.Errorf("diff preview:\n%s", contentMain(body))
	}
	res, _ := get(t, admin, ts.URL+"/attachments/4/wrong.rb")
	if res.StatusCode != 404 {
		t.Errorf("wrong filename: %d", res.StatusCode)
	}

	// サムネイル（400x200 → 200x100）
	res, b := get(t, admin, ts.URL+"/attachments/thumbnail/16/200")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("thumbnail: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	th, _, err := image.Decode(strings.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if s := th.Bounds().Size(); s.X != 200 || s.Y != 100 {
		t.Errorf("thumbnail size %v", s)
	}
	res, _ = get(t, admin, ts.URL+"/attachments/thumbnail/4")
	if res.StatusCode != 404 {
		t.Errorf("thumbnail of non image: %d", res.StatusCode)
	}

	// 更新 API
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/attachments/4.json", strings.NewReader(`{"attachment":{"filename":"x/../renamed.rb","description":"Desc"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "admin")
	ares, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ares.Body.Close()
	var fn string
	if err := d.Get(context.Background(), &fn, `SELECT filename FROM attachments WHERE id = 4`); err != nil {
		t.Fatal(err)
	}
	if ares.StatusCode != 204 || fn != "renamed.rb" {
		t.Errorf("update api: %d %q", ares.StatusCode, fn)
	}

	// チケットの添付の削除はジャーナルに記録される
	res, b = post(t, admin, ts.URL+"/attachments/4", contentForm(t, admin, ts, "_method", "delete"))
	if res.StatusCode != 302 {
		t.Fatalf("destroy: %d\n%s", res.StatusCode, b)
	}
	if n := contentCount(t, d, `SELECT COUNT(*) FROM issue_journal_details WHERE property = 'attachment' AND prop_key = '4' AND old_value = 'renamed.rb'`); n != 1 {
		t.Errorf("journal detail: %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, "2006/07/060719210727_source.rb")); !os.IsNotExist(err) {
		t.Errorf("file not removed: %v", err)
	}
}
