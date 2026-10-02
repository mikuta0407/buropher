package httpx_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/httpx"
)

// Example は internal/server での組み込み例（推奨ミドルウェア順）。
func Example() {
	sessions := &httpx.SessionManager{
		Store:  httpx.NewMemoryStore(),
		Secret: []byte("secret_key from config"),
		// Policy: func() httpx.ExpiryPolicy { return httpx.PolicyFromMinutes(Setting.session_lifetime, Setting.session_timeout) },
	}
	errs := &httpx.ErrorRenderer{} // Page: テンプレート実装（common/error）を後で差し込む

	r := chi.NewRouter()
	// ルーティング前（トップレベル）
	r.Use(
		httpx.RequestIDMiddleware,
		httpx.RemoteIPMiddleware(nil),
		httpx.ParamsMiddleware(&httpx.ParseOptions{TempDir: "" /* data/tmp 推奨 */}, nil),
		httpx.MethodOverride,
		sessions.Middleware,
	)
	// ルーティング後（インライン）: format が確定してから CSRF 検証する
	r.Group(func(r chi.Router) {
		r.Use(httpx.CSRFMiddleware(httpx.CSRFOptions{
			OnFailure: httpx.RedmineCSRFFailure(httpx.RedmineCSRFFailureOptions{Errors: errs}),
		}))
		httpx.Route(r, "GET", "/issues", func(w http.ResponseWriter, r *http.Request) {
			switch httpx.Negotiate(r, "html", "json", "xml", "atom", "csv", "pdf") {
			case "json":
				fmt.Fprint(w, `{"issues":[]}`)
			case "html":
				fmt.Fprint(w, httpx.CSRFMetaTags(r) != "")
			default:
				httpx.Head(w, r, http.StatusNotAcceptable)
			}
		})
		httpx.Route(r, "PATCH", "/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
			p := httpx.ParamsOf(r)
			if httpx.IsAPIRequest(r) {
				errs.RenderAPIErrors(w, r, "Subject cannot be blank")
				return
			}
			httpx.FlashOf(r).SetNotice("Successful update.")
			httpx.RedirectBackOrDefault(w, r, "/issues/"+p.String("id"), httpx.BackURLOptions{})
		})
	})

	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/issues.json", ""},
		{"GET", "/issues", ""},
		{"PATCH", "/issues/1.xml", ""},
		{"POST", "/issues/1", "_method=patch&issue[subject]=x"}, // CSRF トークンなし
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		line := strings.SplitN(w.Body.String(), "\n", 2)[0]
		fmt.Println(w.Code, line[:min(len(line), 38)])
	}
	// Output:
	// 200 {"issues":[]}
	// 200 true
	// 422 <?xml version="1.0" encoding="UTF-8"?>
	// 422 <!DOCTYPE html>
}
