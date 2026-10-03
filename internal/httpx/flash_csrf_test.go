// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestFlashLifecycle(t *testing.T) {
	m := newTestManager(NewMemoryStore(), &clock{t: time.Unix(1_700_000_000, 0)})
	h := stack(m)
	j := newJar()

	// req1: 設定してリダイレクト
	do(t, j, h, "POST", "/issues", "", func(w http.ResponseWriter, r *http.Request) {
		f := FlashOf(r)
		f.SetNotice("Successful creation.")
		f.SetWarning("w")
		f.Now("error", "only now")
		if f.Get("error") != "only now" {
			t.Error("now not readable")
		}
	})
	// req2: 表示（挿入順）
	do(t, j, h, "GET", "/issues/1", "", func(w http.ResponseWriter, r *http.Request) {
		got := FlashOf(r).Entries()
		if len(got) != 2 || got[0] != (FlashEntry{"notice", "Successful creation."}) || got[1].Key != "warning" {
			t.Errorf("entries %+v", got)
		}
	})
	// req3: 消えている（クッキーも削除）
	do(t, j, h, "GET", "/issues/1", "", func(w http.ResponseWriter, r *http.Request) {
		if !FlashOf(r).Empty() {
			t.Errorf("flash not cleared: %+v", FlashOf(r).Entries())
		}
	})
	if _, ok := j.cookies["_redmine_session"]; ok {
		t.Error("empty session cookie remains")
	}

	// アクセスしなければ残る（API リクエスト等）
	do(t, j, h, "POST", "/x", "", func(w http.ResponseWriter, r *http.Request) { FlashOf(r).SetError("e") })
	do(t, j, h, "GET", "/issues.json", "", func(w http.ResponseWriter, r *http.Request) {})
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		if FlashOf(r).Get("error") != "e" {
			t.Error("untouched flash lost")
		}
		FlashOf(r).Keep("error")
	})
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		f := FlashOf(r)
		if f.Get("error") != "e" {
			t.Error("kept flash lost")
		}
		// 再設定したキーは次へ引き継がれる
		f.Set("error", "e2")
	})
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		f := FlashOf(r)
		if f.Get("error") != "e2" {
			t.Error("re-set flash lost")
		}
		f.Set("notice", "n")
		f.Discard("notice")
		f.Delete("error")
	})
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		if !FlashOf(r).Empty() {
			t.Error("discard/delete failed")
		}
	})
	// Reset で flash も消える
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) { FlashOf(r).SetNotice("x") })
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		SessionOf(r).Reset()
		if !FlashOf(r).Empty() {
			t.Error("flash survives reset")
		}
		FlashOf(r).SetError("after reset")
	})
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		if FlashOf(r).Get("error") != "after reset" {
			t.Error("flash set after reset lost")
		}
	})
}

func TestCSRFTokens(t *testing.T) {
	m := newTestManager(NewMemoryStore(), &clock{t: time.Unix(1_700_000_000, 0)})
	req := httptest.NewRequest("GET", "/", nil)
	s := m.Load(req)
	t1, t2 := s.CSRFToken(), s.CSRFToken()
	if t1 == t2 {
		t.Error("masked tokens should differ")
	}
	if len(t1) != 86 || strings.ContainsAny(t1, "+/=") {
		t.Errorf("token format %q", t1)
	}
	if !s.ValidCSRFToken(t1) || !s.ValidCSRFToken(t2) {
		t.Error("valid token rejected")
	}
	// 実トークン（マスクなし）も受け付ける
	real := s.GetString(csrfSessionKey)
	if !s.ValidCSRFToken(real) {
		t.Error("unmasked real token rejected")
	}
	// 実トークンをマスクしたものも受け付ける
	rb, _ := base64.RawURLEncoding.DecodeString(real)
	if !s.ValidCSRFToken(maskCSRFToken(rb)) {
		t.Error("masked real token rejected")
	}
	// パディング付き・標準 base64 文字でも可
	std := strings.NewReplacer("-", "+", "_", "/").Replace(t1)
	if !s.ValidCSRFToken(std + "==") {
		t.Error("std base64 rejected")
	}
	bad := []byte(t1)
	bad[50] ^= 1
	for _, b := range []string{"", "x", string(bad), "!!!!", base64.RawURLEncoding.EncodeToString(make([]byte, 40))} {
		if s.ValidCSRFToken(b) {
			t.Errorf("invalid token accepted: %q", b)
		}
	}
	// 別セッションのトークンは無効
	s2 := m.Load(httptest.NewRequest("GET", "/", nil))
	if s2.ValidCSRFToken(t1) {
		t.Error("token from another session accepted")
	}
}

type pageRec struct {
	status  int
	message string
	layout  bool
}

func csrfApp(t *testing.T, m *SessionManager, page *pageRec) http.Handler {
	er := &ErrorRenderer{Page: ErrorPageFunc(func(w http.ResponseWriter, r *http.Request, status int, message string, layout bool) {
		*page = pageRec{status, message, layout}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("<h2>" + http.StatusText(status) + "</h2>"))
	})}
	r := chi.NewRouter()
	r.Use(ParamsMiddleware(nil, nil), MethodOverride, m.Middleware)
	r.Group(func(r chi.Router) {
		r.Use(CSRFMiddleware(CSRFOptions{
			OnFailure: RedmineCSRFFailure(RedmineCSRFFailureOptions{Errors: er}),
			Skip:      func(r *http.Request) bool { return r.URL.Path == "/sys/fetch_changesets" },
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		}))
		Route(r, "POST", "/sys/fetch_changesets", func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte("sys"))
		})
		Route(r, "GET", "/form", func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(CSRFMetaTags(req)))
		})
		Route(r, "POST", "/issues", func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte("created"))
		})
		Route(r, "DELETE", "/issues/{id}", func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte("deleted"))
		})
	})
	// SkipCSRFMiddleware は CSRFMiddleware より前に適用する必要がある
	r.With(SkipCSRFMiddleware, CSRFMiddleware(CSRFOptions{})).Post("/mail_handler", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("mail"))
	})
	return r
}

var metaTokenRe = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)" />`)

func TestCSRFMiddleware(t *testing.T) {
	m := newTestManager(NewMemoryStore(), &clock{t: time.Unix(1_700_000_000, 0)})
	var page pageRec
	app := csrfApp(t, m, &page)
	j := newJar()

	w := do(t, j, app, "GET", "/form", "", nil)
	if !strings.HasPrefix(w.Body.String(), `<meta name="csrf-param" content="authenticity_token" />`) {
		t.Fatalf("meta: %s", w.Body.String())
	}
	tok := metaTokenRe.FindStringSubmatch(w.Body.String())[1]

	// パラメータで送る
	w = do(t, j, app, "POST", "/issues", "authenticity_token="+url.QueryEscape(tok)+"&issue[subject]=x", nil)
	if w.Code != 200 || w.Body.String() != "created" {
		t.Errorf("valid param token: %d %s", w.Code, w.Body.String())
	}
	// _method=delete と組み合わせ
	w = do(t, j, app, "POST", "/issues/1", "_method=delete&authenticity_token="+url.QueryEscape(tok), nil)
	if w.Body.String() != "deleted" {
		t.Errorf("method override + token: %d %s", w.Code, w.Body.String())
	}
	// ヘッダで送る
	req := httptest.NewRequest("POST", "/issues", nil)
	j.apply(req)
	req.Header.Set("X-CSRF-Token", tok)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("header token: %d", w.Code)
	}
	// API リクエスト（.json）は検証しない
	w = do(t, j, app, "POST", "/issues.json", "issue[subject]=x", nil)
	if w.Code != 200 {
		t.Errorf("api request: %d", w.Code)
	}
	w = do(t, j, app, "POST", "/mail_handler", "", nil)
	if w.Code != 200 {
		t.Errorf("skip: %d", w.Code)
	}
	w = do(t, j, app, "POST", "/sys/fetch_changesets", "", nil)
	if w.Code != 200 {
		t.Errorf("skip option: %d", w.Code)
	}

	// ログイン状態にしてから不正トークン → ログアウト + 422 エラーページ
	sessReq := httptest.NewRequest("GET", "/", nil)
	j.apply(sessReq)
	j.cookies["autologin"] = &http.Cookie{Name: "autologin", Value: "abc"}
	w = do(t, j, app, "POST", "/issues", "authenticity_token=bad", nil)
	if w.Code != 422 || page.status != 422 || page.message != "Invalid form authenticity token." || !page.layout {
		t.Errorf("failure: %d %+v", w.Code, page)
	}
	if _, ok := j.cookies["autologin"]; ok {
		t.Error("autologin cookie not deleted")
	}
	// リセットされたので旧トークンは無効
	w = do(t, j, app, "POST", "/issues", "authenticity_token="+url.QueryEscape(tok), nil)
	if w.Code != 422 {
		t.Errorf("old token after reset: %d", w.Code)
	}
	// XHR + JS は空ボディの 422（text/javascript）
	page = pageRec{}
	req = httptest.NewRequest("POST", "/issues", strings.NewReader("a=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "text/javascript")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 422 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "text/javascript" || page.status != 0 {
		t.Errorf("xhr failure: %d %q %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
}

func TestCSRFWithoutSession(t *testing.T) {
	if CSRFToken(httptest.NewRequest("GET", "/", nil)) != "" {
		t.Error("token without session")
	}
	if VerifiedRequest(httptest.NewRequest("POST", "/", nil)) {
		t.Error("verified without session")
	}
	if !VerifiedRequest(httptest.NewRequest("HEAD", "/", nil)) {
		t.Error("HEAD should be verified")
	}
	w := httptest.NewRecorder()
	CSRFMiddleware(CSRFOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, httptest.NewRequest("POST", "/x", nil))
	if w.Code != 422 {
		t.Errorf("default failure %d", w.Code)
	}
}
