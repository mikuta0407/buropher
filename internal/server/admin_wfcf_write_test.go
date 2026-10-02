package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルはワークフロー・カスタムフィールドの更新系アクションの振る舞い（DB への反映とリダイレクト）を
// test/functional/workflows_controller_test.rb / custom_fields_controller_test.rb /
// custom_field_enumerations_controller_test.rb に倣って確認する。画面のバイト単位の一致は
// testdata/compat/scenarios/admin_workflows_cf.yml（参照 Redmine との差分比較）で確認している。

func count(t *testing.T, d *db.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWorkflowsUpdate(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	// test_post_edit: 遷移を置き換える（old_status 4 → 5 を追加、1 → 2 を削除、no_change は変えない）
	before12 := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = 2 AND tracker_id = 1 AND old_status_id = 1 AND new_status_id = 2`)
	if before12 != 1 {
		t.Fatalf("fixture 1->2 = %d", before12)
	}
	res, _ := adminSubmit(t, c, ts, http.MethodPatch, "/workflows/update", url.Values{
		"role_id[]": {"2"}, "tracker_id[]": {"1"},
		"transitions[4][5][always]": {"1"},
		"transitions[1][2][always]": {"0"},
		"transitions[1][3][always]": {"no_change"},
		"transitions[3][1][author]": {"1"},
		"transitions[3][1][assignee]": {"1"},
	})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/workflows/edit") {
		t.Fatalf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
	}
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = 2 AND tracker_id = 1 AND old_status_id = 4 AND new_status_id = 5 AND author = ? AND assignee = ?`, false, false); n != 1 {
		t.Errorf("4->5 = %d", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = 2 AND tracker_id = 1 AND old_status_id = 1 AND new_status_id = 2`); n != 0 {
		t.Errorf("1->2 = %d", n)
	}
	// author と assignee の両方を指定すると 1 行に両方の印が付く
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = 2 AND tracker_id = 1 AND old_status_id = 3 AND new_status_id = 1 AND author = ? AND assignee = ?`, true, true); n != 1 {
		t.Errorf("3->1 author+assignee = %d", n)
	}
	// author だけ外すと assignee の行が残る
	adminSubmit(t, c, ts, http.MethodPatch, "/workflows/update", url.Values{
		"role_id[]": {"2"}, "tracker_id[]": {"1"}, "transitions[3][1][author]": {"0"},
	})
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = 2 AND tracker_id = 1 AND old_status_id = 3 AND new_status_id = 1 AND author = ? AND assignee = ?`, false, true); n != 1 {
		t.Errorf("3->1 assignee only = %d", n)
	}

	// 新規チケット（old_status 0 → NULL）の遷移
	adminSubmit(t, c, ts, http.MethodPatch, "/workflows/update", url.Values{
		"role_id[]": {"3"}, "tracker_id[]": {"2"}, "transitions[0][4][always]": {"1"},
	})
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = 3 AND tracker_id = 2 AND old_status_id IS NULL AND new_status_id = 4`); n != 1 {
		t.Errorf("new issue -> 4 = %d", n)
	}

	// jsmith（管理者でない）は 403
	cj := login(t, ts, "jsmith", "jsmith")
	_, page := get(t, cj, ts.URL+"/logout")
	res, _ = post(t, cj, ts.URL+"/workflows/update", url.Values{"authenticity_token": {csrfToken(t, page)}, "_method": {"patch"},
		"role_id[]": {"1"}, "tracker_id[]": {"1"}})
	if res.StatusCode != 403 {
		t.Errorf("jsmith: %d", res.StatusCode)
	}
}

func TestWorkflowsUpdatePermissionsAndDuplicate(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	// test_post_permissions
	res, _ := adminSubmit(t, c, ts, http.MethodPatch, "/workflows/update_permissions", url.Values{
		"role_id[]": {"1", "2"}, "tracker_id[]": {"2"},
		"permissions[1][assigned_to_id]": {"required"},
		"permissions[1][due_date]":       {"readonly"},
		"permissions[2][2]":              {"readonly"},
		"permissions[3][start_date]":     {"no_change"},
		"permissions[1][bogus]":          {"readonly"},
	})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/workflows/permissions") {
		t.Fatalf("status %d", res.StatusCode)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_field_rules WHERE tracker_id = 2 AND status_id = 1 AND core_field = 'assigned_to_id' AND rule = 'required'`); n != 2 {
		t.Errorf("assigned_to_id required = %d", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_field_rules WHERE tracker_id = 2 AND status_id = 2 AND custom_field_id = 2 AND rule = 'readonly'`); n != 2 {
		t.Errorf("cf 2 readonly = %d", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_field_rules`); n != 6 {
		t.Errorf("rules = %d", n)
	}
	// 空の規則は削除のみ
	adminSubmit(t, c, ts, http.MethodPatch, "/workflows/update_permissions", url.Values{
		"role_id[]": {"1"}, "tracker_id[]": {"2"}, "permissions[1][assigned_to_id]": {""},
	})
	if n := count(t, d, `SELECT COUNT(*) FROM workflow_field_rules WHERE status_id = 1 AND core_field = 'assigned_to_id'`); n != 1 {
		t.Errorf("after clear = %d", n)
	}

	// test_post_copy_one_to_one
	res, body := adminSubmit(t, c, ts, http.MethodPost, "/workflows/duplicate", url.Values{"source_tracker_id": {""}, "source_role_id": {"1"}})
	if res.StatusCode != 200 || !strings.Contains(body, "Please select a source tracker or role") {
		t.Errorf("no source: %d", res.StatusCode)
	}
	res, body = adminSubmit(t, c, ts, http.MethodPost, "/workflows/duplicate", url.Values{"source_tracker_id": {"1"}, "source_role_id": {"2"}})
	if res.StatusCode != 200 || !strings.Contains(body, "Please select target tracker(s) and role(s)") {
		t.Errorf("no target: %d", res.StatusCode)
	}
	srcN := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE tracker_id = 1 AND role_id = 2`)
	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/workflows/duplicate", url.Values{
		"source_tracker_id": {"1"}, "source_role_id": {"2"}, "target_tracker_ids[]": {"3"}, "target_role_ids[]": {"1", "3"},
	})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/workflows/copy?source_role_id=2&source_tracker_id=1") {
		t.Fatalf("copy: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	for _, role := range []int{1, 3} {
		if n := count(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE tracker_id = 3 AND role_id = ?`, role); n != srcN {
			t.Errorf("role %d: %d transitions, want %d", role, n, srcN)
		}
	}
}

func TestCustomFieldsWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	// 検証エラー（名前なし・選択肢なし）は new を再描画
	res, body := adminSubmit(t, c, ts, http.MethodPost, "/custom_fields", url.Values{
		"type": {"IssueCustomField"}, "custom_field[field_format]": {"list"}, "custom_field[name]": {""},
	})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Name cannot be blank</li>") || !strings.Contains(body, "<li>Possible values cannot be blank</li>") {
		t.Fatalf("invalid create: %d\n%s", res.StatusCode, extract(body, "<div id='errorExplanation'>", "</div>"))
	}

	// test_create_list_custom_field
	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/custom_fields", url.Values{
		"type": {"IssueCustomField"}, "custom_field[field_format]": {"list"}, "custom_field[name]": {"test_post_new_list"},
		"custom_field[possible_values]": {"0.1\n0.2\n"}, "custom_field[default_value]": {"0.1"},
		"custom_field[tracker_ids][]": {"1", "3", ""}, "custom_field[visible]": {"0"}, "custom_field[role_ids][]": {"1", ""},
		"custom_field[multiple]": {"1"}, "custom_field[searchable]": {"1"},
	})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/custom_fields?tab=IssueCustomField") {
		t.Fatalf("create: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var id int64
	if err := d.Get(context.Background(), &id, `SELECT id FROM custom_fields WHERE name = 'test_post_new_list'`); err != nil {
		t.Fatal(err)
	}
	var pv, pos string
	_ = d.Get(context.Background(), &pv, `SELECT possible_values FROM custom_fields WHERE id = ?`, id)
	_ = d.Get(context.Background(), &pos, `SELECT position FROM custom_fields WHERE id = ?`, id)
	if pv != `["0.1","0.2"]` || pos != "6" {
		t.Errorf("possible_values %s position %s", pv, pos)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM custom_fields_trackers WHERE custom_field_id = ?`, id); n != 2 {
		t.Errorf("trackers %d", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM custom_fields_roles WHERE custom_field_id = ?`, id); n != 1 {
		t.Errorf("roles %d", n)
	}

	// 同名は taken、IssueCustomField で非表示かつロールなしは base エラー
	res, body = adminSubmit(t, c, ts, http.MethodPost, "/custom_fields", url.Values{
		"type": {"IssueCustomField"}, "custom_field[field_format]": {"string"}, "custom_field[name]": {"Database"},
		"custom_field[visible]": {"0"}, "custom_field[role_ids][]": {""},
	})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Name has already been taken</li>") || !strings.Contains(body, "<li>Roles cannot be blank</li>") {
		t.Errorf("taken: %d\n%s", res.StatusCode, extract(body, "<div id='errorExplanation'>", "</div>"))
	}

	// update（表示をすべてに戻すとロールが外れる。field_format は変更できない）
	res, _ = adminSubmit(t, c, ts, http.MethodPut, "/custom_fields/"+itoa(id), url.Values{
		"custom_field[name]": {"renamed"}, "custom_field[visible]": {"1"}, "custom_field[field_format]": {"int"},
	})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/custom_fields/"+itoa(id)+"/edit") {
		t.Fatalf("update: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var name, format string
	_ = d.Get(context.Background(), &name, `SELECT name FROM custom_fields WHERE id = ?`, id)
	_ = d.Get(context.Background(), &format, `SELECT field_format FROM custom_fields WHERE id = ?`, id)
	if name != "renamed" || format != "list" {
		t.Errorf("name %s format %s", name, format)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM custom_fields_roles WHERE custom_field_id = ?`, id); n != 0 {
		t.Errorf("roles not cleared: %d", n)
	}

	// 並べ替え（XHR の PUT は 200 で本文なし）: position 6 → 1 で他の行が 1 つずつ後ろへ
	if res := adminXHR(t, c, ts, "/custom_fields/"+itoa(id), url.Values{"custom_field[position]": {"1"}}); res.StatusCode != 200 {
		t.Errorf("reorder: %d", res.StatusCode)
	}
	var positions []string
	_ = d.Select(context.Background(), &positions, `SELECT name || ':' || position FROM custom_fields WHERE owner_kind = 'issue' ORDER BY position, id`)
	if got := strings.Join(positions, ","); got != "renamed:1,Searchable field:2,Database:3,Float field:4,Custom date:5,Project 1 cf:6" {
		t.Errorf("positions %s", got)
	}

	// destroy（値も消え、後ろの position が詰まる）
	res, _ = adminSubmit(t, c, ts, http.MethodDelete, "/custom_fields/2", nil)
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/custom_fields?tab=IssueCustomField") {
		t.Fatalf("destroy: %d", res.StatusCode)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM custom_values WHERE custom_field_id = 2`); n != 0 {
		t.Errorf("values remain: %d", n)
	}
	positions = nil
	_ = d.Select(context.Background(), &positions, `SELECT name || ':' || position FROM custom_fields WHERE owner_kind = 'issue' ORDER BY position, id`)
	if got := strings.Join(positions, ","); got != "renamed:1,Database:2,Float field:3,Custom date:4,Project 1 cf:5" {
		t.Errorf("positions after destroy %s", got)
	}

	// 未知の種類は種類の選択画面
	if res, body := get(t, c, ts.URL+"/custom_fields/new?type=Foo"); res.StatusCode != 200 || !strings.Contains(body, `name="type"`) {
		t.Errorf("select_type: %d", res.StatusCode)
	}
}

func TestCustomFieldEnumerationsWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	res, _ := adminSubmit(t, c, ts, http.MethodPost, "/custom_fields", url.Values{
		"type": {"IssueCustomField"}, "custom_field[field_format]": {"enumeration"}, "custom_field[name]": {"Enum"},
	})
	if res.StatusCode != 302 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	var id int64
	_ = d.Get(context.Background(), &id, `SELECT id FROM custom_fields WHERE name = 'Enum'`)
	base := "/custom_fields/" + itoa(id) + "/enumerations"
	for _, n := range []string{"Foo", "Bar", ""} {
		res, _ := adminSubmit(t, c, ts, http.MethodPost, base, url.Values{"custom_field_enumeration[name]": {n}})
		if res.StatusCode != 302 {
			t.Fatalf("create enumeration %q: %d", n, res.StatusCode)
		}
	}
	var rows []string
	_ = d.Select(context.Background(), &rows, `SELECT name || ':' || position || ':' || active FROM custom_field_enumerations WHERE custom_field_id = ? ORDER BY position`, id)
	if got := strings.Join(rows, ","); got != "Foo:1:1,Bar:2:1" {
		t.Errorf("enumerations %s", got)
	}
	var fooID, barID int64
	_ = d.Get(context.Background(), &fooID, `SELECT id FROM custom_field_enumerations WHERE name = 'Foo'`)
	_ = d.Get(context.Background(), &barID, `SELECT id FROM custom_field_enumerations WHERE name = 'Bar'`)

	// update_each（順序の入れ替えと無効化）
	res, _ = adminSubmit(t, c, ts, http.MethodPut, base, url.Values{
		"custom_field_enumerations[" + itoa(fooID) + "][name]":     {"Baz"},
		"custom_field_enumerations[" + itoa(fooID) + "][position]": {"2"},
		"custom_field_enumerations[" + itoa(fooID) + "][active]":   {"0"},
		"custom_field_enumerations[" + itoa(barID) + "][position]": {"1"},
	})
	if res.StatusCode != 302 {
		t.Fatalf("update_each: %d", res.StatusCode)
	}
	rows = nil
	_ = d.Select(context.Background(), &rows, `SELECT name || ':' || position || ':' || active FROM custom_field_enumerations WHERE custom_field_id = ? ORDER BY position`, id)
	if got := strings.Join(rows, ","); got != "Bar:1:1,Baz:2:0" {
		t.Errorf("after update_each %s", got)
	}

	// 使用中の選択肢は確認画面、移行先を指定すると値を付け替えて削除
	if _, err := d.Exec(context.Background(), `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('issue', 1, ?, ?)`, id, itoa(fooID)); err != nil {
		t.Fatal(err)
	}
	res, body := adminSubmit(t, c, ts, http.MethodDelete, base+"/"+itoa(fooID), nil)
	if res.StatusCode != 200 || !strings.Contains(body, `name="reassign_to_id"`) {
		t.Fatalf("destroy in use: %d", res.StatusCode)
	}
	res, _ = adminSubmit(t, c, ts, http.MethodDelete, base+"/"+itoa(fooID), url.Values{"reassign_to_id": {itoa(barID)}})
	if res.StatusCode != 302 {
		t.Fatalf("destroy reassign: %d", res.StatusCode)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM custom_values WHERE custom_field_id = ? AND value = ?`, id, itoa(barID)); n != 1 {
		t.Errorf("reassigned values %d", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM custom_field_enumerations WHERE id = ?`, fooID); n != 0 {
		t.Errorf("not destroyed")
	}
	// 存在しない選択肢は 404
	if res, _ := adminSubmit(t, c, ts, http.MethodDelete, base+"/999", nil); res.StatusCode != 404 {
		t.Errorf("missing: %d", res.StatusCode)
	}
}
