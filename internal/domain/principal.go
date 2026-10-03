// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package domain は Redmine のモデルに相当するドメイン型を定義する。
//
// 型名・メソッド名は Redmine の語彙 (Principal / User / Group / Project / Role /
// Member / MemberRole / EnabledModule ...) をそのまま使う。DB アクセスは持たず、
// 読み書きは internal/repository、DB を要する権限判定は internal/authz が担う。
package domain

import (
	"time"
)

// PrincipalKind は principals.kind (旧 users.type の STI) の値。
type PrincipalKind string

const (
	KindUser           PrincipalKind = "user"             // User
	KindAnonymousUser  PrincipalKind = "anonymous_user"   // AnonymousUser
	KindGroup          PrincipalKind = "group"            // Group
	KindGroupAnonymous PrincipalKind = "group_anonymous"  // GroupAnonymous
	KindGroupNonMember PrincipalKind = "group_non_member" // GroupNonMember
)

// IsUser は User / AnonymousUser (Redmine の User クラス階層) なら true。
func (k PrincipalKind) IsUser() bool { return k == KindUser || k == KindAnonymousUser }

// IsGroup は Group / GroupBuiltin 系なら true。
func (k PrincipalKind) IsGroup() bool {
	return k == KindGroup || k == KindGroupAnonymous || k == KindGroupNonMember
}

// IsBuiltinGroup は GroupAnonymous / GroupNonMember なら true。
func (k PrincipalKind) IsBuiltinGroup() bool {
	return k == KindGroupAnonymous || k == KindGroupNonMember
}

// RedmineType は Redmine の users.type 値を返す (REST API / インポート用)。
func (k PrincipalKind) RedmineType() string {
	switch k {
	case KindUser:
		return "User"
	case KindAnonymousUser:
		return "AnonymousUser"
	case KindGroup:
		return "Group"
	case KindGroupAnonymous:
		return "GroupAnonymous"
	case KindGroupNonMember:
		return "GroupNonMember"
	}
	return ""
}

// KindFromRedmineType は users.type 値から PrincipalKind を返す。未知なら "" 。
func KindFromRedmineType(t string) PrincipalKind {
	switch t {
	case "User":
		return KindUser
	case "AnonymousUser":
		return KindAnonymousUser
	case "Group":
		return KindGroup
	case "GroupAnonymous":
		return KindGroupAnonymous
	case "GroupNonMember":
		return KindGroupNonMember
	}
	return ""
}

// Principal のステータス (Principal::STATUS_*)。
const (
	StatusAnonymous  = 0
	StatusActive     = 1
	StatusRegistered = 2
	StatusLocked     = 3
)

// Principal は principals 行 (Redmine Principal)。
type Principal struct {
	ID        int64
	Kind      PrincipalKind
	Status    int
	Firstname string // ユーザのみ
	Lastname  string // ユーザのみ
	// Name はグループ名 (旧 users.lastname)。ユーザは ""。
	Name          string
	TwofaRequired bool // グループのみ意味を持つ
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Active は Principal#active? (status == ACTIVE)。
func (p *Principal) Active() bool { return p.Status == StatusActive }

// Locked は status == LOCKED。
func (p *Principal) Locked() bool { return p.Status == StatusLocked }

// Registered は status == REGISTERED。
func (p *Principal) Registered() bool { return p.Status == StatusRegistered }

// User は User / AnonymousUser (principals + user_accounts)。
type User struct {
	Principal
	Login              string
	PasswordHash       string // "" = パスワードなし
	PasswordChangedAt  *time.Time
	MustChangePassword bool
	// AdminFlag は DB の admin 列。判定には IsAdmin を使うこと (OAuth スコープを考慮する)。
	AdminFlag    bool
	Language     string // "" = 既定
	AuthSourceID *int64
	LastLoginAt  *time.Time
	TwofaScheme  string
	// Mail は既定のメールアドレス (User#mail, email_addresses.is_default)。無ければ ""。
	Mail string

	// OAuthScope は OAuth で認証されたリクエストのスコープ (User#oauth_scope=)。
	// nil = OAuth 認証ではない。空スライス (非 nil) = スコープなしの OAuth 認証。
	OAuthScope []string
}

// Logged は User#logged? (匿名ユーザなら false)。
func (u *User) Logged() bool { return u != nil && u.Kind != KindAnonymousUser }

// Anonymous は User#anonymous?。
func (u *User) Anonymous() bool { return !u.Logged() }

// AuthorizedByOAuth は User#authorized_by_oauth?。
func (u *User) AuthorizedByOAuth() bool { return u.OAuthScope != nil }

// IsAdmin は User#admin?。匿名ユーザは常に false。OAuth 認証時は :admin スコープがある場合のみ true。
func (u *User) IsAdmin() bool {
	if u == nil || u.Kind == KindAnonymousUser || !u.AdminFlag {
		return false
	}
	if u.AuthorizedByOAuth() {
		for _, s := range u.OAuthScope {
			if s == "admin" {
				return true
			}
		}
		return false
	}
	return true
}

// BuiltinRoleBuiltin は User#builtin_role の builtin 値 (匿名なら Anonymous、それ以外は Non member)。
func (u *User) BuiltinRoleBuiltin() int {
	if u.Anonymous() {
		return RoleBuiltinAnonymous
	}
	return RoleBuiltinNonMember
}

// BuiltinGroupKind は User#builtin_role に対応する組込グループの kind
// (匿名なら GroupAnonymous、それ以外は GroupNonMember)。
func (u *User) BuiltinGroupKind() PrincipalKind {
	if u.Anonymous() {
		return KindGroupAnonymous
	}
	return KindGroupNonMember
}

// Group は Group / GroupAnonymous / GroupNonMember。
type Group struct {
	Principal
}

// Builtin は Group#builtin? (組込グループなら true)。
func (g *Group) Builtin() bool { return g.Kind.IsBuiltinGroup() }

// Givable は Group#givable? (ユーザを所属させられるグループなら true)。
func (g *Group) Givable() bool { return !g.Builtin() }

// BuiltinType は Group#builtin_type ("anonymous" / "non_member" / "")。
func (g *Group) BuiltinType() string {
	switch g.Kind {
	case KindGroupAnonymous:
		return "anonymous"
	case KindGroupNonMember:
		return "non_member"
	}
	return ""
}
