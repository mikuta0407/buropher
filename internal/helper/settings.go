package helper

import (
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは SettingsHelper（app/helpers/settings_helper.rb）の移植。
// setting_select / setting_multiselect / setting_text_field / setting_text_area / setting_check_box /
// setting_label / render_settings_error / notification_field と、選択肢を返す補助関数。

// SettingError は render_settings_error に渡す検証エラー 1 件（[name, message]）。
type SettingError struct {
	Name    string
	Message string
}

// Notifiable は Redmine::Notifiable（通知イベント）。
type Notifiable struct {
	Name   string
	Parent string
}

// Notifiables は Redmine::Notifiable.all（定義順）。
func Notifiables() []Notifiable {
	return []Notifiable{
		{"issue_added", ""},
		{"issue_updated", ""},
		{"issue_note_added", "issue_updated"},
		{"issue_status_updated", "issue_updated"},
		{"issue_assigned_to_updated", "issue_updated"},
		{"issue_priority_updated", "issue_updated"},
		{"issue_fixed_version_updated", "issue_updated"},
		{"issue_attachment_added", "issue_updated"},
		{"news_added", ""},
		{"news_comment_added", ""},
		{"document_added", ""},
		{"file_added", ""},
		{"message_posted", ""},
		{"wiki_content_added", ""},
		{"wiki_content_updated", ""},
	}
}

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			"setting_value": func(name string) any { return settingValue(pg(), name) },
			"setting_select": func(name string, choices any, opts ...*rails.Hash) html {
				return settingSelect(pg(), name, toChoices(choices), firstHash(opts))
			},
			"setting_multiselect": func(name string, choices any, opts ...*rails.Hash) html {
				return settingMultiselect(pg(), name, toChoices(choices), firstHash(opts))
			},
			"setting_text_field": func(name string, opts ...*rails.Hash) html {
				p := pg()
				o := firstHash(opts)
				return settingLabel(p, name, o) + rails.TextFieldTag("settings["+name+"]", settingValue(p, name), o)
			},
			"setting_text_area": func(name string, opts ...*rails.Hash) html {
				p := pg()
				o := firstHash(opts)
				return settingLabel(p, name, o) + rails.TextAreaTag("settings["+name+"]", settingValue(p, name), o)
			},
			"setting_check_box": func(name string, opts ...*rails.Hash) html {
				p := pg()
				o := firstHash(opts)
				return settingLabel(p, name, o) +
					rails.HiddenFieldTag("settings["+name+"]", 0, rails.NewHash("id", nil)) +
					rails.CheckBoxTag("settings["+name+"]", 1, rails.ToS(settingValue(p, name)) != "0", o)
			},
			"setting_label": func(name string, opts ...*rails.Hash) html {
				return settingLabel(pg(), name, firstHash(opts))
			},
			"render_settings_error": func(errs []SettingError) html { return renderSettingsError(d, pg(), errs) },
			"notification_field":    func(n Notifiable) html { return notificationField(pg(), n) },
			"session_lifetime_options": func() []any {
				p := pg()
				opts := []any{[]any{p.l("label_disabled"), 0}}
				for _, h := range []int{4, 8, 12} {
					opts = append(opts, []any{p.l("datetime.distance_in_words.x_hours", map[string]any{"count": h}), settings.Itoa(h * 60)})
				}
				for _, dd := range []int{1, 7, 30, 60, 365} {
					opts = append(opts, []any{p.l("datetime.distance_in_words.x_days", map[string]any{"count": dd}), settings.Itoa(dd * 24 * 60)})
				}
				return opts
			},
			"session_timeout_options": func() []any {
				p := pg()
				opts := []any{[]any{p.l("label_disabled"), 0}}
				for _, h := range []int{1, 2, 4, 8, 12, 24, 48} {
					opts = append(opts, []any{p.l("datetime.distance_in_words.x_hours", map[string]any{"count": h}), settings.Itoa(h * 60)})
				}
				return opts
			},
			"x_days_options": func(days ...int) []any {
				p := pg()
				opts := []any{[]any{p.l("label_disabled"), 0}}
				for _, dd := range days {
					opts = append(opts, []any{p.l("datetime.distance_in_words.x_days", map[string]any{"count": dd}), settings.Itoa(dd)})
				}
				return opts
			},
			// labelled_options は [[l(label), value], ...]（link_copied_issue_options 等）。
			"labelled_options": func(pairs ...string) []any {
				p := pg()
				var opts []any
				for i := 0; i+1 < len(pairs); i += 2 {
					opts = append(opts, []any{p.l(pairs[i]), pairs[i+1]})
				}
				return opts
			},
			"gravatar_default_setting_options": func() []any {
				return []any{
					[]any{"Identicons", "identicon"}, []any{"Monster ids", "monsterid"}, []any{"Mystery man", "mm"},
					[]any{"Retro", "retro"}, []any{"Robohash", "robohash"}, []any{"Wavatars", "wavatar"}, []any{"Initials", "initials"},
				}
			},
		}
	})
}

// toChoices は選択肢（[]any / [][2]string / []string）を []any にそろえる。
func toChoices(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case [][2]string:
		out := make([]any, len(x))
		for i, p := range x {
			out[i] = []any{p[0], p[1]}
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, p := range x {
			out[i] = p
		}
		return out
	}
	return nil
}

func firstHash(opts []*rails.Hash) *rails.Hash {
	if len(opts) > 0 && opts[0] != nil {
		return opts[0].Clone()
	}
	return rails.NewHash()
}

// settingValue は setting_value（再表示時は params[:settings][name]、無ければ Setting.send(name)）。
func settingValue(p *Page, name string) any {
	if v, ok := p.Params().Lookup("settings", name); ok && v != nil {
		return plainParam(v)
	}
	if p.Settings == nil {
		if d := settings.Lookup(name); d != nil {
			return d.Default
		}
		return nil
	}
	return p.Settings.Get(name)
}

// plainParam は params の値（*httpx.Params / []any）を素の値にする。
func plainParam(v any) any {
	switch x := v.(type) {
	case *httpx.Params:
		return x.ToMap()
	}
	return v
}

// settingLabel は setting_label（:label => false なら空、文字列ならそのまま、シンボルなら l）。
// options から :label / :label_options を取り除く。
func settingLabel(p *Page, name string, opts *rails.Hash) html {
	label, _ := opts.Delete("label")
	labelOpts, _ := opts.Delete("label_options")
	if b, ok := label.(bool); ok && !b {
		return ""
	}
	var text string
	switch x := label.(type) {
	case string:
		text = x
	case rails.Symbol:
		text = p.l(string(x))
	default:
		text = p.l("setting_" + name)
	}
	lo, _ := labelOpts.(*rails.Hash)
	return rails.LabelTag("settings_"+name, text, lo)
}

// settingSelect は setting_select。
func settingSelect(p *Page, name string, choices []any, opts *rails.Hash) html {
	if blank, ok := opts.Delete("blank"); ok && blank != nil && blank != false {
		text := rails.ToS(blank)
		if s, ok := blank.(rails.Symbol); ok {
			text = p.l(string(s))
		}
		choices = append([]any{[]any{text, ""}}, choices...)
	}
	return settingLabel(p, name, opts) +
		rails.SelectTag("settings["+name+"]", rails.OptionsForSelect(choices, rails.ToS(settingValue(p, name))), opts)
}

// settingMultiselect は setting_multiselect。
func settingMultiselect(p *Page, name string, choices []any, opts *rails.Hash) html {
	var values []string
	if a, ok := settingValue(p, name).([]any); ok {
		for _, v := range a {
			values = append(values, rails.ToS(v))
		}
	} else if a, ok := settingValue(p, name).([]string); ok {
		values = a
	}
	label := opts.Get("label")
	text := p.l("setting_" + name)
	if s, ok := label.(rails.Symbol); ok {
		text = p.l(string(s))
	} else if s, ok := label.(string); ok {
		text = p.l(s)
	}
	class := "block"
	if truthy(opts.Get("inline")) {
		class = "inline"
	}
	var b strings.Builder
	b.WriteString(string(rails.ContentTag("label", text, nil)))
	b.WriteString(string(rails.HiddenFieldTag("settings["+name+"][]", "", nil)))
	for _, c := range choices {
		var t, v any
		if pair, ok := c.([]any); ok && len(pair) >= 2 {
			t, v = pair[0], pair[1]
		} else {
			t, v = c, c
		}
		vs := rails.ToS(v)
		checked := false
		for _, x := range values {
			if x == vs {
				checked = true
				break
			}
		}
		b.WriteString(string(rails.ContentTag("label",
			rails.CheckBoxTag("settings["+name+"][]", v, checked, rails.NewHash("id", nil))+rails.H(rails.ToS(t)),
			rails.NewHash("class", class))))
	}
	return html(b.String())
}

// renderSettingsError は render_settings_error。
func renderSettingsError(d *Deps, p *Page, errs []SettingError) html {
	if len(errs) == 0 {
		return ""
	}
	var s strings.Builder
	for _, e := range errs {
		s.WriteString(string(rails.ContentTag("li", rails.ContentTag("b", p.l("setting_"+e.Name), nil)+rails.H(" "+e.Message), nil)))
	}
	return rails.ContentTag("div", d.noticeIcon(p, "error")+rails.ContentTag("ul", html(s.String()), nil), rails.NewHash("id", "errorExplanation"))
}

// notificationField は notification_field(notifiable)。
func notificationField(p *Page, n Notifiable) html {
	var data *rails.Hash
	if n.Parent != "" {
		data = rails.NewHash("parent_notifiable", n.Parent)
	} else {
		data = rails.NewHash("disables", "input[data-parent-notifiable="+n.Name+"]")
	}
	checked := false
	if a, ok := settingValue(p, "notified_events").([]any); ok {
		for _, v := range a {
			if rails.ToS(v) == n.Name {
				checked = true
			}
		}
	}
	tag := rails.CheckBoxTag("settings[notified_events][]", n.Name, checked, rails.NewHash("id", nil, "data", data))
	text := p.Loc.LOrHumanize(n.Name, "label_")
	var opts *rails.Hash
	if n.Parent != "" {
		opts = rails.NewHash("class", "parent")
	}
	return rails.ContentTag("label", tag+rails.H(text), opts)
}
