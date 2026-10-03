// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/validation"
)

// このファイルは REST API（.api.rsb テンプレート）とページネーションのための Req のヘルパー:
// RenderAPI（Redmine::Views::Builders）、render_validation_errors、render_api_ok、
// include_in_api_response?、api_meta、per_page_option、api_offset_and_limit。

// RenderAPI は .api.rsb テンプレートの描画（Redmine::Views::Builders.for(params[:format])）。
// fn で apibuilder.Builder に出力を組み立てる。status が 0 なら 200。
// json / xml 以外のフォーマットは Redmine と同じく 406 とメッセージを返す。
func (c *Req) RenderAPI(status int, fn func(b apibuilder.Builder)) {
	format := httpx.Format(c.R)
	cb := ""
	if format == "json" {
		cb = httpx.JSONPCallback(c.R, c.App.Settings.Bool("jsonp_enabled"))
	}
	b := apibuilder.New(format, apibuilder.Options{Callback: cb})
	c.halted = true
	if b == nil {
		c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.W.WriteHeader(http.StatusNotAcceptable)
		_, _ = c.W.Write([]byte(apibuilder.NoFormatMessage))
		return
	}
	fn(b)
	if status == 0 {
		status = http.StatusOK
	}
	c.W.Header().Set("Content-Type", b.ContentType())
	c.W.WriteHeader(status)
	_, _ = c.W.Write(b.Output())
}

// RenderAPIOK は render_api_ok（204 No Content）。
func (c *Req) RenderAPIOK() {
	httpx.RenderAPIOK(c.W, c.R)
	c.halted = true
}

// RenderAPIErrors は render_api_errors(*messages)（422）。
func (c *Req) RenderAPIErrors(messages ...string) {
	c.App.Errors.RenderAPIErrors(c.W, c.R, messages...)
	c.halted = true
}

// RenderValidationErrors は render_validation_errors(objects)。
func (c *Req) RenderValidationErrors(errs ...*validation.Errors) {
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.FullMessages(c.Loc)...)
	}
	c.RenderAPIErrors(msgs...)
}

// IncludeInAPIResponse は include_in_api_response?(arg)（params[:include] はカンマ区切りか配列）。
func (c *Req) IncludeInAPIResponse(arg string) bool {
	v, _ := c.Params().Get("include")
	var items []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			items = append(items, strings.TrimSpace(httpx.ValueString(e)))
		}
	case nil:
	default:
		for _, s := range strings.Split(httpx.ValueString(x), ",") {
			items = append(items, strings.TrimSpace(s))
		}
	}
	for _, s := range items {
		if s == arg {
			return true
		}
	}
	return false
}

// APIMeta は api_meta(options)（nometa パラメータか X-Redmine-Nometa ヘッダがあれば nil）。
func (c *Req) APIMeta(attrs apibuilder.Attrs) apibuilder.Attrs {
	if c.Params().Present("nometa") || nometaHeader(c.R) {
		return nil
	}
	return attrs
}

// PerPageOption は ApplicationController#per_page_option（params[:per_page] が選択肢にあれば
// session[:per_page] に保存する）。
func (c *Req) PerPageOption() int {
	opts := c.App.Settings.PerPageOptionsArray()
	if v := c.Params().String("per_page"); v != "" {
		n := pagination.RubyToI(v)
		for _, o := range opts {
			if o == n {
				if s := c.Session(); s != nil {
					s.Set("per_page", n)
				}
				return n
			}
		}
	}
	if s := c.Session(); s != nil && s.Has("per_page") {
		if n := int(s.GetInt("per_page")); n > 0 {
			return n
		}
	}
	if len(opts) > 0 {
		return opts[0]
	}
	return 25
}

// APIOffsetAndLimit は ApplicationController#api_offset_and_limit。
func (c *Req) APIOffsetAndLimit() (offset, limit int) {
	p := c.Params()
	return pagination.APIOffsetAndLimit(p.String("offset"), p.String("limit"), p.String("page"))
}

// renderAPIWithoutExtension は accept_api_auth のアクションに拡張子（params[:format]）なしで
// Accept: application/xml / application/json の GET が来た場合の応答。Redmine では respond_to の
// format.api が選ばれ、.api.rsb の Builders.for(params[:format]) が nil になるため 406 と
// NoFormatMessage を返す（Content-Type はネゴシエーションした形式）。応答した場合 true。
// ファイルを返すアクション（attachments#download / thumbnail）は respond_to を使わないので対象外。
func (a *App) renderAPIWithoutExtension(c *Req) bool {
	if !c.cfg.acceptAPIAuth || (c.R.Method != http.MethodGet && c.R.Method != http.MethodHead) || httpx.IsAPIRequest(c.R) {
		return false
	}
	if c.Controller != nil && c.Controller.Name == "attachments" && (c.Action == "download" || c.Action == "thumbnail") {
		return false
	}
	if _, ok := c.Params().Get("format"); ok {
		return false
	}
	f := httpx.Negotiate(c.R, "html", "xml", "json")
	if f != "xml" && f != "json" {
		return false
	}
	httpx.SetContentType(c.W, f, true)
	c.W.WriteHeader(http.StatusNotAcceptable)
	_, _ = c.W.Write([]byte(apibuilder.NoFormatMessage))
	c.halted = true
	return true
}
