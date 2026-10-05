// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package attachments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/mimetype"
)

// このファイルは Redmine::Markdownizer（lib/redmine/markdownizer.rb）と Attachment#markdownized_*
// （Redmine 7.0 の #8959: Microsoft Office / LibreOffice の文書を Pandoc で Markdown に変換してプレビューする）の移植。
//
// Redmine との違い（安全側）:
//   - シェルを通さず exec.CommandContext で起動し、入力形式は添付のファイル名の拡張子から -f で明示する
//     （ディスク上のファイル名の拡張子で Pandoc に読み込み形式を推測させない）。入力ファイルは "--" の後に
//     絶対パスで渡し、オプションとして解釈されないようにする。
//   - Pandoc 2.15 以降では --sandbox を付け、変換中に他のファイルやネットワークへアクセスさせない。
//   - 出力は上限まで読み、超えた分は捨てる（Redmine は一時ファイルに全部書いてから切り詰める）。
//   - 同時に動かす Pandoc の数を制限する。

// Markdownizer のパラメータの既定値（Redmine の configuration.rb の既定値と同じ）。
const (
	DefaultMarkdownizedPreviewGenerationTimeout = 10 * time.Second
	DefaultMarkdownizedPreviewMaxSourceSize     = 10 * 1024 * 1024
	DefaultMarkdownizedPreviewMaxOutputSize     = 100 * 1024
)

// Markdownizer は Redmine::Markdownizer（Pandoc の呼び出し）。
type Markdownizer struct {
	// Command は Pandoc の実行ファイル（pandoc_command。空なら "pandoc"）。
	Command string
	// Timeout は 1 回の変換の時間制限（markdownized_preview_generation_timeout）。
	Timeout time.Duration
	// MaxSourceSize は変換する元ファイルの最大バイト数（markdownized_preview_max_source_size）。
	MaxSourceSize int64
	// MaxOutputSize は保存する Markdown の最大バイト数（markdownized_preview_max_output_size）。
	MaxOutputSize int64
	// Logger はログ出力先（nil なら slog.Default()）。
	Logger *slog.Logger

	once      sync.Once
	available bool
	version   []int
	sem       chan struct{}
}

// NewMarkdownizer は設定値（0 や空は既定値）から Markdownizer を作る。
func NewMarkdownizer(command string, timeout time.Duration, maxSource, maxOutput int64, logger *slog.Logger) *Markdownizer {
	if strings.TrimSpace(command) == "" {
		command = "pandoc"
	}
	if timeout <= 0 {
		timeout = DefaultMarkdownizedPreviewGenerationTimeout
	}
	if maxSource <= 0 {
		maxSource = DefaultMarkdownizedPreviewMaxSourceSize
	}
	if maxOutput <= 0 {
		maxOutput = DefaultMarkdownizedPreviewMaxOutputSize
	}
	return &Markdownizer{Command: command, Timeout: timeout, MaxSourceSize: maxSource, MaxOutputSize: maxOutput, Logger: logger}
}

func (m *Markdownizer) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

var rePandocVersion = regexp.MustCompile(`pandoc(?:\.exe)?\s+([\d.]+)`)

// Available は Redmine::Markdownizer.available?（`pandoc --version` が成功し、版を読み取れるか。結果は覚えておく）。
func (m *Markdownizer) Available() bool {
	if m == nil {
		return false
	}
	m.once.Do(func() {
		m.sem = make(chan struct{}, 2)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, m.Command, "--version").Output()
		if err == nil {
			if v := rePandocVersion.FindSubmatch(out); v != nil {
				m.version = parseVersion(string(v[1]))
				m.available = len(m.version) > 0
			}
		}
		if !m.available {
			m.logger().Warn(fmt.Sprintf("Pandoc binary (%s) not available", m.Command))
		}
	})
	return m.available
}

// parseVersion は "3.8.3" → [3 8 3]（数字でない部分は 0。Ruby の "x".to_i と同じ）。
func parseVersion(s string) []int {
	var v []int
	for _, p := range strings.Split(s, ".") {
		if p == "" {
			continue
		}
		n, _ := strconv.Atoi(p)
		v = append(v, n)
	}
	return v
}

// versionAtLeast は Pandoc の版が want 以上か（Array#<=> と同じ比較）。
func (m *Markdownizer) versionAtLeast(want ...int) bool {
	for i, w := range want {
		if i >= len(m.version) {
			return false
		}
		if m.version[i] != w {
			return m.version[i] > w
		}
	}
	return true
}

// pandocReaders は拡張子 → Pandoc の入力形式（-f）。
var pandocReaders = map[string]string{".docx": "docx", ".odt": "odt", ".xlsx": "xlsx", ".pptx": "pptx"}

// MarkdownizableExtensions は Redmine::Markdownizer.markdownizable_extensions。
func (m *Markdownizer) MarkdownizableExtensions() []string {
	if !m.Available() {
		return nil
	}
	// Microsoft Word と LibreOffice Writer のファイルは幅広い版の Pandoc が対応している
	exts := []string{".docx", ".odt"}
	// Pandoc 3.8.3 以降は Microsoft Excel と PowerPoint のファイルにも対応している
	if m.versionAtLeast(3, 8, 3) {
		exts = append(exts, ".xlsx", ".pptx")
	}
	return exts
}

// Supports は Redmine::Markdownizer.supports?(filename)。
func (m *Markdownizer) Supports(filename string) bool {
	ext := strings.ToLower(mimetype.Extname(filename))
	for _, e := range m.MarkdownizableExtensions() {
		if e == ext {
			return true
		}
	}
	return false
}

// limitedBuffer は最初の limit バイトだけを保持し、残りは捨てる io.Writer。
type limitedBuffer struct {
	buf   []byte
	limit int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - int64(len(b.buf)); room > 0 {
		if int64(len(p)) > room {
			b.buf = append(b.buf, p[:room]...)
		} else {
			b.buf = append(b.buf, p...)
		}
	}
	return len(p), nil
}

// Convert は Redmine::Markdownizer.convert(source, target)（変換済みならそのまま。成功すれば true）。
// filename は添付のファイル名（入力形式の判定に使う）。
func (m *Markdownizer) Convert(ctx context.Context, source, target, filename string) (bool, error) {
	if !m.Available() {
		return false, nil
	}
	if _, err := os.Stat(target); err == nil {
		return true, nil
	}
	reader := pandocReaders[strings.ToLower(mimetype.Extname(filename))]
	if reader == "" || !m.Supports(filename) {
		return false, nil
	}
	st, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	if st.Size() > m.MaxSourceSize {
		m.logger().Warn(fmt.Sprintf("Markdownized preview generation skipped because source file is too large (%d bytes): %s", st.Size(), source))
		return false, nil
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return false, err
	}
	// 同時に動かす Pandoc の数を制限する（待つ間に要求が終われば変換しない）
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-m.sem }()
	// 待っている間に他の要求が変換していればそれを使う
	if _, err := os.Stat(target); err == nil {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}

	args := []string{"-f", reader, "-t", "gfm"}
	if m.versionAtLeast(2, 15) {
		args = append(args, "--sandbox")
	}
	args = append(args, "--", abs)
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, m.Command, args...)
	out := &limitedBuffer{limit: m.MaxOutputSize}
	cmd.Stdout = out
	cmd.Stderr = &limitedBuffer{limit: 4096}
	cmd.WaitDelay = time.Second
	cmdline := m.Command + " " + strings.Join(args, " ")
	if err := cmd.Run(); err != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			m.logger().Error("Markdownized preview generation timed out:\nCommand: " + cmdline)
			return false, nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			m.logger().Error(fmt.Sprintf("Markdownized preview generation failed (%d):\nCommand: %s", ee.ExitCode(), cmdline),
				"stderr", string(cmd.Stderr.(*limitedBuffer).buf))
			return false, nil
		}
		m.logger().Error("Markdownized preview generation failed:\nCommand: "+cmdline+"\nException was: "+err.Error())
		return false, nil
	}
	// 同じプレビューを並行して作っても互いの一時ファイルを壊さないよう、一時ファイル名は一意にする
	f, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.tmp")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	_, err = f.Write(out.buf)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, target)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// MarkdownizedPreviewsDir は Attachment.markdownized_previews_storage_path（機密を含みうるため、
// 添付の保存先の下の derived_cache/markdownized_previews に置く）。
func (s *Store) MarkdownizedPreviewsDir() string {
	return filepath.Join(s.Root, "derived_cache", "markdownized_previews")
}

// MarkdownizedPreviewCachePath は Attachment#markdownized_preview_cache_path（"#{digest}_#{filesize}.md"）。
// digest が 16 進文字列でなければ ""（保存先の外を指さないように）。
func (s *Store) MarkdownizedPreviewCachePath(a *domain.Attachment) string {
	if !reHexDigest.MatchString(a.Digest) {
		return ""
	}
	return filepath.Join(s.MarkdownizedPreviewsDir(), a.Digest+"_"+strconv.FormatInt(a.Filesize, 10)+".md")
}

// MarkdownizedPreviewable は Attachment#markdownized_previewable?。
func (s *Store) MarkdownizedPreviewable(a *domain.Attachment) bool {
	return s.Readable(a) && s.Markdownizer.Available() && s.Markdownizer.Supports(a.Filename)
}

// MarkdownizedPreviewContent は Attachment#markdownized_preview_content（変換できなければ nil, false）。
func (s *Store) MarkdownizedPreviewContent(ctx context.Context, a *domain.Attachment) ([]byte, bool) {
	if !s.MarkdownizedPreviewable(a) {
		return nil, false
	}
	target := s.MarkdownizedPreviewCachePath(a)
	if target == "" {
		return nil, false
	}
	ok, err := s.Markdownizer.Convert(ctx, s.Diskfile(a), target, a.Filename)
	if err == nil && ok {
		var b []byte
		if b, err = os.ReadFile(target); err == nil {
			return b, true
		}
	}
	if err != nil {
		s.logger().Error(fmt.Sprintf("An error occured while generating markdownized preview for %s to %s\nException was: %s",
			a.DiskFilename, target, err))
	}
	return nil, false
}

// ClearMarkdownizedPreviews は Attachment.clear_markdownized_previews（変換済みのプレビューをすべて消す）。
func (s *Store) ClearMarkdownizedPreviews() {
	matches, _ := filepath.Glob(filepath.Join(s.MarkdownizedPreviewsDir(), "*.md"))
	for _, f := range matches {
		_ = os.Remove(f)
	}
}

// deleteMarkdownizedPreview は delete_from_disk! のプレビューの削除（FileUtils.rm_f）。
func (s *Store) deleteMarkdownizedPreview(a *domain.Attachment) {
	if p := s.MarkdownizedPreviewCachePath(a); p != "" {
		_ = os.Remove(p)
	}
}
