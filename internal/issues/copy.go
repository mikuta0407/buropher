// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"

	"github.com/mikuta0407/buropher/internal/domain"
)

// CopyOptions は copy_from のオプション (nil は Ruby の未指定 = true 扱い)。
type CopyOptions struct {
	// KeepStatus はステータスを引き継ぐ (keep_status)。
	KeepStatus bool
	// NoAttachments は添付ファイルをコピーしない (attachments: false)。
	NoAttachments bool
	// NoWatchers はウォッチャーをコピーしない (watchers: false)。
	NoWatchers bool
	// NoSubtasks は子チケットをコピーしない (subtasks: false)。
	NoSubtasks bool
	// NoLink は copied_to 関連を作らない (link: false)。
	NoLink bool
}

// CopyFrom は copy_from(issue, options): src の属性・カスタムフィールド値・添付・ウォッチャーを iss に複製する。
func (e *Env) CopyFrom(ctx context.Context, iss *Issue, src *Issue, opts CopyOptions) error {
	// project_id, tracker_id を先に (assign_attributes の順)
	p, err := e.ProjectOf(ctx, src)
	if err != nil {
		return err
	}
	if err := e.SetProject(ctx, iss, p, false); err != nil {
		return err
	}
	t, err := e.TrackerOf(ctx, src)
	if err != nil {
		return err
	}
	if err := e.SetTracker(ctx, iss, t); err != nil {
		return err
	}
	iss.Subject = src.Subject
	iss.SetDescription(src.Description)
	iss.SetDueDate(src.DueDate)
	iss.CategoryID = copyID(src.CategoryID)
	iss.AssignedToID = copyID(src.AssignedToID)
	iss.PriorityID = src.PriorityID
	iss.FixedVersionID = copyID(src.FixedVersionID)
	iss.AuthorID = src.AuthorID
	iss.LockVersion = src.LockVersion
	iss.SetStartDate(src.StartDate)
	iss.DoneRatio = src.DoneRatio
	if src.EstimatedHours != nil {
		h := *src.EstimatedHours
		iss.SetEstimatedHours(&h)
	} else {
		iss.SetEstimatedHours(nil)
	}
	iss.IsPrivate = src.IsPrivate

	srcValues, err := e.CustomFieldValues(ctx, src)
	if err != nil {
		return err
	}
	h := map[string]any{}
	for _, v := range srcValues {
		h[itoa(v.Field.ID)] = v.Value
	}
	if err := e.SetCustomFieldValues(ctx, iss, h); err != nil {
		return err
	}
	if opts.KeepStatus {
		iss.StatusID = src.StatusID
	}
	cur, err := e.currentUser(ctx)
	if err != nil {
		return err
	}
	iss.AuthorID = cur.ID
	if !opts.NoAttachments {
		if err := e.Q.Select(ctx, &iss.copiedAttachments, `SELECT id FROM attachments WHERE container_kind = 'issue' AND container_id = ?
ORDER BY created_at, id`, src.ID); err != nil {
			return err
		}
	}
	if !opts.NoWatchers {
		ws, err := e.VisibleWatcherUsers(ctx, src, cur)
		if err != nil {
			return err
		}
		var ids []int64
		for _, w := range ws {
			if w.Status == domain.StatusActive {
				ids = append(ids, w.ID)
			}
		}
		iss.SetWatcherUserIDs(ids)
	}
	iss.copiedFrom = src
	iss.copyOptions = opts
	return nil
}

func copyID(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func itoa(id int64) string { return formatInt(id) }

// Copy は copy(attributes, copy_options): コピーした未保存のチケットを返す。
func (e *Env) Copy(ctx context.Context, src *Issue, attrs Params, opts CopyOptions) (*Issue, error) {
	iss, err := e.NewBlank(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.CopyFrom(ctx, iss, src, opts); err != nil {
		return nil, err
	}
	if attrs != nil {
		if err := e.AssignAttributes(ctx, iss, attrs); err != nil {
			return nil, err
		}
	}
	return iss, nil
}

// saveCopiedAttachments はコピー元の添付ファイルを複製する (Attachment#copy、ジャーナルには記録しない)。
func (e *Env) saveCopiedAttachments(ctx context.Context, iss *Issue) error {
	for _, id := range iss.copiedAttachments {
		if _, err := e.Q.Exec(ctx, `INSERT INTO attachments (container_kind, container_id, filename, disk_directory, disk_filename,
  filesize, content_type, digest, digest_algo, downloads, author_id, description, created_at)
SELECT 'issue', ?, filename, disk_directory, disk_filename, filesize, content_type, digest, digest_algo, 0, author_id,
  description, created_at FROM attachments WHERE id = ?`, iss.ID, id); err != nil {
			return err
		}
	}
	iss.copiedAttachments = nil
	return nil
}

// afterCreateFromCopy は after_create_from_copy: copied_to 関連の作成と子チケットのコピー。
func (e *Env) afterCreateFromCopy(ctx context.Context, iss *Issue, st *saveState) error {
	if iss.copiedFrom == nil || iss.afterCreateFromCopyRun {
		return nil
	}
	src := iss.copiedFrom
	opts := iss.copyOptions
	cross := e.Settings != nil && e.Settings.Bool("cross_project_issue_relations")
	var journalUser *domain.User
	if iss.currentJournal != nil {
		u, err := e.UserByID(ctx, iss.currentJournal.UserID)
		if err != nil {
			return err
		}
		journalUser = u
	}
	if (src.ProjectID == iss.ProjectID || cross) && !opts.NoLink {
		if journalUser != nil {
			if _, err := e.InitJournal(ctx, src, journalUser, ""); err != nil {
				return err
			}
		}
		r := &Relation{From: src, To: iss}
		r.RelationType = domain.RelationCopiedTo
		// 検証エラーは Redmine と同じくログのみ (ここでは無視)
		if _, err := e.createRelation(ctx, r, st); err != nil {
			return err
		}
	}
	leaf, err := e.Leaf(ctx, src)
	if err != nil {
		return err
	}
	if !leaf && !opts.NoSubtasks {
		childOpts := opts
		childOpts.NoSubtasks = true
		copied := map[int64]int64{src.ID: iss.ID}
		if err := e.Reload(ctx, src); err != nil {
			return err
		}
		desc, err := e.Descendants(ctx, src)
		if err != nil {
			return err
		}
		cur, err := e.currentUser(ctx)
		if err != nil {
			return err
		}
		proj, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return err
		}
		for _, child := range desc {
			if child.ID == iss.ID {
				continue
			}
			if child.ParentID == nil {
				continue
			}
			newParent, ok := copied[*child.ParentID]
			if !ok {
				continue
			}
			if v, err := e.Visible(ctx, child, cur); err != nil {
				return err
			} else if !v {
				continue
			}
			c, err := e.NewBlank(ctx)
			if err != nil {
				return err
			}
			if err := e.CopyFrom(ctx, c, child, childOpts); err != nil {
				return err
			}
			if journalUser != nil {
				if _, err := e.InitJournal(ctx, c, journalUser, ""); err != nil {
					return err
				}
			}
			c.AuthorID = iss.AuthorID
			if err := e.SetProject(ctx, c, proj, false); err != nil {
				return err
			}
			np, err := e.Find(ctx, newParent)
			if err != nil {
				return err
			}
			c.SetParentIssue(np)
			fv, err := e.Version(ctx, child.FixedVersionID)
			if err != nil {
				return err
			}
			if fv == nil || fv.Status != domain.VersionStatusOpen {
				c.FixedVersionID = nil
			}
			keepAssignee := false
			if child.AssignedToID != nil {
				ap, err := e.Principal(ctx, *child.AssignedToID)
				if err != nil {
					return err
				}
				keepAssignee = ap != nil && ap.Status == domain.StatusActive
			}
			if !keepAssignee {
				c.AssignedToID = nil
			}
			ok, err = e.save(ctx, c, true, st)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			copied[child.ID] = c.ID
		}
	}
	iss.afterCreateFromCopyRun = true
	return nil
}
