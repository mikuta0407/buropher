// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package oidc_test

import (
	"context"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/oidc"
	"github.com/mikuta0407/buropher/internal/auth/oidc/oidctest"
)

// このファイルは ID トークンの検証まわりの攻撃（alg の取り違え・azp・nOAuth）のテスト。

func newGenericProvider(t *testing.T, idp *oidctest.Provider) *oidc.Provider {
	t.Helper()
	p, err := oidc.New(context.Background(), nil, oidc.Config{Issuer: idp.URL(), ClientID: idp.ClientID, ClientSecret: idp.ClientSecret,
		RedirectURL: "http://localhost/cb"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func exchange(t *testing.T, p *oidc.Provider) (*oidc.Identity, error) {
	t.Helper()
	req := oidc.NewAuthRequest()
	return p.Exchange(context.Background(), authorize(t, p, req), req)
}

// alg=none や、クライアントシークレットを鍵にした HS256 の ID トークンは受け付けない。
func TestIDTokenAlgConfusionRejected(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	idp.ClientSecret = "a-client-secret-that-is-long-enough-for-hs256"
	p := newGenericProvider(t, idp)
	idp.SetClaims(map[string]any{"sub": "s1"})
	idp.Unsigned = true
	if _, err := exchange(t, p); oidc.Reason(err) != "id_token" {
		t.Fatalf("alg=none: %v", err)
	}
	idp.Unsigned = false
	idp.SignHS256 = true
	if _, err := exchange(t, p); oidc.Reason(err) != "id_token" {
		t.Fatalf("HS256: %v", err)
	}
	idp.SignHS256 = false
	if _, err := exchange(t, p); err != nil {
		t.Fatalf("RS256: %v", err)
	}
}

// 複数の aud を持つ ID トークンは azp がこのクライアントでなければ拒否する（OIDC Core 3.1.3.7）。
// 別のクライアント向けに発行され、ついでにこちらも aud に含めたトークンを使い回させない。
func TestIDTokenAuthorizedParty(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	p := newGenericProvider(t, idp)
	idp.Audience = []any{"other-client", idp.ClientID}
	idp.SetClaims(map[string]any{"sub": "s1"})
	if _, err := exchange(t, p); oidc.Reason(err) != "id_token" {
		t.Fatalf("multiple aud without azp: %v", err)
	}
	idp.SetClaims(map[string]any{"sub": "s1", "azp": "other-client"})
	if _, err := exchange(t, p); oidc.Reason(err) != "id_token" {
		t.Fatalf("multiple aud with foreign azp: %v", err)
	}
	idp.SetClaims(map[string]any{"sub": "s1", "azp": idp.ClientID})
	if _, err := exchange(t, p); err != nil {
		t.Fatalf("multiple aud with our azp: %v", err)
	}
	// aud が 1 つでも azp が別のクライアントなら拒否
	idp.Audience = nil
	idp.SetClaims(map[string]any{"sub": "s1", "azp": "other-client"})
	if _, err := exchange(t, p); oidc.Reason(err) != "id_token" {
		t.Fatalf("single aud with foreign azp: %v", err)
	}
}

// マルチテナントの Entra ID では、どのテナントの管理者でも email / preferred_username を任意の値にできる
// （nOAuth）。xms_edov（メールのドメイン所有者が検証済み）が true でなければ未検証のメールとして扱う。
func TestEntraMultiTenantMailNeedsDomainOwnerVerification(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	tid := "11111111-2222-3333-4444-555555555555"
	idp.DiscoveryIssuer = "https://login.microsoftonline.com/{tenantid}/v2.0"
	idp.Issuer = oidc.EntraIssuer(tid)
	p, err := oidc.New(context.Background(), nil, oidc.Config{Preset: oidc.PresetEntra, Tenant: "common", Issuer: idp.URL(),
		ClientID: idp.ClientID, ClientSecret: idp.ClientSecret, RedirectURL: "http://localhost/cb"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		claims     map[string]any
		unverified bool
	}{
		{map[string]any{"oid": "o1", "tid": tid, "email": "admin@victim.example"}, true},
		{map[string]any{"oid": "o1", "tid": tid, "preferred_username": "admin@victim.example"}, true},
		{map[string]any{"oid": "o1", "tid": tid, "email": "admin@victim.example", "xms_edov": false}, true},
		{map[string]any{"oid": "o1", "tid": tid, "email": "admin@victim.example", "xms_edov": true}, false},
	} {
		idp.SetClaims(c.claims)
		id, err := exchange(t, p)
		if err != nil {
			t.Fatal(err)
		}
		if id.Mail != "admin@victim.example" || id.MailUnverified != c.unverified {
			t.Fatalf("%v: mail=%q unverified=%v", c.claims, id.Mail, id.MailUnverified)
		}
	}
	// シングルテナントの構成（テナントの管理者は信頼する）では従来どおり
	idp.DiscoveryIssuer = idp.URL()
	idp.Issuer = idp.URL()
	sp, err := oidc.New(context.Background(), nil, oidc.Config{Preset: oidc.PresetEntra, Issuer: idp.URL(),
		ClientID: idp.ClientID, ClientSecret: idp.ClientSecret, RedirectURL: "http://localhost/cb"})
	if err != nil {
		t.Fatal(err)
	}
	idp.SetClaims(map[string]any{"oid": "o1", "tid": tid, "email": "alice@contoso.example"})
	if id, err := exchange(t, sp); err != nil || id.MailUnverified {
		t.Fatalf("single tenant: %v %+v", err, id)
	}
}
