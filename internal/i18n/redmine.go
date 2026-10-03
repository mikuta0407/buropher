// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package i18n

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Settings は Redmine::I18n が参照する Setting の値（settings パッケージに依存しないための値オブジェクト）。
type Settings struct {
	DateFormat      string // Setting.date_format（空ならロケールの date.formats.default）
	TimeFormat      string // Setting.time_format（空ならロケールの time.formats.time）
	TimespanFormat  string // Setting.timespan_format（"minutes" / "decimal"）
	StartOfWeek     string // Setting.start_of_week（空ならロケールの general_first_day_of_week）
	DefaultLanguage string // Setting.default_language
}

// Localizer は 1 リクエスト（User.current + I18n.locale）分の Redmine::I18n 相当。
type Localizer struct {
	Bundle   *Bundle
	Lang     string         // current_language（I18n.locale）
	Settings Settings       // Setting の値
	Location *time.Location // User.current のタイムゾーン（nil ならタイムゾーン未設定ユーザー）
}

// NewLocalizer は Localizer を作る。lang が無効なら Settings.DefaultLanguage、それも無効なら en。
func (b *Bundle) NewLocalizer(lang string, s Settings, loc *time.Location) *Localizer {
	l := b.FindLanguage(lang)
	if l == "" {
		l = b.FindLanguage(s.DefaultLanguage)
	}
	if l == "" {
		l = "en"
	}
	return &Localizer{Bundle: b, Lang: l, Settings: s, Location: loc}
}

// CurrentLanguage は current_language。
func (l *Localizer) CurrentLanguage() string { return l.Lang }

// SetLanguageIfValid は set_language_if_valid。有効なら言語を切り替えて true を返す。
func (l *Localizer) SetLanguageIfValid(lang string) bool {
	if f := l.Bundle.FindLanguage(lang); f != "" {
		l.Lang = f
		return true
	}
	return false
}

// argsToVars は Redmine::I18n#l の引数解釈:
// 引数なし → オプションなし、Vars/map → そのまま、文字列 → value:、それ以外（数値）→ count:。
func argsToVars(args []any) Vars {
	switch len(args) {
	case 0:
		return nil
	case 1:
		switch a := args[0].(type) {
		case nil:
			return Vars{"count": nil}
		case Vars:
			return a
		case map[string]any:
			return Vars(a)
		case string:
			return Vars{"value": a}
		default:
			return Vars{"count": a}
		}
	}
	panic(fmt.Sprintf("Translation string with multiple values: %v", args))
}

// LRaw は l(key, arg) の生の戻り値（文字列・配列・bool 等）。
func (l *Localizer) LRaw(key string, args ...any) any {
	v, _ := l.Bundle.Translate(l.Lang, key, argsToVars(args))
	return v
}

// L は Redmine の l(key[, arg])。arg は Vars（補間変数）、string（%{value}）、数値（count）のいずれか。
// 訳文が無い場合は "Translation missing: <locale>.<key>" を返す。
func (l *Localizer) L(key string, args ...any) string {
	return RubyToS(l.LRaw(key, args...))
}

// LOrHumanize は l_or_humanize(s, prefix:)。訳が無ければ s.to_s.humanize。
func (l *Localizer) LOrHumanize(s, prefix string) string {
	return RubyToS(l.Bundle.TranslateDefault(l.Lang, prefix+s, nil, humanize(s)))
}

// LHours は l_hours。
func (l *Localizer) LHours(hours float64) string {
	key := "label_f_hour_plural"
	if hours < 2.0 {
		key = "label_f_hour"
	}
	return l.L(key, Vars{"value": l.FormatHours(hours)})
}

// LHoursShort は l_hours_short。
func (l *Localizer) LHoursShort(hours float64) string {
	return l.L("label_f_hour_short", Vars{"value": l.FormatHours(hours)})
}

// LL は ll(lang, key[, arg])。arg が Vars でなければ value: として扱う。
func (b *Bundle) LL(lang, key string, arg ...any) string {
	var vars Vars
	if len(arg) > 0 {
		switch a := arg[0].(type) {
		case Vars:
			vars = a
		case map[string]any:
			vars = Vars(a)
		default:
			vars = Vars{"value": a}
		}
	} else {
		vars = Vars{"value": nil}
	}
	v, _ := b.Translate(normalizeLang(lang), key, vars)
	return RubyToS(v)
}

// normalizeLang は ll の locale 正規化（"pt-br" → "pt-BR"）。
func normalizeLang(lang string) string {
	if i := strings.LastIndex(lang, "-"); i > 0 && i < len(lang)-1 {
		return lang[:i] + "-" + strings.ToUpper(lang[i+1:])
	}
	return lang
}

// LL は Localizer からの ll。
func (l *Localizer) LL(lang, key string, arg ...any) string { return l.Bundle.LL(lang, key, arg...) }

// LU は lu(user, key[, arg])。userLang が空なら Setting.default_language を使う。
func (l *Localizer) LU(userLang, key string, arg ...any) string {
	lang := userLang
	if strings.TrimSpace(lang) == "" {
		lang = l.Settings.DefaultLanguage
	}
	return l.Bundle.LL(lang, key, arg...)
}

// FormatDate は format_date。Setting.date_format が空ならロケールの既定書式。
func (l *Localizer) FormatDate(d time.Time) string {
	return l.Bundle.LocalizeDate(l.Lang, d, l.Settings.DateFormat)
}

// UserTime は User#convert_time_to_user_timezone。
// loc があればそのゾーンへ、無ければ UTC の時刻のみサーバローカルへ変換する。
func UserTime(t time.Time, loc *time.Location) time.Time {
	if loc != nil {
		return t.In(loc)
	}
	if t.Location() == time.UTC {
		return t.Local()
	}
	return t
}

// FormatTime は format_time(time, include_date)（ユーザーは User.current = l.Location）。
func (l *Localizer) FormatTime(t time.Time, includeDate bool) string {
	return l.FormatTimeIn(t, includeDate, l.Location)
}

// FormatTimeIn は format_time(time, include_date, user) で別ユーザー（タイムゾーン loc）を指定した版。
func (l *Localizer) FormatTimeIn(t time.Time, includeDate bool, loc *time.Location) string {
	local := UserTime(t, loc)
	f := l.Settings.TimeFormat
	if strings.TrimSpace(f) == "" {
		f = "time"
	}
	s := l.Bundle.LocalizeTime(l.Lang, local, f)
	if includeDate {
		return l.FormatDate(local) + " " + s
	}
	return s
}

// FormatHours は format_hours（Setting.timespan_format が "minutes" なら h:mm、それ以外は小数 2 桁）。
func (l *Localizer) FormatHours(hours float64) string {
	minutes := rubyRound(hours * 60)
	if l.Settings.TimespanFormat == "minutes" {
		sign := ""
		if minutes < 0 {
			sign = "-"
		}
		m := abs64(minutes)
		return fmt.Sprintf("%s%d:%02d", sign, m/60, m%60)
	}
	return l.Bundle.NumberWithDelimiter(l.Lang, fmt.Sprintf("%.2f", float64(minutes)/60), NumberOptions{"delimiter": nil})
}

// NormalizeFloat は normalize_float（ロケールの小数点を "." に置換）。
func (l *Localizer) NormalizeFloat(value string) string {
	sep := l.Bundle.T(l.Lang, "number.format.separator", nil)
	if sep == "" {
		return value
	}
	var sb strings.Builder
	for _, r := range value {
		if strings.ContainsRune(sep, r) {
			sb.WriteByte('.')
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (l *Localizer) nameAt(key string, i int) string {
	arr, _ := l.Bundle.Lookup(l.Lang, key).([]any)
	if i < 0 || i >= len(arr) {
		return ""
	}
	return RubyToS(arr[i])
}

// DayName は day_name（0=日曜、7 も日曜）。
func (l *Localizer) DayName(day int) string { return l.nameAt("date.day_names", mod7(day)) }

// AbbrDayName は abbr_day_name。
func (l *Localizer) AbbrDayName(day int) string { return l.nameAt("date.abbr_day_names", mod7(day)) }

// DayLetter は day_letter（略称の先頭 1 文字）。
func (l *Localizer) DayLetter(day int) string {
	r := []rune(l.AbbrDayName(day))
	if len(r) == 0 {
		return ""
	}
	return string(r[0])
}

// MonthName は month_name（1〜12）。
func (l *Localizer) MonthName(month int) string { return l.nameAt("date.month_names", month) }

func mod7(d int) int { return ((d % 7) + 7) % 7 }

// StartOfWeek は Redmine::Helpers::Calendar#first_wday（1=月曜〜7=日曜）。
// Setting.start_of_week が 1/6/7 以外（空を含む）ならロケールの general_first_day_of_week を使う。
func (l *Localizer) StartOfWeek() int {
	s := rubyToI(l.Settings.StartOfWeek)
	switch s {
	case 1, 6, 7:
		return s
	}
	return int(floorMod(int64(rubyToI(l.L("general_first_day_of_week"))-1), 7)) + 1
}

// rubyToI は String#to_i（先頭の整数部分のみ。解釈できなければ 0）。
func rubyToI(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n := 0
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		return -n
	}
	return n
}

// DistanceOfTimeInWords は distance_of_time_in_words(from, to)（include_seconds なし）。
func (l *Localizer) DistanceOfTimeInWords(from, to time.Time) string {
	return l.Bundle.DistanceOfTimeInWords(l.Lang, from, to, false)
}

// TimeAgoInWords は time_ago_in_words。
func (l *Localizer) TimeAgoInWords(from time.Time) string {
	return l.Bundle.TimeAgoInWords(l.Lang, from, false)
}

// DistanceOfDateInWords は distance_of_date_in_words。
func (l *Localizer) DistanceOfDateInWords(from, to time.Time) string {
	return l.Bundle.DistanceOfDateInWords(l.Lang, from, to)
}

// NumberToHumanSize は number_to_human_size。
func (l *Localizer) NumberToHumanSize(n any) string {
	return l.Bundle.NumberToHumanSize(l.Lang, n, nil)
}

// NumberWithDelimiter は number_with_delimiter。
func (l *Localizer) NumberWithDelimiter(n any, opts NumberOptions) string {
	return l.Bundle.NumberWithDelimiter(l.Lang, n, opts)
}

// ValidLanguages は valid_languages（I18n.available_locales）。
func (b *Bundle) ValidLanguages() []string { return b.AvailableLocales() }

// FindLanguage は find_language（大文字小文字を無視して利用可能ロケールを探す。無ければ ""）。
func (b *Bundle) FindLanguage(lang string) string {
	return b.lookupLC[strings.ToLower(lang)]
}

// LanguagesOptions は languages_options: [[general_lang_name, code], ...] を名前でソートしたもの。
func (b *Bundle) LanguagesOptions() [][2]string {
	b.langOnce.Do(func() {
		var opts [][2]string
		for _, lang := range b.available {
			opts = append(opts, [2]string{b.LL(lang, "general_lang_name"), lang})
		}
		// Ruby の sort_by(&:first)（バイト列比較）。名前が同じ場合の順序は不定だが重複はない。
		sort.SliceStable(opts, func(i, j int) bool { return opts[i][0] < opts[j][0] })
		b.langOptions = opts
	})
	return append([][2]string(nil), b.langOptions...)
}
