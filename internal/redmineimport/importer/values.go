package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/archive"
	"github.com/mikuta0407/buropher/internal/redmineimport/rubyyaml"
)

// ---------------------------------------------------------------- 基本型

// toInt は整数として解釈できる値を返す。
func toInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return int64(x), true
		}
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return n, true
		}
	case []byte:
		return toInt(string(x))
	}
	return 0, false
}

// toStr は文字列として返す(NULL は "", false)。不正 UTF-8 のバイト列は Latin-1 とみなして変換する。
func toStr(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return x, true
	case []byte:
		if utf8.Valid(x) {
			return string(x), true
		}
		return latin1ToUTF8(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), true
	case bool:
		if x {
			return "1", true
		}
		return "0", true
	}
	return fmt.Sprint(v), true
}

func latin1ToUTF8(b []byte) string {
	rs := make([]rune, len(b))
	for i, c := range b {
		rs[i] = rune(c)
	}
	return string(rs)
}

// toBool は Redmine の真偽値表現(1/0, 't'/'f', 'true'/'false', true/false)を解釈する。
func toBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case int64:
		return x != 0, true
	case float64:
		return x != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "t", "true", "y", "yes", "on":
			return true, true
		case "0", "f", "false", "n", "no", "off", "":
			return false, true
		}
	case []byte:
		return toBool(string(x))
	}
	return false, false
}

// toFloat は浮動小数として返す。
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int64:
		return float64(x), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

// ---------------------------------------------------------------- 日時

var reNaive = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[ T](\d{2}):(\d{2})(?::(\d{2})(?:[.,](\d+))?)?)?\s*(?:Z|UTC|[+-]\d{2}(?::?\d{2})?)?$`)

// tzconv は naive な日時(ソース TZ の壁時計)を UTC に変換する。
type tzconv struct {
	loc *time.Location
	// 曖昧(DST 終了時の重複)・存在しない(DST 開始時の欠落)時刻の件数
	ambiguous, nonexistent int64
	ambSample, nonSample   []string
}

// parseWall は "YYYY-MM-DD[ HH:MM[:SS[.ffffff]]]" を壁時計の各要素(UTC として表現)に分解する。
func parseWall(s string) (time.Time, bool) {
	m := reNaive.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || m[1] == "0000" {
		return time.Time{}, false
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	h, mi, sec, ns := atoi(m[4]), atoi(m[5]), atoi(m[6]), 0
	if f := m[7]; f != "" {
		if len(f) > 9 {
			f = f[:9]
		}
		ns = atoi(f + strings.Repeat("0", 9-len(f)))
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 60 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, ns, time.UTC)
	if t.Day() != d { // 2 月 30 日など
		return time.Time{}, false
	}
	return t, true
}

// toUTC は壁時計 wall(UTC の各要素として表現)をソース TZ で解釈した UTC 時刻を返す。
// 曖昧な時刻は早い方、存在しない時刻は Go の正規化(時計を進める)を採用して記録する。
func (c *tzconv) toUTC(wall time.Time) time.Time {
	if c.loc == time.UTC {
		return wall
	}
	u := wall.Unix()
	var cands []int64
	seen := map[int]bool{}
	for _, probe := range []int64{u - 2*86400, u, u + 2*86400} {
		_, off := time.Unix(probe, 0).In(c.loc).Zone()
		if seen[off] {
			continue
		}
		seen[off] = true
		cand := u - int64(off)
		if _, o2 := time.Unix(cand, 0).In(c.loc).Zone(); o2 == off {
			cands = append(cands, cand)
		}
	}
	ns := int64(wall.Nanosecond())
	switch len(cands) {
	case 0:
		c.nonexistent++
		if len(c.nonSample) < maxSamples {
			c.nonSample = append(c.nonSample, wall.Format("2006-01-02 15:04:05"))
		}
		// 欠落区間の前のオフセットで解釈する(= 時計を進める。Ruby の Time.local と同じ)
		_, before := time.Unix(u-2*86400, 0).In(c.loc).Zone()
		return time.Unix(u-int64(before), ns).UTC()
	case 1:
		return time.Unix(cands[0], ns).UTC()
	}
	best := cands[0]
	for _, x := range cands[1:] {
		if x < best {
			best = x
		}
	}
	c.ambiguous++
	if len(c.ambSample) < maxSamples {
		c.ambSample = append(c.ambSample, wall.Format("2006-01-02 15:04:05"))
	}
	return time.Unix(best, ns).UTC()
}

// ts は datetime 値を DB 形式の UTC 文字列にする。解釈できなければ ok=false。
func (c *tzconv) ts(v any) (string, bool) {
	s, ok := toStr(v)
	if !ok {
		return "", false
	}
	w, ok := parseWall(s)
	if !ok {
		return "", false
	}
	return db.FormatTime(c.toUTC(w)), true
}

// toDate は date 値を "YYYY-MM-DD" にする。
func toDate(v any) (string, bool) {
	s, ok := toStr(v)
	if !ok {
		return "", false
	}
	w, ok := parseWall(s)
	if !ok {
		return "", false
	}
	return w.Format(db.DateLayout), true
}

// ---------------------------------------------------------------- YAML / JSON

// decodeYAML は Redmine の YAML 列をデコードする(Hash は挿入順を保持)。
func decodeYAML(v any) (any, error) {
	s, ok := toStr(v)
	if !ok || strings.TrimSpace(s) == "" {
		return nil, nil
	}
	return rubyyaml.DecodeWith(s, rubyyaml.Options{Ordered: true})
}

// asMap はデコード結果を OrderedMap として返す。
func asMap(v any) (rubyyaml.OrderedMap, bool) {
	switch x := v.(type) {
	case rubyyaml.OrderedMap:
		return x, true
	case map[string]any:
		m := make(rubyyaml.OrderedMap, 0, len(x))
		for k, e := range x {
			m = append(m, rubyyaml.MapItem{Key: k, Value: e})
		}
		return m, true
	}
	return nil, false
}

// jsonable は JSON 化できる値に変換する(OrderedMap は順序を保つ)。
func jsonable(v any) any {
	switch x := v.(type) {
	case rubyyaml.OrderedMap:
		out := make(rubyyaml.OrderedMap, len(x))
		for i, it := range x {
			out[i] = rubyyaml.MapItem{Key: it.Key, Value: jsonable(it.Value)}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonable(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = jsonable(e)
		}
		return out
	}
	return rubyyaml.Plain(v)
}

// toJSON は値を JSON 文字列にする。
func toJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(jsonable(v)); err != nil {
		// jsonable 済みの値では起きない
		panic(fmt.Sprintf("importer: json: %v", err))
	}
	return strings.TrimRight(buf.String(), "\n")
}

// stringList は YAML 配列を文字列スライスにする(nil/空要素の扱いは呼び出し側)。
func stringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		if v == nil {
			return nil
		}
		s, _ := toStr(rubyyaml.Plain(v))
		return []string{s}
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if e == nil {
			out = append(out, "")
			continue
		}
		s, _ := toStr(rubyyaml.Plain(e))
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------- 行アクセス

// rec はアーカイブの 1 行に型付きアクセスするラッパ。
type rec struct {
	archive.Row
}

func (r rec) id() int64 { n, _ := toInt(r.Row["id"]); return n }

// int は整数列(NULL/解釈不能なら ok=false)。
func (r rec) int(col string) (int64, bool) {
	v := r.Row[col]
	if v == nil {
		return 0, false
	}
	return toInt(v)
}

// intOr は整数列(NULL なら def)。
func (r rec) intOr(col string, def int64) int64 {
	if n, ok := r.int(col); ok {
		return n
	}
	return def
}

// ref は参照列(NULL・0・解釈不能なら 0)。
func (r rec) ref(col string) int64 {
	n, ok := r.int(col)
	if !ok || n <= 0 {
		return 0
	}
	return n
}

func (r rec) str(col string) string { s, _ := toStr(r.Row[col]); return s }

// strNull は文字列列。NULL は nil、emptyNull なら ” も nil。
func (r rec) strNull(col string, emptyNull bool) any {
	s, ok := toStr(r.Row[col])
	if !ok || (emptyNull && s == "") {
		return nil
	}
	return s
}

func (r rec) bool(col string, def bool) bool {
	v := r.Row[col]
	if v == nil {
		return def
	}
	b, ok := toBool(v)
	if !ok {
		return def
	}
	return b
}

func (r rec) isNull(col string) bool { return r.Row[col] == nil }

// nullIfZero は 0 を NULL にして返す。
func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// ptrStr は文字列ポインタ用の小ヘルパ。
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
