// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package brandtest は Redmine の出力から作ったゴールデンと buropher の出力を比べるテスト用に、
// buropher の製品名表記（"Buropher"）を Redmine の表記へ戻す。
//
// buropher は既定の app_title・app_name・フッター・訳文の製品名を "Buropher" にしている
// （internal/brand）。参照 Redmine の出力はすべて "Redmine" なので、比較の前に候補側へ Unbrand を
// 適用する（互換テストハーネス tools/compat では両側をプレースホルダにそろえる normalize.Brand を使う）。
package brandtest

import (
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/brand"
)

var (
	// フッター: "Powered by <a ...>Buropher</a>, based on <a ...>Redmine</a> © ..." → "Powered by <a ...>Redmine</a> © ..."
	// （生の HTML と、1 要素 1 行に整形した HTML の両方）
	footerRe = regexp.MustCompile(`(Powered by\s*)<a [^>]*>` + brand.Name + `</a>\s*, based on\s*`)
	nameRe   = regexp.MustCompile(`\b` + brand.Name + `\b`)
)

// Unbrand は s に含まれる buropher の製品名表記を Redmine の表記へ戻す。
// フッターの "Powered by Buropher, based on" を除き、Atom の generator の URL を redmine.org に戻し、
// 単語としての "Buropher" をすべて "Redmine" に置き換える（小文字の識別子 "buropher" は変えない）。
func Unbrand(s string) string {
	if !strings.Contains(s, brand.Name) {
		return s
	}
	s = footerRe.ReplaceAllString(s, "${1}")
	s = strings.ReplaceAll(s, `<generator uri="`+brand.SourceURL+`">`, `<generator uri="`+brand.UpstreamURL+`">`)
	return nameRe.ReplaceAllString(s, brand.Upstream)
}

// UnbrandBytes は Unbrand の []byte 版。
func UnbrandBytes(b []byte) []byte {
	if !strings.Contains(string(b), brand.Name) {
		return b
	}
	return []byte(Unbrand(string(b)))
}
