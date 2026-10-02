package httpx

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func parseReq(t *testing.T, req *http.Request, opts *ParseOptions) *RequestParams {
	t.Helper()
	rp, err := ParseRequest(req, opts)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	return rp
}

func TestParseURLEncodedBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/issues?x=q", strings.NewReader("issue%5Bsubject%5D=Hi&issue[watcher_user_ids][]=1&issue[watcher_user_ids][]&x=b\x00"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	rp := parseReq(t, req, nil)
	if got := mustJSON(t, rp.Body); got != `{"issue":{"subject":"Hi","watcher_user_ids":["1"]},"x":"b"}` {
		t.Errorf("body: %s", got)
	}
	if got := mustJSON(t, rp.Query); got != `{"x":"q"}` {
		t.Errorf("query: %s", got)
	}
	// Content-Type なしの POST も urlencoded として解析（Rack の form_data?）
	req = httptest.NewRequest("POST", "/", strings.NewReader("a=1"))
	if got := mustJSON(t, parseReq(t, req, nil).Body); got != `{"a":"1"}` {
		t.Errorf("no content-type POST: %s", got)
	}
	// Content-Type なしの PUT は解析しない
	req = httptest.NewRequest("PUT", "/", strings.NewReader("a=1"))
	if got := mustJSON(t, parseReq(t, req, nil).Body); got != `{}` {
		t.Errorf("no content-type PUT: %s", got)
	}
	// octet-stream はボディを読まない
	req = httptest.NewRequest("POST", "/uploads.json", strings.NewReader("binary"))
	req.Header.Set("Content-Type", "application/octet-stream")
	parseReq(t, req, nil)
	if b, _ := io.ReadAll(req.Body); string(b) != "binary" {
		t.Errorf("octet-stream body consumed: %q", b)
	}
}

func TestParseJSONBody(t *testing.T) {
	cases := []struct{ ct, body, want string }{
		{"application/json", `{"issue":{"subject":"S","project_id":1,"estimated_hours":1.5,"is_private":false,"watcher_user_ids":[1,null,2],"x":null}}`,
			`{"issue":{"subject":"S","project_id":1,"estimated_hours":1.5,"is_private":false,"watcher_user_ids":[1,2],"x":null}}`},
		{"application/json; charset=utf-8", `[1,2]`, `{"_json":[1,2]}`},
		{"text/x-json", `"str"`, `{"_json":"str"}`},
		{"application/json", `{"b":1,"a":2,"b":3}`, `{"b":3,"a":2}`},
		{"application/json", `{"n":12345678901234567890}`, `{"n":"12345678901234567890"}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/issues.json", strings.NewReader(c.body))
		req.Header.Set("Content-Type", c.ct)
		if got := mustJSON(t, parseReq(t, req, nil).Body); got != c.want {
			t.Errorf("%s: got %s want %s", c.body, got, c.want)
		}
	}
	for _, bad := range []string{`{"a":`, `{"a":1} x`, strings.Repeat("[", 101) + strings.Repeat("]", 101)} {
		req := httptest.NewRequest("POST", "/issues.json", strings.NewReader(bad))
		req.Header.Set("Content-Type", "application/json")
		_, err := ParseRequest(req, nil)
		if pe, ok := err.(*ParamError); !ok || pe.Status() != 400 {
			t.Errorf("%q: expected 400 ParamError, got %v", bad, err)
		}
	}
	// 空ボディは {}
	req := httptest.NewRequest("POST", "/issues.json", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	if got := mustJSON(t, parseReq(t, req, nil).Body); got != `{}` {
		t.Errorf("empty json: %s", got)
	}
	// 上限超過は 413
	req = httptest.NewRequest("POST", "/issues.json", strings.NewReader(`{"a":"`+strings.Repeat("x", 100)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	_, err := ParseRequest(req, &ParseOptions{MaxBodyBytes: 50})
	if pe, ok := err.(*ParamError); !ok || pe.Status() != 413 {
		t.Errorf("too large: %v", err)
	}
}

// 期待値は ActiveSupport 7.2.3 の Hash.from_xml の実行結果。
func TestParseXMLBody(t *testing.T) {
	cases := []struct{ xml, want string }{
		{`<issue><subject>Hello</subject><project_id>1</project_id></issue>`, `{"issue":{"subject":"Hello","project_id":"1"}}`},
		{`<issue><subject/><description></description><notes> </notes></issue>`, `{"issue":{"subject":null,"description":null,"notes":" "}}`},
		{`<issue><watcher_user_ids type="array"><watcher_user_id>3</watcher_user_id></watcher_user_ids></issue>`, `{"issue":{"watcher_user_ids":["3"]}}`},
		{`<issue><watcher_user_ids type="array"><watcher_user_id>3</watcher_user_id><watcher_user_id>4</watcher_user_id></watcher_user_ids></issue>`, `{"issue":{"watcher_user_ids":["3","4"]}}`},
		{`<issue><custom_fields type="array"><custom_field id="1"><value>x</value></custom_field><custom_field id="2"><value type="array"><value>a</value><value>b</value></value></custom_field></custom_fields></issue>`,
			`{"issue":{"custom_fields":[{"id":"1","value":"x"},{"id":"2","value":["a","b"]}]}}`},
		{`<issue><uploads type="array"></uploads><n type="integer">12</n><b type="boolean">true</b><s type="string"></s><z nil="true"></z></issue>`,
			`{"issue":{"uploads":[],"n":12,"b":true,"s":"","z":null}}`},
		{`<user id="5"><login>jsmith</login></user>`, `{"user":{"id":"5","login":"jsmith"}}`},
		{`<a><b>1</b><b>2</b><c>x<d>y</d></c></a>`, `{"a":{"b":["1","2"],"c":"x"}}`},
		{"<?xml version=\"1.0\"?>\n<a>\n  <b>1</b>\n</a>", `{"a":{"b":"1"}}`},
		{`<a></a>`, `{"a":null}`},
		{`<a>text &amp; more</a>`, `{"a":"text \u0026 more"}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/issues.xml", strings.NewReader(c.xml))
		req.Header.Set("Content-Type", "application/xml")
		if got := mustJSON(t, parseReq(t, req, nil).Body); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.xml, got, c.want)
		}
	}
	for _, bad := range []string{`<a>`, `<a type="yaml">x</a>`, `<a type="symbol">x</a>`, `<a/><b/>`} {
		req := httptest.NewRequest("POST", "/issues.xml", strings.NewReader(bad))
		req.Header.Set("Content-Type", "text/xml")
		if _, err := ParseRequest(req, nil); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func buildMultipart(t *testing.T, fn func(mw *multipart.Writer)) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fn(mw)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func filePart(t *testing.T, mw *multipart.Writer, name, filename, ct, data string) {
	t.Helper()
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="`+name+`"; filename="`+filename+`"`)
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	w, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(data))
}

func TestParseMultipartBody(t *testing.T) {
	big := strings.Repeat("z", 3000)
	body, ct := buildMultipart(t, func(mw *multipart.Writer) {
		_ = mw.WriteField("_method", "patch")
		_ = mw.WriteField("issue[subject]", "日本語")
		_ = mw.WriteField("issue[watcher_user_ids][]", "1")
		_ = mw.WriteField("issue[watcher_user_ids][]", "2")
		filePart(t, mw, "attachments[1][file]", `C:\Users\me\report.txt`, "text/plain", "hello")
		filePart(t, mw, "attachments[2][file]", "big%20file.bin", "application/octet-stream", big)
		filePart(t, mw, "attachments[3][file]", "", "application/octet-stream", "")
		// name なしのパート
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", "form-data")
		w, _ := mw.CreatePart(h)
		_, _ = w.Write([]byte("anon"))
	})
	tmp := t.TempDir()
	req := httptest.NewRequest("POST", "/issues/1", body)
	req.Header.Set("Content-Type", ct)
	rp := parseReq(t, req, &ParseOptions{MaxMemoryPerFile: 1024, TempDir: tmp})
	p := rp.Body
	if p.String("issue", "subject") != "日本語" || mustJSON(t, p.Strings("issue", "watcher_user_ids")) != `["1","2"]` {
		t.Errorf("fields: %s", mustJSON(t, p))
	}
	f1 := p.File("attachments", "1", "file")
	if f1 == nil || f1.Filename != "report.txt" || f1.ContentType != "text/plain" || f1.Size != 5 || f1.TempPath() != "" {
		t.Fatalf("file1: %+v", f1)
	}
	rc, _ := f1.Open()
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" {
		t.Errorf("file1 content %q", b)
	}
	f2 := p.File("attachments", "2", "file")
	if f2 == nil || f2.Filename != "big file.bin" || f2.Size != 3000 || f2.TempPath() == "" {
		t.Fatalf("file2: %+v", f2)
	}
	if p.Has("attachments", "3") {
		t.Errorf("empty filename part should be skipped: %s", mustJSON(t, p))
	}
	if p.String("text/plain", "0") != "anon" {
		t.Errorf("nameless part: %s", mustJSON(t, p))
	}
	path := f2.TempPath()
	rp.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("temp file not removed")
	}
}

func TestMultipartLimits(t *testing.T) {
	body, ct := buildMultipart(t, func(mw *multipart.Writer) {
		for i := 0; i < 3; i++ {
			filePart(t, mw, "f[]", "a.txt", "", "x")
		}
	})
	req := httptest.NewRequest("POST", "/", body)
	req.Header.Set("Content-Type", ct)
	_, err := ParseRequest(req, &ParseOptions{MultipartFileLimit: 2})
	if pe, ok := err.(*ParamError); !ok || pe.Status() != 413 {
		t.Errorf("file limit: %v", err)
	}
	req = httptest.NewRequest("POST", "/", strings.NewReader("garbage"))
	req.Header.Set("Content-Type", "multipart/form-data")
	if _, err := ParseRequest(req, nil); err == nil {
		t.Error("missing boundary accepted")
	}
}

func TestParamsMiddlewareAndMerge(t *testing.T) {
	r := chi.NewRouter()
	r.Use(ParamsMiddleware(nil, nil), MethodOverride)
	var got *Params
	var method, orig string
	Route(r, "PATCH", "/issues/{id}", func(w http.ResponseWriter, req *http.Request) {
		got = ParamsOf(req)
		method, orig = req.Method, OriginalMethod(req)
	})
	req := httptest.NewRequest("POST", "/issues/5.json?id=9&a=query&format=xml", strings.NewReader("_method=patch&a=body&b=body"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || got == nil {
		t.Fatalf("status %d", w.Code)
	}
	// body < query < path の優先順位
	if got.String("id") != "5" || got.String("a") != "query" || got.String("b") != "body" || got.String("format") != "json" {
		t.Errorf("merge: %s", mustJSON(t, got))
	}
	if method != "PATCH" || orig != "POST" {
		t.Errorf("method %s orig %s", method, orig)
	}

	// 解析エラーは 400（空ボディ）
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/issues/1?x[y]=1&x[]=2", nil))
	if w.Code != 400 || w.Body.Len() != 0 {
		t.Errorf("bad query: %d %q", w.Code, w.Body.String())
	}
}

func TestMethodOverride(t *testing.T) {
	cases := []struct {
		method, ct, body, header, want string
	}{
		{"POST", "application/x-www-form-urlencoded", "_method=delete", "", "DELETE"},
		{"POST", "application/x-www-form-urlencoded", "_method=put", "", "PUT"},
		{"POST", "application/x-www-form-urlencoded", "_method=get", "", "GET"},
		{"POST", "application/x-www-form-urlencoded", "_method=bogus", "", "POST"},
		{"POST", "", "", "patch", "PATCH"},
		{"POST", "application/json", `{"_method":"delete"}`, "", "POST"},
		{"GET", "application/x-www-form-urlencoded", "_method=delete", "", "GET"},
		{"POST", "application/x-www-form-urlencoded", "_method=delete", "put", "DELETE"},
	}
	for _, c := range cases {
		var got string
		h := ParamsMiddleware(nil, nil)(MethodOverride(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Method })))
		req := httptest.NewRequest(c.method, "/", strings.NewReader(c.body))
		if c.ct != "" {
			req.Header.Set("Content-Type", c.ct)
		}
		if c.header != "" {
			req.Header.Set("X-HTTP-Method-Override", c.header)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
}

func TestPathParamsUnescape(t *testing.T) {
	r := chi.NewRouter()
	var got string
	Route(r, "GET", "/projects/{id}/wiki/{title}", func(w http.ResponseWriter, req *http.Request) {
		got = ParamsOf(req).String("title")
	}, NoFormat())
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/projects/x/wiki/A%2FB", nil))
	if got != "A/B" {
		t.Errorf("got %q", got)
	}
}
