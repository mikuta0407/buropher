// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package doorkeeper は Redmine 7.0.2 が使う OAuth2 プロバイダ Doorkeeper 5.8.2 の、
// Redmine の設定（config/initializers/30-redmine.rb）で有効な部分の移植。
//
// Redmine の Doorkeeper 設定:
//
//   - use_refresh_token（リフレッシュトークンを発行する）
//   - authorization_code_expires_in = 10 分、access_token_expires_in = 2 時間（いずれも既定値）
//   - hash_token_secrets（アクセストークン・リフレッシュトークン・認可コードは SHA-256 の 16 進で保存）
//   - hash_application_secrets using: BCrypt（アプリケーションのシークレットは BCrypt で保存）
//   - grant_flows ['authorization_code']（トークンエンドポイントは authorization_code と refresh_token のみ）
//   - realm = "Redmine"、default_scopes = 公開権限、optional_scopes = 全権限 + admin、enforce_configured_scopes
//   - allow_token_introspection false（/oauth/introspect のルートは無い）
//   - force_ssl_in_redirect_uri（localhost / 127.0.0.1 / web 以外の http は不可）、forbid_redirect_uri（data / vbscript / javascript）
//   - reuse_access_token は無効、PKCE（plain / S256）は有効、previous_refresh_token 列があるため
//     リフレッシュ時の旧トークンは新しいトークンが初めて使われたときに失効する（refresh_token_revoked_on_use?）。
//
// トークンの形式は Doorkeeper::OAuth::Helpers::UniqueToken（SecureRandom.urlsafe_base64(32) = 43 文字）。
// DB の形式は Redmine が保存したものと同一なので、redmineimport で取り込んだトークン・シークレットをそのまま使える。
package doorkeeper

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/mikuta0407/buropher/internal/permission"
)

const (
	// AccessTokenExpiresIn は access_token_expires_in（秒）。
	AccessTokenExpiresIn = 7200
	// AuthorizationCodeExpiresIn は authorization_code_expires_in（秒）。
	AuthorizationCodeExpiresIn = 600
	// Realm は realm（Redmine::Info.app_name）。
	Realm = "Redmine"
	// BCryptCost は BCrypt::Engine の既定コスト（Redmine が保存したハッシュも $2a$12$）。
	BCryptCost = 12
	// AdminScope は管理者権限のスコープ。
	AdminScope = "admin"
)

// GenerateToken は UniqueToken.generate（SecureRandom.urlsafe_base64(32)）。
func GenerateToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// HashToken は SecretStoring::Sha256Hash.transform_secret（トークン・認可コードの保存形式）。
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// HashSecret は SecretStoring::BCrypt.transform_secret（アプリケーションのシークレットの保存形式）。
func HashSecret(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), BCryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// SecretMatches は SecretStoring::BCrypt.secret_matches?。
func SecretMatches(input, stored string) bool {
	if stored == "" {
		return false
	}
	if !strings.HasPrefix(stored, "$2") {
		// 不正なハッシュ（BCrypt::Errors::InvalidHash）は不一致
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(stored), []byte(input)) == nil
}

// SecureCompare は ActiveSupport::SecurityUtils.secure_compare。
func SecureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// CodeChallengeS256 は AccessGrant.generate_code_challenge（PKCE の S256）。
func CodeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// PKCEMethods は pkce_code_challenge_methods_supported。
var PKCEMethods = []string{"plain", "S256"}

// ---------------------------------------------------------------- スコープ（Doorkeeper::OAuth::Scopes）

// ParseScopes は Scopes.from_string（空白区切り・重複除去）。
func ParseScopes(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// ScopesFromArray は Scopes.from_array（重複除去。要素はそのまま）。
func ScopesFromArray(xs []string) []string {
	var out []string
	for _, f := range xs {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// DefaultScopes は default_scopes（Redmine::AccessControl.public_permissions の名前）。
func DefaultScopes() []string {
	var out []string
	for _, p := range permission.PublicPermissions() {
		out = append(out, p.Name)
	}
	return out
}

// ServerScopes は Doorkeeper.config.scopes（default_scopes + optional_scopes = 公開権限 + 全権限 + admin）。
func ServerScopes() []string {
	out := DefaultScopes()
	for _, p := range permission.All() {
		if !slices.Contains(out, p.Name) {
			out = append(out, p.Name)
		}
	}
	if !slices.Contains(out, AdminScope) {
		out = append(out, AdminScope)
	}
	return out
}

// HasScopes は Scopes#has_scopes?（requested がすべて allowed に含まれる）。
func HasScopes(allowed, requested []string) bool {
	for _, s := range requested {
		if !slices.Contains(allowed, s) {
			return false
		}
	}
	return true
}

// AllowedScopes は Scopes#allowed(other)（other のうち self に含まれるもの。other の順）。
func AllowedScopes(self, other []string) []string {
	var out []string
	for _, s := range other {
		if slices.Contains(self, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// ScopeValid は ScopeChecker.valid?(scope_str:, server_scopes:, app_scopes:)。
// app_scopes が空でなければ app_scopes、空なら server_scopes を許可範囲とする。
func ScopeValid(scopeStr string, serverScopes, appScopes []string) bool {
	if strings.TrimSpace(scopeStr) == "" || strings.ContainsAny(scopeStr, "\n\r\t") {
		return false
	}
	valid := appScopes
	if len(valid) == 0 {
		valid = serverScopes
	}
	return HasScopes(valid, ParseScopes(scopeStr))
}

// ScopesMatch は AccessToken.scopes_match?（既存トークンの再利用判定）。
func ScopesMatch(tokenScopes, paramScopes, appScopes []string) bool {
	if len(tokenScopes) == 0 && len(paramScopes) == 0 {
		return true
	}
	a, b := slices.Clone(tokenScopes), slices.Clone(paramScopes)
	slices.Sort(a)
	slices.Sort(b)
	if !slices.Equal(a, b) {
		return false
	}
	return ScopeValid(strings.Join(paramScopes, " "), ServerScopes(), appScopes)
}
