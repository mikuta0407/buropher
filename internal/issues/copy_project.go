package issues

import (
	"context"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// ProjectCopyResult は CopyProjectIssues の結果。
type ProjectCopyResult struct {
	// Notifications はコミット後に配送する通知（コピーしたチケットの issue_add 等）。
	Notifications []Notification
	// Copied はコピー元のチケット id → コピー先のチケット id。
	Copied map[int64]int64
	// Failed は検証エラーでコピーできなかったコピー元のチケット id（Redmine はログに残すだけ）。
	Failed []int64
}

// CopyProjectIssues は Project#copy_issues(project): コピー元プロジェクト src のチケットをコピー先 dst へ複製する。
//
//   - コピー先の閉じた・ロックされたバージョンを一時的に open にしてから複製し、最後に元の状態へ戻す
//   - チケットは root_id, lft 順（親が子より先）に copy_from(subtasks: false, link: false, keep_status: true)
//   - 対象バージョン・カテゴリは名前で付け替える（バージョン形式のカスタムフィールドの値も同様）
//   - 親チケットはコピー済みのものへ付け替える
//   - 関連は全チケットのコピー後に、コピー先同士（設定でプロジェクト間の関連を許可していればコピー元の相手とも）で作る
//
// e.Q はコピー先プロジェクトを作成したトランザクションであること。
func (e *Env) CopyProjectIssues(ctx context.Context, srcID, dstID int64) (*ProjectCopyResult, error) {
	out := &ProjectCopyResult{Copied: map[int64]int64{}}
	dst, err := e.Project(ctx, dstID)
	if err != nil {
		return nil, err
	}
	if dst == nil {
		return nil, ErrNotFound
	}
	now := e.now()

	// Store status and reopen locked/closed versions（version.update_attribute :status, 'open'）
	var reopened []struct {
		ID     int64  `db:"id"`
		Status string `db:"status"`
	}
	if err := e.Q.Select(ctx, &reopened, `SELECT id, status FROM versions WHERE project_id = ? AND status <> ? ORDER BY id`,
		dstID, domain.VersionStatusOpen); err != nil {
		return nil, err
	}
	for _, v := range reopened {
		if _, err := e.Q.Exec(ctx, `UPDATE versions SET status = ?, updated_at = ? WHERE id = ?`, domain.VersionStatusOpen, db.NewTime(now), v.ID); err != nil {
			return nil, err
		}
	}

	var dstVersions []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := e.Q.Select(ctx, &dstVersions, `SELECT id, name FROM versions WHERE project_id = ? ORDER BY id`, dstID); err != nil {
		return nil, err
	}
	// versionByName は self.versions.detect {|v| v.name == name}
	versionByName := func(name string) *int64 {
		for _, v := range dstVersions {
			if v.Name == name {
				id := v.ID
				return &id
			}
		}
		return nil
	}

	var srcIDs []int64
	if err := e.Q.Select(ctx, &srcIDs, `SELECT id FROM issues WHERE project_id = ? ORDER BY root_id, hier_path`, srcID); err != nil {
		return nil, err
	}
	copied := map[int64]*Issue{}
	for _, id := range srcIDs {
		src, err := e.Load(ctx, id)
		if err != nil {
			return nil, err
		}
		iss, err := e.NewBlank(ctx)
		if err != nil {
			return nil, err
		}
		if err := e.CopyFrom(ctx, iss, src, CopyOptions{NoSubtasks: true, NoLink: true, KeepStatus: true}); err != nil {
			return nil, err
		}
		if err := e.SetProject(ctx, iss, dst, false); err != nil {
			return nil, err
		}
		// Changing project resets the custom field values
		srcValues, err := e.CustomFieldValues(ctx, src)
		if err != nil {
			return nil, err
		}
		h := map[string]any{}
		for _, v := range srcValues {
			h[itoa(v.Field.ID)] = v.Value
		}
		if err := e.SetCustomFieldValues(ctx, iss, h); err != nil {
			return nil, err
		}
		// Reassign fixed_versions by name, since names are unique per project
		if fv, err := e.Version(ctx, src.FixedVersionID); err != nil {
			return nil, err
		} else if fv != nil && fv.ProjectID == srcID {
			iss.FixedVersionID = versionByName(fv.Name)
		}
		// Reassign version custom field values
		values, err := e.CustomFieldValues(ctx, iss)
		if err != nil {
			return nil, err
		}
		for _, cv := range values {
			if cv.Field.FieldFormat != "version" || !cv.Value.Present() {
				continue
			}
			var vers []struct {
				ID        int64  `db:"id"`
				ProjectID int64  `db:"project_id"`
				Name      string `db:"name"`
			}
			query, args, err := db.In(`SELECT id, project_id, name FROM versions WHERE id IN (?) ORDER BY id`, versionIDs(cv.Value.Strings()))
			if err != nil {
				return nil, err
			}
			if err := e.Q.Select(ctx, &vers, query, args...); err != nil {
				return nil, err
			}
			var nv []string
			for _, v := range vers {
				if v.ProjectID == srcID {
					if id := versionByName(v.Name); id != nil {
						nv = append(nv, strconv.FormatInt(*id, 10))
					}
				} else {
					nv = append(nv, strconv.FormatInt(v.ID, 10))
				}
			}
			if cv.Field.Multiple {
				cv.Value = castCustomFieldInput(cv.Field, nv)
			} else if len(nv) > 0 {
				cv.Value = StrValue(nv[0])
			} else {
				cv.Value = StrValue("")
			}
			iss.cfvChanged = true
		}
		// Reassign the category by name, since names are unique per project
		if cat, err := e.Category(ctx, src.CategoryID); err != nil {
			return nil, err
		} else if cat != nil {
			iss.CategoryID = nil
			var cid []int64
			if err := e.Q.Select(ctx, &cid, `SELECT id FROM issue_categories WHERE project_id = ? AND name = ? ORDER BY id LIMIT 1`, dstID, cat.Name); err != nil {
				return nil, err
			}
			if len(cid) > 0 {
				iss.CategoryID = &cid[0]
			}
		}
		// Parent issue
		if src.ParentID != nil {
			if p := copied[*src.ParentID]; p != nil {
				if err := e.SetParentIssueID(ctx, iss, strconv.FormatInt(p.ID, 10)); err != nil {
					return nil, err
				}
			}
		}
		ok, res, err := e.Save(ctx, iss)
		if err != nil {
			return nil, err
		}
		if !ok {
			out.Failed = append(out.Failed, src.ID)
			continue
		}
		out.Notifications = append(out.Notifications, res.Notifications...)
		copied[src.ID] = iss
		out.Copied[src.ID] = iss.ID
	}

	// Restore locked/closed version statuses
	for _, v := range reopened {
		if _, err := e.Q.Exec(ctx, `UPDATE versions SET status = ?, updated_at = ? WHERE id = ?`, v.Status, db.NewTime(now), v.ID); err != nil {
			return nil, err
		}
	}

	// Relations after in case issues related each other
	cross := e.Settings != nil && e.Settings.Bool("cross_project_issue_relations")
	var allIDs []int64
	if err := e.Q.Select(ctx, &allIDs, `SELECT id FROM issues WHERE project_id = ? ORDER BY id`, srcID); err != nil {
		return nil, err
	}
	// other は関連の相手（コピー済みならコピー、プロジェクト間の関連を許可していればコピー元の相手）
	other := func(id int64) (*Issue, error) {
		if c := copied[id]; c != nil {
			return c, nil
		}
		if !cross {
			return nil, nil
		}
		return e.Find(ctx, id)
	}
	for _, id := range allIDs {
		iss := copied[id]
		if iss == nil {
			// Issue was not copied
			continue
		}
		var rels []struct {
			IssueFromID  int64  `db:"issue_from_id"`
			IssueToID    int64  `db:"issue_to_id"`
			RelationType string `db:"relation_type"`
			Delay        *int   `db:"delay"`
		}
		if err := e.Q.Select(ctx, &rels, `SELECT issue_from_id, issue_to_id, relation_type, delay FROM issue_relations
WHERE issue_from_id = ? OR issue_to_id = ? ORDER BY CASE WHEN issue_from_id = ? THEN 0 ELSE 1 END, id`, id, id, id); err != nil {
			return nil, err
		}
		for _, sr := range rels {
			r := &Relation{}
			r.RelationType = sr.RelationType
			if sr.Delay != nil {
				d := *sr.Delay
				r.Delay = &d
			}
			if sr.IssueFromID == id {
				// relations_from
				to, err := other(sr.IssueToID)
				if err != nil {
					return nil, err
				}
				r.From, r.To = iss, to
			} else {
				// relations_to
				from, err := other(sr.IssueFromID)
				if err != nil {
					return nil, err
				}
				r.From, r.To = from, iss
			}
			if r.From == nil || r.To == nil {
				// issue_from / issue_to が空なので検証で保存されない
				continue
			}
			// 検証エラー（コピー先同士で既に作成済みの関連など）は Redmine と同じく保存されないだけ
			ok, res, err := e.CreateRelation(ctx, r)
			if err != nil {
				return nil, err
			}
			if ok {
				out.Notifications = append(out.Notifications, res.Notifications...)
			}
		}
	}
	return out, nil
}

// versionIDs は Version.where(:id => value) の id（数値でない値は一致しない）。
func versionIDs(vals []string) []int64 {
	ids := []int64{0}
	for _, s := range vals {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
