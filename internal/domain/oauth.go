package domain

import (
	"math"
	"strings"
	"time"
)

// OAuthApplication は Doorkeeper::Application（oauth_applications）。
type OAuthApplication struct {
	ID   int64
	Name string
	UID  string
	// Secret は BCrypt ハッシュ（hash_application_secrets）。
	Secret string
	// RedirectURI は改行（空白）区切りのリダイレクト URI。
	RedirectURI string
	// Scopes は空白区切りのスコープ（権限名と admin）。
	Scopes       string
	Confidential bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ScopeList は Application#scopes（Scopes.from_string）。
func (a *OAuthApplication) ScopeList() []string { return splitScopes(a.Scopes) }

// RedirectURIs は redirect_uri.split。
func (a *OAuthApplication) RedirectURIs() []string { return strings.Fields(a.RedirectURI) }

// OAuthAccessGrant は Doorkeeper::AccessGrant（oauth_access_grants。認可コード）。
type OAuthAccessGrant struct {
	ID              int64
	ResourceOwnerID int64
	ApplicationID   int64
	// Token は認可コードの SHA-256（16 進）。
	Token               string
	ExpiresIn           int
	RedirectURI         string
	CreatedAt           time.Time
	RevokedAt           *time.Time
	Scopes              string
	CodeChallenge       string
	CodeChallengeMethod string
}

// ScopeList は AccessGrant#scopes。
func (g *OAuthAccessGrant) ScopeList() []string { return splitScopes(g.Scopes) }

// Expired は Expirable#expired?。
func (g *OAuthAccessGrant) Expired(now time.Time) bool {
	return now.UTC().After(g.CreatedAt.Add(time.Duration(g.ExpiresIn) * time.Second))
}

// Revoked は Revocable#revoked?。
func (g *OAuthAccessGrant) Revoked(now time.Time) bool {
	return g.RevokedAt != nil && !g.RevokedAt.After(now)
}

// Accessible は Accessible#accessible?。
func (g *OAuthAccessGrant) Accessible(now time.Time) bool { return !g.Expired(now) && !g.Revoked(now) }

// UsesPKCE は AccessGrant#uses_pkce?。
func (g *OAuthAccessGrant) UsesPKCE() bool { return strings.TrimSpace(g.CodeChallenge) != "" }

// OAuthAccessToken は Doorkeeper::AccessToken（oauth_access_tokens）。
type OAuthAccessToken struct {
	ID              int64
	ResourceOwnerID *int64
	ApplicationID   *int64
	// Token / RefreshToken は SHA-256（16 進）。RefreshToken は無ければ ""。
	Token        string
	RefreshToken string
	// ExpiresIn は有効期間（秒）。nil は無期限。
	ExpiresIn            *int
	RevokedAt            *time.Time
	CreatedAt            time.Time
	Scopes               string
	PreviousRefreshToken string
}

// ScopeList は AccessToken#scopes。
func (t *OAuthAccessToken) ScopeList() []string { return splitScopes(t.Scopes) }

// Expired は Expirable#expired?。
func (t *OAuthAccessToken) Expired(now time.Time) bool {
	return t.ExpiresIn != nil && now.UTC().After(t.CreatedAt.Add(time.Duration(*t.ExpiresIn)*time.Second))
}

// Revoked は Revocable#revoked?。
func (t *OAuthAccessToken) Revoked(now time.Time) bool {
	return t.RevokedAt != nil && !t.RevokedAt.After(now)
}

// Accessible は Accessible#accessible?。
func (t *OAuthAccessToken) Accessible(now time.Time) bool { return !t.Expired(now) && !t.Revoked(now) }

// ExpiresInSeconds は Expirable#expires_in_seconds（nil は無期限）。
func (t *OAuthAccessToken) ExpiresInSeconds(now time.Time) *int {
	if t.ExpiresIn == nil {
		return nil
	}
	exp := t.CreatedAt.Add(time.Duration(*t.ExpiresIn) * time.Second)
	sec := int(math.Round(exp.Sub(now).Seconds()))
	if sec < 0 {
		sec = 0
	}
	return &sec
}

func splitScopes(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		dup := false
		for _, x := range out {
			if x == f {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, f)
		}
	}
	return out
}
