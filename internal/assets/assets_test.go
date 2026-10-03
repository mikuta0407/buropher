// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package assets

import (
	"bufio"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mikuta0407/buropher/web"
)

func newEmbedded(t *testing.T) *Pipeline {
	t.Helper()
	p, err := New(web.Assets(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var digestNormRe = regexp.MustCompile(`-[0-9a-f]{8}\.(js|css|png|svg|ico|woff2|gif|map|txt)`)

func norm(s string) string { return digestNormRe.ReplaceAllString(s, "-X.$1") }

// 同梱しない gem 由来のアセット（actioncable, doorkeeper, stimulus-rails の未使用分など）。
var notShipped = map[string]bool{
	"action_cable.js":                  true,
	"actioncable.esm.js":               true,
	"actioncable.js":                   true,
	"doorkeeper/admin/application.css": true,
	"doorkeeper/application.css":       true,
	"doorkeeper/bootstrap.min.css":     true,
	"rails-requestjs.js":               true,
	"rails-ujs.esm.js":                 true,
	"stimulus-autoloader.js":           true,
	"stimulus-importmap-autoloader.js": true,
	"stimulus.js":                      true,
}

// Redmine 6.1.2 の manifest にある論理パスが（同梱対象については）全て解決できること。
func TestManifestLogicalPaths(t *testing.T) {
	p := newEmbedded(t)
	f, err := os.Open("testdata/redmine-assets-manifest.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" {
			continue
		}
		want[l] = true
		if notShipped[l] {
			continue
		}
		a, ok := p.Lookup(l)
		if !ok {
			t.Errorf("logical path %q not resolved", l)
			continue
		}
		if got := p.AssetPath(l); got != "/assets/"+a.DigestedPath {
			t.Errorf("AssetPath(%q) = %q", l, got)
		}
	}
	// 逆方向: Redmine に存在しない論理パスを生やしていないこと。
	for _, l := range p.LogicalPaths() {
		if !want[l] {
			t.Errorf("unexpected logical path %q", l)
		}
	}
}

func TestDigestedPath(t *testing.T) {
	cases := map[string]string{
		"application.css":             "application-abcdef12.css",
		"tribute.min.js.map":          "tribute.min-abcdef12.js.map",
		"stimulus.min.js":             "stimulus.min-abcdef12.js",
		"jquery/jquery-ui-1.13.2.css": "jquery/jquery-ui-1.13.2-abcdef12.css",
		"OFL.txt":                     "OFL-abcdef12.txt",
		"noext":                       "noext",
		"foo-1234567.digested.js":     "foo-1234567.digested.js",
	}
	for in, want := range cases {
		if got := digestedPath(in, "abcdef12"); got != want {
			t.Errorf("digestedPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractPathAndDigest(t *testing.T) {
	cases := []struct{ in, path, digest string }{
		{"application-6dc0ec44.css", "application.css", "6dc0ec44"},
		{"jquery-3.7.1-ui-1.13.3-3ca148b8.js", "jquery-3.7.1-ui-1.13.3.js", "3ca148b8"},
		{"tribute.min-e98831eb.js.map", "tribute.min.js.map", "e98831eb"},
		{"jquery/jquery-ui-1.13.2-70e53573.css", "jquery/jquery-ui-1.13.2.css", "70e53573"},
		{"application.css", "application.css", ""},
		{"foo-1234567.digested.js", "foo-1234567.digested.js", ""},
	}
	for _, c := range cases {
		p, d := ExtractPathAndDigest(c.in)
		if p != c.path || d != c.digest {
			t.Errorf("ExtractPathAndDigest(%q) = %q, %q", c.in, p, d)
		}
	}
}

func TestCSSCompile(t *testing.T) {
	p := newEmbedded(t)
	app, _ := p.Lookup("application.css")
	svg, _ := p.Lookup("chevron-down.svg")
	font, _ := p.Lookup("NotoSans-Regular.woff2")
	c := string(app.Content())
	for _, want := range []string{
		`url("/assets/` + svg.DigestedPath + `")`,
		`src: url("/assets/` + font.DigestedPath + `") format("woff2");`,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("application.css does not contain %s", want)
		}
	}
	if strings.Contains(c, "url(/") {
		t.Errorf("application.css has unrewritten url(/...)")
	}

	// テーマ: @import と テーマ画像 / 本体画像
	th, _ := p.Lookup("themes/classic/application.css")
	home, _ := p.Lookup("themes/classic/home.png")
	tc := string(th.Content())
	for _, want := range []string{
		`@import url("/assets/` + app.DigestedPath + `");`,
		`url("/assets/` + home.DigestedPath + `")`,
	} {
		if !strings.Contains(tc, want) {
			t.Errorf("themes/classic/application.css does not contain %s", want)
		}
	}

	// jstoolbar.css: ./help.png は解決、存在しない img/resizer.png は引用符付きでそのまま
	js, _ := p.Lookup("jstoolbar.css")
	help, _ := p.Lookup("help.png")
	jc := string(js.Content())
	if !strings.Contains(jc, `url("/assets/`+help.DigestedPath+`")`) || !strings.Contains(jc, `url("img/resizer.png")`) {
		t.Errorf("jstoolbar.css not compiled as expected")
	}

	// jquery-ui: 相対の images/... と data: URI
	jq, _ := p.Lookup("jquery/jquery-ui-1.13.2.css")
	icon, _ := p.Lookup("jquery/images/ui-icons_444444_256x240.png")
	qc := string(jq.Content())
	if !strings.Contains(qc, `url("/assets/`+icon.DigestedPath+`")`) {
		t.Errorf("jquery-ui css: relative image not resolved")
	}
	if !strings.Contains(qc, `url("data:image/gif;base64,`) {
		t.Errorf("jquery-ui css: data uri altered")
	}
	if !strings.Contains(qc, `url("%22images%2Fui-icons_555555_256x240.png%22")`) {
		t.Errorf("jquery-ui css: missing-asset url not quoted like Propshaft")
	}

	// ダイジェストはコンパイル後内容の SHA1 先頭 8 桁
	if app.Digest != sha8(app.Content()) || len(app.Digest) != 8 {
		t.Errorf("digest mismatch")
	}
}

func TestSourceMappingURL(t *testing.T) {
	p := newEmbedded(t)
	js, _ := p.Lookup("stimulus.min.js")
	m, _ := p.Lookup("stimulus.min.js.map")
	if !strings.HasSuffix(string(js.Content()), "//# sourceMappingURL=/assets/"+m.DigestedPath+"\n") {
		t.Errorf("stimulus.min.js sourceMappingURL not rewritten")
	}

	fsys := fstest.MapFS{
		"javascripts/a.js":      {Data: []byte("x();\n//# sourceMappingURL=missing.js.map\n")},
		"stylesheets/b.css":     {Data: []byte("a{}\n/*# sourceMappingURL=b.css.map */")},
		"stylesheets/b.css.map": {Data: []byte("{}")},
	}
	p2, err := New(fsys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := p2.Lookup("a.js")
	if got := string(a.Content()); got != "x();\n//\n" {
		t.Errorf("missing map: got %q", got)
	}
	b, _ := p2.Lookup("b.css")
	bm, _ := p2.Lookup("b.css.map")
	if got := string(b.Content()); got != "a{}\n/*# sourceMappingURL=/assets/"+bm.DigestedPath+" */" {
		t.Errorf("css map: got %q", got)
	}
}

// Redmine::AssetPath の url 置換（テーマ内の相対パスを論理パスに合わせる）。
func TestThemeTransition(t *testing.T) {
	fsys := fstest.MapFS{
		"images/add.png":              {Data: []byte("add")},
		"images/files/c.png":          {Data: []byte("c")},
		"stylesheets/application.css": {Data: []byte("body{}")},
		"javascripts/app.js":          {Data: []byte("1")},
		"themes/t/stylesheets/application.css": {Data: []byte(
			"a{background:url(../images/home.png)}\n" +
				"b{background:url(../../../images/add.png)}\n" +
				"c{background:url('../../../images/files/c.png')}\n" +
				"@import url(../../../stylesheets/application.css);\n")},
		"themes/t/images/home.png":      {Data: []byte("home")},
		"themes/notatheme/images/x.png": {Data: []byte("x")},
	}
	p, err := New(fsys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	css, ok := p.Lookup("themes/t/application.css")
	if !ok {
		t.Fatal("theme css not found")
	}
	want := "a{background:url(\"/assets/themes/t/home-X.png\")}\n" +
		"b{background:url(\"/assets/add-X.png\")}\n" +
		"c{background:url(\"/assets/files/c-X.png\")}\n" +
		"@import url(\"/assets/application-X.css\");\n"
	if got := norm(string(css.Content())); got != want {
		t.Errorf("theme css:\n got %q\nwant %q", got, want)
	}
	// Propshaft の通常ロードパス由来の "<theme>/<sub>/<file>" は Redmine の置換なし（素の Propshaft 解決のみ）
	raw, ok := p.Lookup("t/stylesheets/application.css")
	if !ok || norm(string(raw.Content())) != "a{background:url(\"/assets/t/images/home-X.png\")}\n"+
		"b{background:url(\"../../../images/add.png\")}\n"+
		"c{background:url(\"../../../images/files/c.png\")}\n"+
		"@import url(\"../../../stylesheets/application.css\");\n" {
		t.Errorf("generic theme path: %v %q", ok, raw.Content())
	}
	if _, ok := p.Lookup("themes/notatheme/x.png"); ok {
		t.Errorf("directory without application.css must not be a theme")
	}
	if th := p.Theme("t"); th == nil || th.Name != "T" || !th.HasImage("home.png") || !th.HasStylesheet("application") {
		t.Errorf("Theme(t) = %+v", th)
	}
}

func TestExtraThemes(t *testing.T) {
	extra := fstest.MapFS{
		"mytheme/stylesheets/application.css": {Data: []byte("@import url(../../../stylesheets/application.css);")},
		"mytheme/favicon/fav.ico":             {Data: []byte("ico")},
		"mytheme/javascripts/theme.js":        {Data: []byte("1")},
	}
	p, err := New(web.Assets(), Options{ExtraThemes: extra})
	if err != nil {
		t.Fatal(err)
	}
	css, ok := p.Lookup("themes/mytheme/application.css")
	if !ok || norm(string(css.Content())) != `@import url("/assets/application-X.css");` {
		t.Errorf("extra theme css: %v %q", ok, css)
	}
	th := p.Theme("mytheme")
	if th == nil || th.FaviconPath() != "themes/mytheme/fav.ico" || !th.HasJavascript("theme") {
		t.Fatalf("Theme(mytheme) = %+v", th)
	}
	if _, ok := p.Lookup(th.FaviconPath()); !ok {
		t.Errorf("favicon not registered")
	}
	var ids []string
	for _, x := range p.Themes() {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, ",") != "alternate,classic,mytheme" {
		t.Errorf("Themes() = %v", ids)
	}
}

// curl -s http://127.0.0.1:3999/login の head 部（ダイジェストを正規化）。
const redmineLoginHead = `<link rel="shortcut icon" type="image/x-icon" href="/assets/favicon-X.ico" />
<link rel="stylesheet" href="/assets/jquery/jquery-ui-1.13.2-X.css" media="all" />
<link rel="stylesheet" href="/assets/tribute-5.1.3-X.css" media="all" />
<link rel="stylesheet" href="/assets/application-X.css" media="all" />
<link rel="stylesheet" href="/assets/responsive-X.css" media="all" />

<script type="importmap" data-turbo-track="reload">{
  "imports": {
    "@rails/request.js": "/assets/requestjs-X.js",
    "application": "/assets/application-X.js",
    "@hotwired/stimulus": "/assets/stimulus.min-X.js",
    "@hotwired/stimulus-loading": "/assets/stimulus-loading-X.js",
    "turndown": "/assets/turndown-X.js",
    "controllers/api_key_copy_controller": "/assets/controllers/api_key_copy_controller-X.js",
    "controllers/application": "/assets/controllers/application-X.js",
    "controllers": "/assets/controllers/index-X.js",
    "controllers/list_autofill_controller": "/assets/controllers/list_autofill_controller-X.js",
    "controllers/quote_reply_controller": "/assets/controllers/quote_reply_controller-X.js",
    "controllers/sticky_issue_header_controller": "/assets/controllers/sticky_issue_header_controller-X.js"
  }
}</script>
<link rel="modulepreload" href="/assets/requestjs-X.js">
<link rel="modulepreload" href="/assets/application-X.js">
<link rel="modulepreload" href="/assets/stimulus.min-X.js">
<link rel="modulepreload" href="/assets/stimulus-loading-X.js">
<link rel="modulepreload" href="/assets/turndown-X.js">
<link rel="modulepreload" href="/assets/controllers/api_key_copy_controller-X.js">
<link rel="modulepreload" href="/assets/controllers/application-X.js">
<link rel="modulepreload" href="/assets/controllers/index-X.js">
<link rel="modulepreload" href="/assets/controllers/list_autofill_controller-X.js">
<link rel="modulepreload" href="/assets/controllers/quote_reply_controller-X.js">
<link rel="modulepreload" href="/assets/controllers/sticky_issue_header_controller-X.js">
<script type="module">import "application"</script>
<script src="/assets/jquery-3.7.1-ui-1.13.3-X.js"></script>
<script src="/assets/rails-ujs-X.js"></script>
<script src="/assets/tribute-5.1.3.min-X.js"></script><script src="/assets/application-legacy-X.js"></script>
<script src="/assets/responsive-X.js"></script>`

func TestHelpersMatchRedmineHead(t *testing.T) {
	p := newEmbedded(t)
	got := string(p.FaviconLinkTag("favicon.ico")) + "\n" +
		string(p.StylesheetLinkTagMedia("all", "jquery/jquery-ui-1.13.2", "tribute-5.1.3", "application", "responsive")) + "\n\n" +
		string(p.ImportmapTags()) + "\n" +
		string(p.JavascriptIncludeTag("jquery-3.7.1-ui-1.13.3", "rails-ujs", "tribute-5.1.3.min")) +
		string(p.JavascriptIncludeTag("application-legacy", "responsive"))
	if norm(got) != redmineLoginHead {
		t.Errorf("head mismatch:\n%s", got)
	}
}

func TestHelpers(t *testing.T) {
	p := newEmbedded(t)
	// help/wiki_syntax/detailed: media 指定なしは screen
	if got := norm(string(p.StylesheetLinkTag("wiki_syntax_detailed.css"))); got != `<link rel="stylesheet" href="/assets/wiki_syntax_detailed-X.css" media="screen" />` {
		t.Errorf("StylesheetLinkTag = %s", got)
	}
	cases := map[string]string{
		p.JavascriptPath("tablesort-5.2.1.min.js"): "/assets/tablesort-5.2.1.min-X.js",
		p.JavascriptPath("tribute-5.1.3.min"):      "/assets/tribute-5.1.3.min-X.js",
		p.StylesheetPath("tribute-5.1.3"):          "/assets/tribute-5.1.3-X.css",
		p.AssetPath("missing.png"):                 "/assets/missing.png",
		p.AssetPath("/images/x.png"):               "/images/x.png",
		p.AssetPath("https://example.com/a.js"):    "https://example.com/a.js",
		p.AssetPath("icons.svg#icon--add"):         "/assets/icons-X.svg#icon--add",
		p.ImagePath("themes/classic/home.png"):     "/assets/themes/classic/home-X.png",
		p.JavascriptPath("i18n/datepicker-ja"):     "/assets/i18n/datepicker-ja-X.js",
	}
	for got, want := range cases {
		if norm(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if got := string(p.JavascriptIncludeTag("a", "a")); got != `<script src="/assets/a.js"></script>` {
		t.Errorf("uniq: %s", got)
	}
}

func TestRelativeURLRoot(t *testing.T) {
	p, err := New(web.Assets(), Options{RelativeURLRoot: "/redmine/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := norm(p.StylesheetPath("application")); got != "/redmine/assets/application-X.css" {
		t.Errorf("StylesheetPath = %s", got)
	}
	if got := p.AssetPath("missing.png"); got != "/redmine/assets/missing.png" {
		t.Errorf("AssetPath = %s", got)
	}
	app, _ := p.Lookup("application.css")
	if !strings.Contains(string(app.Content()), `url("/redmine/assets/chevron-down-`) {
		t.Errorf("css url prefix does not include relative root")
	}
	if !strings.Contains(p.ImportmapJSON(), `"application": "/redmine/assets/application-`) {
		t.Errorf("importmap: %s", p.ImportmapJSON())
	}
}

func TestHandler(t *testing.T) {
	p := newEmbedded(t)
	h := p.Handler()
	app, _ := p.Lookup("application.css")

	do := func(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do("GET", "/assets/"+app.DigestedPath, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/css" ||
		rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		rec.Header().Get("ETag") != `"`+app.Digest+`"` ||
		rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("headers: %v", rec.Header())
	}
	if rec.Body.String() != string(app.Content()) {
		t.Errorf("body mismatch")
	}

	rec = do("GET", "/assets/"+app.DigestedPath, map[string]string{"Accept-Encoding": "gzip, deflate"})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != string(app.Content()) {
		t.Errorf("gzip body mismatch")
	}

	rec = do("GET", "/assets/"+app.DigestedPath, map[string]string{"If-None-Match": `"` + app.Digest + `"`})
	if rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", rec.Code)
	}

	rec = do("HEAD", "/assets/"+app.DigestedPath, nil)
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD: %d %d", rec.Code, rec.Body.Len())
	}

	for _, target := range []string{"/assets/application.css", "/assets/application-00000000.css", "/assets/nothing-12345678.css"} {
		rec = do("GET", target, nil)
		if rec.Code != 404 || rec.Body.String() != "Not found" {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Body.String())
		}
	}

	fav, _ := p.Lookup("favicon.ico")
	rec = do("GET", "/assets/"+fav.DigestedPath, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/vnd.microsoft.icon" {
		t.Errorf("favicon: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	ctl, _ := p.Lookup("controllers/index.js")
	rec = do("GET", "/assets/"+ctl.DigestedPath, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/javascript" {
		t.Errorf("controllers/index.js: %d", rec.Code)
	}
}

func TestDevReload(t *testing.T) {
	dir := t.TempDir()
	css := filepath.Join(dir, "stylesheets", "application.css")
	os.MkdirAll(filepath.Dir(css), 0o755)
	os.MkdirAll(filepath.Join(dir, "images"), 0o755)
	os.WriteFile(css, []byte("a{background:url(/a.png)}"), 0o644)
	os.WriteFile(filepath.Join(dir, "images", "a.png"), []byte("one"), 0o644)

	p, err := New(os.DirFS(dir), Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := p.Lookup("application.css")

	os.WriteFile(filepath.Join(dir, "images", "a.png"), []byte("two!"), 0o644)
	p.devMu.Lock()
	p.lastCheck = time.Time{}
	p.devMu.Unlock()

	after, _ := p.Lookup("application.css")
	if before.Digest == after.Digest {
		t.Errorf("digest not updated after referenced image changed")
	}
	img, _ := p.Lookup("a.png")
	if !strings.Contains(string(after.Content()), img.DigestedPath) {
		t.Errorf("css not recompiled: %s", after.Content())
	}
}

func TestTransitionUpdate(t *testing.T) {
	tr := &transition{}
	// 子ディレクトリが先に来ても親に置き換わる
	tr.addDest("/images/files/c.png", "files/c.png")
	tr.addDest("/images/add.png", "add.png")
	tr.addDest("/stylesheets/application.css", "application.css")
	tr.addDest("/javascripts/a.js", "a.js")
	tr.addSrc("/themes/x/stylesheets/application.css", "themes/x/application.css")
	tr.addDest("/themes/x/stylesheets/application.css", "themes/x/application.css")
	tr.addDest("/themes/x/images/a.png", "themes/x/a.png")
	got := tr.update()
	want := []kv{{"../../../images", "../.."}, {"../../../stylesheets", "../.."}, {"../images", "."}}
	if len(got) != 1 || len(got["themes/x"]) != len(want) {
		t.Fatalf("update() = %v", got)
	}
	for i, e := range want {
		if got["themes/x"][i] != e {
			t.Errorf("entry %d = %v, want %v", i, got["themes/x"][i], e)
		}
	}
}
