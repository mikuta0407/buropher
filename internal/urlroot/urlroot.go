// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package urlroot はサブパス配置（Rails の config.relative_url_root / RAILS_RELATIVE_URL_ROOT）の
// ルートパスを保持し、アプリが生成する絶対パスへ前置する。
//
// Redmine（Rails）では url_for / *_path / アセットのパスに script_name（= relative_url_root）が
// 前置される。buropher では起動時に Set でルートを設定し、パスを生成する箇所（link_to・form の action・
// redirect_to の Location・テキスト整形のリンクなど）は Path を通す。
//
// ルートはプロセス全体で 1 つ（Rails と同じくグローバル設定）。空ならルート配置で、Path は何もしない
// （既存の出力とバイト単位で同一）。
//
// Path は冪等（既にルートで始まるパスには前置しない）。リクエスト由来のパス（request.path・back_url）は
// ルートを含むため、生成済みのパスを何度 Path に通しても二重にならない。そのため、ルートは
// Redmine のトップレベルのルート（/projects、/issues 等）と衝突しない名前にすること。
package urlroot

import (
	"strings"
	"sync/atomic"
)

var root atomic.Pointer[string]

// Normalize はルートの表記を正規化する（"/" や空は空、先頭に "/" を補い末尾の "/" を除く）。
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "/")
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

// Set はルートを設定する（起動時に 1 回。テストでは終了時に戻すこと）。
func Set(s string) {
	s = Normalize(s)
	root.Store(&s)
}

// Get は現在のルート（例 "/redmine"。ルート配置なら空）を返す。
func Get() string {
	if p := root.Load(); p != nil {
		return *p
	}
	return ""
}

// has は p が既にルート rt で始まっているかを返す。
func has(rt, p string) bool {
	if !strings.HasPrefix(p, rt) {
		return false
	}
	if len(p) == len(rt) {
		return true
	}
	switch p[len(rt)] {
	case '/', '?', '#':
		return true
	}
	return false
}

// Path は "/" で始まるアプリ内の絶対パスにルートを前置する（url_for の script_name 相当）。
// スキーム付きの URL・"//" 始まり・相対パス・既にルートで始まるパスはそのまま返す。
func Path(p string) string {
	rt := Get()
	if rt == "" || p == "" || p[0] != '/' || strings.HasPrefix(p, "//") || has(rt, p) {
		return p
	}
	return rt + p
}

// Strip は p の先頭のルートを取り除く（ルーティング用。ルートで始まらなければ ok=false）。
// ルートそのもの（"/redmine"）は "/" になる。
func Strip(p string) (string, bool) {
	rt := Get()
	if rt == "" {
		return p, true
	}
	if p == rt {
		return "/", true
	}
	if strings.HasPrefix(p, rt+"/") {
		return p[len(rt):], true
	}
	return p, false
}
