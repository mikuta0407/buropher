package query

import (
	"strconv"
	"strings"
)

// frag はプレースホルダ (`?`) 付きの SQL 断片と引数。
// Redmine は値を SQL に埋め込むが、buropher では利用者由来の文字列は常に引数で渡す
// (整数 id・固定文字列のみ埋め込む)。
type frag struct {
	SQL  string
	Args []any
}

// raw は引数の無い SQL 断片。
func raw(s string) frag { return frag{SQL: s} }

// sqlf は SQL と引数から断片を作る。
func sqlf(s string, args ...any) frag { return frag{SQL: s, Args: args} }

func (f frag) empty() bool { return strings.TrimSpace(f.SQL) == "" }

// wrap は前後に文字列を付けた断片を返す。
func (f frag) wrap(pre, post string) frag {
	return frag{SQL: pre + f.SQL + post, Args: f.Args}
}

// joinFrags は空でない断片を sep で連結する。
func joinFrags(sep string, fs ...frag) frag {
	var b strings.Builder
	var args []any
	n := 0
	for _, f := range fs {
		if f.empty() {
			continue
		}
		if n > 0 {
			b.WriteString(sep)
		}
		b.WriteString(f.SQL)
		args = append(args, f.Args...)
		n++
	}
	return frag{SQL: b.String(), Args: args}
}

// concat は断片をそのまま連結する。
func concat(fs ...frag) frag {
	var b strings.Builder
	var args []any
	for _, f := range fs {
		b.WriteString(f.SQL)
		args = append(args, f.Args...)
	}
	return frag{SQL: b.String(), Args: args}
}

// inList は値の IN 句用プレースホルダ ("?, ?, ?") と引数を返す。
func inList(values []string) frag {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return frag{SQL: strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", "), Args: args}
}

// idList は整数 id を "1,2,3" にする (埋め込み用)。
func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// idListOrNull は空なら "NULL" (Rails の空配列 IN (?) の展開と同じ)。
func idListOrNull(ids []int64) string {
	if len(ids) == 0 {
		return "NULL"
	}
	return idList(ids)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
