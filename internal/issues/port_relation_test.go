package issues

// test/unit/issue_relation_test.rb の移植。
//
// 移植済み:
//   test_create, test_create_minimum, test_follows_relation_should_be_reversed,
//   test_cannot_create_inverse_relates_relations,
//   test_follows_relation_should_not_be_reversed_if_validation_fails, test_relation_type_for,
//   test_set_issue_to_dates_without_issue_to, test_set_issue_to_dates_without_issues,
//   test_validates_circular_dependency, test_validates_circular_dependency_of_subtask,
//   test_subtasks_should_allow_precedes_relation, test_validates_circular_dependency_on_reverse_relations,
//   test_create_with_initialized_journals_should_create_journals,
//   test_destroy_with_initialized_journals_should_create_journals,
//   test_to_s_should_return_the_relation_string / test_to_s_without_argument_should_return_the_relation_string_for_issue_from /
//   test_to_s_should_accept_a_block_as_custom_issue_formatting
//     (文字列化は i18n を要するビュー層の責務のため、ラベルの i18n キー (label_for) と相手の id で確認)
// 省略: なし

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
)

func (c *tc) newRel(from, to *Issue, typ string) *Relation {
	r := &Relation{From: from, To: to}
	r.RelationType = typ
	if typ == "" {
		r.RelationType = domain.RelationRelates
	}
	return r
}

func (c *tc) createRel(e *Env, r *Relation) bool {
	c.t.Helper()
	ok, _, err := e.CreateRelation(c.ctx, r)
	c.must(err)
	return ok
}

func (c *tc) relation(id int64) *domain.IssueRelation {
	c.t.Helper()
	rs, err := c.env().loadRelations(c.ctx, `id = ?`, id)
	c.must(err)
	if len(rs) == 0 {
		c.t.Fatalf("relation %d not found", id)
	}
	return rs[0]
}

func TestRelationCreate(t *testing.T) {
	c := setup(t)
	e := c.env()
	r := c.newRel(c.issue(1), c.issue(2), domain.RelationPrecedes)
	if !c.createRel(e, r) {
		t.Fatalf("save: %v", r.Errors.List)
	}
	got := c.relation(r.ID)
	if got.RelationType != domain.RelationPrecedes || got.IssueFromID != 1 || got.IssueToID != 2 {
		t.Errorf("relation = %+v", got)
	}
}

func TestRelationCreateMinimum(t *testing.T) {
	c := setup(t)
	e := c.env()
	r := c.newRel(c.issue(1), c.issue(2), "")
	if !c.createRel(e, r) {
		t.Fatalf("save: %v", r.Errors.List)
	}
	if r.RelationType != domain.RelationRelates {
		t.Errorf("type = %s", r.RelationType)
	}
}

func TestRelationFollowsRelationShouldBeReversed(t *testing.T) {
	c := setup(t)
	e := c.env()
	r := c.newRel(c.issue(1), c.issue(2), domain.RelationFollows)
	if !c.createRel(e, r) {
		t.Fatalf("save: %v", r.Errors.List)
	}
	got := c.relation(r.ID)
	if got.RelationType != domain.RelationPrecedes || got.IssueFromID != 2 || got.IssueToID != 1 {
		t.Errorf("relation = %+v", got)
	}
}

func TestRelationCannotCreateInverseRelatesRelations(t *testing.T) {
	c := setup(t)
	e := c.env()
	from, to := c.issue(1), c.issue(2)
	if !c.createRel(e, c.newRel(from, to, domain.RelationRelates)) {
		t.Fatal("relation1")
	}
	r2 := c.newRel(to, from, domain.RelationRelates)
	if c.createRel(e, r2) {
		t.Fatal("relation2 should not save")
	}
	if len(r2.Errors.On("base")) == 0 {
		t.Errorf("errors = %v", r2.Errors.List)
	}
}

func TestRelationFollowsRelationShouldNotBeReversedIfValidationFails(t *testing.T) {
	c := setup(t)
	e := c.env()
	from, to := c.issue(1), c.issue(2)
	r := c.newRel(from, to, domain.RelationFollows)
	r.delayRaw = "xx"
	if c.createRel(e, r) {
		t.Fatal("should not save")
	}
	if r.RelationType != domain.RelationFollows || r.From != from || r.To != to {
		t.Errorf("relation was reversed: %+v", r.IssueRelation)
	}
}

func TestRelationRelationTypeFor(t *testing.T) {
	r := &domain.IssueRelation{IssueFromID: 1, IssueToID: 2, RelationType: domain.RelationPrecedes}
	if r.RelationTypeFor(1) != domain.RelationPrecedes || r.RelationTypeFor(2) != domain.RelationFollows {
		t.Error("relation_type_for")
	}
}

func TestRelationSetIssueToDatesWithoutIssues(t *testing.T) {
	c := setup(t)
	e := c.env()
	from, err := e.NewBlank(c.ctx)
	c.must(err)
	from.SetStartDate(ptrTime(today()))
	st := &saveState{result: &SaveResult{}}
	r := &domain.IssueRelation{RelationType: domain.RelationPrecedes, Delay: intp(1)}
	// issue_to なし
	c.must(e.setIssueToDatesWith(c.ctx, r, from, nil, nil, st))
	// issue_from / issue_to なし
	c.must(e.setIssueToDatesWith(c.ctx, &domain.IssueRelation{RelationType: domain.RelationPrecedes, Delay: intp(1)}, nil, nil, nil, st))
	if len(st.records) != 0 {
		t.Error("nothing should be saved")
	}
}

func TestRelationValidatesCircularDependency(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM issue_relations`)
	e := c.env()
	c.addRelation(e, c.issue(1), c.issue(2), domain.RelationPrecedes, nil)
	c.addRelation(e, c.issue(2), c.issue(3), domain.RelationPrecedes, nil)
	r := c.newRel(c.issue(3), c.issue(1), domain.RelationPrecedes)
	if c.createRel(e, r) {
		t.Fatal("should not save")
	}
	if len(r.Errors.On("base")) == 0 {
		t.Errorf("errors = %v", r.Errors.List)
	}
}

func TestRelationValidatesCircularDependencyOfSubtask(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, nil)
	i2 := c.generateSaved(e, nil)
	c.addRelation(e, i1, i2, domain.RelationPrecedes, nil)
	child := c.generateSaved(e, Params{"parent_issue_id": i2.ID})
	c.reload(e, i1)
	c.reload(e, child)
	r := c.newRel(child, i1, domain.RelationPrecedes)
	if c.createRel(e, r) {
		t.Fatal("should not save")
	}
	if !hasRelErr(r, "base", "circular_dependency") {
		t.Errorf("errors = %v", r.Errors.List)
	}
}

func hasRelErr(r *Relation, attr, key string) bool {
	for _, k := range r.Errors.On(attr) {
		if k == key {
			return true
		}
	}
	return false
}

func TestRelationSubtasksShouldAllowPrecedesRelation(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, nil)
	c1 := c.generateSaved(e, Params{"parent_issue_id": parent.ID})
	c2 := c.generateSaved(e, Params{"parent_issue_id": parent.ID})
	r := c.newRel(c1, c2, domain.RelationPrecedes)
	if ok, err := e.ValidateRelation(c.ctx, r); err != nil || !ok {
		t.Fatalf("valid: %v %v", err, r.Errors.List)
	}
	if !c.createRel(e, r) {
		t.Fatalf("save: %v", r.Errors.List)
	}
}

func TestRelationValidatesCircularDependencyOnReverseRelations(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM issue_relations`)
	e := c.env()
	c.addRelation(e, c.issue(1), c.issue(3), domain.RelationBlocks, nil)
	c.addRelation(e, c.issue(1), c.issue(2), domain.RelationBlocked, nil)
	r := c.newRel(c.issue(2), c.issue(1), domain.RelationBlocked)
	if c.createRel(e, r) {
		t.Fatal("should not save")
	}
	if len(r.Errors.On("base")) == 0 {
		t.Errorf("errors = %v", r.Errors.List)
	}
}

func hasDetail(j *Journal, property, key string, old, value *string, checkOld bool) bool {
	for _, d := range j.Details {
		if d.Property == property && d.PropKey == key && (value == nil || eqPtr(d.Value, value)) && (!checkOld || eqPtr(d.OldValue, old)) {
			return true
		}
	}
	return false
}

func TestRelationCreateWithInitializedJournalsShouldCreateJournals(t *testing.T) {
	c := setup(t)
	e := c.env()
	from, to := c.issue(1), c.issue(2)
	r := c.newRel(from, to, domain.RelationPrecedes)
	c.must(e.InitRelationJournals(c.ctx, r, c.user(1)))
	fj, tj := len(journals(c, 1)), len(journals(c, 2))
	if !c.createRel(e, r) {
		t.Fatalf("save: %v", r.Errors.List)
	}
	if len(journals(c, 1)) != fj+1 || len(journals(c, 2)) != tj+1 {
		t.Fatalf("journals: %d->%d, %d->%d", fj, len(journals(c, 1)), tj, len(journals(c, 2)))
	}
	if j := lastJournal(c, 1); !hasDetail(j, "relation", "precedes", nil, sp("2"), false) {
		t.Errorf("from details = %v", j.Details)
	}
	j := lastJournal(c, 2)
	if len(j.Details) != 3 || !hasDetail(j, "relation", "follows", nil, sp("1"), true) ||
		!hasDetail(j, "attr", "due_date", nil, nil, false) || !hasDetail(j, "attr", "start_date", nil, nil, false) {
		for _, d := range j.Details {
			t.Logf("detail %+v", *d)
		}
		t.Error("to details")
	}
}

func TestRelationDestroyWithInitializedJournalsShouldCreateJournals(t *testing.T) {
	c := setup(t)
	e := c.env()
	rel := c.relation(1)
	fj, tj := len(journals(c, rel.IssueFromID)), len(journals(c, rel.IssueToID))
	_, err := e.DestroyRelation(c.ctx, rel, c.user(1))
	c.must(err)
	if len(journals(c, rel.IssueFromID)) != fj+1 || len(journals(c, rel.IssueToID)) != tj+1 {
		t.Fatal("journal counts")
	}
	fd := lastJournal(c, rel.IssueFromID).Details
	d := fd[len(fd)-1]
	if d.Property != "relation" || d.PropKey != "blocks" || !eqPtr(d.OldValue, sp("9")) || d.Value != nil {
		t.Errorf("from detail = %+v", *d)
	}
	td := lastJournal(c, rel.IssueToID).Details
	d = td[len(td)-1]
	if d.Property != "relation" || d.PropKey != "blocked" || !eqPtr(d.OldValue, sp("10")) || d.Value != nil {
		t.Errorf("to detail = %+v", *d)
	}
}

func TestRelationToS(t *testing.T) {
	c := setup(t)
	rel := c.relation(1)
	// "Blocks #9" / "Blocked by #10"
	if rel.LabelFor(rel.IssueFromID) != "label_blocks" || rel.OtherIssueID(rel.IssueFromID) != 9 {
		t.Errorf("from: %s #%d", rel.LabelFor(rel.IssueFromID), rel.OtherIssueID(rel.IssueFromID))
	}
	if rel.LabelFor(rel.IssueToID) != "label_blocked_by" || rel.OtherIssueID(rel.IssueToID) != 10 {
		t.Errorf("to: %s #%d", rel.LabelFor(rel.IssueToID), rel.OtherIssueID(rel.IssueToID))
	}
}
