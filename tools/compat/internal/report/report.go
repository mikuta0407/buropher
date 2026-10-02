// Package report は比較結果の集計と Markdown/HTML レポート出力、許容差分リストを扱う。
package report

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status はケースの判定結果。
type Status string

const (
	Pass    Status = "pass"
	Fail    Status = "fail"
	Allowed Status = "allowed"
	Error   Status = "error"
	Skipped Status = "skipped"
)

// Result は 1 ケースの結果。
type Result struct {
	Scenario string
	ID       string
	Method   string
	Path     string
	User     string
	Status   Status
	Diff     string
	Message  string // エラー内容・許容理由・スキップ理由
}

// Report は全結果。
type Report struct {
	Title     string
	Reference string
	Candidate string
	Started   time.Time
	Results   []Result
}

// Counts はステータスごとの件数。
func (r *Report) Counts() map[Status]int {
	m := map[Status]int{}
	for _, x := range r.Results {
		m[x.Status]++
	}
	return m
}

// Failed は失敗（fail/error）があるかを返す。
func (r *Report) Failed() bool {
	c := r.Counts()
	return c[Fail]+c[Error] > 0
}

// SummaryLine は 1 行サマリ。
func (r *Report) SummaryLine() string {
	c := r.Counts()
	return fmt.Sprintf("total=%d pass=%d fail=%d error=%d allowed=%d skipped=%d",
		len(r.Results), c[Pass], c[Fail], c[Error], c[Allowed], c[Skipped])
}

// WriteDir は outDir に summary.md / summary.html / diffs/<scenario>/<id>.diff を書き出す。
func (r *Report) WriteDir(outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// 前回の差分ファイルを掃除
	_ = os.RemoveAll(filepath.Join(outDir, "diffs"))
	for _, x := range r.Results {
		if x.Diff == "" {
			continue
		}
		p := filepath.Join(outDir, "diffs", x.Scenario, x.ID+".diff")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(x.Diff), 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(r.Markdown()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "summary.html"), []byte(r.HTML()), 0o644)
}

func icon(s Status) string {
	switch s {
	case Pass:
		return "PASS"
	case Fail:
		return "FAIL"
	case Allowed:
		return "ALLOWED"
	case Error:
		return "ERROR"
	default:
		return "SKIP"
	}
}

// Markdown は Markdown 形式のレポートを返す。
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", r.Title)
	fmt.Fprintf(&b, "- reference: `%s`\n- candidate: `%s`\n- date: %s\n- result: **%s**\n\n",
		r.Reference, r.Candidate, r.Started.Format(time.RFC3339), r.SummaryLine())
	b.WriteString("| status | scenario | case | method | path | user | note |\n|---|---|---|---|---|---|---|\n")
	for _, x := range r.Results {
		note := strings.ReplaceAll(x.Message, "|", "\\|")
		if x.Diff != "" && x.Status != Pass {
			note = strings.TrimSpace(note + fmt.Sprintf(" (diffs/%s/%s.diff)", x.Scenario, x.ID))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | `%s` | %s | %s |\n",
			icon(x.Status), x.Scenario, x.ID, x.Method, x.Path, x.User, note)
	}
	return b.String()
}

// HTML は差分を折りたたみ表示する単一ファイルの HTML レポートを返す。
func (r *Report) HTML() string {
	var b strings.Builder
	e := html.EscapeString
	b.WriteString(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>` + e(r.Title) + `</title>
<style>
body{font-family:sans-serif;margin:16px;color:#222;background:#fff}
table{border-collapse:collapse;width:100%}td,th{border:1px solid #ccc;padding:4px 6px;font-size:13px;text-align:left}
.pass{color:#16794a}.fail,.error{color:#b42318;font-weight:bold}.allowed{color:#9a6700}.skipped{color:#666}
pre{background:#f6f8fa;padding:8px;overflow:auto;font-size:12px}
.add{color:#16794a}.del{color:#b42318}.hunk{color:#6639ba}
</style></head><body>
`)
	fmt.Fprintf(&b, "<h1>%s</h1>\n<p>reference: <code>%s</code><br>candidate: <code>%s</code><br>date: %s<br><strong>%s</strong></p>\n",
		e(r.Title), e(r.Reference), e(r.Candidate), e(r.Started.Format(time.RFC3339)), e(r.SummaryLine()))
	b.WriteString("<table><tr><th>status</th><th>scenario</th><th>case</th><th>method</th><th>path</th><th>user</th><th>note</th></tr>\n")
	for _, x := range r.Results {
		fmt.Fprintf(&b, `<tr><td class="%s">%s</td><td>%s</td><td>`, x.Status, icon(x.Status), e(x.Scenario))
		if x.Diff != "" && x.Status != Pass {
			fmt.Fprintf(&b, `<a href="#%s">%s</a>`, e(x.Scenario+"-"+x.ID), e(x.ID))
		} else {
			b.WriteString(e(x.ID))
		}
		fmt.Fprintf(&b, "</td><td>%s</td><td><code>%s</code></td><td>%s</td><td>%s</td></tr>\n",
			e(x.Method), e(x.Path), e(x.User), e(x.Message))
	}
	b.WriteString("</table>\n")
	for _, x := range r.Results {
		if x.Diff == "" || x.Status == Pass {
			continue
		}
		fmt.Fprintf(&b, `<details id="%s" open><summary class="%s">%s %s/%s</summary><pre>`,
			e(x.Scenario+"-"+x.ID), x.Status, icon(x.Status), e(x.Scenario), e(x.ID))
		for _, l := range strings.Split(x.Diff, "\n") {
			cls := ""
			switch {
			case strings.HasPrefix(l, "@@"):
				cls = "hunk"
			case strings.HasPrefix(l, "+"):
				cls = "add"
			case strings.HasPrefix(l, "-"):
				cls = "del"
			}
			if cls != "" {
				fmt.Fprintf(&b, `<span class="%s">%s</span>`+"\n", cls, e(l))
			} else {
				b.WriteString(e(l) + "\n")
			}
		}
		b.WriteString("</pre></details>\n")
	}
	b.WriteString("</body></html>\n")
	return b.String()
}
