package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// jar は Set-Cookie を次のリクエストへ引き継ぐ簡易クッキージャー。
type jar struct{ cookies map[string]*http.Cookie }

func newJar() *jar { return &jar{cookies: map[string]*http.Cookie{}} }

func (j *jar) apply(req *http.Request) {
	for _, c := range j.cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
}

func (j *jar) update(w *httptest.ResponseRecorder) {
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(j.cookies, c.Name)
		} else {
			j.cookies[c.Name] = c
		}
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestManager(store Store, clk *clock) *SessionManager {
	return &SessionManager{Store: store, Secret: []byte("test-secret"), Now: clk.now}
}

func do(t *testing.T, j *jar, h http.Handler, method, target string, body string, fn func(w http.ResponseWriter, r *http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	j.apply(req)
	w := httptest.NewRecorder()
	handlerFn = fn
	h.ServeHTTP(w, req)
	j.update(w)
	return w
}

var handlerFn func(w http.ResponseWriter, r *http.Request)

func stack(m *SessionManager) http.Handler {
	return ParamsMiddleware(nil, nil)(m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handlerFn != nil {
			handlerFn(w, r)
		}
	})))
}

func TestSessionAnonymousCookieMode(t *testing.T) {
	store := NewMemoryStore()
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := newTestManager(store, clk)
	h := stack(m)
	j := newJar()

	// 何も書かなければクッキーは出ない
	w := do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) { _ = SessionOf(r).Get("x") })
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("cookie issued for untouched session")
	}
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) { SessionOf(r).Set("per_page", 50) })
	c := j.cookies["_redmine_session"]
	if c == nil || !strings.HasPrefix(c.Value, "c.") || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie: %+v", c)
	}
	if store.Len() != 0 {
		t.Error("anonymous session stored server-side")
	}
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		if got := SessionOf(r).GetInt("per_page"); got != 50 {
			t.Errorf("per_page = %d", got)
		}
	})
	// 改ざんされたクッキーは無視
	j.cookies["_redmine_session"].Value = c.Value[:len(c.Value)-2] + "AA"
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		if SessionOf(r).Has("per_page") || !SessionOf(r).IsNew() {
			t.Error("tampered cookie accepted")
		}
	})
	// 大きいデータはサーバ側へ
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		SessionOf(r).Set("big", strings.Repeat("x", 5000))
	})
	if !strings.HasPrefix(j.cookies["_redmine_session"].Value, "s.") || store.Len() != 1 {
		t.Errorf("large session: %s len=%d", j.cookies["_redmine_session"].Value[:5], store.Len())
	}
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		if len(SessionOf(r).GetString("big")) != 5000 {
			t.Error("big value lost")
		}
	})
}

func TestSessionLoginRotationAndRevocation(t *testing.T) {
	store := NewMemoryStore()
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := newTestManager(store, clk)
	m.ServerSideAnonymous = true
	h := stack(m)
	j := newJar()

	do(t, j, h, "GET", "/login", "", func(w http.ResponseWriter, r *http.Request) { SessionOf(r).Set("k", "v") })
	anonCookie := j.cookies["_redmine_session"].Value
	if !strings.HasPrefix(anonCookie, "s.") || !strings.Contains(anonCookie, ".a.") {
		t.Fatalf("anon cookie %q", anonCookie)
	}
	var oldID, newID string
	do(t, j, h, "POST", "/login", "", func(w http.ResponseWriter, r *http.Request) {
		s := SessionOf(r)
		oldID = s.ID()
		s.Reset() // logged_user=
		s.SetUserID(42)
		s.SetSudoAt(clk.t)
	})
	do(t, j, h, "GET", "/my/page", "", func(w http.ResponseWriter, r *http.Request) {
		s := SessionOf(r)
		newID = s.ID()
		if s.UserID() != 42 || s.Has("k") || !s.SudoAt().Equal(clk.t) {
			t.Errorf("after login: uid=%d k=%v", s.UserID(), s.Has("k"))
		}
	})
	if oldID == "" || newID == "" || oldID == newID {
		t.Fatalf("id not rotated: %q %q", oldID, newID)
	}
	if rec, _ := store.Get(context.Background(), oldID); rec != nil {
		t.Error("old session not destroyed")
	}
	if !strings.Contains(j.cookies["_redmine_session"].Value, ".u.") {
		t.Error("logged-in cookie kind")
	}

	// 別ブラウザでもログイン
	j2 := newJar()
	do(t, j2, h, "POST", "/login", "", func(w http.ResponseWriter, r *http.Request) { SessionOf(r).SetUserID(42) })
	if store.Len() != 2 {
		t.Fatalf("store len %d", store.Len())
	}
	// パスワード変更: 現在のセッション以外を失効
	_ = store.DestroyAllForUser(context.Background(), 42, newID)
	do(t, j2, h, "GET", "/my/page", "", func(w http.ResponseWriter, r *http.Request) {
		s := SessionOf(r)
		if s.UserID() != 0 || !s.Revoked() {
			t.Errorf("revoked session: uid=%d revoked=%v", s.UserID(), s.Revoked())
		}
	})
	do(t, j, h, "GET", "/my/page", "", func(w http.ResponseWriter, r *http.Request) {
		if SessionOf(r).UserID() != 42 || SessionOf(r).Revoked() {
			t.Error("current session lost")
		}
	})

	// ログアウト: Reset して空になればクッキー削除
	do(t, j, h, "POST", "/logout", "", func(w http.ResponseWriter, r *http.Request) { SessionOf(r).Reset() })
	if _, ok := j.cookies["_redmine_session"]; ok {
		t.Error("cookie not deleted on logout")
	}
	if rec, _ := store.Get(context.Background(), newID); rec != nil {
		t.Error("session not destroyed on logout")
	}
}

func TestSessionExpiryAndTouch(t *testing.T) {
	store := NewMemoryStore()
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := newTestManager(store, clk)
	m.Policy = func() ExpiryPolicy { return PolicyFromMinutes(60*24, 30) }
	h := stack(m)
	j := newJar()
	start := clk.t
	do(t, j, h, "POST", "/login", "", func(w http.ResponseWriter, r *http.Request) { SessionOf(r).SetUserID(1) })
	var id string
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) { id = SessionOf(r).ID() })
	rec, _ := store.Get(context.Background(), id)
	if !rec.ExpiresAt.Equal(start.Add(30 * time.Minute)) {
		t.Errorf("expires_at %v", rec.ExpiresAt)
	}
	// 30 秒後: 更新しない
	clk.t = start.Add(30 * time.Second)
	do(t, j, h, "GET", "/", "", nil)
	rec, _ = store.Get(context.Background(), id)
	if !rec.UpdatedAt.Equal(start) {
		t.Errorf("touched too early: %v", rec.UpdatedAt)
	}
	// 2 分後: 更新する
	clk.t = start.Add(2 * time.Minute)
	do(t, j, h, "GET", "/", "", nil)
	rec, _ = store.Get(context.Background(), id)
	if !rec.UpdatedAt.Equal(clk.t) {
		t.Errorf("not touched: %v", rec.UpdatedAt)
	}
	// 31 分無操作でタイムアウト
	clk.t = clk.t.Add(31 * time.Minute)
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		s := SessionOf(r)
		if !s.Expired(m.Policy(), clk.t) {
			t.Error("timeout not detected")
		}
		// Redmine: logged_user = nil; flash[:error] = ...
		s.Reset()
		s.Flash().SetError("Your session has expired. Please login again.")
	})
	do(t, j, h, "GET", "/login", "", func(w http.ResponseWriter, r *http.Request) {
		s := SessionOf(r)
		if s.UserID() != 0 || s.Flash().Get("error") == "" {
			t.Error("expired session handling")
		}
	})

	// lifetime
	p := PolicyFromMinutes(10, 0)
	recL := &Record{CreatedAt: start, UpdatedAt: start.Add(9 * time.Minute)}
	if !p.Valid(recL, start.Add(9*time.Minute)) || p.Valid(recL, start.Add(10*time.Minute)) {
		t.Error("lifetime")
	}
	if !(ExpiryPolicy{}).Valid(recL, start.Add(1000*time.Hour)) {
		t.Error("no policy should be valid")
	}
	anon := &Session{rec: Record{}, m: m}
	if anon.Expired(p, start.Add(time.Hour)) {
		t.Error("anonymous never expires")
	}
}

func TestSessionRenewAndSecureCookie(t *testing.T) {
	store := NewMemoryStore()
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := newTestManager(store, clk)
	m.CookieName = "_buropher"
	m.CookiePath = "/redmine"
	h := stack(m)
	j := newJar()
	var id1, id2 string
	do(t, j, h, "POST", "/", "", func(w http.ResponseWriter, r *http.Request) { SessionOf(r).SetUserID(3); SessionOf(r).Set("a", "b") })
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		id1 = SessionOf(r).ID()
		SessionOf(r).Renew()
	})
	do(t, j, h, "GET", "/", "", func(w http.ResponseWriter, r *http.Request) {
		id2 = SessionOf(r).ID()
		if SessionOf(r).GetString("a") != "b" {
			t.Error("data lost on renew")
		}
	})
	if id1 == id2 || store.Len() != 1 {
		t.Errorf("renew: %q %q len=%d", id1, id2, store.Len())
	}
	c := j.cookies["_buropher"]
	if c == nil || c.Path != "/redmine" || c.Secure {
		t.Errorf("cookie %+v", c)
	}
	req := httptest.NewRequest("GET", "https://example.com/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "127.0.0.1:1234"
	req2.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { SessionOf(r).Set("x", 1) })).ServeHTTP(w, req2)
	if cs := w.Result().Cookies(); len(cs) != 1 || !cs[0].Secure {
		t.Errorf("secure cookie behind proxy: %+v", cs)
	}
}

func TestMemoryStoreDeleteExpired(t *testing.T) {
	s := NewMemoryStore()
	now := time.Unix(1_700_000_000, 0)
	ctx := context.Background()
	_ = s.Save(ctx, &Record{ID: "a", ExpiresAt: now.Add(-time.Second)})
	_ = s.Save(ctx, &Record{ID: "b", ExpiresAt: now.Add(time.Hour)})
	_ = s.Save(ctx, &Record{ID: "c"})
	if n, _ := s.DeleteExpired(ctx, now); n != 1 || s.Len() != 2 {
		t.Errorf("deleted %d len %d", n, s.Len())
	}
	if err := s.Save(ctx, &Record{}); err == nil {
		t.Error("empty id accepted")
	}
	// 値は JSON 往復で DB と同じ型になる
	_ = s.Save(ctx, &Record{ID: "d", Data: map[string]any{"n": 5, "l": []string{"x"}}})
	rec, _ := s.Get(ctx, "d")
	if _, ok := rec.Data["n"].(float64); !ok {
		t.Errorf("type %T", rec.Data["n"])
	}
	if _, ok := rec.Data["l"].([]any); !ok {
		t.Errorf("type %T", rec.Data["l"])
	}
}
