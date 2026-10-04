// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

// raceUpload は POST /uploads.json で content をアップロードし、添付の id を返す。
func raceUpload(t *testing.T, base string, content []byte) int64 {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/uploads.json", strings.NewReader(string(content)))
	req.SetBasicAuth("jsmith", "jsmith")
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Error(err)
		return 0
	}
	defer res.Body.Close()
	var out struct {
		Upload struct {
			ID int64 `json:"id"`
		} `json:"upload"`
	}
	if res.StatusCode != 201 || json.NewDecoder(res.Body).Decode(&out) != nil {
		t.Errorf("upload: status %d", res.StatusCode)
	}
	return out.Upload.ID
}

// TestRaceAttachmentDedupDelete は、同じ内容のファイルのアップロード（既存ファイルの共有＝重複排除）と
// 既存の添付の削除が並行しても、新しい添付のファイルがディスクから失われないことを確認する。
// 修正前は重複排除が「既存の添付を探す → 内容を比較 → 自分の行を既存のファイルに向ける」を
// ロックなしで行っており、その間に既存の添付が削除されると（削除側は共有する行がまだ無いと見て
// ファイルを消す）、新しい添付は消えたファイルを指し、自分のファイルも消して内容を失った
// （Redmine は with_lock で既存の行をロックする）。
func TestRaceAttachmentDedupDelete(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		srv, ts := newFixtureServerOn(t, d)
		store := srv.App().AttachmentStore
		ctx := context.Background()
		content := make([]byte, 3<<20)
		lost := 0
		for i := range 25 {
			_, _ = rand.Read(content)
			existing := raceUpload(t, ts.URL, content)
			var created int64
			raceParallel(2, func(k int) {
				if k == 0 {
					raceAPI(t, ts.URL, "DELETE", fmt.Sprintf("/attachments/%d.json", existing), "jsmith", "jsmith", "")
				} else {
					created = raceUpload(t, ts.URL, content)
				}
			})
			if created == 0 {
				t.Fatalf("round %d: upload failed", i)
			}
			a, err := repository.GetAttachment(ctx, d, created)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(store.Diskfile(a)); err != nil {
				lost++
				t.Errorf("round %d: file of attachment #%d is gone: %v", i, created, err)
			}
		}
		if lost > 0 {
			t.Fatalf("%d uploads lost their file", lost)
		}
	})
}
