// Package client は Redmine 互換サーバへのログインとリクエスト送信を行う。
package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	xhtml "golang.org/x/net/html"

	"github.com/mikuta0407/buropher/tools/compat/internal/scenario"
)

// Response は取得結果。
type Response struct {
	Status      int
	ContentType string
	Location    string
	Body        []byte
}

// Target は比較対象サーバ 1 つ分。ユーザーごとにログイン済みセッションを保持する。
type Target struct {
	Base     *url.URL
	timeout  time.Duration
	sessions map[string]*http.Client
}

// New は baseURL を対象とする Target を作る。
func New(baseURL string, timeout time.Duration) (*Target, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid base url %q", baseURL)
	}
	return &Target{Base: u, timeout: timeout, sessions: map[string]*http.Client{}}, nil
}

// BaseString はベース URL 文字列（末尾スラッシュなし）。
func (t *Target) BaseString() string { return t.Base.String() }

func (t *Target) newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: t.timeout,
		// リダイレクトは追わず、ステータスと Location を比較対象にする
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (t *Target) url(path string) string { return t.Base.String() + path }

// session はユーザーのログイン済みクライアントを返す（初回はログインする）。
func (t *Target) session(c scenario.Case) (*http.Client, error) {
	key := c.User
	if c.Auth != "session" {
		key = "\x00" + c.Auth + ":" + c.User
	}
	if cl, ok := t.sessions[key]; ok {
		return cl, nil
	}
	cl := t.newClient()
	if c.Auth == "session" {
		if err := t.login(cl, c.Cred); err != nil {
			return nil, fmt.Errorf("login as %s: %w", c.User, err)
		}
	}
	t.sessions[key] = cl
	return cl, nil
}

// csrfToken は HTML から authenticity_token（hidden input または meta）を取り出す。
func csrfToken(body []byte) string {
	doc, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	if el := cascadia.MustCompile(`form input[name="authenticity_token"]`).MatchFirst(doc); el != nil {
		for _, a := range el.Attr {
			if a.Key == "value" {
				return a.Val
			}
		}
	}
	if el := cascadia.MustCompile(`meta[name="csrf-token"]`).MatchFirst(doc); el != nil {
		for _, a := range el.Attr {
			if a.Key == "content" {
				return a.Val
			}
		}
	}
	return ""
}

func (t *Target) fetchToken(cl *http.Client, path string) (string, error) {
	resp, err := cl.Get(t.url(path))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	tok := csrfToken(body)
	if tok == "" {
		return "", fmt.Errorf("authenticity_token not found in %s (status %d)", path, resp.StatusCode)
	}
	return tok, nil
}

// login は Redmine のログインフォームでログインする（GET /login → POST /login）。
func (t *Target) login(cl *http.Client, cred scenario.Credential) error {
	tok, err := t.fetchToken(cl, "/login")
	if err != nil {
		return err
	}
	form := url.Values{
		"authenticity_token": {tok},
		"username":           {cred.Login},
		"password":           {cred.Password},
		"login":              {"Login"},
	}
	resp, err := cl.PostForm(t.url("/login"), form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || strings.Contains(loc, "/login") {
		return fmt.Errorf("login failed (status %d, location %q)", resp.StatusCode, loc)
	}
	return nil
}

// Do はケース 1 件を実行する。
func (t *Target) Do(c scenario.Case) (*Response, error) {
	cl, err := t.session(c)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	ctype := c.CType
	switch {
	case len(c.Form) > 0:
		form := url.Values{}
		for k, vs := range c.Form {
			for _, v := range vs {
				form.Add(k, v)
			}
		}
		if (c.Auth == "session" || c.CSRF) && form.Get("authenticity_token") == "" {
			tok, err := t.fetchToken(cl, "/")
			if err != nil {
				return nil, err
			}
			form.Set("authenticity_token", tok)
		}
		body = strings.NewReader(form.Encode())
		if ctype == "" {
			ctype = "application/x-www-form-urlencoded"
		}
	case c.Body != "":
		body = strings.NewReader(c.Body)
	}
	req, err := http.NewRequest(c.Method, t.url(c.Path), body)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	if c.Auth == "basic" {
		req.SetBasicAuth(c.Cred.Login, c.Cred.Password)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Location:    resp.Header.Get("Location"),
		Body:        data,
	}, nil
}
