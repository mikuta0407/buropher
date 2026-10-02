package httpx

import (
	"context"
	"net/http"
	"strings"
)

// overrideMethods は Rack::MethodOverride::HTTP_METHODS。
var overrideMethods = map[string]bool{
	"GET": true, "HEAD": true, "PUT": true, "POST": true, "DELETE": true,
	"OPTIONS": true, "PATCH": true, "LINK": true, "UNLINK": true,
}

// MethodOverride は Rack::MethodOverride の移植。
// POST リクエストのボディの _method（フォーム系 Content-Type の場合のみ）または
// X-HTTP-Method-Override ヘッダの値を大文字化し、許可メソッドなら r.Method を置き換える。
// ボディを参照するため ParamsMiddleware の後に置くこと（未実行ならヘッダのみ見る）。
// 元のメソッドは OriginalMethod で取得できる。
func MethodOverride(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			m := strings.ToUpper(methodOverrideValue(r))
			if overrideMethods[m] {
				ctx := context.WithValue(r.Context(), ctxOrigMethod, r.Method)
				r = r.WithContext(ctx)
				r.Method = m
			}
		}
		next.ServeHTTP(w, r)
	})
}

func methodOverrideValue(r *http.Request) string {
	mt := MediaType(r)
	// req.form_data? || req.parseable_data?
	formData := mt == "" || mt == "application/x-www-form-urlencoded" || mt == "multipart/form-data" ||
		mt == "multipart/related" || mt == "multipart/mixed"
	if formData {
		if v, ok := BodyParams(r).Get("_method"); ok && v != nil {
			return ValueString(v)
		}
	}
	return r.Header.Get("X-HTTP-Method-Override")
}

// OriginalMethod は MethodOverride 前のメソッドを返す（上書きされていなければ r.Method）。
func OriginalMethod(r *http.Request) string {
	if m, ok := r.Context().Value(ctxOrigMethod).(string); ok {
		return m
	}
	return r.Method
}
