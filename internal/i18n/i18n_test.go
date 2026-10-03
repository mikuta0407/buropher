package i18n

import (
	"reflect"
	"testing"
	"time"
)

func TestFallbacks(t *testing.T) {
	cases := map[string][]string{
		"zh-TW": {"zh-TW", "zh", "en"},
		"sr-YU": {"sr-YU", "sr", "en"},
		"en":    {"en"},
		"ja":    {"ja", "en"},
	}
	for in, want := range cases {
		if got := Fallbacks(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Fallbacks(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestOverlay(t *testing.T) {
	b := Default()
	if got := b.T("ja", "buropher.sso.button_login_with", Vars{"provider": "GitHub"}); got != "GitHub でログイン" {
		t.Errorf("ja overlay: %q", got)
	}
	// en/ja 以外は en にフォールバック
	if got := b.T("de", "buropher.sso.label_sso", nil); got != "Single sign-on" {
		t.Errorf("de overlay fallback: %q", got)
	}
}

// Redmine 本体の訳文は読み込み時に製品名だけ Buropher へ置換される（キーと除外キーは変えない）。
func TestRebrand(t *testing.T) {
	b := Default()
	cases := []struct{ loc, key, want string }{
		{"en", "label_oauth_permission_admin", "Administrate this Buropher"},
		{"ja", "label_oauth_permission_admin", "このBuropherの管理"},
		{"ja", "text_scm_config", "バージョン管理システムのコマンドをconfig/configuration.ymlで設定できます。設定後、Redmineを再起動してください。"},
	}
	for _, c := range cases {
		if got := b.T(c.loc, c.key, nil); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.loc, c.key, got, c.want)
		}
	}
}

func TestPsychTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"No", false}, {"yes", true}, {"ON", true}, {"off", false}, {"~", nil}, {"null", nil},
		{"Nope", "Nope"}, {"y", "y"}, {"Yes, delete", "Yes, delete"}, {"3", int64(3)}, {"1.5", 1.5},
		{":year", Symbol("year")}, {"%m/%d/%Y", "%m/%d/%Y"}, {"012", int64(10)}, {"1_000", int64(1000)},
	}
	for _, c := range cases {
		if got := psychTokenize(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("psychTokenize(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestInterpolate(t *testing.T) {
	cases := []struct {
		s    string
		vars Vars
		want string
	}{
		{"%{a} and %{b}", Vars{"a": 1, "b": "x"}, "1 and x"},
		{"100%% %{a}", Vars{"a": 1.0}, "100% 1.0"},
		{"%{missing}", Vars{"a": 1}, "%{missing}"},
		{"%<n>.2f", Vars{"n": 1.005}, "1.00"},
		{"no vars %%", nil, "no vars %%"},
	}
	for _, c := range cases {
		if got := Interpolate(c.s, c.vars); got != c.want {
			t.Errorf("Interpolate(%q) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestRubyFloatToS(t *testing.T) {
	cases := map[float64]string{1: "1.0", 1.5: "1.5", 0.0001: "0.0001", 0.00001: "1.0e-05", 1e16: "1.0e+16", 123456789012345.6: "123456789012345.6", -2.25: "-2.25"}
	for in, want := range cases {
		if got := rubyFloatToS(in); got != want {
			t.Errorf("rubyFloatToS(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestLocalizerBasics(t *testing.T) {
	b := Default()
	l := b.NewLocalizer("JA", Settings{DefaultLanguage: "fr"}, nil)
	if l.CurrentLanguage() != "ja" {
		t.Fatalf("lang = %q", l.CurrentLanguage())
	}
	if got := b.NewLocalizer("xx", Settings{DefaultLanguage: "fr"}, nil).Lang; got != "fr" {
		t.Errorf("default language: %q", got)
	}
	if got := l.L("nonexistent_key"); got != "Translation missing: ja.nonexistent_key" {
		t.Errorf("missing: %q", got)
	}
	// ja の general_first_day_of_week は 7（日曜）
	if got := l.StartOfWeek(); got != 7 {
		t.Errorf("StartOfWeek ja = %d", got)
	}
	l.Settings.StartOfWeek = "1"
	if got := l.StartOfWeek(); got != 1 {
		t.Errorf("StartOfWeek setting = %d", got)
	}
	de := b.NewLocalizer("de", Settings{}, nil)
	if got := de.StartOfWeek(); got != 1 {
		t.Errorf("StartOfWeek de = %d", got)
	}
	if got := de.NormalizeFloat("1,5"); got != "1.5" {
		t.Errorf("NormalizeFloat = %q", got)
	}
	if !l.SetLanguageIfValid("zh-tw") || l.Lang != "zh-TW" {
		t.Errorf("SetLanguageIfValid: %q", l.Lang)
	}
	if l.SetLanguageIfValid("xx") || l.Lang != "zh-TW" {
		t.Errorf("SetLanguageIfValid invalid: %q", l.Lang)
	}
}

func TestTimeZones(t *testing.T) {
	opts := TimeZoneOptions()
	if opts[0] != [2]string{"(GMT-12:00) International Date Line West", "International Date Line West"} {
		t.Errorf("先頭: %v", opts[0])
	}
	z, ok := FindTimeZone("Tokyo")
	if !ok || z.Identifier != "Asia/Tokyo" || z.String() != "(GMT+09:00) Tokyo" {
		t.Errorf("Tokyo: %+v", z)
	}
	if _, ok := FindTimeZone("Nowhere"); ok {
		t.Error("不明なゾーンが見つかった")
	}
	if z, ok := FindTimeZone("Asia/Kolkata"); !ok || z.UTCOffset != 19800 {
		t.Errorf("IANA: %+v", z)
	}
	if UserLocation("") != nil {
		t.Error("空のタイムゾーンは nil")
	}
	tm := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := UserTime(tm, UserLocation("Eastern Time (US & Canada)")); got.Hour() != 19 {
		t.Errorf("UserTime: %v", got)
	}
	if SecondsToUTCOffset(-12600, false) != "-0330" {
		t.Error("SecondsToUTCOffset")
	}
}
