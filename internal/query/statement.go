// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
)

var (
	reChainedCF   = regexp.MustCompile(`^cf_(\d+)\.cf_(\d+)$`)
	reCF          = regexp.MustCompile(`cf_(\d+)$`)
	reCFAttribute = regexp.MustCompile(`^cf_(\d+)\.(.+)$`)
	reAssocCF     = regexp.MustCompile(`^(.+)\.cf_`)
	// reIdentField は列名としてそのまま SQL に入れてよいフィルタ名。
	reIdentField = regexp.MustCompile(`\A[a-z0-9_]+(\.[a-z0-9_]+)?\z`)
)

// meFields は "me" を User.current の id に置き換えるフィールド。
var meFields = []string{"assigned_to_id", "author_id", "user_id", "watcher_id", "updated_by", "last_updated_by"}

// Statement は statement: フィルタ (valid? な場合のみ) とプロジェクト条件の WHERE 句。
// 条件が無ければ空の SQL を返す。
func (q *Query) Statement(ctx context.Context) (string, []any, error) {
	f, err := q.statement(ctx)
	return f.SQL, f.Args, err
}

func (q *Query) statement(ctx context.Context) (frag, error) {
	var clauses []frag
	valid, err := q.Valid(ctx)
	if err != nil {
		return frag{}, err
	}
	if valid {
		for _, field := range q.Filters.Keys() {
			if field == "subproject_id" {
				continue
			}
			v := slices.Clone(q.ValuesFor(field))
			if len(v) == 0 {
				continue
			}
			operator := q.OperatorFor(field)
			v, err = q.substituteValues(ctx, field, v)
			if err != nil {
				return frag{}, err
			}
			var c frag
			switch {
			case reChainedCF.MatchString(field):
				m := reChainedCF.FindStringSubmatch(field)
				c, err = q.sqlForChainedCustomField(ctx, field, operator, v, m[1], m[2])
			case reCF.MatchString(field):
				m := reCF.FindStringSubmatch(field)
				c, err = q.sqlForCustomField(ctx, field, operator, v, m[1])
			case reCFAttribute.MatchString(field):
				m := reCFAttribute.FindStringSubmatch(field)
				c, err = q.sqlForCustomFieldAttribute(ctx, field, operator, v, m[2])
			default:
				var handled bool
				c, handled, err = q.impl.sqlForSpecialField(ctx, q, field, operator, v)
				if err == nil && !handled {
					// 保存済みクエリの filters のキーは Web/API からは AddFilter で検査されるが、
					// Redmine からのインポート等で DB に直接入った任意の文字列が列名として
					// SQL に連結されないよう、識別子の形でなければ無視する。
					if !reIdentField.MatchString(field) {
						continue
					}
					table, col := q.impl.columnFor(field)
					c, err = q.sqlForField(ctx, field, operator, v, table, col, false)
					if !c.empty() {
						c = c.wrap("(", ")")
					}
				}
			}
			if err != nil {
				return frag{}, err
			}
			clauses = append(clauses, c)
		}
	}
	gc, err := q.GroupByColumn(ctx)
	if err != nil {
		return frag{}, err
	}
	if gc != nil && gc.CustomField != nil && gc.Association == "" {
		vis, err := customfield.VisibilityByProjectCondition(ctx, q.env.Auth, gc.CustomField, "", "")
		if err != nil {
			return frag{}, err
		}
		clauses = append(clauses, raw(vis))
	}
	ps, err := q.projectStatement(ctx)
	if err != nil {
		return frag{}, err
	}
	clauses = append(clauses, raw(ps))
	return joinFrags(" AND ", clauses...), nil
}

// substituteValues は "me" / "mine" / "bookmarks" の置き換え。
func (q *Query) substituteValues(ctx context.Context, field string, v []string) ([]string, error) {
	u := q.env.User()
	if slices.Contains(meFields, field) && slices.Contains(v, "me") {
		v = slices.DeleteFunc(v, func(s string) bool { return s == "me" })
		if u.Logged() {
			v = append(v, itoa(u.ID))
			if field == "assigned_to_id" || field == "watcher_id" {
				gids, err := q.env.Auth.GroupIDs(ctx)
				if err != nil {
					return nil, err
				}
				for _, g := range gids {
					v = append(v, itoa(g))
				}
			}
		} else {
			v = append(v, "0")
		}
	}
	isProjectQuery := q.Kind == KindProject || q.Kind == KindProjectAdmin
	if field == "project_id" || (isProjectQuery && (field == "id" || field == "parent_id")) {
		if slices.Contains(v, "mine") {
			v = slices.DeleteFunc(v, func(s string) bool { return s == "mine" })
			ids, err := q.env.Auth.ProjectIDs(ctx)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				v = append(v, itoa(id))
			}
		}
		if slices.Contains(v, "bookmarks") {
			v = slices.DeleteFunc(v, func(s string) bool { return s == "bookmarks" })
			ids, err := q.env.BookmarkedProjectIDs(ctx)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				v = append(v, itoa(id))
			}
		}
	}
	return v, nil
}

// projectStatement は project_statement。
func (q *Query) projectStatement(ctx context.Context) (string, error) {
	if q.Project == nil {
		return "", nil
	}
	pid := q.Project.ID
	subs, err := q.subprojectIDsNotArchived(ctx)
	if err != nil {
		return "", err
	}
	tree := "projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + itoa(pid) + ")"
	if len(subs) == 0 {
		return "projects.id = " + itoa(pid), nil
	}
	if q.HasFilter("subproject_id") {
		values := q.ValuesFor("subproject_id")
		switch q.OperatorFor("subproject_id") {
		case "=":
			ids := []int64{pid}
			for _, v := range values {
				ids = append(ids, customfield.RubyToI(v))
			}
			return "projects.id IN (" + idList(ids) + ")", nil
		case "!":
			ids := append([]int64{pid}, subs...)
			var excl []int64
			for _, v := range values {
				excl = append(excl, customfield.RubyToI(v))
			}
			ids = slices.DeleteFunc(ids, func(id int64) bool { return slices.Contains(excl, id) })
			return "projects.id IN (" + idListOrNull(ids) + ")", nil
		case "!*":
			return "projects.id = " + itoa(pid), nil
		default:
			return tree, nil
		}
	}
	if q.env.settingBool("display_subprojects_issues") {
		return tree, nil
	}
	return "projects.id = " + itoa(pid), nil
}

// visibleNotesCondition は Journal.visible_notes_condition(User.current, :skip_pre_condition => true)。
// Redmine と同じく常に journals テーブル (issue_journals) を参照する。
func (q *Query) visibleNotesCondition(ctx context.Context, table string) (string, error) {
	perm, err := q.env.Auth.AllowedToCondition(ctx, "view_private_notes", authz.ConditionOptions{SkipPreCondition: true}, nil)
	if err != nil {
		return "", err
	}
	return "(" + table + ".private_notes = " + q.env.boolLit(false) + " OR " + table + ".user_id = " + itoa(q.env.User().ID) +
		" OR (" + perm + "))", nil
}

// assocCustomized は関連のカスタムフィールドのフィルタ ("project.cf_3" 等) の customized_key / テーブル / customized_kind。
func (q *Query) assocCustomized(assoc string) (key, table, kind string, ok bool) {
	switch assoc {
	case "project":
		return "project_id", "projects", "project", true
	case "author":
		return "author_id", "principals", "principal", true
	case "assigned_to":
		return "assigned_to_id", "principals", "principal", true
	case "fixed_version":
		return "fixed_version_id", "versions", "version", true
	case "user":
		return "user_id", "principals", "principal", true
	case "issue":
		return "issue_id", "issues", "issue", true
	}
	return "", "", "", false
}

// sqlForCustomField は sql_for_custom_field。
func (q *Query) sqlForCustomField(ctx context.Context, field, operator string, value []string, cfID string) (frag, error) {
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return frag{}, err
	}
	def := af.Get(field)
	if def == nil || def.CustomField == nil {
		return frag{}, nil
	}
	cf := def.CustomField
	if cf.Format().Target == "user" && slices.Contains(value, "me") {
		value = slices.DeleteFunc(slices.Clone(value), func(s string) bool { return s == "me" })
		value = append(value, itoa(q.env.User().ID))
	}
	notIn := ""
	if operator == "!" {
		operator, notIn = "=", "NOT "
	}
	key, table, kind := "id", q.impl.customizedTable(), q.impl.customizedKind()
	if m := reAssocCF.FindStringSubmatch(field); m != nil {
		var ok bool
		key, table, kind, ok = q.assocCustomized(m[1])
		if !ok {
			return frag{}, fmt.Errorf("query: unknown %s association %s", q.Kind, m[1])
		}
	}
	where, err := q.sqlForField(ctx, field, operator, value, "custom_values", "value", true)
	if err != nil {
		return frag{}, err
	}
	if strings.ContainsAny(operator, "<>") {
		where = where.wrap("(", ") AND custom_values.value <> ''")
	}
	vis, err := customfield.VisibilityByProjectCondition(ctx, q.env.Auth, cf, "", "")
	if err != nil {
		return frag{}, err
	}
	return concat(raw(notIn+"EXISTS (SELECT ct.id FROM "+table+" ct LEFT OUTER JOIN custom_values ON custom_values.customized_kind='"+kind+"'"+
		" AND custom_values.customized_id=ct.id AND custom_values.custom_field_id="+cfID+
		" WHERE "+q.impl.queriedTable()+"."+key+" = ct.id AND  ("), where, raw(") AND ("+vis+"))")), nil
}

// sqlForChainedCustomField は sql_for_chained_custom_field ("cf_1.cf_2")。
func (q *Query) sqlForChainedCustomField(ctx context.Context, field, operator string, value []string, cfID, chainedID string) (frag, error) {
	notIn := ""
	if operator == "!" {
		operator, notIn = "=", "NOT "
	}
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return frag{}, err
	}
	def := af.Get(field)
	if def == nil || def.Through == nil {
		return frag{}, nil
	}
	targetKind := "principal"
	if def.Through.Format().Target == "version" {
		targetKind = "version"
	}
	inner, err := q.sqlForField(ctx, field, operator, value, "custom_values", "value", true)
	if err != nil {
		return frag{}, err
	}
	return concat(raw(q.impl.queriedTable()+".id "+notIn+"IN (SELECT customized_id FROM custom_values"+
		" WHERE customized_kind='"+q.impl.customizedKind()+"' AND custom_field_id="+cfID+
		"  AND CAST(CASE value WHEN '' THEN '0' ELSE value END AS decimal(30,0)) IN ("+
		"  SELECT customized_id FROM custom_values"+
		"  WHERE customized_kind='"+targetKind+"' AND custom_field_id="+chainedID+
		"  AND "), inner, raw("))")), nil
}

// sqlForCustomFieldAttribute は sql_for_custom_field_attribute ("cf_4.due_date" / "cf_4.status")。
func (q *Query) sqlForCustomFieldAttribute(ctx context.Context, field, operator string, value []string, attribute string) (frag, error) {
	if attribute == "due_date" {
		attribute = "effective_date"
	}
	notIn := ""
	if operator == "!" {
		operator, notIn = "=", "NOT "
	}
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return frag{}, err
	}
	def := af.Get(field)
	if def == nil || def.CustomField == nil {
		return frag{}, nil
	}
	target := def.CustomField.Format().TargetTable()
	if target == "" {
		return frag{}, fmt.Errorf("query: custom field %d has no target class", def.CustomField.ID)
	}
	m := reCFAttribute.FindStringSubmatch(field)
	inner, err := q.sqlForField(ctx, field, operator, value, target, attribute, false)
	if err != nil {
		return frag{}, err
	}
	return concat(raw(q.impl.queriedTable()+".id "+notIn+"IN (SELECT customized_id FROM custom_values"+
		" WHERE customized_kind='"+q.impl.customizedKind()+"' AND custom_field_id="+m[1]+
		"  AND CAST(CASE value WHEN '' THEN '0' ELSE value END AS decimal(30,0)) IN ("+
		"  SELECT id FROM "+target+" WHERE "), inner, raw("))")), nil
}

// atoiList は文字列 id を整数にする (to_i)。
func atoiList(values []string) []int64 {
	out := make([]int64, 0, len(values))
	for _, v := range values {
		out = append(out, customfield.RubyToI(v))
	}
	return out
}

func parseID(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}
