package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/verify"
)

// richKey は export パッケージの populate.rb で使った database_cipher_key。
const richKey = "export-test-key"

func richArchive(t *testing.T) (dir, archivePath string) {
	t.Helper()
	ref := findReference(t)
	src := filepath.Join(ref, "export-test", "rich.sqlite3")
	files := filepath.Join(ref, "export-test", "rich-files")
	if _, err := os.Stat(src); err != nil {
		t.Skip("rich.sqlite3 not found")
	}
	dir = workDir(t, ref)
	return dir, exportFixture(t, dir, src, files, "Asia/Tokyo", true)
}

func openSealed(t *testing.T, box *secretbox.Box, v *string) string {
	t.Helper()
	if v == nil {
		t.Fatal("secret is NULL")
	}
	if !secretbox.IsSealed(*v) {
		t.Fatalf("secret not sealed: %q", *v)
	}
	p, err := box.Open(*v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportRich(t *testing.T) {
	dir, archivePath := richArchive(t)
	targets(t, dir, func(t *testing.T, d *db.DB, filesDir string) {
		ctx := context.Background()
		rep, err := Run(ctx, d, archivePath, Options{CipherKey: richKey, NewCipherKey: "new-secret", FilesDir: filesDir, TempDir: dir})
		if err != nil {
			var sb strings.Builder
			rep.WriteText(&sb)
			t.Fatalf("import: %v\n%s", err, sb.String())
		}
		box, _ := secretbox.New("new-secret")
		// 暗号化列: 復号して buropher の鍵で再暗号化されている
		if s := q1[*string](t, d, `SELECT twofa_scheme FROM user_accounts WHERE login = 'taro'`); s == nil || *s != "totp" {
			t.Errorf("taro twofa_scheme = %v", s)
		}
		if p := openSealed(t, box, q1[*string](t, d, `SELECT twofa_totp_key FROM user_accounts WHERE login = 'taro'`)); p != "JBSWY3DPEHPK3PXP" {
			t.Errorf("totp key = %q", p)
		}
		if p := openSealed(t, box, q1[*string](t, d, `SELECT secret FROM auth_sources WHERE name = 'LDAP'`)); p != "ldap-secret" {
			t.Errorf("ldap secret = %q", p)
		}
		if p := openSealed(t, box, q1[*string](t, d, `SELECT password FROM repositories WHERE login = 'svnuser'`)); p != "svn-secret" {
			t.Errorf("repository password = %q", p)
		}
		jsonEq(t, "auth source config", q1[string](t, d, `SELECT config FROM auth_sources WHERE name = 'LDAP'`),
			`{"host":"ldap.example.com","port":389,"account":"cn=admin","base_dn":"dc=example,dc=com","tls":false,"verify_peer":true,"attr_login":"uid"}`)
		jsonEq(t, "extra_info", q1[string](t, d, `SELECT extra_info FROM repositories WHERE login = 'svnuser'`), `{"extra_report_last_commit":"1"}`)
		// タイムゾーン: Asia/Tokyo の naive 値 → UTC(-9h)
		if s := tsStr(t, d, `SELECT created_at FROM issues WHERE id = 2`); s != "2026-10-02T15:18:03.932350Z" {
			t.Errorf("issue 2 created_at = %s", s)
		}
		// 設定
		jsonEq(t, "app_title", q1[string](t, d, `SELECT value FROM settings WHERE name = 'app_title'`), `"Export テスト"`)
		jsonEq(t, "enabled_scm", q1[string](t, d, `SELECT value FROM settings WHERE name = 'enabled_scm'`), `["Subversion","Git"]`)
		jsonEq(t, "commit_update_keywords", q1[string](t, d, `SELECT value FROM settings WHERE name = 'commit_update_keywords'`), `[{"keywords":"fixes","status_id":"3"}]`)
		// 個人設定
		taro := q1[int64](t, d, `SELECT principal_id FROM user_accounts WHERE login = 'taro'`)
		if tz := q1[*string](t, d, `SELECT time_zone FROM user_preferences WHERE user_id = ?`, taro); tz == nil || *tz != "Tokyo" {
			t.Errorf("time_zone = %v", tz)
		}
		jsonEq(t, "extra", q1[string](t, d, `SELECT extra FROM user_preferences WHERE user_id = ?`, taro), `{"gantt_zoom":2}`)
		if n := q1[int](t, d, `SELECT COUNT(*) FROM user_recent_projects WHERE user_id = ?`, taro); n != 1 {
			t.Errorf("recent projects = %d", n)
		}
		jsonEq(t, "my_page_settings", q1[string](t, d, `SELECT my_page_settings FROM user_preferences WHERE user_id = 1`),
			`{"issuesassignedtome":{"columns":["subject"],"sort":"id"}}`)
		if s := q1[string](t, d, `SELECT comments_sorting FROM user_preferences WHERE user_id = 1`); s != "desc" {
			t.Errorf("comments_sorting = %q", s)
		}
		if b := q1[bool](t, d, `SELECT no_self_notified FROM user_notification_settings WHERE user_id = ?`, taro); !b {
			t.Error("taro no_self_notified")
		}
		if v := q1[string](t, d, `SELECT value FROM tokens WHERE action = 'api'`); v != "b729c92556c3366938d4bacc420cd0e71735051f" {
			t.Errorf("api token = %q", v)
		}
		// Wiki: gzip(zlib)圧縮の旧版と非圧縮版
		vs := qs[string](t, d, `SELECT v.text FROM wiki_page_versions v JOIN wiki_pages p ON p.id = v.page_id WHERE p.title = 'Wiki' ORDER BY v.version`)
		if len(vs) != 3 || !strings.Contains(vs[0], "本文 v1") || !strings.Contains(vs[1], "本文 v2 長文") || vs[2] != "本文 v3(非圧縮)" {
			t.Errorf("wiki versions = %q", vs)
		}
		// 複数値カスタムフィールドは 1 値 1 行
		if n := q1[int](t, d, `SELECT COUNT(*) FROM custom_values v JOIN custom_fields f ON f.id = v.custom_field_id WHERE f.name = 'リスト' AND v.customized_id = 2`); n != 2 {
			t.Errorf("multi-value rows = %d", n)
		}
		// 添付: 重複排除で共有されたファイル、欠損ファイル
		if rep.Files.Copied != 2 || rep.Files.Missing != 1 {
			t.Errorf("files = %+v", rep.Files)
		}
		vr, err := verify.Verify(ctx, d, archivePath, verify.Options{FilesDir: filesDir, Digests: true, Passwords: map[string]string{"taro": "password123"}})
		if err != nil {
			t.Fatal(err)
		}
		if !vr.OK() {
			var sb strings.Builder
			vr.WriteText(&sb)
			t.Errorf("verify failed:\n%s", sb.String())
		}
	})
}

// 鍵なし: TOTP は 2FA リセット、LDAP/リポジトリのパスワードは空にして報告する。
func TestImportRichWithoutCipherKey(t *testing.T) {
	dir, archivePath := richArchive(t)
	d := newTarget(t, dir)
	rep, err := Run(context.Background(), d, archivePath, Options{NewCipherKey: "new-secret", TempDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if s := q1[*string](t, d, `SELECT twofa_scheme FROM user_accounts WHERE login = 'taro'`); s != nil {
		t.Errorf("2FA should be reset, scheme = %q", *s)
	}
	if s := q1[*string](t, d, `SELECT secret FROM auth_sources`); s != nil {
		t.Errorf("ldap secret should be NULL")
	}
	if s := q1[*string](t, d, `SELECT password FROM repositories`); s != nil {
		t.Errorf("repository password should be NULL")
	}
	if rep.Lookup("users").Find("2FA reset") == nil || rep.Lookup("auth_sources").Find("no --cipher-key") == nil {
		t.Errorf("missing repair records: %+v %+v", rep.Lookup("users").Repaired, rep.Lookup("auth_sources").Repaired)
	}
	if rep.Files.Source != "archive" || rep.Files.Copied != 0 {
		t.Errorf("no files dir: files = %+v", rep.Files)
	}
}

// 復号できたが buropher の鍵がない場合は平文で保存せずエラーにする。
func TestImportRichNeedsSecretKey(t *testing.T) {
	dir, archivePath := richArchive(t)
	d := newTarget(t, dir)
	_, err := Run(context.Background(), d, archivePath, Options{CipherKey: richKey, TempDir: dir})
	if err == nil || !strings.Contains(err.Error(), "secret key") {
		t.Fatalf("err = %v", err)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM principals`); n != 0 {
		t.Errorf("failed import left %d principals", n)
	}
}
