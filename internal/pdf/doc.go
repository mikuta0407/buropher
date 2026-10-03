// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package pdf は Redmine::Export::PDF::ITCPDF（rbpdf / TCPDF）の近似実装。
//
// Redmine の PDF 出力（issue_to_pdf / issues_to_pdf / wiki_page_to_pdf / Gantt#to_pdf）が使う
// TCPDF の API（Cell / MultiCell / GetStringHeight / writeHTMLCell ...）を、純 Go の
// github.com/go-pdf/fpdf の上に同じ座標系（mm・A4・余白 10mm・セル内余白 1mm・行の高さ = 文字サイズ × 1.25）で
// 実装する。バイト単位の一致は目指さず、見た目の近似を目標にする（Q-05）。
//
// 文字は 1 文字ずつ、ロケールのフォントの優先順（fonts.go）で最初にグリフを持つフォントで描く。
// どのフォントにも無い文字は "?" にする。
package pdf

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/go-pdf/fpdf"

	"github.com/mikuta0407/buropher/internal/brand"
)

const (
	ptToMM = 25.4 / 72
	// cellHeightRatio は TCPDF の K_CELL_HEIGHT_RATIO。
	cellHeightRatio = 1.25
)

// Options は New のオプション。
type Options struct {
	// Locale は current_language（フォントの選択に使う）。
	Locale string
	// Orientation は "P"（縦）または "L"（横）。
	Orientation string
	// NoCompress はページの内容を圧縮しない（テストで内容を調べるため）。
	NoCompress bool
	// CreationDate は文書の作成日時（ゼロなら現在時刻）。
	CreationDate time.Time
}

// TextStyle は文字の書式。
type TextStyle struct {
	// Mono は等幅フォント。
	Mono bool
	// Style は "", "B", "I", "BI"。
	Style string
	// Size はポイント。
	Size      float64
	Underline bool
	Strike    bool
	Color     [3]int
}

func (ts TextStyle) sizeMM() float64 { return ts.Size * ptToMM }

func (ts TextStyle) lineHeight() float64 { return ts.sizeMM() * cellHeightRatio }

// Doc は 1 つの PDF 文書（ITCPDF のインスタンス）。
type Doc struct {
	f    *fpdf.Fpdf
	fs   *FontSet
	m    *metrics
	main []*face
	mono []*face

	locale string
	// FooterDate は footer_date（フッター左の日付）。
	FooterDate string

	ts        TextStyle
	fill      [3]int
	cMargin   float64
	lineWidth float64
	lasth     float64

	lMargin, tMargin, rMargin float64
	autoBreak                 bool
	bMargin                   float64
	// imageScale は set_image_scale。
	imageScale float64

	registered map[string]bool
	images     map[string]bool
	hooks      []*breakHook
	err        error
}

// breakHook は自動改ページの前後に呼ばれる処理（枠線の描画など）。
type breakHook struct {
	before func()
	after  func()
}

// New は ITCPDF.new(lang, orientation)。
func New(fs *FontSet, o Options) *Doc {
	if fs == nil {
		fs = Default()
	}
	orient := o.Orientation
	if orient != "L" {
		orient = "P"
	}
	f := fpdf.New(orient, "mm", "A4", "")
	f.SetCompression(!o.NoCompress)
	created := o.CreationDate
	if created.IsZero() {
		created = time.Now()
	}
	f.SetCreationDate(created)
	f.SetModificationDate(created)
	f.SetCreator(brand.Name, true)
	f.SetProducer("buropher", true)
	f.SetDisplayMode("default", "OneColumn")
	f.SetAutoPageBreak(false, 0)
	f.SetMargins(10, 10, 10)
	f.SetLineWidth(0.2)
	f.SetLineCapStyle("butt")
	d := &Doc{f: f, fs: fs, m: newMetrics(), locale: o.Locale,
		cMargin: 1, lineWidth: 0.2, lMargin: 10, tMargin: 10, rMargin: 10,
		autoBreak: true, bMargin: 20, imageScale: 1,
		ts:         TextStyle{Size: 12},
		registered: map[string]bool{}, images: map[string]bool{}}
	d.main, d.mono = fs.chains(o.Locale)
	return d
}

// Fpdf は下層の fpdf（直接の描画用）。
func (d *Doc) Fpdf() *fpdf.Fpdf { return d.f }

// SetTitle は set_title。
func (d *Doc) SetTitle(s string) { d.f.SetTitle(s, true) }

// AddPage は AddPage(orientation)。
func (d *Doc) AddPage(orientation ...string) {
	if len(orientation) > 0 && orientation[0] != "" {
		d.f.AddPageFormat(orientation[0], d.f.GetPageSizeStr("A4"))
	} else {
		d.f.AddPage()
	}
	d.f.SetXY(d.lMargin, d.tMargin)
}

// PageNo は現在のページ番号。
func (d *Doc) PageNo() int { return d.f.PageNo() }

// PageWidth / PageHeight は現在のページの幅・高さ（mm）。
func (d *Doc) PageWidth() float64  { w, _ := d.f.GetPageSize(); return w }
func (d *Doc) PageHeight() float64 { _, h := d.f.GetPageSize(); return h }

// LeftMargin / RightMargin は original_margins。
func (d *Doc) LeftMargin() float64  { return d.lMargin }
func (d *Doc) RightMargin() float64 { return d.rMargin }

// FooterMargin は get_footer_margin。
func (d *Doc) FooterMargin() float64 { return 10 }

// CellMargin は get_margins['cell']。
func (d *Doc) CellMargin() float64 { return d.cMargin }

// SetAutoPageBreak は set_auto_page_break(auto, margin)。
func (d *Doc) SetAutoPageBreak(auto bool, margin ...float64) {
	d.autoBreak = auto
	if len(margin) > 0 {
		d.bMargin = margin[0]
	}
}

// SetImageScale は set_image_scale。
func (d *Doc) SetImageScale(s float64) { d.imageScale = s }

// pageBreakTrigger は自動改ページの位置。
func (d *Doc) pageBreakTrigger() float64 { return d.PageHeight() - d.bMargin }

// X / Y / SetX / SetY / SetXY は GetX / GetY / SetX / SetY / SetXY（負の値は右端・下端から）。
func (d *Doc) X() float64 { return d.f.GetX() }
func (d *Doc) Y() float64 { return d.f.GetY() }
func (d *Doc) SetX(x float64) {
	if x < 0 {
		x = d.PageWidth() + x
	}
	d.f.SetX(x)
}

// SetY は SetY（TCPDF と同じく x を左余白に戻す）。
func (d *Doc) SetY(y float64) {
	if y < 0 {
		y = d.PageHeight() + y
	}
	d.f.SetXY(d.lMargin, y)
}
func (d *Doc) SetXY(x, y float64) { d.SetY(y); d.SetX(x) }

// Ln は Ln(h)（h を省略すると直前のセルの高さ）。
func (d *Doc) Ln(h ...float64) {
	dy := d.lasth
	if len(h) > 0 {
		dy = h[0]
	}
	d.f.SetXY(d.lMargin, d.Y()+dy)
}

// SetFontStyle は SetFontStyle(style, size)（本文のフォント）。
func (d *Doc) SetFontStyle(style string, size float64) {
	d.ts.Mono = false
	d.ts.Style = normStyle(style)
	if size > 0 {
		d.ts.Size = size
	}
}

// SetMonoStyle は等幅フォントを選ぶ。
func (d *Doc) SetMonoStyle(style string, size float64) {
	d.SetFontStyle(style, size)
	d.ts.Mono = true
}

// Style は現在の文字の書式。
func (d *Doc) Style() TextStyle { return d.ts }

func normStyle(s string) string {
	s = strings.ToUpper(s)
	b, i := strings.Contains(s, "B"), strings.Contains(s, "I")
	switch {
	case b && i:
		return "BI"
	case b:
		return "B"
	case i:
		return "I"
	}
	return ""
}

// SetTextColor は SetTextColor(r, g, b)（g, b を省略すると灰色）。
func (d *Doc) SetTextColor(r int, gb ...int) {
	g, b := r, r
	if len(gb) >= 2 {
		g, b = gb[0], gb[1]
	}
	d.ts.Color = [3]int{r, g, b}
}

// SetFillColor は SetFillColor。
func (d *Doc) SetFillColor(r, g, b int) { d.fill = [3]int{r, g, b} }

// Line は Line。
func (d *Doc) Line(x1, y1, x2, y2 float64) {
	d.f.SetDrawColor(0, 0, 0)
	d.f.SetLineWidth(d.lineWidth)
	d.f.Line(x1, y1, x2, y2)
}

// Bookmark は bookmark(txt, level, y)（y < 0 なら現在位置）。
func (d *Doc) Bookmark(txt string, level int) { d.f.Bookmark(txt, level, -1) }

// ---------------------------------------------------------------- 文字

// run は同じフォントで描く文字列。
type run struct {
	face   *face
	style  string
	synth  bool
	italic bool
	text   string
	widthM float64 // em 単位の幅の合計
}

func (d *Doc) chain(ts TextStyle) []*face {
	if ts.Mono {
		return d.mono
	}
	return d.main
}

// cleanRune は描けない制御文字を除く（タブは空白）。
func cleanRune(r rune) (rune, bool) {
	switch {
	case r == '\t':
		return ' ', true
	case r == ' ':
		return ' ', true
	case r < 0x20 || r == 0x7f:
		return 0, false
	case unicode.Is(unicode.Mn, r) && r >= 0xfe00 && r <= 0xfe0f:
		// 異体字セレクタ
		return 0, false
	case r == '\u200b' || r == '\ufeff':
		return 0, false
	}
	return r, true
}

// runs は s をフォントごとの run に分ける。
func (d *Doc) runs(s string, ts TextStyle) []run {
	chain := d.chain(ts)
	var out []run
	var cur *run
	var b strings.Builder
	flush := func() {
		if cur != nil {
			cur.text = b.String()
			out = append(out, *cur)
			cur = nil
			b.Reset()
		}
	}
	for _, r := range s {
		r, ok := cleanRune(r)
		if !ok {
			continue
		}
		var f *face
		if cur != nil && (r == ' ' || d.m.hasRune(cur.face, r)) {
			f = cur.face
		} else {
			for _, c := range chain {
				if d.m.hasRune(c, r) {
					f = c
					break
				}
			}
			if f == nil {
				r = '?'
				f = chain[0]
				if cur != nil {
					f = cur.face
				}
			}
		}
		if cur == nil || cur.face != f {
			flush()
			st, synth, italic := f.style(ts.Style)
			cur = &run{face: f, style: st, synth: synth, italic: italic}
		}
		b.WriteRune(r)
		cur.widthM += d.m.advance(f, cur.style, r)
	}
	flush()
	return out
}

// StringWidth は GetStringWidth。
func (d *Doc) StringWidth(s string) float64 { return d.textWidth(s, d.ts) }

func (d *Doc) textWidth(s string, ts TextStyle) float64 {
	var w float64
	for _, r := range d.runs(s, ts) {
		w += r.widthM
	}
	return w * ts.sizeMM()
}

// useFont は fpdf にフォントを登録して選ぶ。
func (d *Doc) useFont(r run, ts TextStyle) {
	key := r.face.id + "/" + r.style
	if !d.registered[key] {
		d.registered[key] = true
		data, err := r.face.files[r.style]()
		if err != nil {
			d.err = err
			return
		}
		d.f.AddUTF8FontFromBytes(r.face.id, r.style, data)
	}
	st := r.style
	if ts.Underline {
		st += "U"
	}
	if ts.Strike {
		st += "S"
	}
	d.f.SetFont(r.face.id, st, ts.Size)
}

// drawText は x の位置・ベースライン y に s を描き、幅を返す。
func (d *Doc) drawText(x, y float64, s string, ts TextStyle) float64 {
	start := x
	for _, r := range d.runs(s, ts) {
		if r.text == "" {
			continue
		}
		d.useFont(r, ts)
		d.f.SetTextColor(ts.Color[0], ts.Color[1], ts.Color[2])
		if r.synth {
			d.f.SetDrawColor(ts.Color[0], ts.Color[1], ts.Color[2])
			d.f.SetLineWidth(ts.sizeMM() * 0.04)
			d.f.SetTextRenderingMode(2)
		}
		if r.italic {
			// 斜体のフォントが無ければ傾けて描く
			d.f.TransformBegin()
			d.f.TransformSkewX(12, x, y)
		}
		d.f.Text(x, y, r.text)
		if r.italic {
			d.f.TransformEnd()
		}
		if r.synth {
			d.f.SetTextRenderingMode(0)
			d.f.SetLineWidth(d.lineWidth)
			d.f.SetDrawColor(0, 0, 0)
		}
		x += r.widthM * ts.sizeMM()
	}
	return x - start
}

// baseline は高さ h の行の上端 top に対する文字のベースライン（fpdf の Cell と同じ）。
func baseline(top, h float64, ts TextStyle) float64 { return top + h/2 + 0.3*ts.sizeMM() }

// ---------------------------------------------------------------- セル

// drawBorder は TCPDF の border（"1" / "0" / "" / "LTRB" の組み合わせ）を描く。
func (d *Doc) drawBorder(x, y, w, h float64, border string) {
	if border == "" || border == "0" {
		return
	}
	d.f.SetDrawColor(0, 0, 0)
	d.f.SetLineWidth(d.lineWidth)
	if border == "1" {
		d.f.Rect(x, y, w, h, "D")
		return
	}
	if strings.Contains(border, "L") {
		d.f.Line(x, y, x, y+h)
	}
	if strings.Contains(border, "T") {
		d.f.Line(x, y, x+w, y)
	}
	if strings.Contains(border, "R") {
		d.f.Line(x+w, y, x+w, y+h)
	}
	if strings.Contains(border, "B") {
		d.f.Line(x, y+h, x+w, y+h)
	}
}

func (d *Doc) fillRect(x, y, w, h float64) {
	d.f.SetFillColor(d.fill[0], d.fill[1], d.fill[2])
	d.f.Rect(x, y, w, h, "F")
}

// checkPageBreak は高さ h が収まらなければ改ページする（TCPDF の checkPageBreak）。
func (d *Doc) checkPageBreak(h float64) bool {
	if !d.autoBreak || d.Y()+h <= d.pageBreakTrigger() || d.Y() <= d.tMargin {
		return false
	}
	x := d.X()
	d.autoPageBreak()
	d.SetX(x)
	return true
}

// autoPageBreak は自動改ページ（フックを呼ぶ）。
func (d *Doc) autoPageBreak() {
	for i := len(d.hooks) - 1; i >= 0; i-- {
		if d.hooks[i].before != nil {
			d.hooks[i].before()
		}
	}
	w, h := d.f.GetPageSize()
	orient := "P"
	if w > h {
		orient = "L"
	}
	d.AddPage(orient)
	for _, hk := range d.hooks {
		if hk.after != nil {
			hk.after()
		}
	}
}

func (d *Doc) cellWidth(w float64) float64 {
	if w <= 0 {
		return d.PageWidth() - d.rMargin - d.X()
	}
	return w
}

// Cell は Cell / RDMCell(w, h, txt, border, ln, align, fill)。
func (d *Doc) Cell(w, h float64, txt, border string, ln int, align string, fill bool) {
	d.checkPageBreak(h)
	w = d.cellWidth(w)
	x, y := d.X(), d.Y()
	if fill {
		d.fillRect(x, y, w, h)
	}
	d.drawBorder(x, y, w, h, border)
	if txt != "" {
		tw := d.textWidth(txt, d.ts)
		tx := x + d.cMargin
		switch align {
		case "C":
			tx = x + (w-tw)/2
		case "R":
			tx = x + w - d.cMargin - tw
		}
		d.drawText(tx, baseline(y, h, d.ts), txt, d.ts)
	}
	d.lasth = h
	switch ln {
	case 1:
		d.f.SetXY(d.lMargin, y+h)
	case 2:
		d.f.SetXY(x, y+h)
	default:
		d.f.SetXY(x+w, y)
	}
}

// StringHeight は get_string_height(w, txt)。
func (d *Doc) StringHeight(w float64, txt string) float64 {
	lines := d.wrapPlain(txt, w-2*d.cMargin, d.ts)
	return float64(len(lines))*d.ts.lineHeight() + 2*d.cMargin
}

// MultiCell は MultiCell / RDMMultiCell(w, h, txt, border, align, fill, ln)。
// h は最小の高さ。ln は 0（右へ）、1（次の行の左端）、2（下へ）。
func (d *Doc) MultiCell(w, h float64, txt, border, align string, fill bool, ln int) {
	w = d.cellWidth(w)
	lines := d.wrapPlain(txt, w-2*d.cMargin, d.ts)
	lh := d.ts.lineHeight()
	total := math.Max(h, float64(len(lines))*lh+2*d.cMargin)
	x := d.X()
	if d.autoBreak && d.Y()+total > d.pageBreakTrigger() && total <= d.pageBreakTrigger()-d.tMargin && d.Y() > d.tMargin {
		d.autoPageBreak()
		d.f.SetX(x)
	}
	y := d.Y()
	if fill {
		d.fillRect(x, y, w, total)
	}
	d.drawBorder(x, y, w, total, border)
	ty := y + d.cMargin
	for _, line := range lines {
		tw := d.textWidth(line, d.ts)
		tx := x + d.cMargin
		switch align {
		case "C":
			tx = x + (w-tw)/2
		case "R":
			tx = x + w - d.cMargin - tw
		}
		d.drawText(tx, baseline(ty, lh, d.ts), line, d.ts)
		ty += lh
	}
	d.lasth = total
	switch ln {
	case 0:
		d.f.SetXY(x+w, y)
	case 2:
		d.f.SetXY(x, y+total)
	default:
		d.f.SetXY(d.lMargin, y+total)
	}
}

// ---------------------------------------------------------------- 出力

// footer は ITCPDF#Footer（全ページの数が分かってから各ページに描く）。
func (d *Doc) footer(page, pages int) {
	ts := TextStyle{Style: "I", Size: 8}
	y := d.PageHeight() - d.FooterMargin()
	// SetPage で戻ったページでもフォントと色を確実に出力させる
	d.f.SetFontSize(1)
	d.f.SetFillColor(0, 0, 0)
	d.drawText(15, baseline(y, 5, ts), d.FooterDate, ts)
	s := fmt.Sprintf("%d/%d", page, pages)
	x := d.PageWidth() - 30
	w := d.PageWidth() - d.rMargin - x
	d.drawText(x+(w-d.textWidth(s, ts))/2, baseline(y, 5, ts), s, ts)
}

// Output は PDF のバイト列。
func (d *Doc) Output() ([]byte, error) {
	if d.f.PageCount() == 0 {
		d.AddPage()
	}
	n := d.f.PageCount()
	cur := d.f.PageNo()
	for p := 1; p <= n; p++ {
		d.f.SetPage(p)
		d.footer(p, n)
	}
	d.f.SetPage(cur)
	if d.err != nil {
		return nil, d.err
	}
	var buf bytes.Buffer
	if err := d.f.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
