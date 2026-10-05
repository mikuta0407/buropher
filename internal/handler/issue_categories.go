// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// IssueCategoriesController（app/controllers/issue_categories_controller.rb）。menu_item :settings。
var IssueCategoriesController = &Controller{Name: "issue_categories", MainMenu: true,
	MenuItem: func(string) string { return "settings" }}

// routesIssueCategories は issue_categories コントローラのルートを登録する。
//
//	resources :projects do
//	  shallow do
//	    resources :issue_categories
//	  end
//	end
func (a *App) routesIssueCategories(r Router) {
	ctrl := IssueCategoriesController
	// before_action :find_model_object, :except => [:index, :new, :create]
	// before_action :find_project_from_association, :except => [:index, :new, :create]
	// before_action :find_project_by_project_id, :only => [:index, :new, :create]
	// before_action :authorize
	// accept_api_auth :index, :show, :create, :update, :destroy
	find := Before(a.findIssueCategoryFilter)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/issue_categories", ctrl, "index", a.IssueCategoriesIndex, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/projects/{project_id}/issue_categories", ctrl, "create", a.IssueCategoriesCreate, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/projects/{project_id}/issue_categories/new", ctrl, "new", a.IssueCategoriesNew, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodGet, "/issue_categories/{id}/edit", ctrl, "edit", a.IssueCategoriesEdit, find, Authorize())
	a.Handle(r, http.MethodGet, "/issue_categories/{id}", ctrl, "show", a.IssueCategoriesShow, find, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPatch, "/issue_categories/{id}", ctrl, "update", a.IssueCategoriesUpdate, find, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPut, "/issue_categories/{id}", ctrl, "update", a.IssueCategoriesUpdate, find, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodDelete, "/issue_categories/{id}", ctrl, "destroy", a.IssueCategoriesDestroy, find, Authorize(), AcceptAPIAuth())
}

// categoryForm は @category。
type categoryForm struct {
	formModel
	*repository.IssueCategoryInfo
}

// AssignedToID は select の値。
func (f *categoryForm) AssignedToID() any { return derefID(f.IssueCategoryInfo.AssignedToID) }

func (a *App) findIssueCategoryFilter(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	cat, err := repository.GetIssueCategory(c.Ctx(), a.DB, id)
	if errors.Is(err, repository.ErrNotFound) {
		c.Render404("")
		return
	} else if err != nil {
		a.internalError(c, "find issue category", err)
		return
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, cat.ProjectID)
	if err != nil {
		a.internalError(c, "category project", err)
		return
	}
	c.Project = p
	c.setLocal("category", cat)
}

func (c *Req) category() *repository.IssueCategoryInfo {
	cat, _ := c.local("category").(*repository.IssueCategoryInfo)
	return cat
}

func redirectToCategoriesSettings(c *Req) {
	c.Redirect("/projects/" + c.Project.Identifier + "/settings/categories")
}

// IssueCategoriesIndex は issue_categories#index。
func (a *App) IssueCategoriesIndex(c *Req) {
	if !httpx.IsAPIRequest(c.R) {
		redirectToCategoriesSettings(c)
		return
	}
	cats, err := repository.ProjectIssueCategories(c.Ctx(), a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "issue categories", err)
		return
	}
	arr := apiArr("issue_categories")
	for _, cat := range cats {
		el, err := a.issueCategoryAPIEl(c, cat)
		if err != nil {
			a.internalError(c, "issue category api", err)
			return
		}
		arr.children = append(arr.children, el)
	}
	var meta [][2]any
	if !(c.Params().Present("nometa") || nometaHeader(c.R)) {
		meta = [][2]any{{"total_count", len(cats)}}
	}
	c.renderAPIRoot(arr, meta, http.StatusOK)
}

func (a *App) issueCategoryAPIEl(c *Req, cat *repository.IssueCategoryInfo) (*apiEl, error) {
	p, err := repository.GetProject(c.Ctx(), a.DB, cat.ProjectID)
	if err != nil {
		return nil, err
	}
	el := apiObj("issue_category", apiField("id", cat.ID), apiAttr("project", [2]any{"id", p.ID}, [2]any{"name", p.Name}), apiField("name", cat.Name))
	if cat.AssignedToID != nil {
		users, groups, err := repository.PrincipalsByIDs(c.Ctx(), a.DB, []int64{*cat.AssignedToID})
		if err != nil {
			return nil, err
		}
		page := c.Page()
		var name string
		if u := users[*cat.AssignedToID]; u != nil {
			name = helper.PrincipalName(page, u)
		} else if g := groups[*cat.AssignedToID]; g != nil {
			name = helper.PrincipalName(page, g)
		}
		if name != "" {
			el.children = append(el.children, apiAttr("assigned_to", [2]any{"id", *cat.AssignedToID}, [2]any{"name", name}))
		}
	}
	return el, nil
}

// IssueCategoriesShow は issue_categories#show。
func (a *App) IssueCategoriesShow(c *Req) {
	if !httpx.IsAPIRequest(c.R) {
		redirectToCategoriesSettings(c)
		return
	}
	el, err := a.issueCategoryAPIEl(c, c.category())
	if err != nil {
		a.internalError(c, "issue category api", err)
		return
	}
	c.renderAPIRoot(el, nil, http.StatusOK)
}

// assignCategory は @category.safe_attributes = params[:issue_category]。
func assignCategory(f *categoryForm, p *httpx.Params) {
	if p == nil {
		return
	}
	if v, ok := p.StringOK("name"); ok {
		f.Name = v
	}
	if v, ok := p.StringOK("assigned_to_id"); ok {
		f.IssueCategoryInfo.AssignedToID = optionalID(v)
	}
}

// validateCategory は IssueCategory のバリデーション。
func (a *App) validateCategory(c *Req, f *categoryForm) error {
	f.errs = &domain.ValidationErrors{}
	if strings.TrimSpace(f.Name) == "" {
		f.errs.Add("name", "blank", nil)
	}
	taken, err := repository.IssueCategoryNameTaken(c.Ctx(), a.DB, f.ProjectID, f.Name, f.ID)
	if err != nil {
		return err
	}
	if taken {
		f.errs.Add("name", "taken", nil)
	}
	if len([]rune(f.Name)) > 60 {
		f.errs.Add("name", "too_long", map[string]any{"count": 60})
	}
	return nil
}

func (a *App) categoryFormData(c *Req, f *categoryForm) (map[string]any, error) {
	ctx := c.Ctx()
	ids, err := repository.ProjectAssignableUserIDs(ctx, a.DB, c.Project.ID, a.Settings.Bool("issue_group_assignment"))
	if err != nil {
		return nil, err
	}
	users, groups, err := repository.PrincipalsByIDs(ctx, a.DB, ids)
	if err != nil {
		return nil, err
	}
	opts := a.principalsOptionsForSelect(c, c.Page(), users, groups, derefID(f.IssueCategoryInfo.AssignedToID))
	return map[string]any{"Category": f, "AssigneeOptions": opts, "Project": c.Project}, nil
}

func (a *App) renderCategoryForm(c *Req, f *categoryForm, tmpl string, format string) {
	data, err := a.categoryFormData(c, f)
	if err != nil {
		a.internalError(c, "category form", err)
		return
	}
	if format == "js" {
		c.Render(tmpl, data, RenderOptions{Format: "js", Layout: view.NoLayout})
		return
	}
	c.Render(tmpl, data)
}

// IssueCategoriesNew は issue_categories#new（HTML / js）。
func (a *App) IssueCategoriesNew(c *Req) {
	f := &categoryForm{formModel: newFormModel(c, "issue_category", 0), IssueCategoryInfo: &repository.IssueCategoryInfo{ProjectID: c.Project.ID}}
	assignCategory(f, c.Params().Map("issue_category"))
	switch httpx.Negotiate(c.R, "html", "js") {
	case "html":
		a.renderCategoryForm(c, f, "issue_categories/new", "html")
	case "js":
		a.renderCategoryForm(c, f, "issue_categories/new", "js")
	default:
		c.unknownFormat()
	}
}

// IssueCategoriesCreate は issue_categories#create。
func (a *App) IssueCategoriesCreate(c *Req) {
	ctx := c.Ctx()
	f := &categoryForm{formModel: newFormModel(c, "issue_category", 0), IssueCategoryInfo: &repository.IssueCategoryInfo{ProjectID: c.Project.ID}}
	assignCategory(f, c.Params().Map("issue_category"))
	if err := a.validateCategory(c, f); err != nil {
		a.internalError(c, "validate category", err)
		return
	}
	format := httpx.Negotiate(c.R, "html", "js", "xml", "json")
	if !f.errs.Any() {
		if err := a.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.SaveIssueCategory(ctx, tx, f.IssueCategoryInfo) }); err != nil {
			a.internalError(c, "save category", err)
			return
		}
		f.id = f.ID
		switch format {
		case "html":
			c.Flash().SetNotice(c.L("notice_successful_create"))
			redirectToCategoriesSettings(c)
		case "js":
			cats, err := repository.ProjectIssueCategories(ctx, a.DB, c.Project.ID)
			if err != nil {
				a.internalError(c, "issue categories", err)
				return
			}
			items := make([]any, len(cats))
			for i, cat := range cats {
				items[i] = []any{cat.Name, cat.ID}
			}
			sel := rails.ContentTag("select", rails.ContentTag("option", nil, nil)+rails.OptionsForSelect(items, f.ID),
				rails.NewHash("id", "issue_category_id", "name", "issue[category_id]"))
			c.Render("issue_categories/create", map[string]any{"Select": sel}, RenderOptions{Format: "js", Layout: view.NoLayout})
		case "xml", "json":
			el, err := a.issueCategoryAPIEl(c, f.IssueCategoryInfo)
			if err != nil {
				a.internalError(c, "issue category api", err)
				return
			}
			// :location => issue_category_path(@category)（相対パス）
			c.W.Header().Set("Location", urlroot.Path("/issue_categories/"+strconv.FormatInt(f.ID, 10)))
			c.renderAPIRoot(el, nil, http.StatusCreated)
		default:
			c.unknownFormat()
		}
		return
	}
	switch format {
	case "html":
		a.renderCategoryForm(c, f, "issue_categories/new", "html")
	case "js":
		a.renderCategoryForm(c, f, "issue_categories/new", "js")
	case "xml", "json":
		c.RenderAPIErrorsMin(f.errs.FullMessages(c.L)...)
	default:
		c.unknownFormat()
	}
}

// IssueCategoriesEdit は issue_categories#edit。
func (a *App) IssueCategoriesEdit(c *Req) {
	cat := c.category()
	f := &categoryForm{formModel: newFormModel(c, "issue_category", cat.ID), IssueCategoryInfo: cat}
	a.renderCategoryForm(c, f, "issue_categories/edit", "html")
}

// IssueCategoriesUpdate は issue_categories#update。
func (a *App) IssueCategoriesUpdate(c *Req) {
	ctx := c.Ctx()
	cat := c.category()
	f := &categoryForm{formModel: newFormModel(c, "issue_category", cat.ID), IssueCategoryInfo: cat}
	assignCategory(f, c.Params().Map("issue_category"))
	if err := a.validateCategory(c, f); err != nil {
		a.internalError(c, "validate category", err)
		return
	}
	api := httpx.IsAPIRequest(c.R)
	if !f.errs.Any() {
		if err := a.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.SaveIssueCategory(ctx, tx, f.IssueCategoryInfo) }); err != nil {
			a.internalError(c, "save category", err)
			return
		}
		if api {
			c.RenderAPIOKMin()
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		redirectToCategoriesSettings(c)
		return
	}
	if api {
		c.RenderAPIErrorsMin(f.errs.FullMessages(c.L)...)
		return
	}
	a.renderCategoryForm(c, f, "issue_categories/edit", "html")
}

// IssueCategoriesDestroy は issue_categories#destroy。
func (a *App) IssueCategoriesDestroy(c *Req) {
	ctx := c.Ctx()
	cat := c.category()
	count, err := repository.IssueCategoryIssueCount(ctx, a.DB, cat.ID)
	if err != nil {
		a.internalError(c, "category issues", err)
		return
	}
	todo := c.Params().String("todo")
	api := httpx.IsAPIRequest(c.R)
	if count == 0 || todo != "" || api {
		var reassign *int64
		if c.Params().Has("reassign_to_id") && (todo == "reassign" || todo == "") {
			if id, ok := parseIntStrict(c.Params().String("reassign_to_id")); ok {
				if target, err := repository.GetIssueCategory(ctx, a.DB, id); err == nil && target.ProjectID == c.Project.ID && target.ID != cat.ID {
					reassign = &target.ID
				} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
					a.internalError(c, "reassign category", err)
					return
				}
			}
		}
		if err := a.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.DestroyIssueCategory(ctx, tx, cat.ID, reassign) }); err != nil {
			a.internalError(c, "destroy category", err)
			return
		}
		if api {
			c.RenderAPIOKMin()
			return
		}
		redirectToCategoriesSettings(c)
		return
	}
	cats, err := repository.ProjectIssueCategories(ctx, a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "issue categories", err)
		return
	}
	var others []any
	for _, x := range cats {
		if x.ID != cat.ID {
			others = append(others, []any{x.Name, x.ID})
		}
	}
	c.Render("issue_categories/destroy", map[string]any{
		"Category": cat, "IssueCount": count, "Others": others,
		"OtherOptions": template.HTML(rails.OptionsForSelect(others, nil)),
	})
}
