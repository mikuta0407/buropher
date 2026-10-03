package helper

import (
	"html/template"
	"regexp"
	"sort"
	"strconv"
	"strings"
	ttemplate "text/template"
	"time"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは ActivitiesHelper（sort_activity_events）と ApplicationHelper の
// format_activity_title / format_activity_day / format_activity_description、
// IconsHelper#activity_event_type_icon の移植。

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			"format_activity_title":       FormatActivityTitle,
			"format_activity_description": FormatActivityDescription,
			"format_activity_day":         func(day time.Time) string { return formatActivityDay(pg(), day) },
			"activity_event_type_icon": func(eventType string) html {
				return d.spriteIcon(pg(), ActivityEventTypeIconName(eventType), nil, rails.NewHash())
			},
			// activity_event_is_me は dt の "me" クラスの条件
			// （User.current.logged? && e.respond_to?(:event_author) && User.current == e.event_author）。
			"activity_event_is_me": func(e *activity.Event) bool {
				p := pg()
				if !p.logged() || e == nil {
					return false
				}
				u := e.AuthorUser()
				return u != nil && u.ID == p.User.ID
			},
			"format_iso_date":      func(t time.Time) string { return t.Format("2006-01-02") },
			"activity_singularize": ActivitySingularize,
			// activity_type_url は link_to(..., {"show_#{t}" => 1, :user_id => params[:user_id], :from => params[:from]})。
			"activity_type_url": func(path, t string) string {
				h := rails.NewHash("show_"+t, 1)
				params := pg().Params()
				for _, k := range []string{"user_id", "from"} {
					if v, ok := params.Get(k); ok {
						h.Set(k, v)
					}
				}
				return URLWithQuery(path, h)
			},
			"activity_author_name": func(v any) any {
				if u := toUser(v); u != nil {
					return pg().UserName(u)
				}
				return nil
			},
			// project_name は Project#to_s（nil なら空）。
			"project_name": func(v any) string {
				if p := toProject(v); p != nil {
					return p.Name
				}
				return ""
			},
			"same_project": func(a, b any) bool {
				pa, pb := toProject(a), toProject(b)
				if pa == nil || pb == nil {
					return pa == nil && pb == nil
				}
				return pa.ID == pb.ID
			},
		}
	})
}

// ActivityDay は events_by_day の 1 日分（日付と sort_activity_events の結果）。
type ActivityDay struct {
	Day    time.Time
	Events []ActivityEvent
}

// ActivityEvent は sort_activity_events の 1 要素（[event, in_group]）。
type ActivityEvent struct {
	E       *activity.Event
	InGroup bool
}

// GroupActivityEvents は events.group_by {|e| User.current.time_to_date(e.event_datetime)} を
// 新しい日付順（keys.sort.reverse_each）に並べ、各日を sort_activity_events で並べ替える。
// loc は User.current のタイムゾーン（nil ならサーバのローカル時刻）。
func GroupActivityEvents(events []*activity.Event, loc *time.Location) []ActivityDay {
	byDay := map[time.Time][]*activity.Event{}
	var days []time.Time
	for _, e := range events {
		t := i18n.UserTime(e.Datetime, loc)
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		if _, ok := byDay[d]; !ok {
			days = append(days, d)
		}
		byDay[d] = append(byDay[d], e)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].After(days[j]) })
	out := make([]ActivityDay, len(days))
	for i, d := range days {
		out[i] = ActivityDay{Day: d, Events: SortActivityEvents(byDay[d])}
	}
	return out
}

// SortActivityEvents は ActivitiesHelper#sort_activity_events。
// sort_by(&:event_datetime) は参照環境の Ruby（glibc の qsort_r）で安定なので、同時刻のものは
// reverse_each で元の順序の逆になる。
func SortActivityEvents(events []*activity.Event) []ActivityEvent {
	byGroup := map[string][]*activity.Event{}
	for _, e := range events {
		byGroup[e.Group] = append(byGroup[e.Group], e)
	}
	asc := append([]*activity.Event(nil), events...)
	sort.SliceStable(asc, func(i, j int) bool { return asc[i].Datetime.Before(asc[j].Datetime) })
	var out []ActivityEvent
	for i := len(asc) - 1; i >= 0; i-- {
		g, ok := byGroup[asc[i].Group]
		if !ok {
			continue
		}
		delete(byGroup, asc[i].Group)
		ge := append([]*activity.Event(nil), g...)
		sort.SliceStable(ge, func(a, b int) bool { return ge[a].Datetime.Before(ge[b].Datetime) })
		for k := len(ge) - 1; k >= 0; k-- {
			out = append(out, ActivityEvent{E: ge[k], InGroup: k < len(ge)-1})
		}
	}
	return out
}

// FormatActivityTitle は format_activity_title（そのまま返す）。
func FormatActivityTitle(text string) string { return text }

var (
	reQuotedLines = regexp.MustCompile(`(?ms)(^>.*?(?:\r?\n))(?:>.*?(?:\r?\n)+)+`)
	rePreCode     = regexp.MustCompile(`(?s)[\r\n]*<(pre|code)>.*$`)
	reNewlines    = regexp.MustCompile(`[\r\n]+`)
)

// FormatActivityDescription は format_activity_description。
func FormatActivityDescription(text string) html {
	s := text
	// 文字数はバイト数以下なので、バイト数が上限以下なら rune への変換は要らない
	if len(s) > 10240 {
		if r := []rune(s); len(r) > 10240 {
			s = string(r[:10240])
		}
	}
	// 正規表現が一致し得ない入力 (引用行・<pre>/<code> を含まない大半の注記) では照合を省く
	if strings.HasPrefix(s, ">") || strings.Contains(s, "\n>") {
		s = reQuotedLines.ReplaceAllString(s, "${1}> ...\n")
	}
	if strings.Contains(s, "<pre>") || strings.Contains(s, "<code>") {
		if loc := rePreCode.FindStringIndex(s); loc != nil {
			s = s[:loc[0]] + s[loc[1]:]
		}
	}
	s = rails.StringTruncate(s, 240, "", nil)
	h := string(rails.H(s))
	if !strings.ContainsAny(h, "\r\n") {
		return html(h)
	}
	return html(reNewlines.ReplaceAllString(h, "<br>"))
}

// formatActivityDay は format_activity_day（今日なら l(:label_today).titleize）。
func formatActivityDay(p *Page, day time.Time) string {
	t := i18n.UserTime(p.now(), p.userLocation())
	if day.Year() == t.Year() && day.Month() == t.Month() && day.Day() == t.Day() {
		return titleize(p.l("label_today"))
	}
	return formatDate(p, day)
}

// userLocation は User.current のタイムゾーン（nil ならサーバのローカル時刻）。
func (p *Page) userLocation() *time.Location {
	if p.Loc != nil {
		return p.Loc.Location
	}
	return nil
}

// titleize は ActiveSupport の String#titleize（単語の先頭を大文字にする簡易版）。
func titleize(s string) string {
	words := strings.Fields(strings.ReplaceAll(s, "_", " "))
	for i, w := range words {
		r := []rune(strings.ToLower(w))
		if len(r) > 0 {
			r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		}
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// TextilizeEvent は textilizable(item, :event_description, :only_path => false)（Atom の content）。
func (d *Deps) TextilizeEvent(p *Page, e *activity.Event) template.HTML {
	obj := &redmine.Object{ID: e.ID, Project: e.Project}
	switch e.Provider {
	case activity.ProviderIssue:
		obj.Kind = "issue"
	case activity.ProviderJournal:
		obj.Kind = "journal"
		if id, ok := strings.CutPrefix(e.Group, "Issue:"); ok {
			obj.JournalizedID, _ = strconv.ParseInt(id, 10, 64)
		}
	case activity.ProviderChangeset:
		obj.Kind = "changeset"
	case activity.ProviderNews:
		obj.Kind = "news"
	case activity.ProviderDocument:
		obj.Kind = "document"
	case activity.ProviderMessage:
		obj.Kind = "message"
	case activity.ProviderProject:
		obj.Kind = "project"
		obj.Self = e.Project
		obj.Project = nil
	default:
		obj = nil
	}
	return d.WikiRenderer(p).Textilizable(e.Description, redmine.Options{Object: obj, FullURL: true})
}

// UserName は User#name（Setting.user_format）。
func (p *Page) UserName(u *domain.User) string { return p.userName(u, "") }

// FaviconPath は favicon_path（テーマのファビコンがあればそれ）。
func (d *Deps) FaviconPath(p *Page) string {
	icon := "favicon.ico"
	if t := d.currentTheme(p); t != nil && t.Favicon() != "" {
		icon = t.FaviconPath()
	}
	if d.Assets == nil {
		return "/" + icon
	}
	return d.Assets.AssetPath(icon)
}

// ActivitySingularize はイベント種別・検索種別名の String#singularize。
func ActivitySingularize(s string) string {
	switch {
	case strings.HasSuffix(s, "news"):
		return s
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "s"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// ActivityEventTypeIconName は activity_event_type_icon のアイコン名。
func ActivityEventTypeIconName(eventType string) string {
	switch eventType {
	case "reply":
		return "comments"
	case "time-entry":
		return "time"
	case "message":
		return "comment"
	}
	return eventType
}
