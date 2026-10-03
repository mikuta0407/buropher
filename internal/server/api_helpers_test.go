// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// REST API テスト（api_*_test.go。Redmine の test/integration/api_test/*.rb の移植）の共通ヘルパー。
// 各リソースのテストで追加するヘルパーは名前の衝突を避けるためリソース名の接頭辞を付けること。

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// apiOpt は apiCall のリクエストの設定。
type apiOpt func(r *http.Request)

// apiBasic は HTTP Basic 認証（credentials(login, password)）。
func apiBasic(login, password string) apiOpt {
	return func(r *http.Request) { r.SetBasicAuth(login, password) }
}

// apiCreds は Redmine のテストの credentials(login) と同じく login = password の Basic 認証（admin は "admin"）。
func apiCreds(login string) apiOpt {
	pw := login
	switch login {
	case "dlopper", "rhill", "someone", "miscuser8":
		pw = "foo"
	}
	return apiBasic(login, pw)
}

// apiKeyHeader は X-Redmine-API-Key ヘッダ。
func apiKeyHeader(key string) apiOpt {
	return func(r *http.Request) { r.Header.Set("X-Redmine-API-Key", key) }
}

// apiHeader は任意のヘッダ。
func apiHeader(k, v string) apiOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

// apiResp は API のレスポンス。
type apiResp struct {
	Status int
	Header http.Header
	Body   string
}

// apiCall は ts に method path を送る。body が空でなければ Content-Type に ctype を付ける
// （ctype が空なら path の拡張子から application/json / application/xml を選ぶ）。
func apiCall(t *testing.T, ts *httptest.Server, method, path, ctype, body string, opts ...apiOpt) apiResp {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if ctype == "" {
			ctype = "application/json"
			if p, _, _ := strings.Cut(path, "?"); strings.HasSuffix(p, ".xml") {
				ctype = "application/xml"
			}
		}
		req.Header.Set("Content-Type", ctype)
	}
	for _, o := range opts {
		o(req)
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return apiResp{Status: res.StatusCode, Header: res.Header, Body: string(b)}
}

// apiGet は GET（本文なし）。
func apiGet(t *testing.T, ts *httptest.Server, path string, opts ...apiOpt) apiResp {
	t.Helper()
	return apiCall(t, ts, http.MethodGet, path, "", "", opts...)
}

// ContentType は Content-Type の MIME 部分（"; charset=..." を除く）。
func (r apiResp) ContentType() string {
	ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	return strings.TrimSpace(ct)
}

// JSON は本文を JSON として読む（キー順は保持しない。順序の確認は Body を直接比較する）。
func (r apiResp) JSON(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(r.Body))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		t.Fatalf("invalid JSON (%v): %q", err, r.Body)
	}
	return m
}

// XML は本文を XML として読み、cascadia で検索できる html.Node の木にする（assert_select 相当）。
func (r apiResp) XML(t *testing.T) *html.Node {
	t.Helper()
	return parseXMLNode(t, r.Body)
}

// expectStatus はステータスを確認する（不一致なら本文も表示して即失敗）。
func (r apiResp) expectStatus(t *testing.T, want int) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status = %d, want %d; body: %.500s", r.Status, want, r.Body)
	}
}

// parseXMLNode は XML を html.Node の木に変換する（要素名・属性・テキストをそのまま保持）。
func parseXMLNode(t *testing.T, s string) *html.Node {
	t.Helper()
	root := &html.Node{Type: html.DocumentNode}
	cur := root
	d := xml.NewDecoder(strings.NewReader(s))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid XML (%v): %q", err, s)
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			n := &html.Node{Type: html.ElementNode, Data: tk.Name.Local}
			for _, a := range tk.Attr {
				n.Attr = append(n.Attr, html.Attribute{Key: a.Name.Local, Val: a.Value})
			}
			cur.AppendChild(n)
			cur = n
		case xml.EndElement:
			cur = cur.Parent
		case xml.CharData:
			if strings.TrimSpace(string(tk)) != "" || cur.FirstChild == nil {
				cur.AppendChild(&html.Node{Type: html.TextNode, Data: string(tk)})
			}
		}
	}
	return root
}

// xmlSelect は CSS セレクタに一致する要素を返す。
func xmlSelect(t *testing.T, n *html.Node, sel string) []*html.Node {
	t.Helper()
	s, err := cascadia.ParseGroup(sel)
	if err != nil {
		t.Fatalf("selector %q: %v", sel, err)
	}
	return cascadia.QueryAll(n, s)
}

// nodeText は repositories_git_test.go で定義（子孫のテキストを連結）。

// assertXMLCount は assert_select sel, count。
func assertXMLCount(t *testing.T, n *html.Node, sel string, want int) {
	t.Helper()
	if got := len(xmlSelect(t, n, sel)); got != want {
		t.Errorf("assert_select %q: %d elements, want %d", sel, got, want)
	}
}

// assertXMLText は assert_select sel, text: want（最初に一致した要素のテキスト）。
func assertXMLText(t *testing.T, n *html.Node, sel, want string) {
	t.Helper()
	nodes := xmlSelect(t, n, sel)
	if len(nodes) == 0 {
		t.Errorf("assert_select %q: no element (want text %q)", sel, want)
		return
	}
	if got := nodeText(nodes[0]); got != want {
		t.Errorf("assert_select %q: text %q, want %q", sel, got, want)
	}
}

// jsonPath は JSON の値を "issue.custom_fields.0.value" のようなドット区切りの経路で取り出す（無ければ nil, false）。
func jsonPath(v any, path string) (any, bool) {
	for _, k := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			if v, ok = x[k]; !ok {
				return nil, false
			}
		case []any:
			i := 0
			for _, c := range k {
				if c < '0' || c > '9' {
					return nil, false
				}
				i = i*10 + int(c-'0')
			}
			if i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// assertJSON は jsonPath の値の文字列表現（json.Number は元の表記、nil は "null"）が want と一致することを確認する。
func assertJSON(t *testing.T, m map[string]any, path, want string) {
	t.Helper()
	v, ok := jsonPath(m, path)
	if !ok {
		t.Errorf("json %s: missing (want %s)", path, want)
		return
	}
	var got string
	switch x := v.(type) {
	case nil:
		got = "null"
	case string:
		got = x
	case json.Number:
		got = x.String()
	case bool:
		if x {
			got = "true"
		} else {
			got = "false"
		}
	default:
		b, _ := json.Marshal(x)
		got = string(b)
	}
	if got != want {
		t.Errorf("json %s = %s, want %s", path, got, want)
	}
}
