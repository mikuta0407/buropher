package server_test

// projects / memberships / issue_categories / versions / trackers / issue_statuses / enumerations / roles /
// custom_fields の API テスト（api_*_test.go）で使うヘルパー（接頭辞 masters）。

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// mastersExec は前提データを SQL で直接変更する（Redmine のテストの update_attribute 等）。
func mastersExec(t *testing.T, d *db.DB, q string, args ...any) int64 {
	t.Helper()
	res, err := d.Exec(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	n, _ := res.RowsAffected()
	return n
}

// mastersInt は 1 行 1 列の整数（COUNT 等）を返す。
func mastersInt(t *testing.T, d *db.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// mastersStr は 1 行 1 列の文字列（NULL は ""）を返す。
func mastersStr(t *testing.T, d *db.DB, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := d.Get(context.Background(), &s, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return ""
	}
	return *s
}

// mastersForm は Rails の結合テストの post / put(path, :params => {...}) と同じく、本文を
// application/x-www-form-urlencoded で送る（form はエンコード済みのクエリ文字列）。
func mastersForm(t *testing.T, ts *httptest.Server, method, path, form string, opts ...apiOpt) apiResp {
	t.Helper()
	return apiCall(t, ts, method, path, "application/x-www-form-urlencoded", form, opts...)
}

// mastersNoContent は 204 と空の本文（render_api_ok）を確認する。
func mastersNoContent(t *testing.T, r apiResp) {
	t.Helper()
	r.expectStatus(t, 204)
	if r.Body != "" {
		t.Errorf("body = %q, want empty", r.Body)
	}
}

// mastersMedia は Content-Type の MIME 部分を確認する。
func mastersMedia(t *testing.T, r apiResp, want string) {
	t.Helper()
	if got := r.ContentType(); got != want {
		t.Errorf("media type = %q, want %q", got, want)
	}
}
