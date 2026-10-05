// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package importer は Redmine エクスポートアーカイブ(internal/redmineimport/archive)を
// buropher の新スキーマへ取り込む(`buropher redmine import`)。
//
// 方針(詳細は docs/import.md):
//   - 対象 DB はマイグレーション済みかつ空であること。全体を 1 トランザクションで行い、
//     失敗時は何も残さない(DryRun は最後にロールバック)。
//   - ID は Redmine のものを保持する。日時はアーカイブの source.timezone で解釈して UTC にする。
//   - FK 依存順にテーブルを変換し、参照先のない行は破棄または補完してレポートに記録する。
//   - 最後に導出値(カウンタ・position_name 等)の再計算、シーケンス更新、整合性チェックを行う。
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	// ソース TZ の解釈を OS の tzdata に依存させない
	_ "time/tzdata"

	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
)

// Options はインポートの設定。
type Options struct {
	// CipherKey は Redmine の database_cipher_key(暗号化列の復号用)。
	// 空の場合、暗号化された TOTP 鍵は 2FA リセット、LDAP/リポジトリのパスワードは空にする。
	CipherKey string
	// NewCipherKey は buropher の秘密鍵(server.secret_key)。秘密値を secretbox 形式で再暗号化する。
	// 空の場合、平文の秘密値は平文のまま保存し(警告)、暗号化値を復号できた場合はエラーにする。
	NewCipherKey string
	// FilesDir は添付ファイルの保存先(buropher の storage.attachments_path)。空ならファイルはコピーしない。
	FilesDir string
	// SourceFilesDir はアーカイブに添付が含まれない場合(--no-files で書き出した場合)のファイル元
	// (Redmine の files ディレクトリ)。
	SourceFilesDir string
	// TempDir はテーブル展開用の一時ディレクトリの親(既定はアーカイブと同じディレクトリ)。
	TempDir string
	// DryRun は変換・検証のみ行い、最後にロールバックする(ファイルもコピーしない)。
	DryRun bool
	// Now は有効期限判定(トークン等)の基準時刻。ゼロなら現在時刻。
	Now time.Time
	// Logger は進捗ログ(nil なら出力しない)。
	Logger *slog.Logger
}

// ErrNotEmpty は対象 DB が空でない。
var ErrNotEmpty = errors.New("importer: target database is not empty (import requires a freshly migrated database; do not run `buropher init`)")

// Run はアーカイブを d に取り込み、レポートを返す。エラー時もそれまでのレポートを返す。
func Run(ctx context.Context, d *db.DB, archivePath string, opt Options) (*Report, error) {
	rep := newReport()
	rep.Archive = archivePath
	rep.DryRun = opt.DryRun
	rep.Dialect = string(d.Dialect().Name())
	rep.StartedAt = time.Now().UTC()
	defer func() { rep.FinishedAt = time.Now().UTC() }()

	log := opt.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}

	if err := checkTarget(ctx, d); err != nil {
		return rep, err
	}
	var box *secretbox.Box
	if opt.NewCipherKey != "" {
		b, err := secretbox.New(opt.NewCipherKey)
		if err != nil {
			return rep, err
		}
		box = b
	}

	tempDir := opt.TempDir
	if tempDir == "" {
		tempDir = filepath.Dir(archivePath)
	}
	stageDir := ""
	if opt.FilesDir != "" && !opt.DryRun {
		if err := os.MkdirAll(opt.FilesDir, 0o755); err != nil {
			return rep, err
		}
		s, err := os.MkdirTemp(opt.FilesDir, ".buropher-import-")
		if err != nil {
			return rep, err
		}
		stageDir = s
		defer os.RemoveAll(stageDir)
	}

	log.Info("reading archive", "path", archivePath)
	src, err := openSource(ctx, archivePath, tempDir, stageDir)
	if err != nil {
		return rep, err
	}
	defer src.close()
	m := src.manifest
	rep.SourceTimezone = m.Source.Timezone
	rep.SourceDBKind = m.Source.DBKind
	loc, err := time.LoadLocation(m.Source.Timezone)
	if err != nil || m.Source.Timezone == "" {
		return rep, fmt.Errorf("importer: invalid source timezone %q in manifest: %w", m.Source.Timezone, err)
	}
	if m.Source.AcceptanceForced {
		rep.warnf("the archive was exported with --force (acceptance check failed); results may be incomplete")
	}
	for _, p := range m.PluginMigrations {
		rep.warnf("plugin %q data was not exported and is not imported", p.Plugin)
	}

	tx, err := d.Begin(ctx)
	if err != nil {
		return rep, fmt.Errorf("importer: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := tx.DeferConstraints(ctx); err != nil {
		return rep, err
	}

	im := &imp{
		ctx: ctx, tx: tx, opt: opt, src: src, rep: rep, log: log, box: box,
		tz:  &tzconv{loc: loc},
		now: opt.Now.UTC(),
	}
	im.nowStr = db.FormatTime(im.now)
	im.initState()

	if err := im.run(); err != nil {
		return rep, err
	}
	if im.tz.ambiguous > 0 {
		rep.warnf("%d ambiguous local times (DST) were resolved to the earlier instant (e.g. %v)", im.tz.ambiguous, im.tz.ambSample)
	}
	if im.tz.nonexistent > 0 {
		rep.warnf("%d non-existent local times (DST gap) were shifted forward (e.g. %v)", im.tz.nonexistent, im.tz.nonSample)
	}
	if !rep.OK() {
		return rep, errors.New("importer: integrity checks failed; nothing was imported")
	}
	if opt.DryRun {
		log.Info("dry run: rolling back")
		return rep, nil
	}
	if err := tx.Commit(); err != nil {
		return rep, fmt.Errorf("importer: commit: %w", err)
	}
	committed = true
	rep.Committed = true
	if err := im.finishFiles(); err != nil {
		return rep, fmt.Errorf("importer: database committed, but moving attachment files failed: %w", err)
	}
	log.Info("import finished")
	return rep, nil
}

// checkTarget はマイグレーション済みかつ空であることを確認する。
func checkTarget(ctx context.Context, d *db.DB) error {
	pending, err := db.HasPending(ctx, d)
	if err != nil {
		return fmt.Errorf("importer: cannot read migration status (run `buropher migrate` first): %w", err)
	}
	if pending {
		return errors.New("importer: target database has pending migrations (run `buropher migrate` first)")
	}
	for _, t := range []string{"principals", "projects", "settings", "issues", "roles", "trackers"} {
		var n int64
		if err := d.Get(ctx, &n, "SELECT COUNT(*) FROM "+t); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: table %s has %d rows", ErrNotEmpty, t, n)
		}
	}
	return nil
}

// imp は 1 回のインポートの作業状態。
type imp struct {
	lastIDs []lastIDFix
	ctx     context.Context
	tx      *db.Tx
	opt     Options
	src     *source
	rep     *Report
	log     *slog.Logger
	box     *secretbox.Box
	tz      *tzconv
	now     time.Time
	nowStr  string

	st state
}

// step は 1 段階の処理。
type step struct {
	name string
	fn   func() error
}

func (im *imp) run() error {
	steps := []step{
		{"settings", im.importSettings},
		{"auth_sources", im.importAuthSources},
		{"users", im.importUsers},
		{"email_addresses", im.importEmailAddresses},
		{"groups_users", im.importGroupsUsers},
		{"tokens", im.importTokens},
		{"issue_statuses", im.importIssueStatuses},
		{"trackers", im.importTrackers},
		{"enumerations", im.importEnumerations},
		{"roles", im.importRoles},
		{"roles_managed_roles", im.importRolesManagedRoles},
		{"projects", im.importProjects},
		{"enabled_modules", im.importEnabledModules},
		{"projects_trackers", im.importProjectsTrackers},
		{"time_entry_activities", im.importActivities},
		{"custom_fields", im.importCustomFields},
		{"custom_field_enumerations", im.importCustomFieldEnumerations},
		{"custom_fields_joins", im.importCustomFieldJoins},
		{"members", im.importMembers},
		{"member_roles", im.importMemberRoles},
		{"versions", im.importVersions},
		{"issue_categories", im.importIssueCategories},
		{"projects_defaults", im.updateProjectDefaults},
		{"workflows", im.importWorkflows},
		{"issues", im.importIssues},
		{"issue_relations", im.importIssueRelations},
		{"journals", im.importJournals},
		{"journal_details", im.importJournalDetails},
		{"time_entries", im.importTimeEntries},
		{"documents", im.importDocuments},
		{"news", im.importNews},
		{"comments", im.importComments},
		{"boards", im.importBoards},
		{"messages", im.importMessages},
		{"wikis", im.importWikis},
		{"wiki_pages", im.importWikiPages},
		{"wiki_content_versions", im.importWikiVersions},
		{"wiki_redirects", im.importWikiRedirects},
		{"repositories", im.importRepositories},
		{"changesets", im.importChangesets},
		{"changes", im.importChanges},
		{"changeset_parents", im.importChangesetParents},
		{"changesets_issues", im.importChangesetsIssues},
		{"queries", im.importQueries},
		{"queries_roles", im.importQueriesRoles},
		{"projects_default_query", im.updateProjectDefaultQuery},
		{"user_preferences", im.importUserPreferences},
		{"user_notifications", im.importUserNotifications},
		{"custom_values", im.importCustomValues},
		{"attachments", im.importAttachments},
		{"watchers", im.importWatchers},
		{"reactions", im.importReactions},
		{"webhooks", im.importWebhooks},
		{"oauth", im.importOAuth},
		{"derived values", im.recompute},
		{"sequences", im.resetSequences},
		{"checks", im.checks},
	}
	for _, s := range steps {
		if err := im.ctx.Err(); err != nil {
			return err
		}
		start := time.Now()
		if err := s.fn(); err != nil {
			return fmt.Errorf("importer: %s: %w", s.name, err)
		}
		im.log.Info("step done", "step", s.name, "elapsed", time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// table はソーステーブルの TableReport を返し、初回はソース行数を設定する。
func (im *imp) table(name string) *TableReport {
	t := im.rep.Lookup(name)
	if t == nil {
		t = im.rep.Table(name)
		t.SourceRows = im.src.rowCount(name)
		if !im.src.has(name) && name[0] != '(' {
			im.rep.warnf("table %s is not in the archive", name)
		}
	}
	return t
}

// ins は inserter を作り、close 時に TableReport へ挿入数を記録する。
func (im *imp) ins(t *TableReport, target string, cols ...string) *inserter {
	in := newInserter(im.ctx, im.tx, target, cols...)
	in.onDone = func(n int64) { t.Imported[target] += n }
	return in
}

// exec は 1 文を実行する。
func (im *imp) exec(q string, args ...any) (int64, error) {
	res, err := im.tx.Exec(im.ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// tsOr は datetime 列を UTC 文字列にする。NULL/不正なら候補を順に試し、最後は現在時刻(補完を記録)。
func (im *imp) tsOr(t *TableReport, id any, r rec, col string, fallbacks ...string) string {
	if s, ok := im.tz.ts(r.Row[col]); ok {
		return s
	}
	for _, fb := range fallbacks {
		if s, ok := im.tz.ts(r.Row[fb]); ok {
			t.repair(id, "%s missing/invalid; used %s", col, fb)
			return s
		}
	}
	t.repair(id, "%s missing/invalid; used import time", col)
	return im.nowStr
}

// tsNull は NULL 許容の datetime 列(不正値は NULL にして記録)。
func (im *imp) tsNull(t *TableReport, id any, r rec, col string) any {
	v := r.Row[col]
	if v == nil {
		return nil
	}
	if s, ok := im.tz.ts(v); ok {
		return s
	}
	t.repair(id, "%s invalid (%v); set NULL", col, v)
	return nil
}

// dateNull は NULL 許容の date 列。
func (im *imp) dateNull(t *TableReport, id any, r rec, col string) any {
	v := r.Row[col]
	if v == nil {
		return nil
	}
	if s, ok := toDate(v); ok {
		return s
	}
	t.repair(id, "%s invalid (%v); set NULL", col, v)
	return nil
}
