// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package attachments

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
)

// fakePandoc は引数を記録して Markdown を出力する偽の pandoc を作る（version は --version の版、body は出力）。
func fakePandoc(t *testing.T, version, body string, sleep int) (cmd, argsLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	cmd = filepath.Join(dir, "pandoc")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'pandoc " + version + "'; echo 'Features: +server +lua'; exit 0; fi\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + argsLog + "'\n"
	if sleep > 0 {
		script += "sleep " + string(rune('0'+sleep)) + "\n"
	}
	script += "cat <<'EOF'\n" + body + "\nEOF\n"
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cmd, argsLog
}

// newPreviewStore は添付 1 件（ファイル名 filename・内容 content）を置いた Store を作る。
func newPreviewStore(t *testing.T, m *Markdownizer, filename, content string) (*Store, *domain.Attachment) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "files")
	if err := os.MkdirAll(filepath.Join(root, "2026", "01"), 0o755); err != nil {
		t.Fatal(err)
	}
	disk := "260115120000_" + DiskFilenameBase(filename)
	if err := os.WriteFile(filepath.Join(root, "2026", "01", disk), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &domain.Attachment{Filename: filename, DiskDirectory: "2026/01", DiskFilename: disk,
		Filesize: int64(len(content)), Digest: strings.Repeat("ab", 32)}
	return &Store{Root: root, Markdownizer: m}, a
}

// test_markdownized_previewable_should_be_*（Redmine::Markdownizer の対応拡張子）。
func TestMarkdownizerSupports(t *testing.T) {
	cmd, _ := fakePandoc(t, "3.8.3", "x", 0)
	m := NewMarkdownizer(cmd, 0, 0, 0, nil)
	if !m.Available() {
		t.Fatal("not available")
	}
	for fn, want := range map[string]bool{"a.docx": true, "A.DOCX": true, "b.odt": true, "c.xlsx": true, "d.pptx": true,
		"testfile.txt": false, "e.doc": false, "noext": false} {
		if got := m.Supports(fn); got != want {
			t.Errorf("Supports(%s) = %v", fn, got)
		}
	}
	// 3.8.3 より前は Word / Writer のみ
	old, _ := fakePandoc(t, "3.1.11.1", "x", 0)
	m = NewMarkdownizer(old, 0, 0, 0, nil)
	if !m.Supports("a.docx") || m.Supports("c.xlsx") || m.Supports("d.pptx") {
		t.Error("pandoc 3.1: xlsx/pptx should not be supported")
	}
	// 使えないコマンド
	m = NewMarkdownizer(filepath.Join(t.TempDir(), "no-such-pandoc"), 0, 0, 0, nil)
	if m.Available() || m.Supports("a.docx") {
		t.Error("missing pandoc should not be available")
	}
	var nilM *Markdownizer
	if nilM.Available() || nilM.Supports("a.docx") {
		t.Error("nil markdownizer")
	}
}

// test_show_msword / test_delete_from_disk_should_delete_markdownized_preview_cache 相当。
func TestMarkdownizedPreviewContent(t *testing.T) {
	cmd, argsLog := fakePandoc(t, "3.9", "Redmine is a flexible project management web application.", 0)
	s, a := newPreviewStore(t, NewMarkdownizer(cmd, 0, 0, 0, nil), "-o evil.docx", "PK\x03\x04docx")
	if !s.MarkdownizedPreviewable(a) {
		t.Fatal("not previewable")
	}
	b, ok := s.MarkdownizedPreviewContent(context.Background(), a)
	if !ok || !strings.Contains(string(b), "Redmine is a flexible project management web application.") {
		t.Fatalf("content = %q, %v", b, ok)
	}
	// 入力形式は -f で明示し、ファイルは "--" の後に絶対パスで渡す
	raw, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{"-f", "docx", "-t", "gfm", "--sandbox", "--", s.Diskfile(a)}
	if !filepath.IsAbs(s.Diskfile(a)) || strings.Join(args, "|") != strings.Join(want, "|") {
		t.Errorf("args = %q, want %q", args, want)
	}
	// 変換結果は derived_cache に保存され、2 回目は Pandoc を呼ばない
	cache := s.MarkdownizedPreviewCachePath(a)
	if want := filepath.Join(s.Root, "derived_cache", "markdownized_previews", a.Digest+"_8.md"); cache != want {
		t.Errorf("cache path = %s, want %s", cache, want)
	}
	if err := os.Remove(argsLog); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.MarkdownizedPreviewContent(context.Background(), a); !ok {
		t.Error("cached content not returned")
	}
	if _, err := os.Stat(argsLog); err == nil {
		t.Error("pandoc invoked again for a cached preview")
	}
	// 添付の削除でプレビューも消える
	s.deleteMarkdownizedPreview(a)
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("cache not removed: %v", err)
	}
	// 16 進でない digest は保存先の外を指さない
	a.Digest = "../../x"
	if s.MarkdownizedPreviewCachePath(a) != "" {
		t.Error("non-hex digest cache path")
	}
	if _, ok := s.MarkdownizedPreviewContent(context.Background(), a); ok {
		t.Error("preview with non-hex digest")
	}
	// 対応していない拡張子
	_, txt := newPreviewStore(t, s.Markdownizer, "testfile.txt", "hello")
	if s.MarkdownizedPreviewable(txt) {
		t.Error("txt previewable")
	}
}

// 元ファイルの大きさ・出力の大きさの上限と時間制限。
func TestMarkdownizedPreviewLimits(t *testing.T) {
	cmd, _ := fakePandoc(t, "2.9.2.1", strings.Repeat("0123456789", 100), 0)
	// 出力は上限で切り詰める（2.15 より前の Pandoc には --sandbox を付けない）
	s, a := newPreviewStore(t, NewMarkdownizer(cmd, 0, 0, 25, nil), "a.odt", "odt")
	b, ok := s.MarkdownizedPreviewContent(context.Background(), a)
	if !ok || string(b) != "0123456789012345678901234" {
		t.Errorf("truncated content = %q, %v", b, ok)
	}
	// 元ファイルが上限を超えれば変換しない
	s, a = newPreviewStore(t, NewMarkdownizer(cmd, 0, 3, 0, nil), "a.docx", "too large")
	if _, ok := s.MarkdownizedPreviewContent(context.Background(), a); ok {
		t.Error("converted a source file over the size limit")
	}
	// 時間制限を超えれば打ち切る
	slow, _ := fakePandoc(t, "3.9", "late", 5)
	s, a = newPreviewStore(t, NewMarkdownizer(slow, 200*time.Millisecond, 0, 0, nil), "a.docx", "docx")
	start := time.Now()
	if _, ok := s.MarkdownizedPreviewContent(context.Background(), a); ok {
		t.Error("timed-out conversion returned content")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("timeout not enforced: %v", d)
	}
	if _, err := os.Stat(s.MarkdownizedPreviewCachePath(a)); err == nil {
		t.Error("cache written for a timed-out conversion")
	}
}
