// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/server"
)

// server.max_request_body_mb を超える multipart 本文は 413 になり、上限内なら通常どおり処理される。
func TestMaxRequestBody(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	if err := bootstrap.Init(ctx, d, bootstrap.Options{AdminPassword: "admin"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Server.MaxRequestBodyMB = 1
	srv, err := server.New(cfg, d, server.Options{TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	post := func(size int) int {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		fw, _ := w.CreateFormFile("attachments[1][file]", "big.bin")
		fw.Write([]byte(strings.Repeat("x", size)))
		w.Close()
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/login", &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := post(2 << 20); got != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: status = %d, want 413", got)
	}
	if got := post(1 << 10); got == http.StatusRequestEntityTooLarge {
		t.Errorf("small body rejected with 413")
	}
}
