// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package validation は ActiveModel::Errors（モデルの検証エラー）の移植。
//
// Redmine のモデルの検証（validates_presence_of 等）は Go ではモデルごとの Validate 関数で行い、
// 結果を *Errors に追加する。表示（error_messages_for / API の errors 配列）は FullMessages で
// "属性名 メッセージ" の形にする:
//
//	errs := &validation.Errors{Model: "user"}
//	errs.Add("login", "blank")                              // activerecord.errors.messages.blank
//	errs.Add("password", "too_short", "count", 8)           // %{count} の補間
//	errs.AddMessage("base", "Some literal message")         // 翻訳しないメッセージ
//	msgs := errs.FullMessages(loc)                         // ["Login cannot be blank", ...]
//
// 属性名は ApplicationRecord.human_attribute_name と同じく field_<model>_<attr>、field_<attr>
// （末尾の _id と "xxx." の接頭辞を除く）の順に翻訳を探し、無ければ humanize する。
// 属性名の翻訳を上書きしたい場合（Group の lastname → name など）は AttrNames を使う。
package validation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/i18n"
)

// Error は 1 件の検証エラー。
type Error struct {
	// Attr は属性名（"base" は属性なし）。
	Attr string
	// Key は activerecord.errors.messages.<Key> の翻訳キー（Message が空のとき使う）。
	Key string
	// Vars は翻訳の補間変数（count など）。
	Vars map[string]any
	// Message は翻訳しないメッセージ（errors.add(:base, "...") 相当）。
	Message string
}

// Errors は ActiveModel::Errors。
type Errors struct {
	// Model はモデル名（user, group, email_address ...。human_attribute_name の field_<model>_<attr> に使う）。
	Model string
	// AttrNames は属性名の翻訳キーの上書き（attr → i18n キー）。
	AttrNames map[string]string
	list      []Error
}

// New は model の Errors を返す。
func New(model string) *Errors { return &Errors{Model: model} }

// Add は errors.add(attr, key, vars...)。vars は "count", 8, ... の組。
func (e *Errors) Add(attr, key string, vars ...any) {
	var m map[string]any
	if len(vars) > 0 {
		m = map[string]any{}
		for i := 0; i+1 < len(vars); i += 2 {
			m[fmt.Sprint(vars[i])] = vars[i+1]
		}
	}
	e.list = append(e.list, Error{Attr: attr, Key: key, Vars: m})
}

// AddMessage は翻訳済み（または翻訳しない）メッセージを追加する。
func (e *Errors) AddMessage(attr, message string) {
	e.list = append(e.list, Error{Attr: attr, Message: message})
}

// Merge は other のエラーを attr を付け替えて追加する（関連モデルのエラーを取り込む。attr が空ならそのまま）。
func (e *Errors) Merge(other *Errors, attr string) {
	if other == nil {
		return
	}
	for _, x := range other.list {
		if attr != "" {
			x.Attr = attr
		}
		e.list = append(e.list, x)
	}
}

// Any は errors.any?。
func (e *Errors) Any() bool { return e != nil && len(e.list) > 0 }

// Empty は errors.empty?。
func (e *Errors) Empty() bool { return !e.Any() }

// List はエラーを追加順に返す。
func (e *Errors) List() []Error {
	if e == nil {
		return nil
	}
	return e.list
}

// Include は errors.include?(attr)。
func (e *Errors) Include(attr string) bool {
	if e == nil {
		return false
	}
	for _, x := range e.list {
		if x.Attr == attr {
			return true
		}
	}
	return false
}

// HasKey は attr に key のエラーがあれば true（errors.added?(attr, key)）。
func (e *Errors) HasKey(attr, key string) bool {
	if e == nil {
		return false
	}
	for _, x := range e.list {
		if x.Attr == attr && x.Key == key {
			return true
		}
	}
	return false
}

// Clear は errors.clear。
func (e *Errors) Clear() {
	if e != nil {
		e.list = nil
	}
}

// Message は 1 件のエラーメッセージ（属性名なし）。
func Message(l *i18n.Localizer, x Error) string {
	if x.Message != "" || x.Key == "" {
		return x.Message
	}
	vars := i18n.Vars{}
	for k, v := range x.Vars {
		vars[k] = v
	}
	if l == nil {
		return x.Key
	}
	for _, k := range []string{"activerecord.errors.messages." + x.Key, "errors.messages." + x.Key} {
		if l.Bundle.Exists(l.Lang, k) {
			return l.Bundle.T(l.Lang, k, vars)
		}
	}
	return l.Bundle.T(l.Lang, "activerecord.errors.messages."+x.Key, vars)
}

// Messages は attr のメッセージ（errors[attr]）。
func (e *Errors) Messages(l *i18n.Localizer, attr string) []string {
	var out []string
	if e == nil {
		return nil
	}
	for _, x := range e.list {
		if x.Attr == attr {
			out = append(out, Message(l, x))
		}
	}
	return out
}

var idSuffixRe = regexp.MustCompile(`_id$`)
var prefixRe = regexp.MustCompile(`^.+\.`)

// HumanAttributeName は ApplicationRecord.human_attribute_name(attr)。
func (e *Errors) HumanAttributeName(l *i18n.Localizer, attr string) string {
	if e != nil && e.AttrNames != nil {
		if k, ok := e.AttrNames[attr]; ok {
			if l == nil {
				return k
			}
			return l.L(k)
		}
	}
	model := ""
	if e != nil {
		model = e.Model
	}
	return HumanAttributeName(l, model, attr)
}

// HumanAttributeName は ApplicationRecord.human_attribute_name(attr)（model はクラス名の underscore）。
func HumanAttributeName(l *i18n.Localizer, model, attr string) string {
	prepared := prefixRe.ReplaceAllString(idSuffixRe.ReplaceAllString(attr, ""), "")
	if l != nil {
		keys := []string{"activerecord.attributes." + model + "." + attr}
		if model != "" {
			keys = append(keys, "field_"+model+"_"+prepared)
		}
		keys = append(keys, "field_"+prepared)
		for _, k := range keys {
			if l.Bundle.Exists(l.Lang, k) {
				return l.L(k)
			}
		}
	}
	return humanize(attr)
}

// humanize は ActiveSupport の humanize（"_id" を除き、"_" を空白に、先頭を大文字に）。
func humanize(s string) string {
	s = strings.TrimSuffix(s, "_id")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r := []rune(strings.ToLower(s))
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// FullMessage は errors.full_message(attr, message)（"%{attribute} %{message}"。base は message のみ）。
func (e *Errors) FullMessage(l *i18n.Localizer, attr, message string) string {
	if attr == "base" {
		return message
	}
	name := e.HumanAttributeName(l, attr)
	format := "%{attribute} %{message}"
	if l != nil && l.Bundle.Exists(l.Lang, "errors.format") {
		if s, ok := l.Bundle.Lookup(l.Lang, "errors.format").(string); ok {
			format = s
		}
	}
	return i18n.Interpolate(format, i18n.Vars{"attribute": name, "message": message})
}

// FullMessages は errors.full_messages。
func (e *Errors) FullMessages(l *i18n.Localizer) []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.list))
	for _, x := range e.list {
		out = append(out, e.FullMessage(l, x.Attr, Message(l, x)))
	}
	return out
}
