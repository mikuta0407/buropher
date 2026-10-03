// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import "net/http"

// flashSessionKey はセッション内の flash の保存キー（Rails と同じ "flash"）。
const flashSessionKey = "flash"

// FlashEntry は flash の 1 件。
type FlashEntry struct {
	Key   string
	Value string
}

// Flash は ActionDispatch::Flash::FlashHash の移植。
//
// 読み込み時に前リクエストから引き継いだキーはすべて破棄予定（discard）になり、
// このリクエストで Set したキーだけが次のリクエストへ引き継がれる。
// Now で設定した値はこのリクエストのみ有効。順序は挿入順（Redmine の render_flash_messages の表示順）。
type Flash struct {
	entries []FlashEntry
	discard map[string]bool
}

// FlashOf はリクエストのセッションの flash を返す（セッションがなければ一時的な空 flash）。
func FlashOf(r *http.Request) *Flash {
	if s := SessionOf(r); s != nil {
		return s.Flash()
	}
	return &Flash{discard: map[string]bool{}}
}

// Flash はセッションの flash を（初回アクセス時に読み込んで）返す。
// 一度もアクセスしなければ、セッション内の flash は次のリクエストへそのまま残る（Rails と同じ）。
func (s *Session) Flash() *Flash {
	if s.flash == nil {
		s.flash = flashFromSession(s.rec.Data[flashSessionKey])
	}
	return s.flash
}

// flashFromSession は FlashHash.from_session_value 相当。読み込んだキーは全て discard 扱い。
func flashFromSession(v any) *Flash {
	f := &Flash{discard: map[string]bool{}}
	m, ok := v.(map[string]any)
	if !ok {
		return f
	}
	drop := map[string]bool{}
	if d, ok := m["discard"].([]any); ok {
		for _, k := range d {
			if ks, ok := k.(string); ok {
				drop[ks] = true
			}
		}
	}
	// 順序保持のため [[key, value], ...] 形式で保存する
	if list, ok := m["flashes"].([]any); ok {
		for _, e := range list {
			pair, ok := e.([]any)
			if !ok || len(pair) != 2 {
				continue
			}
			k, _ := pair[0].(string)
			val, _ := pair[1].(string)
			if k == "" || drop[k] {
				continue
			}
			f.entries = append(f.entries, FlashEntry{k, val})
			f.discard[k] = true
		}
	}
	return f
}

// toSessionValue は to_session_value 相当（残すものがなければ nil）。
func (f *Flash) toSessionValue() any {
	var list []any
	for _, e := range f.entries {
		if !f.discard[e.Key] {
			list = append(list, []any{e.Key, e.Value})
		}
	}
	if len(list) == 0 {
		return nil
	}
	return map[string]any{"discard": []any{}, "flashes": list}
}

// commitFlash は Rails の commit_flash 相当。flash にアクセスした場合のみ書き戻す。
func (s *Session) commitFlash() {
	if s.flash == nil {
		return
	}
	v := s.flash.toSessionValue()
	if v == nil {
		s.Delete(flashSessionKey)
		return
	}
	s.Set(flashSessionKey, v)
}

func (f *Flash) index(key string) int {
	for i, e := range f.entries {
		if e.Key == key {
			return i
		}
	}
	return -1
}

// Set は値を設定し、次のリクエストへ引き継ぐ（flash[key] = value）。
func (f *Flash) Set(key, value string) {
	delete(f.discard, key)
	if i := f.index(key); i >= 0 {
		f.entries[i].Value = value
		return
	}
	f.entries = append(f.entries, FlashEntry{key, value})
}

// Now はこのリクエストのみ有効な値を設定する（flash.now[key] = value）。
func (f *Flash) Now(key, value string) {
	f.Set(key, value)
	f.discard[key] = true
}

// Get は値を返す。
func (f *Flash) Get(key string) string {
	if i := f.index(key); i >= 0 {
		return f.entries[i].Value
	}
	return ""
}

// Has はキーが存在するかを返す。
func (f *Flash) Has(key string) bool { return f.index(key) >= 0 }

// Delete はキーを削除する。
func (f *Flash) Delete(key string) {
	if i := f.index(key); i >= 0 {
		f.entries = append(f.entries[:i], f.entries[i+1:]...)
	}
	delete(f.discard, key)
}

// Keep は指定キー（省略時は全て）を次のリクエストへ引き継ぐ。
func (f *Flash) Keep(keys ...string) {
	if len(keys) == 0 {
		f.discard = map[string]bool{}
		return
	}
	for _, k := range keys {
		delete(f.discard, k)
	}
}

// Discard は指定キー（省略時は全て）をこのリクエスト限りにする。
func (f *Flash) Discard(keys ...string) {
	if len(keys) == 0 {
		for _, e := range f.entries {
			f.discard[e.Key] = true
		}
		return
	}
	for _, k := range keys {
		f.discard[k] = true
	}
}

// Entries は挿入順の全エントリ（表示用）を返す。
func (f *Flash) Entries() []FlashEntry { return append([]FlashEntry(nil), f.entries...) }

// Empty は 1 件もなければ true。
func (f *Flash) Empty() bool { return len(f.entries) == 0 }

// Notice / Error / Warning は Redmine で使われるキーの短縮。

// SetNotice は flash[:notice] = msg。
func (f *Flash) SetNotice(msg string) { f.Set("notice", msg) }

// SetError は flash[:error] = msg。
func (f *Flash) SetError(msg string) { f.Set("error", msg) }

// SetWarning は flash[:warning] = msg。
func (f *Flash) SetWarning(msg string) { f.Set("warning", msg) }
