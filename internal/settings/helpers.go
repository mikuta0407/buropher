// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package settings

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// DateFormats は Setting::DATE_FORMATS。
var DateFormats = []string{
	"%Y-%m-%d", "%d/%m/%Y", "%d.%m.%Y", "%d-%m-%Y", "%m/%d/%Y",
	"%d %b %Y", "%d %B %Y", "%b %d, %Y", "%B %d, %Y",
}

// TimeFormats は Setting::TIME_FORMATS。
var TimeFormats = []string{"%H:%M", "%I:%M %p"}

// Encodings は Setting::ENCODINGS（リポジトリ・CSV の文字コード候補）。
var Encodings = []string{
	"US-ASCII", "windows-1250", "windows-1251", "windows-1252", "windows-1253", "windows-1254",
	"windows-1255", "windows-1256", "windows-1257", "windows-1258", "windows-31j", "windows-874",
	"ISO-2022-JP", "ISO-8859-1", "ISO-8859-2", "ISO-8859-3", "ISO-8859-4", "ISO-8859-5",
	"ISO-8859-6", "ISO-8859-7", "ISO-8859-8", "ISO-8859-9", "ISO-8859-13", "ISO-8859-15",
	"KOI8-R", "UTF-8", "UTF-16", "UTF-16BE", "UTF-16LE", "EUC-JP", "Shift_JIS", "CP932",
	"CP949", "GB18030", "GBK", "EUC-KR", "Big5", "Big5-HKSCS", "TIS-620",
}

// PasswordCharClasses は Setting::PASSWORD_CHAR_CLASSES（キーの順序も Redmine と同じ）。
var PasswordCharClasses = []struct {
	Name string
	Re   *regexp.Regexp
}{
	{"uppercase", regexp.MustCompile(`[A-Z]`)},
	{"lowercase", regexp.MustCompile(`[a-z]`)},
	{"digits", regexp.MustCompile(`[0-9]`)},
	// [[:ascii:]&&[:graph:]&&[:^alnum:]] = ASCII の可視記号
	{"special_chars", regexp.MustCompile(`[!-/:-@\[-` + "`" + `{-~]`)},
}

var rePerPageSplit = regexp.MustCompile(`[\s,]`)

// PerPageOptionsArray は Setting.per_page_options_array。
func (s *Settings) PerPageOptionsArray() []int {
	var out []int
	for _, p := range rePerPageSplit.Split(s.String("per_page_options"), -1) {
		if n := RubyToI(p); n > 0 {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// TwofaRequired は Setting.twofa_required?。
func (s *Settings) TwofaRequired() bool { return s.String("twofa") == "2" }

// TwofaOptional は Setting.twofa_optional?。
func (s *Settings) TwofaOptional() bool { t := s.String("twofa"); return t == "1" || t == "3" }

// TwofaRequiredForAdministrators は Setting.twofa_required_for_administrators?。
func (s *Settings) TwofaRequiredForAdministrators() bool { return s.String("twofa") == "3" }

// CommitUpdateKeywordsArray は Setting.commit_update_keywords_array。
func (s *Settings) CommitUpdateKeywordsArray() []map[string]any {
	var out []map[string]any
	rules, _ := s.Get("commit_update_keywords").([]any)
	for _, r := range rules {
		rule, ok := r.(map[string]any)
		if !ok {
			continue
		}
		m := map[string]any{}
		for k, v := range rule {
			if !blank(v) {
				m[k] = v
			}
		}
		var kws []string
		for _, k := range strings.Split(strings.ToLower(rubyToS(m["keywords"])), ",") {
			if k = strings.TrimSpace(k); k != "" {
				kws = append(kws, k)
			}
		}
		if len(kws) == 0 {
			continue
		}
		m["keywords"] = kws
		out = append(out, m)
	}
	return out
}

// MemoryStore はテスト・初期化用のメモリ上の Store。
type MemoryStore struct {
	mu     sync.Mutex
	Values map[string]json.RawMessage
}

func (m *MemoryStore) LoadAll(context.Context) (map[string]json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]json.RawMessage, len(m.Values))
	for k, v := range m.Values {
		out[k] = v
	}
	return out, nil
}

func (m *MemoryStore) Save(_ context.Context, name string, v json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Values == nil {
		m.Values = map[string]json.RawMessage{}
	}
	m.Values[name] = v
	return nil
}

// Itoa は設定に整数を保存する際の補助。
func Itoa(n int) string { return strconv.Itoa(n) }
