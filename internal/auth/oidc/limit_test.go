// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package oidc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 巨大なディスカバリ文書を返すプロバイダでも、上限を超えて読み込まずにエラーになる。
func TestDiscoveryResponseSizeLimited(t *testing.T) {
	var written int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"issuer":"`)
		chunk := strings.Repeat("a", 64<<10)
		for range 512 { // 32MB
			n, err := io.WriteString(w, chunk)
			written += int64(n)
			if err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, `"}`)
	}))
	defer srv.Close()
	_, err := New(context.Background(), nil, Config{Issuer: srv.URL, ClientID: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if written >= 32<<20 {
		t.Fatalf("whole body was consumed (%d bytes)", written)
	}
}

func TestLimitedBodyExactAndOver(t *testing.T) {
	b := &limitedBody{rc: io.NopCloser(strings.NewReader("abcd")), n: 4}
	got, err := io.ReadAll(b)
	if err != nil || string(got) != "abcd" {
		t.Fatalf("exact: %q %v", got, err)
	}
	b = &limitedBody{rc: io.NopCloser(strings.NewReader("abcde")), n: 4}
	if _, err := io.ReadAll(b); !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("over: %v", err)
	}
}
