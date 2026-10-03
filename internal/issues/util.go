// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RubyFloatToS は Ruby の Float#to_s (3.0 → "3.0", 1.0e-05, 1.0e+15 など)。
func RubyFloatToS(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	abs := math.Abs(f)
	if abs >= 1e15 || abs < 1e-4 {
		// 指数表記: 仮数は最短表現で小数点以下を最低 1 桁、指数は 2 桁以上
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		sign := exp[0]
		digits := strings.TrimLeft(exp[1:], "0")
		if len(digits) < 2 {
			digits = strings.Repeat("0", 2-len(digits)) + digits
		}
		return mant + "e" + string(sign) + digits
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

var (
	hoursSimpleRe = regexp.MustCompile(`^(\d+([.,]\d+)?)h?$`)
	hoursColonRe  = regexp.MustCompile(`^(\d+):(\d+)$`)
	hoursHMRe     = regexp.MustCompile(`(?i)^((\d+)\s*(h|hours?))?\s*((\d+)\s*(m|min)?)?$`)
	rubyFloatRe   = regexp.MustCompile(`^\s*[+-]?(\d[\d_]*)?(\.\d[\d_]*)?([eE][+-]?\d+)?\s*$`)
)

// ToHours は String#to_hours (2.5 / 2,5 / 2h / 2:30 / 2h30 / 30m / "2 hours 30 min" を時間数に)。
// 解釈できなければ ok = false。
func ToHours(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if m := hoursSimpleRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	} else {
		if m := hoursColonRe.FindStringSubmatch(s); m != nil {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			s = RubyFloatToS(float64(h) + float64(mi)/60.0)
		} else if m := hoursHMRe.FindStringSubmatch(s); m != nil {
			if m[1] != "" || m[4] != "" {
				h, _ := strconv.Atoi(m[2])
				mi, _ := strconv.Atoi(m[5])
				s = RubyFloatToS(float64(h) + float64(mi)/60.0)
			} else if s != "" {
				s = s[:1]
			}
		}
	}
	s = strings.ReplaceAll(s, ",", ".")
	return kernelFloat(s)
}

// kernelFloat は Kernel.Float(s, exception: false)。
func kernelFloat(s string) (float64, bool) {
	if !rubyFloatRe.MatchString(s) || strings.TrimSpace(s) == "" {
		return 0, false
	}
	t := strings.ReplaceAll(strings.TrimSpace(s), "_", "")
	if k := strings.IndexAny(t, "eE"); k >= 0 && !strings.ContainsAny(t[:k], "0123456789") {
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// ---------------------------------------------------------------- 稼働日計算 (Redmine::Utils::DateCalculation)

// cwday は Date#cwday (1=月 ... 7=日)。
func cwday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

// nonWorkingWeekDays は Setting.non_working_week_days (7 日すべてなら空)。
func (e *Env) nonWorkingWeekDays() []int {
	var out []int
	if e.Settings == nil {
		return []int{6, 7}
	}
	for _, s := range e.Settings.Strings("non_working_week_days") {
		out = append(out, int(rubyToI(s)))
	}
	if len(out) >= 7 {
		return nil
	}
	return out
}

func containsInt(a []int, v int) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}

// WorkingDays は working_days(from, to)。
func (e *Env) WorkingDays(from, to time.Time) int {
	days := int(to.Sub(from).Hours() / 24)
	if days <= 0 {
		return 0
	}
	nw := e.nonWorkingWeekDays()
	weeks := days / 7
	result := weeks * (7 - len(nw))
	left := days - weeks*7
	start := cwday(from)
	for i := 0; i < left; i++ {
		if !containsInt(nw, ((start+i-1)%7)+1) {
			result++
		}
	}
	return result
}

// AddWorkingDays は add_working_days(date, working_days)。
func (e *Env) AddWorkingDays(date time.Time, wd int) time.Time {
	if wd <= 0 {
		return date
	}
	nw := e.nonWorkingWeekDays()
	weeks := wd / (7 - len(nw))
	result := weeks * 7
	left := wd - weeks*(7-len(nw))
	c := cwday(date)
	for left > 0 {
		c++
		if !containsInt(nw, ((c-1)%7)+1) {
			left--
		}
		result++
	}
	return e.NextWorkingDate(date.AddDate(0, 0, result))
}

// NextWorkingDate は next_working_date(date)。
func (e *Env) NextWorkingDate(date time.Time) time.Time {
	nw := e.nonWorkingWeekDays()
	c := cwday(date)
	days := 0
	for containsInt(nw, ((c+days-1)%7)+1) {
		days++
	}
	return date.AddDate(0, 0, days)
}

// rubyToI は String#to_i。
func rubyToI(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	n, err := strconv.ParseInt(s[:j], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}( 00:00:00)?$`)

// parseDateInput は日付属性への文字列代入 (Rails の Date 型キャスト + DateValidator の判定)。
// 戻り値: 日付 (nil 可), 不正入力なら raw に元の文字列。
func parseDateInput(s string) (*time.Time, string) {
	if strings.TrimSpace(s) == "" {
		return nil, ""
	}
	if dateRe.MatchString(s) {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return &t, ""
		}
	}
	return nil, s
}

func ptrTime(t time.Time) *time.Time { return &t }

func ptrInt64(v int64) *int64 { return &v }

func ptrString(s string) *string { return &s }

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
