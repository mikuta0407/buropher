package server_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// CSV インポート（ImportsController / IssueImport / TimeEntryImport / UserImport）の振る舞いのテスト。
// Redmine の test/unit/*_import_test.rb と test/functional/imports_controller_test.rb の移植で、
// CSV は Redmine の test/fixtures/files/import_*.csv（testdata/compat/files/imports）を使う。
// HTML の一致は compat シナリオ（testdata/compat/scenarios/imports_write.yml）で確認している。

const importFilesDir = "../../testdata/compat/files/imports/"

var importLocationRe = regexp.MustCompile(`/imports/([0-9a-f]{32})/settings$`)

// importUpload は CSV を POST /imports で送り、作成されたインポートのファイル名を返す。
func importUpload(t *testing.T, c *http.Client, ts *httptest.Server, typ, file, projectID string) string {
	t.Helper()
	res, body := importUploadRaw(t, c, ts, typ, file, projectID)
	m := importLocationRe.FindStringSubmatch(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || m == nil {
		t.Fatalf("create import: status %d location %q\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
	return m[1]
}

func importUploadRaw(t *testing.T, c *http.Client, ts *httptest.Server, typ, file, projectID string) (*http.Response, string) {
	t.Helper()
	_, page := get(t, c, ts.URL+"/my/account")
	tok := csrfMetaContentRe.FindStringSubmatch(page)
	if tok == nil {
		t.Fatal("csrf-token meta not found")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("authenticity_token", tok[1])
	_ = mw.WriteField("type", typ)
	if projectID != "" {
		_ = mw.WriteField("project_id", projectID)
	}
	if file != "" {
		data, err := os.ReadFile(importFilesDir + file)
		if err != nil {
			t.Fatal(err)
		}
		fw, _ := mw.CreateFormFile("file", file)
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/imports", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readUnbranded(res.Body)
	res.Body.Close()
	return res, string(b)
}

// importSettings は settings の送信（mapping を含む。import_settings[...] に展開する）。
func importSettings(t *testing.T, c *http.Client, ts *httptest.Server, id string, settings map[string]string, mapping map[string]string) (*http.Response, string) {
	t.Helper()
	form := url.Values{}
	for k, v := range settings {
		form.Set("import_settings["+k+"]", v)
	}
	for k, v := range mapping {
		form.Set("import_settings[mapping]["+k+"]", v)
	}
	return projSubmit(t, c, ts, http.MethodPost, "/imports/"+id+"/settings", form, false)
}

// importRunAll は完了するまで POST /imports/:id/run を繰り返す（リクエスト回数を返す）。
func importRunAll(t *testing.T, c *http.Client, ts *httptest.Server, id string) int {
	t.Helper()
	for i := 1; i <= 20; i++ {
		res, body := projSubmit(t, c, ts, http.MethodPost, "/imports/"+id+"/run", nil, false)
		switch loc := res.Header.Get("Location"); {
		case res.StatusCode == http.StatusFound && strings.HasSuffix(loc, "/imports/"+id):
			return i
		case res.StatusCode == http.StatusFound && strings.HasSuffix(loc, "/imports/"+id+"/run"):
		default:
			t.Fatalf("run: status %d location %q\n%s", res.StatusCode, loc, body)
		}
	}
	t.Fatal("run did not finish")
	return 0
}

var utf8Semicolon = map[string]string{"separator": ";", "wrapper": `"`, "encoding": "UTF-8"}

// runImport は generate_import_with_mapping + import.run に相当する（新しく作られた行の id を返す）。
func runImport(t *testing.T, c *http.Client, ts *httptest.Server, d *db.DB, typ, file, table string, settings, mapping map[string]string) (string, []int64) {
	t.Helper()
	before := queryInt(t, d, `SELECT COALESCE(MAX(id), 0) FROM `+table)
	id := importUpload(t, c, ts, typ, file, "")
	res, body := importSettings(t, c, ts, id, settings, mapping)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("settings: status %d\n%s", res.StatusCode, body)
	}
	importRunAll(t, c, ts, id)
	return id, queryIDs(t, d, `SELECT id FROM `+table+` WHERE id > ? ORDER BY id`, before)
}

func queryIDs(t *testing.T, d *db.DB, q string, args ...any) []int64 {
	t.Helper()
	var ids []int64
	if err := d.Select(context.Background(), &ids, q, args...); err != nil {
		t.Fatal(err)
	}
	return ids
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

var issueImportMapping = map[string]string{"project_id": "1", "tracker": "13", "subject": "1"}

func importItemMessages(t *testing.T, d *db.DB, filename string) []string {
	t.Helper()
	var msgs []string
	if err := d.Select(context.Background(), &msgs, `SELECT i.message FROM import_items i JOIN imports m ON m.id = i.import_id
WHERE m.filename = ? AND i.obj_id IS NULL ORDER BY i.position`, filename); err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestIssueImport(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")

	t.Run("create_versions_and_categories", func(t *testing.T) {
		v0 := queryInt(t, d, `SELECT MAX(id) FROM versions`)
		c0 := queryInt(t, d, `SELECT MAX(id) FROM issue_categories`)
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon,
			merge(issueImportMapping, map[string]string{"fixed_version": "9", "create_versions": "1", "category": "10", "create_categories": "1"}))
		if len(ids) != 3 {
			t.Fatalf("issues = %d", len(ids))
		}
		if n := queryString(t, d, `SELECT name FROM versions WHERE id > ?`, v0); n != "2.1" {
			t.Errorf("version = %q", n)
		}
		if n := queryString(t, d, `SELECT name FROM issue_categories WHERE id > ?`, c0); n != "New category" {
			t.Errorf("category = %q", n)
		}
	})

	t.Run("trackers", func(t *testing.T) {
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon,
			merge(issueImportMapping, map[string]string{"tracker": "value:2"}))
		for _, id := range ids {
			if tr := queryInt(t, d, `SELECT tracker_id FROM issues WHERE id = ?`, id); tr != 2 {
				t.Errorf("fixed tracker: issue %d tracker %d", id, tr)
			}
		}
		_, ids = runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon, issueImportMapping)
		var got []int64
		for _, id := range ids {
			got = append(got, queryInt(t, d, `SELECT tracker_id FROM issues WHERE id = ?`, id))
		}
		if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 1 {
			t.Errorf("mapped trackers = %v", got)
		}
	})

	t.Run("status_assignee_private_parent", func(t *testing.T) {
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon,
			merge(issueImportMapping, map[string]string{"status": "14", "assigned_to": "11", "is_private": "6", "parent_issue_id": "5"}))
		if len(ids) != 3 {
			t.Fatalf("issues = %d", len(ids))
		}
		var statuses []string
		for _, id := range ids {
			statuses = append(statuses, queryString(t, d, `SELECT s.name FROM issues i JOIN issue_statuses s ON s.id = i.status_id WHERE i.id = ?`, id))
		}
		if strings.Join(statuses, ",") != "New,New,Assigned" {
			t.Errorf("statuses = %v", statuses)
		}
		if a := queryInt(t, d, `SELECT COALESCE(assigned_to_id, 0) FROM issues WHERE id = ?`, ids[0]); a != 3 {
			t.Errorf("assignee = %d", a)
		}
		if p := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE id IN (?, ?, ?) AND is_private = TRUE`, ids[0], ids[1], ids[2]); p != 1 {
			t.Errorf("private = %d", p)
		}
		if p := queryInt(t, d, `SELECT COALESCE(parent_id, 0) FROM issues WHERE id = ?`, ids[0]); p != 0 {
			t.Errorf("first parent = %d", p)
		}
		if p := queryInt(t, d, `SELECT parent_id FROM issues WHERE id = ?`, ids[1]); p != ids[0] {
			t.Errorf("child parent = %d", p)
		}
		if p := queryInt(t, d, `SELECT parent_id FROM issues WHERE id = ?`, ids[2]); p != 2 {
			t.Errorf("existing parent = %d", p)
		}
	})

	t.Run("invalid_tracker", func(t *testing.T) {
		if _, err := d.Exec(context.Background(), `UPDATE trackers SET name = 'Feature' WHERE name = 'Feature request'`); err != nil {
			t.Fatal(err)
		}
		defer d.Exec(context.Background(), `UPDATE trackers SET name = 'Feature request' WHERE name = 'Feature'`)
		id, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon, issueImportMapping)
		if len(ids) != 2 {
			t.Errorf("issues = %d", len(ids))
		}
		if msgs := importItemMessages(t, d, id); len(msgs) != 1 || !strings.Contains(msgs[0], "Tracker cannot be blank") {
			t.Errorf("messages = %v", msgs)
		}
	})

	t.Run("utf8_with_bom", func(t *testing.T) {
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues_utf8_with_bom.csv", "issues", utf8Semicolon, issueImportMapping)
		if len(ids) != 3 {
			t.Errorf("issues = %d", len(ids))
		}
	})

	t.Run("backward_and_forward_parent", func(t *testing.T) {
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_subtasks.csv", "issues", utf8Semicolon,
			map[string]string{"project_id": "1", "tracker": "1", "subject": "2", "parent_issue_id": "3"})
		if len(ids) != 4 {
			t.Fatalf("issues = %d", len(ids))
		}
		root, child1, grandchild, child2 := ids[0], ids[1], ids[2], ids[3]
		if p := queryInt(t, d, `SELECT parent_id FROM issues WHERE id = ?`, child1); p != root {
			t.Errorf("child1 parent = %d", p)
		}
		if p := queryInt(t, d, `SELECT parent_id FROM issues WHERE id = ?`, grandchild); p != child2 {
			t.Errorf("grandchild parent = %d", p)
		}
	})

	t.Run("references_with_unique_id", func(t *testing.T) {
		id, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_subtasks_with_unique_id.csv", "issues", utf8Semicolon,
			map[string]string{"project_id": "1", "unique_id": "0", "tracker": "1", "subject": "2", "parent_issue_id": "3", "relation_follows": "4"})
		if len(ids) != 9 {
			t.Fatalf("issues = %d (%v)", len(ids), importItemMessages(t, d, id))
		}
		red4, red3, red2, red1, blue1, blue2, blue3, blue4, green := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5], ids[6], ids[7], ids[8]
		parent := func(id int64) int64 {
			return queryInt(t, d, `SELECT COALESCE(parent_id, 0) FROM issues WHERE id = ?`, id)
		}
		rel := func(from, to, delay int64) bool {
			return queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? AND issue_to_id = ? AND delay = ? AND relation_type = 'precedes'`, from, to, delay) == 1
		}
		if parent(red2) != red1 || parent(red4) != red3 {
			t.Errorf("future parents: %d %d", parent(red2), parent(red4))
		}
		if !rel(red2, red3, 1) {
			t.Error("future relation missing")
		}
		if parent(blue2) != blue1 || parent(blue4) != blue3 {
			t.Errorf("past parents: %d %d", parent(blue2), parent(blue4))
		}
		if !rel(blue2, blue3, 1) {
			t.Error("past relation missing")
		}
		if parent(green) != 1 || !rel(2, green, 3) {
			t.Errorf("existing issue references: parent %d", parent(green))
		}
	})

	t.Run("relates_and_delayed_relations", func(t *testing.T) {
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_subtasks.csv", "issues", utf8Semicolon,
			map[string]string{"project_id": "1", "tracker": "1", "subject": "2", "relation_relates": "4"})
		one, oneOne, oneTwoOne, oneTwo := ids[0], ids[1], ids[2], ids[3]
		count := func(id int64) int64 {
			return queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE relation_type = 'relates' AND (issue_from_id = ? OR issue_to_id = ?)`, id, id)
		}
		if count(one) != 2 || count(oneOne) != 2 || count(oneTwo) != 3 || count(oneTwoOne) != 1 {
			t.Errorf("relates counts: %d %d %d %d", count(one), count(oneOne), count(oneTwo), count(oneTwoOne))
		}
		_, ids = runImport(t, jsmith, ts, d, "IssueImport", "import_subtasks.csv", "issues", utf8Semicolon,
			map[string]string{"project_id": "1", "tracker": "1", "subject": "2", "relation_precedes": "5"})
		one, oneOne, oneTwoOne, oneTwo = ids[0], ids[1], ids[2], ids[3]
		rel := func(from, to, delay int64) bool {
			return queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? AND issue_to_id = ? AND delay = ? AND relation_type = 'precedes'`, from, to, delay) == 1
		}
		if !rel(oneOne, one, 2) || !rel(oneTwo, one, 1) || !rel(oneTwoOne, oneTwo, -1) {
			t.Error("delayed relations missing")
		}
	})

	t.Run("parent_and_follows_dates", func(t *testing.T) {
		r0 := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations`)
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_subtasks_with_relations.csv", "issues", utf8Semicolon,
			map[string]string{"project_id": "1", "tracker": "1", "subject": "2", "start_date": "3", "due_date": "4", "parent_issue_id": "5", "relation_follows": "6"})
		if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations`) - r0; n != 2 {
			t.Errorf("relations = %d", n)
		}
		second, first, parent, third := ids[0], ids[1], ids[2], ids[3]
		dates := func(id int64) string {
			return queryString(t, d, `SELECT start_date || ' ' || due_date FROM issues WHERE id = ?`, id)
		}
		want := map[int64]string{parent: "2020-01-01 2020-02-03", first: "2020-01-01 2020-01-10", second: "2020-01-14 2020-01-21", third: "2020-01-23 2020-02-03"}
		for id, w := range want {
			if got := dates(id); got != w {
				t.Errorf("issue %d dates %q, want %q", id, got, w)
			}
		}
		for _, c := range []int64{first, second, third} {
			if p := queryInt(t, d, `SELECT parent_id FROM issues WHERE id = ?`, c); p != parent {
				t.Errorf("parent of %d = %d", c, p)
			}
		}
	})

	t.Run("relations_and_invalid_issue", func(t *testing.T) {
		id, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues_with_relation_and_invalid_issues.csv", "issues", utf8Semicolon,
			map[string]string{"project_id": "1", "tracker": "1", "subject": "2", "status": "3", "relation_relates": "4"})
		if len(ids) != 4 {
			t.Fatalf("issues = %d", len(ids))
		}
		if msgs := importItemMessages(t, d, id); len(msgs) != 1 || !strings.Contains(msgs[0], "Subject cannot be blank") {
			t.Errorf("messages = %v", msgs)
		}
		if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ?`, ids[0]); n != 1 {
			t.Errorf("first relations_from = %d", n)
		}
	})

	t.Run("list_custom_fields", func(t *testing.T) {
		if _, err := d.Exec(context.Background(), `INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) SELECT 1, id FROM trackers WHERE id NOT IN (SELECT tracker_id FROM custom_fields_trackers WHERE custom_field_id = 1)`); err != nil {
			t.Fatal(err)
		}
		_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon,
			merge(issueImportMapping, map[string]string{"cf_1": "8"}))
		val := func(id int64) string {
			return queryString(t, d, `SELECT COALESCE(MAX(value), '') FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = 1`, id)
		}
		if val(ids[0]) != "PostgreSQL" || val(ids[1]) != "MySQL" || val(ids[2]) != "" {
			t.Errorf("list values: %q %q %q", val(ids[0]), val(ids[1]), val(ids[2]))
		}
		if _, err := d.Exec(context.Background(), `UPDATE custom_fields SET multiple = TRUE WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		_, ids = runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon,
			merge(issueImportMapping, map[string]string{"cf_1": "15"}))
		vals := queryIDs(t, d, `SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = 1 AND value IN ('Oracle', 'PostgreSQL')`, ids[0])
		if vals[0] != 2 {
			t.Errorf("multiple values = %v", vals)
		}
	})

	t.Run("dates_format", func(t *testing.T) {
		id, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_dates.csv", "issues",
			merge(utf8Semicolon, map[string]string{"date_format": "%d/%m/%Y"}),
			map[string]string{"project_id": "1", "tracker": "value:1", "subject": "0", "start_date": "1", "due_date": "2"})
		if len(ids) != 2 {
			t.Fatalf("issues = %d (%v)", len(ids), importItemMessages(t, d, id))
		}
		if s := queryString(t, d, `SELECT subject || ' ' || start_date || ' ' || due_date FROM issues WHERE id = ?`, ids[0]); s != "Valid dates 2015-07-10 2015-08-12" {
			t.Errorf("dates = %q", s)
		}
		if msgs := importItemMessages(t, d, id); len(msgs) != 1 || !strings.Contains(msgs[0], "Start date is not a valid date") {
			t.Errorf("messages = %v", msgs)
		}
		_, ids = runImport(t, jsmith, ts, d, "IssueImport", "import_dates_ja.csv", "issues",
			merge(utf8Semicolon, map[string]string{"date_format": "%Y/%m/%d"}),
			map[string]string{"project_id": "1", "tracker": "value:1", "subject": "0", "start_date": "1"})
		if s := queryString(t, d, `SELECT start_date FROM issues WHERE id = ?`, ids[0]); s != "2019-05-28" {
			t.Errorf("ja date = %q", s)
		}
	})
}

func TestImportsController(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	admin := login(t, ts, "admin", "admin")

	// new: アップロードフォーム・権限
	res, body := get(t, jsmith, ts.URL+"/issues/imports/new?project_id=subproject1")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `name="file"`) ||
		!strings.Contains(body, `<input type="hidden" name="project_id" id="project_id" value="subproject1" autocomplete="off" />`) {
		t.Fatalf("new: status %d", res.StatusCode)
	}
	if res, _ := get(t, jsmith, ts.URL+"/users/imports/new"); res.StatusCode != http.StatusForbidden {
		t.Errorf("users new by jsmith: %d", res.StatusCode)
	}
	if res, _ := get(t, admin, ts.URL+"/users/imports/new"); res.StatusCode != http.StatusOK {
		t.Errorf("users new by admin: %d", res.StatusCode)
	}
	if _, err := d.Exec(context.Background(), `DELETE FROM role_permissions WHERE permission = 'add_issues'`); err != nil {
		t.Fatal(err)
	}
	if res, _ := get(t, jsmith, ts.URL+"/issues/imports/new"); res.StatusCode != http.StatusForbidden {
		t.Errorf("new without add_issues: %d", res.StatusCode)
	}
	if _, err := d.Exec(context.Background(), `INSERT INTO role_permissions (role_id, permission) VALUES (1, 'add_issues'), (2, 'add_issues'), (3, 'add_issues')`); err != nil {
		t.Fatal(err)
	}

	// create: ファイルを保存し settings へ
	id := importUpload(t, jsmith, ts, "IssueImport", "import_issues.csv", "")
	if n := queryInt(t, d, `SELECT user_id FROM imports WHERE filename = ?`, id); n != 2 {
		t.Errorf("import user = %d", n)
	}
	if res, body := importUploadRaw(t, jsmith, ts, "IssueImport", "", ""); res.StatusCode != http.StatusOK || !strings.Contains(body, `name="file"`) {
		t.Errorf("create without file: %d", res.StatusCode)
	}
	if res, _ := importUploadRaw(t, jsmith, ts, "Foo", "import_issues.csv", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown type: %d", res.StatusCode)
	}

	// set_default_settings: 区切り文字・囲み文字の推定、利用者の言語の日付書式・文字コード
	for _, c := range []struct{ lang, file, want string }{
		{"fr", "import_issues_single_quotation.csv", `"date_format":"%d/%m/%Y","encoding":"UTF-8","notifications":"0","separator":";","wrapper":"'"`},
		{"ja", "import_iso8859-1.csv", `"date_format":"%Y/%m/%d","encoding":"CP932","notifications":"0","separator":";","wrapper":"\""`},
		{"en", "import_dates.csv", `"date_format":"%m/%d/%Y","encoding":"UTF-8","notifications":"0","separator":";","wrapper":"\""`},
	} {
		if _, err := d.Exec(context.Background(), `UPDATE user_accounts SET language = ? WHERE principal_id = 2`, c.lang); err != nil {
			t.Fatal(err)
		}
		f := importUpload(t, jsmith, ts, "IssueImport", c.file, "")
		if s := queryString(t, d, `SELECT settings FROM imports WHERE filename = ?`, f); !strings.Contains(s, c.want) {
			t.Errorf("%s default settings = %s, want %s", c.lang, s, c.want)
		}
	}
	if _, err := d.Exec(context.Background(), `UPDATE user_accounts SET language = 'en' WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	pf := importUpload(t, jsmith, ts, "IssueImport", "import_issues.csv", "ecookbook")
	if s := queryString(t, d, `SELECT settings FROM imports WHERE filename = ?`, pf); !strings.Contains(s, `"mapping":{"project_id":1}`) {
		t.Errorf("project_id default = %s", s)
	}

	// settings: 選択肢と更新
	_, body = get(t, jsmith, ts.URL+"/imports/"+id+"/settings")
	for _, s := range []string{`name="import_settings[separator]"`, `name="import_settings[wrapper]"`, `<option value="ISO-8859-1">ISO-8859-1</option>`,
		`<option value="CP932">CP932</option>`, `name="import_settings[date_format]"`} {
		if !strings.Contains(body, s) {
			t.Errorf("settings form lacks %s", s)
		}
	}
	res, _ = importSettings(t, jsmith, ts, id, map[string]string{"separator": ":", "wrapper": "|", "encoding": "UTF-8", "date_format": "%m/%d/%Y"}, nil)
	expectRedirect(t, res, "/imports/"+id+"/mapping")
	s := queryString(t, d, `SELECT settings FROM imports WHERE filename = ?`, id)
	for _, w := range []string{`"separator":":"`, `"wrapper":"|"`, `"date_format":"%m/%d/%Y"`} {
		if !strings.Contains(s, w) {
			t.Errorf("settings %s lacks %s", s, w)
		}
	}
	// 他のユーザーのインポートは 404
	if res, _ := get(t, admin, ts.URL+"/imports/"+id+"/settings"); res.StatusCode != http.StatusNotFound {
		t.Errorf("other user's import: %d", res.StatusCode)
	}

	// settings のエラー
	iso := importUpload(t, jsmith, ts, "IssueImport", "import_iso8859-1.csv", "")
	if !strings.Contains(queryString(t, d, `SELECT settings FROM imports WHERE filename = ?`, iso), `"encoding":"ISO-8859-1"`) {
		t.Error("encoding is not guessed from user language")
	}
	_, body = importSettings(t, jsmith, ts, iso, utf8Semicolon, nil)
	if !strings.Contains(body, "The file is not a valid UTF-8 encoded file") {
		t.Error("invalid UTF-8 error not shown")
	}
	res, _ = importSettings(t, jsmith, ts, iso, map[string]string{"separator": ";", "wrapper": `"`, "encoding": "ISO-8859-1"}, nil)
	expectRedirect(t, res, "/imports/"+iso+"/mapping")
	if n := queryInt(t, d, `SELECT total_items FROM imports WHERE filename = ?`, iso); n != 2 {
		t.Errorf("total_items = %d", n)
	}
	_, body = get(t, jsmith, ts.URL+"/imports/"+iso+"/mapping")
	if !strings.Contains(body, `<option value="0">column A</option>`) || strings.Count(body, "<td>") != 9 || !strings.Contains(body, "<td>Contenu en français</td>") {
		t.Error("mapping form/preview mismatch")
	}
	sjis := importUpload(t, jsmith, ts, "IssueImport", "invalid-Shift_JIS.csv", "")
	if _, body := importSettings(t, jsmith, ts, sjis, map[string]string{"separator": ";", "wrapper": `"`, "encoding": "Shift_JIS"}, nil); !strings.Contains(body, "not a valid Shift_JIS encoded file") {
		t.Error("invalid Shift_JIS error not shown")
	}
	unclosed := importUpload(t, jsmith, ts, "IssueImport", "unclosed_quoted_field.csv", "")
	if _, body := importSettings(t, jsmith, ts, unclosed, map[string]string{"separator": ";", "wrapper": `"`, "encoding": "US-ASCII"}, nil); !strings.Contains(body, "The file is not a CSV file or does not match the settings below (Unclosed quoted field in line 2.)") {
		t.Error("malformed CSV error not shown")
	}
	nodata := importUpload(t, jsmith, ts, "IssueImport", "import_issues_no_data_row.csv", "")
	if _, body := importSettings(t, jsmith, ts, nodata, map[string]string{"separator": ";", "wrapper": `"`, "encoding": "ISO-8859-1"}, nil); !strings.Contains(body, "The file does not contain any data") {
		t.Error("no data error not shown")
	}

	// mapping: 自動対応付け
	auto := importUpload(t, jsmith, ts, "IssueImport", "import_issues_auto_mapping.csv", "")
	importSettings(t, jsmith, ts, auto, map[string]string{"separator": ";", "wrapper": `"`, "encoding": "ISO-8859-1"}, nil)
	_, body = get(t, jsmith, ts.URL+"/imports/"+auto+"/mapping")
	for _, w := range []string{
		`<option selected="selected" value="1">Subject</option>`, `<option selected="selected" value="10">estimated_hours</option>`,
		`<option selected="selected" value="7">target version</option>`, `<option selected="selected" value="14">cf_6</option>`,
		`<option selected="selected" value="13">database</option>`, `<option selected="selected" value="15">unique_id</option>`,
		`<option selected="selected" value="16">Is duplicate of</option>`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("auto mapping lacks %s", w)
		}
	}
	if m := regexp.MustCompile(`(?s)id="import_mapping_assigned_to">.*?</select>`).FindString(body); strings.Contains(m, "selected") {
		t.Error("assigned_to should not be auto mapped")
	}

	// mapping の POST とインポートの実行（max_items ごとに分割、再開）
	big := importUpload(t, jsmith, ts, "IssueImport", "import_subtasks_with_unique_id.csv", "")
	importSettings(t, jsmith, ts, big, utf8Semicolon, nil)
	res, _ = projSubmit(t, jsmith, ts, http.MethodPost, "/imports/"+big+"/mapping", url.Values{
		"import_settings[mapping][project_id]": {"1"}, "import_settings[mapping][tracker]": {"1"}, "import_settings[mapping][subject]": {"2"},
	}, false)
	expectRedirect(t, res, "/imports/"+big+"/run")
	if res, body := get(t, jsmith, ts.URL+"/imports/"+big+"/run"); res.StatusCode != http.StatusOK || !strings.Contains(body, `id="import-progress"`) {
		t.Errorf("run page: %d", res.StatusCode)
	}
	if n := importRunAll(t, jsmith, ts, big); n != 2 {
		t.Errorf("run requests = %d, want 2 (5 items per request)", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM import_items i JOIN imports m ON m.id = i.import_id WHERE m.filename = ?`, big); n != 9 {
		t.Errorf("items = %d", n)
	}
	if n := queryInt(t, d, `SELECT finished FROM imports WHERE filename = ?`, big); n != 1 {
		t.Error("import not finished")
	}
	// 完了後は show へ
	res, _ = get(t, jsmith, ts.URL+"/imports/"+big+"/mapping")
	expectRedirect(t, res, "/imports/"+big)
	_, body = get(t, jsmith, ts.URL+"/imports/"+big)
	saved := regexp.MustCompile(`(?s)<ul id="saved-items">.*?</ul>`).FindString(body)
	if strings.Count(saved, "<li>") != 9 || strings.Contains(body, "unsaved-items") {
		t.Errorf("show without errors mismatch: %d items\n%s", strings.Count(saved, "<li>"), saved)
	}

	// エラーのある行の表示
	errs := importUpload(t, jsmith, ts, "IssueImport", "import_issues.csv", "")
	importSettings(t, jsmith, ts, errs, utf8Semicolon, map[string]string{"project_id": "1", "tracker": "13", "subject": "20"})
	importRunAll(t, jsmith, ts, errs)
	_, body = get(t, jsmith, ts.URL+"/imports/"+errs)
	if !strings.Contains(body, `<table id="unsaved-items" class="list">`) || strings.Count(body, "Subject cannot be blank") != 3 {
		t.Error("show with errors mismatch")
	}
}

func TestTimeEntryImport(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	mapping := map[string]string{"project_id": "1", "activity": "value:10", "issue_id": "1", "spent_on": "2", "hours": "3", "comments": "4", "user": "7"}
	col := func(id int64, c string) string {
		return queryString(t, d, `SELECT COALESCE(CAST(`+c+` AS TEXT), '') FROM time_entries WHERE id = ?`, id)
	}

	_, ids := runImport(t, jsmith, ts, d, "TimeEntryImport", "import_time_entries.csv", "time_entries", utf8Semicolon, mapping)
	if len(ids) != 4 {
		t.Fatalf("time entries = %d", len(ids))
	}
	want := [][]string{
		{"", "2020-01-01", "1.0", "Some Design", "10", "2"},
		{"", "2020-01-02", "2.0", "Some Development", "10", "2"},
		{"1", "2020-01-03", "3.0", "Some QA", "10", "2"},
		{"2", "2020-01-04", "4.0", "Some Inactivity", "10", "2"},
	}
	for i, id := range ids {
		got := []string{col(id, "issue_id"), col(id, "spent_on"), col(id, "hours"), col(id, "comments"), col(id, "activity_id"), col(id, "user_id")}
		if strings.Join(got, "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %v, want %v", i+1, got, want[i])
		}
	}

	// 作業分類を列から（無効な作業分類の行は取り込まれない）
	id, ids := runImport(t, jsmith, ts, d, "TimeEntryImport", "import_time_entries.csv", "time_entries", utf8Semicolon,
		merge(mapping, map[string]string{"activity": "5", "cf_10": "6"}))
	if len(ids) != 3 || col(ids[0], "activity_id") != "9" || col(ids[1], "activity_id") != "10" || col(ids[2], "activity_id") != "11" {
		t.Errorf("activities: %v", ids)
	}
	if msgs := importItemMessages(t, d, id); len(msgs) != 1 || msgs[0] != "Activity cannot be blank" {
		t.Errorf("messages = %v", msgs)
	}
	cf := func(id int64) string {
		return queryString(t, d, `SELECT COALESCE(MAX(value), '') FROM custom_values WHERE customized_kind = 'time_entry' AND customized_id = ? AND custom_field_id = 10`, id)
	}
	if cf(ids[0]) != "1" || cf(ids[2]) != "0" {
		t.Errorf("overtime cf: %q %q", cf(ids[0]), cf(ids[2]))
	}

	// log_time_for_other_users があればユーザーを列から（メールアドレスで照合）
	if _, err := d.Exec(context.Background(), `INSERT INTO role_permissions (role_id, permission) VALUES (1, 'log_time_for_other_users')`); err != nil {
		t.Fatal(err)
	}
	_, ids = runImport(t, jsmith, ts, d, "TimeEntryImport", "import_time_entries.csv", "time_entries", utf8Semicolon, mapping)
	var users []string
	for _, id := range ids {
		users = append(users, col(id, "user_id"))
	}
	if strings.Join(users, ",") != "2,2,3,2" {
		t.Errorf("users = %v", users)
	}
	_, ids = runImport(t, jsmith, ts, d, "TimeEntryImport", "import_time_entries.csv", "time_entries", utf8Semicolon,
		merge(mapping, map[string]string{"user": "value:3"}))
	for _, id := range ids {
		if col(id, "user_id") != "3" {
			t.Errorf("fixed user: %s", col(id, "user_id"))
		}
	}

	// 他のプロジェクトのチケットの工数
	_, ids = runImport(t, jsmith, ts, d, "TimeEntryImport", "import_time_entries.csv", "time_entries", utf8Semicolon,
		merge(mapping, map[string]string{"project_id": "3"}))
	var projects []string
	for _, id := range ids {
		projects = append(projects, col(id, "project_id"))
	}
	if strings.Join(projects, ",") != "3,3,1,1" {
		t.Errorf("projects = %v", projects)
	}
}

func TestUserImport(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	// auth_sources のフィクスチャ（testfixtures では投入しない）
	now := db.NewTime(time.Now())
	if _, err := d.Exec(context.Background(), `INSERT INTO auth_sources (id, kind, name, created_at, updated_at) VALUES (1, 'ldap', 'LDAP test server', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	mapping := map[string]string{"login": "1", "firstname": "2", "lastname": "3", "mail": "4", "language": "5", "admin": "6",
		"auth_source": "7", "password": "8", "must_change_passwd": "9", "status": "10", "cf_4": "11"}
	_, ids := runImport(t, admin, ts, d, "UserImport", "import_users.csv", "principals", utf8Semicolon, mapping)
	if len(ids) != 3 {
		t.Fatalf("users = %d", len(ids))
	}
	row := func(id int64) string {
		return queryString(t, d, `SELECT ua.login || '|' || p.firstname || '|' || p.lastname || '|' || e.address || '|' || COALESCE(ua.language, '') || '|' ||
  CASE WHEN ua.admin THEN 1 ELSE 0 END || '|' || COALESCE(ua.auth_source_id, 0) || '|' || CASE WHEN ua.must_change_password THEN 1 ELSE 0 END || '|' || p.status
FROM principals p JOIN user_accounts ua ON ua.principal_id = p.id JOIN email_addresses e ON e.user_id = p.id AND e.is_default WHERE p.id = ?`, id)
	}
	want := []string{
		"user1|One|CSV|user1@somenet.foo|en|1|0|1|1",
		"user2|Two|Import|user2@somenet.foo|ja|0|0|0|3",
		"user3|Three|User|user3@somenet.foo|en|0|1|0|2",
	}
	for i, id := range ids {
		if got := row(id); got != want[i] {
			t.Errorf("user %d = %q, want %q", i+1, got, want[i])
		}
	}
	if v := queryString(t, d, `SELECT value FROM custom_values WHERE customized_kind = 'principal' AND customized_id = ? AND custom_field_id = 4`, ids[1]); v != "333-4444-5555" {
		t.Errorf("phone = %q", v)
	}
	// パスワードでログインできる
	login(t, ts, "user1", "password")
}
