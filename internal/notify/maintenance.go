package notify

// 定期ジョブ: 期日リマインダ（rake redmine:send_reminders / Mailer.reminders）と、期限切れの
// セッション・トークン・完了したジョブ・配送ログの削除。

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/repository"
)

// ReminderOptions は Mailer.reminders の options（rake redmine:send_reminders の days / tracker / project / users / version）。
type ReminderOptions struct {
	// Days は何日先までの期日を対象にするか（0 なら 7）。
	Days int `json:"days,omitempty"`
	// Tracker はトラッカー ID（0 なら全トラッカー）。
	Tracker int64 `json:"tracker,omitempty"`
	// Project はプロジェクトの ID または識別子（空なら全プロジェクト）。
	Project string `json:"project,omitempty"`
	// Users は担当者（ユーザー / グループ）の ID。
	Users []int64 `json:"users,omitempty"`
	// Version は対象バージョンの名前。
	Version string `json:"version,omitempty"`
}

// ErrNotFound は reminders のオプションで指定したプロジェクト・トラッカー・バージョンが無い。
var ErrNotFound = errors.New("not found")

// Reminders は Mailer.reminders（期日が近い未完了チケットの担当者に、見えるチケットの一覧を送る）。
// 送った受信者の数を返す。
func (s *Service) Reminders(ctx context.Context, o ReminderOptions) (int, error) {
	days := o.Days
	if days <= 0 {
		days = 7
	}
	var projectID int64
	if o.Project != "" {
		p, err := repository.FindProject(ctx, s.DB, o.Project)
		if err != nil {
			return 0, fmt.Errorf("couldn't find Project with %q: %w", o.Project, ErrNotFound)
		}
		projectID = p.ID
	}
	if o.Tracker != 0 {
		var n int
		if err := s.DB.Get(ctx, &n, `SELECT COUNT(*) FROM trackers WHERE id = ?`, o.Tracker); err != nil || n == 0 {
			return 0, fmt.Errorf("couldn't find Tracker with 'id'=%d: %w", o.Tracker, ErrNotFound)
		}
	}
	var versionIDs []int64
	if o.Version != "" {
		ids, err := repository.VersionIDsNamed(ctx, s.DB, o.Version)
		if err != nil {
			return 0, err
		}
		if len(ids) == 0 {
			return 0, fmt.Errorf("couldn't find Version named %s: %w", o.Version, ErrNotFound)
		}
		versionIDs = ids
	}
	due := s.now().AddDate(0, 0, days)
	rows, err := repository.ReminderIssues(ctx, s.DB, due, o.Users, projectID, o.Tracker, versionIDs)
	if err != nil {
		return 0, err
	}
	// 担当者ごと（グループはメンバーに展開）
	byAssignee := map[int64][]repository.ReminderIssue{}
	var order []int64
	add := func(uid int64, is ...repository.ReminderIssue) {
		if _, ok := byAssignee[uid]; !ok {
			order = append(order, uid)
		}
		byAssignee[uid] = append(byAssignee[uid], is...)
	}
	for _, r := range rows {
		add(r.AssignedToID, r)
	}
	for _, aid := range append([]int64(nil), order...) {
		p, err := repository.GetPrincipal(ctx, s.DB, aid)
		if err != nil || p == nil || !p.Kind.IsGroup() {
			continue
		}
		uids, err := repository.GroupUserIDs(ctx, s.DB, aid)
		if err != nil {
			return 0, err
		}
		for _, u := range uids {
			add(u, byAssignee[aid]...)
		}
	}
	projects := map[int64]*domain.Project{}
	sent := 0
	for _, uid := range order {
		u, err := repository.GetUser(ctx, s.DB, uid)
		if err != nil || u == nil || u.Kind != domain.KindUser || u.Status != domain.StatusActive {
			continue
		}
		az := authz.New(s.DB, u)
		var ids []int64
		seen := map[int64]bool{}
		for _, r := range byAssignee[uid] {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			p := projects[r.ProjectID]
			if p == nil {
				p = s.project(ctx, r.ProjectID)
				projects[r.ProjectID] = p
			}
			if p == nil {
				continue
			}
			ok, err := az.IssueVisible(ctx, &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID, StatusID: r.StatusID,
				AuthorID: r.AuthorID, AssignedToID: &r.AssignedToID, IsPrivate: r.IsPrivate}, p)
			if err != nil {
				return sent, err
			}
			if ok {
				ids = append(ids, r.ID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] }) // 期日順は描画時に並べる
		if err := s.deliverToUsers(ctx, Payload{Kind: KindReminder, Event: KindReminder, Days: days, IssueIDs: ids}, []int64{uid}); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func (s *Service) handleReminders(ctx context.Context, j *jobs.Job) error {
	var o ReminderOptions
	if err := j.Decode(&o); err != nil {
		return jobs.Permanent(err)
	}
	_, err := s.Reminders(ctx, o)
	if errors.Is(err, ErrNotFound) {
		return jobs.Permanent(err)
	}
	return err
}

// CleanupOptions は maintenance.cleanup の設定。
type CleanupOptions struct {
	JobsRetention       time.Duration `json:"jobs_retention"`
	DeliveriesRetention time.Duration `json:"deliveries_retention"`
}

// Cleanup は期限切れのセッション・トークン、古いジョブと配送ログを消す。
func (s *Service) Cleanup(ctx context.Context, o CleanupOptions) error {
	now := s.now()
	st := s.Settings
	var errs []error
	if _, err := repository.DeleteStaleSessions(ctx, s.DB, now, st.Int("session_lifetime"), st.Int("session_timeout"), 24*time.Hour); err != nil {
		errs = append(errs, err)
	}
	if _, err := repository.DestroyExpiredTokens(ctx, s.DB, now, st.Int("autologin")); err != nil {
		errs = append(errs, err)
	}
	jr := o.JobsRetention
	if jr <= 0 {
		jr = 7 * 24 * time.Hour
	}
	if _, err := s.Queue.PurgeFinished(ctx, now.Add(-jr)); err != nil {
		errs = append(errs, err)
	}
	dr := o.DeliveriesRetention
	if dr <= 0 {
		dr = 30 * 24 * time.Hour
	}
	if _, err := repository.PurgeNotificationDeliveries(ctx, s.DB, now.Add(-dr)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) handleCleanup(ctx context.Context, j *jobs.Job) error {
	var o CleanupOptions
	_ = j.Decode(&o)
	return s.Cleanup(ctx, o)
}
