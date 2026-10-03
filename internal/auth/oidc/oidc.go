// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package oidc は OpenID Connect によるシングルサインオン（buropher 拡張。Redmine には無い）の
// プロトコル部分を実装する。認可コードフロー + PKCE（S256）、state / nonce の検証、ID トークンの署名検証
// （JWKS はプロバイダごとにキャッシュ）を github.com/coreos/go-oidc/v3 と golang.org/x/oauth2 で行う。
//
// ユーザーの解決（user_identities・既存ユーザーとの突合・オンザフライ作成）やグループ同期は handler 側で行い、
// このパッケージはクレームの取り出しと許可条件（テナント・グループ）の判定までを受け持つ。
//
// Microsoft Entra ID のプリセット:
//
//	issuer: https://login.microsoftonline.com/{tenant}/v2.0
//	subject: oid（テナント内で不変のオブジェクト ID。sub はアプリごとに異なる）
//	login: preferred_username, mail: email, firstname: given_name, lastname: family_name
//	tenant: tid, groups: groups（グループのオブジェクト ID）
//
// tenant に common / organizations / consumers を指定したマルチテナント構成では、ディスカバリ文書の issuer が
// "https://login.microsoftonline.com/{tenantid}/v2.0" のテンプレートになるため、issuer の一致確認は
// ID トークンの tid を埋め込んだ値で行う（allowed_tenants で許可するテナントを必ず絞ること）。
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// プリセット名。
const (
	PresetGeneric = "generic"
	PresetEntra   = "entra"
)

// EntraIssuerTemplate は Entra ID の issuer（{tenant} をテナント ID / ドメイン / common 等で置き換える）。
const EntraIssuerTemplate = "https://login.microsoftonline.com/{tenant}/v2.0"

// entraMultiTenants はマルチテナント用の特殊なテナント名。
var entraMultiTenants = []string{"common", "organizations", "consumers"}

// Config は OIDC 認証方式の設定（auth_sources.config と復号した client_secret）。
type Config struct {
	Preset string
	// Issuer はディスカバリの基点（{issuer}/.well-known/openid-configuration）。Entra では Tenant から組み立てる。
	Issuer       string
	Tenant       string
	ClientID     string
	ClientSecret string
	Scopes       []string
	RedirectURL  string

	ClaimSubject   string
	ClaimLogin     string
	ClaimFirstname string
	ClaimLastname  string
	ClaimMail      string
	ClaimGroups    string
	ClaimTenant    string

	// AllowedTenants はログインを許可するテナント（tid クレーム）。空なら制限しない。
	AllowedTenants []string
	// AllowedGroups はログインを許可するグループ（groups クレームの値）。空なら制限しない。
	AllowedGroups []string
}

// Defaults は未設定のクレーム名などを既定値（プリセット）で埋めたコピーを返す。
func (c Config) Defaults() Config {
	def := func(p *string, v string) {
		if strings.TrimSpace(*p) == "" {
			*p = v
		} else {
			*p = strings.TrimSpace(*p)
		}
	}
	if c.Preset == PresetEntra {
		def(&c.ClaimSubject, "oid")
		def(&c.ClaimTenant, "tid")
		if strings.TrimSpace(c.Issuer) == "" && c.Tenant != "" {
			c.Issuer = EntraIssuer(c.Tenant)
		}
	}
	def(&c.ClaimSubject, "sub")
	def(&c.ClaimLogin, "preferred_username")
	def(&c.ClaimFirstname, "given_name")
	def(&c.ClaimLastname, "family_name")
	def(&c.ClaimMail, "email")
	def(&c.ClaimGroups, "groups")
	def(&c.ClaimTenant, "tid")
	if len(c.Scopes) == 0 {
		c.Scopes = []string{gooidc.ScopeOpenID, "profile", "email"}
	}
	if !slices.Contains(c.Scopes, gooidc.ScopeOpenID) {
		c.Scopes = append([]string{gooidc.ScopeOpenID}, c.Scopes...)
	}
	c.Issuer = strings.TrimRight(strings.TrimSpace(c.Issuer), "/")
	return c
}

// EntraIssuer は Entra ID のテナントの issuer。
func EntraIssuer(tenant string) string {
	return strings.ReplaceAll(EntraIssuerTemplate, "{tenant}", strings.TrimSpace(tenant))
}

// multiTenant は issuer の一致確認を ID トークンの tid で行う構成か。
func (c Config) multiTenant() bool {
	if c.Preset != PresetEntra {
		return false
	}
	return slices.Contains(entraMultiTenants, strings.ToLower(strings.TrimSpace(c.Tenant))) ||
		strings.Contains(c.Issuer, "/common/") || strings.Contains(c.Issuer, "/organizations/") || strings.Contains(c.Issuer, "/consumers/")
}

// MultiTenant は multiTenant の公開版（subject の名前空間に issuer を含めるかの判定に使う）。
func (c Config) MultiTenant() bool { return c.Defaults().multiTenant() }

// Provider はディスカバリ済みのプロバイダ。
type Provider struct {
	cfg        Config
	provider   *gooidc.Provider
	oauth      oauth2.Config
	endSession string
	client     *http.Client
}

// Error はユーザーに見せる理由（訳文キー）を伴う失敗。
type Error struct {
	// Reason は buropher.sso.error_<reason> の訳文キーの末尾。
	Reason string
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return "oidc: " + e.Reason + ": " + e.Err.Error()
	}
	return "oidc: " + e.Reason
}

func (e *Error) Unwrap() error { return e.Err }

func fail(reason string, err error) error { return &Error{Reason: reason, Err: err} }

// Reason は err が *Error ならその理由、そうでなければ "failed"。
func Reason(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return "failed"
}

// cache はディスカバリ結果（と JWKS のキャッシュを持つ go-oidc の Provider）のキャッシュ。
type cacheEntry struct {
	p   *Provider
	at  time.Time
	key string
}

var (
	cacheMu sync.Mutex
	cache   = map[int64]*cacheEntry{}
)

// cacheTTL はディスカバリ結果を再取得するまでの時間。
const cacheTTL = time.Hour

// Cached は認証方式 id のプロバイダをキャッシュから返し、無ければ（設定が変わっていれば）ディスカバリする。
func Cached(ctx context.Context, id int64, client *http.Client, cfg Config) (*Provider, error) {
	key := fmt.Sprintf("%#v", cfg.Defaults())
	cacheMu.Lock()
	e := cache[id]
	cacheMu.Unlock()
	if e != nil && e.key == key && time.Since(e.at) < cacheTTL {
		return e.p, nil
	}
	p, err := New(ctx, client, cfg)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	cache[id] = &cacheEntry{p: p, at: time.Now(), key: key}
	cacheMu.Unlock()
	return p, nil
}

// Forget はキャッシュを捨てる（設定の更新・削除時）。
func Forget(id int64) {
	cacheMu.Lock()
	delete(cache, id)
	cacheMu.Unlock()
}

// defaultHTTPClient はプロバイダとの通信の既定のクライアント（http.DefaultClient にはタイムアウトが無く、
// 応答しないプロバイダでログインの処理が止まり続ける）。
var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

// New はディスカバリしてプロバイダを作る。
func New(ctx context.Context, client *http.Client, cfg Config) (*Provider, error) {
	cfg = cfg.Defaults()
	if cfg.Issuer == "" {
		return nil, fail("configuration", errors.New("issuer is empty"))
	}
	if client == nil {
		client = defaultHTTPClient
	}
	ctx = gooidc.ClientContext(ctx, client)
	if cfg.multiTenant() {
		// ディスカバリ文書の issuer は {tenantid} を含むテンプレートなので一致確認しない
		ctx = gooidc.InsecureIssuerURLContext(ctx, cfg.Issuer)
	}
	p, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fail("discovery", err)
	}
	var extra struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&extra)
	return &Provider{
		cfg:      cfg,
		provider: p,
		oauth: oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: p.Endpoint(),
			RedirectURL: cfg.RedirectURL, Scopes: cfg.Scopes,
		},
		endSession: extra.EndSession,
		client:     client,
	}, nil
}

// Config は既定値を埋めた設定。
func (p *Provider) Config() Config { return p.cfg }

// EndSessionEndpoint はディスカバリ文書の end_session_endpoint（無ければ ""）。
func (p *Provider) EndSessionEndpoint() string { return p.endSession }

// Endpoint は認可・トークンエンドポイント（接続テストの表示用）。
func (p *Provider) Endpoint() oauth2.Endpoint { return p.provider.Endpoint() }

// RandomString は state / nonce / code_verifier 用の乱数文字列（32 バイトの base64url）。
func RandomString() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// AuthRequest は認可リクエストのパラメータ（セッションに保存して callback で照合する）。
type AuthRequest struct {
	State    string
	Nonce    string
	Verifier string
	// Prompt / MaxAge は再認証（sudo モード）用。
	Prompt string
	MaxAge int
}

// NewAuthRequest は新しい state / nonce / PKCE の code_verifier を作る。
func NewAuthRequest() AuthRequest {
	return AuthRequest{State: RandomString(), Nonce: RandomString(), Verifier: oauth2.GenerateVerifier()}
}

// AuthCodeURL は認可エンドポイントへのリダイレクト先。
func (p *Provider) AuthCodeURL(req AuthRequest) string {
	opts := []oauth2.AuthCodeOption{gooidc.Nonce(req.Nonce), oauth2.S256ChallengeOption(req.Verifier)}
	if req.Prompt != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", req.Prompt))
	}
	if req.MaxAge > 0 || req.Prompt == "login" {
		opts = append(opts, oauth2.SetAuthURLParam("max_age", fmt.Sprint(req.MaxAge)))
	}
	return p.oauth.AuthCodeURL(req.State, opts...)
}

// Identity は ID トークンから取り出したユーザー情報。
type Identity struct {
	Issuer    string
	Subject   string
	Login     string
	Firstname string
	Lastname  string
	Mail      string
	Tenant    string
	Groups    []string
	// GroupsOverage は groups クレームが溢れて _claim_names で別取得を指示された（Entra のグループ超過）。
	GroupsOverage bool
	// AuthTime は auth_time クレーム（無ければゼロ値）。
	AuthTime time.Time
	// MailUnverified は email_verified クレームが明示的に false（IdP がメールアドレスを検証していない）。
	// クレームが無い場合（Entra など）は false。
	MailUnverified bool
	// IDToken は生の ID トークン（RP-Initiated Logout の id_token_hint 用）。
	IDToken string
	// Claims は全クレーム。
	Claims map[string]any
}

// Exchange は認可コードをトークンに交換し、ID トークンを検証してクレームを取り出す。
// state の照合は呼び出し側で行う（セッションの値と比べる）。
func (p *Provider) Exchange(ctx context.Context, code string, req AuthRequest) (*Identity, error) {
	ctx = gooidc.ClientContext(ctx, p.client)
	if strings.TrimSpace(code) == "" {
		return nil, fail("invalid_response", errors.New("authorization code is missing"))
	}
	tok, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(req.Verifier))
	if err != nil {
		return nil, fail("token", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, fail("token", errors.New("id_token is missing in the token response"))
	}
	verifier := p.provider.VerifierContext(ctx, &gooidc.Config{ClientID: p.cfg.ClientID, SkipIssuerCheck: p.cfg.multiTenant()})
	idt, err := verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fail("id_token", err)
	}
	if idt.Nonce == "" || idt.Nonce != req.Nonce {
		return nil, fail("nonce", errors.New("nonce mismatch"))
	}
	claims := map[string]any{}
	if err := idt.Claims(&claims); err != nil {
		return nil, fail("id_token", err)
	}
	id := p.identity(idt.Issuer, claims)
	id.IDToken = raw
	if p.cfg.multiTenant() {
		// issuer は https://login.microsoftonline.com/<tid>/v2.0 でなければならない
		if id.Tenant == "" || idt.Issuer != EntraIssuer(id.Tenant) {
			return nil, fail("id_token", fmt.Errorf("issuer %q does not match tenant %q", idt.Issuer, id.Tenant))
		}
	}
	if id.Subject == "" {
		return nil, fail("id_token", fmt.Errorf("subject claim %q is missing", p.cfg.ClaimSubject))
	}
	return id, nil
}

// claimString はクレームの文字列値（配列なら先頭）。
func claimString(claims map[string]any, name string) string {
	switch v := claims[name].(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprint(v)
	}
	return ""
}

// claimStrings はクレームの文字列配列（文字列 1 つでも配列として扱う）。
func claimStrings(claims map[string]any, name string) []string {
	switch v := claims[name].(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// identity はクレームを設定のクレーム名で取り出す。
func (p *Provider) identity(issuer string, claims map[string]any) *Identity {
	c := p.cfg
	id := &Identity{
		Issuer:    issuer,
		Subject:   claimString(claims, c.ClaimSubject),
		Login:     claimString(claims, c.ClaimLogin),
		Firstname: claimString(claims, c.ClaimFirstname),
		Lastname:  claimString(claims, c.ClaimLastname),
		Mail:      claimString(claims, c.ClaimMail),
		Tenant:    claimString(claims, c.ClaimTenant),
		Groups:    claimStrings(claims, c.ClaimGroups),
		Claims:    claims,
	}
	if n, ok := claims["_claim_names"].(map[string]any); ok {
		if _, ok := n[c.ClaimGroups]; ok {
			id.GroupsOverage = true
		}
	}
	if v, ok := claims["auth_time"].(float64); ok {
		id.AuthTime = time.Unix(int64(v), 0)
	}
	switch v := claims["email_verified"].(type) {
	case bool:
		id.MailUnverified = !v
	case string:
		id.MailUnverified = strings.EqualFold(v, "false")
	}
	// Entra でメールが無い場合は preferred_username（UPN）をメールとして使う
	if id.Mail == "" && c.Preset == PresetEntra && strings.Contains(id.Login, "@") {
		id.Mail = id.Login
	}
	return id
}

// Authorize はテナント・グループの許可条件を判定する。許可されなければ *Error（tenant / group）。
func (p *Provider) Authorize(id *Identity) error {
	c := p.cfg
	if len(c.AllowedTenants) > 0 && !containsFold(c.AllowedTenants, id.Tenant) {
		return fail("tenant", fmt.Errorf("tenant %q is not allowed", id.Tenant))
	}
	if len(c.AllowedGroups) > 0 {
		ok := false
		for _, g := range id.Groups {
			if containsFold(c.AllowedGroups, g) {
				ok = true
				break
			}
		}
		if !ok {
			return fail("group", errors.New("user is not a member of an allowed group"))
		}
	}
	return nil
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), v) {
			return true
		}
	}
	return false
}

// EndSessionURL は RP-Initiated Logout のリダイレクト先（end_session_endpoint が無ければ ""）。
func (p *Provider) EndSessionURL(idTokenHint, postLogoutRedirect string) string {
	if p.endSession == "" {
		return ""
	}
	u, err := url.Parse(p.endSession)
	if err != nil {
		return ""
	}
	q := u.Query()
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	q.Set("client_id", p.cfg.ClientID)
	if postLogoutRedirect != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirect)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// PKCEChallenge は code_verifier の S256 チャレンジ（テスト用）。
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
