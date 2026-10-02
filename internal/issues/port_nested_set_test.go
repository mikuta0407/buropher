package issues

// test/unit/issue_nested_set_test.rb の移植。
// lft/rgt の比較は root_id / parent_id / hier_path (ルートからの経路) の比較に置き換える
// (hier_path 順 = lft 順)。
//
// 移植済み:
//   test_new_record_is_leaf, test_create_root_issue, test_create_child_issue,
//   test_creating_a_child_in_a_subproject_should_validate,
//   test_creating_a_child_in_an_invalid_project_should_not_validate,
//   test_move_a_root_to_child, test_move_a_child_to_root, test_move_a_child_to_another_issue,
//   test_move_a_child_with_descendants_to_another_issue,
//   test_move_a_child_with_descendants_to_another_project,
//   test_moving_an_issue_to_a_descendant_should_not_validate,
//   test_updating_a_root_issue_should_not_trigger_update_nested_set_attributes_on_parent_change,
//   test_updating_a_child_issue_should_not_trigger_update_nested_set_attributes_on_parent_change,
//   test_moving_a_root_issue_should_trigger_update_nested_set_attributes_on_parent_change,
//   test_moving_a_child_issue_to_another_parent_should_trigger_update_nested_set_attributes_on_parent_change,
//   test_moving_a_child_issue_to_root_should_trigger_update_nested_set_attributes_on_parent_change
//     (mock の代わりに saved_change_to_parent_id? で判定),
//   test_destroy_should_destroy_children, test_destroy_child_should_update_parent,
//   test_destroy_parent_issue_updated_during_children_destroy, test_destroy_child_issue_with_children,
//   test_destroy_issue_with_grand_child
// 省略:
//   test_project_copy_should_copy_issue_tree (Project#copy はこのパッケージの範囲外)
//   test_rebuild_single_tree (lft/rgt を持たないため rebuild は不要)

import (
	"testing"
)

func TestNestedSetNewRecordIsLeaf(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	if leaf, _ := e.Leaf(c.ctx, iss); !leaf {
		t.Error("new record should be leaf")
	}
}

func TestNestedSetCreateRootIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, nil)
	i2 := c.generateSaved(e, nil)
	c.assertNode(t, i1.ID, i1.ID)
	c.assertNode(t, i2.ID, i2.ID)
}

func TestNestedSetCreateChildIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	before := c.journalCount()
	child := c.generateChild(e, parent, nil)
	if d := c.journalCount() - before; d != 1 {
		t.Errorf("journal diff = %d", d)
	}
	c.assertNode(t, parent.ID, parent.ID)
	c.assertNode(t, child.ID, parent.ID, child.ID)
}

func TestNestedSetCreatingAChildInASubprojectShouldValidate(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, nil)
	before := c.journalCount()
	child := c.newIssue(e, Params{"project_id": 3, "tracker_id": 2, "author_id": 1, "subject": "child", "parent_issue_id": iss.ID})
	if !c.save(e, child) {
		t.Fatalf("save: %v", child.Errors.List)
	}
	if d := c.journalCount() - before; d != 1 {
		t.Errorf("journal diff = %d", d)
	}
	c.reload(e, child)
	if child.ParentID == nil || *child.ParentID != iss.ID {
		t.Errorf("parent = %v", child.ParentID)
	}
}

func TestNestedSetCreatingAChildInAnInvalidProjectShouldNotValidate(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, nil)
	before := c.journalCount()
	child := c.newIssue(e, Params{"project_id": 2, "tracker_id": 1, "author_id": 1, "subject": "child", "parent_issue_id": iss.ID})
	if c.save(e, child) {
		t.Fatal("should not save")
	}
	if d := c.journalCount() - before; d != 0 {
		t.Errorf("journal diff = %d", d)
	}
	if len(errorsOn(child, "parent_issue_id")) == 0 {
		t.Errorf("errors = %v", child.Errors.List)
	}
}

func TestNestedSetMoveARootToChild(t *testing.T) {
	c := setup(t)
	e := c.env()
	p1 := c.generateSaved(e, nil)
	p2 := c.generateSaved(e, nil)
	child := c.generateChild(e, p1, nil)
	before := c.journalCount()
	_, err := e.InitJournal(c.ctx, p2, c.user(2), "")
	c.must(err)
	c.must(e.SetParentIssueID(c.ctx, p2, itoa(p1.ID)))
	c.saveOK(e, p2)
	if d := c.journalCount() - before; d != 2 {
		t.Errorf("journal diff = %d", d)
	}
	c.assertNode(t, p1.ID, p1.ID)
	c.assertNode(t, p2.ID, p1.ID, p2.ID)
	c.assertNode(t, child.ID, p1.ID, child.ID)
	// lft 順: p1, p2 (lft+1), child (lft+3) — 兄弟は id 順
	eqIDs(t, []int64{p1.ID, p2.ID, child.ID}, c.subtreeOrder(p1.ID), "order")
}

func TestNestedSetMoveAChildToRoot(t *testing.T) {
	c := setup(t)
	e := c.env()
	p1 := c.generateSaved(e, nil)
	p2 := c.generateSaved(e, nil)
	child := c.generateChild(e, p1, nil)
	before := c.journalCount()
	_, err := e.InitJournal(c.ctx, child, c.user(2), "")
	c.must(err)
	c.must(e.SetParentIssueID(c.ctx, child, ""))
	c.saveOK(e, child)
	if d := c.journalCount() - before; d != 2 {
		t.Errorf("journal diff = %d", d)
	}
	c.assertNode(t, p1.ID, p1.ID)
	c.assertNode(t, p2.ID, p2.ID)
	c.assertNode(t, child.ID, child.ID)
}

func TestNestedSetMoveAChildToAnotherIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	p1 := c.generateSaved(e, nil)
	p2 := c.generateSaved(e, nil)
	child := c.generateChild(e, p1, nil)
	before := c.journalCount()
	_, err := e.InitJournal(c.ctx, child, c.user(2), "")
	c.must(err)
	c.must(e.SetParentIssueID(c.ctx, child, itoa(p2.ID)))
	c.saveOK(e, child)
	if d := c.journalCount() - before; d != 3 {
		t.Errorf("journal diff = %d", d)
	}
	c.assertNode(t, p1.ID, p1.ID)
	c.assertNode(t, p2.ID, p2.ID)
	c.assertNode(t, child.ID, p2.ID, child.ID)
}

func TestNestedSetMoveAChildWithDescendantsToAnotherIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	p1 := c.generateSaved(e, nil)
	p2 := c.generateSaved(e, nil)
	child := c.generateChild(e, p1, nil)
	gc := c.generateChild(e, child, nil)
	c.assertNode(t, p1.ID, p1.ID)
	c.assertNode(t, p2.ID, p2.ID)
	c.assertNode(t, child.ID, p1.ID, child.ID)
	c.assertNode(t, gc.ID, p1.ID, child.ID, gc.ID)
	c.reload(e, child)
	c.must(e.SetParentIssueID(c.ctx, child, itoa(p2.ID)))
	c.saveOK(e, child)
	c.assertNode(t, p1.ID, p1.ID)
	c.assertNode(t, p2.ID, p2.ID)
	c.assertNode(t, child.ID, p2.ID, child.ID)
	c.assertNode(t, gc.ID, p2.ID, child.ID, gc.ID)
	if leaf, _ := e.Leaf(c.ctx, c.issue(p1.ID)); !leaf {
		t.Error("p1 should be leaf")
	}
}

func TestNestedSetMoveAChildWithDescendantsToAnotherProject(t *testing.T) {
	c := setup(t)
	e := c.env()
	p1 := c.generateSaved(e, nil)
	child := c.generateChild(e, p1, nil)
	gc := c.generateChild(e, child, nil)
	c.reload(e, child)
	bj, bd := c.journalCount(), c.detailCount()
	_, err := e.InitJournal(c.ctx, child, c.user(2), "")
	c.must(err)
	c.must(e.SetProject(c.ctx, child, c.project(2), false))
	if !c.save(e, child) {
		t.Fatalf("save: %v", child.Errors.List)
	}
	if d := c.journalCount() - bj; d != 2 {
		t.Errorf("journal diff = %d", d)
	}
	if d := c.detailCount() - bd; d != 3 {
		t.Errorf("detail diff = %d", d)
	}
	p1 = c.issue(p1.ID)
	child = c.issue(child.ID)
	gc = c.issue(gc.ID)
	if p1.ProjectID != 1 || child.ProjectID != 2 || gc.ProjectID != 2 {
		t.Errorf("projects = %d %d %d", p1.ProjectID, child.ProjectID, gc.ProjectID)
	}
	c.assertNode(t, p1.ID, p1.ID)
	c.assertNode(t, child.ID, child.ID)
	c.assertNode(t, gc.ID, child.ID, gc.ID)
}

func TestNestedSetMovingAnIssueToADescendantShouldNotValidate(t *testing.T) {
	c := setup(t)
	e := c.env()
	p1 := c.generateSaved(e, nil)
	c.generateSaved(e, nil)
	child := c.generateChild(e, p1, nil)
	gc := c.generateChild(e, child, nil)
	c.reload(e, child)
	before := c.journalCount()
	_, err := e.InitJournal(c.ctx, child, c.user(2), "")
	c.must(err)
	c.must(e.SetParentIssueID(c.ctx, child, itoa(gc.ID)))
	if c.save(e, child) {
		t.Fatal("should not save")
	}
	if d := c.journalCount() - before; d != 0 {
		t.Errorf("journal diff = %d", d)
	}
	if len(errorsOn(child, "parent_issue_id")) == 0 {
		t.Errorf("errors = %v", child.Errors.List)
	}
}

func TestNestedSetParentChangeTriggers(t *testing.T) {
	c := setup(t)
	e := c.env()
	cases := []struct {
		name    string
		parent  any
		set     string
		trigger bool
	}{
		{"updating a root issue", nil, "", false},
		{"updating a child issue", 1, "1", false},
		{"moving a root issue", nil, "1", true},
		{"moving a child to another parent", 1, "2", true},
		{"moving a child to root", 1, "", true},
	}
	for _, tt := range cases {
		attrs := Params{}
		if tt.parent != nil {
			attrs["parent_issue_id"] = tt.parent
		}
		iss := c.issue(c.generateSaved(e, attrs).ID)
		c.must(e.SetParentIssueID(c.ctx, iss, tt.set))
		c.saveOK(e, iss)
		if got := iss.SavedChangeTo("parent_id"); got != tt.trigger {
			t.Errorf("%s: update_nested_set_attributes_on_parent_change called = %v", tt.name, got)
		}
	}
}

func TestNestedSetDestroyShouldDestroyChildren(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, nil)
	i2 := c.generateSaved(e, nil)
	i3 := c.generateChild(e, i2, nil)
	i4 := c.generateChild(e, i1, nil)
	_, err := e.InitJournal(c.ctx, i3, c.user(2), "")
	c.must(err)
	i3.Subject = "child with journal"
	c.saveOK(e, i3)
	bi, bj, bd := c.issueCount(), c.journalCount(), c.detailCount()
	_, err = e.Destroy(c.ctx, c.issue(i2.ID))
	c.must(err)
	if c.issueCount()-bi != -2 || c.journalCount()-bj != -2 || c.detailCount()-bd != -2 {
		t.Errorf("diffs = %d %d %d", c.issueCount()-bi, c.journalCount()-bj, c.detailCount()-bd)
	}
	if c.count(`SELECT COUNT(*) FROM issues WHERE id IN (?, ?)`, i2.ID, i3.ID) != 0 {
		t.Error("i2/i3 should be deleted")
	}
	c.assertNode(t, i1.ID, i1.ID)
	c.assertNode(t, i4.ID, i1.ID, i4.ID)
}

func TestNestedSetDestroyChildShouldUpdateParent(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, nil)
	c.generateChild(e, iss, nil)
	child2 := c.generateChild(e, iss, nil)
	if n := len(c.subtreeOrder(iss.ID)); n != 3 {
		t.Errorf("subtree size = %d", n)
	}
	before := c.journalCount()
	_, err := e.Destroy(c.ctx, c.reload(e, child2))
	c.must(err)
	if d := c.journalCount() - before; d != 1 {
		t.Errorf("journal diff = %d", d)
	}
	if n := len(c.subtreeOrder(iss.ID)); n != 2 {
		t.Errorf("subtree size = %d", n)
	}
}

func TestNestedSetDestroyParentIssueUpdatedDuringChildrenDestroy(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	c.generateChild(e, parent, Params{"start_date": today()})
	c.generateChild(e, parent, Params{"start_date": daysFromNow(2)})
	bi, bj := c.issueCount(), c.journalCount()
	_, err := e.Destroy(c.ctx, c.issue(parent.ID))
	c.must(err)
	if c.issueCount()-bi != -3 || c.journalCount()-bj != -2 {
		t.Errorf("diffs = %d %d", c.issueCount()-bi, c.journalCount()-bj)
	}
}

func TestNestedSetDestroyChildIssueWithChildren(t *testing.T) {
	c := setup(t)
	e := c.env()
	root := c.generateSaved(e, nil)
	child := c.generateChild(e, root, nil)
	leafIss := c.generateChild(e, child, nil)
	_, err := e.InitJournal(c.ctx, leafIss, c.user(2), "")
	c.must(err)
	leafIss.Subject = "leaf with journal"
	c.saveOK(e, leafIss)
	bi, bj, bd := c.issueCount(), c.journalCount(), c.detailCount()
	_, err = e.Destroy(c.ctx, c.issue(child.ID))
	c.must(err)
	if c.issueCount()-bi != -2 || c.journalCount()-bj != -1 || c.detailCount()-bd != -1 {
		t.Errorf("diffs = %d %d %d", c.issueCount()-bi, c.journalCount()-bj, c.detailCount()-bd)
	}
	if leaf, _ := e.Leaf(c.ctx, c.issue(root.ID)); !leaf {
		t.Error("root should be leaf")
	}
}

func TestNestedSetDestroyIssueWithGrandChild(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	iss := c.generateChild(e, parent, nil)
	child := c.generateChild(e, iss, nil)
	c.generateChild(e, child, nil)
	c.generateChild(e, child, nil)
	bi, bj := c.issueCount(), c.journalCount()
	_, err := e.Destroy(c.ctx, c.issue(iss.ID))
	c.must(err)
	if c.issueCount()-bi != -4 || c.journalCount()-bj != -2 {
		t.Errorf("diffs = %d %d", c.issueCount()-bi, c.journalCount()-bj)
	}
	c.assertNode(t, parent.ID, parent.ID)
	if leaf, _ := e.Leaf(c.ctx, c.issue(parent.ID)); !leaf {
		t.Error("parent should be leaf")
	}
}
