package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/tools/compat/internal/scenario"
)

// fakeRedmine は Redmine のログインフロー（CSRF + セッション Cookie）を模したサーバ。
func fakeRedmine() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "_s", Value: "pre"})
		fmt.Fprint(w, `<html><body><form action="/login" method="post"><input type="hidden" name="authenticity_token" value="TOK"></form></body></html>`)
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("_s")
		if err != nil || c.Value != "pre" || r.FormValue("authenticity_token") != "TOK" {
			http.Error(w, "bad csrf", 422)
			return
		}
		if r.FormValue("username") != "admin" || r.FormValue("password") != "admin" {
			fmt.Fprint(w, "invalid")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "_s", Value: "user-admin"})
		http.Redirect(w, r, "/my/page", http.StatusFound)
	})
	mux.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok {
			fmt.Fprintf(w, "basic:%s:%s", u, p)
			return
		}
		c, err := r.Cookie("_s")
		if err != nil || c.Value != "user-admin" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		fmt.Fprint(w, "session:admin")
	})
	return httptest.NewServer(mux)
}

func TestLoginAndFetch(t *testing.T) {
	srv := fakeRedmine()
	defer srv.Close()
	tg, err := New(srv.URL+"/", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	admin := scenario.Credential{Login: "admin", Password: "admin"}

	resp, err := tg.Do(scenario.Case{User: "admin", Cred: admin, Method: "GET", Path: "/whoami", Auth: "session"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "session:admin" {
		t.Errorf("session body = %q", resp.Body)
	}

	resp, err = tg.Do(scenario.Case{User: "admin", Cred: admin, Method: "GET", Path: "/whoami", Auth: "basic"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "basic:admin:admin" {
		t.Errorf("basic body = %q", resp.Body)
	}

	resp, err = tg.Do(scenario.Case{User: scenario.Anonymous, Method: "GET", Path: "/whoami", Auth: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusFound || resp.Location != "/login" {
		t.Errorf("anonymous should get redirect, got %d %q", resp.Status, resp.Location)
	}

	_, err = tg.Do(scenario.Case{User: "bad", Cred: scenario.Credential{Login: "bad", Password: "x"}, Method: "GET", Path: "/whoami", Auth: "session"})
	if err == nil {
		t.Error("expected login failure")
	}
}
