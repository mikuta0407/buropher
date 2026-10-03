// perf は buropher の主要画面に HTTP リクエストを繰り返し送り、レイテンシの p50 / p95 を測る。
//
// tools/gendata で作ったデータを前提にしている (ログイン情報・API キー・プロジェクト識別子)。
//
//	go run ./tools/perf -base http://127.0.0.1:4231 -n 20
//
// 管理者 (admin) と非管理者メンバー (user0001) のそれぞれで全エンドポイントを順に測る。
// HTML はセッション (フォームログイン)、*.json は API キーで認証する。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

type endpoint struct {
	Name string
	Path string
	// API は API キーで認証する (セッションを使わない)。
	API bool
}

type user struct {
	Name, Login, Password, APIKey string
	timeout                       time.Duration
	client                        *http.Client
}

type result struct {
	Endpoint string          `json:"endpoint"`
	Path     string          `json:"path"`
	User     string          `json:"user"`
	P50      float64         `json:"p50_ms"`
	P95      float64         `json:"p95_ms"`
	Mean     float64         `json:"mean_ms"`
	Bytes    int             `json:"bytes"`
	Status   int             `json:"status"`
	N        int             `json:"n"`
	Samples  []time.Duration `json:"-"`
}

func main() {
	base := flag.String("base", "http://127.0.0.1:4231", "base URL of the server")
	n := flag.Int("n", 20, "measured requests per endpoint and user")
	warmup := flag.Int("warmup", 2, "warm-up requests per endpoint and user (not measured)")
	conc := flag.Int("c", 1, "concurrent clients per endpoint (each sends n requests)")
	project := flag.String("project", "proj-001", "identifier of the large project")
	issue := flag.Int("issue", 50001, "issue with many journals")
	only := flag.String("only", "", "comma-separated substrings; measure only matching endpoint names")
	users := flag.String("users", "admin:admin:perfadmin1:perfadminapikey0000000000000000000000000,member:user0001:perfpass1:perfmemberapikey000000000000000000000000",
		"name:login:password:apikey,...")
	jsonOut := flag.String("json", "", "write results as JSON to this file")
	timeout := flag.Duration("timeout", 3*time.Minute, "per-request timeout")
	budget := flag.Duration("budget", time.Minute, "stop measuring an endpoint after this much time (at least 3 samples)")
	flag.Parse()

	p := "/projects/" + *project
	eps := []endpoint{
		{"projects", "/projects", false},
		{"project overview", p, false},
		{"issues (default)", p + "/issues", false},
		{"issues (all projects)", "/issues", false},
		{"issues filter", p + "/issues?set_filter=1&f[]=status_id&op[status_id]=o&f[]=tracker_id&op[tracker_id]=%3D&v[tracker_id][]=1&f[]=cf_6&op[cf_6]=%3D&v[cf_6][]=Major&f[]=subject&op[subject]=~&v[subject][]=error", false},
		{"issues sort priority", p + "/issues?sort=priority:desc,updated_on:desc", false},
		{"issues sort assignee", p + "/issues?sort=assigned_to,id:desc", false},
		{"issues sort cf", p + "/issues?set_filter=1&f[]=status_id&op[status_id]=*&sort=cf_3:desc&c[]=tracker&c[]=subject&c[]=cf_3&c[]=cf_9", false},
		{"issues group status", p + "/issues?set_filter=1&f[]=status_id&op[status_id]=*&group_by=status", false},
		{"issues group assignee+totals", p + "/issues?set_filter=1&f[]=status_id&op[status_id]=o&group_by=assigned_to&t[]=estimated_hours&t[]=spent_hours", false},
		{"issues many columns", p + "/issues?set_filter=1&f[]=status_id&op[status_id]=o&c[]=project&c[]=tracker&c[]=status&c[]=priority&c[]=subject&c[]=assigned_to&c[]=updated_on&c[]=category&c[]=fixed_version&c[]=start_date&c[]=due_date&c[]=estimated_hours&c[]=spent_hours&c[]=done_ratio&c[]=parent&c[]=relations&c[]=watchers&c[]=cf_1&c[]=cf_6&c[]=cf_9&per_page=100", false},
		{"issue show (many journals)", fmt.Sprintf("/issues/%d", *issue), false},
		{"gantt", p + "/issues/gantt", false},
		{"calendar", p + "/issues/calendar", false},
		{"activity", p + "/activity", false},
		{"activity (all)", "/activity", false},
		{"search", "/search?q=crash+login&all_words=1&titles_only=0", false},
		{"search (project)", p + "/search?q=memory+leak", false},
		{"time entries", "/time_entries", false},
		{"time entries (project)", p + "/time_entries", false},
		{"time report", p + "/time_entries/report?criteria[]=user&criteria[]=activity&columns=month&period_type=1&period=all", false},
		{"my page", "/my/page", false},
		{"issues.json limit=100", "/issues.json?limit=100", true},
		{"project issues.json limit=100", p + "/issues.json?limit=100&include=relations", true},
		{"wiki page", p + "/wiki/Wiki", false},
		{"wiki index", p + "/wiki/index", false},
		{"roadmap", p + "/roadmap", false},
		{"issues CSV (10k)", p + "/issues.csv?set_filter=1&f[]=status_id&op[status_id]=*&encoding=UTF-8", false},
	}
	if *only != "" {
		var sel []endpoint
		for _, e := range eps {
			for _, s := range strings.Split(*only, ",") {
				if strings.Contains(e.Name, s) {
					sel = append(sel, e)
					break
				}
			}
		}
		eps = sel
	}

	var us []*user
	for _, spec := range strings.Split(*users, ",") {
		f := strings.Split(spec, ":")
		if len(f) != 4 {
			log.Fatalf("bad -users entry %q", spec)
		}
		us = append(us, &user{Name: f[0], Login: f[1], Password: f[2], APIKey: f[3], timeout: *timeout})
	}

	var results []*result
	for _, e := range eps {
		for _, u := range us {
			// エンドポイントごとに新しいセッションでログインする
			// (Redmine 互換のクエリはセッションに保存されるので、前の測定の条件を引き継がないように)
			if err := u.login(*base); err != nil {
				log.Fatalf("login %s: %v", u.Login, err)
			}
			r := measure(*base, u, e, *n, *warmup, *conc, *budget)
			results = append(results, r)
			log.Printf("%-30s %-7s p50 %7.1fms  p95 %7.1fms  %8d B  [%d] n=%d", e.Name, u.Name, r.P50, r.P95, r.Bytes, r.Status, r.N)
		}
	}
	printTable(os.Stdout, results, us)
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(*jsonOut, b, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

var tokenRe = regexp.MustCompile(`name="authenticity_token" value="([^"]+)"`)

// login はログインフォームからセッションを得る。
func (u *user) login(base string) error {
	jar, _ := cookiejar.New(nil)
	u.client = &http.Client{
		Jar:     jar,
		Timeout: u.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{MaxIdleConnsPerHost: 64},
	}
	res, err := u.client.Get(base + "/login")
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := tokenRe.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("authenticity_token not found")
	}
	form := url.Values{"username": {u.Login}, "password": {u.Password}, "authenticity_token": {string(m[1])}}
	res, err = u.client.PostForm(base+"/login", form)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || strings.Contains(res.Header.Get("Location"), "/login") {
		return fmt.Errorf("login failed: %s %s", res.Status, res.Header.Get("Location"))
	}
	return nil
}

func (u *user) get(base string, e endpoint) (int, int, time.Duration, error) {
	req, _ := http.NewRequest("GET", base+e.Path, nil)
	if e.API {
		req.Header.Set("X-Redmine-API-Key", u.APIKey)
	} else {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	}
	t := time.Now()
	c := u.client
	if e.API {
		// API はセッション Cookie を送らない
		c = &http.Client{Transport: u.client.Transport, Timeout: u.client.Timeout}
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, 0, 0, err
	}
	n, err := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, int(n), time.Since(t), err
}

func measure(base string, u *user, e endpoint, n, warmup, conc int, budget time.Duration) *result {
	r := &result{Endpoint: e.Name, Path: e.Path, User: u.Name}
	for range warmup {
		if st, b, _, err := u.get(base, e); err == nil {
			r.Status, r.Bytes = st, b
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := time.Now()
	for range conc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range n {
				if i >= 3 && time.Since(start) > budget {
					break
				}
				st, b, d, err := u.get(base, e)
				if err != nil {
					log.Printf("%s: %v", e.Path, err)
					continue
				}
				mu.Lock()
				r.Status, r.Bytes = st, b
				r.Samples = append(r.Samples, d)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	slices.Sort(r.Samples)
	r.N = len(r.Samples)
	if len(r.Samples) > 0 {
		r.P50 = ms(percentile(r.Samples, 0.50))
		r.P95 = ms(percentile(r.Samples, 0.95))
		var sum time.Duration
		for _, s := range r.Samples {
			sum += s
		}
		r.Mean = ms(sum / time.Duration(len(r.Samples)))
	}
	return r
}

// percentile は最近傍順位法 (nearest-rank) で分位点を返す。
func percentile(s []time.Duration, q float64) time.Duration {
	i := int(float64(len(s))*q+0.999999) - 1
	return s[max(0, min(i, len(s)-1))]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func printTable(w io.Writer, rs []*result, us []*user) {
	fmt.Fprint(w, "\n| Endpoint |")
	for _, u := range us {
		fmt.Fprintf(w, " %s p50 (ms) | %s p95 (ms) |", u.Name, u.Name)
	}
	fmt.Fprint(w, "\n|---|")
	for range us {
		fmt.Fprint(w, "---:|---:|")
	}
	fmt.Fprintln(w)
	for i := 0; i < len(rs); i += len(us) {
		fmt.Fprintf(w, "| %s |", rs[i].Endpoint)
		for j := range us {
			r := rs[i+j]
			mark := ""
			if r.Status != http.StatusOK {
				mark = fmt.Sprintf(" [%d]", r.Status)
			}
			fmt.Fprintf(w, " %.1f%s | %.1f |", r.P50, mark, r.P95)
		}
		fmt.Fprintln(w)
	}
}
