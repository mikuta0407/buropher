// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package discordtest はテスト用の偽 Discord API サーバ（httptest）。
//
// /users/@me/channels（DM 作成）、/channels/{id}/messages（送信）、/oauth2/token、/users/@me、
// /guilds/{g}/members/{u} を扱う。レート制限（429）や DM 不可（50007）を再現できる。
package discordtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// SentMessage は送信されたメッセージ。
type SentMessage struct {
	ChannelID string
	UserID    string
	Body      map[string]any
}

// Server は偽 Discord API。
type Server struct {
	*httptest.Server
	// BotToken は受け付ける Bot トークン（空なら何でもよい）。
	BotToken string
	// ClientID / ClientSecret は OAuth2 のクライアント。
	ClientID, ClientSecret string
	// Codes は認可コード → Discord ユーザー。
	Codes map[string]User
	// CannotDM は DM を受け付けないユーザー ID（送信時に 50007）。
	CannotDM map[string]bool
	// RateLimitNext は次の n 回の送信を 429 にする。
	RateLimitNext int
	// RetryAfter は 429 の retry_after（秒）。
	RetryAfter float64

	mu        sync.Mutex
	channels  map[string]string // channel → user
	Messages  []SentMessage
	GuildAdds []string // "guild/user"
	DMCreates int
}

// User は OAuth2 で返すユーザー。
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// New は偽サーバを起動する。
func New() *Server {
	s := &Server{Codes: map[string]User{}, CannotDM: map[string]bool{}, channels: map[string]string{}, RetryAfter: 1.5}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// SentTo はユーザー宛てに送信されたメッセージ。
func (s *Server) SentTo(userID string) []SentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SentMessage
	for _, m := range s.Messages {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) botOK(r *http.Request) bool {
	return s.BotToken == "" || r.Header.Get("Authorization") == "Bot "+s.BotToken
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && p == "/oauth2/token":
		_ = r.ParseForm()
		if r.Form.Get("client_id") != s.ClientID || r.Form.Get("client_secret") != s.ClientSecret {
			writeJSON(w, 401, map[string]any{"error": "invalid_client"})
			return
		}
		u, ok := s.Codes[r.Form.Get("code")]
		if !ok {
			writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
			return
		}
		writeJSON(w, 200, map[string]any{"access_token": "at-" + u.ID, "token_type": "Bearer", "expires_in": 604800, "scope": "identify guilds.join"})
	case r.Method == http.MethodGet && p == "/users/@me":
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer at-")
		for _, u := range s.Codes {
			if u.ID == tok {
				writeJSON(w, 200, u)
				return
			}
		}
		writeJSON(w, 401, map[string]any{"message": "401: Unauthorized", "code": 0})
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/guilds/"):
		if !s.botOK(r) {
			writeJSON(w, 401, map[string]any{"message": "401: Unauthorized", "code": 0})
			return
		}
		parts := strings.Split(strings.TrimPrefix(p, "/guilds/"), "/")
		s.GuildAdds = append(s.GuildAdds, parts[0]+"/"+parts[len(parts)-1])
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodPost && p == "/users/@me/channels":
		if !s.botOK(r) {
			writeJSON(w, 401, map[string]any{"message": "401: Unauthorized", "code": 0})
			return
		}
		var body struct {
			RecipientID string `json:"recipient_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.DMCreates++
		id := "dm-" + body.RecipientID
		s.channels[id] = body.RecipientID
		writeJSON(w, 200, map[string]any{"id": id, "type": 1})
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/channels/") && strings.HasSuffix(p, "/messages"):
		if !s.botOK(r) {
			writeJSON(w, 401, map[string]any{"message": "401: Unauthorized", "code": 0})
			return
		}
		ch := strings.TrimSuffix(strings.TrimPrefix(p, "/channels/"), "/messages")
		uid, ok := s.channels[ch]
		if !ok {
			writeJSON(w, 404, map[string]any{"message": "Unknown Channel", "code": 10003})
			return
		}
		if s.RateLimitNext > 0 {
			s.RateLimitNext--
			w.Header().Set("Retry-After", fmt.Sprint(s.RetryAfter))
			writeJSON(w, 429, map[string]any{"message": "You are being rate limited.", "retry_after": s.RetryAfter, "global": false})
			return
		}
		if s.CannotDM[uid] {
			writeJSON(w, 403, map[string]any{"message": "Cannot send messages to this user", "code": 50007})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.Messages = append(s.Messages, SentMessage{ChannelID: ch, UserID: uid, Body: body})
		w.Header().Set("X-RateLimit-Remaining", "4")
		writeJSON(w, 200, map[string]any{"id": fmt.Sprint(len(s.Messages)), "channel_id": ch})
	default:
		writeJSON(w, 404, map[string]any{"message": "404: Not Found", "code": 0})
	}
}
