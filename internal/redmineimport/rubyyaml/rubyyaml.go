// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package rubyyaml は Redmine(Ruby/Psych)が YAML シリアライズした列を
// Go の汎用値へデコードする。
//
// 対象: settings.value, user_preferences.others, queries.filters/column_names/
// sort_criteria/options, roles.permissions/settings, custom_fields.possible_values/
// format_store, repositories.extra_info, imports.settings など。
//
// Psych(Ruby 3.3 / Psych 5)の挙動に合わせて以下を行う:
//   - プレーンスカラーは Psych::ScalarScanner#tokenize と同じ規則で型を決める
//     (yes/no/on/off も真偽値、カンマ入り整数、60 進数、`:sym` はシンボル等)。
//     引用符付き・ブロックスカラー(| >)は常に文字列。
//   - Ruby 固有タグを解釈する: !ruby/symbol, !ruby/sym, !ruby/string,
//     !ruby/hash:<Class>(HashWithIndifferentAccess 等), !ruby/hash-with-ivars(elements を取り出す),
//     !ruby/object:ActionController::Parameters(parameters を取り出す),
//     !ruby/object:<Class>(インスタンス変数の Hash として返す), !ruby/array, !ruby/struct,
//     !ruby/range, !ruby/regexp, !ruby/object:BigDecimal, !ruby/object:Set,
//     !ruby/object:ActiveSupport::TimeWithZone, !binary / !!binary など。
//   - アンカー/エイリアス、マージキー(<<)を展開する。
//
// 返す値の型:
//
//	nil, bool, int64, float64, string, Symbol, Date, time.Time,
//	[]any, map[string]any(Options.Ordered なら OrderedMap)
//
// Hash のキーは常に文字列化する(シンボルキー `:foo` は "foo"、整数キー 1 は "1")。
package rubyyaml

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Symbol は Ruby のシンボル値(`:foo` / `!ruby/symbol foo`)。値に先頭 `:` は含まない。
type Symbol string

// Date は Ruby の Date 値("YYYY-MM-DD")。
type Date string

// MapItem は OrderedMap の 1 要素。
type MapItem struct {
	Key   string
	Value any
}

// OrderedMap は挿入順を保持する Hash(Options.Ordered 指定時に返す)。
// Ruby の Hash は挿入順を保持し、Redmine はクエリのフィルタ表示順などにそれを使う。
type OrderedMap []MapItem

// Get はキーに対応する値を返す(後勝ち)。
func (m OrderedMap) Get(key string) (any, bool) {
	for i := len(m) - 1; i >= 0; i-- {
		if m[i].Key == key {
			return m[i].Value, true
		}
	}
	return nil, false
}

// Map は通常の map へ変換する(入れ子は変換しない)。
func (m OrderedMap) Map() map[string]any {
	out := make(map[string]any, len(m))
	for _, it := range m {
		out[it.Key] = it.Value
	}
	return out
}

// MarshalJSON は挿入順を保ったまま JSON オブジェクトとして出力する。
// 値は Plain 相当に変換される。
func (m OrderedMap) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, it := range m {
		if i > 0 {
			sb.WriteByte(',')
		}
		k, err := json.Marshal(it.Key)
		if err != nil {
			return nil, err
		}
		sb.Write(k)
		sb.WriteByte(':')
		v, err := json.Marshal(jsonValue(it.Value))
		if err != nil {
			return nil, err
		}
		sb.Write(v)
	}
	sb.WriteByte('}')
	return []byte(sb.String()), nil
}

// Options はデコードの挙動を指定する。
type Options struct {
	// Ordered が真なら Hash を map[string]any ではなく OrderedMap で返す。
	Ordered bool
	// StrictInteger は Psych の strict_integer(カンマ入り整数を整数と見なさない)。既定 false は Redmine と同じ。
	StrictInteger bool
}

// Decode は YAML 文字列をデコードする。空文字列・空文書は nil を返す。
func Decode(src string) (any, error) {
	return DecodeWith(src, Options{})
}

// DecodeWith はオプション付きで YAML 文字列をデコードする。
func DecodeWith(src string, opt Options) (v any, err error) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, fmt.Errorf("rubyyaml: %w", err)
	}
	// エイリアスの展開で入力に比例しない量の値を作らせない（billion laughs）。
	// エイリアスがなければノード数は入力の長さ以下なので、通常の値がこの上限に達することはない
	d := &decoder{opt: opt, budget: 10*len(src) + 100000}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(decodeError); ok {
				v, err = nil, e.err
				return
			}
			panic(r)
		}
	}()
	return d.node(&doc), nil
}

type decodeError struct{ err error }

type decoder struct {
	opt   Options
	depth int
	// budget は残りの展開ノード数（エイリアス・マージキーの展開を含む）。
	budget int
}

const maxDepth = 2000

func (d *decoder) fail(format string, args ...any) {
	panic(decodeError{fmt.Errorf("rubyyaml: "+format, args...)})
}

// enter はノード 1 つ分の深さと展開量を消費する（戻り値の関数で深さを戻す）。
func (d *decoder) enter() func() {
	d.depth++
	if d.depth > maxDepth {
		d.fail("nesting too deep (recursive alias?)")
	}
	d.budget--
	if d.budget < 0 {
		d.fail("too many nodes (alias expansion)")
	}
	return func() { d.depth-- }
}

func (d *decoder) node(n *yaml.Node) any {
	defer d.enter()()
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return d.node(n.Content[0])
	case yaml.AliasNode:
		if n.Alias == nil {
			d.fail("unknown alias %q", n.Value)
		}
		return d.node(n.Alias)
	case yaml.ScalarNode:
		return d.scalar(n)
	case yaml.SequenceNode:
		return d.sequence(n)
	case yaml.MappingNode:
		return d.mapping(n)
	}
	return nil
}

// explicitTag は明示されたタグを返す(暗黙解決されたタグは空)。
func explicitTag(n *yaml.Node) string {
	if n.Style&yaml.TaggedStyle == 0 {
		return ""
	}
	t := n.Tag
	// yaml.v3 は tag:yaml.org,2002:xxx を !!xxx に短縮する
	if strings.HasPrefix(t, "tag:yaml.org,2002:") {
		t = "!!" + strings.TrimPrefix(t, "tag:yaml.org,2002:")
	}
	return t
}

func isPlain(n *yaml.Node) bool {
	return n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0
}

// splitRubyTag は "!ruby/object:Foo::Bar" → ("!ruby/object", "Foo::Bar")。
func splitRubyTag(tag string) (string, string) {
	if !strings.HasPrefix(tag, "!ruby/") {
		return tag, ""
	}
	if i := strings.Index(tag, ":"); i >= 0 {
		return tag[:i], tag[i+1:]
	}
	return tag, ""
}

func (d *decoder) scalar(n *yaml.Node) any {
	tag := explicitTag(n)
	base, class := splitRubyTag(tag)
	switch base {
	case "":
		if isPlain(n) {
			return d.tokenize(n.Value)
		}
		return n.Value
	case "!!str", "!str", "!ruby/string":
		return n.Value
	case "!ruby/symbol", "!ruby/sym":
		return Symbol(n.Value)
	case "!!binary", "!binary":
		return decodeBinary(n.Value)
	case "!!int", "!int":
		if v, ok := parseRubyInt(n.Value); ok {
			return v
		}
		d.fail("invalid !!int %q", n.Value)
	case "!!float", "!float":
		if v, ok := d.tokenize(n.Value).(float64); ok {
			return v
		}
		if v, ok := d.tokenize(n.Value).(int64); ok {
			return float64(v)
		}
		if f, err := strconv.ParseFloat(strings.NewReplacer(",", "", "_", "").Replace(n.Value), 64); err == nil {
			return f
		}
		d.fail("invalid !!float %q", n.Value)
	case "!!null", "!null":
		return nil
	case "!!bool", "!bool":
		return d.tokenize(n.Value)
	case "!!timestamp", "!timestamp", "!ruby/object":
		switch class {
		case "BigDecimal":
			// Psych: "18:0.1e1"(精度:値)
			s := n.Value
			if i := strings.Index(s, ":"); i >= 0 {
				s = s[i+1:]
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
			return n.Value
		case "DateTime", "":
			if t, ok := parseTime(n.Value); ok {
				return t
			}
		}
		return n.Value
	case "!ruby/regexp", "!ruby/class", "!ruby/module", "!ruby/encoding", "!ruby/range":
		return n.Value
	case "!ruby/array", "!ruby/hash", "!!map", "!!seq", "!!set", "!!omap":
		// 空コレクションがスカラー表記で書かれた場合など
		if n.Value == "" {
			return nil
		}
		return n.Value
	}
	// 未知のタグ: タグを無視して通常規則で解釈
	if isPlain(n) {
		return d.tokenize(n.Value)
	}
	return n.Value
}

func (d *decoder) sequence(n *yaml.Node) any {
	out := make([]any, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, d.node(c))
	}
	return out
}

// keyString は Hash のキーを文字列化する。
func keyString(k any) string {
	switch v := k.(type) {
	case nil:
		return ""
	case string:
		return v
	case Symbol:
		return string(v)
	case Date:
		return string(v)
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case time.Time:
		return v.Format("2006-01-02 15:04:05.999999999 -07:00")
	default:
		b, _ := json.Marshal(Plain(v))
		return string(b)
	}
}

// mapItems は Mapping ノードを挿入順の (キー, 値) 列にする(マージキー展開込み)。
func (d *decoder) mapItems(n *yaml.Node) OrderedMap {
	// マージキーは node を経由せず再帰するため、ここでも深さ（自己参照するマージの循環）と展開量を数える
	defer d.enter()()
	var items OrderedMap
	pos := map[string]int{}
	set := func(k string, v any) {
		if i, ok := pos[k]; ok {
			// Ruby の Hash#[]= は既存キーの位置を保ったまま値を更新する
			items[i].Value = v
			return
		}
		pos[k] = len(items)
		items = append(items, MapItem{k, v})
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn, vn := n.Content[i], n.Content[i+1]
		if kn.Kind == yaml.ScalarNode && kn.Value == "<<" && isPlain(kn) && explicitTag(kn) == "" {
			// マージキー
			var srcs []*yaml.Node
			target := vn
			if target.Kind == yaml.AliasNode && target.Alias != nil {
				target = target.Alias
			}
			switch target.Kind {
			case yaml.MappingNode:
				srcs = []*yaml.Node{target}
			case yaml.SequenceNode:
				for _, c := range target.Content {
					if c.Kind == yaml.AliasNode && c.Alias != nil {
						c = c.Alias
					}
					srcs = append(srcs, c)
				}
			}
			if len(srcs) > 0 {
				for _, s := range srcs {
					if s.Kind != yaml.MappingNode {
						continue
					}
					for _, it := range d.mapItems(s) {
						set(it.Key, it.Value)
					}
				}
				continue
			}
		}
		set(keyString(d.node(kn)), d.node(vn))
	}
	return items
}

func (d *decoder) finishMap(items OrderedMap) any {
	if d.opt.Ordered {
		if items == nil {
			items = OrderedMap{}
		}
		return items
	}
	return items.Map()
}

func (d *decoder) mapping(n *yaml.Node) any {
	tag := explicitTag(n)
	base, class := splitRubyTag(tag)
	switch base {
	case "!ruby/hash-with-ivars":
		// elements: 実データ, ivars: インスタンス変数(ActionController::Parameters の permitted 等)
		items := d.mapItems(n)
		if el, ok := items.Get("elements"); ok {
			return el
		}
		return d.finishMap(items)
	case "!ruby/object":
		items := d.mapItems(n)
		switch class {
		case "ActionController::Parameters":
			if p, ok := items.Get("parameters"); ok {
				return p
			}
			if p, ok := items.Get("elements"); ok {
				return p
			}
		case "ActiveSupport::TimeWithZone":
			if u, ok := items.Get("utc"); ok {
				return u
			}
		case "Set", "SortedSet":
			if h, ok := items.Get("hash"); ok {
				// 非順序モードでは hash が Go の map になり要素の順序が実行ごとに変わるため、
				// 順序付きで読み直して Ruby の Set と同じ挿入順にする
				if !d.opt.Ordered {
					if hn := mapValueNode(n, "hash"); hn != nil {
						od := *d
						od.opt.Ordered = true
						h = od.node(hn)
						d.budget = od.budget
					}
				}
				return setKeys(h)
			}
		case "Date":
			// 古い Syck 形式などへのフォールバック
		}
		return d.finishMap(items)
	case "!ruby/string":
		// インスタンス変数付き文字列: {str: "...", ...}
		items := d.mapItems(n)
		if s, ok := items.Get("str"); ok {
			return s
		}
		return d.finishMap(items)
	case "!ruby/array":
		items := d.mapItems(n)
		if a, ok := items.Get("internal"); ok {
			return a
		}
		return d.finishMap(items)
	}
	// 無タグ / !ruby/hash:<Class> / !ruby/struct / !ruby/exception / 未知タグ → Hash
	return d.finishMap(d.mapItems(n))
}

// mapValueNode はマッピングノード n のキー key に対応する値ノードを返す（無ければ nil）。
func mapValueNode(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if kn := n.Content[i]; kn.Kind == yaml.ScalarNode && kn.Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// setKeys は Set#hash 表現({要素 => true}) から要素配列を取り出す。
func setKeys(h any) any {
	switch m := h.(type) {
	case OrderedMap:
		out := make([]any, 0, len(m))
		for _, it := range m {
			out = append(out, it.Key)
		}
		return out
	case map[string]any:
		out := make([]any, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out
	}
	return h
}

// decodeBinary は !binary(Base64)を復号する。UTF-8 として正しければそのまま、
// そうでなければ Latin-1 とみなして UTF-8 へ変換した文字列を返す。
func decodeBinary(s string) string {
	var sb strings.Builder
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' {
			sb.WriteRune(c)
		}
	}
	clean := sb.String()
	if len(clean)%4 == 1 {
		clean = clean[:len(clean)-1]
	}
	b, err := base64.RawStdEncoding.DecodeString(clean)
	if err != nil {
		return s
	}
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// ---- Psych::ScalarScanner#tokenize の移植 ----

var (
	reStringish = regexp.MustCompile(`^[^\d.:-]?[\p{L}\p{M}\p{Nl}_\s\v!@#$%^&*(){}<>|/\\~;=]+`)
	reTime      = regexp.MustCompile(`^-?\d{4}-\d{1,2}-\d{1,2}(?:[Tt]|\s+)\d{1,2}:\d\d:\d\d(?:\.\d*)?(?:\s*(?:Z|[-+]\d{1,2}:?(?:\d\d)?))?$`)
	reDate      = regexp.MustCompile(`^\d{4}-(?:1[012]|0\d|\d)-(?:[12]\d|3[01]|0\d|\d)$`)
	reInf       = regexp.MustCompile(`(?i)^\+?\.inf$`)
	reNegInf    = regexp.MustCompile(`(?i)^-\.inf$`)
	reNaN       = regexp.MustCompile(`(?i)^\.nan$`)
	reSexagInt  = regexp.MustCompile(`^[-+]?[0-9][0-9_]*(:[0-5]?[0-9]){1,2}$`)
	reSexagFlt  = regexp.MustCompile(`^[-+]?[0-9][0-9_]*(:[0-5]?[0-9]){1,2}\.[0-9_]*$`)
	reFloat     = regexp.MustCompile(`^(?:[-+]?([0-9][0-9_,]*)?\.[0-9]*([eE][-+][0-9]+)?)$`)
	reIntStrict = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+|[-+]?0[0-7_]+|[-+]?(0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+)$`)
	reIntLegacy = regexp.MustCompile(`^(?:[-+]?0b[0-1_,]+|[-+]?0[0-7_,]+|[-+]?(?:0|[1-9](?:[0-9]|,[0-9]|_[0-9])*)|[-+]?0x[0-9a-fA-F_,]+)$`)
	reNull      = regexp.MustCompile(`(?i)^null$`)
	reTrue      = regexp.MustCompile(`(?i)^(yes|true|on)$`)
	reFalse     = regexp.MustCompile(`(?i)^(no|false|off)$`)
	reNotYTONF  = regexp.MustCompile(`(?i)^[^ytonf~]`)
)

func (d *decoder) tokenize(s string) any {
	if s == "" {
		return nil
	}
	if reStringish.MatchString(s) || strings.Contains(s, "\n") {
		if utf8.RuneCountInString(s) > 5 {
			return s
		}
		switch {
		case reNotYTONF.MatchString(s):
			return s
		case s == "~" || reNull.MatchString(s):
			return nil
		case reTrue.MatchString(s):
			return true
		case reFalse.MatchString(s):
			return false
		}
		return s
	}
	if reTime.MatchString(s) {
		if t, ok := parseTime(s); ok {
			return t
		}
		return s
	}
	if reDate.MatchString(s) {
		if dt, ok := parseDate(s); ok {
			return dt
		}
		return s
	}
	switch {
	case reInf.MatchString(s):
		return math.Inf(1)
	case reNegInf.MatchString(s):
		return math.Inf(-1)
	case reNaN.MatchString(s):
		return math.NaN()
	}
	if strings.HasPrefix(s, ":") && utf8.RuneCountInString(s) >= 2 {
		// Ruby: /^:(["'])(.*)\1/(貪欲マッチ)
		if q := s[1]; q == '"' || q == '\'' {
			if i := strings.LastIndexByte(s[2:], q); i >= 0 {
				return Symbol(strings.TrimPrefix(s[2:2+i], ":"))
			}
		}
		return Symbol(s[1:])
	}
	if reSexagInt.MatchString(s) {
		return sexagesimal(s, false)
	}
	if reSexagFlt.MatchString(s) {
		return sexagesimal(s, true)
	}
	if reFloat.MatchString(s) {
		if s == "." || s == "-." || s == "+." {
			return s
		}
		c := strings.NewReplacer(",", "", "_", "").Replace(s)
		if f, err := strconv.ParseFloat(c, 64); err == nil {
			return f
		}
		// "1.e+5" のような形
		c = strings.Replace(c, ".e", "e", 1)
		c = strings.Replace(c, ".E", "E", 1)
		if f, err := strconv.ParseFloat(c, 64); err == nil {
			return f
		}
		return s
	}
	re := reIntLegacy
	if d.opt.StrictInteger {
		re = reIntStrict
	}
	if re.MatchString(s) {
		if v, ok := parseRubyInt(s); ok {
			return v
		}
		return s
	}
	return s
}

// parseRubyInt は Ruby の Integer(str.delete(',_')) 相当。範囲外は (0,false)。
func parseRubyInt(s string) (any, bool) {
	c := strings.NewReplacer(",", "", "_", "").Replace(strings.TrimSpace(s))
	if v, err := strconv.ParseInt(c, 0, 64); err == nil {
		return v, true
	}
	// int64 を超える整数は文字列(10 進表記)で返す
	if b, ok := new(big.Int).SetString(c, 0); ok {
		return b.String(), true
	}
	return nil, false
}

func sexagesimal(s string, float bool) any {
	parts := strings.Split(s, ":")
	n := len(parts)
	if float {
		var f float64
		for i, p := range parts {
			v, _ := strconv.ParseFloat(strings.ReplaceAll(p, "_", ""), 64)
			f += v * math.Pow(60, float64(abs(i-2)))
		}
		_ = n
		return f
	}
	var total int64
	for i, p := range parts {
		v, _ := strconv.ParseInt(strings.ReplaceAll(p, "_", ""), 10, 64)
		total += v * int64(math.Pow(60, float64(abs(i-2))))
	}
	return total
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

var reTimeParts = regexp.MustCompile(`(\d+:\d+:\d+)(?:\.(\d*))?\s*(Z|[-+]\d+(:\d\d)?)?`)

// parseTime は Psych::ScalarScanner#parse_time 相当。TZ 指定がない時刻は UTC として返す。
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	idx := strings.IndexAny(s, " tT")
	if idx < 0 {
		return time.Time{}, false
	}
	datePart, timePart := s[:idx], s[idx+1:]
	var yy, mo, dd int
	neg := strings.HasPrefix(datePart, "-")
	dp := strings.Split(strings.TrimPrefix(datePart, "-"), "-")
	if len(dp) != 3 {
		return time.Time{}, false
	}
	yy, _ = strconv.Atoi(dp[0])
	if neg {
		yy = -yy
	}
	mo, _ = strconv.Atoi(dp[1])
	dd, _ = strconv.Atoi(dp[2])
	m := reTimeParts.FindStringSubmatch(timePart)
	if m == nil {
		return time.Time{}, false
	}
	hms := strings.Split(m[1], ":")
	hh, _ := strconv.Atoi(hms[0])
	mi, _ := strconv.Atoi(hms[1])
	ss, _ := strconv.Atoi(hms[2])
	var ns int
	if m[2] != "" {
		frac := m[2]
		if len(frac) > 9 {
			frac = frac[:9]
		}
		frac += strings.Repeat("0", 9-len(frac))
		ns, _ = strconv.Atoi(frac)
	}
	if mo < 1 || mo > 12 || dd < 1 || dd > 31 || hh > 24 || mi > 59 || ss > 60 {
		return time.Time{}, false
	}
	loc := time.UTC
	if z := m[3]; z != "" && z != "Z" {
		sign := 1
		if z[0] == '-' {
			sign = -1
		}
		z = z[1:]
		var h, mm int
		if i := strings.Index(z, ":"); i >= 0 {
			h, _ = strconv.Atoi(z[:i])
			mm, _ = strconv.Atoi(z[i+1:])
		} else {
			h, _ = strconv.Atoi(z)
		}
		loc = time.FixedZone("", sign*(h*3600+mm*60))
	}
	return time.Date(yy, time.Month(mo), dd, hh, mi, ss, ns, loc), true
}

func parseDate(s string) (Date, bool) {
	t, err := time.Parse("2006-1-2", s)
	if err != nil {
		return "", false
	}
	return Date(t.Format("2006-01-02")), true
}

// jsonValue は OrderedMap を保ったまま(順序付きで出力するため)Plain 相当の変換を行う。
func jsonValue(v any) any {
	switch x := v.(type) {
	case OrderedMap:
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = jsonValue(e)
		}
		return out
	}
	return Plain(v)
}

// Plain は値を JSON 化しやすい素の値へ再帰変換する:
// Symbol→string, Date→string, time.Time→RFC3339Nano 文字列, OrderedMap→map[string]any,
// NaN/Inf→文字列。
func Plain(v any) any {
	switch x := v.(type) {
	case Symbol:
		return string(x)
	case Date:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return strconv.FormatFloat(x, 'g', -1, 64)
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Plain(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = Plain(e)
		}
		return out
	case OrderedMap:
		out := make(map[string]any, len(x))
		for _, it := range x {
			out[it.Key] = Plain(it.Value)
		}
		return out
	}
	return v
}
