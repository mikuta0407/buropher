package attachments

import (
	"context"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
)

// SaveResult は save_attachments の結果（{:files => saved_attachments, :unsaved => unsaved_attachments}）。
type SaveResult struct {
	// Files は saved_attachments（AttachSaved でコンテナに紐付ける添付）。
	Files []*domain.Attachment
	// Unsaved は unsaved_attachments（保存・検証に失敗した添付）。
	Unsaved []*domain.Attachment
	// UnsavedErrors は Unsaved と同じ順の検証エラー。
	UnsavedErrors []*validation.Errors
	// FailedCount は @failed_attachment_count（見つからなかったトークンの数）。0 より大きければ
	// コンテナの検証エラー（FailedMessage）にする（warn_about_failed_attachments）。
	FailedCount int
	// Created はこの呼び出しで新しく作成した添付（トランザクションがロールバックされたら
	// Store.DeleteFromDisk(ctx, a.DB, res.Created...) でファイルを消す）。
	Created []*domain.Attachment
}

// WarningNotSaved は render_attachment_warning_if_needed の flash[:warning]
// （l(:warning_attachments_not_saved, obj.unsaved_attachments.size)）。未保存が無ければ ""。
func (r *SaveResult) WarningNotSaved(l *i18n.Localizer) string {
	if r == nil || len(r.Unsaved) == 0 {
		return ""
	}
	return WarningNotSaved(l, len(r.Unsaved))
}

// FailedMessage は warn_about_failed_attachments のエラー（errors.add :base, t('warning_attachments_not_saved',
// count: n)）。FailedCount が 0 なら ""。
func (r *SaveResult) FailedMessage(l *i18n.Localizer) string {
	if r == nil || r.FailedCount == 0 {
		return ""
	}
	return WarningNotSaved(l, r.FailedCount)
}

// WarningNotSaved は l(:warning_attachments_not_saved, count)。
func WarningNotSaved(l *i18n.Localizer, count int) string {
	return tr(l, "warning_attachments_not_saved", i18n.Vars{"count": count})
}

// rubyStrip は String#strip（前後の空白と NUL を除く）。
func rubyStrip(s string) string { return strings.Trim(s, " \t\n\v\f\r\x00") }

// entries は params[:attachments]（ハッシュまたは配列）を save_attachments と同じ順に並べる:
// ハッシュのキーは数値（to_i > 0）でないものを文字列順に先に、数値のものを数値順に後に置く。
func entries(v any) []*httpx.Params {
	var out []*httpx.Params
	switch x := v.(type) {
	case *httpx.Params:
		keys := x.Keys()
		sort.SliceStable(keys, func(i, j int) bool {
			a, b := httpx.RubyToI(keys[i]), httpx.RubyToI(keys[j])
			switch {
			case a > 0 && b > 0:
				return a < b
			case a > 0:
				return false
			case b > 0:
				return true
			default:
				return keys[i] < keys[j]
			}
		})
		for _, k := range keys {
			if p := x.Map(k); p != nil {
				out = append(out, p)
			}
		}
	case []any:
		for _, e := range x {
			if p, ok := e.(*httpx.Params); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// SaveAttachments は acts_as_attachable#save_attachments(attachments, author)。
// attachments は params[:attachments]（{"1" => {"file" => ..., "description" => ...}, "2" => {"token" => ..., "filename" => ...}}
// のようなハッシュ、または配列）。file があれば新しい添付を作成（Attachment.create）し、token なら未紐付けの添付を探して
// filename・content_type・description を上書きする（保存は AttachSaved で行う）。
func (s *Store) SaveAttachments(ctx context.Context, q db.Queryer, attachments any, author *domain.User, l *i18n.Localizer) (*SaveResult, error) {
	res := &SaveResult{}
	for _, att := range entries(attachments) {
		if att.Len() == 0 {
			continue
		}
		var a *domain.Attachment
		var errs *validation.Errors
		filenameChanged := false
		if f := att.File("file"); f != nil {
			body, err := f.Open()
			if err != nil {
				return res, err
			}
			created, verrs, err := s.Create(ctx, q, Upload{Filename: f.Filename, ContentType: f.ContentType, Body: body, Size: f.Size}, author, l)
			_ = body.Close()
			if err != nil {
				return res, err
			}
			a, errs = created, verrs
			if !a.NewRecord() {
				res.Created = append(res.Created, a)
			}
		} else if token := att.String("token"); strings.TrimSpace(token) != "" {
			found, err := FindByToken(ctx, q, token)
			if err != nil {
				return res, err
			}
			if found == nil {
				res.FailedCount++
				continue
			}
			a = found
			if fn := att.String("filename"); strings.TrimSpace(fn) != "" {
				if san := SanitizeFilename(fn); san != a.Filename {
					a.Filename = san
					filenameChanged = true
				}
			}
			if ct := att.String("content_type"); strings.TrimSpace(ct) != "" {
				a.ContentType = ct
			}
		}
		if a == nil {
			continue
		}
		a.Description, a.DescriptionNull = rubyStrip(att.String("description")), false
		if a.NewRecord() {
			res.Unsaved = append(res.Unsaved, a)
			res.UnsavedErrors = append(res.UnsavedErrors, errs)
			continue
		}
		// a.invalid?（作成済みの添付は filename_changed? が false なので拡張子は検証しない）
		if verrs := s.Validate(a, -1, filenameChanged, l); verrs.Any() {
			res.Unsaved = append(res.Unsaved, a)
			res.UnsavedErrors = append(res.UnsavedErrors, verrs)
			continue
		}
		res.Files = append(res.Files, a)
	}
	return res, nil
}

// AttachSaved は attach_saved_attachments（self.attachments << attachment）: res.Files を
// コンテナ（kind, id）に紐付けて保存する。コンテナを保存するのと同じトランザクションで呼ぶ。
func (s *Store) AttachSaved(ctx context.Context, q db.Queryer, res *SaveResult, kind string, id int64) error {
	if res == nil {
		return nil
	}
	for _, a := range res.Files {
		cid := id
		a.ContainerKind, a.ContainerID = kind, &cid
		if err := repository.UpdateAttachment(ctx, q, a); err != nil {
			return err
		}
	}
	return nil
}

// AttachFiles は Attachment.attach_files(obj, attachments)（保存済みのコンテナに添付を追加する。
// save_attachments → attach_saved_attachments）。見つからないトークンは無視される（FailedCount に数える）。
// 呼び出し側は res.WarningNotSaved(l) を flash[:warning] に設定する（render_attachment_warning_if_needed）。
func (s *Store) AttachFiles(ctx context.Context, q db.Queryer, kind string, id int64, attachments any, author *domain.User, l *i18n.Localizer) (*SaveResult, error) {
	res, err := s.SaveAttachments(ctx, q, attachments, author, l)
	if err != nil {
		return res, err
	}
	return res, s.AttachSaved(ctx, q, res, kind, id)
}
