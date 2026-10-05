// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// 各モデルの保存・削除から Webhook を発火する部分（webhooks_trigger.go の規約を参照）。

import (
	"context"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/webhook"
)

// webhookPreparedKey は削除の前に計算したペイロード（Req に保持し、コミット後に積む）。
type webhookPreparedKey struct{}

type preparedWebhooks map[string]map[int64][]pendingWebhook // type → id → pending

func (c *Req) preparedWebhooks() preparedWebhooks {
	if p, ok := c.value(webhookPreparedKey{}).(preparedWebhooks); ok {
		return p
	}
	p := preparedWebhooks{}
	c.setValue(webhookPreparedKey{}, p)
	return p
}

// prepareDeleteWebhooks は削除する前に "<type>.deleted" のペイロードを計算して Req に保持する。
func (a *App) prepareDeleteWebhooks(c *Req, typ string, ids ...int64) {
	if !a.webhooksEnabled() || len(ids) == 0 {
		return
	}
	ctx := c.Ctx()
	p := c.preparedWebhooks()
	if p[typ] == nil {
		p[typ] = map[int64][]pendingWebhook{}
	}
	for _, id := range ids {
		obj := webhookObject{Type: typ, ID: id}
		pid, err := a.webhookObjectProjectID(ctx, obj)
		if err != nil {
			continue
		}
		obj.ProjectID = pid
		p[typ][id] = a.prepareWebhooks(ctx, webhook.ActionDeleted, obj)
	}
}

// enqueuePreparedDeleteWebhooks は prepareDeleteWebhooks で計算したペイロードを積む（コミット後。ids が空なら全部）。
func (a *App) enqueuePreparedDeleteWebhooks(c *Req, typ string, ids ...int64) {
	p := c.preparedWebhooks()[typ]
	if p == nil {
		return
	}
	if len(ids) == 0 {
		for id := range p {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		a.enqueueWebhooks(c.Ctx(), p[id])
		delete(p, id)
	}
}

// triggerIssueWebhooks は issues.SaveResult.Webhooks を発火する（削除は事前に計算したペイロード）。
func (a *App) triggerIssueWebhooks(c *Req, evs []issues.WebhookEvent) {
	if !a.webhooksEnabled() {
		return
	}
	for _, ev := range evs {
		if ev.Action == issues.WebhookDeleted {
			a.enqueuePreparedDeleteWebhooks(c, webhook.TypeIssue, ev.IssueID)
			continue
		}
		a.triggerWebhook(c.Ctx(), ev.Action, webhookObject{Type: webhook.TypeIssue, ID: ev.IssueID,
			ProjectID: ev.ProjectID, JournalID: ev.JournalID})
	}
}

// TriggerIssueWebhooks は Req の外（CSV インポート・メール受信・リポジトリのコミット取り込み）から
// issues.SaveResult.Webhooks を発火する（削除は扱わない）。
func (a *App) TriggerIssueWebhooks(ctx context.Context, evs []issues.WebhookEvent) {
	if !a.webhooksEnabled() {
		return
	}
	for _, ev := range evs {
		if ev.Action == issues.WebhookDeleted {
			continue
		}
		a.triggerWebhook(ctx, ev.Action, webhookObject{Type: webhook.TypeIssue, ID: ev.IssueID,
			ProjectID: ev.ProjectID, JournalID: ev.JournalID})
	}
}

// TriggerWebhook は Req の外から Webhook.trigger("<typ>.<action>", object) を呼ぶ（created / updated）。
func (a *App) TriggerWebhook(ctx context.Context, typ, action string, id int64) {
	if !a.webhooksEnabled() {
		return
	}
	obj := webhookObject{Type: typ, ID: id}
	pid, err := a.webhookObjectProjectID(ctx, obj)
	if err != nil {
		return
	}
	obj.ProjectID = pid
	a.triggerWebhook(ctx, action, obj)
}

// triggerWebhookByID はコミット後に Webhook.trigger を呼ぶ（project_id を DB から読む）。
func (a *App) triggerWebhookByID(c *Req, typ, action string, ids ...int64) {
	for _, id := range ids {
		a.TriggerWebhook(c.Ctx(), typ, action, id)
	}
}

// issueDestroyTimeEntryIDs は削除するチケット（子孫を含む）に残る作業時間（has_many :time_entries,
// dependent: :destroy で削除されるもの）。
func (a *App) issueDestroyTimeEntryIDs(ctx context.Context, issueIDs []int64) []int64 {
	if len(issueIDs) == 0 {
		return nil
	}
	var ids []int64
	q, args, err := db.In(`SELECT id FROM time_entries WHERE issue_id IN (?) ORDER BY id`, issueIDs)
	if err != nil {
		return nil
	}
	if err := a.DB.Select(ctx, &ids, q, args...); err != nil {
		a.logger().Error("webhook: time entries of issues", "err", err)
		return nil
	}
	return ids
}

// scmWebhooks はリポジトリの取り込み（scmsync）で更新したチケット・記録した作業時間の Webhook。
func (a *App) scmWebhooks(ctx context.Context, evs []issues.WebhookEvent, timeEntryIDs []int64) {
	a.TriggerIssueWebhooks(ctx, evs)
	for _, id := range timeEntryIDs {
		a.TriggerWebhook(ctx, webhook.TypeTimeEntry, webhook.ActionCreated, id)
	}
}
