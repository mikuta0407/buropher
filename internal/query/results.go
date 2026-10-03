package query

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// ListOptions は結果取得のオプション (issues / issue_ids / results_scope の options)。
type ListOptions struct {
	// Order は ORDER BY の項目 (options[:order])。nil なら sort_clause。
	Order []string
	// Limit は最大件数 (0 以下なら無制限)。
	Limit int
	// Offset は読み飛ばす件数。
	Offset int
	// Conditions は追加の WHERE 条件 (options[:conditions])。
	Conditions string
	// ConditionArgs は Conditions のプレースホルダ引数。
	ConditionArgs []any
}

// QueryError はクエリの実行エラー (Query::StatementInvalid)。
type QueryError struct{ Err error }

func (e *QueryError) Error() string { return "query: statement invalid: " + e.Err.Error() }
func (e *QueryError) Unwrap() error { return e.Err }

func stmtErr(err error) error {
	if err == nil {
		return nil
	}
	return &QueryError{Err: err}
}

// baseSQL は base_scope の "FROM ... WHERE ..." と引数。
func (q *Query) baseSQL(ctx context.Context, extraJoins []string, opts *ListOptions) (string, []any, error) {
	return q.baseSQLFor(ctx, extraJoins, opts, "")
}

// joinPruner は件数・合計のように結果の行を数えるだけの SQL で、使われていない
// LEFT OUTER JOIN (主キーで高々 1 行に結合するもの) を base_scope の FROM から除く。
// 除いても行の数と集計値は変わらない (結合先の列を参照していなければ)。
type joinPruner interface {
	pruneJoins(from, used string) string
}

// baseSQLFor は baseSQL と同じだが、aggregate (SELECT する集計式) が空でなければ
// joinPruner で不要な結合を除く (件数・合計用)。
func (q *Query) baseSQLFor(ctx context.Context, extraJoins []string, opts *ListOptions, aggregate string) (string, []any, error) {
	from, where, err := q.impl.baseScope(ctx, q)
	if err != nil {
		return "", nil, err
	}
	if opts != nil && strings.TrimSpace(opts.Conditions) != "" {
		where = joinFrags(" AND ", where, sqlf("("+opts.Conditions+")", opts.ConditionArgs...))
	}
	if p, ok := q.impl.(joinPruner); ok && aggregate != "" {
		from = p.pruneJoins(from, aggregate+" "+strings.Join(extraJoins, " ")+" "+where.SQL)
	}
	s := " FROM " + from
	if len(extraJoins) > 0 {
		s += " " + strings.Join(extraJoins, " ")
	}
	if !where.empty() {
		s += " WHERE " + where.SQL
	}
	return s, where.Args, nil
}

// orderOption は [group_by_sort_order, (options[:order] || sort_clause)] に種別ごとの最終キーを付けたもの。
func (q *Query) orderOption(ctx context.Context, opts ListOptions) ([]string, error) {
	gs, err := q.GroupBySortOrder(ctx)
	if err != nil {
		return nil, err
	}
	order := opts.Order
	if order == nil {
		order, err = q.SortClause(ctx)
		if err != nil {
			return nil, err
		}
	}
	var out []string
	for _, s := range append(gs, order...) {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	switch q.Kind {
	case KindIssue:
		if !slices.Contains(out, "issues.id ASC") && !slices.Contains(out, "issues.id DESC") {
			out = append(out, "issues.id DESC")
		}
	case KindTimeEntry:
		out = append(out, "time_entries.id ASC")
	case KindUser:
		// 同値の並びを DB 間で揃える (SQLite 上の Redmine は暗黙に id 順になる)
		if !slices.Contains(out, "users.id ASC") && !slices.Contains(out, "users.id DESC") {
			out = append(out, "users.id ASC")
		}
	case KindProject, KindProjectAdmin:
		lft, err := q.projectsLftExpr(ctx, "projects")
		if err != nil {
			return nil, err
		}
		out = append(out, lft+" ASC")
	}
	return q.env.nullsOrder(out), nil
}

// nullsOrder は ORDER BY 項目の NULL の位置を DB 間で揃える。
// SQLite・MySQL (Redmine の大半の環境) は NULL を最小値として扱う (昇順で先頭) が、
// PostgreSQL は最大値扱いなので、PG では NULLS FIRST / NULLS LAST を明示する。
func (e *Env) nullsOrder(terms []string) []string {
	if e.dialect().Name() != db.Postgres {
		return terms
	}
	out := make([]string, len(terms))
	for i, t := range terms {
		u := strings.ToUpper(strings.TrimSpace(t))
		switch {
		case strings.Contains(u, " NULLS "):
			out[i] = t
		case strings.HasSuffix(u, " DESC"):
			out[i] = t + " NULLS LAST"
		case strings.HasSuffix(u, " ASC"):
			out[i] = t + " NULLS FIRST"
		default:
			out[i] = t + " NULLS FIRST"
		}
	}
	return out
}

// Count は base_scope.count (IssueQuery#issue_count, ProjectQuery#result_count 等)。
func (q *Query) Count(ctx context.Context) (int64, error) {
	s, args, err := q.baseSQLFor(ctx, nil, nil, "COUNT(*)")
	if err != nil {
		return 0, err
	}
	var n int64
	if err := q.env.Q.Get(ctx, &n, "SELECT COUNT(*)"+s, args...); err != nil {
		return 0, stmtErr(err)
	}
	return n, nil
}

// IssueCount は IssueQuery#issue_count。
func (q *Query) IssueCount(ctx context.Context) (int64, error) { return q.Count(ctx) }

// IDs は結果の id を順に返す (IssueQuery#issue_ids / results_scope.pluck(:id))。
func (q *Query) IDs(ctx context.Context, opts ListOptions) ([]int64, error) {
	order, err := q.orderOption(ctx, opts)
	if err != nil {
		return nil, err
	}
	joins, err := q.impl.joinsForOrderStatement(ctx, q, strings.Join(order, ","))
	if err != nil {
		return nil, err
	}
	s, args, err := q.baseSQL(ctx, joins, &opts)
	if err != nil {
		return nil, err
	}
	sqlText := "SELECT " + q.impl.queriedTable() + ".id" + s
	if len(order) > 0 {
		sqlText += " ORDER BY " + strings.Join(order, ", ")
	}
	if opts.Limit > 0 || opts.Offset > 0 {
		limit := opts.Limit
		if limit <= 0 {
			limit = -1
		}
		sqlText += " " + q.env.dialect().LimitOffset(limit, opts.Offset)
	}
	var ids []int64
	if err := q.env.Q.Select(ctx, &ids, sqlText, args...); err != nil {
		return nil, stmtErr(fmt.Errorf("%w\n  sql: %s", err, sqlText))
	}
	return ids, nil
}

// IssueIDs は IssueQuery#issue_ids。
func (q *Query) IssueIDs(ctx context.Context, opts ListOptions) ([]int64, error) {
	return q.IDs(ctx, opts)
}

// ---------------------------------------------------------------- グループ

// GroupKey はグループのキー。関連列はレコード id、日付は "YYYY-MM-DD"、真偽は "true"/"false"、
// 数値は整数または Ruby の Float#to_s 表記、カスタムフィールドは cast_value の結果を文字列にしたもの。
type GroupKey struct {
	Null  bool
	Value string
}

func (k GroupKey) String() string {
	if k.Null {
		return "<nil>"
	}
	return k.Value
}

// groupedRows は SELECT group_expr, agg ... GROUP BY group_expr の結果。
func (q *Query) groupedRows(ctx context.Context, agg string, extraJoins []string, extraWhere string) (map[GroupKey]float64, error) {
	c, err := q.GroupByColumn(ctx)
	if err != nil || c == nil {
		return nil, err
	}
	gs := c.GroupSQL
	joins, err := q.impl.joinsForOrderStatement(ctx, q, gs)
	if err != nil {
		return nil, err
	}
	joins = append(joins, extraJoins...)
	s, args, err := q.baseSQL(ctx, joins, &ListOptions{Conditions: extraWhere})
	if err != nil {
		return nil, err
	}
	rows, err := q.env.Q.Query(ctx, "SELECT "+gs+" AS group_key, "+agg+" AS agg"+s+" GROUP BY "+gs, args...)
	if err != nil {
		return nil, stmtErr(err)
	}
	defer rows.Close()
	out := map[GroupKey]float64{}
	for rows.Next() {
		var key any
		var val any
		if err := rows.Scan(&key, &val); err != nil {
			return nil, err
		}
		k := q.groupKey(c, key)
		out[k] += toFloat(val)
	}
	return out, stmtErr(rows.Err())
}

// groupKey は DB から読んだグループ値を GroupKey にする (Rails のキーの型と cast_value に合わせる)。
func (q *Query) groupKey(c *Column, v any) GroupKey {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	if v == nil {
		return GroupKey{Null: true}
	}
	if c.CustomField != nil {
		s := rawString(v)
		cast := c.CustomField.CastValue(&s)
		if cast == nil {
			return GroupKey{Null: true}
		}
		return GroupKey{Value: formatValue(cast)}
	}
	switch c.Name {
	case "is_private", "is_public":
		switch x := v.(type) {
		case bool:
			return GroupKey{Value: strconv.FormatBool(x)}
		default:
			return GroupKey{Value: strconv.FormatBool(toFloat(x) != 0)}
		}
	}
	if t, ok := v.(time.Time); ok {
		return GroupKey{Value: t.Format("2006-01-02")}
	}
	if c.Kind == ColumnTimestamp || c.Name == "start_date" || c.Name == "due_date" || c.Name == "spent_on" {
		s := rawString(v)
		if len(s) >= 10 {
			s = s[:10]
		}
		return GroupKey{Value: s}
	}
	return GroupKey{Value: rawString(v)}
}

func rawString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "1"
		}
		return "0"
	case time.Time:
		return x.Format("2006-01-02")
	}
	return fmt.Sprint(v)
}

// formatValue は cast_value の結果を文字列にする。
func formatValue(v any) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return rubyFloatToS(x)
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// rubyFloatToS は Float#to_s の近似 (整数値は "1.0")。
func rubyFloatToS(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e16 {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case int64:
		return float64(x)
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case []byte:
		f, _ := strconv.ParseFloat(strings.TrimSpace(string(x)), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	}
	f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
	return f
}

// ResultCountByGroup は result_count_by_group (グループ化していなければ nil)。
func (q *Query) ResultCountByGroup(ctx context.Context) (map[GroupKey]int64, error) {
	m, err := q.groupedRows(ctx, "COUNT(*)", nil, "")
	if err != nil || m == nil {
		return nil, err
	}
	out := make(map[GroupKey]int64, len(m))
	for k, v := range m {
		out[k] = int64(v)
	}
	return out, nil
}

// ---------------------------------------------------------------- 合計

// round2 は Float#round(2)。
func round2(f float64) float64 {
	r := math.Round(f*100) / 100
	if r == 0 {
		return 0
	}
	return r
}

// totalSpec は total_for_<column> の集計式・追加 JOIN・追加条件。
func (q *Query) totalSpec(ctx context.Context, c *Column) (agg string, joins []string, where string, cast func(float64) float64, err error) {
	if c.CustomField != nil {
		// Numeric#total_for_scope
		cf := c.CustomField
		// scope.joins(:custom_values): 関連 CF 列でもクエリ対象自身のカスタム値を結合する (Redmine と同じ)
		joins = []string{"INNER JOIN custom_values ON custom_values.customized_kind = '" + q.impl.customizedKind() + "'" +
			" AND custom_values.customized_id = " + q.impl.queriedTable() + ".id"}
		where = "custom_values.custom_field_id = " + itoa(cf.ID) + " AND custom_values.value <> ''"
		agg = "SUM(CAST(custom_values.value AS decimal(30,3)))"
		if cf.FieldFormat == "int" {
			cast = func(f float64) float64 { return math.Trunc(f) }
		} else {
			cast = round2
		}
		return
	}
	cast = round2
	switch q.Kind {
	case KindIssue:
		switch c.Name {
		case "estimated_hours":
			agg = "SUM(issues.estimated_hours)"
		case "estimated_remaining_hours":
			agg = "SUM(" + estimatedRemainingHoursSQL + ")"
		case "spent_hours":
			vis, e := q.timeEntryVisibleCondition(ctx)
			if e != nil {
				return "", nil, "", nil, e
			}
			joins = []string{"INNER JOIN time_entries ON time_entries.issue_id = issues.id"}
			where = vis
			agg = "SUM(time_entries.hours)"
		}
	case KindTimeEntry:
		if c.Name == "hours" {
			agg = "SUM(time_entries.hours)"
		}
	}
	if agg == "" {
		err = fmt.Errorf("query: column %s is not totalable", c.Name)
	}
	return
}

func (q *Query) totalColumn(ctx context.Context, name string) (*Column, error) {
	cols, err := q.AvailableTotalableColumns(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("query: unknown totalable column %q", name)
}

// TotalFor は total_for(column) (列名で指定)。
func (q *Query) TotalFor(ctx context.Context, name string) (float64, error) {
	c, err := q.totalColumn(ctx, name)
	if err != nil {
		return 0, err
	}
	agg, joins, where, cast, err := q.totalSpec(ctx, c)
	if err != nil {
		return 0, err
	}
	s, args, err := q.baseSQLFor(ctx, joins, &ListOptions{Conditions: where}, agg)
	if err != nil {
		return 0, err
	}
	var v any
	if err := q.env.Q.QueryRow(ctx, "SELECT "+agg+s, args...).Scan(&v); err != nil {
		return 0, stmtErr(err)
	}
	return cast(toFloat(v)), nil
}

// TotalByGroupFor は total_by_group_for(column) (グループ化していなければ nil)。
func (q *Query) TotalByGroupFor(ctx context.Context, name string) (map[GroupKey]float64, error) {
	c, err := q.totalColumn(ctx, name)
	if err != nil {
		return nil, err
	}
	agg, joins, where, cast, err := q.totalSpec(ctx, c)
	if err != nil {
		return nil, err
	}
	m, err := q.groupedRows(ctx, agg, joins, where)
	if err != nil || m == nil {
		return nil, err
	}
	for k, v := range m {
		m[k] = cast(v)
	}
	return m, nil
}

// Total は合計列とその合計。
type Total struct {
	Column *Column
	Value  float64
}

// Totals は totals (totalable_columns ごとの total_for)。
func (q *Query) Totals(ctx context.Context) ([]Total, error) {
	cols, err := q.TotalableColumns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Total, 0, len(cols))
	for _, c := range cols {
		v, err := q.TotalFor(ctx, c.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, Total{Column: c, Value: v})
	}
	return out, nil
}

// GroupTotal は合計列とグループ別の合計。
type GroupTotal struct {
	Column  *Column
	ByGroup map[GroupKey]float64
}

// TotalsByGroup は totals_by_group。
func (q *Query) TotalsByGroup(ctx context.Context) ([]GroupTotal, error) {
	cols, err := q.TotalableColumns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GroupTotal, 0, len(cols))
	for _, c := range cols {
		m, err := q.TotalByGroupFor(ctx, c.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, GroupTotal{Column: c, ByGroup: m})
	}
	return out, nil
}

// ---------------------------------------------------------------- チケットの行

// IssueRow は一覧に表示するチケットの属性と、列に応じて読み込む値。
type IssueRow struct {
	ID             int64
	ProjectID      int64
	TrackerID      int64
	StatusID       int64
	PriorityID     int64
	AuthorID       int64
	AssignedToID   *int64
	CategoryID     *int64
	FixedVersionID *int64
	ParentID       *int64
	RootID         int64
	HierPath       string
	Subject        string
	Description    string
	StartDate      *time.Time
	DueDate        *time.Time
	DoneRatio      int
	EstimatedHours *float64
	IsPrivate      bool
	LockVersion    int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClosedAt       *time.Time

	// SpentHours は spent_hours 列があるとき (Issue.load_visible_spent_hours)。
	SpentHours *float64
	// TotalSpentHours は total_spent_hours 列があるとき。
	TotalSpentHours *float64
	// LastUpdatedByID は last_updated_by 列があるとき (可視な最後のジャーナルの作成者。無ければ nil)。
	LastUpdatedByID *int64
	// LastNotes は last_notes 列があるとき (可視な最後の注記。無ければ "")。
	LastNotes *string
	// RelationIDs は relations 列があるとき (相手が可視な関連の id)。
	RelationIDs []int64
	// CustomValues は CF 列があるとき (custom_field_id → 値。複数値は複数要素)。
	CustomValues map[int64][]string
	// WatcherIDs は watcher_users 列があるとき。
	WatcherIDs []int64
}

type issueRowDB struct {
	ID             int64       `db:"id"`
	ProjectID      int64       `db:"project_id"`
	TrackerID      int64       `db:"tracker_id"`
	StatusID       int64       `db:"status_id"`
	PriorityID     int64       `db:"priority_id"`
	AuthorID       int64       `db:"author_id"`
	AssignedToID   *int64      `db:"assigned_to_id"`
	CategoryID     *int64      `db:"category_id"`
	FixedVersionID *int64      `db:"fixed_version_id"`
	ParentID       *int64      `db:"parent_id"`
	RootID         int64       `db:"root_id"`
	HierPath       string      `db:"hier_path"`
	Subject        string      `db:"subject"`
	Description    *string     `db:"description"`
	StartDate      db.NullDate `db:"start_date"`
	DueDate        db.NullDate `db:"due_date"`
	DoneRatio      int         `db:"done_ratio"`
	EstimatedHours *float64    `db:"estimated_hours"`
	IsPrivate      bool        `db:"is_private"`
	LockVersion    int         `db:"lock_version"`
	CreatedAt      db.Time     `db:"created_at"`
	UpdatedAt      db.Time     `db:"updated_at"`
	ClosedAt       db.NullTime `db:"closed_at"`
}

func dateOrNil(d db.NullDate) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Date.Time
	return &t
}

// Issues は IssueQuery#issues (並び順どおりの行。列に応じて関連値を読み込む)。
func (q *Query) Issues(ctx context.Context, opts ListOptions) ([]*IssueRow, error) {
	ids, err := q.IDs(ctx, opts)
	if err != nil {
		return nil, err
	}
	rows, err := q.loadIssueRows(ctx, ids)
	if err != nil {
		return nil, err
	}
	if err := q.preloadIssueColumns(ctx, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (q *Query) loadIssueRows(ctx context.Context, ids []int64) ([]*IssueRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	byID := map[int64]*IssueRow{}
	for _, chunk := range chunk(ids, 500) {
		var rs []issueRowDB
		if err := q.env.Q.Select(ctx, &rs, `SELECT id, project_id, tracker_id, status_id, priority_id, author_id, assigned_to_id, category_id,
  fixed_version_id, parent_id, root_id, hier_path, subject, description, start_date, due_date, done_ratio, estimated_hours,
  is_private, lock_version, created_at, updated_at, closed_at FROM issues WHERE id IN (`+idList(chunk)+`)`); err != nil {
			return nil, err
		}
		for _, r := range rs {
			row := &IssueRow{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID, StatusID: r.StatusID, PriorityID: r.PriorityID,
				AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, CategoryID: r.CategoryID, FixedVersionID: r.FixedVersionID,
				ParentID: r.ParentID, RootID: r.RootID, HierPath: r.HierPath, Subject: r.Subject,
				StartDate: dateOrNil(r.StartDate), DueDate: dateOrNil(r.DueDate), DoneRatio: r.DoneRatio,
				EstimatedHours: r.EstimatedHours, IsPrivate: r.IsPrivate, LockVersion: r.LockVersion,
				CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, ClosedAt: r.ClosedAt.Ptr()}
			if r.Description != nil {
				row.Description = *r.Description
			}
			byID[r.ID] = row
		}
	}
	out := make([]*IssueRow, 0, len(ids))
	for _, id := range ids {
		if r := byID[id]; r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func chunk(ids []int64, n int) [][]int64 {
	var out [][]int64
	for len(ids) > n {
		out = append(out, ids[:n])
		ids = ids[n:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

// preloadIssueColumns は Issue.load_visible_* と custom_values / watcher_users の preload。
func (q *Query) preloadIssueColumns(ctx context.Context, rows []*IssueRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	byID := map[int64]*IssueRow{}
	for i, r := range rows {
		ids[i] = r.ID
		byID[r.ID] = r
	}
	in := idList(ids)
	has := func(name string) bool { ok, _ := q.HasColumn(ctx, name); return ok }
	teVis := ""
	if has("spent_hours") || has("total_spent_hours") {
		v, err := q.timeEntryVisibleCondition(ctx)
		if err != nil {
			return err
		}
		teVis = v
	}
	type idSum struct {
		ID  int64   `db:"id"`
		Sum float64 `db:"total"`
	}
	if has("spent_hours") {
		var sums []idSum
		if err := q.env.Q.Select(ctx, &sums, `SELECT time_entries.issue_id AS id, SUM(time_entries.hours) AS total FROM time_entries
JOIN projects ON projects.id = time_entries.project_id WHERE (`+teVis+`) AND time_entries.issue_id IN (`+in+`) GROUP BY time_entries.issue_id`); err != nil {
			return err
		}
		m := map[int64]float64{}
		for _, s := range sums {
			m[s.ID] = s.Sum
		}
		for _, r := range rows {
			v := m[r.ID]
			r.SpentHours = &v
		}
	}
	if has("total_spent_hours") {
		var sums []idSum
		if err := q.env.Q.Select(ctx, &sums, `SELECT parent.id AS id, SUM(time_entries.hours) AS total FROM time_entries
JOIN projects ON projects.id = time_entries.project_id JOIN issues ON issues.id = time_entries.issue_id
JOIN issues parent ON parent.root_id = issues.root_id AND issues.hier_path LIKE (parent.hier_path || '%')
WHERE (`+teVis+`) AND parent.id IN (`+in+`) GROUP BY parent.id`); err != nil {
			return err
		}
		m := map[int64]float64{}
		for _, s := range sums {
			m[s.ID] = s.Sum
		}
		for _, r := range rows {
			v := m[r.ID]
			r.TotalSpentHours = &v
		}
	}
	if has("last_updated_by") || has("last_notes") {
		notes, err := q.visibleNotesCondition(ctx, "issue_journals")
		if err != nil {
			return err
		}
		if has("last_updated_by") {
			var js []struct {
				IssueID int64 `db:"issue_id"`
				UserID  int64 `db:"user_id"`
			}
			if err := q.env.Q.Select(ctx, &js, `SELECT issue_id, user_id FROM issue_journals WHERE id IN (SELECT MAX(issue_journals.id) FROM issue_journals
JOIN issues ON issues.id = issue_journals.issue_id JOIN projects ON projects.id = issues.project_id
WHERE issue_journals.issue_id IN (`+in+`) AND `+notes+` GROUP BY issue_journals.issue_id)`); err != nil {
				return err
			}
			for _, j := range js {
				uid := j.UserID
				byID[j.IssueID].LastUpdatedByID = &uid
			}
		}
		if has("last_notes") {
			var js []struct {
				IssueID int64  `db:"issue_id"`
				Notes   string `db:"notes"`
			}
			if err := q.env.Q.Select(ctx, &js, `SELECT issue_id, COALESCE(notes, '') AS notes FROM issue_journals WHERE id IN (SELECT MAX(issue_journals.id) FROM issue_journals
JOIN issues ON issues.id = issue_journals.issue_id JOIN projects ON projects.id = issues.project_id
WHERE issue_journals.issue_id IN (`+in+`) AND `+notes+` AND issue_journals.notes <> '' GROUP BY issue_journals.issue_id)`); err != nil {
				return err
			}
			empty := ""
			for _, r := range rows {
				r.LastNotes = &empty
			}
			for _, j := range js {
				n := j.Notes
				byID[j.IssueID].LastNotes = &n
			}
		}
	}
	if has("relations") {
		vis, err := q.env.Auth.IssueVisibleCondition(ctx, authzOpts())
		if err != nil {
			return err
		}
		var rels []struct {
			ID   int64 `db:"id"`
			From int64 `db:"issue_from_id"`
			To   int64 `db:"issue_to_id"`
		}
		if err := q.env.Q.Select(ctx, &rels, `SELECT issue_relations.id, issue_relations.issue_from_id, issue_relations.issue_to_id FROM issue_relations
JOIN issues ON issues.id = issue_relations.issue_to_id JOIN projects ON projects.id = issues.project_id
WHERE (`+vis+`) AND issue_relations.issue_from_id IN (`+in+`)
UNION SELECT issue_relations.id, issue_relations.issue_from_id, issue_relations.issue_to_id FROM issue_relations
JOIN issues ON issues.id = issue_relations.issue_from_id JOIN projects ON projects.id = issues.project_id
WHERE (`+vis+`) AND issue_relations.issue_to_id IN (`+in+`) ORDER BY 1`); err != nil {
			return err
		}
		for _, r := range rows {
			r.RelationIDs = []int64{}
		}
		for _, rel := range rels {
			if r := byID[rel.From]; r != nil && !slices.Contains(r.RelationIDs, rel.ID) {
				r.RelationIDs = append(r.RelationIDs, rel.ID)
			}
			if r := byID[rel.To]; r != nil && !slices.Contains(r.RelationIDs, rel.ID) {
				r.RelationIDs = append(r.RelationIDs, rel.ID)
			}
		}
	}
	if ok, _ := q.HasCustomFieldColumn(ctx); ok {
		var cvs []struct {
			IssueID int64   `db:"customized_id"`
			CFID    int64   `db:"custom_field_id"`
			Value   *string `db:"value"`
		}
		if err := q.env.Q.Select(ctx, &cvs, `SELECT customized_id, custom_field_id, value FROM custom_values
WHERE customized_kind = 'issue' AND customized_id IN (`+in+`) ORDER BY id`); err != nil {
			return err
		}
		for _, r := range rows {
			r.CustomValues = map[int64][]string{}
		}
		for _, cv := range cvs {
			v := ""
			if cv.Value != nil {
				v = *cv.Value
			}
			r := byID[cv.IssueID]
			r.CustomValues[cv.CFID] = append(r.CustomValues[cv.CFID], v)
		}
	}
	if has("watcher_users") {
		var ws []struct {
			IssueID     int64 `db:"watchable_id"`
			PrincipalID int64 `db:"principal_id"`
		}
		if err := q.env.Q.Select(ctx, &ws, `SELECT watchable_id, principal_id FROM watchers WHERE watchable_kind = 'issue' AND watchable_id IN (`+in+`) ORDER BY id`); err != nil {
			return err
		}
		for _, w := range ws {
			r := byID[w.IssueID]
			r.WatcherIDs = append(r.WatcherIDs, w.PrincipalID)
		}
	}
	return nil
}
