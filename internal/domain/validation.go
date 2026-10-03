// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

import "strings"

// ValidationError は ActiveModel::Error 1 件（属性と activerecord.errors.messages.* のキー）。
type ValidationError struct {
	// Attr は属性名（"base" ならベースのエラーで、完全メッセージに属性名を付けない）。
	Attr string
	// Key は activerecord.errors.messages.<Key>（blank / taken / too_long ...）。
	// Message が空でなければ Key は使わない。
	Key string
	// Vars は翻訳の補間変数（too_long の count など）。
	Vars map[string]any
	// Message は翻訳済みの任意のメッセージ（errors.add(:base, "...") 相当）。
	Message string
}

// ValidationErrors は ActiveModel::Errors（追加順を保持）。
type ValidationErrors struct {
	List []ValidationError
}

// Add は errors.add(attr, key, vars)。
func (e *ValidationErrors) Add(attr, key string, vars map[string]any) {
	e.List = append(e.List, ValidationError{Attr: attr, Key: key, Vars: vars})
}

// AddMessage は翻訳済みメッセージのエラーを追加する。
func (e *ValidationErrors) AddMessage(attr, message string) {
	e.List = append(e.List, ValidationError{Attr: attr, Message: message})
}

// Any はエラーがあるか（errors.any?）。
func (e *ValidationErrors) Any() bool { return e != nil && len(e.List) > 0 }

// On は属性のエラー（キーまたはメッセージ）を返す（errors[attr] の代わり。空なら nil）。
func (e *ValidationErrors) On(attr string) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, x := range e.List {
		if x.Attr == attr {
			if x.Message != "" {
				out = append(out, x.Message)
			} else {
				out = append(out, x.Key)
			}
		}
	}
	return out
}

// Translator は翻訳関数（i18n.Localizer.L 相当）。
type Translator func(key string, args ...any) string

// FullMessages は errors.full_messages。属性名は Redmine の human_attribute_name
// （field_<attr>（末尾 _id を除く））、書式は errors.format（"%{attribute} %{message}"）。
func (e *ValidationErrors) FullMessages(l Translator) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, x := range e.List {
		msg := x.Message
		if msg == "" {
			var vars map[string]any
			if len(x.Vars) > 0 {
				vars = x.Vars
			}
			if vars != nil {
				msg = l("activerecord.errors.messages."+x.Key, vars)
			} else {
				msg = l("activerecord.errors.messages." + x.Key)
			}
		}
		if x.Attr == "base" {
			out = append(out, msg)
			continue
		}
		attr := HumanAttributeName(l, x.Attr)
		out = append(out, l("errors.format", map[string]any{"attribute": attr, "message": msg}))
	}
	return out
}

// HumanAttributeName は Redmine の ApplicationRecord.human_attribute_name
// （l("field_#{attr}"), 末尾の _id は除く）。
func HumanAttributeName(l Translator, attr string) string {
	name := strings.TrimSuffix(attr, "_id")
	return l("field_" + name)
}
