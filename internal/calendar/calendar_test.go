// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package calendar

import (
	"testing"
	"time"
)

func d(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC) }

// TestNewMonth は月表示の開始日・終了日（Redmine の test/unit/lib/redmine/helpers/calendar_test.rb 相当）。
func TestNewMonth(t *testing.T) {
	for _, tc := range []struct {
		first      int
		start, end time.Time
	}{
		{7, d(2025, 12, 28), d(2026, 1, 31)}, // 日曜始まり
		{1, d(2025, 12, 29), d(2026, 2, 1)},  // 月曜始まり
		{6, d(2025, 12, 27), d(2026, 2, 6)},  // 土曜始まり
	} {
		c := New(d(2026, 1, 15), tc.first, Month)
		if !c.Startdt.Equal(tc.start) || !c.Enddt.Equal(tc.end) {
			t.Errorf("first=%d: %s..%s, want %s..%s", tc.first, c.Startdt, c.Enddt, tc.start, tc.end)
		}
		if len(c.FormatMonth())%7 != 0 {
			t.Errorf("first=%d: %d days", tc.first, len(c.FormatMonth()))
		}
	}
}

func TestNewWeek(t *testing.T) {
	c := New(d(2026, 1, 15), 1, Week)
	if !c.Startdt.Equal(d(2026, 1, 12)) || !c.Enddt.Equal(d(2026, 1, 18)) {
		t.Errorf("week: %s..%s", c.Startdt, c.Enddt)
	}
	c = New(d(2026, 1, 15), 7, Week)
	if !c.Startdt.Equal(d(2026, 1, 11)) || !c.Enddt.Equal(d(2026, 1, 17)) {
		t.Errorf("week (sunday): %s..%s", c.Startdt, c.Enddt)
	}
}

func TestWeekNumberAndEvents(t *testing.T) {
	c := New(d(2026, 1, 15), 7, Month)
	if n := c.WeekNumber(d(2025, 12, 28)); n != 1 {
		t.Errorf("week number of 2025-12-28: %d", n)
	}
	s, e := d(2026, 1, 15), d(2026, 1, 16)
	ev := &Event{IsIssue: true, ID: 1, StartDate: &s, DueDate: &e}
	same := &Event{IsIssue: true, ID: 2, StartDate: &s, DueDate: &s}
	c.SetEvents([]*Event{ev, same})
	if got := c.EventsOn(s); len(got) != 2 || got[0] != same || got[1] != ev {
		t.Errorf("events on start: %v", got)
	}
	if !c.Starting(same, s) || !c.Ending(same, s) || c.IssueDivClass(same, s) != " tooltip hascontextmenu starting ending" {
		t.Errorf("starting/ending: %q", c.IssueDivClass(same, s))
	}
	c.Today = s
	c.NonWorkingDays = []int{6, 7}
	if got := c.DayCSSClasses(s); got != "this-month today" {
		t.Errorf("css: %q", got)
	}
	if got := c.DayCSSClasses(d(2025, 12, 28)); got != "other-month nwday" {
		t.Errorf("css: %q", got)
	}
}
