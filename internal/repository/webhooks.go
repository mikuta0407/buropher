// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

// Webhook（app/models/webhook.rb）の保存。webhooks と webhook_projects（Redmine の projects_webhooks）。
//
// secret（HMAC 署名の共有鍵）は Redmine と同じく平文で保存する（編集画面に表示し、署名の計算に使うため。
// Redmine からのインポートも平文のまま移す）。secretbox 形式（"sb1:..."）の値があれば復号して読む。
// webhooks の読み書きはこのファイルの関数だけで行うこと。

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
)

// Webhook は webhooks の 1 行と、関連するプロジェクト（project_ids）。
type Webhook struct {
	ID        int64             `db:"id"`
	URL       string            `db:"url"`
	Secret    string            `db:"-"`
	Events    db.JSON[[]string] `db:"events"`
	UserID    int64             `db:"user_id"`
	Active    bool              `db:"active"`
	CreatedAt db.Time           `db:"created_at"`
	UpdatedAt db.Time           `db:"updated_at"`
	// ProjectIDs は webhook_projects の project_id（id 順）。
	ProjectIDs []int64 `db:"-"`

	storedSecret *string
}

type webhookRow struct {
	ID        int64             `db:"id"`
	URL       string            `db:"url"`
	Secret    *string           `db:"secret"`
	Events    db.JSON[[]string] `db:"events"`
	UserID    int64             `db:"user_id"`
	Active    bool              `db:"active"`
	CreatedAt db.Time           `db:"created_at"`
	UpdatedAt db.Time           `db:"updated_at"`
}

const webhookColumns = `w.id, w.url, w.secret, w.events, w.user_id, w.active, w.created_at, w.updated_at`

// openWebhookSecret は保存値を平文にする（復号できなければ空）。
func openWebhookSecret(box *secretbox.Box, s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	if !secretbox.IsSealed(*s) {
		return *s
	}
	if box == nil {
		return ""
	}
	p, err := box.Open(*s)
	if err != nil {
		return ""
	}
	return p
}

func loadWebhooks(ctx context.Context, q db.Queryer, box *secretbox.Box, query string, args ...any) ([]*Webhook, error) {
	var rows []webhookRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*Webhook, len(rows))
	ids := make([]int64, len(rows))
	for i, r := range rows {
		w := &Webhook{ID: r.ID, URL: r.URL, Events: r.Events, UserID: r.UserID, Active: r.Active,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
		if w.Events.V == nil {
			w.Events.V = []string{}
		}
		w.Secret = openWebhookSecret(box, r.Secret)
		out[i] = w
		ids[i] = r.ID
	}
	if len(ids) == 0 {
		return out, nil
	}
	pq, pargs, err := db.In(`SELECT webhook_id, project_id FROM webhook_projects WHERE webhook_id IN (?) ORDER BY project_id`, ids)
	if err != nil {
		return nil, err
	}
	var links []struct {
		WebhookID int64 `db:"webhook_id"`
		ProjectID int64 `db:"project_id"`
	}
	if err := q.Select(ctx, &links, pq, pargs...); err != nil {
		return nil, err
	}
	byID := map[int64]*Webhook{}
	for _, w := range out {
		byID[w.ID] = w
	}
	for _, l := range links {
		if w := byID[l.WebhookID]; w != nil {
			w.ProjectIDs = append(w.ProjectIDs, l.ProjectID)
		}
	}
	return out, nil
}

// ListUserWebhooks は User.current.webhooks.order(:url)。
func ListUserWebhooks(ctx context.Context, q db.Queryer, box *secretbox.Box, userID int64) ([]*Webhook, error) {
	return loadWebhooks(ctx, q, box, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.user_id = ? ORDER BY w.url, w.id`, userID)
}

// GetUserWebhook は User.current.webhooks.find(id)（無ければ ErrNotFound）。
func GetUserWebhook(ctx context.Context, q db.Queryer, box *secretbox.Box, userID, id int64) (*Webhook, error) {
	ws, err := loadWebhooks(ctx, q, box, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.user_id = ? AND w.id = ?`, userID, id)
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, ErrNotFound
	}
	return ws[0], nil
}

// GetWebhook は Webhook.find_by_id(id)（無ければ ErrNotFound）。
func GetWebhook(ctx context.Context, q db.Queryer, box *secretbox.Box, id int64) (*Webhook, error) {
	ws, err := loadWebhooks(ctx, q, box, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, ErrNotFound
	}
	return ws[0], nil
}

// ActiveWebhooksForProject は Webhook.hooks_for の SQL 部分: 有効なフックのうち、ユーザーが有効
// （status = 1）で、project_id のプロジェクトを対象にしているもの（id 順）。
// イベント・閲覧権限・use_webhooks 権限の判定は呼び出し側で行う。
func ActiveWebhooksForProject(ctx context.Context, q db.Queryer, box *secretbox.Box, projectID int64) ([]*Webhook, error) {
	return loadWebhooks(ctx, q, box, `SELECT `+webhookColumns+` FROM webhooks w
  INNER JOIN webhook_projects wp ON wp.webhook_id = w.id
  INNER JOIN principals u ON u.id = w.user_id
WHERE w.active = ? AND u.status = 1 AND wp.project_id = ?
ORDER BY w.id`, true, projectID)
}

// AnyActiveWebhookForProject はプロジェクトを対象にする有効なフックがあるか（発火の前の軽い判定）。
func AnyActiveWebhookForProject(ctx context.Context, q db.Queryer, projectID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM webhooks w INNER JOIN webhook_projects wp ON wp.webhook_id = w.id
WHERE w.active = ? AND wp.project_id = ?`, true, projectID)
	return n > 0, err
}

// SaveWebhook は webhook.save（新規なら INSERT。webhook_projects は w.ProjectIDs に置き換える）。
func SaveWebhook(ctx context.Context, q db.Queryer, w *Webhook, now db.Time) error {
	var secret *string
	if w.Secret != "" {
		s := w.Secret
		secret = &s
	}
	if w.Events.V == nil {
		w.Events.V = []string{}
	}
	if w.ID == 0 {
		w.CreatedAt, w.UpdatedAt = now, now
		id, err := q.InsertReturningID(ctx, `INSERT INTO webhooks (url, secret, events, user_id, active, created_at, updated_at)
  VALUES (?, ?, ?, ?, ?, ?, ?)`, w.URL, secret, w.Events, w.UserID, w.Active, w.CreatedAt, w.UpdatedAt)
		if err != nil {
			return err
		}
		w.ID = id
	} else {
		w.UpdatedAt = now
		if _, err := q.Exec(ctx, `UPDATE webhooks SET url = ?, secret = ?, events = ?, active = ?, updated_at = ? WHERE id = ?`,
			w.URL, secret, w.Events, w.Active, w.UpdatedAt, w.ID); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM webhook_projects WHERE webhook_id = ?`, w.ID); err != nil {
			return err
		}
	}
	ids := uniqIDs(w.ProjectIDs)
	slices.Sort(ids)
	for _, p := range ids {
		if _, err := q.Exec(ctx, `INSERT INTO webhook_projects (webhook_id, project_id) SELECT ?, id FROM projects WHERE id = ?`, w.ID, p); err != nil {
			return err
		}
	}
	w.ProjectIDs = ids
	return nil
}

// DeleteWebhook は webhook.destroy。
func DeleteWebhook(ctx context.Context, q db.Queryer, id int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM webhook_projects WHERE webhook_id = ?`, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
