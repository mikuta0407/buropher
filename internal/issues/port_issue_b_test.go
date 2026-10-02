package issues

// test/unit/issue_test.rb の移植 (240〜1352 行: 可視性スコープ・編集権限・カスタムフィールド・
// トラッカー変更・ステータス遷移・safe_attributes・ワークフローの必須/読み取り専用)。
//
// 移植したテスト:
//   test_visible_scope_for_anonymous
//   test_visible_scope_for_anonymous_without_view_issues_permissions
//   test_visible_scope_for_anonymous_without_view_issues_permissions_and_membership
//   test_anonymous_should_not_see_private_issues_with_issues_visibility_set_to_default
//   test_anonymous_should_not_see_private_issues_with_issues_visibility_set_to_own
//   test_visible_scope_for_non_member
//   test_visible_scope_for_non_member_with_own_issues_visibility
//   test_visible_scope_for_non_member_without_view_issues_permissions
//   test_visible_scope_for_non_member_without_view_issues_permissions_and_membership
//   test_visible_scope_for_member
//   test_visible_scope_for_member_without_view_issues_permission_and_non_member_role_having_the_permission
//   test_visible_scope_with_custom_non_member_role_having_restricted_permission
//   test_visible_scope_with_custom_non_member_role_having_extended_permission
//   test_visible_scope_for_member_with_groups_should_return_assigned_issues
//   test_visible_scope_for_member_with_limited_tracker_ids
//   test_visible_scope_should_consider_tracker_ids_on_each_project
//   test_visible_scope_should_not_consider_roles_without_view_issues_permission
//   test_visible_scope_for_admin
//   test_visible_scope_with_project
//   test_visible_scope_with_project_and_subprojects
//   test_visible_and_nested_set_scopes
//   test_open_scope / test_open_scope_with_arg (SQL で確認)
//   test_fixed_version_scope_with_a_version_should_return_its_fixed_issues (SQL で確認)
//   test_issue_should_be_readonly_on_closed_project
//   test_issue_should_editable_by_author
//   test_errors_full_messages_should_include_custom_fields_errors
//   test_update_issue_with_required_custom_field
//   test_should_not_update_attributes_if_custom_fields_validation_fails
//   test_should_not_recreate_custom_values_objects_on_update
//   test_setting_project_should_set_version_to_default_version
//   test_default_assigned_to_based_on_category_should_be_set_on_create
//   test_default_assigned_to_based_on_project_should_be_set_on_create
//   test_default_assigned_to_with_required_assignee_should_validate (stub の代わりにワークフロー規則)
//   test_should_not_update_custom_fields_on_changing_tracker_with_different_custom_fields
//   test_assigning_tracker_id_should_reload_custom_fields_values
//   test_assigning_tracker_and_custom_fields_should_assign_custom_fields
//   test_changing_tracker_should_clear_disabled_core_fields
//   test_attribute_cleared_on_tracker_change_should_be_journalized
//   test_reload_should_reload_custom_field_values
//   test_should_update_issue_with_disabled_tracker
//   test_should_not_set_a_disabled_tracker
//   test_category_based_assignment
//   test_new_statuses_allowed_to
//   test_new_statuses_allowed_to_should_consider_group_assignment
//   test_new_statuses_allowed_to_should_return_all_transitions_for_admin
//   test_new_statuses_allowed_to_should_only_return_transitions_of_considered_workflows
//   test_new_statuses_allowed_to_should_return_allowed_statuses_when_copying
//   test_safe_attributes_names_should_be_updated_when_changing_project
//   test_safe_attributes_names_should_not_include_disabled_field
//   test_safe_attributes_should_ignore_disabled_fields
//   test_safe_attributes_notes_should_check_add_issue_notes_permission
//   test_safe_attributes_should_accept_target_tracker_enabled_fields
//   test_safe_attributes_should_not_include_readonly_fields
//   test_safe_attributes_should_not_include_readonly_custom_fields
//   test_editable_custom_field_values_should_return_non_readonly_custom_values
//   test_editable_custom_fields_should_return_custom_field_that_is_enabled_for_the_role_only
//   test_safe_attributes_should_accept_target_tracker_writable_fields
//   test_safe_attributes_should_accept_target_status_writable_fields
//   test_required_attributes_should_be_validated
//   test_required_attribute_that_is_disabled_for_the_tracker_should_not_be_required
//   test_category_should_not_be_required_if_project_has_no_categories
//   test_fixed_version_should_not_be_required_no_assignable_versions
//   test_required_custom_field_that_is_not_visible_for_the_user_should_not_be_required
//   test_required_custom_field_that_is_visible_for_the_user_should_be_required
//   test_required_attribute_names_for_multiple_roles_should_intersect_rules
//   test_read_only_attribute_names_for_multiple_roles_should_intersect_rules
//   test_read_only_attribute_names_should_include_custom_fields_that_combine_readonly_and_not_visible_for_roles
//   test_workflow_rules_should_ignore_roles_without_issue_permissions
//   test_workflow_rules_should_work_for_member_with_duplicate_role
//
// 移植しないテスト:
//   test_visible_scope_with_unsaved_user_should_not_raise_an_error (未保存ユーザの概念が無い)
//   test_fixed_version_scope_with_empty_array_should_return_no_result (ActiveRecord スコープ固有)
//   test_assigned_to_scope_should_return_issues_assigned_to_the_user(_groups) (スコープは query パッケージの責務)
//   test_assigning_attributes_should_assign_project_and_tracker_first (mocha の呼び出し順検査。
//     AssignAttributes は assignOrder で project → tracker を先に代入する)

import (
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

func (c *tc) anonymous() *domain.User {
	c.t.Helper()
	u, err := repository.AnonymousUser(c.ctx, c.d)
	c.must(err)
	return u
}

func (c *tc) assertNoPrivateOrHidden(ids []int64) {
	c.t.Helper()
	for _, id := range ids {
		iss := c.issue(id)
		if iss.IsPrivate || !c.project(iss.ProjectID).IsPublic {
			c.t.Errorf("issue %d should not be visible", id)
		}
	}
}

func TestIssueBVisibleScopeForAnonymous(t *testing.T) {
	c := setup(t)
	u := c.anonymous()
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	c.assertNoPrivateOrHidden(ids)
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeForAnonymousWithoutViewIssues(t *testing.T) {
	c := setup(t)
	c.removePermission(c.builtinRoleID(domain.RoleBuiltinAnonymous), "view_issues")
	u := c.anonymous()
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) != 0 {
		t.Errorf("ids = %v", ids)
	}
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeForAnonymousWithoutViewIssuesAndMembership(t *testing.T) {
	c := setup(t)
	c.removePermission(c.builtinRoleID(domain.RoleBuiltinAnonymous), "view_issues")
	c.addMember(c.builtinGroupID(domain.KindGroupAnonymous), 1, 2)
	u := c.anonymous()
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	if p := c.ids(`SELECT DISTINCT project_id FROM issues WHERE id IN (` + inIDs(ids) + `)`); !slices.Equal(p, []int64{1}) {
		t.Errorf("projects = %v", p)
	}
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBAnonymousShouldNotSeePrivateIssues(t *testing.T) {
	for _, vis := range []string{"default", "own"} {
		c := setup(t)
		c.exec(`UPDATE roles SET issues_visibility = ? WHERE builtin = ?`, vis, domain.RoleBuiltinAnonymous)
		u := c.anonymous()
		e := c.env()
		iss := c.generateSaved(e, Params{"author_id": u.ID, "is_private": true})
		if ids := c.visibleIDs(u, authz.ConditionOptions{}, "issues.id = ?", iss.ID); len(ids) != 0 {
			t.Errorf("%s: scope includes private issue", vis)
		}
		if ok, _ := e.Visible(c.ctx, iss, u); ok {
			t.Errorf("%s: visible? true", vis)
		}
	}
}

func TestIssueBVisibleScopeForNonMember(t *testing.T) {
	c := setup(t)
	u := c.user(9)
	if n := c.count(`SELECT COUNT(*) FROM members WHERE principal_id = 9`); n != 0 {
		t.Fatal("user 9 has projects")
	}
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	c.assertNoPrivateOrHidden(ids)
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeForNonMemberWithOwnIssuesVisibility(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE roles SET issues_visibility = 'own' WHERE builtin = ?`, domain.RoleBuiltinNonMember)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 9, "subject": "Issue by non member"})
	c.saveOK(e, iss)
	u := c.user(9)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	for _, id := range ids {
		if c.issue(id).AuthorID != 9 {
			t.Errorf("issue %d not authored by user", id)
		}
	}
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeForNonMemberWithoutViewIssues(t *testing.T) {
	c := setup(t)
	c.removePermission(c.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
	u := c.user(9)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) != 0 {
		t.Errorf("ids = %v", ids)
	}
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeForNonMemberWithoutViewIssuesAndMembership(t *testing.T) {
	c := setup(t)
	c.removePermission(c.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
	c.addMember(c.builtinGroupID(domain.KindGroupNonMember), 1, 2)
	u := c.user(9)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	if p := c.ids(`SELECT DISTINCT project_id FROM issues WHERE id IN (` + inIDs(ids) + `)`); !slices.Equal(p, []int64{1}) {
		t.Errorf("projects = %v", p)
	}
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeForMember(t *testing.T) {
	c := setup(t)
	c.removePermission(c.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
	c.addMember(9, 3, 2)
	u := c.user(9)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	for _, id := range ids {
		iss := c.issue(id)
		if iss.ProjectID != 3 || iss.IsPrivate {
			t.Errorf("issue %d should not be visible", id)
		}
	}
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeForMemberWithoutViewIssuesNonMemberRoleHavingIt(t *testing.T) {
	c := setup(t)
	c.addPermission(c.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
	c.removePermission(1, "view_issues")
	u := c.user(2)
	if ids := c.visibleIDs(u, authz.ConditionOptions{}, "issues.project_id = 1"); len(ids) != 0 {
		t.Errorf("ids = %v", ids)
	}
	first := c.ids(`SELECT id FROM issues WHERE project_id = 1 ORDER BY id LIMIT 1`)[0]
	if ok, _ := c.env().Visible(c.ctx, c.issue(first), u); ok {
		t.Error("visible? should be false")
	}
}

func TestIssueBVisibleScopeWithCustomNonMemberRole(t *testing.T) {
	// restricted
	c := setup(t)
	role := c.generateRole("view_project")
	uid := c.generateUser(testfixtures.UserAttrs{})
	c.addMember(c.builtinGroupID(domain.KindGroupNonMember), 1, role)
	ids := c.visibleIDs(c.user(uid), authz.ConditionOptions{}, "")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	if n := len(c.ids(`SELECT id FROM issues WHERE project_id = 1 AND id IN (` + inIDs(ids) + `)`)); n != 0 {
		t.Error("project 1 issues should be hidden")
	}
	// extended
	c = setup(t)
	role = c.generateRole("view_project", "view_issues")
	c.removePermission(c.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
	uid = c.generateUser(testfixtures.UserAttrs{})
	c.addMember(c.builtinGroupID(domain.KindGroupNonMember), 1, role)
	ids = c.visibleIDs(c.user(uid), authz.ConditionOptions{}, "")
	if n := len(c.ids(`SELECT id FROM issues WHERE project_id = 1 AND id IN (` + inIDs(ids) + `)`)); n == 0 {
		t.Error("project 1 issues should be visible")
	}
}

func TestIssueBVisibleScopeForMemberWithGroupsShouldReturnAssignedIssues(t *testing.T) {
	c := setup(t)
	gids := c.ids(`SELECT group_id FROM group_users WHERE user_id = 8 ORDER BY group_id`)
	if len(gids) == 0 {
		t.Fatal("user 8 has no group")
	}
	group := gids[0]
	c.addMember(group, 1, 2)
	c.removePermission(c.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
	c.setting("issue_group_assignment", "1")
	e := c.env()
	ps, _ := e.Priorities(c.ctx)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 3, "status_id": 1, "priority_id": ps[0].ID,
		"subject": "Assignment test", "assigned_to_id": group, "is_private": true})
	c.saveOK(e, iss)
	for _, vis := range []string{"default", "own"} {
		c.exec(`UPDATE roles SET issues_visibility = ? WHERE id = 2`, vis)
		ids := c.visibleIDs(c.user(8), authz.ConditionOptions{}, "")
		if !slices.Contains(ids, iss.ID) {
			t.Errorf("%s: issue not visible", vis)
		}
	}
}

func TestIssueBVisibleScopeForMemberWithLimitedTrackerIDs(t *testing.T) {
	c := setup(t)
	c.setPermissionTrackers(1, "view_issues", []int64{2})
	u := c.user(2)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "issues.project_id = 1")
	if len(ids) == 0 {
		t.Fatal("no issues")
	}
	if tr := c.ids(`SELECT DISTINCT tracker_id FROM issues WHERE id IN (` + inIDs(ids) + `)`); !slices.Equal(tr, []int64{2}) {
		t.Errorf("trackers = %v", tr)
	}
	e := c.env()
	for _, id := range c.ids(`SELECT id FROM issues WHERE project_id = 1`) {
		iss := c.issue(id)
		ok, _ := e.Visible(c.ctx, iss, u)
		if ok != (iss.TrackerID == 2) {
			t.Errorf("issue %d visible? %v", id, ok)
		}
	}
}

func TestIssueBVisibleScopeShouldConsiderTrackerIDsOnEachProject(t *testing.T) {
	c := setup(t)
	uid := c.generateUser(testfixtures.UserAttrs{})
	p1 := c.generateProject(1, 2, 3)
	r1 := c.generateRole("view_issues")
	c.addMember(uid, p1, r1)
	p2 := c.generateProject(1, 2, 3)
	r2 := c.generateRole("view_issues")
	c.setPermissionTrackers(r2, "view_issues", []int64{2})
	c.addMember(uid, p2, r2)
	e := c.env()
	vis := []*Issue{
		c.generateSaved(e, Params{"project_id": p1, "tracker_id": 1}),
		c.generateSaved(e, Params{"project_id": p1, "tracker_id": 2}),
		c.generateSaved(e, Params{"project_id": p2, "tracker_id": 2}),
	}
	hidden := c.generateSaved(e, Params{"project_id": p2, "tracker_id": 1})
	u := c.user(uid)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "issues.project_id IN (?, ?)", p1, p2)
	eqIDs(t, []int64{vis[0].ID, vis[1].ID, vis[2].ID}, ids)
	for _, iss := range vis {
		if ok, _ := e.Visible(c.ctx, iss, u); !ok {
			t.Errorf("issue %d should be visible", iss.ID)
		}
	}
	if ok, _ := e.Visible(c.ctx, hidden, u); ok {
		t.Error("hidden issue visible")
	}
}

func TestIssueBVisibleScopeShouldNotConsiderRolesWithoutViewIssues(t *testing.T) {
	c := setup(t)
	uid := c.generateUser(testfixtures.UserAttrs{})
	r1 := c.generateRole()
	r2 := c.generateRole("view_issues")
	c.setPermissionTrackers(r2, "view_issues", []int64{2})
	c.addMember(uid, 1, r1, r2)
	u := c.user(uid)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "issues.project_id = 1")
	if tr := c.ids(`SELECT DISTINCT tracker_id FROM issues WHERE id IN (` + inIDs(ids) + `)`); !slices.Equal(tr, []int64{2}) {
		t.Errorf("trackers = %v", tr)
	}
	e := c.env()
	for _, id := range c.ids(`SELECT id FROM issues WHERE project_id = 1`) {
		iss := c.issue(id)
		ok, _ := e.Visible(c.ctx, iss, u)
		if ok != (iss.TrackerID == 2) {
			t.Errorf("issue %d visible? %v", id, ok)
		}
	}
}

func TestIssueBVisibleScopeForAdmin(t *testing.T) {
	c := setup(t)
	for _, m := range c.ids(`SELECT id FROM members WHERE principal_id = 1`) {
		c.must(repository.DestroyMember(c.ctx, c.d, m))
	}
	u := c.user(1)
	ids := c.visibleIDs(u, authz.ConditionOptions{}, "")
	if c.count(`SELECT COUNT(*) FROM issues JOIN projects ON projects.id = issues.project_id WHERE projects.is_public = ? AND issues.id IN (`+inIDs(ids)+`)`, false) == 0 {
		t.Error("admin should see issues of private projects")
	}
	if c.count(`SELECT COUNT(*) FROM issues WHERE is_private = ? AND author_id <> 1 AND id IN (`+inIDs(ids)+`)`, true) == 0 {
		t.Error("admin should see private issues of others")
	}
	c.assertVisibilityMatch(u, ids)
}

func TestIssueBVisibleScopeWithProject(t *testing.T) {
	c := setup(t)
	p := c.project(1)
	ids := c.visibleIDs(c.user(2), authz.ConditionOptions{Project: p}, "")
	if ps := c.ids(`SELECT DISTINCT project_id FROM issues WHERE id IN (` + inIDs(ids) + `)`); !slices.Equal(ps, []int64{1}) {
		t.Errorf("projects = %v", ps)
	}
	ids = c.visibleIDs(c.user(2), authz.ConditionOptions{Project: p, WithSubprojects: true}, "")
	ps := c.ids(`SELECT DISTINCT project_id FROM issues WHERE id IN (` + inIDs(ids) + `)`)
	if len(ps) <= 1 {
		t.Errorf("projects = %v", ps)
	}
	desc := c.ids(`SELECT descendant_id FROM project_closure WHERE ancestor_id = 1`)
	for _, x := range ps {
		if !slices.Contains(desc, x) {
			t.Errorf("project %d not descendant", x)
		}
	}
}

func TestIssueBVisibleAndNestedSetScopes(t *testing.T) {
	c := setup(t)
	uid := c.generateUser(testfixtures.UserAttrs{})
	c.addMember(uid, 1, 1)
	u := c.user(uid)
	e := c.env()
	parent := c.generateSaved(e, Params{"assigned_to_id": uid})
	if ok, _ := e.Visible(c.ctx, parent, u); !ok {
		t.Fatal("parent not visible")
	}
	c1 := c.generateSaved(e, Params{"parent_issue_id": parent.ID, "assigned_to_id": uid})
	c2 := c.generateSaved(e, Params{"parent_issue_id": parent.ID, "assigned_to_id": uid})
	for _, x := range []*Issue{c1, c2} {
		if ok, _ := e.Visible(c.ctx, x, u); !ok {
			t.Errorf("child %d not visible", x.ID)
		}
	}
	d, err := e.DescendantIDs(c.ctx, parent)
	c.must(err)
	if len(d) != 2 {
		t.Errorf("descendants = %v", d)
	}
	if v := c.visibleIDs(u, authz.ConditionOptions{}, "issues.id IN ("+inIDs(d)+")"); len(v) != 2 {
		t.Errorf("visible descendants = %v", v)
	}
}

func TestIssueBOpenScopeAndFixedVersionScope(t *testing.T) {
	c := setup(t)
	e := c.env()
	for _, id := range c.ids(`SELECT issues.id FROM issues JOIN issue_statuses s ON s.id = issues.status_id WHERE s.is_closed = ?`, false) {
		if cl, _ := e.Closed(c.ctx, c.issue(id)); cl {
			t.Errorf("open scope issue %d closed", id)
		}
	}
	for _, id := range c.ids(`SELECT issues.id FROM issues JOIN issue_statuses s ON s.id = issues.status_id WHERE s.is_closed = ?`, true) {
		if cl, _ := e.Closed(c.ctx, c.issue(id)); !cl {
			t.Errorf("closed scope issue %d open", id)
		}
	}
	if n := c.count(`SELECT COUNT(*) FROM issues WHERE fixed_version_id = 2`); n == 0 {
		t.Error("version 2 has no fixed issues")
	}
}

func TestIssueBIssueShouldBeReadonlyOnClosedProject(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	u := c.user(1)
	check := func(vis, ed, del bool) {
		t.Helper()
		v, _ := e.Visible(c.ctx, iss, u)
		ee, _ := e.Editable(c.ctx, iss, u)
		d, _ := e.Deletable(c.ctx, iss, u)
		if v != vis || ee != ed || d != del {
			t.Errorf("visible=%v editable=%v deletable=%v", v, ee, d)
		}
	}
	check(true, true, true)
	c.must(repository.CloseProject(c.ctx, c.d, iss.ProjectID))
	e = c.env()
	iss = c.issue(1)
	check(true, false, false)
}

func TestIssueBIssueShouldBeEditableByAuthor(t *testing.T) {
	c := setup(t)
	for _, r := range c.ids(`SELECT id FROM roles`) {
		c.removePermission(r, "edit_issues")
		c.addPermission(r, "edit_own_issues")
	}
	e := c.env()
	iss := c.issue(1)
	if iss.AuthorID != 2 {
		t.Fatal("author should be jsmith")
	}
	if ok, _ := e.AttributesEditable(c.ctx, iss, c.user(2)); !ok {
		t.Error("author should edit")
	}
	if ok, _ := e.AttributesEditable(c.ctx, iss, c.user(3)); ok {
		t.Error("non author should not edit")
	}
}

func TestIssueBErrorsFullMessagesShouldIncludeCustomFieldsErrors(t *testing.T) {
	c := setup(t)
	fid := c.cfIDByName("Database")
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "status_id": 1, "subject": "test_create",
		"description": "IssueTest#test_create_with_required_custom_field"})
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): "SQLServer"}))
	if c.valid(e, iss) {
		t.Fatal("should be invalid")
	}
	if len(iss.Errors.List) != 1 || iss.Errors.List[0].Attr != "Database" || iss.Errors.List[0].Key != "inclusion" {
		t.Errorf("errors = %v", iss.Errors.List)
	}
}

func TestIssueBUpdateIssueWithRequiredCustomField(t *testing.T) {
	c := setup(t)
	fid := c.cfIDByName("Database")
	c.exec(`UPDATE custom_fields SET is_required = ? WHERE id = ?`, true, fid)
	e := c.env()
	iss := c.issue(1)
	if c.count(`SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'issue' AND customized_id = 1 AND custom_field_id = ?`, fid) != 0 {
		t.Fatal("custom value should not exist")
	}
	if !c.save(e, iss) {
		t.Fatalf("no change save: %v", iss.Errors.List)
	}
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): ""}))
	if c.save(e, iss) {
		t.Fatal("blank should not save")
	}
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): "PostgreSQL"}))
	if !c.save(e, iss) {
		t.Fatalf("save: %v", iss.Errors.List)
	}
	c.reload(e, iss)
	if v := cfVal(c, e, iss, fid); v.String() != "PostgreSQL" {
		t.Errorf("value = %q", v.String())
	}
}

func TestIssueBShouldNotUpdateAttributesIfCustomFieldsValidationFails(t *testing.T) {
	c := setup(t)
	fid := c.cfIDByName("Database")
	e := c.env()
	iss := c.issue(1)
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): "Invalid"}))
	iss.Subject = "Should be not be saved"
	if c.save(e, iss) {
		t.Fatal("should not save")
	}
	c.reload(e, iss)
	if iss.Subject != "Cannot print recipes" {
		t.Errorf("subject = %q", iss.Subject)
	}
}

func TestIssueBShouldNotRecreateCustomValuesObjectsOnUpdate(t *testing.T) {
	c := setup(t)
	fid := c.cfIDByName("Database")
	e := c.env()
	iss := c.issue(1)
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): "PostgreSQL"}))
	c.saveOK(e, iss)
	q := `SELECT id FROM custom_values WHERE customized_kind = 'issue' AND customized_id = 1 AND custom_field_id = ?`
	before := c.ids(q, fid)
	c.reload(e, iss)
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): "MySQL"}))
	c.saveOK(e, iss)
	eqIDs(t, before, c.ids(q, fid))
}

func TestIssueBSettingProjectShouldSetVersionToDefaultVersion(t *testing.T) {
	c := setup(t)
	vid, err := c.d.InsertReturningID(c.ctx, `INSERT INTO versions (project_id, name, status, sharing, created_at, updated_at) VALUES (1, 'Version B', 'open', 'none', ?, ?)`,
		db.NewTime(frozenNow), db.NewTime(frozenNow))
	c.must(err)
	c.exec(`UPDATE projects SET default_version_id = ? WHERE id = 1`, vid)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1})
	if iss.FixedVersionID == nil || *iss.FixedVersionID != vid {
		t.Errorf("fixed_version = %v", iss.FixedVersionID)
	}
}

func TestIssueBDefaultAssignedTo(t *testing.T) {
	// カテゴリ
	c := setup(t)
	cat, err := c.d.InsertReturningID(c.ctx, `INSERT INTO issue_categories (project_id, name, assigned_to_id) VALUES (1, 'With default assignee', 3)`)
	c.must(err)
	e := c.env()
	iss := c.generateSaved(e, Params{"project_id": 1, "category_id": cat})
	if iss.AssignedToID == nil || *iss.AssignedToID != 3 {
		t.Errorf("category: assigned_to = %v", iss.AssignedToID)
	}
	// プロジェクト
	c = setup(t)
	c.exec(`UPDATE projects SET default_assigned_to_id = 3 WHERE id = 1`)
	e = c.env()
	iss = c.generateSaved(e, Params{"project_id": 1})
	if iss.AssignedToID == nil || *iss.AssignedToID != 3 {
		t.Errorf("project: assigned_to = %v", iss.AssignedToID)
	}
}

func TestIssueBDefaultAssignedToWithRequiredAssigneeShouldValidate(t *testing.T) {
	c := setup(t)
	cat, err := c.d.InsertReturningID(c.ctx, `INSERT INTO issue_categories (project_id, name, assigned_to_id) VALUES (1, 'With default assignee', 3)`)
	c.must(err)
	// required_attribute_names の stub の代わりに、全ロールに assigned_to_id 必須の規則を作る
	c.exec(`DELETE FROM workflow_field_rules`)
	for _, r := range c.ids(`SELECT id FROM roles`) {
		c.addWorkflowPermission(1, 1, r, "assigned_to_id", "required")
	}
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "subject": "Default"})
	if c.save(e, iss) || len(errorsOn(iss, "assigned_to_id")) == 0 {
		t.Errorf("errors = %v", iss.Errors.List)
	}
	iss = c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "subject": "Default", "category_id": cat})
	if !c.save(e, iss) {
		t.Errorf("save: %v", iss.Errors.List)
	}
}

func TestIssueBShouldNotUpdateCustomFieldsOnChangingTrackerWithDifferentCustomFields(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "status_id": 1, "subject": "Test",
		"custom_field_values": map[string]any{"2": "Test"}})
	c.saveOK(e, iss)
	tr, _ := e.Tracker(c.ctx, 2)
	if slices.Contains(tr.CustomFieldIDs, 2) {
		t.Fatal("tracker 2 should not have cf 2")
	}
	iss = c.issue(iss.ID)
	c.must(e.AssignAttributes(c.ctx, iss, Params{"tracker_id": 2, "custom_field_values": map[string]any{"1": ""}}))
	iss = c.issue(iss.ID)
	vals := c.ids(`SELECT id FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = 2`, iss.ID)
	if len(vals) != 1 {
		t.Fatal("custom value should exist")
	}
	var v string
	c.must(c.d.Get(c.ctx, &v, `SELECT value FROM custom_values WHERE id = ?`, vals[0]))
	if v != "Test" {
		t.Errorf("value = %q", v)
	}
}

func TestIssueBAssigningTrackerIDShouldReloadCustomFieldsValues(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1})
	vs, _ := e.CustomFieldValues(c.ctx, iss)
	if len(vs) != 0 {
		t.Fatal("should be empty")
	}
	c.must(e.SetTrackerID(c.ctx, iss, 1))
	vs, _ = e.CustomFieldValues(c.ctx, iss)
	if len(vs) == 0 {
		t.Error("should have values")
	}
}

func TestIssueBAssigningTrackerAndCustomFieldsShouldAssignCustomFields(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1})
	c.must(e.AssignAttributes(c.ctx, iss, Params{"custom_field_values": map[string]any{"1": "MySQL"}, "tracker_id": "1"}))
	if v := cfVal(c, e, iss, 1); v.String() != "MySQL" {
		t.Errorf("value = %q", v.String())
	}
}

func TestIssueBChangingTrackerShouldClearDisabledCoreFields(t *testing.T) {
	c := setup(t)
	tr, _ := c.env().Tracker(c.ctx, 2)
	c.setCoreFields(2, slices.DeleteFunc(tr.CoreFields(), func(s string) bool { return s == "due_date" })...)
	e := c.env()
	iss := c.generateSaved(e, Params{"tracker_id": 1, "start_date": today(), "due_date": today()})
	c.saveOK(e, iss)
	c.must(e.SetTrackerID(c.ctx, iss, 2))
	c.saveOK(e, iss)
	if iss.StartDate == nil || iss.DueDate != nil {
		t.Errorf("start=%v due=%v", iss.StartDate, iss.DueDate)
	}
}

func TestIssueBAttributeClearedOnTrackerChangeShouldBeJournalized(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM custom_fields`)
	tr, _ := c.env().Tracker(c.ctx, 2)
	c.setCoreFields(2, slices.DeleteFunc(tr.CoreFields(), func(s string) bool { return s == "due_date" })...)
	e := c.env()
	iss := c.generateSaved(e, Params{"tracker_id": 1, "due_date": today()})
	c.saveOK(e, iss)
	before := c.count(`SELECT COUNT(*) FROM issue_journals`)
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	c.must(e.SetTrackerID(c.ctx, iss, 2))
	c.saveOK(e, iss)
	if iss.DueDate != nil {
		t.Error("due_date should be nil")
	}
	if n := c.count(`SELECT COUNT(*) FROM issue_journals`); n != before+1 {
		t.Fatalf("journal count %d -> %d", before, n)
	}
	j := c.ids(`SELECT id FROM issue_journals ORDER BY id DESC LIMIT 1`)[0]
	if n := c.count(`SELECT COUNT(*) FROM issue_journal_details WHERE journal_id = ? AND prop_key = 'due_date'`, j); n != 1 {
		t.Errorf("due_date details = %d", n)
	}
}

func TestIssueBReloadShouldReloadCustomFieldValues(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, nil)
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{"2": "Foo"}))
	c.saveOK(e, iss)
	iss = c.issue(c.ids(`SELECT id FROM issues ORDER BY id DESC LIMIT 1`)[0])
	if v := cfVal(c, e, iss, 2); v.String() != "Foo" {
		t.Fatalf("value = %q", v.String())
	}
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{"2": "Bar"}))
	if v := cfVal(c, e, iss, 2); v.String() != "Bar" {
		t.Errorf("value = %q", v.String())
	}
	c.reload(e, iss)
	if v := cfVal(c, e, iss, 2); v.String() != "Foo" {
		t.Errorf("reloaded value = %q", v.String())
	}
}

func TestIssueBShouldUpdateIssueWithDisabledTracker(t *testing.T) {
	c := setup(t)
	iss := c.issue(1)
	c.exec(`DELETE FROM project_trackers WHERE project_id = 1 AND tracker_id = ?`, iss.TrackerID)
	e := c.env()
	iss = c.issue(1)
	iss.Subject = "New subject"
	if !c.save(e, iss) {
		t.Errorf("save: %v", iss.Errors.List)
	}
}

func TestIssueBShouldNotSetADisabledTracker(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM project_trackers WHERE project_id = 1 AND tracker_id = 2`)
	e := c.env()
	iss := c.issue(1)
	c.must(e.SetTrackerID(c.ctx, iss, 2))
	iss.Subject = "New subject"
	if c.save(e, iss) || len(errorsOn(iss, "tracker_id")) == 0 {
		t.Errorf("errors = %v", iss.Errors.List)
	}
}

func TestIssueBCategoryBasedAssignment(t *testing.T) {
	c := setup(t)
	e := c.env()
	ps, _ := e.Priorities(c.ctx)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 3, "status_id": 1, "priority_id": ps[0].ID,
		"subject": "Assignment test", "description": "Assignment test", "category_id": 1})
	c.save(e, iss)
	cat, _ := e.Category(c.ctx, ptrInt64(1))
	if !eqPtr(iss.AssignedToID, cat.AssignedToID) {
		t.Errorf("assigned_to = %v, want %v", iss.AssignedToID, cat.AssignedToID)
	}
}

func TestIssueBNewStatusesAllowedTo(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_transitions`)
	c.addTransition(1, 1, 1, 2, false, false)
	c.addTransition(1, 1, 1, 3, true, false)
	c.addTransition(1, 1, 1, 4, false, true)
	c.addTransition(1, 1, 1, 5, true, true)
	e := c.env()
	u := c.user(2)
	cases := []struct {
		author   int64
		assigned any
		want     []int64
	}{
		{1, nil, []int64{1, 2}},
		{2, nil, []int64{1, 2, 3, 5}},
		{1, int64(2), []int64{1, 2, 4, 5}},
		{2, int64(2), []int64{1, 2, 3, 4, 5}},
	}
	for _, tt := range cases {
		p := Params{"tracker_id": 1, "status_id": 1, "project_id": 1, "author_id": tt.author}
		if tt.assigned != nil {
			p["assigned_to_id"] = tt.assigned
		}
		iss := c.generateSaved(e, p)
		ss, err := e.NewStatusesAllowedTo(c.ctx, iss, u, false)
		c.must(err)
		eqIDs(t, tt.want, statusIDs(ss), tt.author, tt.assigned)
	}
}

func TestIssueBNewStatusesAllowedToShouldConsiderGroupAssignment(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_transitions`)
	c.addTransition(1, 1, 1, 4, false, true)
	g := c.generateGroup()
	c.addMember(g, 1, 1)
	c.must(repository.AddUserToGroup(c.ctx, c.d, g, 2))
	c.setting("issue_group_assignment", "1")
	e := c.env()
	iss := c.generateSaved(e, Params{"author_id": 1, "assigned_to_id": g})
	ss, err := e.NewStatusesAllowedTo(c.ctx, iss, c.user(2), false)
	c.must(err)
	if !slices.Contains(statusIDs(ss), 4) {
		t.Errorf("statuses = %v", statusIDs(ss))
	}
}

func TestIssueBNewStatusesAllowedToShouldReturnAllTransitionsForAdmin(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	if c.count(`SELECT COUNT(*) FROM members WHERE principal_id = 1 AND project_id = ?`, iss.ProjectID) != 0 {
		t.Fatal("admin is member")
	}
	news := c.ids(`SELECT DISTINCT s.id FROM workflow_transitions wt JOIN issue_statuses s ON s.id = wt.new_status_id
WHERE wt.old_status_id = ? ORDER BY s.position`, iss.StatusID)
	want := append([]int64{iss.StatusID}, news...)
	ss, err := e.NewStatusesAllowedTo(c.ctx, iss, c.user(1), false)
	c.must(err)
	eqIDs(t, want, statusIDs(ss))
}

func TestIssueBNewStatusesAllowedToShouldOnlyReturnTransitionsOfConsideredWorkflows(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_transitions`)
	c.addTransition(1, 1, 1, 2, false, false)
	c.removePermission(2, "edit_issues", "add_issues")
	c.addTransition(1, 2, 1, 3, false, false)
	e := c.env()
	iss := c.issue(9)
	for _, uid := range []int64{1, 8} {
		ss, err := e.NewStatusesAllowedTo(c.ctx, iss, c.user(uid), false)
		c.must(err)
		eqIDs(t, []int64{1, 2}, statusIDs(ss), uid)
	}
}

func TestIssueBNewStatusesAllowedToShouldReturnAllowedStatusesWhenCopying(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_transitions WHERE tracker_id = 1`)
	c.addTransition(1, 1, 0, 1, false, false)
	c.addTransition(1, 1, 0, 3, false, false)
	e := c.env()
	orig := c.generateSaved(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 4})
	iss, err := e.Copy(c.ctx, orig, nil, CopyOptions{})
	c.must(err)
	ss, err := e.NewStatusesAllowedTo(c.ctx, iss, c.user(2), false)
	c.must(err)
	eqIDs(t, []int64{1, 3}, statusIDs(ss))
	if iss.StatusID != 1 {
		t.Errorf("status = %d", iss.StatusID)
	}
}

func TestIssueBSafeAttributesNamesShouldBeUpdatedWhenChangingProject(t *testing.T) {
	c := setup(t)
	c.as(2)
	e := c.env()
	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	names, err := e.SafeAttributeNames(c.ctx, iss, nil)
	c.must(err)
	if slices.Contains(names, "watcher_user_ids") {
		t.Error("watcher_user_ids should not be included")
	}
	c.must(e.SetProjectID(c.ctx, iss, 1))
	names, err = e.SafeAttributeNames(c.ctx, iss, nil)
	c.must(err)
	if !slices.Contains(names, "watcher_user_ids") {
		t.Error("watcher_user_ids should be included")
	}
}

func TestIssueBSafeAttributesNamesShouldNotIncludeDisabledField(t *testing.T) {
	c := setup(t)
	// Tracker.new(core_fields: ...) の代わりに保存済みトラッカーを変更する
	c.setCoreFields(1, "assigned_to_id", "fixed_version_id")
	e := c.env()
	iss := c.newIssue(e, Params{"tracker_id": 1})
	names, err := e.SafeAttributeNames(c.ctx, iss, nil)
	c.must(err)
	for _, n := range []string{"tracker_id", "status_id", "subject", "custom_field_values", "custom_fields", "lock_version", "assigned_to_id", "fixed_version_id"} {
		if !slices.Contains(names, n) {
			t.Errorf("%s missing", n)
		}
	}
	tr, _ := e.Tracker(c.ctx, 1)
	for _, n := range tr.DisabledCoreFields {
		if slices.Contains(names, n) {
			t.Errorf("%s should be excluded", n)
		}
	}
}

func TestIssueBSafeAttributesShouldIgnoreDisabledFields(t *testing.T) {
	c := setup(t)
	c.setCoreFields(1, "assigned_to_id", "due_date")
	e := c.env()
	iss := c.newIssue(e, Params{"tracker_id": 1})
	c.must(e.SafeAssign(c.ctx, iss, Params{"start_date": "2012-07-14", "due_date": "2012-07-14"}, nil))
	if iss.StartDate != nil || iss.DueDate == nil || !iss.DueDate.Equal(date("2012-07-14")) {
		t.Errorf("start=%v due=%v", iss.StartDate, iss.DueDate)
	}
}

func TestIssueBSafeAttributesNotesShouldCheckAddIssueNotesPermission(t *testing.T) {
	c := setup(t)
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1})
	j, err := e.InitJournal(c.ctx, iss, u, "")
	c.must(err)
	c.must(e.SafeAssign(c.ctx, iss, Params{"notes": "note"}, u))
	if j.Notes != "note" {
		t.Errorf("notes = %q", j.Notes)
	}
	c.removePermission(1, "add_issue_notes")
	e = c.env()
	u = c.user(2)
	iss = c.newIssue(e, Params{"project_id": 1})
	j, err = e.InitJournal(c.ctx, iss, u, "")
	c.must(err)
	c.must(e.SafeAssign(c.ctx, iss, Params{"notes": "note"}, u))
	if j.Notes != "" {
		t.Errorf("notes = %q", j.Notes)
	}
}

func TestIssueBSafeAttributesShouldAcceptTargetTrackerEnabledFields(t *testing.T) {
	c := setup(t)
	c.setCoreFields(1)
	c.setCoreFields(2, "assigned_to_id", "due_date")
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1})
	c.must(e.SafeAssign(c.ctx, iss, Params{"tracker_id": 2, "due_date": "2012-07-14"}, u))
	if iss.TrackerID != 2 || iss.DueDate == nil || !iss.DueDate.Equal(date("2012-07-14")) {
		t.Errorf("tracker=%d due=%v", iss.TrackerID, iss.DueDate)
	}
}

func TestIssueBSafeAttributesShouldNotIncludeReadonlyFields(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "due_date", "readonly")
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1})
	ro, _ := e.ReadOnlyAttributeNames(c.ctx, iss, u)
	if !slices.Equal(ro, []string{"due_date"}) {
		t.Errorf("readonly = %v", ro)
	}
	names, _ := e.SafeAttributeNames(c.ctx, iss, u)
	if slices.Contains(names, "due_date") {
		t.Error("due_date should not be safe")
	}
	c.must(e.SafeAssign(c.ctx, iss, Params{"start_date": "2012-07-14", "due_date": "2012-07-14"}, u))
	if iss.StartDate == nil || !iss.StartDate.Equal(date("2012-07-14")) || iss.DueDate != nil {
		t.Errorf("start=%v due=%v", iss.StartDate, iss.DueDate)
	}
}

func TestIssueBSafeAttributesShouldNotIncludeReadonlyCustomFields(t *testing.T) {
	c := setup(t)
	cf1 := c.createCF(cfAttrs{Name: "Writable field", IsForAll: true, Trackers: []int64{1}})
	cf2 := c.createCF(cfAttrs{Name: "Readonly field", IsForAll: true, Trackers: []int64{1}})
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, itoa(cf2), "readonly")
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1})
	ro, _ := e.ReadOnlyAttributeNames(c.ctx, iss, u)
	if !slices.Equal(ro, []string{itoa(cf2)}) {
		t.Errorf("readonly = %v", ro)
	}
	names, _ := e.SafeAttributeNames(c.ctx, iss, u)
	if slices.Contains(names, itoa(cf2)) {
		t.Error("cf2 should not be safe")
	}
	c.must(e.SafeAssign(c.ctx, iss, Params{"custom_field_values": map[string]any{itoa(cf1): "value1", itoa(cf2): "value2"}}, u))
	if v := cfVal(c, e, iss, cf1); v.String() != "value1" {
		t.Errorf("cf1 = %q", v.String())
	}
	if v := cfVal(c, e, iss, cf2); !v.IsNil() {
		t.Errorf("cf2 = %q", v.String())
	}
	c.must(e.SafeAssign(c.ctx, iss, Params{"custom_fields": []map[string]any{{"id": itoa(cf1), "value": "valuea"}, {"id": itoa(cf2), "value": "valueb"}}}, u))
	if v := cfVal(c, e, iss, cf1); v.String() != "valuea" {
		t.Errorf("cf1 = %q", v.String())
	}
	if v := cfVal(c, e, iss, cf2); !v.IsNil() {
		t.Errorf("cf2 = %q", v.String())
	}
}

func hasCF(vs []*CustomFieldValue, id int64) bool {
	return slices.ContainsFunc(vs, func(v *CustomFieldValue) bool { return v.Field.ID == id })
}

func TestIssueBEditableCustomFieldValuesShouldReturnNonReadonlyCustomValues(t *testing.T) {
	c := setup(t)
	cf1 := c.createCF(cfAttrs{Name: "Writable field", IsForAll: true, Trackers: []int64{1, 2}})
	cf2 := c.createCF(cfAttrs{Name: "Readonly field", IsForAll: true, Trackers: []int64{1, 2}})
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, itoa(cf2), "readonly")
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1})
	vs, _ := e.EditableCustomFieldValues(c.ctx, iss, u)
	if !hasCF(vs, cf1) || hasCF(vs, cf2) {
		t.Error("tracker 1")
	}
	c.must(e.SetTrackerID(c.ctx, iss, 2))
	vs, _ = e.EditableCustomFieldValues(c.ctx, iss, u)
	if !hasCF(vs, cf1) || !hasCF(vs, cf2) {
		t.Error("tracker 2")
	}
}

func TestIssueBEditableCustomFieldsShouldReturnCustomFieldEnabledForTheRoleOnly(t *testing.T) {
	c := setup(t)
	en := c.createCF(cfAttrs{IsForAll: true, Trackers: []int64{1}, Visible: boolp(false), Roles: []int64{1, 2}})
	dis := c.createCF(cfAttrs{IsForAll: true, Trackers: []int64{1}, Visible: boolp(false), Roles: []int64{2}})
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1})
	fs, err := e.EditableCustomFields(c.ctx, iss, c.user(2))
	c.must(err)
	has := func(id int64) bool {
		for _, f := range fs {
			if f.ID == id {
				return true
			}
		}
		return false
	}
	if !has(en) || has(dis) {
		t.Errorf("enabled=%v disabled=%v", has(en), has(dis))
	}
}

func TestIssueBSafeAttributesShouldAcceptTargetTrackerWritableFields(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "due_date", "readonly")
	c.addWorkflowPermission(1, 2, 1, "start_date", "readonly")
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1})
	c.must(e.SafeAssign(c.ctx, iss, Params{"start_date": "2012-07-12", "due_date": "2012-07-14"}, u))
	if !eqTime(iss.StartDate, datep("2012-07-12")) || iss.DueDate != nil {
		t.Errorf("1: start=%v due=%v", iss.StartDate, iss.DueDate)
	}
	c.must(e.SafeAssign(c.ctx, iss, Params{"start_date": "2012-07-15", "due_date": "2012-07-16", "tracker_id": 2}, u))
	if !eqTime(iss.StartDate, datep("2012-07-12")) || !eqTime(iss.DueDate, datep("2012-07-16")) {
		t.Errorf("2: start=%v due=%v", iss.StartDate, iss.DueDate)
	}
}

func TestIssueBSafeAttributesShouldAcceptTargetStatusWritableFields(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "due_date", "readonly")
	c.addWorkflowPermission(2, 1, 1, "start_date", "readonly")
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1})
	c.must(e.SafeAssign(c.ctx, iss, Params{"start_date": "2012-07-12", "due_date": "2012-07-14"}, u))
	if !eqTime(iss.StartDate, datep("2012-07-12")) || iss.DueDate != nil {
		t.Errorf("1: start=%v due=%v", iss.StartDate, iss.DueDate)
	}
	c.must(e.SafeAssign(c.ctx, iss, Params{"start_date": "2012-07-15", "due_date": "2012-07-16", "status_id": 2}, u))
	if !eqTime(iss.StartDate, datep("2012-07-12")) || !eqTime(iss.DueDate, datep("2012-07-16")) {
		t.Errorf("2: start=%v due=%v", iss.StartDate, iss.DueDate)
	}
}

func errorPairs(iss *Issue) []string {
	var out []string
	for _, x := range iss.Errors.List {
		out = append(out, x.Attr+" "+x.Key)
	}
	slices.Sort(out)
	return out
}

func TestIssueBRequiredAttributesShouldBeValidated(t *testing.T) {
	c := setup(t)
	cf := c.createCF(cfAttrs{Name: "Foo", IsForAll: true, Trackers: []int64{1, 2}})
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "due_date", "required")
	c.addWorkflowPermission(1, 1, 1, "category_id", "required")
	c.addWorkflowPermission(1, 1, 1, itoa(cf), "required")
	c.addWorkflowPermission(1, 2, 1, "start_date", "required")
	c.addWorkflowPermission(1, 2, 1, itoa(cf), "required")
	e := c.env()
	u := c.user(2)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1, "subject": "Required fields", "author_id": 2})
	req, _ := e.RequiredAttributeNames(c.ctx, iss, u)
	if want := []string{itoa(cf), "category_id", "due_date"}; !slices.Equal(req, want) {
		t.Errorf("required = %v", req)
	}
	if c.save(e, iss) {
		t.Fatal("saved")
	}
	if got := errorPairs(iss); !slices.Equal(got, []string{"Foo blank", "category_id blank", "due_date blank"}) {
		t.Errorf("errors = %v", got)
	}
	c.must(e.SetTrackerID(c.ctx, iss, 2))
	req, _ = e.RequiredAttributeNames(c.ctx, iss, u)
	if want := []string{itoa(cf), "start_date"}; !slices.Equal(req, want) {
		t.Errorf("required = %v", req)
	}
	if c.save(e, iss) {
		t.Fatal("saved")
	}
	if got := errorPairs(iss); !slices.Equal(got, []string{"Foo blank", "start_date blank"}) {
		t.Errorf("errors = %v", got)
	}
	iss.SetStartDate(ptrTime(today()))
	c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(cf): "bar"}))
	if !c.save(e, iss) {
		t.Errorf("save: %v", iss.Errors.List)
	}
}

func TestIssueBRequiredAttributeDisabledForTheTrackerShouldNotBeRequired(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "start_date", "required")
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1, "subject": "Required fields", "author_id": 2})
	if c.save(e, iss) || !hasError(iss, "start_date", "blank") {
		t.Errorf("errors = %v", iss.Errors.List)
	}
	tr, _ := e.Tracker(c.ctx, 1)
	c.setCoreFields(1, slices.DeleteFunc(tr.CoreFields(), func(s string) bool { return s == "start_date" })...)
	e = c.env()
	iss = c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1, "subject": "Required fields", "author_id": 2})
	if !c.save(e, iss) {
		t.Errorf("save: %v", iss.Errors.List)
	}
}

func TestIssueBCategoryShouldNotBeRequiredIfProjectHasNoCategories(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE issues SET category_id = NULL WHERE category_id IN (SELECT id FROM issue_categories WHERE project_id = 1)`)
	c.exec(`DELETE FROM issue_categories WHERE project_id = 1`)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "category_id", "required")
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1, "subject": "Required fields", "author_id": 2})
	if !c.save(e, iss) {
		t.Errorf("save: %v", iss.Errors.List)
	}
}

func TestIssueBFixedVersionShouldNotBeRequiredNoAssignableVersions(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE projects SET default_version_id = NULL`)
	c.exec(`UPDATE issues SET fixed_version_id = NULL`)
	c.exec(`DELETE FROM versions`)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "fixed_version_id", "required")
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1, "subject": "Required fields", "author_id": 2})
	if !c.save(e, iss) {
		t.Errorf("save: %v", iss.Errors.List)
	}
}

func TestIssueBRequiredCustomFieldVisibility(t *testing.T) {
	for _, role := range []int64{2, 1} {
		c := setup(t)
		c.exec(`DELETE FROM custom_fields`)
		f := c.createCF(cfAttrs{Name: "Hidden required", IsRequired: true, Visible: boolp(false), Roles: []int64{1}, IsForAll: true})
		uid := c.generateUser(testfixtures.UserAttrs{})
		c.addMember(uid, 1, role)
		e := c.env()
		iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1, "subject": "Required fields", "author_id": uid})
		ok := c.save(e, iss)
		if role == 2 && !ok {
			t.Errorf("not visible: save failed: %v", iss.Errors.List)
		}
		if role == 1 && (ok || !hasError(iss, "Hidden required", "blank")) {
			t.Errorf("visible: errors = %v (field %d)", iss.Errors.List, f)
		}
	}
}

func TestIssueBRequiredAttributeNamesForMultipleRolesShouldIntersectRules(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "due_date", "required")
	c.addWorkflowPermission(1, 1, 1, "start_date", "required")
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1})
	req := func() []string {
		t.Helper()
		e = c.env()
		r, err := e.RequiredAttributeNames(c.ctx, iss, c.user(2))
		c.must(err)
		return r
	}
	if r := req(); !slices.Equal(r, []string{"due_date", "start_date"}) {
		t.Errorf("1: %v", r)
	}
	c.must(repository.SetMemberRoles(c.ctx, c.d, 1, []int64{1, 2}))
	if r := req(); len(r) != 0 {
		t.Errorf("2: %v", r)
	}
	c.addWorkflowPermission(1, 1, 2, "due_date", "required")
	if r := req(); !slices.Equal(r, []string{"due_date"}) {
		t.Errorf("3: %v", r)
	}
	c.must(repository.SetMemberRoles(c.ctx, c.d, 1, []int64{1, 2, 3}))
	if r := req(); len(r) != 0 {
		t.Errorf("4: %v", r)
	}
	c.addWorkflowPermission(1, 1, 3, "due_date", "readonly")
	if r := req(); !slices.Equal(r, []string{"due_date"}) {
		t.Errorf("5: %v", r)
	}
}

func TestIssueBReadOnlyAttributeNamesForMultipleRolesShouldIntersectRules(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "due_date", "readonly")
	c.addWorkflowPermission(1, 1, 1, "start_date", "readonly")
	iss := c.newIssue(c.env(), Params{"project_id": 1, "tracker_id": 1, "status_id": 1})
	ro := func() []string {
		t.Helper()
		r, err := c.env().ReadOnlyAttributeNames(c.ctx, iss, c.user(2))
		c.must(err)
		return r
	}
	if r := ro(); !slices.Equal(r, []string{"due_date", "start_date"}) {
		t.Errorf("1: %v", r)
	}
	c.must(repository.SetMemberRoles(c.ctx, c.d, 1, []int64{1, 2}))
	if r := ro(); len(r) != 0 {
		t.Errorf("2: %v", r)
	}
	c.addWorkflowPermission(1, 1, 2, "due_date", "readonly")
	if r := ro(); !slices.Equal(r, []string{"due_date"}) {
		t.Errorf("3: %v", r)
	}
}

func TestIssueBReadOnlyAttributeNamesShouldIncludeCustomFieldsReadonlyAndNotVisible(t *testing.T) {
	c := setup(t)
	f := c.createCF(cfAttrs{IsForAll: true, Visible: boolp(false), Roles: []int64{1}})
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, itoa(f), "readonly")
	uid := c.generateUser(testfixtures.UserAttrs{})
	c.addMember(uid, 1, 1, 2)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1})
	ro, err := e.ReadOnlyAttributeNames(c.ctx, iss, c.user(uid))
	c.must(err)
	if !slices.Equal(ro, []string{itoa(f)}) {
		t.Errorf("readonly = %v", ro)
	}
}

func TestIssueBWorkflowRulesShouldIgnoreRolesWithoutIssuePermissions(t *testing.T) {
	c := setup(t)
	role := c.generateRole("view_issues", "edit_issues")
	ignored := c.generateRole("view_issues")
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, role, "due_date", "required")
	c.addWorkflowPermission(1, 1, role, "start_date", "readonly")
	c.addWorkflowPermission(1, 1, role, "done_ratio", "readonly")
	uid := c.generateUser(testfixtures.UserAttrs{})
	c.addMember(uid, 1, role, ignored)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1})
	req, _ := e.RequiredAttributeNames(c.ctx, iss, c.user(uid))
	ro, _ := e.ReadOnlyAttributeNames(c.ctx, iss, c.user(uid))
	if !slices.Equal(req, []string{"due_date"}) || !slices.Equal(ro, []string{"done_ratio", "start_date"}) {
		t.Errorf("required=%v readonly=%v", req, ro)
	}
}

func TestIssueBWorkflowRulesShouldWorkForMemberWithDuplicateRole(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM workflow_field_rules`)
	c.addWorkflowPermission(1, 1, 1, "due_date", "required")
	c.addWorkflowPermission(1, 1, 1, "start_date", "readonly")
	uid := c.generateUser(testfixtures.UserAttrs{})
	// 同じロールが 2 つ付いたメンバー (直接付与 + 継承の形で重複させる)
	c.addMember(uid, 1, 1)
	mid := c.memberID(1, uid)
	var mr int64
	c.must(c.d.Get(c.ctx, &mr, `SELECT id FROM member_roles WHERE member_id = ?`, mid))
	c.exec(`INSERT INTO member_roles (member_id, role_id, inherited_from) VALUES (?, 1, ?)`, mid, mr)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1})
	req, _ := e.RequiredAttributeNames(c.ctx, iss, c.user(uid))
	ro, _ := e.ReadOnlyAttributeNames(c.ctx, iss, c.user(uid))
	if !slices.Equal(req, []string{"due_date"}) || !slices.Equal(ro, []string{"start_date"}) {
		t.Errorf("required=%v readonly=%v", req, ro)
	}
}
