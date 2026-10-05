// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package settings は Redmine の Setting モデル（app/models/setting.rb）の移植。
//
// 設定項目の定義は Redmine の config/settings.yml を埋め込む（製品名を含む既定値 app_title / welcome_text のみ
// "Buropher" に変更している。Redmine から移行したインストールは保存済みの値を使う）。
// 値の表現は Redmine に合わせ、非シリアライズ項目は常に文字列（Ruby の value.to_s）、
// serialized 項目は配列/ハッシュ（[]any / map[string]any）として扱う。
// 永続化は Store インタフェース経由（DB では settings.value に JSON で保存）。
package settings

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed settings.yml
var settingsYAML []byte

// Definition は settings.yml の 1 項目。
type Definition struct {
	Name                  string
	Default               any // 非シリアライズなら文字列化済み
	Serialized            bool
	Format                string // "", "int", "symbol"
	SecurityNotifications bool
}

var (
	defs  map[string]*Definition
	names []string
)

func init() {
	var raw map[string]map[string]any
	if err := yaml.Unmarshal(settingsYAML, &raw); err != nil {
		panic(fmt.Sprintf("settings: invalid settings.yml: %v", err))
	}
	defs = make(map[string]*Definition, len(raw))
	for name, opts := range raw {
		d := &Definition{Name: name}
		d.Serialized = truthy(opts["serialized"])
		d.SecurityNotifications = truthy(opts["security_notifications"])
		if f, ok := opts["format"].(string); ok {
			d.Format = f
		}
		if d.Serialized {
			d.Default = normalize(opts["default"])
		} else {
			d.Default = rubyToS(opts["default"])
		}
		defs[name] = d
		names = append(names, name)
	}
	sort.Strings(names)
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case string:
		return x != "" && x != "0"
	}
	return false
}

// rubyToS は YAML の既定値を Ruby の to_s と同じ文字列にする。
// シンボル（":firstname_lastname"）は to_s でコロンが外れる。
func rubyToS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if strings.HasPrefix(x, ":") && len(x) > 1 {
			return x[1:]
		}
		return x
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// normalize は YAML 由来の値を JSON 互換の型（map[string]any, []any, string, ...）に揃える。
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = normalize(e)
		}
		return m
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[fmt.Sprint(k)] = normalize(e)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = normalize(e)
		}
		return a
	case []string:
		// Go から []string で設定された配列も []any に揃える (Strings が読めるように)
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = e
		}
		return a
	case nil:
		return nil
	}
	return v
}

// Lookup は定義を返す。未定義なら nil。
func Lookup(name string) *Definition { return defs[name] }

// Names は全設定名をソート済みで返す。
func Names() []string { return names }

// Store は設定値の永続化先。値は JSON（非シリアライズ項目は JSON 文字列）。
type Store interface {
	LoadAll(ctx context.Context) (map[string]json.RawMessage, error)
	Save(ctx context.Context, name string, value json.RawMessage) error
}

// Versioner は設定の版を返せる Store（任意）。版が変わったら他のプロセスが設定を変更したとみなす。
type Versioner interface {
	Version(ctx context.Context) (string, error)
}

// Settings はキャッシュ付きの設定アクセサ（Setting[] / Setting.xxx 相当）。
type Settings struct {
	store Store
	mu    sync.RWMutex
	cache map[string]any
	// version は最後に読み込んだときの Store の版（Versioner の場合）。
	version string
}

// New は store から全設定を読み込む。
func New(ctx context.Context, store Store) (*Settings, error) {
	s := &Settings{store: store}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload はキャッシュを破棄して再読み込みする（Setting.clear_cache 相当）。
func (s *Settings) Reload(ctx context.Context) error {
	// 版は読み込みの前に取る（間に変更が入っても次の CheckCache で読み直す）
	var version string
	if v, ok := s.store.(Versioner); ok {
		var err error
		if version, err = v.Version(ctx); err != nil {
			return err
		}
	}
	raw, err := s.store.LoadAll(ctx)
	if err != nil {
		return err
	}
	cache := make(map[string]any, len(raw))
	for name, js := range raw {
		d := defs[name]
		if d == nil {
			continue // 未知の設定（プラグイン由来など）は無視
		}
		var v any
		if err := json.Unmarshal(js, &v); err != nil {
			return fmt.Errorf("settings: %s: %w", name, err)
		}
		if !d.Serialized {
			v = rubyToS(v)
		}
		cache[name] = v
	}
	s.mu.Lock()
	s.cache = cache
	s.version = version
	s.mu.Unlock()
	return nil
}

// CheckCache は Setting.check_cache: Store の版が最後に読み込んだときから変わっていれば
// キャッシュを読み直す。リクエストごとに呼ぶ（ApplicationController#user_setup と同じ）。
// 複数のプロセスが同じ DB を使う場合に、別のプロセスでの設定変更（REST API の無効化・
// ログイン必須・自己登録の停止など）が、そのプロセスを再起動するまで反映されないことを防ぐ。
func (s *Settings) CheckCache(ctx context.Context) error {
	v, ok := s.store.(Versioner)
	if !ok {
		return nil
	}
	version, err := v.Version(ctx)
	if err != nil {
		return err
	}
	s.mu.RLock()
	same := version == s.version
	s.mu.RUnlock()
	if same {
		return nil
	}
	return s.Reload(ctx)
}

// Get は設定値を返す（Setting[name]）。未定義の名前は panic（Redmine も例外）。
func (s *Settings) Get(name string) any {
	d := defs[name]
	if d == nil {
		panic("settings: there's no setting named " + name)
	}
	s.mu.RLock()
	v, ok := s.cache[name]
	s.mu.RUnlock()
	if ok {
		return v
	}
	return d.Default
}

// String は非シリアライズ項目の値を返す。
func (s *Settings) String(name string) string {
	if v, ok := s.Get(name).(string); ok {
		return v
	}
	return ""
}

// Int は Ruby の String#to_i で解釈した値を返す。
func (s *Settings) Int(name string) int { return RubyToI(s.String(name)) }

// Bool は Setting.xxx? と同じく to_i > 0 を返す。
func (s *Settings) Bool(name string) bool { return s.Int(name) > 0 }

// Strings は serialized 配列項目を文字列スライスで返す。
func (s *Settings) Strings(name string) []string {
	a, _ := s.Get(name).([]any)
	out := make([]string, 0, len(a))
	for _, e := range a {
		out = append(out, rubyToS(e))
	}
	return out
}

// Value は serialized 項目の生の値を返す。
func (s *Settings) Value(name string) any { return s.Get(name) }

// Set は値を保存してキャッシュを更新する（Setting[name] = v）。
// 非シリアライズ項目は文字列化して保存する。nil は "" として扱う。
func (s *Settings) Set(ctx context.Context, name string, v any) error {
	d := defs[name]
	if d == nil {
		return fmt.Errorf("settings: there's no setting named %s", name)
	}
	if v == nil {
		v = ""
	}
	if d.Serialized {
		v = normalize(v)
	} else {
		v = rubyToS(v)
		if d.Format == "int" {
			// validates_numericality_of :value, :only_integer => true（/\A[+-]?\d+\z/）
			if !reInteger.MatchString(v.(string)) {
				return &ValidationError{Name: name, Message: "is not a number"}
			}
		}
	}
	js, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.store.Save(ctx, name, js); err != nil {
		return err
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]any{}
	}
	s.cache[name] = v
	s.mu.Unlock()
	return nil
}

// ValidationError は設定値の検証エラー。Message は i18n キーではなく表示用文字列。
type ValidationError struct {
	Name    string
	Message string
}

func (e *ValidationError) Error() string { return e.Name + " " + e.Message }

// FieldError は SetAllFromParams の検証エラー 1 件（Redmine の [name, message] 組）。
type FieldError struct {
	Name string
	// Key は Redmine の i18n キー（activerecord.errors.messages.*）。
	Key    string
	Detail string
	// Args は i18n の l(key, arg) に渡す補間引数（数値は count）。
	Args []any
}

var (
	reInteger = regexp.MustCompile(`\A[+-]?\d+\z`)
	reLines   = regexp.MustCompile(`[\r\n]+`)
	reComma   = regexp.MustCompile(`\s*,\s*`)
	reEmail   = regexp.MustCompile(`\A[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*\z`)
)

// ValidateAllFromParams は Setting.validate_all_from_params の移植。
// 正規表現の妥当性は Go の regexp で判定する（Ruby との構文差は許容）。
func (s *Settings) ValidateAllFromParams(params map[string]any) []FieldError {
	var errs []FieldError
	checks := []struct {
		enable, field string
		delim         *regexp.Regexp
	}{
		{"mail_handler_enable_regex_delimiters", "mail_handler_body_delimiters", reLines},
		{"mail_handler_enable_regex_excluded_filenames", "mail_handler_excluded_filenames", reComma},
	}
	for _, c := range checks {
		_, hasField := params[c.field]
		ev, hasEnable := params[c.enable]
		if !hasField && !hasEnable {
			continue
		}
		useRegexp := s.Bool(c.enable)
		if hasEnable {
			useRegexp = rubyToS(ev) != "0"
		}
		if !useRegexp {
			continue
		}
		for _, v := range rubySplit(c.delim, rubyToS(params[c.field])) {
			if _, err := regexp.Compile(v); err != nil {
				errs = append(errs, FieldError{Name: c.field, Key: "activerecord.errors.messages.not_a_regexp", Detail: err.Error()})
			}
		}
	}
	if v, ok := params["mail_from"]; ok {
		addr, err := mail.ParseAddress(rubyToS(v))
		if err != nil || !reEmail.MatchString(addr.Address) {
			errs = append(errs, FieldError{Name: "mail_from", Key: "activerecord.errors.messages.invalid"})
		}
	}
	// Redmine 7.0: 新規チケットの期日の既定値（開始日からの日数。空なら設定しない）
	if v, ok := params["default_issue_due_date_offset"]; ok {
		if s := strings.TrimSpace(rubyToS(v)); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err != nil || !reInteger.MatchString(s) {
				errs = append(errs, FieldError{Name: "default_issue_due_date_offset", Key: "activerecord.errors.messages.not_a_number"})
			} else if n < 0 {
				errs = append(errs, FieldError{Name: "default_issue_due_date_offset", Key: "activerecord.errors.messages.greater_than_or_equal_to", Args: []any{0}})
			}
		}
	}
	return errs
}

// rubySplit は Ruby の String#split（末尾の空要素を除去）と同じ分割を行う。
func rubySplit(re *regexp.Regexp, s string) []string {
	parts := re.Split(s, -1)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// SetAllFromParams は Setting.set_all_from_params の移植。
// 検証エラーがあれば何も保存せずにエラーを返す。成功時は security_notifications 対象で
// 値が変化した設定名を返す（呼び出し側が Mailer.deliver_settings_updated 相当を行う）。
// hooks は "<name>_from_params" 相当の変換（twofa の全解除など）を差し込むためのもの。
func (s *Settings) SetAllFromParams(ctx context.Context, params map[string]any, hooks map[string]func(any) any) (changed []string, errs []FieldError, err error) {
	if errs = s.ValidateAllFromParams(params); len(errs) > 0 {
		return nil, errs, nil
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		d := defs[name]
		if d == nil {
			continue
		}
		prev, _ := json.Marshal(s.Get(name))
		v := params[name]
		if a, ok := v.([]any); ok { // 配列は空要素を除く
			b := a[:0:0]
			for _, e := range a {
				if !blank(e) {
					b = append(b, e)
				}
			}
			v = b
		} else if a, ok := v.([]string); ok {
			b := []any{}
			for _, e := range a {
				if strings.TrimSpace(e) != "" {
					b = append(b, e)
				}
			}
			v = b
		}
		switch name {
		case "commit_update_keywords":
			v = CommitUpdateKeywordsFromParams(v)
		case "default_issue_due_date_offset":
			// Setting.default_issue_due_date_offset_from_params
			v = strings.TrimSpace(rubyToS(v))
		}
		if h := hooks[name]; h != nil {
			v = h(v)
		}
		if err := s.Set(ctx, name, v); err != nil {
			// Setting[name]= は検証に失敗すると保存せずに続行する（エラーは返さない）
			var ve *ValidationError
			if errors.As(err, &ve) {
				continue
			}
			return nil, nil, err
		}
		cur, _ := json.Marshal(s.Get(name))
		if d.SecurityNotifications && string(prev) != string(cur) {
			changed = append(changed, name)
		}
	}
	return changed, nil, nil
}

func blank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// CommitUpdateKeywordsFromParams は Setting.commit_update_keywords_from_params の移植。
// params は {"keywords": [...], "status_id": [...], "done_ratio": [...]} 形式。
func CommitUpdateKeywordsFromParams(v any) any {
	params, ok := v.(map[string]any)
	if !ok {
		return []any{}
	}
	kw, ok := params["keywords"].([]any)
	if !ok {
		return []any{}
	}
	attrs := []string{}
	for k, e := range params {
		if k == "keywords" {
			continue
		}
		if _, ok := e.([]any); !ok {
			return []any{}
		}
		attrs = append(attrs, k)
	}
	sort.Strings(attrs)
	out := []any{}
	for i, k := range kw {
		if blank(k) {
			continue
		}
		h := map[string]any{}
		for _, a := range attrs {
			arr := params[a].([]any)
			if i < len(arr) {
				if s := rubyToS(arr[i]); s != "" {
					h[a] = s
				}
			}
		}
		h["keywords"] = k
		out = append(out, h)
	}
	return out
}

// RubyToI は Ruby の String#to_i（先頭の整数部分のみ、なければ 0）を再現する。
func RubyToI(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	end := 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	start := end
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || (s[end] == '_' && end > start && end+1 < len(s) && s[end+1] >= '0' && s[end+1] <= '9')) {
		end++
	}
	if end == start {
		return 0
	}
	n, err := strconv.Atoi(strings.ReplaceAll(s[:end], "_", ""))
	if err != nil {
		return 0
	}
	return n
}
