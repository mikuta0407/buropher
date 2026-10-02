package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// CustomFieldEnumerationsController（app/controllers/custom_field_enumerations_controller.rb）。
var CustomFieldEnumerationsController = &Controller{Name: "custom_field_enumerations", MainMenu: false}

type cfEnumCtxKey struct{}

// findEnumCustomField は find_custom_field（params[:custom_field_id]）。
func (a *App) findEnumCustomField(c *Req) {
	cf, err := getCustomField(c.Ctx(), a.DB, httpx.RubyToI(c.Params().String("custom_field_id")))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.renderErr(c, "find custom field", err)
		}
		return
	}
	c.setCFForm(&cfForm{CF: cf, Errors: &customfield.Errors{}, env: a.cfEnv(c)})
}

// findCFEnumeration は find_enumeration（@custom_field.enumerations.find(params[:id])）。
func (a *App) findCFEnumeration(c *Req) {
	cf := c.cfFormOf().CF
	es, err := customfield.Enumerations(c.Ctx(), a.DB, cf.ID, false)
	if err != nil {
		a.renderErr(c, "find enumeration", err)
		return
	}
	id := httpx.RubyToI(c.Params().String("id"))
	for _, e := range es {
		if e.ID == id {
			c.R = c.R.WithContext(context.WithValue(c.R.Context(), cfEnumCtxKey{}, e))
			return
		}
	}
	c.Render404("")
}

// enumForm は CustomFieldEnumeration のエラー表示用モデル。
type enumForm struct {
	E      *customfield.Enumeration
	Errors *customfield.Errors
	env    *customfield.Env
}

// FullErrorMessages は errors.full_messages。
func (f *enumForm) FullErrorMessages() []string {
	return f.Errors.FullMessages(func(a string) string { return customfield.HumanAttributeName(f.env, a) })
}

// validateEnumeration は CustomFieldEnumeration の検証（validates_presence_of :name / validates_length_of :name, maximum: 60）。
func validateEnumeration(env *customfield.Env, e *customfield.Enumeration) *customfield.Errors {
	errs := &customfield.Errors{}
	if httpx.IsBlank(e.Name) {
		errs.Add("name", env.T("activerecord.errors.messages.blank"))
	}
	if utf8.RuneCountInString(e.Name) > 60 {
		errs.Add("name", env.T("activerecord.errors.messages.too_long", map[string]any{"count": 60}))
	}
	return errs
}

func (a *App) enumerationsIndexData(c *Req) (map[string]any, error) {
	form := c.cfFormOf()
	es, err := customfield.Enumerations(c.Ctx(), a.DB, form.CF.ID, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"CustomField": form, "Enumerations": es}, nil
}

// CustomFieldEnumerationsIndex は custom_field_enumerations#index。
func (a *App) CustomFieldEnumerationsIndex(c *Req) {
	data, err := a.enumerationsIndexData(c)
	if err != nil {
		a.renderErr(c, "enumerations index", err)
		return
	}
	c.Render("custom_field_enumerations/index", data, adminLayout)
}

// CustomFieldEnumerationsCreate は custom_field_enumerations#create（html はリダイレクト、js は create.js.erb）。
func (a *App) CustomFieldEnumerationsCreate(c *Req) {
	form := c.cfFormOf()
	p := c.Params().Map("custom_field_enumeration")
	if p == nil {
		// params.require(:custom_field_enumeration) → ActionController::ParameterMissing
		c.RenderError(http.StatusBadRequest, "")
		return
	}
	e := &customfield.Enumeration{CustomFieldID: form.CF.ID, Active: true}
	if v, ok := p.Get("name"); ok {
		e.Name = httpx.ValueString(v)
	}
	if v, ok := p.Get("active"); ok {
		e.Active = railsBool(v)
	}
	ef := &enumForm{E: e, Errors: validateEnumeration(form.env, e), env: form.env}
	if !ef.Errors.Any() {
		if err := customfield.CreateEnumeration(c.Ctx(), a.DB, e); err != nil {
			a.renderErr(c, "enumeration create", err)
			return
		}
	}
	if httpx.Format(c.R) != "js" {
		c.Redirect("/custom_fields/" + strconv.FormatInt(form.CF.ID, 10) + "/enumerations")
		return
	}
	js := "$('#errorExplanation').remove();\n\n"
	if !ef.Errors.Any() {
		data, err := a.enumerationsIndexData(c)
		if err != nil {
			a.renderErr(c, "enumeration create", err)
			return
		}
		out, err := a.Views.Render(c.ViewContext(), "custom_field_enumerations/index", data, view.RenderOptions{Format: "html", Layout: view.NoLayout})
		if err != nil {
			a.renderErr(c, "enumeration create.js", err)
			return
		}
		js += "$('#content').html('" + rails.EscapeJavascriptString(string(out)) + "');\n"
	} else {
		js += "$('form#add-element').prepend('" + rails.EscapeJavascriptString(string(helper.RenderErrorMessages(a.Helpers, &helper.Page{}, ef.FullErrorMessages()))) + "');\n"
	}
	js += "\n$('#custom_field_enumeration_name').focus();\n"
	httpx.SetContentType(c.W, "js", true)
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(js))
	c.Halt()
}

// CustomFieldEnumerationsUpdateEach は custom_field_enumerations#update_each（PUT /custom_fields/:id/enumerations）。
func (a *App) CustomFieldEnumerationsUpdateEach(c *Req) {
	form := c.cfFormOf()
	attrs := c.Params().Map("custom_field_enumerations")
	if attrs == nil {
		c.RenderError(http.StatusBadRequest, "")
		return
	}
	es, err := customfield.Enumerations(c.Ctx(), a.DB, form.CF.ID, false)
	if err != nil {
		a.renderErr(c, "enumerations update_each", err)
		return
	}
	byID := map[int64]*customfield.Enumeration{}
	for _, e := range es {
		byID[e.ID] = e
	}
	errRollback := errors.New("rollback")
	err = a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		var failed bool
		attrs.Each(func(id string, v any) {
			if failed {
				return
			}
			e := byID[httpx.RubyToI(id)]
			ap, ok := v.(*httpx.Params)
			if e == nil || !ok {
				return
			}
			ne := *e
			if x, ok := ap.Get("name"); ok {
				ne.Name = httpx.ValueString(x)
			}
			if x, ok := ap.Get("active"); ok {
				ne.Active = railsBool(x)
			}
			if x, ok := ap.Get("position"); ok {
				s := httpx.ValueString(x)
				if n, err := strconv.Atoi(s); err == nil {
					ne.Position = n
				} else {
					// validates_numericality_of :position, :only_integer => true
					failed = true
					return
				}
			}
			if validateEnumeration(form.env, &ne).Any() {
				failed = true
				return
			}
			if err := customfield.UpdateEnumeration(c.Ctx(), tx, &ne); err != nil {
				failed = true
			}
		})
		if failed {
			return errRollback
		}
		return nil
	})
	switch {
	case err == nil:
		c.Flash().SetNotice(c.L("notice_successful_update"))
	case errors.Is(err, errRollback):
	default:
		a.renderErr(c, "enumerations update_each", err)
		return
	}
	c.Redirect("/custom_fields/" + strconv.FormatInt(form.CF.ID, 10) + "/enumerations")
}

// CustomFieldEnumerationsDestroy は custom_field_enumerations#destroy（使用中で移行先が無ければ確認画面）。
func (a *App) CustomFieldEnumerationsDestroy(c *Req) {
	form := c.cfFormOf()
	value, _ := c.R.Context().Value(cfEnumCtxKey{}).(*customfield.Enumeration)
	es, err := customfield.Enumerations(c.Ctx(), a.DB, form.CF.ID, false)
	if err != nil {
		a.renderErr(c, "enumeration destroy", err)
		return
	}
	var reassign *customfield.Enumeration
	if s := c.Params().String("reassign_to_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			for _, e := range es {
				if e.ID == id {
					reassign = e
				}
			}
		}
	}
	count, err := customfield.EnumerationObjectsCount(c.Ctx(), a.DB, value)
	if err != nil {
		a.renderErr(c, "enumeration destroy", err)
		return
	}
	if reassign == nil && count > 0 {
		var others []*customfield.Enumeration
		for _, e := range es {
			if e.ID != value.ID {
				others = append(others, e)
			}
		}
		c.Render("custom_field_enumerations/destroy", map[string]any{
			"CustomField": form, "Value": value, "ObjectsCount": count, "Enumerations": others,
		}, adminLayout)
		return
	}
	err = a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		return customfield.DestroyEnumeration(c.Ctx(), tx, value, reassign)
	})
	if err != nil {
		a.renderErr(c, "enumeration destroy", err)
		return
	}
	c.Redirect("/custom_fields/" + strconv.FormatInt(form.CF.ID, 10) + "/enumerations")
}
