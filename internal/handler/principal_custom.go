// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルはユーザー・グループのカスタムフィールド値（acts_as_customizable）の最小限の移植:
// visible_custom_field_values（API・詳細画面）、custom_field_tag_with_label（文字列系の入力）、
// custom_field_values= と単一値の検証。
// TODO(custom_fields): リスト・真偽値・日付・複数値などの形式は internal/customfield の実装に置き換える。

// principalCustomValue は CustomFieldValue（custom_field と値）。
type principalCustomValue struct {
	Field *repository.PrincipalCustomField
	// Value は単一値（nil = 値なし）。
	Value *string
	// Values は複数値のフィールドの値。
	Values []string
}

// ValueString は値の文字列（nil は ""）。
func (v principalCustomValue) ValueString() string {
	if v.Value == nil {
		return strings.Join(v.Values, ", ")
	}
	return *v.Value
}

// CSSClasses は custom_field.css_classes。
func (v principalCustomValue) CSSClasses() string { return v.Field.CSSClasses() }

// Name は custom_field.name。
func (v principalCustomValue) Name() string { return v.Field.Name }

// loadPrincipalCustomValues は users / groups の visible_custom_field_values を id ごとに返す
// （User.current が管理者でなければ visible なフィールドのみ）。
func (a *App) loadPrincipalCustomValues(c *Req, ownerKind string, users []*domain.User) (map[int64][]principalCustomValue, error) {
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	return a.principalCustomValuesByID(c, ownerKind, ids, true)
}

func (a *App) principalCustomValuesByID(c *Req, ownerKind string, ids []int64, visibleOnly bool) (map[int64][]principalCustomValue, error) {
	fields, err := repository.PrincipalCustomFields(c.Ctx(), a.DB, ownerKind)
	if err != nil {
		return nil, err
	}
	if visibleOnly && !c.User.IsAdmin() {
		var vis []*repository.PrincipalCustomField
		for _, f := range fields {
			if f.Visible {
				vis = append(vis, f)
			}
		}
		fields = vis
	}
	out := map[int64][]principalCustomValue{}
	if len(fields) == 0 || len(ids) == 0 {
		return out, nil
	}
	vals, err := repository.PrincipalCustomValues(c.Ctx(), a.DB, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		var list []principalCustomValue
		for _, f := range fields {
			cv := principalCustomValue{Field: f}
			raw := vals[id][f.ID]
			if f.Multiple {
				for _, r := range raw {
					if r.Valid && r.String != "" {
						cv.Values = append(cv.Values, r.String)
					}
				}
				if cv.Values == nil {
					cv.Values = []string{}
				}
			} else if len(raw) > 0 && raw[0].Valid {
				// 行があれば値（NULL は nil、"" は ""。D-17）
				s := raw[0].String
				cv.Value = &s
			}
			list = append(list, cv)
		}
		out[id] = list
	}
	return out, nil
}

// newPrincipalCustomValues は新規作成時の custom_field_values（既定値を入れる）。
func (a *App) newPrincipalCustomValues(c *Req, ownerKind string) ([]principalCustomValue, error) {
	fields, err := repository.PrincipalCustomFields(c.Ctx(), a.DB, ownerKind)
	if err != nil {
		return nil, err
	}
	var out []principalCustomValue
	for _, f := range fields {
		cv := principalCustomValue{Field: f}
		{
			// CustomValue#initialize: value ||= custom_field.default_value（既定値が nil なら nil）
			s := f.DefaultValue.String
			// 日付の相対既定値（7.0.1 #44129）は User.current.today から評価する
			if f.DefaultValue.Valid {
				s = *domain.CustomFieldDefaultValue(f.FieldFormat, f.FormatSetting("default_value_mode"), &s, a.userToday(c))
			}
			if f.Multiple {
				if s != "" {
					cv.Values = []string{s}
				}
			} else if f.DefaultValue.Valid {
				cv.Value = &s
			}
		}
		out = append(out, cv)
	}
	return out, nil
}

// renderAPICustomValues は CustomFieldsHelper#render_api_custom_values。
func renderAPICustomValues(b apibuilder.Builder, cvs []principalCustomValue) {
	if len(cvs) == 0 {
		return
	}
	b.Array("custom_fields", nil, func() {
		for _, cv := range cvs {
			attrs := apibuilder.A("id", cv.Field.ID, "name", cv.Field.Name)
			if cv.Field.Multiple {
				attrs = append(attrs, apibuilder.KV{Key: "multiple", Value: true})
			}
			b.ObjectAttrs("custom_field", attrs, func() {
				if cv.Field.Multiple {
					b.Array("value", nil, func() {
						for _, v := range cv.Values {
							if v != "" {
								b.Value("value", v)
							}
						}
					})
				} else if cv.Value == nil {
					b.Value("value", nil)
				} else {
					b.Value("value", *cv.Value)
				}
			})
		}
	})
}

// assignCustomFieldValues は custom_field_values=（params[prefix][custom_field_values]）と
// custom_fields=（API の [{"id": 1, "value": "x"}, ...]。acts_as_customizable の custom_fields=）。
func assignCustomFieldValues(cvs []principalCustomValue, p *httpx.Params) []principalCustomValue {
	if p == nil {
		return cvs
	}
	set := func(id string, v any) {
		for i := range cvs {
			f := cvs[i].Field
			if itoa(f.ID) != id || !f.Editable {
				continue
			}
			if f.Multiple {
				var vals []string
				for _, e := range anySlice(v) {
					if s := httpx.ValueString(e); s != "" {
						vals = append(vals, s)
					}
				}
				cvs[i].Values = vals
				continue
			}
			s := httpx.ValueString(v)
			cvs[i].Value = &s
		}
	}
	if m := p.Map("custom_field_values"); m != nil {
		for i := range cvs {
			id := itoa(cvs[i].Field.ID)
			if v, ok := m.Get(id); ok {
				set(id, v)
			}
		}
	}
	for _, e := range p.Slice("custom_fields") {
		em, ok := e.(*httpx.Params)
		if !ok {
			continue
		}
		if v, ok := em.Get("value"); ok {
			set(em.String("id"), v)
		}
	}
	return cvs
}

var intRe = regexp.MustCompile(`\A[+-]?\d+\z`)
var floatRe = regexp.MustCompile(`\A[+-]?\d+(\.\d*)?\z`)

// validateCustomFieldValues は acts_as_customizable の validate_custom_field_values
// （必須・形式・長さ・正規表現）。エラーはフィールド名に付く。
func validateCustomFieldValues(e *validation.Errors, cvs []principalCustomValue) {
	for _, cv := range cvs {
		f := cv.Field
		vals := cv.Values
		if !f.Multiple {
			vals = nil
			if cv.Value != nil {
				vals = []string{*cv.Value}
			}
		}
		present := false
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				present = true
			}
		}
		if f.IsRequired && !present {
			e.Add(f.Name, "blank")
			continue
		}
		for _, v := range vals {
			if v == "" {
				continue
			}
			if f.Regexp.Valid && f.Regexp.String != "" {
				if re, err := regexp.Compile(f.Regexp.String); err == nil && !re.MatchString(v) {
					e.Add(f.Name, "invalid")
				}
			}
			n := utf8.RuneCountInString(v)
			if f.MinLength.Valid && f.MinLength.Int64 > 0 && int64(n) < f.MinLength.Int64 {
				e.Add(f.Name, "too_short", "count", f.MinLength.Int64)
			}
			if f.MaxLength.Valid && f.MaxLength.Int64 > 0 && int64(n) > f.MaxLength.Int64 {
				e.Add(f.Name, "too_long", "count", f.MaxLength.Int64)
			}
			switch f.FieldFormat {
			case "int":
				if !intRe.MatchString(v) {
					e.Add(f.Name, "not_a_number")
				}
			case "float":
				if !floatRe.MatchString(v) {
					e.Add(f.Name, "invalid")
				}
			}
		}
	}
}

// customFieldTagWithLabel は custom_field_tag_with_label(prefix, custom_value)
// （string / int / float / link / date / text 形式。その他は文字列入力として扱う）。
func customFieldTagWithLabel(prefix string, cv principalCustomValue, hasError bool) rails.HTML {
	f := cv.Field
	id := prefix + "_custom_field_values_" + itoa(f.ID)
	name := prefix + "[custom_field_values][" + itoa(f.ID) + "]"
	css := f.CSSClasses()
	var placeholder any
	if f.Description.Valid && f.Description.String != "" {
		ph := f.Description.String
		if f.FieldFormat != "text" {
			ph = strings.ReplaceAll(ph, "\n", " ")
		}
		placeholder = ph
	}
	var value any
	if cv.Value != nil {
		value = *cv.Value
	}
	var tag rails.HTML
	switch f.FieldFormat {
	case "text":
		tag = rails.TextAreaTag(name, value, rails.NewHash("id", id, "class", css, "placeholder", placeholder, "rows", 3))
	case "date":
		tag = rails.DateFieldTag(name, value, rails.NewHash("id", id, "class", css, "size", 10, "placeholder", placeholder))
	default:
		tag = rails.TextFieldTag(name, value, rails.NewHash("id", id, "class", css, "placeholder", placeholder))
	}
	content := rails.ContentTag("span", f.Name, nil)
	if f.IsRequired {
		content += ` <span class="required">*</span>`
	}
	var class any
	if hasError {
		class = "error"
	}
	return rails.ContentTag("label", content, rails.NewHash("for", id, "class", class)) + tag
}

// gravatarAPIURL は GravatarHelper#gravatar_url(email, rating: nil, size: nil, default: Setting.gravatar_default)。
func gravatarAPIURL(email, def string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(email)))
	u := "https://www.gravatar.com/avatar/" + hex.EncodeToString(sum[:])
	if def != "" {
		u += "?default=" + url.QueryEscape(def)
	}
	return u
}
