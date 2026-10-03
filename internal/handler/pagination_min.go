package handler

import (
	"html/template"
	"net/url"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは Redmine::Pagination（Paginator と pagination_links_full）の最小移植。
//
// TODO(dedupe): admin users の移植で共通のページネーション（internal/pagination, helper/pagination.go）を
// 作っているため、マージ後はそちらに置き換える。

// minPaginator は Redmine::Pagination::Paginator。
type minPaginator struct {
	ItemCount int
	PerPage   int
	Page      int
}

func newMinPaginator(count, perPage int, page string) *minPaginator {
	p := int(rubyToI(page))
	if p < 1 {
		p = 1
	}
	if perPage < 1 {
		perPage = 25
	}
	return &minPaginator{ItemCount: count, PerPage: perPage, Page: pagination.ClampPage(p, perPage)}
}

// rubyToI は String#to_i。
func rubyToI(s string) int64 {
	s = strings.TrimSpace(s)
	end := 0
	if end < len(s) && (s[end] == '-' || s[end] == '+') {
		end++
	}
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.ParseInt(s[:end], 10, 64)
	return n
}

func (p *minPaginator) Offset() int { return (p.Page - 1) * p.PerPage }

func (p *minPaginator) firstItem() int {
	if p.ItemCount == 0 {
		return 0
	}
	return p.Offset() + 1
}

func (p *minPaginator) lastItem() int { return min(p.firstItem()+p.PerPage-1, p.ItemCount) }

func (p *minPaginator) lastPage() int {
	if p.ItemCount == 0 {
		return 0
	}
	return (p.ItemCount-1)/p.PerPage + 1
}

func (p *minPaginator) multiplePages() bool { return p.PerPage < p.ItemCount }

func (p *minPaginator) linkedPages() []int {
	if p.ItemCount == 0 {
		return nil
	}
	seen := map[int]bool{}
	var pages []int
	add := func(n int) {
		if !seen[n] {
			seen[n] = true
			pages = append(pages, n)
		}
	}
	first, last := 1, p.lastPage()
	add(first)
	add(p.Page)
	add(last)
	for d := -2; d <= 2; d++ {
		if n := p.Page + d; n > first && n < last {
			add(n)
		}
	}
	for i := 1; i < len(pages); i++ {
		for j := i; j > 0 && pages[j] < pages[j-1]; j-- {
			pages[j], pages[j-1] = pages[j-1], pages[j]
		}
	}
	if len(pages) > 1 {
		return pages
	}
	return nil
}

// perPageOptions は per_page_options(selected, item_count)。
func perPageOptions(options []int, selected, itemCount int) []int {
	if len(options) > 0 {
		max := itemCount
		if itemCount > options[0] {
			max = itemCount
			for _, v := range options {
				if v >= itemCount {
					max = v
					break
				}
			}
		}
		var out []int
		for _, v := range options {
			if v <= max || v == selected {
				out = append(out, v)
			}
		}
		options = out
	}
	if len(options) == 0 || (len(options) == 1 && options[0] == selected) {
		return nil
	}
	return options
}

// perPageOptionValues は Setting.per_page_options_array。
func (a *App) perPageOptionValues() []int {
	var out []int
	for _, s := range strings.Split(a.Settings.String("per_page_options"), ",") {
		n := int(rubyToI(s))
		if n > 0 {
			out = append(out, n)
		}
	}
	// sort（Setting.per_page_options_array は sort 済み）
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// perPageOptionMin は ApplicationController#per_page_option（params[:per_page] が選択肢にあれば session に保存）。
func (c *Req) perPageOptionMin() int {
	opts := c.App.perPageOptionValues()
	if v := c.Params().String("per_page"); v != "" {
		n := int(rubyToI(v))
		for _, o := range opts {
			if o == n {
				if s := c.Session(); s != nil {
					s.Set("per_page", n)
				}
				return n
			}
		}
	}
	if s := c.Session(); s != nil {
		if n := int(s.GetInt("per_page")); n > 0 {
			return n
		}
	}
	if len(opts) > 0 {
		return opts[0]
	}
	return 25
}

// paginationLink はページ番号のリンク（text, パラメータ, オプション）を作る関数。
type paginationLink func(text string, params map[string]string, opts *rails.Hash) template.HTML

// queryParamsLink は link_to text, {:params => request.query_parameters.merge(parameters)}, options。
func (c *Req) queryParamsLink(text string, params map[string]string, opts *rails.Hash) template.HTML {
	v := url.Values{}
	for k, vals := range c.R.URL.Query() {
		v[k] = vals
	}
	for k, s := range params {
		if s == "" {
			v.Del(k)
		} else {
			v.Set(k, s)
		}
	}
	u := c.R.URL.Path
	if q := railsToQuery(v); q != "" {
		u += "?" + q
	}
	return rails.LinkTo(text, u, opts)
}

// paginationLinksFull は pagination_links_full(paginator, count, per_page_links:)。link が nil なら
// 現在のパスとクエリパラメータでリンクする。count < 0 は count = nil（件数リンクなし）。
func (c *Req) paginationLinksFull(p *minPaginator, count int, perPageLinks bool, link paginationLink) template.HTML {
	if link == nil {
		link = c.queryParamsLink
	}
	if count < 0 {
		perPageLinks = false
	}
	var b strings.Builder
	b.WriteString(`<ul class="pages">`)
	if p.multiplePages() {
		text := "« " + c.L("label_previous")
		if p.Page > 1 {
			b.WriteString(string(rails.ContentTag("li", link(text, map[string]string{"page": strconv.Itoa(p.Page - 1)}, rails.NewHash("accesskey", "p")), rails.NewHash("class", "previous page"))))
		} else {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", text, nil), rails.NewHash("class", "previous"))))
		}
	}
	prev := 0
	for _, page := range p.linkedPages() {
		if prev != 0 && prev != page-1 {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", template.HTML("&hellip;"), nil), rails.NewHash("class", "spacer"))))
		}
		if page == p.Page {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", strconv.Itoa(page), nil), rails.NewHash("class", "current"))))
		} else {
			b.WriteString(string(rails.ContentTag("li", link(strconv.Itoa(page), map[string]string{"page": strconv.Itoa(page)}, nil), rails.NewHash("class", "page"))))
		}
		prev = page
	}
	if p.multiplePages() {
		text := c.L("label_next") + " »"
		if p.lastItem() < p.ItemCount {
			b.WriteString(string(rails.ContentTag("li", link(text, map[string]string{"page": strconv.Itoa(p.Page + 1)}, rails.NewHash("accesskey", "n")), rails.NewHash("class", "next page"))))
		} else {
			b.WriteString(string(rails.ContentTag("li", rails.ContentTag("span", text, nil), rails.NewHash("class", "next"))))
		}
	}
	b.WriteString("</ul>")
	info := string(rails.ContentTag("span", "("+strconv.Itoa(p.firstItem())+"-"+strconv.Itoa(p.lastItem())+"/"+strconv.Itoa(p.ItemCount)+")", rails.NewHash("class", "items"))) + " "
	if perPageLinks {
		if values := perPageOptions(c.App.perPageOptionValues(), p.PerPage, p.ItemCount); len(values) > 0 {
			parts := make([]string, len(values))
			for i, n := range values {
				if n == p.PerPage {
					parts[i] = string(rails.ContentTag("span", strconv.Itoa(n), rails.NewHash("class", "selected")))
				} else {
					parts[i] = string(link(strconv.Itoa(n), map[string]string{"per_page": strconv.Itoa(n), "page": ""}, nil))
				}
			}
			info += string(rails.ContentTag("span", template.HTML(c.L("label_display_per_page", strings.Join(parts, ", "))), rails.NewHash("class", "per-page")))
		}
	}
	b.WriteString(string(rails.ContentTag("span", template.HTML(info), nil)))
	return template.HTML(b.String())
}
