package rails

import (
	"bytes"
	"encoding/json"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"html/template"
	"math/big"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

// booleanAttributes は ActionView::Helpers::TagHelper::BOOLEAN_ATTRIBUTES。
var booleanAttributes = map[string]bool{}

func init() {
	for _, a := range strings.Fields(`allowfullscreen allowpaymentrequest async autofocus
		autoplay checked compact controls declare default
		defaultchecked defaultmuted defaultselected defer
		disabled enabled formnovalidate hidden indeterminate
		inert ismap itemscope loop multiple muted nohref
		nomodule noresize noshade novalidate nowrap open
		pauseonexit playsinline readonly required reversed
		scoped seamless selected sortable truespeed
		typemustmatch visible`) {
		booleanAttributes[a] = true
	}
}

// voidElements は tag.xxx で " />" ではなく ">" で閉じる要素（TagBuilder.define_void_element）。
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "keygen": true, "link": true, "meta": true,
	"source": true, "track": true, "wbr": true,
}

// TagOptions は TagBuilder#tag_options。先頭の空白を含む属性文字列を返す（空なら ""）。
func TagOptions(opts *Hash, escape bool) string {
	if opts.Len() == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range opts.Entries() {
		key, value := e.Key, e.Value
		switch {
		case key == "data" && isHash(value):
			h, _ := ToHash(value)
			for _, d := range h.Entries() {
				if d.Value == nil {
					continue
				}
				b.WriteByte(' ')
				b.WriteString(prefixTagOption("data", d.Key, d.Value, escape))
			}
		case key == "aria" && isHash(value):
			h, _ := ToHash(value)
			for _, d := range h.Entries() {
				v := d.Value
				if v == nil {
					continue
				}
				if isSlice(v) || isHash(v) {
					tokens := buildTagValues(v)
					if len(tokens) == 0 {
						continue
					}
					v = SafeJoin(tokens, " ")
				} else {
					v = ToS(v)
				}
				b.WriteByte(' ')
				b.WriteString(prefixTagOption("aria", d.Key, v, escape))
			}
		case booleanAttributes[key]:
			if truthy(value) {
				b.WriteByte(' ')
				b.WriteString(key + `="` + key + `"`)
			}
		case value != nil:
			b.WriteByte(' ')
			b.WriteString(tagOption(key, value, escape))
		}
	}
	return b.String()
}

// tagOption は TagBuilder#tag_option。
func tagOption(key string, value any, escape bool) string {
	value = rootURLAttr(key, value)
	if escape {
		key = XMLNameEscape(key)
	}
	var s string
	switch {
	case isSlice(value) || isHash(value):
		vals := value
		if key == "class" {
			vals = toAnySlice(buildTagValues(value))
		} else if isHash(value) {
			// Hash#flatten 相当（[k1, v1, k2, v2...]）
			h, _ := ToHash(value)
			var fl []any
			for _, e := range h.Entries() {
				fl = append(fl, e.Key, e.Value)
			}
			vals = fl
		}
		if escape {
			s = string(SafeJoin(vals, " "))
		} else {
			parts := []string{}
			for _, x := range flatten(vals) {
				parts = append(parts, ToS(x))
			}
			s = strings.Join(parts, " ")
		}
	default:
		if escape {
			s = unwrappedEscape(value)
		} else {
			s = ToS(value)
		}
	}
	if strings.Contains(s, `"`) {
		s = strings.ReplaceAll(s, `"`, "&quot;")
	}
	return key + `="` + s + `"`
}

// rootURLAttr は URL を値に取る属性（href・src・action・formaction と、名前に "url" を含む data-*。
// 例: data-cm-url・data-reorder-url・data-automcomplete-url）の "/" 始まりのパスに
// relative_url_root を前置する（Redmine ではこれらは常にルートヘルパーの *_path が生成する）。
// data-upload-path のように "-path" で終わる data-* も対象。
func rootURLAttr(key string, value any) any {
	switch key {
	case "href", "src", "action", "formaction":
	default:
		if !strings.HasPrefix(key, "data-") || !(strings.Contains(key, "url") || strings.HasSuffix(key, "-path")) {
			return value
		}
	}
	switch v := value.(type) {
	case string:
		return urlroot.Path(v)
	case template.HTML:
		return template.HTML(urlroot.Path(string(v)))
	}
	return value
}

// prefixTagOption は TagBuilder#prefix_tag_option（data-* / aria-*）。
// 値が文字列・シンボル以外なら to_json する。
func prefixTagOption(prefix, key string, value any, escape bool) string {
	k := prefix + "-" + Dasherize(key)
	switch value.(type) {
	case string, template.HTML, Symbol, *big.Float:
	default:
		value = ToJSON(value)
	}
	return tagOption(k, value, escape)
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// buildTagValues は TagHelper.build_tag_values（class 属性・token_list 用）。
func buildTagValues(args ...any) []string {
	var out []string
	for _, v := range args {
		switch {
		case isHash(v):
			h, _ := ToHash(v)
			for _, e := range h.Entries() {
				if truthy(e.Value) && e.Key != "" && IsPresent(e.Key) {
					out = append(out, e.Key)
				}
			}
		case isSlice(v):
			out = append(out, buildTagValues(toSlice(v)...)...)
		default:
			if IsPresent(v) {
				out = append(out, ToS(v))
			}
		}
	}
	return out
}

// TokenList は token_list / class_names。
func TokenList(args ...any) HTML {
	seen := map[string]bool{}
	var tokens []any
	for _, v := range buildTagValues(args...) {
		for _, t := range strings.Fields(unescapeHTML(v)) {
			if !seen[t] {
				seen[t] = true
				tokens = append(tokens, t)
			}
		}
	}
	return SafeJoin(tokens, " ")
}

// unescapeHTML は CGI.unescape_html の基本実体参照のみ対応版。
func unescapeHTML(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	r := strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">", "&apos;", "'")
	return r.Replace(s)
}

// XMLNameEscape は ERB::Util.xml_name_escape（属性名・タグ名の不正文字を _ に置換）。
func XMLNameEscape(name string) string {
	if IsBlank(name) {
		return ""
	}
	ok := true
	for i, r := range name {
		if i == 0 && !isNameStart(r) || i > 0 && !isNameChar(r) {
			ok = false
			break
		}
	}
	if ok {
		return name
	}
	var b strings.Builder
	first := true
	for _, r := range name {
		if first {
			if !isNameStart(r) {
				r = '_'
			}
			first = false
		} else if !isNameChar(r) {
			r = '_'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isNameStart(r rune) bool {
	return r == ':' || r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
		(r >= 0xC0 && r <= 0xD6) || (r >= 0xD8 && r <= 0xF6) || (r >= 0xF8 && r <= 0x2FF) ||
		(r >= 0x370 && r <= 0x37D) || (r >= 0x37F && r <= 0x1FFF) || (r >= 0x200C && r <= 0x200D) ||
		(r >= 0x2070 && r <= 0x218F) || (r >= 0x2C00 && r <= 0x2FEF) || (r >= 0x3001 && r <= 0xD7FF) ||
		(r >= 0xF900 && r <= 0xFDCF) || (r >= 0xFDF0 && r <= 0xFFFD) || (r >= 0x10000 && r <= 0xEFFFF)
}

func isNameChar(r rune) bool {
	return isNameStart(r) || r == '-' || r == '.' || (r >= '0' && r <= '9') || r == 0xB7 ||
		(r >= 0x300 && r <= 0x36F) || (r >= 0x203F && r <= 0x2040)
}

// Tag は tag(name, options = nil, open = false, escape = true)。
// open が真なら "<name ...>"、偽なら "<name ... />" を返す。
func Tag(name string, opts *Hash, open ...bool) HTML {
	o := len(open) > 0 && open[0]
	suffix := " />"
	if o {
		suffix = ">"
	}
	return HTML("<" + name + TagOptions(opts, true) + suffix)
}

// TagNoEscape は tag(name, options, open, false)（属性をエスケープしない）。
func TagNoEscape(name string, opts *Hash, open bool) HTML {
	suffix := " />"
	if open {
		suffix = ">"
	}
	return HTML("<" + name + TagOptions(opts, false) + suffix)
}

// ContentTag は content_tag(name, content, options)。content は html_safe でなければエスケープする。
func ContentTag(name string, content any, opts *Hash) HTML {
	return contentTagString(name, content, opts, true)
}

// ContentTagNoEscape は content_tag(name, content, options, false)。
func ContentTagNoEscape(name string, content any, opts *Hash) HTML {
	return contentTagString(name, content, opts, false)
}

// contentTagString は TagBuilder#content_tag_string。
func contentTagString(name string, content any, opts *Hash, escape bool) HTML {
	attrs := TagOptions(opts, escape)
	var c string
	if escape && IsPresent(content) {
		c = unwrappedEscape(content)
	} else {
		c = ToS(content)
	}
	pre := ""
	if name == "textarea" {
		pre = "\n"
	}
	return HTML("<" + name + attrs + ">" + pre + c + "</" + name + ">")
}

// ContentTagOpen はブロック付き content_tag の開始部分 "<name attrs>"（textarea は改行付き）。
// テンプレートでは {{content_tag_open ...}} ... {{end_tag "name"}} の形で使う。
func ContentTagOpen(name string, opts *Hash) HTML {
	pre := ""
	if name == "textarea" {
		pre = "\n"
	}
	return HTML("<" + name + TagOptions(opts, true) + ">" + pre)
}

// TagBuilderTag は新しい tag.xxx 記法（tag.div, tag.br など）。
// content が nil なら void 要素は "<br>"、それ以外は "<name></name>" を返す。
func TagBuilderTag(name string, content any, opts *Hash) HTML {
	n := strings.ReplaceAll(name, "_", "-")
	if content == nil && voidElements[n] {
		return HTML("<" + n + TagOptions(opts, true) + ">")
	}
	return contentTagString(n, content, opts, true)
}

// TagAttributes は tag.attributes(hash)（先頭空白なしの属性文字列）。
func TagAttributes(opts *Hash) HTML {
	return HTML(strings.TrimSpace(TagOptions(opts, true)))
}

// CDATASection は cdata_section。
func CDATASection(content any) HTML {
	s := strings.ReplaceAll(ToS(content), "]]>", "]]]]><![CDATA[>")
	return HTML("<![CDATA[" + s + "]]>")
}

// ToJSON は ActiveSupport の to_json 相当（data-* 属性値用）。
// <>& と U+2028/2029 は \uXXXX にエスケープされる（escape_html_entities_in_json）。
func ToJSON(v any) string {
	var buf bytes.Buffer
	writeJSON(&buf, v)
	return buf.String()
}

func writeJSON(buf *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
		return
	case *Hash:
		writeJSONHash(buf, x)
		return
	case Hash:
		writeJSONHash(buf, &x)
		return
	case string:
		writeJSONString(buf, x)
		return
	case template.HTML:
		writeJSONString(buf, string(x))
		return
	case Symbol:
		writeJSONString(buf, string(x))
		return
	case float64:
		buf.WriteString(floatToS(x))
		return
	case float32:
		buf.WriteString(floatToS(float64(x)))
		return
	}
	if isSlice(v) {
		buf.WriteByte('[')
		for i, e := range toSlice(v) {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSON(buf, e)
		}
		buf.WriteByte(']')
		return
	}
	if isHash(v) {
		h, _ := ToHash(v)
		writeJSONHash(buf, h)
		return
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		buf.WriteString(ToS(v))
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		writeJSONString(buf, ToS(v))
		return
	}
	buf.Write(b)
}

func writeJSONHash(buf *bytes.Buffer, h *Hash) {
	buf.WriteByte('{')
	for i, e := range h.Entries() {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeJSONString(buf, e.Key)
		buf.WriteByte(':')
		writeJSON(buf, e.Value)
	}
	buf.WriteByte('}')
}

// writeJSONString は Ruby JSON + ActiveSupport の escape と同じ規則で文字列を書き出す。
func writeJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			buf.WriteString(`\"`)
		case r == '\\':
			buf.WriteString(`\\`)
		case r == '\n':
			buf.WriteString(`\n`)
		case r == '\r':
			buf.WriteString(`\r`)
		case r == '\t':
			buf.WriteString(`\t`)
		case r == '\b':
			buf.WriteString(`\b`)
		case r == '\f':
			buf.WriteString(`\f`)
		case r == 0x3c, r == 0x3e, r == 0x26, r == 0x2028, r == 0x2029, r < 0x20:
			// バックスラッシュ + u + 4 桁小文字 16 進でエスケープする（escape_html_entities_in_json）
			writeJSONUnicodeEscape(buf, r)
		default:
			buf.WriteString(s[i : i+size])
		}
		i += size
	}
	buf.WriteByte('"')
}

func writeJSONUnicodeEscape(buf *bytes.Buffer, r rune) {
	const hexdigits = "0123456789abcdef"
	buf.WriteByte(0x5c) // バックスラッシュ
	buf.WriteByte('u')
	buf.WriteByte(hexdigits[(r>>12)&0xf])
	buf.WriteByte(hexdigits[(r>>8)&0xf])
	buf.WriteByte(hexdigits[(r>>4)&0xf])
	buf.WriteByte(hexdigits[r&0xf])
}

// validTagName は ensure_valid_html5_tag_name の判定。
func validTagName(name string) bool {
	if name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	if r > unicode.MaxASCII || !unicode.IsLetter(r) {
		return false
	}
	return !strings.ContainsAny(name, " \t\n\r\f/>")
}
