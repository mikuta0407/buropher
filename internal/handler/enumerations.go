package handler

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// EnumerationsController（app/controllers/enumerations_controller.rb）。
var EnumerationsController = &Controller{Name: "enumerations", MainMenu: false}

// routesEnumerations は enumerations コントローラのルートを登録する。
//
//	resources :enumerations, :except => :show
//	match 'enumerations/:type', :to => 'enumerations#index', :via => :get
func (a *App) routesEnumerations(r Router) {
	// before_action :require_admin, :except => :index
	// before_action :require_admin_or_api_request, :only => :index
	// before_action :build_new_enumeration, :only => [:new, :create]
	// before_action :find_enumeration, :only => [:edit, :update, :destroy]
	// accept_api_auth :index
	build := Before(a.buildNewEnumeration)
	find := Before(a.findEnumeration)
	a.Handle(r, http.MethodGet, "/enumerations", EnumerationsController, "index", a.EnumerationsIndex, RequireAdminOrAPIRequest(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/enumerations", EnumerationsController, "create", a.EnumerationsCreate, RequireAdmin(), build)
	a.Handle(r, http.MethodGet, "/enumerations/new", EnumerationsController, "new", a.EnumerationsNew, RequireAdmin(), build)
	a.Handle(r, http.MethodGet, "/enumerations/{id}/edit", EnumerationsController, "edit", a.EnumerationsEdit, RequireAdmin(), find)
	a.Handle(r, http.MethodPatch, "/enumerations/{id}", EnumerationsController, "update", a.EnumerationsUpdate, RequireAdmin(), find)
	a.Handle(r, http.MethodPut, "/enumerations/{id}", EnumerationsController, "update", a.EnumerationsUpdate, RequireAdmin(), find)
	a.Handle(r, http.MethodDelete, "/enumerations/{id}", EnumerationsController, "destroy", a.EnumerationsDestroy, RequireAdmin(), find)
	// GET enumerations/:type（パスパラメータ名は PUT / DELETE と揃えて {id}）
	a.Handle(r, http.MethodGet, "/enumerations/{id}", EnumerationsController, "index", a.EnumerationsIndex, RequireAdminOrAPIRequest(), AcceptAPIAuth())
}

const ctxEnumeration = "enumeration"

// enumerationForm は enumerations/_form のモデル（@enumeration）。
type enumerationForm struct {
	formModel
	*domain.Enumeration
	cfs    []*domain.CustomFieldInfo
	values map[int64][]string
	// given はフォームで値が渡されたカスタムフィールド。
	given map[int64]bool
}

// Type は enumeration[type] の値（STI のクラス名）。
func (f *enumerationForm) Type() string { return f.Kind }

// CustomFieldValues は @enumeration.custom_field_values。
func (f *enumerationForm) CustomFieldValues() []*domain.CustomFieldValue {
	out := make([]*domain.CustomFieldValue, len(f.cfs))
	for i, cf := range f.cfs {
		vs, ok := f.values[cf.ID]
		if !ok && f.Enumeration.ID == 0 && cf.DefaultValue != nil {
			vs = []string{*cf.DefaultValue}
		}
		out[i] = &domain.CustomFieldValue{Field: cf, Values: vs}
	}
	return out
}

// buildNewEnumeration は EnumerationsController#build_new_enumeration。
func (a *App) buildNewEnumeration(c *Req) {
	p := c.Params()
	className := p.String("enumeration", "type")
	if className == "" {
		className = p.String("type")
	}
	kind := ""
	switch className {
	case domain.EnumDocumentCategory, domain.EnumIssuePriority, domain.EnumTimeEntryActivity:
		kind = className
	}
	if kind == "" {
		c.Render404("")
		return
	}
	f, err := a.newEnumerationForm(c, &domain.Enumeration{Kind: kind, Active: true})
	if err != nil {
		a.internalError(c, "new enumeration", err)
		return
	}
	f.assign(p.Map("enumeration"))
	c.setLocal(ctxEnumeration, f)
}

// findEnumeration は EnumerationsController#find_enumeration。
func (a *App) findEnumeration(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	e, err := repository.GetEnumeration(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find enumeration", err)
		}
		return
	}
	f, err := a.newEnumerationForm(c, e)
	if err != nil {
		a.internalError(c, "enumeration form", err)
		return
	}
	c.setLocal(ctxEnumeration, f)
}

func (a *App) newEnumerationForm(c *Req, e *domain.Enumeration) (*enumerationForm, error) {
	cfs, err := repository.CustomFieldInfosByKind(c.Ctx(), a.DB, domain.EnumerationCustomFieldKind(e.Kind))
	if err != nil {
		return nil, err
	}
	f := &enumerationForm{formModel: newFormModel(c, "enumeration", e.ID), Enumeration: e, cfs: cfs,
		values: map[int64][]string{}, given: map[int64]bool{}}
	if e.ID != 0 {
		if f.values, err = repository.CustomValues(c.Ctx(), a.DB, "enumeration", e.ID); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// assign は enumeration_params（name, active, is_default, position, custom_field_values）の代入。
func (f *enumerationForm) assign(p *httpx.Params) {
	if p == nil {
		return
	}
	e := f.Enumeration
	if v, ok := p.StringOK("name"); ok {
		e.Name = v
	}
	if v, ok := p.StringOK("active"); ok {
		e.Active = castBool(v)
	}
	if v, ok := p.StringOK("is_default"); ok {
		e.IsDefault = castBool(v)
	}
	if v, ok := p.StringOK("position"); ok {
		e.Position = int(httpx.RubyToI(v))
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

func (a *App) validateEnumeration(c *Req, f *enumerationForm) error {
	f.errs = &domain.ValidationErrors{}
	e := f.Enumeration
	if strings.TrimSpace(e.Name) == "" {
		f.errs.Add("name", "blank", nil)
	}
	taken, err := repository.EnumerationNameTaken(c.Ctx(), a.DB, e)
	if err != nil {
		return err
	}
	if taken {
		f.errs.Add("name", "taken", nil)
	}
	if len([]rune(e.Name)) > 30 {
		f.errs.Add("name", "too_long", map[string]any{"count": 30})
	}
	return nil
}

// saveEnumeration は列挙を保存する（check_default → 保存 → update_position → update_children_name →
// カスタム値 → IssuePriority の compute_position_names）。
func (a *App) saveEnumeration(c *Req, f *enumerationForm, orig *domain.Enumeration) error {
	return a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		ctx := c.Ctx()
		e := f.Enumeration
		isNew := e.ID == 0
		scope := repository.EnumerationPositionScope(e)
		if e.IsDefault && (isNew || !orig.IsDefault) {
			if err := repository.ClearEnumerationDefaults(ctx, tx, e.Kind); err != nil {
				return err
			}
		}
		if isNew && e.Position == 0 {
			pos, err := repository.NextPosition(ctx, tx, scope)
			if err != nil {
				return err
			}
			e.Position = pos
		}
		if err := repository.SaveEnumeration(ctx, tx, e); err != nil {
			return err
		}
		f.id = e.ID
		positionChanged := isNew || orig.Position != e.Position
		if isNew {
			if err := repository.InsertPosition(ctx, tx, scope, e.ID, e.Position); err != nil {
				return err
			}
		} else if orig.Position != e.Position {
			if err := repository.ShiftPositions(ctx, tx, scope, e.ID, orig.Position, e.Position); err != nil {
				return err
			}
		}
		if e.Kind == domain.EnumTimeEntryActivity && e.ParentID == nil {
			if positionChanged {
				if err := repository.SyncActivityOverridePositions(ctx, tx); err != nil {
					return err
				}
			}
			if !isNew && orig.Name != e.Name {
				if err := repository.UpdateActivityChildrenName(ctx, tx, e.ID, orig.Name, e.Name); err != nil {
					return err
				}
			}
		}
		// save_custom_field_values: 利用可能なカスタムフィールドすべての値を保存する
		existing := map[int64][]string{}
		if !isNew {
			var err error
			if existing, err = repository.CustomValues(ctx, tx, "enumeration", e.ID); err != nil {
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
			if err := repository.SetCustomValues(ctx, tx, "enumeration", e.ID, cf.ID, vs); err != nil {
				return err
			}
		}
		if e.Kind == domain.EnumIssuePriority &&
			(isNew || orig.Position != e.Position || orig.Active != e.Active || orig.IsDefault != e.IsDefault) {
			return repository.ComputePriorityPositionNames(ctx, tx)
		}
		return nil
	})
}

// ---------------------------------------------------------------- アクション

// enumerationGroup は index の 1 種類分。
type enumerationGroup struct {
	Kind         string
	OptionName   string
	Enumerations []*domain.Enumeration
}

// EnumerationsIndex は enumerations#index（GET /enumerations, /enumerations/:type）。
func (a *App) EnumerationsIndex(c *Req) {
	switch httpx.Negotiate(c.R, "html", "xml", "json") {
	case "html":
		var groups []enumerationGroup
		for _, k := range domain.EnumerationKinds {
			es, err := repository.ListEnumerations(c.Ctx(), a.DB, k, true)
			if err != nil {
				a.internalError(c, "list enumerations", err)
				return
			}
			groups = append(groups, enumerationGroup{Kind: k, OptionName: domain.EnumerationOptionName(k), Enumerations: es})
		}
		c.renderAdmin("enumerations/index", map[string]any{"Groups": groups}, false)
	case "xml", "json":
		kind := domain.EnumerationKindOf(c.Params().String("id"))
		if kind == "" {
			c.Render404("")
			return
		}
		es, err := repository.ListEnumerations(c.Ctx(), a.DB, kind, true)
		if err != nil {
			a.internalError(c, "list enumerations", err)
			return
		}
		a.renderEnumerationsAPI(c, kind, es)
	default:
		c.unknownFormat()
	}
}

// EnumerationsNew は enumerations#new。
func (a *App) EnumerationsNew(c *Req) {
	c.renderAdmin("enumerations/new", map[string]any{"Enumeration": c.local(ctxEnumeration)}, false)
}

// EnumerationsCreate は enumerations#create。
func (a *App) EnumerationsCreate(c *Req) {
	f := c.local(ctxEnumeration).(*enumerationForm)
	if err := a.validateEnumeration(c, f); err != nil {
		a.internalError(c, "validate enumeration", err)
		return
	}
	if !f.errs.Any() {
		if err := a.saveEnumeration(c, f, nil); err != nil {
			a.internalError(c, "save enumeration", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_create"))
		c.Redirect("/enumerations")
		return
	}
	c.renderAdmin("enumerations/new", map[string]any{"Enumeration": f}, false)
}

// EnumerationsEdit は enumerations#edit。
func (a *App) EnumerationsEdit(c *Req) {
	c.renderAdmin("enumerations/edit", map[string]any{"Enumeration": c.local(ctxEnumeration)}, false)
}

// EnumerationsUpdate は enumerations#update（並べ替えの XHR は format.js で head :ok）。
func (a *App) EnumerationsUpdate(c *Req) {
	f := c.local(ctxEnumeration).(*enumerationForm)
	orig := *f.Enumeration
	cp := orig
	f.Enumeration = &cp
	f.assign(c.Params().Map("enumeration"))
	format := httpx.Negotiate(c.R, "html", "js")
	if err := a.validateEnumeration(c, f); err != nil {
		a.internalError(c, "validate enumeration", err)
		return
	}
	if !f.errs.Any() {
		if err := a.saveEnumeration(c, f, &orig); err != nil {
			a.internalError(c, "save enumeration", err)
			return
		}
		if format == "js" {
			c.head(http.StatusOK)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.Redirect("/enumerations")
		return
	}
	if format == "js" {
		c.head(http.StatusUnprocessableEntity)
		return
	}
	c.renderAdmin("enumerations/edit", map[string]any{"Enumeration": f}, false)
}

// EnumerationsDestroy は enumerations#destroy（使用中なら付け替え先を選ぶ画面）。
func (a *App) EnumerationsDestroy(c *Req) {
	ctx := c.Ctx()
	f := c.local(ctxEnumeration).(*enumerationForm)
	e := f.Enumeration
	count, err := repository.EnumerationObjectsCount(ctx, a.DB, e)
	if err != nil {
		a.internalError(c, "enumeration objects count", err)
		return
	}
	var reassignTo *domain.Enumeration
	if count != 0 {
		if v := c.Params().String("reassign_to_id"); strings.TrimSpace(v) != "" {
			if to, err := repository.GetEnumerationOfKind(ctx, a.DB, e.Kind, httpx.RubyToI(v)); err == nil {
				reassignTo = to
			}
		}
	}
	if count == 0 || reassignTo != nil {
		err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
			if reassignTo != nil {
				if err := repository.TransferEnumerationRelations(ctx, tx, e, reassignTo); err != nil {
					return err
				}
			}
			return repository.DestroyEnumeration(ctx, tx, e)
		})
		if err != nil {
			a.internalError(c, "destroy enumeration", err)
			return
		}
		c.Redirect("/enumerations")
		return
	}
	others, err := repository.ListEnumerations(ctx, a.DB, e.Kind, true)
	if err != nil {
		a.internalError(c, "list enumerations", err)
		return
	}
	others = slices.DeleteFunc(others, func(x *domain.Enumeration) bool { return x.ID == e.ID })
	c.renderAdmin("enumerations/destroy", map[string]any{
		"Enumeration":  f,
		"ObjectsCount": count,
		"Enumerations": others,
		"FormPath":     "/enumerations/" + strconv.FormatInt(e.ID, 10),
	}, false)
}
