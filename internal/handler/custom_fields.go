// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// CustomFieldsController（app/controllers/custom_fields_controller.rb）。
var CustomFieldsController = &Controller{Name: "custom_fields", MainMenu: false}

// routesCustomFields は custom_fields / custom_field_enumerations のルートを登録する。
func (a *App) routesCustomFields(r Router) {
	// resources :custom_fields, :except => :show do
	//   resources :enumerations, :controller => 'custom_field_enumerations', :except => [:show, :new, :edit]
	//   put 'enumerations', :to => 'custom_field_enumerations#update_each'
	// end
	enumFind := Before(a.findEnumCustomField)
	a.Handle(r, http.MethodGet, "/custom_fields/{custom_field_id}/enumerations", CustomFieldEnumerationsController, "index", a.CustomFieldEnumerationsIndex, RequireAdmin(), enumFind)
	a.Handle(r, http.MethodPost, "/custom_fields/{custom_field_id}/enumerations", CustomFieldEnumerationsController, "create", a.CustomFieldEnumerationsCreate, RequireAdmin(), enumFind)
	for _, m := range []string{http.MethodPatch, http.MethodPut} {
		// update アクションは定義されていない（AbstractController::ActionNotFound → 404）
		a.Handle(r, m, "/custom_fields/{custom_field_id}/enumerations/{id}", CustomFieldEnumerationsController, "update", func(c *Req) { c.Render404("") }, RequireAdmin(), enumFind)
	}
	a.Handle(r, http.MethodDelete, "/custom_fields/{custom_field_id}/enumerations/{id}", CustomFieldEnumerationsController, "destroy", a.CustomFieldEnumerationsDestroy, RequireAdmin(), enumFind, Before(a.findCFEnumeration))
	a.Handle(r, http.MethodPut, "/custom_fields/{custom_field_id}/enumerations", CustomFieldEnumerationsController, "update_each", a.CustomFieldEnumerationsUpdateEach, RequireAdmin(), enumFind)

	a.Handle(r, http.MethodGet, "/custom_fields", CustomFieldsController, "index", a.CustomFieldsIndex, RequireAdmin(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/custom_fields", CustomFieldsController, "create", a.CustomFieldsCreate, RequireAdmin(), Before(a.buildNewCustomField))
	a.Handle(r, http.MethodGet, "/custom_fields/new", CustomFieldsController, "new", a.CustomFieldsNew, RequireAdmin(), Before(a.buildNewCustomField))
	a.Handle(r, http.MethodGet, "/custom_fields/{id}/edit", CustomFieldsController, "edit", a.CustomFieldsEdit, RequireAdmin(), Before(a.findCustomField))
	for _, m := range []string{http.MethodPatch, http.MethodPut} {
		a.Handle(r, m, "/custom_fields/{id}", CustomFieldsController, "update", a.CustomFieldsUpdate, RequireAdmin(), Before(a.findCustomField))
	}
	a.Handle(r, http.MethodDelete, "/custom_fields/{id}", CustomFieldsController, "destroy", a.CustomFieldsDestroy, RequireAdmin(), Before(a.findCustomField))
}

// ---------------------------------------------------------------- フォームのモデル

// cfForm はフォームビルダに渡すカスタムフィールド（@custom_field）。
// Redmine の属性名（format_store のアクセサを含む）を rails.Sender で返し、エラーと属性名の表示名も提供する。
type cfForm struct {
	CF     *customfield.CustomField
	Errors *customfield.Errors
	// ActiveEnumerations は custom_field.enumerations.active（enumeration 書式の既定値の選択肢）。
	ActiveEnumerations []*customfield.Enumeration
	// CopyFrom は @copy_from（コピー元。無ければ nil）。
	CopyFrom *customfield.CustomField
	// copyEnumerations はコピー元の選択肢（create 時に複製する）。
	copyEnumerations []*customfield.Enumeration
	env              *customfield.Env
}

func (f *cfForm) Send(method string) (any, bool) {
	cf := f.CF
	ptr := func(s *string) any {
		if s == nil {
			return nil
		}
		return *s
	}
	iptr := func(n *int) any {
		if n == nil {
			return nil
		}
		return *n
	}
	switch method {
	case "id":
		return cf.ID, true
	case "name":
		return cf.Name, true
	case "field_format":
		return cf.FieldFormat, true
	case "description":
		return ptr(cf.Description), true
	case "regexp":
		return ptr(cf.Regexp), true
	case "default_value":
		return ptr(cf.DefaultValue), true
	case "min_length":
		return iptr(cf.MinLength), true
	case "max_length":
		return iptr(cf.MaxLength), true
	case "multiple":
		return cf.Multiple, true
	case "is_required":
		return cf.IsRequired, true
	case "is_for_all":
		return cf.IsForAll, true
	case "is_filter":
		return cf.IsFilter, true
	case "searchable":
		return cf.Searchable, true
	case "editable":
		return cf.Editable, true
	case "visible":
		return cf.Visible, true
	case "position":
		return cf.Position, true
	case "persisted?":
		return cf.ID != 0, true
	case "url_pattern", "full_width_layout", "text_formatting", "edit_tag_style", "user_role", "version_status",
		"extensions_allowed", "thousands_delimiter", "ratio_interval", "default_value_mode":
		return cf.SettingValue(method), true
	}
	return nil, false
}

// ParamKey は model_name.param_key。
func (f *cfForm) ParamKey() string { return "custom_field" }

// Persisted は persisted?。
func (f *cfForm) Persisted() bool { return f.CF.ID != 0 }

// ToParam は to_param。
func (f *cfForm) ToParam() string { return strconv.FormatInt(f.CF.ID, 10) }

// ErrorsOn は errors[attr]。
func (f *cfForm) ErrorsOn(attr string) []string { return f.Errors.On(attr) }

// HumanAttributeName は CustomField.human_attribute_name。
func (f *cfForm) HumanAttributeName(attr string) string {
	return customfield.HumanAttributeName(f.env, attr)
}

// FullErrorMessages は errors.full_messages（error_messages_for 用）。
func (f *cfForm) FullErrorMessages() []string {
	return f.Errors.FullMessages(func(a string) string { return customfield.HumanAttributeName(f.env, a) })
}

// Format は custom_field.format。
func (f *cfForm) Format() *customfield.Format { return customfield.FindFormat(f.CF.FieldFormat) }

// FormatInfo は format のクラス属性。
func (f *cfForm) FormatInfo() *customfield.Format { return f.Format() }

// ClassName は custom_field.class.name。
func (f *cfForm) ClassName() string { return f.CF.ClassName() }

// IsNewRecord は new_record?。
func (f *cfForm) IsNewRecord() bool { return f.CF.ID == 0 }

// SettingListIncludes は custom_field.<key>.is_a?(Array) && include?(v)（user_role / version_status）。
func (f *cfForm) SettingListIncludes(key, v string) bool {
	return slices.Contains(f.CF.SettingList(key), v)
}

// SettingBlank は custom_field.<key>.blank?。
func (f *cfForm) SettingBlank(key string) bool {
	if l := f.CF.SettingList(key); l != nil {
		return len(l) == 0
	}
	return httpx.IsBlank(f.CF.Setting(key))
}

// PossibleValuesText は possible_values.to_a.join("\n")。
func (f *cfForm) PossibleValuesText() string {
	s := ""
	for i, v := range f.CF.PossibleValues {
		if i > 0 {
			s += "\n"
		}
		s += v
	}
	return s
}

// PossibleValuesOptions は custom_field.possible_values_options（bool 書式の既定値の選択肢）。
func (f *cfForm) PossibleValuesOptions() []any {
	var out []any
	for _, o := range customfield.PossibleValuesOptionsFor(f.env, f.CF, nil) {
		out = append(out, o.Pair())
	}
	return out
}

// RatioIntervalSelected は progressbar の ratio_interval の選択値（新規なら既定の刻み）。
func (f *cfForm) RatioIntervalSelected(def int) any {
	if f.IsNewRecord() {
		return def
	}
	return f.CF.SettingValue("ratio_interval")
}

// cfEnv はカスタムフィールドの書式が使う環境。
func (a *App) cfEnv(c *Req) *customfield.Env {
	env := &customfield.Env{
		T:              c.L,
		NormalizeFloat: c.Loc.NormalizeFloat,
		DoneRatioInterval: func() int {
			n, _ := strconv.Atoi(a.Settings.String("issue_done_ratio_interval"))
			return n
		},
		Enumerations: func(cfID int64, activeOnly bool) []*customfield.Enumeration {
			es, err := customfield.Enumerations(c.Ctx(), a.DB, cfID, activeOnly)
			if err != nil {
				a.logger().Error("custom field enumerations", "err", err)
			}
			return es
		},
		ActionName: c.Action,
	}
	if c.User != nil {
		env.CurrentUserID = c.User.ID
	}
	// TODO(customfield): ProjectUsers / SharedVersions / SystemVersions / RecordOptions はユーザー・バージョンの
	// リポジトリが揃った時点で設定する（管理画面では user / version 書式の既定値の選択肢にのみ影響する）。
	return env
}

// ---------------------------------------------------------------- before_action

type cfFormCtxKey struct{}

// setCFForm は @custom_field を設定する（before_action からアクションへ渡す）。
func (c *Req) setCFForm(f *cfForm) {
	c.R = c.R.WithContext(context.WithValue(c.R.Context(), cfFormCtxKey{}, f))
}

// cfFormOf は @custom_field。
func (c *Req) cfFormOf() *cfForm {
	f, _ := c.R.Context().Value(cfFormCtxKey{}).(*cfForm)
	return f
}

// buildNewCustomField は build_new_custom_field（種類が不正なら select_type を描画する）。
func (a *App) buildNewCustomField(c *Req) {
	p := c.Params()
	typ := customfield.TypeByClass(p.String("type"))
	if typ == nil {
		a.renderSelectType(c)
		return
	}
	cf := &customfield.CustomField{OwnerKind: typ.Kind, Regexp: strPtr(""), Editable: true, Visible: true,
		PossibleValues: []string{}, Settings: map[string]any{}}
	form := &cfForm{CF: cf, Errors: &customfield.Errors{}, env: a.cfEnv(c)}
	if p.Present("copy") {
		if src, err := getCustomField(c.Ctx(), a.DB, httpx.RubyToI(p.String("copy"))); err == nil {
			a.copyCustomFieldFrom(c, form, src)
		} else if !errors.Is(err, repository.ErrNotFound) {
			a.renderErr(c, "custom field copy", err)
			return
		}
	}
	assignCustomFieldAttributes(form.env, cf, p.Map("custom_field"))
	c.setCFForm(form)
}

// copyCustomFieldFrom は CustomField#copy_from。
func (a *App) copyCustomFieldFrom(c *Req, form *cfForm, src *customfield.CustomField) {
	cf := form.CF
	owner := cf.OwnerKind
	*cf = *src
	cf.ID, cf.Name, cf.Position = 0, "", 0
	cf.OwnerKind = owner
	cf.PossibleValues = slices.Clone(src.PossibleValues)
	cf.Settings = map[string]any{}
	for k, v := range src.Settings {
		cf.Settings[k] = v
	}
	es, err := customfield.Enumerations(c.Ctx(), a.DB, src.ID, false)
	if err != nil {
		a.logger().Error("custom field enumerations", "err", err)
	}
	form.copyEnumerations = es
	if len(es) > 0 {
		cf.DefaultValue = nil
	}
	cf.RoleIDs, cf.TrackerIDs, cf.ProjectIDs = nil, nil, nil
	switch owner {
	case customfield.KindIssue, customfield.KindTimeEntry, customfield.KindProject, customfield.KindVersion:
		cf.RoleIDs = slices.Clone(src.RoleIDs)
	}
	if owner == customfield.KindIssue {
		cf.TrackerIDs = slices.Clone(src.TrackerIDs)
		cf.ProjectIDs = slices.Clone(src.ProjectIDs)
	}
	form.CopyFrom = src
}

// findCustomField は find_custom_field。
func (a *App) findCustomField(c *Req) {
	cf, err := getCustomField(c.Ctx(), a.DB, httpx.RubyToI(c.Params().String("id")))
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

func strPtr(s string) *string { return &s }

// ---------------------------------------------------------------- safe_attributes

var cfSettingKeys = []string{"url_pattern", "text_formatting", "edit_tag_style", "user_role", "version_status",
	"extensions_allowed", "full_width_layout", "thousands_delimiter", "ratio_interval", "default_value_mode"}

// railsBool は ActiveModel::Type::Boolean のキャスト（"0", "f", "false", "off", "" などは偽）。
func railsBool(v any) bool {
	switch s := httpx.ValueString(v); s {
	case "", "0", "f", "F", "false", "FALSE", "off", "OFF":
		return false
	}
	return true
}

// railsInt は ActiveModel::Type::Integer のキャスト（空なら nil）。
func railsInt(v any) *int {
	s := httpx.ValueString(v)
	if httpx.IsBlank(s) {
		return nil
	}
	n := int(httpx.RubyToI(s))
	return &n
}

// paramIDList は *_ids= の値（空文字を除いた id）。
func paramIDList(v any) []int64 {
	var out []int64
	for _, s := range valueStrings(v) {
		if s == "" {
			continue
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func valueStrings(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, httpx.ValueString(e))
		}
		return out
	default:
		return []string{httpx.ValueString(x)}
	}
}

// assignCustomFieldAttributes は custom_field.safe_attributes = params[:custom_field]。
func assignCustomFieldAttributes(env *customfield.Env, cf *customfield.CustomField, p *httpx.Params) {
	if p == nil {
		return
	}
	p.Each(func(key string, v any) {
		switch key {
		case "name":
			cf.Name = httpx.ValueString(v)
		case "field_format":
			// field_format= は new_record? のときのみ
			if cf.ID == 0 {
				cf.FieldFormat = httpx.ValueString(v)
			}
		case "possible_values":
			if a, ok := v.([]any); ok {
				customfield.SetPossibleValues(cf, a)
			} else {
				customfield.SetPossibleValues(cf, httpx.ValueString(v))
			}
		case "regexp":
			cf.Regexp = strPtr(httpx.ValueString(v))
		case "min_length":
			cf.MinLength = railsInt(v)
		case "max_length":
			cf.MaxLength = railsInt(v)
		case "is_required":
			cf.IsRequired = railsBool(v)
		case "is_for_all":
			cf.IsForAll = railsBool(v)
		case "is_filter":
			cf.IsFilter = railsBool(v)
		case "position":
			if n := railsInt(v); n != nil {
				cf.Position = *n
			}
		case "searchable":
			cf.Searchable = railsBool(v)
		case "default_value":
			cf.DefaultValue = strPtr(httpx.ValueString(v))
		case "editable":
			cf.Editable = railsBool(v)
		case "visible":
			cf.Visible = railsBool(v)
		case "multiple":
			cf.Multiple = railsBool(v)
		case "description":
			cf.Description = strPtr(httpx.ValueString(v))
		case "role_ids":
			cf.RoleIDs = paramIDList(v)
		case "tracker_ids":
			if cf.OwnerKind == customfield.KindIssue {
				cf.TrackerIDs = paramIDList(v)
			}
		case "project_ids":
			if cf.OwnerKind == customfield.KindIssue {
				cf.ProjectIDs = paramIDList(v)
			}
		default:
			if slices.Contains(cfSettingKeys, key) {
				if a, ok := v.([]any); ok {
					list := make([]any, 0, len(a))
					for _, e := range a {
						list = append(list, httpx.ValueString(e))
					}
					cf.SetSetting(key, list)
				} else {
					cf.SetSetting(key, httpx.ValueString(v))
				}
			}
		}
	})
}

// ---------------------------------------------------------------- 保存

// saveCustomField は CustomField#save（検証・before_save・acts_as_positioned・関連・after_save）。
// 検証に失敗したら false（form.Errors にエラー）。
func (a *App) saveCustomField(c *Req, form *cfForm, before *customfield.CustomField) (bool, error) {
	cf := form.CF
	env := form.env
	customfield.ApplyFieldRules(cf)
	taken, err := customfield.NameTaken(c.Ctx(), a.DB, cf.OwnerKind, cf.Name, cf.ID)
	if err != nil {
		return false, err
	}
	form.Errors = customfield.ValidateField(env, cf, taken)
	if form.Errors.Any() {
		return false, nil
	}
	customfield.FindFormat(cf.FieldFormat).BeforeSave(env, cf)
	err = a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		isNew := cf.ID == 0
		var oldPos int
		if before != nil {
			oldPos = before.Position
		}
		posGiven := cf.Position != 0
		if cf.Position == 0 {
			// set_default_position
			m, err := customfield.MaxPosition(c.Ctx(), tx, cf.OwnerKind)
			if err != nil {
				return err
			}
			if isNew {
				cf.Position = m + 1
			} else {
				cf.Position = m
			}
		}
		if before != nil && before.Visible != cf.Visible && cf.Visible {
			// after_save: 表示を「すべて」に変えたらロールを外す
			cf.RoleIDs = nil
		}
		if err := customfield.Save(c.Ctx(), tx, cf); err != nil {
			return err
		}
		// update_position
		switch {
		case isNew:
			if err := customfield.InsertPosition(c.Ctx(), tx, cf.OwnerKind, cf.Position, cf.ID); err != nil {
				return err
			}
		case posGiven && oldPos != cf.Position:
			if err := customfield.ShiftPositions(c.Ctx(), tx, cf.OwnerKind, cf.ID, oldPos, cf.Position); err != nil {
				return err
			}
		}
		if isNew {
			for _, e := range form.copyEnumerations {
				ne := &customfield.Enumeration{CustomFieldID: cf.ID, Name: e.Name, Active: e.Active}
				if err := customfield.CreateEnumeration(c.Ctx(), tx, ne); err != nil {
					return err
				}
			}
		}
		if before != nil && before.Multiple && !cf.Multiple {
			// handle_multiplicity_change
			if err := customfield.DeleteDuplicateValues(c.Ctx(), tx, cf.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return err == nil, err
}

// ---------------------------------------------------------------- actions

// customFieldsByType は CustomField.all.group_by {|f| f.class.name}。
func (a *App) customFieldsByType(c *Req) (map[string][]*customfield.CustomField, error) {
	cfs, err := customfield.Load(c.Ctx(), a.DB, "")
	if err != nil {
		return nil, err
	}
	out := map[string][]*customfield.CustomField{}
	for _, cf := range cfs {
		out[cf.ClassName()] = append(out[cf.ClassName()], cf)
	}
	return out, nil
}

// CustomFieldsIndex は custom_fields#index（GET /custom_fields）。
func (a *App) CustomFieldsIndex(c *Req) {
	if httpx.IsAPIRequest(c.R) {
		a.renderCustomFieldsAPI(c)
		return
	}
	byType, err := a.customFieldsByType(c)
	if err != nil {
		a.renderErr(c, "custom fields index", err)
		return
	}
	counts, err := customfield.IssueProjectCounts(c.Ctx(), a.DB)
	if err != nil {
		a.renderErr(c, "custom fields index", err)
		return
	}
	var tabs []helper.Tab
	for _, t := range customfield.Types {
		if _, ok := byType[t.ClassName]; ok {
			tabs = append(tabs, helper.Tab{Name: t.ClassName, Partial: "custom_fields/index", Label: t.TabLabel})
		}
	}
	c.Render("custom_fields/index", map[string]any{
		"CustomFieldsByType": byType,
		"ProjectsCount":      counts,
		"Tabs":               tabs,
	}, adminLayout)
}

// renderSelectType は render :action => 'select_type'。
func (a *App) renderSelectType(c *Req) {
	c.Render("custom_fields/select_type", map[string]any{"CustomField": nil}, adminLayout)
}

// cfFormData は new / edit の表示データ。
func (a *App) cfFormData(c *Req, form *cfForm) (map[string]any, error) {
	if form.CF.FieldFormat == "enumeration" && form.CF.ID != 0 {
		es, err := customfield.Enumerations(c.Ctx(), a.DB, form.CF.ID, true)
		if err != nil {
			return nil, err
		}
		form.ActiveEnumerations = es
	}
	data := map[string]any{"CustomField": form}
	needs := form.CF.FieldFormat == "user"
	switch form.CF.OwnerKind {
	case customfield.KindIssue, customfield.KindTimeEntry, customfield.KindProject, customfield.KindVersion:
		needs = true
	}
	if needs {
		roles, err := repository.GivableRoles(c.Ctx(), a.DB)
		if err != nil {
			return nil, err
		}
		data["GivableRoles"] = roles
	}
	if form.CF.OwnerKind == customfield.KindIssue {
		ts, err := repository.ListTrackers(c.Ctx(), a.DB)
		if err != nil {
			return nil, err
		}
		data["Trackers"] = ts
		projects, err := repository.ListProjects(c.Ctx(), a.DB)
		if err != nil {
			return nil, err
		}
		ns, err := repository.ProjectNestedSet(c.Ctx(), a.DB)
		if err != nil {
			return nil, err
		}
		nested := make([]helper.NestedProject, len(projects))
		for i, p := range projects {
			nested[i] = helper.NestedProject{Project: p, Lft: ns[p.ID].Lft, Rgt: ns[p.ID].Rgt}
		}
		slices.SortStableFunc(nested, func(x, y helper.NestedProject) int { return x.Lft - y.Lft })
		data["Projects"] = nested
	}
	data["DefaultRatioInterval"] = form.env.DoneRatioInterval()
	return data, nil
}

func (a *App) renderCustomFieldForm(c *Req, action string, form *cfForm, status int) {
	data, err := a.cfFormData(c, form)
	if err != nil {
		a.renderErr(c, "custom field form", err)
		return
	}
	c.Render("custom_fields/"+action, data, RenderOptions{Layout: "admin", Status: status})
}

// CustomFieldsNew は custom_fields#new（GET /custom_fields/new(.js)）。
func (a *App) CustomFieldsNew(c *Req) {
	form := c.cfFormOf()
	if form.CF.FieldFormat == "" {
		form.CF.FieldFormat = "string"
	}
	form.CF.DefaultValue = nil
	if httpx.Format(c.R) == "js" {
		// new.js.erb: $('#content').html('<%= escape_javascript(render :template => 'custom_fields/new', :layout => nil, :formats => [:html]) %>')
		data, err := a.cfFormData(c, form)
		if err != nil {
			a.renderErr(c, "custom field new", err)
			return
		}
		out, err := a.Views.Render(c.ViewContext(), "custom_fields/new", data, view.RenderOptions{Format: "html", Layout: view.NoLayout})
		if err != nil {
			a.renderErr(c, "custom field new.js", err)
			return
		}
		c.WriteJS("$('#content').html('" + rails.EscapeJavascriptString(string(out)) + "')\n")
		return
	}
	a.renderCustomFieldForm(c, "new", form, 0)
}

// CustomFieldsCreate は custom_fields#create（POST /custom_fields）。
func (a *App) CustomFieldsCreate(c *Req) {
	form := c.cfFormOf()
	ok, err := a.saveCustomField(c, form, nil)
	if err != nil {
		a.renderErr(c, "custom field create", err)
		return
	}
	if !ok {
		a.renderCustomFieldForm(c, "new", form, 0)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_create"))
	typ := form.CF.ClassName()
	if c.Params().Has("continue") {
		c.Redirect("/custom_fields/new?type=" + url.QueryEscape(typ))
		return
	}
	c.Redirect("/custom_fields?tab=" + url.QueryEscape(typ))
}

// CustomFieldsEdit は custom_fields#edit（GET /custom_fields/:id/edit）。
func (a *App) CustomFieldsEdit(c *Req) {
	a.renderCustomFieldForm(c, "edit", c.cfFormOf(), 0)
}

// CustomFieldsUpdate は custom_fields#update（PUT/PATCH /custom_fields/:id(.js)）。
func (a *App) CustomFieldsUpdate(c *Req) {
	form := c.cfFormOf()
	before := *form.CF
	assignCustomFieldAttributes(form.env, form.CF, c.Params().Map("custom_field"))
	ok, err := a.saveCustomField(c, form, &before)
	if err != nil {
		a.renderErr(c, "custom field update", err)
		return
	}
	isJS := httpx.Format(c.R) == "js"
	switch {
	case ok && isJS:
		httpx.Head(c.W, c.R, http.StatusOK)
		c.Halt()
	case ok:
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.RedirectBackOrDefault("/custom_fields/"+strconv.FormatInt(form.CF.ID, 10)+"/edit", false)
	case isJS:
		httpx.Head(c.W, c.R, http.StatusUnprocessableEntity)
		c.Halt()
	default:
		a.renderCustomFieldForm(c, "edit", form, 0)
	}
}

// CustomFieldsDestroy は custom_fields#destroy（DELETE /custom_fields/:id）。
func (a *App) CustomFieldsDestroy(c *Req) {
	cf := c.cfFormOf().CF
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if err := customfield.Delete(c.Ctx(), tx, cf.ID); err != nil {
			return err
		}
		// after_destroy :remove_position
		return customfield.RemovePosition(c.Ctx(), tx, cf.OwnerKind, cf.Position, cf.ID)
	})
	if err != nil {
		a.logger().Error("custom field destroy", "err", err)
		c.Flash().SetError(c.L("error_can_not_delete_custom_field"))
	} else {
		c.Flash().SetNotice(c.L("notice_successful_delete"))
	}
	c.Redirect("/custom_fields?tab=" + url.QueryEscape(cf.ClassName()))
}

// CustomFieldModel は helper.CustomFieldModel。
func (f *cfForm) CustomFieldModel() *customfield.CustomField { return f.CF }

// DefaultValueModeOrFixed は @custom_field.default_value_mode.presence || 'fixed_date'（_date.html.erb）。
func (f *cfForm) DefaultValueModeOrFixed() string {
	if m := f.CF.DefaultValueMode(); m != "" {
		return m
	}
	return "fixed_date"
}

// DefaultValueForMode は _date.html.erb の fixed_date_default_value / date_offset_default_value
// （モードが一致すれば保存値 @custom_field[:default_value]、そうでなければ nil）。
func (f *cfForm) DefaultValueForMode(mode string) any {
	if f.DefaultValueModeOrFixed() != mode {
		return nil
	}
	return f.DefaultValueAttr()
}

// DefaultValueAttr は default_value（nil は nil）。
func (f *cfForm) DefaultValueAttr() any {
	v, _ := f.Send("default_value")
	return v
}

// getCustomField は CustomField.find(id)（無ければ repository.ErrNotFound）。
func getCustomField(ctx context.Context, q db.Queryer, id int64) (*customfield.CustomField, error) {
	cf, err := customfield.Get(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if cf == nil {
		return nil, repository.ErrNotFound
	}
	return cf, nil
}
