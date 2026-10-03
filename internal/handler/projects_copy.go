// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"slices"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは projects#copy（GET: Project.copy_from によるフォーム、POST: Project#copy）。

// projectCopyItems は Project#copy の to_be_copied（順序も Redmine と同じ）。
var projectCopyItems = []string{"members", "wiki", "versions", "issue_categories", "issues", "queries", "boards", "documents"}

// ProjectsCopy は projects#copy（GET / POST /projects/:id/copy）。
func (a *App) ProjectsCopy(c *Req) {
	ctx := c.Ctx()
	src, err := repository.FindProject(ctx, a.DB, c.Params().String("id"))
	if errors.Is(err, repository.ErrNotFound) {
		c.Render404("")
		return
	} else if err != nil {
		a.internalError(c, "find source project", err)
		return
	}
	var f *projectForm
	if c.R.Method == "GET" {
		f, err = a.copyFromProject(c, src)
		if err != nil {
			a.internalError(c, "copy from", err)
			return
		}
	} else {
		// Mailer.with_deliveries(params[:notifications] == '1'): '1' のときだけコピーで作られたチケット・
		// Wiki ページ・文書の通知をコミット後に配送する
		deliver := c.Params().String("notifications") == "1"
		f, err = a.newProjectForm(c)
		if err != nil {
			a.internalError(c, "new project", err)
			return
		}
		if err := a.assignProject(c, f, c.Params().Map("project")); err != nil {
			a.internalError(c, "assign project", err)
			return
		}
		if err := a.validateProject(c, f); err != nil {
			a.internalError(c, "validate project", err)
			return
		}
		if !f.errs.Any() {
			only := projectCopyItems
			if c.Params().Has("only") {
				var sel []string
				for _, s := range c.Params().Strings("only") {
					if slices.Contains(projectCopyItems, s) {
						sel = append(sel, s)
					}
				}
				var ordered []string
				for _, it := range projectCopyItems {
					if slices.Contains(sel, it) {
						ordered = append(ordered, it)
					}
				}
				only = ordered
			}
			var res *issues.SaveResult
			err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
				if err := a.saveProject(c, f, tx); err != nil {
					return err
				}
				var err error
				res, err = a.copyProjectItems(c, tx, src, f.Project.ID, only)
				return err
			})
			if errors.Is(err, repository.ErrInvalidParent) {
				f.errs.Add("parent_id", "invalid", nil)
			} else if err != nil {
				a.internalError(c, "copy project", err)
				return
			} else {
				if deliver {
					a.dispatchIssueNotifications(c, res)
					a.notifyCopiedContents(c, f.Project.ID, only)
				}
				c.ResetAuthz()
				if np, err := repository.GetProject(ctx, a.DB, f.Project.ID); err == nil {
					c.Project = np
				}
				c.Flash().SetNotice(c.L("notice_successful_create"))
				c.Redirect("/projects/" + f.Project.Identifier + "/settings")
				return
			}
		}
	}
	a.renderProjectCopy(c, f, src)
}

// copyFromProject は Project.copy_from(project)（id / name / identifier / status / parent を除く属性・
// モジュール・トラッカー・カスタム値・チケットのカスタムフィールド）。
func (a *App) copyFromProject(c *Req, src *domain.Project) (*projectForm, error) {
	ctx := c.Ctx()
	f, err := a.newProjectForm(c)
	if err != nil {
		return nil, err
	}
	p := f.Project
	p.Description, p.Homepage, p.IsPublic, p.InheritMembers = src.Description, src.Homepage, src.IsPublic, src.InheritMembers
	p.DefaultVersionID, p.DefaultAssignedToID, p.DefaultIssueQueryID = src.DefaultVersionID, src.DefaultAssignedToID, src.DefaultIssueQueryID
	p.EnabledModuleNames = slices.Clone(src.EnabledModuleNames)
	// identifier は Project.new の既定（sequential_project_identifiers なら次の識別子）
	if f.TrackerIDs, err = repository.ProjectTrackerIDs(ctx, a.DB, src.ID); err != nil {
		return nil, err
	}
	if f.IssueCustomFieldIDs, err = repository.ProjectIssueCustomFieldIDs(ctx, a.DB, src.ID); err != nil {
		return nil, err
	}
	srcVals, err := repository.CustomValues(ctx, a.DB, "project", src.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range f.CFValues {
		if vals, ok := srcVals[v.Field.ID]; ok {
			v.Values = slices.Clone(vals)
		}
	}
	f.cfChanged = true
	return f, nil
}

// copyProjectItems は Project#copy の copy_* を順に実行する。返り値はコピーしたチケットの通知。
func (a *App) copyProjectItems(c *Req, tx *db.Tx, src *domain.Project, dstID int64, only []string) (*issues.SaveResult, error) {
	ctx := c.Ctx()
	res := &issues.SaveResult{}
	for _, name := range only {
		var err error
		switch name {
		case "members":
			err = repository.CopyProjectMembers(ctx, tx, src.ID, dstID)
		case "wiki":
			err = repository.CopyProjectWiki(ctx, tx, src.ID, dstID)
		case "versions":
			_, err = repository.CopyProjectVersions(ctx, tx, src.ID, dstID)
		case "issue_categories":
			err = repository.CopyProjectIssueCategories(ctx, tx, src.ID, dstID)
		case "issues":
			// メンバー・バージョン・カテゴリのコピー後の状態で判定するため、ここで Env を作る
			var r *issues.ProjectCopyResult
			r, err = a.writeIssuesEnv(c, tx).CopyProjectIssues(ctx, src.ID, dstID)
			if err == nil {
				res.Notifications = append(res.Notifications, r.Notifications...)
				for _, id := range r.Failed {
					a.logger().Info("Project#copy_issues: issue could not be copied", "issue_id", id)
				}
			}
		case "queries":
			err = repository.CopyProjectQueries(ctx, tx, src, dstID)
		case "boards":
			err = repository.CopyProjectBoards(ctx, tx, src.ID, dstID)
		case "documents":
			err = repository.CopyProjectDocuments(ctx, tx, src.ID, dstID)
		}
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

// notifyCopiedContents はコピーで作られた Wiki ページ（WikiContent#send_notification_create）と
// 文書（Document#send_notification）の通知を配送する（コミット後。イベントが無効なら何もしない）。
func (a *App) notifyCopiedContents(c *Req, dstID int64, only []string) {
	ctx := c.Ctx()
	if slices.Contains(only, "wiki") && a.Notify != nil {
		var pages []struct {
			ID      int64 `db:"id"`
			Version int   `db:"current_version"`
		}
		if err := a.DB.Select(ctx, &pages, `SELECT p.id, p.current_version FROM wiki_pages p JOIN wikis w ON w.id = p.wiki_id
WHERE w.project_id = ? ORDER BY p.id`, dstID); err != nil {
			a.logger().Error("copy: wiki notification", "err", err)
		}
		for _, p := range pages {
			a.Notify.WikiContentAdded(ctx, p.ID, p.Version, c.User)
		}
	}
	if slices.Contains(only, "documents") {
		var ids []int64
		if err := a.DB.Select(ctx, &ids, `SELECT id FROM documents WHERE project_id = ? ORDER BY id`, dstID); err != nil {
			a.logger().Error("copy: document notification", "err", err)
		}
		for _, id := range ids {
			a.notify(c, "document_added", "document_added", &domain.Document{ID: id})
		}
	}
}

func (a *App) renderProjectCopy(c *Req, f *projectForm, src *domain.Project) {
	c.NewRecordProject = true
	c.NewProjectName, c.NewProjectIdentifier = f.Project.Name, f.Project.Identifier
	data, err := a.projectFormData(c, f)
	if errors.Is(err, errParentNotFound) {
		c.renderPublic404()
		return
	} else if err != nil {
		a.internalError(c, "project form", err)
		return
	}
	counts, err := repository.ProjectCopyCounts(c.Ctx(), a.DB, src.ID)
	if err != nil {
		a.internalError(c, "copy counts", err)
		return
	}
	data["SourceProject"] = src
	data["CopyCounts"] = counts
	data["Notifications"] = c.Params().Present("notifications")
	c.Render("projects/copy", data)
}
