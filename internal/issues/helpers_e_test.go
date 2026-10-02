package issues

// issue_nested_set_test / issue_subtasking_test / issue_relation_test 移植用のヘルパ。

import (
	"fmt"
	"strings"
	"testing"
)

// generateChild は Issue#generate_child!(attrs) (parent_issue_id を付けて Issue.generate!)。
func (c *tc) generateChild(e *Env, parent *Issue, attrs Params) *Issue {
	c.t.Helper()
	p := Params{}
	for k, v := range attrs {
		p[k] = v
	}
	p["parent_issue_id"] = parent.ID
	return c.generateSaved(e, p)
}

// closeIssue は Issue#close! (最初のクローズ済みステータスにして save!)。
func (c *tc) closeIssue(e *Env, iss *Issue) {
	c.t.Helper()
	var sid int64
	c.must(c.d.Get(c.ctx, &sid, `SELECT id FROM issue_statuses WHERE is_closed = ? ORDER BY id LIMIT 1`, true))
	iss.StatusID = sid
	c.saveOK(e, iss)
}

// priorityID は IssuePriority.find_by_name(name).id。
func (c *tc) priorityID(name string) int64 {
	c.t.Helper()
	var id int64
	c.must(c.d.Get(c.ctx, &id, `SELECT id FROM issue_priorities WHERE name = ?`, name))
	return id
}

func (c *tc) priorityName(id int64) string {
	c.t.Helper()
	var n string
	c.must(c.d.Get(c.ctx, &n, `SELECT name FROM issue_priorities WHERE id = ?`, id))
	return n
}

func (c *tc) journalCount() int { return c.count(`SELECT COUNT(*) FROM issue_journals`) }
func (c *tc) detailCount() int  { return c.count(`SELECT COUNT(*) FROM issue_journal_details`) }
func (c *tc) issueCount() int   { return c.count(`SELECT COUNT(*) FROM issues`) }

// pathOf は id 列から hier_path を作る。
func pathOf(ids ...int64) string {
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "%010d/", id)
	}
	return b.String()
}

// assertNode は DB 上の (root_id, parent_id, hier_path) が経路 path (ルートから自身まで) と一致するか確認する
// (Redmine の [root_id, lft, rgt] の比較に相当)。
func (c *tc) assertNode(t testing.TB, id int64, path ...int64) {
	t.Helper()
	var r struct {
		Root   int64  `db:"root_id"`
		Parent *int64 `db:"parent_id"`
		Path   string `db:"hier_path"`
	}
	c.must(c.d.Get(c.ctx, &r, `SELECT root_id, parent_id, hier_path FROM issues WHERE id = ?`, id))
	var wantParent *int64
	if len(path) > 1 {
		wantParent = &path[len(path)-2]
	}
	if r.Root != path[0] || r.Path != pathOf(path...) || !eqPtr(r.Parent, wantParent) {
		t.Errorf("issue %d: root=%d parent=%v path=%s, want path %v", id, r.Root, r.Parent, r.Path, path)
	}
}

// subtreeOrder は root の部分木の id を lft 順 (hier_path 順) で返す。
func (c *tc) subtreeOrder(root int64) []int64 {
	c.t.Helper()
	return c.ids(`SELECT id FROM issues WHERE root_id = ? ORDER BY hier_path`, root)
}
