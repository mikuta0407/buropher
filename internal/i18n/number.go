package i18n

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// NumberOptions は ActiveSupport::NumberHelper のオプション（"delimiter", "separator", "precision",
// "significant", "strip_insignificant_zeros"）。値に nil を入れると Ruby の `delimiter: nil` と同じ意味になる。
type NumberOptions map[string]any

// number_converter.rb の DEFAULTS
var numberDefaults = map[string]map[string]any{
	"":          {"separator": ".", "delimiter": ",", "precision": int64(3), "significant": false, "strip_insignificant_zeros": false},
	"precision": {"delimiter": ""},
	"human":     {"delimiter": "", "precision": int64(3), "significant": true, "strip_insignificant_zeros": true},
}

var storageUnitDefaults = map[string]string{"byte": "Bytes", "kb": "KB", "mb": "MB", "gb": "GB", "tb": "TB"}

// numberFormatOptions は NumberConverter#options（既定値 → number.format → number.<ns>.format → 引数）。
func (b *Bundle) numberFormatOptions(locale, namespace string, opts NumberOptions) map[string]any {
	o := map[string]any{}
	for k, v := range numberDefaults[""] {
		o[k] = v
	}
	if namespace != "" {
		for k, v := range numberDefaults[namespace] {
			o[k] = v
		}
	}
	if m, ok := b.Lookup(locale, "number.format").(map[string]any); ok {
		for k, v := range m {
			o[k] = v
		}
	}
	if namespace != "" {
		if m, ok := b.Lookup(locale, "number."+namespace+".format").(map[string]any); ok {
			for k, v := range m {
				o[k] = v
			}
		}
	}
	for k, v := range opts {
		o[k] = v
	}
	return o
}

func optString(o map[string]any, k string) string { return RubyToS(o[k]) }

func optBool(o map[string]any, k string) bool {
	v := o[k]
	return v != nil && v != false
}

func optInt(o map[string]any, k string) (int, bool) {
	switch x := o[k].(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	}
	return 0, false
}

// decimal は BigDecimal の代わりの 10 進数（値 = sign × digits × 10^-scale）。
type decimal struct {
	neg    bool
	digits string // 先頭ゼロなし（ゼロは ""）
	scale  int
}

var decimalRe = regexp.MustCompile(`^\s*([-+]?)(\d*)(?:\.(\d*))?(?:[eE]([-+]?\d+))?\s*$`)

func parseDecimal(s string) (decimal, bool) {
	s = strings.ReplaceAll(s, "_", "")
	m := decimalRe.FindStringSubmatch(s)
	if m == nil || (m[2] == "" && m[3] == "") {
		return decimal{}, false
	}
	d := decimal{neg: m[1] == "-", digits: m[2] + m[3], scale: len(m[3])}
	if m[4] != "" {
		e, err := strconv.Atoi(m[4])
		if err != nil {
			return decimal{}, false
		}
		d.scale -= e
	}
	if d.scale < 0 {
		d.digits += strings.Repeat("0", -d.scale)
		d.scale = 0
	}
	d.digits = strings.TrimLeft(d.digits, "0")
	if d.digits == "" {
		d.neg = false
	}
	return d, true
}

func decimalFromFloat(f float64) decimal {
	d, _ := parseDecimal(strconv.FormatFloat(f, 'f', -1, 64))
	if f < 0 && d.digits != "" {
		d.neg = true
	}
	return d
}

func (d decimal) isZero() bool { return d.digits == "" }

func (d decimal) float() float64 {
	f, _ := strconv.ParseFloat(d.plain(), 64)
	return f
}

// plain は指数表記なしの文字列。
func (d decimal) plain() string {
	s := d.toF()
	return s
}

// round は BigDecimal#round(precision, :half_up)。precision は負も可。
func (d decimal) round(precision int) decimal {
	if d.scale <= precision || d.isZero() {
		return d
	}
	drop := d.scale - precision
	digits := d.digits
	if len(digits) < drop {
		digits = strings.Repeat("0", drop-len(digits)) + digits
	}
	keep := digits[:len(digits)-drop]
	first := digits[len(digits)-drop]
	n := new(big.Int)
	if keep != "" {
		n.SetString(keep, 10)
	}
	if first >= '5' {
		n.Add(n, big.NewInt(1))
	}
	r := decimal{neg: d.neg, digits: n.String(), scale: precision}
	if r.digits == "0" {
		r.digits = ""
	}
	if precision < 0 {
		if r.digits != "" {
			r.digits += strings.Repeat("0", -precision)
		}
		r.scale = 0
	}
	if r.digits == "" {
		r.neg = false
	}
	return r
}

// toF は BigDecimal#to_s("F")（整数でも ".0" を付ける）。
func (d decimal) toF() string {
	digits := d.digits
	if len(digits) <= d.scale {
		digits = strings.Repeat("0", d.scale-len(digits)+1) + digits
	}
	ip := digits[:len(digits)-d.scale]
	fp := strings.TrimRight(digits[len(digits)-d.scale:], "0")
	if ip == "" {
		ip = "0"
	}
	if fp == "" {
		fp = "0"
	}
	s := ip + "." + fp
	if d.neg {
		s = "-" + s
	}
	return s
}

func digitCount(f float64) int {
	if f == 0 {
		return 1
	}
	return int(math.Floor(math.Log10(math.Abs(f)) + 1))
}

func toDecimal(number any) (decimal, bool) {
	switch x := number.(type) {
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return decimal{}, false
		}
		return decimalFromFloat(x), true
	case float32:
		return decimalFromFloat(float64(x)), true
	case string:
		return parseDecimal(x)
	case int, int64, int32, uint, uint64:
		return parseDecimal(RubyToS(x))
	}
	return decimal{}, false
}

// numberToS は数値の Ruby の to_s。
func numberToS(number any) string { return RubyToS(number) }

// insertDelimiter は /(\d)(?=(\d\d\d)+(?!\d))/ による桁区切り挿入。
func insertDelimiter(s, delim string) string {
	// 先読みが使えないため、連続する数字の塊ごとに右から 3 桁ずつ区切る
	var sb strings.Builder
	i := 0
	for i < len(s) {
		if s[i] < '0' || s[i] > '9' {
			sb.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		run := s[i:j]
		for k, c := range []byte(run) {
			sb.WriteByte(c)
			rest := len(run) - k - 1
			if rest > 0 && rest%3 == 0 {
				sb.WriteString(delim)
			}
		}
		i = j
	}
	return sb.String()
}

// numberToDelimited は NumberToDelimitedConverter#convert（options は確定済み）。
func numberToDelimited(s string, o map[string]any) string {
	left, right, hasRight := strings.Cut(s, ".")
	// Ruby の split(".") は 3 つ以上に分かれても先頭 2 つしか使わない
	if hasRight {
		if i := strings.Index(right, "."); i >= 0 {
			right = right[:i]
		}
	}
	left = insertDelimiter(left, optString(o, "delimiter"))
	if hasRight && right != "" {
		return left + optString(o, "separator") + right
	}
	return left
}

// NumberWithDelimiter は number_with_delimiter 相当。数値として解釈できない値はそのまま返す。
func (b *Bundle) NumberWithDelimiter(locale string, number any, opts NumberOptions) string {
	if number == nil {
		return ""
	}
	if _, ok := toDecimal(number); !ok {
		return numberToS(number)
	}
	o := b.numberFormatOptions(locale, "", opts)
	return numberToDelimited(numberToS(number), o)
}

// NumberWithPrecision は number_with_precision 相当。
func (b *Bundle) NumberWithPrecision(locale string, number any, opts NumberOptions) string {
	if number == nil {
		return ""
	}
	d, ok := toDecimal(number)
	if !ok {
		return numberToS(number)
	}
	o := b.numberFormatOptions(locale, "precision", opts)
	return numberToRounded(d, o)
}

// numberToRounded は NumberToRoundedConverter#convert。
func numberToRounded(d decimal, o map[string]any) string {
	precision, hasPrecision := optInt(o, "precision")
	significant := optBool(o, "significant")
	rounded := d
	if hasPrecision {
		p := precision
		if significant && precision > 0 {
			p = precision - digitCount(d.float())
		}
		rounded = d.round(p)
	}
	var formatted string
	if hasPrecision {
		p := precision
		if significant && p > 0 {
			p -= digitCount(rounded.float())
			if p < 0 {
				p = 0
			}
		}
		s := rounded.toF()
		a, bpart, _ := strings.Cut(s, ".")
		if p != 0 {
			bpart += strings.Repeat("0", p)
			a += "." + bpart[:p]
		}
		formatted = a
	} else {
		formatted = rounded.toF()
	}
	delimited := numberToDelimited(formatted, o)
	if optBool(o, "strip_insignificant_zeros") {
		sep := regexp.QuoteMeta(optString(o, "separator"))
		re1 := regexp.MustCompile(`(` + sep + `)(\d*[1-9])?0+$`)
		delimited = re1.ReplaceAllString(delimited, "${1}${2}")
		re2 := regexp.MustCompile(sep + `$`)
		delimited = re2.ReplaceAllString(delimited, "")
	}
	return delimited
}

var storageUnits = []string{"byte", "kb", "mb", "gb", "tb", "pb", "eb", "zb"}

// NumberToHumanSize は number_to_human_size 相当（ロケールの number.human.storage_units を使用）。
func (b *Bundle) NumberToHumanSize(locale string, number any, opts NumberOptions) string {
	if number == nil {
		return ""
	}
	d, ok := toDecimal(number)
	if !ok {
		return numberToS(number)
	}
	f := d.float()
	o := b.numberFormatOptions(locale, "human", opts)
	if _, ok := o["strip_insignificant_zeros"]; !ok {
		o["strip_insignificant_zeros"] = true
	}
	const base = 1024.0
	toI := int64(f) // Float#to_i（0 方向への切り捨て）
	smaller := abs64(toI) < int64(base)
	var numStr, unitKey string
	if smaller {
		numStr = strconv.FormatInt(toI, 10)
		unitKey = "byte"
	} else {
		exp := int(math.Log(math.Abs(f)) / math.Log(base))
		if exp > len(storageUnits)-1 {
			exp = len(storageUnits) - 1
		}
		human := f / math.Pow(base, float64(exp))
		numStr = numberToRounded(decimalFromFloat(human), o)
		unitKey = storageUnits[exp]
	}
	format := b.numberTranslate(locale, "human.storage_units.format", nil, "%n %u")
	unit := b.numberTranslate(locale, "human.storage_units.units."+unitKey, Vars{"count": toI}, storageUnitDefaults[unitKey])
	return strings.ReplaceAll(strings.ReplaceAll(format, "%n", numStr), "%u", unit)
}

// numberTranslate は translate_number_value_with_default（default は DEFAULTS の値）。
func (b *Bundle) numberTranslate(locale, key string, vars Vars, def string) string {
	v, ok := b.Translate(locale, "number."+key, vars)
	if !ok {
		return def
	}
	return RubyToS(v)
}
