// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/clock"
)

// TimeLayout は DB に保存するタイムスタンプの固定長 UTC 形式。
// 文字列比較の順序が時刻順と一致するよう、小数部は常に 6 桁。
// SQLite の TEXT 列にはこの形式で保存し (CHECK 制約で強制)、
// PostgreSQL の timestamptz にも同じ文字列をパラメータとして渡す。
const TimeLayout = "2006-01-02T15:04:05.000000Z"

// DateLayout は日付列の形式。
const DateLayout = "2006-01-02"

// 読み取り時に受け付ける形式 (旧データ・他ドライバの表現も吸収する)。
var timeParseLayouts = []string{
	TimeLayout,
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	DateLayout,
}

// Now は現在時刻 (clock.Now。互換テストの固定時刻に従う) をマイクロ秒精度の UTC で返す
// (DB に保存して読み戻しても等しくなる)。
func Now() Time { return NewTime(clock.Now()) }

// FormatTime は t を DB 保存形式 (UTC, マイクロ秒) の文字列にする。
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime は DB 由来の文字列を UTC の time.Time に変換する。
// タイムゾーン情報のない文字列は UTC とみなす。
func ParseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range timeParseLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("db: cannot parse time %q", s)
}

// Time は NOT NULL のタイムスタンプ列用の型。常に UTC・マイクロ秒精度で扱う。
type Time struct{ time.Time }

// NewTime は t を UTC・マイクロ秒精度に正規化した Time を返す。
func NewTime(t time.Time) Time { return Time{t.UTC().Truncate(time.Microsecond)} }

// Value は driver.Valuer の実装。両 dialect とも固定長文字列で渡す。
func (t Time) Value() (driver.Value, error) {
	if t.Time.IsZero() {
		return nil, fmt.Errorf("db: zero Time for NOT NULL column")
	}
	return FormatTime(t.Time), nil
}

// Scan は sql.Scanner の実装。
func (t *Time) Scan(src any) error {
	v, ok, err := scanTime(src)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("db: NULL scanned into db.Time (use db.NullTime)")
	}
	t.Time = v
	return nil
}

// String は DB 保存形式を返す。
func (t Time) String() string { return FormatTime(t.Time) }

// NullTime は NULL 許容のタイムスタンプ列用の型。
type NullTime struct {
	Time  time.Time
	Valid bool
}

// NewNullTime は t を正規化した NullTime を返す (t がゼロ値なら NULL)。
func NewNullTime(t time.Time) NullTime {
	if t.IsZero() {
		return NullTime{}
	}
	return NullTime{Time: t.UTC().Truncate(time.Microsecond), Valid: true}
}

// Value は driver.Valuer の実装。
func (n NullTime) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return FormatTime(n.Time), nil
}

// Scan は sql.Scanner の実装。
func (n *NullTime) Scan(src any) error {
	v, ok, err := scanTime(src)
	if err != nil {
		return err
	}
	n.Time, n.Valid = v, ok
	return nil
}

// Ptr は NULL なら nil を返す。
func (n NullTime) Ptr() *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

func scanTime(src any) (time.Time, bool, error) {
	switch v := src.(type) {
	case nil:
		return time.Time{}, false, nil
	case time.Time:
		return v.UTC().Truncate(time.Microsecond), true, nil
	case string:
		t, err := ParseTime(v)
		return t, err == nil, err
	case []byte:
		t, err := ParseTime(string(v))
		return t, err == nil, err
	}
	return time.Time{}, false, fmt.Errorf("db: cannot scan %T into time", src)
}

// Date は日付のみの列 (DATE) 用の型。タイムゾーンを持たない暦日として扱い、
// 内部表現は UTC 0 時の time.Time。
type Date struct{ time.Time }

// NewDate は年月日から Date を作る。
func NewDate(y int, m time.Month, d int) Date {
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// DateOf は t の (t 自身のロケーションでの) 暦日を Date にする。
func DateOf(t time.Time) Date { return NewDate(t.Date()) }

// ParseDate は "YYYY-MM-DD" を Date にする。
func ParseDate(s string) (Date, error) {
	t, err := scanDateString(s)
	return Date{t}, err
}

// String は "YYYY-MM-DD" を返す。
func (d Date) String() string { return d.Time.Format(DateLayout) }

// Value は driver.Valuer の実装 ("YYYY-MM-DD")。
func (d Date) Value() (driver.Value, error) {
	if d.Time.IsZero() {
		return nil, fmt.Errorf("db: zero Date for NOT NULL column")
	}
	return d.String(), nil
}

// Scan は sql.Scanner の実装。
func (d *Date) Scan(src any) error {
	v, ok, err := scanDate(src)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("db: NULL scanned into db.Date (use db.NullDate)")
	}
	d.Time = v
	return nil
}

// NullDate は NULL 許容の日付列用の型。
type NullDate struct {
	Date  Date
	Valid bool
}

// NewNullDate は d から NullDate を作る (ゼロ値なら NULL)。
func NewNullDate(d Date) NullDate {
	if d.Time.IsZero() {
		return NullDate{}
	}
	return NullDate{Date: d, Valid: true}
}

// Value は driver.Valuer の実装。
func (n NullDate) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Date.String(), nil
}

// Scan は sql.Scanner の実装。
func (n *NullDate) Scan(src any) error {
	v, ok, err := scanDate(src)
	if err != nil {
		return err
	}
	n.Date, n.Valid = Date{v}, ok
	return nil
}

func scanDate(src any) (time.Time, bool, error) {
	switch v := src.(type) {
	case nil:
		return time.Time{}, false, nil
	case time.Time:
		// PG の date はドライバによって UTC 0 時の time.Time になる。暦日だけを取り出す。
		y, m, d := v.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), true, nil
	case string:
		t, err := scanDateString(v)
		return t, err == nil, err
	case []byte:
		t, err := scanDateString(string(v))
		return t, err == nil, err
	}
	return time.Time{}, false, fmt.Errorf("db: cannot scan %T into date", src)
}

func scanDateString(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) >= len(DateLayout) {
		if t, err := time.Parse(DateLayout, s[:len(DateLayout)]); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("db: cannot parse date %q", s)
}
