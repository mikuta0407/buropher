package repository

// 通知（Mailer / internal/notify）用の読み取りクエリ: 受信者計算に使うメンバー・ウォッチャー・通知設定と、
// メール本文の描画に使うニュース・コメント・文書・フォーラムのメッセージ・Wiki の版。

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// NotificationSetting は user_notification_settings の 1 行（行が無い・NULL は既定値を補った値）。
type NotificationSetting struct {
	UserID           int64
	MailNotification string
	NoSelfNotified   bool
	HighPriority     bool
	// Channels は通知チャネル（"email" / "discord" / "both"）。Explicit が false なら行が無い。
	Channels string
	Explicit bool
}

// GetNotificationSetting は user_notification_settings（行が無い・NULL は defaultOption / defaultNoSelf / "email"）。
func GetNotificationSetting(ctx context.Context, q db.Queryer, userID int64, defaultOption string, defaultNoSelf bool) (*NotificationSetting, error) {
	var rows []struct {
		MailNotification *string `db:"mail_notification"`
		NoSelfNotified   bool    `db:"no_self_notified"`
		HighPriority     bool    `db:"notify_about_high_priority_issues"`
		Channels         string  `db:"channels"`
	}
	if err := q.Select(ctx, &rows, `SELECT mail_notification, no_self_notified, notify_about_high_priority_issues, channels
FROM user_notification_settings WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	ns := &NotificationSetting{UserID: userID, MailNotification: defaultOption, NoSelfNotified: defaultNoSelf, Channels: "email"}
	if len(rows) > 0 {
		r := rows[0]
		if r.MailNotification != nil {
			ns.MailNotification = *r.MailNotification
		}
		ns.NoSelfNotified, ns.HighPriority, ns.Channels, ns.Explicit = r.NoSelfNotified, r.HighPriority, r.Channels, true
	}
	return ns, nil
}

// SetNotificationChannels は user_notification_settings.channels を保存する（行が無ければ既定値で作る）。
func SetNotificationChannels(ctx context.Context, q db.Queryer, userID int64, channels string, defaultNoSelf bool) error {
	res, err := q.Exec(ctx, `UPDATE user_notification_settings SET channels = ? WHERE user_id = ?`, channels, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO user_notification_settings (user_id, mail_notification, no_self_notified, notify_about_high_priority_issues, channels)
VALUES (?, NULL, ?, ?, ?)`, userID, defaultNoSelf, false, channels)
	return err
}

// ProjectNotifiedUserIDs は Project#notified_users（有効なユーザーのメンバーのうち、通知対象プロジェクトに
// 選んでいるか mail_notification が all のもの）。
func ProjectNotifiedUserIDs(ctx context.Context, q db.Queryer, projectID int64, defaultOption string) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT m.principal_id FROM members m JOIN principals p ON p.id = m.principal_id
LEFT JOIN user_notification_settings ns ON ns.user_id = p.id
WHERE m.project_id = ? AND p.kind = 'user' AND p.status = 1 AND
(EXISTS (SELECT 1 FROM user_notified_projects unp WHERE unp.user_id = p.id AND unp.project_id = m.project_id)
 OR COALESCE(ns.mail_notification, ?) = 'all') ORDER BY m.id`, projectID, defaultOption)
	return ids, err
}

// ProjectActiveUserIDs は Project#users（有効なユーザーのメンバー）。
func ProjectActiveUserIDs(ctx context.Context, q db.Queryer, projectID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT m.principal_id FROM members m JOIN principals p ON p.id = m.principal_id
WHERE m.project_id = ? AND p.kind = 'user' AND p.status = 1 ORDER BY m.id`, projectID)
	return ids, err
}

// ActiveWatcherUserIDs は watcher_users.active（有効なユーザー・グループ）をグループの有効な所属ユーザーに
// 展開したもの（acts_as_watchable#notified_watchers の前半。重複なし）。
func ActiveWatcherUserIDs(ctx context.Context, q db.Queryer, kind string, id int64) ([]int64, error) {
	var rows []struct {
		ID   int64  `db:"id"`
		Kind string `db:"kind"`
	}
	if err := q.Select(ctx, &rows, `SELECT p.id, p.kind FROM watchers w JOIN principals p ON p.id = w.principal_id
WHERE w.watchable_kind = ? AND w.watchable_id = ? AND p.status = 1 ORDER BY w.id`, kind, id); err != nil {
		return nil, err
	}
	var out []int64
	seen := map[int64]bool{}
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, r := range rows {
		if r.Kind == "user" {
			add(r.ID)
			continue
		}
		var us []int64
		if err := q.Select(ctx, &us, `SELECT gu.user_id FROM group_users gu JOIN principals p ON p.id = gu.user_id
WHERE gu.group_id = ? AND p.status = 1 ORDER BY gu.user_id`, r.ID); err != nil {
			return nil, err
		}
		for _, u := range us {
			add(u)
		}
	}
	return out, nil
}

// ActiveAdminIDs は User.active.where(admin: true)。
func ActiveAdminIDs(ctx context.Context, q db.Queryer) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT p.id FROM principals p JOIN user_accounts ua ON ua.principal_id = p.id
WHERE p.kind = 'user' AND p.status = 1 AND ua.admin = ? ORDER BY p.id`, true)
	return ids, err
}

// NotifyEmailAddresses は Mailer.email_addresses(user)（既定のアドレスと notify のアドレス）。
func NotifyEmailAddresses(ctx context.Context, q db.Queryer, userID int64) ([]string, error) {
	var out []string
	err := q.Select(ctx, &out, `SELECT address FROM email_addresses WHERE user_id = ? AND (is_default = ? OR notify = ?) ORDER BY id`, userID, true, true)
	return out, err
}

// AllEmailAddresses は User#mails（すべてのアドレス）。
func AllEmailAddresses(ctx context.Context, q db.Queryer, userID int64) ([]string, error) {
	var out []string
	err := q.Select(ctx, &out, `SELECT address FROM email_addresses WHERE user_id = ? ORDER BY id`, userID)
	return out, err
}

// IsModuleEnabled はプロジェクトのモジュールが有効か。
func IsModuleEnabled(ctx context.Context, q db.Queryer, projectID int64, module string) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM project_modules WHERE project_id = ? AND name = ?`, projectID, module)
	return n > 0, err
}

// EnabledModuleID は project_modules.id（ウォッチ対象 project_module の id。無ければ 0）。
func EnabledModuleID(ctx context.Context, q db.Queryer, projectID int64, module string) (int64, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT id FROM project_modules WHERE project_id = ? AND name = ?`, projectID, module); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return ids[0], nil
}

func noRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// MailNews はニュース（メール用）。
type MailNews struct {
	ID          int64     `db:"id"`
	ProjectID   int64     `db:"project_id"`
	Title       string    `db:"title"`
	Summary     *string   `db:"summary"`
	Description *string   `db:"description"`
	AuthorID    int64     `db:"author_id"`
	CreatedAt   time.Time `db:"-"`
	Created     db.Time   `db:"created_at"`
}

// GetMailNews はニュースを返す。
func GetMailNews(ctx context.Context, q db.Queryer, id int64) (*MailNews, error) {
	var n MailNews
	if err := q.Get(ctx, &n, `SELECT id, project_id, title, summary, description, author_id, created_at FROM news WHERE id = ?`, id); err != nil {
		return nil, noRows(err)
	}
	n.CreatedAt = n.Created.Time
	return &n, nil
}

// MailComment はニュースのコメント（Comment）。
type MailComment struct {
	ID       int64   `db:"id"`
	NewsID   int64   `db:"news_id"`
	AuthorID int64   `db:"author_id"`
	Content  *string `db:"content"`
	Created  db.Time `db:"created_at"`
}

// GetMailComment はコメントを返す。
func GetMailComment(ctx context.Context, q db.Queryer, id int64) (*MailComment, error) {
	var c MailComment
	if err := q.Get(ctx, &c, `SELECT id, news_id, author_id, content, created_at FROM news_comments WHERE id = ?`, id); err != nil {
		return nil, noRows(err)
	}
	return &c, nil
}

// MailDocument は文書。
type MailDocument struct {
	ID           int64   `db:"id"`
	ProjectID    int64   `db:"project_id"`
	CategoryID   int64   `db:"category_id"`
	CategoryName string  `db:"category_name"`
	Title        string  `db:"title"`
	Description  *string `db:"description"`
	Created      db.Time `db:"created_at"`
}

// GetMailDocument は文書を返す。
func GetMailDocument(ctx context.Context, q db.Queryer, id int64) (*MailDocument, error) {
	var d MailDocument
	if err := q.Get(ctx, &d, `SELECT d.id, d.project_id, d.category_id, COALESCE(c.name, '') AS category_name, d.title, d.description, d.created_at
FROM documents d LEFT JOIN document_categories c ON c.id = d.category_id WHERE d.id = ?`, id); err != nil {
		return nil, noRows(err)
	}
	return &d, nil
}

// MailMessage はフォーラムのメッセージ。
type MailMessage struct {
	ID        int64   `db:"id"`
	BoardID   int64   `db:"board_id"`
	ParentID  *int64  `db:"parent_id"`
	Subject   string  `db:"subject"`
	Content   *string `db:"content"`
	AuthorID  *int64  `db:"author_id"`
	Created   db.Time `db:"created_at"`
	BoardName string  `db:"board_name"`
	ProjectID int64   `db:"project_id"`
}

// RootID は message.root.id。
func (m *MailMessage) RootID() int64 {
	if m.ParentID != nil {
		return *m.ParentID
	}
	return m.ID
}

// GetMailMessage はメッセージを返す。
func GetMailMessage(ctx context.Context, q db.Queryer, id int64) (*MailMessage, error) {
	var m MailMessage
	if err := q.Get(ctx, &m, `SELECT m.id, m.board_id, m.parent_id, m.subject, m.content, m.author_id, m.created_at,
b.name AS board_name, b.project_id FROM messages m JOIN boards b ON b.id = m.board_id WHERE m.id = ?`, id); err != nil {
		return nil, noRows(err)
	}
	return &m, nil
}

// MailWikiContent は Wiki ページの版（WikiContent。version が 0 なら最新版）。
type MailWikiContent struct {
	PageID    int64   `db:"page_id"`
	WikiID    int64   `db:"wiki_id"`
	ProjectID int64   `db:"project_id"`
	Title     string  `db:"title"`
	Version   int     `db:"version"`
	AuthorID  *int64  `db:"author_id"`
	Text      string  `db:"text"`
	Comments  *string `db:"comments"`
	Updated   db.Time `db:"updated_at"`
}

// GetMailWikiContent は Wiki ページの版を返す（version 0 なら current_version）。
func GetMailWikiContent(ctx context.Context, q db.Queryer, pageID int64, version int) (*MailWikiContent, error) {
	var w MailWikiContent
	cond := `v.version = wp.current_version`
	args := []any{pageID}
	if version > 0 {
		cond = `v.version = ?`
		args = append(args, version)
	}
	if err := q.Get(ctx, &w, `SELECT wp.id AS page_id, wp.wiki_id, wk.project_id, wp.title, v.version, v.author_id, v.text, v.comments, v.updated_at
FROM wiki_pages wp JOIN wikis wk ON wk.id = wp.wiki_id JOIN wiki_page_versions v ON v.page_id = wp.id
WHERE wp.id = ? AND `+cond, args...); err != nil {
		return nil, noRows(err)
	}
	return &w, nil
}

// MailAttachment は添付（メール用）。
type MailAttachment struct {
	ID            int64   `db:"id"`
	ContainerKind *string `db:"container_kind"`
	ContainerID   *int64  `db:"container_id"`
	Filename      string  `db:"filename"`
	Filesize      int64   `db:"filesize"`
	AuthorID      int64   `db:"author_id"`
	Created       db.Time `db:"created_at"`
}

// MailAttachmentsByIDs は ids の添付を ids の順で返す（見つからないものは除く）。
func MailAttachmentsByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*MailAttachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var rows []*MailAttachment
	if err := q.Select(ctx, &rows, `SELECT id, container_kind, container_id, filename, filesize, author_id, created_at FROM attachments WHERE id IN (`+ph+`)`, args...); err != nil {
		return nil, err
	}
	by := map[int64]*MailAttachment{}
	for _, r := range rows {
		by[r.ID] = r
	}
	var out []*MailAttachment
	for _, id := range ids {
		if a := by[id]; a != nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// MailContainerAttachments はコンテナの添付（created_on, id 順）。
func MailContainerAttachments(ctx context.Context, q db.Queryer, kind string, id int64) ([]*MailAttachment, error) {
	var rows []*MailAttachment
	err := q.Select(ctx, &rows, `SELECT id, container_kind, container_id, filename, filesize, author_id, created_at FROM attachments
WHERE container_kind = ? AND container_id = ? ORDER BY created_at, id`, kind, id)
	return rows, err
}

// VersionProject は versions.project_id と name。
func VersionProject(ctx context.Context, q db.Queryer, versionID int64) (projectID int64, name string, err error) {
	var r struct {
		ProjectID int64  `db:"project_id"`
		Name      string `db:"name"`
	}
	if err := q.Get(ctx, &r, `SELECT project_id, name FROM versions WHERE id = ?`, versionID); err != nil {
		return 0, "", noRows(err)
	}
	return r.ProjectID, r.Name, nil
}

// JournalCreatedAt は issue_journals.created_at。
func JournalCreatedAt(ctx context.Context, q db.Queryer, id int64) (time.Time, error) {
	var t db.Time
	if err := q.Get(ctx, &t, `SELECT created_at FROM issue_journals WHERE id = ?`, id); err != nil {
		return time.Time{}, noRows(err)
	}
	return t.Time, nil
}

// DiscordIdentity は user_identities（provider='discord'）。
type DiscordIdentity struct {
	UserID    int64   `db:"user_id"`
	Subject   string  `db:"subject"`
	RawClaims *string `db:"raw_claims"`
	Created   db.Time `db:"created_at"`
}

// GetDiscordIdentity はユーザーの Discord 連携（無ければ nil）。
func GetDiscordIdentity(ctx context.Context, q db.Queryer, userID int64) (*DiscordIdentity, error) {
	var rows []*DiscordIdentity
	if err := q.Select(ctx, &rows, `SELECT user_id, subject, raw_claims, created_at FROM user_identities WHERE user_id = ? AND provider = 'discord' ORDER BY id`, userID); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// DiscordIdentityOwner は Discord ユーザー ID を連携しているユーザー（無ければ 0）。
func DiscordIdentityOwner(ctx context.Context, q db.Queryer, subject string) (int64, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT user_id FROM user_identities WHERE provider = 'discord' AND subject = ?`, subject); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return ids[0], nil
}

// LinkDiscordIdentity は Discord 連携を保存する（既存の連携は置き換える）。
func LinkDiscordIdentity(ctx context.Context, q db.Queryer, userID int64, subject string, rawClaims string, now time.Time) error {
	if _, err := q.Exec(ctx, `DELETE FROM user_identities WHERE provider = 'discord' AND (user_id = ? OR subject = ?)`, userID, subject); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM discord_dm_channels WHERE user_id = ?`, userID); err != nil {
		return err
	}
	var raw any
	if rawClaims != "" {
		raw = rawClaims
	}
	_, err := q.Exec(ctx, `INSERT INTO user_identities (user_id, provider, subject, raw_claims, created_at) VALUES (?, 'discord', ?, ?, ?)`,
		userID, subject, raw, db.NewTime(now))
	return err
}

// UnlinkDiscordIdentity は Discord 連携を削除する。
func UnlinkDiscordIdentity(ctx context.Context, q db.Queryer, userID int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM user_identities WHERE provider = 'discord' AND user_id = ?`, userID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM discord_dm_channels WHERE user_id = ?`, userID)
	return err
}

// DiscordDMChannel は discord_dm_channels の行。
type DiscordDMChannel struct {
	UserID              int64   `db:"user_id"`
	DiscordUserID       string  `db:"discord_user_id"`
	ChannelID           string  `db:"channel_id"`
	ConsecutiveFailures int     `db:"consecutive_failures"`
	LastError           *string `db:"last_error"`
}

// GetDiscordDMChannel はキャッシュした DM チャンネル（無ければ nil）。
func GetDiscordDMChannel(ctx context.Context, q db.Queryer, userID int64) (*DiscordDMChannel, error) {
	var rows []*DiscordDMChannel
	if err := q.Select(ctx, &rows, `SELECT user_id, discord_user_id, channel_id, consecutive_failures, last_error FROM discord_dm_channels WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// SaveDiscordDMChannel は DM チャンネルを保存する（既存の行の失敗回数はそのまま）。
func SaveDiscordDMChannel(ctx context.Context, q db.Queryer, userID int64, discordUserID, channelID string, now time.Time) error {
	res, err := q.Exec(ctx, `UPDATE discord_dm_channels SET discord_user_id = ?, channel_id = ?, updated_at = ? WHERE user_id = ?`,
		discordUserID, channelID, db.NewTime(now), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO discord_dm_channels (user_id, discord_user_id, channel_id, consecutive_failures, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)`,
		userID, discordUserID, channelID, db.NewTime(now), db.NewTime(now))
	return err
}

// MarkDiscordDMSuccess は送信成功で失敗回数を 0 に戻す。
func MarkDiscordDMSuccess(ctx context.Context, q db.Queryer, userID int64, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE discord_dm_channels SET consecutive_failures = 0, last_error = NULL, updated_at = ? WHERE user_id = ? AND consecutive_failures <> 0`,
		db.NewTime(now), userID)
	return err
}

// MarkDiscordDMFailure は恒久エラーを記録して失敗回数を返す（行が無ければ channel_id 空で作る）。
func MarkDiscordDMFailure(ctx context.Context, q db.Queryer, userID int64, discordUserID, msg string, now time.Time) (int, error) {
	res, err := q.Exec(ctx, `UPDATE discord_dm_channels SET consecutive_failures = consecutive_failures + 1, last_error = ?, updated_at = ? WHERE user_id = ?`,
		msg, db.NewTime(now), userID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := q.Exec(ctx, `INSERT INTO discord_dm_channels (user_id, discord_user_id, channel_id, consecutive_failures, last_error, created_at, updated_at)
VALUES (?, ?, '', 1, ?, ?, ?)`, userID, discordUserID, msg, db.NewTime(now), db.NewTime(now)); err != nil {
			return 0, err
		}
	}
	var n int
	err = q.Get(ctx, &n, `SELECT consecutive_failures FROM discord_dm_channels WHERE user_id = ?`, userID)
	return n, err
}

// ResetDiscordDMFailures は失敗回数を 0 に戻す（連携し直し・テスト DM 成功時）。
func ResetDiscordDMFailures(ctx context.Context, q db.Queryer, userID int64) error {
	_, err := q.Exec(ctx, `UPDATE discord_dm_channels SET consecutive_failures = 0, last_error = NULL WHERE user_id = ?`, userID)
	return err
}

// CreateNotificationDelivery は notification_deliveries に pending の行を作る。
func CreateNotificationDelivery(ctx context.Context, q db.Queryer, userID int64, channel, event, objectKind string, objectID int64, recipient string, now time.Time) (int64, error) {
	var uid, oid any
	if userID != 0 {
		uid = userID
	}
	if objectID != 0 {
		oid = objectID
	}
	var ok any
	if objectKind != "" {
		ok = objectKind
	}
	return q.InsertReturningID(ctx, `INSERT INTO notification_deliveries (user_id, channel, event, object_kind, object_id, recipient, status, attempts, created_at)
VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?)`, uid, channel, event, ok, oid, recipient, db.NewTime(now))
}

// SetNotificationDeliveryJob は配送ログにジョブ id を記録する。
func SetNotificationDeliveryJob(ctx context.Context, q db.Queryer, id, jobID int64) error {
	_, err := q.Exec(ctx, `UPDATE notification_deliveries SET job_id = ? WHERE id = ?`, jobID, id)
	return err
}

// UpdateNotificationDelivery は配送ログの状態を更新する（status: sent / failed / skipped / pending）。
func UpdateNotificationDelivery(ctx context.Context, q db.Queryer, id int64, status, recipient, errMsg string, now time.Time) error {
	var e any
	if errMsg != "" {
		e = errMsg
	}
	var sent any
	if status == "sent" {
		sent = db.NewTime(now)
	}
	_, err := q.Exec(ctx, `UPDATE notification_deliveries SET status = ?, attempts = attempts + 1, error = ?, sent_at = COALESCE(?, sent_at),
recipient = CASE WHEN ? <> '' THEN ? ELSE recipient END WHERE id = ?`, status, e, sent, recipient, recipient, id)
	return err
}

// NotificationDelivery は配送ログの 1 行。
type NotificationDelivery struct {
	ID         int64       `db:"id"`
	JobID      *int64      `db:"job_id"`
	UserID     *int64      `db:"user_id"`
	Channel    string      `db:"channel"`
	Event      string      `db:"event"`
	ObjectKind *string     `db:"object_kind"`
	ObjectID   *int64      `db:"object_id"`
	Recipient  *string     `db:"recipient"`
	Status     string      `db:"status"`
	Attempts   int         `db:"attempts"`
	Error      *string     `db:"error"`
	Created    db.Time     `db:"created_at"`
	SentAt     db.NullTime `db:"sent_at"`
}

// ListNotificationDeliveries は配送ログ（新しい順、userID 0 なら全ユーザー）。
func ListNotificationDeliveries(ctx context.Context, q db.Queryer, userID int64, limit int) ([]*NotificationDelivery, error) {
	var rows []*NotificationDelivery
	where, args := "", []any{}
	if userID != 0 {
		where, args = " WHERE user_id = ?", append(args, userID)
	}
	args = append(args, limit)
	err := q.Select(ctx, &rows, `SELECT id, job_id, user_id, channel, event, object_kind, object_id, recipient, status, attempts, error, created_at, sent_at
FROM notification_deliveries`+where+` ORDER BY id DESC LIMIT ?`, args...)
	return rows, err
}

// PurgeNotificationDeliveries は before より前の配送ログを消す。
func PurgeNotificationDeliveries(ctx context.Context, q db.Queryer, before time.Time) (int64, error) {
	res, err := q.Exec(ctx, `DELETE FROM notification_deliveries WHERE created_at < ? AND status <> 'pending'`, db.NewTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
