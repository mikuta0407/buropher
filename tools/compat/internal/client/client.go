// Package client は Redmine 互換サーバへのログインとリクエスト送信を行う。
package client

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// vars はシナリオの capture で得た変数（${name}）。
	vars map[string]string
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

var reVar = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// expand は ${name} を capture で得た変数に置き換える（未定義はそのまま）。
func (t *Target) expand(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return reVar.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := t.vars[reVar.FindStringSubmatch(m)[1]]; ok {
			return v
		}
		return m
	})
}

// expandCase は Path / Form / Body の変数を展開したケースを返す。
func (t *Target) expandCase(c scenario.Case) scenario.Case {
	c.Path = t.expand(c.Path)
	c.Body = t.expand(c.Body)
	if len(c.Form) > 0 {
		form := scenario.FormValues{}
		for k, vs := range c.Form {
			var out []string
			for _, v := range vs {
				out = append(out, t.expand(v))
			}
			form[t.expand(k)] = out
		}
		c.Form = form
	}
	return c
}

// capture は Location ヘッダから変数を取り出す。
func (t *Target) capture(c scenario.Case, location string) {
	for name, pattern := range c.Capture {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		m := re.FindStringSubmatch(location)
		switch {
		case m == nil:
			delete(t.vars, name)
		case len(m) > 1:
			t.vars[name] = m[1]
		default:
			t.vars[name] = m[0]
		}
	}
}

// multipartBody は Form と Files を multipart/form-data で組み立てる。
func multipartBody(form url.Values, files map[string]string) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range form[k] {
			if err := mw.WriteField(k, v); err != nil {
				return nil, "", err
			}
		}
	}
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		data, err := os.ReadFile(files[k])
		if err != nil {
			return nil, "", err
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, k, filepath.Base(files[k])))
		h.Set("Content-Type", "text/csv")
		w, err := mw.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := w.Write(data); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}

// Do はケース 1 件を実行する。
func (t *Target) Do(c scenario.Case) (*Response, error) {
	if t.vars == nil {
		t.vars = map[string]string{}
	}
	c = t.expandCase(c)
	cl, err := t.session(c)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	ctype := c.CType
	switch {
	case len(c.Files) > 0:
		form := url.Values{}
		for k, vs := range c.Form {
			for _, v := range vs {
				form.Add(k, v)
			}
		}
		if c.Auth == "session" && form.Get("authenticity_token") == "" {
			tok, err := t.fetchToken(cl, "/")
			if err != nil {
				return nil, err
			}
			form.Set("authenticity_token", tok)
		}
		b, ct, err := multipartBody(form, c.Files)
		if err != nil {
			return nil, err
		}
		body = b
		if ctype == "" {
			ctype = ct
		}
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
	t.capture(c, resp.Header.Get("Location"))
	return &Response{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Location:    resp.Header.Get("Location"),
		Body:        data,
	}, nil
}
