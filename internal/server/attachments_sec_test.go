// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// uploadAPI は POST /uploads.json で添付を作り、その id を返す。
func uploadAPI(t *testing.T, base, query, body string) int64 {
	t.Helper()
	res, b := upload(t, newClient(t), base+"/uploads.json?"+query, "application/octet-stream", body, map[string]string{"basic": "jsmith"})
	if res.StatusCode != 201 {
		t.Fatalf("upload %s: %d %s", query, res.StatusCode, b)
	}
	var v struct {
		Upload struct {
			ID int64 `json:"id"`
		} `json:"upload"`
	}
	if err := json.Unmarshal([]byte(b), &v); err != nil {
		t.Fatal(err)
	}
	return v.Upload.ID
}

// TestAttachmentDownloadSpoofedPDFType は、拡張子が .pdf でも Content-Type を text/html などに偽装した添付を
// inline で返さないことを確かめる（inline で返すとブラウザが HTML として描画する）。
func TestAttachmentDownloadSpoofedPDFType(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "jsmith", "jsmith")

	spoof := uploadAPI(t, ts.URL, "filename=evil.pdf&content_type=text/html", "<h1>phish</h1>")
	res, _ := get(t, c, fmt.Sprintf("%s/attachments/download/%d", ts.URL, spoof))
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("spoofed pdf served with Content-Disposition %q (Content-Type %q)", cd, res.Header.Get("Content-Type"))
	}

	// 本物の PDF（Content-Type が application/pdf または未指定）は従来どおり inline
	for _, q := range []string{"filename=ok.pdf&content_type=application/pdf", "filename=ok2.pdf"} {
		id := uploadAPI(t, ts.URL, q, "%PDF-1.4")
		res, _ := get(t, c, fmt.Sprintf("%s/attachments/download/%d", ts.URL, id))
		if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") || res.Header.Get("Content-Type") != "application/pdf" {
			t.Errorf("%s: Content-Disposition %q Content-Type %q", q, cd, res.Header.Get("Content-Type"))
		}
	}
}

// TestAttachmentsDownloadAllEntryNames は download_all の ZIP のエントリ名が展開先の外を指さないことを確かめる
// （取り込み等で filename に "/" や ".." が入っていても zip slip にならない）。
func TestAttachmentsDownloadAllEntryNames(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	names := map[int64]string{}
	for i, fn := range []string{"../../evil.sh", `..\..\win.bat`, "..", "normal.txt", "normal.txt"} {
		id := uploadAPI(t, ts.URL, fmt.Sprintf("filename=f%d.txt", i), fmt.Sprintf("body%d", i))
		if _, err := d.Exec(ctx, `UPDATE attachments SET filename = ?, container_kind = 'issue', container_id = 1 WHERE id = ?`, fn, id); err != nil {
			t.Fatal(err)
		}
		names[id] = fn
	}
	res, body := get(t, login(t, ts, "jsmith", "jsmith"), ts.URL+"/attachments/issues/1/download")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	zr, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != len(names) {
		t.Errorf("entries %d, want %d", len(zr.File), len(names))
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		if strings.ContainsAny(f.Name, `/\`) || f.Name == ".." || f.Name == "." || f.Name == "" {
			t.Errorf("unsafe zip entry name %q", f.Name)
		}
		if seen[f.Name] {
			t.Errorf("duplicate entry %q", f.Name)
		}
		seen[f.Name] = true
	}
	if !seen["normal.txt"] || !seen["normal(1).txt"] {
		t.Errorf("entries %v", seen)
	}
}
