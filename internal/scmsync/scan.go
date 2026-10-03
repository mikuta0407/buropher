// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package scmsync

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/timelog"
)

var reCommitter = regexp.MustCompile(`^([^<]+)(<(.*)>)?$`)

// timelogRE は Changeset::TIMELOG_RE（x 修飾の空白を除いたもの。数字は ASCII に限る）。
const timelogRE = `(` +
	`(([0-9]+)(h|hours?))(([0-9]+)(m|min)?)?` +
	`|` +
	`(([0-9]+)(h|hours?|m|min))` +
	`|` +
	`([0-9]+):([0-9]+)` +
	`|` +
	`([0-9]+([\.,][0-9]+)?)h?` +
	`)`

// Ruby の \s（ASCII の空白）と [[:punct:]]（Unicode の句読点 + ASCII の記号）。
const (
	rubySpace = `[ \t\r\n\f\v]`
	rubyPunct = `(?:\p{P}|[$+<=>^` + "`" + `|~])`
)

var reRefs = regexp2.MustCompile(`#([0-9]+)(`+rubySpace+`+@`+timelogRE+`)?`, regexp2.None)

// commitRegexp は scan_comment_for_issue_ids の正規表現（キーワードごとに組み立てる）。
func commitRegexp(keywords []string) *regexp2.Regexp {
	quoted := make([]string, len(keywords))
	for i, k := range keywords {
		quoted[i] = regexp2.Escape(k)
	}
	kw := strings.Join(quoted, "|")
	tl := `(` + rubySpace + `+@` + timelogRE + `)?`
	pat := `([ \t\r\n\f\v\(\[,-]|^)((` + kw + `)[ \t\r\n\f\v:]+)?` +
		`(#[0-9]+` + tl + `([ \t\r\n\f\v,;&]+#[0-9]+` + tl + `)*)` +
		`(?=` + rubyPunct + `|` + rubySpace + `|<|$)`
	return regexp2.MustCompile(pat, regexp2.IgnoreCase|regexp2.Multiline)
}

// ScanCommentForIssueIDs は Changeset#scan_comment_for_issue_ids（参照キーワードでチケットを関連付け、
// 修正キーワードでステータス・進捗率を更新し、@時間 で作業時間を記録する）。
// 戻り値はコミット後に配送する通知。
func (s *Service) ScanCommentForIssueIDs(ctx context.Context, tx db.Queryer, repo *domain.Repository, cs *domain.Changeset,
	user *domain.User) ([]issues.Notification, error) {
	if strings.TrimSpace(cs.Comments) == "" {
		return nil, nil
	}
	var refKeywords []string
	refAny := false
	for _, k := range strings.Split(strings.ToLower(s.Settings.String("commit_ref_keywords")), ",") {
		k = strings.TrimSpace(k)
		if k == "*" {
			refAny = true
			continue
		}
		refKeywords = append(refKeywords, k)
	}
	rules := s.Settings.CommitUpdateKeywordsArray()
	var fixKeywords []string
	for _, r := range rules {
		if kws, ok := r["keywords"].([]string); ok {
			fixKeywords = append(fixKeywords, kws...)
		}
	}
	re := commitRegexp(append(slices.Clone(refKeywords), fixKeywords...))

	project, err := repository.GetProject(ctx, tx, repo.ProjectID)
	if err != nil {
		return nil, err
	}
	var referenced []int64
	var notifications []issues.Notification
	m, err := re.FindStringMatch(cs.Comments)
	for ; m != nil && err == nil; m, err = re.FindNextMatch(m) {
		g := m.Groups()
		action := strings.ToLower(g[3].String())
		refs := g[4].String()
		if action == "" && !refAny {
			continue
		}
		rm, rerr := reRefs.FindStringMatch(refs)
		for ; rm != nil && rerr == nil; rm, rerr = reRefs.FindNextMatch(rm) {
			rg := rm.Groups()
			id, _ := strconv.ParseInt(rg[1].String(), 10, 64)
			hours := ""
			if rg[3].Length > 0 || rg[2].Length > 0 {
				hours = rg[3].String()
			}
			iss, err := s.FindReferencedIssueByID(ctx, tx, project, id)
			if err != nil {
				return nil, err
			}
			if iss == nil {
				continue
			}
			linked, err := repository.IssueLinkedToSameCommit(ctx, tx, iss.ID, repo, cs)
			if err != nil {
				return nil, err
			}
			if linked {
				continue
			}
			referenced = append(referenced, iss.ID)
			// 古いコミットの取り込みではチケットを更新せず作業時間も記録しない
			if !repo.CreatedOn.IsZero() && cs.CommittedOn.Before(repo.CreatedOn) {
				continue
			}
			if slices.Contains(fixKeywords, action) {
				ns, err := s.fixIssue(ctx, tx, repo, project, cs, iss.ID, action, rules, user)
				if err != nil {
					return nil, err
				}
				notifications = append(notifications, ns...)
			}
			if hours != "" && s.Settings.Bool("commit_logtime_enabled") {
				if err := s.logTime(ctx, tx, repo, project, cs, iss.ID, hours, user); err != nil {
					return nil, err
				}
			}
		}
	}
	slices.Sort(referenced)
	referenced = slices.Compact(referenced)
	for _, id := range referenced {
		if err := repository.AddChangesetIssue(ctx, tx, cs.ID, id); err != nil {
			return nil, err
		}
	}
	return notifications, nil
}

// FindReferencedIssueByID は Changeset#find_referenced_issue_by_id（Setting.commit_cross_project_ref が無効なら
// リポジトリのプロジェクト・その親・子孫のチケットだけ）。
func (s *Service) FindReferencedIssueByID(ctx context.Context, q db.Queryer, project *domain.Project, id int64) (*repository.RefIssueProject, error) {
	if id <= 0 {
		return nil, nil
	}
	iss, err := repository.GetRefIssueProject(ctx, q, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.Settings.Bool("commit_cross_project_ref") {
		return iss, nil
	}
	if iss.ProjectID == project.ID {
		return iss, nil
	}
	ok, err := repository.ProjectsRelated(ctx, q, project.ID, iss.ProjectID)
	if err != nil || !ok {
		return nil, err
	}
	return iss, nil
}

// textTag は Changeset#text_tag(ref_project)。
func textTag(repo *domain.Repository, project *domain.Project, cs *domain.Changeset, refProject *domain.Project) string {
	r := ""
	if strings.TrimSpace(repo.Identifier) != "" {
		r = repo.Identifier + "|"
	}
	var tag string
	if cs.Scmid != "" {
		tag = "commit:" + r + cs.Scmid
	} else {
		tag = r + "r" + cs.Revision
	}
	if refProject != nil && project != nil && refProject.ID != project.ID {
		tag = project.Identifier + ":" + tag
	}
	return tag
}

func (s *Service) bundle() *i18n.Bundle {
	if s.Bundle != nil {
		return s.Bundle
	}
	return i18n.Default()
}

func (s *Service) defaultLocalizer() *i18n.Localizer {
	is := i18n.Settings{
		DateFormat: s.Settings.String("date_format"), TimeFormat: s.Settings.String("time_format"),
		TimespanFormat: s.Settings.String("timespan_format"), StartOfWeek: s.Settings.String("start_of_week"),
		DefaultLanguage: s.Settings.String("default_language"),
	}
	return s.bundle().NewLocalizer(s.Settings.String("default_language"), is, nil)
}

// changesetUser は user || User.anonymous。
func (s *Service) changesetUser(ctx context.Context, q db.Queryer, cs *domain.Changeset) (*domain.User, error) {
	if cs.UserID != nil {
		u, err := repository.GetUser(ctx, q, *cs.UserID)
		if err == nil {
			return u, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	return repository.AnonymousUser(ctx, q)
}

// fixIssue は fix_issue(issue, action)（閉じていなければ規則のステータス・進捗率を設定して保存する）。
func (s *Service) fixIssue(ctx context.Context, tx db.Queryer, repo *domain.Repository, project *domain.Project, cs *domain.Changeset,
	issueID int64, action string, rules []map[string]any, current *domain.User) ([]issues.Notification, error) {
	env := issues.NewEnv(tx, s.Settings, current)
	env.Now = s.now
	loc := s.defaultLocalizer()
	env.Translate = loc.L
	iss, err := env.Load(ctx, issueID)
	if err != nil {
		if errors.Is(err, issues.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	closed, err := env.Closed(ctx, iss)
	if err != nil || closed {
		return nil, err
	}
	u, err := s.changesetUser(ctx, tx, cs)
	if err != nil {
		return nil, err
	}
	issProject, err := env.Project(ctx, iss.ProjectID)
	if err != nil {
		return nil, err
	}
	notes := loc.L("text_status_changed_by_changeset", map[string]any{"value": textTag(repo, project, cs, issProject)})
	if _, err := env.InitJournal(ctx, iss, u, notes); err != nil {
		return nil, err
	}
	for _, r := range rules {
		kws, _ := r["keywords"].([]string)
		if !slices.Contains(kws, action) {
			continue
		}
		if t := toS(r["if_tracker_id"]); t != "" && t != strconv.FormatInt(iss.TrackerID, 10) {
			continue
		}
		if v := toS(r["status_id"]); v != "" {
			if err := env.SetStatusID(ctx, iss, rubyToI(v)); err != nil {
				return nil, err
			}
		}
		if v := toS(r["done_ratio"]); v != "" {
			iss.DoneRatio = int(rubyToI(v))
		}
		break
	}
	if !iss.Changed() {
		iss.ClearJournal()
		return nil, nil
	}
	ok, res, err := env.Save(ctx, iss)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.logger().Warn("Issue could not be saved by changeset", "issue", issueID, "changeset", cs.ID)
		return nil, nil
	}
	if res != nil {
		return res.Notifications, nil
	}
	return nil, nil
}

// logTime は log_time(issue, hours)（作業時間を changeset のユーザーで記録する）。
func (s *Service) logTime(ctx context.Context, tx db.Queryer, repo *domain.Repository, project *domain.Project, cs *domain.Changeset,
	issueID int64, hours string, current *domain.User) error {
	if current == nil {
		var err error
		if current, err = repository.AnonymousUser(ctx, tx); err != nil {
			return err
		}
	}
	loc := s.defaultLocalizer()
	env := &timelog.Env{Q: tx, Settings: s.Settings, User: current, Az: authz.New(tx, current), Loc: loc, Now: s.now}
	issEnv := env.Issues()
	iss, err := issEnv.Load(ctx, issueID)
	if err != nil {
		if errors.Is(err, issues.ErrNotFound) {
			return nil
		}
		return err
	}
	issProject, err := issEnv.Project(ctx, iss.ProjectID)
	if err != nil {
		return err
	}
	t, err := env.New(ctx, issProject, &issueID)
	if err != nil {
		return err
	}
	if cs.UserID != nil {
		uid := *cs.UserID
		t.UserID = &uid
	}
	t.SetHours(hours)
	if cs.CommitDate != nil {
		t.SetSpentOn(cs.CommitDate.Format("2006-01-02"))
	}
	comments := loc.L("text_time_logged_by_changeset", map[string]any{"value": textTag(repo, project, cs, issProject)})
	t.Comments = &comments
	// project.commit_logtime_activity
	if aid := rubyToI(s.Settings.String("commit_logtime_activity_id")); aid > 0 {
		acts, err := repository.TimelogProjectActivities(ctx, tx, issProject.ID, false)
		if err != nil {
			return err
		}
		for _, a := range acts {
			if a.ID == aid {
				id := a.ID
				t.ActivityID = &id
			}
		}
	}
	ok, err := env.Save(ctx, t)
	if err != nil {
		return err
	}
	if !ok {
		s.logger().Warn("TimeEntry could not be created by changeset", "changeset", cs.ID, "errors", t.Errors.FullMessages(loc))
	}
	return nil
}

func toS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

func rubyToI(s string) int64 {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.ParseInt(s[:end], 10, 64)
	return n
}
