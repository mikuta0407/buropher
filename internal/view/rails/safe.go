// Package rails は Redmine のビューが使う ActionView ヘルパー（Rails 7.2）を
// バイト単位で互換に再実装する。
//
// Ruby の「html_safe な文字列」は template.HTML で表現する。template.HTML 以外の
// 値（string, 数値, nil など）はすべて「安全でない」値として扱い、出力時に
// ERB::Util.html_escape と同じ規則でエスケープする。
//
// Ruby のハッシュ（挿入順序を保持する）は Hash 型で表現する。属性の出力順序は
// Rails と同じくハッシュの挿入順序に従う。
package rails

import (
	"fmt"
	"html/template"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

// HTML は html_safe な文字列（ActiveSupport::SafeBuffer 相当）。
type HTML = template.HTML

// Symbol は Ruby のシンボル（:foo）を表す。
// 主に LabelledFormBuilder の label: オプション（シンボルなら翻訳キー）の区別に使う。
type Symbol string

// IsSafe は v が html_safe（template.HTML）かどうかを返す。
func IsSafe(v any) bool {
	_, ok := v.(template.HTML)
	return ok
}

// EscapeString は ERB::Util.html_escape と同じ規則で文字列をエスケープする。
// & " ' < > をそれぞれ &amp; &quot; &#39; &lt; &gt; に置換する（+ などは変換しない）。
func EscapeString(s string) string {
	if !strings.ContainsAny(s, `&"'<>`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// H は ERB::Util.html_escape（ビューの h）。html_safe な値はそのまま返し、
// それ以外は to_s してエスケープした html_safe な値を返す。
func H(v any) HTML {
	if s, ok := v.(template.HTML); ok {
		return s
	}
	return HTML(EscapeString(ToS(v)))
}

// unwrappedEscape は ERB::Util.unwrapped_html_escape（html_safe ならそのまま）。
func unwrappedEscape(v any) string {
	if s, ok := v.(template.HTML); ok {
		return string(s)
	}
	return EscapeString(ToS(v))
}

// EscapeOnce は ERB::Util.html_escape_once。既存の実体参照は二重にエスケープしない。
func EscapeOnce(v any) HTML {
	s := ToS(v)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			if n := entityLen(s[i:]); n > 0 {
				b.WriteString(s[i : i+n])
				i += n - 1
			} else {
				b.WriteString("&amp;")
			}
		default:
			b.WriteByte(c)
		}
	}
	return HTML(b.String())
}

// entityLen は s の先頭が &name; / &#123; / &#x1f; 形式なら長さを返す。
// （Rails の HTML_ESCAPE_ONCE_REGEXP: /["><']|&(?!([a-zA-Z]+|(#\d+)|(#[xX][\dA-Fa-f]+));)/ 相当）
func entityLen(s string) int {
	if len(s) < 3 || s[0] != '&' {
		return 0
	}
	i := 1
	if s[1] == '#' {
		i = 2
		if i < len(s) && (s[i] == 'x' || s[i] == 'X') {
			j := i + 1
			for j < len(s) && isHex(s[j]) {
				j++
			}
			if j > i+1 && j < len(s) && s[j] == ';' {
				return j + 1
			}
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i && j < len(s) && s[j] == ';' {
			return j + 1
		}
		return 0
	}
	j := i
	for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
		j++
	}
	if j > i && j < len(s) && s[j] == ';' {
		return j + 1
	}
	return 0
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// Raw は文字列を html_safe として扱う（Ruby の raw / html_safe）。
func Raw(v any) HTML {
	if s, ok := v.(template.HTML); ok {
		return s
	}
	return HTML(ToS(v))
}

// ToS は Ruby の to_s 相当の文字列化を行う。
// nil は空文字列、bool は "true"/"false"、浮動小数点数は Ruby の Float#to_s 形式。
func ToS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case template.HTML:
		return string(x)
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
		return floatToS(x)
	case float32:
		return floatToS(float64(x))
	case fmt.Stringer:
		return x.String()
	case error:
		return x.Error()
	case []any:
		// Ruby の Array#to_s は inspect 形式だが、ビューで使われることはまずない。
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = ToS(e)
		}
		return strings.Join(parts, "")
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return ""
		}
		return ToS(rv.Elem().Interface())
	case reflect.String:
		return rv.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10)
	case reflect.Bool:
		return ToS(rv.Bool())
	case reflect.Float32, reflect.Float64:
		return floatToS(rv.Float())
	}
	return fmt.Sprint(v)
}

// floatToS は Ruby の Float#to_s を近似する（1.0 → "1.0", 1.5 → "1.5", 1e20 → "1.0e+20"）。
func floatToS(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	abs := math.Abs(f)
	if abs != 0 && (abs >= 1e16 || abs < 1e-4) {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		if exp[0] == '+' || exp[0] == '-' {
			sign := exp[:1]
			digits := strings.TrimLeft(exp[1:], "0")
			if len(digits) < 2 {
				digits = fmt.Sprintf("%02s", digits)
			}
			exp = sign + digits
		}
		return mant + "e" + exp
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// IsBlank は ActiveSupport の blank? 相当。
// nil / false / 空白のみの文字列 / 空の配列・ハッシュ・マップが blank。
func IsBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return isBlankString(x)
	case template.HTML:
		return isBlankString(string(x))
	case Symbol:
		return isBlankString(string(x))
	case Hash:
		return x.Len() == 0
	case *Hash:
		return x == nil || x.Len() == 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	case reflect.String:
		return isBlankString(rv.String())
	}
	return false
}

// IsPresent は !IsBlank。
func IsPresent(v any) bool { return !IsBlank(v) }

func isBlankString(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// truthy は Ruby の真偽判定（nil と false のみ偽）。
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	rv := reflect.ValueOf(v)
	if (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) && rv.IsNil() {
		return false
	}
	return true
}

// SafeJoin は safe_join(array, sep)。各要素を（html_safe でなければ）エスケープして連結する。
// 配列は平坦化される。
func SafeJoin(items any, sep ...any) HTML {
	sepStr := ""
	if len(sep) > 0 {
		sepStr = unwrappedEscape(sep[0])
	}
	var parts []string
	for _, e := range flatten(items) {
		parts = append(parts, unwrappedEscape(e))
	}
	return HTML(strings.Join(parts, sepStr))
}

// flatten は配列（スライス）を再帰的に平坦化した []any を返す。スライスでなければ要素 1 つ。
func flatten(v any) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		var out []any
		for _, e := range x {
			if isSlice(e) {
				out = append(out, flatten(e)...)
			} else {
				out = append(out, e)
			}
		}
		return out
	case string, template.HTML:
		return []any{x}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		var out []any
		for i := 0; i < rv.Len(); i++ {
			e := rv.Index(i).Interface()
			if isSlice(e) {
				out = append(out, flatten(e)...)
			} else {
				out = append(out, e)
			}
		}
		return out
	}
	return []any{v}
}

func isSlice(v any) bool {
	switch v.(type) {
	case nil, string, template.HTML, []byte:
		return false
	case []any:
		return true
	}
	k := reflect.ValueOf(v).Kind()
	return k == reflect.Slice || k == reflect.Array
}

// toSlice は v をスライスとして []any に変換する（Array(v) 相当。nil は空、スカラーは 1 要素）。
func toSlice(v any) []any {
	if v == nil {
		return nil
	}
	if !isSlice(v) {
		return []any{v}
	}
	if x, ok := v.([]any); ok {
		return x
	}
	rv := reflect.ValueOf(v)
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

// concatHTML は html_safe な断片を連結する。
func concatHTML(parts ...HTML) HTML {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(string(p))
	}
	return HTML(b.String())
}

// Humanize は ActiveSupport の String#humanize の簡易版（_id 除去、_ → 空白、先頭大文字）。
func Humanize(s string) string {
	orig := s
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if strings.HasSuffix(orig, "_id") {
		s = strings.TrimSuffix(s, " id")
	}
	s = strings.ToLower(s)
	if s == "" || !(unicode.IsLetter([]rune(s)[0]) || unicode.IsDigit([]rune(s)[0])) {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// Dasherize は String#dasherize（_ → -）。
func Dasherize(s string) string { return strings.ReplaceAll(s, "_", "-") }
