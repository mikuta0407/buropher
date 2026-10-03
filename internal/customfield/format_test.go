// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

// test/unit/custom_field_test.rb と test/unit/lib/redmine/field_format/*_test.rb のうち
// クエリに関わる部分 (cast_value, totalable, query_filter_options, order/group_statement,
// visible スコープ, visibility_by_project_condition) の移植。
// 表示・編集タグ・検証・value_from_keyword などのテストは対象外。

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

func strp(s string) *string { return &s }

// test_float_cast_blank_value_should_return_nil / test_float_cast_valid_value_should_return_float
func TestFieldFormatFloatCast(t *testing.T) {
	f := &CustomField{FieldFormat: "float"}
	if f.CastValue(nil) != nil || f.CastValue(strp("")) != nil {
		t.Error("blank should be nil")
	}
	for in, want := range map[string]float64{"12": 12.0, "12.5": 12.5, "+12.5": 12.5, "-12.5": -12.5} {
		if got := f.CastValue(strp(in)); got != want {
			t.Errorf("cast %q = %v", in, got)
		}
	}
}

// progressbar_format_test.rb: test_cast_value_clamping / test_empty_value / test_totalable_support / test_query_filter_options
func TestFieldFormatProgressbar(t *testing.T) {
	f := &CustomField{FieldFormat: "progressbar"}
	for in, want := range map[string]int64{"-10": 0, "0": 0, "50": 50, "120": 100} {
		if got := f.CastValue(strp(in)); got != want {
			t.Errorf("cast %q = %v, want %d", in, got, want)
		}
	}
	if f.CastValue(strp("")) != nil {
		t.Error("empty")
	}
	if f.Totalable() || f.Format().TotalableSupported {
		t.Error("progressbar is not totalable")
	}
	if f.Format().FilterType != "integer" {
		t.Errorf("filter type %q", f.Format().FilterType)
	}
}

func TestFieldFormatQueryProperties(t *testing.T) {
	cases := []struct {
		format, filter string
		totalable      bool
		group          string
		order          []string
	}{
		{"string", "string", false, "", []string{"COALESCE(cf_1.value, '')"}},
		{"text", "text", false, "", []string{"COALESCE(cf_1.value, '')"}},
		{"link", "string", false, "", []string{"COALESCE(cf_1.value, '')"}},
		{"int", "integer", true, "CAST(CASE cf_1.value WHEN '' THEN '0' ELSE cf_1.value END AS decimal(30,3))", []string{"CAST(CASE cf_1.value WHEN '' THEN '0' ELSE cf_1.value END AS decimal(30,3))"}},
		{"float", "float", true, "", []string{"CAST(CASE cf_1.value WHEN '' THEN '0' ELSE cf_1.value END AS decimal(30,3))"}},
		{"date", "date", false, "COALESCE(cf_1.value, '')", []string{"COALESCE(cf_1.value, '')"}},
		{"list", "list_optional", false, "COALESCE(cf_1.value, '')", []string{"COALESCE(cf_1.value, '')"}},
		{"bool", "list_optional", false, "COALESCE(cf_1.value, '')", []string{"COALESCE(cf_1.value, '')"}},
		{"enumeration", "list_optional", false, "COALESCE(cf_1.value, '')", nil},
		{"user", "list_optional", false, "COALESCE(cf_1.value, '')", []string{"cf_1_user.firstname", "(cf_1_user.lastname || cf_1_user.name)", "cf_1_user.id"}},
		{"version", "list_optional", false, "COALESCE(cf_1.value, '')", VersionOrderFields("cf_1_version")},
	}
	for _, c := range cases {
		f := &CustomField{ID: 1, OwnerKind: KindIssue, FieldFormat: c.format}
		if f.Format().FilterType != c.filter || f.Totalable() != c.totalable {
			t.Errorf("%s: filter %q totalable %v", c.format, f.Format().FilterType, f.Totalable())
		}
		if g := f.GroupStatement(); g != c.group {
			t.Errorf("%s: group %q", c.format, g)
		}
		if o := f.OrderStatement("firstname_lastname"); !slices.Equal(o, c.order) {
			t.Errorf("%s: order %q", c.format, o)
		}
		// 複数値はソート・グループ不可
		f.Multiple = true
		if f.GroupStatement() != "" || f.OrderStatement("") != nil {
			t.Errorf("%s multiple: order/group should be empty", c.format)
		}
	}
	if FindFormat("attachment").IsFilterSupported {
		t.Error("attachment is not filterable")
	}
	// version_field_format_test: test_cast_value_should_not_raise_error_when_array_contains_value_casted_to_nil
	v := &CustomField{FieldFormat: "version"}
	for _, s := range []string{"1", "2", "42"} {
		if v.CastValue(strp(s)) == nil {
			t.Errorf("version cast %s", s)
		}
	}
}

func authzFor(t *testing.T, d *db.DB, id int64) *authz.Authorizer {
	t.Helper()
	var u *domain.User
	var err error
	if id == 0 {
		u, err = repository.AnonymousUser(context.Background(), d)
	} else {
		u, err = repository.GetUser(context.Background(), d, id)
	}
	if err != nil {
		t.Fatal(err)
	}
	return authz.New(d, u)
}

func exec(t *testing.T, d *db.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func visibleIDs(t *testing.T, d *db.DB, a *authz.Authorizer) []int64 {
	t.Helper()
	cond, err := VisibleCondition(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	if err := d.Select(context.Background(), &ids, `SELECT id FROM custom_fields WHERE `+cond+` ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	return ids
}

// test_visibile_scope_with_*: CustomField.visible
func TestFieldFormatVisibleScope(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		testfixtures.LoadAt(t, d, time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), testfixtures.All()...)
		exec(t, d, `DELETE FROM custom_fields`)
		var fields []int64
		for i, vis := range []bool{true, false, false, false} {
			id, err := d.InsertReturningID(context.Background(), `INSERT INTO custom_fields (owner_kind, name, field_format, visible) VALUES ('issue', ?, 'string', ?)`,
				[]string{"CF a", "CF b", "CF c", "CF d"}[i], vis)
			if err != nil {
				t.Fatal(err)
			}
			fields = append(fields, id)
		}
		for _, r := range []int64{1, 3} {
			exec(t, d, `INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (?, ?)`, fields[2], r)
		}
		for _, r := range []int64{1, 2} {
			exec(t, d, `INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (?, ?)`, fields[3], r)
		}
		uid := testfixtures.GenerateUser(t, d)
		mid, err := d.InsertReturningID(context.Background(), `INSERT INTO members (project_id, principal_id, created_at) VALUES (1, ?, ?)`, uid, db.Now())
		if err != nil {
			t.Fatal(err)
		}
		exec(t, d, `INSERT INTO member_roles (member_id, role_id) VALUES (?, 3)`, mid)

		if got := visibleIDs(t, d, authzFor(t, d, 1)); !slices.Equal(got, fields) {
			t.Errorf("admin: %v", got)
		}
		if got := visibleIDs(t, d, authzFor(t, d, uid)); !slices.Equal(got, []int64{fields[0], fields[2]}) {
			t.Errorf("member: %v", got)
		}
		if got := visibleIDs(t, d, authzFor(t, d, 0)); !slices.Equal(got, []int64{fields[0]}) {
			t.Errorf("anonymous: %v", got)
		}
	})
}

// test_project_custom_field_visibility
func TestFieldFormatProjectCustomFieldVisibility(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		testfixtures.LoadAt(t, d, time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), testfixtures.All()...)
		id, err := d.InsertReturningID(context.Background(), `INSERT INTO custom_fields (owner_kind, name, field_format, visible, possible_values) VALUES ('project', 'PCF', 'list', ?, '["a","b","c"]')`, false)
		if err != nil {
			t.Fatal(err)
		}
		exec(t, d, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('project', 3, ?, 'a')`, id)
		cf, err := Get(context.Background(), d, id)
		if err != nil || cf == nil {
			t.Fatal(err)
		}
		has := func(uid int64) bool {
			cond, err := VisibilityByProjectCondition(context.Background(), authzFor(t, d, uid), cf, "", "")
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			if err := d.Select(context.Background(), &ids, `SELECT id FROM projects WHERE `+cond); err != nil {
				t.Fatal(err)
			}
			return slices.Contains(ids, 3)
		}
		if !has(1) {
			t.Error("admin should find project 3")
		}
		if has(2) {
			t.Error("user 2 should not find project 3")
		}
	})
}
