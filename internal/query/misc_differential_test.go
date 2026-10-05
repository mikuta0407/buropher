// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
)

// testdata/misc.json.gz は testdata/gen/dump_misc.rb を Redmine 7.0.1 で実行して得た正解データ（REDMINE_FIXTURES_ROOT=_reference/redmine7-fixtures）
// (保存クエリの可視性・編集可否、build_from_params、既定クエリ、issues の preload 値、
// available_filters_as_json、演算子ラベル、列の属性)。

type miscData struct {
	Visibility      map[string]map[string]json.RawMessage `json:"visibility"`
	BuildFromParams []struct {
		Kind                string          `json:"kind"`
		User                string          `json:"user"`
		Project             *int64          `json:"project"`
		Params              map[string]any  `json:"params"`
		Filters             [][]any         `json:"filters"`
		ColumnNames         []string        `json:"column_names"`
		Columns             []string        `json:"columns"`
		InlineColumns       []string        `json:"inline_columns"`
		BlockColumns        []string        `json:"block_columns"`
		SortCriteria        [][2]string     `json:"sort_criteria"`
		GroupBy             *string         `json:"group_by"`
		TotalableNames      []string        `json:"totalable_names"`
		DisplayType         string          `json:"display_type"`
		DrawRelations       *bool           `json:"draw_relations"`
		DrawProgressLine    *bool           `json:"draw_progress_line"`
		DrawSelectedColumns *bool           `json:"draw_selected_columns"`
		Valid               bool            `json:"valid"`
		IDs                 []int64         `json:"ids"`
		AsParamsSort        *string         `json:"as_params_sort"`
		CSS                 *string         `json:"css"`
		Extra               json.RawMessage `json:"-"`
	} `json:"build_from_params"`
	Defaults []struct {
		Scenario string `json:"scenario"`
		Kind     string `json:"kind"`
		User     string `json:"user"`
		Project  *int64 `json:"project"`
		ID       *int64 `json:"id"`
	} `json:"defaults"`
	IssueRows []struct {
		User            string  `json:"user"`
		Project         *int64  `json:"project"`
		ID              int64   `json:"id"`
		SpentHours      float64 `json:"spent_hours"`
		TotalSpentHours float64 `json:"total_spent_hours"`
		LastUpdatedBy   *int64  `json:"last_updated_by"`
		Relations       []int64 `json:"relations"`
		LastNotes       string  `json:"last_notes"`
		CustomValues    [][]any `json:"custom_values"`
		Watchers        []int64 `json:"watchers"`
	} `json:"issue_rows"`
	FiltersJSON []struct {
		Kind    string                     `json:"kind"`
		User    string                     `json:"user"`
		Project *int64                     `json:"project"`
		Filters map[string]json.RawMessage `json:"filters"`
		Keys    []string                   `json:"keys"`
	} `json:"filters_json"`
	JournalsVersions []struct {
		User     string  `json:"user"`
		Project  *int64  `json:"project"`
		Filters  [][]any `json:"filters"`
		Journals []int64 `json:"journals"`
		Versions []int64 `json:"versions"`
	} `json:"journals_versions"`
	OperatorsLabels  map[string]string `json:"operators_labels"`
	ColumnCaptions   [][]any           `json:"column_captions"`
	TEColumnCaptions [][]any           `json:"te_column_captions"`
}

func loadMisc(t *testing.T) *miscData {
	t.Helper()
	f, err := os.Open("testdata/misc.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var m miscData
	if err := json.NewDecoder(gz).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return &m
}

// applyMiscScenario は dump_misc.rb の追加クエリを再現する。
func applyMiscScenario(tdb *testDB) {
	tdb.exec(`INSERT INTO queries (id, kind, project_id, user_id, name, visibility, filters, options) VALUES (20, 'issue', 1, 2, 'Roles query', 1, ?, '{}')`,
		`{"status_id":{"operator":"o","values":[""]}}`)
	tdb.exec(`INSERT INTO queries_roles (query_id, role_id) VALUES (20, 2)`)
	tdb.exec(`INSERT INTO queries (id, kind, project_id, user_id, name, visibility, filters, options) VALUES (21, 'issue', NULL, 1, 'Global roles query', 1, ?, '{}')`,
		`{"tracker_id":{"operator":"=","values":["1"]}}`)
	tdb.exec(`INSERT INTO queries_roles (query_id, role_id) VALUES (21, 1)`)
	tdb.exec(`INSERT INTO queries (id, kind, project_id, user_id, name, visibility, filters, options) VALUES (22, 'issue', 5, 1, 'Private project query', 2, '{}', '{}')`)
}

func miscUser(s string) int64 {
	if s == "anon" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// paramsToValues は Ruby の params ハッシュを Rails 形式の url.Values にする。
func paramsToValues(prefix string, v any, out url.Values) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			key := k
			if prefix != "" {
				key = prefix + "[" + k + "]"
			}
			paramsToValues(key, val, out)
		}
	case []any:
		if len(x) > 0 {
			if _, nested := x[0].([]any); nested {
				// [["subject", "desc"]] (sort_criteria) は "subject:desc" 形式で渡す
				var c SortCriteria
				for _, e := range x {
					p := e.([]any)
					c = append(c, [2]string{fmt.Sprint(p[0]), fmt.Sprint(p[len(p)-1])})
				}
				out.Set(prefix, c.ToParam())
				return
			}
		}
		for _, e := range x {
			out.Add(prefix+"[]", fmt.Sprint(e))
		}
	default:
		out.Set(prefix, fmt.Sprint(x))
	}
}

func TestMiscDifferential(t *testing.T) {
	m := loadMisc(t)
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		tdb := newTestDB(t, d)
		applyMiscScenario(tdb)

		t.Run("visibility", func(t *testing.T) {
			var allIDs []int64
			allIDs = tdb.ints(`SELECT id FROM queries ORDER BY id`)
			for us, v := range m.Visibility {
				env := tdb.env(miscUser(us))
				for _, kind := range []struct {
					key  string
					kind Kind
				}{{"IssueQuery", KindIssue}, {"TimeEntryQuery", KindTimeEntry}, {"ProjectQuery", KindProject},
					{"ProjectAdminQuery", KindProjectAdmin}, {"UserQuery", KindUser}} {
					var want []int64
					_ = json.Unmarshal(v[kind.key], &want)
					cond, err := VisibleCondition(ctx, env.Auth, kind.kind)
					if err != nil {
						t.Fatal(err)
					}
					got := tdb.ints(`SELECT queries.id FROM queries LEFT OUTER JOIN projects ON queries.project_id = projects.id WHERE ` + cond + ` ORDER BY queries.id`)
					if !slices.Equal(got, want) && len(got)+len(want) > 0 {
						t.Errorf("user %s %s.visible = %v, want %v", us, kind.key, got, want)
					}
				}
				var vis, edit [][2]any
				_ = json.Unmarshal(v["visible?"], &vis)
				_ = json.Unmarshal(v["editable_by?"], &edit)
				for i, id := range allIDs {
					q, err := Load(ctx, env, id, "")
					if err != nil {
						t.Fatal(err)
					}
					gv, err := q.VisibleTo(ctx, env.Auth)
					if err != nil {
						t.Fatal(err)
					}
					if gv != vis[i][1].(bool) {
						t.Errorf("user %s query %d visible? = %v, want %v", us, id, gv, vis[i][1])
					}
					ge, err := q.EditableBy(ctx, env.Auth)
					if err != nil {
						t.Fatal(err)
					}
					if ge != edit[i][1].(bool) {
						t.Errorf("user %s query %d editable_by? = %v, want %v", us, id, ge, edit[i][1])
					}
				}
				var wantList []int64
				_ = json.Unmarshal(v["global_or_on_project_1"], &wantList)
				list, err := ListVisible(ctx, env.Auth, KindIssue, tdb.project(1), false)
				if err != nil {
					t.Fatal(err)
				}
				var gotList []int64
				for _, s := range list {
					gotList = append(gotList, s.ID)
				}
				if !slices.Equal(gotList, wantList) && len(gotList)+len(wantList) > 0 {
					t.Errorf("user %s global_or_on_project(1).sorted = %v, want %v", us, gotList, wantList)
				}
			}
		})

		t.Run("build_from_params", func(t *testing.T) {
			for _, c := range m.BuildFromParams {
				var pid int64
				if c.Project != nil {
					pid = *c.Project
				}
				q := tdb.newQuery(miscUser(c.User), Kind(c.Kind), pid)
				vals := url.Values{}
				paramsToValues("", c.Params, vals)
				if err := q.BuildFromParams(ctx, ParseParams(vals), nil); err != nil {
					t.Fatal(err)
				}
				label := fmt.Sprintf("%s user=%s project=%v params=%v", c.Kind, c.User, c.Project, c.Params)
				var gotFilters [][]any
				for _, k := range q.Filters.Keys() {
					f, _ := q.Filters.Get(k)
					var vs []any
					for _, v := range f.Values {
						vs = append(vs, v)
					}
					var op any = f.Operator
					if f.Operator == "" {
						op = nil
					}
					gotFilters = append(gotFilters, []any{k, op, vs})
				}
				if fmt.Sprint(gotFilters) != fmt.Sprint(c.Filters) {
					t.Errorf("%s: filters %v, want %v", label, gotFilters, c.Filters)
				}
				cn := q.ColumnNames()
				if len(cn) == 0 {
					cn = nil
				}
				if !slices.Equal(cn, c.ColumnNames) {
					t.Errorf("%s: column_names %v, want %v", label, cn, c.ColumnNames)
				}
				colNames := func(cols []*Column, err error) []string {
					if err != nil {
						t.Fatal(err)
					}
					var out []string
					for _, c := range cols {
						out = append(out, c.Name)
					}
					return out
				}
				if got := colNames(q.Columns(ctx)); !slices.Equal(got, c.Columns) {
					t.Errorf("%s: columns %v, want %v", label, got, c.Columns)
				}
				if c.InlineColumns != nil {
					if got := colNames(q.InlineColumns(ctx)); !slices.Equal(got, c.InlineColumns) {
						t.Errorf("%s: inline_columns %v, want %v", label, got, c.InlineColumns)
					}
					if got := colNames(q.BlockColumns(ctx)); !slices.Equal(got, c.BlockColumns) && len(got)+len(c.BlockColumns) > 0 {
						t.Errorf("%s: block_columns %v, want %v", label, got, c.BlockColumns)
					}
				}
				if got := q.SortCriteria(); fmt.Sprint([][2]string(got)) != fmt.Sprint(c.SortCriteria) && len(got)+len(c.SortCriteria) > 0 {
					t.Errorf("%s: sort_criteria %v, want %v", label, got, c.SortCriteria)
				}
				wantGroup := ""
				if c.GroupBy != nil {
					wantGroup = *c.GroupBy
				}
				if q.GroupBy != wantGroup {
					t.Errorf("%s: group_by %q, want %q", label, q.GroupBy, wantGroup)
				}
				if c.TotalableNames != nil && !slices.Equal(q.TotalableNames(), c.TotalableNames) && len(q.TotalableNames())+len(c.TotalableNames) > 0 {
					t.Errorf("%s: totalable_names %v, want %v", label, q.TotalableNames(), c.TotalableNames)
				}
				if c.DisplayType != "" && q.DisplayType() != c.DisplayType {
					t.Errorf("%s: display_type %q, want %q", label, q.DisplayType(), c.DisplayType)
				}
				if c.DrawRelations != nil {
					if q.DrawRelations() != *c.DrawRelations || q.DrawProgressLine() != *c.DrawProgressLine || q.DrawSelectedColumns() != *c.DrawSelectedColumns {
						t.Errorf("%s: draw options %v %v %v, want %v %v %v", label, q.DrawRelations(), q.DrawProgressLine(), q.DrawSelectedColumns(),
							*c.DrawRelations, *c.DrawProgressLine, *c.DrawSelectedColumns)
					}
				}
				valid, err := q.Valid(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if valid != c.Valid {
					t.Errorf("%s: valid %v, want %v", label, valid, c.Valid)
				}
				ids, err := q.IDs(ctx, ListOptions{})
				if err != nil {
					t.Errorf("%s: %v", label, err)
					continue
				}
				if !slices.Equal(ids, c.IDs) && len(ids)+len(c.IDs) > 0 {
					t.Errorf("%s: ids %v, want %v", label, ids, c.IDs)
				}
				if c.AsParamsSort != nil && q.SortCriteria().ToParam() != *c.AsParamsSort {
					t.Errorf("%s: sort param %q, want %q", label, q.SortCriteria().ToParam(), *c.AsParamsSort)
				}
				if c.CSS != nil && q.CSSClasses() != *c.CSS {
					t.Errorf("%s: css %q, want %q", label, q.CSSClasses(), *c.CSS)
				}
			}
		})

		t.Run("issue_rows", func(t *testing.T) {
			type key struct {
				user    string
				project int64
			}
			byCombo := map[key][]int{}
			var order []key
			for i, r := range m.IssueRows {
				var pid int64
				if r.Project != nil {
					pid = *r.Project
				}
				k := key{r.User, pid}
				if _, ok := byCombo[k]; !ok {
					order = append(order, k)
				}
				byCombo[k] = append(byCombo[k], i)
			}
			for _, k := range order {
				q := tdb.newQuery(miscUser(k.user), KindIssue, k.project)
				q.Filters = NewFilters()
				mustFilter(t, q, "status_id", "*")
				if err := q.SetColumnNames(ctx, []string{"subject", "spent_hours", "total_spent_hours", "last_updated_by", "relations", "last_notes", "cf_2", "cf_1", "watcher_users"}); err != nil {
					t.Fatal(err)
				}
				rows, err := q.Issues(ctx, ListOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != len(byCombo[k]) {
					t.Errorf("%v: %d rows, want %d", k, len(rows), len(byCombo[k]))
					continue
				}
				for i, idx := range byCombo[k] {
					want := m.IssueRows[idx]
					got := rows[i]
					label := fmt.Sprintf("%v issue %d", k, want.ID)
					if got.ID != want.ID {
						t.Errorf("%s: id %d", label, got.ID)
						continue
					}
					f := func(p *float64) float64 {
						if p == nil {
							return -1
						}
						return *p
					}
					if math.Abs(f(got.SpentHours)-want.SpentHours) > 1e-9 || math.Abs(f(got.TotalSpentHours)-want.TotalSpentHours) > 1e-9 {
						t.Errorf("%s: spent %v/%v, want %v/%v", label, f(got.SpentHours), f(got.TotalSpentHours), want.SpentHours, want.TotalSpentHours)
					}
					if !reflect.DeepEqual(got.LastUpdatedByID, want.LastUpdatedBy) {
						t.Errorf("%s: last_updated_by %v, want %v", label, ptrS(got.LastUpdatedByID), ptrS(want.LastUpdatedBy))
					}
					rel := slices.Sorted(slices.Values(got.RelationIDs))
					if !slices.Equal(rel, want.Relations) && len(rel)+len(want.Relations) > 0 {
						t.Errorf("%s: relations %v, want %v", label, rel, want.Relations)
					}
					if got.LastNotes == nil || *got.LastNotes != want.LastNotes {
						t.Errorf("%s: last_notes %v, want %q", label, got.LastNotes, want.LastNotes)
					}
					var cvs [][]any
					var cfIDs []int64
					for id := range got.CustomValues {
						cfIDs = append(cfIDs, id)
					}
					slices.Sort(cfIDs)
					for _, id := range cfIDs {
						vs := slices.Clone(got.CustomValues[id])
						sort.Strings(vs)
						for _, v := range vs {
							cvs = append(cvs, []any{float64(id), v})
						}
					}
					if fmt.Sprint(cvs) != fmt.Sprint(want.CustomValues) && len(cvs)+len(want.CustomValues) > 0 {
						t.Errorf("%s: custom_values %v, want %v", label, cvs, want.CustomValues)
					}
					w := slices.Sorted(slices.Values(got.WatcherIDs))
					if !slices.Equal(w, want.Watchers) && len(w)+len(want.Watchers) > 0 {
						t.Errorf("%s: watchers %v, want %v", label, w, want.Watchers)
					}
				}
			}
		})

		t.Run("filters_json", func(t *testing.T) {
			for _, c := range m.FiltersJSON {
				var pid int64
				if c.Project != nil {
					pid = *c.Project
				}
				q := tdb.newQuery(miscUser(c.User), Kind(c.Kind), pid)
				if c.Kind == "issue" {
					q.Filters = NewFilters()
					q.Filters.Set("status_id", Filter{Operator: "o", Values: []string{""}})
					q.Filters.Set("assigned_to_id", Filter{Operator: "=", Values: []string{"5"}})
					q.Filters.Set("project_id", Filter{Operator: "=", Values: []string{"1"}})
				}
				keys, js, err := q.AvailableFiltersAsJSON(ctx)
				if err != nil {
					t.Fatal(err)
				}
				label := fmt.Sprintf("%s user=%s project=%v", c.Kind, c.User, c.Project)
				if !slices.Equal(keys, c.Keys) {
					t.Errorf("%s: keys %v, want %v", label, keys, c.Keys)
				}
				for _, k := range c.Keys {
					var want struct {
						Type   string  `json:"type"`
						Name   string  `json:"name"`
						Remote bool    `json:"remote"`
						Values [][]any `json:"values"`
					}
					_ = json.Unmarshal(c.Filters[k], &want)
					got, ok := js[k]
					if !ok {
						continue
					}
					if got.Type != want.Type || got.Name != want.Name || got.Remote != want.Remote {
						t.Errorf("%s: filter %s = {%s %q %v}, want {%s %q %v}", label, k, got.Type, got.Name, got.Remote, want.Type, want.Name, want.Remote)
					}
					var wv [][]string
					for _, v := range want.Values {
						var row []string
						for _, x := range v {
							row = append(row, fmt.Sprint(x))
						}
						wv = append(wv, row)
					}
					if fmt.Sprint(got.Values) != fmt.Sprint(wv) {
						t.Errorf("%s: filter %s values %v, want %v", label, k, got.Values, wv)
					}
				}
			}
		})

		t.Run("journals_versions", func(t *testing.T) {
			for _, c := range m.JournalsVersions {
				var pid int64
				if c.Project != nil {
					pid = *c.Project
				}
				q := tdb.newQuery(miscUser(c.User), KindIssue, pid)
				q.Filters = NewFilters()
				for _, f := range c.Filters {
					var vals []string
					for _, v := range f[2].([]any) {
						vals = append(vals, fmt.Sprint(v))
					}
					mustFilter(t, q, f[0].(string), f[1].(string), vals...)
				}
				label := fmt.Sprintf("user=%s project=%v filters=%v", c.User, c.Project, c.Filters)
				js, err := q.JournalIDs(ctx, ListOptions{Order: []string{"issue_journals.id DESC"}})
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(js, c.Journals) && len(js)+len(c.Journals) > 0 {
					t.Errorf("%s: journals %v, want %v", label, js, c.Journals)
				}
				vs, err := q.VersionIDs(ctx, ListOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(vs, c.Versions) && len(vs)+len(c.Versions) > 0 {
					t.Errorf("%s: versions %v, want %v", label, vs, c.Versions)
				}
			}
		})

		t.Run("labels", func(t *testing.T) {
			env := tdb.env(1)
			got := env.OperatorsLabels()
			for op, want := range m.OperatorsLabels {
				if got[op] != want {
					t.Errorf("operator %s label %q, want %q", op, got[op], want)
				}
			}
			for _, tc := range []struct {
				kind Kind
				want [][]any
			}{{KindIssue, m.ColumnCaptions}, {KindTimeEntry, m.TEColumnCaptions}} {
				q := tdb.newQuery(1, tc.kind, 0)
				cols, err := q.AvailableColumns(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(cols) != len(tc.want) {
					t.Errorf("%s: %d columns, want %d", tc.kind, len(cols), len(tc.want))
					continue
				}
				for i, c := range cols {
					w := tc.want[i]
					totalable, _ := w[5].(bool)
					groupable, _ := w[4].(bool)
					do := ""
					if s, ok := w[6].(string); ok {
						do = s
					}
					if c.Kind == ColumnTimestamp {
						groupable = c.Groupable // Redmine (SQLite) ではタイムスタンプ列をグループ化できない
					}
					if c.Name != w[0] || c.CaptionText(env) != w[1] || c.Inline != w[2] || c.IsSortable() != w[3] ||
						c.Groupable != groupable || c.Totalable != totalable || c.DefaultOrder != do {
						t.Errorf("%s column %d: {%s %q %v %v %v %v %q}, want %v", tc.kind, i, c.Name, c.CaptionText(env), c.Inline,
							c.IsSortable(), c.Groupable, c.Totalable, c.DefaultOrder, w)
					}
				}
			}
		})
	})

	t.Run("defaults", func(t *testing.T) {
		scenarios := map[string]func(tdb *testDB){
			"none":            func(*testDB) {},
			"setting":         func(tdb *testDB) { tdb.setting("default_issue_query", "4"); tdb.setting("default_project_query", "11") },
			"setting_private": func(tdb *testDB) { tdb.setting("default_issue_query", "3") },
			"project": func(tdb *testDB) {
				tdb.exec(`UPDATE projects SET default_issue_query_id = 1 WHERE id = 1`)
				tdb.setting("default_issue_query", "4")
			},
			"project_private": func(tdb *testDB) { tdb.exec(`UPDATE projects SET default_issue_query_id = 2 WHERE id = 1`) },
			"pref": func(tdb *testDB) {
				tdb.exec(`UPDATE user_preferences SET default_issue_query_id = 5, default_project_query_id = 12 WHERE user_id = 2`)
				tdb.setting("default_issue_query", "4")
			},
			"pref_invisible": func(tdb *testDB) {
				tdb.exec(`UPDATE user_preferences SET default_issue_query_id = 3 WHERE user_id = 2`)
				tdb.setting("default_issue_query", "4")
			},
			"pref_project_query": func(tdb *testDB) {
				tdb.exec(`UPDATE user_preferences SET default_issue_query_id = 2 WHERE user_id = 3`)
			},
		}
		dbs := map[string]*testDB{}
		for _, c := range m.Defaults {
			tdb := dbs[c.Scenario]
			if tdb == nil {
				tdb = newTestDB(t, dbtest.NewSQLite(t))
				applyMiscScenario(tdb)
				scenarios[c.Scenario](tdb)
				dbs[c.Scenario] = tdb
			}
			env := tdb.env(miscUser(c.User))
			var p *domain.Project
			if c.Project != nil {
				p = tdb.project(*c.Project)
			}
			q, err := Default(context.Background(), env, Kind(c.Kind), p)
			if err != nil {
				t.Fatal(err)
			}
			var got *int64
			if q != nil {
				got = &q.ID
			}
			if !reflect.DeepEqual(got, c.ID) {
				t.Errorf("%s %s.default(user=%s, project=%v) = %v, want %v", c.Scenario, c.Kind, c.User, ptrS(c.Project), ptrS(got), ptrS(c.ID))
			}
		}
	})
}

func ptrS(p *int64) string {
	if p == nil {
		return "nil"
	}
	return strconv.FormatInt(*p, 10)
}
