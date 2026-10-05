// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは Redmine::Pagination::Helper（pagination_links_full / pagination_links_each /
// per_page_links）と、URL 生成に使う Hash#to_query の移植。

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			// pagination_links_full(paginator, count = nil, per_page_links: true)
			"pagination_links_full": func(p *pagination.Paginator, args ...any) html {
				return paginationLinksFull(pg(), p, args...)
			},
			"per_page_links": func(p *pagination.Paginator) html {
				return perPageLinks(pg(), p, pg().paginationLink)
			},
			"to_query": func(v any) string { return ToQuery(v) },
		}
	})
}

// QueryParameters は request.query_parameters（URL のクエリ文字列だけを解析したもの）。
func (p *Page) QueryParameters() *httpx.Params {
	if p == nil || p.Request == nil {
		return httpx.NewParams()
	}
	qp, err := httpx.ParseRailsQuery(p.Request.URL.RawQuery)
	if err != nil || qp == nil {
		return httpx.NewParams()
	}
	return qp
}

// paginationLink は pagination_links_full の既定のリンク:
// link_to text, {:params => request.query_parameters.merge(parameters)}, options。
// URL は現在のパス（url_for はコントローラ・アクションから同じパスを生成する）。
func (p *Page) paginationLink(text string, params *rails.Hash, opts *rails.Hash) html {
	qp := p.QueryParameters()
	for _, e := range params.Entries() {
		qp.Set(e.Key, e.Value)
	}
	path := "/"
	if p.Request != nil {
		path = p.Request.URL.Path
	}
	return rails.LinkTo(text, URLWithQuery(path, qp), opts)
}

// URLWithQuery は path にクエリ（to_query。nil の値は除く）を付ける。
func URLWithQuery(path string, params any) string {
	if q := ToQuery(params); q != "" {
		return path + "?" + q
	}
	return path
}

// paginationLinksFull は pagination_links_full(paginator, count, options)。
func paginationLinksFull(pg *Page, p *pagination.Paginator, args ...any) html {
	var count any
	opts := rails.NewHash()
	for _, a := range args {
		if h, ok := a.(*rails.Hash); ok {
			opts = h
		} else {
			count = a
		}
	}
	perPage := opts.Fetch("per_page_links", true)
	if count == nil {
		perPage = false
	}
	return paginationLinksEach(pg, p, perPage != false && truthy(perPage), pg.paginationLink)
}

type linkFunc func(text string, params *rails.Hash, opts *rails.Hash) html

// paginationLinksEach は pagination_links_each。
func paginationLinksEach(pg *Page, p *pagination.Paginator, perPage bool, link linkFunc) html {
	param := p.PageParam
	var b strings.Builder
	b.WriteString(`<ul class="pages">`)
	if p.MultiplePages() {
		text := "« " + pg.l("label_previous")
		if prev := p.PreviousPage(); prev > 0 {
			b.WriteString(string(rails.ContentTag("li", link(text, rails.NewHash(param, prev), rails.NewHash("accesskey", pg.accesskey("previous"))), rails.NewHash("class", "previous page"))))
		} else {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", text, nil), rails.NewHash("class", "previous"))))
		}
	}
	prev := 0
	for _, page := range p.LinkedPages() {
		if prev != 0 && prev != page-1 {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", rails.Raw("&hellip;"), nil), rails.NewHash("class", "spacer"))))
		}
		if page == p.Page {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", strconv.Itoa(page), nil), rails.NewHash("class", "current"))))
		} else {
			b.WriteString(string(rails.ContentTag("li", link(strconv.Itoa(page), rails.NewHash(param, page), rails.NewHash()), rails.NewHash("class", "page"))))
		}
		prev = page
	}
	if p.MultiplePages() {
		text := pg.l("label_next") + " »"
		if next := p.NextPage(); next > 0 {
			b.WriteString(string(rails.ContentTag("li", link(text, rails.NewHash(param, next), rails.NewHash("accesskey", pg.accesskey("next"))), rails.NewHash("class", "next page"))))
		} else {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", text, nil), rails.NewHash("class", "next"))))
		}
	}
	b.WriteString("</ul>")

	info := string(rails.ContentTag("span", "("+strconv.Itoa(p.FirstItem())+"-"+strconv.Itoa(p.LastItem())+"/"+strconv.Itoa(p.ItemCount)+")", rails.NewHash("class", "items"))) + " "
	if perPage {
		if links := perPageLinks(pg, p, link); links != "" {
			info += string(rails.ContentTag("span", links, rails.NewHash("class", "per-page")))
		}
	}
	b.WriteString(string(rails.ContentTag("span", rails.Raw(info), nil)))
	return html(b.String())
}

// perPageLinks は per_page_links（選択肢が無ければ ""）。
func perPageLinks(pg *Page, p *pagination.Paginator, link linkFunc) html {
	var options []int
	if pg.Settings != nil {
		options = pg.Settings.PerPageOptionsArray()
	} else {
		options = []int{25, 50, 100}
	}
	values := pagination.PerPageOptions(options, p.PerPage, p.ItemCount)
	if len(values) == 0 {
		return ""
	}
	links := make([]string, len(values))
	for i, n := range values {
		if n == p.PerPage {
			links[i] = string(rails.ContentTag("span", strconv.Itoa(n), rails.NewHash("class", "selected")))
		} else {
			links[i] = string(link(strconv.Itoa(n), rails.NewHash("per_page", n, p.PageParam, nil), rails.NewHash()))
		}
	}
	return html(pg.l("label_display_per_page", strings.Join(links, ", ")))
}

// ---------------------------------------------------------------- to_query

// ToQuery は Hash#to_query（キーをソートし、ネストしたハッシュ・配列を key[sub] / key[] で表す）。
// トップレベルの nil の値は url_for と同じく除く。v は *httpx.Params / *rails.Hash / map[string]any。
func ToQuery(v any) string {
	var parts []string
	for _, kv := range queryEntries(v) {
		if kv.v == nil {
			continue
		}
		parts = append(parts, toQueryValue(kv.v, kv.k)...)
	}
	return strings.Join(parts, "&")
}

type queryKV struct {
	k string
	v any
}

// queryEntries はハッシュ類の (キー, 値) をキーの昇順で返す。ハッシュでなければ nil。
func queryEntries(v any) []queryKV {
	var out []queryKV
	switch x := v.(type) {
	case *httpx.Params:
		if x == nil {
			return nil
		}
		x.Each(func(k string, val any) { out = append(out, queryKV{k, val}) })
	case *rails.Hash:
		if x == nil {
			return nil
		}
		for _, e := range x.Entries() {
			out = append(out, queryKV{e.Key, e.Value})
		}
	case map[string]any:
		for k, val := range x {
			out = append(out, queryKV{k, val})
		}
	case url.Values:
		for k, vals := range x {
			if len(vals) == 1 && !strings.HasSuffix(k, "[]") {
				out = append(out, queryKV{k, vals[0]})
			} else {
				out = append(out, queryKV{strings.TrimSuffix(k, "[]"), stringsToAny(vals)})
			}
		}
	default:
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].k < out[j].k })
	return out
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// toQueryValue は Object#to_query / Array#to_query / Hash#to_query(namespace)。
func toQueryValue(v any, key string) []string {
	switch x := v.(type) {
	case []any:
		if len(x) == 0 {
			// [].to_query(key) => "key%5B%5D="
			return []string{url.QueryEscape(key+"[]") + "="}
		}
		var out []string
		for _, e := range x {
			out = append(out, toQueryValue(e, key+"[]")...)
		}
		return out
	case []string:
		return toQueryValue(stringsToAny(x), key)
	case []int64:
		a := make([]any, len(x))
		for i, n := range x {
			a[i] = n
		}
		return toQueryValue(a, key)
	case []int:
		a := make([]any, len(x))
		for i, n := range x {
			a[i] = n
		}
		return toQueryValue(a, key)
	}
	if ents := queryEntries(v); ents != nil || isHashLike(v) {
		var out []string
		for _, kv := range ents {
			// Hash#to_query は nil の値を含める（Rails 8 では "key%5Bsub%5D"）が、空のハッシュは何も出さない
			out = append(out, toQueryValue(kv.v, key+"["+kv.k+"]")...)
		}
		if len(out) == 0 {
			// {}.to_query(key) => "key="? Rails 7 は CGI.escape(namespace) + "=" を返さず空になる
			return nil
		}
		return out
	}
	if v == nil {
		// Rails 8 (ActiveSupport 8): NilClass#to_query(key) は "=" を付けずにキーだけを返す
		return []string{url.QueryEscape(key)}
	}
	s := httpx.ValueString(v)
	if b, ok := v.(bool); ok {
		s = strconv.FormatBool(b)
	}
	return []string{url.QueryEscape(key) + "=" + url.QueryEscape(s)}
}

func isHashLike(v any) bool {
	switch v.(type) {
	case *httpx.Params, *rails.Hash, map[string]any, url.Values:
		return true
	}
	return false
}
