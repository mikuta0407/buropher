// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

// test/unit/issue_subtasking_test.rb の移植 (全テスト)。
//
// 移植済み:
//   test_leaf_planning_fields_should_be_editable,
//   test_parent_dates_should_be_read_only_with_parent_issue_dates_set_to_derived,
//   test_parent_dates_should_be_lowest_start_and_highest_due_dates_with_parent_issue_dates_set_to_derived,
//   test_reschuling_a_parent_should_reschedule_subtasks_with_parent_issue_dates_set_to_derived,
//   test_parent_priority_should_be_read_only_with_parent_issue_priority_set_to_derived,
//   test_parent_priority_should_be_the_highest_open_child_priority,
//   test_parent_priority_should_be_set_to_default_when_all_children_are_closed,
//   test_parent_priority_should_be_left_unchanged_when_all_children_are_closed_and_no_default_priority,
//   test_parent_done_ratio_should_be_read_only_with_parent_issue_done_ratio_set_to_derived,
//   test_parent_done_ratio_should_be_average_done_ratio_of_leaves,
//   test_parent_done_ratio_should_be_rounded_down_to_the_nearest_integer,
//   test_parent_done_ratio_should_be_weighted_by_estimated_times_if_any,
//   test_parent_done_ratio_should_be_weighted_by_estimated_times_if_any_with_grandchildren,
//   test_parent_done_ratio_with_child_estimate_to_0_should_reach_100,
//   test_done_ratio_of_parent_with_a_child_without_estimated_time_should_not_exceed_100,
//   test_done_ratio_of_parent_with_a_child_with_estimated_time_at_0_should_not_exceed_100,
//   test_done_ratio_of_parent_with_completed_children_should_not_be_99,
//   test_changing_parent_should_update_previous_parent_done_ratio,
//   test_done_ratio_of_parent_should_reflect_children,
//   test_parent_dates_should_be_editable_with_parent_issue_dates_set_to_independent,
//   test_parent_dates_should_not_be_updated_with_parent_issue_dates_set_to_independent,
//   test_reschuling_a_parent_should_not_reschedule_subtasks_with_parent_issue_dates_set_to_independent,
//   test_parent_priority_should_be_editable_with_parent_issue_priority_set_to_independent,
//   test_parent_priority_should_not_be_updated_with_parent_issue_priority_set_to_independent,
//   test_parent_done_ratio_should_be_editable_with_parent_issue_done_ratio_set_to_independent,
//   test_parent_done_ratio_should_not_be_updated_with_parent_issue_done_ratio_set_to_independent,
//   test_parent_total_estimated_hours_should_be_sum_of_visible_descendants,
//   test_open_issue_with_closed_parent_should_not_validate
// 省略: なし

import (
	"testing"
	"time"
)

func (c *tc) generateWithChild(e *Env, attrs Params) *Issue {
	c.t.Helper()
	iss := c.generateSaved(e, attrs)
	c.generateSaved(e, Params{"parent_issue_id": iss.ID})
	return c.reload(e, iss)
}

func (c *tc) safeAttr(e *Env, iss *Issue, attr string, uid int64) bool {
	c.t.Helper()
	ok, err := e.SafeAttribute(c.ctx, iss, attr, c.user(uid))
	c.must(err)
	return ok
}

func TestSubtaskingLeafPlanningFieldsShouldBeEditable(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, nil)
	for _, a := range []string{"priority_id", "done_ratio", "start_date", "due_date", "estimated_hours"} {
		if !c.safeAttr(e, iss, a, 1) {
			t.Errorf("%s should be safe", a)
		}
	}
}

func TestSubtaskingDerivedFieldsShouldBeReadOnly(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateWithChild(e, nil)
	for _, a := range []string{"start_date", "due_date", "priority_id", "done_ratio"} {
		if c.safeAttr(e, iss, a, 1) {
			t.Errorf("%s should not be safe", a)
		}
	}
}

func TestSubtaskingIndependentFieldsShouldBeEditable(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_dates", "independent")
	c.setting("parent_issue_priority", "independent")
	c.setting("parent_issue_done_ratio", "independent")
	e := c.env()
	iss := c.generateWithChild(e, nil)
	for _, a := range []string{"start_date", "due_date", "priority_id", "done_ratio"} {
		if !c.safeAttr(e, iss, a, 1) {
			t.Errorf("%s should be safe", a)
		}
	}
}

func TestSubtaskingParentDatesShouldBeLowestStartAndHighestDue(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_dates", "derived")
	e := c.env()
	parent := c.generateSaved(e, nil)
	c.generateChild(e, parent, Params{"start_date": "2010-01-25", "due_date": "2010-02-15"})
	c.generateChild(e, parent, Params{"due_date": "2010-02-13"})
	c.generateChild(e, parent, Params{"start_date": "2010-02-01", "due_date": "2010-02-22"})
	c.reload(e, parent)
	if !parent.StartDate.Equal(date("2010-01-25")) || !parent.DueDate.Equal(date("2010-02-22")) {
		t.Errorf("dates = %v %v", parent.StartDate, parent.DueDate)
	}
}

func assertDates(t *testing.T, iss *Issue, start, due string) {
	t.Helper()
	if iss.StartDate == nil || iss.DueDate == nil || !iss.StartDate.Equal(date(start)) || !iss.DueDate.Equal(date(due)) {
		t.Errorf("issue %d dates = %v %v, want %s %s", iss.ID, iss.StartDate, iss.DueDate, start, due)
	}
}

func TestSubtaskingReschedulingAParentShouldRescheduleSubtasks(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_dates", "derived")
	e := c.env()
	parent := c.generateSaved(e, nil)
	c1 := c.generateChild(e, parent, Params{"start_date": "2010-05-12", "due_date": "2010-05-18"})
	c2 := c.generateChild(e, parent, Params{"start_date": "2010-06-03", "due_date": "2010-06-10"})
	_, err := e.RescheduleOnAndSave(c.ctx, c.reload(e, parent), date("2010-06-02"))
	c.must(err)
	assertDates(t, c.reload(e, c1), "2010-06-02", "2010-06-08")
	assertDates(t, c.reload(e, c2), "2010-06-03", "2010-06-10")
	assertDates(t, c.reload(e, parent), "2010-06-02", "2010-06-10")
}

func TestSubtaskingParentPriorityShouldBeTheHighestOpenChildPriority(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_priority", "derived")
	e := c.env()
	pri := func(iss *Issue) string { return c.priorityName(c.reload(e, iss).PriorityID) }
	parent := c.generateSaved(e, Params{"priority_id": c.priorityID("Normal")})
	child1 := c.generateChild(e, parent, Params{"priority_id": c.priorityID("High")})
	if p := pri(parent); p != "High" {
		t.Errorf("1: %s", p)
	}
	c.generateChild(e, child1, Params{"priority_id": c.priorityID("Immediate")})
	if p := pri(child1); p != "Immediate" {
		t.Errorf("2: %s", p)
	}
	if p := pri(parent); p != "Immediate" {
		t.Errorf("3: %s", p)
	}
	child3 := c.generateChild(e, parent, Params{"priority_id": c.priorityID("Low")})
	child4 := c.generateChild(e, parent, Params{"priority_id": c.priorityID("Urgent")})
	if p := pri(parent); p != "Immediate" {
		t.Errorf("4: %s", p)
	}
	_, err := e.Destroy(c.ctx, child1)
	c.must(err)
	if p := pri(parent); p != "Urgent" {
		t.Errorf("destroy: %s", p)
	}
	child4.StatusID = 5
	c.saveOK(e, child4)
	if p := pri(parent); p != "Low" {
		t.Errorf("close: %s", p)
	}
	c.reload(e, child3)
	child3.PriorityID = c.priorityID("Normal")
	c.saveOK(e, child3)
	if p := pri(parent); p != "Normal" {
		t.Errorf("update: %s", p)
	}
	child4.StatusID = 1
	c.saveOK(e, child4)
	if p := pri(parent); p != "Urgent" {
		t.Errorf("reopen: %s", p)
	}
}

func TestSubtaskingParentPriorityWhenAllChildrenAreClosed(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	child := c.generateChild(e, parent, Params{"priority_id": c.priorityID("High")})
	if p := c.priorityName(c.reload(e, parent).PriorityID); p != "High" {
		t.Errorf("1: %s", p)
	}
	child.StatusID = 5
	c.saveOK(e, child)
	if p := c.priorityName(c.reload(e, parent).PriorityID); p != "Normal" {
		t.Errorf("2: %s", p)
	}
}

func TestSubtaskingParentPriorityUnchangedWithoutDefaultPriority(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE issue_priorities SET is_default = ?`, false)
	e := c.env()
	parent := c.generateSaved(e, Params{"priority_id": c.priorityID("Normal")})
	child := c.generateChild(e, parent, Params{"priority_id": c.priorityID("High")})
	if p := c.priorityName(c.reload(e, parent).PriorityID); p != "High" {
		t.Errorf("1: %s", p)
	}
	child.StatusID = 5
	c.saveOK(e, child)
	if p := c.priorityName(c.reload(e, parent).PriorityID); p != "High" {
		t.Errorf("2: %s", p)
	}
}

func (c *tc) doneRatio(e *Env, iss *Issue) int {
	c.t.Helper()
	c.reload(e, iss)
	dr, err := e.DoneRatio(c.ctx, iss)
	c.must(err)
	return dr
}

func TestSubtaskingParentDoneRatioShouldBeAverageDoneRatioOfLeaves(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	check := func(want int) {
		t.Helper()
		if got := c.doneRatio(e, parent); got != want {
			t.Errorf("done_ratio = %d, want %d", got, want)
		}
	}
	c.generateChild(e, parent, Params{"done_ratio": 20})
	check(20)
	c.generateChild(e, parent, Params{"done_ratio": 70})
	check(45)
	child := c.generateChild(e, parent, Params{"done_ratio": 0})
	check(30)
	c.generateChild(e, child, Params{"done_ratio": 30})
	if got := c.doneRatio(e, child); got != 30 {
		t.Errorf("child = %d", got)
	}
	check(40)
}

func TestSubtaskingParentDoneRatioShouldBeRoundedDown(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	for _, dr := range []int{20, 20, 10} {
		c.generateChild(e, parent, Params{"done_ratio": dr})
	}
	if got := c.doneRatio(e, parent); got != 16 {
		t.Errorf("done_ratio = %d", got)
	}
}

func TestSubtaskingParentDoneRatioShouldBeWeightedByEstimatedTimes(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	c.generateChild(e, parent, Params{"estimated_hours": 10, "done_ratio": 20})
	if got := c.doneRatio(e, parent); got != 20 {
		t.Errorf("1: %d", got)
	}
	c.generateChild(e, parent, Params{"estimated_hours": 20, "done_ratio": 50})
	if got := c.doneRatio(e, parent); got != (50*20+20*10)/30 {
		t.Errorf("2: %d", got)
	}
}

func TestSubtaskingParentDoneRatioWeightedWithGrandchildren(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	c.generateChild(e, parent, Params{"estimated_hours": 2, "done_ratio": 0})
	child := c.generateChild(e, parent, nil)
	c.generateChild(e, child, Params{"estimated_hours": 2, "done_ratio": 50})
	c.generateChild(e, child, Params{"estimated_hours": 2, "done_ratio": 50})
	if got := c.doneRatio(e, child); got != 50 {
		t.Errorf("child = %d", got)
	}
	if got := c.doneRatio(e, parent); got != 33 {
		t.Errorf("parent = %d", got)
	}
}

func TestSubtaskingParentDoneRatioWithChildEstimateTo0ShouldReach100(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	i1 := c.generateChild(e, parent, nil)
	i2 := c.generateChild(e, parent, Params{"estimated_hours": 0})
	if got := c.doneRatio(e, parent); got != 0 {
		t.Errorf("1: %d", got)
	}
	c.closeIssue(e, c.reload(e, i1))
	if got := c.doneRatio(e, parent); got != 50 {
		t.Errorf("2: %d", got)
	}
	c.closeIssue(e, c.reload(e, i2))
	if got := c.doneRatio(e, parent); got != 100 {
		t.Errorf("3: %d", got)
	}
}

func TestSubtaskingDoneRatioOfParentShouldNotExceed100(t *testing.T) {
	for _, last := range []any{nil, 0} {
		c := setup(t)
		e := c.env()
		parent := c.generateSaved(e, nil)
		c.generateChild(e, parent, Params{"estimated_hours": 40})
		c.generateChild(e, parent, Params{"estimated_hours": 40})
		c.generateChild(e, parent, Params{"estimated_hours": 20})
		attrs := Params{}
		if last != nil {
			attrs["estimated_hours"] = last
		}
		c.generateChild(e, parent, attrs)
		children, err := e.Children(c.ctx, c.reload(e, parent))
		c.must(err)
		for _, ch := range children {
			c.closeIssue(e, ch)
		}
		if got := c.doneRatio(e, parent); got != 100 {
			t.Errorf("last=%v: done_ratio = %d", last, got)
		}
	}
}

func TestSubtaskingDoneRatioOfParentWithCompletedChildrenShouldNotBe99(t *testing.T) {
	c := setup(t)
	e := c.env()
	p1 := c.generateSaved(e, nil)
	c.generateChild(e, p1, Params{"estimated_hours": 8.0, "done_ratio": 100})
	c.generateChild(e, p1, Params{"estimated_hours": 8.1, "done_ratio": 100})
	if got := c.doneRatio(e, p1); got != 100 {
		t.Errorf("p1 = %d", got)
	}
	p2 := c.generateSaved(e, nil)
	c.generateChild(e, p2, Params{"estimated_hours": 9.0, "done_ratio": 100})
	for i := 0; i < 10; i++ {
		c.generateChild(e, p2, Params{"estimated_hours": 10.0, "done_ratio": 100})
	}
	if got := c.doneRatio(e, p2); got != 100 {
		t.Errorf("p2 = %d", got)
	}
}

func TestSubtaskingChangingParentShouldUpdatePreviousParentDoneRatio(t *testing.T) {
	c := setup(t)
	e := c.env()
	first := c.generateSaved(e, nil)
	second := c.generateSaved(e, nil)
	c.generateChild(e, first, Params{"done_ratio": 40})
	child := c.generateChild(e, first, Params{"done_ratio": 20})
	if c.doneRatio(e, first) != 30 || c.doneRatio(e, second) != 0 {
		t.Errorf("before: %d %d", c.doneRatio(e, first), c.doneRatio(e, second))
	}
	c.must(e.SetParentIssueID(c.ctx, child, itoa(second.ID)))
	c.saveOK(e, child)
	if c.doneRatio(e, first) != 40 || c.doneRatio(e, second) != 20 {
		t.Errorf("after: %d %d", c.doneRatio(e, first), c.doneRatio(e, second))
	}
}

func TestSubtaskingDoneRatioOfParentShouldReflectChildren(t *testing.T) {
	c := setup(t)
	e := c.env()
	root := c.generateSaved(e, nil)
	child1 := c.generateChild(e, root, nil)
	child2 := c.generateChild(e, child1, nil)
	if root.DoneRatio != 0 || child1.DoneRatio != 0 || child2.DoneRatio != 0 {
		t.Error("initial done ratios")
	}
	c.setting("issue_done_ratio", "issue_status")
	c.exec(`UPDATE issue_statuses SET default_done_ratio = 50 WHERE id = 4`)
	e = c.env()
	c.reload(e, child1)
	child1.StatusID = 4
	_, err := e.SaveWithoutValidation(c.ctx, child1)
	c.must(err)
	if got := c.doneRatio(e, child1); got != 50 {
		t.Errorf("child1 = %d", got)
	}
	if got := c.doneRatio(e, root); got != 50 {
		t.Errorf("root = %d", got)
	}
}

func TestSubtaskingParentDatesShouldNotBeUpdatedWhenIndependent(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_dates", "independent")
	e := c.env()
	parent := c.generateSaved(e, Params{"start_date": "2015-07-01", "due_date": "2015-08-01"})
	c.generateChild(e, parent, Params{"start_date": "2015-06-01", "due_date": "2015-09-01"})
	assertDates(t, c.reload(e, parent), "2015-07-01", "2015-08-01")
}

func TestSubtaskingReschedulingAParentShouldNotRescheduleSubtasksWhenIndependent(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_dates", "independent")
	e := c.env()
	parent := c.generateSaved(e, Params{"start_date": "2010-05-01", "due_date": "2010-05-20"})
	c1 := c.generateChild(e, parent, Params{"start_date": "2010-05-12", "due_date": "2010-05-18"})
	_, err := e.RescheduleOnAndSave(c.ctx, c.reload(e, parent), date("2010-06-01"))
	c.must(err)
	if s := c.reload(e, parent).StartDate; s == nil || !s.Equal(date("2010-06-01")) {
		t.Errorf("parent start = %v", s)
	}
	assertDates(t, c.reload(e, c1), "2010-05-12", "2010-05-18")
}

func TestSubtaskingParentPriorityShouldNotBeUpdatedWhenIndependent(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_priority", "independent")
	e := c.env()
	parent := c.generateSaved(e, Params{"priority_id": c.priorityID("Normal")})
	c.generateChild(e, parent, Params{"priority_id": c.priorityID("High")})
	if p := c.priorityName(c.reload(e, parent).PriorityID); p != "Normal" {
		t.Errorf("priority = %s", p)
	}
}

func TestSubtaskingParentDoneRatioShouldNotBeUpdatedWhenIndependent(t *testing.T) {
	c := setup(t)
	c.setting("parent_issue_done_ratio", "independent")
	e := c.env()
	parent := c.generateSaved(e, Params{"done_ratio": 0})
	c.generateChild(e, parent, Params{"done_ratio": 10})
	if got := c.doneRatio(e, parent); got != 0 {
		t.Errorf("done_ratio = %d", got)
	}
}

func TestSubtaskingParentTotalEstimatedHoursShouldBeSumOfVisibleDescendants(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	check := func(want float64) {
		t.Helper()
		h, err := e.TotalEstimatedHours(c.ctx, c.reload(e, parent))
		c.must(err)
		if h == nil || *h != want {
			t.Errorf("total = %v, want %v", h, want)
		}
	}
	c.generateChild(e, parent, Params{"estimated_hours": nil})
	check(0)
	c.generateChild(e, parent, Params{"estimated_hours": 5})
	check(5)
	c.generateChild(e, parent, Params{"estimated_hours": 7})
	check(12)
	c.generateChild(e, parent, Params{"estimated_hours": 9, "is_private": true})
	check(12)
}

func TestSubtaskingOpenIssueWithClosedParentShouldNotValidate(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, Params{"status_id": 5})
	child := c.generateSaved(e, nil)
	c.must(e.SetParentIssueID(c.ctx, child, itoa(parent.ID)))
	if c.save(e, child) {
		t.Fatal("should not save")
	}
	if !hasError(child, "base", "open_issue_with_closed_parent") {
		t.Errorf("errors = %v", child.Errors.List)
	}
}

var _ = time.Now
