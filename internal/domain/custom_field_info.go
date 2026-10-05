// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

import (
	"strconv"
	"time"
)

// CustomFieldInfo はカスタムフィールドの定義のうち、トラッカー・列挙の管理画面と
// 列挙の値の入力欄（custom_field_tag）に必要な属性だけを持つ軽量な型。
//
// TODO: カスタムフィールド管理（CustomFieldsController / Redmine::FieldFormat）の移植で
// 完全な型ができたら置き換える。
type CustomFieldInfo struct {
	ID             int64
	OwnerKind      string
	Name           string
	Description    *string
	FieldFormat    string
	IsRequired     bool
	IsForAll       bool
	Visible        bool
	Multiple       bool
	DefaultValue   *string
	Position       int
	PossibleValues []string
	// EditTagStyle は format_settings.edit_tag_style（"", "check_box", "radio"）。
	EditTagStyle string
	// DefaultValueMode は format_settings.default_value_mode（日付書式の "fixed_date" / "date_offset"）。
	DefaultValueMode string
}

// DefaultValueOn は CustomField#default_value（日付書式の相対既定値は today からの日数として評価する）。
func (c *CustomFieldInfo) DefaultValueOn(today time.Time) *string {
	return CustomFieldDefaultValue(c.FieldFormat, c.DefaultValueMode, c.DefaultValue, today)
}

// CSSClasses は CustomField#css_classes。
func (c *CustomFieldInfo) CSSClasses() string {
	return c.FieldFormat + "_cf cf_" + strconv.FormatInt(c.ID, 10)
}

// DescriptionString は description（nil なら空文字列）。
func (c *CustomFieldInfo) DescriptionString() string {
	if c.Description == nil {
		return ""
	}
	return *c.Description
}

// DefaultValueString は default_value（nil なら空文字列）。
func (c *CustomFieldInfo) DefaultValueString() string {
	if c.DefaultValue == nil {
		return ""
	}
	return *c.DefaultValue
}

// CustomFieldValue は CustomFieldValue（カスタムフィールドと値。複数値は Values）。
type CustomFieldValue struct {
	Field  *CustomFieldInfo
	Values []string
}

// Value は単一値（複数値なら先頭。無ければ空文字列）。
func (v *CustomFieldValue) Value() string {
	if len(v.Values) == 0 {
		return ""
	}
	return v.Values[0]
}
