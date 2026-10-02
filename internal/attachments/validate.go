package attachments

import (
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/mimetype"
	"github.com/mikuta0407/buropher/internal/validation"
)

// MaxSizeBytes は Setting.attachment_max_size.to_i.kilobytes。
func (s *Store) MaxSizeBytes() int64 {
	if s.Settings == nil {
		return 5120 * 1024
	}
	return int64(s.Settings.Int("attachment_max_size")) * 1024
}

// Validate は Attachment の検証（validates_presence_of :filename, :author / validates_length_of
// :filename, :description（255）/ validate_max_file_size / validate_file_extension）。
//
// newFileSize は新しく保存するファイル（@temp_file）のサイズで、無ければ -1（最大サイズを検証しない）。
// 事前にサイズが分からないアップロードでは 0 以上の任意の値ではなく -1 を渡し、書き込み後に再検証する
// （Create が行う）。filenameChanged は filename_changed?（拡張子の検証を行うか）。
func (s *Store) Validate(a *domain.Attachment, newFileSize int64, filenameChanged bool, l *i18n.Localizer) *validation.Errors {
	errs := validation.New("attachment")
	if strings.TrimSpace(a.Filename) == "" {
		errs.Add("filename", "blank")
	}
	if a.AuthorID == 0 && a.Author == nil {
		errs.Add("author", "blank")
	}
	if n := len([]rune(a.Filename)); n > 255 {
		errs.Add("filename", "too_long", "count", 255)
	}
	if n := len([]rune(a.DiskFilename)); n > 255 {
		errs.Add("disk_filename", "too_long", "count", 255)
	}
	if n := len([]rune(a.Description)); n > 255 {
		errs.Add("description", "too_long", "count", 255)
	}
	// validate_max_file_size（エラーメッセージの max_size はバイト数そのまま）
	if newFileSize >= 0 && newFileSize > s.MaxSizeBytes() {
		errs.AddMessage("base", tr(l, "error_attachment_too_big", i18n.Vars{"max_size": strconv.FormatInt(s.MaxSizeBytes(), 10)}))
	}
	// validate_file_extension（:if => :filename_changed?）
	if filenameChanged {
		ext := mimetype.Extname(a.Filename)
		if !s.ValidExtension(ext) {
			errs.AddMessage("base", tr(l, "error_attachment_extension_not_allowed", i18n.Vars{"extension": ext}))
		}
	}
	return errs
}

func tr(l *i18n.Localizer, key string, vars i18n.Vars) string {
	if l == nil {
		return key
	}
	return l.L(key, vars)
}

// ValidExtension は Attachment.valid_extension?(extension)（拒否リストに含まれず、許可リストが
// あればそれに含まれる）。
func (s *Store) ValidExtension(ext string) bool {
	if s.Settings == nil {
		return true
	}
	denied := s.Settings.String("attachment_extensions_denied")
	allowed := s.Settings.String("attachment_extensions_allowed")
	if strings.TrimSpace(denied) != "" && ExtensionIn(ext, denied) {
		return false
	}
	if strings.TrimSpace(allowed) != "" && !ExtensionIn(ext, allowed) {
		return false
	}
	return true
}

// ExtensionIn は Attachment.extension_in?(extension, extensions)（extensions はカンマ区切り。
// 大文字小文字・先頭のドットを無視）。
func ExtensionIn(ext, extensions string) bool {
	ext = strings.TrimLeft(strings.ToLower(ext), ".")
	for _, e := range strings.Split(extensions, ",") {
		e = strings.TrimLeft(strings.ToLower(strings.TrimSpace(e)), ".")
		if e != "" && e == ext {
			return true
		}
	}
	return false
}
