package issues

// test/unit/issue_test.rb の移植 (34〜239 行: 初期化・作成・検証)。
//
// 移植したテスト:
//   test_initialize, test_create, test_create_minimal, test_create_with_all_fields_disabled,
//   test_create_with_no_priority_defined (優先度の削除の代わりに既定・有効な優先度を無くす),
//   test_default_priority_should_be_set_when_priority_field_is_disabled,
//   test_start_date_format_should_be_validated / test_due_date_format_should_be_validated (1 関数),
//   test_due_date_lesser_than_start_date_should_not_validate,
//   test_start_date_lesser_than_soonest_start_should_not_validate_on_create /
//     ..._on_update_if_changed / ..._should_validate_on_update_if_unchanged (stub は soonestStartStub),
//   test_estimated_hours_should_be_validated, test_create_with_required_custom_field,
//   test_create_with_group_assignment, test_create_with_parent_issue_id / test_create_with_sharp_parent_issue_id,
//   test_create_with_invalid_parent_issue_id / test_create_with_invalid_sharp_parent_issue_id,
//   test_create_with_emoji_character
// 移植しなかったテスト: なし (エラーは翻訳済みの全文ではなく属性とキーで確認する)。

import (
	"testing"
)

func TestIssueInitialize(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	if iss.ProjectID != 0 || iss.TrackerID != 0 || iss.StatusID != 0 || iss.AuthorID != 0 || iss.AssignedToID != nil || iss.CategoryID != nil {
		t.Error("new issue should be blank")
	}
	def, _ := e.DefaultPriority(c.ctx)
	if iss.PriorityID != def.ID {
		t.Errorf("priority = %d, want default %d", iss.PriorityID, def.ID)
	}
}

func TestIssueCreate(t *testing.T) {
	c := setup(t)
	e := c.env()
	ps, _ := e.Priorities(c.ctx)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 3, "status_id": 1, "priority_id": ps[0].ID,
		"subject": "test_create", "description": "IssueTest#test_create", "estimated_hours": "1:30"})
	if !c.save(e, iss) {
		t.Fatalf("save: %v", iss.Errors.List)
	}
	c.reload(e, iss)
	if iss.EstimatedHours == nil || *iss.EstimatedHours != 1.5 {
		t.Errorf("estimated_hours = %v", iss.EstimatedHours)
	}
}

func TestIssueCreateMinimal(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 3, "subject": "test_create"})
	if !c.save(e, iss) {
		t.Fatalf("save: %v", iss.Errors.List)
	}
	tr, _ := e.TrackerOf(c.ctx, iss)
	if iss.StatusID != tr.DefaultStatusID {
		t.Errorf("status = %d", iss.StatusID)
	}
	if iss.Description != nil || iss.EstimatedHours != nil {
		t.Error("description/estimated_hours should be nil")
	}
}

func TestIssueCreateWithAllFieldsDisabled(t *testing.T) {
	c := setup(t)
	c.setCoreFields(1)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 3, "subject": "test_create_with_all_fields_disabled"})
	if !c.save(e, iss) {
		t.Fatalf("save: %v", iss.Errors.List)
	}
}

func TestIssueCreateWithNoPriorityDefined(t *testing.T) {
	c := setup(t)
	// IssuePriority.delete_all の代わり (既存チケットが参照するため): 既定・有効な優先度を無くす
	c.exec(`UPDATE issue_priorities SET is_default = ?, active = ?`, false, false)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 3, "subject": "test_create_with_no_priority_defined"})
	if c.save(e, iss) {
		t.Fatal("should not save")
	}
	if !hasError(iss, "priority", "blank") {
		t.Errorf("errors = %v", iss.Errors.List)
	}
}

func TestIssueDefaultPriorityShouldBeSetWhenPriorityFieldIsDisabled(t *testing.T) {
	c := setup(t)
	var fields []string
	for _, f := range []string{"assigned_to_id", "category_id", "fixed_version_id", "parent_issue_id", "start_date", "due_date", "estimated_hours", "done_ratio", "description"} {
		fields = append(fields, f)
	}
	c.setCoreFields(1, fields...)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "subject": "priority_id is disabled"})
	c.saveOK(e, iss)
	def, _ := e.DefaultPriority(c.ctx)
	if iss.PriorityID != def.ID {
		t.Errorf("priority = %d", iss.PriorityID)
	}
}

func TestIssueStartAndDueDateFormatShouldBeValidated(t *testing.T) {
	c := setup(t)
	e := c.env()
	for _, attr := range []string{"start_date", "due_date"} {
		for _, d := range []string{"2012", "ABC", "2012-15-20"} {
			iss := c.newIssue(e, Params{attr: d})
			if c.valid(e, iss) || !hasError(iss, attr, "not_a_date") {
				t.Errorf("%s %q: errors = %v", attr, d, iss.Errors.List)
			}
		}
	}
}

func TestIssueDueDateLesserThanStartDateShouldNotValidate(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"start_date": "2012-10-06", "due_date": "2012-10-02"})
	if c.valid(e, iss) || !hasError(iss, "due_date", "greater_than_start_date") {
		t.Errorf("errors = %v", iss.Errors.List)
	}
}

func TestIssueStartDateLesserThanSoonestStart(t *testing.T) {
	c := setup(t)
	e := c.env()
	// on create
	iss := c.generate(e, Params{"start_date": "2013-06-04"})
	iss.soonestStartStub = datep("2013-06-10")
	if c.valid(e, iss) || !hasError(iss, "start_date", "earlier_than_minimum_start_date") {
		t.Errorf("create: errors = %v", iss.Errors.List)
	}
	// on update if changed
	iss = c.generateSaved(e, Params{"start_date": "2013-06-04"})
	iss.soonestStartStub = datep("2013-06-10")
	iss.SetStartDateString("2013-06-07")
	if c.valid(e, iss) || !hasError(iss, "start_date", "earlier_than_minimum_start_date") {
		t.Errorf("update: errors = %v", iss.Errors.List)
	}
	// on update if unchanged
	iss = c.generateSaved(e, Params{"start_date": "2013-06-04"})
	iss.soonestStartStub = datep("2013-06-10")
	if !c.valid(e, iss) {
		t.Errorf("unchanged: errors = %v", iss.Errors.List)
	}
}

func TestIssueEstimatedHoursShouldBeValidated(t *testing.T) {
	c := setup(t)
	e := c.env()
	for _, v := range []string{"-2", "123abc"} {
		iss := c.newIssue(e, Params{"estimated_hours": v})
		if c.valid(e, iss) || !hasError(iss, "estimated_hours", "invalid") {
			t.Errorf("%q: errors = %v", v, iss.Errors.List)
		}
	}
}

func TestIssueCreateWithRequiredCustomField(t *testing.T) {
	c := setup(t)
	var fid int64
	c.must(c.d.Get(c.ctx, &fid, `SELECT id FROM custom_fields WHERE name = 'Database'`))
	c.exec(`UPDATE custom_fields SET is_required = ? WHERE id = ?`, true, fid)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "status_id": 1, "subject": "test_create",
		"description": "IssueTest#test_create_with_required_custom_field"})
	av, _ := e.AvailableCustomFields(c.ctx, iss)
	found := false
	for _, f := range av {
		found = found || f.ID == fid
	}
	if !found {
		t.Fatal("field should be available")
	}
	check := func(want string) {
		t.Helper()
		if c.save(e, iss) {
			t.Fatal("should not save")
		}
		if len(iss.Errors.List) != 1 || iss.Errors.List[0].Attr != "Database" || iss.Errors.List[0].Key != want {
			t.Errorf("errors = %v, want Database %s", iss.Errors.List, want)
		}
	}
	check("blank")
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): ""}))
	check("blank")
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): "SQLServer"}))
	check("inclusion")
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): "PostgreSQL"}))
	if !c.save(e, iss) {
		t.Fatalf("save: %v", iss.Errors.List)
	}
	c.reload(e, iss)
	if v := cfVal(c, e, iss, fid); v.String() != "PostgreSQL" {
		t.Errorf("value = %q", v.String())
	}
}

func TestIssueCreateWithGroupAssignment(t *testing.T) {
	c := setup(t)
	c.setting("issue_group_assignment", "1")
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 2, "tracker_id": 1, "author_id": 1, "subject": "Group assignment", "assigned_to_id": 11})
	if !c.save(e, iss) {
		t.Fatalf("save: %v", iss.Errors.List)
	}
	last := c.ids(`SELECT id FROM issues ORDER BY id DESC LIMIT 1`)[0]
	got := c.issue(last)
	if got.AssignedToID == nil || *got.AssignedToID != 11 {
		t.Errorf("assigned_to = %v", got.AssignedToID)
	}
}

func TestIssueCreateWithParentIssueID(t *testing.T) {
	c := setup(t)
	e := c.env()
	for _, pid := range []string{"1", "#1"} {
		iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "subject": "Group assignment", "parent_issue_id": pid})
		if !c.save(e, iss) {
			t.Fatalf("%s: save: %v", pid, iss.Errors.List)
		}
		if iss.ParentIssueID() != "1" || iss.ParentID == nil || *iss.ParentID != 1 {
			t.Errorf("%s: parent = %v / %v", pid, iss.ParentIssueID(), iss.ParentID)
		}
	}
}

func TestIssueCreateWithInvalidParentIssueID(t *testing.T) {
	c := setup(t)
	e := c.env()
	for _, pid := range []string{"01ABC", "#01ABC"} {
		iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "subject": "Group assignment", "parent_issue_id": pid})
		if c.save(e, iss) {
			t.Fatal("should not save")
		}
		if iss.ParentIssueID() != pid || !hasError(iss, "parent_issue_id", "invalid") {
			t.Errorf("%s: %v %v", pid, iss.ParentIssueID(), iss.Errors.List)
		}
	}
}

func TestIssueCreateWithEmojiCharacter(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "subject": "Group assignment", "description": "Hello 😀"})
	c.saveOK(e, iss)
	c.reload(e, iss)
	if iss.Description == nil || *iss.Description != "Hello 😀" {
		t.Errorf("description = %v", iss.Description)
	}
}
