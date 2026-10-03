// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// ProjectEnumerationsController（app/controllers/project_enumerations_controller.rb）。
var ProjectEnumerationsController = &Controller{Name: "project_enumerations", MainMenu: true}

// routesProjectEnumerations は project_enumerations コントローラのルートを登録する。
//
//	resources :projects do
//	  resource :enumerations, :controller => 'project_enumerations', :only => [:update, :destroy]
//	end
func (a *App) routesProjectEnumerations(r Router) {
	ctrl := ProjectEnumerationsController
	// before_action :find_project_by_project_id
	// before_action :authorize
	a.Handle(r, http.MethodPatch, "/projects/{project_id}/enumerations", ctrl, "update", a.ProjectEnumerationsUpdate, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodPut, "/projects/{project_id}/enumerations", ctrl, "update", a.ProjectEnumerationsUpdate, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodDelete, "/projects/{project_id}/enumerations", ctrl, "destroy", a.ProjectEnumerationsDestroy, FindProjectByProjectID(), Authorize())
}

// ProjectEnumerationsUpdate は project_enumerations#update
// （Project#update_or_create_time_entry_activities）。
func (a *App) ProjectEnumerationsUpdate(c *Req) {
	ctx := c.Ctx()
	p := c.Project
	enums := c.Params().Map("enumerations")
	if enums == nil {
		// params.require(:enumerations) → ActionController::ParameterMissing（400）
		c.RenderError(http.StatusBadRequest, "")
		return
	}
	cfs, err := repository.CustomFieldInfosByKind(ctx, a.DB, "time_entry_activity")
	if err != nil {
		a.internalError(c, "activity custom fields", err)
		return
	}
	err = a.DB.WithTx(ctx, func(tx *db.Tx) error {
		for _, key := range enums.Keys() {
			attrs := enums.Map(key)
			if attrs == nil {
				continue
			}
			if attrs.Has("parent_id") {
				if err := a.createTimeEntryActivityIfNeeded(c, tx, p, attrs, cfs); err != nil {
					return err
				}
				continue
			}
			id, ok := parseIntStrict(key)
			if !ok {
				continue
			}
			e, err := repository.GetEnumerationOfKind(ctx, tx, domain.EnumTimeEntryActivity, id)
			if errors.Is(err, repository.ErrNotFound) || (err == nil && (e.ProjectID == nil || *e.ProjectID != p.ID)) {
				continue
			} else if err != nil {
				return err
			}
			if v, ok := attrs.StringOK("active"); ok {
				e.Active = castBool(v)
			}
			if err := repository.SaveEnumeration(ctx, tx, e); err != nil {
				return err
			}
			if cv := attrs.Map("custom_field_values"); cv != nil {
				for _, cf := range cfs {
					k := strconv.FormatInt(cf.ID, 10)
					if cv.Has(k) {
						if err := repository.SetCustomValues(ctx, tx, "enumeration", e.ID, cf.ID, []string{cv.String(k)}); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		a.internalError(c, "update project activities", err)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.Redirect("/projects/" + p.Identifier + "/settings/activities")
}

// createTimeEntryActivityIfNeeded は Project#create_time_entry_activity_if_needed:
// システムの作業分類と有効・カスタム値が異なればプロジェクト別の上書き行を作り、
// プロジェクトの作業時間の作業分類を付け替える。
func (a *App) createTimeEntryActivityIfNeeded(c *Req, tx *db.Tx, p *domain.Project, attrs *httpx.Params, cfs []*domain.CustomFieldInfo) error {
	ctx := c.Ctx()
	pid, ok := parseIntStrict(attrs.String("parent_id"))
	if !ok {
		return nil
	}
	parent, err := repository.GetEnumerationOfKind(ctx, tx, domain.EnumTimeEntryActivity, pid)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return err
	}
	// Enumeration.overriding_change?
	active := attrs.String("active") == "1"
	changed := active != parent.Active
	cv := attrs.Map("custom_field_values")
	parentVals, err := repository.CustomValues(ctx, tx, "enumeration", parent.ID)
	if err != nil {
		return err
	}
	for _, cf := range cfs {
		var prev string
		if vs := parentVals[cf.ID]; len(vs) > 0 {
			prev = vs[0]
		}
		var now string
		if cv != nil {
			now = cv.String(strconv.FormatInt(cf.ID, 10))
		}
		if prev != now {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	pp := p.ID
	parentID := parent.ID
	e := &domain.Enumeration{Kind: domain.EnumTimeEntryActivity, Name: parent.Name, Position: parent.Position,
		Active: active, ProjectID: &pp, ParentID: &parentID}
	if err := repository.SaveEnumeration(ctx, tx, e); err != nil {
		return err
	}
	if cv != nil {
		for _, cf := range cfs {
			k := strconv.FormatInt(cf.ID, 10)
			if cv.Has(k) {
				if err := repository.SetCustomValues(ctx, tx, "enumeration", e.ID, cf.ID, []string{cv.String(k)}); err != nil {
					return err
				}
			}
		}
	}
	return repository.ReassignTimeEntryActivity(ctx, tx, p.ID, parent.ID, e.ID)
}

// ProjectEnumerationsDestroy は project_enumerations#destroy（上書き行を削除し、作業時間を親に戻す）。
func (a *App) ProjectEnumerationsDestroy(c *Req) {
	ctx := c.Ctx()
	p := c.Project
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		acts, err := repository.ProjectTimeEntryActivities(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		for _, e := range acts {
			if e.ParentID != nil {
				parent, err := repository.GetEnumerationOfKind(ctx, tx, domain.EnumTimeEntryActivity, *e.ParentID)
				if err == nil {
					if err := repository.TransferEnumerationRelations(ctx, tx, e, parent); err != nil {
						return err
					}
				} else if !errors.Is(err, repository.ErrNotFound) {
					return err
				}
			}
			if err := repository.DestroyEnumeration(ctx, tx, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.internalError(c, "reset project activities", err)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.Redirect("/projects/" + p.Identifier + "/settings/activities")
}
