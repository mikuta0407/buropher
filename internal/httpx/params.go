// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package httpx は Redmine (Rails/Rack) 互換の HTTP リクエスト/レスポンス基盤を提供する。
//
// 主な機能:
//   - Rack 互換のネストパラメータ解析（Params）
//   - _method によるメソッド上書き
//   - .:format 拡張子 / Accept ヘッダによるフォーマット判定とルート登録ヘルパ
//   - サーバサイドセッション（Store）、flash、CSRF トークン
//   - back_url 検証付きリダイレクト
//   - リモート IP / ベース URL / リクエスト ID
//   - Redmine 互換のエラーレスポンス
package httpx

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Params は Rails の params（HashWithIndifferentAccess）に相当する順序付きマップ。
//
// 値として保持しうる型:
//   - nil（値なしの "foo" など）
//   - string
//   - []any（配列。要素は本リストのいずれか）
//   - *Params（ネストしたハッシュ）
//   - *UploadedFile（multipart のファイル）
//   - int64 / float64 / bool（JSON・XML ボディ由来）
//
// キーの挿入順は保持される（Ruby の Hash と同じ）。nil レシーバは空として振る舞う。
type Params struct {
	keys []string
	vals map[string]any
}

// NewParams は空の Params を返す。
func NewParams() *Params {
	return &Params{vals: map[string]any{}}
}

// Len はキー数を返す。
func (p *Params) Len() int {
	if p == nil {
		return 0
	}
	return len(p.keys)
}

// Keys は挿入順のキー一覧（コピー）を返す。
func (p *Params) Keys() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.keys...)
}

// Get はトップレベルのキーの値を返す。
func (p *Params) Get(key string) (any, bool) {
	if p == nil {
		return nil, false
	}
	v, ok := p.vals[key]
	return v, ok
}

// Set はキーに値を設定する。既存キーなら順序は維持する。
func (p *Params) Set(key string, v any) {
	if p.vals == nil {
		p.vals = map[string]any{}
	}
	if _, ok := p.vals[key]; !ok {
		p.keys = append(p.keys, key)
	}
	p.vals[key] = v
}

// Delete はキーを削除し、削除した値を返す。
func (p *Params) Delete(key string) any {
	if p == nil {
		return nil
	}
	v, ok := p.vals[key]
	if !ok {
		return nil
	}
	delete(p.vals, key)
	for i, k := range p.keys {
		if k == key {
			p.keys = append(p.keys[:i], p.keys[i+1:]...)
			break
		}
	}
	return v
}

// Each は挿入順に fn を呼ぶ。
func (p *Params) Each(fn func(key string, v any)) {
	if p == nil {
		return
	}
	for _, k := range p.keys {
		fn(k, p.vals[k])
	}
}

// Merge は other のトップレベルキーで上書きした新しい Params を返す（Hash#merge 相当、浅いマージ）。
func (p *Params) Merge(other *Params) *Params {
	out := p.shallowCopy()
	other.Each(func(k string, v any) { out.Set(k, v) })
	return out
}

func (p *Params) shallowCopy() *Params {
	out := NewParams()
	p.Each(func(k string, v any) { out.Set(k, v) })
	return out
}

// Clone は深いコピーを返す（UploadedFile はポインタ共有）。
func (p *Params) Clone() *Params {
	if p == nil {
		return NewParams()
	}
	out := NewParams()
	p.Each(func(k string, v any) { out.Set(k, cloneValue(v)) })
	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case *Params:
		return x.Clone()
	case []any:
		cp := make([]any, len(x))
		for i, e := range x {
			cp[i] = cloneValue(e)
		}
		return cp
	default:
		return v
	}
}

// Lookup はネストしたキー列で値を辿る。配列要素は数値文字列のインデックスで辿れる。
func (p *Params) Lookup(keys ...string) (any, bool) {
	var cur any = p
	if len(keys) == 0 {
		return p, p != nil
	}
	for _, k := range keys {
		switch c := cur.(type) {
		case *Params:
			v, ok := c.Get(k)
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// Has はキー（列）が存在するかを返す（値が nil でも true。Hash#key? 相当）。
func (p *Params) Has(keys ...string) bool {
	_, ok := p.Lookup(keys...)
	return ok
}

// Present は値が Rails の present?（nil・空文字・空白のみ・空配列・空ハッシュ以外）かを返す。
func (p *Params) Present(keys ...string) bool {
	v, ok := p.Lookup(keys...)
	return ok && !IsBlank(v)
}

// IsBlank は Rails の blank? 相当。
func IsBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case *Params:
		return x.Len() == 0
	case bool:
		return !x
	case *UploadedFile:
		return x == nil
	default:
		return false
	}
}

// String は値を Ruby の to_s 相当で文字列化して返す（ハッシュ・配列・ファイル・欠損は ""）。
func (p *Params) String(keys ...string) string {
	v, _ := p.Lookup(keys...)
	return ValueString(v)
}

// StringOK は値が文字列（または数値・真偽値）のときのみ ok=true を返す。
func (p *Params) StringOK(keys ...string) (string, bool) {
	v, ok := p.Lookup(keys...)
	if !ok {
		return "", false
	}
	switch v.(type) {
	case string, int64, float64, bool:
		return ValueString(v), true
	}
	return "", false
}

// ValueString はスカラー値を Ruby の to_s 相当で文字列化する。
func ValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return rubyFloatString(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// rubyFloatString は Ruby の Float#to_s に近い表現を返す（1.0 → "1.0"）。
func rubyFloatString(f float64) string {
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if math.IsNaN(f) {
		return "NaN"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEn") {
		s += ".0"
	}
	return s
}

var leadingIntRe = regexp.MustCompile(`^\s*[+-]?\d+(?:_\d+)*`)

// RubyToI は Ruby の String#to_i 相当（先頭の整数部分のみ、なければ 0）。
func RubyToI(s string) int64 {
	m := leadingIntRe.FindString(s)
	if m == "" {
		return 0
	}
	m = strings.ReplaceAll(strings.TrimSpace(m), "_", "")
	n, err := strconv.ParseInt(m, 10, 64)
	if err != nil {
		// 桁あふれ時は符号に応じて飽和させる
		if strings.HasPrefix(m, "-") {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	return n
}

// Int は値を Ruby の to_i 相当で整数化する（"12abc" → 12、欠損・非スカラーは 0）。
// Redmine のコントローラは params[:x].to_i を多用するため、こちらを既定とする。
func (p *Params) Int(keys ...string) int64 {
	v, _ := p.Lookup(keys...)
	return ValueInt(v)
}

// ValueInt はスカラー値を Ruby の to_i 相当で整数化する。
func ValueInt(v any) int64 {
	switch x := v.(type) {
	case string:
		return RubyToI(x)
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case bool:
		return 0
	default:
		return 0
	}
}

// IntStrict は値が厳密な整数表記（前後空白可）のときのみ ok=true を返す。
func (p *Params) IntStrict(keys ...string) (int64, bool) {
	v, ok := p.Lookup(keys...)
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n, err == nil
	case int64:
		return x, true
	case float64:
		if x == math.Trunc(x) {
			return int64(x), true
		}
	}
	return 0, false
}

// falseValues は ActiveModel::Type::Boolean::FALSE_VALUES 相当。
var falseValues = map[string]bool{
	"0": true, "f": true, "F": true, "false": true, "FALSE": true, "off": true, "OFF": true,
}

// Bool は ActiveModel の boolean キャスト相当で真偽値化する。
// 欠損・nil・空文字は false、"0"/"f"/"false"/"off"（大文字含む）も false、それ以外は true。
func (p *Params) Bool(keys ...string) bool {
	v, ok := p.Lookup(keys...)
	if !ok {
		return false
	}
	return ValueBool(v)
}

// ValueBool は ActiveModel の boolean キャスト相当。
func ValueBool(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		if x == "" {
			return false
		}
		return !falseValues[x]
	case int64:
		return x != 0
	case float64:
		return x != 0
	default:
		return true
	}
}

// Slice は値が配列ならそれを返す（配列でなければ nil）。
func (p *Params) Slice(keys ...string) []any {
	v, _ := p.Lookup(keys...)
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

// Strings は値を文字列スライスとして返す。Ruby の Array.wrap(x).map(&:to_s) 相当:
// 配列なら各要素のスカラー文字列、スカラーなら 1 要素、欠損・nil なら空。
// 配列中のハッシュ等の非スカラー要素は捨てる。
func (p *Params) Strings(keys ...string) []string {
	v, _ := p.Lookup(keys...)
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if isPermittedScalar(e) && e != nil {
				out = append(out, ValueString(e))
			}
		}
		return out
	case *Params, *UploadedFile:
		return nil
	default:
		return []string{ValueString(x)}
	}
}

// Ints は Strings の各要素を to_i したものを返す。
func (p *Params) Ints(keys ...string) []int64 {
	ss := p.Strings(keys...)
	out := make([]int64, len(ss))
	for i, s := range ss {
		out[i] = RubyToI(s)
	}
	return out
}

// Map は値がハッシュならそれを返す（なければ nil。nil の *Params は空として安全に使える）。
func (p *Params) Map(keys ...string) *Params {
	v, _ := p.Lookup(keys...)
	if m, ok := v.(*Params); ok {
		return m
	}
	return nil
}

// File は値がアップロードファイルならそれを返す。
func (p *Params) File(keys ...string) *UploadedFile {
	v, _ := p.Lookup(keys...)
	if f, ok := v.(*UploadedFile); ok {
		return f
	}
	return nil
}

// Only は指定キーのみを含む新しい Params を返す（Hash#slice 相当）。
func (p *Params) Only(keys ...string) *Params {
	out := NewParams()
	for _, k := range keys {
		if v, ok := p.Get(k); ok {
			out.Set(k, v)
		}
	}
	return out
}

// Except は指定キーを除いた新しい Params を返す。
func (p *Params) Except(keys ...string) *Params {
	skip := map[string]bool{}
	for _, k := range keys {
		skip[k] = true
	}
	out := NewParams()
	p.Each(func(k string, v any) {
		if !skip[k] {
			out.Set(k, v)
		}
	})
	return out
}

// ToMap はプレーンな map[string]any に再帰変換する（配列中のハッシュも変換）。
func (p *Params) ToMap() map[string]any {
	out := map[string]any{}
	p.Each(func(k string, v any) { out[k] = plainValue(v) })
	return out
}

func plainValue(v any) any {
	switch x := v.(type) {
	case *Params:
		return x.ToMap()
	case []any:
		cp := make([]any, len(x))
		for i, e := range x {
			cp[i] = plainValue(e)
		}
		return cp
	default:
		return v
	}
}

// MarshalJSON はキー順を保持して JSON 化する（テスト・デバッグ・セッション保存用）。
func (p *Params) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range p.Keys() {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(p.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ---- permit ----

// PermitField は Permit に渡すフィールド指定。Scalar / ScalarArray / Nested / AnyHash で生成する。
type PermitField struct {
	name     string
	kind     permitKind
	children []PermitField
}

type permitKind int

const (
	permitScalar permitKind = iota
	permitScalarArray
	permitNested
	permitAnyHash
)

// Scalar はスカラー値（文字列・数値・真偽値・nil・ファイル）を許可する（permit(:name)）。
func Scalar(name string) PermitField { return PermitField{name: name, kind: permitScalar} }

// ScalarArray はスカラーの配列を許可する（permit(name: [])）。
func ScalarArray(name string) PermitField { return PermitField{name: name, kind: permitScalarArray} }

// Nested はネストしたハッシュ（またはハッシュの配列、数値キーのハッシュ）を許可する
// （permit(name: [:a, :b])）。
func Nested(name string, children ...PermitField) PermitField {
	return PermitField{name: name, kind: permitNested, children: children}
}

// AnyHash は任意キーのハッシュ（値はスカラー・スカラー配列・ネストハッシュ）を許可する（permit(name: {})）。
func AnyHash(name string) PermitField { return PermitField{name: name, kind: permitAnyHash} }

// Permit は ActionController::Parameters#permit 相当で、許可されたフィールドのみを含む新しい Params を返す。
// 許可されないキー・型の値は黙って捨てる（Rails の既定 action_on_unpermitted_parameters=false と同じ）。
func (p *Params) Permit(fields ...PermitField) *Params {
	out := NewParams()
	for _, f := range fields {
		v, ok := p.Get(f.name)
		if !ok {
			continue
		}
		switch f.kind {
		case permitScalar:
			if isPermittedScalar(v) {
				out.Set(f.name, v)
			}
		case permitScalarArray:
			if a, ok := v.([]any); ok && allScalars(a) {
				out.Set(f.name, append([]any(nil), a...))
			}
		case permitNested:
			if r, ok := permitNestedValue(v, f.children); ok {
				out.Set(f.name, r)
			}
		case permitAnyHash:
			if m, ok := v.(*Params); ok {
				out.Set(f.name, permitAny(m))
			}
		}
	}
	return out
}

var numericKeyRe = regexp.MustCompile(`^-?\d+$`)

func permitNestedValue(v any, children []PermitField) (any, bool) {
	switch x := v.(type) {
	case *Params:
		// fields_for 形式（{"0"=>{...}, "1"=>{...}}）なら各要素に適用
		if x.Len() > 0 && allNumericKeys(x) {
			out := NewParams()
			x.Each(func(k string, e any) {
				if m, ok := e.(*Params); ok {
					out.Set(k, m.Permit(children...))
				}
			})
			return out, true
		}
		return x.Permit(children...), true
	case []any:
		out := []any{}
		for _, e := range x {
			if m, ok := e.(*Params); ok {
				out = append(out, m.Permit(children...))
			}
		}
		return out, true
	}
	return nil, false
}

func allNumericKeys(p *Params) bool {
	for _, k := range p.keys {
		if !numericKeyRe.MatchString(k) {
			return false
		}
	}
	return true
}

func permitAny(m *Params) *Params {
	out := NewParams()
	m.Each(func(k string, v any) {
		switch x := v.(type) {
		case *Params:
			out.Set(k, permitAny(x))
		case []any:
			if allScalars(x) {
				out.Set(k, append([]any(nil), x...))
			}
		default:
			if isPermittedScalar(v) {
				out.Set(k, v)
			}
		}
	})
	return out
}

func isPermittedScalar(v any) bool {
	switch v.(type) {
	case nil, string, int64, int, float64, bool, *UploadedFile:
		return true
	}
	return false
}

func allScalars(a []any) bool {
	for _, e := range a {
		if !isPermittedScalar(e) {
			return false
		}
	}
	return true
}
