// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import (
	"fmt"
	"reflect"
	"sort"
)

// KV は Hash の 1 エントリ。
type KV struct {
	Key   string
	Value any
}

// Hash は挿入順序を保持するハッシュ（Ruby の Hash 相当）。
// Rails ヘルパーの属性出力順序はハッシュの挿入順に従うため、オプション引数は Hash で渡す。
// キーはすべて文字列として扱う（Ruby の stringify_keys 後と同じ扱い）。
// ゼロ値は空ハッシュとして使える。値として nil を保持でき、nil の属性は出力されない
// （ただしキーの位置は保持される — Ruby の {"id" => nil} と同じ）。
type Hash struct {
	kv []KV
}

// NewHash は "key", value, "key2", value2, ... の並びから Hash を作る。
// 奇数個の場合は最後のキーの値を nil とする。
func NewHash(pairs ...any) *Hash {
	h := &Hash{}
	for i := 0; i < len(pairs); i += 2 {
		k := ToS(pairs[i])
		var v any
		if i+1 < len(pairs) {
			v = pairs[i+1]
		}
		h.Set(k, v)
	}
	return h
}

// Len はエントリ数を返す。
func (h *Hash) Len() int {
	if h == nil {
		return 0
	}
	return len(h.kv)
}

// Entries はエントリのスライス（挿入順）を返す。呼び出し側で変更しないこと。
func (h *Hash) Entries() []KV {
	if h == nil {
		return nil
	}
	return h.kv
}

// Keys はキーの一覧を返す。
func (h *Hash) Keys() []string {
	out := make([]string, 0, h.Len())
	for _, e := range h.Entries() {
		out = append(out, e.Key)
	}
	return out
}

func (h *Hash) index(k string) int {
	if h == nil {
		return -1
	}
	for i := range h.kv {
		if h.kv[i].Key == k {
			return i
		}
	}
	return -1
}

// Has はキーの存在を返す（key? / has_key?）。
func (h *Hash) Has(k string) bool { return h.index(k) >= 0 }

// Get は値を返す（存在しなければ nil）。
func (h *Hash) Get(k string) any {
	if i := h.index(k); i >= 0 {
		return h.kv[i].Value
	}
	return nil
}

// Lookup は値と存在有無を返す。
func (h *Hash) Lookup(k string) (any, bool) {
	if i := h.index(k); i >= 0 {
		return h.kv[i].Value, true
	}
	return nil, false
}

// Fetch は存在すれば値を、なければ def を返す（Hash#fetch(k, def)）。
func (h *Hash) Fetch(k string, def any) any {
	if v, ok := h.Lookup(k); ok {
		return v
	}
	return def
}

// Set は値を設定する。既存キーなら位置を保ったまま値を置換し、新規キーなら末尾に追加する。
func (h *Hash) Set(k string, v any) *Hash {
	if i := h.index(k); i >= 0 {
		h.kv[i].Value = v
		return h
	}
	h.kv = append(h.kv, KV{k, v})
	return h
}

// SetDefault はキーが無いときだけ設定する（h[k] ||= v ではなく fetch ベースの存在判定）。
func (h *Hash) SetDefault(k string, v any) *Hash {
	if !h.Has(k) {
		h.Set(k, v)
	}
	return h
}

// Delete はキーを削除し、その値と存在有無を返す。
func (h *Hash) Delete(k string) (any, bool) {
	i := h.index(k)
	if i < 0 {
		return nil, false
	}
	v := h.kv[i].Value
	h.kv = append(h.kv[:i:i], h.kv[i+1:]...)
	return v, true
}

// Del は Delete の値だけを返す版（Ruby の h.delete(k)）。
func (h *Hash) Del(k string) any {
	v, _ := h.Delete(k)
	return v
}

// Clone は浅いコピーを返す。nil レシーバでも空ハッシュを返す。
func (h *Hash) Clone() *Hash {
	n := &Hash{}
	if h != nil {
		n.kv = append(make([]KV, 0, len(h.kv)), h.kv...)
	}
	return n
}

// Update は other のエントリで上書き・追加する（Hash#update / merge!）。
func (h *Hash) Update(other *Hash) *Hash {
	for _, e := range other.Entries() {
		h.Set(e.Key, e.Value)
	}
	return h
}

// Merge は新しいハッシュに other をマージして返す（Hash#merge）。
func (h *Hash) Merge(other *Hash) *Hash { return h.Clone().Update(other) }

// ReverseMerge は other.merge(self) を返す（ActiveSupport の reverse_merge）。
func (h *Hash) ReverseMerge(other *Hash) *Hash { return other.Clone().Update(h) }

// Except は指定キーを除いた新しいハッシュを返す。
func (h *Hash) Except(keys ...string) *Hash {
	n := h.Clone()
	for _, k := range keys {
		n.Delete(k)
	}
	return n
}

// Slice は指定キーだけを（指定順で）含む新しいハッシュを返す（Hash#slice）。
func (h *Hash) Slice(keys ...string) *Hash {
	n := &Hash{}
	for _, k := range keys {
		if v, ok := h.Lookup(k); ok {
			n.Set(k, v)
		}
	}
	return n
}

// String はデバッグ用表現。
func (h *Hash) String() string {
	s := "{"
	for i, e := range h.Entries() {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%q=>%#v", e.Key, e.Value)
	}
	return s + "}"
}

// ToHash は任意の値をオプション用 Hash に変換する。
// *Hash / Hash はそのまま（*Hash は複製しない）、map[string]any 等はキーをソートして
// 変換する（Go のマップは順序を持たないため、順序が重要な場合は Hash を使うこと）。
// nil は空ハッシュ。変換できない場合は ok=false。
func ToHash(v any) (*Hash, bool) {
	switch x := v.(type) {
	case nil:
		return &Hash{}, true
	case *Hash:
		if x == nil {
			return &Hash{}, true
		}
		return x, true
	case Hash:
		return &x, true
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		h := &Hash{}
		for _, k := range keys {
			h.Set(k, x[k])
		}
		return h, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		keys := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		h := &Hash{}
		for _, k := range keys {
			h.Set(k, rv.MapIndex(reflect.ValueOf(k).Convert(rv.Type().Key())).Interface())
		}
		return h, true
	}
	return nil, false
}

// isHash は v がハッシュとして扱える値かを返す。
func isHash(v any) bool {
	switch v.(type) {
	case *Hash, Hash, map[string]any:
		return v != nil
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Map
}

// optHash は可変長引数の i 番目をオプションハッシュとして取り出す（複製を返す）。
func optHash(args []any, i int) *Hash {
	if i >= len(args) {
		return &Hash{}
	}
	h, ok := ToHash(args[i])
	if !ok {
		return &Hash{}
	}
	return h.Clone()
}
