// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
)

// ---------------------------------------------------------------- ProjectQuery / ProjectAdminQuery

// projectKind は ProjectQuery (admin = true なら ProjectAdminQuery)。
type projectKind struct{ admin bool }

func (projectKind) queriedTable() string    { return "projects" }
func (projectKind) customizedTable() string { return "projects" }
func (projectKind) customizedKind() string  { return "project" }
func (projectKind) viewPermission() string  { return "search_project" }

func (projectKind) defaultFilters() *Filters {
	f := NewFilters()
	f.Set("status", Filter{Operator: "=", Values: []string{"1"}})
	return f
}

func (projectKind) defaultSortCriteria() SortCriteria { return SortCriteria{{"", ""}} }

func (k projectKind) availableDisplayTypes(*Query) []string {
	if k.admin {
		return []string{"list"}
	}
	return []string{"board", "list"}
}

func (k projectKind) defaultDisplayType(q *Query) string {
	if k.admin {
		return "list"
	}
	return q.env.setting("project_list_display_type")
}

func (projectKind) defaultColumnNames(q *Query) []string {
	return q.env.settingHashStrings("project_list_defaults", "column_names")
}

func (projectKind) defaultTotalableNames(*Query) []string { return nil }

func (projectKind) columnFor(field string) (string, string) {
	return "projects", renameTimestamp(field)
}

func (projectKind) initializeAvailableFilters(ctx context.Context, q *Query) error {
	q.addAvailableFilter("status", filterOpt{Type: "list", ValuesFunc: q.projectStatusesValues})
	q.addAvailableFilter("id", filterOpt{Type: "list", ValuesFunc: q.projectValues, Label: "field_project"})
	q.addAvailableFilter("name", filterOpt{Type: "text"})
	q.addAvailableFilter("description", filterOpt{Type: "text"})
	q.addAvailableFilter("parent_id", filterOpt{Type: "list_subprojects", ValuesFunc: q.projectValues, Label: "field_parent"})
	q.addAvailableFilter("is_public", filterOpt{Type: "list",
		Values: []Option{{Label: q.env.l("general_text_yes"), Value: "1"}, {Label: q.env.l("general_text_no"), Value: "0"}}})
	q.addAvailableFilter("created_on", filterOpt{Type: "date_past"})
	q.addAvailableFilter("updated_on", filterOpt{Type: "date_past"})
	cfs, err := q.visibleFilterCustomFields(ctx, "custom_fields.owner_kind = 'project'")
	if err != nil {
		return err
	}
	return q.addCustomFieldsFilters(ctx, cfs, "")
}

func (projectKind) availableColumns(ctx context.Context, q *Query) ([]*Column, error) {
	lft, err := q.projectsLftExpr(ctx, "projects")
	if err != nil {
		return nil, err
	}
	cols := []*Column{
		newColumn("name", ColumnPlain, colOpt{sortable: []string{"projects.name"}}),
		newColumn("status", ColumnPlain, colOpt{sortable: []string{"projects.status"}}),
		newColumn("short_description", ColumnPlain, colOpt{sortable: []string{"projects.description"}, caption: "field_description"}),
		newColumn("homepage", ColumnPlain, colOpt{sortable: []string{"projects.homepage"}}),
		newColumn("identifier", ColumnPlain, colOpt{sortable: []string{"projects.identifier"}}),
		newColumn("parent_id", ColumnPlain, colOpt{sortable: []string{lft + " ASC"}, defaultOrder: "desc", caption: "field_parent"}),
		newColumn("is_public", ColumnPlain, colOpt{sortable: []string{"projects.is_public"}, groupable: true, groupSQL: "projects.is_public"}),
		newColumn("created_on", ColumnPlain, colOpt{sortable: []string{"projects.created_at"}, defaultOrder: "desc"}),
		newColumn("updated_on", ColumnPlain, colOpt{sortable: []string{"projects.updated_at"}, defaultOrder: "desc"}),
		newColumn("last_activity_date", ColumnPlain, colOpt{}),
	}
	cfs, err := q.visibleCustomFields(ctx, "custom_fields.owner_kind = 'project'")
	if err != nil {
		return nil, err
	}
	for _, cf := range cfs {
		cols = append(cols, q.newCustomFieldColumn(cf, nil))
	}
	return cols, nil
}

func (k projectKind) baseScope(ctx context.Context, q *Query) (string, frag, error) {
	st, err := q.statement(ctx)
	if err != nil {
		return "", frag{}, err
	}
	if k.admin {
		return "projects", paren(st), nil
	}
	vis, err := q.env.Auth.VisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return "", frag{}, err
	}
	return "projects", joinFrags(" AND ", raw("("+vis+")"), paren(st)), nil
}

func (projectKind) joinsForOrderStatement(ctx context.Context, q *Query, order string) ([]string, error) {
	return q.cfJoinsForOrder(ctx, order)
}

func (projectKind) sqlForSpecialField(context.Context, *Query, string, string, []string) (frag, bool, error) {
	return frag{}, false, nil
}

// ---------------------------------------------------------------- UserQuery

// userKind は UserQuery (queried_class は Principal。テーブルは principals を users として参照する)。
type userKind struct{}

func (userKind) queriedTable() string    { return "users" }
func (userKind) customizedTable() string { return "principals" }
func (userKind) customizedKind() string  { return "principal" }
func (userKind) viewPermission() string  { return "" }

func (userKind) defaultFilters() *Filters {
	f := NewFilters()
	f.Set("status", Filter{Operator: "=", Values: []string{"1"}})
	return f
}

func (userKind) defaultSortCriteria() SortCriteria     { return SortCriteria{{"login", "asc"}} }
func (userKind) availableDisplayTypes(*Query) []string { return []string{"list"} }
func (userKind) defaultDisplayType(*Query) string      { return "list" }
func (userKind) defaultColumnNames(*Query) []string {
	return []string{"login", "firstname", "lastname", "mail", "admin", "created_on", "last_login_on"}
}
func (userKind) defaultTotalableNames(*Query) []string { return nil }

func (userKind) columnFor(field string) (string, string) {
	switch field {
	case "login", "auth_source_id", "twofa_scheme", "admin":
		return "user_accounts", field
	case "last_login_on", "passwd_changed_on":
		return "user_accounts", renameTimestamp(field)
	}
	return "users", renameTimestamp(field)
}

func (userKind) initializeAvailableFilters(ctx context.Context, q *Query) error {
	q.addAvailableFilter("status", filterOpt{Type: "list_optional", ValuesFunc: func(context.Context) ([]Option, error) {
		return []Option{
			{Label: q.env.l("status_active"), Value: "1"},
			{Label: q.env.l("status_registered"), Value: "2"},
			{Label: q.env.l("status_locked"), Value: "3"},
		}, nil
	}})
	q.addAvailableFilter("auth_source_id", filterOpt{Type: "list_optional", ValuesFunc: func(ctx context.Context) ([]Option, error) {
		var rows []struct {
			ID   int64  `db:"id"`
			Name string `db:"name"`
		}
		if err := q.env.Q.Select(ctx, &rows, `SELECT id, name FROM auth_sources ORDER BY name ASC`); err != nil {
			return nil, err
		}
		out := make([]Option, len(rows))
		for i, r := range rows {
			out[i] = Option{Label: r.Name, Value: itoa(r.ID)}
		}
		return out, nil
	}})
	q.addAvailableFilter("is_member_of_group", filterOpt{Type: "list_optional", ValuesFunc: q.groupValues})
	if q.env.settingBool("twofa") {
		q.addAvailableFilter("twofa_scheme", filterOpt{Type: "list_optional", ValuesFunc: func(context.Context) ([]Option, error) {
			return []Option{{Label: q.env.l("twofa__totp__name"), Value: "totp"}}, nil
		}})
	}
	q.addAvailableFilter("name", filterOpt{Type: "text", Label: "field_name_or_email_or_login"})
	q.addAvailableFilter("login", filterOpt{Type: "string"})
	q.addAvailableFilter("firstname", filterOpt{Type: "string"})
	q.addAvailableFilter("lastname", filterOpt{Type: "string"})
	q.addAvailableFilter("mail", filterOpt{Type: "string"})
	q.addAvailableFilter("created_on", filterOpt{Type: "date_past"})
	q.addAvailableFilter("last_login_on", filterOpt{Type: "date_past"})
	q.addAvailableFilter("admin", filterOpt{Type: "list",
		Values: []Option{{Label: q.env.l("general_text_yes"), Value: "1"}, {Label: q.env.l("general_text_no"), Value: "0"}}})
	cfs, err := q.visibleFilterCustomFields(ctx, "custom_fields.owner_kind = 'user'")
	if err != nil {
		return err
	}
	return q.addCustomFieldsFilters(ctx, cfs, "")
}

func (userKind) availableColumns(ctx context.Context, q *Query) ([]*Column, error) {
	cols := []*Column{
		newColumn("login", ColumnPlain, colOpt{sortable: []string{"user_accounts.login"}}),
		newColumn("firstname", ColumnPlain, colOpt{sortable: []string{"users.firstname"}}),
		newColumn("lastname", ColumnPlain, colOpt{sortable: []string{"users.lastname"}}),
		newColumn("mail", ColumnPlain, colOpt{sortable: []string{"email_addresses.address"}}),
		newColumn("admin", ColumnPlain, colOpt{sortable: []string{"user_accounts.admin"}}),
		newColumn("created_on", ColumnPlain, colOpt{sortable: []string{"users.created_at"}}),
		newColumn("updated_on", ColumnPlain, colOpt{sortable: []string{"users.updated_at"}}),
		newColumn("last_login_on", ColumnPlain, colOpt{sortable: []string{"user_accounts.last_login_at"}}),
		newColumn("passwd_changed_on", ColumnPlain, colOpt{sortable: []string{"user_accounts.password_changed_at"}}),
		newColumn("status", ColumnPlain, colOpt{sortable: []string{"users.status"}}),
		{Name: "auth_source.name", Kind: ColumnAssociation, Association: "auth_source", Attribute: "name", Inline: true,
			CaptionKey: "field_auth_source", Sortable: []string{"auth_sources.name"}},
	}
	if q.env.settingBool("twofa") {
		cols = append(cols, newColumn("twofa_scheme", ColumnPlain, colOpt{sortable: []string{"user_accounts.twofa_scheme"}}))
	}
	cfs, err := q.visibleCustomFields(ctx, "custom_fields.owner_kind = 'user'")
	if err != nil {
		return nil, err
	}
	for _, cf := range cfs {
		cols = append(cols, q.newCustomFieldColumn(cf, nil))
	}
	return cols, nil
}

func (userKind) baseScope(ctx context.Context, q *Query) (string, frag, error) {
	st, err := q.statement(ctx)
	if err != nil {
		return "", frag{}, err
	}
	from := "principals users INNER JOIN user_accounts ON user_accounts.principal_id = users.id" +
		" LEFT OUTER JOIN email_addresses ON email_addresses.user_id = users.id AND email_addresses.is_default = " + q.env.boolLit(true)
	return from, joinFrags(" AND ", raw("users.kind IN ('user', 'anonymous_user') AND users.status <> 0"), paren(st)), nil
}

func (userKind) joinsForOrderStatement(ctx context.Context, q *Query, order string) ([]string, error) {
	joins, err := q.cfJoinsForOrder(ctx, order)
	if err != nil {
		return nil, err
	}
	if strings.Contains(order, "auth_source") {
		joins = append(joins, "LEFT OUTER JOIN auth_sources ON auth_sources.id = user_accounts.auth_source_id")
	}
	// CF の JOIN は customized_class のテーブル名 (principals) で外部参照するので、別名 users に読み替える
	for i, j := range joins {
		joins[i] = strings.ReplaceAll(j, "= principals.id", "= users.id")
	}
	return joins, nil
}

func (userKind) sqlForSpecialField(ctx context.Context, q *Query, field, operator string, v []string) (frag, bool, error) {
	switch field {
	case "admin":
		if len(v) == 0 {
			return frag{}, true, nil
		}
		trueValue := "0"
		if operator == "=" {
			trueValue = "1"
		}
		return raw("(user_accounts.admin = " + q.env.boolLit(v[0] == trueValue) + ")"), true, nil
	case "is_member_of_group":
		if operator == "*" || operator == "!*" {
			ids, err := q.givableGroupIDs(ctx)
			if err != nil {
				return frag{}, true, err
			}
			v = make([]string, len(ids))
			for i, id := range ids {
				v[i] = itoa(id)
			}
		}
		e := "EXISTS"
		if strings.HasPrefix(operator, "!") {
			e = "NOT EXISTS"
		}
		inner, err := q.sqlForField(ctx, field, "=", v, "group_users", "group_id", false)
		if err != nil {
			return frag{}, true, err
		}
		return concat(raw("("+e+" (SELECT 1 FROM group_users WHERE users.id = group_users.user_id AND "), inner, raw("))")), true, nil
	case "mail":
		match := true
		if operator == "!*" {
			match, operator = false, "*"
		}
		inner, err := q.sqlForField(ctx, field, operator, v, "email_addresses", "address", false)
		if err != nil {
			return frag{}, true, err
		}
		e := "EXISTS"
		if !match {
			e = "NOT EXISTS"
		}
		return concat(raw(e+" (SELECT 1 FROM email_addresses WHERE email_addresses.user_id = users.id AND "), inner, raw(")")), true, nil
	case "name":
		switch operator {
		case "*":
			return raw("1=1"), true, nil
		case "!*":
			return raw("1=0"), true, nil
		}
		match := !strings.HasPrefix(operator, "!")
		matching := strings.TrimPrefix(operator, "!")
		var conds []frag
		for _, c := range []struct{ table, col string }{{"user_accounts", "login"}, {"users", "firstname"}, {"users", "lastname"}} {
			s, err := q.sqlForField(ctx, field, operator, v, c.table, c.col, false)
			if err != nil {
				return frag{}, true, err
			}
			conds = append(conds, s.wrap("(", ")"))
		}
		email, err := q.sqlForField(ctx, field, matching, v, "email_addresses", "address", false)
		if err != nil {
			return frag{}, true, err
		}
		e := "EXISTS"
		if !match {
			e = "NOT EXISTS"
		}
		conds = append(conds, concat(raw("("+e+" (SELECT 1 FROM email_addresses WHERE email_addresses.user_id = users.id AND "), email, raw("))")))
		sep := " OR "
		if !match {
			sep = " AND "
		}
		return joinFrags(sep, conds...).wrap("(", ")"), true, nil
	}
	return frag{}, false, nil
}
