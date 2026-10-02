package rails

import (
	"path"
	"regexp"
	"strings"
)

// computeExtname は AssetUrlHelper#compute_asset_extname（拡張子が違えば付与する）。
func computeExtname(source, ext string) string {
	if ext == "" || isURL(source) {
		return source
	}
	// クエリ・フラグメントは除いて判定する
	base := source
	if i := strings.IndexAny(base, "?#"); i >= 0 {
		base = base[:i]
	}
	if path.Ext(base) != ext {
		return base + ext + source[len(base):]
	}
	return source
}

// ImageTag は image_tag(source, options)。src は View.AssetPath で解決する。
// size: "16x16" / "16" は width/height に展開する。
func (v *View) ImageTag(source string, options *Hash) HTML {
	opts := options.Clone()
	opts.Delete("skip_pipeline")
	opts.Set("src", v.assetPath("image", source))
	if size, ok := opts.Lookup("size"); ok && truthy(size) {
		opts.Delete("size")
		if w, h, ok := extractDimensions(ToS(size)); ok {
			opts.Set("width", w)
			opts.Set("height", h)
		} else {
			opts.Set("width", nil)
			opts.Set("height", nil)
		}
	}
	return Tag("img", opts)
}

var (
	dimWH = regexp.MustCompile(`\A\d+(?:\.\d+)?x\d+(?:\.\d+)?\z`)
	dimN  = regexp.MustCompile(`\A\d+(?:\.\d+)?\z`)
)

func extractDimensions(size string) (string, string, bool) {
	switch {
	case dimWH.MatchString(size):
		w, h, _ := strings.Cut(size, "x")
		return w, h, true
	case dimN.MatchString(size):
		return size, size, true
	}
	return "", "", false
}

// ImagePath は image_path(source)。
func (v *View) ImagePath(source string) string { return v.assetPath("image", source) }

// JavascriptPath は javascript_path(source)（.js を補う）。
func (v *View) JavascriptPath(source string) string {
	return v.assetPath("javascript", computeExtname(source, ".js"))
}

// StylesheetPath は stylesheet_path(source)（.css を補う）。
func (v *View) StylesheetPath(source string) string {
	return v.assetPath("stylesheet", computeExtname(source, ".css"))
}

// JavascriptIncludeTag は javascript_include_tag(*sources, options)（preload ヘッダは扱わない）。
func (v *View) JavascriptIncludeTag(sources []string, options *Hash) HTML {
	opts := options.Except("protocol", "extname", "host", "skip_pipeline", "preload_links_header", "nopush")
	crossorigin := opts.Del("crossorigin")
	if crossorigin == true {
		crossorigin = "anonymous"
	}
	var out []string
	for _, src := range uniqStrings(sources) {
		tagOpts := NewHash("src", v.JavascriptPath(src), "crossorigin", crossorigin).Update(opts)
		out = append(out, string(ContentTag("script", "", tagOpts)))
	}
	return HTML(strings.Join(out, "\n"))
}

// StylesheetLinkTag は stylesheet_link_tag(*sources, options)（apply_stylesheet_media_default は偽）。
func (v *View) StylesheetLinkTag(sources []string, options *Hash) HTML {
	opts := options.Except("protocol", "extname", "host", "skip_pipeline", "preload_links_header", "nopush")
	crossorigin := opts.Del("crossorigin")
	if crossorigin == true {
		crossorigin = "anonymous"
	}
	var out []string
	for _, src := range uniqStrings(sources) {
		tagOpts := NewHash("rel", "stylesheet", "crossorigin", crossorigin, "href", v.StylesheetPath(src)).Update(opts)
		out = append(out, string(Tag("link", tagOpts)))
	}
	return HTML(strings.Join(out, "\n"))
}

// FaviconLinkTag は favicon_link_tag(source, options)。
func (v *View) FaviconLinkTag(source string, options *Hash) HTML {
	if source == "" {
		source = "favicon.ico"
	}
	opts := options.Except("skip_pipeline")
	return Tag("link", NewHash("rel", "icon", "type", "image/x-icon", "href", v.assetPath("image", source)).Update(opts))
}

func uniqStrings(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
