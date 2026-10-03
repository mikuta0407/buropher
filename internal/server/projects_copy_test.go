package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/issues"
)

// TestProjectsCopyIssues は Project#copy_issues（ProjectCopyTest の test_copy_issues 系）。
func TestProjectsCopyIssues(t *testing.T) {
	setup := func(t *testing.T) (*httptest.Server, *db.DB, *fakeNotifier, *http.Client) {
		ts, d, n := newIssuesWriteServer(t)
		admin := login(t, ts, "admin", "admin")
		// 親子関係（#3 を #7 の子に）・遅延付きの関連・ロックされたバージョン
		res, _ := projSubmit(t, admin, ts, http.MethodPatch, "/issues/3", url.Values{"issue[parent_issue_id]": {"7"}}, false)
		expectRedirect(t, res, "/issues/3")
		mastersExec(t, d, `INSERT INTO issue_relations (issue_from_id, issue_to_id, relation_type, delay) VALUES (1, 8, 'precedes', 2)`)
		mastersExec(t, d, `UPDATE versions SET status = 'locked' WHERE id = 2`)
		n.take()
		return ts, d, n, admin
	}
	copyForm := func(notifications string) url.Values {
		v := url.Values{
			"project[name]": {"Copied"}, "project[identifier]": {"copied"},
			"project[enabled_module_names][]": {"issue_tracking", ""},
			"project[tracker_ids][]":          {"1", "2", "3"},
			"only[]":                          {"members", "versions", "issue_categories", "issues", ""},
		}
		if notifications != "" {
			v.Set("notifications", notifications)
		}
		return v
	}

	t.Run("copies issues", func(t *testing.T) {
		ts, d, n, admin := setup(t)
		res, _ := projSubmit(t, admin, ts, http.MethodPost, "/projects/ecookbook/copy", copyForm(""), false)
		expectRedirect(t, res, "/projects/copied/settings")
		pid := queryInt(t, d, `SELECT id FROM projects WHERE identifier = 'copied'`)
		if got, want := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE project_id = ?`, pid), queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE project_id = 1`); got != want {
			t.Fatalf("copied issues = %d, want %d", got, want)
		}
		// copied はコピー元 id → コピー先の id（フィクスチャの ecookbook の件名は一意）
		copied := func(srcID int) int64 {
			t.Helper()
			return queryInt(t, d, `SELECT id FROM issues WHERE project_id = ? AND subject = (SELECT subject FROM issues WHERE id = ?)`, pid, srcID)
		}
		// keep_status・作成者は User.current
		if s := queryInt(t, d, `SELECT status_id FROM issues WHERE id = ?`, copied(8)); s != 5 {
			t.Errorf("status = %d, want 5 (keep_status)", s)
		}
		if a := queryInt(t, d, `SELECT author_id FROM issues WHERE id = ?`, copied(12)); a != 1 {
			t.Errorf("author = %d, want 1 (User.current)", a)
		}
		// バージョン・カテゴリは名前で付け替え、ロックされたバージョンも割り当てたまま元の状態に戻す
		v2 := queryInt(t, d, `SELECT id FROM versions WHERE project_id = ? AND name = (SELECT name FROM versions WHERE id = 2)`, pid)
		if fv := queryInt(t, d, `SELECT fixed_version_id FROM issues WHERE id = ?`, copied(2)); fv != v2 {
			t.Errorf("fixed_version = %d, want %d", fv, v2)
		}
		if st := queryString(t, d, `SELECT status FROM versions WHERE id = ?`, v2); st != "locked" {
			t.Errorf("copied version status = %s, want locked", st)
		}
		cat := queryInt(t, d, `SELECT id FROM issue_categories WHERE project_id = ? AND name = (SELECT name FROM issue_categories WHERE id = 1)`, pid)
		if c := queryInt(t, d, `SELECT category_id FROM issues WHERE id = ?`, copied(1)); c != cat {
			t.Errorf("category = %d, want %d", c, cat)
		}
		// 親チケットはコピー先へ
		if p := queryInt(t, d, `SELECT parent_id FROM issues WHERE id = ?`, copied(3)); p != copied(7) {
			t.Errorf("parent = %d, want %d", p, copied(7))
		}
		// 関連はコピー先同士で作り直す（関連・遅延付きの先行）
		if c := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? AND issue_to_id = ? AND relation_type = 'relates'`, copied(2), copied(3)); c != 1 {
			t.Errorf("relates relation = %d", c)
		}
		if c := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? AND issue_to_id = ? AND relation_type = 'precedes' AND delay = 2`, copied(1), copied(8)); c != 1 {
			t.Errorf("precedes relation = %d", c)
		}
		// copied_to 関連は作らない
		if c := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE relation_type = 'copied_to'`); c != 0 {
			t.Errorf("copied_to relations = %d", c)
		}
		// notifications が '1' でなければ通知しない
		if got := n.take(); len(got) != 0 {
			t.Errorf("notifications = %d, want 0", len(got))
		}
	})

	// test_copy_issues_should_reassign_version_custom_fields_to_copied_versions
	t.Run("version custom field", func(t *testing.T) {
		ts, d, _, admin := setup(t)
		mastersExec(t, d, `INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, multiple, position) VALUES ('issue', 'Version CF', 'version', 1, 0, 99)`)
		cf := queryInt(t, d, `SELECT id FROM custom_fields WHERE name = 'Version CF'`)
		mastersExec(t, d, `INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) SELECT ?, id FROM trackers`, cf)
		mastersExec(t, d, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('issue', 1, ?, '3')`, cf)
		res, _ := projSubmit(t, admin, ts, http.MethodPost, "/projects/ecookbook/copy", copyForm(""), false)
		expectRedirect(t, res, "/projects/copied/settings")
		pid := queryInt(t, d, `SELECT id FROM projects WHERE identifier = 'copied'`)
		v3 := queryInt(t, d, `SELECT id FROM versions WHERE project_id = ? AND name = (SELECT name FROM versions WHERE id = 3)`, pid)
		got := queryString(t, d, `SELECT cv.value FROM custom_values cv JOIN issues i ON i.id = cv.customized_id
WHERE cv.customized_kind = 'issue' AND cv.custom_field_id = ? AND i.project_id = ? AND i.subject = (SELECT subject FROM issues WHERE id = 1)`, cf, pid)
		if got != strconv.FormatInt(v3, 10) {
			t.Errorf("version custom value = %q, want %d", got, v3)
		}
	})

	t.Run("notifications", func(t *testing.T) {
		ts, d, n, admin := setup(t)
		res, _ := projSubmit(t, admin, ts, http.MethodPost, "/projects/ecookbook/copy", copyForm("1"), false)
		expectRedirect(t, res, "/projects/copied/settings")
		pid := queryInt(t, d, `SELECT id FROM projects WHERE identifier = 'copied'`)
		adds := 0
		for _, x := range n.take() {
			if x.Event == issues.NotifyIssueAdd {
				adds++
			}
		}
		if want := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE project_id = ?`, pid); int64(adds) != want {
			t.Errorf("issue_add notifications = %d, want %d", adds, want)
		}
	})
}
