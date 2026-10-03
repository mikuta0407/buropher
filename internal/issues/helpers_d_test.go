package issues

import (
	"slices"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func repository_AddUserToGroup(c *tc, g, u int64) error {
	return repository.AddUserToGroup(c.ctx, c.d, g, u)
}

// setMailNotification は user.update!(mail_notification: v) / members.update_all(mail_notification: ...)。
func (c *tc) setMailNotification(userID int64, v string) {
	c.t.Helper()
	if c.count(`SELECT COUNT(*) FROM user_notification_settings WHERE user_id = ?`, userID) == 0 {
		c.exec(`INSERT INTO user_notification_settings (user_id, mail_notification) VALUES (?, ?)`, userID, v)
		return
	}
	c.exec(`UPDATE user_notification_settings SET mail_notification = ? WHERE user_id = ?`, v, userID)
}

// setPrefFlag は user.pref.update!(no_self_notified / notify_about_high_priority_issues)。
func (c *tc) setNotifyFlag(userID int64, col string, v bool) {
	c.t.Helper()
	if c.count(`SELECT COUNT(*) FROM user_notification_settings WHERE user_id = ?`, userID) == 0 {
		c.exec(`INSERT INTO user_notification_settings (user_id) VALUES (?)`, userID)
	}
	c.exec(`UPDATE user_notification_settings SET `+col+` = ? WHERE user_id = ?`, v, userID)
}

// setAutoWatchOn は user.pref.auto_watch_on = v; save。
func (c *tc) setAutoWatchOn(userID int64, v string) {
	c.t.Helper()
	if c.count(`SELECT COUNT(*) FROM user_preferences WHERE user_id = ?`, userID) == 0 {
		c.exec(`INSERT INTO user_preferences (user_id, auto_watch_on) VALUES (?, ?)`, userID, v)
		return
	}
	c.exec(`UPDATE user_preferences SET auto_watch_on = ? WHERE user_id = ?`, v, userID)
}

func (c *tc) lockUser(id int64) {
	c.t.Helper()
	c.exec(`UPDATE principals SET status = 3 WHERE id = ?`, id)
}

// saveJournal は Journal#save (チケットを保存せずにジャーナルだけ保存し、コミット後コールバックを実行する)。
func (c *tc) saveJournal(e *Env, iss *Issue, j *Journal) (bool, *SaveResult) {
	c.t.Helper()
	st := &saveState{result: &SaveResult{}}
	ok := false
	c.must(e.inTx(c.ctx, func() error {
		var err error
		if ok, err = e.saveJournal(c.ctx, j, iss, st); err != nil {
			return err
		}
		return e.runCommitCallbacks(c.ctx, st)
	}))
	return ok, st.result
}

// generateJournal は Journal.generate! (journalized は issue 1、user は User.first = 1)。
func (c *tc) generateJournal(e *Env, issueID, userID int64, notes string, private bool, details []*domain.JournalDetail) (*Journal, *SaveResult) {
	c.t.Helper()
	iss := c.issue(issueID)
	j := &Journal{}
	j.IssueID, j.UserID, j.Notes, j.PrivateNotes = issueID, userID, notes, private
	j.newDetails = details
	ok, res := c.saveJournal(e, iss, j)
	if !ok {
		c.t.Fatal("journal not saved")
	}
	return j, res
}

func principalIDs(ps []*PrincipalRef) []int64 {
	out := make([]int64, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

// deliveries は配信数 (受信者ごとに 1 通)。
func deliveries(res *SaveResult) int {
	n := 0
	for _, x := range res.Notifications {
		n += len(x.Recipients)
	}
	return n
}

func deliveredTo(res *SaveResult, id int64) bool {
	for _, x := range res.Notifications {
		if slices.Contains(x.Recipients, id) {
			return true
		}
	}
	return false
}

func notifiedIDs(c *tc, e *Env, iss *Issue) []int64 {
	c.t.Helper()
	us, err := e.NotifiedUsers(c.ctx, iss)
	c.must(err)
	return userIDs(us)
}
