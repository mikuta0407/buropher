// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package view

import (
	"crypto/rand"
	"encoding/hex"
)

// Translator は i18n の翻訳関数（Redmine の l(:key, args) 相当）。
// internal/i18n の実装をアダプタで渡す。
type Translator func(key string, args ...any) string

// AssetPathFunc はアセットの論理パスを公開 URL に変換する（"image", "add.png" → "/assets/add-<digest>.png"）。
// kind は "image" / "javascript" / "stylesheet"。internal/assets の実装をアダプタで渡す。
type AssetPathFunc func(kind, source string) string

// Flash は flash メッセージ 1 件（挿入順に表示される）。
type Flash struct {
	Type    string // "notice" / "error" / "warning" など
	Message string // html_safe として出力される（Redmine の v.html_safe と同じ）
}

// Context はリクエスト単位の描画コンテキスト（ERB のコントローラ状態・インスタンス変数の共通部分）。
// テンプレートからは関数（current_user, controller_name など）経由で参照する。
type Context struct {
	// Locale は現在の言語（current_language、html の lang 属性）。
	Locale string
	// T は翻訳関数（nil ならキーをそのまま返す）。
	T Translator
	// AssetPath はアセット URL の解決関数（nil なら "/" + source）。
	AssetPath AssetPathFunc

	// CSRFToken はフォームと <meta name="csrf-token"> に埋め込むトークン。空なら CSRF 保護なしとして扱う。
	CSRFToken string
	// FormNameSuffix は Redmine の form_tag_html が name 属性に付ける乱数部（SecureRandom.hex(4)）を返す。
	// nil なら暗号論的乱数で 8 桁の 16 進を生成する（テストでは固定値を返す関数を設定する）。
	FormNameSuffix func() string

	// RequestPath はリクエストパス（request.path）。
	RequestPath string
	// Controller / Action は Rails のコントローラ名・アクション名（"issues" / "show"）。
	// body の CSS クラス（controller-issues action-show）と相対 partial 名の解決に使う。
	Controller string
	Action     string

	// User は現在のユーザー（User.current）。型は利用側で決める。
	User any
	// Project は現在のプロジェクト（@project）。nil 可。
	Project any
	// ProjectName / ProjectIdentifier は html_title と body_css_classes 用（@project.name / identifier）。
	ProjectName       string
	ProjectIdentifier string
	// AppTitle は Setting.app_title（html_title の末尾）。
	AppTitle string

	// body_css_classes 用の設定値。
	Theme        string // テーマ名（"theme-<name>"、空ならなし）
	HasMainMenu  bool   // display_main_menu?(@project)
	TextareaFont string // User.current.pref.textarea_font（"monospace" / "proportional" のときのみ付与）

	// Flash は表示する flash メッセージ。
	Flash []Flash

	// Values は上記以外の任意の値（ヘルパー間で共有する状態など）。
	Values map[string]any
}

func (c *Context) translate(key string, args ...any) string {
	if c.T == nil {
		return key
	}
	return c.T(key, args...)
}

func (c *Context) formNameSuffix() string {
	if c.FormNameSuffix != nil {
		return c.FormNameSuffix()
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
