package repository

import (
	"context"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// ---------------------------------------------------------------- 一括読み込み

// ProjectsByIDs は id のプロジェクトを id -> Project の map で返す (preload(:project) 等に使う)。
func ProjectsByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*domain.Project, error) {
	out := make(map[int64]*domain.Project, len(ids))
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`projects.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		ps, err := LoadProjects(ctx, q, query, args...)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			out[p.ID] = p
		}
	}
	return out, nil
}

// UserProjects は user.projects.active (アーカイブされていないメンバーシップのうち有効なプロジェクト)。
// 並びはツリー順。
func UserProjects(ctx context.Context, q db.Queryer, userID int64) ([]*domain.Project, error) {
	return LoadProjects(ctx, q, `projects.status = ? AND projects.id IN (SELECT project_id FROM members WHERE principal_id = ?)`,
		domain.ProjectStatusActive, userID)
}

// ---------------------------------------------------------------- ブックマーク・最近使ったプロジェクト
//
// Redmine は user.pref[:bookmarked_project_ids] / [:recently_used_project_ids] にカンマ区切りで
// 保存するが、buropher は user_project_bookmarks / user_recent_projects に position 付きで保存する。

// BookmarkedProjectIDs は bookmarked_project_ids (保存順)。
func BookmarkedProjectIDs(ctx context.Context, q db.Queryer, userID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT project_id FROM user_project_bookmarks WHERE user_id = ? ORDER BY position, project_id`, userID)
	return ids, err
}

// RecentProjectIDs は recently_used_project_ids (新しい順、件数制限なし)。
func RecentProjectIDs(ctx context.Context, q db.Queryer, userID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT project_id FROM user_recent_projects WHERE user_id = ? ORDER BY position, project_id`, userID)
	return ids, err
}

// setProjectIDs は table のユーザの行を ids (重複除去・順序維持) で置き換える。
// 変更が無ければ何もしない (set_pref_project_ids の old_value != new_value)。
func setProjectIDs(ctx context.Context, q db.Queryer, table string, userID int64, old, ids []int64) error {
	ids = uniqIDs(ids)
	if slices.Equal(old, ids) {
		return nil
	}
	if _, err := q.Exec(ctx, `DELETE FROM `+table+` WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := q.Exec(ctx, `INSERT INTO `+table+` (user_id, project_id, position) VALUES (?, ?, ?)`, userID, id, i); err != nil {
			return err
		}
	}
	return nil
}

// SetBookmarkedProjectIDs は bookmarked_project_ids= 。
func SetBookmarkedProjectIDs(ctx context.Context, q db.Queryer, userID int64, ids []int64) error {
	old, err := BookmarkedProjectIDs(ctx, q, userID)
	if err != nil {
		return err
	}
	return setProjectIDs(ctx, q, "user_project_bookmarks", userID, old, ids)
}

// SetRecentProjectIDs は recently_used_project_ids= 。
func SetRecentProjectIDs(ctx context.Context, q db.Queryer, userID int64, ids []int64) error {
	old, err := RecentProjectIDs(ctx, q, userID)
	if err != nil {
		return err
	}
	return setProjectIDs(ctx, q, "user_recent_projects", userID, old, ids)
}

// ProjectUsed は Redmine::ProjectJumpBox#project_used の移植。
// 最近使ったプロジェクトの先頭に projectID を移す (ブックマーク済みなら一覧から外すだけ)。
// limit は user.pref.recently_used_projects。
func ProjectUsed(ctx context.Context, q db.Queryer, userID, projectID int64, limit int) error {
	if userID == 0 || projectID == 0 {
		return nil
	}
	recent, err := RecentProjectIDs(ctx, q, userID)
	if err != nil {
		return err
	}
	recent = headIDs(recent, limit)
	bookmarks, err := BookmarkedProjectIDs(ctx, q, userID)
	if err != nil {
		return err
	}
	ids := slices.DeleteFunc(slices.Clone(recent), func(id int64) bool { return id == projectID })
	if !slices.Contains(bookmarks, projectID) {
		ids = append([]int64{projectID}, ids...)
	}
	return SetRecentProjectIDs(ctx, q, userID, headIDs(ids, limit))
}

// BookmarkProject は Redmine::ProjectJumpBox#bookmark_project の移植。
func BookmarkProject(ctx context.Context, q db.Queryer, userID, projectID int64) error {
	recent, err := RecentProjectIDs(ctx, q, userID)
	if err != nil {
		return err
	}
	if err := SetRecentProjectIDs(ctx, q, userID, slices.DeleteFunc(recent, func(id int64) bool { return id == projectID })); err != nil {
		return err
	}
	bookmarks, err := BookmarkedProjectIDs(ctx, q, userID)
	if err != nil {
		return err
	}
	return SetBookmarkedProjectIDs(ctx, q, userID, append(bookmarks, projectID))
}

// DeleteProjectBookmark は Redmine::ProjectJumpBox#delete_project_bookmark の移植。
func DeleteProjectBookmark(ctx context.Context, q db.Queryer, userID, projectID int64) error {
	bookmarks, err := BookmarkedProjectIDs(ctx, q, userID)
	if err != nil {
		return err
	}
	return SetBookmarkedProjectIDs(ctx, q, userID, slices.DeleteFunc(bookmarks, func(id int64) bool { return id == projectID }))
}

// headIDs は ids[0, n] (Ruby の Array#[0, n]。n < 0 は空)。
func headIDs(ids []int64, n int) []int64 {
	if n < 0 {
		return nil
	}
	if len(ids) > n {
		return ids[:n]
	}
	return ids
}

// ---------------------------------------------------------------- プロジェクトメニューの表示条件

// SharedVersionsCondition は Project#shared_versions の条件 (versions と projects を参照する SQL)。
// lft/rgt の比較は閉包テーブルで置き換える。
func SharedVersionsCondition(p *domain.Project) string {
	id := strconv.FormatInt(p.ID, 10)
	archived := strconv.Itoa(domain.ProjectStatusArchived)
	root := "(SELECT rc.ancestor_id FROM project_closure rc WHERE rc.descendant_id = " + id + " ORDER BY rc.depth DESC LIMIT 1)"
	return "(projects.id = " + id +
		" OR (projects.status <> " + archived + " AND (" +
		" versions.sharing = 'system'" +
		" OR (projects.id IN (SELECT tc.descendant_id FROM project_closure tc WHERE tc.ancestor_id = " + root + ")" +
		" AND versions.sharing = 'tree')" +
		" OR (projects.id IN (SELECT ac.ancestor_id FROM project_closure ac WHERE ac.descendant_id = " + id + " AND ac.depth > 0)" +
		" AND versions.sharing IN ('hierarchy', 'descendants'))" +
		" OR (projects.id IN (SELECT dc.descendant_id FROM project_closure dc WHERE dc.ancestor_id = " + id + " AND dc.depth > 0)" +
		" AND versions.sharing = 'hierarchy'))))"
}

// RolledUpVersionsCondition は Project#rolled_up_versions の条件 (自身と子孫の、アーカイブされていないプロジェクトのバージョン)。
func RolledUpVersionsCondition(p *domain.Project) string {
	return "(projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + strconv.FormatInt(p.ID, 10) + ")" +
		" AND projects.status <> " + strconv.Itoa(domain.ProjectStatusArchived) + ")"
}

func exists(ctx context.Context, q db.Queryer, query string, args ...any) (bool, error) {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM (`+query+` LIMIT 1) x`, args...); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ProjectHasSharedVersions は project.shared_versions.any?。
func ProjectHasSharedVersions(ctx context.Context, q db.Queryer, p *domain.Project) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM versions JOIN projects ON projects.id = versions.project_id WHERE `+SharedVersionsCondition(p))
}

// ProjectHasRolledUpVersions は project.rolled_up_versions.any?。
func ProjectHasRolledUpVersions(ctx context.Context, q db.Queryer, p *domain.Project) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM versions JOIN projects ON projects.id = versions.project_id WHERE `+RolledUpVersionsCondition(p))
}

// ProjectHasWiki は project.wiki && !project.wiki.new_record?。
func ProjectHasWiki(ctx context.Context, q db.Queryer, projectID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM wikis WHERE project_id = ?`, projectID)
}

// ProjectHasBoards は project.boards.any?。
func ProjectHasBoards(ctx context.Context, q db.Queryer, projectID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM boards WHERE project_id = ?`, projectID)
}

// ProjectHasRepositories は project.repositories.exists?。
func ProjectHasRepositories(ctx context.Context, q db.Queryer, projectID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM repositories WHERE project_id = ?`, projectID)
}

// ProjectTrackerIDs は project.trackers.sorted の id (trackers.position 順)。
func ProjectTrackerIDs(ctx context.Context, q db.Queryer, projectID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT t.id FROM trackers t JOIN project_trackers pt ON pt.tracker_id = t.id
WHERE pt.project_id = ? ORDER BY t.position, t.id`, projectID)
	return ids, err
}
