package rails

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Truncate は truncate(text, length: 30, omission: "...", separator: nil, escape: true)。
// text が nil なら nil を返す。escape が偽でなければ結果を HTML エスケープした template.HTML を返す。
func Truncate(text any, options *Hash) any {
	if text == nil {
		return nil
	}
	length := 30
	if l, ok := options.Lookup("length"); ok {
		length = toInt(l)
	}
	content := StringTruncate(ToS(text), length, ToS(options.Get("omission")), options.Get("separator"))
	if options.Get("escape") == false {
		return HTML(content)
	}
	return H(content)
}

// StringTruncate は ActiveSupport の String#truncate（文字数単位）。
// omission が空文字列なら "..."、separator が nil でなければその位置で切る。
func StringTruncate(s string, truncateTo int, omission string, separator any) string {
	if utf8.RuneCountInString(s) <= truncateTo {
		return s
	}
	if omission == "" {
		omission = "..."
	}
	room := truncateTo - utf8.RuneCountInString(omission)
	if room < 0 {
		room = 0
	}
	runes := []rune(s)
	stop := room
	if separator != nil {
		sep := []rune(ToS(separator))
		// rindex(separator, room)
		found := -1
		for i := room; i >= 0; i-- {
			if i+len(sep) <= len(runes) && string(runes[i:i+len(sep)]) == string(sep) {
				found = i
				break
			}
		}
		if found >= 0 {
			stop = found
		}
	}
	if stop > len(runes) {
		stop = len(runes)
	}
	return string(runes[:stop]) + omission
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	n := 0
	for _, c := range ToS(v) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

var (
	paraSplit = regexp.MustCompile(`\n\n+`)
)

// splitParagraphs は TextHelper#split_paragraphs。
func splitParagraphs(text string) []string {
	if IsBlank(text) {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := paraSplit.Split(text, -1)
	// Ruby の String#split は末尾の空要素を除く
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, t := range parts {
		parts[i] = insertBR(t)
	}
	return parts
}

// insertBR は gsub(/([^\n]\n)(?=[^\n])/, '\1<br />') を先読みなしで実装したもの。
func insertBR(t string) string {
	var b strings.Builder
	rs := []rune(t)
	for i, r := range rs {
		b.WriteRune(r)
		if r == '\n' && i > 0 && rs[i-1] != '\n' && i+1 < len(rs) && rs[i+1] != '\n' {
			b.WriteString("<br />")
		}
	}
	return b.String()
}

// SimpleFormat は simple_format(text, html_options, options)。
// options の sanitize: false でサニタイズを省略、wrapper_tag: で <p> 以外を使う。
func SimpleFormat(text any, htmlOptions *Hash, options *Hash) HTML {
	wrapper := "p"
	if w := options.Get("wrapper_tag"); truthy(w) {
		wrapper = ToS(w)
	}
	s := ToS(text)
	if options.Fetch("sanitize", true) != false {
		s = string(Sanitize(s))
	}
	paras := splitParagraphs(s)
	if len(paras) == 0 {
		return ContentTag(wrapper, nil, htmlOptions)
	}
	out := make([]string, len(paras))
	for i, p := range paras {
		out[i] = string(ContentTag(wrapper, HTML(p), htmlOptions))
	}
	return HTML(strings.Join(out, "\n\n"))
}

// cycle は TextHelper::Cycle。
type cycle struct {
	values []string
	index  int
}

// Cycle は cycle(first_value, *values, name: "default")。
// 名前付きにする場合は最後の引数に NewHash("name", "x") を渡す。
func (v *View) Cycle(values ...any) string {
	name := "default"
	if n := len(values); n > 0 && isHash(values[n-1]) {
		h, _ := ToHash(values[n-1])
		name = ToS(h.Fetch("name", "default"))
		values = values[:n-1]
	}
	var vals []string
	for _, x := range flatten(values) {
		vals = append(vals, ToS(x))
	}
	if v.cycles == nil {
		v.cycles = map[string]*cycle{}
	}
	c := v.cycles[name]
	if c == nil || strings.Join(c.values, "\x00") != strings.Join(vals, "\x00") || len(c.values) != len(vals) {
		c = &cycle{values: vals}
		v.cycles[name] = c
	}
	if len(c.values) == 0 {
		return ""
	}
	val := c.values[c.index]
	c.index = (c.index + 1) % len(c.values)
	return val
}

// CurrentCycle は current_cycle(name)。
func (v *View) CurrentCycle(name ...string) any {
	n := "default"
	if len(name) > 0 {
		n = name[0]
	}
	c := v.cycles[n]
	if c == nil || len(c.values) == 0 {
		return nil
	}
	return c.values[(c.index-1+len(c.values))%len(c.values)]
}

// ResetCycle は reset_cycle(name)。
func (v *View) ResetCycle(name ...string) {
	n := "default"
	if len(name) > 0 {
		n = name[0]
	}
	if c := v.cycles[n]; c != nil {
		c.index = 0
	}
}

var pluralOne = regexp.MustCompile(`^1(\.0+)?$`)

// Pluralize は pluralize(count, singular, plural)（英語の語形変化規則のみ）。
// plural が空なら Inflector で複数形を作る。count が nil なら 0 と表示する。
func Pluralize(count any, singular string, plural string) string {
	word := singular
	cs := ToS(count)
	if !(isOne(count) || pluralOne.MatchString(cs)) {
		if plural != "" {
			word = plural
		} else {
			word = PluralizeWord(singular)
		}
	}
	if count == nil {
		cs = "0"
	}
	return cs + " " + word
}

func isOne(v any) bool {
	switch x := v.(type) {
	case int:
		return x == 1
	case int64:
		return x == 1
	case float64:
		return x == 1
	}
	return false
}

type inflectRule struct {
	re  *regexp.Regexp
	rep string
}

var (
	pluralRules  []inflectRule
	uncountables = []string{"equipment", "information", "rice", "money", "species", "series", "fish", "sheep", "jeans", "police"}
)

func init() {
	// ActiveSupport::Inflector の英語規則（後に定義したものが優先されるため逆順で保持）
	add := func(re, rep string) {
		pluralRules = append([]inflectRule{{regexp.MustCompile("(?i)" + re), rep}}, pluralRules...)
	}
	add(`$`, "s")
	add(`s$`, "s")
	add(`^(ax|test)is$`, "${1}es")
	add(`(octop|vir)us$`, "${1}i")
	add(`(octop|vir)i$`, "${1}i")
	add(`(alias|status)$`, "${1}es")
	add(`(bu)s$`, "${1}ses")
	add(`(buffal|tomat)o$`, "${1}oes")
	add(`([ti])um$`, "${1}a")
	add(`([ti])a$`, "${1}a")
	add(`sis$`, "ses")
	add(`(?:([^f])fe|([lr])f)$`, "${1}${2}ves")
	add(`(hive)$`, "${1}s")
	add(`([^aeiouy]|qu)y$`, "${1}ies")
	add(`(x|ch|ss|sh)$`, "${1}es")
	add(`(matr|vert|ind)(?:ix|ex)$`, "${1}ices")
	add(`^(m|l)ouse$`, "${1}ice")
	add(`^(m|l)ice$`, "${1}ice")
	add(`^(ox)$`, "${1}en")
	add(`^(oxen)$`, "${1}")
	add(`(quiz)$`, "${1}zes")
	for _, ir := range [][2]string{{"person", "people"}, {"man", "men"}, {"child", "children"}, {"sex", "sexes"}, {"move", "moves"}, {"zombie", "zombies"}} {
		s, p := ir[0], ir[1]
		add(`(`+s[:1]+`)`+s[1:]+`$`, "${1}"+p[1:])
		add(`(`+p[:1]+`)`+p[1:]+`$`, "${1}"+p[1:])
	}
}

// PluralizeWord は String#pluralize（英語）。
func PluralizeWord(word string) string {
	if word == "" {
		return word
	}
	lw := strings.ToLower(word)
	for _, u := range uncountables {
		if strings.HasSuffix(lw, u) && (len(lw) == len(u) || !isWordChar(lw[len(lw)-len(u)-1])) {
			return word
		}
	}
	for _, r := range pluralRules {
		if r.re.MatchString(word) {
			return r.re.ReplaceAllString(word, r.rep)
		}
	}
	return word
}

func isWordChar(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
