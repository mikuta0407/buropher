// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
)

// AvailableCustomFields は Issue#available_custom_fields
// (project.all_issue_custom_fields & tracker.custom_fields、sorted 順)。
func (e *Env) AvailableCustomFields(ctx context.Context, iss *Issue) ([]*customfield.CustomField, error) {
	if iss.ProjectID == 0 || iss.TrackerID == 0 {
		return nil, nil
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil || p == nil {
		return nil, err
	}
	t, err := e.TrackerOf(ctx, iss)
	if err != nil || t == nil {
		return nil, err
	}
	cfs, err := e.IssueCustomFields(ctx)
	if err != nil {
		return nil, err
	}
	var out []*customfield.CustomField
	for _, cf := range cfs {
		if (cf.IsForAll || slices.Contains(cf.ProjectIDs, p.ID)) && slices.Contains(t.CustomFieldIDs, cf.ID) {
			out = append(out, cf)
		}
	}
	return out, nil
}

// setCustomFieldDefault は Issue#set_custom_field_default?。
func (iss *Issue) setCustomFieldDefault() bool {
	return iss.NewRecord() || iss.AttrChanged("project_id") || iss.AttrChanged("tracker_id")
}

// storedValue は DB の custom_values から field の値を作る (行が無ければ ok = false)。
func (iss *Issue) storedValue(cf *customfield.CustomField) (CFValue, bool) {
	var vals []sql.NullString
	found := false
	for _, r := range iss.cvRows {
		if r.FieldID != cf.ID {
			continue
		}
		found = true
		vals = append(vals, r.Value)
	}
	if !found {
		return CFValue{}, false
	}
	if cf.Multiple {
		// 複数値の NULL 要素 (Redmine では nil) は配列に nil を持てないため "" として読む
		ss := make([]string, len(vals))
		for i, v := range vals {
			ss[i] = v.String
		}
		return ArrayValue(ss...), true
	}
	// NULL は nil (D-17: '' と NULL は保存値のまま区別する)
	if !vals[0].Valid {
		return NilValue(), true
	}
	return StrValue(vals[0].String), true
}

// CustomFieldValues は custom_field_values (遅延計算)。
func (e *Env) CustomFieldValues(ctx context.Context, iss *Issue) ([]*CustomFieldValue, error) {
	if iss.cfv != nil {
		return iss.cfv, nil
	}
	fields, err := e.AvailableCustomFields(ctx, iss)
	if err != nil {
		return nil, err
	}
	out := make([]*CustomFieldValue, 0, len(fields))
	for _, cf := range fields {
		v, ok := iss.storedValue(cf)
		if !ok {
			if b, built := iss.builtValues[cf.ID]; built {
				// 既に組み立て済みの CustomValue (custom_values.build) を再利用する
				v = b
				if cf.Multiple {
					v = ArrayValue(b.String())
				}
			} else {
				var dv *string
				if iss.setCustomFieldDefault() {
					dv = e.cfDefaultValue(ctx, cf)
				}
				if cf.Multiple {
					if dv != nil {
						v = ArrayValue(*dv)
					} else {
						v = ArrayValue("")
					}
				} else if dv != nil {
					v = StrValue(*dv)
				}
				if iss.builtValues == nil {
					iss.builtValues = map[int64]CFValue{}
				}
				if dv != nil {
					iss.builtValues[cf.ID] = StrValue(*dv)
				} else {
					iss.builtValues[cf.ID] = CFValue{}
				}
			}
		}
		out = append(out, &CustomFieldValue{Field: cf, Value: v, ValueWas: v.clone()})
	}
	iss.cfv = out
	return out, nil
}

// CustomFieldValue は custom_field_value(c) (無ければ ok = false)。
func (e *Env) CustomFieldValue(ctx context.Context, iss *Issue, cfID int64) (CFValue, bool, error) {
	vs, err := e.CustomFieldValues(ctx, iss)
	if err != nil {
		return CFValue{}, false, err
	}
	for _, v := range vs {
		if v.Field.ID == cfID {
			return v.Value, true, nil
		}
	}
	return CFValue{}, false, nil
}

// castCustomFieldInput は CustomField#set_custom_field_value (format.set_custom_field_value)。
func castCustomFieldInput(cf *customfield.CustomField, v any) CFValue {
	switch x := v.(type) {
	case []string:
		var out []string
		for _, s := range x {
			if s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			out = []string{""}
		}
		return ArrayValue(out...)
	case []any:
		ss := make([]string, len(x))
		for k, e := range x {
			ss[k] = rubyToS(e)
		}
		return castCustomFieldInput(cf, ss)
	case CFValue:
		if x.isArray {
			return castCustomFieldInput(cf, x.a)
		}
		return StrValue(x.String())
	case nil:
		return StrValue("")
	}
	return StrValue(rubyToS(v))
}

// SetCustomFieldValues は custom_field_values= ({"<id>" => 値})。
// 値は string / []string / CFValue。
func (e *Env) SetCustomFieldValues(ctx context.Context, iss *Issue, values map[string]any) error {
	vs, err := e.CustomFieldValues(ctx, iss)
	if err != nil {
		return err
	}
	for _, cv := range vs {
		key := strconv.FormatInt(cv.Field.ID, 10)
		if v, ok := values[key]; ok {
			cv.Value = castCustomFieldInput(cv.Field, v)
		}
	}
	iss.cfvChanged = true
	return nil
}

// SetCustomFields は custom_fields= ([{"id" => .., "value" => ..}])。
func (e *Env) SetCustomFields(ctx context.Context, iss *Issue, list []map[string]any) error {
	h := map[string]any{}
	for _, m := range list {
		id, ok := m["id"]
		if !ok {
			continue
		}
		v, ok := m["value"]
		if !ok {
			continue
		}
		h[rubyToS(id)] = v
	}
	return e.SetCustomFieldValues(ctx, iss, h)
}

// reassignCustomFieldValues は reassign_custom_field_values。
func (e *Env) reassignCustomFieldValues(ctx context.Context, iss *Issue) error {
	if iss.cfv == nil {
		return nil
	}
	values := map[string]any{}
	built := map[int64]CFValue{}
	for _, v := range iss.cfv {
		values[strconv.FormatInt(v.Field.ID, 10)] = v.Value
		built[v.Field.ID] = v.Value
	}
	iss.cfv = nil
	if _, err := e.CustomFieldValues(ctx, iss); err != nil {
		return err
	}
	// Ruby は custom_field_values= で値を代入し直す (共通のフィールドは値を引き継ぐ)
	for _, cv := range iss.cfv {
		if v, ok := built[cv.Field.ID]; ok {
			cv.Value = castReassigned(cv.Field, v)
		}
	}
	iss.cfvChanged = true
	return nil
}

// castReassigned は custom_field_values= による再代入 (set_custom_field_value) の結果。
func castReassigned(cf *customfield.CustomField, v CFValue) CFValue {
	if v.isArray {
		return castCustomFieldInput(cf, v.a)
	}
	if v.s == nil {
		// nil.to_s = ""
		return StrValue("")
	}
	return StrValue(*v.s)
}

// CustomFieldValuesChanged は custom_field_values_changed?。
func (iss *Issue) CustomFieldValuesChanged() bool { return iss.cfvChanged }

// ResetCustomValues は reset_custom_values!。
func (iss *Issue) ResetCustomValues() {
	iss.cfv = nil
	iss.cfvChanged = true
}

// VisibleCustomFieldValues は visible_custom_field_values(user)。
func (e *Env) VisibleCustomFieldValues(ctx context.Context, iss *Issue, u *domain.User) ([]*CustomFieldValue, error) {
	vs, err := e.CustomFieldValues(ctx, iss)
	if err != nil {
		return nil, err
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	var out []*CustomFieldValue
	for _, v := range vs {
		ok, err := e.cfVisibleBy(ctx, v.Field, p, u)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// cfVisibleBy は IssueCustomField#visible_by?(project, user)。
func (e *Env) cfVisibleBy(ctx context.Context, cf *customfield.CustomField, p *domain.Project, u *domain.User) (bool, error) {
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return false, err
		}
	}
	if cf.Visible || u.IsAdmin() {
		return true, nil
	}
	if p == nil {
		return false, nil
	}
	return customfield.VisibleBy(ctx, e.authz(u), cf, p)
}

// EditableCustomFieldValues は editable_custom_field_values(user)。
func (e *Env) EditableCustomFieldValues(ctx context.Context, iss *Issue, u *domain.User) ([]*CustomFieldValue, error) {
	ro, err := e.ReadOnlyAttributeNames(ctx, iss, u)
	if err != nil {
		return nil, err
	}
	vs, err := e.VisibleCustomFieldValues(ctx, iss, u)
	if err != nil {
		return nil, err
	}
	var out []*CustomFieldValue
	for _, v := range vs {
		if !slices.Contains(ro, strconv.FormatInt(v.Field.ID, 10)) {
			out = append(out, v)
		}
	}
	return out, nil
}

// EditableCustomFields は editable_custom_fields(user)。
func (e *Env) EditableCustomFields(ctx context.Context, iss *Issue, u *domain.User) ([]*customfield.CustomField, error) {
	vs, err := e.EditableCustomFieldValues(ctx, iss, u)
	if err != nil {
		return nil, err
	}
	var out []*customfield.CustomField
	for _, v := range vs {
		if !slices.Contains(out, v.Field) {
			out = append(out, v.Field)
		}
	}
	return out, nil
}

// saveCustomFieldValues は save_custom_field_values。戻り値は touch が必要か
// (custom_values のいずれかが保存で変更されたか)。
func (e *Env) saveCustomFieldValues(ctx context.Context, iss *Issue) (bool, error) {
	vs, err := e.CustomFieldValues(ctx, iss)
	if err != nil {
		return false, err
	}
	existing := slices.Clone(iss.cvRows)
	used := map[int64]bool{}
	changed := false
	var keep []cvRow
	// 行が無かったフィールドの組み立て済み CustomValue は、関連の autosave (after_create / after_update) で
	// 先に保存される。その後の save_custom_field_values では「既存の行」として扱われ、
	// 値が変わらなければ touch の対象にならない。
	for _, cv := range vs {
		b, built := iss.builtValues[cv.Field.ID]
		if !built || slices.ContainsFunc(existing, func(r cvRow) bool { return r.FieldID == cv.Field.ID }) {
			continue
		}
		// 組み立て済みの値が nil (既定値なし) なら NULL、それ以外は空文字列もそのまま保存する
		val := cfNullString(b)
		id, err := e.Q.InsertReturningID(ctx, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('issue', ?, ?, ?)`,
			iss.ID, cv.Field.ID, val)
		if err != nil {
			return false, err
		}
		r := cvRow{ID: id, FieldID: cv.Field.ID, Value: val}
		existing = append(existing, r)
	}
	iss.builtValues = nil
	insert := func(fieldID int64, val sql.NullString) error {
		id, err := e.Q.InsertReturningID(ctx, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('issue', ?, ?, ?)`,
			iss.ID, fieldID, val)
		if err != nil {
			return err
		}
		r := cvRow{ID: id, FieldID: fieldID, Value: val}
		keep = append(keep, r)
		// save_custom_field_values で新たに組み立てた行 (複数値の追加分) は保存で変更ありになる
		changed = true
		return nil
	}
	for _, cv := range vs {
		if cv.Value.isArray {
			for _, v := range cv.Value.a {
				idx := slices.IndexFunc(existing, func(r cvRow) bool {
					return !used[r.ID] && r.FieldID == cv.Field.ID && r.Value.String == v
				})
				if idx >= 0 {
					used[existing[idx].ID] = true
					keep = append(keep, existing[idx])
					continue
				}
				if err := insert(cv.Field.ID, sql.NullString{String: v, Valid: true}); err != nil {
					return false, err
				}
			}
			continue
		}
		v := cfNullString(cv.Value)
		idx := slices.IndexFunc(existing, func(r cvRow) bool { return !used[r.ID] && r.FieldID == cv.Field.ID })
		if idx < 0 {
			if err := insert(cv.Field.ID, v); err != nil {
				return false, err
			}
			continue
		}
		r := existing[idx]
		used[r.ID] = true
		if r.Value != v {
			// target.value = custom_field_value.value (nil → "" も変更として保存する)
			if _, err := e.Q.Exec(ctx, `UPDATE custom_values SET value = ? WHERE id = ?`, v, r.ID); err != nil {
				return false, err
			}
			r.Value = v
			changed = true
		}
		keep = append(keep, r)
	}
	for _, r := range existing {
		if !used[r.ID] {
			if _, err := e.Q.Exec(ctx, `DELETE FROM custom_values WHERE id = ?`, r.ID); err != nil {
				return false, err
			}
		}
	}
	slices.SortFunc(keep, func(a, b cvRow) int { return int(a.ID - b.ID) })
	iss.cvRows = keep
	for _, cv := range vs {
		cv.ValueWas = cv.Value.clone()
	}
	iss.cfvChanged = false
	return changed, nil
}

// cfNullString は単一値の CustomValue#value として保存する値 (nil は NULL、"" は "")。
func cfNullString(v CFValue) sql.NullString {
	if v.IsNil() {
		return sql.NullString{}
	}
	return sql.NullString{String: v.String(), Valid: true}
}

// rubyToS は Ruby の to_s 相当の文字列化 (params の値用)。
func rubyToS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return RubyFloatToS(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []string:
		if len(x) == 0 {
			return ""
		}
		return fmt.Sprint(x)
	}
	return fmt.Sprint(v)
}
