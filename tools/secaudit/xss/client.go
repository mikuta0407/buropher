// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// client は 1 サーバ・1 ユーザー分のセッション（Cookie）付き HTTP クライアント。
type client struct {
	base  string
	user  string
	pass  string
	hc    *http.Client
	token string // authenticity_token（セッション単位で使い回す）
}

func newClient(base, user, pass string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{
		base: strings.TrimRight(base, "/"),
		user: user,
		pass: pass,
		hc: &http.Client{
			Jar:     jar,
			Timeout: 60 * time.Second,
			// リダイレクトは追わない（Location を見て ID を拾うため）
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type resp struct {
	Status int
	Header http.Header
	Body   []byte
}

var reCSRF = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

func (c *client) do(req *http.Request) (*resp, error) {
	r, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if m := reCSRF.FindSubmatch(b); m != nil {
		c.token = string(m[1])
	}
	return &resp{Status: r.StatusCode, Header: r.Header, Body: b}, nil
}

// get は GET する。xhr なら X-Requested-With を付ける（Rails の .js 応答は XHR 必須）。
func (c *client) get(path string, xhr bool) (*resp, error) {
	req, err := http.NewRequest("GET", c.abs(path), nil)
	if err != nil {
		return nil, err
	}
	if xhr {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "text/javascript, application/javascript, */*")
	}
	req.Header.Set("Accept-Language", "en")
	return c.do(req)
}

func (c *client) abs(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return c.base + path
}

func (c *client) login() error {
	if c.user == "" {
		return nil
	}
	if _, err := c.get("/login", false); err != nil {
		return err
	}
	r, err := c.post("/login", url.Values{"username": {c.user}, "password": {c.pass}, "login": {"Login"}}, false)
	if err != nil {
		return err
	}
	loc := r.Header.Get("Location")
	if r.Status/100 != 3 || strings.Contains(loc, "/login") {
		return fmt.Errorf("login %s failed: %d %s", c.user, r.Status, loc)
	}
	// ログイン後はセッションが変わるのでトークンを取り直す
	c.token = ""
	_, err = c.get("/my/page", false)
	return err
}

// post はフォームを POST する（authenticity_token を自動付与）。
func (c *client) post(path string, form url.Values, xhr bool) (*resp, error) {
	if c.token == "" {
		if _, err := c.get("/", false); err != nil {
			return nil, err
		}
	}
	f := url.Values{}
	for k, v := range form {
		f[k] = v
	}
	if f.Get("authenticity_token") == "" {
		f.Set("authenticity_token", c.token)
	}
	req, err := http.NewRequest("POST", c.abs(path), strings.NewReader(f.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept-Language", "en")
	if xhr {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "text/javascript, application/javascript, */*")
	}
	return c.do(req)
}

// api は REST API を Basic 認証（c.user/c.pass）で呼ぶ。body は JSON 化する。
func (c *client) api(method, path string, body any) (*resp, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.abs(path), rd)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return &resp{Status: r.StatusCode, Header: r.Header, Body: b}, nil
}

// upload は /uploads.json にバイナリを送ってトークンを返す。
func (c *client) upload(filename string, content []byte) (string, error) {
	req, err := http.NewRequest("POST", c.abs("/uploads.json?filename="+url.QueryEscape(filename)), bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Content-Type", "application/octet-stream")
	r, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	var v struct {
		Upload struct {
			Token string `json:"token"`
		} `json:"upload"`
	}
	if err := json.Unmarshal(b, &v); err != nil || v.Upload.Token == "" {
		return "", fmt.Errorf("upload %q: %d %s", filename, r.StatusCode, b)
	}
	return v.Upload.Token, nil
}
