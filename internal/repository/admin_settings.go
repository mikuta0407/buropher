package repository

import (
	"context"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルは管理画面（admin#index / settings#edit）で使う問い合わせ。

// NoConfigurationData は Redmine::DefaultData::Loader.no_data?
// （組込み以外のロール・トラッカー・ステータス・列挙・クエリがすべて無い）。
func NoConfigurationData(ctx context.Context, q db.Queryer) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT
  (SELECT COUNT(*) FROM roles WHERE builtin = 0) +
  (SELECT COUNT(*) FROM trackers) +
  (SELECT COUNT(*) FROM issue_statuses) +
  (SELECT COUNT(*) FROM issue_priorities) +
  (SELECT COUNT(*) FROM time_entry_activities) +
  (SELECT COUNT(*) FROM document_categories) +
  (SELECT COUNT(*) FROM queries)`)
	return n == 0, err
}

// UnpairAllTwofa は Redmine::Twofa.unpair_all!（全ユーザーの 2FA ペアリングとバックアップコードを削除する）。
func UnpairAllTwofa(ctx context.Context, q db.Queryer) error {
	if _, err := q.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = NULL, twofa_totp_key = NULL, twofa_totp_last_used_at = NULL
WHERE twofa_scheme IS NOT NULL`); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM twofa_backup_codes`)
	return err
}

// DefaultAdminAccountChanged は User.default_admin_account_changed?
// （login が admin で、パスワードが "admin" の有効な管理者が居なければ true）。
// パスワードの照合は呼び出し側で行うため、ここでは候補のハッシュを返す。
func DefaultAdminPasswordHashes(ctx context.Context, q db.Queryer) ([]string, error) {
	var hashes []string
	err := q.Select(ctx, &hashes, `SELECT ua.password_hash FROM user_accounts ua JOIN principals p ON p.id = ua.principal_id
WHERE ua.login = 'admin' AND p.status = 1 AND ua.password_hash IS NOT NULL`)
	return hashes, err
}
