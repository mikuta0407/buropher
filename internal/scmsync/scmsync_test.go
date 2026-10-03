// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scmsync_test

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/scm/scmtest"
	"github.com/mikuta0407/buropher/internal/scmsync"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

var frozenNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

type env struct {
	t   *testing.T
	ctx context.Context
	d   *db.DB
	st  *settings.Settings
	svc *scmsync.Service
}

func setup(t *testing.T) *env {
	t.Helper()
	d := dbtest.New(t)
	testfixtures.LoadAt(t, d, frozenNow, testfixtures.All()...)
	ctx := context.Background()
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, ctx: ctx, d: d, st: st, svc: &scmsync.Service{DB: d, Settings: st, Now: func() time.Time { return frozenNow }}}
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

// gitRepo は repository_git_test.rb の setup（Project 3 に path_encoding ISO-8859-1 の Git リポジトリ）。
func (e *env) gitRepo(path string) *domain.Repository {
	r := &domain.Repository{ProjectID: 3, SCM: "git", URL: path, PathEncoding: "ISO-8859-1", IsDefault: true, CreatedOn: frozenNow}
	e.must(repository.InsertScmRepository(e.ctx, e.d, r))
	return r
}

func (e *env) reload(r *domain.Repository) *domain.Repository {
	x, err := repository.GetScmRepository(e.ctx, e.d, r.ID)
	e.must(err)
	return x
}

func TestFetchChangesetsFromScratch(t *testing.T) {
	path := scmtest.GitRepositoryPath(t)
	e := setup(t)
	r := e.gitRepo(path)
	e.must(e.svc.Fetch(e.ctx, r, nil))
	r = e.reload(r)
	if n, _ := repository.CountChangesets(e.ctx, e.d, r.ID); n != 29 {
		t.Fatalf("changesets = %d", n)
	}
	var files int
	e.must(e.d.Get(e.ctx, &files, `SELECT COUNT(*) FROM changeset_files JOIN changesets ON changesets.id = changeset_files.changeset_id WHERE repository_id = ?`, r.ID))
	if files != 40 {
		t.Errorf("filechanges = %d", files)
	}
	c, err := repository.FindChangesetByRevision(e.ctx, e.d, r.ID, "7234cb2750b63f47bff735edc50a1c0a433c2518")
	e.must(err)
	if c.Scmid != "7234cb2750b63f47bff735edc50a1c0a433c2518" || c.Comments != "Initial import.\nThe repository contains 3 files." ||
		c.Committer != "jsmith <jsmith@foo.bar>" || c.UserID == nil || *c.UserID != 2 {
		t.Errorf("commit = %+v", c)
	}
	if !c.CommittedOn.Equal(time.Date(2007, 12, 14, 9, 22, 52, 0, time.UTC)) || c.CommitDate.Format("2006-01-02") != "2007-12-14" {
		t.Errorf("committed_on = %v %v", c.CommittedOn, c.CommitDate)
	}
	fs, err := repository.ChangesetFiles(e.ctx, e.d, c.ID, true, 0)
	e.must(err)
	if len(fs) != 3 || fs[0].Path != "README" || fs[0].Action != "A" || fs[0].FromPath != "" {
		t.Errorf("files = %+v", fs)
	}
	heads, _ := r.ExtraInfo["heads"].([]any)
	if len(heads) != 8 {
		t.Errorf("heads = %v", r.ExtraInfo["heads"])
	}
	if dc, _ := r.ExtraInfo["db_consistent"].(map[string]any); dc["ordering"] != float64(1) {
		t.Errorf("db_consistent = %v", r.ExtraInfo["db_consistent"])
	}
	// test_log_utf8
	c, err = repository.FindChangesetByRevision(e.ctx, e.d, r.ID, "ed5bb786bbda2dee66a2d50faf51429dbc043a7b")
	e.must(err)
	if c.Committer != "Felix Schäfer <felix@fachschaften.org>" {
		t.Errorf("committer = %q", c.Committer)
	}
	// 2 回目は何もしない
	e.must(e.svc.Fetch(e.ctx, r, nil))
	if n, _ := repository.CountChangesets(e.ctx, e.d, r.ID); n != 29 {
		t.Errorf("refetch changesets = %d", n)
	}
}

func TestFetchChangesetsIncremental(t *testing.T) {
	path := scmtest.GitRepositoryPath(t)
	e := setup(t)
	r := e.gitRepo(path)
	e.must(e.svc.Fetch(e.ctx, r, nil))
	r = e.reload(r)
	var heads []any
	for _, h := range r.ExtraInfo["heads"].([]any) {
		if h != "b1650eac7c505a6dab9f19858afc9ecb481eccc2" {
			heads = append(heads, h)
		}
	}
	if len(heads) != 6 {
		t.Fatalf("heads = %d", len(heads))
	}
	del := []string{"b1650eac7c505a6dab9f19858afc9ecb481eccc2", "83ca5fd546063a3c7dc2e568ba3355661a9e2b2c",
		"ed5bb786bbda2dee66a2d50faf51429dbc043a7b", "4f26664364207fa8b1af9f8722647ab2d4ac5d43",
		"deff712f05a90d96edbd70facc47d944be5897e3", "32ae898b720c2f7eec2723d5bdd558b4cb2d3ddf",
		"7e61ac704deecde634b51e59daa8110435dcb3da"}
	for _, s := range del {
		_, err := e.d.Exec(e.ctx, `DELETE FROM changesets WHERE repository_id = ? AND scmid = ?`, r.ID, s)
		e.must(err)
	}
	heads = append(heads, "4a07fe31bffcf2888791f3e6cbc9c4545cefe3e8")
	r.MergeExtraInfo(map[string]any{"heads": heads})
	e.must(repository.UpdateScmRepositoryExtraInfo(e.ctx, e.d, r.ID, r.ExtraInfo))
	e.must(e.svc.Fetch(e.ctx, r, nil))
	r = e.reload(r)
	if n, _ := repository.CountChangesets(e.ctx, e.d, r.ID); n != 29 {
		t.Errorf("changesets = %d", n)
	}
	hs := r.ExtraInfo["heads"].([]any)
	if len(hs) != 8 || !slices.Contains(hs, any("b1650eac7c505a6dab9f19858afc9ecb481eccc2")) {
		t.Errorf("heads = %v", hs)
	}
}

func TestParentsAndFindByName(t *testing.T) {
	path := scmtest.GitRepositoryPath(t)
	e := setup(t)
	r := e.gitRepo(path)
	e.must(e.svc.Fetch(e.ctx, r, nil))
	find := func(name string) *domain.Changeset {
		c, err := scmsync.FindChangesetByName(e.ctx, e.d, r, name)
		e.must(err)
		return c
	}
	r1 := find("7234cb2750b63")
	if ps, _ := repository.ChangesetParents(e.ctx, e.d, r1.ID); len(ps) != 0 {
		t.Errorf("r1 parents = %d", len(ps))
	}
	r2 := find("899a15dba03a3")
	if ps, _ := repository.ChangesetParents(e.ctx, e.d, r2.ID); len(ps) != 1 || ps[0].Scmid != "7234cb2750b63f47bff735edc50a1c0a433c2518" {
		t.Errorf("r2 parents = %v", ps)
	}
	r3 := find("32ae898b720c2")
	ps, _ := repository.ChangesetParents(e.ctx, e.d, r3.ID)
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.Scmid)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"4a07fe31bffcf2888791f3e6cbc9c4545cefe3e8", "7e61ac704deecde634b51e59daa8110435dcb3da"}) {
		t.Errorf("r3 parents = %v", ids)
	}
	for _, n := range []string{"", " "} {
		if find(n) != nil {
			t.Errorf("find(%q) should be nil", n)
		}
	}
	if c := find("7234cb2750b"); c == nil || c.FormatIdentifier() != "7234cb27" || c.Identifier() != c.Scmid {
		t.Errorf("format_identifier = %+v", c)
	}
	// test_previous / test_next
	cs := find("1ca7f5ed")
	prev, _ := repository.PreviousChangeset(e.ctx, e.d, cs)
	if prev == nil || prev.Scmid != "64f1f3e89ad1cb57976ff0ad99a107012ba3481d" {
		t.Errorf("previous = %+v", prev)
	}
	if p, _ := repository.PreviousChangeset(e.ctx, e.d, find("7234cb275")); p != nil {
		t.Errorf("previous of first = %+v", p)
	}
	next, _ := repository.NextChangeset(e.ctx, e.d, find("64f1f3e89ad1"))
	if next == nil || next.Scmid != "1ca7f5ed374f3cb31a93ae5215c2e25cc6ec5127" {
		t.Errorf("next = %+v", next)
	}
	if n, _ := repository.NextChangeset(e.ctx, e.d, find("2a682156a3b6e77a")); n != nil {
		t.Errorf("next of last = %+v", n)
	}
}

func TestFetchInvalidRepository(t *testing.T) {
	scmtest.GitRepositoryPath(t)
	e := setup(t)
	r := e.gitRepo("/invalid")
	e.must(e.svc.Fetch(e.ctx, r, nil))
	if n, _ := repository.CountChangesets(e.ctx, e.d, r.ID); n != 0 {
		t.Errorf("changesets = %d", n)
	}
}

// TestFetchFixesIssue は取り込み時の修正キーワード・作業時間の記録（リポジトリ作成後のコミット）を確認する。
func TestFetchFixesIssue(t *testing.T) {
	scmtest.GitRepositoryPath(t) // git が無ければスキップ
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=jsmith", "GIT_AUTHOR_EMAIL=jsmith@somenet.foo",
			"GIT_COMMITTER_NAME=jsmith", "GIT_COMMITTER_EMAIL=jsmith@somenet.foo",
			"GIT_AUTHOR_DATE=2026-01-15T13:00:00Z", "GIT_COMMITTER_DATE=2026-01-15T13:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "master")
	run("commit", "-q", "--allow-empty", "-m", "Fixes #1 @2h")
	e := setup(t)
	e.set("commit_update_keywords", []any{map[string]any{"keywords": "fixes", "status_id": "5", "done_ratio": "100"}})
	e.set("commit_logtime_enabled", "1")
	e.set("default_language", "en")
	r := &domain.Repository{ProjectID: 1, SCM: "git", URL: dir + "/.git", IsDefault: false, CreatedOn: frozenNow}
	r.Identifier = "fixes"
	e.must(repository.InsertScmRepository(e.ctx, e.d, r))
	e.must(e.svc.Fetch(e.ctx, r, nil))
	if st, dr := e.issueStatus(1); st != 5 || dr != 100 {
		t.Errorf("issue 1 = %d %d", st, dr)
	}
	var notes string
	e.must(e.d.Get(e.ctx, &notes, `SELECT notes FROM issue_journals WHERE issue_id = 1 ORDER BY id DESC LIMIT 1`))
	if !strings.HasPrefix(notes, "Applied in changeset commit:fixes|") {
		t.Errorf("notes = %q", notes)
	}
	var hours float64
	e.must(e.d.Get(e.ctx, &hours, `SELECT hours FROM time_entries WHERE issue_id = 1 ORDER BY id DESC LIMIT 1`))
	if hours != 2 {
		t.Errorf("hours = %v", hours)
	}
}

// ---------------------------------------------------------------- changeset_test.rb

// newChangeset は Changeset.new(repository: Project.find(1).repository, ...).save（作成 + scan_for_issues）。
func (e *env) newChangeset(revision, comments string, committedOn time.Time, userID *int64) *domain.Changeset {
	e.t.Helper()
	r, err := repository.GetScmRepository(e.ctx, e.d, 10)
	e.must(err)
	cd := time.Date(committedOn.Year(), committedOn.Month(), committedOn.Day(), 0, 0, 0, 0, time.UTC)
	c := &domain.Changeset{RepositoryID: r.ID, Revision: revision, Comments: comments, CommittedOn: committedOn, CommitDate: &cd,
		UserID: userID, RepositorySCM: r.SCM}
	e.must(e.d.WithTx(e.ctx, func(tx *db.Tx) error {
		if err := repository.InsertChangeset(e.ctx, tx, c); err != nil {
			return err
		}
		_, err := e.svc.ScanCommentForIssueIDs(e.ctx, tx, r, c, nil)
		return err
	}))
	return c
}

func (e *env) issueIDs(c *domain.Changeset) []int64 {
	ids, err := repository.ChangesetIssueIDs(e.ctx, e.d, c.ID)
	e.must(err)
	return ids
}

func (e *env) set(name string, v any) { e.must(e.st.Set(e.ctx, name, v)) }

func (e *env) issueStatus(id int64) (int64, int) {
	var r struct {
		StatusID  int64 `db:"status_id"`
		DoneRatio int   `db:"done_ratio"`
	}
	e.must(e.d.Get(e.ctx, &r, `SELECT status_id, done_ratio FROM issues WHERE id = ?`, id))
	return r.StatusID, r.DoneRatio
}

func TestRefKeywordsAny(t *testing.T) {
	e := setup(t)
	e.set("commit_ref_keywords", "*")
	e.set("commit_update_keywords", []any{map[string]any{"keywords": "fixes , closes", "status_id": "5", "done_ratio": "90"}})
	c := e.newChangeset("12345", "New commit (#2). Fixes #1", frozenNow, nil)
	if got := e.issueIDs(c); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("issue_ids = %v", got)
	}
	if st, dr := e.issueStatus(1); st != 5 || dr != 90 {
		t.Errorf("issue 1 = %d %d", st, dr)
	}
}

func TestRefKeywords(t *testing.T) {
	e := setup(t)
	e.set("commit_ref_keywords", "refs")
	e.set("commit_update_keywords", "")
	c := e.newChangeset("12345", "Ignores #2. Refs #1", frozenNow, nil)
	if got := e.issueIDs(c); !slices.Equal(got, []int64{1}) {
		t.Errorf("issue_ids = %v", got)
	}
}

func TestRefKeywordsVariants(t *testing.T) {
	for _, tt := range []struct {
		comments string
		want     []int64
	}{
		{"Ignores #2. Refs #1", []int64{1, 2}},
		{"#1 is the reason of this commit", []int64{1}},
		{"[#1] Worked on this issue", []int64{1}},
		{"[#1 #2, #3] Worked on these", []int64{1, 2, 3}},
		{"Out of range #2010021810000121", nil},
		{"refs #5, a subproject issue", []int64{5}},
	} {
		e := setup(t)
		e.set("commit_ref_keywords", "*")
		c := e.newChangeset("12345", tt.comments, frozenNow, nil)
		if got := e.issueIDs(c); !slices.Equal(got, tt.want) {
			t.Errorf("%q: issue_ids = %v", tt.comments, got)
		}
	}
}

func TestRefKeywordsAnyWithTimelog(t *testing.T) {
	e := setup(t)
	e.set("commit_ref_keywords", "*")
	e.set("commit_logtime_enabled", "1")
	e.set("commit_logtime_activity_id", "9")
	uid := int64(2)
	for syntax, want := range map[string]float64{"2": 2, "2h": 2, "2hours": 2, "15m": 0.25, "15min": 0.25, "3h15": 3.25,
		"3h15m": 3.25, "3h15min": 3.25, "3:15": 3.25, "3.25": 3.25, "3.25h": 3.25, "3,25": 3.25, "3,25h": 3.25} {
		_, err := e.d.Exec(e.ctx, `DELETE FROM changesets WHERE repository_id = 10 AND revision = '520'`)
		e.must(err)
		c := e.newChangeset("520", "Worked on this issue #1 @"+syntax, frozenNow.Add(-24*time.Hour), &uid)
		if got := e.issueIDs(c); !slices.Equal(got, []int64{1}) {
			t.Errorf("issue_ids = %v", got)
		}
		var te struct {
			IssueID    int64   `db:"issue_id"`
			ProjectID  int64   `db:"project_id"`
			UserID     int64   `db:"user_id"`
			Hours      float64 `db:"hours"`
			ActivityID int64   `db:"activity_id"`
			Comments   string  `db:"comments"`
			SpentOn    db.Date `db:"spent_on"`
		}
		e.must(e.d.Get(e.ctx, &te, `SELECT issue_id, project_id, user_id, hours, activity_id, comments, spent_on FROM time_entries ORDER BY id DESC LIMIT 1`))
		if te.IssueID != 1 || te.ProjectID != 1 || te.UserID != 2 || te.Hours != want || te.ActivityID != 9 ||
			te.SpentOn.Format("2006-01-02") != "2026-01-14" || !contains(te.Comments, "r520") {
			t.Errorf("@%s: time entry = %+v", syntax, te)
		}
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestCommitClosingSubprojectIssue(t *testing.T) {
	e := setup(t)
	e.set("commit_update_keywords", []any{map[string]any{"keywords": "closes", "status_id": "5"}})
	e.set("default_language", "en")
	c := e.newChangeset("12345", "closes #5, a subproject issue", frozenNow, nil)
	if got := e.issueIDs(c); !slices.Equal(got, []int64{5}) {
		t.Errorf("issue_ids = %v", got)
	}
	if st, _ := e.issueStatus(5); st != 5 {
		t.Errorf("status = %d", st)
	}
	var notes string
	e.must(e.d.Get(e.ctx, &notes, `SELECT notes FROM issue_journals WHERE issue_id = 5 ORDER BY id DESC LIMIT 1`))
	if !contains(notes, "Applied in changeset ecookbook:r12345.") {
		t.Errorf("notes = %q", notes)
	}
}

func TestUpdateKeywordsWithMultipleRulesForTheSameKeywordShouldMatchTracker(t *testing.T) {
	e := setup(t)
	e.set("commit_update_keywords", []any{
		map[string]any{"keywords": "fixes", "status_id": "5", "if_tracker_id": "2"},
		map[string]any{"keywords": "fixes", "status_id": "3", "if_tracker_id": ""},
	})
	// issue 1 は tracker 1、issue 2 は tracker 2（fixtures）
	e.newChangeset("12345", "Fixes #2, #1", frozenNow, nil)
	if st, _ := e.issueStatus(2); st != 5 {
		t.Errorf("issue 2 status = %d", st)
	}
	if st, _ := e.issueStatus(1); st != 3 {
		t.Errorf("issue 1 status = %d", st)
	}
}

func TestOldCommitDoesNotUpdateIssue(t *testing.T) {
	e := setup(t)
	e.set("commit_update_keywords", []any{map[string]any{"keywords": "fixes", "status_id": "5"}})
	// リポジトリ作成（2006-07-19）より前のコミットは参照のみ
	c := e.newChangeset("12345", "Fixes #1", time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC), nil)
	if got := e.issueIDs(c); !slices.Equal(got, []int64{1}) {
		t.Errorf("issue_ids = %v", got)
	}
	if st, _ := e.issueStatus(1); st != 1 {
		t.Errorf("status = %d", st)
	}
}
