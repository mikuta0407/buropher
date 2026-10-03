// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// Redmine::Helpers::Gantt#to_pdf（pdf_subject / pdf_task / pdf_new_page?）の移植。

import (
	"html"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/pdf"
	"github.com/mikuta0407/buropher/internal/query"
)

// Gantt::PDF の定数。
const (
	ganttPDFMaxCharsForSubject = 45
	ganttPDFTotalWidth         = 280.0
	ganttPDFLeftPaneWidth      = 100.0
)

// ganttPDF は :format => :pdf の描画先。
type ganttPDF struct {
	d            *pdf.Doc
	zoom         float64
	subjectWidth float64
	gWidth       float64
	// top は options[:top]（mm。ganttOptions.top は整数のため別に持つ）。
	top float64
}

// renderEnd は render_end（最後の行の下線）。
func (p *ganttPDF) renderEnd() {
	p.d.Line(15, p.top, ganttPDFTotalWidth, p.top)
}

// ganttShowPDF は gantts#show の format.pdf（send_data(@gantt.to_pdf, :filename => "#{basename}.pdf")）。
func (a *App) ganttShowPDF(c *Req, g *ganttChart) {
	basename := "gantt"
	if c.Project != nil {
		basename = c.Project.Identifier + "-gantt"
	}
	a.renderPDF(c, basename+".pdf", false, func() (*pdf.Doc, error) { return g.toPDF(), nil })
}

// toPDF は Gantt#to_pdf。
func (g *ganttChart) toPDF() *pdf.Doc {
	c := g.c
	d := g.a.newPDF(c, "P")
	pname := ""
	if g.Project != nil {
		pname = g.Project.Name
	}
	d.SetTitle(c.L("label_gantt") + " " + pname)
	d.AddPage("L")
	d.SetFontStyle("B", 12)
	d.SetX(15)
	d.Cell(ganttPDFLeftPaneWidth, 20, pname, "", 0, "", false)
	d.Ln()
	d.SetFontStyle("B", 9)
	subjectWidth := ganttPDFLeftPaneWidth
	const headerHeight = 5.0
	headersHeight := headerHeight
	showWeeks, showDays, showDayNum := false, false, false
	if g.Months < 7 {
		showWeeks = true
		headersHeight = 2 * headerHeight
		if g.Months < 3 {
			showDays = true
			headersHeight = 3 * headerHeight
			if g.Months < 2 {
				showDayNum = true
				headersHeight = 4 * headerHeight
			}
		}
	}
	gWidth := ganttPDFTotalWidth - ganttPDFLeftPaneWidth
	nDays := int(ganttDays(g.DateTo) - ganttDays(g.DateFrom) + 1)
	zoom := gWidth / float64(nDays)
	yStart := d.Y()
	// Months headers
	monthF := g.DateFrom
	left := subjectWidth
	for range g.Months {
		next := monthF.AddDate(0, 1, 0)
		width := float64(ganttDays(next)-ganttDays(monthF)) * zoom
		d.SetY(yStart)
		d.SetX(left)
		d.Cell(width, headerHeight, strconv.Itoa(monthF.Year())+"-"+strconv.Itoa(int(monthF.Month())), "LTR", 0, "C", false)
		left += width
		monthF = next
	}
	// Weeks headers
	if showWeeks {
		left = subjectWidth
		var weekF time.Time
		if cw := isoWeekday(g.DateFrom); cw == 1 {
			weekF = g.DateFrom
		} else {
			weekF = g.DateFrom.AddDate(0, 0, 7-cw+1)
			width := float64(7-cw+1)*zoom - 1
			d.SetY(yStart + headerHeight)
			d.SetX(left)
			d.Cell(width+1, headerHeight, "", "LTR", 0, "", false)
			left += width + 1
		}
		for !weekF.After(g.DateTo) {
			var width float64
			if !weekF.AddDate(0, 0, 6).After(g.DateTo) {
				width = 7 * zoom
			} else {
				width = float64(ganttDays(g.DateTo)-ganttDays(weekF)+1) * zoom
			}
			d.SetY(yStart + headerHeight)
			d.SetX(left)
			label := ""
			if width >= 5 {
				_, wk := weekF.ISOWeek()
				label = strconv.Itoa(wk)
			}
			d.Cell(width, headerHeight, label, "LTR", 0, "C", false)
			left += width
			weekF = weekF.AddDate(0, 0, 7)
		}
	}
	gray := func(day time.Time) {
		if slices.Contains(g.nonWorking, isoWeekday(day)) {
			d.SetTextColor(150)
		} else {
			d.SetTextColor(0)
		}
	}
	// Day numbers headers
	if showDayNum {
		left = subjectWidth
		d.SetFontStyle("B", 7)
		for i := range nDays {
			day := g.DateFrom.AddDate(0, 0, i)
			d.SetY(yStart + headerHeight*2)
			d.SetX(left)
			gray(day)
			d.Cell(zoom, headerHeight, strconv.Itoa(day.Day()), "LTR", 0, "C", false)
			left += zoom
		}
	}
	// Days headers
	if showDays {
		left = subjectWidth
		d.SetFontStyle("B", 7)
		row := 2.0
		if showDayNum {
			row = 3
		}
		for i := range nDays {
			day := g.DateFrom.AddDate(0, 0, i)
			d.SetY(yStart + headerHeight*row)
			d.SetX(left)
			gray(day)
			d.Cell(zoom, headerHeight, c.Loc.DayLetter(isoWeekday(day)), "LTR", 0, "C", false)
			left += zoom
		}
	}
	d.SetY(yStart)
	d.SetX(15)
	d.SetTextColor(0)
	d.Cell(subjectWidth+gWidth-15, headersHeight, "", "1", 0, "", false)
	// Tasks
	top := headersHeight + yStart
	p := &ganttPDF{d: d, zoom: zoom, subjectWidth: subjectWidth, gWidth: gWidth, top: top}
	g.render(ganttOptions{topIncrement: 5, indentIncrement: 5, pdf: p})
	return d
}

// pdfObjectRow は render_object_row の pdf_subject と line_for_*（pdf_task）。
func (g *ganttChart) pdfObjectRow(obj any, o *ganttOptions) {
	p := o.pdf
	if o.only != "lines" && o.only != "selected_columns" {
		var subject string
		switch x := obj.(type) {
		case *domain.Project:
			subject = x.Name
		case *ganttVersion:
			pn := ""
			if x.Project != nil {
				pn = x.Project.Name
			}
			subject = pn + " - " + x.Name
		case *query.IssueRow:
			subject = x.Subject
		}
		p.subject(o, subject)
	}
	if o.only == "subjects" || o.only == "selected_columns" {
		p.top += float64(o.topIncrement)
		return
	}
	switch x := obj.(type) {
	case *domain.Project:
		info := g.projectInfo[x.ID]
		if info != nil && info.StartDate != nil && info.DueDate != nil {
			p.task(g.pdfCoordinates(info.StartDate, info.DueDate, nil, p.zoom), true, x.Name)
		}
	case *ganttVersion:
		if x.EffectiveDate.Valid && x.StartDate != nil {
			label := html.EscapeString(x.Name) + " " + strconv.FormatInt(int64(math.Round(x.VisiblePercent)), 10) + "%"
			if g.Project == nil || g.Project.ID != x.ProjectID {
				pn := ""
				if x.Project != nil {
					pn = x.Project.Name
				}
				label = html.EscapeString(pn+" -") + label
			}
			due := x.EffectiveDate.Date.Time
			pct := x.VisiblePercent
			p.task(g.pdfCoordinates(x.StartDate, &due, &pct, p.zoom), true, label)
		}
	case *query.IssueRow:
		if due := g.issueDueBefore(x); due != nil {
			label := g.l.status(x.StatusID).Name
			if !slices.Contains(g.l.tracker(x.TrackerID).DisabledCoreFields, "done_ratio") {
				label += " " + strconv.Itoa(g.l.doneRatio(x)) + "%"
			}
			done := float64(g.l.doneRatio(x))
			p.task(g.pdfCoordinates(x.StartDate, due, &done, p.zoom), g.l.hasChildren(x.ID), label)
		}
	}
	p.top += float64(o.topIncrement)
}

// newPage は pdf_new_page?。
func (p *ganttPDF) newPage() {
	if p.top > 180 {
		p.d.Line(15, p.top, ganttPDFTotalWidth, p.top)
		p.d.AddPage("L")
		p.top = 15
		p.d.Line(15, p.top-0.1, ganttPDFTotalWidth, p.top-0.1)
	}
}

// subject は pdf_subject（subject.sub(/^(.{char_limit}[^\s]*\s).*$/, '\1 (...)')）。
func (p *ganttPDF) subject(o *ganttOptions, subject string) {
	p.newPage()
	d := p.d
	d.SetY(p.top)
	d.SetX(15)
	limit := max(ganttPDFMaxCharsForSubject-o.indent, 0)
	d.Cell(p.subjectWidth-15, 5, strings.Repeat(" ", max(o.indent, 0))+truncateGanttSubject(subject, limit), "LR", 0, "", false)
	d.SetY(p.top)
	d.SetX(p.subjectWidth)
	d.Cell(p.gWidth, 5, "", "LR", 0, "", false)
}

// truncateGanttSubject は /^(.{n}[^\s]*\s).*$/ を '\1 (...)' に置き換える。
func truncateGanttSubject(s string, n int) string {
	line := s
	rest := ""
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		line, rest = s[:i], s[i:]
	}
	if utf8.RuneCountInString(line) <= n {
		return s
	}
	re := regexp.MustCompile(`^(.{` + strconv.Itoa(n) + `}\S*\s)`)
	m := re.FindStringSubmatch(line)
	if m == nil {
		return s
	}
	return m[1] + " (...)" + rest
}

// task は pdf_task(params, coords, markers, label, object)。
func (p *ganttPDF) task(c ganttCoords, markers bool, label string) {
	d := p.d
	height := 2.0
	if markers {
		height /= 2
	}
	sw := p.subjectWidth
	if c.barStart != nil && c.barEnd != nil {
		bar := func(end int, r, g, b int) {
			width := math.Max(1, float64(end-*c.barStart))
			d.SetY(p.top + 1.5)
			d.SetX(sw + float64(*c.barStart))
			d.SetFillColor(r, g, b)
			d.Cell(width, height, "", "", 0, "", true)
		}
		bar(*c.barEnd, 200, 200, 200)
		if c.barLateEnd != nil {
			bar(*c.barLateEnd, 255, 100, 100)
		}
		if c.barProgressEnd != nil {
			bar(*c.barProgressEnd, 90, 200, 90)
		}
	}
	if markers {
		for _, pos := range []*int{c.start, c.end} {
			if pos == nil {
				continue
			}
			d.SetY(p.top + 1)
			d.SetX(sw + float64(*pos) - 1)
			d.SetFillColor(50, 50, 200)
			d.Cell(2, 2, "", "", 0, "", true)
		}
	}
	if label != "" {
		end := 0
		if c.barEnd != nil {
			end = *c.barEnd
		}
		d.SetX(sw + float64(end) + 5)
		d.Cell(30, 2, label, "", 0, "", false)
	}
}

// pdfCoordinates は coordinates(start_date, end_date, progress, zoom)（zoom は mm / 日の小数）。
func (g *ganttChart) pdfCoordinates(startDate, endDate *time.Time, progress *float64, zoom float64) ganttCoords {
	var c ganttCoords
	from, to := ganttDays(g.DateFrom), ganttDays(g.DateTo)
	if startDate == nil || endDate == nil {
		return c
	}
	sd, ed := ganttDays(*startDate), ganttDays(*endDate)
	if !(sd <= to && ed >= from) {
		return c
	}
	px := func(days float64) *int { return intp(int(math.Floor(days * zoom))) }
	if sd >= from {
		c.start = px(float64(sd - from))
		c.barStart = px(float64(sd - from))
	} else {
		c.barStart = intp(0)
	}
	if ed <= to {
		c.end = px(float64(ed - from + 1))
		c.barEnd = px(float64(ed - from + 1))
	} else {
		c.barEnd = px(float64(to - from + 1))
	}
	if progress != nil {
		pd := calcProgressDate(*startDate, *endDate, *progress)
		if pd.cmp(from) > 0 && pd.cmp(sd) > 0 {
			if pd.cmp(to) < 0 {
				c.barProgressEnd = px(float64(pd.days-from) + float64(pd.ns)/float64(ganttDayNS))
			} else {
				c.barProgressEnd = px(float64(to - from + 1))
			}
		}
		if pd.cmp(ganttDays(g.today)) <= 0 {
			late := min(ganttDays(g.today), ed) + 1
			if late > from && late > sd {
				if late < to {
					c.barLateEnd = px(float64(late - from))
				} else {
					c.barLateEnd = px(float64(to - from + 1))
				}
			}
		}
	}
	return c
}
