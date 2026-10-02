package query

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
)

// ColumnKind は QueryColumn のサブクラス。
type ColumnKind int

const (
	ColumnPlain       ColumnKind = iota // QueryColumn
	ColumnTimestamp                     // TimestampQueryColumn
	ColumnWatcher                       // WatcherQueryColumn
	ColumnAssociation                   // QueryAssociationColumn ("parent.subject" 等)
	ColumnCustomField                   // QueryCustomFieldColumn ("cf_1")
	ColumnAssocCF                       // QueryAssociationCustomFieldColumn ("issue.cf_1")
)

// Column は QueryColumn。
type Column struct {
	Name string
	Kind ColumnKind
	// Sortable はソート用の SQL 式 (複数可。nil ならソート不可)。
	Sortable []string
	// Groupable はグループ化できる。
	Groupable bool
	// GroupSQL はグループ化の SQL 式 (group_by_statement)。関連列は外部キー列。
	GroupSQL string
	// GroupAssociation はグループのキーがレコード (関連) なら true (Rails は外部キーでグループ化する)。
	GroupAssociation bool
	// Totalable は合計できる。
	Totalable bool
	// DefaultOrder は既定の並び ("asc" / "desc" / "")。
	DefaultOrder string
	// Inline は表の列として表示する (false はブロック列: description, last_notes, 全幅 CF)。
	Inline bool
	// Frozen は常に表示する列 (id)。
	Frozen bool
	// CaptionKey は見出しの i18n キー (Caption が空の場合)。
	CaptionKey string
	// Caption は見出し (カスタムフィールド名など翻訳しないもの)。
	Caption string
	// Association は QueryAssociationColumn / 関連 CF 列の関連名 ("parent", "issue", "project" ...)。
	Association string
	// Attribute は QueryAssociationColumn の属性名。
	Attribute string
	// CustomField は CF 列のカスタムフィールド。
	CustomField *customfield.CustomField
}

// CaptionText は caption (翻訳済み見出し)。
func (c *Column) CaptionText(e *Env) string {
	if c.Caption != "" {
		return c.Caption
	}
	key := c.CaptionKey
	if key == "" {
		key = "field_" + c.Name
	}
	return e.l(key)
}

// IsSortable は sortable?。
func (c *Column) IsSortable() bool { return len(c.Sortable) > 0 }

// CSSClasses は css_classes。
func (c *Column) CSSClasses() string {
	switch c.Kind {
	case ColumnAssociation:
		return c.Association + "-" + c.Attribute
	case ColumnCustomField:
		return c.Name + " " + c.CustomField.FieldFormat
	case ColumnAssocCF:
		return c.Association + "_cf_" + itoa(c.CustomField.ID) + " " + c.CustomField.FieldFormat
	}
	return c.Name
}

// colOpt は QueryColumn.new のオプション。
type colOpt struct {
	sortable     []string
	groupable    bool
	groupSQL     string
	groupAssoc   bool
	totalable    bool
	defaultOrder string
	notInline    bool
	frozen       bool
	caption      string
}

func newColumn(name string, kind ColumnKind, o colOpt) *Column {
	c := &Column{Name: name, Kind: kind, Sortable: o.sortable, Groupable: o.groupable, GroupSQL: o.groupSQL,
		GroupAssociation: o.groupAssoc, Totalable: o.totalable, DefaultOrder: o.defaultOrder,
		Inline: !o.notInline, Frozen: o.frozen, CaptionKey: o.caption}
	if c.Groupable && c.GroupSQL == "" {
		c.GroupSQL = name
	}
	return c
}

// newCustomFieldColumn は QueryCustomFieldColumn.new(cf, totalable)。
func (q *Query) newCustomFieldColumn(cf *customfield.CustomField, totalable *bool) *Column {
	t := cf.Totalable()
	if totalable != nil {
		t = *totalable
	}
	c := &Column{Name: "cf_" + itoa(cf.ID), Kind: ColumnCustomField, Sortable: cf.OrderStatement(q.env.setting("user_format")),
		Totalable: t, Inline: !cf.FullWidthLayout(), Caption: cf.Name, CustomField: cf}
	if g := cf.GroupStatement(); g != "" {
		c.Groupable, c.GroupSQL = true, g
	}
	return c
}

// newAssocCustomFieldColumn は QueryAssociationCustomFieldColumn.new(assoc, cf, totalable)。
func (q *Query) newAssocCustomFieldColumn(assoc string, cf *customfield.CustomField, totalable *bool) *Column {
	c := q.newCustomFieldColumn(cf, totalable)
	c.Name = assoc + ".cf_" + itoa(cf.ID)
	c.Kind = ColumnAssocCF
	c.Sortable, c.Groupable, c.GroupSQL = nil, false, ""
	c.Association = assoc
	return c
}

// timestampToDate は Redmine::Database.timestamp_to_date(column, User.current.time_zone)。
// SQLite では Redmine は nil (グループ化不可) だが、buropher は UTC 固定長文字列を
// ユーザのタイムゾーン (未設定なら UTC) の日付に変換する関数で対応する。
func (q *Query) timestampToDate(column string) string {
	tz := q.env.userLoc().String()
	if tz == "" || tz == "Local" {
		tz = "UTC"
	}
	return tsDateSQL(q.env.dialect().Name(), column, tz)
}

// timestampGroupable は TimestampQueryColumn#groupable?（Redmine::Database.timestamp_to_date が
// SQLite では nil を返すため、SQLite では日時の列でグループ化できない）。
func (q *Query) timestampGroupable() bool { return q.env.dialect().Name() != db.SQLite }

// AvailableColumns は available_columns。
func (q *Query) AvailableColumns(ctx context.Context) ([]*Column, error) {
	if q.availableCols == nil {
		cols, err := q.impl.availableColumns(ctx, q)
		if err != nil {
			return nil, err
		}
		q.availableCols = cols
	}
	return q.availableCols, nil
}

func (q *Query) findColumn(ctx context.Context, name string) (*Column, error) {
	cols, err := q.AvailableColumns(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, nil
}

// Columns は columns: 常に表示する列 + (既定列または column_names) の順。
func (q *Query) Columns(ctx context.Context) ([]*Column, error) {
	avail, err := q.AvailableColumns(ctx)
	if err != nil || len(avail) == 0 {
		return nil, err
	}
	names := q.columnNames
	if q.HasDefaultColumns() {
		names = q.DefaultColumnNames()
	}
	var out []*Column
	for _, c := range avail {
		if c.Frozen {
			out = append(out, c)
		}
	}
	for _, n := range names {
		for _, c := range avail {
			if c.Name == n && !slices.Contains(out, c) {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

// InlineColumns は inline_columns。
func (q *Query) InlineColumns(ctx context.Context) ([]*Column, error) {
	cols, err := q.Columns(ctx)
	return filterCols(cols, func(c *Column) bool { return c.Inline }), err
}

// BlockColumns は block_columns。
func (q *Query) BlockColumns(ctx context.Context) ([]*Column, error) {
	cols, err := q.Columns(ctx)
	return filterCols(cols, func(c *Column) bool { return !c.Inline }), err
}

// AvailableInlineColumns は available_inline_columns。
func (q *Query) AvailableInlineColumns(ctx context.Context) ([]*Column, error) {
	cols, err := q.AvailableColumns(ctx)
	return filterCols(cols, func(c *Column) bool { return c.Inline }), err
}

// AvailableBlockColumns は available_block_columns。
func (q *Query) AvailableBlockColumns(ctx context.Context) ([]*Column, error) {
	cols, err := q.AvailableColumns(ctx)
	return filterCols(cols, func(c *Column) bool { return !c.Inline }), err
}

// AvailableTotalableColumns は available_totalable_columns。
func (q *Query) AvailableTotalableColumns(ctx context.Context) ([]*Column, error) {
	cols, err := q.AvailableColumns(ctx)
	return filterCols(cols, func(c *Column) bool { return c.Totalable }), err
}

// GroupableColumns は groupable_columns。
func (q *Query) GroupableColumns(ctx context.Context) ([]*Column, error) {
	cols, err := q.AvailableColumns(ctx)
	return filterCols(cols, func(c *Column) bool { return c.Groupable }), err
}

// TotalableColumns は totalable_columns。
func (q *Query) TotalableColumns(ctx context.Context) ([]*Column, error) {
	cols, err := q.AvailableTotalableColumns(ctx)
	names := q.TotalableNames()
	return filterCols(cols, func(c *Column) bool { return slices.Contains(names, c.Name) }), err
}

// HasColumn は has_column?(name)。
func (q *Query) HasColumn(ctx context.Context, name string) (bool, error) {
	cols, err := q.Columns(ctx)
	return slices.ContainsFunc(cols, func(c *Column) bool { return c.Name == name }), err
}

// HasCustomFieldColumn は has_custom_field_column?。
func (q *Query) HasCustomFieldColumn(ctx context.Context) (bool, error) {
	cols, err := q.Columns(ctx)
	return slices.ContainsFunc(cols, func(c *Column) bool { return c.CustomField != nil }), err
}

func filterCols(cols []*Column, keep func(*Column) bool) []*Column {
	var out []*Column
	for _, c := range cols {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// SortableColumns は sortable_columns (列名 → ソート式)。
func (q *Query) SortableColumns(ctx context.Context) (map[string][]string, error) {
	cols, err := q.AvailableColumns(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, c := range cols {
		if c.IsSortable() {
			out[c.Name] = c.Sortable
		}
	}
	return out, nil
}

// SortClause は sort_clause (ORDER BY の各項目)。
func (q *Query) SortClause(ctx context.Context) ([]string, error) {
	s, err := q.SortableColumns(ctx)
	if err != nil {
		return nil, err
	}
	return q.SortCriteria().sortClause(s), nil
}

// GroupByColumn は group_by_column (nil = グループ化しない)。
func (q *Query) GroupByColumn(ctx context.Context) (*Column, error) {
	if q.GroupBy == "" {
		return nil, nil
	}
	cols, err := q.GroupableColumns(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		if c.Name == q.GroupBy {
			return c, nil
		}
	}
	return nil, nil
}

// Grouped は grouped?。
func (q *Query) Grouped(ctx context.Context) (bool, error) {
	c, err := q.GroupByColumn(ctx)
	return c != nil, err
}

// groupByStatement は group_by_statement (SQL 式)。
func (q *Query) groupByStatement(ctx context.Context) (string, error) {
	c, err := q.GroupByColumn(ctx)
	if err != nil || c == nil {
		return "", err
	}
	return c.GroupSQL, nil
}

// GroupBySortOrder は group_by_sort_order (グループ列のソートを先頭に付ける ORDER BY 項目)。
func (q *Query) GroupBySortOrder(ctx context.Context) ([]string, error) {
	c, err := q.GroupByColumn(ctx)
	if err != nil || c == nil {
		return nil, err
	}
	order := q.SortCriteria().OrderFor(c.Name)
	if order == "" {
		order = c.DefaultOrder
	}
	if order == "" {
		order = "asc"
	}
	order = strings.ToUpper(order)
	sortable := c.Sortable
	if c.Kind == ColumnTimestamp && len(sortable) > 0 {
		sortable = []string{q.timestampToDate(sortable[0])}
	}
	out := make([]string, len(sortable))
	for i, s := range sortable {
		out[i] = s + " " + order
	}
	return out, nil
}

var reCFName = regexp.MustCompile(`cf_\d+`)

// cfJoinsForOrder は Query#joins_for_order_statement (CF 列のソート・グループ用 JOIN)。
func (q *Query) cfJoinsForOrder(ctx context.Context, order string) ([]string, error) {
	var joins []string
	if order == "" {
		return nil, nil
	}
	var seen []string
	for _, name := range reCFName.FindAllString(order, -1) {
		if slices.Contains(seen, name) {
			continue
		}
		seen = append(seen, name)
		c, err := q.findColumn(ctx, name)
		if err != nil {
			return nil, err
		}
		if c == nil || c.CustomField == nil {
			continue
		}
		vis, err := customfield.VisibilityByProjectCondition(ctx, q.env.Auth, c.CustomField, "", "")
		if err != nil {
			return nil, err
		}
		joins = append(joins, c.CustomField.JoinForOrderStatement(vis))
	}
	return joins, nil
}
