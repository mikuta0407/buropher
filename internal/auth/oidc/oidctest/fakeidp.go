// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package oidctest はテスト用の OpenID Connect プロバイダ（httptest）を提供する。
//
// ディスカバリ（/.well-known/openid-configuration）・認可（/authorize）・トークン（/token）・JWKS（/jwks）・
// RP-Initiated Logout（/logout）を実装し、PKCE（S256）の code_verifier とクライアント認証を検証する。
// ID トークンは RS256 で署名し、Claims に設定したクレームと nonce・aud・iss・exp を入れる。
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Provider はテスト用の OIDC プロバイダ。
type Provider struct {
	Server       *httptest.Server
	ClientID     string
	ClientSecret string

	mu sync.Mutex
	// Claims は次の ID トークンに入れるクレーム（sub 等）。
	Claims map[string]any
	// Issuer は ID トークンの iss（空ならサーバの URL）。DiscoveryIssuer はディスカバリ文書の issuer（空なら Issuer）。
	Issuer          string
	DiscoveryIssuer string
	// TamperNonce が true なら ID トークンの nonce を変える。
	TamperNonce bool
	// TamperAudience が true なら aud を別のクライアントにする。
	TamperAudience bool
	// SignWithOtherKey が true なら JWKS に無い鍵で署名する。
	SignWithOtherKey bool
	// OmitIDToken が true ならトークン応答に id_token を入れない。
	OmitIDToken bool
	// Audience が nil でなければ aud をこの値にする（複数の aud の検証用）。
	Audience any
	// SignHS256 が true ならクライアントシークレットを鍵に HS256 で署名する（alg の取り違えの検証用）。
	SignHS256 bool
	// Unsigned が true なら alg=none の署名なし ID トークンを返す。
	Unsigned bool

	key, otherKey *rsa.PrivateKey
	codes         map[string]authCode
	// LastAuthorize は最後の認可リクエストのクエリ。
	LastAuthorize url.Values
	// TokenRequests はトークンエンドポイントへのリクエスト数。
	TokenRequests int
}

type authCode struct {
	clientID, redirectURI, nonce, challenge, method string
}

// New はプロバイダを起動する（テスト終了時に Close すること）。
func New() *Provider {
	p := &Provider{ClientID: "buropher-test", ClientSecret: "s3cret", Claims: map[string]any{}, codes: map[string]authCode{}}
	p.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	p.otherKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	mux.HandleFunc("/jwks", p.jwks)
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	p.Server = httptest.NewServer(mux)
	return p
}

// URL はプロバイダの URL（issuer）。
func (p *Provider) URL() string { return p.Server.URL }

// Close はサーバを止める。
func (p *Provider) Close() { p.Server.Close() }

// SetClaims は次の ID トークンのクレームを設定する。
func (p *Provider) SetClaims(c map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Claims = c
}

func (p *Provider) issuer() string {
	if p.Issuer != "" {
		return p.Issuer
	}
	return p.Server.URL
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request) {
	iss := p.DiscoveryIssuer
	if iss == "" {
		iss = p.issuer()
	}
	writeJSON(w, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                p.Server.URL + "/authorize",
		"token_endpoint":                        p.Server.URL + "/token",
		"jwks_uri":                              p.Server.URL + "/jwks",
		"end_session_endpoint":                  p.Server.URL + "/logout",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func randomString() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// authorize は認可エンドポイント（同意なしで即座に redirect_uri へコードを返す）。
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p.mu.Lock()
	p.LastAuthorize = q
	p.mu.Unlock()
	if q.Get("response_type") != "code" || q.Get("client_id") != p.ClientID || q.Get("redirect_uri") == "" ||
		q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" ||
		!strings.Contains(" "+q.Get("scope")+" ", " openid ") {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}
	code := randomString()
	p.mu.Lock()
	p.codes[code] = authCode{clientID: q.Get("client_id"), redirectURI: q.Get("redirect_uri"), nonce: q.Get("nonce"),
		challenge: q.Get("code_challenge"), method: q.Get("code_challenge_method")}
	p.mu.Unlock()
	u, _ := url.Parse(q.Get("redirect_uri"))
	rq := u.Query()
	rq.Set("code", code)
	rq.Set("state", q.Get("state"))
	u.RawQuery = rq.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// token はトークンエンドポイント（クライアント認証・PKCE を検証して ID トークンを返す）。
func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.TokenRequests++
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		tokenError(w, "invalid_client")
		return
	}
	code := r.PostForm.Get("code")
	ac, found := p.codes[code]
	delete(p.codes, code)
	if !found || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("redirect_uri") != ac.redirectURI {
		tokenError(w, "invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != ac.challenge {
		tokenError(w, "invalid_grant")
		return
	}
	resp := map[string]any{"access_token": randomString(), "token_type": "Bearer", "expires_in": 3600}
	if !p.OmitIDToken {
		claims := map[string]any{}
		for k, v := range p.Claims {
			claims[k] = v
		}
		now := time.Now()
		claims["iss"] = p.issuer()
		claims["aud"] = p.ClientID
		if p.Audience != nil {
			claims["aud"] = p.Audience
		}
		if p.TamperAudience {
			claims["aud"] = "someone-else"
		}
		claims["iat"] = now.Unix()
		claims["exp"] = now.Add(time.Hour).Unix()
		claims["nonce"] = ac.nonce
		if p.TamperNonce {
			claims["nonce"] = "tampered"
		}
		payload, _ := json.Marshal(claims)
		if p.Unsigned {
			hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"test"}`))
			resp["id_token"] = hdr + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
			writeJSON(w, resp)
			return
		}
		var sk jose.SigningKey
		switch {
		case p.SignHS256:
			sk = jose.SigningKey{Algorithm: jose.HS256, Key: []byte(p.ClientSecret)}
		case p.SignWithOtherKey:
			sk = jose.SigningKey{Algorithm: jose.RS256, Key: p.otherKey}
		default:
			sk = jose.SigningKey{Algorithm: jose.RS256, Key: p.key}
		}
		signer, err := jose.NewSigner(sk, (&jose.SignerOptions{}).WithHeader("kid", "test"))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jws, err := signer.Sign(payload)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		raw, _ := jws.CompactSerialize()
		resp["id_token"] = raw
	}
	writeJSON(w, resp)
}

func (p *Provider) jwks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
