// Package mailhandler は Redmine のメール受信（app/models/mail_handler.rb）の移植。
//
// 受信したメールを解析し、送信者のユーザーで新しいチケット・チケットへの返信（ノート）・
// フォーラムの返信・ニュースのコメントを作る。入口は次の 3 つ:
//
//   - HTTP: POST /mail_handler（MailHandlerController。internal/handler/mail_handler.go）
//   - CLI: buropher mail receive --stdin | --imap | --pop3（rake redmine:email:read / receive_imap / receive_pop3）
//   - 定期取得: config の [mail_receive]（IMAP / POP3 をサーバー内で定期的に取得する）
//
// 使い方:
//
//	h := &mailhandler.Handler{DB: d, Settings: st, Bundle: i18n.Default(), Attachments: store}
//	obj := h.SafeReceive(ctx, raw, mailhandler.Options{UnknownUser: "accept"})
//	// obj は *issues.Issue / *issues.Journal / *domain.Message / *domain.Comment（受け付けなければ nil）
package mailhandler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
)

// AccountMailer はメール受信で作成したユーザーへのアカウント情報のメール（Mailer.deliver_account_information）。
type AccountMailer interface {
	AccountInformation(ctx context.Context, user *domain.User, password string) error
}

// ContentNotifier はフォーラムの返信（message_posted）・ニュースのコメント（news_comment_added）の通知。
// Setting.notified_events に含まれるときだけ呼ばれる。obj は *domain.Message / *domain.Comment。
type ContentNotifier interface {
	Notify(ctx context.Context, action string, obj any)
}

// Handler はメール受信の依存（MailHandler クラス）。
type Handler struct {
	DB       *db.DB
	Settings *settings.Settings
	// Bundle は翻訳（キーワードの訳語・"(no subject)"）。nil なら i18n.Default()。
	Bundle *i18n.Bundle
	// Attachments は添付ファイルの保存先（nil なら添付を保存しない）。
	Attachments *attachments.Store
	// Now は現在時刻（nil なら clock.Now）。
	Now func() time.Time
	// Logger はログ出力先（nil なら slog.Default()）。
	Logger *slog.Logger
	// IssueNotifier はチケットの通知（issue_added / issue_updated）の配送先（nil なら破棄）。
	IssueNotifier issues.Notifier
	// ContentNotifier はフォーラム・ニュースの通知（nil なら通知しない）。
	ContentNotifier ContentNotifier
	// AccountMailer は作成したユーザーへのアカウント情報のメール（nil ならログに記録するだけ）。
	AccountMailer AccountMailer
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return clock.Now()
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *Handler) bundle() *i18n.Bundle {
	if h.Bundle != nil {
		return h.Bundle
	}
	return i18n.Default()
}

// ---------------------------------------------------------------- 例外（MailHandler の例外クラス）

type errKind int

const (
	kindUnauthorized errKind = iota // UnauthorizedAction（NotAllowedInProject / InsufficientPermissions / LockedTopic）
	kindMissingInformation
	kindMissingContainer
	kindRecordInvalid
)

// handlerError は dispatch で rescue される例外。
type handlerError struct {
	kind errKind
	msg  string
}

func (e *handlerError) Error() string { return e.msg }

func unauthorized(format string, args ...any) error {
	return &handlerError{kindUnauthorized, fmt.Sprintf(format, args...)}
}

func missingInformation(msg string) error { return &handlerError{kindMissingInformation, msg} }

func missingContainer(format string, args ...any) error {
	return &handlerError{kindMissingContainer, fmt.Sprintf(format, args...)}
}

func recordInvalid(msg string) error { return &handlerError{kindRecordInvalid, msg} }

// ---------------------------------------------------------------- 受信

// receiver は 1 通のメールの受信処理（MailHandler のインスタンス）。
type receiver struct {
	h     *Handler
	email *Part
	user  *domain.User
	opts  *handlerOptions

	plainBody *string
	cleanBody *string
	keywords  map[string]*string
	excluded  []string
	// created は保存した添付（ロールバック時にディスクから消す）。
	created []*domain.Attachment
}

// Receive は MailHandler.receive(raw_mail, options)。受け付けたオブジェクト（*issues.Issue / *issues.Journal /
// *domain.Message / *domain.Comment）を返し、受け付けなければ nil を返す。
// err は想定外の障害（DB エラー等）。Redmine では例外として呼び出し側に伝わる。
func (h *Handler) Receive(ctx context.Context, raw []byte, o Options) (any, error) {
	r := &receiver{h: h, email: Parse(raw), opts: o.normalize(), keywords: map[string]*string{}}
	return r.receive(ctx)
}

// SafeReceive は MailHandler.safe_receive（想定外のエラーはログに記録して nil を返す）。
func (h *Handler) SafeReceive(ctx context.Context, raw []byte, o Options) (obj any) {
	defer func() {
		if p := recover(); p != nil {
			h.logger().Error(fmt.Sprintf("MailHandler: an unexpected error occurred when receiving email: %v", p))
			obj = nil
		}
	}()
	obj, err := h.Receive(ctx, raw, o)
	if err != nil {
		h.logger().Error("MailHandler: an unexpected error occurred when receiving email: " + err.Error())
		return nil
	}
	return obj
}

var (
	emissionAddressRe = regexp.MustCompile(`(?:.*<|>.*|\(.*\))`)
	autoSubmittedRe   = regexp.MustCompile(`\Aauto-(replied|generated)`)
)

// ignoredEmailsHeaders は MailHandler.ignored_emails_headers（自動送信のメールを無視する）。
var ignoredEmailsHeaders = []struct {
	name  string
	match func(string) bool
}{
	{"Auto-Submitted", autoSubmittedRe.MatchString},
	{"X-Autoreply", func(v string) bool { return v == "yes" }},
}

// senderEmail は email.from.to_a.first.to_s.strip。
func (r *receiver) senderEmail() string {
	from := r.email.From()
	if len(from) == 0 {
		return ""
	}
	return strings.TrimSpace(from[0].Address)
}

func (r *receiver) receive(ctx context.Context) (any, error) {
	log := r.h.logger()
	sender := r.senderEmail()
	// Redmine の送信元アドレスからのメールは無視する（ループ防止）
	emission := strings.TrimSpace(emissionAddressRe.ReplaceAllString(r.h.Settings.String("mail_from"), ""))
	if strings.EqualFold(sender, emission) {
		log.Info("MailHandler: ignoring email from Redmine emission address [" + sender + "]")
		return nil, nil
	}
	// 自動送信のメールは無視する
	for _, ig := range ignoredEmailsHeaders {
		if v, ok := r.email.HeaderValue(ig.name); ok {
			v = strings.ToLower(decodeWords(v))
			if ig.match(v) {
				log.Info("MailHandler: ignoring email with " + ig.name + ":" + v + " header")
				return nil, nil
			}
		}
	}
	if sender != "" {
		u, err := repository.FindUserByMail(ctx, r.h.DB, sender)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		r.user = u
	}
	if r.user != nil && !r.user.Active() {
		log.Info("MailHandler: ignoring email from non-active user [" + r.user.Login + "]")
		return nil, nil
	}
	if r.user == nil {
		// 未知のユーザーからのメール
		switch r.opts.unknownUser {
		case "accept":
			u, err := repository.AnonymousUser(ctx, r.h.DB)
			if err != nil {
				return nil, err
			}
			r.user = u
		case "create":
			u, password, err := r.createUserFromEmail(ctx)
			if err != nil {
				return nil, err
			}
			if u == nil {
				log.Error("MailHandler: could not create account for [" + sender + "]")
				return nil, nil
			}
			r.user = u
			log.Info("MailHandler: [" + u.Login + "] account created")
			if err := r.addUserToGroup(ctx, r.opts.defaultGroup); err != nil {
				return nil, err
			}
			if !r.opts.noAccountNotice {
				r.deliverAccountInformation(ctx, u, password)
			}
		default:
			// 既定では未知のユーザーからのメールは無視する
			log.Info("MailHandler: ignoring email from unknown user [" + sender + "]")
			return nil, nil
		}
	}
	return r.dispatch(ctx)
}

var (
	messageIDRe           = regexp.MustCompile(`^<?redmine\.([a-z0-9_]+)\-(\d+)\.\d+(\.[a-f0-9]+)?@`)
	issueReplySubjectRe   = regexp.MustCompile(`\[(?:[^\]]*\s+)?#(\d+)\]`)
	messageReplySubjectRe = regexp.MustCompile(`\[[^\]]*msg(\d+)\]`)
)

// userString は ログに出す #{user}（User#to_s）。
func (r *receiver) userString() string {
	if r.user == nil {
		return ""
	}
	return strings.TrimSpace(r.user.Name(r.h.Settings.String("user_format")))
}

// dispatch は In-Reply-To / References のメッセージ ID、件名の [#123] / [msg123] から処理を振り分ける。
func (r *receiver) dispatch(ctx context.Context) (any, error) {
	obj, err := r.dispatchInner(ctx)
	var he *handlerError
	if errors.As(err, &he) {
		log := r.h.logger()
		switch he.kind {
		case kindRecordInvalid:
			// TODO: send a email to the user（Redmine と同じく未実装）
			log.Error("MailHandler: " + he.msg)
		case kindMissingInformation:
			log.Error("MailHandler: missing information from " + r.userString() + ": " + he.msg)
		case kindMissingContainer:
			log.Error("MailHandler: reply to nonexistant object from " + r.userString() + ": " + he.msg)
		case kindUnauthorized:
			log.Error("MailHandler: unauthorized attempt from " + r.userString() + ": " + he.msg)
		}
		return nil, nil
	}
	return obj, err
}

func (r *receiver) dispatchInner(ctx context.Context) (any, error) {
	headers := append(r.email.MessageIDs("In-Reply-To"), r.email.MessageIDs("References")...)
	subject := r.email.Subject()
	for _, hd := range headers {
		m := messageIDRe.FindStringSubmatch(hd)
		if m == nil {
			continue
		}
		id, _ := strconv.ParseInt(m[2], 10, 64)
		switch m[1] {
		case "issue":
			return r.receiveIssueReply(ctx, id, nil)
		case "journal":
			return r.receiveJournalReply(ctx, id)
		case "message":
			return r.receiveMessageReply(ctx, id)
		case "news":
			return r.receiveNewsReply(ctx, id)
		case "comment":
			return r.receiveCommentReply(ctx, id)
		}
		// 対応する受信処理の無い種類は無視する
		return nil, nil
	}
	if m := issueReplySubjectRe.FindStringSubmatch(subject); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		return r.receiveIssueReply(ctx, id, nil)
	}
	if m := messageReplySubjectRe.FindStringSubmatch(subject); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		return r.receiveMessageReply(ctx, id)
	}
	return r.receiveIssue(ctx)
}
