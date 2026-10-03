// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	ttemplate "text/template"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは SearchHelper（highlight_tokens, type_label, render_results_by_type）と
// list_autofill_data_attributes の移植。

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			"highlight_tokens": HighlightTokens,
			"search_type_label": func(t string) string {
				return pg().l("label_" + ActivitySingularize(t) + "_plural")
			},
			// list_autofill_data_attributes（Setting.text_formatting が空なら {}）
			"list_autofill_data_attributes": func() *rails.Hash {
				tf := pg().setting("text_formatting")
				if strings.TrimSpace(tf) == "" {
					return rails.NewHash()
				}
				return rails.NewHash("controller", "list-autofill",
					"action", "beforeinput->list-autofill#handleBeforeInput",
					"list_autofill_text_formatting_param", tf)
			},
			"param_string": func(key string) string { return pg().Params().String(key) },
			// search_input_data は {:auto_complete => true}.merge(list_autofill_data_attributes)。
			"search_input_data": func() *rails.Hash {
				h := rails.NewHash("auto_complete", true)
				if tf := pg().setting("text_formatting"); strings.TrimSpace(tf) != "" {
					h.Set("controller", "list-autofill").Set("action", "beforeinput->list-autofill#handleBeforeInput").
						Set("list_autofill_text_formatting_param", tf)
				}
				return h
			},
			"truncate_chars": func(s string, n int) string { return rails.StringTruncate(s, n, "", nil) },
		}
	})
}

// HighlightTokens は SearchHelper#highlight_tokens（トークンが無ければ text をそのまま返す）。
func HighlightTokens(text string, tokens []string) any {
	if len(tokens) == 0 {
		return text
	}
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = regexp.QuoteMeta(t)
	}
	re := regexp.MustCompile("(?i)(" + strings.Join(quoted, "|") + ")")
	parts := rubySplitWithCaptures(text, re)
	var b strings.Builder
	n := 0 // result.length（文字数）
	for i, words := range parts {
		if n > 1200 {
			b.WriteString("...")
			break
		}
		var s string
		if i%2 == 0 {
			w := words
			if utf8.RuneCountInString(w) > 100 {
				rs := []rune(w)
				w = string(rs[:45]) + " ... " + string(rs[len(rs)-45:])
			}
			s = string(rails.H(w))
		} else {
			t := 0
			lw := strings.ToLower(words)
			for k, tok := range tokens {
				if tok == lw {
					t = k % 4
					break
				}
			}
			s = `<span class="highlight token-` + strconv.Itoa(t) + `">` + string(rails.H(words)) + `</span>`
		}
		b.WriteString(s)
		n += utf8.RuneCountInString(s)
	}
	return html(b.String())
}

// rubySplitWithCaptures は String#split(regexp)（キャプチャを含め、末尾の空要素を除く）。
func rubySplitWithCaptures(s string, re *regexp.Regexp) []string {
	var out []string
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		if m[1] == m[0] {
			continue
		}
		out = append(out, s[last:m[0]])
		if m[2] >= 0 {
			out = append(out, s[m[2]:m[3]])
		}
		last = m[1]
	}
	out = append(out, s[last:])
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// RenderResultsByType は SearchHelper#render_results_by_type。order は result_count_by_type のキーの順。
// リンクは link_to(h(text), :q => params[:q], :titles_only => ..., :all_words => ..., :scope => ..., t => 1)。
func (d *Deps) RenderResultsByType(p *Page, path string, counts map[string]int, order []string) html {
	keys := append([]string(nil), order...)
	sort.SliceStable(keys, func(i, j int) bool { return counts[keys[i]] < counts[keys[j]] })
	params := p.Params()
	var links []string
	for i := len(keys) - 1; i >= 0; i-- {
		t := keys[i]
		c := counts[t]
		if c == 0 {
			continue
		}
		text := p.l("label_"+ActivitySingularize(t)+"_plural") + " (" + strconv.Itoa(c) + ")"
		h := rails.NewHash()
		for _, k := range []string{"q", "titles_only", "all_words", "scope"} {
			if v, ok := params.Get(k); ok {
				h.Set(k, v)
			}
		}
		h.Set(t, 1)
		links = append(links, string(rails.ContentTag("li", rails.LinkTo(rails.H(text), URLWithQuery(path, h), nil), nil)))
	}
	if len(links) == 0 {
		return ""
	}
	return html("<ul>" + strings.Join(links, " ") + "</ul>")
}
