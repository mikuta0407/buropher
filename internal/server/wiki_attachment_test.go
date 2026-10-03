package server_test

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
)

func mustParseID(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestWikiAddAttachment はアップロードのトークンで Wiki ページに添付を追加し、表示されることを確認する。
func TestWikiAddAttachment(t *testing.T) {
	ts, d := newFixtureServer(t)
	res, body := upload(t, newClient(t), ts.URL+"/uploads.json?filename=note.txt", "application/octet-stream", "hello", map[string]string{"basic": "admin"})
	if res.StatusCode != 201 {
		t.Fatalf("upload: %d %s", res.StatusCode, body)
	}
	m := regexp.MustCompile(`"token":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("token: %s", body)
	}
	c := login(t, ts, "admin", "admin")
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page/add_attachment", wikiForm(t, c, ts,
		"attachments[1][token]", m[1], "attachments[1][description]", "a note"))
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/ecookbook/wiki/Another_page" {
		t.Fatalf("add_attachment: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	var kind string
	var cid int64
	if err := d.QueryRow(context.Background(), `SELECT container_kind, container_id FROM attachments WHERE filename = 'note.txt'`).Scan(&kind, &cid); err != nil {
		t.Fatal(err)
	}
	if kind != "wiki_page" || cid != 2 {
		t.Errorf("container = %s %d", kind, cid)
	}
	_, body = get(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page")
	if !strings.Contains(body, `<span class="icon-label">note.txt</span></a>    <span class="size">(5 Bytes)</span>`) ||
		!strings.Contains(body, "<td>a note</td>") || !strings.Contains(body, "Files (1)") {
		t.Errorf("attachment not listed: %s", extract(body, `<div class="attachments">`, `</table>`))
	}
	// 編集フォームで削除にチェックした添付は保存時に消える
	_, body = get(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page/edit")
	if !strings.Contains(body, `name="wiki_page[deleted_attachment_ids][]"`) {
		t.Fatalf("existing attachments block missing")
	}
	var id string
	if err := d.Get(context.Background(), &id, `SELECT CAST(id AS TEXT) FROM attachments WHERE filename = 'note.txt'`); err != nil {
		t.Fatal(err)
	}
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "changed", "content[version]", "1", "wiki_page[deleted_attachment_ids][]", id))
	if res.StatusCode != 302 {
		t.Fatalf("update: %d", res.StatusCode)
	}
	var n int
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM attachments WHERE filename = 'note.txt'`); err != nil || n != 0 {
		t.Errorf("attachment should be deleted: %d %v", n, err)
	}
}

// 添付の削除を含む Wiki の更新がロールバックされたら（編集の競合など）、添付のファイルも残る
// （ファイルはコミット後に消す。after_commit :delete_from_disk）。成功すればファイルも消える。
func TestWikiUpdateRollbackKeepsAttachmentFile(t *testing.T) {
	srv, ts, d := newFixtureServerFull(t)
	ctx := context.Background()
	if d.Dialect().Name() != "sqlite" {
		t.Skip("uses an SQLite trigger to make the update fail")
	}
	res, body := upload(t, newClient(t), ts.URL+"/uploads.json?filename=keep.txt", "application/octet-stream", "keep me", map[string]string{"basic": "admin"})
	if res.StatusCode != 201 {
		t.Fatalf("upload: %d %s", res.StatusCode, body)
	}
	token := regexp.MustCompile(`"token":"([^"]+)"`).FindStringSubmatch(body)[1]
	c := login(t, ts, "admin", "admin")
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page/add_attachment", wikiForm(t, c, ts, "attachments[1][token]", token))
	if res.StatusCode != 302 {
		t.Fatalf("add_attachment: %d", res.StatusCode)
	}
	var id string
	if err := d.Get(ctx, &id, `SELECT CAST(id AS TEXT) FROM attachments WHERE filename = 'keep.txt'`); err != nil {
		t.Fatal(err)
	}
	att := &domain.Attachment{}
	if err := d.QueryRow(ctx, `SELECT COALESCE(disk_directory, ''), disk_filename FROM attachments WHERE id = ?`, mustParseID(t, id)).Scan(&att.DiskDirectory, &att.DiskFilename); err != nil {
		t.Fatal(err)
	}
	path := srv.App().AttachmentStore.Diskfile(att)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// 本文のバージョンの保存（添付の削除の後）を失敗させる
	if _, err := d.Exec(ctx, `CREATE TRIGGER fail_wiki_version BEFORE INSERT ON wiki_page_versions BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	update := func() *http.Response {
		res, _ := post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts,
			"_method", "put", "content[text]", "changed again", "content[version]", "1", "wiki_page[deleted_attachment_ids][]", id))
		return res
	}
	if res := update(); res.StatusCode == 302 {
		t.Fatalf("update should fail")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("attachment file removed although the update was rolled back: %v", err)
	}
	if _, err := d.Exec(ctx, `DROP TRIGGER fail_wiki_version`); err != nil {
		t.Fatal(err)
	}
	if res := update(); res.StatusCode != 302 {
		t.Fatalf("update: %d", res.StatusCode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("attachment file should be removed after commit: %v", err)
	}
}
