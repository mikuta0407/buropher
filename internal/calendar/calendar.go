// Package calendar は Redmine::Helpers::Calendar（lib/redmine/helpers/calendar.rb）の移植。
//
// 月表示（CalendarsController#show）と週表示（マイページのカレンダーブロック）の開始日・終了日を求め、
// イベント（チケット・バージョン）を日ごとに引けるようにする。描画は部分テンプレート common/_calendar
// （{{partial "common/calendar" (dict "calendar" $cal)}}）で行う。
//
// 日付はすべて UTC の 0 時で表す（time.Time の年月日だけを使う）。
package calendar

import (
	"html/template"
	"slices"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
)

// Period は表示期間（:month / :week）。
type Period int

const (
	// Month は月表示（月初を含む週の最初の日から月末を含む週の最後の日まで）。
	Month Period = iota
	// Week は週表示（date を含む 1 週間）。
	Week
)

// Event はカレンダーに置くイベント（Issue または Version）。描画に必要な値は呼び出し側が用意する。
type Event struct {
	// IsIssue は Issue なら true、Version なら false。
	IsIssue bool
	// ID はチケット / バージョンの id。
	ID int64
	// StartDate / DueDate は start_date / due_date（バージョンは DueDate に effective_date）。
	StartDate *time.Time
	DueDate   *time.Time
	// Project は所属プロジェクト（"#{i.project} -" の表示・ツールチップのリンクに使う）。
	Project *domain.Project

	// ---- Issue
	// CSSClasses は Issue#css_classes。
	CSSClasses string
	// LinkShort は link_to_issue(i, :truncate => 30)。
	LinkShort template.HTML
	// Link は link_to_issue(i)（ツールチップ）。
	Link template.HTML
	// StatusName / Closed / ClosedOn はツールチップのステータス欄。
	StatusName string
	Closed     bool
	ClosedOn   *time.Time
	// AssignedTo は担当者がユーザーのとき（avatar に渡す）。AssignedToName は担当者の表示名（無ければ ""）。
	AssignedTo     *domain.User
	AssignedToName string
	// AssignedToAvatar は担当者がユーザー以外（グループ）のときの avatar の HTML（呼び出し側で用意。空可）。
	AssignedToAvatar template.HTML
	// PriorityName は優先度名。
	PriorityName string

	// ---- Version
	// VersionName は Version#name（format_version_name はテンプレート側で project と比較して組み立てる）。
	VersionName string
}

// OtherProject は「@project && @project == i.project」でない（"#{i.project} -" を出す）とき true。
// current は current_project（nil 可）。
func (e *Event) OtherProject(current any) bool {
	p, ok := current.(*domain.Project)
	if !ok || p == nil || e.Project == nil {
		return true
	}
	return p.ID != e.Project.ID
}

// ProjectName は所属プロジェクト名。
func (e *Event) ProjectName() string {
	if e.Project == nil {
		return ""
	}
	return e.Project.Name
}

// Calendar は Redmine::Helpers::Calendar。
type Calendar struct {
	date time.Time
	// Startdt / Enddt は表示範囲の最初と最後の日。
	Startdt, Enddt time.Time
	// FirstWday / LastWday は週の最初と最後の曜日（1 = 月曜 ... 7 = 日曜）。
	FirstWday, LastWday int
	// Today は User.current.today（day_css_classes の today 判定）。
	Today time.Time
	// NonWorkingDays は Setting.non_working_week_days（cwday の列）。
	NonWorkingDays []int
	// Loc は曜日名の翻訳に使う（nil なら英語の既定名は出ない）。
	Loc *i18n.Localizer
	// BackURL は back_url（url_for(:params => request.query_parameters)）。空ならテンプレートで request_path を使う。
	BackURL string

	events   []*Event
	ending   map[time.Time][]*Event
	starting map[time.Time][]*Event
}

// DateOnly は t の年月日だけを残した UTC の日付。
func DateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Cwday は Date#cwday（1 = 月曜 ... 7 = 日曜）。
func Cwday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

func mod7(n int) int { return ((n % 7) + 7) % 7 }

// New は Redmine::Helpers::Calendar.new(date, lang, period)。firstWday は first_wday
// （i18n.Localizer.StartOfWeek: Setting.start_of_week またはロケールの general_first_day_of_week）。
func New(date time.Time, firstWday int, period Period) *Calendar {
	date = DateOnly(date)
	c := &Calendar{date: date, FirstWday: firstWday, LastWday: mod7(firstWday+5) + 1,
		ending: map[time.Time][]*Event{}, starting: map[time.Time][]*Event{}}
	switch period {
	case Week:
		c.Startdt = date.AddDate(0, 0, -mod7(Cwday(date)-c.FirstWday))
		c.Enddt = date.AddDate(0, 0, mod7(c.LastWday-Cwday(date)))
	default:
		start := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1)
		c.Startdt = start.AddDate(0, 0, -mod7(Cwday(start)-c.FirstWday))
		c.Enddt = end.AddDate(0, 0, mod7(c.LastWday-Cwday(end)))
	}
	return c
}

// FormatMonth は format_month（Startdt から Enddt までの日付）。
func (c *Calendar) FormatMonth() []time.Time {
	var out []time.Time
	for d := c.Startdt; !d.After(c.Enddt); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// Weeks は format_month.each_slice(7)。
func (c *Calendar) Weeks() [][]time.Time {
	days := c.FormatMonth()
	var out [][]time.Time
	for len(days) > 0 {
		n := min(7, len(days))
		out = append(out, days[:n])
		days = days[n:]
	}
	return out
}

// WeekNumber は week_number(day)（(day + (11 - day.cwday) % 7).cweek）。
func (c *Calendar) WeekNumber(day time.Time) int {
	_, w := day.AddDate(0, 0, mod7(11-Cwday(day))).ISOWeek()
	return w
}

// Month は month（基準日の月）。
func (c *Calendar) Month() int { return int(c.date.Month()) }

// DayCSSClasses は day_css_classes(day)。
func (c *Calendar) DayCSSClasses(day time.Time) string {
	css := "other-month"
	if int(day.Month()) == c.Month() {
		css = "this-month"
	}
	if !c.Today.IsZero() && DateOnly(c.Today).Equal(day) {
		css += " today"
	}
	if slices.Contains(c.NonWorkingDays, Cwday(day)) {
		css += " nwday"
	}
	return css
}

// SetEvents は events=（期日・開始日ごとにまとめる。順序は渡した順）。
func (c *Calendar) SetEvents(events []*Event) {
	c.events = events
	c.ending = map[time.Time][]*Event{}
	c.starting = map[time.Time][]*Event{}
	for _, e := range events {
		if e.DueDate != nil {
			d := DateOnly(*e.DueDate)
			c.ending[d] = append(c.ending[d], e)
		}
		if e.StartDate != nil {
			d := DateOnly(*e.StartDate)
			c.starting[d] = append(c.starting[d], e)
		}
	}
}

// Events は events。
func (c *Calendar) Events() []*Event { return c.events }

// EventsOn は events_on(day)（その日に終わるもの + 始まるもの。重複は除く）。
func (c *Calendar) EventsOn(day time.Time) []*Event {
	day = DateOnly(day)
	var out []*Event
	for _, e := range append(slices.Clone(c.ending[day]), c.starting[day]...) {
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out
}

// Starting は day == i.start_date。
func (c *Calendar) Starting(e *Event, day time.Time) bool {
	return e.StartDate != nil && DateOnly(*e.StartDate).Equal(day)
}

// Ending は day == i.due_date。
func (c *Calendar) Ending(e *Event, day time.Time) bool {
	return e.DueDate != nil && DateOnly(*e.DueDate).Equal(day)
}

// IssueDivClass は tag.div class: [i.css_classes, 'tooltip hascontextmenu', starting:, ending:] の class 属性値。
func (c *Calendar) IssueDivClass(e *Event, day time.Time) string {
	s := e.CSSClasses + " tooltip hascontextmenu"
	if c.Starting(e, day) {
		s += " starting"
	}
	if c.Ending(e, day) {
		s += " ending"
	}
	return s
}

// DayName は day_name((first_wday + i) % 7)。
func (c *Calendar) DayName(i int) string {
	if c.Loc == nil {
		return ""
	}
	return c.Loc.DayName(mod7(c.FirstWday + i))
}

// AbbrDayName は abbr_day_name(day.cwday)。
func (c *Calendar) AbbrDayName(day time.Time) string {
	if c.Loc == nil {
		return ""
	}
	return c.Loc.AbbrDayName(Cwday(day))
}

// VersionLabel は format_version_name(version)（@project と同じプロジェクトなら名前、違えば "プロジェクト - 名前"）。
func (e *Event) VersionLabel(current any) string {
	if e.OtherProject(current) {
		return e.ProjectName() + " - " + e.VersionName
	}
	return e.VersionName
}

// FormatDate は format_date（nil なら ""）。
func (c *Calendar) FormatDate(t *time.Time) string {
	if t == nil || c.Loc == nil {
		return ""
	}
	return c.Loc.FormatDate(*t)
}

// DayHeaders は 7.times の i（0..6）。
func (c *Calendar) DayHeaders() []int { return []int{0, 1, 2, 3, 4, 5, 6} }
