// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// HelpController（app/controllers/help_controller.rb）と Rails のヘルスチェック（/up, rails/health#show）。
//
// ヘルプのテンプレート（app/views/help/wiki_syntax/**/*.html.erb）は Redmine のファイルをそのまま
// web/templates/help/wiki_syntax に置き、ERB の式（パス・stylesheet_link_tag・image_tag のみ）を
// 描画時に置き換える（中身に {{toc}} 等があり Go テンプレートにはしにくいため）。
// コードハイライトの言語一覧（Rouge::Lexer.all）は参照環境から抽出した code_highlighting_languages.tsv。

import (
	"bufio"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/view/rails"
	"github.com/mikuta0407/buropher/web"
)

// HelpController。
var HelpController = &Controller{Name: "help", MainMenu: true}

// routesHelp は
//
//	get 'help/wiki_syntax/(:type)', :controller => 'help', :action => 'show_wiki_syntax',
//	    :constraints => {:type => /detailed/}, :as => 'help_wiki_syntax'
//	get 'help/code_highlighting', :controller => 'help', :action => 'show_code_highlighting', :as => 'help_code_highlighting'
//	get 'up' => 'rails/health#show', :as => :rails_health_check
func (a *App) routesHelp(r Router) {
	a.Handle(r, http.MethodGet, "/help/wiki_syntax", HelpController, "show_wiki_syntax", a.HelpShowWikiSyntax)
	a.Handle(r, http.MethodGet, "/help/wiki_syntax/{type:detailed}", HelpController, "show_wiki_syntax", a.HelpShowWikiSyntax)
	a.Handle(r, http.MethodGet, "/help/code_highlighting", HelpController, "show_code_highlighting", a.HelpShowCodeHighlighting)
	// Rails::HealthController は ActionController::Base の直下（Redmine のフィルタ・セッションを通らない）
	httpx.Route(r, http.MethodGet, "/up", railsHealthShow)
}

// railsHealthShow は rails/health#show（例外が無ければ緑の HTML。拡張子によらず text/html）。
func railsHealthShow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html><html><body style="background-color: green"></body></html>`))
}

// helpHTMLFormat は html 以外の形式（.json 等）なら ActionView::MissingTemplate → render_404 と同じく
// 本文なしの 404 を返して false。
func helpHTMLFormat(c *Req) bool {
	switch f := httpx.Format(c.R); f {
	case "", "html", httpx.FormatAll:
		return true
	default:
		httpx.HeadAs(c.W, c.R, http.StatusNotFound, f)
		c.Halt()
		return false
	}
}

// HelpShowWikiSyntax は help#show_wiki_syntax（Setting.text_formatting と current_language のテンプレート。
// その言語のテンプレートが無ければ en。レイアウトなし）。
func (a *App) HelpShowWikiSyntax(c *Req) {
	if !helpHTMLFormat(c) {
		return
	}
	typ := ""
	if t := c.Params().String("type"); t != "" {
		typ = t + "_"
	}
	tf := a.Settings.String("text_formatting")
	if tf == "" {
		// Setting.text_formatting が空（書式なし）のテンプレートは存在しない（Redmine では MissingTemplate → 404）
		c.Render404("")
		return
	}
	lang := c.Loc.CurrentLanguage()
	path := func(lang string) string {
		return "help/wiki_syntax/" + tf + "/" + lang + "/wiki_syntax_" + typ + tf + ".html.erb"
	}
	// lookup_context.exists? はファイル名の大文字小文字を区別する（pt-BR は pt-br に一致しない）
	b, err := fs.ReadFile(web.Templates(), path(lang))
	if err != nil {
		b, err = fs.ReadFile(web.Templates(), path("en"))
	}
	if err != nil {
		c.Render404("")
		return
	}
	a.writeHelpHTML(c, a.renderHelpERB(string(b)))
}

// HelpShowCodeHighlighting は help#show_code_highlighting（help/wiki_syntax/code_highlighting_languages）。
func (a *App) HelpShowCodeHighlighting(c *Req) {
	if !helpHTMLFormat(c) {
		return
	}
	b, err := fs.ReadFile(web.Templates(), "help/wiki_syntax/code_highlighting_languages.tsv")
	if err != nil {
		a.internalError(c, "code highlighting languages", err)
		return
	}
	var sb strings.Builder
	sb.WriteString(`<!doctype html>
<html lang="en">
<head>
  <title>List of languages supported by Redmine code highlighter</title>
  <meta charset="UTF-8">
  ` + a.renderHelpERB(`<%= stylesheet_link_tag "wiki_syntax_detailed.css" %>`) + `
</head>

<body>
  <h1>List of languages supported by Redmine code highlighter</h1>

  <table class="list">
    <tr>
      <th>Language</th>
      <th>Description</th>
    </tr>
`)
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		for len(f) < 3 {
			f = append(f, "")
		}
		aliases := ""
		if f[2] != "" {
			aliases = " [aliases: " + f[2] + "]"
		}
		// tag / desc / aliases は抽出時点で HTML エスケープ済み
		sb.WriteString("      <tr>\n        <td><code>" + f[0] + "</code></td>\n        <td>\n          " + f[1] +
			"\n          " + aliases + "\n        </td>\n      </tr>\n")
	}
	sb.WriteString("  </table>\n</body>\n</html>\n")
	a.writeHelpHTML(c, sb.String())
}

// writeHelpHTML はレイアウトなしの HTML を返す。
func (a *App) writeHelpHTML(c *Req, body string) {
	httpx.SetContentType(c.W, "html", true)
	if httpx.ShouldVaryAccept(c.R) && c.W.Header().Get("Vary") == "" {
		c.W.Header().Add("Vary", "Accept")
	}
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(body))
	c.Halt()
}

var (
	helpERBTagRe     = regexp.MustCompile(`<%=\s*(.*?)\s*%>`)
	helpSyntaxPathRe = regexp.MustCompile(`^help_wiki_syntax_path\(:detailed(?:, anchor: "(\w+)")?\)$`)
	helpStylesheetRe = regexp.MustCompile(`^stylesheet_link_tag "([^"]+)"$`)
	helpImageTagRe   = regexp.MustCompile(`^image_tag\("([^"]+)", \{ alt: ?"([^"\\]*)" \}\)$`)
)

// renderHelpERB はヘルプのテンプレートの ERB 式を評価する（使われている式だけに対応）。
func (a *App) renderHelpERB(src string) string {
	return helpERBTagRe.ReplaceAllStringFunc(src, func(tag string) string {
		expr := helpERBTagRe.FindStringSubmatch(tag)[1]
		if expr == "help_code_highlighting_path" {
			return "/help/code_highlighting"
		}
		if m := helpSyntaxPathRe.FindStringSubmatch(expr); m != nil {
			if m[1] != "" {
				return "/help/wiki_syntax/detailed#" + m[1]
			}
			return "/help/wiki_syntax/detailed"
		}
		if m := helpStylesheetRe.FindStringSubmatch(expr); m != nil {
			if a.Assets == nil {
				return ""
			}
			return string(a.Assets.StylesheetLinkTag(m[1]))
		}
		if m := helpImageTagRe.FindStringSubmatch(expr); m != nil {
			src := "/" + m[1]
			if a.Assets != nil {
				src = a.Assets.ImagePath(m[1])
			}
			return `<img alt="` + string(rails.H(m[2])) + `" src="` + string(rails.H(src)) + `" />`
		}
		a.logger().Error("help: unsupported ERB expression", "expr", expr)
		return ""
	})
}
