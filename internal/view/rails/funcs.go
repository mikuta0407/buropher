// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import "github.com/mikuta0407/buropher/internal/urlroot"

// テンプレート用の関数群。Ruby のメソッド名・引数順をそのまま使えるように、
// 省略可能な位置引数と末尾のオプションハッシュ（hash "k" v ...）を可変長引数で受ける。

// argAt は args[i]（無ければ nil）。
func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// splitOpts は末尾がハッシュならそれをオプションとして切り出す（extract_options!）。
func splitOpts(args []any) ([]any, *Hash) {
	if n := len(args); n > 0 && isHash(args[n-1]) {
		h, _ := ToHash(args[n-1])
		return args[:n-1], h.Clone()
	}
	return args, &Hash{}
}

// toBool は Ruby の真偽判定。
func toBool(v any) bool { return truthy(v) }

// FuncMap はテンプレートに登録する ActionView ヘルパー群を返す。
// View の状態（CSRF トークン、cycle の状態など）に依存する関数も含むため、リクエストごとに作ること。
func (v *View) FuncMap() map[string]any {
	return map[string]any{
		// --- 値の構築・安全性 ---
		"hash":        NewHash,
		"sym":         func(s string) Symbol { return Symbol(s) },
		"h":           H,
		"html_escape": H,
		"raw":         Raw,
		"safe_join":   SafeJoin,
		"escape_once": EscapeOnce,
		"token_list":  TokenList,
		"class_names": TokenList,
		"to_json":     func(x any) string { return ToJSON(x) },
		"blank":       IsBlank,
		"present":     IsPresent,

		// --- TagHelper ---
		"tag": func(name string, args ...any) HTML {
			return Tag(name, optHash(args, 0), toBool(argAt(args, 1)))
		},
		"content_tag": func(name string, args ...any) HTML {
			// content_tag name [content] [opts] / content_tag name opts（中身なし）
			if len(args) == 1 && isHash(args[0]) {
				return ContentTag(name, nil, optHash(args, 0))
			}
			return ContentTag(name, argAt(args, 0), optHash(args, 1))
		},
		"content_tag_open": func(name string, args ...any) HTML { return ContentTagOpen(name, optHash(args, 0)) },
		"end_tag":          EndTag,
		"end_form":         EndForm,
		"cdata_section":    CDATASection,

		// --- UrlHelper ---
		"link_to": func(name any, args ...any) HTML {
			return LinkTo(name, argAt(args, 0), optHash(args, 1))
		},
		"link_to_if": func(cond any, name any, args ...any) HTML {
			return LinkToIf(toBool(cond), name, argAt(args, 0), optHash(args, 1))
		},
		"link_to_unless": func(cond any, name any, args ...any) HTML {
			return LinkToUnless(toBool(cond), name, argAt(args, 0), optHash(args, 1))
		},
		"button_to": func(name any, args ...any) HTML {
			return v.ButtonTo(name, argAt(args, 0), optHash(args, 1))
		},
		"mail_to": func(email any, args ...any) HTML {
			if len(args) == 1 && isHash(args[0]) {
				return MailTo(email, nil, optHash(args, 0))
			}
			return MailTo(email, argAt(args, 0), optHash(args, 1))
		},
		"url_encode": URLEncode,

		// --- AssetTagHelper ---
		"image_tag":  func(src string, args ...any) HTML { return v.ImageTag(src, optHash(args, 0)) },
		"image_path": v.ImagePath,
		"javascript_include_tag": func(args ...any) HTML {
			srcs, opts := splitOpts(args)
			return v.JavascriptIncludeTag(stringsOf(srcs), opts)
		},
		"stylesheet_link_tag": func(args ...any) HTML {
			srcs, opts := splitOpts(args)
			return v.StylesheetLinkTag(stringsOf(srcs), opts)
		},

		// --- FormTagHelper ---
		"form_tag": func(args ...any) HTML { return v.FormTag(argAt(args, 0), optHash(args, 1)) },
		"label_tag": func(name any, args ...any) HTML {
			if len(args) == 1 && isHash(args[0]) {
				return LabelTag(name, nil, optHash(args, 0))
			}
			return LabelTag(name, argAt(args, 0), optHash(args, 1))
		},
		"text_field_tag":      valueFieldFunc(TextFieldTag),
		"hidden_field_tag":    valueFieldFunc(HiddenFieldTag),
		"password_field_tag":  valueFieldFunc(PasswordFieldTag),
		"email_field_tag":     valueFieldFunc(EmailFieldTag),
		"search_field_tag":    valueFieldFunc(SearchFieldTag),
		"url_field_tag":       valueFieldFunc(URLFieldTag),
		"telephone_field_tag": valueFieldFunc(TelephoneFieldTag),
		"color_field_tag":     valueFieldFunc(ColorFieldTag),
		"time_field_tag":      valueFieldFunc(TimeFieldTag),
		"number_field_tag":    valueFieldFunc(NumberFieldTag),
		"date_field_tag":      valueFieldFunc(DateFieldTag),
		"text_area_tag":       valueFieldFunc(TextAreaTag),
		"file_field_tag":      func(name any, args ...any) HTML { return FileFieldTag(name, optHash(args, 0)) },
		"check_box_tag": func(name any, args ...any) HTML {
			pos, opts := splitOpts(args)
			value := any("1")
			if len(pos) > 0 {
				value = pos[0]
			}
			return CheckBoxTag(name, value, toBool(argAt(pos, 1)), opts)
		},
		"radio_button_tag": func(name any, value any, args ...any) HTML {
			pos, opts := splitOpts(args)
			return RadioButtonTag(name, value, toBool(argAt(pos, 0)), opts)
		},
		"select_tag": func(name any, args ...any) HTML {
			if len(args) == 1 && isHash(args[0]) {
				return SelectTag(name, nil, optHash(args, 0))
			}
			return SelectTag(name, argAt(args, 0), optHash(args, 1))
		},
		"submit_tag": func(args ...any) HTML {
			if len(args) == 0 {
				return v.SubmitTag("Save changes", nil)
			}
			if len(args) == 1 && isHash(args[0]) {
				return v.SubmitTag("Save changes", optHash(args, 0))
			}
			return v.SubmitTag(args[0], optHash(args, 1))
		},
		"button_tag": func(args ...any) HTML {
			if len(args) == 1 && isHash(args[0]) {
				return ButtonTag(nil, optHash(args, 0))
			}
			return ButtonTag(argAt(args, 0), optHash(args, 1))
		},
		"field_set_tag": func(legend any, args ...any) HTML {
			return FieldSetTag(legend, optHash(args, 0), argAt(args, 1))
		},
		"field_set_tag_open": func(args ...any) HTML {
			if len(args) == 1 && isHash(args[0]) {
				return FieldSetTagOpen(nil, optHash(args, 0))
			}
			return FieldSetTagOpen(argAt(args, 0), optHash(args, 1))
		},
		"sanitize_to_id": SanitizeToID,

		// --- FormOptionsHelper ---
		"options_for_select": func(container any, args ...any) any {
			// 文字列の container は（安全性を保ったまま）そのまま返す
			if s, ok := container.(string); ok {
				return s
			}
			return OptionsForSelect(container, argAt(args, 0))
		},
		"options_from_collection_for_select": func(coll any, valueMethod, textMethod string, args ...any) HTML {
			return OptionsFromCollectionForSelect(coll, valueMethod, textMethod, argAt(args, 0))
		},
		"grouped_options_for_select": func(grouped any, args ...any) HTML {
			return GroupedOptionsForSelect(grouped, argAt(args, 0), optHash(args, 1))
		},

		// --- FormHelper（ビルダ） ---
		"form_for": func(name string, model any, args ...any) *FormBuilder {
			return v.FormFor(name, model, optHash(args, 0), false)
		},
		"labelled_form_for": func(name string, model any, args ...any) *FormBuilder {
			return v.FormFor(name, model, optHash(args, 0), true)
		},
		"fields_for": func(name string, model any) *FormBuilder {
			return v.NewFormBuilder(name, model, false)
		},
		"labelled_fields_for": func(name string, model any) *FormBuilder {
			return v.NewFormBuilder(name, model, true)
		},

		// --- JavaScriptHelper ---
		"escape_javascript": EscapeJavascript,
		"j":                 EscapeJavascript,
		// url_path は "/" 始まりのパスに relative_url_root を前置する（JS 文字列など link_to を通らない URL 用）。
		"url_path": urlroot.Path,
		"javascript_tag": func(content any, args ...any) HTML {
			return JavascriptTag(content, optHash(args, 0))
		},

		// --- TextHelper / SanitizeHelper ---
		"simple_format": func(text any, args ...any) HTML {
			return SimpleFormat(text, optHash(args, 0), optHash(args, 1))
		},
		"truncate":   func(text any, args ...any) any { return Truncate(text, optHash(args, 0)) },
		"sanitize":   Sanitize,
		"strip_tags": StripTags,
		"pluralize": func(count any, singular string, args ...any) string {
			return Pluralize(count, singular, ToS(argAt(args, 0)))
		},
		"cycle":         v.Cycle,
		"current_cycle": v.CurrentCycle,
		"reset_cycle": func(name ...string) string {
			v.ResetCycle(name...)
			return ""
		},
	}
}

// valueFieldFunc は name [value] [opts] 形式のテンプレート関数を作る。
func valueFieldFunc(fn func(name, value any, opts *Hash) HTML) func(name any, args ...any) HTML {
	return func(name any, args ...any) HTML {
		if len(args) == 1 && isHash(args[0]) {
			return fn(name, nil, optHash(args, 0))
		}
		return fn(name, argAt(args, 0), optHash(args, 1))
	}
}

func stringsOf(xs []any) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = ToS(x)
	}
	return out
}
