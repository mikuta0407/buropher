package server_test

// Redmine の test/integration/api_test/issues_test.rb の移植。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/mikuta0407/buropher/internal/db"
)

// issuesAPIExec は SQL を実行する（テストの前提データの変更用）。
func issuesAPIExec(t *testing.T, d *db.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// issuesAPIInt は SQL の結果（整数 1 つ）を返す。
func issuesAPIInt(t *testing.T, d *db.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// issuesAPIStrings は SQL の結果（文字列の列）を返す。
func issuesAPIStrings(t *testing.T, d *db.DB, q string, args ...any) []string {
	t.Helper()
	var s []string
	if err := d.Select(context.Background(), &s, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

// issuesAPIGenerate は Issue.generate!（既定はプロジェクト 1・作成者 jsmith・件名 Generated）を API で行い id を返す。
// attrs は issue のパラメータ（JSON のオブジェクトの中身。例 `"parent_issue_id": 3`）。
func issuesAPIGenerate(t *testing.T, ts *httptest.Server, attrs string) int64 {
	t.Helper()
	body := `{"issue":{"project_id":1,"subject":"Generated"`
	if attrs != "" {
		body += "," + attrs
	}
	body += "}}"
	res := apiCall(t, ts, http.MethodPost, "/issues.json", "", body, apiCreds("jsmith"))
	res.expectStatus(t, http.StatusCreated)
	id, _ := jsonPath(res.JSON(t), "issue.id")
	n, err := strconv.ParseInt(id.(json.Number).String(), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// issuesAPIGenerateWithDescendants は Issue.generate_with_descendants!（子 2 つ・孫 1 つ）。
func issuesAPIGenerateWithDescendants(t *testing.T, ts *httptest.Server) int64 {
	t.Helper()
	id := issuesAPIGenerate(t, ts, "")
	child := issuesAPIGenerate(t, ts, `"subject":"Child1","parent_issue_id":`+itoaTest(id))
	issuesAPIGenerate(t, ts, `"subject":"Child2","parent_issue_id":`+itoaTest(id))
	issuesAPIGenerate(t, ts, `"subject":"Child11","parent_issue_id":`+itoaTest(child))
	return id
}

// issuesAPITimeEntry は TimeEntry.create!（作業分類は TimeEntryActivity.first = 9）を管理者の API で行う。
func issuesAPITimeEntry(t *testing.T, ts *httptest.Server, issueID, userID int64, hours string) {
	t.Helper()
	body := `{"time_entry":{"issue_id":` + itoaTest(issueID) + `,"user_id":` + itoaTest(userID) +
		`,"hours":"` + hours + `","activity_id":9,"spent_on":"2026-01-15"}}`
	res := apiCall(t, ts, http.MethodPost, "/time_entries.json", "", body, apiCreds("admin"))
	res.expectStatus(t, http.StatusCreated)
}

// issuesAPICustomField は CustomField.generate!（既定は文字列）を SQL で作り id を返す。
// trackers が nil なら IssueCustomField.generate! と同じく全トラッカー。
func issuesAPICustomField(t *testing.T, d *db.DB, kind, name, format string, multiple bool, possible []string, def *string, trackers []int64) int64 {
	t.Helper()
	pv := "null"
	if possible != nil {
		b, _ := json.Marshal(possible)
		pv = string(b)
	}
	mult := 0
	if multiple {
		mult = 1
	}
	issuesAPIExec(t, d, `INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, multiple, possible_values, default_value, position)
		VALUES (?, ?, ?, 1, ?, CASE WHEN ? = 'null' THEN NULL ELSE ? END, ?, 99)`, kind, name, format, mult, pv, pv, def)
	id := issuesAPIInt(t, d, `SELECT id FROM custom_fields WHERE owner_kind = ? AND name = ?`, kind, name)
	if kind == "issue" {
		if trackers == nil {
			issuesAPIExec(t, d, `INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) SELECT ?, id FROM trackers`, id)
		}
		for _, tr := range trackers {
			issuesAPIExec(t, d, `INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`, id, tr)
		}
	}
	return id
}

// issuesAPICFValues は customized の custom_field の値（複数値は id 順）。
func issuesAPICFValues(t *testing.T, d *db.DB, kind string, id, cf int64) []string {
	t.Helper()
	return issuesAPIStrings(t, d, `SELECT COALESCE(value, '') FROM custom_values WHERE customized_kind = ? AND customized_id = ? AND custom_field_id = ? ORDER BY id`, kind, id, cf)
}

// issuesAPIIssueNode は XML の issues > issue のうち id が id のもの（assert_select 'issue id', text: id の親）。
func issuesAPIIssueNode(t *testing.T, doc *html.Node, id string) *html.Node {
	t.Helper()
	for _, n := range xmlSelect(t, doc, "issue > id") {
		if nodeText(n) == id {
			return n.Parent
		}
	}
	t.Fatalf("issue %s not found", id)
	return nil
}

// issuesAPIIDs は issues > issue > id の値。
func issuesAPIIDs(t *testing.T, doc *html.Node) []string {
	t.Helper()
	var ids []string
	for _, n := range xmlSelect(t, doc, "issues > issue > id") {
		ids = append(ids, nodeText(n))
	}
	return ids
}

// issuesAPIUpload は POST /uploads.<format>（octet-stream）でトークンを得る（xml_upload / json_upload）。
func issuesAPIUpload(t *testing.T, ts *httptest.Server, format, content, login string) string {
	t.Helper()
	res := apiCall(t, ts, http.MethodPost, "/uploads."+format, "application/octet-stream", content, apiCreds(login))
	res.expectStatus(t, http.StatusCreated)
	if format == "json" {
		v, _ := jsonPath(res.JSON(t), "upload.token")
		return v.(string)
	}
	nodes := xmlSelect(t, res.XML(t), "upload > token")
	if len(nodes) == 0 {
		t.Fatalf("no token: %s", res.Body)
	}
	return nodeText(nodes[0])
}

func TestAPIIssues(t *testing.T) {
	ro, roDB := newFixtureServer(t)
	_ = roDB

	t.Run("GET /issues.xml should contain metadata", func(t *testing.T) {
		assertXMLCount(t, apiGet(t, ro, "/issues.xml").XML(t), `issues[type=array][total_count][limit="25"][offset="0"]`, 1)
	})
	t.Run("GET /issues.xml with nometa param should not contain metadata", func(t *testing.T) {
		assertXMLCount(t, apiGet(t, ro, "/issues.xml?nometa=1").XML(t), `issues[type=array]:not([total_count]):not([limit]):not([offset])`, 1)
	})
	t.Run("GET /issues.xml with nometa header should not contain metadata", func(t *testing.T) {
		res := apiGet(t, ro, "/issues.xml", apiHeader("X-Redmine-Nometa", "1"))
		assertXMLCount(t, res.XML(t), `issues[type=array]:not([total_count]):not([limit]):not([offset])`, 1)
	})
	t.Run("GET /issues.xml with offset and limit", func(t *testing.T) {
		doc := apiGet(t, ro, "/issues.xml?offset=2&limit=3").XML(t)
		assertXMLCount(t, doc, `issues[type=array][total_count][limit="3"][offset="2"]`, 1)
		assertXMLCount(t, doc, "issues issue", 3)
	})
	t.Run("GET /issues.xml with relations", func(t *testing.T) {
		res := apiGet(t, ro, "/issues.xml?include=relations")
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		doc := res.XML(t)
		i3 := issuesAPIIssueNode(t, doc, "3")
		assertXMLCount(t, i3, "relations relation", 1)
		assertXMLCount(t, i3, `relations relation[id="2"][issue_id="2"][issue_to_id="3"][relation_type=relates]`, 1)
		i1 := issuesAPIIssueNode(t, doc, "1")
		assertXMLCount(t, i1, "relations", 1)
		assertXMLCount(t, i1, "relations relation", 0)
	})
	t.Run("GET /issues.xml with attachments", func(t *testing.T) {
		res := apiGet(t, ro, "/issues.xml?include=attachments")
		res.expectStatus(t, 200)
		doc := res.XML(t)
		assertXMLCount(t, issuesAPIIssueNode(t, doc, "3"), "attachments attachment", 4)
		i1 := issuesAPIIssueNode(t, doc, "1")
		assertXMLCount(t, i1, "attachments", 1)
		assertXMLCount(t, i1, "attachments attachment", 0)
	})
	t.Run("GET /issues.xml with invalid query params", func(t *testing.T) {
		res := apiGet(t, ro, "/issues.xml?f[]=start_date&op[start_date]==")
		res.expectStatus(t, http.StatusUnprocessableEntity)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertXMLText(t, res.XML(t), "errors error", "Start date cannot be blank")
	})
	t.Run("GET /issues.xml with custom field filter", func(t *testing.T) {
		doc := apiGet(t, ro, "/issues.xml?set_filter=1&f[]=cf_1&op[cf_1]==&v[cf_1][]=MySQL").XML(t)
		if got := issuesAPIIDs(t, doc); !slices.Equal(got, []string{"3"}) {
			t.Errorf("ids %v", got)
		}
	})
	t.Run("GET /issues.xml with custom field filter (shorthand method)", func(t *testing.T) {
		doc := apiGet(t, ro, "/issues.xml?cf_1=MySQL").XML(t)
		if got := issuesAPIIDs(t, doc); !slices.Equal(got, []string{"3"}) {
			t.Errorf("ids %v", got)
		}
	})
	t.Run("test_index_should_include_issue_attributes", func(t *testing.T) {
		assertXMLText(t, apiGet(t, ro, "/issues.xml").XML(t), "issues>issue>is_private", "false")
	})
	t.Run("test_index_should_include_issue_status_is_closed_false", func(t *testing.T) {
		if n := len(xmlSelect(t, apiGet(t, ro, "/issues.xml").XML(t), "issues>issue>status[is_closed=false]")); n == 0 {
			t.Error("no status[is_closed=false]")
		}
	})
	t.Run("test_index_should_include_issue_status_is_closed_true", func(t *testing.T) {
		if n := len(xmlSelect(t, apiGet(t, ro, "/issues.xml?status_id=5").XML(t), "issues>issue>status[is_closed=true]")); n == 0 {
			t.Error("no status[is_closed=true]")
		}
	})
	t.Run("test_index_should_include_spent_hours", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `DELETE FROM issues`)
		parent := issuesAPIGenerate(t, ts, `"estimated_hours":2.0`)
		child := issuesAPIGenerate(t, ts, `"parent_issue_id":`+itoaTest(parent)+`,"estimated_hours":3.0`)
		issuesAPITimeEntry(t, ts, parent, 2, "2.5")
		issuesAPITimeEntry(t, ts, child, 2, "2.5")
		doc := apiGet(t, ts, "/issues.xml").XML(t)
		assertXMLCount(t, doc, "issues issue", 2)
		// assert_select 'sel', 'text' は一致する要素のいずれかのテキストが一致すればよい
		issuesAPIAnyText(t, doc, "issues>issue>spent_hours", "2.5")
		issuesAPIAnyText(t, doc, "issues>issue>total_spent_hours", "5.0")
	})
	t.Run("test_index_should_not_include_spent_hours", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `DELETE FROM role_permissions WHERE permission = 'view_time_entries' AND role_id = (SELECT id FROM roles WHERE builtin = 2)`)
		doc := apiGet(t, ts, "/issues.xml").XML(t)
		assertXMLCount(t, doc, "issues>issue>spent_hours", 0)
		assertXMLCount(t, doc, "issues>issue>total_spent_hours", 0)
	})
	t.Run("test_index_should_allow_timestamp_filtering", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `DELETE FROM issues`)
		i1 := issuesAPIGenerate(t, ts, `"subject":"1"`)
		i2 := issuesAPIGenerate(t, ts, `"subject":"2"`)
		issuesAPIExec(t, d, `UPDATE issues SET updated_at = '2014-01-02T10:25:00.000000Z' WHERE id = ?`, i1)
		issuesAPIExec(t, d, `UPDATE issues SET updated_at = '2014-01-02T12:13:00.000000Z' WHERE id = ?`, i2)
		doc := apiGet(t, ts, "/issues.xml?set_filter=1&f[]=updated_on&op[updated_on]=%3C%3D&v[updated_on][]=2014-01-02T12:00:00Z").XML(t)
		assertXMLCount(t, doc, "issues>issue", 1)
		assertXMLText(t, doc, "issues>issue>subject", "1")
		doc = apiGet(t, ts, "/issues.xml?set_filter=1&f[]=updated_on&op[updated_on]=%3E%3D&v[updated_on][]=2014-01-02T12:00:00Z").XML(t)
		assertXMLCount(t, doc, "issues>issue", 1)
		assertXMLText(t, doc, "issues>issue>subject", "2")
		doc = apiGet(t, ts, "/issues.xml?set_filter=1&f[]=updated_on&op[updated_on]=%3E%3D&v[updated_on][]=2014-01-02T08:00:00Z").XML(t)
		assertXMLCount(t, doc, "issues>issue", 2)
	})
	t.Run("GET /issues.xml with filter", func(t *testing.T) {
		got := issuesAPIIDs(t, apiGet(t, ro, "/issues.xml?status_id=5").XML(t))
		slices.Sort(got)
		if !slices.Equal(got, []string{"11", "12", "8"}) {
			t.Errorf("ids %v", got)
		}
	})
	t.Run("GET /issues.json with filter", func(t *testing.T) {
		m := apiGet(t, ro, "/issues.json?status_id=5").JSON(t)
		issues := m["issues"].([]any)
		if len(issues) != 3 {
			t.Fatalf("%d issues", len(issues))
		}
		for i := range issues {
			assertJSON(t, m, "issues."+strconv.Itoa(i)+".status.id", "5")
		}
	})
	t.Run("GET /issues/:id.xml with journals", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `UPDATE issue_journals SET private_notes = 1 WHERE id = 2`)
		doc := apiGet(t, ts, "/issues/1.xml?include=journals", apiCreds("jsmith")).XML(t)
		assertXMLCount(t, doc, "issue journals[type=array]", 1)
		j1 := xmlSelect(t, doc, `issue journals journal[id="1"]`)
		if len(j1) != 1 {
			t.Fatalf("journal 1: %d", len(j1))
		}
		// Issue.find(1).journals.order(:id)[0].updated_on（参照環境の値）
		assertXMLText(t, j1[0], "updated_on", "2026-01-14T00:00:00Z")
		assertXMLCount(t, j1[0], `updated_by[id="1"][name="Redmine Admin"]`, 1)
		assertXMLText(t, j1[0], "private_notes", "false")
		assertXMLText(t, j1[0], "details[type=array] detail[name=status_id] old_value", "1")
		assertXMLText(t, j1[0], "details[type=array] detail[name=status_id] new_value", "2")
		j2 := xmlSelect(t, doc, `issue journals journal[id="2"]`)
		if len(j2) != 1 {
			t.Fatalf("journal 2: %d", len(j2))
		}
		assertXMLText(t, j2[0], "private_notes", "true")
		assertXMLCount(t, j2[0], "details[type=array]", 1)
	})
	t.Run("GET /issues/:id.xml with journals should format timestamps in ISO 8601", func(t *testing.T) {
		doc := apiGet(t, ro, "/issues/1.xml?include=journals").XML(t)
		// fixtures の相対日時を固定時刻で評価した値（参照環境の /issues/1.xml?include=journals と同じ）
		assertXMLText(t, doc, "issue>created_on", "2026-01-12T12:00:00Z")
		assertXMLText(t, doc, "issue>updated_on", "2026-01-14T12:00:00Z")
		assertXMLText(t, doc, "issue journal>created_on", "2026-01-13T00:00:00Z")
	})
	t.Run("GET /issues/:id.xml with custom fields", func(t *testing.T) {
		doc := apiGet(t, ro, "/issues/3.xml").XML(t)
		assertXMLText(t, doc, `issue custom_fields[type=array] custom_field[id="1"] value`, "MySQL")
	})

	multiCF := func(t *testing.T, values ...string) *httptest.Server {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `UPDATE custom_fields SET multiple = 1 WHERE id = 1`)
		issuesAPIExec(t, d, `DELETE FROM custom_values WHERE customized_kind = 'issue' AND customized_id = 3 AND custom_field_id = 1`)
		for _, v := range values {
			var val any = v
			if v == "" {
				val = nil
			}
			issuesAPIExec(t, d, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('issue', 3, 1, ?)`, val)
		}
		return ts
	}
	t.Run("GET /issues/:id.xml with multi custom fields", func(t *testing.T) {
		ts := multiCF(t, "MySQL", "Oracle")
		res := apiGet(t, ts, "/issues/3.xml")
		res.expectStatus(t, 200)
		doc := res.XML(t)
		vals := xmlSelect(t, doc, `issue custom_fields[type=array] custom_field[id="1"] value[type=array] value`)
		var got []string
		for _, v := range vals {
			got = append(got, nodeText(v))
		}
		slices.Sort(got)
		if !slices.Equal(got, []string{"MySQL", "Oracle"}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("GET /issues/:id.json with multi custom fields", func(t *testing.T) {
		ts := multiCF(t, "MySQL", "Oracle")
		res := apiGet(t, ts, "/issues/3.json")
		res.expectStatus(t, 200)
		if !strings.Contains(res.Body, `{"id":1,"name":"Database","multiple":true,"value":["MySQL","Oracle"]}`) {
			t.Errorf("custom field 1 not found: %s", res.Body)
		}
	})
	t.Run("GET /issues/:id.xml with empty value for multi custom field", func(t *testing.T) {
		ts := multiCF(t, "")
		doc := apiGet(t, ts, "/issues/3.xml").XML(t)
		assertXMLCount(t, doc, `issue custom_fields[type=array] custom_field[id="1"] value[type=array]:empty`, 1)
	})
	t.Run("GET /issues/:id.json with empty value for multi custom field", func(t *testing.T) {
		ts := multiCF(t, "")
		res := apiGet(t, ts, "/issues/3.json")
		res.expectStatus(t, 200)
		if !strings.Contains(res.Body, `{"id":1,"name":"Database","multiple":true,"value":[]}`) {
			t.Errorf("custom field 1 not found: %s", res.Body)
		}
	})
	t.Run("GET /issues/:id.xml with attachments", func(t *testing.T) {
		doc := apiGet(t, ro, "/issues/3.xml?include=attachments").XML(t)
		assertXMLCount(t, doc, "issue attachments[type=array] attachment", 4)
		var a1 *html.Node
		for _, n := range xmlSelect(t, doc, "issue attachments attachment > id") {
			if nodeText(n) == "1" {
				a1 = n.Parent
			}
		}
		if a1 == nil {
			t.Fatal("attachment 1 not found")
		}
		assertXMLText(t, a1, "filename", "error281.txt")
		assertXMLText(t, a1, "content_url", ro.URL+"/attachments/download/1/error281.txt")
	})
	t.Run("GET /issues/:id.xml with subtasks", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		id := issuesAPIGenerateWithDescendants(t, ts)
		doc := apiGet(t, ts, "/issues/"+itoaTest(id)+".xml?include=children").XML(t)
		// assert_select 'issue id', text: id do assert_select '~ children[type=array] > issue' ...（ルートの issue の直下）
		root := xmlSelect(t, doc, "issue")[0]
		var kids, grand int
		for c := root.FirstChild; c != nil; c = c.NextSibling {
			if c.Data == "children" && attr(c, "type") == "array" {
				for i := c.FirstChild; i != nil; i = i.NextSibling {
					if i.Data == "issue" {
						kids++
						for g := i.FirstChild; g != nil; g = g.NextSibling {
							if g.Data == "children" {
								grand++
							}
						}
					}
				}
			}
		}
		if kids != 2 || grand != 1 {
			t.Errorf("children %d, children with children %d", kids, grand)
		}
	})
	t.Run("GET /issues/:id.json with subtasks", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		id := issuesAPIGenerateWithDescendants(t, ts)
		m := apiGet(t, ts, "/issues/"+itoaTest(id)+".json?include=children").JSON(t)
		children, _ := jsonPath(m, "issue.children")
		list, _ := children.([]any)
		if len(list) != 2 {
			t.Fatalf("children %v", children)
		}
		n := 0
		for _, c := range list {
			if _, ok := c.(map[string]any)["children"]; ok {
				n++
			}
		}
		if n != 1 {
			t.Errorf("children with children: %d", n)
		}
	})
	t.Run("GET /issues/:id.json with no spent time should return floats", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		id := issuesAPIGenerate(t, ts, "")
		res := apiGet(t, ts, "/issues/"+itoaTest(id)+".json")
		if !strings.Contains(res.Body, `"spent_hours":0.0,"total_spent_hours":0.0`) {
			t.Errorf("spent hours not floats: %s", res.Body)
		}
	})
	t.Run("test_show_should_include_issue_attributes", func(t *testing.T) {
		doc := apiGet(t, ro, "/issues/1.xml").XML(t)
		assertXMLText(t, doc, "issue>is_private", "false")
		assertXMLCount(t, doc, "issue>status[is_closed=false]", 1)
	})
	t.Run("GET /issues/:id.xml?include=watchers should include watchers", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', 1, 3)`)
		res := apiGet(t, ts, "/issues/1.xml?include=watchers", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		doc := res.XML(t)
		n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = 1`)
		assertXMLCount(t, doc, "issue watchers", 1)
		assertXMLCount(t, doc, "issue watchers user", int(n))
		assertXMLCount(t, doc, `issue watchers user[id="3"]`, 1)
	})
	t.Run("GET /issues/:id.xml should not disclose associated changesets from projects the user has no access to", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		// Project.generate!(:is_public => false) の代わりに非公開で jsmith がメンバーでないプロジェクト 5（private-child）を使う。
		// Repository::Subversion の代わりに Git（D-15）。
		issuesAPIExec(t, d, `INSERT INTO repositories (project_id, scm, url, identifier, is_default, created_at) VALUES (5, 'git', '/tmp/none', 'api', 0, '2026-01-15T12:00:00.000000Z')`)
		repo := issuesAPIInt(t, d, `SELECT id FROM repositories WHERE identifier = 'api'`)
		issuesAPIExec(t, d, `INSERT INTO changesets (repository_id, revision, committed_at, comments) VALUES (?, '123457', '2026-01-15T12:00:00.000000Z', 'x')`, repo)
		cs := issuesAPIInt(t, d, `SELECT id FROM changesets WHERE repository_id = ?`, repo)
		issuesAPIExec(t, d, `INSERT INTO changesets_issues (changeset_id, issue_id) VALUES (?, 1)`, cs)
		doc := apiGet(t, ts, "/issues/1.xml?include=changesets", apiCreds("jsmith")).XML(t)
		assertXMLCount(t, doc, "issue changesets[type=array]", 1)
		assertXMLCount(t, doc, "issue changesets[type=array] changeset", 0)
	})
	t.Run("GET /issues/:id.xml?include=allowed_statuses should include available statuses", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `UPDATE issues SET status_id = 2 WHERE id = 1`)
		res := apiGet(t, ts, "/issues/1.xml?include=allowed_statuses", apiCreds("dlopper"))
		res.expectStatus(t, 200)
		doc := res.XML(t)
		want := [][3]string{{"1", "New", "false"}, {"2", "Assigned", "false"}, {"4", "Feedback", "false"}, {"5", "Closed", "true"}, {"6", "Rejected", "true"}}
		sts := xmlSelect(t, doc, "issue allowed_statuses[type=array] status")
		if len(sts) != len(want) {
			t.Fatalf("statuses %d: %s", len(sts), res.Body)
		}
		for i, s := range sts {
			got := [3]string{attr(s, "id"), attr(s, "name"), attr(s, "is_closed")}
			if got != want[i] {
				t.Errorf("status %d = %v, want %v", i, got, want[i])
			}
		}
	})

	// total_estimated_hours / total_spent_hours の前提（issue 3 の見積 2.0、見積 3.0 の子、子に 2.5 時間）
	totals := func(t *testing.T, withTime bool) (*httptest.Server, *db.DB) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `UPDATE issues SET estimated_hours = 2.0 WHERE id = 3`)
		child := issuesAPIGenerate(t, ts, `"parent_issue_id":3,"estimated_hours":3.0`)
		if withTime {
			issuesAPITimeEntry(t, ts, child, 2, "2.5")
		}
		return ts, d
	}
	t.Run("GET /issues/:id.xml should contains total_estimated_hours and total_spent_hours", func(t *testing.T) {
		ts, _ := totals(t, true)
		res := apiGet(t, ts, "/issues/3.xml")
		doc := res.XML(t)
		assertXMLText(t, doc, "issue>estimated_hours", "2.0")
		assertXMLText(t, doc, "issue>total_estimated_hours", "5.0")
		assertXMLText(t, doc, "issue>spent_hours", "1.0")
		assertXMLText(t, doc, "issue>total_spent_hours", "3.5")
	})
	t.Run("GET /issues/:id.xml should contains total_estimated_hours, and should not contains spent_hours and total_spent_hours when permission does not exists", func(t *testing.T) {
		ts, d := totals(t, false)
		issuesAPIExec(t, d, `DELETE FROM role_permissions WHERE permission = 'view_time_entries' AND role_id = (SELECT id FROM roles WHERE builtin = 2)`)
		doc := apiGet(t, ts, "/issues/3.xml").XML(t)
		assertXMLText(t, doc, "issue>estimated_hours", "2.0")
		assertXMLText(t, doc, "issue>total_estimated_hours", "5.0")
		assertXMLCount(t, doc, "issue>spent_hours", 0)
		assertXMLCount(t, doc, "issue>total_spent_hours", 0)
	})
	visibleSpent := func(t *testing.T) *httptest.Server {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `UPDATE roles SET time_entries_visibility = 'own' WHERE id = 1`)
		child := issuesAPIGenerate(t, ts, `"parent_issue_id":3`)
		// fixtures の issue 3 の 1.0 時間（user 2）を除き、TimeEntry.generate! の 3 件にする
		issuesAPIExec(t, d, `DELETE FROM time_entries WHERE issue_id = 3`)
		issuesAPITimeEntry(t, ts, 3, 2, "5.5")
		issuesAPITimeEntry(t, ts, child, 2, "2")
		issuesAPITimeEntry(t, ts, child, 1, "100")
		return ts
	}
	t.Run("GET /issues/:id.xml should contains visible spent_hours only", func(t *testing.T) {
		ts := visibleSpent(t)
		doc := apiGet(t, ts, "/issues/3.xml", apiCreds("jsmith")).XML(t)
		assertXMLText(t, doc, "issue>spent_hours", "5.5")
		assertXMLText(t, doc, "issue>total_spent_hours", "7.5")
	})
	t.Run("GET /issues/:id.json should contains total_estimated_hours and total_spent_hours", func(t *testing.T) {
		ts, _ := totals(t, true)
		res := apiGet(t, ts, "/issues/3.json")
		if res.ContentType() != "application/json" {
			t.Errorf("content type %s", res.ContentType())
		}
		m := res.JSON(t)
		assertJSON(t, m, "issue.estimated_hours", "2.0")
		assertJSON(t, m, "issue.total_estimated_hours", "5.0")
		assertJSON(t, m, "issue.spent_hours", "1.0")
		assertJSON(t, m, "issue.total_spent_hours", "3.5")
	})
	t.Run("GET /issues/:id.json should contains total_estimated_hours, and should not contains spent_hours and total_spent_hours when permission does not exists", func(t *testing.T) {
		ts, d := totals(t, false)
		issuesAPIExec(t, d, `DELETE FROM role_permissions WHERE permission = 'view_time_entries' AND role_id = (SELECT id FROM roles WHERE builtin = 2)`)
		m := apiGet(t, ts, "/issues/3.json").JSON(t)
		assertJSON(t, m, "issue.estimated_hours", "2.0")
		assertJSON(t, m, "issue.total_estimated_hours", "5.0")
		if _, ok := jsonPath(m, "issue.spent_hours"); ok {
			t.Error("spent_hours present")
		}
		if _, ok := jsonPath(m, "issue.total_spent_hours"); ok {
			t.Error("total_spent_hours present")
		}
	})
	t.Run("GET /issues/:id.json should contains visible spent_hours only", func(t *testing.T) {
		ts := visibleSpent(t)
		m := apiGet(t, ts, "/issues/3.json", apiCreds("jsmith")).JSON(t)
		assertJSON(t, m, "issue.spent_hours", "5.5")
		assertJSON(t, m, "issue.total_spent_hours", "7.5")
	})

	lastIssue := func(t *testing.T, d *db.DB) int64 {
		return issuesAPIInt(t, d, `SELECT MAX(id) FROM issues`)
	}
	issueCount := func(t *testing.T, d *db.DB) int64 { return issuesAPIInt(t, d, `SELECT COUNT(*) FROM issues`) }
	journalCount := func(t *testing.T, d *db.DB) int64 { return issuesAPIInt(t, d, `SELECT COUNT(*) FROM issue_journals`) }
	checkAttrs := func(t *testing.T, d *db.DB, id int64) {
		t.Helper()
		var row struct {
			P, T, S int64
			C       *int64
			Subject string
		}
		if err := d.Get(context.Background(), &row, `SELECT project_id AS p, tracker_id AS t, status_id AS s, category_id AS c, subject FROM issues WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if row.P != 1 || row.T != 2 || row.S != 3 || row.C == nil || *row.C != 2 || row.Subject != "API test" {
			t.Errorf("issue = %+v", row)
		}
	}

	t.Run("POST /issues.xml should create an issue with the attributes", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := issueCount(t, d)
		payload := `<?xml version="1.0" encoding="UTF-8" ?>
  <issue>
    <project_id>1</project_id>
    <tracker_id>2</tracker_id>
    <status_id>3</status_id>
    <category_id>2</category_id>
   <subject>API test</subject>
</issue>
`
		res := apiCall(t, ts, http.MethodPost, "/issues.xml", "application/xml", payload, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		if issueCount(t, d) != before+1 {
			t.Fatal("issue not created")
		}
		id := lastIssue(t, d)
		checkAttrs(t, d, id)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertXMLText(t, res.XML(t), "issue > id", itoaTest(id))
		// render :status => :created, :location => issue_url(@issue)
		if loc := res.Header.Get("Location"); loc != ts.URL+"/issues/"+itoaTest(id) {
			t.Errorf("Location %q", loc)
		}
	})
	t.Run("POST /issues.xml with watcher_user_ids should create issue with watchers", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		form := "issue[project_id]=1&issue[subject]=Watchers&issue[tracker_id]=2&issue[status_id]=3&issue[watcher_user_ids][]=3&issue[watcher_user_ids][]=1"
		res := apiCall(t, ts, http.MethodPost, "/issues.xml", "application/x-www-form-urlencoded", form, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		ids := issuesAPIStrings(t, d, `SELECT CAST(principal_id AS TEXT) FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = ? ORDER BY principal_id`, lastIssue(t, d))
		if !slices.Equal(ids, []string{"1", "3"}) {
			t.Errorf("watchers %v", ids)
		}
	})
	t.Run("POST /issues.xml with failure should return errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := issueCount(t, d)
		res := apiCall(t, ts, http.MethodPost, "/issues.xml", "application/x-www-form-urlencoded", "issue[project_id]=1", apiCreds("jsmith"))
		if issueCount(t, d) != before {
			t.Error("issue created")
		}
		assertXMLText(t, res.XML(t), "errors error", "Subject cannot be blank")
	})
	t.Run("POST /issues.json should create an issue with the attributes", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := issueCount(t, d)
		payload := `{
  "issue": {
  "project_id": "1",
  "tracker_id": "2",
  "status_id": "3",
  "category_id": "2",
  "subject": "API test"
  }
}`
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/json", payload, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		if issueCount(t, d) != before+1 {
			t.Fatal("issue not created")
		}
		checkAttrs(t, d, lastIssue(t, d))
	})
	t.Run("POST /issues.json should accept project identifier as project_id", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/x-www-form-urlencoded",
			"issue[project_id]=subproject1&issue[tracker_id]=2&issue[subject]=Foo", apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
	})
	t.Run("POST /issues.json without tracker_id should accept custom fields", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		def := "V2"
		cf := issuesAPICustomField(t, d, "issue", "Custom field 1", "list", true, []string{"V1", "V2", "V3"}, &def, nil)
		payload := `{"issue": {"project_id": "1", "subject": "Multivalued custom field", "custom_field_values":{"` + itoaTest(cf) + `":["V1","V3"]}}}`
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/json", payload, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		got := issuesAPICFValues(t, d, "issue", lastIssue(t, d), cf)
		slices.Sort(got)
		if !slices.Equal(got, []string{"V1", "V3"}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("POST /issues.json with omitted custom field should set default value", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		def := "Default"
		cf := issuesAPICustomField(t, d, "issue", "Custom field 1", "string", false, nil, &def, nil)
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/json",
			`{"issue":{"project_id":1,"subject":"API","custom_field_values":{}}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		if got := issuesAPICFValues(t, d, "issue", lastIssue(t, d), cf); !slices.Equal(got, []string{"Default"}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("POST /issues.json with custom field set to blank should not set default value", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		def := "Default"
		cf := issuesAPICustomField(t, d, "issue", "Custom field 1", "string", false, nil, &def, nil)
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/json",
			`{"issue":{"project_id":1,"subject":"API","custom_field_values":{"`+itoaTest(cf)+`":""}}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		if got := issuesAPICFValues(t, d, "issue", lastIssue(t, d), cf); !slices.Equal(got, []string{""}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("POST /issues.json with failure should return errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := issueCount(t, d)
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/x-www-form-urlencoded", "issue[project_id]=1", apiCreds("jsmith"))
		if issueCount(t, d) != before {
			t.Error("issue created")
		}
		errs, _ := res.JSON(t)["errors"].([]any)
		if !slices.Contains(errs, any("Subject cannot be blank")) {
			t.Errorf("errors %v", errs)
		}
	})
	for _, extra := range []struct{ name, form string }{
		{"POST /issues.json with invalid project_id should respond with 422", ""},
		{"POST /issues.json with invalid project_id and any assigned_to_id should respond with 422", "&issue[assigned_to_id]=1"},
		{"POST /issues.json with invalid project_id and any fixed_version_id should respond with 422", "&issue[fixed_version_id]=1"},
	} {
		t.Run(extra.name, func(t *testing.T) {
			res := apiCall(t, ro, http.MethodPost, "/issues.json", "application/x-www-form-urlencoded",
				"issue[project_id]=999&issue[subject]=API"+extra.form, apiCreds("jsmith"))
			res.expectStatus(t, http.StatusUnprocessableEntity)
		})
	}
	put := func(t *testing.T, ts *httptest.Server, path, form string) apiResp {
		return apiCall(t, ts, http.MethodPut, path, "application/x-www-form-urlencoded", form, apiCreds("jsmith"))
	}
	lastNotes := func(t *testing.T, d *db.DB) string {
		return issuesAPIStrings(t, d, `SELECT COALESCE(notes, '') FROM issue_journals ORDER BY id DESC LIMIT 1`)[0]
	}
	subjectOf := func(t *testing.T, d *db.DB, id int64) string {
		return issuesAPIStrings(t, d, `SELECT subject FROM issues WHERE id = ?`, id)[0]
	}
	t.Run("PUT /issues/:id.xml", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := journalCount(t, d)
		put(t, ts, "/issues/6.xml", "issue[subject]=API+update&issue[notes]=A+new+note")
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if s := subjectOf(t, d, 6); s != "API update" {
			t.Errorf("subject %q", s)
		}
		if n := lastNotes(t, d); n != "A new note" {
			t.Errorf("notes %q", n)
		}
	})
	t.Run("PUT /issues/:id.xml with custom fields", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		put(t, ts, "/issues/3.xml", "issue[custom_fields][][id]=1&issue[custom_fields][][value]=PostgreSQL&issue[custom_fields][][id]=2&issue[custom_fields][][value]=150")
		if got := issuesAPICFValues(t, d, "issue", 3, 2); !slices.Equal(got, []string{"150"}) {
			t.Errorf("cf 2 %v", got)
		}
		if got := issuesAPICFValues(t, d, "issue", 3, 1); !slices.Equal(got, []string{"PostgreSQL"}) {
			t.Errorf("cf 1 %v", got)
		}
	})
	t.Run("PUT /issues/:id.xml with multi custom fields", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `UPDATE custom_fields SET multiple = 1 WHERE id = 1`)
		put(t, ts, "/issues/3.xml", "issue[custom_fields][][id]=1&issue[custom_fields][][value][]=MySQL&issue[custom_fields][][value][]=PostgreSQL&issue[custom_fields][][id]=2&issue[custom_fields][][value]=150")
		if got := issuesAPICFValues(t, d, "issue", 3, 2); !slices.Equal(got, []string{"150"}) {
			t.Errorf("cf 2 %v", got)
		}
		got := issuesAPICFValues(t, d, "issue", 3, 1)
		slices.Sort(got)
		if !slices.Equal(got, []string{"MySQL", "PostgreSQL"}) {
			t.Errorf("cf 1 %v", got)
		}
	})
	t.Run("PUT /issues/:id.xml with project change", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		put(t, ts, "/issues/3.xml", "issue[project_id]=2&issue[subject]=Project+changed")
		if p := issuesAPIInt(t, d, `SELECT project_id FROM issues WHERE id = 3`); p != 2 {
			t.Errorf("project %d", p)
		}
		if s := subjectOf(t, d, 3); s != "Project changed" {
			t.Errorf("subject %q", s)
		}
	})
	t.Run("PUT /issues/:id.xml with notes only", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := journalCount(t, d)
		put(t, ts, "/issues/6.xml", "issue[notes]=Notes+only")
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if n := lastNotes(t, d); n != "Notes only" {
			t.Errorf("notes %q", n)
		}
	})

	// 既定値つきカスタムフィールドと、その値が空のチケット（Issue.generate!(custom_field_values: {cf => ""})）
	blankCF := func(t *testing.T, trackers []int64, setBlank bool) (*httptest.Server, *db.DB, int64, int64) {
		ts, d := newFixtureServer(t)
		def := "Default"
		cf := issuesAPICustomField(t, d, "issue", "Custom field 1", "string", false, nil, &def, trackers)
		attrs := `"tracker_id":1`
		if setBlank {
			attrs += `,"custom_field_values":{"` + itoaTest(cf) + `":""}`
		}
		id := issuesAPIGenerate(t, ts, attrs)
		if setBlank {
			if got := issuesAPICFValues(t, d, "issue", id, cf); !slices.Equal(got, []string{""}) {
				t.Fatalf("precondition: %v", got)
			}
		}
		return ts, d, cf, id
	}
	t.Run("PUT /issues/:id.json with omitted custom field should not change blank value to default value", func(t *testing.T) {
		ts, d, cf, id := blankCF(t, nil, true)
		before := journalCount(t, d)
		res := apiCall(t, ts, http.MethodPut, "/issues/"+itoaTest(id)+".json", "", `{"issue":{"custom_field_values":{},"notes":"API"}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusNoContent)
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if got := issuesAPICFValues(t, d, "issue", id, cf); !slices.Equal(got, []string{""}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("PUT /issues/:id.json with custom field set to blank should not change blank value to default value", func(t *testing.T) {
		ts, d, cf, id := blankCF(t, nil, true)
		before := journalCount(t, d)
		res := apiCall(t, ts, http.MethodPut, "/issues/"+itoaTest(id)+".json", "", `{"issue":{"custom_field_values":{"`+itoaTest(cf)+`":""},"notes":"API"}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusNoContent)
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if got := issuesAPICFValues(t, d, "issue", id, cf); !slices.Equal(got, []string{""}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("PUT /issues/:id.json with tracker change and omitted custom field specific to that tracker should set default value", func(t *testing.T) {
		ts, d, cf, id := blankCF(t, []int64{2}, false)
		before := journalCount(t, d)
		res := apiCall(t, ts, http.MethodPut, "/issues/"+itoaTest(id)+".json", "", `{"issue":{"tracker_id":2,"custom_field_values":{},"notes":"API"}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusNoContent)
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if tr := issuesAPIInt(t, d, `SELECT tracker_id FROM issues WHERE id = ?`, id); tr != 2 {
			t.Errorf("tracker %d", tr)
		}
		if got := issuesAPICFValues(t, d, "issue", id, cf); !slices.Equal(got, []string{"Default"}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("PUT /issues/:id.json with tracker change and custom field specific to that tracker set to blank should not set default value", func(t *testing.T) {
		ts, d, cf, id := blankCF(t, []int64{2}, false)
		before := journalCount(t, d)
		res := apiCall(t, ts, http.MethodPut, "/issues/"+itoaTest(id)+".json", "", `{"issue":{"tracker_id":2,"custom_field_values":{"`+itoaTest(cf)+`":""},"notes":"API"}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusNoContent)
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if tr := issuesAPIInt(t, d, `SELECT tracker_id FROM issues WHERE id = ?`, id); tr != 2 {
			t.Errorf("tracker %d", tr)
		}
		if got := issuesAPICFValues(t, d, "issue", id, cf); !slices.Equal(got, []string{""}) {
			t.Errorf("values %v", got)
		}
	})
	t.Run("PUT /issues/:id.xml with failed update", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		res := put(t, ts, "/issues/6.xml", "issue[subject]=")
		res.expectStatus(t, http.StatusUnprocessableEntity)
		assertXMLText(t, res.XML(t), "errors error", "Subject cannot be blank")
	})
	t.Run("PUT /issues/:id.xml with invalid assignee should return error", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		res := apiCall(t, ts, http.MethodPost, "/users.json", "", `{"user":{"login":"generated1","firstname":"Gen","lastname":"User","mail":"generated1@example.net","password":"generated1pw"}}`, apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
		uid := issuesAPIInt(t, d, `SELECT principal_id FROM user_accounts WHERE login = 'generated1'`)
		res = put(t, ts, "/issues/6.xml", "issue[assigned_to_id]="+itoaTest(uid))
		res.expectStatus(t, http.StatusUnprocessableEntity)
		assertXMLText(t, res.XML(t), "errors error", "Assignee is invalid")
	})
	t.Run("PUT /issues/:id.json", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := journalCount(t, d)
		res := put(t, ts, "/issues/6.json", "issue[subject]=API+update&issue[notes]=A+new+note")
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if s := subjectOf(t, d, 6); s != "API update" {
			t.Errorf("subject %q", s)
		}
		if n := lastNotes(t, d); n != "A new note" {
			t.Errorf("notes %q", n)
		}
	})
	t.Run("PUT /issues/:id.json with failed update", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		res := put(t, ts, "/issues/6.json", "issue[subject]=")
		res.expectStatus(t, http.StatusUnprocessableEntity)
		errs, _ := res.JSON(t)["errors"].([]any)
		if !slices.Contains(errs, any("Subject cannot be blank")) {
			t.Errorf("errors %v", errs)
		}
	})
	for _, f := range []string{"xml", "json"} {
		t.Run("DELETE /issues/:id."+f, func(t *testing.T) {
			ts, d := newFixtureServer(t)
			before := issueCount(t, d)
			res := apiCall(t, ts, http.MethodDelete, "/issues/6."+f, "", "", apiCreds("jsmith"))
			res.expectStatus(t, http.StatusNoContent)
			if res.Body != "" {
				t.Errorf("body %q", res.Body)
			}
			if issueCount(t, d) != before-1 || issuesAPIInt(t, d, `SELECT COUNT(*) FROM issues WHERE id = 6`) != 0 {
				t.Error("issue not deleted")
			}
		})
	}
	t.Run("POST /issues/:id/watchers.xml should add watcher", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		res := apiCall(t, ts, http.MethodPost, "/issues/1/watchers.xml", "application/x-www-form-urlencoded", "user_id=3", apiCreds("jsmith"))
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		var w struct {
			Kind string `db:"watchable_kind"`
			ID   int64  `db:"watchable_id"`
			P    int64  `db:"principal_id"`
		}
		if err := d.Get(context.Background(), &w, `SELECT watchable_kind, watchable_id, principal_id FROM watchers ORDER BY id DESC LIMIT 1`); err != nil {
			t.Fatal(err)
		}
		if w.Kind != "issue" || w.ID != 1 || w.P != 3 {
			t.Errorf("watcher %+v", w)
		}
	})
	t.Run("DELETE /issues/:id/watchers/:user_id.xml should remove watcher", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		issuesAPIExec(t, d, `INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', 1, 3)`)
		res := apiCall(t, ts, http.MethodDelete, "/issues/1/watchers/3.xml", "", "", apiCreds("jsmith"))
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = 1 AND principal_id = 3`); n != 0 {
			t.Error("watcher not removed")
		}
	})
	t.Run("test_create_issue_with_uploaded_file", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		token := issuesAPIUpload(t, ts, "xml", "test_create_with_upload", "jsmith")
		form := "issue[project_id]=1&issue[subject]=Uploaded+file&issue[uploads][][token]=" + token +
			"&issue[uploads][][filename]=test.txt&issue[uploads][][content_type]=text/plain"
		res := apiCall(t, ts, http.MethodPost, "/issues.xml", "application/x-www-form-urlencoded", form, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		id := lastIssue(t, d)
		var at struct {
			ID       int64   `db:"id"`
			Filename string  `db:"filename"`
			CT       *string `db:"content_type"`
			Size     int64   `db:"filesize"`
			Author   int64   `db:"author_id"`
		}
		if err := d.Get(context.Background(), &at, `SELECT id, filename, content_type, filesize, author_id FROM attachments WHERE container_kind = 'issue' AND container_id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if at.Filename != "test.txt" || at.CT == nil || *at.CT != "text/plain" || at.Size != int64(len("test_create_with_upload")) || at.Author != 2 {
			t.Errorf("attachment %+v", at)
		}
		res = apiGet(t, ts, "/issues/"+itoaTest(id)+".xml?include=attachments")
		res.expectStatus(t, 200)
		urls := xmlSelect(t, res.XML(t), "issue attachments attachment content_url")
		if len(urls) != 1 {
			t.Fatalf("attachments %d", len(urls))
		}
		u := nodeText(urls[0])
		dl := apiGet(t, ts, strings.TrimPrefix(u, ts.URL))
		dl.expectStatus(t, 200)
		if dl.Body != "test_create_with_upload" {
			t.Errorf("download %q", dl.Body)
		}
	})
	t.Run("test_create_issue_with_multiple_uploaded_files_as_xml", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		t1 := issuesAPIUpload(t, ts, "xml", "File content 1", "jsmith")
		t2 := issuesAPIUpload(t, ts, "xml", "File content 2", "jsmith")
		payload := `<?xml version="1.0" encoding="UTF-8" ?>
<issue>
  <project_id>1</project_id>
  <tracker_id>1</tracker_id>
  <subject>Issue with multiple attachments</subject>
  <uploads type="array">
    <upload>
      <token>` + t1 + `</token>
      <filename>test1.txt</filename>
    </upload>
    <upload>
      <token>` + t2 + `</token>
      <filename>test1.txt</filename>
    </upload>
  </uploads>
</issue>
`
		res := apiCall(t, ts, http.MethodPost, "/issues.xml", "application/xml", payload, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		if n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM attachments WHERE container_kind = 'issue' AND container_id = ?`, lastIssue(t, d)); n != 2 {
			t.Errorf("attachments %d", n)
		}
	})
	t.Run("test_create_issue_with_multiple_uploaded_files_as_json", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		t1 := issuesAPIUpload(t, ts, "json", "File content 1", "jsmith")
		t2 := issuesAPIUpload(t, ts, "json", "File content 2", "jsmith")
		payload := `{"issue": {"project_id": "1", "tracker_id": "1", "subject": "Issue with multiple attachments",
  "uploads": [{"token": "` + t1 + `", "filename": "test1.txt"}, {"token": "` + t2 + `", "filename": "test2.txt"}]}}`
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/json", payload, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		if n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM attachments WHERE container_kind = 'issue' AND container_id = ?`, lastIssue(t, d)); n != 2 {
			t.Errorf("attachments %d", n)
		}
	})
	t.Run("test_update_issue_with_uploaded_file", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		token := issuesAPIUpload(t, ts, "xml", "test_upload_with_upload", "jsmith")
		before := journalCount(t, d)
		form := "issue[notes]=Attachment+added&issue[uploads][][token]=" + token +
			"&issue[uploads][][filename]=test.txt&issue[uploads][][content_type]=text/plain"
		res := put(t, ts, "/issues/1.xml", form)
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if journalCount(t, d) != before+1 {
			t.Error("journal not created")
		}
		if n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM attachments WHERE container_kind = 'issue' AND container_id = 1 AND filename = 'test.txt'`); n != 1 {
			t.Errorf("attachment not attached")
		}
	})
}

// issuesAPIAnyText は assert_select sel, text（一致する要素のいずれかのテキストが want）。
func issuesAPIAnyText(t *testing.T, n *html.Node, sel, want string) {
	t.Helper()
	var got []string
	for _, e := range xmlSelect(t, n, sel) {
		if nodeText(e) == want {
			return
		}
		got = append(got, nodeText(e))
	}
	t.Errorf("assert_select %q: texts %q, want %q", sel, got, want)
}
