package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは Doorkeeper のモデル（Doorkeeper::Application / AccessGrant / AccessToken。
// oauth_applications / oauth_access_grants / oauth_access_tokens）の SQL。
// トークン・認可コードは呼び出し側で SHA-256 にしたもの（保存形式）で検索する。

const oauthAppColumns = `id, name, uid, secret, redirect_uri, scopes, confidential, created_at, updated_at`

type oauthAppRow struct {
	ID           int64   `db:"id"`
	Name         string  `db:"name"`
	UID          string  `db:"uid"`
	Secret       string  `db:"secret"`
	RedirectURI  string  `db:"redirect_uri"`
	Scopes       string  `db:"scopes"`
	Confidential bool    `db:"confidential"`
	CreatedAt    db.Time `db:"created_at"`
	UpdatedAt    db.Time `db:"updated_at"`
}

func (r *oauthAppRow) app() *domain.OAuthApplication {
	return &domain.OAuthApplication{ID: r.ID, Name: r.Name, UID: r.UID, Secret: r.Secret, RedirectURI: r.RedirectURI,
		Scopes: r.Scopes, Confidential: r.Confidential, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

func oauthApps(rows []oauthAppRow) []*domain.OAuthApplication {
	out := make([]*domain.OAuthApplication, len(rows))
	for i := range rows {
		out[i] = rows[i].app()
	}
	return out
}

// OAuthApplications は Application.ordered_by(:created_at)。
func OAuthApplications(ctx context.Context, q db.Queryer) ([]*domain.OAuthApplication, error) {
	var rows []oauthAppRow
	if err := q.Select(ctx, &rows, `SELECT `+oauthAppColumns+` FROM oauth_applications ORDER BY created_at, id`); err != nil {
		return nil, err
	}
	return oauthApps(rows), nil
}

// GetOAuthApplication は Application.find(id)。
func GetOAuthApplication(ctx context.Context, q db.Queryer, id int64) (*domain.OAuthApplication, error) {
	var r oauthAppRow
	if err := q.Get(ctx, &r, `SELECT `+oauthAppColumns+` FROM oauth_applications WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return r.app(), nil
}

// FindOAuthApplicationByUID は Application.by_uid(uid)。
func FindOAuthApplicationByUID(ctx context.Context, q db.Queryer, uid string) (*domain.OAuthApplication, error) {
	var r oauthAppRow
	if err := q.Get(ctx, &r, `SELECT `+oauthAppColumns+` FROM oauth_applications WHERE uid = ?`, uid); err != nil {
		return nil, notFound(err)
	}
	return r.app(), nil
}

// OAuthApplicationUIDTaken は validates :uid, uniqueness（exceptID 以外に同じ uid があるか）。
func OAuthApplicationUIDTaken(ctx context.Context, q db.Queryer, uid string, exceptID int64) (bool, error) {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM oauth_applications WHERE uid = ? AND id <> ?`, uid, exceptID); err != nil {
		return false, err
	}
	return n > 0, nil
}

// CreateOAuthApplication はアプリケーションを作成し、ID・作成日時を設定する。
func CreateOAuthApplication(ctx context.Context, q db.Queryer, a *domain.OAuthApplication, at time.Time) error {
	now := db.NewTime(at)
	id, err := q.InsertReturningID(ctx, `INSERT INTO oauth_applications (name, uid, secret, redirect_uri, scopes, confidential, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.Name, a.UID, a.Secret, a.RedirectURI, a.Scopes, a.Confidential, now, now)
	if err != nil {
		return err
	}
	a.ID, a.CreatedAt, a.UpdatedAt = id, now.Time, now.Time
	return nil
}

// UpdateOAuthApplication は名前・リダイレクト URI・スコープ・confidential を更新する
// （変更が無ければ updated_at も変えない。ActiveRecord の partial update と同じ）。
func UpdateOAuthApplication(ctx context.Context, q db.Queryer, a *domain.OAuthApplication, changed bool, at time.Time) error {
	if !changed {
		return nil
	}
	now := db.NewTime(at)
	_, err := q.Exec(ctx, `UPDATE oauth_applications SET name = ?, redirect_uri = ?, scopes = ?, confidential = ?, updated_at = ? WHERE id = ?`,
		a.Name, a.RedirectURI, a.Scopes, a.Confidential, now, a.ID)
	if err == nil {
		a.UpdatedAt = now.Time
	}
	return err
}

// DeleteOAuthApplication は Application#destroy（access_grants / access_tokens は dependent: :delete_all）。
func DeleteOAuthApplication(ctx context.Context, q db.Queryer, id int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM oauth_access_grants WHERE application_id = ?`, id); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM oauth_access_tokens WHERE application_id = ?`, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM oauth_applications WHERE id = ?`, id)
	return err
}

// AuthorizedOAuthApplications は Application.authorized_for(resource_owner)
// （失効していないアクセストークンがあるアプリケーション）。
func AuthorizedOAuthApplications(ctx context.Context, q db.Queryer, ownerID int64) ([]*domain.OAuthApplication, error) {
	var rows []oauthAppRow
	if err := q.Select(ctx, &rows, `SELECT `+oauthAppColumns+` FROM oauth_applications WHERE id IN (
  SELECT DISTINCT application_id FROM oauth_access_tokens WHERE resource_owner_id = ? AND revoked_at IS NULL)
ORDER BY id`, ownerID); err != nil {
		return nil, err
	}
	return oauthApps(rows), nil
}

// RevokeOAuthTokensAndGrantsFor は Application.revoke_tokens_and_grants_for(id, resource_owner)。
func RevokeOAuthTokensAndGrantsFor(ctx context.Context, q db.Queryer, appID, ownerID int64, now time.Time) error {
	t := db.NewTime(now)
	if _, err := q.Exec(ctx, `UPDATE oauth_access_tokens SET revoked_at = ? WHERE resource_owner_id = ? AND application_id = ? AND revoked_at IS NULL`,
		t, ownerID, appID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE oauth_access_grants SET revoked_at = ? WHERE resource_owner_id = ? AND application_id = ? AND revoked_at IS NULL`,
		t, ownerID, appID)
	return err
}

// ---------------------------------------------------------------- 認可コード

const oauthGrantColumns = `id, resource_owner_id, application_id, token, expires_in, redirect_uri, created_at, revoked_at, scopes,
code_challenge, code_challenge_method`

type oauthGrantRow struct {
	ID                  int64          `db:"id"`
	ResourceOwnerID     int64          `db:"resource_owner_id"`
	ApplicationID       int64          `db:"application_id"`
	Token               string         `db:"token"`
	ExpiresIn           int            `db:"expires_in"`
	RedirectURI         string         `db:"redirect_uri"`
	CreatedAt           db.Time        `db:"created_at"`
	RevokedAt           db.NullTime    `db:"revoked_at"`
	Scopes              sql.NullString `db:"scopes"`
	CodeChallenge       sql.NullString `db:"code_challenge"`
	CodeChallengeMethod sql.NullString `db:"code_challenge_method"`
}

// CreateOAuthAccessGrant は AccessGrant.create!（token は保存形式）。
func CreateOAuthAccessGrant(ctx context.Context, q db.Queryer, g *domain.OAuthAccessGrant, at time.Time) error {
	now := db.NewTime(at)
	id, err := q.InsertReturningID(ctx, `INSERT INTO oauth_access_grants (resource_owner_id, application_id, token, expires_in, redirect_uri,
created_at, scopes, code_challenge, code_challenge_method) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ResourceOwnerID, g.ApplicationID, g.Token, g.ExpiresIn, g.RedirectURI, now, g.Scopes,
		nullString(g.CodeChallenge), nullString(g.CodeChallengeMethod))
	if err != nil {
		return err
	}
	g.ID, g.CreatedAt = id, now.Time
	return nil
}

// FindOAuthAccessGrantByToken は AccessGrant.by_token（hashed は保存形式）。
func FindOAuthAccessGrantByToken(ctx context.Context, q db.Queryer, hashed string) (*domain.OAuthAccessGrant, error) {
	var r oauthGrantRow
	if err := q.Get(ctx, &r, `SELECT `+oauthGrantColumns+` FROM oauth_access_grants WHERE token = ?`, hashed); err != nil {
		return nil, notFound(err)
	}
	return &domain.OAuthAccessGrant{ID: r.ID, ResourceOwnerID: r.ResourceOwnerID, ApplicationID: r.ApplicationID, Token: r.Token,
		ExpiresIn: r.ExpiresIn, RedirectURI: r.RedirectURI, CreatedAt: r.CreatedAt.Time, RevokedAt: r.RevokedAt.Ptr(),
		Scopes: r.Scopes.String, CodeChallenge: r.CodeChallenge.String, CodeChallengeMethod: r.CodeChallengeMethod.String}, nil
}

// RevokeOAuthAccessGrant は grant.lock! + raise InvalidGrantReuse if grant.revoked? + grant.revoke。
// 既に失効していれば false。
func RevokeOAuthAccessGrant(ctx context.Context, q db.Queryer, id int64, now time.Time) (bool, error) {
	t := db.NewTime(now)
	res, err := q.Exec(ctx, `UPDATE oauth_access_grants SET revoked_at = ? WHERE id = ? AND (revoked_at IS NULL OR revoked_at > ?)`, t, id, t)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ---------------------------------------------------------------- アクセストークン

const oauthTokenColumns = `id, resource_owner_id, application_id, token, refresh_token, expires_in, revoked_at, created_at, scopes,
previous_refresh_token`

type oauthTokenRow struct {
	ID                   int64          `db:"id"`
	ResourceOwnerID      sql.NullInt64  `db:"resource_owner_id"`
	ApplicationID        sql.NullInt64  `db:"application_id"`
	Token                string         `db:"token"`
	RefreshToken         sql.NullString `db:"refresh_token"`
	ExpiresIn            sql.NullInt64  `db:"expires_in"`
	RevokedAt            db.NullTime    `db:"revoked_at"`
	CreatedAt            db.Time        `db:"created_at"`
	Scopes               sql.NullString `db:"scopes"`
	PreviousRefreshToken string         `db:"previous_refresh_token"`
}

func (r *oauthTokenRow) token() *domain.OAuthAccessToken {
	t := &domain.OAuthAccessToken{ID: r.ID, Token: r.Token, RefreshToken: r.RefreshToken.String, RevokedAt: r.RevokedAt.Ptr(),
		CreatedAt: r.CreatedAt.Time, Scopes: r.Scopes.String, PreviousRefreshToken: r.PreviousRefreshToken}
	if r.ResourceOwnerID.Valid {
		v := r.ResourceOwnerID.Int64
		t.ResourceOwnerID = &v
	}
	if r.ApplicationID.Valid {
		v := r.ApplicationID.Int64
		t.ApplicationID = &v
	}
	if r.ExpiresIn.Valid {
		v := int(r.ExpiresIn.Int64)
		t.ExpiresIn = &v
	}
	return t
}

// CreateOAuthAccessToken は AccessToken.create_for（token / refresh_token は保存形式）。
func CreateOAuthAccessToken(ctx context.Context, q db.Queryer, t *domain.OAuthAccessToken, at time.Time) error {
	now := db.NewTime(at)
	var expires any
	if t.ExpiresIn != nil {
		expires = *t.ExpiresIn
	}
	var owner, app any
	if t.ResourceOwnerID != nil {
		owner = *t.ResourceOwnerID
	}
	if t.ApplicationID != nil {
		app = *t.ApplicationID
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO oauth_access_tokens (resource_owner_id, application_id, token, refresh_token, expires_in,
created_at, scopes, previous_refresh_token) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		owner, app, t.Token, nullString(t.RefreshToken), expires, now, t.Scopes, t.PreviousRefreshToken)
	if err != nil {
		return err
	}
	t.ID, t.CreatedAt = id, now.Time
	return nil
}

func findOAuthToken(ctx context.Context, q db.Queryer, column, value string) (*domain.OAuthAccessToken, error) {
	var r oauthTokenRow
	if err := q.Get(ctx, &r, `SELECT `+oauthTokenColumns+` FROM oauth_access_tokens WHERE `+column+` = ?`, value); err != nil {
		return nil, notFound(err)
	}
	return r.token(), nil
}

// FindOAuthAccessTokenByToken は AccessToken.by_token（hashed は保存形式）。
func FindOAuthAccessTokenByToken(ctx context.Context, q db.Queryer, hashed string) (*domain.OAuthAccessToken, error) {
	return findOAuthToken(ctx, q, "token", hashed)
}

// FindOAuthAccessTokenByRefreshToken は AccessToken.by_refresh_token / by_previous_refresh_token（hashed は保存形式）。
func FindOAuthAccessTokenByRefreshToken(ctx context.Context, q db.Queryer, hashed string) (*domain.OAuthAccessToken, error) {
	if hashed == "" {
		return nil, ErrNotFound
	}
	return findOAuthToken(ctx, q, "refresh_token", hashed)
}

// RevokeOAuthAccessToken は AccessToken#revoke（lock して失効済みでなければ失効させる）。既に失効していれば false。
func RevokeOAuthAccessToken(ctx context.Context, q db.Queryer, id int64, now time.Time) (bool, error) {
	t := db.NewTime(now)
	res, err := q.Exec(ctx, `UPDATE oauth_access_tokens SET revoked_at = ? WHERE id = ? AND (revoked_at IS NULL OR revoked_at > ?)`, t, id, t)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ClearOAuthPreviousRefreshToken は update_attribute(:previous_refresh_token, "")。
func ClearOAuthPreviousRefreshToken(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `UPDATE oauth_access_tokens SET previous_refresh_token = '' WHERE id = ?`, id)
	return err
}

// AuthorizedOAuthTokens は AccessToken.authorized_tokens_for(application_id, resource_owner)
// （失効していないトークン。期限切れを含む）。
func AuthorizedOAuthTokens(ctx context.Context, q db.Queryer, appID, ownerID int64) ([]*domain.OAuthAccessToken, error) {
	var rows []oauthTokenRow
	if err := q.Select(ctx, &rows, `SELECT `+oauthTokenColumns+` FROM oauth_access_tokens
WHERE resource_owner_id = ? AND application_id = ? AND revoked_at IS NULL ORDER BY id`, ownerID, appID); err != nil {
		return nil, err
	}
	out := make([]*domain.OAuthAccessToken, len(rows))
	for i := range rows {
		out[i] = rows[i].token()
	}
	return out, nil
}
