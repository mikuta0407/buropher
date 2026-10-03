// gendata は性能測定用に、マイグレーション済みの buropher DB へ現実的な規模のデータを投入する。
//
// 既定の規模 (-scale 1): プロジェクト 200 (入れ子), ユーザー 500, グループ 50, チケット 10 万
// (親子・関連・カスタム値 10 項目・ウォッチャー), ジャーナル 100 万 + 詳細, 作業時間 20 万,
// Wiki ページ 2 万 (版つき), 添付ファイル行 5 万 (実ファイルなし), ニュース・フォーラム。
// 乱数は -seed で固定するので、同じ引数なら同じデータになる。
//
// 使い方:
//
//	go run ./tools/gendata -driver sqlite -dsn _reference/perf/perf.db
//	go run ./tools/gendata -driver postgres -dsn 'postgres://...'
//
// DB は空であること (マイグレーションと `buropher init` 相当の初期化もここで行う)。
// 管理者は admin / perfadmin1、一般ユーザーは user0001.. / perfpass1。
// user0001 は多数のプロジェクトに開発者として参加する「非管理者メンバー」の測定用ユーザー。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
)

func main() {
	driver := flag.String("driver", "sqlite", "sqlite or postgres")
	dsn := flag.String("dsn", "", "database DSN (sqlite: file path)")
	scale := flag.Float64("scale", 1, "data volume multiplier")
	seed := flag.Uint64("seed", 1, "random seed")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("-dsn is required")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, *driver, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	g := &gen{d: d, rng: rand.New(rand.NewPCG(*seed, *seed^0x9e3779b97f4a7c15)), scale: *scale}
	start := time.Now()
	if err := g.run(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("done in %s", time.Since(start).Round(time.Second))
}

// 生成の起点となる時刻範囲 (チケットの作成日時はこの間に単調増加で分布する)。
var (
	baseTime = time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime  = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
)

type gen struct {
	d     *db.DB
	tx    *db.Tx
	rng   *rand.Rand
	scale float64

	trackers   []int64
	statuses   []int64
	closed     map[int64]bool
	priorities []int64
	activities []int64
	docCats    []int64
	roleIDs    map[string]int64 // Manager / Developer / Reporter

	users    []int64 // 一般ユーザー (index 0 = user0001)
	groups   []int64
	groupMem map[int64][]int64

	projects   []project
	members    map[int64][]int64 // プロジェクト → メンバーのユーザー ID
	versions   map[int64][]int64
	categories map[int64][]int64
	cfs        []cf
	wikiIDs    map[int64]int64

	issues []issue
}

type project struct {
	id, parent int64
	depth      int
}

type cf struct {
	id     int64
	format string
	values []string
}

type issue struct {
	id, project int64
	created     time.Time
}

func (g *gen) n(base int) int { return max(1, int(math.Round(float64(base)*g.scale))) }

func (g *gen) pick(xs []int64) int64 { return xs[g.rng.IntN(len(xs))] }

func (g *gen) run(ctx context.Context) error {
	if _, err := db.Migrate(ctx, g.d, db.Up); err != nil {
		return err
	}
	if err := bootstrap.Init(ctx, g.d, bootstrap.Options{AdminPassword: "perfadmin1"}); err != nil {
		return err
	}
	if g.d.Dialect().Name() == db.SQLite {
		// 投入中は 1 接続に固定して同期を切る (生成用。サーバー起動時は通常の設定に戻る)
		g.d.X().SetMaxOpenConns(1)
		if _, err := g.d.Exec(ctx, `PRAGMA synchronous=OFF`); err != nil {
			return err
		}
	}
	if err := g.loadMasters(ctx); err != nil {
		return err
	}
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"users", g.genUsers},
		{"projects", g.genProjects},
		{"members", g.genMembers},
		{"custom fields", g.genCustomFields},
		{"issues", g.genIssues},
		{"relations/watchers", g.genRelationsWatchers},
		{"journals", g.genJournals},
		{"time entries", g.genTimeEntries},
		{"wiki", g.genWiki},
		{"news/boards", g.genNewsBoards},
		{"attachments", g.genAttachments},
		{"settings/api keys", g.genSettings},
	}
	for _, s := range steps {
		t := time.Now()
		err := g.d.WithTx(ctx, func(tx *db.Tx) error {
			g.tx = tx
			return s.fn(ctx)
		})
		if err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		log.Printf("%-20s %s", s.name, time.Since(t).Round(time.Millisecond))
	}
	if g.d.Dialect().Name() == db.Postgres {
		if err := g.fixSequences(ctx); err != nil {
			return err
		}
		// PostgreSQL は autovacuum が統計を取るので、測定条件をそろえるため ANALYZE しておく。
		// SQLite は実運用で ANALYZE されていないのが普通なので、ここでは行わない。
		if _, err := g.d.Exec(ctx, `ANALYZE`); err != nil {
			return err
		}
	}
	return nil
}

func (g *gen) loadMasters(ctx context.Context) error {
	if err := g.d.Select(ctx, &g.trackers, `SELECT id FROM trackers ORDER BY position`); err != nil {
		return err
	}
	var sts []struct {
		ID       int64 `db:"id"`
		IsClosed bool  `db:"is_closed"`
	}
	if err := g.d.Select(ctx, &sts, `SELECT id, is_closed FROM issue_statuses ORDER BY position`); err != nil {
		return err
	}
	g.closed = map[int64]bool{}
	for _, s := range sts {
		g.statuses = append(g.statuses, s.ID)
		g.closed[s.ID] = s.IsClosed
	}
	if err := g.d.Select(ctx, &g.priorities, `SELECT id FROM issue_priorities ORDER BY position`); err != nil {
		return err
	}
	if err := g.d.Select(ctx, &g.activities, `SELECT id FROM time_entry_activities ORDER BY position`); err != nil {
		return err
	}
	if err := g.d.Select(ctx, &g.docCats, `SELECT id FROM document_categories ORDER BY position`); err != nil {
		return err
	}
	var roles []struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	if err := g.d.Select(ctx, &roles, `SELECT id, name FROM roles WHERE builtin = 0 ORDER BY position`); err != nil {
		return err
	}
	g.roleIDs = map[string]int64{}
	for _, r := range roles {
		g.roleIDs[r.Name] = r.ID
	}
	for _, n := range []string{"Manager", "Developer", "Reporter"} {
		if g.roleIDs[n] == 0 {
			return fmt.Errorf("role %s not found (default data must be English)", n)
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// 一括 INSERT
// ---------------------------------------------------------------------

// inserter は複数行 VALUES の INSERT にまとめて投入する。
type inserter struct {
	g     *gen
	ctx   context.Context
	head  string
	ncols int
	args  []any
	rows  int
	total int
	// deps は先に投入しておく必要がある (FK の参照先の) inserter。
	deps []*inserter
}

// after は自分を flush する前に deps を flush するよう登録する。
func (in *inserter) after(deps ...*inserter) *inserter {
	in.deps = append(in.deps, deps...)
	return in
}

func (g *gen) ins(ctx context.Context, table string, cols ...string) *inserter {
	return &inserter{g: g, ctx: ctx, head: "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ", ncols: len(cols)}
}

func (in *inserter) add(vals ...any) error {
	if len(vals) != in.ncols {
		panic(fmt.Sprintf("%s: %d values for %d columns", in.head, len(vals), in.ncols))
	}
	in.args = append(in.args, vals...)
	in.rows++
	in.total++
	// SQLite の変数上限 (32766) と PostgreSQL の上限 (65535) の内側に収める
	if in.rows >= 1000 || len(in.args)+in.ncols > 30000 {
		return in.flush()
	}
	return nil
}

func (in *inserter) flush() error {
	if in.rows == 0 {
		return nil
	}
	for _, d := range in.deps {
		if err := d.flush(); err != nil {
			return err
		}
	}
	var b strings.Builder
	b.WriteString(in.head)
	row := "(" + strings.TrimSuffix(strings.Repeat("?, ", in.ncols), ", ") + ")"
	for i := range in.rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(row)
	}
	_, err := in.g.tx.Exec(in.ctx, b.String(), in.args...)
	in.args = in.args[:0]
	in.rows = 0
	return err
}

func ts(t time.Time) string { return db.FormatTime(t) }
func ds(t time.Time) string { return t.Format(db.DateLayout) }

// ---------------------------------------------------------------------
// ユーザー・グループ
// ---------------------------------------------------------------------

var firstNames = []string{"John", "Jane", "Taro", "Hanako", "Alice", "Bob", "Carol", "Dave", "Eve", "Frank", "Grace", "Heidi", "Ivan", "Judy", "Ken", "Yuki", "Sora", "Mei", "Liam", "Olivia"}
var lastNames = []string{"Smith", "Suzuki", "Tanaka", "Sato", "Brown", "Miller", "Davis", "Garcia", "Wilson", "Taylor", "Ito", "Watanabe", "Yamamoto", "Nakamura", "Kobayashi", "Lee", "Martin", "Clark", "Lewis", "Walker"}

func (g *gen) genUsers(ctx context.Context) error {
	hash, err := password.Hash("perfpass1")
	if err != nil {
		return err
	}
	var maxID int64
	if err := g.tx.Get(ctx, &maxID, `SELECT MAX(id) FROM principals`); err != nil {
		return err
	}
	now := ts(baseTime)
	pr := g.ins(ctx, "principals", "id", "kind", "status", "firstname", "lastname", "name", "created_at", "updated_at")
	ua := g.ins(ctx, "user_accounts", "principal_id", "login", "password_hash", "password_changed_at", "admin", "language", "last_login_at").after(pr)
	em := g.ins(ctx, "email_addresses", "user_id", "address", "is_default", "notify", "created_at", "updated_at").after(ua)
	nu := g.n(500)
	id := maxID
	for i := range nu {
		id++
		g.users = append(g.users, id)
		fn, ln := firstNames[g.rng.IntN(len(firstNames))], lastNames[g.rng.IntN(len(lastNames))]
		status := 1
		if i >= 10 && g.rng.IntN(50) == 0 {
			status = 3 // ロック
		}
		login := fmt.Sprintf("user%04d", i+1)
		if err := pr.add(id, "user", status, fn, ln, "", now, now); err != nil {
			return err
		}
		if err := ua.add(id, login, hash, now, false, "en", ts(endTime.Add(-time.Duration(g.rng.IntN(1000))*time.Hour))); err != nil {
			return err
		}
		if err := em.add(id, login+"@example.net", true, true, now, now); err != nil {
			return err
		}
	}
	ng := g.n(50)
	for i := range ng {
		id++
		g.groups = append(g.groups, id)
		if err := pr.add(id, "group", 1, "", "", fmt.Sprintf("Group %02d", i+1), now, now); err != nil {
			return err
		}
	}
	for _, in := range []*inserter{pr, ua, em} {
		if err := in.flush(); err != nil {
			return err
		}
	}
	gu := g.ins(ctx, "group_users", "group_id", "user_id")
	g.groupMem = map[int64][]int64{}
	for _, gid := range g.groups {
		seen := map[int64]bool{}
		for range 5 + g.rng.IntN(26) {
			u := g.users[g.rng.IntN(len(g.users))]
			if seen[u] {
				continue
			}
			seen[u] = true
			g.groupMem[gid] = append(g.groupMem[gid], u)
			if err := gu.add(gid, u); err != nil {
				return err
			}
		}
	}
	return gu.flush()
}

// ---------------------------------------------------------------------
// プロジェクト
// ---------------------------------------------------------------------

var modules = []string{"issue_tracking", "time_tracking", "news", "documents", "files", "wiki", "boards", "calendar", "gantt"}

func (g *gen) genProjects(ctx context.Context) error {
	np := g.n(200)
	roots := max(1, np/5)
	pins := g.ins(ctx, "projects", "id", "parent_id", "name", "identifier", "description", "is_public", "status", "position", "created_at", "updated_at")
	children := map[int64]int{}
	for i := 1; i <= np; i++ {
		p := project{id: int64(i)}
		if i > roots {
			// 既存プロジェクトの子にする (深さ 3 まで。若い ID ほど子を持ちやすい)
			for {
				cand := g.projects[int(float64(len(g.projects))*math.Pow(g.rng.Float64(), 2))]
				if cand.depth < 3 {
					p.parent, p.depth = cand.id, cand.depth+1
					break
				}
			}
		}
		g.projects = append(g.projects, p)
		var parent any
		if p.parent != 0 {
			parent = p.parent
		}
		pos := children[p.parent]
		children[p.parent]++
		status := 1
		if i > 10 && g.rng.IntN(20) == 0 {
			status = 5 // 終了
		}
		created := baseTime.Add(time.Duration(i) * time.Hour)
		if err := pins.add(p.id, parent, fmt.Sprintf("Project %03d", i), fmt.Sprintf("proj-%03d", i),
			fmt.Sprintf("Description of project %d.\n\nSee [[Wiki]] for details.", i),
			g.rng.IntN(5) != 0, status, pos, ts(created), ts(created)); err != nil {
			return err
		}
	}
	if err := pins.flush(); err != nil {
		return err
	}
	byID := map[int64]project{}
	for _, p := range g.projects {
		byID[p.id] = p
	}
	cl := g.ins(ctx, "project_closure", "ancestor_id", "descendant_id", "depth")
	pm := g.ins(ctx, "project_modules", "project_id", "name")
	pt := g.ins(ctx, "project_trackers", "project_id", "tracker_id")
	wk := g.ins(ctx, "wikis", "id", "project_id", "start_page")
	vs := g.ins(ctx, "versions", "id", "project_id", "name", "description", "effective_date", "status", "sharing", "created_at", "updated_at")
	ic := g.ins(ctx, "issue_categories", "id", "project_id", "name", "assigned_to_id")
	g.versions = map[int64][]int64{}
	g.categories = map[int64][]int64{}
	g.wikiIDs = map[int64]int64{}
	var vid, cid int64
	sharings := []string{"none", "none", "none", "descendants", "hierarchy", "tree", "system"}
	for _, p := range g.projects {
		for a, depth := p, 0; ; depth++ {
			if err := cl.add(a.id, p.id, depth); err != nil {
				return err
			}
			if a.parent == 0 {
				break
			}
			a = byID[a.parent]
		}
		for _, m := range modules {
			if err := pm.add(p.id, m); err != nil {
				return err
			}
		}
		for _, t := range g.trackers {
			if err := pt.add(p.id, t); err != nil {
				return err
			}
		}
		if err := wk.add(p.id, p.id, "Wiki"); err != nil {
			return err
		}
		g.wikiIDs[p.id] = p.id
		for v := range 5 {
			vid++
			g.versions[p.id] = append(g.versions[p.id], vid)
			eff := baseTime.AddDate(0, 6*(v+1), 0)
			status := "open"
			if v < 2 {
				status = "closed"
			}
			if err := vs.add(vid, p.id, fmt.Sprintf("v%d.%d", p.id, v), "Release "+strconv.Itoa(v), ds(eff), status, sharings[g.rng.IntN(len(sharings))], ts(baseTime), ts(baseTime)); err != nil {
				return err
			}
		}
		for c := range 4 {
			cid++
			g.categories[p.id] = append(g.categories[p.id], cid)
			if err := ic.add(cid, p.id, []string{"Backend", "Frontend", "Docs", "Infra"}[c], nil); err != nil {
				return err
			}
		}
	}
	for _, in := range []*inserter{cl, pm, pt, wk, vs, ic} {
		if err := in.flush(); err != nil {
			return err
		}
	}
	return nil
}

func (g *gen) genMembers(ctx context.Context) error {
	mi := g.ins(ctx, "members", "id", "project_id", "principal_id", "created_at")
	mr := g.ins(ctx, "member_roles", "id", "member_id", "role_id", "inherited_from").after(mi)
	roles := []int64{g.roleIDs["Manager"], g.roleIDs["Developer"], g.roleIDs["Developer"], g.roleIDs["Reporter"], g.roleIDs["Reporter"]}
	g.members = map[int64][]int64{}
	var mid, mrid int64
	now := ts(baseTime)
	for _, p := range g.projects {
		memberOf := map[int64]int64{} // principal → member id
		addMember := func(principal int64) (int64, error) {
			if m, ok := memberOf[principal]; ok {
				return m, nil
			}
			mid++
			memberOf[principal] = mid
			return mid, mi.add(mid, p.id, principal, now)
		}
		// user0001 は 3 プロジェクトに 1 つ (と先頭のプロジェクト) で開発者
		if p.id <= 5 || p.id%3 == 0 {
			m, err := addMember(g.users[0])
			if err != nil {
				return err
			}
			mrid++
			if err := mr.add(mrid, m, g.roleIDs["Developer"], nil); err != nil {
				return err
			}
		}
		for range 10 + g.rng.IntN(31) {
			u := g.users[g.rng.IntN(len(g.users))]
			if _, ok := memberOf[u]; ok {
				continue
			}
			m, err := addMember(u)
			if err != nil {
				return err
			}
			mrid++
			if err := mr.add(mrid, m, roles[g.rng.IntN(len(roles))], nil); err != nil {
				return err
			}
		}
		// グループのメンバーシップ (グループのメンバーへ継承ロールを作る)
		for range g.rng.IntN(4) {
			gid := g.groups[g.rng.IntN(len(g.groups))]
			if _, ok := memberOf[gid]; ok {
				continue
			}
			gm, err := addMember(gid)
			if err != nil {
				return err
			}
			mrid++
			groupMR := mrid
			role := roles[g.rng.IntN(len(roles))]
			if err := mr.add(groupMR, gm, role, nil); err != nil {
				return err
			}
			for _, u := range g.groupMem[gid] {
				m, err := addMember(u)
				if err != nil {
					return err
				}
				mrid++
				if err := mr.add(mrid, m, role, groupMR); err != nil {
					return err
				}
			}
		}
		for principal := range memberOf {
			if !g.isGroup(principal) {
				g.members[p.id] = append(g.members[p.id], principal)
			}
		}
		sort.Slice(g.members[p.id], func(i, j int) bool { return g.members[p.id][i] < g.members[p.id][j] })
	}
	if err := mi.flush(); err != nil {
		return err
	}
	return mr.flush()
}

func (g *gen) isGroup(id int64) bool { return len(g.groups) > 0 && id >= g.groups[0] }

// ---------------------------------------------------------------------
// カスタムフィールド
// ---------------------------------------------------------------------

func (g *gen) genCustomFields(ctx context.Context) error {
	defs := []struct {
		name, format   string
		filter, search bool
		values         []string
	}{
		{"Customer", "string", true, true, nil},
		{"Notes", "text", false, true, nil},
		{"Story points", "int", true, false, nil},
		{"Cost", "float", true, false, nil},
		{"Deadline", "date", true, false, nil},
		{"Severity", "list", true, false, []string{"Trivial", "Minor", "Major", "Critical", "Blocker"}},
		{"Platform", "list", true, false, []string{"Linux", "Windows", "macOS", "iOS", "Android"}},
		{"Regression", "bool", true, false, nil},
		{"Reviewer", "user", true, false, nil},
		{"Reference", "link", false, false, nil},
	}
	ci := g.ins(ctx, "custom_fields", "id", "owner_kind", "name", "field_format", "is_for_all", "is_filter", "searchable", "position", "possible_values", "format_settings")
	ct := g.ins(ctx, "custom_fields_trackers", "custom_field_id", "tracker_id")
	for i, d := range defs {
		id := int64(i + 1)
		var pv any
		if d.values != nil {
			pv = `["` + strings.Join(d.values, `","`) + `"]`
		}
		if err := ci.add(id, "issue", d.name, d.format, true, d.filter, d.search, i+1, pv, "{}"); err != nil {
			return err
		}
		for _, t := range g.trackers {
			if err := ct.add(id, t); err != nil {
				return err
			}
		}
		g.cfs = append(g.cfs, cf{id: id, format: d.format, values: d.values})
	}
	if err := ci.flush(); err != nil {
		return err
	}
	return ct.flush()
}

func (g *gen) cfValue(c cf, project int64) any {
	switch c.format {
	case "string":
		return fmt.Sprintf("Customer %d", g.rng.IntN(200))
	case "text":
		return "Some *notes* about this issue.\nLine two."
	case "int":
		return strconv.Itoa([]int{1, 2, 3, 5, 8, 13}[g.rng.IntN(6)])
	case "float":
		return strconv.FormatFloat(float64(g.rng.IntN(100000))/100, 'f', 2, 64)
	case "date":
		return ds(baseTime.AddDate(0, 0, g.rng.IntN(1700)))
	case "list":
		return c.values[g.rng.IntN(len(c.values))]
	case "bool":
		return strconv.Itoa(g.rng.IntN(2))
	case "user":
		if ms := g.members[project]; len(ms) > 0 {
			return strconv.FormatInt(g.pick(ms), 10)
		}
		return ""
	case "link":
		return "https://example.com/ref/" + strconv.Itoa(g.rng.IntN(10000))
	}
	return nil
}

// ---------------------------------------------------------------------
// チケット
// ---------------------------------------------------------------------

var words = strings.Fields(`the a error crash login page report export import search user project issue timeline wiki
server database slow fast memory leak button form validation layout mobile desktop api token feature request update delete
create list filter sort group total chart graph calendar gantt roadmap version release build deploy test failing broken
improve refactor cleanup documentation translation japanese english settings admin permission role workflow tracker status`)

func (g *gen) sentence(n int) string {
	ws := make([]string, n)
	for i := range ws {
		ws[i] = words[g.rng.IntN(len(words))]
	}
	ws[0] = strings.ToUpper(ws[0][:1]) + ws[0][1:]
	return strings.Join(ws, " ")
}

func (g *gen) paragraph() string {
	var b strings.Builder
	for i := range 2 + g.rng.IntN(5) {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(g.sentence(6 + g.rng.IntN(10)))
		b.WriteString(".")
	}
	return b.String()
}

// projectWeights はチケット数の偏り (Zipf 風。proj-001 が最大)。
func (g *gen) projectWeights() []float64 {
	cum := make([]float64, len(g.projects))
	s := 0.0
	for i := range g.projects {
		s += 1 / math.Pow(float64(i+1), 0.9)
		cum[i] = s
	}
	for i := range cum {
		cum[i] /= s
	}
	return cum
}

func (g *gen) genIssues(ctx context.Context) error {
	ni := g.n(100000)
	cum := g.projectWeights()
	ii := g.ins(ctx, "issues", "id", "project_id", "tracker_id", "status_id", "priority_id", "author_id", "assigned_to_id",
		"category_id", "fixed_version_id", "parent_id", "root_id", "hier_path", "subject", "description", "start_date", "due_date",
		"done_ratio", "estimated_hours", "is_private", "lock_version", "created_at", "updated_at", "closed_at")
	cv := g.ins(ctx, "custom_values", "customized_kind", "customized_id", "custom_field_id", "value")
	type node struct {
		id, root int64
		path     string
		depth    int
	}
	recent := map[int64][]node{} // プロジェクトごとの直近チケット (親候補)
	span := endTime.Sub(baseTime)
	statusW := []int{30, 20, 10, 10, 25, 5} // New, In Progress, Resolved, Feedback, Closed, Rejected
	for i := 1; i <= ni; i++ {
		id := int64(i)
		r := g.rng.Float64()
		pi := sort.SearchFloat64s(cum, r)
		p := g.projects[min(pi, len(g.projects)-1)].id
		created := baseTime.Add(time.Duration(float64(span) * float64(i) / float64(ni+1)))
		n := node{id: id, root: id, path: fmt.Sprintf("%010d/", id)}
		var parent any
		if rs := recent[p]; len(rs) > 0 && g.rng.IntN(4) == 0 {
			pn := rs[g.rng.IntN(len(rs))]
			if pn.depth < 3 {
				parent = pn.id
				n.root, n.path, n.depth = pn.root, pn.path+n.path, pn.depth+1
			}
		}
		rs := append(recent[p], n)
		if len(rs) > 50 {
			rs = rs[1:]
		}
		recent[p] = rs
		status := g.statuses[weighted(g.rng, statusW, len(g.statuses))]
		var closedAt any
		updated := created.Add(time.Duration(g.rng.IntN(90*24)) * time.Hour)
		if updated.After(endTime) {
			updated = endTime
		}
		if g.closed[status] {
			closedAt = ts(updated)
		}
		ms := g.members[p]
		author := g.pick(ms)
		var assignee, category, version, start, due, est any
		if g.rng.IntN(10) < 7 {
			assignee = g.pick(ms)
		}
		if g.rng.IntN(2) == 0 {
			category = g.pick(g.categories[p])
		}
		if g.rng.IntN(3) != 0 {
			version = g.pick(g.versions[p])
		}
		if g.rng.IntN(5) != 0 {
			sd := created.Truncate(24*time.Hour).AddDate(0, 0, g.rng.IntN(30))
			start = ds(sd)
			if g.rng.IntN(4) != 0 {
				due = ds(sd.AddDate(0, 0, 1+g.rng.IntN(60)))
			}
		}
		if g.rng.IntN(2) == 0 {
			est = float64(1+g.rng.IntN(80)) / 2
		}
		desc := g.paragraph() + "\n\n* " + g.sentence(5) + "\n* " + g.sentence(6) + "\n\nSee #" + strconv.Itoa(1+g.rng.IntN(i)) + "."
		if err := ii.add(id, p, g.pick(g.trackers), status, g.pick(g.priorities), author, assignee, category, version,
			parent, n.root, n.path, g.sentence(4+g.rng.IntN(6)), desc, start, due, 10*g.rng.IntN(11), est,
			g.rng.IntN(33) == 0, 0, ts(created), ts(updated), closedAt); err != nil {
			return err
		}
		for _, c := range g.cfs {
			if g.rng.IntN(10) < 6 {
				if err := cv.add("issue", id, c.id, g.cfValue(c, p)); err != nil {
					return err
				}
			}
		}
		g.issues = append(g.issues, issue{id: id, project: p, created: created})
	}
	if err := ii.flush(); err != nil {
		return err
	}
	return cv.flush()
}

func weighted(r *rand.Rand, w []int, n int) int {
	s := 0
	for _, x := range w[:min(n, len(w))] {
		s += x
	}
	v := r.IntN(s)
	for i, x := range w[:min(n, len(w))] {
		if v < x {
			return i
		}
		v -= x
	}
	return 0
}

func (g *gen) genRelationsWatchers(ctx context.Context) error {
	rel := g.ins(ctx, "issue_relations", "issue_from_id", "issue_to_id", "relation_type", "delay")
	types := []string{"relates", "relates", "blocks", "precedes", "duplicates", "copied_to"}
	seen := map[[2]int64]bool{}
	byProject := map[int64][]int64{}
	for _, is := range g.issues {
		byProject[is.project] = append(byProject[is.project], is.id)
	}
	for range g.n(20000) {
		is := g.issues[g.rng.IntN(len(g.issues))]
		ids := byProject[is.project]
		to := ids[g.rng.IntN(len(ids))]
		from := is.id
		if from == to {
			continue
		}
		if from > to {
			from, to = to, from
		}
		if seen[[2]int64{from, to}] || seen[[2]int64{to, from}] {
			continue
		}
		seen[[2]int64{from, to}] = true
		t := types[g.rng.IntN(len(types))]
		var delay any
		if t == "precedes" {
			delay = g.rng.IntN(3)
		}
		if err := rel.add(from, to, t, delay); err != nil {
			return err
		}
	}
	if err := rel.flush(); err != nil {
		return err
	}
	w := g.ins(ctx, "watchers", "watchable_kind", "watchable_id", "principal_id")
	for _, is := range g.issues {
		ms := g.members[is.project]
		ws := map[int64]bool{}
		for range g.rng.IntN(5) {
			u := g.pick(ms)
			if ws[u] {
				continue
			}
			ws[u] = true
			if err := w.add("issue", is.id, u); err != nil {
				return err
			}
		}
	}
	return w.flush()
}

// ---------------------------------------------------------------------
// ジャーナル
// ---------------------------------------------------------------------

// HotIssue は大量のジャーナルを持つチケット (詳細画面の測定用)。
const hotIssueJournals = 300

func (g *gen) genJournals(ctx context.Context) error {
	target := g.n(1000000)
	// 1 チケットあたりの件数は指数分布 (平均 target/len(issues))
	counts := make([]int, len(g.issues))
	mean := float64(target) / float64(len(g.issues))
	sum := 0
	for i := range counts {
		counts[i] = int(math.Round(g.rng.ExpFloat64() * mean))
		sum += counts[i]
	}
	hot := len(g.issues) / 2 // 中ほどのチケットを「長い履歴」にする
	sum += hotIssueJournals - counts[hot]
	counts[hot] = hotIssueJournals
	log.Printf("journals: %d (hot issue #%d with %d)", sum, g.issues[hot].id, hotIssueJournals)
	ji := g.ins(ctx, "issue_journals", "id", "issue_id", "user_id", "notes", "private_notes", "created_at")
	jd := g.ins(ctx, "issue_journal_details", "journal_id", "property", "prop_key", "custom_field_id", "old_value", "value").after(ji)
	var jid int64
	attrs := []string{"status_id", "assigned_to_id", "done_ratio", "priority_id", "fixed_version_id", "subject"}
	for idx, is := range g.issues {
		n := counts[idx]
		if n == 0 {
			continue
		}
		ms := g.members[is.project]
		gap := endTime.Sub(is.created) / time.Duration(n+1)
		t := is.created
		for range n {
			jid++
			t = t.Add(time.Duration(g.rng.Int64N(int64(gap)*2 + 1)))
			if t.After(endTime) {
				t = endTime
			}
			var notes any
			hasNotes := g.rng.IntN(10) < 6
			if hasNotes {
				notes = g.paragraph()
			}
			if err := ji.add(jid, is.id, g.pick(ms), notes, g.rng.IntN(50) == 0, ts(t)); err != nil {
				return err
			}
			nd := g.rng.IntN(3)
			if !hasNotes {
				nd++
			}
			for range nd {
				if g.rng.IntN(5) == 0 {
					c := g.cfs[g.rng.IntN(len(g.cfs))]
					v := g.cfValue(c, is.project)
					if err := jd.add(jid, "cf", strconv.FormatInt(c.id, 10), c.id, nil, v); err != nil {
						return err
					}
					continue
				}
				a := attrs[g.rng.IntN(len(attrs))]
				var ov, nv string
				switch a {
				case "status_id":
					ov, nv = strconv.FormatInt(g.pick(g.statuses), 10), strconv.FormatInt(g.pick(g.statuses), 10)
				case "assigned_to_id":
					ov, nv = strconv.FormatInt(g.pick(ms), 10), strconv.FormatInt(g.pick(ms), 10)
				case "done_ratio":
					ov, nv = strconv.Itoa(10*g.rng.IntN(11)), strconv.Itoa(10*g.rng.IntN(11))
				case "priority_id":
					ov, nv = strconv.FormatInt(g.pick(g.priorities), 10), strconv.FormatInt(g.pick(g.priorities), 10)
				case "fixed_version_id":
					ov, nv = strconv.FormatInt(g.pick(g.versions[is.project]), 10), strconv.FormatInt(g.pick(g.versions[is.project]), 10)
				case "subject":
					ov, nv = g.sentence(5), g.sentence(5)
				}
				if err := jd.add(jid, "attr", a, nil, ov, nv); err != nil {
					return err
				}
			}
		}
	}
	if err := ji.flush(); err != nil {
		return err
	}
	return jd.flush()
}

// ---------------------------------------------------------------------
// 作業時間
// ---------------------------------------------------------------------

func (g *gen) genTimeEntries(ctx context.Context) error {
	te := g.ins(ctx, "time_entries", "project_id", "user_id", "author_id", "issue_id", "hours", "comments", "activity_id",
		"spent_on", "tyear", "tmonth", "tweek", "created_at", "updated_at")
	for range g.n(200000) {
		is := g.issues[g.rng.IntN(len(g.issues))]
		var issueID any = is.id
		if g.rng.IntN(5) == 0 {
			issueID = nil
		}
		u := g.pick(g.members[is.project])
		day := is.created.Truncate(24*time.Hour).AddDate(0, 0, g.rng.IntN(60))
		if day.After(endTime) {
			day = endTime
		}
		y, w := day.ISOWeek()
		_ = y
		var comments any
		if g.rng.IntN(2) == 0 {
			comments = g.sentence(5)
		}
		if err := te.add(is.project, u, u, issueID, float64(1+g.rng.IntN(32))/4, comments, g.pick(g.activities),
			ds(day), day.Year(), int(day.Month()), w, ts(day.Add(18*time.Hour)), ts(day.Add(18*time.Hour))); err != nil {
			return err
		}
	}
	return te.flush()
}

// ---------------------------------------------------------------------
// Wiki
// ---------------------------------------------------------------------

func (g *gen) genWiki(ctx context.Context) error {
	cum := g.projectWeights()
	wp := g.ins(ctx, "wiki_pages", "id", "wiki_id", "title", "parent_id", "protected", "current_version", "created_at")
	wv := g.ins(ctx, "wiki_page_versions", "page_id", "version", "author_id", "text", "comments", "updated_at").after(wp)
	np := g.n(20000)
	pages := map[int64][]int64{}
	var id int64
	addPage := func(p int64, title string) error {
		id++
		var parent any
		if ps := pages[p]; len(ps) > 0 && g.rng.IntN(3) != 0 {
			parent = ps[g.rng.IntN(len(ps))]
		}
		pages[p] = append(pages[p], id)
		nv := 1 + g.rng.IntN(5)
		created := baseTime.Add(time.Duration(g.rng.Int64N(int64(endTime.Sub(baseTime)) / 2)))
		if err := wp.add(id, g.wikiIDs[p], title, parent, false, nv, ts(created)); err != nil {
			return err
		}
		ms := g.members[p]
		for v := 1; v <= nv; v++ {
			var b strings.Builder
			b.WriteString("h1. " + strings.ReplaceAll(title, "_", " ") + "\n\n{{toc}}\n\n")
			for s := range 3 + g.rng.IntN(6) {
				fmt.Fprintf(&b, "h2. Section %d\n\n%s\n\n* [[Page_%d]]\n* #%d\n\n", s+1, g.paragraph(), g.rng.IntN(np), 1+g.rng.IntN(len(g.issues)))
			}
			created = created.Add(time.Duration(1+g.rng.IntN(500)) * time.Hour)
			if err := wv.add(id, v, g.pick(ms), b.String(), "rev "+strconv.Itoa(v), ts(created)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, p := range g.projects {
		if err := addPage(p.id, "Wiki"); err != nil {
			return err
		}
	}
	for i := len(g.projects); i < np; i++ {
		p := g.projects[min(sort.SearchFloat64s(cum, g.rng.Float64()), len(g.projects)-1)].id
		if err := addPage(p, fmt.Sprintf("Page_%d", i)); err != nil {
			return err
		}
	}
	if err := wp.flush(); err != nil {
		return err
	}
	return wv.flush()
}

// ---------------------------------------------------------------------
// ニュース・フォーラム
// ---------------------------------------------------------------------

func (g *gen) genNewsBoards(ctx context.Context) error {
	ni := g.ins(ctx, "news", "id", "project_id", "title", "summary", "description", "author_id", "comments_count", "created_at")
	nc := g.ins(ctx, "news_comments", "news_id", "author_id", "content", "created_at", "updated_at").after(ni)
	for i := 1; i <= g.n(2000); i++ {
		p := g.projects[g.rng.IntN(len(g.projects))].id
		ms := g.members[p]
		t := baseTime.Add(time.Duration(g.rng.Int64N(int64(endTime.Sub(baseTime)))))
		ncm := g.rng.IntN(5)
		if err := ni.add(i, p, g.sentence(5), g.sentence(10), g.paragraph(), g.pick(ms), ncm, ts(t)); err != nil {
			return err
		}
		for range ncm {
			t = t.Add(time.Hour)
			if err := nc.add(i, g.pick(ms), g.paragraph(), ts(t), ts(t)); err != nil {
				return err
			}
		}
	}
	if err := ni.flush(); err != nil {
		return err
	}
	if err := nc.flush(); err != nil {
		return err
	}
	// フォーラム: プロジェクトごとに 1〜2 個、トピックと返信で計 2 万件程度
	bi := g.ins(ctx, "boards", "id", "project_id", "name", "description", "position", "topics_count", "messages_count", "last_message_id")
	mi := g.ins(ctx, "messages", "id", "board_id", "parent_id", "subject", "content", "author_id", "replies_count", "last_reply_id", "created_at", "updated_at")
	var bid, mid int64
	var msgRows [][]any // boards を先に入れるため messages は後でまとめて投入する
	perProject := g.n(20000) / len(g.projects)
	for _, p := range g.projects {
		ms := g.members[p.id]
		for b := range 1 + g.rng.IntN(2) {
			bid++
			topics, msgs := 0, 0
			var last int64
			t := baseTime.Add(time.Duration(g.rng.Int64N(int64(endTime.Sub(baseTime)) / 2)))
			for msgs < perProject/2 {
				mid++
				topic := mid
				nr := g.rng.IntN(8)
				var lastReply any
				if nr > 0 {
					lastReply = topic + int64(nr)
				}
				t = t.Add(time.Duration(g.rng.IntN(48)) * time.Hour)
				msgRows = append(msgRows, []any{topic, bid, nil, g.sentence(5), g.paragraph(), g.pick(ms), nr, lastReply, ts(t), ts(t)})
				for range nr {
					mid++
					t = t.Add(time.Hour)
					msgRows = append(msgRows, []any{mid, bid, topic, "RE: topic", g.paragraph(), g.pick(ms), 0, nil, ts(t), ts(t)})
				}
				topics++
				msgs += 1 + nr
				last = mid
			}
			var lastAny any
			if last != 0 {
				lastAny = last
			}
			if err := bi.add(bid, p.id, fmt.Sprintf("Board %d", b+1), "Discussion", b+1, topics, msgs, lastAny); err != nil {
				return err
			}
		}
	}
	// boards を先に入れる (messages.board_id の FK)。last_message_id は遅延 FK
	if err := bi.flush(); err != nil {
		return err
	}
	for _, r := range msgRows {
		if err := mi.add(r...); err != nil {
			return err
		}
	}
	return mi.flush()
}

// ---------------------------------------------------------------------
// 添付ファイル (行のみ)
// ---------------------------------------------------------------------

func (g *gen) genAttachments(ctx context.Context) error {
	at := g.ins(ctx, "attachments", "container_kind", "container_id", "filename", "disk_directory", "disk_filename", "filesize",
		"content_type", "digest", "digest_algo", "downloads", "author_id", "description", "created_at")
	exts := []struct{ ext, ct string }{{"txt", "text/plain"}, {"png", "image/png"}, {"log", "text/plain"}, {"pdf", "application/pdf"}, {"zip", "application/zip"}}
	for i := range g.n(50000) {
		is := g.issues[g.rng.IntN(len(g.issues))]
		kind, cid := "issue", is.id
		if g.rng.IntN(10) == 0 {
			kind, cid = "project", is.project
		}
		e := exts[g.rng.IntN(len(exts))]
		t := is.created.Add(time.Duration(g.rng.IntN(72)) * time.Hour)
		name := fmt.Sprintf("file%d.%s", i, e.ext)
		sum := sha256.Sum256([]byte(name))
		if err := at.add(kind, cid, name, t.Format("2006/01"), t.Format("060102150405")+"_"+name, 100+g.rng.IntN(5000000),
			e.ct, hex.EncodeToString(sum[:]), "sha256", g.rng.IntN(20), g.pick(g.members[is.project]), nil, ts(t)); err != nil {
			return err
		}
	}
	return at.flush()
}

// API キー (測定ツールの既定値)。
const (
	AdminAPIKey  = "perfadminapikey0000000000000000000000000"
	MemberAPIKey = "perfmemberapikey000000000000000000000000"
)

// genSettings は REST API を有効にし、CSV 出力の上限を 1 万件にして、API キーを作る。
func (g *gen) genSettings(ctx context.Context) error {
	st, err := settings.New(ctx, repository.SettingsStore{DB: g.tx})
	if err != nil {
		return err
	}
	if err := st.Set(ctx, "rest_api_enabled", "1"); err != nil {
		return err
	}
	if err := st.Set(ctx, "issues_export_limit", "10000"); err != nil {
		return err
	}
	var adminID int64
	if err := g.tx.Get(ctx, &adminID, `SELECT principal_id FROM user_accounts WHERE login = 'admin'`); err != nil {
		return err
	}
	tk := g.ins(ctx, "tokens", "user_id", "action", "value", "created_at", "updated_at")
	if err := tk.add(adminID, "api", AdminAPIKey, ts(baseTime), ts(baseTime)); err != nil {
		return err
	}
	if err := tk.add(g.users[0], "api", MemberAPIKey, ts(baseTime), ts(baseTime)); err != nil {
		return err
	}
	return tk.flush()
}

// fixSequences は明示 ID で投入したテーブルの PostgreSQL シーケンスを進める。
func (g *gen) fixSequences(ctx context.Context) error {
	var tables []string
	if err := g.d.Select(ctx, &tables, `SELECT table_name FROM information_schema.columns
WHERE table_schema = current_schema() AND column_name = 'id' AND column_default IS NOT NULL OR (table_schema = current_schema() AND column_name = 'id' AND is_identity = 'YES')`); err != nil {
		return err
	}
	for _, t := range tables {
		if _, err := g.d.Exec(ctx, fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', 'id'), COALESCE((SELECT MAX(id) FROM %s), 0) + 1, false)`, t, t)); err != nil {
			return fmt.Errorf("setval %s: %w", t, err)
		}
	}
	return nil
}

func init() {
	log.SetFlags(log.Ltime)
	log.SetOutput(os.Stderr)
}
