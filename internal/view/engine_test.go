// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package view

import (
	"html/template"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/web"
)

type testIssue struct{ Subject string }

type testPage struct {
	Title  string
	Issues []testIssue
	Nil    any
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Options{
		FS: os.DirFS("testdata/engine"),
		FlashIcon: func(r *Render, kind string) template.HTML {
			return template.HTML(`<svg class="` + kind + `"></svg>`)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRenderWithLayout(t *testing.T) {
	e := newTestEngine(t)
	ctx := &Context{
		Locale: "ja", CSRFToken: "TOK", Controller: "issues", Action: "index",
		ProjectName: "Proj", ProjectIdentifier: "proj", Project: struct{}{}, AppTitle: "Redmine",
		HasMainMenu: true, TextareaFont: "monospace",
		Flash: []Flash{{"notice", "Saved <b>ok</b>"}, {"error", "Bad"}},
	}
	page := testPage{Title: "All <issues>", Issues: []testIssue{{"A&B"}, {"C"}, {"D"}}}
	got, err := e.Render(ctx, "issues/index", page, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := `<!DOCTYPE html>
<html lang="ja">
<head>
<title>Issues - Proj - Redmine</title>
<meta name="csrf-param" content="authenticity_token" />
<meta name="csrf-token" content="TOK" />
<!-- page specific tags -->
&lt;raw-is-escaped&gt;</head>
<body class="project-proj has-main-menu controller-issues action-index avatars-off textarea-monospace">
<div id="sidebar"><h3>Side &amp; All &lt;issues&gt;</h3>
</div>
<div id="content">
<div class="flash notice" id="flash_notice"><svg class="notice"></svg>Saved <b>ok</b></div><div class="flash error" id="flash_error"><svg class="error"></svg>Bad</div>
<h2>All &lt;issues&gt;</h2>
<table>
<tr class="first n0"><td>A&amp;B</td><td>All &lt;issues&gt;</td></tr>
<tr class="n1"><td>C</td><td>All &lt;issues&gt;</td></tr>
<tr class="last n2"><td>D</td><td>All &lt;issues&gt;</td></tr>

</table>
<p>x&lt;y</p>

<p class="shared">All &lt;issues&gt;</p>


</div>
</body>
</html>
`
	if string(got) != want {
		t.Errorf("不一致\n%s", lineDiff(want, string(got)))
	}
}

func TestRenderJS(t *testing.T) {
	e := newTestEngine(t)
	ctx := &Context{Controller: "issues", Action: "update"}
	got, err := e.Render(ctx, "issues/update", testPage{Title: `it's "x"`}, RenderOptions{Format: "js"})
	if err != nil {
		t.Fatal(err)
	}
	// partial は js 形式で探して無ければ html にフォールバックする。
	// j(安全な値) は安全なまま、j(安全でない値) は結果がさらに HTML エスケープされる（Rails と同じ）。
	want := `$('#footer').html('<p>it&#39;s<\/p>\n');
$('#title').text('it\&#39;s \&quot;x\&quot;');
`
	if string(got) != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRenderText(t *testing.T) {
	e := newTestEngine(t)
	got, err := e.Render(&Context{}, "mailer/issue", testPage{Title: "<&>", Issues: []testIssue{{"a\"b"}}}, RenderOptions{Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Subject: <&>\n* a\"b\nend\n"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestPartialNotFound(t *testing.T) {
	e := newTestEngine(t)
	r := e.NewRender(&Context{Controller: "issues"}, nil, "html")
	if _, err := r.Partial("nope", nil); err == nil {
		t.Fatal("エラーにならない")
	}
	out, err := r.Partial("issues/footer", map[string]any{"note": 1})
	if err != nil || out != "<p>1</p>\n" {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestDuplicateDefine(t *testing.T) {
	fsys := fstest.MapFS{
		"a/x.html.tmpl": {Data: []byte(`{{define "dup"}}a{{end}}`)},
		"b/y.html.tmpl": {Data: []byte(`{{define "dup"}}b{{end}}`)},
	}
	if _, err := New(Options{FS: fsys}); err == nil || !strings.Contains(err.Error(), "重複") {
		t.Fatalf("重複定義がエラーにならない: %v", err)
	}
}

func TestErrorLineNumbers(t *testing.T) {
	// 行ごと削除されるアクションがあっても、エラーの行番号は元ソースと一致する
	src := "a\n{{if .X}}\n  {{/* c */}}\nb\n{{end}}\n{{.Y.Z.W}}\n"
	fsys := fstest.MapFS{"t.html.tmpl": {Data: []byte(src)}}
	e, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Render(&Context{}, "t", map[string]any{"X": true, "Y": 1}, RenderOptions{Layout: NoLayout})
	if err == nil || !strings.Contains(err.Error(), "t.html:6:") {
		t.Fatalf("行番号が一致しない: %v", err)
	}
	fsys["bad.html.tmpl"] = &fstest.MapFile{Data: []byte("x\n{{if .X}}\n{{end}}\n{{bogus_func}}\n")}
	if _, err := New(Options{FS: fsys}); err == nil || !strings.Contains(err.Error(), "bad.html:4:") {
		t.Fatalf("解析エラーの行番号が一致しない: %v", err)
	}
}

func TestReload(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(dir+"/p.html.tmpl", []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	e, err := New(Options{FS: os.DirFS(dir), Reload: true})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.Render(&Context{}, "p", nil, RenderOptions{Layout: NoLayout})
	if string(got) != "one" {
		t.Fatalf("got %q", got)
	}
	write("second")
	got, err = e.Render(&Context{}, "p", nil, RenderOptions{Layout: NoLayout})
	if err != nil || string(got) != "second" {
		t.Fatalf("再読み込みされない: %q %v", got, err)
	}
}

func TestRequestFuncs(t *testing.T) {
	fsys := fstest.MapFS{"t.html.tmpl": {Data: []byte(`{{who}} {{l "hello" 2}}`)}}
	e, err := New(Options{FS: fsys, RequestFuncs: []func(*Render) ttemplate.FuncMap{
		func(r *Render) ttemplate.FuncMap {
			return ttemplate.FuncMap{"who": func() any { return r.Ctx.User }}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &Context{User: "<admin>", T: func(k string, a ...any) string { return k + "!" }}
	got, err := e.Render(ctx, "t", nil, RenderOptions{Layout: NoLayout})
	if err != nil || string(got) != "&lt;admin&gt; hello!" {
		t.Fatalf("got %q %v", got, err)
	}
}

type formModel struct{ Subject string }

func (formModel) ParamKey() string { return "issue" }
func (formModel) Persisted() bool  { return true }
func (formModel) ToParam() string  { return "7" }

func TestFormBuilderInTemplate(t *testing.T) {
	src := `<div>
  {{$f := labelled_form_for "issue" .Issue (hash "url" "/issues/7" "html" (hash "id" "issue-form"))}}
  {{$f.Open}}
    <p>{{$f.TextField "subject" (hash "size" 80 "required" true)}}</p>
    <p>{{$f.CheckBox "is_private"}}</p>
    <p>{{$f.Select "status_id" (list (list "New" 1)) (hash "include_blank" true)}}</p>
  {{end_form}}
</div>
`
	fsys := fstest.MapFS{"f.html.tmpl": {Data: []byte(src)}}
	e, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &Context{CSRFToken: "T", FormNameSuffix: func() string { return "x" }}
	got, err := e.Render(ctx, "f", map[string]any{"Issue": formModel{Subject: "a<b"}}, RenderOptions{Layout: NoLayout})
	if err != nil {
		t.Fatal(err)
	}
	want := `<div>
  <form class="edit_issue" id="issue-form" action="/issues/7" accept-charset="UTF-8" name="issue-form-x" method="post"><input type="hidden" name="_method" value="patch" autocomplete="off" /><input type="hidden" name="authenticity_token" value="T" autocomplete="off" />
    <p><label for="issue_subject">Subject<span class="required"> *</span></label><input size="80" type="text" value="a&lt;b" name="issue[subject]" id="issue_subject" /></p>
    <p><label for="issue_is_private">Is private</label><input name="issue[is_private]" type="hidden" value="0" autocomplete="off" /><input type="checkbox" value="1" name="issue[is_private]" id="issue_is_private" /></p>
    <p><label for="issue_status_id">Status</label><select name="issue[status_id]" id="issue_status_id"><option value="" label=" "></option>
<option value="1">New</option></select></p>
</form></div>
`
	if string(got) != want {
		t.Errorf("不一致\n%s\n---\n%s", lineDiff(want, string(got)), got)
	}
}

func TestConcurrentRender(t *testing.T) {
	e := newTestEngine(t)
	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			ctx := &Context{Controller: "issues", Action: "index", CSRFToken: "T"}
			_, err := e.Render(ctx, "issues/index", testPage{Title: "x", Issues: []testIssue{{"a"}}}, RenderOptions{})
			done <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// TestWebTemplatesParse は web/templates（embed）のすべてのテンプレートが解析できることを確認する。
// テンプレートを移植したら、ここで構文エラー・未定義関数・重複定義が検出される。
// Redmine ヘルパー（internal/view 以外で定義される関数）を使うテンプレートを検査するには、
// それらの関数名を Options.Funcs に登録したうえで同様のテストを行うこと。
func TestWebTemplatesParse(t *testing.T) {
	e, err := New(Options{FS: web.Templates(), SkipFuncCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d templates", len(e.Names()))
}
