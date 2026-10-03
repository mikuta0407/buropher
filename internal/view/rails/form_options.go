// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import (
	"strings"
)

// extractSelectedAndDisabled は FormOptionsHelper#extract_selected_and_disabled。
// selected がハッシュ（selected: / disabled: キー）ならそれを分解する。
func extractSelectedAndDisabled(selected any) (sel any, disabled any) {
	if isHash(selected) {
		h, _ := ToHash(selected)
		return h.Fetch("selected", []any{}), h.Get("disabled")
	}
	items := toSlice(selected)
	if n := len(items); n > 0 && isHash(items[n-1]) {
		h, _ := ToHash(items[n-1])
		rest := items[:n-1]
		return h.Fetch("selected", toAnyList(rest)), h.Get("disabled")
	}
	return toAnyList(items), nil
}

func toAnyList(items []any) []any {
	if items == nil {
		return []any{}
	}
	return items
}

// stringList は Array(r).map(&:to_s)。
func stringList(v any) []string {
	var out []string
	for _, e := range toSlice(v) {
		out = append(out, ToS(e))
	}
	return out
}

func includes(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// optionHTMLAttributes は要素が配列の場合に含まれるハッシュをマージしたもの。
func optionHTMLAttributes(element any) *Hash {
	h := &Hash{}
	if isSlice(element) {
		for _, e := range toSlice(element) {
			if isHash(e) {
				eh, _ := ToHash(e)
				h.Update(eh)
			}
		}
	}
	return h
}

// optionTextAndValue は [text, value] を返す（配列なら first / last、それ以外は要素自身）。
func optionTextAndValue(element any) (any, any) {
	if isSlice(element) {
		var xs []any
		for _, e := range toSlice(element) {
			if !isHash(e) {
				xs = append(xs, e)
			}
		}
		if len(xs) == 0 {
			return nil, nil
		}
		return xs[0], xs[len(xs)-1]
	}
	return element, element
}

// OptionsForSelect は options_for_select(container, selected)。
// container は要素（文字列・数値）または [text, value, {html属性}] 配列のスライス、*Hash（{text => value}）。
// selected は値・値の配列、または selected: / disabled: を持つ *Hash。
func OptionsForSelect(container any, selected any) HTML {
	switch c := container.(type) {
	case string:
		return H(c)
	case HTML:
		return c
	}
	sel, dis := extractSelectedAndDisabled(selected)
	selList := stringList(sel)
	var disList []string
	if dis != nil {
		disList = stringList(dis)
	}
	var out []string
	for _, element := range containerElements(container) {
		attrs := optionHTMLAttributes(element)
		t, val := optionTextAndValue(element)
		text, value := ToS(t), ToS(val)
		if !truthy(attrs.Get("selected")) {
			attrs.Set("selected", includes(selList, value))
		}
		if !truthy(attrs.Get("disabled")) {
			if dis != nil {
				attrs.Set("disabled", includes(disList, value))
			} else {
				attrs.Set("disabled", attrs.Get("disabled"))
			}
		}
		attrs.Set("value", value)
		out = append(out, string(contentTagString("option", text, attrs, true)))
	}
	return HTML(strings.Join(out, "\n"))
}

// containerElements は options_for_select の container を要素列に変換する（Hash は [key, value] 対の列）。
func containerElements(container any) []any {
	if isHash(container) {
		h, _ := ToHash(container)
		out := make([]any, 0, h.Len())
		for _, e := range h.Entries() {
			out = append(out, []any{e.Key, e.Value})
		}
		return out
	}
	return toSlice(container)
}

// OptionsFromCollectionForSelect は options_from_collection_for_select(collection, value_method, text_method, selected)。
// 各要素の値は Send(element, method) で取り出す。
func OptionsFromCollectionForSelect(collection any, valueMethod, textMethod string, selected any) HTML {
	var options []any
	for _, element := range toSlice(collection) {
		options = append(options, []any{Send(element, textMethod), Send(element, valueMethod), &Hash{}})
	}
	sel, dis := extractSelectedAndDisabled(selected)
	return OptionsForSelect(options, NewHash("selected", sel, "disabled", dis))
}

// GroupedOptionsForSelect は grouped_options_for_select(grouped_options, selected_key, options)。
// grouped は [label, [options...], {html属性}?] のスライス、または *Hash（{label => options}）。
// options は prompt: / divider: を受け付ける。
func GroupedOptionsForSelect(grouped any, selectedKey any, options *Hash) HTML {
	prompt := options.Get("prompt")
	divider := options.Get("divider")
	var body strings.Builder
	if truthy(prompt) {
		body.WriteString(string(ContentTag("option", promptText(prompt), NewHash("value", ""))))
	}
	for _, container := range containerElements(grouped) {
		attrs := optionHTMLAttributes(container)
		var label, inner any
		if truthy(divider) {
			label, inner = divider, container
		} else {
			xs := toSlice(container)
			if len(xs) > 0 {
				label = xs[0]
			}
			if len(xs) > 1 {
				inner = xs[1]
			}
		}
		attrs = NewHash("label", label).Update(attrs)
		body.WriteString(string(ContentTag("optgroup", OptionsForSelect(inner, selectedKey), attrs)))
	}
	return HTML(body.String())
}

func promptText(prompt any) any {
	if s, ok := prompt.(string); ok {
		return s
	}
	if s, ok := prompt.(HTML); ok {
		return s
	}
	return "Please select"
}
