package issues

// 差分シナリオ 2 (testdata/gen/dump_scenario2.rb): 複数値カスタムフィールド、ワークフローの必須・読み取り専用、
// メンション、子の進捗率の導出、一括更新・一括コピー、再オープン、工数の付け替えを伴う削除、ノートの編集、
// ブロックによる遷移制限、子を伴うプロジェクト移動、バージョン共有の変更。

import (
	_ "embed"
	"fmt"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

//go:embed testdata/scenario2.json
var scenario2JSON []byte

func (s *scenarioRun) bulkUpdate(uid int64, ids []int64, attrs Params, notes string, opts BulkOptions) {
	c := s.c
	c.as(uid)
	e := c.env()
	var issues []*Issue
	for _, id := range ids {
		iss, err := e.Load(c.ctx, id)
		c.must(err)
		issues = append(issues, iss)
	}
	opts.Notes = notes
	// 1 件ずつ保存して結果を記録する (BulkUpdate と同じ処理順)
	res, err := e.BulkUpdate(c.ctx, issues, ParseBulkParams(attrs), opts, c.user(uid))
	c.must(err)
	s.notify(&res.SaveResult)
	for _, iss := range res.Saved {
		s.record("bulk", true, iss.ID, nil)
	}
	for _, iss := range res.Unsaved {
		s.record("bulk", false, iss.ID, issueErrs(iss))
	}
}

func runScenario2(t *testing.T, c *tc) *scenarioRun {
	s := &scenarioRun{c: c}
	var g, h, i, k, l, m *Issue

	s.begin("t1 setup")
	// Redmine の参照 DB は custom_fields の採番が 12 から (フィクスチャの異常行の分)
	cf := c.createCF(cfAttrs{ID: 12, Name: "Multi", Format: "list", Multiple: true, PossibleValues: []string{"A", "B", "C"}, IsForAll: true})
	s.log = append(s.log, map[string]any{"step": s.step, "name": "cf", "value": cf})
	c.addWorkflowPermission(1, 1, 2, "due_date", "required")
	c.addWorkflowPermission(1, 1, 2, "priority_id", "readonly")
	cfKey := fmt.Sprint(cf)

	s.begin("t2 create with workflow rules")
	s.createIssue(3, 1, Params{"tracker_id": "1", "subject": "Diff G missing due"})
	g = s.createIssue(3, 1, Params{"tracker_id": "1", "subject": "Diff G", "due_date": "2026-02-20", "priority_id": "7",
		"is_private": "1", "custom_field_values": map[string]any{cfKey: []string{"A", "B"}}})

	s.begin("t3 multi value change and mention")
	s.updateIssue(3, g.ID, Params{"custom_field_values": map[string]any{cfKey: []string{"B", "C"}}}, "@jsmith please check\n\n```\n@admin\n```")

	s.begin("t4 children and derived done ratio")
	h = s.createIssue(2, 1, Params{"tracker_id": "1", "subject": "Diff H", "parent_issue_id": fmt.Sprint(g.ID), "estimated_hours": "2",
		"done_ratio": "40"})
	i = s.createIssue(2, 1, Params{"tracker_id": "1", "subject": "Diff I", "parent_issue_id": fmt.Sprint(g.ID), "estimated_hours": "6",
		"status_id": "5"})
	s.updateIssue(2, i.ID, Params{"status_id": "5"}, "closing child")

	s.begin("t5 bulk update")
	s.bulkUpdate(1, []int64{g.ID, 2}, Params{"assigned_to_id": "3", "fixed_version_id": "3", "start_date": "none"}, "bulk", BulkOptions{})

	s.begin("t6 bulk copy with subtasks")
	s.bulkUpdate(1, []int64{g.ID}, Params{"project_id": "1"}, "bulk copy", BulkOptions{Copy: true, CopySubtasks: true, Link: true})

	s.begin("t7 reopen child")
	s.updateIssue(1, i.ID, Params{"status_id": "1"}, "reopen")

	s.begin("t8 destroy with time entry reassign")
	{
		c.exec(`INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at)
VALUES (1, 2, 2, ?, 1.5, 9, '2026-01-15', 2026, 1, 3, ?, ?)`, h.ID, db.NewTime(frozenNow), db.NewTime(frozenNow))
		c.as(1)
		e := c.env()
		res, err := e.DestroyIssues(c.ctx, []int64{g.ID}, DestroyOptions{Todo: TimeEntriesReassign, ReassignToID: 1, ProjectID: 1})
		c.must(err)
		s.notify(res)
		s.log = append(s.log, map[string]any{"step": s.step, "name": "destroy", "value": []int64{g.ID}})
	}

	s.begin("t9 edit journal notes")
	for _, x := range []struct {
		id    int64
		notes string
	}{{1, "Edited note"}, {2, ""}} {
		c.as(1)
		e := c.env()
		j, err := e.FindJournal(c.ctx, x.id)
		c.must(err)
		c.must(e.UpdateJournalNotes(c.ctx, j, x.notes, nil, c.user(1)))
		exists := c.count(`SELECT COUNT(*) FROM issue_journals WHERE id = ?`, x.id) > 0
		s.log = append(s.log, map[string]any{"step": s.step, "name": "edit_journal", "value": map[string]any{"id": x.id, "exists": exists}})
	}

	s.begin("t10 blocked issue cannot be closed")
	j := s.createIssue(1, 1, Params{"tracker_id": "1", "subject": "Diff J"})
	k = s.createIssue(1, 1, Params{"tracker_id": "1", "subject": "Diff K"})
	s.addRelation(1, j.ID, "blocks", k.ID, "")
	{
		c.as(1)
		e := c.env()
		kk, err := e.Load(c.ctx, k.ID)
		c.must(err)
		ss, err := e.NewStatusesAllowedTo(c.ctx, kk, c.user(1), false)
		c.must(err)
		s.log = append(s.log, map[string]any{"step": s.step, "name": "allowed", "value": statusIDs(ss)})
	}
	s.updateIssue(1, k.ID, Params{"status_id": "5"}, "try close")

	s.begin("t11 move with subtasks")
	l = s.createIssue(1, 1, Params{"tracker_id": "1", "subject": "Diff L", "category_id": "1", "fixed_version_id": "3"})
	m = s.createIssue(1, 1, Params{"tracker_id": "1", "subject": "Diff M", "parent_issue_id": fmt.Sprint(l.ID), "category_id": "2"})
	s.updateIssue(1, l.ID, Params{"project_id": "3"}, "")

	s.begin("t12 version sharing change")
	{
		c.as(1)
		e := c.env()
		c.exec(`UPDATE versions SET sharing = 'descendants' WHERE id = 3`)
		res, err := e.UpdateVersionsFromSharingChange(c.ctx, 3)
		c.must(err)
		s.notify(res)
		s.updateIssue(1, m.ID, Params{"fixed_version_id": "3"}, "")
		c.exec(`UPDATE versions SET sharing = 'none' WHERE id = 3`)
		e = c.env()
		res, err = e.UpdateVersionsFromSharingChange(c.ctx, 3)
		c.must(err)
		s.notify(res)
	}
	return s
}

func TestDifferentialScenario2(t *testing.T) {
	c := diffSetup(t)
	s := runScenario2(t, c)
	compareScenario(t, scenario2JSON, c, s)
}
