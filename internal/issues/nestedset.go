// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
)

// Redmine の lft/rgt (Redmine::NestedSet::IssueNestedSet) の代わりに root_id + hier_path で階層を表す。
// 兄弟の並び (lft 順) は Redmine でも id 順になるため、hier_path の辞書順 = lft 順。

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// hierPathFor は親の経路と id から hier_path を作る。
func hierPathFor(parentPath string, id int64) string {
	return parentPath + fmt.Sprintf("%010d/", id)
}

// dbPath は DB 上の (root_id, hier_path) を返す (新規なら ok = false)。
func (e *Env) dbPath(ctx context.Context, id int64) (root int64, path string, ok bool, err error) {
	if id == 0 {
		return 0, "", false, nil
	}
	err = e.Q.QueryRow(ctx, `SELECT root_id, hier_path FROM issues WHERE id = ?`, id).Scan(&root, &path)
	if isNoRows(err) {
		return 0, "", false, nil
	}
	return root, path, err == nil, err
}

// Leaf は leaf? (子が無い。新規は常に true)。
func (e *Env) Leaf(ctx context.Context, iss *Issue) (bool, error) {
	if iss.NewRecord() {
		return true, nil
	}
	var n int
	if err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM issues WHERE parent_id = ?`, iss.ID); err != nil {
		return false, err
	}
	return n == 0, nil
}

// ancestorIDs は ancestors の id (ルートから親の順)。
func (e *Env) ancestorIDs(ctx context.Context, iss *Issue) ([]int64, error) {
	_, path, ok, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok {
		return nil, err
	}
	return pathIDs(path, iss.ID), nil
}

// pathIDs は hier_path に含まれる self 以外の id。
func pathIDs(path string, self int64) []int64 {
	var out []int64
	for _, s := range strings.Split(strings.TrimSuffix(path, "/"), "/") {
		id, err := strconv.ParseInt(s, 10, 64)
		if err == nil && id != self {
			out = append(out, id)
		}
	}
	return out
}

// Ancestors は ancestors (ルートから順)。
func (e *Env) Ancestors(ctx context.Context, iss *Issue) ([]*Issue, error) {
	ids, err := e.ancestorIDs(ctx, iss)
	if err != nil {
		return nil, err
	}
	var out []*Issue
	for _, id := range ids {
		a, err := e.Find(ctx, id)
		if err != nil {
			return nil, err
		}
		if a != nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// DescendantIDs は descendants の id (lft 順)。
func (e *Env) DescendantIDs(ctx context.Context, iss *Issue) ([]int64, error) {
	root, path, ok, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok {
		return nil, err
	}
	var ids []int64
	err = e.Q.Select(ctx, &ids, `SELECT id FROM issues WHERE root_id = ? AND hier_path LIKE ? AND id <> ? ORDER BY hier_path`, root, path+"%", iss.ID)
	return ids, err
}

// SelfAndDescendantIDs は self_and_descendants の id (lft 順)。
func (e *Env) SelfAndDescendantIDs(ctx context.Context, iss *Issue) ([]int64, error) {
	ids, err := e.DescendantIDs(ctx, iss)
	if err != nil {
		return nil, err
	}
	return append([]int64{iss.ID}, ids...), nil
}

// Descendants は descendants (lft 順)。
func (e *Env) Descendants(ctx context.Context, iss *Issue) ([]*Issue, error) {
	root, path, ok, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok {
		return nil, err
	}
	is, err := e.LoadMany(ctx, `root_id = ? AND hier_path LIKE ? AND id <> ?`, root, path+"%", iss.ID)
	if err != nil {
		return nil, err
	}
	sortByHierPath(is)
	return is, nil
}

// Children は children (lft 順)。
func (e *Env) Children(ctx context.Context, iss *Issue) ([]*Issue, error) {
	if iss.ID == 0 {
		return nil, nil
	}
	is, err := e.LoadMany(ctx, `parent_id = ?`, iss.ID)
	if err != nil {
		return nil, err
	}
	sortByHierPath(is)
	return is, nil
}

// Leaves は leaves (子を持たない子孫、lft 順)。
func (e *Env) Leaves(ctx context.Context, iss *Issue) ([]*Issue, error) {
	root, path, ok, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok {
		return nil, err
	}
	is, err := e.LoadMany(ctx, `root_id = ? AND hier_path LIKE ? AND id <> ? AND NOT EXISTS (SELECT 1 FROM issues c WHERE c.parent_id = issues.id)`,
		root, path+"%", iss.ID)
	if err != nil {
		return nil, err
	}
	sortByHierPath(is)
	return is, nil
}

func sortByHierPath(is []*Issue) {
	for i := 1; i < len(is); i++ {
		for j := i; j > 0 && is[j].HierPath < is[j-1].HierPath; j-- {
			is[j], is[j-1] = is[j-1], is[j]
		}
	}
}

// IsAncestorOf は is_ancestor_of?(other) (DB 上の階層で判定)。
func (e *Env) IsAncestorOf(ctx context.Context, iss, other *Issue) (bool, error) {
	return e.IsDescendantOf(ctx, other, iss)
}

// IsDescendantOf は is_descendant_of?(other)。
func (e *Env) IsDescendantOf(ctx context.Context, iss, other *Issue) (bool, error) {
	if iss.ID == 0 || other.ID == 0 || iss.ID == other.ID {
		return false, nil
	}
	r1, p1, ok1, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok1 {
		return false, err
	}
	r2, p2, ok2, err := e.dbPath(ctx, other.ID)
	if err != nil || !ok2 {
		return false, err
	}
	return r1 == r2 && strings.HasPrefix(p1, p2) && p1 != p2, nil
}

// IsOrIsAncestorOf は is_or_is_ancestor_of?(other)。
func (e *Env) IsOrIsAncestorOf(ctx context.Context, iss, other *Issue) (bool, error) {
	if iss.ID != 0 && iss.ID == other.ID {
		return true, nil
	}
	return e.IsAncestorOf(ctx, iss, other)
}

// Root は root (ルートのチケット)。
func (e *Env) Root(ctx context.Context, iss *Issue) (*Issue, error) {
	if iss.ParentID == nil {
		return iss, nil
	}
	root, _, ok, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok {
		return nil, err
	}
	return e.Find(ctx, root)
}

// lockHierarchy は lock_nested_set: 親の変更（既存チケット）・親を指定した作成の前に、関係する木
// （自身の木と新しい親の木）のルートの行をロックする（PostgreSQL の SELECT ... FOR UPDATE）。
// 親の検証（自身や子孫を親にしない）より前に呼ぶこと。ロックしないと、2 つのチケットを互いの親にする
// 更新が並行したとき、どちらの検証も相手の変更前の hier_path を読んで通り、parent_id が循環した
// （以後の保存で親の再計算が無限に再帰する）。木がロック待ちの間に別の木へ移った場合は、
// 移動先のルートも取り直してロックする。SQLite は書き込みトランザクションが直列化されるので何もしない。
func (e *Env) lockHierarchy(ctx context.Context, iss *Issue) error {
	if db.ForUpdate(e.Q) == "" {
		return nil
	}
	newParent := iss.parentIssueIDValue()
	if iss.NewRecord() {
		if newParent == nil {
			return nil
		}
	} else if prev := iss.orig0().ParentID; ptrEqInt64(prev, newParent) {
		return nil
	}
	locked := map[int64]bool{}
	for range 5 {
		var need []int64
		for _, id := range []int64{iss.ID, derefInt64(newParent)} {
			root, _, ok, err := e.dbPath(ctx, id)
			if err != nil {
				return err
			}
			if ok && !locked[root] && !slices.Contains(need, root) {
				need = append(need, root)
			}
		}
		if len(need) == 0 {
			return nil
		}
		query, args, err := db.In(`SELECT id FROM issues WHERE id IN (?) ORDER BY id`+db.ForUpdate(e.Q), need)
		if err != nil {
			return err
		}
		var got []int64
		if err := e.Q.Select(ctx, &got, query, args...); err != nil {
			return err
		}
		for _, id := range need {
			locked[id] = true
		}
	}
	return nil
}

func ptrEqInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// moveSubtree は handle_parent_change: 既存チケットの親変更に伴い、部分木の root_id / hier_path を付け替える。
func (e *Env) moveSubtree(ctx context.Context, iss *Issue, newParentID *int64) error {
	oldRoot, oldPath, ok, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok {
		return err
	}
	newRoot, newPath := iss.ID, hierPathFor("", iss.ID)
	if newParentID != nil {
		pr, pp, ok, err := e.dbPath(ctx, *newParentID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("issues: parent issue %d not found", *newParentID)
		}
		newRoot, newPath = pr, hierPathFor(pp, iss.ID)
	}
	if _, err := e.Q.Exec(ctx, `UPDATE issues SET root_id = ?, hier_path = CAST(? AS TEXT) || substr(hier_path, CAST(? AS INTEGER))
WHERE root_id = ? AND hier_path LIKE ?`, newRoot, newPath, len(oldPath)+1, oldRoot, oldPath+"%"); err != nil {
		return err
	}
	iss.RootID, iss.HierPath = newRoot, newPath
	return nil
}
