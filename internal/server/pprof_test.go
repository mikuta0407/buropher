// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server

import "testing"

func TestCheckLoopbackAddr(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:6060": true,
		"[::1]:6060":     true,
		"localhost:6060": true,
		":6060":          false,
		"0.0.0.0:6060":   false,
		"10.0.0.1:6060":  false,
		"example.com:1":  false,
		"bad":            false,
	} {
		if err := checkLoopbackAddr(addr); (err == nil) != ok {
			t.Errorf("checkLoopbackAddr(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}
