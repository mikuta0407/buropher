// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package brand は製品名・ライセンス表記など、buropher のブランディングに関する定数と
// Redmine 由来の訳文に対するブランド置換を提供する。
//
// buropher は Redmine（現在の互換対象は 7.0.1）を Go で再実装した派生著作物であり、利用者に見える製品名は
// "Buropher" とする。一方で、HTTP/メールヘッダ（X-Redmine-*）・Message-ID・REST API の要素名・
// CSS クラス・JS グローバルなど機械が読む互換識別子は Redmine のまま維持する（docs/compatibility.md）。
package brand

import "strings"

const (
	// Name は利用者向けの製品名（Redmine::Info.app_name 相当）。
	Name = "Buropher"
	// Upstream は派生元の製品名。
	Upstream = "Redmine"
	// UpstreamVersion は互換対象の Redmine のバージョン（web/UPSTREAM_VERSION）。
	UpstreamVersion = "7.0.1"
	// UpstreamURL は派生元のプロジェクト URL（Redmine::Info.url）。
	UpstreamURL = "https://www.redmine.org/"
	// UpstreamCopyright は派生元の著作権表示。
	UpstreamCopyright = "Copyright (C) 2006- Jean-Philippe Lang"
	// License は SPDX 形式のライセンス識別子。
	License = "GPL-2.0-or-later"
	// LicenseURL は GPL v2 本文の URL。
	LicenseURL = "https://www.gnu.org/licenses/old-licenses/gpl-2.0.html"
	// Copyright は buropher 独自コードの著作権表示。
	Copyright = "Copyright (C) 2026 mikuta0407 and Buropher contributors"
)

// SourceURL は対応するソースコードの入手先（GPL の「対応するソースコード」の提供先として
// フッター・管理画面・version コマンドに表示する）。フォーク等で配布する場合は
// -ldflags "-X github.com/mikuta0407/buropher/internal/brand.SourceURL=..." で差し替える。
var SourceURL = "https://github.com/mikuta0407/buropher"

// LicenseLine は version コマンド等に表示する 1 行のライセンス表記。
func LicenseLine() string {
	return "license: " + License + " (derivative work of " + Upstream + ", " + UpstreamCopyright + ")\nsource: " + SourceURL
}

// localeExcludedKeys はブランド置換の対象外とする訳文キー（ロケールを除いたドット区切りのキー）。
// ここに挙げたキーの「Redmine」は Redmine 本体の設定ファイル（config/configuration.yml）と
// その再起動を指す説明であり、buropher の製品名に読み替えると誤った案内になるため原文のまま残す。
var localeExcludedKeys = map[string]bool{
	"text_scm_config":                    true,
	"text_setting_config_change":         true,
	"text_email_delivery_not_configured": true,
}

// LocaleExcluded は key（ロケールを除いたドット区切りのキー）がブランド置換の対象外かを返す。
func LocaleExcluded(key string) bool { return localeExcludedKeys[key] }

// SubstituteLocale は Redmine 由来の訳文の値に含まれる製品名 "Redmine" を "Buropher" に置き換える。
//
//   - 大文字始まりの "Redmine" のみ対象（"redmine.org" などの URL・パス、小文字の識別子は変えない）
//   - 直前が ASCII 英数字・'-'・'_'・'.'・'/' の場合（"X-Redmine-API-Key" 等）は変えない
//   - 直後が ASCII 英数字・'_'（"RedmineWiki"、バスク語の格語尾 "Redmineko" 等）または
//     '.' + 英字（"Redmine.org" 等のドメイン）の場合は変えない
//
// 単語区切りの無い言語（"Redmineを再起動"）や複合語（"Redmine-Benutzer"）は置換対象になる。
func SubstituteLocale(s string) string {
	if !strings.Contains(s, Upstream) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	rest := s
	prev := byte(0)
	for {
		i := strings.Index(rest, Upstream)
		if i < 0 {
			b.WriteString(rest)
			break
		}
		if i > 0 {
			prev = rest[i-1]
		}
		b.WriteString(rest[:i])
		after := rest[i+len(Upstream):]
		if replaceable(prev, after) {
			b.WriteString(Name)
		} else {
			b.WriteString(Upstream)
		}
		prev = 'e'
		rest = after
	}
	return b.String()
}

func replaceable(prev byte, after string) bool {
	if isASCIIAlnum(prev) || prev == '-' || prev == '_' || prev == '.' || prev == '/' {
		return false
	}
	if after != "" {
		c := after[0]
		if isASCIIAlnum(c) || c == '_' {
			return false
		}
		if c == '.' && len(after) > 1 && isASCIILetter(after[1]) {
			return false
		}
	}
	return true
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIAlnum(c byte) bool  { return isASCIILetter(c) || c >= '0' && c <= '9' }
