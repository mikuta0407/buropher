// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

// View はリクエスト単位の状態を持つ ActionView のビューコンテキスト相当。
// CSRF トークンやアセットパス解決など、リクエストや設定に依存するヘルパーは View のメソッドとして提供する。
// ゼロ値でも動作する（その場合 CSRF トークンは出力されない）。
type View struct {
	// ProtectAgainstForgery が真のとき、POST 系フォームに authenticity_token の hidden を出力する。
	ProtectAgainstForgery bool
	// CSRFToken はフォームに埋め込むトークン（form_authenticity_token）。
	CSRFToken string
	// CSRFParam はトークンのパラメータ名（既定 "authenticity_token"）。
	CSRFParam string
	// AssetPath は image_tag などで論理パス（"add.png"）を公開 URL（"/assets/add-<digest>.png"）に変換する。
	// kind は "image" / "javascript" / "stylesheet"。nil の場合は "/" + source を返す
	// （Rails の skip_pipeline 時の public パス相当）。
	AssetPath func(kind, source string) string
	// FormName は Redmine の form_tag_html オーバーライド（name 属性を自動付与）を再現するための関数。
	// base には html_options の id（無ければ "form"）が渡され、戻り値が name 属性になる。
	// Redmine では "#{base}-#{SecureRandom.hex(4)}"。nil なら name 属性を付与しない（素の Rails）。
	FormName func(base string) string
	// EnforceUTF8 は utf8=✓ の hidden を出力するか（Redmine は default_enforce_utf8 = false）。
	EnforceUTF8 bool
	// AutomaticallyDisableSubmitTag は submit_tag に data-disable-with を付けるか（Rails 既定 true）。
	// ゼロ値の View では false になるため、NewView を使うこと。
	AutomaticallyDisableSubmitTag bool
	// EmbedAuthenticityTokenInRemoteForms は embed_authenticity_token_in_remote_forms（nil / true / false）。
	// Redmine では nil。このとき form_tag(remote: true) はトークンを埋め込むが
	// （判定が == false のため）、form_for(remote: true) は埋め込まない（判定が !embed のため）。
	EmbedAuthenticityTokenInRemoteForms *bool
	// Translate はフォームビルダの label: シンボル等の翻訳に使う（l(:key) 相当）。nil ならキーをそのまま返す。
	Translate func(key string) string

	cycles map[string]*cycle
}

// NewView は Redmine 7.0.1 の設定（utf8 hidden なし、submit の自動 disable あり、CSRF 保護あり）で View を作る。
func NewView(csrfToken string) *View {
	return &View{
		ProtectAgainstForgery:         true,
		CSRFToken:                     csrfToken,
		CSRFParam:                     "authenticity_token",
		AutomaticallyDisableSubmitTag: true,
	}
}

func (v *View) csrfParam() string {
	if v.CSRFParam == "" {
		return "authenticity_token"
	}
	return v.CSRFParam
}

func (v *View) t(key string) string {
	if v.Translate != nil {
		return v.Translate(key)
	}
	return key
}

// assetPath はアセットの公開パスを返す。
func (v *View) assetPath(kind, source string) string {
	if isURL(source) {
		return source
	}
	if v.AssetPath != nil {
		return v.AssetPath(kind, source)
	}
	if len(source) > 0 && source[0] == '/' {
		return source
	}
	return "/" + source
}

func isURL(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ':' {
			return i > 0
		}
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			break
		}
	}
	return len(s) > 1 && s[0] == '/' && s[1] == '/'
}

// TokenTag は token_tag(token, form_options)。
// token が false なら出力しない。nil / true なら View の CSRFToken を使う。
func (v *View) TokenTag(token any) HTML {
	if b, ok := token.(bool); ok && !b {
		return ""
	}
	if !v.ProtectAgainstForgery {
		return ""
	}
	tok := token
	if token == nil || token == true {
		tok = v.CSRFToken
	}
	return Tag("input", NewHash("type", "hidden", "name", v.csrfParam(), "value", tok, "autocomplete", "off"))
}

// MethodTag は method_tag(method)（_method の hidden）。
func MethodTag(method string) HTML {
	return Tag("input", NewHash("type", "hidden", "name", "_method", "value", method, "autocomplete", "off"))
}
