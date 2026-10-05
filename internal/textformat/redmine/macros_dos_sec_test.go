// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"database/sql"
	"fmt"
	"strconv"
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

// fanoutStore は P0..P3 の各ページが次のページを fan 回 include する Wiki（展開数は fan^3）。
type fanoutStore struct {
	*DBStore
	fan   int
	texts atomic.Int64
}

func (s *fanoutStore) FindWikiPage(w *Wiki, title string) (*WikiPage, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(title, "P"))
	if err != nil || !strings.HasPrefix(title, "P") {
		return s.DBStore.FindWikiPage(w, title)
	}
	p, err := s.DBStore.FindWikiPage(w, "Another_page")
	if p == nil || err != nil {
		return p, err
	}
	cp := *p
	cp.ID = int64(8000 + n)
	cp.Title = title
	return &cp, nil
}

func (s *fanoutStore) WikiPageText(id int64) (string, bool, error) {
	s.texts.Add(1)
	n := int(id - 8000)
	if n >= 3 {
		return "leaf", true, nil
	}
	return strings.Repeat(fmt.Sprintf("{{include(P%d)}}\n\n", n+1), s.fan), true, nil
}

// TestIncludeFanoutIsBounded は同じページを何度も include するページを入れ子にしても、
// 1 回の描画で展開する include の数が上限で止まること（指数的な描画による DoS の防止）。
func TestIncludeFanoutIsBounded(t *testing.T) {
	e := newTestEnv(t)
	r, st := e.renderer(t, "admin", "ecookbook", "textile")
	fs := &fanoutStore{DBStore: st, fan: 30}
	r.Store = fs
	out := string(r.Textilizable("{{include(P0)}}", Options{}))
	if n := fs.texts.Load(); n > 100 {
		t.Errorf("included %d pages in one render", n)
	}
	if !strings.Contains(out, "Too many wiki pages are included") {
		t.Error("limit error is not shown")
	}
	// 次の最上位の描画では数え直す
	fs.texts.Store(0)
	fs.fan = 2
	out = string(r.Textilizable("{{include(P0)}}", Options{}))
	if strings.Contains(out, "Too many") || fs.texts.Load() != 1+2+4+8 {
		t.Errorf("small fan-out: %d pages, %q", fs.texts.Load(), out)
	}
}
