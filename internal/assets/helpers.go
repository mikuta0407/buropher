// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package assets

import (
	"html/template"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// uriRe は ActionView::Helpers::AssetUrlHelper::URI_REGEXP。
var uriRe = regexp.MustCompile(`(?i)^[-a-z]+://|^(?:cid|data):|^//`)

// tailRe は asset_path の source[/([?#].+)$/]。
var tailRe = regexp.MustCompile(`[?#].+$`)

// resolve は Propshaft resolver + Redmine の MissingAssetError 救済
// （見つからなければ "/assets/<path>"）。
func (p *Pipeline) resolve(logical string) string {
	if a, ok := p.current().assets[logical]; ok {
		return p.prefix + "/" + a.DigestedPath
	}
	return path.Join(p.prefix, logical)
}

// assetPath は ActionView の asset_path（extname は ".js" / ".css" / ""）。
func (p *Pipeline) assetPath(source, extname string) string {
	if source == "" {
		return ""
	}
	if uriRe.MatchString(source) {
		return source
	}
	tail := tailRe.FindString(source)
	source = source[:len(source)-len(tail)]
	if extname != "" && rubyExtname(source) != extname {
		source += extname
	}
	if !strings.HasPrefix(source, "/") {
		source = p.resolve(source)
	}
	if p.urlRoot != "" && !strings.HasPrefix(source, p.urlRoot+"/") {
		source = p.urlRoot + "/" + strings.TrimPrefix(source, "/")
	}
	return source + tail
}

// rubyExtname は File.extname（先頭ドットのみのファイル名は拡張子なし）。
func rubyExtname(s string) string {
	base := path.Base(s)
	trimmed := strings.TrimLeft(base, ".")
	i := strings.LastIndex(trimmed, ".")
	if i < 0 {
		return ""
	}
	return trimmed[i:]
}

// AssetPath は asset_path / image_path 相当（拡張子補完なし）。
// 例: AssetPath("favicon.ico") => "/assets/favicon-0e291875.ico"
func (p *Pipeline) AssetPath(source string) string { return p.assetPath(source, "") }

// ImagePath は image_path 相当（AssetPath と同じ）。
func (p *Pipeline) ImagePath(source string) string { return p.assetPath(source, "") }

// JavascriptPath は javascript_path 相当（".js" を補完）。
func (p *Pipeline) JavascriptPath(source string) string { return p.assetPath(source, ".js") }

// StylesheetPath は stylesheet_path 相当（".css" を補完）。
func (p *Pipeline) StylesheetPath(source string) string { return p.assetPath(source, ".css") }

// escapeAttr は ERB::Util.html_escape（Rails のタグ属性値エスケープ）。
func escapeAttr(s string) string {
	if !strings.ContainsAny(s, `&<>"'`) {
		return s
	}
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

func uniq(sources []string) []string {
	seen := map[string]bool{}
	out := sources[:0:0]
	for _, s := range sources {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// StylesheetLinkTag は media 指定なしの stylesheet_link_tag（Rails 既定の media="screen"）。
func (p *Pipeline) StylesheetLinkTag(sources ...string) template.HTML {
	return p.StylesheetLinkTagMedia("", sources...)
}

// StylesheetLinkTagMedia は stylesheet_link_tag(*sources, media: media)。
// media が空なら apply_stylesheet_media_default により "screen"。
// テーマ・プラグインによるパス差し替え（ApplicationHelper#stylesheet_link_tag）は呼び出し側で行う。
func (p *Pipeline) StylesheetLinkTagMedia(media string, sources ...string) template.HTML {
	if media == "" {
		media = "screen"
	}
	var tags []string
	for _, s := range uniq(sources) {
		tags = append(tags, `<link rel="stylesheet" href="`+escapeAttr(p.StylesheetPath(s))+`" media="`+escapeAttr(media)+`" />`)
	}
	return template.HTML(strings.Join(tags, "\n"))
}

// JavascriptIncludeTag は javascript_include_tag(*sources)。
func (p *Pipeline) JavascriptIncludeTag(sources ...string) template.HTML {
	var tags []string
	for _, s := range uniq(sources) {
		tags = append(tags, `<script src="`+escapeAttr(p.JavascriptPath(s))+`"></script>`)
	}
	return template.HTML(strings.Join(tags, "\n"))
}

// FaviconLinkTag は favicon_link_tag(source, rel: "shortcut icon")（ApplicationHelper#favicon）。
// source は論理パス（例: "favicon.ico" やテーマの FaviconPath()）。
func (p *Pipeline) FaviconLinkTag(source string) template.HTML {
	return template.HTML(`<link rel="shortcut icon" type="image/x-icon" href="` + escapeAttr(p.ImagePath(source)) + `" />`)
}

// ---- importmap ----

// pin は config/importmap.rb（+ requestjs-rails の config/importmap.rb）の pin。
// noPreload は preload: false（modulepreload を出さない）。
type pin struct {
	name, path string
	noPreload  bool
}

// staticPins は importmap の pin 定義（出力順）。
// requestjs-rails gem の pin が先に描画され、その後 Redmine の config/importmap.rb。
var staticPins = []pin{
	{"@rails/request.js", "requestjs.js", false},
	{"application", "application.js", false},
	{"@hotwired/stimulus", "stimulus.min.js", false},
	{"@hotwired/stimulus-loading", "stimulus-loading.js", false},
	{"turndown", "turndown.js", false},
	{"tablesort", "tablesort.min.js", false},
	{"tablesort.number", "tablesort.number.min.js", false},
	{"chart.js", "chart.min.js", true},
}

// pinAllFrom は pin_all_from "app/javascript/controllers", under: "controllers"。
var pinAllFrom = []struct{ dir, under string }{
	{"javascript/controllers", "controllers"},
}

var indexRe = regexp.MustCompile(`(?:/|^)index$`)

type importmapData struct {
	names     []string
	paths     []string // 解決済み URL（names と同順）
	noPreload []bool   // preload: false の pin（names と同順）
}

func (p *Pipeline) buildImportmap(idx *index) importmapData {
	resolve := func(logical string) string {
		var u string
		if a, ok := idx.assets[logical]; ok {
			u = p.prefix + "/" + a.DigestedPath
		} else {
			u = path.Join(p.prefix, logical)
		}
		if p.urlRoot != "" {
			u = p.urlRoot + u
		}
		return u
	}
	var d importmapData
	set := func(name, logical string, noPreload bool) {
		u := resolve(logical)
		for i, n := range d.names {
			if n == name {
				d.paths[i] = u
				d.noPreload[i] = noPreload
				return
			}
		}
		d.names = append(d.names, name)
		d.paths = append(d.paths, u)
		d.noPreload = append(d.noPreload, noPreload)
	}
	for _, pn := range staticPins {
		set(pn.name, pn.path, pn.noPreload)
	}
	for _, dir := range pinAllFrom {
		var files []string
		_ = fs.WalkDir(p.fsys, dir.dir, func(name string, de fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // 読めないディレクトリは pin しない
			}
			if !de.IsDir() && (strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".jsm")) {
				files = append(files, name)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			rel := strings.TrimPrefix(f, dir.dir+"/")
			noExt := strings.TrimSuffix(rel, path.Ext(rel))
			modName := dir.under
			if n := indexRe.ReplaceAllString(noExt, ""); n != "" {
				modName += "/" + n
			}
			set(modName, dir.under+"/"+rel, false)
		}
	}
	return d
}

// jsonString は Ruby の JSON.generate と同じ文字列エスケープ（"/" はエスケープしない）。
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				const hexd = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hexd[r>>4])
				b.WriteByte(hexd[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ImportmapJSON は importmap.to_json（JSON.pretty_generate, インデント 2）。
func (p *Pipeline) ImportmapJSON() string {
	d := p.current().importmap
	var b strings.Builder
	b.WriteString("{\n  \"imports\": {")
	for i, n := range d.names {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n    ")
		b.WriteString(jsonString(n))
		b.WriteString(": ")
		b.WriteString(jsonString(d.paths[i]))
	}
	if len(d.names) > 0 {
		b.WriteString("\n  }")
	} else {
		b.WriteString("}")
	}
	b.WriteString("\n}")
	return b.String()
}

// ImportmapTags は javascript_importmap_tags（entry_point "application"）。
func (p *Pipeline) ImportmapTags() template.HTML {
	d := p.current().importmap
	var b strings.Builder
	b.WriteString(`<script type="importmap" data-turbo-track="reload">`)
	b.WriteString(p.ImportmapJSON())
	b.WriteString("</script>\n")
	// preload: false の pin（chart.js）以外を modulepreload する。
	for i, u := range d.paths {
		if d.noPreload[i] {
			continue
		}
		b.WriteString(`<link rel="modulepreload" href="` + escapeAttr(u) + "\">\n")
	}
	b.WriteString(`<script type="module">import "application"</script>`)
	return template.HTML(b.String())
}
