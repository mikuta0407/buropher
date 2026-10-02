package importer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// checkFiles は取り込んだ添付が参照するファイルの所在を確認し、
// アーカイブに含まれない場合は SourceFilesDir から staging へコピーする(コミット前)。
func (im *imp) checkFiles() error {
	fr := &im.rep.Files
	paths := make([]string, 0, len(im.st.attachmentPaths))
	for p := range im.st.attachmentPaths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	missing := func(p string) {
		fr.Missing++
		if len(fr.Samples) < maxSamples {
			fr.Samples = append(fr.Samples, fmt.Sprintf("%s (attachment %d)", p, im.st.attachmentPaths[p]))
		}
	}
	switch {
	case im.src.manifest.IncludeFiles:
		fr.Source = "archive"
		for _, p := range paths {
			if !im.src.inArchive[p] {
				missing(p)
			}
		}
	case im.opt.SourceFilesDir != "":
		fr.Source = "dir"
		for _, p := range paths {
			srcPath := filepath.Join(im.opt.SourceFilesDir, filepath.FromSlash(p))
			st, err := os.Stat(srcPath)
			if err != nil || !st.Mode().IsRegular() {
				missing(p)
				continue
			}
			if im.src.stageDir == "" {
				continue
			}
			dst := filepath.Join(im.src.stageDir, filepath.FromSlash(p))
			if err := copyFile(srcPath, dst); err != nil {
				return err
			}
			im.src.staged[p] = st.Size()
		}
	default:
		fr.Source = "none"
		if len(paths) > 0 {
			im.rep.warnf("the archive has no attachment files and no source files directory was given; %d attachment files were not copied", len(paths))
		}
		return nil
	}
	if im.opt.FilesDir == "" && len(paths) > 0 && !im.opt.DryRun {
		im.rep.warnf("no files directory was given; attachment files were not copied")
	}
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(dst, in)
}

// finishFiles は staging の添付を FilesDir の本来の位置へ移す(コミット後)。
// 既に同じ内容のファイルがあればそのまま、内容が異なれば上書きせず警告する。
func (im *imp) finishFiles() error {
	if im.src.stageDir == "" {
		return nil
	}
	fr := &im.rep.Files
	paths := make([]string, 0, len(im.src.staged))
	for p := range im.src.staged {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		staged := filepath.Join(im.src.stageDir, filepath.FromSlash(p))
		dst := filepath.Join(im.opt.FilesDir, filepath.FromSlash(p))
		if _, err := os.Stat(dst); err == nil {
			same, err := sameContent(staged, dst)
			if err != nil {
				return err
			}
			if same {
				fr.Skipped++
			} else {
				im.rep.warnf("attachment file %s already exists with different content; not overwritten", p)
			}
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(staged, dst); err != nil {
			return err
		}
		fr.Copied++
		fr.Bytes += im.src.staged[p]
	}
	return nil
}

func sameContent(a, b string) (bool, error) {
	ha, err := fileSHA256(a)
	if err != nil {
		return false, err
	}
	hb, err := fileSHA256(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

func fileSHA256(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
