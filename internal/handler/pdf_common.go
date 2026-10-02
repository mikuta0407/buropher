package handler

// PDF 出力の共通部分（Redmine::Export::PDF::ITCPDF の生成・send_file_headers!・get_image_filename）。

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/pdf"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
)

// newPDF は ITCPDF.new(current_language, orientation) と footer_date = format_date(User.current.today)。
func (a *App) newPDF(c *Req, orientation string) *pdf.Doc {
	fs := a.PDFFonts
	if fs == nil {
		fs = pdf.Default()
	}
	d := pdf.New(fs, pdf.Options{Locale: c.Loc.Lang, Orientation: orientation, CreationDate: a.now()})
	d.FooterDate = c.Loc.FormatDate(a.userToday(c))
	return d
}

// renderPDF は build で PDF を作り、send_file_headers!(:type => 'application/pdf', :filename => filename) で返す。
// filename_for_content_disposition を通すとき（wiki#show）は fcd を真にする。
func (a *App) renderPDF(c *Req, filename string, fcd bool, build func() (*pdf.Doc, error)) {
	data, err := func() (data []byte, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("pdf: %v", r)
			}
		}()
		d, err := build()
		if err != nil {
			return nil, err
		}
		return d.Output()
	}()
	if err != nil {
		a.internalError(c, "pdf", err)
		return
	}
	if fcd {
		filename = filenameForContentDisposition(c, filename)
	}
	h := c.W.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", ContentDisposition("attachment", filename))
	h.Set("Content-Transfer-Encoding", "binary")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(data)
	c.Halt()
}

// pdfTextOptions は pdf_format_text / write_wiki_page の textilizable のオプション
// （:only_path => false, :edit_section_links => false, :headings => false, :inline_attachments => false）。
func pdfTextOptions(obj *redmine.Object) redmine.Options {
	return redmine.Options{Object: obj, FullURL: true, NoHeadings: true, NoInlineAttachments: true}
}

var (
	rePDFImageName = regexp.MustCompile(`(?i)^[^/"]+\.(gif|jpg|jpe|jpeg|png|webp)$`)
	rePDFDownload  = regexp.MustCompile(`/attachments/download/([^/]+)/`)
	rePDFThumbnail = regexp.MustCompile(`/attachments/thumbnail/([^/]+)/(\d+)`)
)

// maxPDFImageSize は PDF に埋め込む画像ファイルの大きさの上限。
const maxPDFImageSize = 32 << 20

// pdfImageLoader は ITCPDF#get_image_filename（attachments の中から img の src に当たる画像を探す）。
func (a *App) pdfImageLoader(atts []*domain.Attachment) pdf.ImageLoader {
	return func(src string) ([]byte, bool) {
		if a.AttachmentStore == nil {
			return nil, false
		}
		read := func(path string) ([]byte, bool) {
			st, err := os.Stat(path)
			if err != nil || st.IsDir() || st.Size() > maxPDFImageSize {
				return nil, false
			}
			b, err := os.ReadFile(path)
			return b, err == nil
		}
		byID := func(id string) *domain.Attachment {
			for _, att := range atts {
				if strconv.FormatInt(att.ID, 10) == id {
					return att
				}
			}
			return nil
		}
		name := src
		if u, err := url.PathUnescape(src); err == nil {
			name = u
		}
		if rePDFImageName.MatchString(name) {
			// Attachment.latest_attach（created_on, id の新しいものから）
			sorted := slices.Clone(atts)
			slices.SortStableFunc(sorted, func(x, y *domain.Attachment) int {
				if c := y.CreatedOn.Compare(x.CreatedOn); c != 0 {
					return c
				}
				return int(y.ID - x.ID)
			})
			for _, att := range sorted {
				if strings.EqualFold(att.Filename, name) {
					if a.AttachmentStore.Readable(att) {
						return read(a.AttachmentStore.Diskfile(att))
					}
					return nil, false
				}
			}
			return nil, false
		}
		if m := rePDFDownload.FindStringSubmatch(src); m != nil {
			if att := byID(m[1]); att != nil && a.AttachmentStore.Readable(att) {
				return read(a.AttachmentStore.Diskfile(att))
			}
			return nil, false
		}
		if m := rePDFThumbnail.FindStringSubmatch(src); m != nil {
			if att := byID(m[1]); att != nil && a.AttachmentStore.Readable(att) {
				size, _ := strconv.Atoi(m[2])
				if p, ok := a.AttachmentStore.Thumbnail(att, size); ok {
					return read(p)
				}
			}
		}
		return nil, false
	}
}

// pdfAttachmentRow は添付ファイルの一覧の 1 行（filename / number_to_human_size / format_date / author.name）。
func pdfAttachmentRow(d *pdf.Doc, filename, size, date, author string) {
	d.SetFontStyle("", 8)
	d.Cell(80, 5, filename, "", 0, "", false)
	d.Cell(20, 5, size, "", 0, "R", false)
	d.Cell(25, 5, date, "", 0, "R", false)
	d.Cell(65, 5, author, "", 0, "R", false)
	d.Ln()
}
