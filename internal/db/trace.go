package db

import (
	"cmp"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SQL の実行統計 (性能調査用)。EnableQueryStats で有効にしたときだけ記録する。
// 無効時のコストは各クエリでの atomic.Bool の読み出し 1 回のみ。
//
// クエリ文は数値リテラルと IN リストを正規化した形で集計するので、
// N+1 (同じ形のクエリが大量に呼ばれる) と遅いクエリの両方を見つけられる。

var (
	statsEnabled atomic.Bool
	statsMu      sync.Mutex
	stats        = map[string]*QueryStat{}
)

// QueryStat は正規化したクエリ文ごとの実行回数と合計時間。
type QueryStat struct {
	Query string
	Count int64
	Total time.Duration
	Max   time.Duration
}

// EnableQueryStats は SQL の実行統計の記録を有効 / 無効にする。
func EnableQueryStats(on bool) { statsEnabled.Store(on) }

// ResetQueryStats は記録した統計を消す。
func ResetQueryStats() {
	statsMu.Lock()
	stats = map[string]*QueryStat{}
	statsMu.Unlock()
}

var (
	reNumber  = regexp.MustCompile(`\b\d+\b`)
	reNumList = regexp.MustCompile(`N(?:\s*,\s*N)+`)
	reArgList = regexp.MustCompile(`\?(?:\s*,\s*\?)+`)
	reSpaces  = regexp.MustCompile(`\s+`)
)

func normalizeQuery(q string) string {
	q = reSpaces.ReplaceAllString(q, " ")
	q = reNumber.ReplaceAllString(q, "N")
	q = reNumList.ReplaceAllString(q, "N,…")
	q = reArgList.ReplaceAllString(q, "?,…")
	return strings.TrimSpace(q)
}

// traceQuery は start からの経過時間を q の統計に加える。
func traceQuery(start time.Time, q string) {
	d := time.Since(start)
	key := normalizeQuery(q)
	statsMu.Lock()
	s := stats[key]
	if s == nil {
		s = &QueryStat{Query: key}
		stats[key] = s
	}
	s.Count++
	s.Total += d
	s.Max = max(s.Max, d)
	statsMu.Unlock()
}

// QueryStats は記録した統計を合計時間の降順で返す。
func QueryStats() []QueryStat {
	statsMu.Lock()
	out := make([]QueryStat, 0, len(stats))
	for _, s := range stats {
		out = append(out, *s)
	}
	statsMu.Unlock()
	slices.SortFunc(out, func(a, b QueryStat) int { return cmp.Compare(b.Total, a.Total) })
	return out
}

// WriteQueryStats は統計を上位 limit 件 (0 以下なら全件) だけテキストで書き出す。
// full が偽ならクエリ文を 600 バイトで切り詰める。
func WriteQueryStats(w io.Writer, limit int, full bool) {
	all := QueryStats()
	var total time.Duration
	var count int64
	for _, s := range all {
		total += s.Total
		count += s.Count
	}
	fmt.Fprintf(w, "queries: %d  total: %s  distinct: %d\n\n", count, total.Round(time.Microsecond), len(all))
	for i, s := range all {
		if limit > 0 && i >= limit {
			break
		}
		q := s.Query
		if !full && len(q) > 600 {
			q = q[:600] + "…"
		}
		fmt.Fprintf(w, "%8d  total %12s  avg %10s  max %10s\n    %s\n", s.Count, s.Total.Round(time.Microsecond),
			(s.Total / time.Duration(s.Count)).Round(time.Microsecond), s.Max.Round(time.Microsecond), q)
	}
}
