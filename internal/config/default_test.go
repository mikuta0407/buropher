// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Redmine 7.0 で sudo モードは既定で有効になった(#44052)。
func TestSudoModeDefault(t *testing.T) {
	t.Setenv("BUROPHER_SUDO_MODE", "")
	os.Unsetenv("BUROPHER_SUDO_MODE")
	if !Default().Auth.SudoMode {
		t.Error("sudo_mode should be enabled by default")
	}
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte("[auth]\nsudo_mode = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth.SudoMode {
		t.Error("explicit sudo_mode = false must disable it")
	}
	t.Setenv("BUROPHER_SUDO_MODE", "0")
	if c, err := Load(""); err != nil || c.Auth.SudoMode {
		t.Errorf("BUROPHER_SUDO_MODE=0: %v", err)
	}
}
