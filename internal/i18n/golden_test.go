// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package i18n

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ゴールデンデータは testdata/gen_golden.rb で実際の Redmine 7.0.1（Rails I18n）から生成したもの。
//
// buropher は Redmine 本体の訳文の値に含まれる製品名 "Redmine" を読み込み時に "Buropher" へ置換する
// （brand.SubstituteLocale）。ゴールデンは Redmine の出力そのままなので、訳文を返す関数（l / ll / lu /
// l_or_humanize）と訳文ダンプの期待値には同じ置換（RebrandLocaleValue）を適用してから比較する。

type goldenCase struct {
	Fn     string `json:"fn"`
	Locale string `json:"locale"`
	Args   []any  `json:"args"`
	Want   any    `json:"want"`
}

func decodeJSON(t *testing.T, path string, v any) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r interface{ Read([]byte) (int, error) } = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		r = gz
	}
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		t.Fatal(err)
	}
}

// fromJSON は json.Number を int64 / float64 に戻す（Ruby の Integer / Float の区別を保つ）。
func fromJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		s := x.String()
		if strings.ContainsAny(s, ".eE") {
			f, _ := x.Float64()
			return f
		}
		i, _ := x.Int64()
		return i
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = fromJSON(e)
		}
		return out
	case map[string]any:
		if s, ok := x["__sym"]; ok && len(x) == 1 {
			return Symbol(s.(string))
		}
		out := map[string]any{}
		for k, e := range x {
			out[k] = fromJSON(e)
		}
		return out
	}
	return v
}

// normalize は Go 側の値を比較用に正規化する（数値は float64 に寄せる）。
func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	}
	return v
}

func TestTranslationsDump(t *testing.T) {
	var dump map[string]map[string]any
	decodeJSON(t, "testdata/translations.json.gz", &dump)
	b := Default()
	if got, want := len(b.AvailableLocales()), len(dump); got != want {
		t.Errorf("ロケール数: got %d want %d", got, want)
	}
	fails := 0
	total := 0
	for loc, m := range dump {
		for key, raw := range m {
			total++
			want := RebrandLocaleValue(key, fromJSON(raw))
			if em, ok := want.(map[string]any); ok {
				if _, isErr := em["__error"]; isErr {
					continue
				}
			}
			got, _ := b.Translate(loc, key, nil)
			if !reflect.DeepEqual(normalize(got), normalize(want)) {
				fails++
				if fails <= 30 {
					t.Errorf("%s %s: got %#v want %#v", loc, key, got, want)
				}
			}
		}
	}
	if fails > 0 {
		t.Errorf("%d / %d 件不一致", fails, total)
	}
	t.Logf("%d 件比較", total)
}

func argString(a any) string {
	if a == nil {
		return ""
	}
	return a.(string)
}

func argFloat(a any) float64 {
	switch x := a.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	panic(fmt.Sprintf("数値ではありません: %#v", a))
}

func argTime(t *testing.T, a any) time.Time {
	tm, err := time.Parse(time.RFC3339Nano, a.(string))
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func argDate(t *testing.T, a any) time.Time {
	tm, err := time.Parse("2006-01-02", a.(string))
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func toArgs(a []any) []any {
	out := make([]any, len(a))
	for i, e := range a {
		v := fromJSON(e)
		if m, ok := v.(map[string]any); ok {
			v = Vars(m)
		}
		out[i] = v
	}
	return out
}

func TestGolden(t *testing.T) {
	var g struct {
		TZ    string       `json:"tz"`
		Cases []goldenCase `json:"cases"`
	}
	decodeJSON(t, "testdata/golden.json", &g)
	loc, err := time.LoadLocation(g.TZ)
	if err != nil {
		t.Fatal(err)
	}
	saved := time.Local
	time.Local = loc
	defer func() { time.Local = saved }()

	b := Default()
	counts := map[string]int{}
	fails := map[string]int{}
	for _, c := range g.Cases {
		args := toArgs(c.Args)
		want := normalize(fromJSON(c.Want))
		switch c.Fn {
		case "l", "l_or_humanize":
			want = RebrandLocaleValue(argString(args[0]), want)
		case "ll":
			want = RebrandLocaleValue(argString(args[1]), want)
		case "lu":
			want = RebrandLocaleValue(argString(args[2]), want)
		}
		if m, ok := want.(map[string]any); ok {
			if e, isErr := m["__error"]; isErr {
				// Ruby で例外になるケース。複数形データ不備のみ想定し、Go 側はエラーメッセージ文字列を返す。
				if e != "I18n::InvalidPluralizationData" || c.Fn != "l" {
					t.Errorf("想定外の例外ケース: %v", c)
					continue
				}
				got := (&Localizer{Bundle: b, Lang: c.Locale}).L(args[0].(string), args[1:]...)
				if !strings.HasPrefix(got, "translation data ") {
					t.Errorf("%v: got %q", c.Args, got)
				}
				continue
			}
		}
		s := Settings{TimespanFormat: "minutes", DefaultLanguage: "en"}
		l := &Localizer{Bundle: b, Lang: c.Locale, Settings: s}
		var got any
		switch c.Fn {
		case "l":
			got = l.LRaw(args[0].(string), args[1:]...)
		case "l_or_humanize":
			got = l.LOrHumanize(args[0].(string), args[1].(string))
		case "ll":
			got = b.LL(args[0].(string), args[1].(string), args[2:]...)
		case "lu":
			l.Settings.DefaultLanguage = args[1].(string)
			got = l.LU(args[0].(string), args[2].(string))
		case "format_hours", "l_hours", "l_hours_short":
			l.Settings.TimespanFormat = args[0].(string)
			h := argFloat(args[1])
			switch c.Fn {
			case "format_hours":
				got = l.FormatHours(h)
			case "l_hours":
				got = l.LHours(h)
			default:
				got = l.LHoursShort(h)
			}
		case "day_name":
			got = l.DayName(int(args[0].(int64)))
		case "abbr_day_name":
			got = l.AbbrDayName(int(args[0].(int64)))
		case "day_letter":
			got = l.DayLetter(int(args[0].(int64)))
		case "month_name":
			got = l.MonthName(int(args[0].(int64)))
		case "format_date":
			l.Settings.DateFormat = args[0].(string)
			got = l.FormatDate(argDate(t, args[1]))
		case "l_date":
			got = b.LocalizeDate(c.Locale, argDate(t, args[1]), args[0].(string))
		case "l_time":
			got = b.LocalizeTime(c.Locale, argTime(t, args[1]), args[0].(string))
		case "format_time":
			l.Settings.DateFormat = args[0].(string)
			l.Settings.TimeFormat = args[1].(string)
			got = l.FormatTimeIn(argTime(t, args[3]), args[4].(bool), UserLocation(argString(args[2])))
		case "strftime":
			z, ok := FindTimeZone(args[2].(string))
			if !ok {
				t.Fatalf("zone %v", args[2])
			}
			zl, _ := z.Location()
			got = Strftime(argTime(t, args[1]).In(zl), args[0].(string))
		case "distance_of_time_in_words":
			got = b.DistanceOfTimeInWords(c.Locale, argTime(t, args[0]), argTime(t, args[1]), args[2].(bool))
		case "distance_of_date_in_words":
			got = b.DistanceOfDateInWords(c.Locale, argDate(t, args[0]), argDate(t, args[1]))
		case "number_to_human_size":
			got = b.NumberToHumanSize(c.Locale, args[0], nil)
		case "number_with_delimiter":
			var opts NumberOptions
			switch args[1] {
			case nil:
				opts = NumberOptions{"delimiter": nil}
			case "locale":
				opts = NumberOptions{"delimiter": b.T(c.Locale, "number.format.delimiter", nil)}
			}
			got = b.NumberWithDelimiter(c.Locale, args[0], opts)
		case "number_with_precision":
			got = b.NumberWithPrecision(c.Locale, args[0], NumberOptions{"precision": args[1]})
		case "valid_languages":
			var arr []any
			for _, s := range b.ValidLanguages() {
				arr = append(arr, s)
			}
			got = arr
		case "languages_options":
			var arr []any
			for _, o := range b.LanguagesOptions() {
				arr = append(arr, []any{o[0], o[1]})
			}
			got = arr
		case "find_language":
			if f := b.FindLanguage(args[0].(string)); f != "" {
				got = f
			}
		case "time_zones":
			var arr []any
			for _, z := range AllTimeZones() {
				arr = append(arr, []any{z.String(), z.Name, z.Identifier})
			}
			got = arr
		default:
			t.Fatalf("未知の fn: %s", c.Fn)
		}
		counts[c.Fn]++
		if !reflect.DeepEqual(normalize(got), want) {
			fails[c.Fn]++
			if fails[c.Fn] <= 8 {
				t.Errorf("%s %s %v:\n got  %#v\n want %#v", c.Fn, c.Locale, c.Args, got, want)
			}
		}
	}
	t.Logf("ケース数: %v", counts)
	for fn, n := range counts {
		if fails[fn] > 0 {
			t.Errorf("%s: %d / %d 件不一致", fn, fails[fn], n)
		}
	}
}
