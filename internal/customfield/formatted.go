// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/textformat/sanitize"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// FormattedValue は formatted_value(view, custom_field, value, customized, html)。
// html が偽ならキャスト後の値（string / int64 / float64 / bool / time.Time / Option / []any）を返し、
// 真なら表示用の HTML（rails.HTML）または値を返す（呼び出し側で format_object する）。
func (f *Format) FormattedValue(env *Env, cf *CustomField, value any, customized *Customized, html bool) any {
	if f.formatted != nil {
		return f.formatted(f, env, cf, value, customized, html)
	}
	return baseFormatted(f, env, cf, value, customized, html)
}

// FormattedCustomValue は formatted_custom_value(view, custom_value, html)。
func (f *Format) FormattedCustomValue(env *Env, cv *CustomValue, html bool) any {
	return f.FormattedValue(env, cv.CustomField, cv.Value, cv.Customized, html)
}

// baseFormatted は Base#formatted_value（url_pattern があれば各値をリンクにする）。
func baseFormatted(f *Format, env *Env, cf *CustomField, value any, customized *Customized, html bool) any {
	casted := f.Cast(env, cf, value, customized)
	if html && trimSpace(cf.URLPattern()) != "" {
		type link struct{ text, url string }
		var links []link
		var singles []any
		switch c := casted.(type) {
		case nil:
		case []any:
			singles = c
		default:
			singles = []any{c}
		}
		for _, sv := range singles {
			text := rails.ToS(env.formatObject(sv, false))
			links = append(links, link{text, URLFromPattern(cf, castedToS(sv), customized)})
		}
		sort.SliceStable(links, func(i, j int) bool { return links[i].text < links[j].text })
		parts := make([]string, len(links))
		for i, l := range links {
			parts[i] = string(sanitizedLink(rails.H(l.text), l.url))
		}
		return rails.HTML(strings.Join(parts, ", "))
	}
	return casted
}

// castedToS は url_from_pattern に渡す value.to_s（レコードは id ではなく to_s。Redmine と同じ）。
func castedToS(v any) string {
	switch x := v.(type) {
	case Option:
		return x.Label
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return rails.ToS(x)
	}
	return rails.ToS(v)
}

func formattedString(f *Format, env *Env, cf *CustomField, value any, customized *Customized, html bool) any {
	if html {
		switch {
		case trimSpace(cf.URLPattern()) != "":
			return baseFormatted(f, env, cf, value, customized, html)
		case cf.TextFormatting() == "full":
			return textilizable(env, rails.ToS(value), customized)
		}
	}
	return rails.ToS(value)
}

func formattedText(f *Format, env *Env, cf *CustomField, value any, customized *Customized, html bool) any {
	if !html {
		return rails.ToS(value)
	}
	if isBlank(value) {
		return rails.HTML("")
	}
	if cf.TextFormatting() == "full" {
		return textilizable(env, rails.ToS(value), customized)
	}
	return rails.SimpleFormat(rails.H(rails.ToS(value)), nil, nil)
}

func formattedLink(f *Format, env *Env, cf *CustomField, value any, customized *Customized, html bool) any {
	if html && !isBlank(value) {
		s := rails.ToS(value)
		var u string
		if trimSpace(cf.URLPattern()) != "" {
			u = URLFromPattern(cf, s, customized)
		} else {
			u = s
			if !schemeRe.MatchString(u) {
				u = "http://" + u
			}
		}
		return sanitizedLink(rails.H(rails.StringTruncate(s, 40, "...", nil)), u)
	}
	return rails.ToS(value)
}

var schemeRe = regexp.MustCompile(`(?i)\A[a-z]+://`)

// LinkValueHTML は url_pattern の無いリンク形式の値の HTML（formatted_value の link 形式。
// スキームが無ければ http:// を補い、危険なスキームの href は付けない）。
func LinkValueHTML(s string) rails.HTML {
	u := s
	if !schemeRe.MatchString(u) {
		u = "http://" + u
	}
	return sanitizedLink(rails.H(rails.StringTruncate(s, 40, "...", nil)), u)
}

func formattedProgressbar(f *Format, env *Env, cf *CustomField, value any, customized *Customized, html bool) any {
	if !html {
		return rails.ToS(value)
	}
	n := int(RubyToI(rails.ToS(value)))
	text := strconv.Itoa(n) + "%"
	legend := ""
	if env != nil && env.ActionName == "show" {
		legend = text
	}
	if env != nil && env.ProgressBar != nil {
		return env.ProgressBar(n, legend)
	}
	return ProgressBar(n, legend)
}

// ProgressBar は ApplicationHelper#progress_bar(pct, legend:)（単一値の場合）。
func ProgressBar(pct int, legend string) rails.HTML {
	done := 0
	todo := 100 - pct
	var cells rails.HTML
	if pct > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(pct)+"%;", "class", "closed", "title", strconv.Itoa(pct)+"%"))
	}
	if done > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(done)+"%;", "class", "done"))
	}
	if todo > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(todo)+"%;", "class", "todo"))
	}
	return rails.ContentTag("table", rails.ContentTag("tr", cells, nil), rails.NewHash("class", "progress progress-"+strconv.Itoa(pct))) +
		rails.ContentTag("p", legend, rails.NewHash("class", "percent"))
}

func textilizable(env *Env, text string, customized *Customized) rails.HTML {
	if env != nil && env.Textilizable != nil {
		return env.Textilizable(text, customized)
	}
	return rails.SimpleFormat(rails.H(text), nil, nil)
}

// URLFromPattern は url_from_pattern(custom_field, value, customized)。
func URLFromPattern(cf *CustomField, value string, customized *Customized) string {
	u := cf.URLPattern()
	var id, projectID, projectIdentifier string
	if customized != nil {
		id = strconv.FormatInt(customized.ID, 10)
		if customized.ProjectID != 0 {
			projectID = strconv.FormatInt(customized.ProjectID, 10)
		}
		projectIdentifier = customized.ProjectIdentifier
	}
	u = strings.ReplaceAll(u, "%value%", EncodeComponent(value))
	u = strings.ReplaceAll(u, "%id%", EncodeComponent(id))
	u = strings.ReplaceAll(u, "%project_id%", EncodeComponent(projectID))
	u = strings.ReplaceAll(u, "%project_identifier%", EncodeComponent(projectIdentifier))
	if re := cf.RegexpString(); trimSpace(re) != "" {
		var matches []string
		matched := false
		u = mTokenRe.ReplaceAllStringFunc(u, func(tok string) string {
			m, _ := strconv.Atoi(mTokenRe.FindStringSubmatch(tok)[1])
			if !matched {
				matched = true
				if r, err := CompileRegexp(re); err == nil {
					matches = r.FindStringSubmatch(value)
				}
			}
			if matches == nil {
				return ""
			}
			if m < len(matches) {
				return EncodeComponent(matches[m])
			}
			return ""
		})
	}
	return u
}

var (
	mTokenRe     = regexp.MustCompile(`%m(\d+)%`)
	urlTokenRe   = regexp.MustCompile(`%(value|id|project_id|project_identifier|m\d+)%`)
	invalidURIRe = regexp.MustCompile(`[^A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]`)
)

func urlPatternWithoutTokens(p string) string { return urlTokenRe.ReplaceAllString(p, "") }

// URIWithSafeScheme は Redmine::Helpers::URL#uri_with_safe_scheme?（http, https, ftp, mailto, スキームなし）。
func URIWithSafeScheme(u string) bool {
	if !strings.Contains(u, ":") {
		return true
	}
	scheme, ok := parseScheme(u)
	if !ok {
		return false
	}
	switch scheme {
	case "", "http", "https", "ftp", "mailto":
		return true
	}
	return false
}

// parseScheme は URI.parse(u).scheme（RFC 3986 に反する文字を含む場合は解析失敗）。
func parseScheme(u string) (string, bool) {
	if invalidURIRe.MatchString(u) {
		return "", false
	}
	p, err := url.Parse(u)
	if err != nil {
		return "", false
	}
	return p.Scheme, true
}

// EncodeComponent は Addressable::URI.encode_component(value)（予約文字・非予約文字以外をパーセントエンコード）。
func EncodeComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.IndexByte("-._~:/?#[]@!$&'()*+,;=", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)>>4, 16)) + strings.ToUpper(strconv.FormatInt(int64(c)&15, 16)))
	}
	return b.String()
}

// sanitizedLink は sanitize_html(link_to(text, url))（HtmlSanitizer の ExternalLinksFilter が外部リンクに
// class="external"、mailto に class="email" を付ける。許可されないスキームの href は除かれる）。
func sanitizedLink(text rails.HTML, u string) rails.HTML {
	attrs := rails.NewHash()
	scheme := ""
	if !strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "#") && strings.Contains(u, ":") {
		if s, ok := parseScheme(u); ok {
			scheme = strings.ToLower(s)
		}
	}
	// 利用者が設定した URL（Redmine では link_to に文字列で渡すため relative_url_root を前置しない）
	if !sanitize.URIWithLinkSafeScheme(strings.TrimSpace(u)) {
		// SanitizationFilter の uri_with_link_safe_scheme?（URL として解析できない値も正規表現で判定する）
		return rails.ContentTag("a", text, attrs)
	}
	switch scheme {
	case "":
		attrs.Set("href", rails.RawURL(u))
	case "http", "https", "ftp", "mailto":
		attrs.Set("href", rails.RawURL(u))
		if scheme == "mailto" {
			attrs.Set("class", "email")
		} else {
			attrs.Set("class", "external")
		}
	default:
		// SanitizationFilter が href を除く
	}
	return rails.ContentTag("a", text, attrs)
}
