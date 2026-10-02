package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
)

// q1 は 1 値を返すクエリを実行する。
func q1[T any](t *testing.T, d *db.DB, q string, args ...any) T {
	t.Helper()
	var v T
	if err := d.Get(context.Background(), &v, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func qs[T any](t *testing.T, d *db.DB, q string, args ...any) []T {
	t.Helper()
	var v []T
	if err := d.Select(context.Background(), &v, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

// jsonEq は JSON 文字列を正規化して比較する(PG の jsonb はキー順を変えるため)。
func jsonEq(t *testing.T, what, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Errorf("%s: invalid JSON %q", what, got)
		return
	}
	json.Unmarshal([]byte(want), &w)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// ts は DB から読んだタイムスタンプを固定形式の文字列にする。
func tsStr(t *testing.T, d *db.DB, q string, args ...any) string {
	t.Helper()
	return q1[db.Time](t, d, q, args...).String()
}

// checkFixtureFacts は Redmine 公式フィクスチャの既知の事実を確認する。
func checkFixtureFacts(t *testing.T, d *db.DB, rep *Report, filesDir string) {
	t.Helper()
	// --- issues
	if s := q1[string](t, d, `SELECT subject FROM issues WHERE id = 1`); s != "Cannot print recipes" {
		t.Errorf("issue 1 subject = %q", s)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM issues`); n != 14 {
		t.Errorf("issues = %d", n)
	}
	if p := q1[string](t, d, `SELECT hier_path FROM issues WHERE id = 1`); p != "0000000001/" {
		t.Errorf("hier_path = %q", p)
	}
	if s := tsStr(t, d, `SELECT created_at FROM issues WHERE id = 1`); s != "2026-01-12T12:00:00.000000Z" {
		t.Errorf("issue 1 created_at = %s", s)
	}
	if s := q1[db.NullTime](t, d, `SELECT closed_at FROM issues WHERE id = 8`); !s.Valid || db.FormatTime(s.Time) != "2026-01-12T12:00:00.000000Z" {
		t.Errorf("issue 8 closed_at = %v", s)
	}
	if v := q1[float64](t, d, `SELECT estimated_hours FROM issues WHERE id = 1`); v != 200 {
		t.Errorf("estimated_hours = %v", v)
	}
	if b := q1[bool](t, d, `SELECT is_private FROM issues WHERE id = 14`); !b {
		t.Error("issue 14 should be private")
	}
	// --- projects
	if ids := qs[int64](t, d, `SELECT id FROM projects WHERE parent_id = (SELECT id FROM projects WHERE identifier = 'ecookbook') ORDER BY id`); !reflect.DeepEqual(ids, []int64{3, 4, 5}) {
		t.Errorf("ecookbook children = %v", ids)
	}
	if ids := qs[int64](t, d, `SELECT descendant_id FROM project_closure WHERE ancestor_id = 1 ORDER BY descendant_id`); !reflect.DeepEqual(ids, []int64{1, 3, 4, 5, 6}) {
		t.Errorf("ecookbook descendants = %v", ids)
	}
	if n := q1[int](t, d, `SELECT depth FROM project_closure WHERE ancestor_id = 1 AND descendant_id = 6`); n != 2 {
		t.Errorf("depth = %d", n)
	}
	if h := q1[*string](t, d, `SELECT homepage FROM projects WHERE id = 2`); h != nil {
		t.Errorf("homepage '' should be NULL, got %q", *h)
	}
	// --- principals / passwords
	for login, pw := range map[string]string{"admin": "admin", "jsmith": "jsmith"} {
		h := q1[string](t, d, `SELECT password_hash FROM user_accounts WHERE login = ?`, login)
		if ok, err := password.Verify(h, pw); !ok || err != nil {
			t.Errorf("%s: password does not verify (%q, %v)", login, h, err)
		}
		if !strings.HasPrefix(h, "redmine-sha1$") {
			t.Errorf("%s: hash = %q", login, h)
		}
	}
	if b := q1[bool](t, d, `SELECT admin FROM user_accounts WHERE login = 'admin'`); !b {
		t.Error("admin is not admin")
	}
	if k := q1[string](t, d, `SELECT kind FROM principals WHERE id = 6`); k != "anonymous_user" {
		t.Errorf("principal 6 kind = %s", k)
	}
	if h := q1[*string](t, d, `SELECT password_hash FROM user_accounts WHERE principal_id = 6`); h != nil {
		t.Errorf("anonymous password_hash = %q", *h)
	}
	if n := q1[string](t, d, `SELECT name FROM principals WHERE id = 10`); n != "A Team" {
		t.Errorf("group 10 name = %q", n)
	}
	if n := q1[string](t, d, `SELECT kind FROM principals WHERE id = 12`); n != "group_non_member" {
		t.Errorf("principal 12 kind = %q", n)
	}
	if s := q1[int](t, d, `SELECT status FROM principals WHERE id = 5`); s != 3 {
		t.Errorf("dlopper2 status = %d", s)
	}
	if l := q1[*string](t, d, `SELECT language FROM user_accounts WHERE principal_id = 8`); l == nil || *l != "it" {
		t.Errorf("miscuser8 language = %v", l)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM group_users WHERE user_id = 8`); n != 2 {
		t.Errorf("group_users of 8 = %d", n)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM email_addresses WHERE is_default = ?`, true); n != 8 {
		t.Errorf("default emails = %d", n)
	}
	// --- preferences / notifications
	if ids := qs[int64](t, d, `SELECT project_id FROM user_project_bookmarks WHERE user_id = 1 ORDER BY position`); !reflect.DeepEqual(ids, []int64{1, 5}) {
		t.Errorf("bookmarks = %v", ids)
	}
	jsonEq(t, "my_page_layout", q1[string](t, d, `SELECT my_page_layout FROM user_preferences WHERE user_id = 1`),
		`{"left":["latestnews","documents"],"right":["issuesassignedtome"],"top":["calendar"]}`)
	if b := q1[bool](t, d, `SELECT hide_mail FROM user_preferences WHERE user_id = 3`); b {
		t.Error("user 3 hide_mail should be false")
	}
	if m := q1[*string](t, d, `SELECT mail_notification FROM user_notification_settings WHERE user_id = 1`); m == nil || *m != "all" {
		t.Errorf("admin mail_notification = %v", m)
	}
	if b := q1[bool](t, d, `SELECT no_self_notified FROM user_notification_settings WHERE user_id = 1`); b {
		t.Error("admin no_self_notified should be false")
	}
	// 設定行のないユーザーは default_users_no_self_notified(既定 '1')
	if b := q1[bool](t, d, `SELECT no_self_notified FROM user_notification_settings WHERE user_id = 4`); !b {
		t.Error("rhill no_self_notified should default to true")
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM user_notification_settings`); n != 9 {
		t.Errorf("notification settings = %d", n)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM user_notified_projects`); n != 6 {
		t.Errorf("user_notified_projects = %d", n)
	}
	// --- roles / workflows
	perms := qs[string](t, d, `SELECT permission FROM role_permissions WHERE role_id = 1`)
	if len(perms) != 66 {
		t.Errorf("role 1 permissions = %d, want 66", len(perms))
	}
	has := map[string]bool{}
	for _, p := range perms {
		has[p] = true
	}
	if !has["add_project"] || !has["manage_members"] || !has["view_issues"] || has["delete_time_entries"] {
		t.Errorf("role 1 permissions = %v", perms)
	}
	if rt := rep.Lookup("roles"); rt.Find(`unknown permission "delete_time_entries"`) == nil {
		t.Error("unknown permission not reported")
	}
	if n := q1[int](t, d, `SELECT builtin FROM roles WHERE id = 5`); n != 2 {
		t.Errorf("role 5 builtin = %d", n)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM workflow_transitions`); n != 276 {
		t.Errorf("transitions = %d", n)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE old_status_id IS NULL`); n != 7 {
		t.Errorf("initial transitions = %d", n)
	}
	jsonEq(t, "disabled_core_fields", q1[string](t, d, `SELECT disabled_core_fields FROM trackers WHERE id = 1`), `[]`)
	// --- enumerations
	if n := q1[string](t, d, `SELECT position_name FROM issue_priorities WHERE id = 5`); n != "default" {
		t.Errorf("priority 5 position_name = %q", n)
	}
	if b := q1[bool](t, d, `SELECT active FROM time_entry_activities WHERE id = 14`); b {
		t.Error("activity 14 should be inactive")
	}
	if et := rep.Lookup("enumerations"); et.Find(`unknown enumeration type "Enumeration"`) == nil {
		t.Error("abnormal enumeration types not reported")
	}
	// --- custom fields / values
	if v := q1[string](t, d, `SELECT value FROM custom_values WHERE customized_kind = 'issue' AND customized_id = 1 AND custom_field_id = 2`); v != "125" {
		t.Errorf("issue 1 cf 2 = %q", v)
	}
	if v := q1[*string](t, d, `SELECT value FROM custom_values WHERE id = 1`); v != nil {
		t.Errorf("empty value should be NULL, got %q", *v)
	}
	if v := q1[string](t, d, `SELECT value FROM custom_values WHERE id = 15`); v != "1" {
		t.Errorf("bool 't' should be normalized to '1', got %q", v)
	}
	if k := q1[string](t, d, `SELECT customized_kind FROM custom_values WHERE id = 3`); k != "principal" {
		t.Errorf("customized_kind = %q", k)
	}
	jsonEq(t, "possible_values", q1[string](t, d, `SELECT possible_values FROM custom_fields WHERE id = 1`), `["MySQL","PostgreSQL","Oracle"]`)
	if n := q1[int](t, d, `SELECT COUNT(*) FROM custom_fields WHERE id = 11`); n != 0 {
		t.Error("custom field of unknown type CustomField should be dropped")
	}
	// --- journals
	if ids := qs[int64](t, d, `SELECT id FROM issue_journals WHERE issue_id = 1 ORDER BY id`); !reflect.DeepEqual(ids, []int64{1, 2}) {
		t.Errorf("journals of issue 1 = %v", ids)
	}
	if n := q1[string](t, d, `SELECT notes FROM issue_journals WHERE id = 1`); n != "Journal notes" {
		t.Errorf("journal 1 notes = %q", n)
	}
	if s := tsStr(t, d, `SELECT created_at FROM issue_journals WHERE id = 1`); s != "2026-01-13T00:00:00.000000Z" {
		t.Errorf("journal 1 created_at = %s", s)
	}
	type detail struct {
		Prop string  `db:"property"`
		Key  string  `db:"prop_key"`
		Old  *string `db:"old_value"`
		New  *string `db:"value"`
	}
	ds := qs[detail](t, d, `SELECT property, prop_key, old_value, value FROM issue_journal_details WHERE journal_id = 1 ORDER BY id`)
	if len(ds) != 2 || ds[0].Key != "status_id" || *ds[0].Old != "1" || *ds[0].New != "2" || ds[1].Key != "done_ratio" {
		t.Errorf("journal 1 details = %+v", ds)
	}
	if n := q1[int64](t, d, `SELECT custom_field_id FROM issue_journal_details WHERE id = 5`); n != 2 {
		t.Errorf("cf detail custom_field_id = %d", n)
	}
	// --- relations / time entries
	type rel struct {
		From int64  `db:"issue_from_id"`
		To   int64  `db:"issue_to_id"`
		Type string `db:"relation_type"`
	}
	if rs := qs[rel](t, d, `SELECT issue_from_id, issue_to_id, relation_type FROM issue_relations ORDER BY id`); !reflect.DeepEqual(rs, []rel{{10, 9, "blocks"}, {2, 3, "relates"}}) {
		t.Errorf("relations = %+v", rs)
	}
	if w := q1[int](t, d, `SELECT tweek FROM time_entries WHERE id = 1`); w != 12 {
		t.Errorf("tweek = %d", w)
	}
	// --- wiki
	type ver struct {
		V        int     `db:"version"`
		Text     string  `db:"text"`
		Comments *string `db:"comments"`
	}
	vs := qs[ver](t, d, `SELECT v.version, v.text, v.comments FROM wiki_page_versions v JOIN wiki_pages p ON p.id = v.page_id
		WHERE p.title = 'CookBook_documentation' ORDER BY v.version`)
	if len(vs) != 3 {
		t.Fatalf("CookBook_documentation versions = %+v", vs)
	}
	if !strings.HasPrefix(vs[0].Text, "h1. CookBook documentation") || vs[1].Comments == nil || *vs[1].Comments != "Small update" {
		t.Errorf("versions = %+v", vs)
	}
	if !strings.Contains(vs[2].Text, "with gzipped history") {
		t.Errorf("latest version should come from wiki_contents: %q", vs[2].Text)
	}
	if c := q1[int](t, d, `SELECT current_version FROM wiki_pages WHERE title = 'CookBook_documentation'`); c != 3 {
		t.Errorf("current_version = %d", c)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM wiki_page_versions`); n != 15 {
		t.Errorf("wiki_page_versions = %d", n)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM wiki_pages WHERE current_version = 0`); n != 0 {
		t.Errorf("pages without current version = %d", n)
	}
	// --- forums / news counters (移行元の値を保持)
	type board struct {
		Topics   int    `db:"topics_count"`
		Messages int    `db:"messages_count"`
		Last     *int64 `db:"last_message_id"`
	}
	if b := q1[board](t, d, `SELECT topics_count, messages_count, last_message_id FROM boards WHERE id = 1`); b.Topics != 2 || b.Messages != 6 || b.Last == nil || *b.Last != 6 {
		t.Errorf("board 1 = %+v", b)
	}
	if n := q1[int](t, d, `SELECT replies_count FROM messages WHERE id = 1`); n != 2 {
		t.Errorf("message 1 replies = %d", n)
	}
	if n := q1[int](t, d, `SELECT comments_count FROM news WHERE id = 1`); n != 1 {
		t.Errorf("news 1 comments_count = %d", n)
	}
	// --- queries
	jsonEq(t, "query 1 filters", q1[string](t, d, `SELECT filters FROM queries WHERE id = 1`),
		`{"cf_1":{"values":["MySQL"],"operator":"="},"status_id":{"values":["1"],"operator":"o"},"cf_2":{"values":["125"],"operator":"="}}`)
	if d.Dialect().Name() == db.SQLite {
		// SQLite では挿入順(Ruby の Hash 順)が保たれる
		if f := q1[string](t, d, `SELECT filters FROM queries WHERE id = 1`); !strings.HasPrefix(f, `{"cf_1"`) || strings.Index(f, "status_id") > strings.Index(f, "cf_2") {
			t.Errorf("filter order not preserved: %s", f)
		}
	}
	jsonEq(t, "query 5 sort", q1[string](t, d, `SELECT sort_criteria FROM queries WHERE id = 5`), `[["priority","desc"],["tracker","asc"]]`)
	if u := q1[*int64](t, d, `SELECT user_id FROM queries WHERE id = 11`); u != nil {
		t.Errorf("query 11 user_id should be NULL (was 0), got %d", *u)
	}
	if s := q1[string](t, d, `SELECT display_type FROM queries WHERE id = 12`); s != "board" {
		t.Errorf("query 12 display_type = %q", s)
	}
	if k := q1[string](t, d, `SELECT kind FROM queries WHERE id = 10`); k != "time_entry" {
		t.Errorf("query 10 kind = %q", k)
	}
	// --- attachments / files
	if a := q1[string](t, d, `SELECT digest_algo FROM attachments WHERE id = 1`); a != "md5" {
		t.Errorf("attachment 1 digest_algo = %q", a)
	}
	if a := q1[string](t, d, `SELECT digest_algo FROM attachments WHERE id = 22`); a != "sha256" {
		t.Errorf("attachment 22 digest_algo = %q", a)
	}
	if k := q1[string](t, d, `SELECT container_kind FROM attachments WHERE id = 3`); k != "wiki_page" {
		t.Errorf("attachment 3 container_kind = %q", k)
	}
	if dd := q1[*string](t, d, `SELECT disk_directory FROM attachments WHERE id = 20`); dd != nil {
		t.Errorf("attachment 20 disk_directory should be NULL")
	}
	if _, err := os.Stat(filepath.Join(filesDir, "2006", "07", "060719210727_archive.zip")); err != nil {
		t.Errorf("attachment file not copied: %v", err)
	}
	if rep.Files.Copied == 0 || rep.Files.Missing == 0 {
		t.Errorf("files report = %+v", rep.Files)
	}
	// --- misc
	jsonEq(t, "setting rest_api_enabled", q1[string](t, d, `SELECT value FROM settings WHERE name = 'rest_api_enabled'`), `"1"`)
	if n := q1[int](t, d, `SELECT COUNT(*) FROM tokens`); n != 0 {
		t.Errorf("expired tokens should be dropped, got %d", n)
	}
	if s := q1[string](t, d, `SELECT scm FROM repositories WHERE id = 10`); s != "subversion" {
		t.Errorf("repository scm = %q", s)
	}
	if k := q1[string](t, d, `SELECT watchable_kind FROM watchers WHERE id = 201238997`); k != "wiki_page" {
		t.Errorf("watcher kind = %q", k)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM reactions WHERE reactable_kind = 'comment'`); n != 1 {
		t.Errorf("comment reactions = %d", n)
	}
	if n := q1[int](t, d, `SELECT COUNT(*) FROM member_roles WHERE inherited_from IS NOT NULL`); n != 3 {
		t.Errorf("inherited member roles = %d", n)
	}
	// シーケンスが max(id) の次から採番される
	id, err := d.InsertReturningID(context.Background(), `INSERT INTO issue_statuses (name, position) VALUES ('Seq test', 99)`)
	if err != nil || id != 7 {
		t.Errorf("next issue_statuses id = %d, %v", id, err)
	}
	if _, err := d.Exec(context.Background(), `DELETE FROM issue_statuses WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || !rep.Committed {
		t.Errorf("report not OK: %+v", rep.Checks)
	}
}
