package mailhandler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは未知の送信者のユーザー作成（unknown_user=create。new_user_from_attributes /
// create_user_from_email / add_user_to_group）。

// NewUser は MailHandler.new_user_from_attributes の結果（未保存のユーザー）。
type NewUser struct {
	Login            string
	Mail             string
	Firstname        string
	Lastname         string
	Language         string
	MailNotification string
}

var atDomainRe = regexp.MustCompile(`@.*$`)

// NewUserFromAttributes は MailHandler.new_user_from_attributes(email_address, fullname)。
// login・氏名が不正ならそれぞれ "user<乱数>"・"-" にする（メールアドレスの検証は保存時に行う）。
func (h *Handler) NewUserFromAttributes(ctx context.Context, q db.Queryer, emailAddress, fullname string) (*NewUser, error) {
	u := &NewUser{Mail: emailAddress}
	// メールアドレスを切り詰めると不正な形式になるため mail はそのまま
	u.Login = truncateRunes(emailAddress, domain.LoginLengthLimit)
	var names []string
	if strings.TrimSpace(fullname) == "" {
		names = strings.Split(atDomainRe.ReplaceAllString(emailAddress, ""), ".")
	} else {
		names = strings.Fields(fullname)
	}
	if len(names) > 0 {
		u.Firstname = truncateRunes(names[0], 30)
		names = names[1:]
	}
	u.Lastname = truncateRunes(strings.Join(names, " "), 30)
	if strings.TrimSpace(u.Lastname) == "" {
		u.Lastname = "-"
	}
	u.Language = h.Settings.String("default_language")
	u.MailNotification = "only_my_events"

	loginInvalid, err := h.loginInvalid(ctx, q, u.Login)
	if err != nil {
		return nil, err
	}
	if loginInvalid {
		u.Login = "user" + randomHex(6)
	}
	if strings.TrimSpace(u.Firstname) == "" || utf8.RuneCountInString(u.Firstname) > 30 {
		u.Firstname = "-"
	}
	if strings.TrimSpace(u.Lastname) == "" || utf8.RuneCountInString(u.Lastname) > 255 {
		u.Lastname = "-"
	}
	return u, nil
}

// loginInvalid は login の検証（presence / uniqueness / format / length）にエラーがあるか。
func (h *Handler) loginInvalid(ctx context.Context, q db.Queryer, login string) (bool, error) {
	if strings.TrimSpace(login) == "" || !domain.LoginFormat.MatchString(login) || utf8.RuneCountInString(login) > domain.LoginLengthLimit {
		return true, nil
	}
	return repository.LoginTaken(ctx, q, login, 0)
}

// randomHex は Redmine::Utils.random_hex(n)（2n 文字の 16 進数）。
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// mailInvalid は EmailAddress の検証（presence / format / length / uniqueness / domain）にエラーがあるか。
func (h *Handler) mailInvalid(ctx context.Context, q db.Queryer, address string) (string, error) {
	switch {
	case strings.TrimSpace(address) == "":
		return "Email cannot be blank", nil
	case !domain.EmailRegexp.MatchString(address):
		return "Email is invalid", nil
	case utf8.RuneCountInString(address) > domain.MailLengthLimit:
		return "Email is too long", nil
	}
	taken, err := repository.EmailTaken(ctx, q, address, 0)
	if err != nil {
		return "", err
	}
	if taken {
		return "Email has already been taken", nil
	}
	if !domain.ValidEmailDomain(address, h.Settings.String("email_domains_denied"), h.Settings.String("email_domains_allowed")) {
		return "Email is invalid", nil
	}
	return "", nil
}

// generatePassword は User#random_password（Setting.password_min_length + 2 と 10 の大きい方の長さ）。
func (h *Handler) generatePassword() string {
	length := max(h.Settings.Int("password_min_length")+2, 10)
	sets := []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz", "0123456789"}
	if slices.Contains(h.Settings.Strings("password_required_char_classes"), "special_chars") {
		var sp strings.Builder
		for c := rune(0x20); c <= 0x7e; c++ {
			if domain.IsSpecialChar(c) {
				sp.WriteRune(c)
			}
		}
		sets = append(sets, sp.String())
	}
	for i, s := range sets {
		sets[i] = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`0O1l|'"`+"`*", r) {
				return -1
			}
			return r
		}, s)
	}
	pick := func(s string) byte {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(s))))
		return s[n.Int64()]
	}
	var buf []byte
	for _, s := range sets {
		buf = append(buf, pick(s))
		length--
	}
	all := strings.Join(sets, "")
	for i := 0; i < length; i++ {
		buf = append(buf, pick(all))
	}
	for i := len(buf) - 1; i > 0; i-- {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		j := int(n.Int64())
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// createUserFromEmail は create_user_from_email（送信者のユーザーを作る。作れなければ nil）。
// 生成したパスワードも返す（アカウント情報のメールに載せる）。
func (r *receiver) createUserFromEmail(ctx context.Context) (*domain.User, string, error) {
	from := r.email.From()
	if len(from) == 0 {
		r.h.logger().Error("MailHandler: failed to create User: no FROM address found")
		return nil, "", nil
	}
	addr := from[0]
	name := addr.DisplayName
	if name == "" && len(addr.Comments) > 0 {
		name = addr.Comments[0]
	}
	h := r.h
	nu, err := h.NewUserFromAttributes(ctx, h.DB, addr.Address, name)
	if err != nil {
		return nil, "", err
	}
	if r.opts.noNotification {
		nu.MailNotification = "none"
	}
	if msg, err := h.mailInvalid(ctx, h.DB, nu.Mail); err != nil {
		return nil, "", err
	} else if msg != "" {
		h.logger().Error("MailHandler: failed to create User: [\"" + msg + "\"]")
		return nil, "", nil
	}
	pw := h.generatePassword()
	hash, err := password.Hash(pw)
	if err != nil {
		return nil, "", err
	}
	now := h.now()
	changed := now.UTC().Truncate(time.Second)
	u := &domain.User{Principal: domain.Principal{Kind: domain.KindUser, Status: domain.StatusActive,
		Firstname: nu.Firstname, Lastname: nu.Lastname}}
	u.Login = nu.Login
	u.Language = nu.Language
	u.PasswordHash = hash
	u.PasswordChangedAt = &changed
	n := &domain.UserNotification{MailNotification: nu.MailNotification, NoSelfNotified: h.Settings.Bool("default_users_no_self_notified")}
	if _, err := repository.InsertUser(ctx, h.DB, u, nu.Mail, n, now); err != nil {
		return nil, "", err
	}
	u.Mail = nu.Mail
	u.CreatedAt, u.UpdatedAt = now, now
	return u, pw, nil
}

// addUserToGroup は add_user_to_group（作成したユーザーを既定のグループに追加する）。
func (r *receiver) addUserToGroup(ctx context.Context, defaultGroup string) error {
	if strings.TrimSpace(defaultGroup) == "" {
		return nil
	}
	groups, err := repository.ListGroups(ctx, r.h.DB, false)
	if err != nil {
		return err
	}
	for _, name := range strings.Split(defaultGroup, ",") {
		var found *domain.Group
		for _, g := range groups {
			// Group.named（LOWER(lastname) = LOWER(name.strip)）
			if named(g.Name, name) {
				found = g
				break
			}
		}
		if found == nil {
			r.h.logger().Warn("MailHandler: could not add user to [" + name + "], group not found")
			continue
		}
		if err := repository.AddUserToGroup(ctx, r.h.DB, found.ID, r.user.ID); err != nil {
			return err
		}
	}
	return nil
}

// deliverAccountInformation は Mailer.deliver_account_information(user, password)。
func (r *receiver) deliverAccountInformation(ctx context.Context, u *domain.User, pw string) {
	if r.h.AccountMailer == nil {
		r.h.logger().Info("mail not delivered (no mailer): account_information", "user", u.Login, "to", u.Mail)
		return
	}
	if err := r.h.AccountMailer.AccountInformation(ctx, u, pw); err != nil {
		r.h.logger().Error("mail delivery failed", "kind", "account_information", "err", err)
	}
}
