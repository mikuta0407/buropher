package handler

// モデルのコールバックで Mailer.deliver_* を呼ぶ部分（User / EmailAddress のセキュリティ通知など）を、
// 保存処理の後から呼ぶためのヘルパー。配送は App.Notify（internal/notify）。

import (
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
)

// remoteIP は User.current.remote_ip。
func (c *Req) remoteIP() string { return httpx.RemoteIP(c.R) }

// userSavedSnapshot は保存前のユーザーの状態（User#deliver_security_notification の saved_change_to_*）。
type userSavedSnapshot struct {
	newRecord bool
	admin     bool
	status    int
	mail      string
}

// notifyUserSaved は User の after_save :deliver_security_notification と、既定のメールアドレスを
// 変更したときの EmailAddress の after_update_commit :deliver_security_notification_update。
func (a *App) notifyUserSaved(c *Req, u *domain.User, before userSavedSnapshot, newMail string) {
	if a.Notify == nil {
		return
	}
	ctx := c.Ctx()
	active := u.Status == domain.StatusActive
	adminChanged := before.admin != u.AdminFlag
	statusChanged := before.status != u.Status
	switch {
	case u.AdminFlag && active && (before.newRecord || adminChanged || statusChanged):
		a.Notify.AdminFlagChanged(ctx, u, c.User, c.remoteIP(), true)
	case (!u.AdminFlag && adminChanged && active) || (u.AdminFlag && statusChanged && !active):
		a.Notify.AdminFlagChanged(ctx, u, c.User, c.remoteIP(), false)
	}
	if !before.newRecord && before.mail != "" && newMail != "" && before.mail != newMail {
		a.Notify.EmailAddressChanged(ctx, u, c.User, c.remoteIP(), before.mail, newMail)
	}
}

// notifyUserDestroyed は User の after_destroy :deliver_security_notification（有効な管理者の削除）。
func (a *App) notifyUserDestroyed(c *Req, u *domain.User) {
	if a.Notify == nil || u == nil || !u.AdminFlag || u.Status != domain.StatusActive {
		return
	}
	a.Notify.AdminFlagChanged(c.Ctx(), u, c.User, c.remoteIP(), false)
}
