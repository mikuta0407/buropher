// Package testfixtures は Redmine の公式テストフィクスチャ (test/fixtures/*.yml) を
// buropher の新スキーマへ変換して投入するテスト用ローダ。
//
//	d := dbtest.New(t)
//	testfixtures.Load(t, d, "projects", "users", "members", "member_roles", "roles")
//
// 名前は Redmine のフィクスチャ名 (= 旧テーブル名)。外部キーを満たすため依存する
// フィクスチャは自動的に追加され (例: issues → projects, trackers, users ...)、
// 指定順に関係なく依存順で投入される。変換規則は付録 A のマッピングに従う (custom_values.value 等の空文字は NULL):
//   - users.type → principals.kind、ユーザ属性は user_accounts、グループ名は principals.name
//   - users.hashed_password/salt → user_accounts.password_hash ("redmine-sha1$salt$hash")
//   - users.mail_notification → user_notification_settings
//   - projects.lft/rgt は捨て、parent_id から project_closure を構築
//   - enabled_modules → project_modules, projects_trackers → project_trackers
//   - roles.permissions (YAML) → role_permissions, roles.settings → all_trackers / role_permission_trackers
//   - members.user_id → principal_id、mail_notification → user_notified_projects
//   - enumerations → issue_priorities / time_entry_activities / document_categories
//     (type が Enumeration の異常行は捨てる)
//   - issues.lft/rgt は捨て、parent_id から root_id / hier_path を構築
//   - workflows (WorkflowTransition) → workflow_transitions (old_status_id 0 → NULL)
//   - watchers.watchable_type → watchable_kind
//   - user_preferences.others (YAML) → 列 (warn_on_leaving_unsaved 等) と
//     bookmarked_project_ids / recently_used_project_ids → user_project_bookmarks / user_recent_projects
//   - wikis.status は捨てる、boards.last_message_id は messages 投入時に設定する (messages なしなら NULL)
//   - wiki_pages / wiki_content_versions / wiki_contents → wiki_pages + wiki_page_versions
//     (同じ版は wiki_contents が優先、wiki_pages.current_version = wiki_contents.version)
//   - attachments.container_type → container_kind、journals → issue_journals、journal_details → issue_journal_details、
//     comments → news_comments、changes → changeset_files (変換規則は internal/redmineimport と同じ)
//   - repositories.type (Repository::Subversion 等) → scm
//
// ERB の相対日時 (<%= 2.days.ago.to_fs(:db) %> 等) は Load 呼び出し時刻 (UTC) を基準に評価する。
// フィクスチャに無いタイムスタンプは Rails と同様に現在時刻で埋める。
package testfixtures

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// fixtureDef は 1 フィクスチャの変換定義。
type fixtureDef struct {
	deps   []string
	tables []string // ResetSequence 対象 (id 列を持つテーブル)
	load   func(c *loadCtx, rows []row) error
}

// order は投入順 (依存順)。
var order = []string{
	"issue_statuses", "trackers", "enumerations", "users", "email_addresses", "groups_users",
	"projects", "enabled_modules", "projects_trackers", "versions", "issue_categories",
	"roles", "members", "member_roles", "issues", "workflows", "watchers",
	"news", "user_preferences", "wikis", "boards", "repositories",
	"custom_fields", "custom_fields_projects", "custom_fields_trackers", "custom_values",
	"journals", "journal_details", "time_entries", "queries", "issue_relations",
	"documents", "wiki_pages", "wiki_content_versions", "wiki_contents", "messages", "comments",
	"changesets", "changes", "attachments",
}

var defs = map[string]fixtureDef{
	"issue_statuses":    {tables: []string{"issue_statuses"}, load: loadIssueStatuses},
	"trackers":          {deps: []string{"issue_statuses"}, tables: []string{"trackers"}, load: loadTrackers},
	"enumerations":      {tables: []string{"issue_priorities", "time_entry_activities", "document_categories"}, load: loadEnumerations},
	"users":             {tables: []string{"principals"}, load: loadUsers},
	"email_addresses":   {deps: []string{"users"}, tables: []string{"email_addresses"}, load: loadEmailAddresses},
	"groups_users":      {deps: []string{"users"}, load: loadGroupsUsers},
	"projects":          {deps: []string{"users"}, tables: []string{"projects"}, load: loadProjects},
	"enabled_modules":   {deps: []string{"projects"}, tables: []string{"project_modules"}, load: loadEnabledModules},
	"projects_trackers": {deps: []string{"projects", "trackers"}, load: loadProjectsTrackers},
	"versions":          {deps: []string{"projects"}, tables: []string{"versions"}, load: loadVersions},
	"issue_categories":  {deps: []string{"projects", "users"}, tables: []string{"issue_categories"}, load: loadIssueCategories},
	"roles":             {deps: []string{"trackers"}, tables: []string{"roles"}, load: loadRoles},
	"members":           {deps: []string{"users", "projects"}, tables: []string{"members"}, load: loadMembers},
	"member_roles":      {deps: []string{"members", "roles"}, tables: []string{"member_roles"}, load: loadMemberRoles},
	"issues": {deps: []string{"projects", "trackers", "issue_statuses", "enumerations", "users", "issue_categories", "versions"},
		tables: []string{"issues"}, load: loadIssues},
	"workflows":              {deps: []string{"trackers", "roles", "issue_statuses"}, tables: []string{"workflow_transitions"}, load: loadWorkflows},
	"watchers":               {deps: []string{"users"}, tables: []string{"watchers"}, load: loadWatchers},
	"news":                   {deps: []string{"projects", "users"}, tables: []string{"news"}, load: loadNews},
	"user_preferences":       {deps: []string{"users", "projects"}, load: loadUserPreferences},
	"wikis":                  {deps: []string{"projects"}, tables: []string{"wikis"}, load: loadWikis},
	"boards":                 {deps: []string{"projects"}, tables: []string{"boards"}, load: loadBoards},
	"repositories":           {deps: []string{"projects"}, tables: []string{"repositories"}, load: loadRepositories},
	"custom_fields":          {tables: []string{"custom_fields"}, load: loadCustomFields},
	"custom_fields_projects": {deps: []string{"custom_fields", "projects"}, load: loadCustomFieldsProjects},
	"custom_fields_trackers": {deps: []string{"custom_fields", "trackers"}, load: loadCustomFieldsTrackers},
	"custom_values":          {deps: []string{"custom_fields"}, tables: []string{"custom_values"}, load: loadCustomValues},
	"journals":               {deps: []string{"issues", "users"}, tables: []string{"issue_journals"}, load: loadJournals},
	"journal_details":        {deps: []string{"journals"}, tables: []string{"issue_journal_details"}, load: loadJournalDetails},
	"time_entries": {deps: []string{"projects", "users", "issues", "enumerations"},
		tables: []string{"time_entries"}, load: loadTimeEntries},
	"queries":         {deps: []string{"projects", "users"}, tables: []string{"queries"}, load: loadQueries},
	"issue_relations": {deps: []string{"issues"}, tables: []string{"issue_relations"}, load: loadIssueRelations},
	// テキスト系コンテンツ (convert_text.go)
	"documents":             {deps: []string{"projects", "enumerations"}, tables: []string{"documents"}, load: loadDocuments},
	"wiki_pages":            {deps: []string{"wikis"}, tables: []string{"wiki_pages"}, load: loadWikiPages},
	"wiki_content_versions": {deps: []string{"wiki_pages", "users"}, tables: []string{"wiki_page_versions"}, load: loadWikiContentVersions},
	"wiki_contents":         {deps: []string{"wiki_pages", "users"}, tables: []string{"wiki_page_versions"}, load: loadWikiContents},
	"messages":              {deps: []string{"boards", "users"}, tables: []string{"messages"}, load: loadMessages},
	"comments":              {deps: []string{"news", "users"}, tables: []string{"news_comments"}, load: loadComments},
	"changesets":            {deps: []string{"repositories", "users"}, tables: []string{"changesets"}, load: loadChangesets},
	"changes":               {deps: []string{"changesets"}, tables: []string{"changeset_files"}, load: loadChanges},
	"attachments": {deps: []string{"users", "projects", "versions", "issues", "wiki_pages", "messages", "news", "documents"},
		tables: []string{"attachments"}, load: loadAttachments},
}

// Supported は変換に対応しているフィクスチャ名を投入順で返す。
func Supported() []string { return slices.Clone(order) }

// All は対応している全フィクスチャ名 (Supported と同じ)。Load(t, d, All()...) で全投入する。
func All() []string { return Supported() }

type loadCtx struct {
	ctx    context.Context
	tx     *db.Tx
	now    time.Time
	loaded map[string]bool
	// projectDefaultVersions は versions 投入後に設定する projects.default_version_id。
	projectDefaultVersions map[int64]int64
	// userNotify は members.mail_notification の変換に使う principals.kind。
	kinds map[int64]string
	// customFields は投入したカスタムフィールドの id (基底クラスの異常行は投入しない)。
	customFields map[int64]bool
}

func (c *loadCtx) exec(q string, args ...any) error {
	if _, err := c.tx.Exec(c.ctx, q, args...); err != nil {
		return fmt.Errorf("%w\n  query: %s\n  args: %v", err, q, args)
	}
	return nil
}

// Resolve は依存フィクスチャを含めた投入対象を投入順で返す。未知の名前はエラー。
func Resolve(names ...string) ([]string, error) {
	want := map[string]bool{}
	var visit func(n string) error
	visit = func(n string) error {
		def, ok := defs[n]
		if !ok {
			return fmt.Errorf("testfixtures: fixture %q is not supported (supported: %s)", n, strings.Join(order, ", "))
		}
		if want[n] {
			return nil
		}
		want[n] = true
		for _, d := range def.deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	var out []string
	for _, n := range order {
		if want[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

// Load は指定フィクスチャ (と依存フィクスチャ) を d に投入する。失敗時は t.Fatal。
func Load(t testing.TB, d *db.DB, names ...string) {
	t.Helper()
	if err := LoadContext(context.Background(), d, time.Now().UTC(), names...); err != nil {
		t.Fatal(err)
	}
}

// LoadAt は ERB の相対日時と既定タイムスタンプの基準時刻を now に固定して投入する。
// Redmine 側を時刻固定 (COMPAT_FROZEN_TIME) で動かした結果と比較するテストで使う。
func LoadAt(t testing.TB, d *db.DB, now time.Time, names ...string) {
	t.Helper()
	if err := LoadContext(context.Background(), d, now, names...); err != nil {
		t.Fatal(err)
	}
}

// LoadContext は Load のエラーを返す版。now は ERB の相対日時と既定タイムスタンプの基準時刻。
func LoadContext(ctx context.Context, d *db.DB, now time.Time, names ...string) error {
	list, err := Resolve(names...)
	if err != nil {
		return err
	}
	now = now.UTC().Truncate(time.Second)
	return d.WithTx(ctx, func(tx *db.Tx) error {
		if err := tx.DeferConstraints(ctx); err != nil {
			return err
		}
		c := &loadCtx{ctx: ctx, tx: tx, now: now, loaded: map[string]bool{},
			projectDefaultVersions: map[int64]int64{}, kinds: map[int64]string{}, customFields: map[int64]bool{}}
		for _, n := range list {
			c.loaded[n] = true
		}
		for _, n := range list {
			rows, err := readFixture(n, now)
			if err != nil {
				return err
			}
			if err := defs[n].load(c, rows); err != nil {
				return fmt.Errorf("testfixtures: load %s: %w", n, err)
			}
		}
		for pid, vid := range c.projectDefaultVersions {
			if err := c.exec(`UPDATE projects SET default_version_id = ? WHERE id = ?`, vid, pid); err != nil {
				return err
			}
		}
		var tables []string
		for _, n := range list {
			tables = append(tables, defs[n].tables...)
		}
		sort.Strings(tables)
		for _, tbl := range slices.Compact(tables) {
			if err := tx.Dialect().ResetSequence(ctx, tx, tbl); err != nil {
				return fmt.Errorf("testfixtures: reset sequence %s: %w", tbl, err)
			}
		}
		return nil
	})
}
