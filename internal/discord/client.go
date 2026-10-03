// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package discord は Discord REST API（v10）の最小限のクライアント（Bot による DM 送信、OAuth2 での
// アカウント連携、Guild への参加）。依存を増やさないよう net/http だけで実装する。
//
// レート制限: 429 応答の retry_after（秒）、Retry-After / X-RateLimit-Reset-After ヘッダを RateLimitError
// として返す。成功応答で X-RateLimit-Remaining が 0 なら、そのバケットの次の要求を reset まで送らずに
// RateLimitError を返す（呼び出し側のジョブは jobs.Retry で延期する）。
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 既定の接続先。
const (
	DefaultAPIBase      = "https://discord.com/api/v10"
	DefaultAuthorizeURL = "https://discord.com/oauth2/authorize"
)

// JSON エラーコード（https://discord.com/developers/docs/topics/opcodes-and-status-codes#json）。
const (
	CodeUnknownChannel    = 10003
	CodeUnknownGuild      = 10004
	CodeUnknownMember     = 10007
	CodeUnknownUser       = 10013
	CodeMissingAccess     = 50001
	CodeCannotSendToUser  = 50007
	CodeMaxGuilds         = 30001
	CodeInvalidOAuthToken = 50025
)

// Client は Discord API のクライアント。
type Client struct {
	// BaseURL は API の基底 URL（空なら DefaultAPIBase）。
	BaseURL string
	HTTP    *http.Client
	// Now は現在時刻（テスト用。nil なら time.Now）。
	Now func() time.Time

	mu      sync.Mutex
	buckets map[string]time.Time // ルート → この時刻まで送らない
}

// APIError は Discord API のエラー応答。
type APIError struct {
	Status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("discord: HTTP %d: %s (code %d)", e.Status, e.Message, e.Code)
}

// RateLimitError はレート制限（RetryAfter 後に再試行する）。
type RateLimitError struct {
	RetryAfter time.Duration
	Global     bool
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("discord: rate limited (retry after %s, global=%v)", e.RetryAfter, e.Global)
}

// IsPermanent は再試行しても成功しないエラー（DM を受け付けない・ユーザーが存在しない等）か。
func IsPermanent(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Code {
	case CodeCannotSendToUser, CodeUnknownUser, CodeMissingAccess, CodeUnknownChannel:
		return true
	}
	return ae.Status == http.StatusForbidden || ae.Status == http.StatusNotFound
}

// IsUnauthorized は Bot トークンが無効（401）か。
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultAPIBase
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do は API を呼ぶ。auth は "Bot <token>" / "Bearer <token>"（空なら付けない）。
func (c *Client) do(ctx context.Context, method, path, bucket, auth string, body any, form url.Values, out any) error {
	c.mu.Lock()
	if until, ok := c.buckets[bucket]; ok {
		if d := until.Sub(c.now()); d > 0 {
			c.mu.Unlock()
			return &RateLimitError{RetryAfter: d}
		}
		delete(c.buckets, bucket)
	}
	c.mu.Unlock()
	var rd io.Reader
	ctype := ""
	switch {
	case body != nil:
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd, ctype = bytes.NewReader(b), "application/json"
	case form != nil:
		rd, ctype = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/mikuta0407/buropher, 1.0)")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.Header.Get("X-RateLimit-Remaining") == "0" {
		if d := parseSeconds(res.Header.Get("X-RateLimit-Reset-After")); d > 0 {
			c.mu.Lock()
			if c.buckets == nil {
				c.buckets = map[string]time.Time{}
			}
			c.buckets[bucket] = c.now().Add(d)
			c.mu.Unlock()
		}
	}
	if res.StatusCode == http.StatusTooManyRequests {
		var rl struct {
			RetryAfter float64 `json:"retry_after"`
			Global     bool    `json:"global"`
		}
		_ = json.Unmarshal(data, &rl)
		d := time.Duration(rl.RetryAfter * float64(time.Second))
		if d <= 0 {
			d = parseSeconds(res.Header.Get("Retry-After"))
		}
		if d <= 0 {
			d = parseSeconds(res.Header.Get("X-RateLimit-Reset-After"))
		}
		if d <= 0 {
			d = time.Second
		}
		return &RateLimitError{RetryAfter: d, Global: rl.Global || res.Header.Get("X-RateLimit-Global") == "true"}
	}
	if res.StatusCode >= 300 {
		ae := &APIError{Status: res.StatusCode}
		_ = json.Unmarshal(data, ae)
		if ae.Message == "" {
			ae.Message = strings.TrimSpace(string(data))
			if ae.Message == "" {
				ae.Message = http.StatusText(res.StatusCode)
			}
		}
		return ae
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func parseSeconds(s string) time.Duration {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return time.Duration(math.Ceil(f*1000)) * time.Millisecond
}

// CreateDM は POST /users/@me/channels（DM チャンネルを作る/取得する）。
func (c *Client) CreateDM(ctx context.Context, botToken, recipientID string) (string, error) {
	var ch struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/users/@me/channels", "POST /users/@me/channels", "Bot "+botToken,
		map[string]string{"recipient_id": recipientID}, nil, &ch)
	if err != nil {
		return "", err
	}
	if ch.ID == "" {
		return "", errors.New("discord: empty channel id")
	}
	return ch.ID, nil
}

// EmbedField は埋め込みのフィールド。
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// Embed は埋め込み。
type Embed struct {
	Title       string       `json:"title,omitempty"`
	URL         string       `json:"url,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Author      *EmbedAuthor `json:"author,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
}

// EmbedFooter は埋め込みのフッター。
type EmbedFooter struct {
	Text string `json:"text"`
}

// EmbedAuthor は埋め込みの作成者欄。
type EmbedAuthor struct {
	Name string `json:"name"`
}

// Message はメッセージの作成要求。
type Message struct {
	Content         string           `json:"content,omitempty"`
	Embeds          []Embed          `json:"embeds,omitempty"`
	AllowedMentions *AllowedMentions `json:"allowed_mentions,omitempty"`
}

// AllowedMentions はメンションの扱い（通知ではメンションしない）。
type AllowedMentions struct {
	Parse []string `json:"parse"`
}

// SendMessage は POST /channels/{id}/messages。
func (c *Client) SendMessage(ctx context.Context, botToken, channelID string, m Message) (string, error) {
	if m.AllowedMentions == nil {
		m.AllowedMentions = &AllowedMentions{Parse: []string{}}
	}
	var res struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/channels/"+url.PathEscape(channelID)+"/messages", "POST /channels/"+channelID+"/messages",
		"Bot "+botToken, m, nil, &res)
	return res.ID, err
}

// Token は OAuth2 のアクセストークン応答。
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// ExchangeCode は POST /oauth2/token（認可コードをアクセストークンに交換する）。
func (c *Client) ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (*Token, error) {
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "client_secret": {clientSecret},
	}
	var t Token
	if err := c.do(ctx, http.MethodPost, "/oauth2/token", "POST /oauth2/token", "", nil, form, &t); err != nil {
		return nil, err
	}
	if t.AccessToken == "" {
		return nil, errors.New("discord: empty access token")
	}
	return &t, nil
}

// User は Discord のユーザー。
type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

// DisplayName は表示名（global_name、なければ username）。
func (u *User) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

// CurrentUser は GET /users/@me（OAuth2 のアクセストークンで）。
func (c *Client) CurrentUser(ctx context.Context, accessToken string) (*User, error) {
	var u User
	if err := c.do(ctx, http.MethodGet, "/users/@me", "GET /users/@me", "Bearer "+accessToken, nil, nil, &u); err != nil {
		return nil, err
	}
	if u.ID == "" {
		return nil, errors.New("discord: empty user id")
	}
	return &u, nil
}

// AddGuildMember は PUT /guilds/{guild}/members/{user}（guilds.join スコープのトークンで Guild に参加させる）。
// 既に参加済みなら 204。
func (c *Client) AddGuildMember(ctx context.Context, botToken, guildID, userID, accessToken string) error {
	return c.do(ctx, http.MethodPut, "/guilds/"+url.PathEscape(guildID)+"/members/"+url.PathEscape(userID),
		"PUT /guilds/"+guildID+"/members", "Bot "+botToken, map[string]string{"access_token": accessToken}, nil, nil)
}

// GuildMember は GET /guilds/{guild}/members/{user} で参加しているかを返す。
func (c *Client) GuildMember(ctx context.Context, botToken, guildID, userID string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/guilds/"+url.PathEscape(guildID)+"/members/"+url.PathEscape(userID),
		"GET /guilds/"+guildID+"/members", "Bot "+botToken, nil, nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && (ae.Code == CodeUnknownMember || ae.Status == http.StatusNotFound) {
		return false, nil
	}
	return err == nil, err
}

// AuthorizeURL は OAuth2 の認可画面の URL。
func AuthorizeURL(base, clientID, redirectURI, state string, scopes []string) string {
	if base == "" {
		base = DefaultAuthorizeURL
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"scope": {strings.Join(scopes, " ")}, "state": {state}, "prompt": {"consent"},
	}
	return base + "?" + q.Encode()
}
