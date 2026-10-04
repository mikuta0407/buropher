// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Rails の RequestForgeryProtection（actionpack 7.2）互換の CSRF 対策。
//
//   - パラメータ名は "authenticity_token"、ヘッダは X-CSRF-Token
//   - セッションに実トークン（32 バイト乱数の urlsafe base64）を "_csrf_token" として保存
//   - ページに出すのはグローバルトークン HMAC-SHA256(実トークン, "!real_csrf_token") を
//     ワンタイムパッドでマスクしたもの（base64url・パディングなし、86 文字）。表示ごとに変わる
//   - 検証はマスク済み（グローバル/実トークン）とマスクなしの実トークンを受け付ける

const (
	// CSRFParam は csrf-param（request_forgery_protection_token）。
	CSRFParam = "authenticity_token"
	// CSRFHeader はトークンを送るヘッダ名。
	CSRFHeader = "X-CSRF-Token"

	csrfSessionKey    = "_csrf_token"
	csrfTokenLength   = 32
	globalCSRFTokenID = "!real_csrf_token"
)

func decodeCSRFToken(s string) ([]byte, bool) {
	// Base64.urlsafe_decode64 相当: +/ も受け入れ、パディングは任意
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	s = strings.TrimRight(s, "=")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	return b, true
}

func encodeCSRFToken(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// realCSRFToken はセッションの実トークン（バイト列）を返す。なければ生成して保存する。
func realCSRFToken(s *Session) []byte {
	if v := s.GetString(csrfSessionKey); v != "" {
		if b, ok := decodeCSRFToken(v); ok && len(b) > 0 {
			return b
		}
	}
	raw := make([]byte, csrfTokenLength)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	// SecureRandom.urlsafe_base64(32)
	tok := base64.RawURLEncoding.EncodeToString(raw)
	s.Set(csrfSessionKey, tok)
	b, _ := decodeCSRFToken(tok)
	return b
}

func csrfTokenHMAC(real []byte, identifier string) []byte {
	h := hmac.New(sha256.New, real)
	h.Write([]byte(identifier))
	return h.Sum(nil)
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	for i := range a {
		out[i] ^= a[i]
	}
	return out
}

// maskCSRFToken は mask_token 相当。
func maskCSRFToken(raw []byte) string {
	pad := make([]byte, csrfTokenLength)
	if _, err := rand.Read(pad); err != nil {
		panic(err)
	}
	enc := xorBytes(pad, raw)
	return encodeCSRFToken(append(pad, enc...))
}

// CSRFToken は form_authenticity_token（マスク済み・毎回異なる）を返す。
// <meta name="csrf-token"> と各フォームの hidden authenticity_token に使う。
// セッションがない場合は空文字。
func CSRFToken(r *http.Request) string {
	s := SessionOf(r)
	if s == nil {
		return ""
	}
	return s.CSRFToken()
}

// CSRFToken はこのセッションのマスク済みトークンを返す。
func (s *Session) CSRFToken() string {
	return maskCSRFToken(csrfTokenHMAC(realCSRFToken(s), globalCSRFTokenID))
}

// ValidCSRFToken は valid_authenticity_token? の移植。
func (s *Session) ValidCSRFToken(encoded string) bool {
	if encoded == "" {
		return false
	}
	masked, ok := decodeCSRFToken(encoded)
	if !ok {
		return false
	}
	// 実トークンが未生成なら生成される（Rails も real_csrf_token で生成する）が、一致はしない
	real := realCSRFToken(s)
	switch len(masked) {
	case csrfTokenLength:
		// マスクなしの実トークン
		return subtle.ConstantTimeCompare(masked, real) == 1
	case csrfTokenLength * 2:
		tok := xorBytes(masked[:csrfTokenLength], masked[csrfTokenLength:])
		global := csrfTokenHMAC(real, globalCSRFTokenID)
		return subtle.ConstantTimeCompare(tok, global) == 1 || subtle.ConstantTimeCompare(tok, real) == 1
	}
	return false
}

// CSRFMetaTags は csrf_meta_tags の出力（Redmine のレイアウトの 2 行）を返す。
// HTML テンプレート側でエスケープ不要の安全な文字列。
func CSRFMetaTags(r *http.Request) string {
	tok := CSRFToken(r)
	return `<meta name="csrf-param" content="` + CSRFParam + `" />` + "\n" +
		`<meta name="csrf-token" content="` + tok + `" />`
}

// VerifiedRequest は verified_request? 相当: GET/HEAD、または authenticity_token パラメータか
// X-CSRF-Token ヘッダのいずれかが正しければ true。
func VerifiedRequest(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	s := SessionOf(r)
	if s == nil {
		return false
	}
	candidates := []string{}
	if v, ok := ParamsOf(r).Get(CSRFParam); ok {
		if str, isStr := v.(string); isStr {
			candidates = append(candidates, str)
		}
	}
	candidates = append(candidates, r.Header.Get(CSRFHeader))
	for _, c := range candidates {
		if s.ValidCSRFToken(c) {
			return true
		}
	}
	return false
}

// SkipCSRF は以降の CSRF 検証を省略するリクエストを返す
// （Redmine の skip_before_action :verify_authenticity_token。mail_handler / sys 用）。
func SkipCSRF(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxSkipCSRF, true))
}

// SkipCSRFMiddleware は SkipCSRF を適用するミドルウェア。CSRFMiddleware より前に置くこと
// （chi では Group の Use は With より先に動くため、グループ内のルートを除外するなら
// CSRFOptions.Skip を使う）。
func SkipCSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, SkipCSRF(r)) })
}

// CSRFOptions は CSRF ミドルウェアの設定。
type CSRFOptions struct {
	// IsAPIRequest が true を返すリクエストは検証しない（Redmine の verify_authenticity_token が
	// api_request? のとき super を呼ばないのと同じ）。nil なら IsAPIRequest（format が xml/json）。
	IsAPIRequest func(r *http.Request) bool
	// Skip が true を返すリクエストは検証しない（mail_handler / sys のように
	// skip_before_action :verify_authenticity_token しているもの）。nil 可。
	Skip func(r *http.Request) bool
	// OnFailure は検証失敗時の処理。nil なら空ボディの 422。
	// Redmine 互換の処理は RedmineCSRFFailure で作る。
	OnFailure http.HandlerFunc
	// Logger は失敗時の警告出力先（nil なら slog.Default()）。
	Logger *slog.Logger
}

// CSRFMiddleware は verify_authenticity_token の before_action 相当。
// format（.json 等）を正しく判定するため、chi のルーティング後に動くインライン
// ミドルウェア（r.With / r.Group 内の Use）として使うことを推奨する。
// ルーティング前に置いた場合はパスの拡張子で API リクエストを近似判定する。
func CSRFMiddleware(opts CSRFOptions) func(http.Handler) http.Handler {
	isAPI := opts.IsAPIRequest
	if isAPI == nil {
		isAPI = IsAPIRequest
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			skip, _ := r.Context().Value(ctxSkipCSRF).(bool)
			if skip || (opts.Skip != nil && opts.Skip(r)) {
				next.ServeHTTP(w, r)
				return
			}
			if isAPI(r) {
				// buropher 独自（セキュリティ）: 検証を省いた API 形式の変更系リクエストには、ブラウザの
				// セッション（クッキー）の状態を使わせない。
				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					r = DetachSession(r)
				}
				next.ServeHTTP(w, r)
				return
			}
			if VerifiedRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			logger.Warn("Can't verify CSRF token authenticity.", "path", r.URL.Path, "request_id", RequestID(r))
			if opts.OnFailure != nil {
				opts.OnFailure(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnprocessableEntity)
		})
	}
}

// DefaultCSRFFailureMessage は l(:error_invalid_authenticity_token) の英語版。
const DefaultCSRFFailureMessage = "Invalid form authenticity token."

// RedmineCSRFFailureOptions は RedmineCSRFFailure の設定。
type RedmineCSRFFailureOptions struct {
	// Errors はエラーレンダラ（必須）。
	Errors *ErrorRenderer
	// AutologinCookie は削除する自動ログインクッキー名（空なら "autologin"）。
	AutologinCookie string
	// AutologinCookiePath はそのクッキーのパス（空なら "/"）。
	AutologinCookiePath string
	// Message はロケールに応じたメッセージを返す（nil なら DefaultCSRFFailureMessage）。
	// set_localization はログアウト後（匿名ユーザ）に行われるので、上位でそれを考慮して返すこと。
	Message func(r *http.Request) string
	// OnLogout はログアウト時の追加処理（User.current を匿名にする等）。nil 可。
	OnLogout func(w http.ResponseWriter, r *http.Request)
}

// RedmineCSRFFailure は ApplicationController#handle_unverified_request を再現するハンドラを返す:
// 自動ログインクッキーを削除し、セッションをリセット（logged_user = nil）し、
// 422 で「Invalid form authenticity token.」のエラーページ（HTML 以外は空ボディ）を返す。
func RedmineCSRFFailure(o RedmineCSRFFailureOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := o.AutologinCookie
		if name == "" {
			name = "autologin"
		}
		path := o.AutologinCookiePath
		if path == "" {
			path = "/"
		}
		// Rails の cookies.delete はクッキーの有無に関わらず削除ヘッダを出す
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, Expires: time.Unix(0, 0)})
		if s := SessionOf(r); s != nil {
			s.Reset()
		}
		if o.OnLogout != nil {
			o.OnLogout(w, r)
		}
		msg := DefaultCSRFFailureMessage
		if o.Message != nil {
			msg = o.Message(r)
		}
		o.Errors.RenderError(w, r, http.StatusUnprocessableEntity, msg)
	}
}
