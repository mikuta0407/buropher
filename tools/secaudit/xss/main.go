// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// secaudit/xss は参照 Redmine と候補 buropher の両方に同一のペイロードを格納し、
// 全ページ・全フォーマットを巡回して、候補でのみ実行可能文脈に現れる出力インジェクションを検出する。
//
//	go run ./tools/secaudit/xss -ref http://127.0.0.1:4053 -cand http://127.0.0.1:4153
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
)

func main() {
	ref := flag.String("ref", "http://127.0.0.1:4053", "参照 Redmine のベース URL")
	cand := flag.String("cand", "http://127.0.0.1:4153", "候補 buropher のベース URL")
	seedOnly := flag.Bool("seed-only", false, "格納のみ（巡回しない）")
	flag.Parse()

	reg := newRegistry()
	refSrv := &server{name: "ref", admin: newClient(*ref, "admin", "admin"), ids: map[string]string{}}
	candSrv := &server{name: "cand", admin: newClient(*cand, "admin", "admin"), ids: map[string]string{}}

	for _, s := range []*server{refSrv, candSrv} {
		if err := s.admin.login(); err != nil {
			log.Fatalf("[%s] admin login: %v", s.name, err)
		}
		seedAll(s, reg)
	}
	log.Printf("seeded %d fields", len(reg.names))
	if *seedOnly {
		return
	}

	refF := crawl(refSrv)
	candF := crawl(candSrv)
	report(reg, refF, candF)
}

// pathSpec は巡回対象の 1 URL。
type pathSpec struct {
	path string
	xhr  bool
	api  bool
}

// hit はある検出キーが現れた最初の場所。
type hit struct {
	path string
	f    finding
}

// crawl は全パスを取得し、検出キー -> 初出 hit を返す。
func crawl(s *server) map[string]hit {
	out := map[string]hit{}
	for _, p := range crawlPaths(s) {
		var r *resp
		var err error
		if p.api {
			r, err = s.admin.api("GET", p.path, nil)
		} else {
			r, err = s.admin.get(p.path, p.xhr)
		}
		if err != nil {
			log.Printf("[%s] GET %s: %v", s.name, p.path, err)
			continue
		}
		fs := analyze(r.Header.Get("Content-Type"), r.Body)
		fs = append(fs, checkHeaders(r.Header)...)
		for _, f := range fs {
			if _, ok := out[f.key()]; !ok {
				out[f.key()] = hit{p.path, f}
			}
		}
	}
	return out
}

func checkHeaders(h map[string][]string) []finding {
	var out []finding
	for name, vals := range h {
		ln := strings.ToLower(name)
		if ln != "content-disposition" && ln != "location" && ln != "link" && !strings.HasPrefix(ln, "x-redmine") {
			continue
		}
		for _, v := range vals {
			for _, id := range markerIDs(v) {
				out = append(out, finding{"header-" + ln, id, name + ": " + v})
			}
		}
	}
	return out
}

func report(reg *registry, refF, candF map[string]hit) {
	type row struct {
		h        hit
		upstream bool
	}
	var rows []row
	for k, h := range candF {
		_, up := refF[k]
		rows = append(rows, row{h, up})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].upstream != rows[j].upstream {
			return !rows[i].upstream
		}
		return rows[i].h.f.key() < rows[j].h.f.key()
	})

	fmt.Println("\n==================== FINDINGS (cand) ====================")
	fmt.Printf("%-6s %-24s %-30s %s\n", "UPSTR?", "KIND", "FIELD", "PATH / CTX")
	bugs := 0
	for _, r := range rows {
		mark := "equal"
		if !r.upstream {
			mark = "BUG"
			bugs++
		}
		fmt.Printf("%-6s %-24s %-30s %s\n", mark, r.h.f.Kind, reg.name(r.h.f.ID), r.h.path+" | "+truncN(r.h.f.Ctx, 70))
	}
	fmt.Printf("\n%d buropher-only (BUG), %d total cand findings\n", bugs, len(rows))

	// 参照のみ（cand で正しくエスケープされた／ページ未実装）も参考表示
	onlyRef := 0
	for k := range refF {
		if _, ok := candF[k]; !ok {
			onlyRef++
		}
	}
	fmt.Printf("%d findings present in ref but not cand (cand safer or page missing)\n", onlyRef)
}

func truncN(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
