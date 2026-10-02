package domain

import "time"

// 認証方式の種類（auth_sources.kind）。Redmine の STI（AuthSourceLdap）を kind で表す。
const (
	AuthSourceKindLDAP = "ldap"
	AuthSourceKindOIDC = "oidc"
)

// AuthSourceRecord は auth_sources の 1 行（設定は種類ごとの config JSON、秘密値は secret 列）。
type AuthSourceRecord struct {
	ID               int64
	Kind             string
	Name             string
	Enabled          bool
	Position         int
	OntheflyRegister bool
	// Config は種類ごとの設定（LDAP は host, port, account, base_dn, filter, timeout, tls, verify_peer,
	// starttls, attr_login, attr_firstname, attr_lastname, attr_mail）。未知のキーも保持する。
	Config map[string]any
	// Secret は暗号化された秘密値（LDAP の account_password）。nil は未設定。
	Secret    *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AuthMethodName は auth_method_name（一覧の「種類」列）。
func (r *AuthSourceRecord) AuthMethodName() string {
	switch r.Kind {
	case AuthSourceKindLDAP:
		return "LDAP"
	case AuthSourceKindOIDC:
		// buropher 拡張（Redmine に無い種類）
		return "OIDC"
	}
	return "Abstract"
}

// ConfigString は config の文字列値（nil・非文字列は ("", false)）。
func (r *AuthSourceRecord) ConfigString(key string) (string, bool) {
	s, ok := r.Config[key].(string)
	return s, ok
}

// ConfigInt は config の整数値（JSON の数値。無ければ (0, false)）。
func (r *AuthSourceRecord) ConfigInt(key string) (int, bool) {
	switch v := r.Config[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

// ConfigBool は config の真偽値。
func (r *AuthSourceRecord) ConfigBool(key string) bool {
	b, _ := r.Config[key].(bool)
	return b
}

// Host は source.host（一覧の「ホスト」列。LDAP 以外は空）。
func (r *AuthSourceRecord) Host() string {
	s, _ := r.ConfigString("host")
	return s
}
