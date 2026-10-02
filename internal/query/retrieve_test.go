package query

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"testing"
)

// QueriesHelper#retrieve_query・IssuesController#retrieve_default_query と保存・読み込みのテスト。

func TestRetrieveQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		env := tdb.env(2)
		p1 := tdb.project(1)
		opts := RetrieveOptions{UseSession: true}

		// セッション無し: パラメータから組み立て、セッションに保存する
		v := url.Values{"set_filter": {"1"}, "f[]": {"status_id", "tracker_id"}, "op[status_id]": {"*"}, "op[tracker_id]": {"="},
			"v[tracker_id][]": {"2"}, "c[]": {"subject", "status"}, "sort": {"subject:desc"}, "group_by": {"status"}}
		q, sess, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(v), nil, opts)
		if err != nil {
			t.Fatal(err)
		}
		if sess == nil || sess.ProjectID == nil || *sess.ProjectID != 1 || sess.GroupBy != "status" || len(sess.Filters) != 2 {
			t.Fatalf("session = %+v", sess)
		}
		ids, err := q.IssueIDs(ctx, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, ids, []int64{2})

		// 同じプロジェクトでパラメータ無し: セッションから復元
		q2, sess2, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(url.Values{}), sess, opts)
		if err != nil {
			t.Fatal(err)
		}
		if q2.GroupBy != "status" || !slices.Equal(q2.ColumnNames(), []string{"subject", "status"}) || q2.SortCriteria().ToParam() != "subject:desc" {
			t.Errorf("restored query = group %q columns %v sort %q", q2.GroupBy, q2.ColumnNames(), q2.SortCriteria().ToParam())
		}
		if sess2 != sess {
			t.Errorf("session should be kept")
		}
		if f, _ := q2.Filters.Get("tracker_id"); !slices.Equal(f.Values, []string{"2"}) {
			t.Errorf("restored filters = %v", q2.Filters.Keys())
		}

		// 別プロジェクトではセッションを使わず既定のクエリ
		q3, sess3, err := Retrieve(ctx, env, KindIssue, nil, ParseParams(url.Values{}), sess, opts)
		if err != nil {
			t.Fatal(err)
		}
		if q3.GroupBy != "" || q3.OperatorFor("status_id") != "o" || sess3.ProjectID != nil {
			t.Errorf("new query = %v / %+v", q3.Filters.Keys(), sess3)
		}

		// sort パラメータはセッションのソートを上書きする
		q4, sess4, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(url.Values{"sort": {"id"}}), sess, opts)
		if err != nil {
			t.Fatal(err)
		}
		if q4.SortCriteria().ToParam() != "id" || sess4.Sort.ToParam() != "id" {
			t.Errorf("sort = %q / %q", q4.SortCriteria().ToParam(), sess4.Sort.ToParam())
		}

		// query_id: 可視な保存クエリ
		q5, sess5, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(url.Values{"query_id": {"1"}}), nil, opts)
		if err != nil {
			t.Fatal(err)
		}
		if q5.ID != 1 || sess5.ID != 1 || *sess5.ProjectID != 1 {
			t.Errorf("query_id: %d / %+v", q5.ID, sess5)
		}
		// 他プロジェクトのクエリは見つからない
		if _, _, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(url.Values{"query_id": {"7"}}), nil, opts); !errors.Is(err, ErrNotFound) {
			t.Errorf("query of other project: %v", err)
		}
		// 他人の非公開クエリは見えない
		if _, _, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(url.Values{"query_id": {"2"}}), nil, opts); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("private query of other user: %v", err)
		}
		// 保存クエリ id をセッションに持つ場合は保存クエリを読み直す
		q6, _, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(url.Values{}), sess5, opts)
		if err != nil {
			t.Fatal(err)
		}
		if q6.ID != 1 || q6.Project == nil || q6.Project.ID != 1 {
			t.Errorf("session query = %d", q6.ID)
		}
		// use_session = false ならセッションを返さない
		if _, s, err := Retrieve(ctx, env, KindIssue, p1, ParseParams(url.Values{}), sess, RetrieveOptions{}); err != nil || s != nil {
			t.Errorf("no session: %v %v", s, err)
		}
	})
}

func TestDefaultQueryID(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		tdb.setting("default_issue_query", "4")
		env := tdb.env(2)
		id, setFilter, err := DefaultQueryID(ctx, env, KindIssue, nil, ParseParams(url.Values{}), nil, true, false)
		if err != nil || id != 4 || setFilter {
			t.Errorf("default = %d %v %v", id, setFilter, err)
		}
		for _, v := range []url.Values{{"set_filter": {"1"}}, {"query_id": {"5"}}} {
			if id, _, _ := DefaultQueryID(ctx, env, KindIssue, nil, ParseParams(v), nil, true, false); id != 0 {
				t.Errorf("%v: default = %d", v, id)
			}
		}
		if id, _, _ := DefaultQueryID(ctx, env, KindIssue, nil, ParseParams(url.Values{}), nil, true, true); id != 0 {
			t.Errorf("api: default = %d", id)
		}
		if id, sf, _ := DefaultQueryID(ctx, env, KindIssue, nil, ParseParams(url.Values{"without_default": {"1"}}), nil, true, false); id != 0 || !sf {
			t.Errorf("without_default: %d %v", id, sf)
		}
		// セッションに有効な保存クエリがあれば既定クエリを適用しない
		sess := &SessionState{ID: 5}
		if id, _, _ := DefaultQueryID(ctx, env, KindIssue, nil, ParseParams(url.Values{}), sess, true, false); id != 0 {
			t.Errorf("session query: default = %d", id)
		}
		// プロジェクトが違えば適用する
		p := int64(1)
		sess.ProjectID = &p
		if id, _, _ := DefaultQueryID(ctx, env, KindIssue, nil, ParseParams(url.Values{}), sess, true, false); id != 4 {
			t.Errorf("session query of other project: default = %d", id)
		}
	})
}

func TestQuerySaveAndLoad(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		env := tdb.env(2)
		q, err := New(ctx, env, KindIssue, tdb.project(1))
		if err != nil {
			t.Fatal(err)
		}
		q.Name = "My query"
		q.UserID = &env.User().ID
		q.Visibility = VisibilityRoles
		mustFilter(t, q, "tracker_id", "=", "1", "2")
		mustFilter(t, q, "assigned_to_id", "=", "me")
		if err := q.SetColumnNames(ctx, []string{"subject", "cf_1"}); err != nil {
			t.Fatal(err)
		}
		q.SetSortParam("priority:desc,id")
		q.GroupBy = "tracker"
		q.SetTotalableNames([]string{"estimated_hours"})
		q.SetDrawRelations("0")
		// ロール限定でロールが無ければ保存できない
		var inv *ErrInvalid
		if err := q.Save(ctx); !errors.As(err, &inv) {
			t.Fatalf("save without roles: %v", err)
		}
		q.RoleIDs = []int64{2, 1}
		if err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
		l, err := Load(ctx, env, q.ID, KindIssue)
		if err != nil || l == nil {
			t.Fatalf("load: %v %v", l, err)
		}
		if l.Name != "My query" || l.Visibility != VisibilityRoles || !slices.Equal(l.RoleIDs, []int64{1, 2}) || l.GroupBy != "tracker" ||
			!slices.Equal(l.ColumnNames(), []string{"subject", "cf_1"}) || l.SortCriteria().ToParam() != "priority:desc,id" ||
			!slices.Equal(l.TotalableNames(), []string{"estimated_hours"}) || l.DrawRelations() || l.Project == nil || l.Project.ID != 1 {
			t.Errorf("loaded query = %+v", l)
		}
		if tdb.d.Dialect().Name() == "sqlite" && !slices.Equal(l.Filters.Keys(), []string{"status_id", "tracker_id", "assigned_to_id"}) {
			t.Errorf("filter order = %v", l.Filters.Keys())
		}
		if f, _ := l.Filters.Get("assigned_to_id"); f.Operator != "=" || !slices.Equal(f.Values, []string{"me"}) {
			t.Errorf("filter = %+v", f)
		}
		// 公開以外に変えるとロールは消え、プロジェクトの既定クエリ参照も外れる
		tdb.exec(`UPDATE projects SET default_issue_query_id = ? WHERE id = 1`, q.ID)
		l.Visibility = VisibilityPrivate
		if err := l.Save(ctx); err != nil {
			t.Fatal(err)
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM queries_roles WHERE query_id = ?`, q.ID); n[0] != 0 {
			t.Errorf("roles not cleared")
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM projects WHERE default_issue_query_id = ?`, q.ID); n[0] != 0 {
			t.Errorf("project default query not cleared")
		}
		if err := l.Delete(ctx); err != nil {
			t.Fatal(err)
		}
		if x, err := Load(ctx, env, q.ID, ""); err != nil || x != nil {
			t.Errorf("deleted query: %v %v", x, err)
		}
	})
}

func TestResultRows(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(1, KindTimeEntry, 0)
		ids := queryIDs(t, q)
		rows, err := q.TimeEntries(ctx, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(ids) || len(rows) == 0 {
			t.Fatalf("%d rows, want %d", len(rows), len(ids))
		}
		for i, r := range rows {
			if r.ID != ids[i] || r.Hours <= 0 || r.SpentOn.IsZero() {
				t.Errorf("row %d = %+v", i, r)
			}
		}
		pq := tdb.newQuery(1, KindProject, 0)
		pids := queryIDs(t, pq)
		ps, err := pq.Projects(ctx, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != len(pids) {
			t.Fatalf("%d projects, want %d", len(ps), len(pids))
		}
		for i, p := range ps {
			if p.ID != pids[i] {
				t.Errorf("project %d = %d, want %d", i, p.ID, pids[i])
			}
		}
	})
}

func TestAsParams(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(2, KindIssue, 1)
		mustFilter(t, q, "tracker_id", "=", "1")
		q.GroupBy = "status"
		q.SetSortParam("subject:desc")
		v := q.AsParams()
		if v.Get("set_filter") != "1" || v.Get("op[tracker_id]") != "=" || v.Get("group_by") != "status" || v.Get("sort") != "subject:desc" ||
			!slices.Equal(v["f[]"], []string{"status_id", "tracker_id"}) {
			t.Errorf("as_params = %v", v)
		}
		// 組み立て直すと同じクエリになる
		q2 := tdb.newQuery(2, KindIssue, 1)
		if err := q2.BuildFromParams(context.Background(), ParseParams(v), nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(q2.Filters.Keys(), q.Filters.Keys()) || q2.GroupBy != "status" || q2.SortCriteria().ToParam() != "subject:desc" {
			t.Errorf("rebuilt = %v %q %q", q2.Filters.Keys(), q2.GroupBy, q2.SortCriteria().ToParam())
		}
		saved, err := Load(context.Background(), tdb.env(2), 1, KindIssue)
		if err != nil {
			t.Fatal(err)
		}
		if saved.AsParams().Get("query_id") != "1" {
			t.Errorf("saved as_params = %v", saved.AsParams())
		}
	})
}
