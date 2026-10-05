// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package attachments

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // image.Decode に WebP を登録する

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/settings"
)

// このファイルは Attachment#thumbnail と Redmine::Thumbnail.generate の移植。
// ImageMagick（convert -thumbnail "NxN>"）の代わりに純 Go で縮小する。
// 出力形式は元画像と同じ（PNG / JPEG / GIF / BMP）。WebP は書き出せないため PNG で保存する。
// PDF のサムネイルは生成できない（常に失敗し、コントローラは 404 を返す）。

// thumbnailsDir は Attachment.thumbnails_storage_path。
func (s *Store) thumbnailsDir() string {
	if s.ThumbnailsRoot != "" {
		return s.ThumbnailsRoot
	}
	return filepath.Join(filepath.Dir(filepath.Clean(s.Root)), "tmp", "thumbnails")
}

// thumbnailPath は thumbnail_path(size)（"#{digest}_#{filesize}_#{size}.thumb"）。
// digest が 16 進文字列でなければ ""（取り込んだデータの digest に "../" や "*" があると、
// サムネイルの書き込み・削除がサムネイルの保存先の外に及ぶため）。
func (s *Store) thumbnailPath(a *domain.Attachment, size int) string {
	if !thumbnailDigestOK(a.Digest) {
		return ""
	}
	return filepath.Join(s.thumbnailsDir(), a.Digest+"_"+strconv.FormatInt(a.Filesize, 10)+"_"+strconv.Itoa(size)+".thumb")
}

// thumbnailDigestOK は digest がサムネイルのファイル名に使える（空または 16 進数字のみ）か。
func thumbnailDigestOK(d string) bool {
	for _, r := range d {
		if !('0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F') {
			return false
		}
	}
	return true
}

// ThumbnailSize は Attachment#thumbnail のサイズの正規化（50 単位に切り上げ、最大 800、
// 0 以下なら Setting.thumbnails_size、それも 0 以下なら 100）。
func (s *Store) ThumbnailSize(size int) int {
	if size > 0 {
		size = int(math.Ceil(float64(size)/50.0)) * 50
		if size > 800 {
			size = 800
		}
	} else if s.Settings != nil {
		size = int(settings.RubyToI(s.Settings.String("thumbnails_size")))
	}
	if size <= 0 {
		size = 100
	}
	return size
}

// reHexDigest は digest として受け付ける形式。digest はサムネイルのファイル名に使うため、
// 取り込んだデータなどで "../" を含む値が入っていても保存先の外を指さないよう 16 進に限る。
var reHexDigest = regexp.MustCompile(`\A[0-9a-fA-F]+\z`)

// thumbnailGenSem はサムネイルの同時生成数の上限。元画像の展開は 1 枚で最大約 200MB
// （maxThumbnailSourcePixels）使うため、サイズ違いを並行して要求されてもメモリを使い尽くさないようにする。
var thumbnailGenSem = make(chan struct{}, 2)

// Thumbnail は Attachment#thumbnail(:size => size)（生成済みならそのパス、生成できなければ false）。
// 生成の枠（thumbnailGenSem）を待つ間に ctx が終わった（クライアントが切断した等）場合は生成しない。
func (s *Store) Thumbnail(ctx context.Context, a *domain.Attachment, size int) (string, bool) {
	if !a.Thumbnailable() || !s.Readable(a) || a.IsPDF() || !reHexDigest.MatchString(a.Digest) {
		return "", false
	}
	size = s.ThumbnailSize(size)
	target := s.thumbnailPath(a, size)
	if target == "" {
		return "", false
	}
	if st, err := os.Stat(target); err == nil && st.Size() > 0 {
		return target, true
	}
	select {
	case thumbnailGenSem <- struct{}{}:
	case <-ctx.Done():
		return "", false
	}
	defer func() { <-thumbnailGenSem }()
	// 待っている間に他の要求が同じサムネイルを作っていればそれを使う
	if st, err := os.Stat(target); err == nil && st.Size() > 0 {
		return target, true
	}
	if err := s.generateThumbnail(s.Diskfile(a), target, size); err != nil {
		s.logger().Error("An error occured while generating thumbnail for "+a.DiskFilename+" to "+target, "err", err)
		return "", false
	}
	return target, true
}

// maxThumbnailSourcePixels はサムネイルを作る元画像の画素数の上限（RGBA で約 200MB）。
const maxThumbnailSourcePixels = 50_000_000

// generateThumbnail は Redmine::Thumbnail.generate(source, target, size)。
func (s *Store) generateThumbnail(source, target string, size int) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	// 画素数の上限を先に確かめる（ヘッダで巨大な寸法を宣言した小さな画像を展開すると、メモリを使い尽くす）
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxThumbnailSourcePixels {
		return fmt.Errorf("image too large to thumbnail (%dx%d)", cfg.Width, cfg.Height)
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// "NxN>": 幅・高さのどちらかが size を超える場合だけ、縦横比を保って縮小する
	if w > size || h > size {
		scale := math.Min(float64(size)/float64(w), float64(size)/float64(h))
		nw := max(1, int(math.Round(float64(w)*scale)))
		nh := max(1, int(math.Round(float64(h)*scale)))
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	var out bytes.Buffer
	switch format {
	case "jpeg":
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 85})
	case "gif":
		err = gif.Encode(&out, img, nil)
	case "bmp":
		err = bmp.Encode(&out, img)
	default:
		err = png.Encode(&out, img)
	}
	if err != nil {
		return err
	}
	// 同じサムネイルを並行して生成しても互いの一時ファイルを壊さないよう、一時ファイル名は一意にする
	f, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(out.Bytes())
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
	}
	return err
}

// deleteThumbnails は delete_from_disk! のサムネイル削除（Dir[thumbnail_path("*")]）。
func (s *Store) deleteThumbnails(a *domain.Attachment) {
	if !reHexDigest.MatchString(a.Digest) {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(s.thumbnailsDir(), a.Digest+"_"+strconv.FormatInt(a.Filesize, 10)+"_*.thumb"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
