package discord_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/discord"
	"github.com/mikuta0407/buropher/internal/discord/discordtest"
)

func TestDMAndErrors(t *testing.T) {
	ctx := context.Background()
	fake := discordtest.New()
	defer fake.Close()
	fake.BotToken = "tok"
	c := &discord.Client{BaseURL: fake.URL}
	ch, err := c.CreateDM(ctx, "tok", "u1")
	if err != nil || ch != "dm-u1" {
		t.Fatal(ch, err)
	}
	if _, err := c.SendMessage(ctx, "tok", ch, discord.Message{Content: "hi", Embeds: []discord.Embed{{Title: "t"}}}); err != nil {
		t.Fatal(err)
	}
	m := fake.SentTo("u1")[0].Body
	if m["content"] != "hi" || m["allowed_mentions"].(map[string]any)["parse"] == nil {
		t.Errorf("body = %v", m)
	}
	// 401
	_, err = c.CreateDM(ctx, "bad", "u1")
	if !discord.IsUnauthorized(err) {
		t.Errorf("err = %v", err)
	}
	// 50007
	fake.CannotDM["u1"] = true
	_, err = c.SendMessage(ctx, "tok", ch, discord.Message{Content: "x"})
	var ae *discord.APIError
	if !errors.As(err, &ae) || ae.Code != discord.CodeCannotSendToUser || !discord.IsPermanent(err) {
		t.Errorf("err = %v", err)
	}
	// 429
	fake.RateLimitNext, fake.RetryAfter = 1, 0.25
	_, err = c.SendMessage(ctx, "tok", ch, discord.Message{Content: "x"})
	var rl *discord.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 250*time.Millisecond {
		t.Errorf("err = %v", err)
	}
}

// TestBucketRemaining は X-RateLimit-Remaining: 0 のバケットへの次の要求を reset まで送らないことを確かめる。
func TestBucketRemaining(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset-After", "2.5")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer ts.Close()
	now := time.Unix(1000, 0)
	c := &discord.Client{BaseURL: ts.URL, Now: func() time.Time { return now }}
	if _, err := c.SendMessage(context.Background(), "t", "c1", discord.Message{Content: "a"}); err != nil {
		t.Fatal(err)
	}
	_, err := c.SendMessage(context.Background(), "t", "c1", discord.Message{Content: "b"})
	var rl *discord.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 2500*time.Millisecond || calls != 1 {
		t.Fatalf("err = %v calls = %d", err, calls)
	}
	// 別のチャンネル（別バケット）は送れる
	if _, err := c.SendMessage(context.Background(), "t", "c2", discord.Message{Content: "c"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if _, err := c.SendMessage(context.Background(), "t", "c1", discord.Message{Content: "d"}); err != nil {
		t.Fatal(err)
	}
}

func TestOAuth(t *testing.T) {
	ctx := context.Background()
	fake := discordtest.New()
	defer fake.Close()
	fake.ClientID, fake.ClientSecret = "cid", "sec"
	fake.Codes["c1"] = discordtest.User{ID: "42", Username: "alice"}
	c := &discord.Client{BaseURL: fake.URL}
	tok, err := c.ExchangeCode(ctx, "cid", "sec", "c1", "http://x/cb")
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.CurrentUser(ctx, tok.AccessToken)
	if err != nil || u.ID != "42" || u.DisplayName() != "alice" {
		t.Fatal(u, err)
	}
	if err := c.AddGuildMember(ctx, "", "g", "42", tok.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExchangeCode(ctx, "cid", "sec", "nope", "http://x/cb"); err == nil {
		t.Error("invalid code accepted")
	}
	u2 := discord.AuthorizeURL("", "cid", "http://x/cb", "st", []string{"identify", "guilds.join"})
	if !strings.HasPrefix(u2, discord.DefaultAuthorizeURL+"?") || !strings.Contains(u2, "scope=identify+guilds.join") || !strings.Contains(u2, "state=st") {
		t.Errorf("authorize url = %s", u2)
	}
}
