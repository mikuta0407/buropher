// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package oidc_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/oidc"
	"github.com/mikuta0407/buropher/internal/auth/oidc/oidctest"
)

// authorize は認可リクエストを IdP に送り、callback に渡される code を返す。
func authorize(t *testing.T, p *oidc.Provider, req oidc.AuthRequest) string {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(p.AuthCodeURL(req))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	u, err := url.Parse(res.Header.Get("Location"))
	if err != nil || u.Query().Get("state") != req.State {
		t.Fatalf("authorize redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	return u.Query().Get("code")
}

func TestEntraMultiTenant(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	tid := "72f988bf-86f1-41af-91ab-2d7cd011db47"
	// ディスカバリ文書の issuer はテンプレート、ID トークンの iss はテナント固有
	idp.DiscoveryIssuer = "https://login.microsoftonline.com/{tenantid}/v2.0"
	idp.Issuer = oidc.EntraIssuer(tid)
	cfg := oidc.Config{Preset: oidc.PresetEntra, Tenant: "organizations", Issuer: idp.URL(), ClientID: idp.ClientID, ClientSecret: idp.ClientSecret,
		RedirectURL: "http://localhost/auth/oidc/1/callback", AllowedTenants: []string{tid}}
	if !cfg.MultiTenant() {
		t.Fatal("organizations should be multi-tenant")
	}
	p, err := oidc.New(context.Background(), nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	idp.SetClaims(map[string]any{"sub": "pairwise-sub", "oid": "object-id", "tid": tid, "preferred_username": "alice@contoso.com",
		"given_name": "Alice", "family_name": "Smith", "groups": []any{"g1", "g2"}})
	req := oidc.NewAuthRequest()
	id, err := p.Exchange(context.Background(), authorize(t, p, req), req)
	if err != nil {
		t.Fatal(err)
	}
	// Entra プリセット: subject は oid、メールが無ければ preferred_username
	if id.Subject != "object-id" || id.Tenant != tid || id.Mail != "alice@contoso.com" || id.Login != "alice@contoso.com" ||
		id.Firstname != "Alice" || strings.Join(id.Groups, ",") != "g1,g2" {
		t.Fatalf("identity: %+v", id)
	}
	if err := p.Authorize(id); err != nil {
		t.Fatal(err)
	}
	// マルチテナントでは preferred_username（UPN）も変更可能で認可に使えない（Microsoft の指針）ため、
	// xms_edov（ドメイン所有の確認済み）が true でなければ UPN 由来のメールも未検証として扱う（nOAuth）
	if !id.MailUnverified {
		t.Error("UPN fallback mail without xms_edov should be unverified in multi-tenant mode")
	}
	for _, c := range []struct {
		edov       any
		unverified bool
	}{{nil, true}, {false, true}, {true, false}, {"1", false}} {
		claims := map[string]any{"oid": "object-id", "tid": tid, "preferred_username": "alice@contoso.com", "email": "admin@victim.example"}
		if c.edov != nil {
			claims["xms_edov"] = c.edov
		}
		idp.SetClaims(claims)
		req := oidc.NewAuthRequest()
		id, err := p.Exchange(context.Background(), authorize(t, p, req), req)
		if err != nil {
			t.Fatal(err)
		}
		if id.MailUnverified != c.unverified {
			t.Errorf("xms_edov=%v: MailUnverified = %v", c.edov, id.MailUnverified)
		}
	}
	// 別テナントの ID トークン（iss と tid が一致しない）は拒否
	idp.SetClaims(map[string]any{"oid": "x", "tid": "other-tenant"})
	req = oidc.NewAuthRequest()
	if _, err := p.Exchange(context.Background(), authorize(t, p, req), req); oidc.Reason(err) != "id_token" {
		t.Fatalf("tenant/issuer mismatch: %v", err)
	}
	// 許可されていないテナント
	if err := p.Authorize(&oidc.Identity{Tenant: "other"}); oidc.Reason(err) != "tenant" {
		t.Fatalf("tenant restriction: %v", err)
	}
}

func TestGroupsOverageAndDefaults(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	p, err := oidc.New(context.Background(), nil, oidc.Config{Issuer: idp.URL() + "/", ClientID: idp.ClientID, ClientSecret: idp.ClientSecret,
		RedirectURL: "http://localhost/cb", Scopes: []string{"email"}, AllowedGroups: []string{"Staff"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Config().Scopes, " "); got != "openid email" {
		t.Fatalf("scopes: %s", got)
	}
	idp.SetClaims(map[string]any{"sub": "s1", "email": "a@example.net", "_claim_names": map[string]any{"groups": "src1"},
		"_claim_sources": map[string]any{"src1": map[string]any{"endpoint": "https://graph.microsoft.com/v1.0/users/x/getMemberObjects"}}})
	req := oidc.NewAuthRequest()
	id, err := p.Exchange(context.Background(), authorize(t, p, req), req)
	if err != nil {
		t.Fatal(err)
	}
	if !id.GroupsOverage || id.Subject != "s1" {
		t.Fatalf("overage: %+v", id)
	}
	if oidc.Reason(p.Authorize(id)) != "group" {
		t.Fatal("allowed groups must deny a user without groups")
	}
	if err := p.Authorize(&oidc.Identity{Groups: []string{"staff"}}); err != nil {
		t.Fatalf("groups are compared case-insensitively: %v", err)
	}
	if u := p.EndSessionURL("tok", "http://localhost/"); !strings.HasPrefix(u, idp.URL()+"/logout?") || !strings.Contains(u, "id_token_hint=tok") {
		t.Fatalf("end session: %s", u)
	}
	// 認可コードの再利用は拒否される
	req = oidc.NewAuthRequest()
	code := authorize(t, p, req)
	if _, err := p.Exchange(context.Background(), code, req); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exchange(context.Background(), code, req); oidc.Reason(err) != "token" {
		t.Fatalf("code reuse: %v", err)
	}
	if _, err := oidc.New(context.Background(), nil, oidc.Config{Issuer: "http://127.0.0.1:1"}); oidc.Reason(err) != "discovery" {
		t.Fatalf("discovery failure: %v", err)
	}
}
