// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package verify

import (
	"bytes"
	"strings"
	"testing"
)

// 環境変数の DSN（パスワードを含みうる）が -h やフラグの誤りで表示される使い方に出ないこと。
func TestUsageDoesNotPrintDSN(t *testing.T) {
	t.Setenv("BUROPHER_DB_DSN", "postgres://u:db-password@localhost/x")
	for _, args := range [][]string{{"-h"}, {"--no-such-flag"}, {}} {
		var out, errOut bytes.Buffer
		if err := mainWith(args, &out, &errOut); err == nil {
			t.Fatalf("%v: expected error", args)
		}
		if all := out.String() + errOut.String(); strings.Contains(all, "db-password") {
			t.Errorf("%v: usage contains the DSN password:\n%s", args, all)
		}
	}
}
