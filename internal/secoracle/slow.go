// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package secoracle

import (
	"runtime"
	"testing"
	"time"
)

// SlowLimit はファジング入力 1 件の処理に許す時間（入力は高々数 KB なので、超えたら超線形の疑い）。
var SlowLimit = 3 * time.Second

// Bounded は fn を実行し、時間が SlowLimit を超えたら、またはヒープの割り当てが
// 入力長 n に対して allocFactor 倍 + 16MiB を超えたら失敗にする。allocFactor が 0 なら割り当ては見ない。
func Bounded(t testing.TB, n int, allocFactor uint64, fn func()) {
	t.Helper()
	var before, after runtime.MemStats
	if allocFactor > 0 {
		runtime.ReadMemStats(&before)
	}
	start := time.Now()
	fn()
	d := time.Since(start)
	if d > SlowLimit {
		t.Fatalf("took %v for %d bytes of input (superlinear?)", d, n)
	}
	if allocFactor > 0 {
		runtime.ReadMemStats(&after)
		// TotalAlloc はプロセス全体の累計なので、並行実行中の他のテストの分も含む（ファジングでは 1 ワーカー 1 入力）
		total := after.TotalAlloc - before.TotalAlloc
		if limit := uint64(n)*allocFactor + 16<<20; total > limit {
			t.Fatalf("allocated %d bytes for %d bytes of input (limit %d)", total, n, limit)
		}
	}
}
