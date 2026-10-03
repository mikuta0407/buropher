// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import (
	"reflect"
	"strings"
	"unicode"
)

// Sender は Ruby の public_send 相当を自前で提供したい型が実装するインターフェース。
// Send が ok=false を返した場合はリフレクションによる解決にフォールバックする。
type Sender interface {
	Send(method string) (any, bool)
}

// Send は obj.public_send(method) を近似する。
//   - Sender 実装ならそれを使う
//   - *Hash / map[string]any ならキー参照
//   - 構造体なら snake_case → CamelCase 変換したメソッド（引数なし）またはフィールド
//     （"id" → ID / Id、"to_s" → String()、"name" → Name、"persisted?" → Persisted / IsPersisted）
//
// 解決できなければ nil を返す。
func Send(obj any, method string) any {
	v, _ := SendOK(obj, method)
	return v
}

// SendOK は Send に解決できたかどうかを加えたもの。
func SendOK(obj any, method string) (any, bool) {
	if obj == nil {
		return nil, false
	}
	if s, ok := obj.(Sender); ok {
		if v, ok := s.Send(method); ok {
			return v, true
		}
	}
	switch x := obj.(type) {
	case *Hash:
		return x.Lookup(method)
	case Hash:
		return x.Lookup(method)
	case map[string]any:
		v, ok := x[method]
		return v, ok
	}
	if method == "to_s" {
		return ToS(obj), true
	}
	rv := reflect.ValueOf(obj)
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		mv := rv.MapIndex(reflect.ValueOf(method).Convert(rv.Type().Key()))
		if mv.IsValid() {
			return mv.Interface(), true
		}
		return nil, false
	}
	for _, name := range goNames(method) {
		if m := rv.MethodByName(name); m.IsValid() && m.Type().NumIn() == 0 && m.Type().NumOut() >= 1 {
			return m.Call(nil)[0].Interface(), true
		}
		ev := rv
		for ev.Kind() == reflect.Pointer || ev.Kind() == reflect.Interface {
			if ev.IsNil() {
				return nil, false
			}
			ev = ev.Elem()
		}
		if ev.Kind() == reflect.Struct {
			if f := ev.FieldByName(name); f.IsValid() && f.CanInterface() {
				return f.Interface(), true
			}
		}
	}
	return nil, false
}

// initialisms は Go の命名慣習で大文字化される略語。
var initialisms = map[string]string{
	"id": "ID", "url": "URL", "html": "HTML", "uri": "URI", "api": "API", "css": "CSS",
	"ip": "IP", "json": "JSON", "xml": "XML", "ldap": "LDAP", "uuid": "UUID",
}

// goNames は snake_case のメソッド名から候補となる Go の識別子を返す。
func goNames(method string) []string {
	m := method
	pred := strings.HasSuffix(m, "?")
	m = strings.TrimSuffix(m, "?")
	parts := strings.Split(m, "_")
	var plain, init strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		plain.WriteString(string(r))
		if up, ok := initialisms[p]; ok {
			init.WriteString(up)
		} else {
			init.WriteString(string(r))
		}
	}
	names := []string{init.String()}
	if plain.String() != init.String() {
		names = append(names, plain.String())
	}
	if pred {
		names = append(names, "Is"+init.String())
	}
	return names
}
