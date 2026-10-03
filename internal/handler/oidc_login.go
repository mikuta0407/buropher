// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/auth/oidc"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

// このファイルは OIDC シングルサインオン（buropher 拡張）のログイン処理。
//
//	GET /auth/oidc/{id}/start     IdP の認可エンドポイントへリダイレクト（state / nonce / PKCE をセッションに保存）
//	GET /auth/oidc/{id}/callback  認可コードを交換し、ID トークンを検証してユーザーを解決・ログインする
//
// ユーザーの解決順:
//  1. user_identities（provider = "oidc:<id>", subject）に連携があればそのユーザー
//  2. 連携の開始（マイアカウントの「連携する」）ならログイン中のユーザーに連携を作る
//  3. match_by（login / mail）で既存ユーザーと突合できれば連携を作る（初回のみ。以後は 1 で固定）
//  4. onthefly_register が有効ならユーザーを作成する（auth_source_id = この認証方式。パスワードログイン不可）
//  5. いずれでもなければ拒否
//
// ログイン後、group_sync が有効なら groups クレームと auth_source_group_mappings に従って
// buropher のグループの所属を追加・削除する（対応表に現れるグループだけを対象にする）。

// OIDCController は OIDC ログイン（buropher 拡張）。
var OIDCController = &Controller{Name: "oidc", MainMenu: false}

const (
	sessionOIDCRequest = "oidc_request"
	// sessionSSOSource はログインに使った OIDC 認証方式の id（RP-Initiated Logout・sudo の再認証に使う）。
	sessionSSOSource = "sso_auth_source_id"
	// sessionSSOIDToken は id_token_hint 用の ID トークン。
	sessionSSOIDToken = "sso_id_token"
	// oidcRequestTTL は認可リクエストの有効期間。
	oidcRequestTTL = 10 * time.Minute
)

// routesOIDC は OIDC ログインのルート（routesAccount から呼ぶ）。
func (a *App) routesOIDC(r Router) {
	skip := Skip(FilterLoginRequired, FilterPasswordChange, FilterTwofaActivation)
	a.Handle(r, http.MethodGet, "/auth/oidc/{id}/start", OIDCController, "start", a.OIDCStart, skip)
	a.Handle(r, http.MethodGet, "/auth/oidc/{id}/callback", OIDCController, "callback", a.OIDCCallback, skip)
}

// oidcSettings は OIDC 認証方式の設定（config JSON の解釈）。
type oidcSettings struct {
	rec *domain.AuthSourceRecord
}

func (s oidcSettings) str(k string) string {
	v, _ := s.rec.ConfigString(k)
	return strings.TrimSpace(v)
}

func (s oidcSettings) Preset() string {
	if p := s.str("preset"); p != "" {
		return p
	}
	return oidc.PresetGeneric
}
func (s oidcSettings) MatchBy() string {
	switch v := s.str("match_by"); v {
	case "login", "mail", "none":
		return v
	}
	return "mail"
}
func (s oidcSettings) GroupSync() bool     { return s.rec.ConfigBool("group_sync") }
func (s oidcSettings) SSORequired() bool   { return s.rec.ConfigBool("sso_required") }
func (s oidcSettings) SkipTwofa() bool     { return s.rec.ConfigBool("skip_twofa") }
func (s oidcSettings) RPLogout() bool      { return s.rec.ConfigBool("rp_logout") }
func (s oidcSettings) ButtonLabel() string { return s.str("button_label") }

// splitList は改行・カンマ区切りの一覧。
func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' || r == '\r' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// oidcConfig は認証方式のレコードから oidc.Config を作る（client_secret は復号する）。
func (a *App) oidcConfig(c *Req, rec *domain.AuthSourceRecord) oidc.Config {
	s := oidcSettings{rec}
	cfg := oidc.Config{
		Preset: s.Preset(), Issuer: s.str("issuer"), Tenant: s.str("tenant"), ClientID: s.str("client_id"),
		Scopes:       strings.Fields(s.str("scopes")),
		RedirectURL:  a.externalURL(c, "/auth/oidc/"+strconv.FormatInt(rec.ID, 10)+"/callback"),
		ClaimSubject: s.str("claim_subject"), ClaimLogin: s.str("claim_login"), ClaimFirstname: s.str("claim_firstname"),
		ClaimLastname: s.str("claim_lastname"), ClaimMail: s.str("claim_mail"), ClaimGroups: s.str("claim_groups"),
		ClaimTenant:    s.str("claim_tenant"),
		AllowedTenants: splitList(s.str("allowed_tenants")), AllowedGroups: splitList(s.str("allowed_groups")),
	}
	if rec.Secret != nil && *rec.Secret != "" {
		if sec, err := a.openKey(*rec.Secret); err == nil {
			cfg.ClientSecret = sec
		} else {
			a.logger().Error("oidc client secret cannot be decrypted", "auth_source", rec.ID, "err", err)
		}
	}
	return cfg
}

// externalURL は外部から見た絶対 URL（server.base_url があればそれ、なければリクエストのスキームとホスト）。
// path には relative_url_root を前置する（server.base_url が既にルートで終わっていれば前置しない）。
func (a *App) externalURL(c *Req, path string) string {
	if a.BaseURL != "" {
		if root := urlroot.Get(); root != "" && strings.HasSuffix(a.BaseURL, root) {
			return a.BaseURL + path
		}
		return a.BaseURL + urlroot.Path(path)
	}
	return httpx.RequestBaseURL(c.R) + urlroot.Path(path)
}

// oidcProvider は認証方式のプロバイダ（ディスカバリ結果はキャッシュする）。
func (a *App) oidcProvider(c *Req, rec *domain.AuthSourceRecord) (*oidc.Provider, error) {
	return oidc.Cached(c.Ctx(), rec.ID, a.OIDCHTTPClient, a.oidcConfig(c, rec))
}

// oidcSources はログイン画面に出す OIDC 認証方式（有効なもの）。
func (a *App) oidcSources(c *Req) []*domain.AuthSourceRecord {
	recs, err := repository.OIDCAuthSources(c.Ctx(), a.DB)
	if err != nil {
		a.logger().Error("oidc auth sources", "err", err)
		return nil
	}
	return recs
}

// ssoRequired は SSO 必須モード（有効な OIDC 認証方式のいずれかで sso_required が設定されている）。
func (a *App) ssoRequired(c *Req) bool {
	for _, rec := range a.oidcSources(c) {
		if (oidcSettings{rec}).SSORequired() {
			return true
		}
	}
	return false
}

// localLoginAllowed はパスワードによるログインを許可するか（SSO 必須モードでは管理者のみ）。
func (a *App) localLoginAllowed(c *Req, u *domain.User) bool {
	return u.IsAdmin() || !a.ssoRequired(c)
}

// findOIDCSource は {id} の有効な OIDC 認証方式（無ければ 404 を描画して nil）。
func (a *App) findOIDCSource(c *Req) *domain.AuthSourceRecord {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return nil
	}
	rec, err := repository.GetAuthSource(c.Ctx(), a.DB, id)
	if err != nil || rec.Kind != domain.AuthSourceKindOIDC || !rec.Enabled {
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.serverError(c, err)
			return nil
		}
		c.Render404("")
		return nil
	}
	return rec
}

// oidcFail はログイン失敗の flash を出してログイン画面へ戻す。
func (a *App) oidcFail(c *Req, reason string, err error) {
	if err != nil {
		a.logger().Warn("OIDC login failed", "reason", reason, "err", err, "ip", httpx.RemoteIP(c.R))
	}
	c.Flash().SetError(c.L("buropher.sso.error_" + reason))
	c.Redirect("/login")
}

// OIDCStart は GET /auth/oidc/{id}/start。
// mode=link はログイン中のユーザーへの連携、mode=sudo は sudo モードの再認証（prompt=login）。
func (a *App) OIDCStart(c *Req) {
	rec := a.findOIDCSource(c)
	if rec == nil {
		return
	}
	p, err := a.oidcProvider(c, rec)
	if err != nil {
		a.oidcFail(c, oidc.Reason(err), err)
		return
	}
	mode := c.Params().String("mode")
	if (mode == "link" || mode == "sudo") && !c.User.Logged() {
		mode = ""
	}
	req := oidc.NewAuthRequest()
	if mode == "sudo" {
		req.Prompt = "login"
	}
	back := c.Params().String("back_url")
	if _, ok := httpx.ValidateBackURL(c.R, back, ""); !ok {
		back = ""
	}
	c.Session().Set(sessionOIDCRequest, map[string]any{
		"auth_source_id": rec.ID, "state": req.State, "nonce": req.Nonce, "verifier": req.Verifier,
		"mode": mode, "back_url": back, "autologin": c.Params().String("autologin"), "created_at": a.now().Unix(),
	})
	c.Redirect(p.AuthCodeURL(req))
}

// OIDCCallback は GET /auth/oidc/{id}/callback。
func (a *App) OIDCCallback(c *Req) {
	rec := a.findOIDCSource(c)
	if rec == nil {
		return
	}
	s := c.Session()
	saved, _ := s.Get(sessionOIDCRequest).(map[string]any)
	s.Delete(sessionOIDCRequest)
	p := c.Params()
	if saved == nil {
		a.oidcFail(c, "state", errors.New("no pending authorization request in the session"))
		return
	}
	str := func(k string) string { return httpx.ValueString(saved[k]) }
	created := httpx.ValueInt(saved["created_at"])
	if httpx.ValueInt(saved["auth_source_id"]) != rec.ID || p.String("state") == "" ||
		subtle.ConstantTimeCompare([]byte(p.String("state")), []byte(str("state"))) != 1 {
		a.oidcFail(c, "state", errors.New("state mismatch"))
		return
	}
	if a.now().Unix()-created > int64(oidcRequestTTL/time.Second) {
		a.oidcFail(c, "state", errors.New("authorization request expired"))
		return
	}
	if e := p.String("error"); e != "" {
		a.oidcFail(c, "idp", errors.New(e+": "+p.String("error_description")))
		return
	}
	prov, err := a.oidcProvider(c, rec)
	if err != nil {
		a.oidcFail(c, oidc.Reason(err), err)
		return
	}
	id, err := prov.Exchange(c.Ctx(), p.String("code"), oidc.AuthRequest{State: str("state"), Nonce: str("nonce"), Verifier: str("verifier")})
	if err != nil {
		a.oidcFail(c, oidc.Reason(err), err)
		return
	}
	if err := prov.Authorize(id); err != nil {
		a.oidcFail(c, oidc.Reason(err), err)
		return
	}
	if id.GroupsOverage {
		a.logger().Warn("OIDC groups claim overage: groups are not synchronized (Microsoft Graph lookup is not supported)", "auth_source", rec.ID, "subject", id.Subject)
	}
	// back_url と autologin をパラメータに戻す（successful_authentication が使う）
	if b := str("back_url"); b != "" {
		httpx.BodyParams(c.R).Set("back_url", b)
	}
	if v := str("autologin"); v != "" {
		httpx.BodyParams(c.R).Set("autologin", v)
	}
	mode := str("mode")
	if (mode == "link" || mode == "sudo") && !c.User.Logged() {
		// 連携・再認証はログイン中のユーザーに対してのみ（匿名ユーザーに外部 ID を紐付けない）
		a.oidcFail(c, "state", errors.New("not logged in for "+mode))
		return
	}
	switch mode {
	case "link":
		a.oidcLink(c, rec, prov, id)
		return
	case "sudo":
		a.oidcSudo(c, rec, prov, id)
		return
	}
	user, err := a.oidcResolveUser(c, rec, prov, id)
	if err != nil {
		var re *oidc.Error
		if errors.As(err, &re) {
			a.oidcFail(c, re.Reason, err)
		} else {
			a.serverError(c, err)
		}
		return
	}
	if !user.Active() {
		a.handleInactiveUser(c, user, "/login")
		return
	}
	if err := repository.UpdateLastLogin(c.Ctx(), a.DB, user.ID, a.now()); err != nil {
		a.logger().Error("update last login", "err", err)
	}
	if err := a.oidcSyncGroups(c, rec, user, id); err != nil {
		a.serverError(c, err)
		return
	}
	set := oidcSettings{rec}
	if user.TwofaActive() && !set.SkipTwofa() && a.Settings.String("twofa") != "0" {
		// buropher の 2 要素認証を続けて求める（IdP の多要素認証を信頼しない設定）
		a.startTwofaLogin(c, user)
		return
	}
	a.logger().Info("Successful SSO authentication", "login", user.Login, "auth_source", rec.ID, "ip", httpx.RemoteIP(c.R))
	a.handleActiveUser(c, user, func() {
		s := c.Session()
		s.Set(sessionSSOSource, rec.ID)
		if set.RPLogout() && len(id.IDToken) < 8192 {
			s.Set(sessionSSOIDToken, id.IDToken)
		}
		if set.SkipTwofa() {
			// IdP の多要素認証を信頼する設定では 2 要素認証の有効化も求めない
			s.Delete("must_activate_twofa")
		}
	})
}

// oidcProviderKey は user_identities.provider。
func oidcProviderKey(rec *domain.AuthSourceRecord) string {
	return "oidc:" + strconv.FormatInt(rec.ID, 10)
}

// oidcSubject は user_identities.subject（マルチテナント構成では issuer を前置して名前空間を分ける）。
func oidcSubject(prov *oidc.Provider, id *oidc.Identity) string {
	if prov.Config().MultiTenant() {
		return id.Issuer + "|" + id.Subject
	}
	return id.Subject
}

// oidcResolveUser はクレームからユーザーを解決する（必要なら連携・ユーザーを作成する）。
func (a *App) oidcResolveUser(c *Req, rec *domain.AuthSourceRecord, prov *oidc.Provider, id *oidc.Identity) (*domain.User, error) {
	ctx := c.Ctx()
	providerKey, subject := oidcProviderKey(rec), oidcSubject(prov, id)
	ident, err := repository.FindUserIdentity(ctx, a.DB, providerKey, subject)
	switch {
	case err == nil:
		if err := repository.TouchUserIdentity(ctx, a.DB, ident.ID, id.Mail, id.Claims, a.now()); err != nil {
			return nil, err
		}
		u, err := repository.GetUser(ctx, a.DB, ident.UserID)
		if err != nil {
			return nil, err
		}
		return u, nil
	case !errors.Is(err, repository.ErrNotFound):
		return nil, err
	}
	// 初回: 既存ユーザーとの突合
	var user *domain.User
	set := oidcSettings{rec}
	switch set.MatchBy() {
	case "login":
		if id.Login != "" {
			u, err := repository.FindUserByLogin(ctx, a.DB, id.Login)
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				return nil, err
			}
			user = u
		}
	case "mail":
		// 未検証のメールアドレス（email_verified=false）では既存ユーザーに紐付けない
		if id.Mail != "" && !id.MailUnverified {
			u, err := repository.FindUserByMail(ctx, a.DB, id.Mail)
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				return nil, err
			}
			user = u
		}
	}
	if user != nil {
		// 既に同じ認証方式の別の ID と連携しているユーザーには紐付けない（なりすまし防止）
		if _, err := repository.UserIdentityForSource(ctx, a.DB, user.ID, providerKey); err == nil {
			return nil, &oidc.Error{Reason: "already_linked", Err: errors.New("user is linked to another identity of this provider")}
		}
		// IdP の多要素認証を信頼する設定（skip_twofa）では、クレーム（メール / ログイン ID）の一致だけで
		// buropher の 2 要素認証を有効にしているユーザーに紐付けると、以後その 2 要素認証を迂回できてしまう。
		// その場合は自動では紐付けず、パスワード + 2 要素認証でログインしてからマイアカウントで連携させる
		if user.TwofaActive() && set.SkipTwofa() && a.Settings.String("twofa") != "0" {
			return nil, &oidc.Error{Reason: "link_required", Err: errors.New("automatic linking to a user with two-factor authentication is not allowed")}
		}
	} else {
		if !rec.OntheflyRegister {
			return nil, &oidc.Error{Reason: "no_account", Err: errors.New("no matching user and on-the-fly registration is disabled")}
		}
		u, err := a.oidcCreateUser(c, rec, id)
		if err != nil {
			return nil, err
		}
		user = u
	}
	now := a.now()
	sid := rec.ID
	if err := repository.CreateUserIdentity(ctx, a.DB, &domain.UserIdentity{UserID: user.ID, Provider: providerKey, AuthSourceID: &sid,
		Subject: subject, Email: id.Mail, RawClaims: id.Claims, CreatedAt: now, LastLoginAt: &now}); err != nil {
		return nil, err
	}
	a.logger().Info("OIDC identity linked", "login", user.Login, "auth_source", rec.ID)
	return user, nil
}

// oidcCreateUser はオンザフライでユーザーを作成する。
func (a *App) oidcCreateUser(c *Req, rec *domain.AuthSourceRecord, id *oidc.Identity) (*domain.User, error) {
	m := a.newUserModel(c)
	login := id.Login
	if login == "" {
		login = id.Mail
	}
	m.Login = login
	m.Firstname, m.Lastname = id.Firstname, id.Lastname
	if m.Firstname == "" && m.Lastname == "" {
		m.Firstname, m.Lastname = login, "-"
	} else if m.Lastname == "" {
		m.Lastname = "-"
	} else if m.Firstname == "" {
		m.Firstname = "-"
	}
	m.mail, m.mailSet = domain.NormalizeEmail(id.Mail), true
	sid := rec.ID
	m.AuthSourceID = &sid
	m.Language = a.Settings.String("default_language")
	ok, err := a.saveUser(c, m)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &oidc.Error{Reason: "user_creation_failed", Err: errors.New(strings.Join(m.errors.FullMessages(c.Loc), ", "))}
	}
	a.logger().Info("User created from OIDC auth source", "login", m.Login, "auth_source", rec.ID)
	return repository.GetUser(c.Ctx(), a.DB, m.ID)
}

// oidcSyncGroups は groups クレームに従ってグループの所属を同期する（対応表にあるグループのみ追加・削除）。
func (a *App) oidcSyncGroups(c *Req, rec *domain.AuthSourceRecord, user *domain.User, id *oidc.Identity) error {
	if !(oidcSettings{rec}).GroupSync() || id.GroupsOverage {
		return nil
	}
	ctx := c.Ctx()
	ms, err := repository.AuthSourceGroupMappings(ctx, a.DB, rec.ID)
	if err != nil || len(ms) == 0 {
		return err
	}
	have := map[string]bool{}
	for _, g := range id.Groups {
		have[strings.ToLower(g)] = true
	}
	want := map[int64]bool{}
	managed := map[int64]bool{}
	for _, m := range ms {
		managed[m.GroupID] = true
		if have[strings.ToLower(m.ExternalGroup)] {
			want[m.GroupID] = true
		}
	}
	current, err := repository.UserGroupIDs(ctx, a.DB, user.ID)
	if err != nil {
		return err
	}
	cur := map[int64]bool{}
	for _, g := range current {
		cur[g] = true
	}
	return a.DB.WithTx(ctx, func(tx *db.Tx) error {
		for g := range managed {
			switch {
			case want[g] && !cur[g]:
				if err := repository.AddUserToGroup(ctx, tx, g, user.ID); err != nil {
					return err
				}
			case !want[g] && cur[g]:
				if err := repository.RemoveUserFromGroup(ctx, tx, g, user.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// oidcLink はログイン中のユーザーに外部 ID を連携する（マイアカウントの「連携する」）。
func (a *App) oidcLink(c *Req, rec *domain.AuthSourceRecord, prov *oidc.Provider, id *oidc.Identity) {
	ctx := c.Ctx()
	providerKey, subject := oidcProviderKey(rec), oidcSubject(prov, id)
	if ident, err := repository.FindUserIdentity(ctx, a.DB, providerKey, subject); err == nil {
		if ident.UserID != c.User.ID {
			c.Flash().SetError(c.L("buropher.sso.error_identity_taken"))
		} else {
			c.Flash().SetNotice(c.L("buropher.sso.notice_identity_linked"))
		}
		c.Redirect("/my/sso")
		return
	}
	if _, err := repository.UserIdentityForSource(ctx, a.DB, c.User.ID, providerKey); err == nil {
		c.Flash().SetError(c.L("buropher.sso.error_already_linked"))
		c.Redirect("/my/sso")
		return
	}
	now := a.now()
	sid := rec.ID
	if err := repository.CreateUserIdentity(ctx, a.DB, &domain.UserIdentity{UserID: c.User.ID, Provider: providerKey, AuthSourceID: &sid,
		Subject: subject, Email: id.Mail, RawClaims: id.Claims, CreatedAt: now, LastLoginAt: &now}); err != nil {
		a.serverError(c, err)
		return
	}
	c.Flash().SetNotice(c.L("buropher.sso.notice_identity_linked"))
	c.Redirect("/my/sso")
}

// oidcSudo は sudo モードの再認証（ログイン中のユーザーと同じ外部 ID なら sudo を有効にする）。
// oidcSudoMaxAge は sudo モードの再認証として受け付ける auth_time の古さの上限。
const oidcSudoMaxAge = 10 * time.Minute

func (a *App) oidcSudo(c *Req, rec *domain.AuthSourceRecord, prov *oidc.Provider, id *oidc.Identity) {
	ident, err := repository.FindUserIdentity(c.Ctx(), a.DB, oidcProviderKey(rec), oidcSubject(prov, id))
	// prompt=login / max_age=0 を IdP が無視して既存の IdP セッションで応答した場合は再認証とみなさない
	// （max_age を要求したときは auth_time が必須。直近の認証であること）
	reauthenticated := !id.AuthTime.IsZero() && a.now().Sub(id.AuthTime) < oidcSudoMaxAge && id.AuthTime.Sub(a.now()) < time.Minute
	if err != nil || ident.UserID != c.User.ID || !reauthenticated {
		c.Flash().SetError(c.L("notice_account_wrong_password"))
	} else {
		a.updateSudoTimestamp(c)
	}
	c.RedirectBackOrDefault("/my/account", false)
}

// ssoLogoutURL はログアウト後のリダイレクト先（RP-Initiated Logout が有効なら IdP の end_session_endpoint）。
// ログアウト前（セッションの破棄前）に呼ぶ。
func (a *App) ssoLogoutURL(c *Req) string {
	s := c.Session()
	if s == nil || !s.Has(sessionSSOSource) {
		return ""
	}
	rec, err := repository.GetAuthSource(c.Ctx(), a.DB, s.GetInt(sessionSSOSource))
	if err != nil || rec.Kind != domain.AuthSourceKindOIDC || !(oidcSettings{rec}).RPLogout() {
		return ""
	}
	p, err := a.oidcProvider(c, rec)
	if err != nil {
		a.logger().Warn("oidc logout discovery", "err", err)
		return ""
	}
	return p.EndSessionURL(s.GetString(sessionSSOIDToken), a.externalURL(c, "/"))
}

// ssoButton はログイン画面の「<name> でログイン」ボタン。
type ssoButton struct {
	Label string
	URL   string
}

// ssoLoginData はログイン画面に渡す SSO のデータ（OIDC 認証方式が無ければパスワードのフォームだけ）。
func (a *App) ssoLoginData(c *Req) map[string]any {
	recs := a.oidcSources(c)
	if len(recs) == 0 {
		return map[string]any{"ShowLocalForm": true}
	}
	back := c.Params().String("back_url")
	var buttons []ssoButton
	required := false
	for _, rec := range recs {
		set := oidcSettings{rec}
		label := set.ButtonLabel()
		if label == "" {
			label = c.L("buropher.sso.button_login_with", map[string]any{"provider": rec.Name})
		}
		u := "/auth/oidc/" + strconv.FormatInt(rec.ID, 10) + "/start"
		if back != "" {
			u += "?back_url=" + url.QueryEscape(back)
		}
		buttons = append(buttons, ssoButton{Label: label, URL: u})
		if set.SSORequired() {
			required = true
		}
	}
	return map[string]any{
		"SSOButtons":  buttons,
		"SSORequired": required,
		// SSO 必須モードでもローカルログイン（管理者の救済用）は /login?local=1 で表示する
		"ShowLocalForm": !required || c.Params().Present("local") || c.R.Method == http.MethodPost,
	}
}
