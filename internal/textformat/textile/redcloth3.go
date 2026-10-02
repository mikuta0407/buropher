// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later
//
// 本ファイルは Redmine 6.1.2 の lib/redmine/wiki_formatting/textile/redcloth3.rb
// (RedCloth 3.0.4 を Redmine 向けに改変したもの) を Go へ移植したものである。
// 元の RedCloth は (cc) 2004 why the lucky stiff による BSD ライセンスのコード。
// 関数名・順序は原典に合わせ、コメントに原典の行番号を記す。

package textile

import (
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

// redcloth は RedCloth3 インスタンス (1 回の変換) の状態を保持する。
type redcloth struct {
	// hard_breaks / filter_styles / filter_html (Formatter#initialize で設定)
	hardBreaks   bool
	filterStyles bool

	shelf   []string // @shelf
	preList []string // @pre_list

	highlight Highlighter
}

// ---- 定数 (redcloth3.rb:340-354) ----

const (
	aHlgn  = `(?:(?:<>|<|>|=|[()]+)+)` // A_HLGN
	aVlgn  = `[\-^~]`                  // A_VLGN
	cClas  = `(?:\([^")]+\))`          // C_CLAS
	cLnge  = `(?:\[[a-z\-_]+\])`       // C_LNGE
	cStyl  = `(?:\{[^{][^"}]+\})`      // C_STYL
	sCspn  = `(?:\\[0-9]+)`            // S_CSPN
	sRspn  = `(?:/[0-9]+)`             // S_RSPN
	reA    = `(?:` + aHlgn + `?` + aVlgn + `?|` + aVlgn + `?` + aHlgn + `?)`
	reSpan = `(?:` + sCspn + `?` + sRspn + `|` + sRspn + `?` + sCspn + `?)` // S
	reC    = `(?:` + cClas + `?` + cStyl + `?` + cLnge + `?|` + cStyl + `?` + cLnge + `?` + cClas + `?|` + cLnge + `?` + cStyl + `?` + cClas + `?)`
	// PUNCT (文字クラス内側)
	punctRC = `!"#$%&'*+,\-./:;=?@\\^_` + "`" + `|~`
	// HYPERLINK
	hyperlink = `(` + reNS + `+?)([^` + wIn + spIn + `/;=\?]*?)(?=` + reS + `|<|$)`
)

var (
	reAHlgn = rx(aHlgn)
	reAVlgn = rx(aVlgn)
)

// SIMPLE_HTML_TAGS (redcloth3.rb:357-361)
var simpleHTMLTags = map[string]bool{
	"tt": true, "b": true, "i": true, "big": true, "small": true, "em": true, "strong": true,
	"dfn": true, "code": true, "samp": true, "kbd": true, "var": true, "cite": true,
	"abbr": true, "acronym": true, "a": true, "img": true, "br": true, "map": true,
	"q": true, "sub": true, "sup": true, "span": true, "bdo": true,
}

// QTAGS (redcloth3.rb:363-399)
type qtag struct {
	rc string
	ht string
	re *regexp2.Regexp
}

var qtags = func() []qtag {
	defs := [][2]string{
		{"**", "b"}, {"*", "strong"}, {"??", "cite"}, {"-", "del"}, {"__", "i"},
		{"_", "em"}, {"%", "span"}, {"+", "ins"}, {"^", "sup"}, {"~", "sub"},
	}
	quote := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if strings.ContainsRune(`*?-+^.$|()[]{}\`, r) {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	joins := make([]string, len(defs))
	for i, d := range defs {
		joins[i] = quote(d[0])
	}
	join := strings.Join(joins, "|")
	res := make([]qtag, len(defs))
	for i, d := range defs {
		rcq := quote(d[0])
		re := rx(`(^|[>` + spIn + `\(])` + // sta
			`(?!\-\-)` +
			`(` + join + `|)` + // oqs
			`(` + rcq + `)` + // qtag
			`([` + wordIn + `]|` + reNS + `.*?` + reNS + `)` + // content
			`(?!\-\-)` +
			rcq +
			`(` + join + `|)` + // oqa
			`(?=[` + punctIn + `]|<|` + reS + `|\)|$)`)
		res[i] = qtag{rc: d[0], ht: d[1], re: re}
	}
	return res
}()

// H_ALGN_VALS / V_ALGN_VALS (redcloth3.rb:425-436)
var hAlgnVals = map[string]string{"<": "left", "=": "center", ">": "right", "<>": "justify"}
var vAlgnVals = map[string]string{"^": "top", "-": "middle", "~": "bottom"}

type escMode int

const (
	escQuotes escMode = iota
	escNoQuotes
)

// htmlesc (redcloth3.rb:441-450): 柔軟な HTML エスケープ
func htmlesc(str string, mode escMode) string {
	str = strings.ReplaceAll(str, "&", "&amp;")
	if mode != escNoQuotes {
		str = strings.ReplaceAll(str, `"`, "&quot;")
	}
	if mode == escQuotes {
		str = strings.ReplaceAll(str, "'", "&#039;")
	}
	str = strings.ReplaceAll(str, "<", "&lt;")
	str = strings.ReplaceAll(str, ">", "&gt;")
	return str
}

var rePglAbbr = rx(reB + `([A-Z][A-Z0-9]{1,})` + reB + `(?:[(]([^)]*)[)])`)

// pgl (redcloth3.rb:453-461): グリフ置換 (Redmine 版は略語 <abbr> のみ)
func (rc *redcloth) pgl(text string) string {
	return gsub(rePglAbbr, text, func(m md) string {
		return `<abbr title="` + htmlesc(m.s(2), escQuotes) + `">` + m.s(1) + `</abbr>`
	})
}

var (
	rePbaColspan  = rx(`\\([0-9]+)`)
	rePbaRowspan  = rx(`/([0-9]+)`)
	rePbaStyle    = rx(`\{([^"}]*)\}`)
	rePbaLang     = rx(`\[([a-z\-_]+?)\]`)
	rePbaClass    = rx(`\(([^()]+?)\)`)
	rePbaPadLeft  = rx(`([(]+)`)
	rePbaPadRight = rx(`([)]+)`)
	rePbaClsID    = rx(`^(.*?)#(.*)$`)
	reWS          = rx(reS + `+`)
)

// pba (redcloth3.rb:464-512): Textile の属性指定を解析して HTML 属性文字列を作る
func (rc *redcloth) pba(textIn *string, element string) string {
	if textIn == nil {
		return ""
	}
	var style []string
	text := *textIn
	var colspan, rowspan *string
	if element == "td" {
		if m := match(rePbaColspan, text); m != nil {
			colspan = m.opt(1)
		}
		if m := match(rePbaRowspan, text); m != nil {
			rowspan = m.opt(1)
		}
		if m := match(reAVlgn, text); m != nil {
			style = append(style, "vertical-align:"+vAlgnVals[m.all()]+";")
		}
	}

	var styleSrc string
	if t, ok := sub(rePbaStyle, text, func(m md) string { styleSrc = m.s(1); return "" }); ok {
		text = t
		if !rc.filterStyles {
			sanitized := sanitizeStyles(styleSrc)
			if !blank(sanitized) {
				style = append(style, sanitized+";")
			}
		}
	}

	var lang, cls, id *string
	var cap string
	if t, ok := sub(rePbaLang, text, func(m md) string { cap = m.s(1); return "" }); ok {
		text = t
		v := cap
		lang = &v
	}
	if t, ok := sub(rePbaClass, text, func(m md) string { cap = m.s(1); return "" }); ok {
		text = t
		v := cap
		cls = &v
	}
	if t, ok := sub(rePbaPadLeft, text, func(m md) string { cap = m.s(1); return "" }); ok {
		text = t
		style = append(style, "padding-left:"+strconv.Itoa(len(cap))+"em;")
	}
	if t, ok := sub(rePbaPadRight, text, func(m md) string { cap = m.s(1); return "" }); ok {
		text = t
		style = append(style, "padding-right:"+strconv.Itoa(len(cap))+"em;")
	}
	if m := match(reAHlgn, text); m != nil {
		style = append(style, "text-align:"+hAlgnVals[m.all()]+";")
	}

	if cls != nil {
		if m := match(rePbaClsID, *cls); m != nil {
			c, i := m.s(1), m.s(2)
			cls, id = &c, &i
		}
	}

	// wiki-class- / wiki-id- を前置して任意の class / id を設定できないようにする
	if cls != nil {
		parts := splitRe(reWS, *cls)
		for i, c := range parts {
			if !strings.HasPrefix(c, "wiki-class-") {
				parts[i] = "wiki-class-" + c
			}
		}
		c := strings.Join(parts, " ")
		cls = &c
	}
	if id != nil && !strings.HasPrefix(*id, "wiki-id-") {
		i := "wiki-id-" + *id
		id = &i
	}

	var atts strings.Builder
	if len(style) > 0 {
		atts.WriteString(` style="` + strings.Join(style, "") + `"`)
	}
	if cls != nil && *cls != "" {
		atts.WriteString(` class="` + *cls + `"`)
	}
	if lang != nil {
		atts.WriteString(` lang="` + *lang + `"`)
	}
	if id != nil {
		atts.WriteString(` id="` + *id + `"`)
	}
	if colspan != nil {
		atts.WriteString(` colspan="` + *colspan + `"`)
	}
	if rowspan != nil {
		atts.WriteString(` rowspan="` + *rowspan + `"`)
	}
	return atts.String()
}

// STYLES_RE (redcloth3.rb:514)
var reStyles = rxi(`^(color|(?>(min-|max-)?)(width|height)|border|background|padding|margin|font|text|float)(-[a-z]+)*:` +
	reS + `*((` + `[0-9]+%?|[0-9]+px|[0-9]+(\.[0-9]+)?em|#[0-9a-f]+|[a-z]+` + `)` + reS + `*)+$`)

// sanitize_styles (redcloth3.rb:516-522)
func sanitizeStyles(str string) string {
	var res []string
	for _, s := range splitStr(str, ";") {
		s = rubyStrip(s)
		if matches(reStyles, s) {
			res = append(res, s)
		}
	}
	return strings.Join(res, ";")
}

var (
	// TABLE_RE (redcloth3.rb:524)
	reTable      = rxm(`^(?:table(_?` + reSpan + reA + reC + `)\. ?\n)?^(` + reA + reC + `\.? ?\|.*?\|)(\n\n|\Z)`)
	reTableBr    = rx(`([^|` + spIn + `])` + reS + `*\n`)
	reTableRow   = rxm(`^(` + reA + reC + `\. )(.*)`)
	reTableCells = rx(`\|(_?` + reSpan + reA + reC + `\. ?)?((\[\[[^|\]]*\|[^|\]]*\]\]|[^|])*?)(?=\|)`)
)

// block_textile_table (redcloth3.rb:527-553): テーブルブロックの解析
func (rc *redcloth) blockTextileTable(text string) (string, bool) {
	return gsubB(reTable, text, func(m md) string {
		tattsSrc, fullrow := m.opt(1), m.s(2)
		tatts := rc.pba(tattsSrc, "table")
		tatts = rc.shelve(tatts)
		var rows []string
		fullrow = gsub(reTableBr, fullrow, func(m md) string { return m.s(1) + "<br />" })
		for _, row := range eachLine(fullrow) {
			var ratts *string
			if rm := match(reTableRow, row); rm != nil {
				a := rc.pba(rm.opt(1), "tr")
				ratts = &a
				row = rm.s(2)
			}
			var cells []string
			// wiki リンク内の | でセルが分割されないようにした正規表現
			for _, cm := range scan(reTableCells, row) {
				modifiers := cm.opt(1)
				cell := cm.s(2)
				ctyp := "d"
				if modifiers != nil && strings.HasPrefix(*modifiers, "_") {
					ctyp = "h"
				}
				catts := ""
				if modifiers != nil {
					catts = rc.shelve(rc.pba(modifiers, "td"))
				}
				cells = append(cells, "\t\t\t<t"+ctyp+catts+">"+cell+"</t"+ctyp+">")
			}
			r := ""
			if ratts != nil {
				r = rc.shelve(*ratts)
			}
			rows = append(rows, "\t\t<tr"+r+">\n"+strings.Join(cells, "\n")+"\n\t\t</tr>")
		}
		return "\t<table" + tatts + ">\n" + strings.Join(rows, "\n") + "\n\t</table>\n\n"
	})
}

var (
	// LISTS_RE / LISTS_CONTENT_RE (redcloth3.rb:555-556)
	reLists        = rxm(`^([#*]+?` + reC + ` .*?)$(?![^#*])`)
	reListsContent = rxm(`^([#*]+)(` + reA + reC + `) (.*)$`)
)

// block_textile_lists (redcloth3.rb:559-599): リストの解析
func (rc *redcloth) blockTextileLists(text string) (string, bool) {
	return gsubB(reLists, text, func(m md) string {
		lines := splitStr(m.all(), "\n")
		lastLine := -1
		var depth []string
		for lineID, line := range lines {
			if lm := match(reListsContent, line); lm != nil {
				tl, attsSrc, content := lm.s(1), lm.s(2), lm.s(3)
				if len(depth) > 0 {
					if len(depth[len(depth)-1]) > len(tl) {
						for i := len(depth) - 1; i >= 0; i-- {
							if len(depth[i]) == len(tl) {
								break
							}
							lines[lineID-1] += "</li>\n\t</" + lT(depth[i]) + "l>\n\t"
							depth = depth[:len(depth)-1]
						}
					}
					if len(depth) > 0 && len(depth[len(depth)-1]) == len(tl) {
						lines[lineID-1] += "</li>"
					}
				}
				if len(depth) == 0 || depth[len(depth)-1] != tl {
					depth = append(depth, tl)
					atts := rc.shelve(rc.pba(&attsSrc, ""))
					lines[lineID] = "\t<" + lT(tl) + "l" + atts + ">\n\t<li>" + content
				} else {
					lines[lineID] = "\t\t<li>" + content
				}
				lastLine = lineID
			} else {
				lastLine = lineID
			}
			if lineID-lastLine > 1 || lineID == len(lines)-1 {
				for len(depth) > 0 {
					v := depth[len(depth)-1]
					depth = depth[:len(depth)-1]
					lines[lastLine] += "</li>\n\t</" + lT(v) + "l>"
				}
			}
		}
		return strings.Join(lines, "\n")
	})
}

var (
	// QUOTES_RE / QUOTES_CONTENT_RE (redcloth3.rb:601-602)
	reQuotes        = rxm(`(^>+([^\n]*?)(\n|$))+`)
	reQuotesContent = rxm(`^([> ]+)(.*)$`)
)

// block_textile_quotes (redcloth3.rb:604-622): > による引用ブロック
func (rc *redcloth) blockTextileQuotes(text string) string {
	return gsub(reQuotes, text, func(m md) string {
		lines := splitStr(m.all(), "\n")
		var quotes strings.Builder
		indent := 0
		for _, line := range lines {
			var bq, content string
			if qm := match(reQuotesContent, line); qm != nil {
				bq, content = qm.s(1), qm.s(2)
			}
			l := strings.Count(bq, ">")
			if l != indent {
				quotes.WriteString("\n\n")
				if l > indent {
					quotes.WriteString(strings.Repeat("<blockquote>", l-indent))
				} else {
					quotes.WriteString(strings.Repeat("</blockquote>", indent-l))
				}
				quotes.WriteString("\n\n")
				indent = l
			}
			quotes.WriteString(content + "\n")
		}
		quotes.WriteString("\n" + strings.Repeat("</blockquote>", indent) + "\n\n")
		return quotes.String()
	})
}

// CODE_RE (redcloth3.rb:624-629)
var reCode = rx(`(` + reNW + `)@(?:\|(` + reW + `+?)\|)?(.+?)@(?=` + reNW + `)`)

// inline_textile_code (redcloth3.rb:631-637): @code@
func (rc *redcloth) inlineTextileCode(text string) string {
	return gsub(reCode, text, func(m md) string {
		before, code := m.s(1), m.s(3)
		lang := ""
		if m.ok(2) {
			lang = ` lang="` + m.s(2) + `"`
		}
		return rc.ripOfftags(before+"<code"+lang+">"+code+"</code>", false, true)
	})
}

// lT (redcloth3.rb:639-641)
func lT(text string) string {
	if strings.HasSuffix(text, "#") {
		return "o"
	}
	return "u"
}

// BLOCKS_GROUP_RE (redcloth3.rb:647)
var (
	reBlocksGroup  = rxm(`\n{2,}(?! )`)
	reBlocksPlain  = rx(`\A[#*> ]`)
	reBlocksHTML   = rx(`^</?(` + reW + `+).*>`)
	reBlocksIndent = rxm(`((?:\n(?:\n^ +[^\n]*)+)+)`)
)

// blocks (redcloth3.rb:649-690): ブロック単位の処理
func (rc *redcloth) blocks(text string, deepCode bool) string {
	parts := splitRe(reBlocksGroup, text)
	out := make([]string, len(parts))
	for i, blk := range parts {
		plain := !matches(reBlocksPlain, blk)

		// 複雑な HTML のブロックはそのまま
		if hm := match(reBlocksHTML, blk); hm != nil && !simpleHTMLTags[hm.s(1)] {
			out[i] = blk
			continue
		}
		// インデントレベルの探索
		blk = rubyStrip(blk)
		if blk == "" {
			out[i] = blk
			continue
		}
		var codeBlk string
		blk = gsub(reBlocksIndent, blk, func(m md) string {
			iblk := flushLeft(m.all())
			iblk = rc.blocks(iblk, plain)
			if plain {
				codeBlk = iblk
				return ""
			}
			return iblk
		})

		blockApplied := 0
		for _, rule := range blockRules {
			var ok bool
			blk, ok = rule(rc, blk)
			if ok {
				blockApplied++
			}
		}
		if blockApplied == 0 {
			if deepCode {
				blk = "\t<pre><code>" + blk + "</code></pre>"
			} else {
				blk = "\t<p>" + blk + "</p>"
			}
		}
		out[i] = blk + "\n" + codeBlk
	}
	return strings.Join(out, "\n\n")
}

// textile_bq (redcloth3.rb:692-697)
func (rc *redcloth) textileBq(tag, atts string, cite *string, content string) string {
	c := ""
	if cite != nil {
		c = ` cite="` + htmlesc(*cite, escQuotes) + `"`
	}
	atts = rc.shelve(atts)
	return "\t<blockquote" + c + ">\n\t\t<p" + atts + ">" + content + "</p>\n\t</blockquote>"
}

// textile_p (redcloth3.rb:699-709) — textile_h1..h6 も同じ
func (rc *redcloth) textileP(tag, atts string, cite *string, content string) string {
	atts = rc.shelve(atts)
	return "\t<" + tag + atts + ">" + content + "</" + tag + ">"
}

// textile_fn_ (redcloth3.rb:711-716): 脚注
func (rc *redcloth) textileFn(tag, num, atts string, cite *string, content string) string {
	atts += ` id="fn` + num + `" class="footnote"`
	content = "<sup>" + num + "</sup> " + content
	atts = rc.shelve(atts)
	return "\t<p" + atts + ">" + content + "</p>"
}

// BLOCK_RE (redcloth3.rb:718)
var reBlock = rxm(`^(([a-z]+)([0-9]*))(` + reA + reC + `)\.(?::(` + reNS + `+))? (.*)$`)

// block_textile_prefix (redcloth3.rb:720-734): h1. / p. / bq. / fnN. などの接頭辞ブロック
func (rc *redcloth) blockTextilePrefix(text string) (string, bool) {
	m := match(reBlock, text)
	if m == nil {
		return text, false
	}
	tag, tagpre, num, attsSrc, cite, content := m.s(1), m.s(2), m.s(3), m.s(4), m.opt(5), m.s(6)
	atts := rc.pba(&attsSrc, "")

	// 接頭辞ハンドラへ渡す
	var replacement *string
	switch {
	case tag == "bq":
		r := rc.textileBq(tag, atts, cite, content)
		replacement = &r
	case tag == "p" || tag == "h1" || tag == "h2" || tag == "h3" || tag == "h4" || tag == "h5" || tag == "h6":
		r := rc.textileP(tag, atts, cite, content)
		replacement = &r
	case tagpre == "fn":
		r := rc.textileFn(tagpre, num, atts, cite, content)
		replacement = &r
	}
	if replacement == nil {
		return text, false
	}
	// text.gsub!($&) { replacement } — 文字列パターンによる全置換
	all := m.all()
	if !strings.Contains(text, all) {
		return text, false
	}
	return strings.ReplaceAll(text, all, *replacement), true
}

// MARKDOWN_RULE_RE (redcloth3.rb:773-775)
var reMarkdownRule = rx(`^( ?(\* ?){3,}| ?(\- ?){3,}| ?(_ ?){3,})$`)

// block_markdown_rule (redcloth3.rb:777-781): 水平線
func (rc *redcloth) blockMarkdownRule(text string) (string, bool) {
	return gsubB(reMarkdownRule, text, func(m md) string { return "<hr />" })
}

var reSpanAtts = rx(`^(` + reC + `)(.+)$`)

// inline_textile_span (redcloth3.rb:787-807): *strong* などのフレーズ修飾
func (rc *redcloth) inlineTextileSpan(text string) string {
	for _, q := range qtags {
		text = gsub(q.re, text, func(m md) string {
			sta, oqs, content, oqa := m.s(1), m.s(2), m.s(4), m.s(5)
			var atts *string
			if cm := match(reSpanAtts, content); cm != nil {
				atts = cm.opt(1)
				content = cm.s(2)
			}
			a := rc.shelve(rc.pba(atts, ""))
			return sta + oqs + "<" + q.ht + a + ">" + content + "</" + q.ht + ">" + oqa
		})
	}
	return text
}

// LINK_RE (redcloth3.rb:809-826)
var reLink = rx(`(` +
	`([` + spIn + `\[{(]|[` + punctRC + `])?` + // $pre
	`"` + // start
	`(` + reC + `)` + // $atts
	`([^"\n]+?)` + // $text
	reS + `?` +
	`(?:\(([^)]+?)\)(?="))?` + // $title
	`":` +
	`(` + // $url
	`(/|[a-zA-Z]+://|www\.|mailto:)` + // $proto
	`[` + alnumIn + `_/]` + reNS + `+?` +
	`)` +
	`(/)?` + // $slash
	`([^` + alnumIn + `_=/;\(\)\-]*?)` + // $post
	`)` +
	`(?=<|` + reS + `|$)`)

var reHTTP = rx(`^https?://`)

// inline_textile_link (redcloth3.rb:828-854): "text":url 形式のリンク
func (rc *redcloth) inlineTextileLink(text string) string {
	return gsub(reLink, text, func(m md) string {
		all, pre, attsSrc, ltext, title, url, slash, post := m.s(1), m.s(2), m.s(3), m.s(4), m.opt(5), m.s(6), m.s(8), m.s(9)
		if strings.Contains(ltext, "<br />") {
			return all
		}
		// 括弧の対応が取れておらず ')' で終わる URL は、外側の括弧とみなす
		if strings.HasSuffix(url, ")") && strings.Count(url, "(")-strings.Count(url, ")") < 0 {
			url = url[:len(url)-1]
			post = ")" + post
		}
		url = htmlesc(url, escQuotes)
		if !uriWithLinkSafeScheme(url) {
			return all
		}
		atts := rc.pba(&attsSrc, "")
		atts = ` href="` + url + slash + `"` + atts
		if title != nil {
			atts += ` title="` + htmlesc(*title, escQuotes) + `"`
		}
		atts = rc.shelve(atts)
		external := ""
		if matches(reHTTP, url) {
			external = ` class="external"`
		}
		return pre + "<a" + atts + external + ">" + ltext + "</a>" + post
	})
}

// IMAGE_RE (redcloth3.rb:938-949)
var reImage = rx(`(>|` + reS + `|^)` + // start of line?
	`!` + // opening
	`(<|=|>)?` + // optional alignment atts
	`(` + reC + `)` + // optional style,class atts
	`(?:\. )?` + // optional dot-space
	`([^` + spIn + `(!]+?)` + // presume this is the src
	reS + `?` + // optional space
	`(?:\(((?:[^\(\)]|\([^\)]+\))+?)\))?` + // optional title
	`!` + // closing
	`(?::` + hyperlink + `)?`) // optional href

// inline_textile_image (redcloth3.rb:951-989): !image! 形式の画像
func (rc *redcloth) inlineTextileImage(text string) string {
	return gsub(reImage, text, func(m md) string {
		stln, algn, attsSrc, url, title, href, hrefA1 := m.s(1), m.opt(2), m.s(3), m.s(4), m.opt(5), m.opt(6), m.s(7)
		if title != nil {
			t := htmlesc(*title, escQuotes)
			title = &t
		}
		atts := rc.pba(&attsSrc, "")
		atts = ` src="` + htmlesc(url, escQuotes) + `"` + atts
		if title != nil {
			atts += ` title="` + *title + `"`
		}
		t := ""
		if title != nil {
			t = *title
		}
		atts += ` alt="` + t + `"`

		u, _, _ := strings.Cut(url, "?")
		if !uriWithSafeScheme(u) {
			return m.all()
		}
		var h string
		if href != nil {
			h = htmlesc(*href, escQuotes)
			if !uriWithLinkSafeScheme(h) {
				return m.all()
			}
		}

		var out strings.Builder
		if href != nil {
			out.WriteString("<a" + rc.shelve(` href="`+h+`"`) + ">")
		}
		out.WriteString("<img" + rc.shelve(atts) + " />")
		if href != nil {
			out.WriteString("</a>" + hrefA1)
		}
		res := out.String()
		if algn != nil {
			a := hAlgnVals[*algn]
			if stln == "<p>" {
				res = `<p style="float:` + a + `">` + res
			} else {
				res = stln + `<span style="float:` + a + `">` + res + "</span>"
			}
		} else {
			res = stln + res
		}
		return res
	})
}

// shelve (redcloth3.rb:991-994)
func (rc *redcloth) shelve(val string) string {
	rc.shelf = append(rc.shelf, val)
	return " :redsh#" + strconv.Itoa(len(rc.shelf)) + ":"
}

var reRetrieve = rx(` :redsh#([0-9]+):`)

// retrieve (redcloth3.rb:996-1000)
func (rc *redcloth) retrieve(text string) string {
	return gsub(reRetrieve, text, func(m md) string {
		n, err := strconv.Atoi(m.s(1))
		if err == nil && n-1 >= 0 && n-1 < len(rc.shelf) {
			return rc.shelf[n-1]
		}
		// Ruby の配列は負のインデックスで末尾から参照する
		if err == nil && n-1 < 0 && len(rc.shelf)+(n-1) >= 0 {
			return rc.shelf[len(rc.shelf)+(n-1)]
		}
		return m.all()
	})
}

var reIncomingEntities = rxi(`&(?![#a-z0-9]+;)`)

// incoming_entities (redcloth3.rb:1002-1008)
func incomingEntities(text string) string {
	return gsub(reIncomingEntities, text, func(md) string { return "x%x%" })
}

var (
	reNoTextile1 = rx(`(^|` + reS + `)==([^=]+.*?)==(` + reS + `|$)?`)
	reNoTextile2 = rxm(`^ *==([^=]+.*?)==`)
)

// no_textile (redcloth3.rb:1010-1015)
func noTextile(text string) string {
	text = gsub(reNoTextile1, text, func(m md) string {
		return m.s(1) + "<notextile>" + m.s(2) + "</notextile>" + m.s(3)
	})
	// 置換文字列 '\1<notextile>\2</notextile>\3' は存在しないグループを空文字列として扱う
	text = gsub(reNoTextile2, text, func(m md) string {
		return m.s(1) + "<notextile></notextile>"
	})
	return text
}

var (
	reBlankLine = rx(`^ +$`)
	reManyNL    = rx(`\n{3,}`)
	reQuoteEOL  = rx(`"$`)
	reCntrl     = rx(`(?![\r\n\t ])\p{Cc}`)
	reIndented  = rx(`^ +` + reNS)
)

// clean_white_space (redcloth3.rb:1017-1029)
func cleanWhiteSpace(text string) string {
	// 改行の正規化
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	text = gsub(reBlankLine, text, func(md) string { return "" })
	text = gsub(reManyNL, text, func(md) string { return "\n\n" })
	text = gsub(reQuoteEOL, text, func(md) string { return "\" " })
	// 文書全体がインデントされていれば左に寄せる
	return flushLeft(text)
}

// flush_left (redcloth3.rb:1031-1042)
func flushLeft(text string) string {
	if matches(reCntrl, text) {
		text = gsub(reCntrl, text, func(md) string { return "" })
	}
	if matches(reIndented, text) {
		indt := 0
		for !lineStartsWithIndent(text, indt) {
			indt++
		}
		if indt != 0 {
			text = removeIndent(text, indt)
		}
	}
	return text
}

// lineStartsWithIndent は /^ {n}\S/ にマッチする行があるかを返す。
func lineStartsWithIndent(text string, n int) bool {
	for _, line := range strings.Split(text, "\n") {
		if len(line) > n && strings.Count(line[:n], " ") == n && !isRubySpace(line[n]) {
			return true
		}
	}
	return false
}

// removeIndent は gsub(/^ {n}/, ”) 相当。
func removeIndent(text string, n int) string {
	lines := strings.Split(text, "\n")
	pre := strings.Repeat(" ", n)
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, pre)
	}
	return strings.Join(lines, "\n")
}

func isRubySpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\v' || b == '\f' || b == '\r'
}

var reFootnoteRef = rx(`(?<=[` + wordIn + `\]])\[([0-9]+?)\](` + reS + `)?`)

// footnote_ref (redcloth3.rb:1044-1047)
func footnoteRef(text string) string {
	return gsub(reFootnoteRef, text, func(m md) string {
		return `<sup><a href="#fn` + m.s(1) + `">` + m.s(1) + `</a></sup>` + m.s(2)
	})
}

// OFFTAGS 系 (redcloth3.rb:1049-1054)
// OFFTAGS は Regexp として埋め込まれるため (?-mix:...) となり、大文字小文字を区別する。
const offtags = `(?-i:(code|pre|kbd|notextile))`

var (
	reOfftagMatch = rxmi(`(?:(</` + offtags + reB + `>)|(<` + offtags + reB + `[^>]*>))(.*?)(?=</?` + offtags + reB + reNW + `|\Z)`)
	reOfftagOpen  = rx(`<` + offtags)
	reOfftagClose = rx(`</?` + offtags)
	reHastag      = rxm(`(</?` + reW + `[^\n]*?>)`)
	reAlltag      = rxm(`(</?` + reW + `[^\n]*?>)|.*?(?=</?` + reW + `[^\n]*?>|$)`)
)

// glyphs_textile (redcloth3.rb:1056-1081)
func (rc *redcloth) glyphsTextile(text string, level int) string {
	if !matches(reHastag, text) {
		text = rc.pgl(text)
		text = footnoteRef(text)
		return text
	}
	codepre := 0
	return gsub(reAlltag, text, func(m md) string {
		line := m.all()
		// <code> や <pre> の間はグリフ処理しない
		if m.ok(1) {
			if matches(reOfftagOpen, line) {
				codepre++
			} else if matches(reOfftagClose, line) {
				codepre--
				if codepre < 0 {
					codepre = 0
				}
			}
		} else if codepre == 0 {
			line = rc.glyphsTextile(line, level+1)
		} else {
			line = htmlesc(line, escNoQuotes)
		}
		return line
	})
}

var (
	reHasAnyTag   = rx(`<.*>`)
	reCodeClassW  = rx(`<code` + reS + `+class="(` + reW + `+)">`)
	reOfftagFirst = rx(`<` + offtags + `([^>]*)>`)
	reClassAttr   = rxi(`(class=("[^"]+"|'[^']+'))`)
)

// rip_offtags (redcloth3.rb:1083-1122): <pre> 等の中身を退避する
func (rc *redcloth) ripOfftags(text string, escapeAftertag, escapeLine bool) string {
	if !matches(reHasAnyTag, text) {
		return text
	}
	codepre := 0
	used := map[string]bool{}
	return gsub(reOfftagMatch, text, func(m md) string {
		line := m.all()
		if m.ok(3) {
			first, offtag, aftertag := m.s(3), m.s(4), m.s(5)
			codepre++
			used[offtag] = true
			if codepre-len(used) > 0 {
				if escapeLine {
					line = htmlesc(line, escNoQuotes)
				}
				rc.preList[len(rc.preList)-1] += line
				line = ""
			} else {
				// ハイライト対象の <code class="..."> の中身はエスケープしない
				if m.ok(5) && escapeAftertag && !matches(reCodeClassW, first) {
					aftertag = htmlesc(aftertag, escNoQuotes)
				}
				line = "<redpre#" + strconv.Itoa(len(rc.preList)) + ">"
				tag := ""
				attrs := ""
				if fm := match(reOfftagFirst, first); fm != nil {
					tag = fm.s(1)
					attrs = fm.s(2)
				}
				if cm := match(reClassAttr, attrs); cm != nil && tag == "code" {
					tag += " " + cm.s(1)
				}
				rc.preList = append(rc.preList, "<"+tag+">"+aftertag)
			}
		} else if m.ok(1) && codepre > 0 {
			if codepre-len(used) > 0 {
				if escapeLine {
					line = htmlesc(line, escNoQuotes)
				}
				rc.preList[len(rc.preList)-1] += line
				line = ""
			}
			if codepre != 0 {
				codepre--
			}
			if codepre == 0 {
				used = map[string]bool{}
			}
		}
		return line
	})
}

var reRedpre = rx(`<redpre#([0-9]+)>`)

// smooth_offtags (redcloth3.rb:1124-1129): 退避した <pre> 等の中身を戻す
func (rc *redcloth) smoothOfftagsPlain(text string) string {
	if len(rc.preList) == 0 {
		return text
	}
	return gsub(reRedpre, text, func(m md) string {
		n, err := strconv.Atoi(m.s(1))
		if err != nil || n >= len(rc.preList) {
			return ""
		}
		return rc.preList[n]
	})
}

var reEscapeHTMLTags = rx(`<(/?([!` + wIn + `][^ >\t\f\r\n]*)[^<>\n]*)(>?)`)
var reRedpreTag = rx(`\Aredpre#[0-9]+\z`)

// ALLOWED_TAGS (redcloth3.rb:1211)
var allowedTags = map[string]bool{"pre": true, "code": true, "kbd": true, "notextile": true}

// escape_html_tags (redcloth3.rb:1212-1222)
func escapeHTMLTags(text string) string {
	return gsub(reEscapeHTMLTags, text, func(m md) string {
		all, tag, cl := m.s(1), m.s(2), m.s(3)
		if !blank(cl) && (allowedTags[tag] || matches(reRedpreTag, tag)) {
			return "<" + htmlesc(all, escQuotes) + cl
		}
		r := "&lt;" + htmlesc(all, escQuotes)
		if !blank(cl) {
			r += "&gt;"
		}
		return r
	})
}

var reHTMLComment = rx(`<!--[\s\S]*?-->`)

// remove_html_comments (redcloth3.rb:1224-1226)
func removeHTMLComments(text string) string {
	return gsub(reHTMLComment, text, func(md) string { return "" })
}
