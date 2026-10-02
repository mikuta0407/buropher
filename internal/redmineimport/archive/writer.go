package archive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

// Writer はアーカイブを書き出す。
//
// テーブル行は一時ディレクトリのファイルへストリーミングで書き、Close 時に
// manifest.json → tables/* → files/* の順で tar+zstd に書き出す(tar ヘッダにサイズが必要なため)。
// 添付ファイルは AddFile 時にハッシュを計算し、Close 時のコピーで再計算して一致を検証する。
type Writer struct {
	out     io.Writer
	tmpDir  string
	ownsTmp bool
	tables  []*TableWriter
	names   map[string]bool
	files   []File
	srcs    map[string]string // アーカイブ内パス → 元ファイル
	closed  bool
}

// NewWriter は out へ書き出す Writer を作る。tmpDir は一時ファイル置き場(空なら out と無関係に
// os.MkdirTemp("") を使う。大きなデータでは十分な空きのあるディレクトリを指定すること)。
func NewWriter(out io.Writer, tmpDir string) (*Writer, error) {
	d, err := os.MkdirTemp(tmpDir, ".buropher-archive-*")
	if err != nil {
		return nil, err
	}
	return &Writer{out: out, tmpDir: d, ownsTmp: true, names: map[string]bool{}, srcs: map[string]string{}}, nil
}

// TableWriter は 1 テーブル分の ndjson を書く。
type TableWriter struct {
	w       *Writer
	info    Table
	f       *os.File
	bw      *bufio.Writer
	h       hash.Hash
	size    int64
	keys    [][]byte // JSON エンコード済みの列名 + ':'
	buf     bytes.Buffer
	closed  bool
	enc     *json.Encoder
	scratch bytes.Buffer
}

// BeginTable はテーブルの書き込みを開始する。同時に開けるテーブルは 1 つでなくてもよい。
func (w *Writer) BeginTable(name string, columns []Column) (*TableWriter, error) {
	if w.closed {
		return nil, errors.New("archive: writer closed")
	}
	if !validName(name) {
		return nil, fmt.Errorf("archive: invalid table name %q", name)
	}
	if w.names[name] {
		return nil, fmt.Errorf("archive: duplicate table %q", name)
	}
	f, err := os.CreateTemp(w.tmpDir, "table-*.ndjson")
	if err != nil {
		return nil, err
	}
	w.names[name] = true
	tw := &TableWriter{
		w:    w,
		info: Table{Name: name, Path: TablesDir + name + ".ndjson", Columns: append([]Column(nil), columns...)},
		f:    f,
		h:    sha256.New(),
	}
	tw.bw = bufio.NewWriterSize(io.MultiWriter(f, tw.h), 256*1024)
	for i, c := range columns {
		k, _ := json.Marshal(c.Name)
		prefix := []byte{','}
		if i == 0 {
			prefix = []byte{'{'}
		}
		tw.keys = append(tw.keys, append(append(prefix, k...), ':'))
	}
	tw.enc = json.NewEncoder(&tw.scratch)
	tw.enc.SetEscapeHTML(false)
	w.tables = append(w.tables, tw)
	return tw, nil
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// WriteRow は 1 行を書く。values は BeginTable の columns と同じ順序・個数。
// 値の型: nil, bool, int64(int/int32 も可), float64, string, []byte(→ {"$b64": ...})。
func (tw *TableWriter) WriteRow(values []any) error {
	if tw.closed {
		return errors.New("archive: table closed")
	}
	if len(values) != len(tw.keys) {
		return fmt.Errorf("archive: table %s: %d values for %d columns", tw.info.Name, len(values), len(tw.keys))
	}
	tw.buf.Reset()
	if len(values) == 0 {
		tw.buf.WriteString("{")
	}
	for i, v := range values {
		tw.buf.Write(tw.keys[i])
		if err := tw.appendValue(v); err != nil {
			return fmt.Errorf("archive: table %s column %s: %w", tw.info.Name, tw.info.Columns[i].Name, err)
		}
	}
	tw.buf.WriteString("}\n")
	n, err := tw.bw.Write(tw.buf.Bytes())
	tw.size += int64(n)
	if err != nil {
		return err
	}
	tw.info.Rows++
	return nil
}

func (tw *TableWriter) appendValue(v any) error {
	b := &tw.buf
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int:
		b.WriteString(strconv.Itoa(x))
	case int32:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case uint64:
		b.WriteString(strconv.FormatUint(x, 10))
	case float64:
		b.WriteString(FormatFloat(x))
	case string:
		if !utf8.ValidString(x) {
			return errors.New("invalid UTF-8 string (use []byte)")
		}
		tw.scratch.Reset()
		if err := tw.enc.Encode(x); err != nil {
			return err
		}
		b.Write(bytes.TrimRight(tw.scratch.Bytes(), "\n"))
	case []byte:
		b.WriteString(`{"$b64":"`)
		b.WriteString(base64.StdEncoding.EncodeToString(x))
		b.WriteString(`"}`)
	default:
		return fmt.Errorf("unsupported value type %T", v)
	}
	return nil
}

// FormatFloat は浮動小数を JSON 表現にする。整数と区別できるよう必ず '.' か指数部を含める。
// NaN/±Inf は {"$float":"NaN"} 等で表す。
func FormatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return `{"$float":"NaN"}`
	case math.IsInf(f, 1):
		return `{"$float":"+Inf"}`
	case math.IsInf(f, -1):
		return `{"$float":"-Inf"}`
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// Close はテーブルの書き込みを終える。
func (tw *TableWriter) Close() error {
	if tw.closed {
		return nil
	}
	tw.closed = true
	if err := tw.bw.Flush(); err != nil {
		return err
	}
	tw.info.Size = tw.size
	tw.info.SHA256 = hex.EncodeToString(tw.h.Sum(nil))
	return tw.f.Close()
}

// Rows はこれまでに書いた行数を返す。
func (tw *TableWriter) Rows() int64 { return tw.info.Rows }

// AddFile は添付ファイルを登録する。relPath は files/ からの相対パス(disk_directory/disk_filename)。
// ここでサイズと SHA256 を計算し、実体は Close 時にコピーする。同じパスの重複登録は無視する。
func (w *Writer) AddFile(relPath, srcPath string) (File, error) {
	rel, err := CleanFilePath(relPath)
	if err != nil {
		return File{}, err
	}
	if _, dup := w.srcs[rel]; dup {
		for _, f := range w.files {
			if f.Path == rel {
				return f, nil
			}
		}
	}
	sum, size, err := hashFile(srcPath)
	if err != nil {
		return File{}, err
	}
	f := File{Path: rel, Size: size, SHA256: sum}
	w.files = append(w.files, f)
	w.srcs[rel] = srcPath
	return f, nil
}

// CleanFilePath は添付の相対パスを正規化し、アーカイブ外を指すパスを拒否する。
func CleanFilePath(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("archive: unsafe file path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("archive: unsafe file path %q", p)
		}
	}
	c := path.Clean(p)
	if c == "." {
		return "", fmt.Errorf("archive: unsafe file path %q", p)
	}
	return c, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Close はマニフェストと全エントリを書き出して一時ファイルを削除する。
// m の Format/FormatVersion/Tables/Files は Writer が設定する。out は閉じない。
func (w *Writer) Close(m *Manifest) (err error) {
	if w.closed {
		return errors.New("archive: writer already closed")
	}
	w.closed = true
	defer w.cleanup()
	for _, tw := range w.tables {
		if err := tw.Close(); err != nil {
			return err
		}
	}
	sort.Slice(w.tables, func(i, j int) bool { return w.tables[i].info.Name < w.tables[j].info.Name })
	m.Format = FormatName
	m.FormatVersion = FormatVersion
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	m.Tables = make([]Table, 0, len(w.tables))
	for _, tw := range w.tables {
		m.Tables = append(m.Tables, tw.info)
	}
	m.Files = append([]File{}, w.files...)
	if m.MissingFiles == nil {
		m.MissingFiles = []MissingFile{}
	}
	if m.Warnings == nil {
		m.Warnings = []string{}
	}
	if m.SchemaMigrations == nil {
		m.SchemaMigrations = []string{}
	}
	if m.PluginMigrations == nil {
		m.PluginMigrations = []PluginMigrations{}
	}
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	mj = append(mj, '\n')

	zw, err := zstd.NewWriter(w.out, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return err
	}
	defer func() {
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
	}()
	tw := tar.NewWriter(zw)
	mtime := m.CreatedAt.Truncate(time.Second)
	hdr := func(name string, size int64) *tar.Header {
		return &tar.Header{Typeflag: tar.TypeReg, Name: name, Size: size, Mode: 0o644, ModTime: mtime, Format: tar.FormatPAX}
	}
	if err := tw.WriteHeader(hdr(ManifestPath, int64(len(mj)))); err != nil {
		return err
	}
	if _, err := tw.Write(mj); err != nil {
		return err
	}
	for _, t := range w.tables {
		if err := copyEntry(tw, hdr(t.info.Path, t.info.Size), t.f.Name(), t.info.SHA256); err != nil {
			return fmt.Errorf("archive: table %s: %w", t.info.Name, err)
		}
	}
	for _, f := range w.files {
		if err := copyEntry(tw, hdr(FilesDir+f.Path, f.Size), w.srcs[f.Path], f.SHA256); err != nil {
			return fmt.Errorf("archive: file %s: %w", f.Path, err)
		}
	}
	return tw.Close()
}

func copyEntry(tw *tar.Writer, h *tar.Header, src, wantSum string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(tw, sum), io.LimitReader(f, h.Size))
	if err != nil {
		return err
	}
	if n != h.Size {
		return fmt.Errorf("size changed during export (%d → %d)", h.Size, n)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != wantSum {
		return errors.New("content changed during export (sha256 mismatch)")
	}
	return nil
}

// Abort は書き出しを中止して一時ファイルを削除する。
func (w *Writer) Abort() {
	if w.closed {
		return
	}
	w.closed = true
	for _, tw := range w.tables {
		if !tw.closed {
			tw.closed = true
			tw.f.Close()
		}
	}
	w.cleanup()
}

func (w *Writer) cleanup() {
	for _, tw := range w.tables {
		if !tw.closed {
			tw.f.Close()
		}
	}
	if w.ownsTmp {
		os.RemoveAll(w.tmpDir)
	}
}

// TempDir は一時ディレクトリのパスを返す(テスト用)。
func (w *Writer) TempDir() string { return filepath.Clean(w.tmpDir) }
