// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package pagination は Redmine::Pagination::Paginator（lib/redmine/pagination.rb）と
// ApplicationController の per_page_option / api_offset_and_limit の移植。
//
// 画面側（pagination_links_full / per_page_links）は internal/helper の pagination_links_full
// テンプレート関数が *Paginator を受け取って描画する。
//
//	limit := c.PerPageOption()                         // handler.Req（params[:per_page] / session）
//	pages := pagination.New(count, limit, c.Params().String("page"))
//	rows := repository.Xxx(ctx, q, ..., pages.PerPage, pages.Offset())
//	c.Render("xxx/index", map[string]any{"Pages": pages, "Count": count, ...})
//	// テンプレート: {{pagination_links_full .Pages .Count}}
package pagination

import (
	"strconv"
	"strings"
)

// Paginator は Redmine::Pagination::Paginator。
type Paginator struct {
	ItemCount int
	PerPage   int
	Page      int
	// PageParam はページ番号のパラメータ名（既定 "page"）。
	PageParam string
}

// New は Paginator.new(item_count, per_page, page, page_param)。page は params[:page]（to_i、1 未満は 1）。
func New(itemCount, perPage int, page any, pageParam ...string) *Paginator {
	p := &Paginator{ItemCount: itemCount, PerPage: perPage, Page: toI(page), PageParam: "page"}
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PerPage < 1 {
		p.PerPage = 1
	}
	if len(pageParam) > 0 && pageParam[0] != "" {
		p.PageParam = pageParam[0]
	}
	return p
}

// toI は Ruby の to_i（先頭の数字のみ。nil は 0）。
func toI(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case int:
		return x
	case int64:
		return int(x)
	case string:
		return RubyToI(x)
	}
	return 0
}

// RubyToI は String#to_i（先頭の空白・符号・数字列のみを読む）。
func RubyToI(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	end := 0
	if end < len(s) && (s[end] == '-' || s[end] == '+') {
		end++
	}
	start := end
	for end < len(s) && ((s[end] >= '0' && s[end] <= '9') || (s[end] == '_' && end > start)) {
		end++
	}
	n, err := strconv.Atoi(strings.ReplaceAll(s[:end], "_", ""))
	if err != nil {
		return 0
	}
	return n
}

// Offset は (page - 1) * per_page。
func (p *Paginator) Offset() int { return (p.Page - 1) * p.PerPage }

// FirstPage は first_page（件数 0 なら 0 = nil）。
func (p *Paginator) FirstPage() int {
	if p.ItemCount > 0 {
		return 1
	}
	return 0
}

// PreviousPage は previous_page（無ければ 0）。
func (p *Paginator) PreviousPage() int {
	if p.Page > 1 {
		return p.Page - 1
	}
	return 0
}

// NextPage は next_page（無ければ 0）。
func (p *Paginator) NextPage() int {
	if p.LastItem() < p.ItemCount {
		return p.Page + 1
	}
	return 0
}

// LastPage は last_page（件数 0 なら 0）。
func (p *Paginator) LastPage() int {
	if p.ItemCount > 0 {
		return (p.ItemCount-1)/p.PerPage + 1
	}
	return 0
}

// MultiplePages は multiple_pages?。
func (p *Paginator) MultiplePages() bool { return p.PerPage < p.ItemCount }

// FirstItem は first_item。
func (p *Paginator) FirstItem() int {
	if p.ItemCount == 0 {
		return 0
	}
	return p.Offset() + 1
}

// LastItem は last_item。
func (p *Paginator) LastItem() int {
	l := p.FirstItem() + p.PerPage - 1
	return min(l, p.ItemCount)
}

// LinkedPages は linked_pages（先頭・現在・末尾と現在の前後 2 ページ。1 ページ以下なら空）。
func (p *Paginator) LinkedPages() []int {
	if p.ItemCount == 0 {
		return nil
	}
	set := map[int]bool{}
	first, last := p.FirstPage(), p.LastPage()
	for _, n := range []int{first, p.Page, last} {
		set[n] = true
	}
	for n := p.Page - 2; n <= p.Page+2; n++ {
		if n > first && n < last {
			set[n] = true
		}
	}
	var pages []int
	for n := range set {
		pages = append(pages, n)
	}
	// 昇順
	for i := 1; i < len(pages); i++ {
		for j := i; j > 0 && pages[j-1] > pages[j]; j-- {
			pages[j-1], pages[j] = pages[j], pages[j-1]
		}
	}
	if len(pages) > 1 {
		return pages
	}
	return nil
}

// PerPageOptions は per_page_options(selected, item_count)（Setting.per_page_options_array を絞り込む）。
// itemCount < 0 は nil（件数指定なし）。
func PerPageOptions(options []int, selected, itemCount int) []int {
	if itemCount >= 0 && len(options) > 0 {
		var max int
		if itemCount > options[0] {
			max = itemCount
			for _, v := range options {
				if v >= itemCount {
					max = v
					break
				}
			}
		} else {
			max = itemCount
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

// APIOffsetAndLimit は ApplicationController#api_offset_and_limit。
// offset / limit / page は params の値（未指定は ""）。
func APIOffsetAndLimit(offset, limit, page string) (int, int) {
	off := -1
	if strings.TrimSpace(offset) != "" {
		off = max(RubyToI(offset), 0)
	}
	lim := RubyToI(limit)
	if lim < 1 {
		lim = 25
	} else if lim > 100 {
		lim = 100
	}
	if off < 0 && strings.TrimSpace(page) != "" {
		off = max((RubyToI(page)-1)*lim, 0)
	}
	if off < 0 {
		off = 0
	}
	return off, lim
}
