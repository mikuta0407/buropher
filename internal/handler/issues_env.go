package handler

// チケットの書き込み系（IssuesController の作成・更新・削除、IssueRelationsController、WatchersController、
// JournalsController）で共通に使う issues.Env の組み立てと通知の配送。

import (
	"context"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/issues"
)

// LogNotifier は通知をログに出すだけの issues.Notifier（メール・Discord の配送は後続の実装で差し替える）。
type LogNotifier struct {
	App *App
}

// Enqueue は通知をログに記録する。
func (n LogNotifier) Enqueue(ctx context.Context, x issues.Notification) error {
	n.App.logger().Info("issue notification", "event", x.Event, "issue_id", x.IssueID, "journal_id", x.JournalID,
		"author_id", x.AuthorID, "recipients", x.Recipients)
	return nil
}

// issueNotifier は通知の配送先（App.Notifier。nil ならログ出力のみ）。
func (a *App) issueNotifier() issues.Notifier {
	if a.Notifier != nil {
		return a.Notifier
	}
	return LogNotifier{App: a}
}

// writeIssuesEnv は User.current の書き込み用 issues.Env（q は通常 *db.Tx。*db.DB なら操作ごとにトランザクションを張る）。
func (a *App) writeIssuesEnv(c *Req, q db.Queryer) *issues.Env {
	e := issues.NewEnv(q, a.Settings, c.User)
	e.Now = a.now
	e.Notifier = a.issueNotifier()
	e.Translate = func(key string, args ...any) string { return c.L(key, args...) }
	if c.Loc != nil {
		e.DateFormat = c.Loc.FormatDate
	}
	return e
}

// dispatchIssueNotifications はコミット後に保存結果の通知を配送する（失敗はログのみ。Redmine の deliver_later 相当）。
func (a *App) dispatchIssueNotifications(c *Req, res ...*issues.SaveResult) {
	e := a.writeIssuesEnv(c, a.DB)
	for _, r := range res {
		if r == nil {
			continue
		}
		if err := e.Dispatch(c.Ctx(), r.Notifications); err != nil {
			a.logger().Error("issue notification", "err", err)
		}
	}
}
