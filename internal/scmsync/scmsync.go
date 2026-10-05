// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package scmsync はリポジトリのチェンジセット取り込み（Repository::Git#fetch_changesets,
// Repository.fetch_changesets）と、コミットメッセージからのチケット参照の解析
// （Changeset#scan_comment_for_issue_ids: 参照・修正キーワード、作業時間の記録）の移植。
package scmsync

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/scm"
	"github.com/mikuta0407/buropher/internal/settings"
)

// Service はチェンジセットの取り込み処理。
type Service struct {
	DB       *db.DB
	Settings *settings.Settings
	// GitCommand は git の実行ファイル（空なら "git"）。
	GitCommand string
	// Bundle は翻訳（ll(Setting.default_language, ...)）。nil なら i18n.Default()。
	Bundle *i18n.Bundle
	// Now は現在時刻（nil なら clock.Now）。
	Now func() time.Time
	// Notifier はキーワードで更新したチケットの通知先（nil なら通知しない）。
	Notifier issues.Notifier
	// Logger はログ出力先（nil なら slog.Default()）。
	Logger *slog.Logger
	// Webhooks はコミット後に Webhook を発火する（キーワードで更新したチケットの issue.updated と、
	// 記録した作業時間の time_entry.created。nil なら発火しない）。
	Webhooks func(ctx context.Context, issueEvents []issues.WebhookEvent, timeEntryIDs []int64)

	// pendingIssueWebhooks / pendingTimeEntries は取り込み中のリビジョンで発火する Webhook（コミット後に Webhooks へ渡す）。
	pendingIssueWebhooks []issues.WebhookEvent
	pendingTimeEntries   []int64
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return clock.Now()
}

// Adapter は repository.scm（Git アダプタ）。
func (s *Service) Adapter(repo *domain.Repository) *scm.Git {
	return scm.NewGit(s.GitCommand, repo.URL, repo.RootURL, repo.PathEncoding)
}

// EnsureRootURL は Repository#scm の root_url の補完（アダプタは root_url が空なら retrieve_root_url で
// url を root_url とし、Repository#scm がそれを update_attribute で保存する）。
func EnsureRootURL(ctx context.Context, q db.Queryer, repo *domain.Repository) error {
	if !repo.IsGit() || strings.TrimSpace(repo.RootURL) != "" {
		return nil
	}
	repo.RootURL = repo.URL
	return repository.UpdateScmRepositoryRootURL(ctx, q, repo.ID, repo.RootURL)
}

// FetchAll は Repository.fetch_changesets（リポジトリモジュールが有効な稼働中プロジェクトの全リポジトリ）。
// user は User.current（キーワードによるチケット更新・作業時間の作成者。nil なら匿名）。
func (s *Service) FetchAll(ctx context.Context, user *domain.User) error {
	ids, err := repository.ActiveRepositoryProjectIDs(ctx, s.DB)
	if err != nil {
		return err
	}
	for _, pid := range ids {
		if err := s.FetchProject(ctx, pid, user); err != nil {
			s.logger().Error("scm: error during fetching changesets", "project_id", pid, "err", err)
		}
	}
	return nil
}

// FetchProject は project.repositories.each(&:fetch_changesets)。
func (s *Service) FetchProject(ctx context.Context, projectID int64, user *domain.User) error {
	repos, err := repository.ProjectScmRepositories(ctx, s.DB, projectID)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if err := s.Fetch(ctx, r, user); err != nil {
			return err
		}
	}
	return nil
}

// Fetch は Repository::Git#fetch_changesets（ブランチの先頭が前回から変わっていれば新しいリビジョンを取り込む）。
// Git 以外のリポジトリは何もしない（D-15）。
func (s *Service) Fetch(ctx context.Context, repo *domain.Repository, user *domain.User) error {
	if !repo.IsGit() {
		return nil
	}
	if err := EnsureRootURL(ctx, s.DB, repo); err != nil {
		return err
	}
	g := s.Adapter(repo)
	brs := g.Branches(ctx)
	if len(brs) == 0 {
		return nil
	}
	h := cloneMap(repo.ExtraInfo)
	repoHeads := make([]string, len(brs))
	for i, b := range brs {
		repoHeads[i] = b.Scmid
	}
	prevHeads := stringList(h["heads"])
	if len(prevHeads) == 0 {
		prevHeads = append(prevHeads, headsFromBranchesHash(h)...)
	}
	if equalSorted(prevHeads, repoHeads) {
		return nil
	}
	dc, _ := h["db_consistent"].(map[string]any)
	if dc == nil {
		dc = map[string]any{}
	}
	h["db_consistent"] = dc
	n, err := repository.CountChangesets(ctx, s.DB, repo.ID)
	if err != nil {
		return err
	}
	save := false
	if n == 0 {
		dc["ordering"] = 1
		save = true
	} else if _, ok := dc["ordering"]; !ok {
		dc["ordering"] = 0
		save = true
	}
	if save {
		repo.MergeExtraInfo(h)
		if err := repository.UpdateScmRepositoryExtraInfo(ctx, s.DB, repo.ID, repo.ExtraInfo); err != nil {
			return err
		}
	}
	return s.saveRevisions(ctx, repo, g, prevHeads, repoHeads, user)
}

// saveRevisions は save_revisions(prev_db_heads, repo_heads)。
func (s *Service) saveRevisions(ctx context.Context, repo *domain.Repository, g *scm.Git, prevHeads, repoHeads []string, user *domain.User) error {
	revs := g.Revisions(ctx, "", "", "", scm.RevisionsOptions{Reverse: true, Excludes: prevHeads, Includes: repoHeads})
	if len(revs) == 0 {
		return nil
	}
	// DB に既にあるリビジョンを除く（100 件ずつ）
	known := map[string]bool{}
	for off := 0; off < len(revs); off += 100 {
		end := min(off+100, len(revs))
		ids := make([]string, 0, end-off)
		for _, r := range revs[off:end] {
			ids = append(ids, r.Scmid)
		}
		m, err := repository.ExistingChangesetScmids(ctx, s.DB, repo.ID, ids)
		if err != nil {
			return err
		}
		for k := range m {
			known[k] = true
		}
	}
	committers := map[string]*int64{}
	for _, rev := range revs {
		if known[rev.Scmid] {
			continue
		}
		var notifications []issues.Notification
		s.pendingIssueWebhooks, s.pendingTimeEntries = nil, nil
		err := s.DB.WithTx(ctx, func(tx *db.Tx) error {
			ns, err := s.saveRevision(ctx, tx, repo, rev, committers, user)
			notifications = ns
			return err
		})
		if err != nil {
			s.pendingIssueWebhooks, s.pendingTimeEntries = nil, nil
			return err
		}
		if s.Webhooks != nil && (len(s.pendingIssueWebhooks) > 0 || len(s.pendingTimeEntries) > 0) {
			s.Webhooks(ctx, s.pendingIssueWebhooks, s.pendingTimeEntries)
		}
		s.pendingIssueWebhooks, s.pendingTimeEntries = nil, nil
		if len(notifications) > 0 {
			env := issues.NewEnv(s.DB, s.Settings, user)
			env.Notifier = s.Notifier
			if err := env.Dispatch(ctx, notifications); err != nil {
				s.logger().Error("scm: dispatch notifications", "err", err)
			}
		}
	}
	h := map[string]any{"heads": stringsToAny(repoHeads)}
	repo.MergeExtraInfo(h)
	return repository.UpdateScmRepositoryExtraInfo(ctx, s.DB, repo.ID, repo.ExtraInfo)
}

// saveRevision は save_revision(rev)（Changeset.create + 親 + 変更ファイル + scan_for_issues）。
func (s *Service) saveRevision(ctx context.Context, tx *db.Tx, repo *domain.Repository, rev *scm.Revision,
	committers map[string]*int64, user *domain.User) ([]issues.Notification, error) {
	var parentIDs []int64
	for _, p := range rev.Parents {
		cs, err := FindChangesetByName(ctx, tx, repo, p)
		if err != nil {
			return nil, err
		}
		if cs != nil {
			parentIDs = append(parentIDs, cs.ID)
		}
	}
	// validates_uniqueness_of :revision
	if c, err := repository.FindChangesetByRevision(ctx, tx, repo.ID, rev.Identifier); err == nil && c != nil {
		return nil, nil
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	committer := scm.ToUTF8(truncate(rev.Author, 255), repo.RepoLogEncoding())
	cd := time.Date(rev.Time.Year(), rev.Time.Month(), rev.Time.Day(), 0, 0, 0, 0, time.UTC)
	cs := &domain.Changeset{
		RepositoryID: repo.ID, Revision: rev.Identifier, Scmid: rev.Scmid, Committer: committer,
		CommittedOn: rev.Time.UTC(), CommitDate: &cd,
		Comments:      strings.TrimSpace(scm.ToUTF8(rev.Message, repo.RepoLogEncoding())),
		RepositorySCM: repo.SCM,
	}
	uid, err := s.findCommitterUser(ctx, tx, repo, committer, committers)
	if err != nil {
		return nil, err
	}
	cs.UserID = uid
	if err := repository.InsertChangeset(ctx, tx, cs); err != nil {
		return nil, err
	}
	for _, pid := range parentIDs {
		if err := repository.InsertChangesetParent(ctx, tx, cs.ID, pid); err != nil {
			return nil, err
		}
	}
	// after_create :scan_for_issues
	ns, err := s.ScanCommentForIssueIDs(ctx, tx, repo, cs, user)
	if err != nil {
		return nil, err
	}
	for _, ch := range rev.Paths {
		f := &domain.ChangesetFile{ChangesetID: cs.ID, Action: ch.Action, Path: scm.ReplaceInvalidUTF8(ch.Path),
			FromPath: scm.ReplaceInvalidUTF8(ch.FromPath), FromRevision: ch.FromRevision}
		if f.Action == "" || f.Path == "" {
			continue
		}
		if err := repository.InsertChangesetFile(ctx, tx, f); err != nil {
			return nil, err
		}
	}
	return ns, nil
}

// findCommitterUser は find_committer_user(committer)（既に対応付けたユーザー、ログイン名、メールアドレスの順）。
func (s *Service) findCommitterUser(ctx context.Context, q db.Queryer, repo *domain.Repository, committer string, cache map[string]*int64) (*int64, error) {
	if strings.TrimSpace(committer) == "" {
		return nil, nil
	}
	if v, ok := cache[committer]; ok {
		return v, nil
	}
	uid, err := repository.CommitterMappedUserID(ctx, q, repo.ID, committer)
	if err != nil {
		return nil, err
	}
	if uid == nil {
		if m := reCommitter.FindStringSubmatch(strings.TrimSpace(committer)); m != nil {
			username, email := strings.TrimSpace(m[1]), m[3]
			if uid, err = repository.FindUserIDByLoginOrMail(ctx, q, username, email); err != nil {
				return nil, err
			}
		}
	}
	cache[committer] = uid
	return uid, nil
}

// FindChangesetByName は Repository::Git#find_changeset_by_name（revision の完全一致、なければ scmid の前方一致）。
// Git 以外は Repository#find_changeset_by_name（数字なら revision の完全一致、それ以外は revision の前方一致）。
func FindChangesetByName(ctx context.Context, q db.Queryer, repo *domain.Repository, name string) (*domain.Changeset, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	var cs *domain.Changeset
	var err error
	if repo.IsGit() {
		cs, err = repository.FindChangesetByRevision(ctx, q, repo.ID, name)
		if errors.Is(err, repository.ErrNotFound) {
			cs, err = repository.FindChangesetByScmidPrefix(ctx, q, repo.ID, name)
		}
	} else if isDigits(name) {
		cs, err = repository.FindChangesetByRevision(ctx, q, repo.ID, name)
	} else {
		cs, err = repository.FindChangesetByRevisionPrefix(ctx, q, repo.ID, name)
	}
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	return cs, err
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// truncate は String#truncate(n)（n 文字を超えたら n-3 文字 + "..."）。
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}

func cloneMap(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return slices.Clone(x)
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// headsFromBranchesHash は heads_from_branches_hash（旧形式の extra_info["branches"][name]["last_scmid"]）。
func headsFromBranchesHash(h map[string]any) []string {
	brs, _ := h["branches"].(map[string]any)
	var out []string
	for _, v := range brs {
		if m, ok := v.(map[string]any); ok {
			if s, ok := m["last_scmid"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func equalSorted(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
