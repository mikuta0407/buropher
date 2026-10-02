package importer

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// maxSamples はレポートに残すサンプル ID の最大数。
const maxSamples = 10

// Report はインポート結果。
type Report struct {
	Archive        string    `json:"archive"`
	SourceTimezone string    `json:"source_timezone"`
	SourceDBKind   string    `json:"source_db_kind"`
	Dialect        string    `json:"target_dialect"`
	DryRun         bool      `json:"dry_run"`
	Committed      bool      `json:"committed"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	// Tables はソーステーブル別の結果(処理順)。
	Tables []*TableReport `json:"tables"`
	// Files は添付ファイルのコピー結果。
	Files FileReport `json:"files"`
	// Checks は事後の整合性チェック結果。
	Checks []Check `json:"checks"`
	// Warnings はテーブルに属さない警告。
	Warnings []string `json:"warnings"`

	byName map[string]*TableReport
}

// TableReport は 1 ソーステーブルの結果。
type TableReport struct {
	// Source は Redmine 側のテーブル名(派生処理は "(post)" 等)。
	Source string `json:"source"`
	// SourceRows はアーカイブ内の行数。
	SourceRows int64 `json:"source_rows"`
	// Imported は書き込み先テーブル別の挿入行数。
	Imported map[string]int64 `json:"imported"`
	// Dropped は取り込まなかった行(理由別)。
	Dropped []*Issue `json:"dropped,omitempty"`
	// Repaired は値を補完・修正して取り込んだ行(理由別)。
	Repaired []*Issue `json:"repaired,omitempty"`
}

// Issue は理由ごとの件数とサンプル ID。
type Issue struct {
	Reason  string   `json:"reason"`
	Count   int64    `json:"count"`
	Samples []string `json:"samples,omitempty"`
}

// FileReport は添付ファイルの結果。
type FileReport struct {
	// Source は "archive" / "dir" / "none"。
	Source  string   `json:"source"`
	Copied  int64    `json:"copied"`
	Bytes   int64    `json:"bytes"`
	Skipped int64    `json:"skipped_existing"`
	Missing int64    `json:"missing"`
	Samples []string `json:"missing_samples,omitempty"`
}

// Check は整合性チェック 1 件の結果。
type Check struct {
	Name   string   `json:"name"`
	OK     bool     `json:"ok"`
	Detail []string `json:"detail,omitempty"`
}

func newReport() *Report {
	return &Report{byName: map[string]*TableReport{}, Tables: []*TableReport{}, Checks: []Check{}, Warnings: []string{}}
}

// Table はソーステーブル名の TableReport を返す(なければ作る)。
func (r *Report) Table(name string) *TableReport {
	if t, ok := r.byName[name]; ok {
		return t
	}
	t := &TableReport{Source: name, Imported: map[string]int64{}}
	r.byName[name] = t
	r.Tables = append(r.Tables, t)
	return t
}

// Lookup はソーステーブル名の TableReport を返す(なければ nil)。
func (r *Report) Lookup(name string) *TableReport {
	if r.byName == nil {
		r.byName = map[string]*TableReport{}
		for _, t := range r.Tables {
			r.byName[t.Source] = t
		}
	}
	return r.byName[name]
}

func (r *Report) warnf(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

func addIssue(list []*Issue, reason string, id any) []*Issue {
	for _, is := range list {
		if is.Reason == reason {
			is.Count++
			if len(is.Samples) < maxSamples {
				is.Samples = append(is.Samples, fmt.Sprint(id))
			}
			return list
		}
	}
	return append(list, &Issue{Reason: reason, Count: 1, Samples: []string{fmt.Sprint(id)}})
}

func (t *TableReport) drop(id any, reason string, args ...any) {
	if len(args) > 0 {
		reason = fmt.Sprintf(reason, args...)
	}
	t.Dropped = addIssue(t.Dropped, reason, id)
}

func (t *TableReport) repair(id any, reason string, args ...any) {
	if len(args) > 0 {
		reason = fmt.Sprintf(reason, args...)
	}
	t.Repaired = addIssue(t.Repaired, reason, id)
}

// DroppedCount は破棄行数の合計。
func (t *TableReport) DroppedCount() int64 {
	var n int64
	for _, is := range t.Dropped {
		n += is.Count
	}
	return n
}

// RepairedCount は補完件数の合計(1 行に複数の補完があれば重複して数える)。
func (t *TableReport) RepairedCount() int64 {
	var n int64
	for _, is := range t.Repaired {
		n += is.Count
	}
	return n
}

// Find は理由に部分一致する破棄/補完を探す(テスト・検証用)。
func (t *TableReport) Find(substr string) *Issue {
	for _, is := range append(append([]*Issue{}, t.Dropped...), t.Repaired...) {
		if strings.Contains(is.Reason, substr) {
			return is
		}
	}
	return nil
}

// OK は全チェックが成功したか。
func (r *Report) OK() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// WriteJSON はレポートを JSON で書き出す。
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText は人が読む形式でレポートを書き出す。
func (r *Report) WriteText(w io.Writer) {
	mode := "committed"
	switch {
	case r.DryRun:
		mode = "dry-run (rolled back)"
	case !r.Committed:
		mode = "NOT committed"
	}
	fmt.Fprintf(w, "Redmine import report: %s\n", r.Archive)
	fmt.Fprintf(w, "  source: %s (timezone %s) -> target: %s, %s, %s\n",
		r.SourceDBKind, r.SourceTimezone, r.Dialect, mode, r.FinishedAt.Sub(r.StartedAt).Round(time.Millisecond))
	fmt.Fprintf(w, "\n%-28s %8s %8s %8s  %s\n", "source table", "rows", "dropped", "repaired", "imported")
	for _, t := range r.Tables {
		var imp []string
		keys := make([]string, 0, len(t.Imported))
		for k := range t.Imported {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			imp = append(imp, fmt.Sprintf("%s=%d", k, t.Imported[k]))
		}
		fmt.Fprintf(w, "%-28s %8d %8d %8d  %s\n", t.Source, t.SourceRows, t.DroppedCount(), t.RepairedCount(), strings.Join(imp, " "))
	}
	for _, t := range r.Tables {
		if len(t.Dropped) == 0 && len(t.Repaired) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n[%s]\n", t.Source)
		for _, is := range t.Dropped {
			fmt.Fprintf(w, "  dropped  %5d  %s (e.g. %s)\n", is.Count, is.Reason, strings.Join(is.Samples, ", "))
		}
		for _, is := range t.Repaired {
			fmt.Fprintf(w, "  repaired %5d  %s (e.g. %s)\n", is.Count, is.Reason, strings.Join(is.Samples, ", "))
		}
	}
	fmt.Fprintf(w, "\nattachments files (%s): copied %d (%d bytes), already present %d, missing %d",
		r.Files.Source, r.Files.Copied, r.Files.Bytes, r.Files.Skipped, r.Files.Missing)
	if len(r.Files.Samples) > 0 {
		fmt.Fprintf(w, " (e.g. %s)", strings.Join(r.Files.Samples, ", "))
	}
	fmt.Fprintln(w)
	if len(r.Checks) > 0 {
		fmt.Fprintln(w, "\nchecks:")
		for _, c := range r.Checks {
			st := "OK"
			if !c.OK {
				st = "FAIL"
			}
			fmt.Fprintf(w, "  %-4s %s\n", st, c.Name)
			for _, d := range c.Detail {
				fmt.Fprintf(w, "         %s\n", d)
			}
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintln(w, "\nwarnings:")
		for _, s := range r.Warnings {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
}
