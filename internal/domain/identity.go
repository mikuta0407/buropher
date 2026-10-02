package domain

import "time"

// UserIdentity は外部 ID 連携（user_identities。buropher 拡張）。OIDC では provider = "oidc:<auth_source_id>"。
type UserIdentity struct {
	ID           int64
	UserID       int64
	Provider     string
	AuthSourceID *int64
	Subject      string
	Email        string
	RawClaims    map[string]any
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

// AuthSourceGroupMapping は認証方式の外部グループ → buropher グループの対応（auth_source_group_mappings）。
type AuthSourceGroupMapping struct {
	ID            int64
	AuthSourceID  int64
	ExternalGroup string
	GroupID       int64
}

// TwofaState は 2 要素認証（TOTP）の保存状態（user_accounts の twofa_* 列）。
type TwofaState struct {
	Scheme string
	// TotpKey は暗号化された（または平文の）TOTP 鍵。"" = 未設定。
	TotpKey string
	// TotpLastUsedAt は最後に検証に成功したタイムステップの UNIX 秒（nil = なし）。
	TotpLastUsedAt *int64
}
