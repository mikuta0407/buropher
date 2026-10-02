// Package verify はインポート結果をエクスポートアーカイブと突き合わせて検証する
// (`buropher redmine verify`)。
//
// 検証項目:
//   - テーブル別件数と ID の突き合わせ(ID を保持するテーブルは、アーカイブにない ID が
//     新 DB にあれば失敗。アーカイブにあって新 DB にない ID はインポート時の破棄として報告)
//   - 階層: projects の closure と旧 lft/rgt の祖先関係の一致、issues の旧 lft 順と
//     新 ORDER BY root_id, hier_path の一致(旧入れ子集合が parent_id と矛盾する場合は警告のみ)
//   - 添付ファイルの存在・サイズ・digest(任意)
//   - サンプルユーザーのパスワード照合(任意)
package verify

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/archive"
)

// Options は検証の設定。
type Options struct {
	// FilesDir は buropher の添付保存先。空ならファイル検証をしない。
	FilesDir string
	// Digests は添付の digest まで照合する(時間がかかる)。
	Digests bool
	// Passwords はログイン名 → 平文パスワード。各ユーザーのハッシュ照合を行う。
	Passwords map[string]string
	// StrictCounts はアーカイブにあって新 DB にない行(インポート時の破棄)も失敗にする。
	StrictCounts bool
}

// Report は検証結果。
type Report struct {
	Archive  string      `json:"archive"`
	Counts   []*CountRow `json:"counts"`
	Checks   []Check     `json:"checks"`
	Warnings []string    `json:"warnings"`
}

// CountRow はテーブル 1 組の件数比較。
type CountRow struct {
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	SourceRows int64    `json:"source_rows"`
	TargetRows int64    `json:"target_rows"`
	Missing    int64    `json:"missing"`
	Extra      int64    `json:"extra"`
	MissingIDs []string `json:"missing_samples,omitempty"`
	ExtraIDs   []string `json:"extra_samples,omitempty"`
}

// Check は 1 項目の検証結果。
type Check struct {
	Name   string   `json:"name"`
	OK     bool     `json:"ok"`
	Detail []string `json:"detail,omitempty"`
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

func (r *Report) add(c Check) { r.Checks = append(r.Checks, c) }

func (r *Report) warnf(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// WriteJSON は JSON で書き出す。
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText は人が読む形式で書き出す。
func (r *Report) WriteText(w io.Writer) {
	fmt.Fprintf(w, "Redmine import verification: %s\n\n", r.Archive)
	fmt.Fprintf(w, "%-24s %-28s %8s %8s %8s %6s\n", "source", "target", "source", "target", "missing", "extra")
	for _, c := range r.Counts {
		fmt.Fprintf(w, "%-24s %-28s %8d %8d %8d %6d", c.Source, c.Target, c.SourceRows, c.TargetRows, c.Missing, c.Extra)
		if len(c.MissingIDs) > 0 {
			fmt.Fprintf(w, "  missing e.g. %s", strings.Join(c.MissingIDs, ","))
		}
		if len(c.ExtraIDs) > 0 {
			fmt.Fprintf(w, "  extra e.g. %s", strings.Join(c.ExtraIDs, ","))
		}
		fmt.Fprintln(w)
	}
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
	if len(r.Warnings) > 0 {
		fmt.Fprintln(w, "\nwarnings:")
		for _, s := range r.Warnings {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
}

const maxSamples = 10

// mapping はソーステーブルと移行先テーブルの対応。
type mapping struct {
	source, target string
	// filter はソース行のうち target に対応するもの(nil なら全行)。
	filter func(archive.Row) bool
	// where は target 側の条件(SQL)。
	where string
	// pair は ID を持たない結合テーブル(件数のみ比較)。
	pair bool
	// generated は新 DB 側で生成されうる行(extra を失敗にしない)。
	generated bool
	// idCol は target の ID 列(既定 id)。
	idCol string
}

func typeIs(col string, vals ...string) func(archive.Row) bool {
	return func(r archive.Row) bool {
		s, _ := r[col].(string)
		for _, v := range vals {
			if s == v {
				return true
			}
		}
		return false
	}
}

var mappings = []mapping{
	{source: "settings", target: "settings", pair: true},
	{source: "auth_sources", target: "auth_sources"},
	{source: "users", target: "principals", generated: true},
	{source: "users", target: "user_accounts", filter: typeIs("type", "User", "AnonymousUser"), generated: true, idCol: "principal_id"},
	{source: "email_addresses", target: "email_addresses"},
	{source: "groups_users", target: "group_users", pair: true},
	{source: "tokens", target: "tokens", filter: typeIs("action", "api", "feeds", "autologin", "recovery", "register")},
	{source: "tokens", target: "twofa_backup_codes", filter: typeIs("action", "twofa_backup_code"), pair: true},
	{source: "issue_statuses", target: "issue_statuses"},
	{source: "trackers", target: "trackers"},
	{source: "enumerations", target: "issue_priorities", filter: typeIs("type", "IssuePriority")},
	{source: "enumerations", target: "document_categories", filter: typeIs("type", "DocumentCategory")},
	{source: "enumerations", target: "time_entry_activities", filter: typeIs("type", "TimeEntryActivity")},
	{source: "roles", target: "roles"},
	{source: "roles_managed_roles", target: "roles_managed_roles", pair: true},
	{source: "projects", target: "projects"},
	{source: "enabled_modules", target: "project_modules"},
	{source: "projects_trackers", target: "project_trackers", pair: true},
	{source: "custom_fields", target: "custom_fields"},
	{source: "custom_field_enumerations", target: "custom_field_enumerations"},
	{source: "custom_fields_projects", target: "custom_fields_projects", pair: true},
	{source: "custom_fields_roles", target: "custom_fields_roles", pair: true},
	{source: "custom_fields_trackers", target: "custom_fields_trackers", pair: true},
	{source: "members", target: "members"},
	{source: "member_roles", target: "member_roles"},
	{source: "versions", target: "versions"},
	{source: "issue_categories", target: "issue_categories"},
	{source: "workflows", target: "workflow_transitions", filter: typeIs("type", "WorkflowTransition")},
	{source: "workflows", target: "workflow_field_rules", filter: typeIs("type", "WorkflowPermission")},
	{source: "issues", target: "issues"},
	{source: "issue_relations", target: "issue_relations"},
	{source: "journals", target: "issue_journals"},
	{source: "journal_details", target: "issue_journal_details"},
	{source: "time_entries", target: "time_entries"},
	{source: "documents", target: "documents"},
	{source: "news", target: "news"},
	{source: "comments", target: "news_comments"},
	{source: "boards", target: "boards"},
	{source: "messages", target: "messages"},
	{source: "wikis", target: "wikis"},
	{source: "wiki_pages", target: "wiki_pages"},
	{source: "wiki_content_versions", target: "wiki_page_versions", generated: true},
	{source: "wiki_redirects", target: "wiki_redirects"},
	{source: "repositories", target: "repositories"},
	{source: "changesets", target: "changesets"},
	{source: "changes", target: "changeset_files"},
	{source: "changeset_parents", target: "changeset_parents", pair: true},
	{source: "changesets_issues", target: "changesets_issues", pair: true},
	{source: "queries", target: "queries"},
	{source: "queries_roles", target: "queries_roles", pair: true},
	{source: "custom_values", target: "custom_values"},
	{source: "attachments", target: "attachments"},
	{source: "watchers", target: "watchers"},
	{source: "reactions", target: "reactions"},
	{source: "oauth_applications", target: "oauth_applications"},
	{source: "oauth_access_grants", target: "oauth_access_grants"},
	{source: "oauth_access_tokens", target: "oauth_access_tokens"},
}

// srcData はアーカイブから集めた検証用データ。
type srcData struct {
	ids      map[string]map[int64]bool // "source>target" → ID
	counts   map[string]int64
	projects []nested
	issues   []nested
	attach   []attachment
	missing  map[string]bool
}

type nested struct {
	id, parent, root, lft, rgt int64
}

type attachment struct {
	id       int64
	path     string
	size     int64
	digest   string
	hasFile  bool
	archived bool
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

func loadSource(archivePath string) (*srcData, *archive.Manifest, error) {
	r, err := archive.Open(archivePath)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	m := r.Manifest()
	sd := &srcData{ids: map[string]map[int64]bool{}, counts: map[string]int64{}, missing: map[string]bool{}}
	for _, mf := range m.MissingFiles {
		sd.missing[mf.Path] = true
	}
	byTable := map[string][]mapping{}
	for _, mp := range mappings {
		byTable[mp.source] = append(byTable[mp.source], mp)
	}
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if e.Kind != archive.KindTable {
			continue
		}
		rows := e.Rows()
		for rows.Next() {
			row := rows.Row()
			for _, mp := range byTable[e.Name] {
				if mp.filter != nil && !mp.filter(row) {
					continue
				}
				k := mp.source + ">" + mp.target
				sd.counts[k]++
				if !mp.pair {
					if sd.ids[k] == nil {
						sd.ids[k] = map[int64]bool{}
					}
					sd.ids[k][toInt(row["id"])] = true
				}
			}
			switch e.Name {
			case "projects":
				sd.projects = append(sd.projects, nested{id: toInt(row["id"]), parent: toInt(row["parent_id"]), lft: toInt(row["lft"]), rgt: toInt(row["rgt"])})
			case "issues":
				sd.issues = append(sd.issues, nested{id: toInt(row["id"]), parent: toInt(row["parent_id"]), root: toInt(row["root_id"]), lft: toInt(row["lft"]), rgt: toInt(row["rgt"])})
			case "attachments":
				dir, _ := row["disk_directory"].(string)
				fn, _ := row["disk_filename"].(string)
				p := fn
				if dir != "" {
					p = dir + "/" + fn
				}
				d, _ := row["digest"].(string)
				sd.attach = append(sd.attach, attachment{id: toInt(row["id"]), path: p, size: toInt(row["filesize"]), digest: strings.ToLower(d)})
			}
		}
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return sd, m, nil
}

// Verify はアーカイブと新 DB を突き合わせる。
func Verify(ctx context.Context, d *db.DB, archivePath string, opt Options) (*Report, error) {
	rep := &Report{Archive: archivePath, Counts: []*CountRow{}, Checks: []Check{}, Warnings: []string{}}
	sd, _, err := loadSource(archivePath)
	if err != nil {
		return rep, err
	}
	if err := verifyCounts(ctx, d, sd, opt, rep); err != nil {
		return rep, err
	}
	if err := verifyProjects(ctx, d, sd, rep); err != nil {
		return rep, err
	}
	if err := verifyIssues(ctx, d, sd, rep); err != nil {
		return rep, err
	}
	if opt.FilesDir != "" {
		if err := verifyFiles(ctx, d, sd, opt, rep); err != nil {
			return rep, err
		}
	}
	if len(opt.Passwords) > 0 {
		if err := verifyPasswords(ctx, d, opt.Passwords, rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func verifyCounts(ctx context.Context, d *db.DB, sd *srcData, opt Options, rep *Report) error {
	c := Check{Name: "row counts and ids", OK: true}
	for _, mp := range mappings {
		k := mp.source + ">" + mp.target
		row := &CountRow{Source: mp.source, Target: mp.target, SourceRows: sd.counts[k]}
		if err := d.Get(ctx, &row.TargetRows, "SELECT COUNT(*) FROM "+mp.target); err != nil {
			return err
		}
		if mp.pair {
			if row.TargetRows < row.SourceRows {
				row.Missing = row.SourceRows - row.TargetRows
			} else if row.TargetRows > row.SourceRows {
				row.Extra = row.TargetRows - row.SourceRows
			}
		} else {
			var ids []int64
			col := mp.idCol
			if col == "" {
				col = "id"
			}
			if err := d.Select(ctx, &ids, "SELECT "+col+" FROM "+mp.target); err != nil {
				return err
			}
			got := make(map[int64]bool, len(ids))
			for _, id := range ids {
				got[id] = true
				if !sd.ids[k][id] {
					row.Extra++
					if len(row.ExtraIDs) < maxSamples {
						row.ExtraIDs = append(row.ExtraIDs, fmt.Sprint(id))
					}
				}
			}
			srcIDs := make([]int64, 0, len(sd.ids[k]))
			for id := range sd.ids[k] {
				srcIDs = append(srcIDs, id)
			}
			sort.Slice(srcIDs, func(i, j int) bool { return srcIDs[i] < srcIDs[j] })
			for _, id := range srcIDs {
				if !got[id] {
					row.Missing++
					if len(row.MissingIDs) < maxSamples {
						row.MissingIDs = append(row.MissingIDs, fmt.Sprint(id))
					}
				}
			}
		}
		if row.Extra > 0 && !mp.generated && !mp.pair {
			c.OK = false
			c.Detail = append(c.Detail, fmt.Sprintf("%s: %d rows not in the archive (%s)", mp.target, row.Extra, strings.Join(row.ExtraIDs, ",")))
		}
		if row.Missing > 0 {
			if opt.StrictCounts {
				c.OK = false
				c.Detail = append(c.Detail, fmt.Sprintf("%s -> %s: %d rows missing", mp.source, mp.target, row.Missing))
			}
		}
		rep.Counts = append(rep.Counts, row)
	}
	rep.add(c)
	return nil
}

// verifyProjects は project_closure の祖先関係が旧 lft/rgt と一致するかを確認する。
func verifyProjects(ctx context.Context, d *db.DB, sd *srcData, rep *Report) error {
	c := Check{Name: "project hierarchy (closure vs. old lft/rgt)", OK: true}
	var rows []struct {
		A     int64 `db:"ancestor_id"`
		D     int64 `db:"descendant_id"`
		Depth int64 `db:"depth"`
	}
	if err := d.Select(ctx, &rows, "SELECT ancestor_id, descendant_id, depth FROM project_closure WHERE depth > 0"); err != nil {
		return err
	}
	closure := map[[2]int64]bool{}
	for _, r := range rows {
		closure[[2]int64{r.A, r.D}] = true
	}
	if !nestedConsistent(sd.projects, func(n nested) int64 { return 0 }) {
		rep.warnf("source projects nested set (lft/rgt) is inconsistent with parent_id; hierarchy was rebuilt from parent_id and the lft/rgt comparison is skipped")
		rep.add(c)
		return nil
	}
	imported := map[int64]bool{}
	var ids []int64
	if err := d.Select(ctx, &ids, "SELECT id FROM projects"); err != nil {
		return err
	}
	for _, id := range ids {
		imported[id] = true
	}
	for _, a := range sd.projects {
		for _, x := range sd.projects {
			if a.id == x.id || !imported[a.id] || !imported[x.id] {
				continue
			}
			old := a.lft < x.lft && x.rgt < a.rgt
			if old != closure[[2]int64{a.id, x.id}] {
				c.OK = false
				if len(c.Detail) < 20 {
					c.Detail = append(c.Detail, fmt.Sprintf("project %d ancestor of %d: lft/rgt %v, closure %v", a.id, x.id, old, !old))
				}
			}
		}
	}
	rep.add(c)
	return nil
}

// nestedConsistent は入れ子集合から導いた親が parent_id と一致するかを返す。
// scope は集合の単位(issues は root_id、projects は全体)。
func nestedConsistent(ns []nested, scope func(nested) int64) bool {
	groups := map[int64][]nested{}
	for _, n := range ns {
		if n.lft == 0 && n.rgt == 0 {
			return false
		}
		groups[scope(n)] = append(groups[scope(n)], n)
	}
	for _, g := range groups {
		for _, n := range g {
			var best *nested
			for i := range g {
				p := &g[i]
				if p.id != n.id && p.lft < n.lft && n.rgt < p.rgt && (best == nil || p.lft > best.lft) {
					best = p
				}
			}
			parent := int64(0)
			if best != nil {
				parent = best.id
			}
			if parent != n.parent {
				return false
			}
		}
	}
	return true
}

// verifyIssues は旧 ORDER BY root_id, lft と新 ORDER BY root_id, hier_path が一致するかを確認する。
func verifyIssues(ctx context.Context, d *db.DB, sd *srcData, rep *Report) error {
	c := Check{Name: "issue hierarchy (old lft order vs. ORDER BY root_id, hier_path)", OK: true}
	var rows []struct {
		ID     int64  `db:"id"`
		Root   int64  `db:"root_id"`
		Parent *int64 `db:"parent_id"`
		Path   string `db:"hier_path"`
	}
	if err := d.Select(ctx, &rows, "SELECT id, root_id, parent_id, hier_path FROM issues ORDER BY root_id, hier_path"); err != nil {
		return err
	}
	// hier_path 自体の整合(祖先列 + 自身)
	pathOf := map[int64]string{}
	for _, r := range rows {
		pathOf[r.ID] = r.Path
	}
	for _, r := range rows {
		want := fmt.Sprintf("%010d/", r.ID)
		if r.Parent != nil {
			want = pathOf[*r.Parent] + want
		}
		if r.Path != want {
			c.OK = false
			if len(c.Detail) < 20 {
				c.Detail = append(c.Detail, fmt.Sprintf("issue %d: hier_path %q, want %q", r.ID, r.Path, want))
			}
		}
	}
	if !nestedConsistent(sd.issues, func(n nested) int64 { return n.root }) {
		rep.warnf("source issues nested set (root_id/lft/rgt) is inconsistent with parent_id; the lft order comparison is skipped")
		rep.add(c)
		return nil
	}
	imported := map[int64]bool{}
	var newOrder []int64
	for _, r := range rows {
		imported[r.ID] = true
		newOrder = append(newOrder, r.ID)
	}
	old := make([]nested, 0, len(sd.issues))
	for _, n := range sd.issues {
		if imported[n.id] {
			old = append(old, n)
		}
	}
	sort.SliceStable(old, func(i, j int) bool {
		if old[i].root != old[j].root {
			return old[i].root < old[j].root
		}
		return old[i].lft < old[j].lft
	})
	if len(old) != len(newOrder) {
		c.OK = false
		c.Detail = append(c.Detail, fmt.Sprintf("issue count differs: %d vs %d", len(old), len(newOrder)))
	} else {
		for i := range old {
			if old[i].id != newOrder[i] {
				c.OK = false
				c.Detail = append(c.Detail, fmt.Sprintf("order differs at position %d: old %d, new %d", i, old[i].id, newOrder[i]))
				break
			}
		}
	}
	rep.add(c)
	return nil
}

func verifyFiles(ctx context.Context, d *db.DB, sd *srcData, opt Options, rep *Report) error {
	c := Check{Name: "attachment files", OK: true}
	var rows []struct {
		ID     int64   `db:"id"`
		Dir    *string `db:"disk_directory"`
		File   string  `db:"disk_filename"`
		Size   int64   `db:"filesize"`
		Digest *string `db:"digest"`
		Algo   *string `db:"digest_algo"`
	}
	if err := d.Select(ctx, &rows, "SELECT id, disk_directory, disk_filename, filesize, digest, digest_algo FROM attachments ORDER BY id"); err != nil {
		return err
	}
	var knownMissing, missing, badSize, badDigest int64
	for _, r := range rows {
		rel := r.File
		if r.Dir != nil && *r.Dir != "" {
			rel = *r.Dir + "/" + r.File
		}
		p := filepath.Join(opt.FilesDir, filepath.FromSlash(rel))
		st, err := os.Stat(p)
		if err != nil {
			if sd.missing[rel] {
				knownMissing++
				continue
			}
			missing++
			c.OK = false
			if len(c.Detail) < 20 {
				c.Detail = append(c.Detail, fmt.Sprintf("attachment %d: file %s is missing", r.ID, rel))
			}
			continue
		}
		if st.Size() != r.Size {
			badSize++
			rep.warnf("attachment %d: size %d on disk, %d in the database", r.ID, st.Size(), r.Size)
		}
		if opt.Digests && r.Digest != nil && r.Algo != nil {
			var h hash.Hash = sha256.New()
			if *r.Algo == "md5" {
				h = md5.New()
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil {
				return err
			}
			if hex.EncodeToString(h.Sum(nil)) != *r.Digest {
				badDigest++
				rep.warnf("attachment %d: digest mismatch (%s)", r.ID, rel)
			}
		}
	}
	if knownMissing > 0 {
		rep.warnf("%d attachments refer to files that were already missing in Redmine (manifest missing_files)", knownMissing)
	}
	c.Detail = append(c.Detail, fmt.Sprintf("%d attachments, %d missing, %d known missing in source, %d size mismatches, %d digest mismatches",
		len(rows), missing, knownMissing, badSize, badDigest))
	rep.add(c)
	return nil
}

func verifyPasswords(ctx context.Context, d *db.DB, pws map[string]string, rep *Report) error {
	c := Check{Name: "sample passwords", OK: true}
	logins := make([]string, 0, len(pws))
	for l := range pws {
		logins = append(logins, l)
	}
	sort.Strings(logins)
	for _, login := range logins {
		var hashes []*string
		if err := d.Select(ctx, &hashes, "SELECT password_hash FROM user_accounts WHERE lower(login) = lower(?)", login); err != nil {
			return err
		}
		if len(hashes) != 1 || hashes[0] == nil {
			c.OK = false
			c.Detail = append(c.Detail, fmt.Sprintf("%s: user or password hash not found", login))
			continue
		}
		ok, err := password.Verify(*hashes[0], pws[login])
		if err != nil || !ok {
			c.OK = false
			c.Detail = append(c.Detail, fmt.Sprintf("%s: password does not verify (%v)", login, err))
			continue
		}
		c.Detail = append(c.Detail, login+": ok")
	}
	rep.add(c)
	return nil
}

// elapsed は小さなユーティリティ(CLI 用)。
func elapsed(start time.Time) time.Duration { return time.Since(start).Round(time.Millisecond) }
