package export

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// normalizeValue はドライバ差を吸収して、アーカイブに書く「生」の値へ変換する。
// 型変換の方針(docs/export-format.md):
//   - integer: int64
//   - float/decimal: float64
//   - boolean: true/false(1/0, 't'/'f', 'true'/'false' を受理)
//   - datetime: naive "YYYY-MM-DD HH:MM:SS[.ffffff]"(TZ 変換しない)
//   - date: "YYYY-MM-DD"
//   - binary: []byte
//   - string/text: string(不正な UTF-8 は []byte)
//
// 解釈できない値は元の値(文字列化)を残し、警告文字列を返す。
func normalizeValue(colType string, v any) (any, string) {
	if v == nil {
		return nil, ""
	}
	switch colType {
	case "integer":
		return normInteger(v)
	case "float", "decimal":
		return normFloat(v)
	case "boolean":
		return normBool(v)
	case "datetime":
		return normDatetime(v)
	case "date":
		return normDate(v)
	case "binary":
		switch x := v.(type) {
		case []byte:
			return append([]byte{}, x...), ""
		case string:
			return []byte(x), ""
		}
		return []byte(fmt.Sprint(v)), fmt.Sprintf("unexpected %T for binary", v)
	default: // string, text
		return normString(v)
	}
}

func asText(v any) (string, bool) {
	switch x := v.(type) {
	case []byte:
		return string(x), true
	case string:
		return x, true
	}
	return "", false
}

func normInteger(v any) (any, string) {
	switch x := v.(type) {
	case int64:
		return x, ""
	case int32:
		return int64(x), ""
	case int:
		return int64(x), ""
	case int16:
		return int64(x), ""
	case int8:
		return int64(x), ""
	case uint8:
		return int64(x), ""
	case uint16:
		return int64(x), ""
	case uint32:
		return int64(x), ""
	case uint64:
		if x <= math.MaxInt64 {
			return int64(x), ""
		}
	case bool:
		if x {
			return int64(1), ""
		}
		return int64(0), ""
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<63 {
			return int64(x), ""
		}
		return x, "non-integral value in integer column"
	}
	if s, ok := asText(v); ok {
		t := strings.TrimSpace(s)
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return n, ""
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil && f == math.Trunc(f) && math.Abs(f) < 1<<63 {
			return int64(f), ""
		}
		return keepText(s, "non-numeric value in integer column")
	}
	return fmt.Sprint(v), fmt.Sprintf("unexpected %T in integer column", v)
}

func normFloat(v any) (any, string) {
	switch x := v.(type) {
	case float64:
		return x, ""
	case float32:
		// MySQL の FLOAT(単精度)。Ruby(mysql2)と同様に 10 進の最短表現を倍精度で読み直す
		// (0.3 が 0.30000001192092896 にならないように)
		f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(x), 'g', -1, 32), 64)
		return f, ""
	case int64:
		return float64(x), ""
	case int32:
		return float64(x), ""
	case int:
		return float64(x), ""
	}
	if s, ok := asText(v); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f, ""
		}
		return keepText(s, "non-numeric value in float column")
	}
	return fmt.Sprint(v), fmt.Sprintf("unexpected %T in float column", v)
}

func normBool(v any) (any, string) {
	switch x := v.(type) {
	case bool:
		return x, ""
	case int64:
		return x != 0, ""
	case int32:
		return x != 0, ""
	case int:
		return x != 0, ""
	case uint8:
		return x != 0, ""
	case float64:
		return x != 0, ""
	}
	if s, ok := asText(v); ok {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "1", "t", "true", "y", "yes":
			return true, ""
		case "0", "f", "false", "n", "no":
			return false, ""
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return n != 0, ""
		}
		return keepText(s, "unrecognized boolean value")
	}
	return fmt.Sprint(v), fmt.Sprintf("unexpected %T in boolean column", v)
}

var reDatetime = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[ T](\d{2}):(\d{2})(?::(\d{2})(?:[.,](\d+))?)?)?\s*(Z|[+-]\d{2}(?::?\d{2})?)?$`)

// FormatNaiveDatetime は時刻の壁時計表現を "YYYY-MM-DD HH:MM:SS[.ffffff]" で返す(TZ は無視)。
func FormatNaiveDatetime(t time.Time) string {
	s := t.Format("2006-01-02 15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return s
}

func normDatetime(v any) (any, string) {
	if t, ok := v.(time.Time); ok {
		return FormatNaiveDatetime(t), ""
	}
	s, ok := asText(v)
	if !ok {
		return fmt.Sprint(v), fmt.Sprintf("unexpected %T in datetime column", v)
	}
	m := reDatetime.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return keepText(s, "unparseable datetime value")
	}
	if m[1] == "0000" {
		return keepText(s, "zero datetime value")
	}
	out := m[1] + "-" + m[2] + "-" + m[3] + " "
	hh, mi, ss := m[4], m[5], m[6]
	if hh == "" {
		hh, mi = "00", "00"
	}
	if ss == "" {
		ss = "00"
	}
	out += hh + ":" + mi + ":" + ss
	if frac := m[7]; frac != "" {
		if len(frac) > 6 {
			frac = frac[:6]
		}
		frac += strings.Repeat("0", 6-len(frac))
		if frac != "000000" {
			out += "." + frac
		}
	}
	if m[8] != "" {
		// TZ 付きの値(通常の Redmine では書かれない): 壁時計部分を残し警告
		return out, "datetime value had explicit offset " + m[8] + " (dropped)"
	}
	return out, ""
}

var reDate = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)

func normDate(v any) (any, string) {
	if t, ok := v.(time.Time); ok {
		return t.Format("2006-01-02"), ""
	}
	s, ok := asText(v)
	if !ok {
		return fmt.Sprint(v), fmt.Sprintf("unexpected %T in date column", v)
	}
	m := reDate.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || m[1] == "0000" {
		return keepText(s, "unparseable date value")
	}
	return m[0], ""
}

func normString(v any) (any, string) {
	switch x := v.(type) {
	case string:
		if !utf8.ValidString(x) {
			return []byte(x), "invalid UTF-8 in text column (stored as $b64)"
		}
		return x, ""
	case []byte:
		if !utf8.Valid(x) {
			return append([]byte{}, x...), "invalid UTF-8 in text column (stored as $b64)"
		}
		return string(x), ""
	case int64:
		return strconv.FormatInt(x, 10), ""
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), ""
	case bool:
		if x {
			return "1", ""
		}
		return "0", ""
	case time.Time:
		return FormatNaiveDatetime(x), ""
	}
	return fmt.Sprint(v), fmt.Sprintf("unexpected %T in text column", v)
}

func keepText(s, warn string) (any, string) {
	if !utf8.ValidString(s) {
		return []byte(s), warn
	}
	return s, warn
}
