package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはアカウント・認証まわり（パスワード再発行、自己登録、2 要素認証、外部 ID 連携、
// 認証方式のグループ対応表）の読み書き。

// FindUserByMail は User.find_by_mail(mail)（追加のメールアドレスも含め、大文字小文字を区別しない）。
// ユーザーの状態は問わない。見つからなければ (nil, ErrNotFound)。
func FindUserByMail(ctx context.Context, q db.Queryer, mail string) (*domain.User, error) {
	var r userRow
	err := q.Get(ctx, &r, userSelect+` WHERE p.kind = 'user' AND p.id = (SELECT e.user_id FROM email_addresses e WHERE LOWER(e.address) = ? ORDER BY e.id LIMIT 1)`,
		strings.ToLower(strings.TrimSpace(mail)))
	if err != nil {
		return nil, notFound(err)
	}
	return r.user(), nil
}

// UpdateUserStatus はユーザーの状態だけを変える（User#activate! / register! / lock!。updated_at も更新する）。
func UpdateUserStatus(ctx context.Context, q db.Queryer, userID int64, status int, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE principals SET status = ?, updated_at = ? WHERE id = ?`, status, db.NewTime(now), userID)
	return err
}

// ---------------------------------------------------------------- 2 要素認証

// GetTwofaState は user_accounts の twofa_* 列。
func GetTwofaState(ctx context.Context, q db.Queryer, userID int64) (*domain.TwofaState, error) {
	var r struct {
		Scheme   sql.NullString `db:"twofa_scheme"`
		Key      sql.NullString `db:"twofa_totp_key"`
		LastUsed sql.NullInt64  `db:"twofa_totp_last_used_at"`
	}
	if err := q.Get(ctx, &r, `SELECT twofa_scheme, twofa_totp_key, twofa_totp_last_used_at FROM user_accounts WHERE principal_id = ?`, userID); err != nil {
		return nil, notFound(err)
	}
	st := &domain.TwofaState{Scheme: r.Scheme.String, TotpKey: r.Key.String}
	if r.LastUsed.Valid {
		v := r.LastUsed.Int64
		st.TotpLastUsedAt = &v
	}
	return st, nil
}

// SetTwofaTotpKey は twofa_totp_key を設定する（Totp#init_pairing!。"" は NULL）。
func SetTwofaTotpKey(ctx context.Context, q db.Queryer, userID int64, sealedKey string) error {
	_, err := q.Exec(ctx, `UPDATE user_accounts SET twofa_totp_key = ? WHERE principal_id = ?`, nullString(sealedKey), userID)
	return err
}

// SetTwofaTotpLastUsed は twofa_totp_last_used_at を設定する（Totp#verify_otp!）。
// 保存済みの値が at 以上なら（同じコードを並行するリクエストで先に使われた）更新せず false を返す
// （読み込み済みの値での判定と更新の間に同じコードが二度通らないように）。
func SetTwofaTotpLastUsed(ctx context.Context, q db.Queryer, userID, at int64) (bool, error) {
	res, err := q.Exec(ctx, `UPDATE user_accounts SET twofa_totp_last_used_at = ? WHERE principal_id = ?
AND (twofa_totp_last_used_at IS NULL OR twofa_totp_last_used_at < ?)`, at, userID, at)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ActivateTwofa は confirm_pairing!（twofa_scheme を設定し、after_save :destroy_tokens で
// recovery / autologin / session トークンを削除する）。
func ActivateTwofa(ctx context.Context, q db.Queryer, userID int64, scheme string, now time.Time) error {
	if _, err := q.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = ? WHERE principal_id = ?`, scheme, userID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE principals SET updated_at = ? WHERE id = ?`, db.NewTime(now), userID); err != nil {
		return err
	}
	return DeleteUserTokensByActions(ctx, q, userID, "recovery", "autologin", "session")
}

// DeactivateTwofa は destroy_pairing_without_verify!（鍵・最終使用時刻・方式・バックアップコードを消す）。
func DeactivateTwofa(ctx context.Context, q db.Queryer, userID int64, now time.Time) error {
	if _, err := q.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = NULL, twofa_totp_key = NULL, twofa_totp_last_used_at = NULL WHERE principal_id = ?`, userID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE principals SET updated_at = ? WHERE id = ?`, db.NewTime(now), userID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM twofa_backup_codes WHERE user_id = ?`, userID)
	return err
}

// ReplaceTwofaBackupCodes は init_backup_codes!（既存を消して digests を作る）。
func ReplaceTwofaBackupCodes(ctx context.Context, q db.Queryer, userID int64, digests []string, now time.Time) error {
	if _, err := q.Exec(ctx, `DELETE FROM twofa_backup_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	t := db.NewTime(now)
	for _, d := range digests {
		if _, err := q.Exec(ctx, `INSERT INTO twofa_backup_codes (user_id, code_digest, created_at) VALUES (?, ?, ?)`, userID, d, t); err != nil {
			return err
		}
	}
	return nil
}

// ConsumeTwofaBackupCode は verify_backup_code!（一致するコードを削除し、あれば true）。
func ConsumeTwofaBackupCode(ctx context.Context, q db.Queryer, userID int64, digest string) (bool, error) {
	res, err := q.Exec(ctx, `DELETE FROM twofa_backup_codes WHERE user_id = ? AND code_digest = ?`, userID, digest)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TwofaBackupCodesCreatedAt は backup_codes.map(&:created_on).max（無ければ nil）。
func TwofaBackupCodesCreatedAt(ctx context.Context, q db.Queryer, userID int64) (*time.Time, error) {
	var t db.NullTime
	if err := q.Get(ctx, &t, `SELECT MAX(created_at) FROM twofa_backup_codes WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	return t.Ptr(), nil
}

// ---------------------------------------------------------------- 外部 ID 連携（user_identities）

type identityRow struct {
	ID           int64                   `db:"id"`
	UserID       int64                   `db:"user_id"`
	Provider     string                  `db:"provider"`
	AuthSourceID sql.NullInt64           `db:"auth_source_id"`
	Subject      string                  `db:"subject"`
	Email        sql.NullString          `db:"email"`
	RawClaims    db.JSON[map[string]any] `db:"raw_claims"`
	CreatedAt    db.Time                 `db:"created_at"`
	LastLoginAt  db.NullTime             `db:"last_login_at"`
}

const identityCols = `id, user_id, provider, auth_source_id, subject, email, raw_claims, created_at, last_login_at`

func (r identityRow) domain() *domain.UserIdentity {
	out := &domain.UserIdentity{ID: r.ID, UserID: r.UserID, Provider: r.Provider, Subject: r.Subject, Email: r.Email.String,
		RawClaims: r.RawClaims.V, CreatedAt: r.CreatedAt.Time, LastLoginAt: r.LastLoginAt.Ptr()}
	if r.AuthSourceID.Valid {
		v := r.AuthSourceID.Int64
		out.AuthSourceID = &v
	}
	return out
}

// FindUserIdentity は (provider, subject) の連携を返す。無ければ (nil, ErrNotFound)。
func FindUserIdentity(ctx context.Context, q db.Queryer, provider, subject string) (*domain.UserIdentity, error) {
	var r identityRow
	if err := q.Get(ctx, &r, `SELECT `+identityCols+` FROM user_identities WHERE provider = ? AND subject = ?`, provider, subject); err != nil {
		return nil, notFound(err)
	}
	return r.domain(), nil
}

// UserIdentities はユーザーの連携一覧（id 順）。
func UserIdentities(ctx context.Context, q db.Queryer, userID int64) ([]*domain.UserIdentity, error) {
	var rows []identityRow
	if err := q.Select(ctx, &rows, `SELECT `+identityCols+` FROM user_identities WHERE user_id = ? ORDER BY id`, userID); err != nil {
		return nil, err
	}
	out := make([]*domain.UserIdentity, len(rows))
	for i, r := range rows {
		out[i] = r.domain()
	}
	return out, nil
}

// UserIdentityForSource はユーザーの認証方式 sourceID の連携（無ければ ErrNotFound）。
func UserIdentityForSource(ctx context.Context, q db.Queryer, userID int64, provider string) (*domain.UserIdentity, error) {
	var r identityRow
	if err := q.Get(ctx, &r, `SELECT `+identityCols+` FROM user_identities WHERE user_id = ? AND provider = ? ORDER BY id LIMIT 1`, userID, provider); err != nil {
		return nil, notFound(err)
	}
	return r.domain(), nil
}

// CreateUserIdentity は連携を作成する。
func CreateUserIdentity(ctx context.Context, q db.Queryer, id *domain.UserIdentity) error {
	var claims any
	if id.RawClaims != nil {
		claims = db.NewJSON(id.RawClaims)
	}
	newID, err := q.InsertReturningID(ctx, `INSERT INTO user_identities (user_id, provider, auth_source_id, subject, email, raw_claims, created_at, last_login_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id.UserID, id.Provider, id.AuthSourceID, id.Subject, nullString(id.Email), claims,
		db.NewTime(id.CreatedAt), nullTimePtr(id.LastLoginAt))
	if err != nil {
		return err
	}
	id.ID = newID
	return nil
}

// TouchUserIdentity はログイン時に連携のメール・クレーム・最終ログイン時刻を更新する。
func TouchUserIdentity(ctx context.Context, q db.Queryer, id int64, email string, claims map[string]any, now time.Time) error {
	var c any
	if claims != nil {
		c = db.NewJSON(claims)
	}
	_, err := q.Exec(ctx, `UPDATE user_identities SET email = ?, raw_claims = ?, last_login_at = ? WHERE id = ?`, nullString(email), c, db.NewTime(now), id)
	return err
}

// DeleteUserIdentity はユーザーの連携を削除する（他人の連携は消さない）。削除したら true。
func DeleteUserIdentity(ctx context.Context, q db.Queryer, userID, id int64) (bool, error) {
	res, err := q.Exec(ctx, `DELETE FROM user_identities WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---------------------------------------------------------------- グループ対応表

// AuthSourceGroupMappings は認証方式のグループ対応表（id 順）。
func AuthSourceGroupMappings(ctx context.Context, q db.Queryer, sourceID int64) ([]domain.AuthSourceGroupMapping, error) {
	var rows []struct {
		ID            int64  `db:"id"`
		AuthSourceID  int64  `db:"auth_source_id"`
		ExternalGroup string `db:"external_group"`
		GroupID       int64  `db:"group_id"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, auth_source_id, external_group, group_id FROM auth_source_group_mappings WHERE auth_source_id = ? ORDER BY id`, sourceID); err != nil {
		return nil, err
	}
	out := make([]domain.AuthSourceGroupMapping, len(rows))
	for i, r := range rows {
		out[i] = domain.AuthSourceGroupMapping{ID: r.ID, AuthSourceID: r.AuthSourceID, ExternalGroup: r.ExternalGroup, GroupID: r.GroupID}
	}
	return out, nil
}

// ReplaceAuthSourceGroupMappings は認証方式のグループ対応表を置き換える。
func ReplaceAuthSourceGroupMappings(ctx context.Context, q db.Queryer, sourceID int64, ms []domain.AuthSourceGroupMapping) error {
	if _, err := q.Exec(ctx, `DELETE FROM auth_source_group_mappings WHERE auth_source_id = ?`, sourceID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, m := range ms {
		k := m.ExternalGroup + "\x00" + itoa64(m.GroupID)
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, err := q.Exec(ctx, `INSERT INTO auth_source_group_mappings (auth_source_id, external_group, group_id) VALUES (?, ?, ?)`,
			sourceID, m.ExternalGroup, m.GroupID); err != nil {
			return err
		}
	}
	return nil
}

// OIDCAuthSources は有効な OIDC 認証方式（position, id 順）。
func OIDCAuthSources(ctx context.Context, q db.Queryer) ([]*domain.AuthSourceRecord, error) {
	return selectAuthSources(ctx, q, `WHERE kind = 'oidc' AND enabled = TRUE ORDER BY position, id`)
}
