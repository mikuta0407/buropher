package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/discord"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
)

// ジョブ種別。
const (
	JobEmail     = "notify.email"
	JobDiscord   = "notify.discord"
	JobReminders = "notify.reminders"
	JobCleanup   = "maintenance.cleanup"
)

// Service は通知の配送層（受信者ごとのチャネル決定・ジョブ投入・配送）。
type Service struct {
	DB       *db.DB
	Settings *settings.Settings
	Queue    *jobs.Queue
	Renderer Renderer
	// Mail はメールの配送先（nil ならメールを送らない。Redmine の perform_deliveries = false）。
	Mail mail.Sender
	// Discord は Discord API のクライアント（接続先のみ。トークン等は設定から読む）。
	Discord *discord.Client
	// Secrets は設定に保存した Bot トークン等の復号器。
	Secrets *secretbox.Box
	Logger  *slog.Logger
	// Now は現在時刻（nil なら clock.Now）。
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return clock.Now().UTC()
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// RegisterJobs はジョブの処理をキューに登録する。
func (s *Service) RegisterJobs() {
	s.Queue.Register(JobEmail, s.handleEmail)
	s.Queue.Register(JobDiscord, s.handleDiscord)
	s.Queue.Register(JobReminders, s.handleReminders)
	s.Queue.Register(JobCleanup, s.handleCleanup)
}

// MailEnabled はメールを配送するか（email_delivery が設定されているか）。
func (s *Service) MailEnabled() bool {
	if s == nil || s.Mail == nil {
		return false
	}
	_, null := s.Mail.(mail.NullSender)
	return !null
}

// notifiedEvents は Setting.notified_events。
func (s *Service) eventEnabled(ev string) bool {
	return slices.Contains(s.Settings.Strings("notified_events"), ev)
}

// DiscordConfig は Discord 連携の設定（復号済み）。
type DiscordConfig struct {
	Enabled      bool
	BotToken     string
	ClientID     string
	ClientSecret string
	GuildID      string
	JoinGuild    bool
}

// Usable は DM を送れる設定か（有効・Bot トークンあり）。
func (c DiscordConfig) Usable() bool { return c.Enabled && c.BotToken != "" }

// LinkUsable はアカウント連携ができる設定か。
func (c DiscordConfig) LinkUsable() bool { return c.Usable() && c.ClientID != "" && c.ClientSecret != "" }

// OpenSecret は secretbox で暗号化した設定値を復号する（暗号化されていなければそのまま）。
func OpenSecret(box *secretbox.Box, v string) string {
	if !secretbox.IsSealed(v) {
		return v
	}
	if box == nil {
		return ""
	}
	s, err := box.Open(v)
	if err != nil {
		return ""
	}
	return s
}

// DiscordSettings は設定から Discord の設定を読む。
func DiscordSettings(st *settings.Settings, box *secretbox.Box) DiscordConfig {
	return DiscordConfig{
		Enabled:      st.Bool("buropher_discord_enabled"),
		BotToken:     OpenSecret(box, st.String("buropher_discord_bot_token")),
		ClientID:     strings.TrimSpace(st.String("buropher_discord_client_id")),
		ClientSecret: OpenSecret(box, st.String("buropher_discord_client_secret")),
		GuildID:      strings.TrimSpace(st.String("buropher_discord_guild_id")),
		JoinGuild:    st.Bool("buropher_discord_join_guild"),
	}
}

// DiscordConfig は現在の Discord の設定。
func (s *Service) DiscordConfig() DiscordConfig { return DiscordSettings(s.Settings, s.Secrets) }

func (s *Service) failureThreshold() int {
	n := s.Settings.Int("buropher_discord_failure_threshold")
	if n <= 0 {
		return 3
	}
	return n
}

// ChannelsFor はユーザーの通知チャネル（Discord は有効・連携済み・恒久エラーで止まっていない場合のみ。
// それ以外はメールに切り替える）。
func (s *Service) ChannelsFor(ctx context.Context, userID int64) ([]string, error) {
	cfg := s.DiscordConfig()
	if !cfg.Usable() {
		return []string{ChannelEmail}, nil
	}
	ns, err := repository.GetNotificationSetting(ctx, s.DB, userID, s.Settings.String("default_notification_option"), s.Settings.Bool("default_users_no_self_notified"))
	if err != nil {
		return nil, err
	}
	pref := ns.Channels
	if !ns.Explicit {
		pref = s.Settings.String("buropher_default_notification_channel")
	}
	if pref == ChannelEmail || pref == "" {
		return []string{ChannelEmail}, nil
	}
	ok, err := s.discordAvailable(ctx, userID)
	if err != nil {
		return nil, err
	}
	switch {
	case pref == ChannelDiscord && ok:
		return []string{ChannelDiscord}, nil
	case pref == "both" && ok:
		return []string{ChannelEmail, ChannelDiscord}, nil
	}
	return []string{ChannelEmail}, nil
}

// discordAvailable はユーザーに DM を送れる状態か（連携済みで、恒久エラーの連続回数が閾値未満）。
func (s *Service) discordAvailable(ctx context.Context, userID int64) (bool, error) {
	id, err := repository.GetDiscordIdentity(ctx, s.DB, userID)
	if err != nil || id == nil {
		return false, err
	}
	ch, err := repository.GetDiscordDMChannel(ctx, s.DB, userID)
	if err != nil {
		return false, err
	}
	return ch == nil || ch.ConsecutiveFailures < s.failureThreshold(), nil
}

// objectOf は記録用のオブジェクト（種類と id）。
func objectOf(p *Payload) (string, int64) {
	switch {
	case p.JournalID != 0:
		return "journal", p.JournalID
	case p.IssueID != 0:
		return "issue", p.IssueID
	case p.CommentID != 0:
		return "comment", p.CommentID
	case p.NewsID != 0:
		return "news", p.NewsID
	case p.DocumentID != 0:
		return "document", p.DocumentID
	case p.MessageID != 0:
		return "message", p.MessageID
	case p.WikiPageID != 0:
		return "wiki_page", p.WikiPageID
	case len(p.AttachmentIDs) > 0:
		return "attachment", p.AttachmentIDs[0]
	}
	return "", 0
}

// enqueue は 1 受信者分を配送ログに記録し、チャネルのジョブを積む。
func (s *Service) enqueue(ctx context.Context, channel string, p Payload) error {
	if channel == ChannelEmail && !s.MailEnabled() {
		s.logger().Debug("notify: mail delivery is disabled", "kind", p.Kind, "user", p.UserID)
		return nil
	}
	if p.Event == "" {
		p.Event = p.Kind
	}
	kind, oid := objectOf(&p)
	id, err := repository.CreateNotificationDelivery(ctx, s.DB, p.UserID, channel, p.Event, kind, oid, strings.Join(p.Addresses, ", "), s.now())
	if err != nil {
		return err
	}
	p.DeliveryID = id
	job := JobEmail
	if channel == ChannelDiscord {
		job = JobDiscord
	}
	jid, err := s.Queue.Enqueue(ctx, nil, job, p)
	if err != nil {
		return err
	}
	return repository.SetNotificationDeliveryJob(ctx, s.DB, id, jid)
}

// deliverToUsers は各受信者のチャネルへ積む（アカウント系は常にメール）。
func (s *Service) deliverToUsers(ctx context.Context, base Payload, userIDs []int64) error {
	var errs []error
	for _, uid := range userIDs {
		p := base
		p.UserID = uid
		chs := []string{ChannelEmail}
		if !IsAccountKind(p.Kind) {
			var err error
			chs, err = s.ChannelsFor(ctx, uid)
			if err != nil {
				errs = append(errs, err)
				continue
			}
		}
		for _, ch := range chs {
			if err := s.enqueue(ctx, ch, p); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Enqueue は issues.Notifier の実装（チケットの追加・更新の通知。受信者は internal/issues が計算済み）。
func (s *Service) Enqueue(ctx context.Context, n issues.Notification) error {
	if s == nil {
		return nil
	}
	p := Payload{IssueID: n.IssueID, JournalID: n.JournalID}
	switch n.Event {
	case issues.NotifyIssueAdd:
		p.Kind, p.Event = KindIssueAdd, EventIssueAdded
	case issues.NotifyIssueEdit:
		p.Kind, p.Event = KindIssueEdit, EventIssueUpdated
	default:
		return fmt.Errorf("notify: unknown issue notification %q", n.Event)
	}
	return s.deliverToUsers(ctx, p, n.Recipients)
}

// Dispatch は保存結果の通知（issues.SaveResult.Notifications）をまとめて積む。エラーは記録だけする
// （Redmine の deliver_later と同じく、通知の失敗で操作を失敗させない）。
func (s *Service) Dispatch(ctx context.Context, ns []issues.Notification) {
	if s == nil {
		return
	}
	for _, n := range ns {
		if err := s.Enqueue(ctx, n); err != nil {
			s.logger().Error("notify: enqueue failed", "event", n.Event, "issue", n.IssueID, "err", err)
		}
	}
}

// logErr は通知の投入エラーを記録する（呼び出し元の操作は失敗させない）。
func (s *Service) logErr(what string, err error) {
	if err != nil {
		s.logger().Error("notify: "+what, "err", err)
	}
}
