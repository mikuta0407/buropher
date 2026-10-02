package domain

import (
	"math"
	"time"
)

// TimeEntry は time_entries 行 (Redmine TimeEntry)。
//
// SpentOn は UTC 0 時の日付。TYear / TMonth / TWeek は spent_on から導出する集計用の列。
type TimeEntry struct {
	ID         int64
	ProjectID  int64
	UserID     int64
	AuthorID   int64
	IssueID    *int64
	Hours      float64
	Comments   *string
	ActivityID int64
	SpentOn    time.Time
	TYear      int
	TMonth     int
	TWeek      int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RoundedHours は TimeEntry#hours（分単位の有理数に丸めた値。(h * 60).round / 60r）。
func RoundedHours(h float64) float64 {
	return roundHalfAwayFromZero(h*60) / 60
}

// roundHalfAwayFromZero は Ruby の Float#round（0.5 は 0 から遠い方へ）。
func roundHalfAwayFromZero(f float64) float64 {
	return math.Round(f)
}

// CommentsString は comments（nil なら空文字列）。
func (t *TimeEntry) CommentsString() string {
	if t.Comments == nil {
		return ""
	}
	return *t.Comments
}

// SetSpentOn は spent_on= (tyear / tmonth / tweek も設定する)。
func (t *TimeEntry) SetSpentOn(d time.Time) {
	t.SpentOn = d
	if d.IsZero() {
		t.TYear, t.TMonth, t.TWeek = 0, 0, 0
		return
	}
	t.TYear = d.Year()
	t.TMonth = int(d.Month())
	_, t.TWeek = d.ISOWeek()
}
