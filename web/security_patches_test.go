// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package web

import (
	"io/fs"
	"strings"
	"testing"
)

// TestSecurityPatchesApplied は tools/upstream-patches の本家セキュリティ修正が、
// アセットの再同期（tools/sync-upstream.sh）後も当たっていることを確かめる。
func TestSecurityPatchesApplied(t *testing.T) {
	// 7.0.2 #44429: 貼り付けた HTML を innerHTML（ライブ DOM でイベントハンドラが走る）ではなく DOMParser で解析する
	b, err := fs.ReadFile(Assets(), "javascript/controllers/table_paste_controller.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, ".innerHTML") || !strings.Contains(s, "new DOMParser().parseFromString(") {
		t.Errorf("table_paste_controller.js lacks the #44429 fix (DOMParser)")
	}
}
