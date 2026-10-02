package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/mimetype"
	"github.com/mikuta0407/buropher/internal/textformat/highlight"
)

// 添付ファイルのコンテナ種別（attachments.container_kind。Redmine の container_type を小文字化したもの）。
const (
	AttachmentContainerIssue       = "issue"
	AttachmentContainerProject     = "project"
	AttachmentContainerVersion     = "version"
	AttachmentContainerWikiPage    = "wiki_page"
	AttachmentContainerMessage     = "message"
	AttachmentContainerNews        = "news"
	AttachmentContainerDocument    = "document"
	AttachmentContainerCustomValue = "custom_value"
)

// ThumbnailsAvailable は Redmine::Thumbnail.convert_available?（サムネイルを生成できるか）。
var ThumbnailsAvailable = true

// ThumbnailPDFAvailable は Redmine::Thumbnail.gs_available?（PDF のサムネイルを生成できるか）。
var ThumbnailPDFAvailable = true

// Attachment は attachments の行（Attachment モデル）。
type Attachment struct {
	ID int64
	// ContainerKind は container_type（未紐付けなら ""）。
	ContainerKind string
	// ContainerID は container_id（未紐付けなら nil）。
	ContainerID   *int64
	Filename      string
	DiskDirectory string
	DiskFilename  string
	Filesize      int64
	ContentType   string
	Digest        string
	// DigestAlgo は "sha256" または "md5"（Redmine 3.4 より前の添付）。
	DigestAlgo  string
	Downloads   int
	AuthorID    int64
	Description string
	// CreatedOn は created_on。
	CreatedOn time.Time

	// Author は author（読み込んだ場合のみ）。
	Author *User
}

// NewRecord は new_record?（未保存）。
func (a *Attachment) NewRecord() bool { return a.ID == 0 }

// Attached は container が設定されているか（container_id.present?）。
func (a *Attachment) Attached() bool { return a.ContainerID != nil }

// Token は Attachment#token（"#{id}.#{digest}"）。
func (a *Attachment) Token() string { return strconv.FormatInt(a.ID, 10) + "." + a.Digest }

// Title は Attachment#title（説明があれば "filename (description)"）。
func (a *Attachment) Title() string {
	if strings.TrimSpace(a.Description) != "" {
		return a.Filename + " (" + a.Description + ")"
	}
	return a.Filename
}

var reImageFilename = regexp.MustCompile(`(?i)\.(bmp|gif|jpg|jpe|jpeg|png|webp)$`)

// IsImage は Attachment#image?（拡張子が bmp/gif/jpg/jpe/jpeg/png/webp）。
// is_image?（MIME タイプが image/*）は IsImageType。
func (a *Attachment) IsImage() bool { return reImageFilename.MatchString(a.Filename) }

// Thumbnailable は Attachment#thumbnailable?。
func (a *Attachment) Thumbnailable() bool {
	return ThumbnailsAvailable && (a.IsImage() || (a.IsPDF() && ThumbnailPDFAvailable))
}

// IsText は Attachment#is_text?。
func (a *Attachment) IsText() bool {
	return mimetype.IsType("text", a.Filename) || highlight.FilenameSupported(a.Filename)
}

// IsMarkdown は Attachment#is_markdown?。
func (a *Attachment) IsMarkdown() bool { return mimetype.Of(a.Filename) == "text/markdown" }

// IsTextile は Attachment#is_textile?。
func (a *Attachment) IsTextile() bool { return mimetype.Of(a.Filename) == "text/x-textile" }

// IsImageType は Attachment#is_image?（MIME タイプが image/*）。image? は IsImage。
func (a *Attachment) IsImageType() bool { return mimetype.IsType("image", a.Filename) }

var reDiffFilename = regexp.MustCompile(`(?i)\.(patch|diff)$`)

// IsDiff は Attachment#is_diff?。
func (a *Attachment) IsDiff() bool { return reDiffFilename.MatchString(a.Filename) }

// IsPDF は Attachment#is_pdf?。
func (a *Attachment) IsPDF() bool { return mimetype.Of(a.Filename) == "application/pdf" }

// IsVideo は Attachment#is_video?。
func (a *Attachment) IsVideo() bool { return mimetype.IsType("video", a.Filename) }

// IsAudio は Attachment#is_audio?。
func (a *Attachment) IsAudio() bool { return mimetype.IsType("audio", a.Filename) }

// Previewable は Attachment#previewable?。
func (a *Attachment) Previewable() bool {
	return a.IsText() || a.IsImageType() || a.IsVideo() || a.IsAudio()
}

// DigestType は Attachment#digest_type（"MD5" / "SHA256"、digest が無ければ ""）。
func (a *Attachment) DigestType() string {
	if a.Digest == "" {
		return ""
	}
	if len(a.Digest) < 64 {
		return "MD5"
	}
	return "SHA256"
}
