package pdf

// writeHTMLCell（TCPDF の HTML 描画）の近似。textilizable の出力（p, br, strong, em, ins, del, code, pre,
// h1-h6, ul, ol, li, blockquote, table, img, a, hr ...）を扱う。CSS は style 属性の color と
// text-align だけを読む。

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"image"
	_ "image/gif" // GIF の復号
	"image/jpeg"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/colornames"
	_ "golang.org/x/image/webp" // WebP の復号
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ImageLoader は img の src から画像のデータを返す（使えなければ ok = false）。
type ImageLoader func(src string) (data []byte, ok bool)

// reTOC は RDMwriteFormattedCell が取り除く {{toc}}。
var reTOC = regexp.MustCompile(`(?i)<p>\{\{((<|&lt;)|(>|&gt;))?toc\}\}</p>`)

// WriteHTMLCell は RDMwriteFormattedCell(w, h, ”, ”, txt, attachments, border)（ln = 1）。
// 現在位置から幅 w（0 なら右余白まで）に HTML を描き、枠線 border を付ける。
func (d *Doc) WriteHTMLCell(w, h float64, src, border string, images ImageLoader) {
	src = reTOC.ReplaceAllString(src, "")
	w = d.cellWidth(w)
	x := d.X()
	startY := d.Y()
	segY := startY
	drawSides := func(y1, y2 float64) {
		if strings.Contains(border, "L") || border == "1" {
			d.Line(x, y1, x, y2)
		}
		if strings.Contains(border, "R") || border == "1" {
			d.Line(x+w, y1, x+w, y2)
		}
	}
	if border == "1" || strings.Contains(border, "T") {
		d.Line(x, startY, x+w, startY)
	}
	hook := &breakHook{
		before: func() { drawSides(segY, d.pageBreakTrigger()) },
		after:  func() { segY = d.tMargin },
	}
	d.hooks = append(d.hooks, hook)
	fl := &flow{d: d, left: x + d.cMargin, right: x + w - d.cMargin, y: startY + d.cMargin, images: images}
	fl.render(src, inlineStyle{TextStyle: d.ts})
	d.hooks = d.hooks[:len(d.hooks)-1]
	endY := fl.y + d.cMargin
	if d.PageNo() == 0 {
		endY = fl.y
	}
	if endY-segY < h && segY == startY {
		endY = segY + h
	}
	drawSides(segY, endY)
	if border == "1" || strings.Contains(border, "B") {
		d.Line(x, endY, x+w, endY)
	}
	d.lasth = endY - segY
	d.f.SetXY(d.lMargin, endY)
}

// inlineStyle はインラインの書式（TextStyle とリンク先）。
type inlineStyle struct {
	TextStyle
	link  string
	pre   bool
	align string
}

// flow はブロックの内容を上から並べる描画状態。
type flow struct {
	d           *Doc
	dry         bool
	noBreak     bool
	left, right float64
	y           float64
	pending     float64
	started     bool
	toks        []token
	align       string
	decor       []func(top, h float64)
	marker      *token
	markerX     float64
	images      ImageLoader
	// 計測（表の列幅）
	maxLineW, maxTokW float64
}

// render は HTML 断片を描く。
func (fl *flow) render(src string, st inlineStyle) {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		fl.text(src, st)
		fl.flushInline()
		return
	}
	for _, n := range nodes {
		fl.node(n, st)
	}
	fl.flushInline()
}

func (fl *flow) children(n *html.Node, st inlineStyle) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		fl.node(c, st)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// styleProp は style 属性の prop の値。
func styleProp(n *html.Node, prop string) string {
	for _, decl := range strings.Split(attr(n, "style"), ";") {
		k, v, ok := strings.Cut(decl, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), prop) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var reRGB = regexp.MustCompile(`(?i)^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

// parseColor は CSS の色（#rgb, #rrggbb, rgb(), 色名）。
func parseColor(s string) ([3]int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) == 6 {
			v, err := strconv.ParseUint(h, 16, 32)
			if err == nil {
				return [3]int{int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)}, true
			}
		}
		return [3]int{}, false
	}
	if m := reRGB.FindStringSubmatch(s); m != nil {
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		return [3]int{min(r, 255), min(g, 255), min(b, 255)}, true
	}
	if c, ok := colornames.Map[s]; ok {
		return [3]int{int(c.R), int(c.G), int(c.B)}, true
	}
	return [3]int{}, false
}

func addStyle(s, add string) string { return normStyle(s + add) }

// node は 1 つのノードを描く。
func (fl *flow) node(n *html.Node, st inlineStyle) {
	switch n.Type {
	case html.TextNode:
		fl.text(n.Data, st)
		return
	case html.ElementNode:
	default:
		fl.children(n, st)
		return
	}
	if c, ok := parseColor(styleProp(n, "color")); ok {
		st.Color = c
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Head, atom.Title, atom.Input, atom.Select, atom.Textarea, atom.Button:
		return
	case atom.B, atom.Strong:
		st.Style = addStyle(st.Style, "B")
		fl.children(n, st)
		return
	case atom.I, atom.Em, atom.Cite, atom.Var, atom.Dfn:
		st.Style = addStyle(st.Style, "I")
		fl.children(n, st)
		return
	case atom.U, atom.Ins:
		st.Underline = true
		fl.children(n, st)
		return
	case atom.Del, atom.S, atom.Strike:
		st.Strike = true
		fl.children(n, st)
		return
	case atom.Code, atom.Tt, atom.Kbd, atom.Samp:
		st.Mono = true
		fl.children(n, st)
		return
	case atom.Sup, atom.Sub, atom.Small:
		st.Size *= 0.75
		fl.children(n, st)
		return
	case atom.Big:
		st.Size *= 1.25
		fl.children(n, st)
		return
	case atom.A:
		if href := attr(n, "href"); href != "" && !strings.HasPrefix(href, "#") {
			st.link = href
			st.Underline = true
			st.Color = [3]int{0, 0, 255}
		}
		fl.children(n, st)
		return
	case atom.Span, atom.Font, atom.Abbr, atom.Acronym, atom.Q, atom.Label, atom.Mark:
		if n.DataAtom == atom.Q {
			fl.text("“", st)
			fl.children(n, st)
			fl.text("”", st)
			return
		}
		fl.children(n, st)
		return
	case atom.Br:
		fl.toks = append(fl.toks, token{kind: tokBreak, ts: st.TextStyle})
		return
	case atom.Img:
		fl.image(n, st)
		return
	case atom.Hr:
		fl.beginBlock(st.lineHeight() * 0.5)
		fl.ensureSpace(1)
		if !fl.dry {
			fl.d.Line(fl.left, fl.y+0.5, fl.right, fl.y+0.5)
		}
		fl.y += 1
		fl.started = true
		fl.endBlock(st.lineHeight() * 0.5)
		return
	}
	// ブロック
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		st.Size += float64((4 - level) * 2)
		if st.Size < 4 {
			st.Size = 4
		}
		st.Style = addStyle(st.Style, "B")
		fl.block(n, st, st.lineHeight()*0.5, st.lineHeight()*0.3)
	case atom.P, atom.Address, atom.Center, atom.Figure, atom.Caption, atom.Legend, atom.Fieldset:
		if n.DataAtom == atom.Center {
			st.align = "C"
		}
		fl.block(n, st, st.lineHeight()*0.5, st.lineHeight()*0.5)
	case atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Nav, atom.Aside, atom.Main, atom.Details, atom.Summary, atom.Figcaption, atom.Form:
		fl.block(n, st, 0, 0)
	case atom.Pre:
		fl.pre(n, st)
	case atom.Blockquote:
		fl.blockquote(n, st)
	case atom.Ul, atom.Ol, atom.Menu, atom.Dir:
		fl.list(n, st)
	case atom.Li:
		fl.block(n, st, 0, 0)
	case atom.Dl:
		fl.block(n, st, st.lineHeight()*0.5, st.lineHeight()*0.5)
	case atom.Dt:
		st.Style = addStyle(st.Style, "B")
		fl.block(n, st, 0, 0)
	case atom.Dd:
		fl.indented(5, func() { fl.block(n, st, 0, 0) })
	case atom.Table:
		fl.table(n, st)
	default:
		fl.children(n, st)
	}
}

// text はテキストを token にして溜める。
func (fl *flow) text(s string, st inlineStyle) {
	if st.pre {
		fl.toks = append(fl.toks, fl.d.tokenize(s, st.TextStyle, false, st.link)...)
		return
	}
	toks := fl.d.tokenize(s, st.TextStyle, true, st.link)
	// 連続する空白（要素をまたぐもの）は 1 つにする
	for _, t := range toks {
		if t.kind == tokSpace && len(fl.toks) > 0 && fl.toks[len(fl.toks)-1].kind == tokSpace {
			continue
		}
		fl.toks = append(fl.toks, t)
	}
}

// blockAlign は align 属性・text-align。
func blockAlign(n *html.Node, cur string) string {
	a := strings.ToLower(attr(n, "align"))
	if v := strings.ToLower(styleProp(n, "text-align")); v != "" {
		a = v
	}
	switch a {
	case "center":
		return "C"
	case "right":
		return "R"
	case "left", "justify":
		return ""
	}
	return cur
}

// block はブロック要素（上下の余白 top / bottom）。
func (fl *flow) block(n *html.Node, st inlineStyle, top, bottom float64) {
	fl.beginBlock(top)
	saved := fl.align
	st.align = blockAlign(n, st.align)
	fl.align = st.align
	fl.children(n, st)
	fl.flushInline()
	fl.align = saved
	fl.endBlock(bottom)
}

func (fl *flow) beginBlock(top float64) {
	fl.flushInline()
	fl.pending = math.Max(fl.pending, top)
}

func (fl *flow) endBlock(bottom float64) {
	fl.flushInline()
	fl.pending = math.Max(fl.pending, bottom)
}

func (fl *flow) indented(dx float64, fn func()) {
	fl.flushInline()
	fl.left += dx
	fn()
	fl.flushInline()
	fl.left -= dx
}

// applyPending は溜めた余白を加える（ブロックの先頭では加えない）。
func (fl *flow) applyPending() {
	if fl.started {
		fl.y += fl.pending
	}
	fl.pending = 0
}

// ensureSpace は高さ h が収まらなければ改ページする。
func (fl *flow) ensureSpace(h float64) {
	d := fl.d
	if fl.dry || fl.noBreak || !d.autoBreak {
		return
	}
	if fl.y+h > d.pageBreakTrigger() && fl.y > d.tMargin+0.01 {
		d.autoPageBreak()
		fl.y = d.tMargin
	}
}

// flushInline は溜めた token を行に分けて描く。
func (fl *flow) flushInline() {
	toks := fl.toks
	fl.toks = nil
	content := false
	for _, t := range toks {
		if t.kind != tokSpace {
			content = true
			break
		}
	}
	if !content {
		return
	}
	// 末尾の改行は空行にしない（<br> で終わる段落）
	for len(toks) > 0 && toks[len(toks)-1].kind == tokSpace {
		toks = toks[:len(toks)-1]
	}
	for _, t := range toks {
		if t.w > fl.maxTokW && t.kind != tokSpace {
			fl.maxTokW = t.w
		}
	}
	for _, line := range fl.d.breakLines(toks, fl.right-fl.left) {
		fl.line(line)
	}
}

// line は 1 行を描く。
func (fl *flow) line(line []token) {
	d := fl.d
	fl.applyPending()
	var asc, desc, w float64
	for _, t := range line {
		a, de := t.ascentDescent()
		asc, desc = math.Max(asc, a), math.Max(desc, de)
		if t.kind != tokBreak {
			w += t.w
		}
	}
	h := asc + desc
	fl.ensureSpace(h)
	if w > fl.maxLineW {
		fl.maxLineW = w
	}
	top := fl.y
	if !fl.dry {
		for _, dec := range fl.decor {
			dec(top, h)
		}
		if fl.marker != nil {
			m := *fl.marker
			d.drawText(fl.markerX-m.w, top+asc, m.text, m.ts)
		}
		x := fl.left
		switch fl.align {
		case "C":
			x += (fl.right - fl.left - w) / 2
		case "R":
			x += fl.right - fl.left - w
		}
		for i := 0; i < len(line); i++ {
			t := line[i]
			switch t.kind {
			case tokWord, tokSpace:
				// 同じ書式の単語と空白はまとめて描く
				text, w := t.text, t.w
				for i+1 < len(line) && (line[i+1].kind == tokWord || line[i+1].kind == tokSpace) &&
					line[i+1].ts == t.ts && line[i+1].link == t.link {
					i++
					text += line[i].text
					w += line[i].w
				}
				if strings.TrimSpace(text) != "" || t.ts.Underline || t.ts.Strike {
					d.drawText(x, top+asc, text, t.ts)
				}
				if t.link != "" {
					d.f.LinkString(x, top, w, h, t.link)
				}
				x += w
			case tokImage:
				d.f.ImageOptions(t.img, x, top+asc-t.imgH, t.w, t.imgH, false, fpdf.ImageOptions{}, 0, t.link)
				x += t.w
			}
		}
	}
	fl.marker = nil
	fl.y += h
	fl.started = true
}

// ---------------------------------------------------------------- pre / blockquote / list

func (fl *flow) pre(n *html.Node, st inlineStyle) {
	fl.beginBlock(st.lineHeight() * 0.5)
	st.pre = true
	st.Mono = true
	fl.applyPending()
	left, right := fl.left, fl.right
	d := fl.d
	fl.decor = append(fl.decor, func(top, h float64) {
		d.f.SetFillColor(0xfa, 0xfa, 0xfa)
		d.f.Rect(left, top, right-left, h, "F")
	})
	fl.left += 1
	fl.right -= 1
	// <pre> 直後の改行は無視する（HTML の規則）
	first := true
	var walk func(*html.Node, inlineStyle)
	walk = func(c *html.Node, st inlineStyle) {
		switch c.Type {
		case html.TextNode:
			s := c.Data
			if first {
				s = strings.TrimPrefix(s, "\n")
			}
			first = false
			fl.text(s, st)
		case html.ElementNode:
			if c.DataAtom == atom.Br {
				fl.toks = append(fl.toks, token{kind: tokBreak, ts: st.TextStyle})
				return
			}
			for x := c.FirstChild; x != nil; x = x.NextSibling {
				walk(x, st)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, st)
	}
	for len(fl.toks) > 0 && fl.toks[len(fl.toks)-1].kind == tokBreak {
		fl.toks = fl.toks[:len(fl.toks)-1]
	}
	fl.flushInline()
	fl.left, fl.right = left, right
	fl.decor = fl.decor[:len(fl.decor)-1]
	fl.endBlock(st.lineHeight() * 0.5)
}

func (fl *flow) blockquote(n *html.Node, st inlineStyle) {
	fl.beginBlock(st.lineHeight() * 0.5)
	left := fl.left
	d := fl.d
	fl.decor = append(fl.decor, func(top, h float64) {
		d.f.SetFillColor(0xdd, 0xdd, 0xdd)
		d.f.Rect(left+1, top, 0.8, h, "F")
	})
	st.Color = [3]int{0x55, 0x55, 0x55}
	fl.indented(5, func() { fl.children(n, st) })
	fl.decor = fl.decor[:len(fl.decor)-1]
	fl.endBlock(st.lineHeight() * 0.5)
}

var bullets = []string{"•", "◦", "▪"}

func (fl *flow) list(n *html.Node, st inlineStyle) {
	depth := 0
	for p := n.Parent; p != nil; p = p.Parent {
		if p.DataAtom == atom.Ul || p.DataAtom == atom.Ol {
			depth++
		}
	}
	top := st.lineHeight() * 0.5
	if depth > 0 {
		top = 0
	}
	fl.beginBlock(top)
	ordered := n.DataAtom == atom.Ol
	counter := 1
	if s := attr(n, "start"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			counter = v
		}
	}
	const indent = 6
	fl.indented(indent, func() {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				if c.Type == html.TextNode && strings.TrimSpace(c.Data) != "" {
					fl.text(c.Data, st)
				}
				continue
			}
			if c.DataAtom != atom.Li {
				fl.node(c, st)
				continue
			}
			fl.flushInline()
			label := bullets[min(depth, len(bullets)-1)]
			if ordered {
				label = strconv.Itoa(counter) + "."
			}
			mts := st.TextStyle
			mts.Underline, mts.Strike = false, false
			fl.marker = &token{text: label, ts: mts, w: fl.d.textWidth(label, mts)}
			fl.markerX = fl.left - 1.5
			fl.block(c, st, 0, 0)
			fl.marker = nil
			counter++
		}
	})
	fl.endBlock(top)
}

// ---------------------------------------------------------------- 画像

func (fl *flow) image(n *html.Node, st inlineStyle) {
	if fl.images == nil {
		return
	}
	src := attr(n, "src")
	data, ok := fl.images(src)
	if !ok {
		return
	}
	d := fl.d
	name, wpx, hpx, ok := d.registerImage(data)
	if !ok {
		return
	}
	k := 72 / 25.4
	scale := d.imageScale
	if scale <= 0 {
		scale = 1
	}
	if v, err := strconv.ParseFloat(strings.TrimSuffix(attr(n, "width"), "px"), 64); err == nil && v > 0 {
		if hv, err := strconv.ParseFloat(strings.TrimSuffix(attr(n, "height"), "px"), 64); err == nil && hv > 0 {
			hpx = hv
		} else {
			hpx = hpx * v / wpx
		}
		wpx = v
	}
	w := wpx / (scale * k)
	h := hpx / (scale * k)
	if maxW := fl.right - fl.left; w > maxW {
		h *= maxW / w
		w = maxW
	}
	if maxH := d.pageBreakTrigger() - d.tMargin - 2; h > maxH {
		w *= maxH / h
		h = maxH
	}
	fl.toks = append(fl.toks, token{kind: tokImage, img: name, w: w, imgH: h, ts: st.TextStyle, link: st.link})
}

// registerImage は画像を fpdf に登録し、名前と大きさ（px）を返す。
func (d *Doc) registerImage(data []byte) (name string, w, h float64, ok bool) {
	sum := sha1.Sum(data)
	name = "img" + hex.EncodeToString(sum[:8])
	if info := d.f.GetImageInfo(name); info != nil {
		return name, info.Width(), info.Height(), true
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 || cfg.Width*cfg.Height > 50_000_000 {
		return "", 0, 0, false
	}
	var r *bytes.Reader
	typ := "PNG"
	switch format {
	case "jpeg":
		typ = "JPG"
		r = bytes.NewReader(data)
	default:
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return "", 0, 0, false
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			// 透過の無い画像は JPEG にしても良いが、PNG で失敗することはまず無い
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
				return "", 0, 0, false
			}
			typ = "JPG"
		}
		r = bytes.NewReader(buf.Bytes())
	}
	info := d.f.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: typ}, r)
	if d.f.Err() || info == nil {
		d.f.ClearError()
		return "", 0, 0, false
	}
	return name, float64(cfg.Width), float64(cfg.Height), true
}

// ---------------------------------------------------------------- 表

type tableCell struct {
	n       *html.Node
	colspan int
	head    bool
}

func tableRows(n *html.Node) [][]tableCell {
	var rows [][]tableCell
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		for x := c.FirstChild; x != nil; x = x.NextSibling {
			if x.Type != html.ElementNode {
				continue
			}
			switch x.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(x)
			case atom.Tr:
				var row []tableCell
				for td := x.FirstChild; td != nil; td = td.NextSibling {
					if td.Type == html.ElementNode && (td.DataAtom == atom.Td || td.DataAtom == atom.Th) {
						span, _ := strconv.Atoi(attr(td, "colspan"))
						row = append(row, tableCell{n: td, colspan: max(span, 1), head: td.DataAtom == atom.Th})
					}
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
			}
		}
	}
	walk(n)
	return rows
}

// sub は (left, right) の範囲で y から描く子の flow。
func (fl *flow) sub(left, right, y float64, dry bool) *flow {
	return &flow{d: fl.d, dry: dry, noBreak: true, left: left, right: right, y: y, images: fl.images}
}

func cellStyle(c tableCell, st inlineStyle) inlineStyle {
	if c.head {
		st.Style = addStyle(st.Style, "B")
		st.align = "C"
	}
	st.align = blockAlign(c.n, st.align)
	return st
}

// cellContent はセルの内容を描く。
func (fl *flow) cellContent(c tableCell, st inlineStyle) {
	st = cellStyle(c, st)
	fl.align = st.align
	fl.children(c.n, st)
	fl.flushInline()
}

func (fl *flow) table(n *html.Node, st inlineStyle) {
	rows := tableRows(n)
	if len(rows) == 0 {
		return
	}
	fl.beginBlock(st.lineHeight() * 0.5)
	fl.applyPending()
	ncols := 0
	for _, r := range rows {
		c := 0
		for _, cell := range r {
			c += cell.colspan
		}
		ncols = max(ncols, c)
	}
	const pad = 1.0
	avail := fl.right - fl.left
	nat := make([]float64, ncols)
	minw := make([]float64, ncols)
	for _, r := range rows {
		col := 0
		for _, cell := range r {
			if cell.colspan == 1 && col < ncols {
				m := fl.sub(0, 10000, 0, true)
				m.cellContent(cell, st)
				nat[col] = math.Max(nat[col], m.maxLineW+2*pad+0.5)
				minw[col] = math.Max(minw[col], m.maxTokW+2*pad+0.5)
			}
			col += cell.colspan
		}
	}
	widths := tableWidths(nat, minw, avail)
	for _, r := range rows {
		// 行の高さ
		var rowH float64
		col := 0
		xs := make([]float64, len(r))
		ws := make([]float64, len(r))
		x := fl.left
		for i, cell := range r {
			w := 0.0
			for j := col; j < col+cell.colspan && j < ncols; j++ {
				w += widths[j]
			}
			xs[i], ws[i] = x, w
			m := fl.sub(x+pad, x+w-pad, 0, true)
			m.cellContent(cell, st)
			rowH = math.Max(rowH, m.y+2*pad)
			x += w
			col += cell.colspan
		}
		rowH = math.Max(rowH, st.lineHeight()+2*pad)
		fl.ensureSpace(rowH)
		if !fl.dry {
			d := fl.d
			for i, cell := range r {
				if cell.head {
					d.f.SetFillColor(0xee, 0xee, 0xee)
					d.f.Rect(xs[i], fl.y, ws[i], rowH, "F")
				}
				d.f.SetDrawColor(0, 0, 0)
				d.f.SetLineWidth(d.lineWidth)
				d.f.Rect(xs[i], fl.y, ws[i], rowH, "D")
				m := fl.sub(xs[i]+pad, xs[i]+ws[i]-pad, fl.y+pad, false)
				m.cellContent(cell, st)
			}
		}
		fl.y += rowH
		fl.started = true
	}
	fl.maxLineW = math.Max(fl.maxLineW, avail)
	fl.endBlock(st.lineHeight() * 0.5)
}

// tableWidths は列幅（表は幅いっぱい。内容の自然な幅に比例し、最長の単語より狭くしない）。
func tableWidths(nat, minw []float64, avail float64) []float64 {
	n := len(nat)
	out := make([]float64, n)
	var sumNat, sumMin float64
	for i := range nat {
		nat[i] = math.Max(nat[i], minw[i])
		sumNat += nat[i]
		sumMin += minw[i]
	}
	switch {
	case sumNat <= 0:
		for i := range out {
			out[i] = avail / float64(n)
		}
	case sumNat <= avail:
		for i := range out {
			out[i] = nat[i] * avail / sumNat
		}
	case sumMin >= avail:
		for i := range out {
			out[i] = minw[i] * avail / sumMin
		}
	default:
		extra := avail - sumMin
		for i := range out {
			out[i] = minw[i] + extra*(nat[i]-minw[i])/(sumNat-sumMin)
		}
	}
	return out
}
