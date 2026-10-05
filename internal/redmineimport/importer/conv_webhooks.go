// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------- webhooks (Redmine 7.0)

// importWebhooks は webhooks / projects_webhooks を webhooks / webhook_projects へ取り込む。
//
//   - events は Redmine では YAML シリアライズの Array(serialize :events, coder: YAML, type: Array)。
//     buropher では JSON 配列で保存する(空要素は Webhook#check_events_array と同じく除く)。
//   - secret は平文のまま保存する。Redmine も平文で保存しており(Redmine::Ciphering の対象外)、
//     Webhook 編集フォームは secret をそのまま表示する。HMAC 署名(X-Redmine-Signature-256)の
//     共有鍵として毎回平文が必要で、buropher 自身へのログイン資格情報でもないため、
//     auth_sources.secret / repositories.password のような secretbox での再暗号化は行わない。
//   - user_id のユーザーがいない Webhook は破棄する(Redmine の belongs_to :user 必須)。
func (im *imp) importWebhooks() error {
	if !im.src.has("webhooks") && !im.src.has("projects_webhooks") && im.schema() == "6.1" {
		return nil // 6.1 のアーカイブには存在しない
	}
	t := im.table("webhooks")
	ins := im.ins(t, "webhooks", "id", "url", "secret", "events", "user_id", "active", "created_at", "updated_at")
	ids := map[int64]bool{}
	err := im.src.each("webhooks", func(r rec) error {
		id := r.id()
		url := strings.TrimSpace(r.str("url"))
		if url == "" {
			t.drop(id, "empty url")
			return nil
		}
		uid := r.ref("user_id")
		if !im.st.users.has(uid) {
			t.drop(id, "user_id refers to a missing user")
			return nil
		}
		events := []string{}
		raw, derr := decodeYAML(r.Row["events"])
		if derr != nil {
			t.repair(id, "unparseable events YAML; set to empty")
		} else if raw != nil {
			if _, ok := raw.([]any); !ok {
				t.repair(id, "events is not an array; set to empty")
			} else {
				for _, s := range stringList(raw) {
					if s = strings.TrimSpace(s); s != "" {
						events = append(events, s)
					}
				}
			}
		}
		ids[id] = true
		return ins.add(id, url, r.strNull("secret", true), toJSON(events), uid, r.bool("active", false),
			im.tsOr(t, id, r, "created_at", "updated_at"), im.tsOr(t, id, r, "updated_at", "created_at"))
	})
	if err != nil {
		return err
	}
	if _, err := ins.close(); err != nil {
		return err
	}

	pt := im.table("projects_webhooks")
	pins := im.ins(pt, "webhook_projects", "webhook_id", "project_id")
	seen := map[string]bool{}
	err = im.src.each("projects_webhooks", func(r rec) error {
		id := r.id()
		wid, pid := r.ref("webhook_id"), r.ref("project_id")
		if !ids[wid] {
			pt.drop(id, "webhook_id refers to a missing webhook")
			return nil
		}
		if !im.projectExists(pid) {
			pt.drop(id, "project_id refers to a missing project")
			return nil
		}
		k := fmt.Sprint(wid, "-", pid)
		if seen[k] {
			pt.drop(id, "duplicate")
			return nil
		}
		seen[k] = true
		return pins.add(wid, pid)
	})
	if err != nil {
		return err
	}
	_, err = pins.close()
	return err
}

// schema はアーカイブのスキーマ版("6.1" / "7.0")。項目のない古いアーカイブは "6.1"。
func (im *imp) schema() string {
	if s := im.src.manifest.RedmineSchema; s != "" {
		return s
	}
	return "6.1"
}
