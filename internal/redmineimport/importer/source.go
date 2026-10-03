package importer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mikuta0407/buropher/internal/redmineimport/archive"
)

// source はアーカイブを一度だけ先頭から読み、テーブルを一時ディレクトリへ展開したもの。
// 変換は FK 依存順に行うため(アーカイブはテーブル名順)、テーブルは展開後に任意の順で何度でも読める。
// 添付ファイル実体は stageDir が指定されていればそこへ書き出す(コミット後に本来の場所へ移す)。
type source struct {
	dir      string
	manifest *archive.Manifest
	tables   map[string]string
	stageDir string
	// staged はアーカイブから展開した添付(files/ からの相対パス → サイズ)。
	staged map[string]int64
	// inArchive はアーカイブに含まれる添付パス(stage しない場合も記録)。
	inArchive map[string]bool
}

func openSource(ctx context.Context, archivePath, tempDir, stageDir string) (*source, error) {
	r, err := archive.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	m := r.Manifest()
	if m.Format != archive.FormatName {
		return nil, fmt.Errorf("importer: %s is not a buropher Redmine export (format %q)", archivePath, m.Format)
	}
	if m.FormatVersion != archive.FormatVersion {
		return nil, fmt.Errorf("importer: unsupported archive format_version %d (want %d)", m.FormatVersion, archive.FormatVersion)
	}
	dir, err := os.MkdirTemp(tempDir, "buropher-import-")
	if err != nil {
		return nil, err
	}
	s := &source{dir: dir, manifest: m, tables: map[string]string{}, stageDir: stageDir,
		staged: map[string]int64{}, inArchive: map[string]bool{}}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch e.Kind {
		case archive.KindTable:
			p := filepath.Join(dir, e.Name+".ndjson")
			if err := writeFile(p, e.Reader()); err != nil {
				return nil, err
			}
			s.tables[e.Name] = p
		case archive.KindFile:
			rel, err := cleanRelPath(e.Name)
			if err != nil {
				return nil, err
			}
			s.inArchive[rel] = true
			if stageDir == "" {
				continue
			}
			p := filepath.Join(stageDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, err
			}
			if err := writeFile(p, e.Reader()); err != nil {
				return nil, err
			}
			s.staged[rel] = e.Size
		}
	}
	ok = true
	return s, nil
}

func writeFile(p string, r io.Reader) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// cleanRelPath は添付の相対パスを検証する(絶対パス・.. を拒否)。
func cleanRelPath(p string) (string, error) {
	c := path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if c == "." || strings.HasPrefix(c, "/") || c == ".." || strings.HasPrefix(c, "../") || strings.Contains(c, "/../") {
		return "", fmt.Errorf("importer: unsafe attachment path %q", p)
	}
	return c, nil
}

func (s *source) close() {
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
}

// has はテーブルがアーカイブに含まれるか。
func (s *source) has(table string) bool { _, ok := s.tables[table]; return ok }

// rowCount はマニフェスト上の行数。
func (s *source) rowCount(table string) int64 {
	if t, ok := s.manifest.Table(table); ok {
		return t.Rows
	}
	return 0
}

// errStop は each の反復を途中で止める(エラー扱いしない)。
var errStop = errors.New("stop")

// each はテーブルの全行を順に fn へ渡す。テーブルがなければ何もしない。
func (s *source) each(table string, fn func(rec) error) error {
	p, ok := s.tables[table]
	if !ok {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	var n int64
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			n++
			row, derr := archive.DecodeRow(line)
			if derr != nil {
				return fmt.Errorf("importer: %s line %d: %w", table, n, derr)
			}
			if ferr := fn(rec{row}); ferr != nil {
				if errors.Is(ferr, errStop) {
					return nil
				}
				return ferr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// all はテーブルの全行をメモリに読み込む(小さいマスタテーブル用)。
func (s *source) all(table string) ([]rec, error) {
	var out []rec
	err := s.each(table, func(r rec) error { out = append(out, r); return nil })
	return out, err
}
