// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// DocumentsController（app/controllers/documents_controller.rb）。
//
//	default_search_scope :documents
//	model_object Document
//	before_action :find_project_by_project_id, :only => [:index, :new, :create]
//	before_action :find_model_object, :except => [:index, :new, :create]
//	before_action :find_project_from_association, :except => [:index, :new, :create]
//	before_action :authorize
var DocumentsController = &Controller{Name: "documents", MainMenu: true, DefaultSearchScope: "documents"}

// routesDocuments は documents コントローラのルートを登録する。
//
//	resources :projects do resources :documents, :except => [:show, :edit, :update, :destroy] end
//	resources :documents, :only => [:show, :edit, :update, :destroy] do post 'add_attachment', :on => :member end
func (a *App) routesDocuments(r Router) {
	d := DocumentsController
	find := Before(a.findDocument)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/documents", d, "index", a.DocumentsIndex, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodPost, "/projects/{project_id}/documents", d, "create", a.DocumentsCreate, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodGet, "/projects/{project_id}/documents/new", d, "new", a.DocumentsNew, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodPost, "/documents/{id}/add_attachment", d, "add_attachment", a.DocumentsAddAttachment, find, Authorize())
	a.Handle(r, http.MethodGet, "/documents/{id}/edit", d, "edit", a.DocumentsEdit, find, Authorize())
	a.Handle(r, http.MethodGet, "/documents/{id}", d, "show", a.DocumentsShow, find, Authorize())
	a.Handle(r, http.MethodPatch, "/documents/{id}", d, "update", a.DocumentsUpdate, find, Authorize())
	a.Handle(r, http.MethodPut, "/documents/{id}", d, "update", a.DocumentsUpdate, find, Authorize())
	a.Handle(r, http.MethodDelete, "/documents/{id}", d, "destroy", a.DocumentsDestroy, find, Authorize())
}

type documentCtxKey struct{}

// documentForm は @document（labelled_form_for のモデル）。
type documentForm struct {
	contentForm
	*domain.Document
	cfs    []*domain.CustomFieldInfo
	values map[int64][]string
	given  map[int64]bool
}

// CustomFieldValues は @document.custom_field_values。
func (f *documentForm) CustomFieldValues() []*domain.CustomFieldValue {
	out := make([]*domain.CustomFieldValue, len(f.cfs))
	for i, cf := range f.cfs {
		vs, ok := f.values[cf.ID]
		if !ok && f.Document.ID == 0 && cf.DefaultValue != nil {
			vs = []string{*cf.DefaultValue}
		}
		out[i] = &domain.CustomFieldValue{Field: cf, Values: vs}
	}
	return out
}

func (c *Req) document() *documentForm {
	f, _ := c.value(documentCtxKey{}).(*documentForm)
	return f
}

func (a *App) newDocumentForm(c *Req, d *domain.Document) (*documentForm, error) {
	cfs, err := repository.CustomFieldInfosByKind(c.Ctx(), a.DB, "document")
	if err != nil {
		return nil, err
	}
	f := &documentForm{contentForm: newContentForm(c, "document", d.ID), Document: d, cfs: cfs,
		values: map[int64][]string{}, given: map[int64]bool{}}
	if d.ID != 0 {
		if f.values, err = repository.CustomValues(c.Ctx(), a.DB, "document", d.ID); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// findDocument は find_model_object（Document.find(params[:id])）と find_project_from_association。
func (a *App) findDocument(c *Req) {
	id, ok := paramInt64(c, "id")
	if !ok {
		c.Render404("")
		return
	}
	d, err := repository.GetDocument(c.Ctx(), a.DB, id)
	if err != nil {
		a.notFoundOr500(c, "find document", err)
		return
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, d.ProjectID)
	if err != nil {
		a.notFoundOr500(c, "document project", err)
		return
	}
	d.Project = p
	c.Project = p
	f, err := a.newDocumentForm(c, d)
	if err != nil {
		a.internalError(c, "document form", err)
		return
	}
	c.setValue(documentCtxKey{}, f)
}

// buildDocument は @project.documents.build（category は DocumentCategory.default）。
func (a *App) buildDocument(c *Req) (*documentForm, error) {
	catID, err := repository.DefaultDocumentCategoryID(c.Ctx(), a.DB)
	if err != nil {
		return nil, err
	}
	return a.newDocumentForm(c, &domain.Document{ProjectID: c.Project.ID, Project: c.Project, CategoryID: catID})
}

// assignDocument は document.safe_attributes = params[:document]
// （category_id / title / description / custom_field_values）。
func (f *documentForm) assign(c *Req) {
	p := c.Params().Map("document")
	if p == nil {
		return
	}
	d := f.Document
	if v, ok := p.StringOK("category_id"); ok {
		d.CategoryID = httpx.RubyToI(v)
	}
	if v, ok := p.StringOK("title"); ok {
		d.Title = v
	}
	if v, ok := p.StringOK("description"); ok {
		d.Description = v
	}
	if m := p.Map("custom_field_values"); m != nil {
		for _, cf := range f.cfs {
			k := strconv.FormatInt(cf.ID, 10)
			if !m.Has(k) {
				continue
			}
			var vs []string
			if cf.Multiple {
				for _, s := range m.Strings(k) {
					if s != "" {
						vs = append(vs, s)
					}
				}
			} else {
				vs = []string{m.String(k)}
			}
			f.values[cf.ID] = vs
			f.given[cf.ID] = true
		}
	}
}

// validate は Document の検証（project / title / category 必須、title 255 文字以内）。
// TODO: カスタムフィールドの値の検証（必須・形式）。
func (a *App) validateDocument(c *Req, f *documentForm) error {
	e := f.errs
	d := f.Document
	if httpx.IsBlank(d.Title) {
		e.Add("title", "blank")
	}
	ok, err := repository.DocumentCategoryExists(c.Ctx(), a.DB, d.CategoryID)
	if err != nil {
		return err
	}
	if !ok {
		e.Add("category", "blank")
	}
	if len([]rune(d.Title)) > 255 {
		e.Add("title", "too_long", "count", 255)
	}
	return nil
}

// saveDocumentCustomValues は save_custom_field_values。
func (a *App) saveDocumentCustomValues(c *Req, tx *db.Tx, f *documentForm, isNew bool) error {
	existing := map[int64][]string{}
	if !isNew {
		var err error
		if existing, err = repository.CustomValues(c.Ctx(), tx, "document", f.ID); err != nil {
			return err
		}
	}
	for _, cf := range f.cfs {
		_, had := existing[cf.ID]
		if !f.given[cf.ID] && had {
			continue
		}
		var vs []string
		if f.given[cf.ID] {
			vs = f.values[cf.ID]
		} else if cf.DefaultValue != nil {
			vs = []string{*cf.DefaultValue}
		}
		if err := repository.SetCustomValues(c.Ctx(), tx, "document", f.ID, cf.ID, vs); err != nil {
			return err
		}
	}
	return nil
}

// documentGroup は index のグループ（@grouped の 1 キー分）。
type documentGroup struct {
	Name      string
	Documents []*domain.Document
}

// DocumentsIndex は documents#index。
func (a *App) DocumentsIndex(c *Req) {
	sortBy := c.Params().String("sort_by")
	switch sortBy {
	case "category", "date", "title", "author":
	default:
		sortBy = "category"
	}
	docs, err := repository.ProjectDocuments(c.Ctx(), a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "documents", err)
		return
	}
	groups := groupDocuments(c, docs, sortBy)
	f, err := a.buildDocument(c)
	if err != nil {
		a.internalError(c, "build document", err)
		return
	}
	data := map[string]any{"Groups": groups, "SortBy": sortBy, "Document": f, "Categories": a.activeDocumentCategories(c)}
	opts := RenderOptions{}
	if httpx.IsXHR(c.R) {
		opts.Layout = view.NoLayout
	}
	c.Render("documents/index", data, opts)
}

// groupDocuments は index の @grouped と @grouped.keys.sort（日付は逆順）を作る。
func groupDocuments(c *Req, docs []*domain.Document, sortBy string) []documentGroup {
	type group struct {
		name  string
		key   any
		order int
		docs  []*domain.Document
	}
	var groups []*group
	find := func(name string, key any, mk func() *group) *group {
		for _, g := range groups {
			if g.name == name {
				return g
			}
		}
		g := mk()
		groups = append(groups, g)
		return g
	}
	switch sortBy {
	case "date":
		sorted := append([]*domain.Document(nil), docs...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].UpdatedOn().After(sorted[j].UpdatedOn()) })
		for _, d := range sorted {
			// updated_on.to_date（Time.zone = UTC の日付）
			day := d.UpdatedOn().UTC().Format("2006-01-02")
			g := find(day, day, func() *group { return &group{name: day, key: day} })
			g.docs = append(g.docs, d)
		}
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].name > groups[j].name })
	case "title":
		for _, d := range docs {
			k := ""
			if r := []rune(d.Title); len(r) > 0 {
				k = strings.ToUpper(string(r[0]))
			}
			g := find(k, k, func() *group { return &group{name: k} })
			g.docs = append(g.docs, d)
		}
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].name < groups[j].name })
	case "author":
		page := c.Page()
		for _, d := range docs {
			if len(d.Attachments) == 0 {
				continue
			}
			au := d.Attachments[len(d.Attachments)-1].Author
			name := ""
			if au != nil {
				name = page.UserName(au)
			}
			g := find(name, au, func() *group { return &group{name: name} })
			g.docs = append(g.docs, d)
		}
		// Principal#<=>（to_s.casecmp）
		sort.SliceStable(groups, func(i, j int) bool { return casecmp(groups[i].name, groups[j].name) < 0 })
	default:
		for _, d := range docs {
			name := ""
			pos := 0
			if d.Category != nil {
				name, pos = d.Category.Name, d.Category.Position
			}
			g := find(name, name, func() *group { return &group{name: name, order: pos} })
			g.docs = append(g.docs, d)
		}
		// Enumeration#<=>（position）
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].order < groups[j].order })
	}
	out := make([]documentGroup, len(groups))
	for i, g := range groups {
		out[i] = documentGroup{Name: g.name, Documents: g.docs}
	}
	return out
}

// activeDocumentCategories は DocumentCategory.active.collect {|c| [c.name, c.id]}。
func (a *App) activeDocumentCategories(c *Req) []any {
	cats, err := repository.ListEnumerations(c.Ctx(), a.DB, domain.EnumDocumentCategory, false)
	if err != nil {
		a.logger().Error("document categories", "err", err)
		return nil
	}
	var out []any
	for _, cat := range cats {
		if cat.Active {
			out = append(out, []any{cat.Name, cat.ID})
		}
	}
	return out
}

func (a *App) renderDocumentForm(c *Req, tmpl string, f *documentForm, saved []*domain.Attachment) {
	c.Render(tmpl, map[string]any{"Document": f, "Categories": a.activeDocumentCategories(c), "SavedAttachments": saved})
}

// DocumentsShow は documents#show。
func (a *App) DocumentsShow(c *Req) {
	f := c.document()
	atts, err := repository.ContainerAttachmentList(c.Ctx(), a.DB, domain.AttachmentContainerDocument, f.ID)
	if err != nil {
		a.internalError(c, "document attachments", err)
		return
	}
	f.Attachments = atts
	// render_custom_field_values(@document): visible_custom_field_values（CustomFieldValue#visible? は
	// custom_field.visible? だけを見るので管理者にも非表示フィールドは出さない）の、値が空でないもの
	var cvs []documentCustomValue
	all := f.CustomFieldValues()
	for _, v := range all {
		if !v.Field.Visible {
			continue
		}
		if s := strings.Join(v.Values, ", "); strings.TrimSpace(s) != "" {
			cvs = append(cvs, documentCustomValue{Name: v.Field.Name, Value: s})
		}
	}
	c.Render("documents/show", map[string]any{
		"Document":        f,
		"Attachments":     atts,
		"HasCustomFields": len(all) > 0,
		"CustomValues":    cvs,
		"CanEdit":         c.AllowedTo(domain.Perm("edit_documents"), c.Project),
		"CanDelete":       c.AllowedTo(domain.Perm("delete_documents"), c.Project),
	})
}

// documentCustomValue は show のカスタムフィールドの表示（render_custom_field_values）。
// TODO: show_value（フィールド形式ごとの表示）の移植。
type documentCustomValue struct {
	Name, Value string
}

// DocumentsNew は documents#new。
func (a *App) DocumentsNew(c *Req) {
	f, err := a.buildDocument(c)
	if err != nil {
		a.internalError(c, "build document", err)
		return
	}
	f.assign(c)
	a.renderDocumentForm(c, "documents/new", f, nil)
}

// DocumentsCreate は documents#create。
func (a *App) DocumentsCreate(c *Req) {
	f, err := a.buildDocument(c)
	if err != nil {
		a.internalError(c, "build document", err)
		return
	}
	f.assign(c)
	res, err := a.saveContainerAttachments(c, paramAttachments(c))
	if err != nil {
		a.internalError(c, "save attachments", err)
		return
	}
	if err := a.validateDocument(c, f); err != nil {
		a.internalError(c, "validate document", err)
		return
	}
	if msg := res.FailedMessage(c.Loc); msg != "" {
		f.errs.AddMessage("base", msg)
	}
	if f.errs.Any() {
		a.renderDocumentForm(c, "documents/new", f, res.Files)
		return
	}
	d := f.Document
	d.CreatedAt = a.now()
	err = a.withTx(c, func(tx *db.Tx) error {
		if err := repository.InsertDocument(c.Ctx(), tx, d); err != nil {
			return err
		}
		f.id = d.ID
		if err := a.saveDocumentCustomValues(c, tx, f, true); err != nil {
			return err
		}
		return a.AttachmentStore.AttachSaved(c.Ctx(), tx, res, domain.AttachmentContainerDocument, d.ID)
	})
	if err != nil {
		a.internalError(c, "create document", err)
		return
	}
	a.notify(c, "document_added", "document_added", d)
	c.AttachFilesWarning(res)
	c.Flash().SetNotice(c.L("notice_successful_create"))
	c.Redirect("/projects/" + c.Project.Identifier + "/documents")
}

// DocumentsEdit は documents#edit。
func (a *App) DocumentsEdit(c *Req) {
	a.renderDocumentForm(c, "documents/edit", c.document(), nil)
}

// DocumentsUpdate は documents#update。
func (a *App) DocumentsUpdate(c *Req) {
	f := c.document()
	f.assign(c)
	if err := a.validateDocument(c, f); err != nil {
		a.internalError(c, "validate document", err)
		return
	}
	if f.errs.Any() {
		a.renderDocumentForm(c, "documents/edit", f, nil)
		return
	}
	err := a.withTx(c, func(tx *db.Tx) error {
		if err := repository.UpdateDocument(c.Ctx(), tx, f.Document); err != nil {
			return err
		}
		return a.saveDocumentCustomValues(c, tx, f, false)
	})
	if err != nil {
		a.internalError(c, "update document", err)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.Redirect("/documents/" + itoa(f.ID))
}

// DocumentsDestroy は documents#destroy。
func (a *App) DocumentsDestroy(c *Req) {
	f := c.document()
	var deleted []*domain.Attachment
	err := a.withTx(c, func(tx *db.Tx) error {
		var err error
		if deleted, err = repository.DeleteContainerAttachments(c.Ctx(), tx, domain.AttachmentContainerDocument, []int64{f.ID}); err != nil {
			return err
		}
		return repository.DeleteDocument(c.Ctx(), tx, f.ID)
	})
	if err != nil {
		a.internalError(c, "destroy document", err)
		return
	}
	a.deleteAttachmentsAfterCommit(c, deleted)
	c.Flash().SetNotice(c.L("notice_successful_delete"))
	c.Redirect("/projects/" + c.Project.Identifier + "/documents")
}

// DocumentsAddAttachment は documents#add_attachment。
func (a *App) DocumentsAddAttachment(c *Req) {
	f := c.document()
	res, err := a.AttachmentStore.AttachFiles(c.Ctx(), a.DB, domain.AttachmentContainerDocument, f.ID, paramAttachments(c), c.User, c.Loc)
	if err != nil {
		a.internalError(c, "add attachment", err)
		return
	}
	c.AttachFilesWarning(res)
	if len(res.Files) > 0 {
		a.notify(c, "document_added", "attachments_added", res.Files)
	}
	c.Redirect("/documents/" + itoa(f.ID))
}

var _ = time.Time{}
