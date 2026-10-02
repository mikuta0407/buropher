package attachments

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
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
func (s *Store) thumbnailPath(a *domain.Attachment, size int) string {
	return filepath.Join(s.thumbnailsDir(), a.Digest+"_"+strconv.FormatInt(a.Filesize, 10)+"_"+strconv.Itoa(size)+".thumb")
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

// Thumbnail は Attachment#thumbnail(:size => size)（生成済みならそのパス、生成できなければ false）。
func (s *Store) Thumbnail(a *domain.Attachment, size int) (string, bool) {
	if !a.Thumbnailable() || !s.Readable(a) || a.IsPDF() {
		return "", false
	}
	size = s.ThumbnailSize(size)
	target := s.thumbnailPath(a, size)
	if st, err := os.Stat(target); err == nil && st.Size() > 0 {
		return target, true
	}
	if err := s.generateThumbnail(s.Diskfile(a), target, size); err != nil {
		s.logger().Error("An error occured while generating thumbnail for "+a.DiskFilename+" to "+target, "err", err)
		return "", false
	}
	return target, true
}

// generateThumbnail は Redmine::Thumbnail.generate(source, target, size)。
func (s *Store) generateThumbnail(source, target string, size int) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
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
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// deleteThumbnails は delete_from_disk! のサムネイル削除（Dir[thumbnail_path("*")]）。
func (s *Store) deleteThumbnails(a *domain.Attachment) {
	matches, _ := filepath.Glob(filepath.Join(s.thumbnailsDir(), a.Digest+"_"+strconv.FormatInt(a.Filesize, 10)+"_*.thumb"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
