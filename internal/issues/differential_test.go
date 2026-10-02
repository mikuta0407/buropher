package issues

// Redmine との差分テスト: testdata/gen/dump_scenario.rb と同じ操作列 (作成・更新・ワークフロー・
// 親子・関連と再スケジュール・コピー・移動・重複のクローズ・親の解除・不正な更新・削除) を
// issues パッケージの API で実行し、DB の状態 (チケット・階層順・ジャーナルと詳細・関連・ウォッチャー・
// カスタム値) と、各ステップの通知 (イベント・対象・受信者) を Redmine の結果 (testdata/scenario.json) と比較する。

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

//go:embed testdata/scenario.json
var scenarioJSON []byte

type scenarioRun struct {
	c          *tc
	log        []map[string]any
	deliveries map[string][]string
	step       string
	steps      []string
}

func (s *scenarioRun) begin(name string) {
	s.step = name
	s.steps = append(s.steps, name)
	if s.deliveries == nil {
		s.deliveries = map[string][]string{}
	}
	s.deliveries[name] = []string{}
}

func (s *scenarioRun) notify(res *SaveResult) {
	if res == nil {
		return
	}
	for _, n := range res.Notifications {
		users := slices.Clone(n.Recipients)
		if n.SelfExcluded {
			users = append(users, n.AuthorID)
		}
		obj := fmt.Sprintf("issue:%d", n.IssueID)
		if n.Event == NotifyIssueEdit {
			obj = fmt.Sprintf("journal:%d", n.JournalID)
		}
		for _, u := range users {
			s.deliveries[s.step] = append(s.deliveries[s.step], fmt.Sprintf("%s %s user:%d", n.Event, obj, u))
		}
	}
}

func errList(errs []errPair) []any {
	out := []any{}
	for _, e := range errs {
		out = append(out, []any{e.attr, e.key})
	}
	return out
}

type errPair struct{ attr, key string }

func (s *scenarioRun) record(name string, ok bool, id any, errs []errPair) {
	s.log = append(s.log, map[string]any{"step": s.step, "name": name, "value": map[string]any{"ok": ok, "id": id, "errors": errList(errs)}})
}

func issueErrs(iss *Issue) []errPair {
	var out []errPair
	for _, e := range iss.Errors.List {
		k := e.Key
		if k == "" {
			k = e.Message
		}
		out = append(out, errPair{e.Attr, k})
	}
	return out
}

func (s *scenarioRun) createIssue(uid, projectID int64, attrs Params) *Issue {
	c := s.c
	c.as(uid)
	e := c.env()
	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	c.must(e.SetProject(c.ctx, iss, c.project(projectID), false))
	iss.AuthorID = uid
	if c.st.Bool("default_issue_start_date_to_creation_date") && iss.StartDate == nil {
		iss.StartDate = ptrTime(today())
	}
	c.must(e.SafeAssign(c.ctx, iss, attrs, c.user(uid)))
	if iss.TrackerID == 0 {
		ts, err := e.AllowedTargetTrackers(c.ctx, iss, c.user(uid))
		c.must(err)
		if len(ts) > 0 {
			c.must(e.SetTracker(c.ctx, iss, ts[0]))
		}
	}
	ok, res, err := e.Save(c.ctx, iss)
	c.must(err)
	s.notify(res)
	var id any
	if ok {
		id = iss.ID
	}
	s.record("create", ok, id, issueErrs(iss))
	return iss
}

func (s *scenarioRun) updateIssue(uid, id int64, attrs Params, notes string) {
	c := s.c
	c.as(uid)
	e := c.env()
	iss, err := e.Load(c.ctx, id)
	c.must(err)
	_, err = e.InitJournal(c.ctx, iss, c.user(uid), "")
	c.must(err)
	if notes != "" {
		attrs["notes"] = notes
	}
	c.must(e.SafeAssign(c.ctx, iss, attrs, c.user(uid)))
	ok, res, err := e.Save(c.ctx, iss)
	c.must(err)
	s.notify(res)
	s.record("update", ok, id, issueErrs(iss))
}

func (s *scenarioRun) addRelation(uid, from int64, typ string, to int64, delay string) {
	c := s.c
	c.as(uid)
	e := c.env()
	fi, err := e.Load(c.ctx, from)
	c.must(err)
	r, err := e.NewRelation(c.ctx, fi, RelationParams{IssueToID: fmt.Sprint(to), RelationType: typ, Delay: delay}, c.user(uid))
	c.must(err)
	c.must(e.InitRelationJournals(c.ctx, r, c.user(uid)))
	ok, res, err := e.CreateRelation(c.ctx, r)
	c.must(err)
	s.notify(res)
	var id any
	if ok {
		id = r.ID
	}
	var errs []errPair
	for _, x := range r.Errors.List {
		errs = append(errs, errPair{x.Attr, x.Key})
	}
	s.record("relation", ok, id, errs)
}

// runScenario は dump_scenario.rb と同じ操作を実行する。
func runScenario(t *testing.T, c *tc) *scenarioRun {
	s := &scenarioRun{c: c}
	var a, b, cc, d, e2, cp *Issue

	s.begin("s1 create A")
	a = s.createIssue(2, 1, Params{"tracker_id": "1", "subject": "Diff A", "description": "line1\nline2 @dlopper",
		"priority_id": "5", "assigned_to_id": "3", "category_id": "1", "fixed_version_id": "3",
		"start_date": "2026-01-19", "due_date": "2026-01-23", "estimated_hours": "2h30",
		"custom_field_values": map[string]any{"2": "value1", "1": "PostgreSQL"}, "watcher_user_ids": []string{"3", "8"}})

	s.begin("s2 create B")
	b = s.createIssue(2, 1, Params{"tracker_id": "1", "subject": "Diff B", "start_date": "2026-01-20", "due_date": "2026-01-21"})

	s.begin("s3 create child C")
	cc = s.createIssue(2, 1, Params{"tracker_id": "1", "subject": "Diff C", "parent_issue_id": fmt.Sprint(a.ID), "start_date": "2026-01-26",
		"due_date": "2026-01-28", "estimated_hours": "4", "done_ratio": "50", "priority_id": "6"})

	s.begin("s4 relation A precedes B")
	s.addRelation(2, a.ID, "precedes", b.ID, "2")

	s.begin("s5 workflow status change by assignee")
	s.updateIssue(3, a.ID, Params{"status_id": "2"}, "Status change note")

	s.begin("s6 update child C (reschedule B through parent)")
	s.updateIssue(2, cc.ID, Params{"due_date": "2026-01-30", "done_ratio": "80", "custom_field_values": map[string]any{"2": "changed"}}, "")

	s.begin("s7 private note with changes on B")
	s.updateIssue(2, b.ID, Params{"private_notes": "1", "done_ratio": "20"}, "private note")

	s.begin("s8 copy A with subtasks")
	{
		c.as(1)
		e := c.env()
		cp, _ = e.NewBlank(c.ctx)
		_, err := e.InitJournal(c.ctx, cp, c.user(1), "")
		c.must(err)
		src, err := e.Load(c.ctx, a.ID)
		c.must(err)
		c.must(e.CopyFrom(c.ctx, cp, src, CopyOptions{}))
		pid := ""
		if src.ParentID != nil {
			pid = fmt.Sprint(*src.ParentID)
		}
		c.must(e.SetParentIssueID(c.ctx, cp, pid))
		c.must(e.SetProject(c.ctx, cp, c.project(1), false))
		c.must(e.SafeAssign(c.ctx, cp, Params{"subject": "Diff A copy", "status_id": "1"}, c.user(1)))
		ok, res, err := e.Save(c.ctx, cp)
		c.must(err)
		s.notify(res)
		s.record("copy", ok, cp.ID, issueErrs(cp))
	}

	s.begin("s9 move B to project 2")
	s.updateIssue(1, b.ID, Params{"project_id": "2"}, "")

	s.begin("s10 close duplicated issue")
	e2 = s.createIssue(2, 1, Params{"tracker_id": "1", "subject": "Diff E"})
	d = s.createIssue(2, 1, Params{"tracker_id": "1", "subject": "Diff D (duplicate)"})
	s.addRelation(2, d.ID, "duplicates", e2.ID, "")
	s.updateIssue(1, e2.ID, Params{"status_id": "5"}, "closing E")

	s.begin("s11 remove parent of C")
	s.updateIssue(2, cc.ID, Params{"parent_issue_id": ""}, "")

	s.begin("s12 invalid updates")
	s.updateIssue(2, a.ID, Params{"start_date": "2026-02-10", "due_date": "2026-02-01"}, "")
	s.updateIssue(2, cc.ID, Params{"parent_issue_id": fmt.Sprint(cc.ID)}, "")
	s.addRelation(2, b.ID, "follows", a.ID, "")

	s.begin("s13 destroy copy")
	{
		c.as(1)
		e := c.env()
		res, err := e.DestroyIssues(c.ctx, []int64{cp.ID}, DestroyOptions{})
		c.must(err)
		s.notify(res)
	}
	return s
}

func tsStr(t db.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Format("2006-01-02 15:04:05")
}

func dateStrAny(d db.NullDate) any {
	if !d.Valid {
		return nil
	}
	return d.Date.String()
}

func nullStr(s sql.NullString) any {
	if !s.Valid || s.String == "" {
		return nil
	}
	return s.String
}

func nullInt(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

// dumpState は dump_scenario.rb と同じ形で DB の状態を書き出す。
func dumpState(t *testing.T, c *tc) map[string]any {
	var rows []issueRow
	c.must(c.d.Select(c.ctx, &rows, `SELECT `+issueCols+` FROM issues ORDER BY id`))
	var issues []any
	for _, r := range rows {
		var est any
		if r.EstimatedHours.Valid {
			est = r.EstimatedHours.Float64
		}
		issues = append(issues, map[string]any{"id": r.ID, "project_id": r.ProjectID, "tracker_id": r.TrackerID, "status_id": r.StatusID,
			"priority_id": r.PriorityID, "author_id": r.AuthorID, "assigned_to_id": nullInt(r.AssignedToID),
			"category_id": nullInt(r.CategoryID), "fixed_version_id": nullInt(r.FixedVersionID), "parent_id": nullInt(r.ParentID),
			"root_id": r.RootID, "subject": r.Subject, "description": nullStr(r.Description),
			"start_date": dateStrAny(r.StartDate), "due_date": dateStrAny(r.DueDate), "done_ratio": r.DoneRatio,
			"estimated_hours": est, "is_private": r.IsPrivate, "lock_version": r.LockVersion,
			"created_on": tsStr(db.NullTime{Time: r.CreatedAt.Time, Valid: true}), "updated_on": tsStr(db.NullTime{Time: r.UpdatedAt.Time, Valid: true}),
			"closed_on": tsStr(r.ClosedAt)})
	}
	tree := c.ids(`SELECT id FROM issues ORDER BY root_id, hier_path`)
	var jrows []journalRow
	c.must(c.d.Select(c.ctx, &jrows, `SELECT id, issue_id, user_id, notes, private_notes, created_at, updated_at, updated_by_id FROM issue_journals ORDER BY id`))
	var journals []any
	e := c.env()
	for _, j := range jrows {
		ds, err := e.journalDetails(c.ctx, j.ID)
		c.must(err)
		details := []any{}
		for _, d := range ds {
			var o, v any
			if d.OldValue != nil {
				o = *d.OldValue
			}
			if d.Value != nil {
				v = *d.Value
			}
			details = append(details, []any{d.Property, d.PropKey, o, v})
		}
		journals = append(journals, map[string]any{"id": j.ID, "issue_id": j.IssueID, "user_id": j.UserID, "notes": nullStr(j.Notes),
			"private_notes": j.PrivateNotes, "created_on": tsStr(db.NullTime{Time: j.CreatedAt.Time, Valid: true}), "details": details})
	}
	var rels []relationRow
	c.must(c.d.Select(c.ctx, &rels, `SELECT id, issue_from_id, issue_to_id, relation_type, delay FROM issue_relations ORDER BY id`))
	var relations []any
	for _, r := range rels {
		relations = append(relations, []any{r.ID, r.IssueFromID, r.IssueToID, r.RelationType, nullInt(r.Delay)})
	}
	var ws []struct {
		IssueID int64 `db:"watchable_id"`
		UserID  int64 `db:"principal_id"`
	}
	c.must(c.d.Select(c.ctx, &ws, `SELECT watchable_id, principal_id FROM watchers WHERE watchable_kind = 'issue' ORDER BY watchable_id, principal_id`))
	var watchers []any
	for _, w := range ws {
		watchers = append(watchers, []any{w.IssueID, w.UserID})
	}
	var cvs []struct {
		IssueID int64          `db:"customized_id"`
		FieldID int64          `db:"custom_field_id"`
		Value   sql.NullString `db:"value"`
	}
	c.must(c.d.Select(c.ctx, &cvs, `SELECT customized_id, custom_field_id, value FROM custom_values WHERE customized_kind = 'issue'
ORDER BY customized_id, custom_field_id, id`))
	var values []any
	for _, v := range cvs {
		values = append(values, []any{v.IssueID, v.FieldID, nullStr(v.Value)})
	}
	return map[string]any{"issues": issues, "tree_order": tree, "journals": journals, "relations": relations,
		"watchers": watchers, "custom_values": values}
}

// normalizeJSON は JSON を経由して数値型などを揃える。
func normalizeJSON(t *testing.T, v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDifferentialScenario(t *testing.T) {
	var want map[string]any
	if err := json.Unmarshal(scenarioJSON, &want); err != nil {
		t.Fatal(err)
	}
	c := setup(t)
	// 参照 DB (redmine.pristine.sqlite3) の設定
	c.setting("default_notification_option", "only_assigned")
	c.setting("text_formatting", "common_mark")
	c.setting("wiki_tablesort_enabled", "0")
	c.setting("rest_api_enabled", "1")

	s := runScenario(t, c)
	got := normalizeJSON(t, dumpState(t, c)).(map[string]any)
	got["log"] = normalizeJSON(t, s.log)

	for _, key := range []string{"log", "issues", "tree_order", "journals", "relations", "watchers", "custom_values"} {
		w, g := want[key], got[key]
		if reflect.DeepEqual(w, g) {
			continue
		}
		wl, ok1 := w.([]any)
		gl, ok2 := g.([]any)
		if !ok1 || !ok2 {
			t.Errorf("%s: want %v\n got %v", key, w, g)
			continue
		}
		for i := 0; i < max(len(wl), len(gl)); i++ {
			var wi, gi any
			if i < len(wl) {
				wi = wl[i]
			}
			if i < len(gl) {
				gi = gl[i]
			}
			if !reflect.DeepEqual(wi, gi) {
				t.Errorf("%s[%d]:\n want %v\n  got %v", key, i, wi, gi)
			}
		}
	}

	// 通知: ステップごとに (イベント, 対象, 受信者) の集合を比較する
	wantDel := map[string][]string{}
	cur := ""
	for _, x := range want["deliveries"].([]any) {
		m := x.(map[string]any)
		if st, ok := m["step"]; ok {
			cur = st.(string)
			wantDel[cur] = []string{}
			continue
		}
		wantDel[cur] = append(wantDel[cur], fmt.Sprintf("%s %s:%v user:%v", m["event"], m["object"], m["object_id"], m["user"]))
	}
	for _, st := range s.steps {
		w, g := slices.Clone(wantDel[st]), slices.Clone(s.deliveries[st])
		sort.Strings(w)
		sort.Strings(g)
		if !slices.Equal(w, g) {
			t.Errorf("deliveries %q:\n want %s\n  got %s", st, strings.Join(w, ", "), strings.Join(g, ", "))
		}
	}
}
