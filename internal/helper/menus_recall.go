package helper

import (
	"context"
	"net/http"
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

// routeTypeKey はルートの :defaults => {:type => ...}（WithRouteType）の context キー。
type routeTypeKey struct{}

// WithRouteType はルートの :type 既定値（imports#new の IssueImport など）をリクエストに記録する
// （url_for の recall。管理メニューの {:controller => 'enumerations'} が /enumerations/:type になる）。
func WithRouteType(r *http.Request, typ string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), routeTypeKey{}, typ))
}

// RecalledType は url_for が引き継ぐ params[:type]（ルートの既定値で与えられた場合のみ）。
func (e menuEnv) RecalledType() string {
	if e.p == nil || e.p.Request == nil {
		return ""
	}
	t, _ := e.p.Request.Context().Value(routeTypeKey{}).(string)
	return t
}
