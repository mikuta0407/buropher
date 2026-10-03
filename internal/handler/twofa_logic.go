// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"

	"github.com/mikuta0407/buropher/internal/auth/totp"
	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/redmineimport/rediscipher"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは Redmine::Twofa（lib/redmine/twofa/base.rb, totp.rb）の移植。
// 利用できる方式は totp のみ（Redmine::Twofa.available_schemes）。
//
// TOTP 鍵は user_accounts.twofa_totp_key に secretbox 形式（"sb1:..."）で保存する。
// Redmine から移行した鍵はインポート時に database_cipher_key で復号し再暗号化済み。
// secretbox の鍵（server.secret_key）が無い環境では平文で保存する（Redmine の database_cipher_key 未設定と同じ）。

// twofaSchemes は Redmine::Twofa.available_schemes。
var twofaSchemes = []string{"totp"}

// errTwofaKey は TOTP 鍵を復号できない。
var errTwofaKey = errors.New("twofa: TOTP key cannot be decrypted")

// twofaTotp は Redmine::Twofa::Totp.new(user)。
type twofaTotp struct {
	a    *App
	c    *Req
	user *domain.User
	st   *domain.TwofaState
}

// twofaFor は Redmine::Twofa::Totp.new(user)（状態を DB から読む）。
func (a *App) twofaFor(c *Req, user *domain.User) (*twofaTotp, error) {
	st, err := repository.GetTwofaState(c.Ctx(), a.DB, user.ID)
	if err != nil {
		return nil, err
	}
	return &twofaTotp{a: a, c: c, user: user, st: st}, nil
}

func (t *twofaTotp) SchemeName() string { return "totp" }

// sealKey は TOTP 鍵を保存形式にする（write_ciphered_attribute）。
func (a *App) sealKey(plain string) (string, error) {
	if a.Secrets == nil {
		return plain, nil
	}
	return a.Secrets.Seal(plain)
}

// openKey は保存形式の TOTP 鍵を復号する（read_ciphered_attribute）。
func (a *App) openKey(stored string) (string, error) {
	switch {
	case stored == "":
		return "", errTwofaKey
	case secretbox.IsSealed(stored):
		if a.Secrets == nil {
			return "", errTwofaKey
		}
		return a.Secrets.Open(stored)
	case rediscipher.IsEncrypted(stored):
		// Redmine の暗号化形式のまま（インポートで再暗号化されていない）
		return "", errTwofaKey
	}
	return stored, nil
}

// key は復号した TOTP 鍵。
func (t *twofaTotp) key() (string, error) { return t.a.openKey(t.st.TotpKey) }

// initPairing は Totp#init_pairing!（新しい鍵を生成して保存する）。
func (t *twofaTotp) initPairing() error {
	sealed, err := t.a.sealKey(totp.RandomKey())
	if err != nil {
		return err
	}
	if err := repository.SetTwofaTotpKey(t.c.Ctx(), t.a.DB, t.user.ID, sealed); err != nil {
		return err
	}
	t.st.TotpKey = sealed
	return nil
}

// verifyOTP は Totp#verify_otp!（成功したタイムステップを twofa_totp_last_used_at に保存する）。
func (t *twofaTotp) verifyOTP(code string) (bool, error) {
	key, err := t.key()
	if err != nil {
		t.a.logger().Error("twofa key", "user", t.user.Login, "err", err)
		return false, nil
	}
	at, ok := totp.Verify(key, code, t.a.now(), t.st.TotpLastUsedAt)
	if !ok {
		return false, nil
	}
	if err := repository.SetTwofaTotpLastUsed(t.c.Ctx(), t.a.DB, t.user.ID, at); err != nil {
		return false, err
	}
	t.st.TotpLastUsedAt = &at
	return true, nil
}

// backupCodeDigest はバックアップコードの保存値（空白を除き小文字にしたコードの SHA-256 hex）。
func backupCodeDigest(code string) string {
	code = strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, code))
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// verifyBackupCode は Base#verify_backup_code!（使ったコードは削除し、セキュリティ通知を送る）。
func (t *twofaTotp) verifyBackupCode(code string) (bool, error) {
	if strings.TrimSpace(code) == "" {
		return false, nil
	}
	ok, err := repository.ConsumeTwofaBackupCode(t.c.Ctx(), t.a.DB, t.user.ID, backupCodeDigest(code))
	if err != nil || !ok {
		return false, err
	}
	t.a.deliver("security_notification", t.a.accountMailer().SecurityNotification(t.c.Ctx(), t.user, t.c.User, SecurityNotice{
		Message: "twofa_mail_body_backup_code_used", Title: "label_my_account", URL: t.a.settingURL("/my/account")}))
	return true, nil
}

// verify は Base#verify!（OTP またはバックアップコード）。
func (t *twofaTotp) verify(code string) (bool, error) {
	ok, err := t.verifyOTP(code)
	if err != nil || ok {
		return ok, err
	}
	return t.verifyBackupCode(code)
}

// confirmPairing は Base#confirm_pairing!（OTP のみ。バックアップコードは使えない）。
func (t *twofaTotp) confirmPairing(code string) (bool, error) {
	ok, err := t.verifyOTP(code)
	if err != nil || !ok {
		return false, err
	}
	if err := repository.ActivateTwofa(t.c.Ctx(), t.a.DB, t.user.ID, t.SchemeName(), t.a.now()); err != nil {
		return false, err
	}
	t.user.TwofaScheme = t.SchemeName()
	t.a.deliver("security_notification", t.a.accountMailer().SecurityNotification(t.c.Ctx(), t.user, t.c.User, SecurityNotice{
		Message: "twofa_mail_body_security_notification_paired", Title: "label_my_account", Field: "twofa__totp__name",
		URL: t.a.settingURL("/my/account")}))
	return true, nil
}

// destroyPairing は Base#destroy_pairing!。
func (t *twofaTotp) destroyPairing(code string) (bool, error) {
	ok, err := t.verify(code)
	if err != nil || !ok {
		return false, err
	}
	return true, t.destroyPairingWithoutVerify()
}

// destroyPairingWithoutVerify は destroy_pairing_without_verify!。
func (t *twofaTotp) destroyPairingWithoutVerify() error {
	if err := repository.DeactivateTwofa(t.c.Ctx(), t.a.DB, t.user.ID, t.a.now()); err != nil {
		return err
	}
	t.user.TwofaScheme = ""
	t.a.deliver("security_notification", t.a.accountMailer().SecurityNotification(t.c.Ctx(), t.user, t.c.User, SecurityNotice{
		Message: "twofa_mail_body_security_notification_unpaired", Title: "label_my_account", URL: t.a.settingURL("/my/account")}))
	return nil
}

// initBackupCodes は Base#init_backup_codes!（10 個の 12 桁 16 進コードを作り、平文を返す）。
func (t *twofaTotp) initBackupCodes() ([]string, error) {
	codes := make([]string, 10)
	digests := make([]string, 10)
	for i := range codes {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		codes[i] = hex.EncodeToString(b[:])
		digests[i] = backupCodeDigest(codes[i])
	}
	err := t.a.DB.WithTx(t.c.Ctx(), func(tx *db.Tx) error {
		return repository.ReplaceTwofaBackupCodes(t.c.Ctx(), tx, t.user.ID, digests, t.a.now())
	})
	if err != nil {
		return nil, err
	}
	t.a.deliver("security_notification", t.a.accountMailer().SecurityNotification(t.c.Ctx(), t.user, t.c.User, SecurityNotice{
		Message: "twofa_mail_body_backup_codes_generated", Title: "label_my_account", URL: t.a.settingURL("/my/account")}))
	return codes, nil
}

// twofaView は @twofa_view（otp_confirm_view_variables / init_pairing_view_variables）。
type twofaView struct {
	SchemeName      string
	Resendable      bool
	ProvisioningURI string
	TotpKey         string
	// QRCode は provisioning_uri の QR コード（PNG の data URL）。
	QRCode string
}

// TotpKeyGroups は totp_key.scan(/.{4}/).join(' ')。
func (v twofaView) TotpKeyGroups() string { return groupsOf4(v.TotpKey) }

// groupsOf4 は s.scan(/.{4}/).join(' ')（4 文字未満の端数は捨てる）。
func groupsOf4(s string) string {
	var parts []string
	r := []rune(s)
	for i := 0; i+4 <= len(r); i += 4 {
		parts = append(parts, string(r[i:i+4]))
	}
	return strings.Join(parts, " ")
}

// otpConfirmView は otp_confirm_view_variables。
func (t *twofaTotp) otpConfirmView() twofaView {
	return twofaView{SchemeName: t.SchemeName()}
}

// initPairingView は Totp#init_pairing_view_variables（登録用 URI・鍵・QR コード）。
func (t *twofaTotp) initPairingView() (twofaView, error) {
	v := t.otpConfirmView()
	key, err := t.key()
	if err != nil {
		return v, err
	}
	v.TotpKey = key
	v.ProvisioningURI = totp.ProvisioningURI(key, t.a.Settings.String("host_name"), t.user.Login)
	qr, err := totp.QRCodeDataURL(v.ProvisioningURI)
	if err != nil {
		return v, err
	}
	v.QRCode = qr
	return v, nil
}
