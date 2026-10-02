package query

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
)

// testdata/differential.json.gz は testdata/gen/dump_differential.rb を Redmine 6.1.2 (公式フィクスチャ、
// 時刻固定 2026-01-15 12:00 UTC) で実行して得た正解データ。フィルタ×演算子×値、ソート、グループ、
// 合計の組み合わせごとの結果 (id の並び・件数・グループ別件数・合計) を持つ。

type diffCase struct {
	Scenario    string               `json:"scenario"`
	Kind        string               `json:"kind"`
	User        int64                `json:"user"`
	Anonymous   bool                 `json:"anonymous"`
	Project     *int64               `json:"project"`
	Filters     [][]json.RawMessage  `json:"filters"`
	Sort        [][2]string          `json:"sort"`
	GroupBy     *string              `json:"group_by"`
	Totals      []string             `json:"totals"`
	Applied     []string             `json:"applied"`
	Valid       *bool                `json:"valid"`
	IDs         []int64              `json:"ids"`
	Count       *int64               `json:"count"`
	Grouped     *bool                `json:"grouped"`
	Groups      [][2]json.RawMessage `json:"groups"`
	TotalValues map[string]*float64  `json:"total_values"`
	GroupTotals map[string][][2]any  `json:"group_totals"`
	Error       string               `json:"error"`
}

func loadDiffCases(t *testing.T) []diffCase {
	t.Helper()
	f, err := os.Open("testdata/differential.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []diffCase `json:"cases"`
	}
	if err := json.NewDecoder(gz).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

// applyRichScenario は dump_differential.rb の apply_rich を新スキーマで再現する。
func applyRichScenario(tdb *testDB) {
	x := tdb.exec
	x(`UPDATE issues SET parent_id = NULL, root_id = 1, hier_path = '0000000001/' WHERE id = 1`)
	x(`UPDATE issues SET parent_id = 1, root_id = 1, hier_path = '0000000001/0000000002/' WHERE id = 2`)
	x(`UPDATE issues SET parent_id = 2, root_id = 1, hier_path = '0000000001/0000000002/0000000007/' WHERE id = 7`)
	x(`UPDATE issues SET parent_id = 1, root_id = 1, hier_path = '0000000001/0000000003/' WHERE id = 3`)
	x(`UPDATE issues SET assigned_to_id = 10 WHERE id = 5`)
	x(`UPDATE issues SET assigned_to_id = 3, estimated_hours = 4.5, done_ratio = 50 WHERE id = 13`)
	x(`UPDATE issues SET is_private = ?, assigned_to_id = 2 WHERE id = 9`, true)
	x(`UPDATE issues SET description = 'Notes about printing' WHERE id = 6`)
	type cf struct {
		id                                         int64
		kind, name, format, pv                     string
		all, filter, visible, multiple, searchable bool
		pos                                        int
	}
	for _, c := range []cf{
		{20, "issue", "Int field", "int", "", true, true, true, false, false, 6},
		{21, "issue", "Milestone", "version", "", true, true, true, false, false, 7},
		{22, "issue", "Reviewer", "user", "", true, true, true, true, false, 8},
		{23, "version", "Release date", "date", "", false, true, true, false, false, 1},
		{24, "user", "Team", "string", "", false, true, true, false, false, 3},
		{25, "issue", "Urgent", "bool", "", true, true, false, false, false, 9},
		{26, "issue", "Deadline", "date", "", true, true, true, false, false, 10},
		{27, "project", "Budget", "int", "", false, true, true, false, false, 2},
		{28, "issue", "Labels", "list", `["a","b","c"]`, true, true, true, true, true, 11},
		{29, "time_entry", "Note", "string", "", false, true, true, false, false, 2},
	} {
		var pv any
		if c.pv != "" {
			pv = c.pv
		}
		x(`INSERT INTO custom_fields (id, owner_kind, name, field_format, possible_values, is_required, is_for_all, is_filter, position,
  searchable, editable, visible, multiple) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.id, c.kind, c.name, c.format, pv, false, c.all, c.filter, c.pos, c.searchable, true, c.visible, c.multiple)
	}
	for _, id := range []int64{20, 21, 22, 25, 28} {
		for _, tr := range []int64{1, 2, 3} {
			x(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`, id, tr)
		}
	}
	for _, tr := range []int64{1, 3} {
		x(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (26, ?)`, tr)
	}
	x(`INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (25, 1)`)
	type cv struct {
		cf    int64
		kind  string
		id    int64
		value string
	}
	for i, v := range []cv{
		{20, "issue", 1, "5"}, {20, "issue", 2, "12"}, {20, "issue", 3, ""}, {20, "issue", 7, "-3"}, {20, "issue", 5, "5"},
		{21, "issue", 1, "2"}, {21, "issue", 3, "3"}, {21, "issue", 9, "6"},
		{22, "issue", 1, "2"}, {22, "issue", 1, "3"}, {22, "issue", 2, "3"}, {22, "issue", 6, "2"},
		{23, "version", 2, "2026-01-20"}, {23, "version", 3, "2026-01-15"}, {23, "version", 6, "2025-12-01"},
		{24, "principal", 2, "alpha"}, {24, "principal", 3, "beta"}, {24, "principal", 4, "Alpha team"},
		{25, "issue", 1, "1"}, {25, "issue", 2, "0"}, {25, "issue", 3, "1"}, {25, "issue", 14, "1"},
		{26, "issue", 1, "2026-01-14"}, {26, "issue", 3, "2026-01-16"}, {26, "issue", 6, "2026-01-20"}, {26, "issue", 7, "2025-12-31"}, {26, "issue", 13, "2026-02-01"},
		{27, "project", 1, "1000"}, {27, "project", 2, "50"}, {27, "project", 5, "1000"},
		{28, "issue", 1, "a"}, {28, "issue", 1, "b"}, {28, "issue", 2, "b"}, {28, "issue", 3, "c"}, {28, "issue", 5, "a"},
		{29, "time_entry", 1, "billable"}, {29, "time_entry", 2, "internal"},
	} {
		var val any
		if v.value != "" {
			val = v.value
		}
		x(`INSERT INTO custom_values (id, customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?, ?)`, 100+i, v.kind, v.id, v.cf, val)
	}
	ts := func(s string) db.Time {
		t, err := db.ParseTime(s)
		if err != nil {
			panic(err)
		}
		return db.NewTime(t)
	}
	x(`INSERT INTO issue_journals (id, issue_id, user_id, notes, private_notes, created_at, updated_at) VALUES (6, 1, 3, 'secret note', ?, ?, ?)`,
		true, ts("2026-01-14 10:00:00"), ts("2026-01-14 10:00:00"))
	x(`INSERT INTO issue_journals (id, issue_id, user_id, notes, private_notes, created_at, updated_at) VALUES (7, 3, 2, 'public reply', ?, ?, ?)`,
		false, ts("2026-01-15 09:00:00"), ts("2026-01-15 09:00:00"))
	x(`INSERT INTO issue_journals (id, issue_id, user_id, notes, private_notes, created_at, updated_at) VALUES (8, 13, 4, NULL, ?, ?, ?)`,
		false, ts("2026-01-13 09:00:00"), ts("2026-01-13 09:00:00"))
	x(`INSERT INTO issue_journal_details (id, journal_id, property, prop_key, old_value, value) VALUES (7, 6, 'attr', 'status_id', '2', '1')`)
	x(`INSERT INTO issue_journal_details (id, journal_id, property, prop_key, old_value, value) VALUES (8, 7, 'attr', 'assigned_to_id', '2', '3')`)
	x(`INSERT INTO issue_journal_details (id, journal_id, property, prop_key, old_value, value) VALUES (9, 8, 'attr', 'priority_id', '6', '4')`)
	x(`INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', 3, 10)`)
	x(`INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', 5, 2)`)
	x(`INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', 9, 8)`)
	x(`INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type) VALUES (3, 1, 7, 'precedes')`)
	x(`INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type) VALUES (4, 13, 14, 'duplicates')`)
	x(`INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type) VALUES (5, 3, 8, 'blocks')`)
	x(`INSERT INTO time_entries (id, project_id, user_id, author_id, issue_id, hours, comments, activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at)
VALUES (6, 1, 2, 2, 7, 3.5, 'work on child', 9, '2026-01-14', 2026, 1, 3, ?, ?)`, ts("2026-01-14 08:00:00"), ts("2026-01-14 08:00:00"))
	x(`UPDATE user_preferences SET time_zone = 'Hawaii' WHERE user_id = 2`)
	x(`UPDATE user_preferences SET time_zone = 'Nuku''alofa' WHERE user_id = 3`)
	x(`UPDATE projects SET status = 5 WHERE id = 3`)
}

// comboKey はクエリのテンプレートを共有する単位。
type comboKey struct {
	kind    string
	user    int64
	project int64
}

// diffRunner は 1 シナリオのケースを評価する。利用可能なフィルタ・列は組み合わせごとに一度だけ計算し、
// ケースごとにテンプレートを複製して使う。
type diffRunner struct {
	tdb       *testDB
	templates map[comboKey]*Query
}

func (r *diffRunner) template(t *testing.T, c *diffCase) *Query {
	uid := c.User
	if c.Anonymous {
		uid = 0
	}
	var pid int64
	if c.Project != nil {
		pid = *c.Project
	}
	k := comboKey{c.Kind, uid, pid}
	if q := r.templates[k]; q != nil {
		return q
	}
	q := r.tdb.newQuery(uid, Kind(c.Kind), pid)
	ctx := context.Background()
	if _, err := q.AvailableFilters(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AvailableColumns(ctx); err != nil {
		t.Fatal(err)
	}
	r.templates[k] = q
	return q
}

func (r *diffRunner) run(t *testing.T, c *diffCase) (mismatch string) {
	ctx := context.Background()
	tpl := r.template(t, c)
	q := *tpl
	q.Options = map[string]string{}
	q.Filters = tpl.Filters.Clone()
	if c.Filters != nil {
		q.Filters = NewFilters()
	}
	for _, f := range c.Filters {
		var field, op string
		var vals []string
		_ = json.Unmarshal(f[0], &field)
		_ = json.Unmarshal(f[1], &op)
		_ = json.Unmarshal(f[2], &vals)
		if err := q.AddFilter(ctx, field, op, vals); err != nil {
			return "add filter: " + err.Error()
		}
	}
	if c.Sort != nil {
		q.SetSortCriteria(SortCriteria(c.Sort))
	}
	if c.GroupBy != nil {
		q.GroupBy = *c.GroupBy
	}
	if c.Totals != nil {
		q.SetTotalableNames(c.Totals)
	}
	var diffs []string
	if applied := q.Filters.Keys(); !slices.Equal(applied, c.Applied) && !(len(applied) == 0 && len(c.Applied) == 0) {
		diffs = append(diffs, fmt.Sprintf("applied filters %v, want %v", applied, c.Applied))
	}
	valid, err := q.Valid(ctx)
	if err != nil {
		return "valid: " + err.Error()
	}
	if c.Valid != nil && valid != *c.Valid {
		errs, _ := q.Errors(ctx)
		diffs = append(diffs, fmt.Sprintf("valid %v, want %v (%v)", valid, *c.Valid, errs))
	}
	ids, err := q.IDs(ctx, ListOptions{})
	if c.Error != "" {
		if err == nil {
			diffs = append(diffs, "expected error: "+c.Error)
		}
		return strings.Join(diffs, "; ")
	}
	if err != nil {
		return "ids: " + err.Error()
	}
	if !slices.Equal(ids, c.IDs) && !(len(ids) == 0 && len(c.IDs) == 0) {
		diffs = append(diffs, fmt.Sprintf("ids %v, want %v", ids, c.IDs))
	}
	if c.Count != nil {
		n, err := q.Count(ctx)
		if err != nil {
			return "count: " + err.Error()
		}
		if n != *c.Count {
			diffs = append(diffs, fmt.Sprintf("count %d, want %d", n, *c.Count))
		}
	}
	if c.GroupBy != nil && c.Grouped != nil {
		grouped, err := q.Grouped(ctx)
		if err != nil {
			return "grouped: " + err.Error()
		}
		if grouped != *c.Grouped {
			// Redmine (SQLite) ではタイムスタンプ列はグループ化できないが buropher はできる
			col, _ := q.GroupByColumn(ctx)
			if !(grouped && col != nil && col.Kind == ColumnTimestamp) {
				diffs = append(diffs, fmt.Sprintf("grouped %v, want %v", grouped, *c.Grouped))
			}
		} else if grouped {
			got, err := q.ResultCountByGroup(ctx)
			if err != nil {
				return "count by group: " + err.Error()
			}
			want := map[GroupKey]int64{}
			for _, g := range c.Groups {
				var n int64
				_ = json.Unmarshal(g[1], &n)
				want[jsonKey(g[0])] = n
			}
			if !mapsEqual(got, want, func(a, b int64) bool { return a == b }) {
				diffs = append(diffs, fmt.Sprintf("groups %v, want %v", fmtMap(got), fmtMap(want)))
			}
		}
	}
	if c.TotalValues != nil {
		for name, want := range c.TotalValues {
			got, err := q.TotalFor(ctx, name)
			if err != nil {
				diffs = append(diffs, "total "+name+": "+err.Error())
				continue
			}
			w := 0.0
			if want != nil {
				w = *want
			}
			if math.Abs(got-w) > 1e-6 {
				diffs = append(diffs, fmt.Sprintf("total %s = %v, want %v", name, got, w))
			}
		}
		for name, gt := range c.GroupTotals {
			got, err := q.TotalByGroupFor(ctx, name)
			if err != nil {
				diffs = append(diffs, "group total "+name+": "+err.Error())
				continue
			}
			want := map[GroupKey]float64{}
			for _, kv := range gt {
				k := GroupKey{Null: true}
				if kv[0] != nil {
					k = GroupKey{Value: fmt.Sprint(kv[0])}
				}
				f, _ := kv[1].(float64)
				want[k] = f
			}
			if !mapsEqual(got, want, func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }) {
				diffs = append(diffs, fmt.Sprintf("group total %s %v, want %v", name, fmtMap(got), fmtMap(want)))
			}
		}
	}
	return strings.Join(diffs, "; ")
}

func jsonKey(raw json.RawMessage) GroupKey {
	if string(raw) == "null" {
		return GroupKey{Null: true}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return GroupKey{Value: string(raw)}
	}
	return GroupKey{Value: s}
}

func mapsEqual[V any](a, b map[GroupKey]V, eq func(V, V) bool) bool {
	// 値が 0 のグループは Redmine の合計で省略されないが、件数 0 は出ない。キー集合も比較する。
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !eq(v, w) {
			return false
		}
	}
	return true
}

func fmtMap[V any](m map[GroupKey]V) string {
	var keys []GroupKey
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%v", k, m[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func caseLabel(c *diffCase) string {
	var fs []string
	for _, f := range c.Filters {
		fs = append(fs, string(f[0])+" "+string(f[1])+" "+string(f[2]))
	}
	p := "nil"
	if c.Project != nil {
		p = fmt.Sprint(*c.Project)
	}
	u := fmt.Sprint(c.User)
	if c.Anonymous {
		u = "anon"
	}
	s := fmt.Sprintf("[%s %s user=%s project=%s] filters=%s", c.Scenario, c.Kind, u, p, strings.Join(fs, " & "))
	if c.Sort != nil {
		s += fmt.Sprintf(" sort=%v", c.Sort)
	}
	if c.GroupBy != nil {
		s += " group_by=" + *c.GroupBy
	}
	return s
}

// TestDifferential は Redmine の結果と全ケースを比較する。
func TestDifferential(t *testing.T) {
	cases := loadDiffCases(t)
	byScenario := map[string][]*diffCase{}
	var scenarios []string
	for i := range cases {
		c := &cases[i]
		if _, ok := byScenario[c.Scenario]; !ok {
			scenarios = append(scenarios, c.Scenario)
		}
		byScenario[c.Scenario] = append(byScenario[c.Scenario], c)
	}
	dbtest.ForEachDialect(t, func(t *testing.T, d0 *db.DB) {
		for _, sc := range scenarios {
			t.Run(sc, func(t *testing.T) {
				var d *db.DB
				if sc == scenarios[0] {
					d = d0
				} else if d0.Dialect().Name() == db.SQLite {
					d = dbtest.NewSQLite(t)
				} else {
					d = dbtest.NewPostgres(t)
				}
				tdb := newTestDB(t, d)
				if sc == "rich" {
					applyRichScenario(tdb)
				}
				r := &diffRunner{tdb: tdb, templates: map[comboKey]*Query{}}
				pass, fail := 0, 0
				byKind := map[string][2]int{}
				var failures []string
				for i, c := range byScenario[sc] {
					// -short では 10 件に 1 件だけ比較する
					if testing.Short() && i%10 != 0 {
						continue
					}
					m := r.run(t, c)
					st := byKind[c.Kind]
					if m == "" {
						pass++
						st[0]++
					} else {
						fail++
						st[1]++
						failures = append(failures, caseLabel(c)+": "+m)
					}
					byKind[c.Kind] = st
				}
				t.Logf("scenario %s: %d/%d passed (%.2f%%) %v", sc, pass, pass+fail, 100*float64(pass)/float64(pass+fail), byKind)
				for i, f := range failures {
					if i >= 100000 {
						t.Errorf("... and %d more", len(failures)-i)
						break
					}
					t.Error(f)
				}
			})
		}
	})
}
