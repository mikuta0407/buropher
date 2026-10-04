// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"bytes"
	"strings"
	"testing"
)

// 環境変数で渡した鍵・DSN のパスワードが、-h やフラグの誤りで表示される使い方に出ないこと。
func TestUsageDoesNotPrintSecrets(t *testing.T) {
	t.Setenv("REDMINE_CIPHER_KEY", "redmine-cipher-secret")
	t.Setenv("BUROPHER_SECRET_KEY", "buropher-secret-key")
	t.Setenv("BUROPHER_DB_DSN", "postgres://u:db-password@localhost/x")
	for _, args := range [][]string{{"-h"}, {"--no-such-flag"}, {}} {
		var out, errOut bytes.Buffer
		if err := mainWith(args, &out, &errOut); err == nil {
			t.Fatalf("%v: expected error", args)
		}
		all := out.String() + errOut.String()
		for _, s := range []string{"redmine-cipher-secret", "buropher-secret-key", "db-password"} {
			if strings.Contains(all, s) {
				t.Errorf("%v: usage contains %q:\n%s", args, s, all)
			}
		}
	}
}
