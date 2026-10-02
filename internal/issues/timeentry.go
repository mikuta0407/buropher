package issues

// チケット編集フォームの「時間を記録」（IssuesController#save_issue_with_child_records の TimeEntry）。
// 作業時間の本格的な移植（TimelogController / TimeEntry モデル）は別パッケージで行うため、ここでは
// チケット更新と同時に記録する 1 件の作成と、その検証に必要な範囲だけを扱う。

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// TimeEntry はチケットと同時に記録する作業時間（TimeEntry.new(:issue => @issue, :project => @issue.project)）。
type TimeEntry struct {
	ID         int64
	ProjectID  int64
	IssueID    int64
	UserID     int64
	AuthorID   int64
	ActivityID *int64
	// Hours は hours（解釈できなければ nil で、HoursRaw に入力値が残る）。
	Hours *float64
	// HoursRaw は hours_before_type_cast（フォームの再表示用）。
	HoursRaw string
	Comments string
	SpentOn  time.Time
	// CustomFieldValues は custom_field_values（id → 値。複数値は複数要素）。
	CustomFieldValues map[int64][]string

	// Errors は直近の検証エラー。
	Errors domain.ValidationErrors
}

// TimeEntryParams は params[:time_entry]。
type TimeEntryParams struct {
	Hours        *string
	Comments     *string
	ActivityID   *string
	CustomFields map[string]any
}

// Present は params[:time_entry][:hours].present? || params[:time_entry][:comments].present?。
func (p *TimeEntryParams) Present() bool {
	if p == nil {
		return false
	}
	return (p.Hours != nil && strings.TrimSpace(*p.Hours) != "") || (p.Comments != nil && strings.TrimSpace(*p.Comments) != "")
}

// DefaultActivityID は TimeEntryActivity.default(project) の id（無ければ nil）。
func (e *Env) DefaultActivityID(ctx context.Context, projectID int64) (*int64, error) {
	acts, err := repository.ProjectActivities(ctx, e.Q, projectID, false)
	if err != nil {
		return nil, err
	}
	for _, a := range acts {
		if a.IsDefault {
			id := a.ID
			return &id, nil
		}
	}
	return nil, nil
}

// NewTimeEntry は TimeEntry.new(:issue => iss, :project => iss.project) と safe_attributes = params。
func (e *Env) NewTimeEntry(ctx context.Context, iss *Issue, p *TimeEntryParams) (*TimeEntry, error) {
	te := &TimeEntry{ProjectID: iss.ProjectID, IssueID: iss.ID}
	act, err := e.DefaultActivityID(ctx, iss.ProjectID)
	if err != nil {
		return nil, err
	}
	te.ActivityID = act
	te.AssignParams(p)
	return te, nil
}

// AssignParams は safe_attributes=（hours / comments / activity_id / custom_field_values）。
func (te *TimeEntry) AssignParams(p *TimeEntryParams) {
	if p == nil {
		return
	}
	if p.Hours != nil {
		s := strings.TrimSpace(*p.Hours)
		te.HoursRaw = *p.Hours
		te.Hours = nil
		if s != "" {
			if h, ok := ToHours(s); ok {
				te.Hours = &h
			}
		}
	}
	if p.Comments != nil {
		te.Comments = *p.Comments
	}
	if p.ActivityID != nil {
		te.ActivityID = castID(*p.ActivityID)
	}
	if len(p.CustomFields) > 0 {
		if te.CustomFieldValues == nil {
			te.CustomFieldValues = map[int64][]string{}
		}
		for k, v := range p.CustomFields {
			id := rubyToI(k)
			if id <= 0 {
				continue
			}
			te.CustomFieldValues[id] = toStrings(v)
		}
	}
}

// ValidateTimeEntry は TimeEntry#valid?（チケットからの記録で起こり得る検証のみ）。
func (e *Env) ValidateTimeEntry(ctx context.Context, te *TimeEntry, iss *Issue) (bool, error) {
	te.Errors = domain.ValidationErrors{}
	errs := &te.Errors
	// validates_presence_of :author_id, :user_id, :activity_id, :project_id, :hours, :spent_on
	if te.ActivityID == nil {
		errs.Add("activity_id", "blank", nil)
	}
	if te.Hours == nil && strings.TrimSpace(te.HoursRaw) == "" {
		errs.Add("hours", "blank", nil)
	}
	req := e.Settings.Strings("timelog_required_fields")
	if slices.Contains(req, "comments") && strings.TrimSpace(te.Comments) == "" {
		errs.Add("comments", "blank", nil)
	}
	// validates_numericality_of :hours, :allow_nil => true, :message => :invalid
	if te.Hours == nil && strings.TrimSpace(te.HoursRaw) != "" {
		errs.Add("hours", "invalid", nil)
	}
	// validates_length_of :comments, :maximum => 1024
	if len([]rune(te.Comments)) > 1024 {
		errs.Add("comments", "too_long", map[string]any{"count": 1024})
	}
	// validate_time_entry
	if te.Hours != nil {
		h := *te.Hours
		if h < 0 {
			errs.Add("hours", "invalid", nil)
		}
		if h == 0 && !e.Settings.Bool("timelog_accept_0_hours") {
			errs.Add("hours", "invalid", nil)
		}
		maxHours, _ := strconv.ParseFloat(strings.TrimSpace(e.Settings.String("timelog_max_hours_per_day")), 64)
		if maxHours > 0 {
			var logged float64
			if err := e.Q.Get(ctx, &logged, `SELECT COALESCE(SUM(hours), 0) FROM time_entries WHERE user_id = ? AND spent_on = ?`,
				te.UserID, db.DateOf(te.SpentOn)); err != nil {
				return false, err
			}
			if logged+h > maxHours {
				errs.AddMessage("base", e.translator()("error_exceeds_maximum_hours_per_day",
					map[string]any{"logged_hours": formatHoursSetting(e, logged), "max_hours": formatHoursSetting(e, maxHours)}))
			}
		}
	}
	if te.ActivityID != nil {
		acts, err := repository.ProjectActivities(ctx, e.Q, te.ProjectID, false)
		if err != nil {
			return false, err
		}
		if !slices.ContainsFunc(acts, func(a *domain.Enumeration) bool { return a.ID == *te.ActivityID }) {
			errs.Add("activity_id", "inclusion", nil)
		}
	}
	if !e.Settings.Bool("timelog_accept_closed_issues") && iss != nil {
		closed, err := e.Closed(ctx, iss)
		if err != nil {
			return false, err
		}
		wasClosed, err := e.WasClosed(ctx, iss)
		if err != nil {
			return false, err
		}
		if closed && wasClosed {
			errs.AddMessage("base", e.translator()("error_spent_on_closed_issue"))
		}
	}
	return !errs.Any(), nil
}

// formatHoursSetting は format_hours（Setting.timespan_format）。
func formatHoursSetting(e *Env, h float64) string {
	if e.Settings != nil && e.Settings.String("timespan_format") == "minutes" {
		hh := int(h)
		mm := int((h-float64(hh))*60 + 0.5)
		if mm == 60 {
			hh, mm = hh+1, 0
		}
		return strconv.Itoa(hh) + ":" + twoDigits(mm)
	}
	return strconv.FormatFloat(h, 'f', 2, 64)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// InsertTimeEntry は検証済みの作業時間を保存する（@issue.time_entries << time_entry）。
func (e *Env) InsertTimeEntry(ctx context.Context, te *TimeEntry) error {
	now := db.NewTime(e.now())
	d := te.SpentOn
	_, week := d.ISOWeek()
	var comments any = te.Comments
	id, err := e.Q.InsertReturningID(ctx, `INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, comments,
  activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		te.ProjectID, te.UserID, te.AuthorID, te.IssueID, *te.Hours, comments, *te.ActivityID, db.DateOf(d),
		d.Year(), int(d.Month()), week, now, now)
	if err != nil {
		return err
	}
	te.ID = id
	ids := make([]int64, 0, len(te.CustomFieldValues))
	for id := range te.CustomFieldValues {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, cfID := range ids {
		for _, v := range te.CustomFieldValues[cfID] {
			if _, err := e.Q.Exec(ctx, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value)
VALUES ('time_entry', ?, ?, ?)`, te.ID, cfID, v); err != nil {
				return err
			}
		}
	}
	return nil
}
