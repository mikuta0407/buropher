// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package apibuilder は Redmine::Views::Builders（lib/redmine/views/builders/*.rb）の移植。
//
// Redmine の REST API は .api.rsb テンプレートの DSL（api.id 1 / api.user do ... end /
// api.array :users do ... end）から、同じ構造を JSON（Builders::Json = Structure#to_json）と
// XML（Builders::Xml = Builder::XmlMarkup）の両方に出力する。このパッケージは DSL を
// Go のメソッドに置き換えたもので、出力はキー順・型・空要素の扱いまで Redmine と同一にする。
//
//	ERB (.api.rsb)                              Go
//	api.array :users, api_meta(...) do          b.Array("users", meta, func() {
//	  api.user do                                 b.Object("user", func() {
//	    api.id user.id                              b.Value("id", u.ID)
//	    api.project :id => 1, :name => "x"          b.Attrs("project", apibuilder.A("id", 1, "name", "x"))
//	    api.custom_field attrs do ... end           b.ObjectAttrs("custom_field", attrs, func() { ... })
//	  end                                         })
//	end                                         })
//
// 値の型: nil（JSON null / XML 空要素 <x/>）、bool、整数、浮動小数（Ruby の Float#to_s と同じ表記）、
// 文字列、time.Time / *time.Time（xmlschema(0) = "2006-01-02T15:04:05Z"。UTC に変換する）。
// それ以外は fmt.Sprint で文字列化する。
//
// JSON の文字列は Rails の to_json と同じく < > & と U+2028 / U+2029 を \u エスケープする。
// XML は Builder::XmlMarkup と同じく <?xml ...?> の後にインデントなしで出力し、
// テキストは XChar.encode、属性は " と改行も文字参照にする（httpx.XMLEscapeText / XMLEscapeAttr）。
//
// JSONP（callback / jsonp パラメータ、Setting.jsonp_enabled）は JSON の Output 時に Callback を
// 指定すると callback(...) で包み、ContentType は application/javascript になる。
package apibuilder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/httpx"
)

// KV は属性の 1 組（Ruby のハッシュの 1 エントリ）。
type KV struct {
	Key   string
	Value any
}

// Attrs は挿入順を保つ属性（Ruby のハッシュ）。
type Attrs []KV

// A は key, value, key, value ... から Attrs を作る。
func A(kv ...any) Attrs {
	out := make(Attrs, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, KV{Key: fmt.Sprint(kv[i]), Value: kv[i+1]})
	}
	return out
}

// Set は key の値を置き換える（無ければ末尾に追加。Hash#[]= / merge! と同じ順序）。
func (a Attrs) Set(key string, v any) Attrs {
	for i := range a {
		if a[i].Key == key {
			a[i].Value = v
			return a
		}
	}
	return append(a, KV{key, v})
}

// Builder は .api.rsb の api オブジェクト。
type Builder interface {
	// Value は api.name value（スカラー値）。配列の中では要素として追加される。
	Value(name string, v any)
	// Attrs は api.name :k => v（属性だけの要素）。配列の中では要素として追加される。
	Attrs(name string, attrs Attrs)
	// Object は api.name do ... end。
	Object(name string, fn func())
	// ObjectAttrs は api.name attrs do ... end（属性付きの要素。JSON では属性が先に並ぶ）。
	ObjectAttrs(name string, attrs Attrs, fn func())
	// Array は api.array name, meta do ... end。meta は JSON では配列の後のキー、XML では属性
	// （XML では最後に type="array" が付く）。
	Array(name string, meta Attrs, fn func())
	// Output は出力（本文）を返す。
	Output() []byte
	// ContentType は Content-Type ヘッダの値（charset 付き）。
	ContentType() string
	// Format は "json" / "xml"。
	Format() string
}

// Options は New のオプション。
type Options struct {
	// Callback は JSONP のコールバック名（JSON のみ。空なら JSONP にしない）。
	// httpx.JSONPCallback(r, Setting.jsonp_enabled?) の結果を渡す。
	Callback string
}

// New は format（"json" / "xml"）の Builder を返す。それ以外は nil（Redmine は 406）。
func New(format string, opts ...Options) Builder {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	switch format {
	case "json":
		return &jsonBuilder{stack: []*node{newHash()}, callback: o.Callback}
	case "xml":
		b := &xmlBuilder{}
		b.buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
		return b
	}
	return nil
}

// NoFormatMessage は Builders.for が未知のフォーマットで返す本文（406）。
const NoFormatMessage = "We couldn't handle your request, sorry. If you were trying to access the API, make sure to append .json or .xml to your request URL.\n"

// ---------------------------------------------------------------- JSON（Builders::Structure）

// node は Structure の @struct の要素（Hash か Array）。
type node struct {
	isArray bool
	keys    []string
	vals    map[string]any
	items   []any
}

func newHash() *node  { return &node{vals: map[string]any{}} }
func newArray() *node { return &node{isArray: true} }

func (n *node) set(k string, v any) {
	if _, ok := n.vals[k]; !ok {
		n.keys = append(n.keys, k)
	}
	n.vals[k] = v
}

func hashFromAttrs(a Attrs) *node {
	h := newHash()
	for _, kv := range a {
		h.set(kv.Key, kv.Value)
	}
	return h
}

type jsonBuilder struct {
	stack    []*node
	callback string
}

func (b *jsonBuilder) last() *node { return b.stack[len(b.stack)-1] }

func (b *jsonBuilder) Value(name string, v any) {
	if l := b.last(); l.isArray {
		l.items = append(l.items, v)
	} else {
		l.set(name, v)
	}
}

func (b *jsonBuilder) Attrs(name string, attrs Attrs) {
	h := hashFromAttrs(attrs)
	if l := b.last(); l.isArray {
		l.items = append(l.items, h)
	} else {
		l.set(name, h)
	}
}

func (b *jsonBuilder) Object(name string, fn func()) { b.ObjectAttrs(name, nil, fn) }

func (b *jsonBuilder) ObjectAttrs(name string, attrs Attrs, fn func()) {
	h := hashFromAttrs(attrs)
	b.stack = append(b.stack, h)
	if fn != nil {
		fn()
	}
	b.stack = b.stack[:len(b.stack)-1]
	l := b.last()
	if l.isArray {
		l.items = append(l.items, h)
		return
	}
	// 既に同名のハッシュがあればマージする（Structure#method_missing と同じ）
	if cur, ok := l.vals[name].(*node); ok && !cur.isArray {
		for _, k := range h.keys {
			cur.set(k, h.vals[k])
		}
		return
	}
	l.set(name, h)
}

func (b *jsonBuilder) Array(name string, meta Attrs, fn func()) {
	arr := newArray()
	b.stack = append(b.stack, arr)
	if fn != nil {
		fn()
	}
	b.stack = b.stack[:len(b.stack)-1]
	l := b.last()
	if l.isArray {
		// Structure では配列の中の array は Hash#[]= が無いため例外になる。ここでは要素として追加する。
		l.items = append(l.items, arr)
		return
	}
	l.set(name, arr)
	for _, kv := range meta {
		l.set(kv.Key, kv.Value)
	}
}

func (b *jsonBuilder) Output() []byte {
	var buf bytes.Buffer
	writeJSON(&buf, b.stack[0])
	if b.callback != "" {
		return []byte(b.callback + "(" + buf.String() + ")")
	}
	return buf.Bytes()
}

func (b *jsonBuilder) ContentType() string {
	if b.callback != "" {
		return "application/javascript; charset=utf-8"
	}
	return "application/json; charset=utf-8"
}

func (b *jsonBuilder) Format() string { return "json" }

func writeJSON(buf *bytes.Buffer, v any) {
	switch x := v.(type) {
	case *node:
		if x.isArray {
			buf.WriteByte('[')
			for i, it := range x.items {
				if i > 0 {
					buf.WriteByte(',')
				}
				writeJSON(buf, it)
			}
			buf.WriteByte(']')
			return
		}
		buf.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, k)
			buf.WriteByte(':')
			writeJSON(buf, x.vals[k])
		}
		buf.WriteByte('}')
	case Attrs:
		writeJSON(buf, hashFromAttrs(x))
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(x))
	case string:
		writeJSONString(buf, x)
	case float64:
		buf.WriteString(rubyFloat(x))
	case float32:
		buf.WriteString(rubyFloat(float64(x)))
	case time.Time:
		writeJSONString(buf, xmlschema(x))
	case *time.Time:
		if x == nil {
			buf.WriteString("null")
		} else {
			writeJSONString(buf, xmlschema(*x))
		}
	case *string:
		if x == nil {
			buf.WriteString("null")
		} else {
			writeJSONString(buf, *x)
		}
	case *int64:
		if x == nil {
			buf.WriteString("null")
		} else {
			buf.WriteString(strconv.FormatInt(*x, 10))
		}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		fmt.Fprint(buf, x)
	case []any:
		buf.WriteByte('[')
		for i, it := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSON(buf, it)
		}
		buf.WriteByte(']')
	case []string:
		buf.WriteByte('[')
		for i, it := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, it)
		}
		buf.WriteByte(']')
	default:
		writeJSONString(buf, fmt.Sprint(x))
	}
}

// writeJSONString は ActiveSupport の JSON エンコード（<>& と U+2028/2029 を \u エスケープ）。
func writeJSONString(buf *bytes.Buffer, s string) {
	// encoding/json は既定で <>& と U+2028/2029 を \u00xx 形式にする（Rails と同じ）
	b, _ := json.Marshal(s)
	buf.Write(b)
}

// ---------------------------------------------------------------- XML（Builders::Xml）

type xmlBuilder struct {
	buf bytes.Buffer
}

func (b *xmlBuilder) startTag(name string, attrs Attrs, selfClose bool) {
	b.buf.WriteByte('<')
	b.buf.WriteString(name)
	for _, kv := range attrs {
		// Builder は nil の属性も to_s（空文字）で出力する
		b.buf.WriteByte(' ')
		b.buf.WriteString(kv.Key)
		b.buf.WriteString(`="`)
		b.buf.WriteString(httpx.XMLEscapeAttr(xmlText(kv.Value)))
		b.buf.WriteByte('"')
	}
	if selfClose {
		b.buf.WriteString("/>")
	} else {
		b.buf.WriteByte('>')
	}
}

func (b *xmlBuilder) endTag(name string) {
	b.buf.WriteString("</")
	b.buf.WriteString(name)
	b.buf.WriteByte('>')
}

func isNil(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case *time.Time:
		return x == nil
	case *string:
		return x == nil
	case *int64:
		return x == nil
	}
	return false
}

func (b *xmlBuilder) Value(name string, v any) {
	if isNil(v) {
		b.startTag(name, nil, true)
		return
	}
	b.startTag(name, nil, false)
	b.buf.WriteString(httpx.XMLEscapeText(xmlText(v)))
	b.endTag(name)
}

func (b *xmlBuilder) Attrs(name string, attrs Attrs) { b.startTag(name, attrs, true) }

func (b *xmlBuilder) Object(name string, fn func()) { b.ObjectAttrs(name, nil, fn) }

func (b *xmlBuilder) ObjectAttrs(name string, attrs Attrs, fn func()) {
	b.startTag(name, attrs, false)
	if fn != nil {
		fn()
	}
	b.endTag(name)
}

func (b *xmlBuilder) Array(name string, meta Attrs, fn func()) {
	attrs := make(Attrs, 0, len(meta)+1)
	attrs = append(attrs, meta...)
	attrs = attrs.Set("type", "array")
	b.ObjectAttrs(name, attrs, fn)
}

func (b *xmlBuilder) Output() []byte      { return b.buf.Bytes() }
func (b *xmlBuilder) ContentType() string { return "application/xml; charset=utf-8" }
func (b *xmlBuilder) Format() string      { return "xml" }

// xmlText は Builder の arg.to_s（Time は xmlschema）。
func xmlText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return rubyFloat(x)
	case float32:
		return rubyFloat(float64(x))
	case time.Time:
		return xmlschema(x)
	case nil:
		return ""
	case *time.Time:
		if x == nil {
			return ""
		}
		return xmlschema(*x)
	case *string:
		if x == nil {
			return ""
		}
		return *x
	case *int64:
		if x == nil {
			return ""
		}
		return strconv.FormatInt(*x, 10)
	}
	return fmt.Sprint(v)
}

// ---------------------------------------------------------------- 共通

// xmlschema は Time#xmlschema(0)（UTC なら末尾 Z）。
func xmlschema(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// rubyFloat は Ruby の Float#to_s（整数値でも "1.0"、指数表記は 1e16 以上・1e-4 未満）。
func rubyFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	abs := math.Abs(f)
	if abs != 0 && (abs >= 1e16 || abs < 1e-4) {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		sign := exp[0]
		exp = strings.TrimLeft(exp[1:], "0")
		if len(exp) < 2 {
			exp = strings.Repeat("0", 2-len(exp)) + exp
		}
		return mant + "e" + string(sign) + exp
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
