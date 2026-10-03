// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

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
	"strings"

	"github.com/klauspost/compress/zstd"
)

// EntryKind はエントリの種類。
type EntryKind int

const (
	// KindTable は tables/<table>.ndjson。
	KindTable EntryKind = iota + 1
	// KindFile は files/<path>。
	KindFile
)

// Row はテーブルの 1 行。値の型: nil, bool, int64, float64, string, []byte。
// ndjson で "." / 指数部を含む数値は float64、それ以外の数値は int64 になる。
type Row map[string]any

// Reader はアーカイブを先頭から順に読む。エントリごとに SHA256 をマニフェストと照合する。
type Reader struct {
	closer   io.Closer
	zr       *zstd.Decoder
	tr       *tar.Reader
	manifest *Manifest
	cur      *Entry
	seen     map[string]bool
	tables   map[string]*Table
	files    map[string]*File
	done     bool
}

// Entry は 1 エントリ。Next を呼ぶと前のエントリは読めなくなる。
type Entry struct {
	Kind EntryKind
	// Name はテーブル名(KindTable)または files/ からの相対パス(KindFile)。
	Name string
	Size int64
	// Table は KindTable のときのマニフェスト情報。
	Table *Table
	// File は KindFile のときのマニフェスト情報。
	File *File

	r       *Reader
	hr      *hashingReader
	wantSum string
	rows    *Rows
}

// Open はアーカイブファイルを開き、マニフェストを読み込む。
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, err := newReader(f, f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

// NewReader は任意のストリームからアーカイブを読む。Close は src を閉じない。
func NewReader(src io.Reader) (*Reader, error) {
	return newReader(src, nil)
}

// maxZstdWindow は受け付ける zstd のウィンドウサイズの上限。Writer（SpeedDefault）は 8MB、
// zstd コマンドの --long（既定 27）は 128MB なので、それを超える要求は悪意のあるアーカイブとみなす
// （伸長時にウィンドウ分のメモリを確保させない）。
const maxZstdWindow = 1 << 28

// maxManifestBytes は manifest.json の大きさの上限（添付 100 万件でも数百 MB に収まる）。
const maxManifestBytes = 512 << 20

// MaxRowBytes は ndjson の 1 行の上限。巨大な 1 行で全体をメモリに読み込ませない（テストで変更する）。
var MaxRowBytes = 256 << 20

// ErrRowTooLong は 1 行が MaxRowBytes を超えた。
var ErrRowTooLong = errors.New("archive: ndjson line too long")

// ReadLine は改行までの 1 行を読む（改行を含む）。MaxRowBytes を超えたら ErrRowTooLong。
// 終端では最後の行と io.EOF を返す（bufio.Reader.ReadBytes('\n') と同じ）。
func ReadLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(line)+len(frag) > MaxRowBytes {
			return nil, ErrRowTooLong
		}
		line = append(line, frag...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func newReader(src io.Reader, closer io.Closer) (*Reader, error) {
	zr, err := zstd.NewReader(bufio.NewReaderSize(src, 1<<20), zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(maxZstdWindow), zstd.WithDecoderMaxMemory(maxZstdWindow))
	if err != nil {
		return nil, err
	}
	r := &Reader{closer: closer, zr: zr, tr: tar.NewReader(zr), seen: map[string]bool{}}
	h, err := r.tr.Next()
	if err != nil {
		zr.Close()
		return nil, fmt.Errorf("archive: reading first entry: %w", err)
	}
	if h.Name != ManifestPath {
		zr.Close()
		return nil, fmt.Errorf("archive: first entry is %q, want %s", h.Name, ManifestPath)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(r.tr, maxManifestBytes)).Decode(&m); err != nil {
		zr.Close()
		return nil, fmt.Errorf("archive: manifest: %w", err)
	}
	if m.Format != FormatName {
		zr.Close()
		return nil, fmt.Errorf("archive: unknown format %q", m.Format)
	}
	if m.FormatVersion != FormatVersion {
		zr.Close()
		return nil, fmt.Errorf("archive: unsupported format version %d (supported: %d)", m.FormatVersion, FormatVersion)
	}
	r.manifest = &m
	r.tables = map[string]*Table{}
	for i := range m.Tables {
		r.tables[m.Tables[i].Path] = &m.Tables[i]
	}
	r.files = map[string]*File{}
	for i := range m.Files {
		r.files[FilesDir+m.Files[i].Path] = &m.Files[i]
	}
	return r, nil
}

// Manifest はマニフェストを返す。
func (r *Reader) Manifest() *Manifest { return r.manifest }

// Next は次のエントリを返す。終端では io.EOF。前のエントリは未読部分を読み捨ててハッシュを検証する。
// 終端でマニフェスト記載のエントリが全て現れたことも検証する。
func (r *Reader) Next() (*Entry, error) {
	if err := r.finishCurrent(); err != nil {
		return nil, err
	}
	if r.done {
		return nil, io.EOF
	}
	for {
		h, err := r.tr.Next()
		if err == io.EOF {
			r.done = true
			for p := range r.tables {
				if !r.seen[p] {
					return nil, fmt.Errorf("archive: entry %s listed in manifest is missing", p)
				}
			}
			for p := range r.files {
				if !r.seen[p] {
					return nil, fmt.Errorf("archive: entry %s listed in manifest is missing", p)
				}
			}
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if r.seen[h.Name] {
			return nil, fmt.Errorf("archive: duplicate entry %s", h.Name)
		}
		e := &Entry{r: r, Size: h.Size}
		switch {
		case r.tables[h.Name] != nil:
			e.Kind, e.Table = KindTable, r.tables[h.Name]
			e.Name, e.wantSum = e.Table.Name, e.Table.SHA256
		case r.files[h.Name] != nil:
			e.Kind, e.File = KindFile, r.files[h.Name]
			e.Name, e.wantSum = e.File.Path, e.File.SHA256
		default:
			return nil, fmt.Errorf("archive: entry %s is not listed in manifest", h.Name)
		}
		// 展開する前にマニフェストのサイズと照合する（小さいと宣言して巨大な内容を書き出させない）
		if want := e.wantSize(); h.Size != want {
			return nil, fmt.Errorf("archive: size mismatch for %s (header %d, manifest %d)", h.Name, h.Size, want)
		}
		r.seen[h.Name] = true
		e.hr = &hashingReader{r: r.tr, h: sha256.New()}
		r.cur = e
		return e, nil
	}
}

// wantSize はマニフェストに記載されたサイズ。
func (e *Entry) wantSize() int64 {
	if e.Table != nil {
		return e.Table.Size
	}
	return e.File.Size
}

func (r *Reader) finishCurrent() error {
	e := r.cur
	if e == nil {
		return nil
	}
	r.cur = nil
	if _, err := io.Copy(io.Discard, e.hr); err != nil {
		return err
	}
	if got := hex.EncodeToString(e.hr.h.Sum(nil)); got != e.wantSum {
		return fmt.Errorf("archive: sha256 mismatch for %s", e.Name)
	}
	if e.hr.n != e.Size {
		return fmt.Errorf("archive: size mismatch for %s", e.Name)
	}
	return nil
}

// Close はリソースを解放する。
func (r *Reader) Close() error {
	r.zr.Close()
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

type hashingReader struct {
	r io.Reader
	h hash.Hash
	n int64
}

func (h *hashingReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	h.h.Write(p[:n])
	h.n += int64(n)
	return n, err
}

// Reader はエントリの生データを返す(ハッシュ検証は Next 時)。Rows と併用しないこと。
func (e *Entry) Reader() io.Reader { return e.hr }

// Rows は KindTable エントリの行イテレータを返す。
func (e *Entry) Rows() *Rows {
	if e.rows == nil {
		e.rows = &Rows{br: bufio.NewReaderSize(e.hr, 256*1024), table: e.Name}
		if e.Kind != KindTable {
			e.rows.err = errors.New("archive: not a table entry")
		}
	}
	return e.rows
}

// Rows は ndjson の行を 1 行ずつデコードする。
type Rows struct {
	br    *bufio.Reader
	table string
	row   Row
	err   error
	line  int64
}

// Next は次の行へ進む。終端またはエラーで false。
func (rs *Rows) Next() bool {
	if rs.err != nil {
		return false
	}
	line, err := ReadLine(rs.br)
	if errors.Is(err, ErrRowTooLong) {
		rs.err = fmt.Errorf("%w: %s line %d", err, rs.table, rs.line+1)
		return false
	}
	if len(bytes.TrimSpace(line)) == 0 {
		if err != nil && !errors.Is(err, io.EOF) {
			rs.err = err
		}
		if err == nil {
			// 空行は許容しない
			rs.err = fmt.Errorf("archive: %s: empty line %d", rs.table, rs.line+1)
		}
		return false
	}
	rs.line++
	row, derr := DecodeRow(line)
	if derr != nil {
		rs.err = fmt.Errorf("archive: %s line %d: %w", rs.table, rs.line, derr)
		return false
	}
	rs.row = row
	return true
}

// Row は現在の行を返す。
func (rs *Rows) Row() Row { return rs.row }

// Err は反復中のエラーを返す。
func (rs *Rows) Err() error { return rs.err }

// DecodeRow は ndjson の 1 行をデコードする。
func DecodeRow(line []byte) (Row, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	row := make(Row, len(raw))
	for k, v := range raw {
		cv, err := convertValue(v)
		if err != nil {
			return nil, fmt.Errorf("column %s: %w", k, err)
		}
		row[k] = cv
	}
	return row, nil
}

func convertValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case json.Number:
		s := string(x)
		if strings.ContainsAny(s, ".eE") {
			return x.Float64()
		}
		return x.Int64()
	case map[string]any:
		if b, ok := x["$b64"].(string); ok && len(x) == 1 {
			return base64.StdEncoding.DecodeString(b)
		}
		if f, ok := x["$float"].(string); ok && len(x) == 1 {
			switch f {
			case "NaN":
				return math.NaN(), nil
			case "+Inf":
				return math.Inf(1), nil
			case "-Inf":
				return math.Inf(-1), nil
			}
		}
	}
	return nil, fmt.Errorf("unexpected value %#v", v)
}

// ReadTable はアーカイブを開いて指定テーブルの全行を fn に渡す。
// テーブルは添付ファイルより前に格納されているため、添付部分は伸長しない。
func ReadTable(path, table string, fn func(Row) error) error {
	r, err := Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	if _, ok := r.manifest.Table(table); !ok {
		return fmt.Errorf("archive: table %s not in archive", table)
	}
	for {
		e, err := r.Next()
		if err != nil {
			return err
		}
		if e.Kind != KindTable || e.Name != table {
			continue
		}
		rows := e.Rows()
		for rows.Next() {
			if err := fn(rows.Row()); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// ハッシュ検証のため次へ進める
		return r.finishCurrent()
	}
}
