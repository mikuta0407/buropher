// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"net/http"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/webhook"
)

// WikisController（app/controllers/wikis_controller.rb）。
//
//	menu_item :wiki
//	before_action :find_project, :authorize
var WikisController = &Controller{Name: "wikis", MainMenu: true, MenuItem: func(string) string { return "wiki" }}

// routesWikis は wikis コントローラのルートを登録する。
//
//	match 'projects/:id/wiki/destroy', :to => 'wikis#destroy', :via => [:get, :post]
func (a *App) routesWikis(r Router) {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/projects/{id}/wiki/destroy", WikisController, "destroy", a.WikisDestroy,
			FindProject("id"), Authorize())
	}
}

// WikisDestroy は wikis#destroy（確認後にプロジェクトの Wiki を削除し、既定の Wiki を作り直す）。
func (a *App) WikisDestroy(c *Req) {
	if c.R.Method == http.MethodPost && c.Params().Present("confirm") {
		w, err := repository.FindWiki(c.Ctx(), a.DB, c.Project.ID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.wikiError(c, err)
			return
		}
		if w != nil {
			var removedAttachments []*domain.Attachment
			// has_many :pages, dependent: :destroy の各ページの wiki_page.deleted（削除の前に計算する）
			if a.webhooksEnabled() {
				if ids, err := repository.WikiPageIDs(c.Ctx(), a.DB, w.ID); err == nil {
					a.prepareDeleteWebhooks(c, webhook.TypeWikiPage, ids...)
				}
			}
			err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
				ids, err := repository.WikiPageIDs(c.Ctx(), tx, w.ID)
				if err != nil {
					return err
				}
				deleted, err := repository.DeleteContainerAttachments(c.Ctx(), tx, "wiki_page", ids)
				if err != nil {
					return err
				}
				// ファイルはコミット後に消す（ロールバックで行が戻ってもファイルが失われないように）
				removedAttachments = deleted
				if err := repository.DeleteWatchers(c.Ctx(), tx, "wiki_page", ids); err != nil {
					return err
				}
				if err := repository.DeleteWiki(c.Ctx(), tx, w.ID); err != nil {
					return err
				}
				// Wiki.create_default(@project) unless @wiki（@wiki は常に nil）
				_, err = tx.Exec(c.Ctx(), `INSERT INTO wikis (project_id, start_page) VALUES (?, 'Wiki')`, c.Project.ID)
				return err
			})
			if err != nil {
				a.wikiError(c, err)
				return
			}
			a.enqueuePreparedDeleteWebhooks(c, webhook.TypeWikiPage)
			a.deleteAttachmentsAfterCommit(c, removedAttachments)
			c.Redirect("/projects/" + projectParam(c.Project))
			return
		}
	}
	if !a.wikiHTMLFormat(c) {
		return
	}
	c.Render("wikis/destroy", map[string]any{})
}
