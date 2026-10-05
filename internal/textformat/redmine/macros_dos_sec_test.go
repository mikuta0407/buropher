// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"
)

// cycleStore は親子関係が循環した Wiki ページ（CycA の親が CycB、CycB の親が CycA）を返す。
type cycleStore struct {
	*DBStore
	calls atomic.Int64
}

func (s *cycleStore) cyclePage(id int64) *WikiPage {
	p, _ := s.DBStore.FindWikiPage(&Wiki{ID: 1, ProjectID: 1}, "Another_page")
	if p == nil {
		return nil
	}
	cp := *p
	cp.ID = id
	cp.Title = map[int64]string{9001: "CycA", 9002: "CycB"}[id]
	cp.ParentID = sql.NullInt64{Int64: 9001 + 9002 - id, Valid: true}
	return &cp
}

func (s *cycleStore) FindWikiPage(w *Wiki, title string) (*WikiPage, error) {
	switch title {
	case "CycA":
		return s.cyclePage(9001), nil
	case "CycB":
		return s.cyclePage(9002), nil
	}
	return s.DBStore.FindWikiPage(w, title)
}

func (s *cycleStore) WikiPageChildren(id int64) ([]*WikiPage, error) {
	if s.calls.Add(1) > 1000 {
		// 修正前は無限に再帰してプロセスごと落ちるため、テストでは途中で打ち切る
		panic("child_pages recursion did not terminate")
	}
	if id == 9001 || id == 9002 {
		return []*WikiPage{s.cyclePage(9001 + 9002 - id)}, nil
	}
	return s.DBStore.WikiPageChildren(id)
}

// TestChildPagesParentCycle は親子関係が循環していても {{child_pages}} が終わること（無限再帰でプロセスが落ちない）。
func TestChildPagesParentCycle(t *testing.T) {
	e := newTestEnv(t)
	for _, src := range []string{"{{child_pages(CycA)}}", "{{child_pages(CycA, parent=1)}}", "{{child_pages(CycB, depth=-1)}}"} {
		r, st := e.renderer(t, "admin", "ecookbook", "textile")
		cs := &cycleStore{DBStore: st}
		r.Store = cs
		var out string
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("%s: %v", src, rec)
				}
			}()
			out = string(r.Textilizable(src, Options{}))
		}()
		if !strings.Contains(out, "pages-hierarchy") {
			t.Errorf("%s: %q", src, out)
		}
	}
}
