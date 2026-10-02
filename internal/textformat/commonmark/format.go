// Package commonmark は Redmine 6.1.2 の CommonMark 整形
// （Redmine::WikiFormatting::CommonMark::Formatter）を移植したもの。
//
// Redmine は commonmarker（Rust の comrak 0.41）で Markdown を HTML にし、
// html-pipeline のフィルタ群（Sanitization → SyntaxHighlight → FixupAutoLinks →
// ExternalLinks → AlertsIcons）で後処理する。本パッケージは goldmark で構文解析し、
// comrak と同じ規則で HTML を生成したうえで、libxml2 互換 DOM（htmldom）上で
// 同じ順序のフィルタを適用する。
//
// Redmine リンク（#123, [[Wiki]] 等）とマクロは別レイヤ（parse_redmine_links 相当）で
// 処理するため、ここでは扱わない（テキストはそのまま出力される）。
package commonmark

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark/text"

	"github.com/mikuta0407/buropher/internal/textformat/highlight"
	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
	"github.com/mikuta0407/buropher/internal/textformat/sanitize"
)

// Options は整形オプション。
type Options struct {
	// DisableHardBreaks が true の場合、単一改行を <br> にしない
	// （Redmine の common_mark_enable_hardbreaks: false 相当。既定は有効）。
	DisableHardBreaks bool
	// IconsPath はアイコンスプライト SVG の URL（asset_path('icons.svg') 相当）。
	// 空の場合は "/assets/icons.svg"。
	IconsPath string
	// Translate は I18n.t 相当（label_alert_note 等）。nil または空文字列を返すと既定値を使う。
	Translate func(key string) string
}

// Format は Markdown テキストを Redmine と同じ HTML に変換する。
func Format(src string, opts Options) string {
	html := MarkdownToHTML(src, !opts.DisableHardBreaks)
	frag := htmldom.ParseFragment(html)
	sanitize.Node(frag)
	SyntaxHighlightFilter(frag)
	FixupAutoLinksFilter(frag)
	sanitize.ExternalLinks(frag)
	AlertsIconsFilter(frag, opts)
	return htmldom.Render(frag)
}

// MarkdownToHTML は MarkdownFilter 相当（\r を除去し comrak 互換の HTML を生成、末尾空白を除去）。
func MarkdownToHTML(src string, hardbreaks bool) string {
	src = strings.ReplaceAll(src, "\r", "")
	b := []byte(src)
	// comrak と同様に NUL を U+FFFD に置き換える
	if strings.IndexByte(src, 0) >= 0 {
		b = []byte(strings.ReplaceAll(src, "\x00", "�"))
	}
	p := newParser()
	doc := p.Parse(text.NewReader(b))
	r := &renderer{src: b, hardbreaks: hardbreaks}
	r.render(doc)
	return strings.TrimRight(r.buf.String(), " \t\n\v\f\r\x00")
}

var reLanguageClass = regexp.MustCompile(`\Alanguage-(\S+)\z`)

// SyntaxHighlightFilter は pre > code.language-xxx を Rouge 互換でハイライトする。
func SyntaxHighlightFilter(frag *htmldom.Node) {
	codes := frag.FindAll(func(n *htmldom.Node) bool {
		return n.IsElement("code") && n.Parent != nil && n.Parent.IsElement("pre")
	})
	for _, node := range codes {
		cls := node.AttrVal("class")
		if strings.TrimSpace(cls) == "" {
			continue
		}
		m := reLanguageClass.FindStringSubmatch(cls)
		if m == nil {
			continue
		}
		lang := m[1]
		txt := node.Text()
		if !node.HasAttr("data-language") {
			node.SetAttr("data-language", lang)
		}
		if highlight.LanguageSupported(lang) {
			nodes := highlight.Nodes(txt, lang)
			node.RemoveChildren()
			for _, c := range nodes {
				node.AppendChild(c)
			}
			node.SetAttr("class", lang+" syntaxhl")
		} else {
			node.RemoveAttr("class")
		}
	}
}

var (
	reUserLinkPrefix = regexp.MustCompile(`(@|user:)\z`)
	reHiresImage     = regexp.MustCompile(`.+@\dx\.(bmp|gif|jpg|jpe|jpeg|png)\z`)
)

// FixupAutoLinksFilter はユーザー参照や高解像度画像名の誤った mailto 自動リンクを元に戻す。
func FixupAutoLinksFilter(frag *htmldom.Node) {
	for _, a := range frag.FindAll(func(n *htmldom.Node) bool { return n.IsElement("a") }) {
		href, ok := a.GetAttr("href")
		if !ok || !strings.HasPrefix(href, "mailto:") {
			continue
		}
		p := a.PrevSibling
		if (p != nil && p.Type == htmldom.TextNode && reUserLinkPrefix.MatchString(p.Data)) ||
			reHiresImage.MatchString(a.Text()) {
			a.ReplaceWith(htmldom.NewText(a.Text()))
		}
	}
}

// alertIcons はアラート種別とアイコン名の対応（ALERT_TYPE_TO_ICON_NAME）。
var alertIcons = map[string]string{
	"note":      "help",
	"tip":       "bulb",
	"warning":   "warning",
	"caution":   "alert-circle",
	"important": "message-report",
}

var reAlertType = regexp.MustCompile(`markdown-alert-(\w+)`)

// AlertsIconsFilter はアラートのタイトルを翻訳し、アイコンを挿入する。
func AlertsIconsFilter(frag *htmldom.Node, opts Options) {
	titles := frag.FindAll(func(n *htmldom.Node) bool {
		return n.IsElement("p") && hasClass(n, "markdown-alert-title")
	})
	for _, node := range titles {
		parent := node.Parent
		if parent == nil || parent.Type != htmldom.ElementNode {
			continue
		}
		pc, ok := parent.GetAttr("class")
		if !ok {
			continue
		}
		m := reAlertType.FindStringSubmatch(pc)
		if m == nil {
			continue
		}
		alertType := m[1]
		icon, ok := alertIcons[alertType]
		if !ok {
			continue
		}
		if _, known := alertIcons[strings.ToLower(node.Text())]; known {
			label := ""
			if opts.Translate != nil {
				label = opts.Translate("label_alert_" + alertType)
			}
			if label == "" {
				label = node.Text()
			}
			node.SetText(label)
		}
		first := node.FirstChild
		if first == nil {
			continue
		}
		iconsPath := opts.IconsPath
		if iconsPath == "" {
			iconsPath = "/assets/icons.svg"
		}
		svg := htmldom.NewElement("svg", htmldom.Attr{Name: "class", Value: "s18 icon-svg"}, htmldom.Attr{Name: "aria-hidden", Value: "true"})
		svg.AppendChild(htmldom.NewElement("use", htmldom.Attr{Name: "href", Value: iconsPath + "#icon--" + icon}))
		span := htmldom.NewElement("span", htmldom.Attr{Name: "class", Value: "icon-label"})
		span.AppendChild(htmldom.NewText(node.Text()))
		first.ReplaceWith(svg, span)
	}
}

// hasClass は CSS のクラスセレクタ相当の判定。
func hasClass(n *htmldom.Node, cls string) bool {
	for _, c := range strings.Fields(n.AttrVal("class")) {
		if c == cls {
			return true
		}
	}
	return false
}
