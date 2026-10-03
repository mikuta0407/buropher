// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// buropher 独自: Discord DM 通知の設定画面（管理 > プラグイン > Discord 通知。Redmine のプラグイン設定画面
// settings/plugin/:id と同じ形）と、マイアカウントでの Discord アカウント連携（OAuth2）・連携解除・テスト DM・
// 通知の送信先（メール / Discord / 両方）の選択。設定方法は docs/discord.md。

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/discord"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// DiscordPluginID は管理画面のプラグイン一覧・設定画面での id（/settings/plugin/buropher_discord）。
const DiscordPluginID = "buropher_discord"

// DiscordController は Discord 連携（my/discord/*）のコントローラ。
var DiscordController = &Controller{Name: "my", MainMenu: false}

const discordStateKey = "buropher_discord_state"

// routesDiscord は Discord 連携のルート。
func (a *App) routesDiscord(r Router) {
	login := RequireLogin()
	a.Handle(r, http.MethodGet, "/my/discord/link", DiscordController, "discord_link", a.DiscordLink, login)
	a.Handle(r, http.MethodGet, "/my/discord/callback", DiscordController, "discord_callback", a.DiscordCallback, login)
	a.Handle(r, http.MethodPost, "/my/discord/unlink", DiscordController, "discord_unlink", a.DiscordUnlink, login)
	a.Handle(r, http.MethodPost, "/my/discord/test", DiscordController, "discord_test", a.DiscordTest, login)
}

// RegisterDiscordHooks はマイアカウントの設定欄（view_my_account_preferences）に通知の送信先と連携状態を出す。
func (a *App) RegisterDiscordHooks() {
	a.Helpers.AddHook("view_my_account_preferences", a.discordPreferencesHook)
}

func (a *App) discordConfig() notify.DiscordConfig {
	return notify.DiscordSettings(a.Settings, a.Secrets)
}

// discordRedirectURI は OAuth2 のリダイレクト URL（Setting.protocol / host_name から作る）。
func (a *App) discordRedirectURI() string {
	return a.mailURLOptions().BaseURL() + "/my/discord/callback"
}

// discordFeatureVisible は管理画面に Discord 通知の設定を出すか（config の discord.enabled か、既に有効化済み）。
func (a *App) discordFeatureVisible() bool {
	return a.DiscordFeature || a.Settings.Bool("buropher_discord_enabled")
}

// discordPlugin は管理画面のプラグイン一覧の 1 行。
type discordPlugin struct {
	ID, Name, Description, Author, Version string
	Configurable                           bool
}

// adminPlugins は admin#plugins の @plugins（buropher 組み込みの Discord 通知のみ）。
func (a *App) adminPlugins(c *Req) []any {
	if !a.discordFeatureVisible() {
		return []any{}
	}
	return []any{&discordPlugin{ID: DiscordPluginID, Name: c.L("buropher.discord.plugin_name"),
		Description: c.L("buropher.discord.plugin_description"), Author: "buropher", Version: a.version(), Configurable: true}}
}

// discordChannelOptions は通知の送信先の選択肢。
func discordChannelOptions(c *Req) [][2]string {
	return [][2]string{
		{c.L("buropher.discord.channel_email"), notify.ChannelEmail},
		{c.L("buropher.discord.channel_discord"), notify.ChannelDiscord},
		{c.L("buropher.discord.channel_both"), "both"},
	}
}

// DiscordSettingsPage は settings#plugin（id = buropher_discord）。
func (a *App) DiscordSettingsPage(c *Req) {
	if !a.discordFeatureVisible() {
		c.Render404("")
		return
	}
	if c.R.Method == http.MethodPost {
		p := c.Params().Map("settings")
		if p == nil {
			p = c.Params()
		}
		ctx := c.Ctx()
		set := func(name string, v any) bool {
			if err := a.Settings.Set(ctx, name, v); err != nil {
				// フラッシュは raw HTML として描画されるため動的値はエスケープする
				c.Flash().Now("error", template.HTMLEscapeString(err.Error()))
				return false
			}
			return true
		}
		ok := set("buropher_discord_enabled", boolSetting(p.String("enabled")))
		ok = ok && set("buropher_discord_client_id", strings.TrimSpace(p.String("client_id")))
		ok = ok && set("buropher_discord_guild_id", strings.TrimSpace(p.String("guild_id")))
		ok = ok && set("buropher_discord_join_guild", boolSetting(p.String("join_guild")))
		if ch := p.String("default_channel"); ch == notify.ChannelEmail || ch == notify.ChannelDiscord || ch == "both" {
			ok = ok && set("buropher_default_notification_channel", ch)
		}
		if n := strings.TrimSpace(p.String("failure_threshold")); n != "" {
			ok = ok && set("buropher_discord_failure_threshold", n)
		}
		for _, k := range []string{"bot_token", "client_secret"} {
			v := strings.TrimSpace(p.String(k))
			if v == "" {
				continue // 空欄なら変更しない
			}
			if a.Secrets != nil {
				sealed, err := a.Secrets.Seal(v)
				if err != nil {
					a.serverError(c, err)
					return
				}
				v = sealed
			}
			ok = ok && set("buropher_discord_"+k, v)
		}
		if ok {
			c.Flash().SetNotice(c.L("notice_successful_update"))
			c.Redirect("/settings/plugin/" + DiscordPluginID)
			return
		}
	}
	cfg := a.discordConfig()
	deliveries, err := repository.ListNotificationDeliveries(c.Ctx(), a.DB, 0, 25)
	if err != nil {
		a.serverError(c, err)
		return
	}
	var rows []map[string]any
	for _, d := range deliveries {
		user := ""
		if d.UserID != nil {
			if u, err := repository.GetUser(c.Ctx(), a.DB, *d.UserID); err == nil {
				user = u.Login
			}
		}
		rows = append(rows, map[string]any{"Delivery": d, "Created": d.Created.Time, "User": user,
			"Recipient": strDeref(d.Recipient), "Error": strDeref(d.Error)})
	}
	c.renderAdmin("settings/buropher_discord", map[string]any{
		"PluginName":       c.L("buropher.discord.plugin_name"),
		"Config":           cfg,
		"BotTokenSet":      cfg.BotToken != "",
		"ClientSecretSet":  cfg.ClientSecret != "",
		"DefaultChannel":   a.Settings.String("buropher_default_notification_channel"),
		"FailureThreshold": a.Settings.String("buropher_discord_failure_threshold"),
		"ChannelOptions":   discordChannelOptions(c),
		"RedirectURI":      a.discordRedirectURI(),
		"Deliveries":       rows,
	}, false)
}

func boolSetting(v string) string {
	if v == "1" || v == "true" {
		return "1"
	}
	return "0"
}

// discordPreferencesHook はマイアカウントの設定欄に出す Discord の項目（Discord 通知が有効な場合のみ）。
func (a *App) discordPreferencesHook(r *view.Render, p *helper.Page, _ ...any) template.HTML {
	cfg := a.discordConfig()
	if !cfg.Usable() || p.User == nil || !p.User.Logged() {
		return ""
	}
	c := ReqOf(p.Request)
	if c == nil {
		return ""
	}
	ctx := c.Ctx()
	ns, err := repository.GetNotificationSetting(ctx, a.DB, p.User.ID, a.Settings.String("default_notification_option"), a.Settings.Bool("default_users_no_self_notified"))
	if err != nil {
		a.logger().Error("discord preferences", "err", err)
		return ""
	}
	channel := ns.Channels
	if !ns.Explicit {
		channel = a.Settings.String("buropher_default_notification_channel")
	}
	data := map[string]any{"Channel": channel, "ChannelOptions": discordChannelOptions(c), "LinkUsable": cfg.LinkUsable()}
	if ident, err := repository.GetDiscordIdentity(ctx, a.DB, p.User.ID); err == nil && ident != nil {
		name := ident.Subject
		if ident.RawClaims != nil {
			var u discord.User
			if json.Unmarshal([]byte(*ident.RawClaims), &u) == nil && u.DisplayName() != "" {
				name = u.DisplayName()
			}
		}
		data["LinkedAs"] = name
		if ch, err := repository.GetDiscordDMChannel(ctx, a.DB, p.User.ID); err == nil && ch != nil && ch.ConsecutiveFailures > 0 {
			data["Failing"] = c.L("buropher.discord.label_dm_failing", map[string]any{"count": ch.ConsecutiveFailures, "error": strDeref(ch.LastError)})
		}
	}
	h, err := r.Partial("my/discord_preferences", data)
	if err != nil {
		a.logger().Error("discord preferences", "err", err)
		return ""
	}
	return h
}

// saveNotificationChannel はマイアカウントの保存時に pref[notification_channel] を保存する。
func (a *App) saveNotificationChannel(c *Req, userID int64) {
	pref := c.Params().Map("pref")
	if pref == nil || !pref.Has("notification_channel") || !a.discordConfig().Usable() {
		return
	}
	ch := pref.String("notification_channel")
	if ch != notify.ChannelEmail && ch != notify.ChannelDiscord && ch != "both" {
		return
	}
	if err := repository.SetNotificationChannels(c.Ctx(), a.DB, userID, ch, a.Settings.Bool("default_users_no_self_notified")); err != nil {
		a.logger().Error("save notification channel", "err", err)
	}
}

// DiscordLink は Discord の OAuth2 認可画面へリダイレクトする（scope: identify と、Guild に参加させる場合は guilds.join）。
func (a *App) DiscordLink(c *Req) {
	cfg := a.discordConfig()
	if !cfg.LinkUsable() {
		c.Flash().SetError(c.L("buropher.discord.error_disabled"))
		c.Redirect("/my/account")
		return
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	state := hex.EncodeToString(b[:])
	if s := c.Session(); s != nil {
		s.Set(discordStateKey, state)
	}
	scopes := []string{"identify"}
	if cfg.GuildID != "" && cfg.JoinGuild {
		scopes = append(scopes, "guilds.join")
	}
	u := discord.AuthorizeURL(a.DiscordAuthorizeURL, cfg.ClientID, a.discordRedirectURI(), state, scopes)
	http.Redirect(c.W, c.R, u, http.StatusFound)
	c.Halt()
}

// DiscordCallback は OAuth2 のコールバック（コードを交換して Discord ユーザーを連携し、必要なら Guild に参加させる）。
func (a *App) DiscordCallback(c *Req) {
	cfg := a.discordConfig()
	if !cfg.LinkUsable() {
		c.Flash().SetError(c.L("buropher.discord.error_disabled"))
		c.Redirect("/my/account")
		return
	}
	s := c.Session()
	state := c.Params().String("state")
	if s == nil || state == "" || subtle.ConstantTimeCompare([]byte(s.GetString(discordStateKey)), []byte(state)) != 1 {
		c.Flash().SetError(c.L("buropher.discord.error_invalid_state"))
		c.Redirect("/my/account")
		return
	}
	s.Delete(discordStateKey)
	fail := func(err error) {
		a.logger().Warn("discord link failed", "user", c.User.Login, "err", err)
		// err は OAuth の error パラメータ（攻撃者が制御可）を含みうる。フラッシュは
		// raw HTML として描画されるためエスケープする。
		c.Flash().SetError(c.L("buropher.discord.error_link_failed", template.HTMLEscapeString(err.Error())))
		c.Redirect("/my/account")
	}
	if e := c.Params().String("error"); e != "" {
		fail(errors.New(e))
		return
	}
	ctx := c.Ctx()
	client := a.Notify.Discord
	if client == nil {
		fail(errors.New("discord client is not configured"))
		return
	}
	tok, err := client.ExchangeCode(ctx, cfg.ClientID, cfg.ClientSecret, c.Params().String("code"), a.discordRedirectURI())
	if err != nil {
		fail(err)
		return
	}
	du, err := client.CurrentUser(ctx, tok.AccessToken)
	if err != nil {
		fail(err)
		return
	}
	if owner, err := repository.DiscordIdentityOwner(ctx, a.DB, du.ID); err != nil {
		fail(err)
		return
	} else if owner != 0 && owner != c.User.ID {
		c.Flash().SetError(c.L("buropher.discord.error_already_linked"))
		c.Redirect("/my/account")
		return
	}
	raw, _ := json.Marshal(du)
	if err := repository.LinkDiscordIdentity(ctx, a.DB, c.User.ID, du.ID, string(raw), a.now()); err != nil {
		a.serverError(c, err)
		return
	}
	if cfg.GuildID != "" && cfg.JoinGuild && strings.Contains(tok.Scope, "guilds.join") {
		if err := client.AddGuildMember(ctx, cfg.BotToken, cfg.GuildID, du.ID, tok.AccessToken); err != nil {
			a.logger().Warn("discord: add guild member failed", "user", c.User.Login, "err", err)
		}
	}
	c.Flash().SetNotice(c.L("buropher.discord.notice_linked"))
	// 到達確認のテスト DM（失敗したら理由を表示する）
	if err := a.Notify.SendDM(ctx, cfg, c.User.ID, du.ID, a.DiscordTestMessage(ctx, c.User)); err != nil {
		c.Flash().SetWarning(a.discordErrorMessage(c, err))
	}
	c.Redirect("/my/account")
}

// discordErrorMessage は DM の送信エラーの表示文言。
func (a *App) discordErrorMessage(c *Req, err error) string {
	var ae *discord.APIError
	if errors.As(err, &ae) && ae.Code == discord.CodeCannotSendToUser {
		return c.L("buropher.discord.error_test_failed", c.L("buropher.discord.error_cannot_dm"))
	}
	return c.L("buropher.discord.error_test_failed", template.HTMLEscapeString(err.Error()))
}

// DiscordUnlink は連携を解除する。
func (a *App) DiscordUnlink(c *Req) {
	if err := repository.UnlinkDiscordIdentity(c.Ctx(), a.DB, c.User.ID); err != nil {
		a.serverError(c, err)
		return
	}
	c.Flash().SetNotice(c.L("buropher.discord.notice_unlinked"))
	c.Redirect("/my/account")
}

// DiscordTest はテスト DM を送る（成功したら恒久エラーの連続回数を 0 に戻し、Discord 通知を再開する）。
func (a *App) DiscordTest(c *Req) {
	cfg := a.discordConfig()
	ctx := c.Ctx()
	ident, err := repository.GetDiscordIdentity(ctx, a.DB, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if !cfg.Usable() || ident == nil {
		c.Flash().SetError(c.L("buropher.discord.error_disabled"))
		c.Redirect("/my/account")
		return
	}
	if err := a.Notify.SendDM(ctx, cfg, c.User.ID, ident.Subject, a.DiscordTestMessage(ctx, c.User)); err != nil {
		c.Flash().SetError(a.discordErrorMessage(c, err))
	} else {
		_ = repository.ResetDiscordDMFailures(ctx, a.DB, c.User.ID)
		c.Flash().SetNotice(c.L("buropher.discord.notice_test_sent"))
	}
	c.Redirect("/my/account")
}

var _ = strconv.Itoa
