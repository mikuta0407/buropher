// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/httpx"
)

// SessionStore は sessions テーブルを使う httpx.Store 実装。
// DB にはセッション ID そのものではなく SHA-256 を保存する（DB 漏洩時のセッション乗っ取り対策）。
// sudo 時刻は Record.SudoAt を data の "_sudo_at" に保存する（sudo_until 列は未使用）。
type SessionStore struct{ DB *db.DB }

var _ httpx.Store = SessionStore{}

const sudoKey = "_sudo_at"

func hashSessionID(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

type sessionRow struct {
	UserID     sql.NullInt64  `db:"user_id"`
	CreatedAt  db.Time        `db:"created_at"`
	LastSeenAt db.Time        `db:"last_seen_at"`
	ExpiresAt  db.NullTime    `db:"expires_at"`
	IP         sql.NullString `db:"ip"`
	UserAgent  sql.NullString `db:"user_agent"`
	Data       string         `db:"data"`
}

func (s SessionStore) Get(ctx context.Context, id string) (*httpx.Record, error) {
	var r sessionRow
	err := s.DB.Get(ctx, &r, `SELECT user_id, created_at, last_seen_at, expires_at, ip, user_agent, data FROM sessions WHERE id = ?`, hashSessionID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec := &httpx.Record{
		ID: id, UserID: r.UserID.Int64, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.LastSeenAt.Time,
		IP: r.IP.String, UserAgent: r.UserAgent.String, Data: map[string]any{},
	}
	if r.ExpiresAt.Valid {
		rec.ExpiresAt = r.ExpiresAt.Time
	}
	if err := json.Unmarshal([]byte(r.Data), &rec.Data); err != nil {
		return nil, err
	}
	if v, ok := rec.Data[sudoKey].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			rec.SudoAt = t
		}
		delete(rec.Data, sudoKey)
	}
	return rec, nil
}

// sessionColumns は Save / Update が書き込む値（id 以外）を返す。
func sessionColumns(rec *httpx.Record) ([]any, error) {
	data := make(map[string]any, len(rec.Data)+1)
	for k, v := range rec.Data {
		data[k] = v
	}
	if !rec.SudoAt.IsZero() {
		data[sudoKey] = rec.SudoAt.UTC().Format(time.RFC3339Nano)
	}
	js, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var uid any
	if rec.UserID != 0 {
		uid = rec.UserID
	}
	var exp any
	if !rec.ExpiresAt.IsZero() {
		exp = db.NewTime(rec.ExpiresAt)
	}
	created, updated := rec.CreatedAt, rec.UpdatedAt
	if created.IsZero() {
		created = time.Now()
	}
	if updated.IsZero() {
		updated = created
	}
	return []any{uid, db.NewTime(created), db.NewTime(updated), exp, nullString(rec.IP), nullString(rec.UserAgent), string(js)}, nil
}

var sessionCols = []string{"id", "user_id", "created_at", "last_seen_at", "expires_at", "ip", "user_agent", "data"}

func (s SessionStore) Save(ctx context.Context, rec *httpx.Record) error {
	vals, err := sessionColumns(rec)
	if err != nil {
		return err
	}
	q := s.DB.Dialect().Upsert("sessions", sessionCols, []string{"id"}, sessionCols[1:])
	_, err = s.DB.Exec(ctx, q, append([]any{hashSessionID(rec.ID)}, vals...)...)
	return err
}

// Update は既存の行だけを更新する（削除済みなら false。httpx.Store.Update）。
func (s SessionStore) Update(ctx context.Context, rec *httpx.Record) (bool, error) {
	vals, err := sessionColumns(rec)
	if err != nil {
		return false, err
	}
	res, err := s.DB.Exec(ctx, `UPDATE sessions SET user_id = ?, created_at = ?, last_seen_at = ?, expires_at = ?, ip = ?, user_agent = ?, data = ? WHERE id = ?`,
		append(vals, hashSessionID(rec.ID))...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s SessionStore) Destroy(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE id = ?`, hashSessionID(id))
	return err
}

func (s SessionStore) DestroyAllForUser(ctx context.Context, userID int64, exceptID string) error {
	if exceptID == "" {
		_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
		return err
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, hashSessionID(exceptID))
	return err
}

// DeleteExpiredSessions は失効したセッションを削除する（定期ジョブ用）。
func (s SessionStore) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at IS NOT NULL AND expires_at < ?`, db.NewTime(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
