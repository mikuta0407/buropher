// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// 期待値（testdata/** の参照 Redmine の生の出力）の取り直し。
//
// BUROPHER_RECAPTURE_REF に参照 Redmine の URL（例: 専用インスタンス http://127.0.0.1:42095）を設定すると、
// http.DefaultTransport を使うクライアントはテストサーバ宛てのリクエストを参照へ転送し（応答の参照 URL はテストサーバの URL に
// 書き換える）、比較ヘルパーは一致しなかった期待値ファイルを参照の出力で書き換える。
// 書き込みを伴うテストもあるため専用の参照インスタンスを使い、テストごとに reset すること:
//
//	COMPAT_REF_DIR=... COMPAT_REF_PORT=42095 tools/compat/redmine-ref.sh reset
//	BUROPHER_RECAPTURE_REF=http://127.0.0.1:42095 go test -p 1 -parallel 1 -run '^TestAdminMastersPagesMatchRedmine$' ./internal/server
//
// テスト側で DB を直接書き換えてから取得するページは参照に反映されないため、取り直した差分は必ず目で確認する。
// 環境変数付きの専用の取り直し（BUROPHER_*_GOLDEN_REF）を持つテストはそちらを使う。
var recaptureRef = strings.TrimSuffix(os.Getenv("BUROPHER_RECAPTURE_REF"), "/")

var (
	recaptureMu   sync.Mutex
	recaptureFrom string // 転送元（現在のテストサーバの URL）
)

// setRecaptureFrom はテストサーバの URL を転送元として登録する。
func setRecaptureFrom(base string) {
	if recaptureRef == "" {
		return
	}
	recaptureMu.Lock()
	recaptureFrom = base
	recaptureMu.Unlock()
}

type recaptureTransport struct{}

func (recaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	recaptureMu.Lock()
	from := recaptureFrom
	recaptureMu.Unlock()
	fu, _ := url.Parse(from)
	ru, _ := url.Parse(recaptureRef)
	if fu == nil || ru == nil || req.URL.Host != fu.Host {
		return recaptureOrigTransport.RoundTrip(req)
	}
	r2 := req.Clone(req.Context())
	r2.URL.Scheme, r2.URL.Host, r2.Host = ru.Scheme, ru.Host, ru.Host
	if o := r2.Header.Get("Origin"); o != "" {
		r2.Header.Set("Origin", strings.ReplaceAll(o, fu.Host, ru.Host))
	}
	if rf := r2.Header.Get("Referer"); rf != "" {
		r2.Header.Set("Referer", strings.ReplaceAll(rf, fu.Host, ru.Host))
	}
	res, err := recaptureOrigTransport.RoundTrip(r2)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, err
	}
	b = bytes.ReplaceAll(b, []byte(ru.Host), []byte(fu.Host))
	res.Body = io.NopCloser(bytes.NewReader(b))
	res.ContentLength = int64(len(b))
	res.Header.Set("Content-Length", strconv.Itoa(len(b)))
	if l := res.Header.Get("Location"); l != "" {
		res.Header.Set("Location", strings.ReplaceAll(l, ru.Host, fu.Host))
	}
	res.Request = req
	return res, nil
}

// recaptureOrigTransport は差し替え前の http.DefaultTransport。
var recaptureOrigTransport = http.DefaultTransport

// 取り直しのときは http.DefaultTransport を差し替える（newClient・http.DefaultClient・apiCall はすべてこれを使う）。
func init() {
	if recaptureRef != "" {
		http.DefaultTransport = recaptureTransport{}
	}
}

// recaptureGolden は取り直しのとき、参照の出力 got（テストサーバの URL base を含む）を
// 期待値ファイル path に書き出して true を返す。ベース URL は {{BASE}}、ホストは 127.0.0.1:3998 にする
// （従来どおり共用の参照から取得した形）。
func recaptureGolden(t *testing.T, path, got, base string) bool {
	t.Helper()
	if recaptureRef == "" {
		return false
	}
	s := got
	if base != "" {
		s = strings.ReplaceAll(s, base, "{{BASE}}")
		s = strings.ReplaceAll(s, strings.TrimPrefix(base, "http://"), "127.0.0.1:3998")
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recaptured %s", path)
	return true
}

// recaptureBase は現在の転送元の URL（取り直しでないときは空）。
func recaptureBase() string {
	recaptureMu.Lock()
	defer recaptureMu.Unlock()
	if recaptureRef == "" {
		return ""
	}
	return recaptureFrom
}
