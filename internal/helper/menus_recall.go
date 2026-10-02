package helper

import (
	"net/url"

	"github.com/go-chi/chi/v5"
)

// RecalledProjectID は url_for がリクエストのパスパラメータ :project_id を引き継ぐ（recall）場合の値。
// /projects/:project_id/... のルートで @project が nil になった画面（render_403 / render_404）では、
// アプリケーションメニューの {:controller => 'issues', :action => 'index'} 等が
// /projects/:project_id/issues になる（Rails のルート生成の recall）。
func (e menuEnv) RecalledProjectID() string {
	if e.p == nil || e.p.Request == nil || e.p.Project != nil {
		return ""
	}
	v := chi.URLParam(e.p.Request, "project_id")
	if v == "" {
		return ""
	}
	if u, err := url.PathUnescape(v); err == nil {
		v = u
	}
	return escapeSegment(v)
}
