package rails

import (
	"strings"
)

// SanitizeToID は FormTagHelper#sanitize_to_id（"]" を除去し、[-a-zA-Z0-9:.] 以外を _ に置換）。
func SanitizeToID(name any) string {
	s := strings.ReplaceAll(ToS(name), "]", "")
	b := []byte(s)
	out := make([]byte, 0, len(b))
	for _, r := range s {
		if r < 128 && (r == '-' || r == ':' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			out = append(out, byte(r))
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// htmlOptionsForForm は FormTagHelper#html_options_for_form。url が false なら action を出力しない。
func (v *View) htmlOptionsForForm(url any, options *Hash) *Hash {
	html := options.Clone()
	if truthy(html.Del("multipart")) {
		html.Set("enctype", "multipart/form-data")
	}
	if b, ok := url.(bool); ok && !b {
		html.Delete("action")
	} else if a, ok := html.Lookup("action"); ok && a == false {
		html.Delete("action")
	} else {
		if url == nil {
			url = ""
		}
		html.Set("action", urlFor(url))
	}
	html.Set("accept-charset", "UTF-8")
	if truthy(html.Del("remote")) {
		html.Set("data-remote", true)
	}
	if truthy(html.Get("data-remote")) && v.EmbedAuthenticityTokenInRemoteForms != nil && !*v.EmbedAuthenticityTokenInRemoteForms && IsBlank(html.Get("authenticity_token")) {
		html.Set("authenticity_token", false)
	} else if html.Get("authenticity_token") == true {
		html.Set("authenticity_token", nil)
	}
	return html
}

// extraTagsForForm は FormTagHelper#extra_tags_for_form（html を破壊的に変更する）。
func (v *View) extraTagsForForm(html *Hash) HTML {
	authToken := html.Del("authenticity_token")
	method := strings.ToLower(ToS(html.Del("method")))
	var tags HTML
	switch method {
	case "get":
		html.Set("method", "get")
	case "post", "":
		html.Set("method", "post")
		tags = v.TokenTag(authToken)
	default:
		html.Set("method", "post")
		tags = MethodTag(method) + v.TokenTag(authToken)
	}
	enforce := v.EnforceUTF8
	if e, ok := html.Delete("enforce_utf8"); ok {
		enforce = truthy(e)
	}
	if enforce {
		return `<input name="utf8" type="hidden" value="&#x2713;" autocomplete="off" />` + tags
	}
	return tags
}

// formTagHTML は form_tag_html（Redmine のオーバーライドによる name 属性付与を含む）。
func (v *View) formTagHTML(html *Hash) HTML {
	if v.FormName != nil && !truthy(html.Get("name")) {
		base := "form"
		if id := html.Get("id"); truthy(id) {
			base = ToS(id)
		}
		html.Set("name", v.FormName(base))
	}
	extra := v.extraTagsForForm(html)
	return Tag("form", html, true) + extra
}

// FormTag は form_tag(url, options) の開始部分（<form ...> と hidden 群）を返す。
// ブロック付き form_tag は {{form_tag ...}} ... {{end_form}} として書く（end_form は "</form>"）。
func (v *View) FormTag(url any, options *Hash) HTML {
	return v.formTagHTML(v.htmlOptionsForForm(url, options))
}

// FormTagWithBody はブロック付き form_tag（body を含め </form> まで）を返す。
func (v *View) FormTagWithBody(url any, options *Hash, body any) HTML {
	return v.FormTag(url, options) + HTML(unwrappedEscape(body)) + "</form>"
}

// LabelTag は label_tag(name, content, options)。content が nil なら name.humanize。
func LabelTag(name any, content any, options *Hash) HTML {
	opts := options.Clone()
	if !IsBlank(name) && !opts.Has("for") {
		opts.Set("for", SanitizeToID(name))
	}
	if content == nil {
		content = Humanize(ToS(name))
	}
	return ContentTag("label", content, opts)
}

// TextFieldTag は text_field_tag(name, value, options)。
func TextFieldTag(name any, value any, options *Hash) HTML {
	base := NewHash("type", "text", "name", name, "id", SanitizeToID(name), "value", value)
	return Tag("input", base.Update(options))
}

// fieldTag は options.merge(type: typ) を text_field_tag に渡す共通処理。
func fieldTag(typ string, name, value any, options *Hash) HTML {
	return TextFieldTag(name, value, options.Clone().Set("type", typ))
}

// HiddenFieldTag は hidden_field_tag(name, value, options)。
func HiddenFieldTag(name any, value any, options *Hash) HTML {
	return TextFieldTag(name, value, options.Clone().Set("type", "hidden").Set("autocomplete", "off"))
}

// PasswordFieldTag は password_field_tag(name, value, options)。
func PasswordFieldTag(name any, value any, options *Hash) HTML {
	return fieldTag("password", name, value, options)
}

// FileFieldTag は file_field_tag(name, options)。
func FileFieldTag(name any, options *Hash) HTML {
	return fieldTag("file", name, nil, options)
}

// EmailFieldTag / SearchFieldTag / URLFieldTag / TelephoneFieldTag / ColorFieldTag / TimeFieldTag / NumberFieldTag。
func EmailFieldTag(name, value any, options *Hash) HTML {
	return fieldTag("email", name, value, options)
}
func SearchFieldTag(name, value any, options *Hash) HTML {
	return fieldTag("search", name, value, options)
}
func URLFieldTag(name, value any, options *Hash) HTML { return fieldTag("url", name, value, options) }
func TelephoneFieldTag(name, value any, options *Hash) HTML {
	return fieldTag("tel", name, value, options)
}
func ColorFieldTag(name, value any, options *Hash) HTML {
	return fieldTag("color", name, value, options)
}
func TimeFieldTag(name, value any, options *Hash) HTML { return fieldTag("time", name, value, options) }

// NumberFieldTag は number_field_tag（in:/within: は未対応）。
func NumberFieldTag(name, value any, options *Hash) HTML {
	opts := options.Clone()
	if !truthy(opts.Get("type")) {
		opts.Set("type", "number")
	}
	return TextFieldTag(name, value, opts)
}

// DateFieldTag は date_field_tag。Redmine のパッチ（config/initializers/10-patches.rb）により
// max: '9999-12-31' が既定で付く。
func DateFieldTag(name, value any, options *Hash) HTML {
	opts := options.ReverseMerge(NewHash("max", "9999-12-31"))
	return fieldTag("date", name, value, opts)
}

// TextAreaTag は text_area_tag(name, content, options)。
// content は（escape: false でなければ）エスケープされ、textarea の直後に改行が入る。
func TextAreaTag(name any, content any, options *Hash) HTML {
	opts := options.Clone()
	if size, ok := opts.Delete("size"); ok && size != nil {
		if s, isStr := size.(string); isStr {
			cols, rows, _ := strings.Cut(s, "x")
			opts.Set("cols", cols)
			if strings.Contains(s, "x") {
				opts.Set("rows", rows)
			} else {
				opts.Set("rows", nil)
			}
		}
	}
	escape := true
	if e, ok := opts.Delete("escape"); ok {
		escape = truthy(e)
	}
	var c HTML
	if escape {
		c = H(content)
	} else {
		c = Raw(content)
	}
	base := NewHash("name", name, "id", SanitizeToID(name))
	return ContentTag("textarea", c, base.Update(opts))
}

// CheckBoxTag は check_box_tag(name, value = "1", checked = false, options = {})。
func CheckBoxTag(name any, value any, checked bool, options *Hash) HTML {
	html := NewHash("type", "checkbox", "name", name, "id", SanitizeToID(name), "value", value).Update(options)
	if checked {
		html.Set("checked", "checked")
	}
	return Tag("input", html)
}

// RadioButtonTag は radio_button_tag(name, value, checked = false, options = {})。
func RadioButtonTag(name any, value any, checked bool, options *Hash) HTML {
	html := NewHash("type", "radio", "name", name, "id", SanitizeToID(name)+"_"+SanitizeToID(value), "value", value).Update(options)
	if checked {
		html.Set("checked", "checked")
	}
	return Tag("input", html)
}

// SubmitTag は submit_tag(value = "Save changes", options)。
// AutomaticallyDisableSubmitTag が真なら data-disable-with を付与する。
func (v *View) SubmitTag(value any, options *Hash) HTML {
	html := NewHash("type", "submit", "name", "commit", "value", value).Update(deepCloneHash(options))
	v.setDefaultDisableWith(value, html)
	return Tag("input", html)
}

func deepCloneHash(h *Hash) *Hash {
	n := h.Clone()
	for i, e := range n.kv {
		if isHash(e.Value) {
			sub, _ := ToHash(e.Value)
			n.kv[i].Value = deepCloneHash(sub)
		}
	}
	return n
}

func (v *View) setDefaultDisableWith(value any, html *Hash) {
	var data *Hash
	if d, ok := html.Lookup("data"); ok && isHash(d) {
		data, _ = ToHash(d)
	}
	if html.Get("data-disable-with") == false || (data != nil && data.Get("disable_with") == false) {
		if data != nil {
			data.Delete("disable_with")
		}
	} else if v.AutomaticallyDisableSubmitTag {
		text := html.Get("data-disable-with")
		if !truthy(text) && data != nil {
			text = data.Get("disable_with")
		}
		if !truthy(text) {
			// value.to_s（html_safe な値は SafeBuffer のまま。データ属性で二重にエスケープされない）
			if h, ok := value.(HTML); ok {
				text = h
			} else {
				text = ToS(value)
			}
		}
		if data != nil {
			data.Set("disable_with", text)
		} else {
			html.Set("data", NewHash("disable_with", text))
		}
	}
	html.Delete("data-disable-with")
}

// ButtonTag は button_tag(content, options)（content が nil なら "Button"）。
func ButtonTag(content any, options *Hash) HTML {
	html := NewHash("name", "button", "type", "submit").Update(options)
	if content == nil {
		content = "Button"
	}
	return ContentTag("button", content, html)
}

// FieldSetTag は field_set_tag(legend, options) に content（ブロックの中身）を与えた結果。
func FieldSetTag(legend any, options *Hash, content any) HTML {
	var parts []any
	if !IsBlank(legend) {
		parts = append(parts, ContentTag("legend", legend, nil))
	}
	if content != nil {
		parts = append(parts, content)
	}
	return ContentTag("fieldset", SafeJoin(parts), options)
}

// FieldSetTagOpen はブロック付き field_set_tag の開始部分（<fieldset ...><legend>..</legend>）。
// 終了は {{end_tag "fieldset"}}。
func FieldSetTagOpen(legend any, options *Hash) HTML {
	s := HTML("<fieldset" + TagOptions(options, true) + ">")
	if !IsBlank(legend) {
		s += ContentTag("legend", legend, nil)
	}
	return s
}

// SelectTag は select_tag(name, option_tags, options)。
// options の include_blank / prompt / multiple を Rails と同様に処理する。
func SelectTag(name any, optionTags any, options *Hash) HTML {
	opts := options.Clone()
	if optionTags == nil {
		optionTags = ""
	}
	n := ToS(name)
	htmlName := n
	if opts.Get("multiple") == true && !strings.HasSuffix(n, "[]") {
		htmlName = n + "[]"
	}
	if ib, ok := opts.Delete("include_blank"); ok {
		blankOpts := NewHash("value", "")
		if ib == true {
			ib = ""
			blankOpts.Set("label", " ")
		}
		if truthy(ib) {
			optionTags = ContentTag("option", ib, blankOpts) + HTML(ToS(optionTags))
		}
	}
	if prompt := opts.Del("prompt"); truthy(prompt) {
		optionTags = ContentTag("option", prompt, NewHash("value", "")) + HTML(ToS(optionTags))
	}
	return ContentTag("select", optionTags, NewHash("name", htmlName, "id", SanitizeToID(name)).Update(opts))
}

// EndForm は "</form>"（ブロック付き form_tag / form_for の終端）。
func EndForm() HTML { return "</form>" }

// EndTag は "</name>"（ブロック付き content_tag / field_set_tag の終端）。
func EndTag(name string) HTML { return HTML("</" + name + ">") }
