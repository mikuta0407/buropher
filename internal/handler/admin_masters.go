package handler

import (
	"bytes"
	"context"
	"golang.org/x/text/encoding/htmlindex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/permission"
	"github.com/mikuta0407/buropher/internal/view"
)

// このファイルは管理画面のマスタ系コントローラ（roles / trackers / issue_statuses / enumerations）で
// 共通に使う before_action・フォームモデル・パラメータ変換。

// RequireAdminOrAPIRequest は ApplicationController#require_admin_or_api_request
// （API 形式なら通す。管理者以外のログインユーザーは 406、未ログインはログインを要求）。
func RequireAdminOrAPIRequest() ActionOption {
	return Before(func(c *Req) {
		if httpx.IsAPIRequest(c.R) || c.User.IsAdmin() {
			return
		}
		if c.User.Logged() {
			c.RenderError(http.StatusNotAcceptable, "")
			return
		}
		c.DenyAccess()
	})
}

// renderAdmin は layout 'admin' で描画する（XHR で layout: false の場合は noLayout）。
func (c *Req) renderAdmin(name string, data any, noLayout bool) {
	layout := "admin"
	if noLayout {
		layout = view.NoLayout
	}
	c.Render(name, data, RenderOptions{Layout: layout})
}

// head は head :status（本文なし）。
func (c *Req) head(status int) {
	httpx.Head(c.W, c.R, status)
	c.Halt()
}

// unknownFormat は respond_to に一致する形式がない場合（ActionController::UnknownFormat → 406、本文なし）。
func (c *Req) unknownFormat() {
	httpx.HeadAs(c.W, c.R, http.StatusNotAcceptable, "html")
	c.Halt()
}

// formModel はフォームビルダ（labelled_form_for）に渡すモデルの共通部分。
// ParamKey / Persisted / ToParam / ErrorsOn / HumanAttributeName を実装する。
type formModel struct {
	paramKey string
	id       int64
	errs     *domain.ValidationErrors
	tr       domain.Translator
}

func (m *formModel) ParamKey() string { return m.paramKey }
func (m *formModel) Persisted() bool  { return m.id != 0 }
func (m *formModel) ToParam() string  { return strconv.FormatInt(m.id, 10) }

// Errors はバリデーションエラー（error_messages_for に渡す）。
func (m *formModel) Errors() *domain.ValidationErrors { return m.errs }

func (m *formModel) ErrorsOn(attr string) []string { return m.errs.On(attr) }

func (m *formModel) HumanAttributeName(attr string) string {
	if m.tr == nil {
		return attr
	}
	return domain.HumanAttributeName(m.tr, attr)
}

func newFormModel(c *Req, key string, id int64) formModel {
	return formModel{paramKey: key, id: id, errs: &domain.ValidationErrors{}, tr: c.L}
}

// castBool は ActiveModel::Type::Boolean#cast（"" は nil 扱いで false、"0" / "false" / "f" / "off" 等は false）。
func castBool(v string) bool {
	switch strings.ToLower(v) {
	case "", "0", "f", "false", "off":
		return false
	}
	return true
}

// paramIDs は配列パラメータを id の列にする（空文字は除く。Rails の xxx_ids= と同じ）。
func paramIDs(ss []string) []int64 {
	var out []int64
	for _, s := range ss {
		if strings.TrimSpace(s) == "" {
			continue
		}
		out = append(out, httpx.RubyToI(s))
	}
	return out
}

// optionalID は "" → nil、それ以外は to_i。
func optionalID(s string) *int64 {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	v := httpx.RubyToI(s)
	return &v
}

// permissionGroup は権限をモジュール単位にまとめたもの（setable_permissions.group_by(&:project_module)）。
type permissionGroup struct {
	// Module はモジュール名（空ならプロジェクト）。
	Module string
	// ID は fieldset の id（module_project / module_<mod>）。
	ID string
	// Label は表示名（l(:label_project) / l_or_humanize(mod, prefix: 'project_module_')）。
	Label       string
	Permissions []*permission.Permission
}

// groupPermissions は perms_by_module.keys.sort の順に権限をまとめる。
func groupPermissions(c *Req, perms []*permission.Permission) []permissionGroup {
	byMod := map[string][]*permission.Permission{}
	var mods []string
	for _, p := range perms {
		if _, ok := byMod[p.Module]; !ok {
			mods = append(mods, p.Module)
		}
		byMod[p.Module] = append(byMod[p.Module], p)
	}
	sort.Strings(mods)
	out := make([]permissionGroup, 0, len(mods))
	for _, m := range mods {
		g := permissionGroup{Module: m, Permissions: byMod[m]}
		if m == "" {
			g.ID = "module_project"
			g.Label = c.L("label_project")
		} else {
			g.ID = "module_" + m
			g.Label = c.Loc.LOrHumanize(m, "project_module_")
		}
		out = append(out, g)
	}
	return out
}

// pagePath は redirect_to xxx_path(:page => params[:page])。
func pagePath(c *Req, path string) string {
	if p := c.Params().String("page"); p != "" {
		return path + "?page=" + url.QueryEscape(p)
	}
	return path
}

type localKey string

// setLocal は before_action で見つけたモデル（@role 等）をリクエストに保存する。
func (c *Req) setLocal(key string, v any) {
	c.R = c.R.WithContext(context.WithValue(c.R.Context(), localKey(key), v))
}

// local は setLocal で保存した値。
func (c *Req) local(key string) any { return c.R.Context().Value(localKey(key)) }

// encodeQueryRails は Hash#to_query（キー順、配列は key[]=v）。
func encodeQueryRails(q url.Values) string { return q.Encode() }

// csvNil は CSV の nil 値（引用符なしの空欄になる）。
const csvNil = "\x00nil"

// csvRow は Redmine::Export::CSV の 1 行（Ruby の CSV と同じく必要なときだけ引用する）。
func csvRow(b *bytes.Buffer, sep string, fields []string) {
	for i, f := range fields {
		if i > 0 {
			b.WriteString(sep)
		}
		if f == csvNil {
			// nil は引用符なしの空欄（"" は引用符付き）
			continue
		}
		if f == "" || strings.ContainsAny(f, sep+"\"\r\n") {
			b.WriteString(`"` + strings.ReplaceAll(f, `"`, `""`) + `"`)
		} else {
			b.WriteString(f)
		}
	}
	b.WriteString("\n")
}

// sendCSV は Redmine::Export::CSV.generate の出力（encoding / field_separator パラメータ、UTF-8 なら BOM）を送る。
// withSeparatorParam が偽なら field_separator パラメータを使わない（generate に encoding だけを渡す出力）。
func (c *Req) sendCSV(filename string, rows [][]string, withSeparatorParam bool) {
	sep := ""
	if withSeparatorParam {
		sep = c.Params().String("field_separator")
	}
	if sep == "" {
		sep = c.L("general_csv_separator")
	}
	encName := c.Params().String("encoding")
	enc, err := htmlindex.Get(encName)
	if encName == "" || err != nil {
		encName = c.L("general_csv_encoding")
		enc, err = htmlindex.Get(encName)
	}
	var b bytes.Buffer
	for _, r := range rows {
		csvRow(&b, sep, r)
	}
	out := b.Bytes()
	if err != nil || strings.EqualFold(encName, "UTF-8") {
		out = append([]byte("\xEF\xBB\xBF"), out...)
	} else {
		var e bytes.Buffer
		w := enc.NewEncoder()
		for _, r := range b.String() {
			s, err := w.String(string(r))
			if err != nil {
				s = "?"
			}
			e.WriteString(s)
		}
		out = e.Bytes()
	}
	c.W.Header().Set("Content-Type", "text/csv; header=present")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"; filename*=UTF-8''`+url.PathEscape(filename))
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(out)
	c.Halt()
}
