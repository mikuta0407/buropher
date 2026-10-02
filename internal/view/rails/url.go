package rails

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/urlroot"
)

// urlFor は url_for(String) 相当。文字列以外は to_s する。
// buropher ではルートヘルパー（*_path）の代わりに "/issues/1" のような絶対パスを渡すため、
// "/" で始まるパスには relative_url_root（script_name）を前置する（urlroot.Path。冪等）。
func urlFor(u any) string { return urlroot.Path(ToS(u)) }

// convertOptionsToDataAttributes は UrlHelper#convert_options_to_data_attributes。
// html は破壊的に変更される（呼び出し側で複製しておくこと）。
func convertOptionsToDataAttributes(html *Hash) *Hash {
	if html == nil {
		return &Hash{}
	}
	if truthy(html.Del("remote")) {
		html.Set("data-remote", "true")
	}
	if method, ok := html.Delete("method"); ok && truthy(method) {
		addMethodToAttributes(html, method)
	}
	return html
}

func addMethodToAttributes(html *Hash, method any) {
	if methodNotGet(method) && !strings.Contains(ToS(html.Get("rel")), "nofollow") {
		if IsBlank(html.Get("rel")) {
			html.Set("rel", "nofollow")
		} else {
			html.Set("rel", ToS(html.Get("rel"))+" nofollow")
		}
	}
	html.Set("data-method", method)
}

func methodNotGet(method any) bool {
	if !truthy(method) {
		return false
	}
	return strings.ToLower(ToS(method)) != "get"
}

// LinkTo は link_to(name, url, html_options)。
// name が nil の場合は URL をリンクテキストにする。name は html_safe でなければエスケープされる。
// html_options の method / remote は data-method / data-remote（+ rel="nofollow"）に変換される。
func LinkTo(name any, url any, htmlOptions *Hash) HTML {
	html := convertOptionsToDataAttributes(htmlOptions.Clone())
	u := urlFor(url)
	if !truthy(html.Get("href")) {
		html.Set("href", u)
	}
	if name == nil {
		name = u
	}
	return ContentTag("a", name, html)
}

// LinkToIf は link_to_if(condition, name, url, html_options)。条件が偽なら name をエスケープして返す。
func LinkToIf(cond bool, name any, url any, htmlOptions *Hash) HTML {
	if cond {
		return LinkTo(name, url, htmlOptions)
	}
	return H(name)
}

// LinkToUnless は link_to_unless。
func LinkToUnless(cond bool, name any, url any, htmlOptions *Hash) HTML {
	return LinkToIf(!cond, name, url, htmlOptions)
}

var buttonTagMethodVerbs = map[string]bool{"patch": true, "put": true, "delete": true}

// ButtonTo は button_to(name, url, html_options)（ブロックなし）。
// Redmine（load_defaults 未指定）では button_to_generates_button_tag = false のため
// <input type="submit"> を出力する。url が false の場合は action 属性を出力しない。
func (v *View) ButtonTo(name any, url any, htmlOptions *Hash) HTML {
	return v.buttonTo(name, url, htmlOptions, nil, false)
}

// ButtonToContent はブロック付き button_to（content を中身とする <button> を出力）。
func (v *View) ButtonToContent(url any, htmlOptions *Hash, content any) HTML {
	return v.buttonTo(nil, url, htmlOptions, content, true)
}

func (v *View) buttonTo(name any, url any, htmlOptions *Hash, content any, block bool) HTML {
	html := htmlOptions.Clone()
	var action any
	if b, ok := url.(bool); ok && !b {
		action = nil
	} else {
		action = urlFor(url)
	}
	remote := html.Del("remote")
	params := html.Del("params")
	authToken := html.Del("authenticity_token")
	method := ""
	if m := html.Del("method"); IsPresent(m) {
		method = ToS(m)
	}
	var methodTag HTML
	if buttonTagMethodVerbs[method] {
		methodTag = MethodTag(method)
	}
	formMethod := "post"
	if method == "get" {
		formMethod = "get"
	}
	var formOptions *Hash
	if f, ok := html.Delete("form"); ok && truthy(f) {
		formOptions, _ = ToHash(f)
		formOptions = formOptions.Clone()
	} else {
		formOptions = &Hash{}
	}
	if !truthy(formOptions.Get("class")) {
		fc := html.Del("form_class")
		if !truthy(fc) {
			fc = "button_to"
		}
		formOptions.Set("class", fc)
	}
	formOptions.Set("method", formMethod)
	formOptions.Set("action", action)
	if truthy(remote) {
		formOptions.Set("data-remote", true)
	}
	var requestTokenTag HTML
	if formMethod == "post" {
		requestTokenTag = v.TokenTag(authToken)
	}
	html = convertOptionsToDataAttributes(html)
	html.Set("type", "submit")
	var button HTML
	if block {
		button = ContentTag("button", content, html)
	} else {
		if name == nil {
			name = action
		}
		html.Set("value", name)
		button = Tag("input", html)
	}
	inner := concatHTML(methodTag, button, requestTokenTag)
	if params != nil {
		for _, p := range toFormParams(params, "") {
			inner += Tag("input", NewHash("type", "hidden", "name", p[0], "value", p[1], "autocomplete", "off"))
		}
	}
	return ContentTag("form", inner, formOptions)
}

// toFormParams は UrlHelper#to_form_params（name でソート済みの [name, value] 列）。
func toFormParams(attr any, namespace string) [][2]string {
	var out [][2]string
	switch {
	case isHash(attr):
		h, _ := ToHash(attr)
		for _, e := range h.Entries() {
			prefix := e.Key
			if namespace != "" {
				prefix = namespace + "[" + e.Key + "]"
			}
			out = append(out, toFormParams(e.Value, prefix)...)
		}
	case isSlice(attr):
		for _, e := range toSlice(attr) {
			out = append(out, toFormParams(e, namespace+"[]")...)
		}
	default:
		out = append(out, [2]string{namespace, ToS(attr)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// URLEncode は ERB::Util.url_encode（英数字と -._~ 以外を %XX に変換）。
func URLEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// MailTo は mail_to(email_address, name, html_options)。
func MailTo(email any, name any, htmlOptions *Hash) HTML {
	html := htmlOptions.Clone()
	var extras []string
	for _, item := range []string{"cc", "bcc", "body", "subject", "reply_to"} {
		opt := html.Del(item)
		if IsBlank(opt) {
			continue
		}
		extras = append(extras, Dasherize(item)+"="+URLEncode(ToS(opt)))
	}
	ex := ""
	if len(extras) > 0 {
		ex = "?" + strings.Join(extras, "&")
	}
	addr := strings.ReplaceAll(URLEncode(ToS(email)), "%40", "@")
	html.Set("href", "mailto:"+addr+ex)
	if name == nil {
		name = email
	}
	return ContentTag("a", name, html)
}
