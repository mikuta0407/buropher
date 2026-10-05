// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import (
	"regexp"
	"strings"
	"time"
)

// フォームビルダが扱うモデルは任意の値で、属性値は Send(obj, "attr") で取得する。
// 以下のインターフェースを実装すると Rails / Redmine の挙動をより正確に再現できる。

// ParamKeyer はフォームのスコープ名（model_name.param_key、例 "issue"）を返す。
type ParamKeyer interface{ ParamKey() string }

// Persistable は persisted?（保存済みなら edit_xxx / PATCH になる）。
type Persistable interface{ Persisted() bool }

// ToParamer は to_param（dom_id に使う ID 文字列）。
type ToParamer interface{ ToParam() string }

// ErrorReporter は属性ごとのエラーメッセージ（errors[attr]）。
type ErrorReporter interface{ ErrorsOn(attr string) []string }

// HumanAttributeNamer は Model.human_attribute_name(attr)。
// Redmine では l("field_#{attr}") 相当（_id は除去）。未実装なら Humanize(attr)。
type HumanAttributeNamer interface{ HumanAttributeName(attr string) string }

func modelPersisted(obj any) bool {
	if p, ok := obj.(Persistable); ok {
		return p.Persisted()
	}
	if v, ok := SendOK(obj, "persisted?"); ok {
		return truthy(v)
	}
	return false
}

func modelParam(obj any) string {
	if p, ok := obj.(ToParamer); ok {
		return p.ToParam()
	}
	return ToS(Send(obj, "id"))
}

func modelErrors(obj any, attr string) []string {
	if obj == nil {
		return nil
	}
	if e, ok := obj.(ErrorReporter); ok {
		return e.ErrorsOn(attr)
	}
	return nil
}

func humanAttributeName(obj any, attr string) string {
	if h, ok := obj.(HumanAttributeNamer); ok {
		return h.HumanAttributeName(attr)
	}
	return Humanize(attr)
}

// FieldName は field_name(object_name, method_name, multiple:, index:)。
func FieldName(objectName, method string, multiple bool, index any) string {
	m := ""
	if multiple {
		m = "[]"
	}
	switch {
	case IsBlank(objectName):
		return method + m
	case index != nil:
		return objectName + "[" + ToS(index) + "][" + method + "]" + m
	default:
		return objectName + "[" + method + "]" + m
	}
}

var fieldIDSanitize = regexp.MustCompile(`\]\[|[^-a-zA-Z0-9:.]`)

// FieldID は field_id(object_name, method_name, index:, namespace:)。
func FieldID(objectName, method string, index any, namespace any) string {
	so := strings.TrimSuffix(fieldIDSanitize.ReplaceAllString(objectName, "_"), "_")
	sm := strings.TrimSuffix(method, "?")
	var parts []string
	if namespace != nil {
		parts = append(parts, ToS(namespace))
	}
	if so != "" {
		parts = append(parts, so)
		if index != nil {
			parts = append(parts, ToS(index))
		}
	}
	parts = append(parts, sm)
	return strings.Join(parts, "_")
}

// FormBuilder は ActionView::Helpers::FormBuilder（form_for / fields_for のブロック引数）。
type FormBuilder struct {
	View       *View
	ObjectName string
	Object     any
	// Index / Namespace は form_for の index: / namespace: オプション。
	Index     any
	Namespace any
	// Labelled が真なら Redmine::Views::LabelledFormBuilder として振る舞う。
	Labelled bool
	// Open は form_for が生成した開始タグ（<form ...> と hidden 群）。fields_for では空。
	Open HTML
}

// NewFormBuilder は fields_for 相当のビルダを作る。
func (v *View) NewFormBuilder(objectName string, object any, labelled bool) *FormBuilder {
	return &FormBuilder{View: v, ObjectName: objectName, Object: object, Labelled: labelled}
}

// FormFor は form_for(record, options) / labelled_form_for を実行し、ビルダを返す。
// 開始タグは builder.Open に入る（テンプレートでは {{$f.Open}} ... {{end_form}}）。
// objectName が空ならモデルの ParamKey（options の as: が優先）を使う。
// url: は必須（ルーティングを持たないため polymorphic_path は使えない）。
// multipart はビルダから自動検出できないため、必要なら html: {multipart: true} を明示すること。
func (v *View) FormFor(objectName string, model any, options *Hash, labelled bool) *FormBuilder {
	opts := options.Clone()
	var htmlOpts *Hash
	if h, ok := opts.Lookup("html"); ok && h != nil {
		htmlOpts, _ = ToHash(h)
		htmlOpts = htmlOpts.Clone()
	} else {
		htmlOpts = &Hash{}
	}
	isModel := model != nil
	if as := opts.Get("as"); truthy(as) {
		objectName = ToS(as)
	} else if objectName == "" {
		if pk, ok := model.(ParamKeyer); ok {
			objectName = pk.ParamKey()
		}
	}
	if isModel {
		// apply_form_for_options!
		action := "new"
		if modelPersisted(model) {
			action = "edit"
		}
		var class, id any
		ns := opts.Get("namespace")
		if as := opts.Get("as"); truthy(as) {
			class = action + "_" + ToS(as)
			id = joinCompact("_", ns, action, as)
		} else {
			key := objectName
			if pk, ok := model.(ParamKeyer); ok {
				key = pk.ParamKey()
			}
			class = action + "_" + key
			domID := action + "_" + key
			if modelPersisted(model) {
				domID = action + "_" + key + "_" + modelParam(model)
			}
			id = joinCompact("_", ns, domID)
		}
		if id == "" {
			id = nil
		}
		htmlOpts = htmlOpts.ReverseMerge(NewHash("class", class, "id", id))
	}
	remote := truthy(opts.Del("remote"))
	if remote && (v.EmbedAuthenticityTokenInRemoteForms == nil || !*v.EmbedAuthenticityTokenInRemoteForms) && IsBlank(opts.Get("authenticity_token")) {
		opts.Set("authenticity_token", false)
	}
	// html_options_for_form_with
	ho := opts.Slice("id", "class", "multipart", "method", "data", "authenticity_token").Update(htmlOpts)
	r := htmlOpts.Get("remote")
	ho.Set("remote", truthy(r) || remote)
	if isModel && modelPersisted(model) && !truthy(ho.Get("method")) {
		ho.Set("method", "patch")
	}
	if e, ok := opts.Lookup("skip_enforcing_utf8"); ok && e != nil {
		ho.Set("enforce_utf8", !truthy(e))
	} else if e, ok := opts.Lookup("enforce_utf8"); ok {
		ho.Set("enforce_utf8", e)
	}
	url := opts.Get("url")
	if url == nil {
		url = ""
	}
	open := v.formTagHTML(v.htmlOptionsForForm(url, ho))
	return &FormBuilder{
		View: v, ObjectName: objectName, Object: model,
		Index: opts.Get("index"), Namespace: opts.Get("namespace"),
		Labelled: labelled, Open: open,
	}
}

func joinCompact(sep string, parts ...any) string {
	var out []string
	for _, p := range parts {
		if p != nil {
			out = append(out, ToS(p))
		}
	}
	return strings.Join(out, sep)
}

// --- Tags::Base 相当 ---

func (f *FormBuilder) value(method string) any {
	if f.Object == nil {
		return nil
	}
	return Send(f.Object, method)
}

func (f *FormBuilder) valueBeforeTypeCast(method string) any {
	if f.Object == nil {
		return nil
	}
	if v, ok := SendOK(f.Object, method+"_before_type_cast"); ok {
		return v
	}
	return f.value(method)
}

// objectify は objectify_options（index / namespace の既定値を補う）。
func (f *FormBuilder) objectify(options *Hash) *Hash {
	o := &Hash{}
	if f.Index != nil {
		o.Set("index", f.Index)
	}
	if f.Namespace != nil {
		o.Set("namespace", f.Namespace)
	}
	return o.Update(options)
}

func (f *FormBuilder) nameAndIDIndex(options *Hash) (any, bool) {
	if v, ok := options.Delete("index"); ok {
		if v == nil {
			return "", true
		}
		return v, true
	}
	return nil, false
}

func (f *FormBuilder) addDefaultNameAndID(method string, options *Hash) {
	index, _ := f.nameAndIDIndex(options)
	if !options.Has("name") {
		options.Set("name", FieldName(f.ObjectName, strings.TrimSuffix(method, "?"), truthy(options.Get("multiple")), index))
	}
	if !options.Has("id") {
		ns := options.Del("namespace")
		options.Set("id", FieldID(f.ObjectName, method, index, ns))
	}
	if ns := options.Del("namespace"); truthy(ns) {
		if truthy(options.Get("id")) {
			options.Set("id", ToS(ns)+"_"+ToS(options.Get("id")))
		} else {
			options.Set("id", ns)
		}
	}
}

var (
	sanitizeValueRe1 = regexp.MustCompile(`[\s.]`)
	sanitizeValueRe2 = regexp.MustCompile(`[^-\p{L}\p{M}\p{Nd}\p{Pc}]`)
)

func sanitizedValue(v any) string {
	s := sanitizeValueRe1.ReplaceAllString(ToS(v), "_")
	return strings.ToLower(sanitizeValueRe2.ReplaceAllString(s, ""))
}

func (f *FormBuilder) addDefaultNameAndIDForValue(method string, tagValue any, options *Hash) {
	if tagValue == nil {
		f.addDefaultNameAndID(method, options)
		return
	}
	specified := options.Get("id")
	f.addDefaultNameAndID(method, options)
	if IsBlank(specified) && IsPresent(options.Get("id")) {
		options.Set("id", ToS(options.Get("id"))+"_"+sanitizedValue(tagValue))
	}
}

func (f *FormBuilder) placeholder(method string, options *Hash) {
	if p, ok := options.Lookup("placeholder"); ok && truthy(p) {
		if _, isStr := p.(string); !isStr {
			if p == true {
				options.Set("placeholder", humanAttributeName(f.Object, method))
			} else {
				options.Set("placeholder", Humanize(method))
			}
		}
	}
}

// renderTextField は Tags::TextField#render（field_type は "text" / "password" / "hidden" など）。
func (f *FormBuilder) renderTextField(fieldType, method string, options *Hash) HTML {
	o := options.Clone()
	if !o.Has("size") {
		o.Set("size", o.Get("maxlength"))
	}
	if !truthy(o.Get("type")) {
		o.Set("type", fieldType)
	}
	if fieldType != "file" && !o.Has("value") {
		o.Set("value", f.valueBeforeTypeCast(method))
	}
	f.addDefaultNameAndID(method, o)
	return Tag("input", o)
}

func (f *FormBuilder) prepare(method string, options *Hash) *Hash {
	o := f.objectify(options)
	f.placeholder(method, o)
	return o
}

// --- 素の FormBuilder のフィールド（ラベルなし） ---

// RawTextField は FormBuilder#text_field（ラベルなし）。
func (f *FormBuilder) RawTextField(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	return f.renderTextField("text", method, f.prepare(method, options))
}

// RawPasswordField は password_field（value は明示しない限り出力しない）。
func (f *FormBuilder) RawPasswordField(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	o := NewHash("value", nil).Update(f.prepare(method, options))
	return f.renderTextField("password", method, o)
}

// RawTypedField は email_field / number_field / url_field などの text_field 派生。
func (f *FormBuilder) RawTypedField(fieldType, method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	return f.renderTextField(fieldType, method, f.prepare(method, options))
}

// HiddenField は hidden_field（LabelledFormBuilder でもラベルは付かない）。
func (f *FormBuilder) HiddenField(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	o := f.objectify(options)
	// Rails 8: @options.reverse_merge!(autocomplete: "off")（指定がなければ先頭に入る）
	if _, ok := o.Lookup("autocomplete"); !ok {
		o = NewHash("autocomplete", "off").Update(o)
	}
	return f.renderTextField("hidden", method, o)
}

// RawTextArea は text_area（ラベルなし）。
func (f *FormBuilder) RawTextArea(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	o := f.prepare(method, options)
	f.addDefaultNameAndID(method, o)
	if size, ok := o.Delete("size"); ok {
		if s, isStr := size.(string); isStr {
			cols, rows, found := strings.Cut(s, "x")
			o.Set("cols", cols)
			if found {
				o.Set("rows", rows)
			} else {
				o.Set("rows", nil)
			}
		}
	}
	var content any
	if v, ok := o.Delete("value"); ok {
		content = v
	} else {
		content = f.valueBeforeTypeCast(method)
	}
	return ContentTag("textarea", content, o)
}

// RawCheckBox は check_box(method, options, checked_value, unchecked_value)（ラベルなし）。
// unchecked_value が nil なら hidden を出力しない。
func (f *FormBuilder) RawCheckBoxWith(method string, options *Hash, checkedValue, uncheckedValue any) HTML {
	o := f.objectify(options)
	o.Set("type", "checkbox")
	o.Set("value", checkedValue)
	var checked bool
	if c, ok := o.Delete("checked"); ok {
		checked = c == true || c == "checked"
	} else {
		checked = checkBoxChecked(f.value(method), checkedValue)
	}
	if checked {
		o.Set("checked", "checked")
	}
	if truthy(o.Get("multiple")) {
		f.addDefaultNameAndIDForValue(method, checkedValue, o)
		o.Delete("multiple")
	} else {
		f.addDefaultNameAndID(method, o)
	}
	includeHidden := true
	if ih, ok := o.Delete("include_hidden"); ok {
		includeHidden = truthy(ih)
	}
	cb := Tag("input", o)
	if !includeHidden {
		return cb
	}
	var hidden HTML
	if truthy(uncheckedValue) {
		hidden = Tag("input", o.Slice("name", "disabled", "form").Update(NewHash("type", "hidden", "value", uncheckedValue, "autocomplete", "off")))
	}
	return hidden + cb
}

func checkBoxChecked(value, checkedValue any) bool {
	switch x := value.(type) {
	case bool:
		return x == truthy(checkedValue)
	case nil:
		return false
	case string:
		return x == ToS(checkedValue)
	}
	if isSlice(value) {
		for _, e := range toSlice(value) {
			if ToS(e) == ToS(checkedValue) {
				return true
			}
		}
		return false
	}
	return toInt(value) == toInt(checkedValue)
}

// RadioButton は radio_button(method, tag_value, options)。
func (f *FormBuilder) RadioButton(method string, tagValue any, opts ...*Hash) HTML {
	options := firstHash(opts)
	o := f.objectify(options)
	o.Set("type", "radio")
	o.Set("value", tagValue)
	var checked bool
	if c, ok := o.Delete("checked"); ok {
		checked = c == true || c == "checked"
	} else {
		checked = ToS(f.value(method)) == ToS(tagValue)
	}
	if checked {
		o.Set("checked", "checked")
	}
	f.addDefaultNameAndIDForValue(method, tagValue, o)
	return Tag("input", o)
}

// RawDateField は date_field（Redmine パッチにより max: '9999-12-31' が既定）。
func (f *FormBuilder) RawDateField(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	o := f.prepare(method, options).ReverseMerge(NewHash("max", "9999-12-31"))
	v := o.Get("value")
	if !truthy(v) {
		v = f.value(method)
	}
	o.Set("value", formatDateValue(v))
	o.Set("min", formatDateValue(parseDate(o.Get("min"))))
	o.Set("max", formatDateValue(parseDate(o.Get("max"))))
	return f.renderTextField("date", method, o)
}

func formatDateValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return x
	case time.Time:
		return x.Format("2006-01-02")
	case *time.Time:
		if x == nil {
			return nil
		}
		return x.Format("2006-01-02")
	}
	return ToS(v)
}

func parseDate(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05", "2006/01/02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return nil
}

// RawSelect は select(method, choices, options, html_options)（ラベルなし）。
func (f *FormBuilder) RawSelect(method string, choices any, opts ...*Hash) HTML {
	options, htmlOptions := nthHash(opts, 0), nthHash(opts, 1)
	o := f.objectify(options)
	ho := htmlOptions.Clone()
	if f.Index != nil && !ho.Has("index") {
		ho = NewHash("index", f.Index).Update(ho)
	}
	if f.Namespace != nil && !ho.Has("namespace") {
		ho = NewHash("namespace", f.Namespace).Update(ho)
	}
	value := f.value(method)
	var selected any
	if s, ok := o.Lookup("selected"); ok {
		selected = s
	} else if value == nil {
		selected = ""
	} else {
		selected = value
	}
	tagsOpts := NewHash("selected", selected, "disabled", o.Get("disabled"))
	var optionTags HTML
	if groupedChoices(choices) {
		optionTags = GroupedOptionsForSelect(choices, tagsOpts, nil)
	} else {
		optionTags = OptionsForSelect(choices, tagsOpts)
	}
	return f.selectContentTag(method, optionTags, o, ho)
}

func groupedChoices(choices any) bool {
	if IsBlank(choices) {
		return false
	}
	xs := containerElements(choices)
	if len(xs) == 0 || !isSlice(xs[0]) {
		return false
	}
	first := toSlice(xs[0])
	return len(first) > 1 && isSlice(first[1])
}

func (f *FormBuilder) selectContentTag(method string, optionTags HTML, options, html *Hash) HTML {
	for _, prop := range []string{"required", "multiple", "size"} {
		if v, ok := options.Lookup(prop); ok && !html.Has(prop) {
			options.Delete(prop)
			html.Set(prop, v)
		}
	}
	f.addDefaultNameAndID(method, html)
	if truthy(html.Get("required")) && !truthy(html.Get("multiple")) && toInt(html.Fetch("size", 1)) == 1 {
		if !truthy(options.Get("include_blank")) && !truthy(options.Get("prompt")) {
			options.Set("include_blank", true)
		}
	}
	var value any
	if s, ok := options.Lookup("selected"); ok {
		value = s
	} else {
		value = f.value(method)
	}
	sel := ContentTag("select", addSelectOptions(optionTags, options, value), html)
	if truthy(html.Get("multiple")) && options.Fetch("include_hidden", true) != false && truthy(options.Fetch("include_hidden", true)) {
		return Tag("input", NewHash("disabled", html.Get("disabled"), "name", html.Get("name"), "type", "hidden", "value", "", "autocomplete", "off")) + sel
	}
	return sel
}

func addSelectOptions(optionTags HTML, options *Hash, value any) HTML {
	if ib := options.Get("include_blank"); truthy(ib) {
		var content, label any
		if s, ok := ib.(string); ok {
			content = s
		} else {
			label = " "
		}
		optionTags = contentTagString("option", content, NewHash("value", "", "label", label), true) + "\n" + optionTags
	}
	if IsBlank(value) && truthy(options.Get("prompt")) {
		to := NewHash("value", "")
		if options.Get("disabled") == "" {
			to.Set("disabled", true)
		}
		if options.Get("selected") == "" {
			to.Set("selected", true)
		}
		optionTags = contentTagString("option", promptText(options.Get("prompt")), to, true) + "\n" + optionTags
	}
	return optionTags
}

// Label は FormBuilder#label(method, text, options)。text が nil なら human_attribute_name。
// テンプレートからは {{$f.Label "subject"}} / {{$f.Label "subject" "Text" (hash ...)}} / {{$f.Label "subject" (hash ...)}}。
func (f *FormBuilder) Label(method string, args ...any) HTML {
	var text any
	var options *Hash
	switch {
	case len(args) == 1 && isHash(args[0]):
		options = optHash(args, 0)
	case len(args) >= 1:
		text = args[0]
		options = optHash(args, 1)
	}
	o := f.objectify(options)
	tagValue := o.Del("value")
	nameAndID := o.Clone()
	if truthy(nameAndID.Get("for")) {
		nameAndID.Set("id", nameAndID.Get("for"))
	} else {
		nameAndID.Delete("id")
	}
	f.addDefaultNameAndIDForValue(method, tagValue, nameAndID)
	o.Delete("index")
	o.Delete("namespace")
	if !o.Has("for") {
		o.Set("for", nameAndID.Get("id"))
	}
	var content any
	if IsPresent(text) {
		content = ToS(text)
		if h, ok := text.(HTML); ok {
			content = h
		}
	} else {
		mv := method
		if IsPresent(tagValue) {
			mv = method + "." + ToS(tagValue)
		}
		content = humanAttributeName(f.Object, mv)
	}
	return LabelTag(nameAndID.Get("id"), content, o)
}

// Submit は FormBuilder#submit(value, options)。value が nil なら "Create Issue" / "Update Issue" 形式。
// テンプレートからは {{$f.Submit}} / {{$f.Submit "Save"}} / {{$f.Submit "Save" (hash ...)}}。
func (f *FormBuilder) Submit(args ...any) HTML {
	var value any
	var options *Hash
	switch {
	case len(args) == 1 && isHash(args[0]):
		options = optHash(args, 0)
	case len(args) >= 1:
		value = args[0]
		options = optHash(args, 1)
	}
	if value == nil {
		key := "Submit"
		if f.Object != nil {
			if modelPersisted(f.Object) {
				key = "Update"
			} else {
				key = "Create"
			}
		}
		value = key + " " + Humanize(f.ObjectName)
	}
	return f.View.SubmitTag(value, options)
}

// --- Redmine::Views::LabelledFormBuilder ---

// LabelForField は LabelledFormBuilder#label_for_field。
// options の no_label / required は削除される（required は入力要素に出力されない）。
// label: が Symbol なら翻訳キー、文字列ならそのまま（いずれも html_safe 扱いでエスケープされない）。
func (f *FormBuilder) LabelForField(field string, options *Hash) HTML {
	if truthy(options.Del("no_label")) {
		return ""
	}
	var text string
	switch l := options.Get("label").(type) {
	case Symbol:
		text = f.View.t(string(l))
	case nil:
		text = humanAttributeName(f.Object, field)
	default:
		text = ToS(l)
		if !truthy(l) {
			text = humanAttributeName(f.Object, field)
		}
	}
	if truthy(options.Del("required")) {
		text += string(ContentTag("span", " *", NewHash("class", "required")))
	}
	var class any
	if f.Object != nil && len(modelErrors(f.Object, field)) > 0 {
		class = "error"
	}
	return ContentTag("label", HTML(text), NewHash("class", class, "for", f.ObjectName+"_"+field))
}

func (f *FormBuilder) labelled(field string, options *Hash, render func(o *Hash) HTML) HTML {
	o := options.Clone()
	if !f.Labelled {
		return render(o)
	}
	label := f.LabelForField(field, o)
	return label + render(o.Except("label"))
}

// TextField は text_field（Labelled ならラベル付き）。
func (f *FormBuilder) TextField(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	return f.labelled(method, options, func(o *Hash) HTML { return f.RawTextField(method, o) })
}

// PasswordField は password_field。
func (f *FormBuilder) PasswordField(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	return f.labelled(method, options, func(o *Hash) HTML { return f.RawPasswordField(method, o) })
}

// TextArea は text_area。
func (f *FormBuilder) TextArea(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	return f.labelled(method, options, func(o *Hash) HTML { return f.RawTextArea(method, o) })
}

// DateField は date_field。
func (f *FormBuilder) DateField(method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	return f.labelled(method, options, func(o *Hash) HTML { return f.RawDateField(method, o) })
}

// TypedField は email_field / number_field / url_field / file_field など（fieldType は input の type）。
func (f *FormBuilder) TypedField(fieldType, method string, opts ...*Hash) HTML {
	options := firstHash(opts)
	return f.labelled(method, options, func(o *Hash) HTML { return f.RawTypedField(fieldType, method, o) })
}

// CheckBox は check_box(method, [options], [checked_value="1"], [unchecked_value="0"])（テンプレート向け）。
func (f *FormBuilder) CheckBox(method string, args ...any) HTML {
	options, checkedValue, uncheckedValue := checkBoxArgs(args)
	return f.CheckBoxWith(method, options, checkedValue, uncheckedValue)
}

// RawCheckBox はラベルなしの CheckBox（テンプレート向け）。
func (f *FormBuilder) RawCheckBox(method string, args ...any) HTML {
	options, checkedValue, uncheckedValue := checkBoxArgs(args)
	return f.RawCheckBoxWith(method, options, checkedValue, uncheckedValue)
}

func checkBoxArgs(args []any) (*Hash, any, any) {
	options := optHash(args, 0)
	var checked, unchecked any = "1", "0"
	if len(args) > 1 {
		checked = args[1]
	}
	if len(args) > 2 {
		unchecked = args[2]
	}
	return options, checked, unchecked
}

// CheckBoxWith は check_box(method, options, checked_value, unchecked_value)。
func (f *FormBuilder) CheckBoxWith(method string, options *Hash, checkedValue, uncheckedValue any) HTML {
	return f.labelled(method, options, func(o *Hash) HTML { return f.RawCheckBoxWith(method, o, checkedValue, uncheckedValue) })
}

// Select は select(method, choices, options, html_options)。
// Labelled の場合、ラベルは options（html_options ではない）の label: / required: / no_label: で決まる。
func (f *FormBuilder) Select(method string, choices any, opts ...*Hash) HTML {
	o, htmlOptions := nthHash(opts, 0).Clone(), nthHash(opts, 1)
	if !f.Labelled {
		return f.RawSelect(method, choices, o, htmlOptions)
	}
	label := f.LabelForField(method, o)
	return label + f.RawSelect(method, choices, o, htmlOptions.Except("label"))
}

func firstHash(opts []*Hash) *Hash { return nthHash(opts, 0) }

func nthHash(opts []*Hash, i int) *Hash {
	if i < len(opts) && opts[i] != nil {
		return opts[i]
	}
	return &Hash{}
}
