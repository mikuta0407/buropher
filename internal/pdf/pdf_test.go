// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package pdf_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/pdf"
	"github.com/mikuta0407/buropher/internal/pdf/pdftest"
)

// cjkFontDir はテスト用の CJK フォント（DroidSansFallbackFull.ttf）のあるディレクトリ
// （BUROPHER_PDF_TEST_FONT_DIR か、親ディレクトリの _reference/fonts。無ければ ""）。
func cjkFontDir() string {
	if d := os.Getenv("BUROPHER_PDF_TEST_FONT_DIR"); d != "" {
		return d
	}
	dir, _ := os.Getwd()
	for range 8 {
		p := filepath.Join(dir, "_reference", "fonts")
		if _, err := os.Stat(filepath.Join(p, "DroidSansFallbackFull.ttf")); err == nil {
			return p
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// debugWrite は BUROPHER_PDF_DEBUG_DIR があれば PDF を書き出す（目視確認用）。
func debugWrite(t *testing.T, data []byte) {
	if dir := os.Getenv("BUROPHER_PDF_DEBUG_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "_")+".pdf"), data, 0o644)
	}
}

func output(t *testing.T, d *pdf.Doc) *pdftest.Doc {
	t.Helper()
	data, err := d.Output()
	if err != nil {
		t.Fatal(err)
	}
	debugWrite(t, data)
	if !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data[len(data)-64:], []byte("%%EOF")) {
		t.Fatalf("not a PDF")
	}
	return pdftest.Parse(data)
}

func TestCellsAndFooter(t *testing.T) {
	d := pdf.New(pdf.Default(), pdf.Options{Locale: "en"})
	d.FooterDate = "01/15/2026"
	d.AddPage()
	d.SetFontStyle("B", 11)
	d.MultiCell(190, 5, "eCookbook - Bug #1", "", "", false, 1)
	d.SetFontStyle("", 9)
	d.Cell(35, 5, "Status:", "LT", 0, "", false)
	d.Cell(60, 5, "New", "RT", 1, "", false)
	p := output(t, d)
	if p.Pages != 1 {
		t.Fatalf("pages %d", p.Pages)
	}
	text := p.Text()
	for _, want := range []string{"eCookbook - Bug #1", "Status:", "New", "01/15/2026", "1/1"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
}

func TestMultiCellWrapAndPageBreak(t *testing.T) {
	d := pdf.New(nil, pdf.Options{Locale: "en"})
	d.AddPage()
	d.SetFontStyle("", 9)
	long := strings.Repeat("lorem ipsum dolor sit amet ", 400)
	if h := d.StringHeight(100, long); h < 50 {
		t.Fatalf("string height %v", h)
	}
	d.MultiCell(190, 5, long, "", "", false, 1)
	d.WriteHTMLCell(190, 5, "<p>"+long+"</p>", "LRB", nil)
	p := output(t, d)
	if p.Pages < 3 {
		t.Fatalf("pages %d", p.Pages)
	}
	if !strings.Contains(p.Text(), "2/") {
		t.Errorf("footer")
	}
}

func pngData(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		for y := range 20 {
			img.Set(x, y, color.RGBA{uint8(x * 6), 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWriteHTML(t *testing.T) {
	d := pdf.New(nil, pdf.Options{Locale: "en"})
	d.AddPage()
	d.SetFontStyle("", 9)
	img := pngData(t)
	html := `<p>{{toc}}</p><h1>Title</h1><p>Some <strong>bold</strong> and <em>italic</em> <code>code</code> <a href="http://example.com/">link</a></p>
<ul><li>one</li><li>two<ol><li>nested</li></ol></li></ul>
<pre><code class="ruby">def foo
  bar
end</code></pre>
<blockquote><p>quoted</p></blockquote>
<table><tr><th>Head A</th><th>Head B</th></tr><tr><td>cell 1</td><td>cell 2 with longer text</td></tr></table>
<p><img src="image.png" alt="" /> <img src="missing.png"/></p><hr/><p>after</p>`
	d.WriteHTMLCell(190, 5, html, "LRB", func(src string) ([]byte, bool) {
		if src == "image.png" {
			return img, true
		}
		return nil, false
	})
	data, err := d.Output()
	if err != nil {
		t.Fatal(err)
	}
	debugWrite(t, data)
	p := pdftest.Parse(data)
	text := p.Text()
	for _, want := range []string{"Title", "bold", "italic", "code", "link", "one", "nested", "def", "foo", "quoted", "Head A", "cell 2", "after", "•", "1."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(text, "toc") {
		t.Errorf("{{toc}} not stripped")
	}
	if !bytes.Contains(data, []byte("/Subtype /Image")) {
		t.Errorf("image not embedded")
	}
	if !bytes.Contains(data, []byte("http://example.com/")) {
		t.Errorf("link annotation missing")
	}
}

func TestUnsupportedGlyphs(t *testing.T) {
	// CJK フォントが無い場合は ? に置き換えて落ちない
	fs := pdf.NewFontSet(pdf.Config{})
	d := pdf.New(fs, pdf.Options{Locale: "ja"})
	d.AddPage()
	d.SetFontStyle("", 9)
	d.MultiCell(190, 5, "日本語のテキスト abc", "", "", false, 1)
	d.WriteHTMLCell(190, 5, "<p>日本語 <strong>太字</strong></p>", "", nil)
	p := output(t, d)
	if !strings.Contains(p.Text(), "abc") || !strings.Contains(p.Text(), "?") {
		t.Errorf("text %q", p.Text())
	}
}

func TestCJKFont(t *testing.T) {
	dir := cjkFontDir()
	if dir == "" {
		t.Skip("CJK のテスト用フォント（DroidSansFallbackFull.ttf）がありません")
	}
	fs := pdf.NewFontSet(pdf.Config{Dir: dir})
	if !fs.HasCJK("ja") {
		t.Fatal("ja font not detected")
	}
	d := pdf.New(fs, pdf.Options{Locale: "ja"})
	d.AddPage()
	d.SetFontStyle("B", 11)
	d.MultiCell(190, 5, "チケット #1: 日本語の題名 abc", "", "", false, 1)
	d.SetFontStyle("", 9)
	d.WriteHTMLCell(190, 5, "<p>説明文です。"+strings.Repeat("長い日本語の文章を折り返します。", 20)+"</p>", "LRB", nil)
	p := output(t, d)
	joined := p.Joined()
	for _, want := range []string{"チケット #1: 日本語の題名 abc", "説明文です。"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "?") {
		t.Errorf("unexpected replacement: %q", joined)
	}
}

func TestTruncate(t *testing.T) {
	if got := pdf.Truncate("abcdefghij", 8); got != "abcde..." {
		t.Errorf("got %q", got)
	}
	if got := pdf.Truncate("abc", 8); got != "abc" {
		t.Errorf("got %q", got)
	}
}
