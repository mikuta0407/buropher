// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package i18n

import (
	"strconv"
	"strings"
	"time"
)

var (
	enDayNames       = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	enAbbrDayNames   = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	enMonthNames     = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	enAbbrMonthNames = []string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
)

// Strftime は Ruby の Time#strftime 互換の書式化を行う（ローカライズなし。%a 等は英語）。
// t はそのタイムゾーンで書式化される。%Z は t のゾーン略称（ActiveSupport::TimeWithZone と同じ）。
func Strftime(t time.Time, format string) string {
	zone, _ := t.Zone()
	return strftime(t, format, zone)
}

// StrftimeDate は Ruby の Date#strftime 相当（時刻は 0、%z は +0000、%Z は +00:00）。
func StrftimeDate(d time.Time, format string) string {
	y, m, dd := d.Date()
	return strftime(time.Date(y, m, dd, 0, 0, 0, 0, time.UTC), format, "+00:00")
}

func strftime(t time.Time, format, zone string) string {
	var sb strings.Builder
	sb.Grow(len(format) + 16)
	n := len(format)
	for i := 0; i < n; i++ {
		c := format[i]
		if c != '%' {
			sb.WriteByte(c)
			continue
		}
		start := i
		i++
		var (
			padding  byte // 0: 既定, '-', '_', '0'
			upcase   bool
			chcase   bool
			colons   int
			width    = -1
			modifier byte
		)
	flags:
		for ; i < n; i++ {
			switch format[i] {
			case '-':
				padding = '-'
			case '_':
				padding = '_'
			case '0':
				padding = '0'
			case '^':
				upcase = true
			case '#':
				chcase = true
			case ':':
				colons++
			default:
				break flags
			}
		}
		if i < n && format[i] >= '1' && format[i] <= '9' {
			w := 0
			for i < n && format[i] >= '0' && format[i] <= '9' {
				w = w*10 + int(format[i]-'0')
				i++
			}
			width = w
		}
		if i < n && (format[i] == 'E' || format[i] == 'O') {
			modifier = format[i]
			i++
		}
		if i >= n {
			sb.WriteString(format[start:])
			break
		}
		conv := format[i]
		literal := func() { sb.WriteString(format[start : i+1]) }
		if modifier == 'E' && !strings.ContainsRune("cCxXyY", rune(conv)) {
			literal()
			continue
		}
		if modifier == 'O' && !strings.ContainsRune("deHkIlmMSuUVwWy", rune(conv)) {
			literal()
			continue
		}
		if colons > 0 && conv != 'z' {
			literal()
			continue
		}
		num := func(v int64, defWidth int, defPad byte) {
			pad := defPad
			switch padding {
			case '-':
				pad = 0
			case '_':
				pad = ' '
			case '0':
				pad = '0'
			}
			w := defWidth
			if width >= 0 {
				w = width
			}
			s := strconv.FormatInt(abs64(v), 10)
			neg := v < 0
			if pad != 0 {
				l := len(s)
				if neg {
					l++
				}
				if l < w {
					if pad == '0' {
						s = strings.Repeat("0", w-l) + s
						if neg {
							s = "-" + s
						}
					} else {
						if neg {
							s = "-" + s
						}
						s = strings.Repeat(" ", w-l) + s
					}
					sb.WriteString(s)
					return
				}
			}
			if neg {
				s = "-" + s
			}
			sb.WriteString(s)
		}
		str := func(s string, lower bool) {
			if upcase {
				s = strings.ToUpper(s)
			}
			if chcase {
				if lower {
					s = strings.ToLower(s)
				} else {
					s = strings.ToUpper(s)
				}
			}
			if width > 0 && len([]rune(s)) < width && padding != '-' {
				p := byte(' ')
				if padding == '0' {
					p = '0'
				}
				s = strings.Repeat(string(p), width-len([]rune(s))) + s
			}
			sb.WriteString(s)
		}
		composite := func(f string) {
			s := strftime(t, f, zone)
			if upcase {
				s = strings.ToUpper(s)
			}
			if width > 0 && len(s) < width && padding != '-' {
				p := " "
				if padding == '0' {
					p = "0"
				}
				s = strings.Repeat(p, width-len(s)) + s
			}
			sb.WriteString(s)
		}
		year := int64(t.Year())
		switch conv {
		case 'Y':
			num(year, 4, '0')
		case 'C':
			num(floorDiv(year, 100), 2, '0')
		case 'y':
			num(floorMod(year, 100), 2, '0')
		case 'm':
			num(int64(t.Month()), 2, '0')
		case 'd':
			num(int64(t.Day()), 2, '0')
		case 'e':
			num(int64(t.Day()), 2, ' ')
		case 'j':
			num(int64(t.YearDay()), 3, '0')
		case 'H':
			num(int64(t.Hour()), 2, '0')
		case 'k':
			num(int64(t.Hour()), 2, ' ')
		case 'I':
			num(int64(hour12(t.Hour())), 2, '0')
		case 'l':
			num(int64(hour12(t.Hour())), 2, ' ')
		case 'M':
			num(int64(t.Minute()), 2, '0')
		case 'S':
			num(int64(t.Second()), 2, '0')
		case 'L', 'N':
			w := width
			if w < 0 {
				if conv == 'L' {
					w = 3
				} else {
					w = 9
				}
			}
			s := strconv.FormatInt(int64(t.Nanosecond()), 10)
			s = strings.Repeat("0", 9-len(s)) + s
			if w <= 9 {
				s = s[:w]
			} else {
				s += strings.Repeat("0", w-9)
			}
			sb.WriteString(s)
		case 's':
			num(t.Unix(), 1, '0')
		case 'u':
			wd := int64(t.Weekday())
			if wd == 0 {
				wd = 7
			}
			num(wd, 1, '0')
		case 'w':
			num(int64(t.Weekday()), 1, '0')
		case 'U':
			yd := t.YearDay() - 1
			num(int64((yd+7-int(t.Weekday()))/7), 2, '0')
		case 'W':
			yd := t.YearDay() - 1
			num(int64((yd+7-(int(t.Weekday())+6)%7)/7), 2, '0')
		case 'G':
			y, _ := t.ISOWeek()
			num(int64(y), 4, '0')
		case 'g':
			y, _ := t.ISOWeek()
			num(floorMod(int64(y), 100), 2, '0')
		case 'V':
			_, w := t.ISOWeek()
			num(int64(w), 2, '0')
		case 'a':
			str(enAbbrDayNames[t.Weekday()], false)
		case 'A':
			str(enDayNames[t.Weekday()], false)
		case 'b', 'h':
			str(enAbbrMonthNames[t.Month()], false)
		case 'B':
			str(enMonthNames[t.Month()], false)
		case 'p':
			if t.Hour() < 12 {
				str("AM", true)
			} else {
				str("PM", true)
			}
		case 'P':
			if t.Hour() < 12 {
				str("am", true)
			} else {
				str("pm", true)
			}
		case 'Z':
			str(zone, true)
		case 'z':
			_, off := t.Zone()
			sb.WriteString(formatZ(off, colons, width, padding))
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case '%':
			sb.WriteByte('%')
		case 'c':
			composite("%a %b %e %H:%M:%S %Y")
		case 'D', 'x':
			composite("%m/%d/%y")
		case 'F':
			composite("%Y-%m-%d")
		case 'T', 'X':
			composite("%H:%M:%S")
		case 'R':
			composite("%H:%M")
		case 'r':
			composite("%I:%M:%S %p")
		case 'v':
			composite("%e-%^b-%4Y")
		default:
			literal()
		}
	}
	return sb.String()
}

// formatZ は Ruby strftime.c の %z 処理の移植。
func formatZ(off, colons, width int, padding byte) string {
	sign := 1
	if off < 0 {
		off = -off
		sign = -1
	}
	precision := width
	switch colons {
	case 0:
		if precision <= 5 {
			precision = 2
		} else {
			precision -= 3
		}
	case 1:
		if precision <= 6 {
			precision = 2
		} else {
			precision -= 4
		}
	default:
		if precision <= 9 {
			precision = 2
		} else {
			precision -= 7
		}
	}
	h := off / 3600
	hs := strconv.Itoa(h)
	var s string
	if padding == '_' {
		// "%+*ld"（幅 precision+1、空白詰め）
		s = "+" + hs
		if sign < 0 {
			s = "-" + hs
		}
		if len(s) < precision+1 {
			s = strings.Repeat(" ", precision+1-len(s)) + s
		}
		if sign < 0 && off < 3600 {
			b := []byte(s)
			b[len(b)-2] = '-'
			s = string(b)
		}
	} else {
		// "%+.*ld"（符号＋ゼロ詰め precision 桁）
		if len(hs) < precision {
			hs = strings.Repeat("0", precision-len(hs)) + hs
		}
		if sign < 0 {
			s = "-" + hs
		} else {
			s = "+" + hs
		}
		if sign < 0 && off < 3600 {
			s = "-" + s[1:]
		}
	}
	off %= 3600
	if colons >= 1 {
		s += ":"
	}
	s += twoDigits(off / 60)
	off %= 60
	if colons >= 2 {
		s += ":" + twoDigits(off)
	}
	return s
}

func twoDigits(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

func hour12(h int) int {
	h %= 12
	if h == 0 {
		return 12
	}
	return h
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int64) int64 {
	return a - floorDiv(a, b)*b
}
