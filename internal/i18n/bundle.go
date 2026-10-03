// Package i18n は Rails I18n（i18n gem 1.14 + Fallbacks + Pluralization）と
// Redmine::I18n 互換のローカライズ機能を提供する。
//
// ロケールは web.Locales() から次の順で読み込み、同一ロケール内では後勝ちで深いマージを行う
// （Redmine 実行時の I18n.load_path の順序と同じ）:
//
//  1. rails/{activesupport,activemodel,activerecord,actionview}.en.yml（Rails gem 同梱の既定訳）
//  2. rails/doorkeeper.*.yml（doorkeeper-i18n gem）、rails/doorkeeper-gem.en.yml（doorkeeper gem 本体）
//  3. redmine/*.yml（Redmine 本体。ファイルは無改変で、読み込み時に訳文の値の製品名 "Redmine" を
//     "Buropher" に置換する。brand.SubstituteLocale / brand.LocaleExcluded を参照）
//  4. overlay/*.yml（buropher 独自キー）
//
// 利用可能ロケール（valid_languages）は redmine/*.yml のファイル名で決まる。
package i18n

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/mikuta0407/buropher/internal/brand"
	"github.com/mikuta0407/buropher/web"
)

// Vars は補間変数（Ruby の options ハッシュ相当）。"count" は複数形選択にも使われる。
type Vars map[string]any

// Bundle は全ロケールの訳文を保持する。読み込み後は不変で、並行利用して安全。
type Bundle struct {
	trees     map[string]map[string]any
	available []string // 利用可能ロケール（ソート済み）
	lookupLC  map[string]string

	langOnce    sync.Once
	langOptions [][2]string
}

var (
	defaultOnce   sync.Once
	defaultBundle *Bundle
)

// Default は web.Locales() から読み込んだ Bundle を返す（初回のみ読み込み）。
// 埋め込みロケールが壊れている場合は panic する。
func Default() *Bundle {
	defaultOnce.Do(func() {
		b, err := Load(web.Locales())
		if err != nil {
			panic(err)
		}
		defaultBundle = b
	})
	return defaultBundle
}

// railsGemFiles は Rails gem 同梱ロケールの読み込み順（I18n.load_path と同じ）。
var railsGemFiles = []string{
	"rails/activesupport.en.yml",
	"rails/activemodel.en.yml",
	"rails/activerecord.en.yml",
	"rails/actionview.en.yml",
}

// Load は fsys（web/locales と同じ構成）から訳文を読み込む。
func Load(fsys fs.FS) (*Bundle, error) {
	b := &Bundle{trees: map[string]map[string]any{}, lookupLC: map[string]string{}}

	var files []string
	for _, f := range railsGemFiles {
		if _, err := fs.Stat(fsys, f); err == nil {
			files = append(files, f)
		}
	}
	glob := func(pattern string) ([]string, error) {
		m, err := fs.Glob(fsys, pattern)
		sort.Strings(m)
		return m, err
	}
	dk, err := glob("rails/doorkeeper.*.yml")
	if err != nil {
		return nil, err
	}
	files = append(files, dk...)
	if _, err := fs.Stat(fsys, "rails/doorkeeper-gem.en.yml"); err == nil {
		files = append(files, "rails/doorkeeper-gem.en.yml")
	}
	rm, err := glob("redmine/*.yml")
	if err != nil {
		return nil, err
	}
	if len(rm) == 0 {
		return nil, fmt.Errorf("i18n: redmine/*.yml が見つかりません")
	}
	files = append(files, rm...)
	ov, err := glob("overlay/*.yml")
	if err != nil {
		return nil, err
	}
	files = append(files, ov...)

	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		tree, err := parseYAML(data)
		if err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", f, err)
		}
		if strings.HasPrefix(f, "redmine/") {
			for _, v := range tree {
				if m, ok := v.(map[string]any); ok {
					rebrand(m, "")
				}
			}
		}
		for loc, v := range tree {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if b.trees[loc] == nil {
				b.trees[loc] = map[string]any{}
			}
			deepMerge(b.trees[loc], m)
		}
	}
	for _, f := range rm {
		loc := strings.TrimSuffix(path.Base(f), ".yml")
		b.available = append(b.available, loc)
		b.lookupLC[strings.ToLower(loc)] = loc
	}
	sort.Strings(b.available)
	return b, nil
}

// rebrand は Redmine 本体の訳文ツリーの文字列値（配列要素を含む）に brand.SubstituteLocale を適用する。
// キーは変えない。prefix はロケールを除いたドット区切りのキー。
func rebrand(m map[string]any, prefix string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if brand.LocaleExcluded(key) {
			continue
		}
		m[k] = rebrandValue(v, key)
	}
}

func rebrandValue(v any, key string) any {
	switch x := v.(type) {
	case string:
		return brand.SubstituteLocale(x)
	case map[string]any:
		rebrand(x, key)
	case []any:
		for i, e := range x {
			x[i] = rebrandValue(e, key)
		}
	}
	return v
}

// RebrandLocaleValue は Redmine 本体の訳文の値 v（キー key）に読み込み時と同じブランド置換を適用した値を返す。
// Redmine から生成したゴールデンデータとの比較に使う。
func RebrandLocaleValue(key string, v any) any {
	if brand.LocaleExcluded(key) {
		return v
	}
	return rebrandValue(v, key)
}

// deepMerge は I18n::Utils.deep_merge! と同じく、両方がハッシュなら再帰し、それ以外は後勝ちで上書きする。
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
			cp := map[string]any{}
			deepMerge(cp, sm)
			dst[k] = cp
			continue
		}
		dst[k] = v
	}
}

// AvailableLocales は利用可能なロケールコード（I18n.available_locales）をソートして返す。
func (b *Bundle) AvailableLocales() []string {
	return append([]string(nil), b.available...)
}

// Fallbacks は I18n.fallbacks[locale] と同じ探索順を返す（例: zh-TW → [zh-TW zh en]）。
func Fallbacks(locale string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(l string) {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	l := locale
	for {
		add(l)
		i := strings.LastIndex(l, "-")
		if i < 0 {
			break
		}
		l = l[:i]
	}
	add("en")
	return out
}

// normalizeKeys は I18n.normalize_keys と同じく "." で分割し空要素を除く。
func normalizeKeys(key string) []string {
	parts := strings.Split(key, ".")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// lookup は単一ロケールでキーを引く（I18n::Backend::Simple#lookup）。値がシンボルならリンクとして解決する。
func (b *Bundle) lookup(locale string, keys []string, depth int) any {
	tree := b.trees[locale]
	if tree == nil {
		return nil
	}
	var cur any = tree
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := m[k]
		if !ok {
			return nil
		}
		if s, ok := v.(Symbol); ok && depth < 8 {
			v = b.lookup(locale, normalizeKeys(string(s)), depth+1)
		}
		cur = v
	}
	return cur
}

// Lookup はフォールバック込みで生の値（string / []any / map[string]any / 数値 / bool）を返す。
// 見つからなければ nil。
func (b *Bundle) Lookup(locale, key string) any {
	keys := normalizeKeys(key)
	for _, fb := range Fallbacks(locale) {
		if v := b.lookup(fb, keys, 0); v != nil {
			return v
		}
	}
	return nil
}

// Exists はキーがフォールバック込みで存在するかを返す（I18n.exists?）。
func (b *Bundle) Exists(locale, key string) bool { return b.Lookup(locale, key) != nil }

// MissingMessage は I18n::MissingTranslation#message と同じ文言を返す。
func MissingMessage(locale, key string) string {
	return "Translation missing: " + strings.Join(append([]string{locale}, normalizeKeys(key)...), ".")
}

// Translate は I18n.t(key, locale:, **vars) 相当。
// 見つからない場合は MissingMessage と ok=false を返す（Rails 本番での I18n.t の戻り値と同じ文字列）。
func (b *Bundle) Translate(locale, key string, vars Vars) (any, bool) {
	return b.translate(locale, key, vars, nil)
}

// TranslateDefault は default: に文字列を与えた I18n.t（全フォールバックを試した後に default を使う）。
func (b *Bundle) TranslateDefault(locale, key string, vars Vars, def string) any {
	v, _ := b.translate(locale, key, vars, &def)
	return v
}

func (b *Bundle) translate(locale, key string, vars Vars, def *string) (any, bool) {
	keys := normalizeKeys(key)
	count, hasCount := vars["count"]
	if hasCount && count == nil {
		hasCount = false
	}
	if len(keys) > 0 {
		for _, fb := range Fallbacks(locale) {
			entry := b.lookup(fb, keys, 0)
			if entry == nil {
				continue
			}
			if hasCount {
				var err error
				entry, err = pluralize(entry, count)
				if err != nil {
					return err.Error(), false
				}
				if entry == nil {
					continue
				}
			}
			return interpolateEntry(entry, vars), true
		}
	}
	if def != nil {
		return interpolateEntry(*def, vars), true
	}
	return MissingMessage(locale, key), false
}

// T は Translate の結果を文字列化して返す（ビューで <%= l(...) %> したときの表示）。
func (b *Bundle) T(locale, key string, vars Vars) string {
	v, _ := b.Translate(locale, key, vars)
	return RubyToS(v)
}

type pluralizationError struct {
	entry any
	count any
	key   string
}

func (e *pluralizationError) Error() string {
	return fmt.Sprintf("translation data %s can not be used with :count => %s. key '%s' is missing.",
		rubyInspect(e.entry), RubyToS(e.count), e.key)
}

// pluralize は I18n::Backend::Base#pluralize（Redmine はロケール別ルールを定義していないため既定規則）。
// count==0 かつ zero があれば zero、count==1 なら one、それ以外は other。
func pluralize(entry, count any) (any, error) {
	m, ok := entry.(map[string]any)
	if !ok {
		return entry, nil
	}
	var key string
	if numEq(count, 0) {
		if _, ok := m["zero"]; ok {
			key = "zero"
		}
	}
	if key == "" {
		if numEq(count, 1) {
			key = "one"
		} else {
			key = "other"
		}
	}
	v, ok := m[key]
	if !ok {
		return nil, &pluralizationError{entry, count, key}
	}
	return v, nil
}

func numEq(v any, n int) bool {
	switch x := v.(type) {
	case int:
		return x == n
	case int64:
		return x == int64(n)
	case int32:
		return x == int32(n)
	case float64:
		return x == float64(n)
	case float32:
		return x == float32(n)
	case uint:
		return n >= 0 && x == uint(n)
	case uint64:
		return n >= 0 && x == uint64(n)
	}
	return false
}

var interpolationRe = regexp.MustCompile(`%%|%\{([\w|]+)\}|%<(\w+)>([^\d]*?\d*\.?\d*[bBdiouxXeEfgGcps])`)

// interpolateEntry は Base#interpolate 相当。文字列は補間、配列は要素ごとに再帰、その他はそのまま。
func interpolateEntry(entry any, vars Vars) any {
	if len(vars) == 0 {
		return entry
	}
	switch e := entry.(type) {
	case string:
		return Interpolate(e, vars)
	case []any:
		out := make([]any, len(e))
		for i, x := range e {
			out[i] = interpolateEntry(x, vars)
		}
		return out
	}
	return entry
}

// Interpolate は I18n.interpolate 相当（%{name}、%<name>d 形式、%% → %）。
// 未定義の変数は Ruby では例外になるが、ここではプレースホルダをそのまま残す。
func Interpolate(s string, vars Vars) string {
	if len(vars) == 0 || !strings.Contains(s, "%") {
		return s
	}
	return interpolationRe.ReplaceAllStringFunc(s, func(m string) string {
		if m == "%%" {
			return "%"
		}
		sub := interpolationRe.FindStringSubmatch(m)
		name := sub[1]
		if name == "" {
			name = sub[2]
		}
		v, ok := vars[name]
		if !ok {
			return m
		}
		if sub[3] != "" {
			return rubySprintf("%"+sub[3], v)
		}
		return RubyToS(v)
	})
}

// rubySprintf は %<name>fmt 形式の書式を Go の fmt で近似する。
func rubySprintf(f string, v any) string {
	conv := f[len(f)-1]
	head := f[:len(f)-1]
	switch conv {
	case 'i', 'u':
		f = head + "d"
	case 's', 'p':
		return fmt.Sprintf(head+"s", RubyToS(v))
	}
	switch conv {
	case 'd', 'i', 'u', 'x', 'X', 'o', 'b', 'B', 'c':
		switch x := v.(type) {
		case float64:
			return fmt.Sprintf(f, int64(x))
		}
	case 'f', 'e', 'E', 'g', 'G':
		switch x := v.(type) {
		case int:
			return fmt.Sprintf(f, float64(x))
		case int64:
			return fmt.Sprintf(f, float64(x))
		}
	}
	return fmt.Sprintf(f, v)
}
