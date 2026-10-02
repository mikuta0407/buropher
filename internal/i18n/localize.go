package i18n

import (
	"regexp"
	"strings"
	"time"
)

var localizationFormatRe = regexp.MustCompile(`%\^?[aAbBpP]`)

// LocalizeDate は I18n.l(date, format: ...) 相当。
// format に "%" を含む場合は strftime 書式として、含まない場合は date.formats.<format> のキー名
// （"default" / "short" / "long" 等。空なら "default"）として扱う（Ruby の String / Symbol の区別に対応）。
func (b *Bundle) LocalizeDate(locale string, d time.Time, format string) string {
	y, m, dd := d.Date()
	date := time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
	f, ok := b.resolveFormat(locale, "date", format)
	if !ok {
		return f
	}
	return strftime(date, b.translateLocalizationFormat(locale, date, f, true), "+00:00")
}

// LocalizeTime は I18n.l(time, format: ...) 相当（time.formats.<format>）。t はそのゾーンで書式化される。
func (b *Bundle) LocalizeTime(locale string, t time.Time, format string) string {
	f, ok := b.resolveFormat(locale, "time", format)
	if !ok {
		return f
	}
	zone, _ := t.Zone()
	return strftime(t, b.translateLocalizationFormat(locale, t, f, false), zone)
}

func (b *Bundle) resolveFormat(locale, typ, format string) (string, bool) {
	if strings.Contains(format, "%") {
		return format, true
	}
	if format == "" {
		format = "default"
	}
	v, ok := b.Translate(locale, typ+".formats."+format, nil)
	if !ok {
		return RubyToS(v), false
	}
	return RubyToS(v), true
}

// translateLocalizationFormat は I18n::Backend::Base#translate_localization_format の移植。
// %a %A %b %B %p %P（および ^ 付き）をロケールの曜日名・月名・午前午後に置き換える。
func (b *Bundle) translateLocalizationFormat(locale string, t time.Time, format string, isDate bool) string {
	var missing string
	out := localizationFormatRe.ReplaceAllStringFunc(format, func(m string) string {
		up := strings.Contains(m, "^")
		var key string
		var idx int
		switch m[len(m)-1] {
		case 'a':
			key, idx = "date.abbr_day_names", int(t.Weekday())
		case 'A':
			key, idx = "date.day_names", int(t.Weekday())
		case 'b':
			key, idx = "date.abbr_month_names", int(t.Month())
		case 'B':
			key, idx = "date.month_names", int(t.Month())
		case 'p', 'P':
			h := t.Hour()
			if isDate {
				h = 0
			}
			k := "time.am"
			if h >= 12 {
				k = "time.pm"
			}
			v, ok := b.Translate(locale, k, nil)
			if !ok && missing == "" {
				missing = RubyToS(v)
			}
			s := RubyToS(v)
			if m == "%p" {
				return strings.ToUpper(s)
			}
			if m == "%P" {
				return strings.ToLower(s)
			}
			return "" // %^p / %^P は Ruby の case に該当せず nil（空文字）に置換される
		}
		v, ok := b.Translate(locale, key, nil)
		if !ok {
			if missing == "" {
				missing = RubyToS(v)
			}
			return ""
		}
		arr, _ := v.([]any)
		var s string
		if idx < len(arr) {
			s = RubyToS(arr[idx])
		}
		if up {
			s = strings.ToUpper(s)
		}
		return s
	})
	if missing != "" {
		return missing
	}
	return out
}
