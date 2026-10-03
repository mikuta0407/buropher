// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestSessionNotResurrectedAfterConcurrentRevocation は、読み込み済みのセッションがリクエスト処理中に
// DestroyAllForUser（パスワード変更・ロック等）で削除された場合、そのリクエストの保存処理で
// セッションが upsert により復活しないことを確認する（盗まれたセッションでリクエストを連打して
// パスワード変更による失効をすり抜ける攻撃の対策）。
func TestSessionNotResurrectedAfterConcurrentRevocation(t *testing.T) {
	store := NewMemoryStore()
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := newTestManager(store, clk)
	h := stack(m)
	attacker := newJar()
	do(t, attacker, h, "POST", "/login", "", func(w http.ResponseWriter, r *http.Request) { SessionOf(r).SetUserID(42) })
	if store.Len() != 1 {
		t.Fatalf("store len %d", store.Len())
	}
	clk.t = clk.t.Add(2 * time.Minute)
	// 攻撃者のリクエストの処理中に、被害者がパスワードを変更して全セッションを失効させる
	do(t, attacker, h, "GET", "/projects", "", func(w http.ResponseWriter, r *http.Request) {
		if SessionOf(r).UserID() != 42 {
			t.Fatal("not logged in")
		}
		_ = store.DestroyAllForUser(context.Background(), 42, "")
		SessionOf(r).Set("dirty", "1")
	})
	if store.Len() != 0 {
		t.Fatalf("revoked session resurrected by in-flight request (store len %d)", store.Len())
	}
	do(t, attacker, h, "GET", "/my/page", "", func(w http.ResponseWriter, r *http.Request) {
		if s := SessionOf(r); s.UserID() != 0 || !s.Revoked() {
			t.Errorf("still logged in: uid=%d", s.UserID())
		}
	})
}
