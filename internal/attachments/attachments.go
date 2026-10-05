// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package attachments は Redmine の Attachment モデル（app/models/attachment.rb）のうち、
// ディスク上のファイルの保存・検証・削除と、acts_as_attachable の save_attachments /
// attach_saved_attachments（Attachment.attach_files）の移植。
//
// 行の読み書き（SQL）は internal/repository/attachments.go、モデルの型と DB を要しない判定
// （image?、token など）は domain.Attachment にある。
//
// ディスク上の配置は Redmine と同一（Root/disk_directory/disk_filename、disk_directory は作成時刻の
// "YYYY/MM"、disk_filename は "yymmddHHMMSS_<ASCII のファイル名またはその MD5>"）。
// そのため Redmine の files ディレクトリをそのまま Root に指定できる。
package attachments

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/mimetype"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/validation"
)

// Store は添付ファイルの保存先（Attachment.storage_path）と設定。
type Store struct {
	// Root は保存先のディレクトリ（config の storage.attachments_path）。
	Root string
	// Settings は attachment_max_size / attachment_extensions_allowed / attachment_extensions_denied の参照先。
	Settings *settings.Settings
	// Now は現在時刻（nil なら clock.Now）。ディスク上のディレクトリ名・ファイル名の時刻に使う。
	Now func() time.Time
	// Logger はログ出力先（nil なら slog.Default()）。
	Logger *slog.Logger
	// ThumbnailsRoot はサムネイルの保存先（Attachment.thumbnails_storage_path。空なら Root の親の tmp/thumbnails）。
	ThumbnailsRoot string
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return clock.Now()
}

func (s *Store) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Upload は新しく保存するファイル（Attachment.new(:file => ...) に渡すもの）。
type Upload struct {
	// Filename は元のファイル名（original_filename。SanitizeFilename される）。
	Filename string
	// ContentType は指定された Content-Type（空なら拡張子から決める）。
	ContentType string
	// Body はファイルの内容。
	Body io.Reader
	// Size はバイト数（不明なら -1。その場合は書き込み後に最大サイズを検証する）。
	Size int64
}

var (
	reSanitizePath = regexp.MustCompile(`(?s)\A.*(\\|/)`)
	// Redmine の [\/\?\%\*\:\|\"\'<>\n\r]+ に、タブ以外の制御文字（NUL など）を加えたもの。
	// NUL を含む名前は PostgreSQL に保存できず、メール受信・アップロードが失敗するため
	reSanitizeChars = regexp.MustCompile(`[/?%*:|"'<>\n\r\x00-\x08\x0b\x0c\x0e-\x1f\x7f]+`)
)

// SanitizeFilename は Attachment#sanitize_filename（パス部分を除き、/ ? % * : | " ' < > 改行を _ にする）。
func SanitizeFilename(value string) string {
	just := reSanitizePath.ReplaceAllString(value, "")
	return reSanitizeChars.ReplaceAllString(just, "_")
}

var (
	reASCIIFilename = regexp.MustCompile(`^[a-zA-Z0-9_.\-]*$`)
	reFilenameExt   = regexp.MustCompile(`(\.[a-zA-Z0-9]+)$`)
)

// DiskFilenameBase は Attachment.create_diskfile の "<timestamp>_" に続く部分。
// ASCII（英数字 _ . -）のみで 50 文字以下ならそのまま、それ以外は ActiveSupport::Digest.hexdigest
// （Redmine は load_defaults を呼ばないため MD5 の 16 進 32 文字）に元の拡張子を付けたもの。
func DiskFilenameBase(filename string) string {
	const maxFilenameLength = 50
	if reASCIIFilename.MatchString(filename) && len(filename) <= maxFilenameLength {
		return filename
	}
	sum := md5.Sum([]byte(filename))
	ascii := hex.EncodeToString(sum[:])
	// 拡張子は MD5 と合わせて 50 文字以下のときだけ残す（長すぎる拡張子でディスク上のファイル名が
	// 長くなりすぎ、保存に失敗しないように。Redmine 7.0 の #44186）
	if m := reFilenameExt.FindStringSubmatch(filename); m != nil && len(ascii)+len(m[1]) <= maxFilenameLength {
		ascii += m[1]
	}
	return ascii
}

// TargetDirectory は Attachment#target_directory（"%Y/%m"）。
func TargetDirectory(t time.Time) string { return t.Format("2006/01") }

// DiskTimestamp は create_diskfile のタイムスタンプ（"%y%m%d%H%M%S"）。
func DiskTimestamp(t time.Time) string { return t.Format("060102150405") }

// succDigits は数字だけの文字列の String#succ（"0099" → "0100"、"99" → "100"）。
func succDigits(s string) string {
	b := []byte(s)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < '9' {
			b[i]++
			return string(b)
		}
		b[i] = '0'
	}
	return "1" + string(b)
}

var reToken = regexp.MustCompile(`^(\d+)\.([0-9a-f]+)$`)

// ParseToken は Attachment.find_by_token のトークン（"#{id}.#{digest}"）を分解する。
func ParseToken(token string) (id int64, digest string, ok bool) {
	m := reToken.FindStringSubmatch(token)
	if m == nil {
		return 0, "", false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return id, m[2], true
}

// FindByToken は Attachment.find_by_token（未紐付けの添付のみ。無ければ nil, nil）。
func FindByToken(ctx context.Context, q db.Queryer, token string) (*domain.Attachment, error) {
	id, digest, ok := ParseToken(token)
	if !ok {
		return nil, nil
	}
	a, err := repository.FindUnattachedAttachment(ctx, q, id, digest)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	return a, err
}

// Diskfile は Attachment#diskfile（Root/disk_directory/disk_filename）。
// disk_directory / disk_filename が保存先の外を指す（".." や絶対パスを含む）場合は ""（開けない・消せない）。
func (s *Store) Diskfile(a *domain.Attachment) string {
	rel := filepath.Join(filepath.FromSlash(a.DiskDirectory), filepath.FromSlash(a.DiskFilename))
	if !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.Join(s.Root, rel)
}

// Readable は Attachment#readable?（disk_filename があり、ファイルを読める）。
func (s *Store) Readable(a *domain.Attachment) bool {
	if a.DiskFilename == "" {
		return false
	}
	f, err := os.Open(s.Diskfile(a))
	if err != nil {
		return false
	}
	st, err := f.Stat()
	_ = f.Close()
	return err == nil && st.Mode().IsRegular()
}

// Open はダウンロード用にファイルを開く。
func (s *Store) Open(a *domain.Attachment) (*os.File, error) {
	if a.DiskFilename == "" {
		return nil, os.ErrNotExist
	}
	return os.Open(s.Diskfile(a))
}

// createDiskfile は Attachment.create_diskfile: 一意なファイル名を確保して開く。
func (s *Store) createDiskfile(filename, directory string) (*os.File, error) {
	timestamp := DiskTimestamp(s.now().In(time.Local))
	ascii := DiskFilenameBase(filename)
	dir := filepath.Join(s.Root, filepath.FromSlash(directory))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for {
		name := timestamp + "_" + ascii
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, os.ErrExist) {
			timestamp = succDigits(timestamp)
			continue
		}
		return f, err
	}
}

// writeFile は files_to_final_location のファイル書き込み部分。disk_directory・disk_filename・
// digest・filesize を設定する。
func (s *Store) writeFile(a *domain.Attachment, body io.Reader) error {
	a.DiskDirectory = TargetDirectory(s.now().In(time.Local))
	f, err := s.createDiskfile(a.Filename, a.DiskDirectory)
	if err != nil {
		return err
	}
	a.DiskFilename = filepath.Base(f.Name())
	sha := sha256.New()
	var n int64
	if body != nil {
		n, err = io.Copy(io.MultiWriter(f, sha), body)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	s.logger().Info(fmt.Sprintf("Saving attachment '%s' (%d bytes)", s.Diskfile(a), n))
	a.Filesize = n
	a.Digest = hex.EncodeToString(sha.Sum(nil))
	a.DigestAlgo = "sha256"
	return nil
}

// finalizeContentType は files_to_final_location の content_type の補完（空なら拡張子から、
// 255 文字を超えるなら保存しない）。
func finalizeContentType(a *domain.Attachment) {
	if strings.TrimSpace(a.ContentType) == "" && a.Filename != "" {
		a.ContentType = mimetype.Of(a.Filename)
	}
	if len([]rune(a.ContentType)) > 255 {
		a.ContentType = ""
	}
}

// New は Attachment.new(:file => up, :author => author) 相当の未保存の添付を作る。
func New(up Upload, author *domain.User) *domain.Attachment {
	a := &domain.Attachment{
		Filename:    SanitizeFilename(up.Filename),
		ContentType: strings.TrimRight(up.ContentType, "\r\n"),
		Filesize:    up.Size,
		// description は nil（attachments.description に既定値は無い）
		DescriptionNull: true,
	}
	if author != nil {
		a.AuthorID = author.ID
		a.Author = author
	}
	return a
}

// Create は Attachment.create(:file => up, :author => author)（attachment.save）。
// 検証に失敗した場合は未保存の添付（NewRecord）とエラーを返す（ファイルは残さない）。
// err は DB・ディスクの障害のみ。
//
// q がトランザクションでロールバックされた場合、呼び出し側は DeleteFromDisk で
// 書き込んだファイルを消すこと（after_rollback :delete_from_disk）。
func (s *Store) Create(ctx context.Context, q db.Queryer, up Upload, author *domain.User, l *i18n.Localizer) (*domain.Attachment, *validation.Errors, error) {
	a := New(up, author)
	if errs := s.Validate(a, up.Size, true, l); errs.Any() {
		return a, errs, nil
	}
	body := up.Body
	if up.Size < 0 && body != nil {
		// サイズが分からない（chunked）本文は上限 + 1 バイトで打ち切る。超えた分は書き込み後の
		// 検証で too big になる（上限なしに書き続けてディスクを埋められないように）。
		// 上限 0（以下）は空でないファイルをすべて拒否する設定なので、1 バイトで打ち切る
		body = io.LimitReader(body, max(s.MaxSizeBytes(), 0)+1)
	}
	if err := s.writeFile(a, body); err != nil {
		return a, nil, err
	}
	if up.Size < 0 {
		// サイズが事前に分からなかった場合は書き込み後に検証する
		if errs := s.Validate(a, a.Filesize, true, l); errs.Any() {
			_ = os.Remove(s.Diskfile(a))
			a.DiskFilename, a.DiskDirectory, a.Digest, a.DigestAlgo = "", "", "", ""
			return a, errs, nil
		}
	}
	finalizeContentType(a)
	if err := repository.InsertAttachment(ctx, q, a); err != nil {
		_ = os.Remove(s.Diskfile(a))
		a.ID = 0
		return a, nil, err
	}
	if err := s.reuseExistingFileIfPossible(ctx, q, a); err != nil {
		// 重複排除に失敗しても致命的ではない（Redmine もロックエラー等を無視する）
		s.logger().Warn("reuse existing attachment file", "id", a.ID, "err", err)
	}
	return a, nil, nil
}

// reuseExistingFileIfPossible は Attachment#reuse_existing_file_if_possible（after_commit on create）:
// digest と filesize が同じ既存の添付があり内容が同一なら、そのファイルを共有して今回のファイルを消す。
func (s *Store) reuseExistingFileIfPossible(ctx context.Context, q db.Queryer, a *domain.Attachment) error {
	if a.Digest == "" {
		return nil
	}
	existing, err := repository.FindReusableAttachment(ctx, q, a.Digest, a.Filesize, a.DiskFilename)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// existing.with_lock: 比較と付け替えの間に既存の添付が削除されないよう行をロックする（PostgreSQL。
	// トランザクション外では効かないので、付け替えも既存の行がある場合だけ行う）
	if ok, err := repository.LockAttachment(ctx, q, existing.ID); err != nil || !ok {
		return err
	}
	original := s.Diskfile(a)
	originalName := a.DiskFilename
	same, err := identicalFiles(original, s.Diskfile(existing))
	if err != nil || !same {
		return nil //nolint:nilerr // 比較できなければ重複排除しない
	}
	if ok, err := repository.ReuseAttachmentDiskfile(ctx, q, a.ID, existing.ID, existing.DiskDirectory, existing.DiskFilename); err != nil || !ok {
		// 既存の添付が削除された（そのファイルは削除側が消す）なら自分のファイルを使い続ける
		return err
	}
	a.DiskDirectory, a.DiskFilename = existing.DiskDirectory, existing.DiskFilename
	n, err := repository.AttachmentsSharingDiskfile(ctx, q, originalName, 0)
	if err != nil {
		return err
	}
	if n == 0 {
		return os.Remove(original)
	}
	return nil
}

// identicalFiles は FileUtils.identical?（内容が同一か）。
func identicalFiles(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	sa, err := fa.Stat()
	if err != nil {
		return false, err
	}
	sb, err := fb.Stat()
	if err != nil {
		return false, err
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	ba, bb := make([]byte, 32*1024), make([]byte, 32*1024)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		if ea != nil || eb != nil {
			done := func(e error) bool { return errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) }
			if done(ea) && done(eb) {
				return true, nil
			}
			if !done(ea) && ea != nil {
				return false, ea
			}
			if !done(eb) && eb != nil {
				return false, eb
			}
		}
	}
}

// DeleteFromDisk は Attachment#delete_from_disk: 同じ disk_filename を参照する他の添付が無ければ
// ディスク上のファイルとサムネイルを消す。行を削除したトランザクションのコミット後に呼ぶ（after_commit on destroy）。
func (s *Store) DeleteFromDisk(ctx context.Context, q db.Queryer, atts ...*domain.Attachment) error {
	var firstErr error
	for _, a := range atts {
		if a == nil || a.DiskFilename == "" {
			continue
		}
		n, err := repository.AttachmentsSharingDiskfile(ctx, q, a.DiskFilename, a.ID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if n > 0 {
			continue
		}
		// delete_from_disk!（FileUtils.rm_f とサムネイルの削除）
		if err := os.Remove(s.Diskfile(a)); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
		s.deleteThumbnails(a)
	}
	return firstErr
}

// Destroy は attachment.destroy（行を削除し、他に参照が無ければファイルも消す）。
// q がトランザクションの場合はファイルの削除が先に行われるので、トランザクション内では
// repository.DeleteAttachment を使い、コミット後に DeleteFromDisk を呼ぶこと。
func (s *Store) Destroy(ctx context.Context, q db.Queryer, a *domain.Attachment) error {
	if err := repository.DeleteAttachment(ctx, q, a.ID); err != nil {
		return err
	}
	return s.DeleteFromDisk(ctx, q, a)
}

// RandomHex は Redmine::Utils.random_hex(n)（n バイトの乱数の 16 進表記）。
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
