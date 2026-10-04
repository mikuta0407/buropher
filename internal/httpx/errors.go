// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"encoding/json"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Redmine の既定メッセージ（英語）。ロケール対応は呼び出し側で行う。
const (
	MessageNotAuthorized = "You are not authorized to access this page."                           // notice_not_authorized
	MessageFileNotFound  = "The page you were trying to access doesn't exist or has been removed." // notice_file_not_found
)

// ErrorPage は common/error.html.erb の描画を担う（テンプレート実装は後で差し込む）。
// layout=false は XHR リクエスト（ApplicationController#use_layout）を表す。
// message が空なら errorExplanation を出さない。
type ErrorPage interface {
	RenderErrorPage(w http.ResponseWriter, r *http.Request, status int, message string, layout bool)
}

// ErrorPageFunc は関数を ErrorPage として使うためのアダプタ。
type ErrorPageFunc func(w http.ResponseWriter, r *http.Request, status int, message string, layout bool)

// RenderErrorPage は ErrorPage の実装。
func (f ErrorPageFunc) RenderErrorPage(w http.ResponseWriter, r *http.Request, status int, message string, layout bool) {
	f(w, r, status, message, layout)
}

// ErrorRenderer は Redmine 互換のエラーレスポンスを出す。
type ErrorRenderer struct {
	// Page は HTML のエラーページ描画（nil なら最小限の組み込み HTML）。
	Page ErrorPage
	// JSONPEnabled は Setting.jsonp_enabled?（nil なら無効）。
	JSONPEnabled func(r *http.Request) bool
}

// RenderError は ApplicationController#render_error 相当:
// HTML フォーマットならエラーページ（status）、それ以外は空ボディの head(status)。
func (e *ErrorRenderer) RenderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	// respond_to { format.html; format.any { head } }
	if f := Negotiate(r, "html", FormatAll); f == "html" {
		if ShouldVaryAccept(r) {
			w.Header().Add("Vary", "Accept")
		}
		if e != nil && e.Page != nil {
			e.Page.RenderErrorPage(w, r, status, message, !IsXHR(r))
			return
		}
		defaultErrorPage(w, status, message)
		return
	}
	Head(w, r, status)
}

// Render403 は render_403 相当（message 空なら notice_not_authorized）。
func (e *ErrorRenderer) Render403(w http.ResponseWriter, r *http.Request, message string) {
	if message == "" {
		message = MessageNotAuthorized
	}
	e.RenderError(w, r, http.StatusForbidden, message)
}

// Render404 は render_404 相当（message 空なら notice_file_not_found）。
func (e *ErrorRenderer) Render404(w http.ResponseWriter, r *http.Request, message string) {
	if message == "" {
		message = MessageFileNotFound
	}
	e.RenderError(w, r, http.StatusNotFound, message)
}

func defaultErrorPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\" /><title>")
	b.WriteString(strconv.Itoa(status))
	b.WriteString("</title></head><body>\n<h2>")
	b.WriteString(strconv.Itoa(status))
	b.WriteString("</h2>\n")
	if message != "" {
		b.WriteString("<p id=\"errorExplanation\">")
		b.WriteString(html.EscapeString(message))
		b.WriteString("</p>\n")
	}
	b.WriteString("<p><a href=\"javascript:history.back()\">Back</a></p>\n</body></html>\n")
	_, _ = w.Write([]byte(b.String()))
}

// WritePublicException は ActionDispatch::PublicExceptions 相当: 捕捉されない例外（500）や
// ルート無し（404）の応答。request.formats の先頭が json / xml なら {status, error} を
// to_json / to_xml した本文、それ以外は public/<status>.html（htmlBody）を返す。
// 内部のエラー文（SQL・ファイルパス等）は本文に含めない。
func WritePublicException(w http.ResponseWriter, r *http.Request, status int, htmlBody []byte) {
	text := http.StatusText(status)
	switch Format(r) {
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":` + strconv.Itoa(status) + `,"error":"` + text + `"}`))
		return
	case "xml":
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<hash>\n  <status type=\"integer\">" +
			strconv.Itoa(status) + "</status>\n  <error>" + text + "</error>\n</hash>\n"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(htmlBody)
}

// bodilessStatus は Rack::Utils::STATUS_WITH_NO_ENTITY_BODY（1xx, 204, 304）。
func bodilessStatus(status int) bool {
	return (status >= 100 && status < 200) || status == http.StatusNoContent || status == http.StatusNotModified
}

// Head は Rails の head(status) 相当: 空ボディで、Content-Type はリクエストのフォーマット
// （charset なし。未知なら text/html）。
func Head(w http.ResponseWriter, r *http.Request, status int) {
	HeadAs(w, r, status, Format(r))
}

// HeadAs はフォーマットを指定して head する。
func HeadAs(w http.ResponseWriter, r *http.Request, status int, format string) {
	if !bodilessStatus(status) {
		if format == FormatAll || format == "" {
			format = "html"
		}
		SetContentType(w, format, false)
	}
	if r != nil && ShouldVaryAccept(r) {
		w.Header().Add("Vary", "Accept")
	}
	w.WriteHeader(status)
}

// RenderAPIOK は render_api_ok（204 No Content）。
func RenderAPIOK(w http.ResponseWriter, r *http.Request) { Head(w, r, http.StatusNoContent) }

// RenderAPIErrors は render_api_errors 相当: 422 で
// JSON {"errors":["..."]} または XML <errors type="array"><error>...</error></errors> を返す。
// フォーマットはリクエストの format（xml 以外は JSON）。
func (e *ErrorRenderer) RenderAPIErrors(w http.ResponseWriter, r *http.Request, messages ...string) {
	e.RenderAPIErrorsStatus(w, r, http.StatusUnprocessableEntity, messages...)
}

// RenderAPIErrorsStatus は任意のステータスで API エラーを返す。
func (e *ErrorRenderer) RenderAPIErrorsStatus(w http.ResponseWriter, r *http.Request, status int, messages ...string) {
	if Format(r) == "xml" {
		w.Header().Set("Content-Type", ContentTypeFor("xml"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(APIErrorsXML(messages)))
		return
	}
	body := APIErrorsJSON(messages)
	if cb := e.jsonpCallback(r); cb != "" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(cb + "(" + body + ")"))
		return
	}
	w.Header().Set("Content-Type", ContentTypeFor("json"))
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

var jsonpSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_.]`)

// JSONPCallback は Redmine::Views::Builders::Json の callback 決定（callback / jsonp パラメータ、
// [^a-zA-Z0-9_.] を除去）。enabled=false なら ""。
func JSONPCallback(r *http.Request, enabled bool) string {
	if !enabled {
		return ""
	}
	p := ParamsOf(r)
	// request.params[:callback] || request.params[:jsonp]（"" も Ruby では真）
	var cb string
	if v, ok := p.Get("callback"); ok && v != nil {
		cb = ValueString(v)
	} else {
		cb = p.String("jsonp")
	}
	return jsonpSanitizeRe.ReplaceAllString(cb, "")
}

func (e *ErrorRenderer) jsonpCallback(r *http.Request) string {
	if e == nil || e.JSONPEnabled == nil {
		return ""
	}
	return JSONPCallback(r, e.JSONPEnabled(r))
}

// APIErrorsJSON は {"errors":[...]} を返す（Rails の to_json と同じく <>& は \u エスケープ）。
func APIErrorsJSON(messages []string) string {
	if messages == nil {
		messages = []string{}
	}
	b, _ := json.Marshal(map[string][]string{"errors": messages})
	return string(b)
}

// APIErrorsXML は Builder::XmlMarkup と同じ形式（インデントなし）の XML を返す。
func APIErrorsXML(messages []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><errors type="array">`)
	for _, m := range messages {
		b.WriteString("<error>")
		b.WriteString(XMLEscapeText(m))
		b.WriteString("</error>")
	}
	b.WriteString("</errors>")
	return b.String()
}

// cp1252 は Builder::XChar::CP1252（U+0080〜U+009F を CP1252 の文字に読み替える）。
var cp1252 = map[rune]rune{
	128: 8364, 130: 8218, 131: 402, 132: 8222, 133: 8230, 134: 8224, 135: 8225, 136: 710,
	137: 8240, 138: 352, 139: 8249, 140: 338, 142: 381, 145: 8216, 146: 8217, 147: 8220,
	148: 8221, 149: 8226, 150: 8211, 151: 8212, 152: 732, 153: 8482, 154: 353, 155: 8250,
	156: 339, 158: 382, 159: 376,
}

func validXMLChar(c rune) bool {
	return c == 0x9 || c == 0xA || c == 0xD || (c >= 0x20 && c <= 0xD7FF) ||
		(c >= 0xE000 && c <= 0xFFFD) || (c >= 0x10000 && c <= 0x10FFFF)
}

// XMLEscapeText は Builder の XChar.encode（テキストノード用）を移植したもの:
// CP1252 の読み替え、XML として不正な文字は U+FFFD、& < > をエスケープ。
func XMLEscapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		if m, ok := cp1252[c]; ok {
			c = m
		}
		if !validXMLChar(c) {
			c = '�'
		}
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// XMLEscapeAttr は Builder の属性値エスケープ（テキストのエスケープ＋改行・" の文字参照化）。
func XMLEscapeAttr(s string) string {
	t := XMLEscapeText(s)
	t = strings.ReplaceAll(t, "\n", "&#10;")
	t = strings.ReplaceAll(t, "\r", "&#13;")
	return strings.ReplaceAll(t, `"`, "&quot;")
}

// BadRequest はパラメータ解析エラー等の 400（Rails 本番の空ボディ）を返す。
func BadRequest(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
}
