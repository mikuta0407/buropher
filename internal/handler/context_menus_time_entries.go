// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"html/template"
	"slices"
	"sort"
	"strconv"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは ContextMenusController#time_entries（context_menus/time_entries.html）の移植。
// ルートは routesTimelogContextMenu（routesTimelog から登録）。

// routesTimelogContextMenu は match '/time_entries/context_menu', :to => 'context_menus#time_entries', :via => [:get, :post]。
func (a *App) routesTimelogContextMenu(r Router) {
	a.Handle(r, "GET", "/time_entries/context_menu", ContextMenusController, "time_entries", a.ContextMenusTimeEntries)
	a.Handle(r, "POST", "/time_entries/context_menu", ContextMenusController, "time_entries", a.ContextMenusTimeEntries)
}

// teCMFolder は CF の副メニュー。
type teCMFolder struct {
	CSS   string
	Name  string
	Links []template.HTML
}

// teContextMenuLink は ContextMenusHelper#context_menu_link。
func teContextMenuLink(c *Req, name any, url string, class string, selected, disabled bool, method string, data *rails.Hash) template.HTML {
	label := name
	classes := []string{}
	if class != "" {
		classes = append(classes, class)
	}
	if selected {
		classes = append(classes, "icon", "disabled")
		disabled = true
		label = c.App.Helpers.Icon(c.Page(), "checked", name, nil)
	}
	opts := rails.NewHash()
	if disabled {
		method, data = "", nil
		opts.Set("onclick", "return false;")
		classes = append(classes, "disabled")
		url = "#"
	}
	// class_names（重複を除く）
	var uniq []string
	for _, cl := range classes {
		if !slices.Contains(uniq, cl) {
			uniq = append(uniq, cl)
		}
	}
	css := ""
	for i, cl := range uniq {
		if i > 0 {
			css += " "
		}
		css += cl
	}
	// オプションの Hash の順序（呼び出し側が :class を渡していればそれが先頭）
	h := rails.NewHash()
	if data != nil {
		h.Set("data", data)
	}
	if class != "" {
		h.Set("class", css)
	}
	if v, ok := opts.Lookup("onclick"); ok {
		h.Set("onclick", v)
	}
	h.Set("class", css)
	if method != "" {
		h.Set("method", method)
	}
	return rails.LinkTo(label, url, h)
}

// ContextMenusTimeEntries は context_menus#time_entries（layout なし）。
func (a *App) ContextMenusTimeEntries(c *Req) {
	ctx := c.Ctx()
	ids := idsFromParam(c.Params().Slice("ids"))
	rs, err := repository.TimeEntriesByIDs(ctx, a.DB, ids)
	if err != nil {
		a.internalError(c, "time entries", err)
		return
	}
	env := a.teEnv(c)
	var entries []*timelog.Entry
	var projects []*domain.Project
	editable := true
	for _, r := range rs {
		t := timelog.FromRecord(r)
		// 本家は TimeEntry.where(:id => ...) で可視性を見ないため、見えない工数の存在とそのプロジェクトの
		// 作業分類名が分かった。見えない工数は無いものとして扱う
		vis, err := env.Visible(ctx, t, c.User)
		if err != nil {
			a.internalError(c, "time entry visible", err)
			return
		}
		if !vis {
			continue
		}
		entries = append(entries, t)
		p, err := env.Project(ctx, t.ProjectID)
		if err != nil {
			a.internalError(c, "project", err)
			return
		}
		if p != nil && !containsProject(projects, p) {
			projects = append(projects, p)
		}
		ok, err := env.EditableBy(ctx, t, c.User)
		if err != nil {
			a.internalError(c, "editable", err)
			return
		}
		editable = editable && ok
	}
	if len(entries) == 0 {
		c.Render404("")
		return
	}
	if len(projects) == 1 {
		c.Project = projects[0]
	}
	var single *timelog.Entry
	if len(entries) == 1 {
		single = entries[0]
	}
	// @activities = @projects.map(&:activities).reduce(:&)
	var acts []*domain.Enumeration
	for i, p := range projects {
		pa, err := repository.TimelogProjectActivities(ctx, a.DB, p.ID, false)
		if err != nil {
			a.internalError(c, "activities", err)
			return
		}
		if i == 0 {
			acts = pa
		} else {
			acts = slices.DeleteFunc(acts, func(e *domain.Enumeration) bool {
				return !slices.ContainsFunc(pa, func(x *domain.Enumeration) bool { return x.ID == e.ID })
			})
		}
	}
	back := helper.BackURLOf(c.Page())
	idList := make([]any, len(entries))
	for i, t := range entries {
		idList[i] = t.ID
	}
	withBack := func(h *rails.Hash) *rails.Hash {
		if back != "" {
			h.Set("back_url", back)
		}
		return h
	}
	page := c.Page()
	data := map[string]any{}
	if single != nil {
		data["EditLink"] = teContextMenuLink(c, a.Helpers.Icon(page, "edit", c.L("button_edit"), nil), "/time_entries/"+strconv.FormatInt(single.ID, 10)+"/edit", "icon icon-edit", false, !editable, "", nil)
	} else {
		data["EditLink"] = teContextMenuLink(c, a.Helpers.Icon(page, "edit", c.L("label_bulk_edit"), nil),
			helper.URLWithQuery("/time_entries/bulk_edit", rails.NewHash("ids", idList)), "icon icon-edit", false, !editable, "", nil)
	}
	var actLinks []template.HTML
	for _, act := range acts {
		u := helper.URLWithQuery("/time_entries/bulk_update", withBack(rails.NewHash("ids", idList, "time_entry", rails.NewHash("activity_id", act.ID))))
		sel := single != nil && single.ActivityID != nil && *single.ActivityID == act.ID
		actLinks = append(actLinks, teContextMenuLink(c, act.Name, u, "", sel, !editable, "post", nil))
	}
	data["ActivityLinks"] = actLinks
	var folders []*teCMFolder
	if editable {
		cfs, err := a.teContextMenuCFs(c, env, entries)
		if err != nil {
			a.internalError(c, "custom fields", err)
			return
		}
		sorted := make([]any, len(entries))
		sortedIDs := make([]int64, len(entries))
		for i, t := range entries {
			sortedIDs[i] = t.ID
		}
		sort.Slice(sortedIDs, func(i, j int) bool { return sortedIDs[i] < sortedIDs[j] })
		for i, id := range sortedIDs {
			sorted[i] = id
		}
		for _, cf := range cfs {
			cenv := &customfield.Env{T: c.L, CurrentUserID: c.User.ID}
			opts := cf.Format().PossibleValuesOptions(cenv, cf, nil)
			if len(opts) == 0 {
				continue
			}
			f := &teCMFolder{CSS: cf.FieldFormat + "_cf cf_" + strconv.FormatInt(cf.ID, 10), Name: cf.Name}
			link := func(text, value string) template.HTML {
				u := helper.URLWithQuery("/time_entries/bulk_update", withBack(rails.NewHash("ids", sorted,
					"time_entry", rails.NewHash("custom_field_values", rails.NewHash(strconv.FormatInt(cf.ID, 10), value)))))
				sel := false
				if single != nil {
					cvs, _ := repository.CustomValues(ctx, a.DB, "time_entry", single.ID)
					if vs, ok := cvs[cf.ID]; ok && len(vs) > 0 && vs[0] == value {
						sel = true
					}
				}
				return teContextMenuLink(c, rails.H(text), u, "", sel, false, "post", nil)
			}
			for _, o := range opts {
				v := o.Value
				if v == "" {
					v = o.Label
				}
				f.Links = append(f.Links, link(o.Label, v))
			}
			if !cf.IsRequired {
				f.Links = append(f.Links, link(c.L("label_none"), "__none__"))
			}
			folders = append(folders, f)
		}
	}
	data["Folders"] = folders
	data["DeleteLink"] = teContextMenuLink(c, a.Helpers.Icon(page, "del", c.L("button_delete"), nil),
		helper.URLWithQuery("/time_entries/destroy", withBack(rails.NewHash("ids", idList))), "icon icon-del", false, !editable,
		"delete", rails.NewHash("confirm", c.L("text_time_entries_destroy_confirmation")))
	c.Render("context_menus/time_entries", data, RenderOptions{Layout: view.NoLayout})
}

// teContextMenuCFs は @time_entries.map(&:editable_custom_fields).reduce(:&)（複数値を除き bulk_edit_supported なもの）。
func (a *App) teContextMenuCFs(c *Req, env *timelog.Env, entries []*timelog.Entry) ([]*customfield.CustomField, error) {
	ctx := c.Ctx()
	var ids []int64
	for i, t := range entries {
		full, err := env.Find(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		vals, err := env.VisibleCustomFieldValues(ctx, full, c.User)
		if err != nil {
			return nil, err
		}
		var cur []int64
		for _, v := range vals {
			cur = append(cur, v.Field.ID)
		}
		if i == 0 {
			ids = cur
		} else {
			ids = slices.DeleteFunc(ids, func(id int64) bool { return !slices.Contains(cur, id) })
		}
	}
	var out []*customfield.CustomField
	for _, id := range ids {
		cf, err := customfield.Get(ctx, a.DB, id)
		if err != nil {
			return nil, err
		}
		if cf == nil || cf.Multiple || !cf.Format().BulkEditSupported {
			continue
		}
		out = append(out, cf)
	}
	return out, nil
}

var _ = httpx.IsBlank
