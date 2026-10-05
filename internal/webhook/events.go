// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package webhook は Redmine 7.0 の Webhook（Feature #29664）のうち、画面・DB に依存しない部分の移植。
//
//   - イベントの登録（WebhookPayload.register_model / acts_as_webhookable）: events.go
//   - 送信先 URL の検証（lib/webhook_endpoint_validator.rb。SSRF 対策）: validator.go
//   - 送信（Webhook::Executor。検証済み IP への接続・HMAC 署名・Basic 認証）: executor.go
//   - モデルの検証（Webhook の validates）: model.go
//
// フックの保存は internal/repository/webhooks.go、画面（WebhooksController）・ペイロードの描画・
// 発火（Webhook.trigger）・送信ジョブ（WebhookJob）は internal/handler/webhooks*.go。
package webhook

import (
	"slices"
	"strings"
)

// 既定のアクション（acts_as_webhookable の events = %w(created updated deleted)）。
const (
	ActionCreated = "created"
	ActionUpdated = "updated"
	ActionDeleted = "deleted"
)

// 対象の種別（model_name.singular）。
const (
	TypeIssue     = "issue"
	TypeNews      = "news"
	TypeTimeEntry = "time_entry"
	TypeVersion   = "version"
	TypeWikiPage  = "wiki_page"
)

// EventType は 1 種別の登録（WebhookPayload.events の 1 エントリ）。
type EventType struct {
	Type    string
	Actions []string
}

var defaultActions = []string{ActionCreated, ActionUpdated, ActionDeleted}

// registry は acts_as_webhookable を宣言したモデル（Issue / News / TimeEntry / Version / WikiPage）。
// 順序は Redmine の本番環境の eager load（app/models のファイル名順）と同じ。
var registry = []EventType{
	{TypeIssue, defaultActions},
	{TypeNews, defaultActions},
	{TypeTimeEntry, defaultActions},
	{TypeVersion, defaultActions},
	{TypeWikiPage, defaultActions},
}

// Events は WebhookPayload.events（種別とアクションの一覧。登録順）。
func Events() []EventType {
	out := make([]EventType, len(registry))
	for i, e := range registry {
		out[i] = EventType{Type: e.Type, Actions: slices.Clone(e.Actions)}
	}
	return out
}

// SortedEvents は setable_events.keys.sort の順（フォームの表示順）。
func SortedEvents() []EventType {
	out := Events()
	slices.SortFunc(out, func(a, b EventType) int { return strings.Compare(a.Type, b.Type) })
	return out
}

// EventNames は setable_event_names（"type.action" の一覧）。
func EventNames() []string {
	var out []string
	for _, e := range registry {
		for _, a := range e.Actions {
			out = append(out, e.Type+"."+a)
		}
	}
	return out
}

// ValidEvent は name が登録済みのイベント（"type.action"）か。
func ValidEvent(name string) bool {
	typ, action, ok := strings.Cut(name, ".")
	if !ok {
		return false
	}
	for _, e := range registry {
		if e.Type == typ {
			return slices.Contains(e.Actions, action)
		}
	}
	return false
}

// EventName は model_name.singular + "." + action。
func EventName(typ, action string) string { return typ + "." + action }
