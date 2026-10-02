// Package customfield は Redmine のカスタムフィールド (app/models/custom_field.rb とサブクラス,
// lib/redmine/field_format.rb) のうち、クエリシステムが必要とする部分を移植する。
//
// 値の書式 (Format)、ソート・グループ用の SQL 断片 (order_statement / group_statement /
// join_for_order_statement)、可視性 (CustomField.visible スコープ, visibility_by_project_condition,
// visible_by?) と読み込みを提供する。
package customfield

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// OwnerKind は custom_fields.owner_kind (旧 custom_fields.type の STI)。
type OwnerKind string

const (
	KindIssue             OwnerKind = "issue"               // IssueCustomField
	KindProject           OwnerKind = "project"             // ProjectCustomField
	KindTimeEntry         OwnerKind = "time_entry"          // TimeEntryCustomField
	KindVersion           OwnerKind = "version"             // VersionCustomField
	KindDocument          OwnerKind = "document"            // DocumentCustomField
	KindUser              OwnerKind = "user"                // UserCustomField
	KindGroup             OwnerKind = "group"               // GroupCustomField
	KindIssuePriority     OwnerKind = "issue_priority"      // IssuePriorityCustomField
	KindTimeEntryActivity OwnerKind = "time_entry_activity" // TimeEntryActivityCustomField
	KindDocumentCategory  OwnerKind = "document_category"   // DocumentCategoryCustomField
)

// CustomizedTable は customized_class.table_name (新スキーマのテーブル名)。
func (k OwnerKind) CustomizedTable() string {
	switch k {
	case KindIssue:
		return "issues"
	case KindProject:
		return "projects"
	case KindTimeEntry:
		return "time_entries"
	case KindVersion:
		return "versions"
	case KindDocument:
		return "documents"
	case KindUser, KindGroup:
		return "principals"
	case KindIssuePriority:
		return "issue_priorities"
	case KindTimeEntryActivity:
		return "time_entry_activities"
	case KindDocumentCategory:
		return "document_categories"
	}
	return ""
}

// CustomizedKind は custom_values.customized_kind (旧 customized_type = base_class.name)。
func (k OwnerKind) CustomizedKind() string {
	switch k {
	case KindIssue, KindProject, KindTimeEntry, KindVersion, KindDocument:
		return string(k)
	case KindUser, KindGroup:
		return "principal"
	case KindIssuePriority, KindTimeEntryActivity, KindDocumentCategory:
		return "enumeration"
	}
	return ""
}

// CustomField は custom_fields 行と関連 (roles / trackers / projects)。
type CustomField struct {
	ID             int64
	OwnerKind      OwnerKind
	Name           string
	Description    string
	FieldFormat    string
	Regexp         string
	MinLength      *int
	MaxLength      *int
	IsRequired     bool
	IsForAll       bool
	IsFilter       bool
	Searchable     bool
	DefaultValue   string
	Editable       bool
	Visible        bool
	Multiple       bool
	Position       int
	PossibleValues []string
	// Settings は format_store (url_pattern, text_formatting, edit_tag_style, user_role, version_status, ...)。
	Settings map[string]any

	// RoleIDs は custom_fields_roles (visible = false のときに閲覧できるロール)。
	RoleIDs []int64
	// TrackerIDs / ProjectIDs は IssueCustomField の custom_fields_trackers / custom_fields_projects。
	TrackerIDs []int64
	ProjectIDs []int64
}

// Format は custom_field.format。
func (cf *CustomField) Format() *Format { return FindFormat(cf.FieldFormat) }

// Setting は format_store の文字列値。
func (cf *CustomField) Setting(name string) string {
	if cf.Settings == nil {
		return ""
	}
	switch v := cf.Settings[name].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// SettingList は format_store の配列値 (user_role / version_status)。配列でなければ nil。
func (cf *CustomField) SettingList(name string) []string {
	a, ok := cf.Settings[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(a))
	for _, v := range a {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// Totalable は totalable? (書式が合計をサポートする)。
func (cf *CustomField) Totalable() bool { return cf.Format().TotalableSupported }

// FullWidthLayout は full_width_layout? (format_store の full_width_layout が '1')。
func (cf *CustomField) FullWidthLayout() bool { return cf.Setting("full_width_layout") == "1" }

// CastValue は cast_value: 空なら nil、それ以外は書式ごとの変換。
func (cf *CustomField) CastValue(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return cf.Format().CastSingleValue(*v)
}

// OrderStatement は CustomField#order_statement。複数値やソート不可なら nil。
// userFormat は Setting.user_format (user 書式のソート列に使う)。
func (cf *CustomField) OrderStatement(userFormat string) []string {
	if cf.Multiple {
		return nil
	}
	f := cf.Format()
	alias := JoinAlias(cf)
	switch {
	case f.numeric:
		return []string{"CAST(CASE " + alias + ".value WHEN '' THEN '0' ELSE " + alias + ".value END AS decimal(30,3))"}
	case f.Target == "user":
		return UserOrderFields(valueJoinAlias(cf), userFormat)
	case f.Target == "version":
		return VersionOrderFields(valueJoinAlias(cf))
	case f.Target == "enumeration":
		// CustomFieldEnumeration は fields_for_order_statement を持たない
		return nil
	}
	return []string{"COALESCE(" + alias + ".value, '')"}
}

// GroupStatement は CustomField#group_statement。グループ化できなければ ""。
func (cf *CustomField) GroupStatement() string {
	if cf.Multiple {
		return ""
	}
	f := cf.Format()
	if !f.groupable {
		return ""
	}
	if f.IsRecordList() {
		return "COALESCE(" + JoinAlias(cf) + ".value, '')"
	}
	return strings.Join(cf.OrderStatement(""), ", ")
}

// JoinForOrderStatement は CustomField#join_for_order_statement。
// visibility は visibility_by_project_condition の SQL。
func (cf *CustomField) JoinForOrderStatement(visibility string) string {
	alias := JoinAlias(cf)
	table := cf.OwnerKind.CustomizedTable()
	id := strconv.FormatInt(cf.ID, 10)
	s := "LEFT OUTER JOIN custom_values " + alias +
		" ON " + alias + ".customized_kind = '" + cf.OwnerKind.CustomizedKind() + "'" +
		" AND " + alias + ".customized_id = " + table + ".id" +
		" AND " + alias + ".custom_field_id = " + id +
		" AND (" + visibility + ")" +
		" AND " + alias + ".value <> ''" +
		" AND " + alias + ".id = (SELECT max(" + alias + "_2.id) FROM custom_values " + alias + "_2" +
		" WHERE " + alias + "_2.customized_kind = " + alias + ".customized_kind" +
		" AND " + alias + "_2.customized_id = " + alias + ".customized_id" +
		" AND " + alias + "_2.custom_field_id = " + alias + ".custom_field_id)"
	if f := cf.Format(); f.IsRecordList() {
		va := valueJoinAlias(cf)
		s += " LEFT OUTER JOIN " + f.TargetTable() + " " + va +
			" ON CAST(CASE " + alias + ".value WHEN '' THEN '0' ELSE " + alias + ".value END AS decimal(30,0)) = " + va + ".id"
	}
	return s
}

// UserOrderFields は User.fields_for_order_statement(table) (Setting.user_format の :order)。
// 新スキーマではグループ名が principals.name にあるため、旧 users.lastname は
// lastname || name (どちらか一方は常に空) で表す。login は user_accounts から引く。
func UserOrderFields(table, userFormat string) []string {
	var fields []string
	switch userFormat {
	case "firstname":
		fields = []string{"firstname", "id"}
	case "lastname_firstname", "lastnamefirstname", "lastname_comma_firstname":
		fields = []string{"lastname", "firstname", "id"}
	case "lastname":
		fields = []string{"lastname", "id"}
	case "username":
		fields = []string{"login", "id"}
	default:
		fields = []string{"firstname", "lastname", "id"}
	}
	out := make([]string, len(fields))
	for i, f := range fields {
		switch f {
		case "lastname":
			out[i] = "(" + table + ".lastname || " + table + ".name)"
		case "login":
			out[i] = "COALESCE((SELECT ua.login FROM user_accounts ua WHERE ua.principal_id = " + table + ".id), '')"
		default:
			out[i] = table + "." + f
		}
	}
	return out
}

// VersionOrderFields は Version.fields_for_order_statement(table)。
func VersionOrderFields(table string) []string {
	return []string{
		"(CASE WHEN " + table + ".effective_date IS NULL THEN 1 ELSE 0 END)",
		table + ".effective_date", table + ".name", table + ".id",
	}
}

// ---------------------------------------------------------------- 可視性

// VisibleCondition は CustomField.visible(user) スコープの WHERE 句断片 (custom_fields テーブル参照)。
func VisibleCondition(ctx context.Context, a *authz.Authorizer) (string, error) {
	u := a.User()
	if u.IsAdmin() {
		return "1=1", nil
	}
	pids, err := a.ProjectIDs(ctx)
	if err != nil {
		return "", err
	}
	t := a.Dialect().BoolLiteral(true)
	if len(pids) > 0 {
		return "(custom_fields.visible = " + t + " OR custom_fields.id IN (SELECT DISTINCT cfr.custom_field_id FROM members m" +
			" INNER JOIN member_roles mr ON mr.member_id = m.id" +
			" INNER JOIN custom_fields_roles cfr ON cfr.role_id = mr.role_id" +
			" WHERE m.principal_id = " + strconv.FormatInt(u.ID, 10) + "))", nil
	}
	return "custom_fields.visible = " + t, nil
}

// VisibilityByProjectCondition は CustomField#visibility_by_project_condition(project_key, user, id_column)
// (IssueCustomField / ProjectCustomField の上書きを含む)。projectKey / idColumn が空なら既定値。
func VisibilityByProjectCondition(ctx context.Context, a *authz.Authorizer, cf *CustomField, projectKey, idColumn string) (string, error) {
	u := a.User()
	if idColumn == "" {
		idColumn = strconv.FormatInt(cf.ID, 10)
	}
	if cf.OwnerKind == KindProject && projectKey == "" {
		projectKey = "projects.id"
	}
	var sql string
	switch {
	case cf.Visible || u.IsAdmin():
		sql = "1=1"
	case u.Anonymous():
		sql = "1=0"
	default:
		if projectKey == "" {
			projectKey = cf.OwnerKind.CustomizedTable() + ".project_id"
		}
		sql = projectKey + " IN (SELECT DISTINCT m.project_id FROM members m" +
			" INNER JOIN member_roles mr ON mr.member_id = m.id" +
			" INNER JOIN custom_fields_roles cfr ON cfr.role_id = mr.role_id" +
			" WHERE m.principal_id = " + strconv.FormatInt(u.ID, 10) + " AND cfr.custom_field_id = " + idColumn + ")"
	}
	if cf.OwnerKind != KindIssue {
		return sql, nil
	}
	vis, err := a.IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return "", err
	}
	tracker := "issues.tracker_id IN (SELECT tracker_id FROM custom_fields_trackers WHERE custom_field_id = " + idColumn + ")"
	project := "EXISTS (SELECT 1 FROM custom_fields ifa WHERE ifa.is_for_all = " + a.Dialect().BoolLiteral(true) + " AND ifa.id = " + idColumn + ")" +
		" OR issues.project_id IN (SELECT project_id FROM custom_fields_projects WHERE custom_field_id = " + idColumn + ")"
	return "((" + sql + ") AND (" + tracker + ") AND (" + project + ") AND (" + vis + "))", nil
}

// VisibleBy は CustomField#visible_by?(project, user)
// (Issue / Project / TimeEntry / Version 用はロールの積集合も見る)。
func VisibleBy(ctx context.Context, a *authz.Authorizer, cf *CustomField, p *domain.Project) (bool, error) {
	if cf.Visible || a.User().IsAdmin() {
		return true, nil
	}
	switch cf.OwnerKind {
	case KindIssue, KindProject, KindTimeEntry, KindVersion:
	default:
		return false, nil
	}
	roles, err := a.RolesForProject(ctx, p)
	if err != nil {
		return false, err
	}
	for _, r := range roles {
		if slices.Contains(cf.RoleIDs, r.ID) {
			return true, nil
		}
	}
	return false, nil
}

// ---------------------------------------------------------------- 読み込み

type cfRow struct {
	ID             int64          `db:"id"`
	OwnerKind      string         `db:"owner_kind"`
	Name           string         `db:"name"`
	Description    sql.NullString `db:"description"`
	FieldFormat    string         `db:"field_format"`
	Regexp         sql.NullString `db:"regexp"`
	MinLength      sql.NullInt64  `db:"min_length"`
	MaxLength      sql.NullInt64  `db:"max_length"`
	IsRequired     bool           `db:"is_required"`
	IsForAll       bool           `db:"is_for_all"`
	IsFilter       bool           `db:"is_filter"`
	Searchable     bool           `db:"searchable"`
	DefaultValue   sql.NullString `db:"default_value"`
	Editable       bool           `db:"editable"`
	Visible        bool           `db:"visible"`
	Multiple       bool           `db:"multiple"`
	Position       int            `db:"position"`
	PossibleValues db.RawJSON     `db:"possible_values"`
	FormatSettings db.RawJSON     `db:"format_settings"`
}

const cfCols = `custom_fields.id, custom_fields.owner_kind, custom_fields.name, custom_fields.description,
  custom_fields.field_format, custom_fields.regexp, custom_fields.min_length, custom_fields.max_length,
  custom_fields.is_required, custom_fields.is_for_all, custom_fields.is_filter, custom_fields.searchable,
  custom_fields.default_value, custom_fields.editable, custom_fields.visible, custom_fields.multiple,
  custom_fields.position, custom_fields.possible_values, custom_fields.format_settings`

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// Load は where 条件 (custom_fields テーブル参照) に一致するカスタムフィールドを
// position, id 順 (CustomField.sorted) で読み込む。
func Load(ctx context.Context, q db.Queryer, where string, args ...any) ([]*CustomField, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []cfRow
	if err := q.Select(ctx, &rows, `SELECT `+cfCols+` FROM custom_fields WHERE `+where+` ORDER BY custom_fields.position, custom_fields.id`, args...); err != nil {
		return nil, err
	}
	out := make([]*CustomField, 0, len(rows))
	byID := map[int64]*CustomField{}
	for _, r := range rows {
		cf := &CustomField{
			ID: r.ID, OwnerKind: OwnerKind(r.OwnerKind), Name: r.Name, Description: r.Description.String,
			FieldFormat: r.FieldFormat, Regexp: r.Regexp.String, MinLength: nullInt(r.MinLength), MaxLength: nullInt(r.MaxLength),
			IsRequired: r.IsRequired, IsForAll: r.IsForAll, IsFilter: r.IsFilter, Searchable: r.Searchable,
			DefaultValue: r.DefaultValue.String, Editable: r.Editable, Visible: r.Visible, Multiple: r.Multiple,
			Position: r.Position, Settings: map[string]any{},
		}
		if len(r.PossibleValues) > 0 {
			var pv []any
			if err := json.Unmarshal(r.PossibleValues, &pv); err == nil {
				for _, v := range pv {
					cf.PossibleValues = append(cf.PossibleValues, fmt.Sprint(v))
				}
			}
		}
		if len(r.FormatSettings) > 0 {
			_ = json.Unmarshal(r.FormatSettings, &cf.Settings)
		}
		out = append(out, cf)
		byID[cf.ID] = cf
	}
	if len(out) == 0 {
		return out, nil
	}
	type link struct {
		CFID int64 `db:"custom_field_id"`
		ID   int64 `db:"id"`
	}
	for _, rel := range []struct {
		sql string
		set func(cf *CustomField, id int64)
	}{
		{`SELECT custom_field_id, role_id AS id FROM custom_fields_roles ORDER BY custom_field_id, role_id`, func(cf *CustomField, id int64) { cf.RoleIDs = append(cf.RoleIDs, id) }},
		{`SELECT custom_field_id, tracker_id AS id FROM custom_fields_trackers ORDER BY custom_field_id, tracker_id`, func(cf *CustomField, id int64) { cf.TrackerIDs = append(cf.TrackerIDs, id) }},
		{`SELECT custom_field_id, project_id AS id FROM custom_fields_projects ORDER BY custom_field_id, project_id`, func(cf *CustomField, id int64) { cf.ProjectIDs = append(cf.ProjectIDs, id) }},
	} {
		var links []link
		if err := q.Select(ctx, &links, rel.sql); err != nil {
			return nil, err
		}
		for _, l := range links {
			if cf := byID[l.CFID]; cf != nil {
				rel.set(cf, l.ID)
			}
		}
	}
	return out, nil
}

// ListByKind は owner_kind のカスタムフィールドを sorted 順で返す。
func ListByKind(ctx context.Context, q db.Queryer, kind OwnerKind) ([]*CustomField, error) {
	return Load(ctx, q, "custom_fields.owner_kind = ?", string(kind))
}

// Get は id のカスタムフィールドを返す (無ければ nil, nil)。
func Get(ctx context.Context, q db.Queryer, id int64) (*CustomField, error) {
	cfs, err := Load(ctx, q, "custom_fields.id = ?", id)
	if err != nil || len(cfs) == 0 {
		return nil, err
	}
	return cfs[0], nil
}
