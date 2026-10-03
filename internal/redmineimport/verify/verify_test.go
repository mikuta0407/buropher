// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package verify

import "testing"

func TestNestedConsistent(t *testing.T) {
	ok := []nested{{id: 1, lft: 1, rgt: 6}, {id: 2, parent: 1, lft: 2, rgt: 5}, {id: 3, parent: 2, lft: 3, rgt: 4}, {id: 4, lft: 7, rgt: 8}}
	if !nestedConsistent(ok, func(nested) int64 { return 0 }) {
		t.Error("consistent set reported as inconsistent")
	}
	bad := []nested{{id: 1, lft: 1, rgt: 6}, {id: 2, parent: 0, lft: 2, rgt: 5}}
	if nestedConsistent(bad, func(nested) int64 { return 0 }) {
		t.Error("inconsistent set reported as consistent")
	}
	// issues: root_id ごとに独立した集合
	issues := []nested{{id: 1, root: 1, lft: 1, rgt: 2}, {id: 2, root: 2, lft: 1, rgt: 4}, {id: 3, root: 2, parent: 2, lft: 2, rgt: 3}}
	if !nestedConsistent(issues, func(n nested) int64 { return n.root }) {
		t.Error("per-root sets reported as inconsistent")
	}
}

func TestPasswordFlags(t *testing.T) {
	p := passwordFlags{}
	if err := p.Set("admin:a:b"); err != nil || p["admin"] != "a:b" {
		t.Errorf("Set = %v %v", err, p)
	}
	if err := p.Set("nopw"); err == nil {
		t.Error("want error")
	}
}
