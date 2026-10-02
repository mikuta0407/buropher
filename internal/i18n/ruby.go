package i18n

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// RubyToS は値を Ruby の to_s と同じ規則で文字列化する（ERB の <%= %> 出力相当）。
func RubyToS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case Symbol:
		return string(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return rubyFloatToS(x)
	case float32:
		return rubyFloatToS(float64(x))
	case []any:
		return rubyInspect(x)
	case map[string]any:
		return rubyInspect(x)
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

// rubyFloatToS は Ruby の Float#to_s（最短表現。指数が -4 以上 16 未満なら小数表記）。
func rubyFloatToS(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case math.IsNaN(f):
		return "NaN"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // 例: -1.2345e+06
	neg := strings.HasPrefix(e, "-")
	e = strings.TrimPrefix(e, "-")
	mant, expS, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expS)
	digits := strings.Replace(mant, ".", "", 1)
	var s string
	if exp < -4 || exp >= 16 {
		frac := digits[1:]
		if frac == "" {
			frac = "0"
		}
		sign := "+"
		if exp < 0 {
			sign = "-"
			exp = -exp
		}
		s = fmt.Sprintf("%s.%se%s%02d", digits[:1], frac, sign, exp)
	} else if exp < 0 {
		s = "0." + strings.Repeat("0", -exp-1) + digits
	} else if len(digits) <= exp+1 {
		s = digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
	} else {
		s = digits[:exp+1] + "." + digits[exp+1:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// rubyInspect は配列・ハッシュ等を Ruby の inspect 風に文字列化する（エラーメッセージ用）。
func rubyInspect(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case string:
		return strconv.Quote(x)
	case Symbol:
		return ":" + string(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = rubyInspect(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := sortedKeys(x)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + rubyInspect(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return RubyToS(v)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// 安定した出力のため辞書順
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// rubyRound は Ruby の Float#round（0 桁、四捨五入は 0 から遠い方へ）。
func rubyRound(f float64) int64 {
	return int64(math.Round(f))
}

// humanize は ActiveSupport の String#humanize（Redmine は inflections を追加していない）。
func humanize(s string) string {
	r := strings.ReplaceAll(s, "_", " ")
	r = strings.TrimLeft(r, " \t\n\v\f\r\x00")
	if strings.HasSuffix(s, "_id") {
		r = strings.TrimSuffix(r, " id")
	}
	// ASCII 英数字の連続のみ小文字化（Ruby の /([a-z\d]+)/i）
	b := []byte(r)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	// 先頭が \w（ASCII 英数字・_）なら大文字化
	if len(b) > 0 && b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}
