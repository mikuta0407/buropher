// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// registry はフィールド名 → ペイロード番号の対応。両サーバで同じ順に呼ぶので番号は一致する。
type registry struct {
	ids   map[string]int
	names []string
}

func newRegistry() *registry { return &registry{ids: map[string]int{}} }

func (r *registry) id(field string) int {
	if n, ok := r.ids[field]; ok {
		return n
	}
	n := len(r.names) + 100
	r.ids[field] = n
	r.names = append(r.names, field)
	return n
}

func (r *registry) name(id int) string {
	if i := id - 100; i >= 0 && i < len(r.names) {
		return r.names[i]
	}
	return "?"
}

// short は 30 文字以下の欄用（無害なマーカー関数 xss(N) を呼ぶだけ）。
func (r *registry) short(f string) string { return fmt.Sprintf(`"'><svg onload=xss(%d)>`, r.id(f)) }

// long は HTML・属性・JS 文字列の各文脈を一度に試す欄用。
func (r *registry) long(f string) string {
	n := r.id(f)
	return fmt.Sprintf("L%d\"'><img src=x onerror=xss(%d)>'-xss(%d)-'\"-xss(%d)-\"</script><script>xss(%d)</script> \\", n, n, n, n, n)
}

func (r *registry) url(f string) string { return fmt.Sprintf("javascript:xss(%d)//", r.id(f)) }

// text は整形テキスト欄用（textile / CommonMark 双方のリンク記法・生 HTML・マクロを含む）。
func (r *registry) text(f string) string {
	n := r.id(f)
	return r.long(f) + fmt.Sprintf(`

"t%[1]d":javascript:xss(%[1]d) [m%[1]d](javascript:xss(%[1]d)) <javascript:xss(%[1]d)>

!x.png" onerror="xss(%[1]d)!  ![i](x" onerror="xss(%[1]d))

<a href="javascript:xss(%[1]d)">raw</a> <img src=x onerror=xss(%[1]d)> <div style="x:xss(%[1]d)">s</div>

[[Wiki"><img src=x onerror=xss(%[1]d)>]] {{include("><img src=x onerror=xss(%[1]d)>)}}

{{collapse("><img src=x onerror=xss(%[1]d)>)
body
}}

@code"><img src=x onerror=xss(%[1]d)>@ ==<img src=x onerror=xss(%[1]d)>==
`, n)
}

// server は 1 サーバ分の状態（admin セッションと採番された ID）。
type server struct {
	name  string
	admin *client
	ids   map[string]string
	errs  []string
}

func (s *server) errf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	s.errs = append(s.errs, msg)
	log.Printf("[%s] %s", s.name, msg)
}

func (s *server) apiJSON(method, path string, body any, idKey, saveAs string) map[string]any {
	r, err := s.admin.api(method, path, body)
	if err != nil {
		s.errf("%s %s: %v", method, path, err)
		return nil
	}
	if r.Status >= 300 {
		s.errf("%s %s: %d %s", method, path, r.Status, trunc(r.Body))
		return nil
	}
	var v map[string]any
	_ = json.Unmarshal(r.Body, &v)
	if idKey != "" && v != nil {
		if o, ok := v[idKey].(map[string]any); ok {
			if id, ok := o["id"].(float64); ok {
				s.ids[saveAs] = strconv.Itoa(int(id))
			}
		}
	}
	return v
}

var reLocID = regexp.MustCompile(`/(\d+)(?:/[a-z_]+)?(?:\?.*)?$`)

// form はフォーム送信し、302 の Location から ID を拾う。
func (s *server) form(path string, f url.Values, saveAs string) string {
	r, err := s.admin.post(path, f, false)
	if err != nil {
		s.errf("POST %s: %v", path, err)
		return ""
	}
	loc := r.Header.Get("Location")
	if r.Status/100 != 3 {
		s.errf("POST %s: %d (expected redirect) %s", path, r.Status, errorExplanation(r.Body))
		return ""
	}
	id := ""
	if m := reLocID.FindStringSubmatch(loc); m != nil {
		id = m[1]
	}
	if saveAs != "" && id != "" {
		s.ids[saveAs] = id
	}
	return loc
}

var reErr = regexp.MustCompile(`(?s)<div id="errorExplanation">(.*?)</div>`)

func errorExplanation(b []byte) string {
	if m := reErr.FindSubmatch(b); m != nil {
		return strings.Join(strings.Fields(string(m[1])), " ")
	}
	return ""
}

func trunc(b []byte) string {
	if len(b) > 300 {
		return string(b[:300])
	}
	return string(b)
}

// maxIDFrom は一覧ページから指定パスの最大 ID を返す（Location に ID が出ない作成操作用）。
func (s *server) maxIDFrom(listPath, re string) string {
	r, err := s.admin.get(listPath, false)
	if err != nil {
		return ""
	}
	best := 0
	for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(string(r.Body), -1) {
		if n, _ := strconv.Atoi(m[1]); n > best {
			best = n
		}
	}
	if best == 0 {
		return ""
	}
	return strconv.Itoa(best)
}
