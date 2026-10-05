// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// Webhook の発火（Webhook.trigger / hooks_for）・ペイロード（WebhookPayload / acts_as_webhookable /
// Issue::Webhookable / News::Webhookable / WikiPage::Webhookable）・送信ジョブ（WebhookJob）。
//
// 規約: Issue / News / TimeEntry / Version / WikiPage を保存・削除する処理は、Redmine の
// after_create_commit / after_update_commit / after_destroy_commit と同じく、コミット後に
// a.triggerWebhook(ctx, action, obj) を呼ぶ。削除は削除するとペイロードを計算できないため、
// 削除の前に a.prepareWebhooks で計算しておき、コミット後に a.enqueueWebhooks で積む。
// チケットは issues.SaveResult.Webhooks（issues パッケージの保存処理が記録する）を
// a.dispatchIssueNotifications / a.triggerIssueWebhooks が発火する。

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/webhook"
)

// JobWebhook は Webhook の送信ジョブ（WebhookJob）。
const JobWebhook = "webhook.deliver"

// webhookObject は発火の対象（Webhook.trigger の object）。
type webhookObject struct {
	// Type は webhook.TypeIssue など（model_name.singular）。
	Type string
	ID   int64
	// ProjectID は object.project_id（hooks_for の対象プロジェクト）。
	ProjectID int64
	// JournalID は issue.updated の current_journal（0 ならなし）。
	JournalID int64
}

// pendingWebhook は送信待ちの 1 件（計算済みのペイロード）。
type pendingWebhook struct {
	HookID  int64
	Payload string
}

type webhookJobPayload struct {
	HookID  int64  `json:"hook_id"`
	Payload string `json:"payload"`
}

// webhooksEnabled は Webhook.enabled?（Setting.webhooks_enabled?）。
func (a *App) webhooksEnabled() bool { return a.Settings != nil && a.Settings.Bool("webhooks_enabled") }

// webhookValidator は送信先の検証（config の webhook_blocklist）。
func (a *App) webhookValidator() *webhook.Validator {
	if a.WebhookValidator != nil {
		return a.WebhookValidator
	}
	return webhook.NewValidator(nil)
}

// RegisterWebhookJob は送信ジョブを登録し、発火したフックを q に積むようにする。
func (a *App) RegisterWebhookJob(q *jobs.Queue) {
	a.WebhookQueue = q
	q.Register(JobWebhook, a.webhookJob)
}

// triggerWebhook は Webhook.trigger(event, object)（コミット後に呼ぶ。失敗はログのみ）。
func (a *App) triggerWebhook(ctx context.Context, action string, obj webhookObject) {
	a.enqueueWebhooks(ctx, a.prepareWebhooks(ctx, action, obj))
}

// triggerWebhooks は複数の対象をまとめて発火する。
func (a *App) triggerWebhooks(ctx context.Context, action string, objs ...webhookObject) {
	for _, o := range objs {
		a.triggerWebhook(ctx, action, o)
	}
}

// prepareWebhooks は hooks_for(event, object) の各フックのペイロードを計算する
// （送信は enqueueWebhooks。削除の前に計算しておくために分けてある）。
func (a *App) prepareWebhooks(ctx context.Context, action string, obj webhookObject) []pendingWebhook {
	if !a.webhooksEnabled() || obj.ProjectID == 0 || obj.ID == 0 {
		return nil
	}
	event := webhook.EventName(obj.Type, action)
	hooks, err := repository.ActiveWebhooksForProject(ctx, a.DB, a.Secrets, obj.ProjectID)
	if err != nil {
		a.logger().Error("webhook: find hooks", "event", event, "err", err)
		return nil
	}
	if len(hooks) == 0 {
		return nil
	}
	project, err := repository.GetProject(ctx, a.DB, obj.ProjectID)
	if err != nil {
		a.logger().Error("webhook: project", "event", event, "err", err)
		return nil
	}
	var out []pendingWebhook
	for _, h := range hooks {
		if !slices.Contains(h.Events.V, event) {
			continue
		}
		user, err := repository.GetUser(ctx, a.DB, h.UserID)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				a.logger().Error("webhook: hook user", "hook_id", h.ID, "err", err)
			}
			continue
		}
		if user.Status != domain.StatusActive {
			continue
		}
		c := a.newBackgroundReq(ctx, user, WebhooksController, "trigger")
		// hook.user.allowed_to?(:use_webhooks, object.project)
		if !c.AllowedTo(domain.Perm("use_webhooks"), project) {
			continue
		}
		payload, ok, err := a.webhookPayload(c, obj, action, project)
		if err != nil {
			a.logger().Error("webhook: payload", "hook_id", h.ID, "event", event, "err", err)
			continue
		}
		if !ok {
			// object.visible?(hook.user) が偽
			continue
		}
		out = append(out, pendingWebhook{HookID: h.ID, Payload: string(payload)})
	}
	return out
}

// enqueueWebhooks は WebhookJob.perform_later(hook.id, payload.to_json)。
func (a *App) enqueueWebhooks(ctx context.Context, ps []pendingWebhook) {
	for _, p := range ps {
		if a.WebhookQueue == nil {
			a.logger().Info("webhook: no job queue; delivery skipped", "hook_id", p.HookID)
			continue
		}
		// 送信の失敗は Redmine と同じく再試行しない（ログに残し、ジョブは failed として残す）
		if _, err := a.WebhookQueue.Enqueue(ctx, nil, JobWebhook, webhookJobPayload{HookID: p.HookID, Payload: p.Payload},
			jobs.MaxAttempts(1)); err != nil {
			a.logger().Error("webhook: enqueue", "hook_id", p.HookID, "err", err)
		}
	}
}

// webhookJob は WebhookJob#perform。
func (a *App) webhookJob(ctx context.Context, j *jobs.Job) error {
	var p webhookJobPayload
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	hook, err := repository.GetWebhook(ctx, a.DB, a.Secrets, p.HookID)
	if errors.Is(err, repository.ErrNotFound) {
		a.logger().Debug("WebhookJob: couldn't find hook", "hook_id", p.HookID)
		return nil
	}
	if err != nil {
		return err
	}
	user, err := repository.GetUser(ctx, a.DB, hook.UserID)
	if err != nil || user.Status != domain.StatusActive {
		a.logger().Debug("WebhookJob: user is not active", "user_id", hook.UserID)
		return nil
	}
	x := a.WebhookExecutor
	if x == nil {
		x = &webhook.Executor{Validator: a.webhookValidator()}
	}
	if _, err := x.Call(ctx, hook.URL, []byte(p.Payload), hook.Secret); err != nil {
		a.logger().Warn("Webhook Error", "hook_id", hook.ID, "err", err)
		return jobs.Permanent(err)
	}
	return nil
}

// iso8601 は Time#iso8601（UTC。小数秒なし）。
func iso8601(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z07:00") }

// WebhookPayload は hook の利用者 user に対する event（"type.action"）のペイロード（テスト用）。
// 閲覧できなければ ok = false。
func (a *App) WebhookPayload(ctx context.Context, user *domain.User, event string, id, journalID int64) ([]byte, bool, error) {
	typ, action := splitEvent(event)
	obj := webhookObject{Type: typ, ID: id, JournalID: journalID}
	pid, err := a.webhookObjectProjectID(ctx, obj)
	if err != nil {
		return nil, false, err
	}
	project, err := repository.GetProject(ctx, a.DB, pid)
	if err != nil {
		return nil, false, err
	}
	c := a.newBackgroundReq(ctx, user, WebhooksController, "trigger")
	return a.webhookPayload(c, obj, action, project)
}

func splitEvent(event string) (string, string) {
	for i := len(event) - 1; i >= 0; i-- {
		if event[i] == '.' {
			return event[:i], event[i+1:]
		}
	}
	return event, ""
}

// webhookObjectProjectID は object.project_id。
func (a *App) webhookObjectProjectID(ctx context.Context, obj webhookObject) (int64, error) {
	var pid int64
	var err error
	switch obj.Type {
	case webhook.TypeIssue:
		err = a.DB.Get(ctx, &pid, `SELECT project_id FROM issues WHERE id = ?`, obj.ID)
	case webhook.TypeNews:
		err = a.DB.Get(ctx, &pid, `SELECT project_id FROM news WHERE id = ?`, obj.ID)
	case webhook.TypeTimeEntry:
		err = a.DB.Get(ctx, &pid, `SELECT project_id FROM time_entries WHERE id = ?`, obj.ID)
	case webhook.TypeVersion:
		err = a.DB.Get(ctx, &pid, `SELECT project_id FROM versions WHERE id = ?`, obj.ID)
	case webhook.TypeWikiPage:
		err = a.DB.Get(ctx, &pid, `SELECT w.project_id FROM wiki_pages p INNER JOIN wikis w ON w.id = p.wiki_id WHERE p.id = ?`, obj.ID)
	default:
		return 0, errors.New("webhook: unknown type " + obj.Type)
	}
	return pid, err
}

// webhookPayload は WebhookPayload#to_h（c.User = フックの利用者）を JSON にする。
// 閲覧できない（object.visible?(user) が偽）なら ok = false。
func (a *App) webhookPayload(c *Req, obj webhookObject, action string, project *domain.Project) (out []byte, ok bool, lerr error) {
	ctx := c.Ctx()
	b := apibuilder.New("json")
	event := webhook.EventName(obj.Type, action)
	var render func()
	var ts time.Time
	switch obj.Type {
	case webhook.TypeIssue:
		r, err := repository.ReadIssue(ctx, a.DB, obj.ID)
		if err != nil {
			return nil, false, err
		}
		ok, err := c.Authz().IssueVisible(ctx, &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
			StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, project)
		if err != nil || !ok {
			return nil, false, err
		}
		row := issueRowFromRead(r)
		l := a.newIssueLookup(c)
		// ApiRenderer には @project が無いため、allowed_to?(:view_time_entries, nil) は偽（spent_hours を出さない）
		l.apiTimeProjectSet = true
		m := l.model(row)
		var journal func()
		switch action {
		case webhook.ActionCreated:
			ts = row.CreatedAt
		case webhook.ActionUpdated:
			ts = row.UpdatedAt
			if obj.JournalID != 0 {
				jr, jts, err := a.webhookJournal(c, b, obj.JournalID, r.ID)
				if err != nil {
					return nil, false, err
				}
				if jr != nil {
					journal, ts = jr, jts
				}
			}
		default:
			ts = a.now()
		}
		render = func() {
			b.Object("issue", func() { l.renderAPIIssueCore(b, m) })
			if journal != nil {
				b.Object("journal", journal)
			}
		}
		defer func() {
			if l.err != nil {
				lerr = l.err
			}
		}()
	case webhook.TypeNews:
		n, err := repository.GetNews(ctx, a.DB, obj.ID)
		if err != nil {
			return nil, false, err
		}
		if !c.AllowedTo(domain.Perm("view_news"), project) {
			return nil, false, nil
		}
		// News::Webhookable#webhook_payload_timestamp（created 以外は Time.now）
		ts = a.now()
		if action == webhook.ActionCreated {
			ts = n.CreatedAt
		}
		render = func() { a.apiNews(c, b, n, nil, nil) }
	case webhook.TypeTimeEntry:
		env := a.teEnv(c)
		t, err := env.Find(ctx, obj.ID)
		if err != nil {
			return nil, false, err
		}
		if t == nil {
			return nil, false, repository.ErrNotFound
		}
		ok, err := c.Authz().AllowedToWith(ctx, domain.Perm("view_time_entries"), project, func(role *domain.Role, u *domain.User) bool {
			switch role.TimeEntriesVisibility {
			case domain.TimeEntriesVisibilityAll:
				return true
			case domain.TimeEntriesVisibilityOwn:
				return t.UserID != nil && *t.UserID == u.ID
			}
			return false
		})
		if err != nil || !ok {
			return nil, false, err
		}
		l, err := a.newTELookup(c, []*timelog.Entry{t})
		if err != nil {
			return nil, false, err
		}
		cvs, err := env.VisibleCustomFieldValues(ctx, t, c.User)
		if err != nil {
			return nil, false, err
		}
		ts = a.webhookTimestamp(action, t.CreatedAt, t.UpdatedAt)
		render = func() { b.Object("time_entry", func() { l.apiEntry(b, t, cvs) }) }
	case webhook.TypeVersion:
		v, err := repository.GetVersion(ctx, a.DB, obj.ID)
		if err != nil {
			return nil, false, err
		}
		if !c.AllowedTo(domain.Perm("view_issues"), project) {
			return nil, false, nil
		}
		ts = a.webhookTimestamp(action, v.CreatedAt, v.UpdatedAt)
		render = func() {
			b.Object("version", func() {
				a.versionAPIFields(c, b, v, project)
				a.versionAPICustomValues(c, b, v, project)
				b.Value("created_on", v.CreatedAt)
				b.Value("updated_on", v.UpdatedAt)
			})
		}
	case webhook.TypeWikiPage:
		page, err := repository.GetWikiPageByID(ctx, a.DB, 0, obj.ID)
		if err != nil {
			return nil, false, err
		}
		page.Project = project
		if !c.AllowedTo(domain.Perm("view_wiki_pages"), project) {
			return nil, false, nil
		}
		content, err := repository.WikiPageVersion(ctx, a.DB, page.ID, page.CurrentVersion)
		if err != nil {
			return nil, false, err
		}
		ts = a.webhookTimestamp(action, page.CreatedAt, content.UpdatedOn)
		render = func() { a.apiWikiPage(c, b, page, content, false) }
	default:
		return nil, false, errors.New("webhook: unknown type " + obj.Type)
	}
	b.Value("type", event)
	b.Value("timestamp", iso8601(ts))
	b.Object("data", render)
	return b.Output(), true, lerr
}

// webhookTimestamp は acts_as_webhookable の webhook_payload_timestamp（created → created_on、
// updated → updated_on、それ以外は Time.now）。
func (a *App) webhookTimestamp(action string, created, updated time.Time) time.Time {
	switch action {
	case webhook.ActionCreated:
		return created
	case webhook.ActionUpdated:
		return updated
	}
	return a.now()
}

// webhookJournal は Issue::Webhookable の journal_payload（journals.visible(user) で見えなければ nil）。
func (a *App) webhookJournal(c *Req, b apibuilder.Builder, journalID, issueID int64) (func(), time.Time, error) {
	ctx := c.Ctx()
	user := c.User
	e := issues.NewEnv(a.DB, a.Settings, user)
	vis, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return nil, time.Time{}, err
	}
	notes, err := e.JournalVisibleNotesCondition(ctx, user)
	if err != nil {
		return nil, time.Time{}, err
	}
	var n int
	if err := a.DB.Get(ctx, &n, `SELECT COUNT(*) FROM issue_journals
  INNER JOIN issues ON issues.id = issue_journals.issue_id
  INNER JOIN projects ON projects.id = issues.project_id
WHERE issue_journals.id = ? AND issue_journals.issue_id = ? AND (`+vis+`) AND `+notes, journalID, issueID); err != nil {
		return nil, time.Time{}, err
	}
	if n == 0 {
		return nil, time.Time{}, nil
	}
	j, err := e.FindJournal(ctx, journalID)
	if err != nil || j == nil {
		return nil, time.Time{}, err
	}
	iss, err := e.Load(ctx, issueID)
	if err != nil {
		return nil, time.Time{}, err
	}
	details, err := e.VisibleDetails(ctx, j, iss, user)
	if err != nil {
		return nil, time.Time{}, err
	}
	var jname string
	if u, err := repository.GetUser(ctx, a.DB, j.UserID); err == nil {
		jname = c.Page().UserName(u)
	}
	render := func() {
		b.Value("id", j.ID)
		b.Value("created_on", iso8601(j.CreatedAt))
		if j.NotesNull {
			b.Value("notes", nil)
		} else {
			b.Value("notes", j.Notes)
		}
		b.Attrs("user", apibuilder.A("id", j.UserID, "name", jname))
		b.Array("details", nil, func() {
			for _, d := range details {
				b.Attrs("detail", apibuilder.A("property", d.Property, "prop_key", d.PropKey, "old_value", strOrNil(d.OldValue), "value", strOrNil(d.Value)))
			}
		})
	}
	return render, j.CreatedAt, nil
}
