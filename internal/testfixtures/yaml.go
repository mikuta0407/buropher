package testfixtures

import (
	"embed"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// testdata/redmine/*.yml は Redmine 6.1.2 の test/fixtures/*.yml の無改変コピー (GPL-2.0-or-later)。
//
//go:embed testdata/redmine/*.yml
var fixtureFS embed.FS

// row は 1 フィクスチャ行。値は YAML スカラーの文字列 (NULL は存在しないキーと同じく nil)。
type row struct {
	label string
	cols  map[string]*string
	// lists はシーケンス値の列 (custom_fields.possible_values 等)。!binary は復号済み。
	lists map[string][]string
}

// readFixture は ERB を評価した上で YAML を読み、id (無ければラベル) 順の行を返す。
func readFixture(name string, now time.Time) ([]row, error) {
	b, err := fixtureFS.ReadFile("testdata/redmine/" + name + ".yml")
	if err != nil {
		return nil, fmt.Errorf("testfixtures: unknown fixture %q: %w", name, err)
	}
	src, err := evalERB(string(b), now)
	if err != nil {
		return nil, fmt.Errorf("testfixtures: %s: %w", name, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, fmt.Errorf("testfixtures: %s: %w", name, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("testfixtures: %s: top level is not a mapping", name)
	}
	var rows []row
	for i := 0; i+1 < len(top.Content); i += 2 {
		label := top.Content[i].Value
		m := top.Content[i+1]
		r := row{label: label, cols: map[string]*string{}, lists: map[string][]string{}}
		for j := 0; j+1 < len(m.Content); j += 2 {
			k, v := m.Content[j].Value, m.Content[j+1]
			if v.Kind == yaml.SequenceNode {
				var items []string
				for _, it := range v.Content {
					if it.Kind != yaml.ScalarNode {
						return nil, fmt.Errorf("testfixtures: %s.%s.%s: nested sequence", name, label, k)
					}
					val := it.Value
					if it.Tag == "!binary" || it.Tag == "!!binary" {
						b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(val), ""))
						if err != nil {
							return nil, fmt.Errorf("testfixtures: %s.%s.%s: %w", name, label, k, err)
						}
						val = string(b)
					}
					items = append(items, val)
				}
				r.lists[k] = items
				continue
			}
			if v.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("testfixtures: %s.%s.%s: non-scalar value", name, label, k)
			}
			if v.ShortTag() == "!!null" {
				r.cols[k] = nil
				continue
			}
			s := v.Value
			r.cols[k] = &s
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, aok := rows[i].id()
		b, bok := rows[j].id()
		if aok && bok && a != b {
			return a < b
		}
		return rows[i].label < rows[j].label
	})
	return rows, nil
}

func (r row) id() (int64, bool) {
	v := r.cols["id"]
	if v == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(*v, 10, 64)
	return n, err == nil
}

func (r row) has(k string) bool { return r.cols[k] != nil }

func (r row) str(k string) string {
	if v := r.cols[k]; v != nil {
		return *v
	}
	return ""
}

// nstr は値が無いか空文字なら nil (”→NULL 変換)。
func (r row) nstr(k string) any {
	if v := r.cols[k]; v != nil && *v != "" {
		return *v
	}
	return nil
}

func (r row) int(k string, def int64) int64 {
	v := r.cols[k]
	if v == nil || *v == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(*v), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("testfixtures: %s.%s: %q is not an integer", r.label, k, *v))
	}
	return n
}

// nint は値が無ければ nil。
func (r row) nint(k string) any {
	v := r.cols[k]
	if v == nil || *v == "" {
		return nil
	}
	return r.int(k, 0)
}

func (r row) bool(k string, def bool) bool {
	v := r.cols[k]
	if v == nil || *v == "" {
		return def
	}
	switch strings.ToLower(*v) {
	case "true", "1", "t", "yes":
		return true
	case "false", "0", "f", "no":
		return false
	}
	panic(fmt.Sprintf("testfixtures: %s.%s: %q is not a boolean", r.label, k, *v))
}

func (r row) float(k string) any {
	v := r.cols[k]
	if v == nil || *v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(*v, 64)
	if err != nil {
		panic(fmt.Sprintf("testfixtures: %s.%s: %q is not a number", r.label, k, *v))
	}
	return f
}

var timeLayouts = []string{
	"2006-01-02 15:04:05 -07:00",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}

// time は日時。無ければ def。
func (r row) time(k string, def time.Time) time.Time {
	v := r.cols[k]
	if v == nil || *v == "" {
		return def
	}
	t, err := parseTime(*v)
	if err != nil {
		panic(fmt.Sprintf("testfixtures: %s.%s: %v", r.label, k, err))
	}
	return t
}

// date は日付文字列 (YYYY-MM-DD)。無ければ nil。
func (r row) date(k string) any {
	v := r.cols[k]
	if v == nil || *v == "" {
		return nil
	}
	t, err := parseTime(*v)
	if err != nil {
		panic(fmt.Sprintf("testfixtures: %s.%s: %v", r.label, k, err))
	}
	return t.Format("2006-01-02")
}

var (
	erbRe      = regexp.MustCompile(`<%=\s*(.*?)\s*%>`)
	durationRe = regexp.MustCompile(`^(\d+)\.(minutes?|hours?|days?|weeks?|months?|years?)\.(ago|from_now)(\.to_date)?\.to_fs\(:db\)$`)
)

// evalERB はフィクスチャに含まれる単純な ERB 式 (相対日時など) を評価する。
// 未知の式はエラー。
func evalERB(src string, now time.Time) (string, error) {
	var firstErr error
	out := erbRe.ReplaceAllStringFunc(src, func(m string) string {
		expr := erbRe.FindStringSubmatch(m)[1]
		v, err := evalExpr(expr, now)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return v
	})
	return out, firstErr
}

func evalExpr(expr string, now time.Time) (string, error) {
	const dbTime, dbDate = "2006-01-02 15:04:05", "2006-01-02"
	switch expr {
	case "Date.today.to_fs(:db)":
		return now.Format(dbDate), nil
	case "Time.now.to_fs(:db)":
		return now.Format(dbTime), nil
	case "Rails.root":
		return "/redmine", nil
	case "$redmine_test_ldap_server":
		return "127.0.0.1", nil
	}
	m := durationRe.FindStringSubmatch(expr)
	if m == nil {
		return "", fmt.Errorf("unsupported ERB expression %q", expr)
	}
	n, _ := strconv.Atoi(m[1])
	if m[3] == "ago" {
		n = -n
	}
	t := now
	switch strings.TrimSuffix(m[2], "s") {
	case "minute":
		t = t.Add(time.Duration(n) * time.Minute)
	case "hour":
		t = t.Add(time.Duration(n) * time.Hour)
	case "day":
		t = t.AddDate(0, 0, n)
	case "week":
		t = t.AddDate(0, 0, 7*n)
	case "month":
		t = t.AddDate(0, n, 0)
	case "year":
		t = t.AddDate(n, 0, 0)
	}
	if m[4] != "" {
		return t.Format(dbDate), nil
	}
	return t.Format(dbTime), nil
}
