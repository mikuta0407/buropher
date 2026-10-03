// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// IssuesController の format.pdf（Redmine::Export::PDF::IssuesPdfHelper#issue_to_pdf / issues_to_pdf）。

import (
	"math"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/pdf"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// issuesShowPDF は show の format.pdf（filename は "#{@project.identifier}-#{@issue.id}.pdf"）。
func (a *App) issuesShowPDF(c *Req) {
	l := a.newIssueLookup(c)
	v := a.newIssueShowView(c, l)
	m := v.M
	name := m.Project.Identifier + "-" + strconv.FormatInt(m.Row.ID, 10) + ".pdf"
	a.renderPDF(c, name, false, func() (*pdf.Doc, error) {
		d, err := a.issueToPDF(c, l, v)
		if err == nil && l.err != nil {
			err = l.err
		}
		return d, err
	})
}

// pdfText は pdf_format_text(object, attribute)。
func (l *issueLookup) pdfText(text string, obj *redmine.Object) string {
	return string(l.renderer().Textilizable(text, pdfTextOptions(obj)))
}

// pdfHours は l_hours(hours)（nil は 0）。
func (l *issueLookup) pdfHours(v *float64) string {
	var h float64
	if v != nil {
		h = *v
	}
	return l.c.Loc.LHours(h)
}

// issueToPDF は issue_to_pdf(issue, :journals => @journals)。
func (a *App) issueToPDF(c *Req, l *issueLookup, v *issueShowView) (*pdf.Doc, error) {
	ctx := c.Ctx()
	m := v.M
	r := m.Row
	idStr := strconv.FormatInt(r.ID, 10)
	d := a.newPDF(c, "P")
	d.SetTitle(m.Project.Name + " - " + m.Tracker.Name + " #" + idStr)
	d.AddPage()
	d.SetFontStyle("B", 11)
	d.MultiCell(190, 5, m.Project.Name+" - "+m.Tracker.Name+" #"+idStr, "", "", false, 1)
	d.SetFontStyle("", 8)
	baseX := d.X()
	i := 1.0
	for _, anc := range m.Ancestors() {
		if !l.issueVisible(anc) {
			continue
		}
		d.SetX(baseX + i)
		d.MultiCell(190-i, 5, l.tracker(anc.TrackerID).Name+" # "+strconv.FormatInt(anc.ID, 10)+" ("+l.status(anc.StatusID).Name+"): "+anc.Subject, "", "", false, 1)
		if i < 35 {
			i++
		}
	}
	d.SetFontStyle("B", 11)
	d.MultiCell(190-i, 5, r.Subject, "", "", false, 1)
	d.SetFontStyle("", 8)
	d.MultiCell(190, 5, l.formatTime(r.CreatedAt)+" - "+l.principalName(l.principal(r.AuthorID)), "", "", false, 1)
	d.Ln()

	type attrItem struct{ label, value string }
	var left, right []*attrItem
	left = append(left, &attrItem{l.L("field_status"), m.Status.Name})
	left = append(left, &attrItem{l.L("field_priority"), m.Priority.Name})
	if !m.FieldDisabled("assigned_to_id") {
		left = append(left, &attrItem{l.L("field_assigned_to"), l.principalName(l.principalPtr(r.AssignedToID))})
	}
	if !m.FieldDisabled("category_id") {
		s := ""
		if cat := l.category(r.CategoryID); cat != nil {
			s = cat.Name
		}
		left = append(left, &attrItem{l.L("field_category"), s})
	}
	if !m.FieldDisabled("fixed_version_id") {
		s := ""
		if ver := l.version(r.FixedVersionID); ver != nil {
			s = ver.Name
		}
		left = append(left, &attrItem{l.L("field_fixed_version"), s})
	}
	date := func(p *timeLike) string {
		if p == nil {
			return ""
		}
		return l.formatDate(*p)
	}
	if !m.FieldDisabled("start_date") {
		right = append(right, &attrItem{l.L("field_start_date"), date(r.StartDate)})
	}
	if !m.FieldDisabled("due_date") {
		right = append(right, &attrItem{l.L("field_due_date"), date(r.DueDate)})
	}
	if !m.FieldDisabled("done_ratio") {
		right = append(right, &attrItem{l.L("field_done_ratio"), strconv.Itoa(l.doneRatio(r)) + "%"})
	}
	if !m.FieldDisabled("estimated_hours") {
		right = append(right, &attrItem{l.L("field_estimated_hours"), l.pdfHours(r.EstimatedHours)})
	}
	if c.AllowedTo(domain.Perm("view_time_entries"), m.Project) {
		t := m.TotalSpentHours()
		right = append(right, &attrItem{l.L("label_spent_time"), l.pdfHours(&t)})
	}
	rows := max(len(left), len(right))
	for len(left) < rows {
		left = append(left, nil)
	}
	for len(right) < rows {
		right = append(right, nil)
	}
	var half, full []*issueCFValue
	for _, cv := range m.VisibleCustomFieldValues() {
		if cv.CF.FullWidthLayout() {
			full = append(full, cv)
		} else {
			half = append(half, cv)
		}
	}
	h := (len(half) + 1) / 2
	for idx, cv := range half {
		item := &attrItem{cv.CF.Name, rails.ToS(l.formatCustomValue(cv.CF, cv.Value(), m.customized(), false))}
		if idx < h {
			left = append(left, item)
		} else {
			right = append(right, item)
		}
	}
	label := func(it *attrItem) string {
		if it == nil {
			return ""
		}
		return it.label + ":"
	}
	value := func(it *attrItem) string {
		if it == nil {
			return ""
		}
		return it.value
	}
	rows = max(len(left), len(right))
	for idx := range rows {
		var li, ri *attrItem
		if idx < len(left) {
			li = left[idx]
		}
		if idx < len(right) {
			ri = right[idx]
		}
		d.SetFontStyle("B", 9)
		height := max(d.StringHeight(35, label(li)), d.StringHeight(35, label(ri)))
		d.SetFontStyle("", 9)
		height = max(height, d.StringHeight(60, value(li)), d.StringHeight(60, value(ri)))
		first, last := "L", "R"
		if idx == 0 {
			first, last = "LT", "RT"
		}
		d.SetFontStyle("B", 9)
		d.MultiCell(35, height, label(li), first, "", false, 0)
		d.SetFontStyle("", 9)
		d.MultiCell(60, height, value(li), last, "", false, 0)
		d.SetFontStyle("B", 9)
		d.MultiCell(35, height, label(ri), first, "", false, 0)
		d.SetFontStyle("", 9)
		d.MultiCell(60, height, value(ri), last, "", false, 2)
		d.SetX(baseX)
	}

	atts, err := repository.ContainerAttachmentList(ctx, a.DB, domain.AttachmentContainerIssue, r.ID)
	if err != nil {
		return nil, err
	}
	images := a.pdfImageLoader(atts)
	issueObj := &redmine.Object{Kind: "issue", ID: r.ID, Project: m.Project}

	d.SetFontStyle("B", 9)
	d.Cell(35+155, 5, l.L("field_description"), "LRT", 1, "", false)
	d.SetFontStyle("", 9)
	d.SetImageScale(1.6)
	d.WriteHTMLCell(35+155, 5, l.pdfText(r.Description, issueObj), "LRB", images)

	for _, cv := range full {
		isHTML := cv.CF.FullTextFormatting()
		text := rails.ToS(l.formatCustomValue(cv.CF, cv.Value(), m.customized(), isHTML))
		if strings.TrimSpace(text) == "" {
			continue
		}
		d.SetFontStyle("B", 9)
		d.Cell(35+155, 5, cv.CF.Name, "LRT", 1, "", false)
		d.SetFontStyle("", 9)
		if !isHTML {
			text = l.pdfText(text, issueObj)
		}
		d.WriteHTMLCell(35+155, 5, text, "LRB", images)
	}

	if !m.Leaf() {
		truncate := 90
		if pdf.IsCJK(c.Loc.Lang) {
			truncate = 65
		}
		d.SetFontStyle("B", 9)
		d.Cell(35+155, 5, l.L("label_subtask_plural")+":", "LTR", 0, "", false)
		d.Ln()
		var ancestors []*query.IssueRow
		for _, child := range m.VisibleDescendants() {
			for len(ancestors) > 0 && !isDescendantOf(child, ancestors[len(ancestors)-1]) {
				ancestors = ancestors[:len(ancestors)-1]
			}
			level := len(ancestors)
			if l.hasChildren(child.ID) {
				ancestors = append(ancestors, child)
			}
			buf := pdf.Truncate(l.tracker(child.TrackerID).Name+" # "+strconv.FormatInt(child.ID, 10)+": "+child.Subject, truncate)
			level = min(level, 10)
			d.SetFontStyle("", 8)
			d.Cell(35+135, 5, strings.Repeat("  ", level)+buf, "L", 0, "", false)
			d.SetFontStyle("B", 8)
			d.Cell(20, 5, l.status(child.StatusID).Name, "R", 0, "", false)
			d.Ln()
		}
	}

	if len(v.Relations) > 0 {
		truncate := 80
		if pdf.IsCJK(c.Loc.Lang) {
			truncate = 60
		}
		d.SetFontStyle("B", 9)
		d.Cell(35+155, 5, l.L("label_related_issues")+":", "LTR", 0, "", false)
		d.Ln()
		cross := a.Settings.Bool("cross_project_issue_relations")
		for _, rel := range v.Relations {
			other := l.issue(otherIssueID(rel, r.ID))
			if other == nil {
				continue
			}
			text := ""
			if cross {
				if p := l.project(other.ProjectID); p != nil {
					text += p.Name + " - "
				}
			}
			text += l.tracker(other.TrackerID).Name + " #" + strconv.FormatInt(other.ID, 10) + ": " + other.Subject
			buf := pdf.Truncate(l.relationToSPlain(rel, r.ID, text), truncate)
			d.SetFontStyle("", 8)
			d.Cell(35+155-60, 5, buf, "L", 0, "", false)
			d.SetFontStyle("B", 8)
			d.Cell(20, 5, l.status(other.StatusID).Name, "", 0, "", false)
			d.Cell(20, 5, date(other.StartDate), "", 0, "", false)
			d.Cell(20, 5, date(other.DueDate), "R", 0, "", false)
			d.Ln()
		}
	}
	d.Cell(190, 5, "", "T", 0, "", false)
	d.Ln()

	// issue.changesets（可視性で絞らない）
	if c.AllowedTo(domain.Perm("view_changesets"), m.Project) {
		cs, err := repository.IssueChangesets(ctx, a.DB, r.ID, "")
		if err != nil {
			return nil, err
		}
		if len(cs) > 0 {
			d.SetFontStyle("B", 9)
			d.Cell(190, 5, l.L("label_associated_revisions"), "B", 0, "", false)
			d.Ln()
			for _, ch := range cs {
				item := &changesetItem{ReadChangeset: ch, l: l, m: m}
				d.SetFontStyle("B", 8)
				author := item.Author()
				authorS := ""
				if u, ok := author.(*domain.User); ok {
					authorS = l.principalName(u)
				} else {
					authorS = rails.ToS(author)
				}
				d.Cell(190, 5, l.L("label_revision")+" "+item.FormatIdentifier()+" - "+l.formatTime(ch.CommittedAt.Time)+" - "+authorS, "", 0, "", false)
				d.Ln()
				if comments := strPtrValue(ch.Comments); strings.TrimSpace(comments) != "" {
					d.SetFontStyle("", 8)
					d.WriteHTMLCell(190, 5, l.pdfText(comments, issueObj), "", images)
				}
				d.Ln()
			}
		}
	}

	if len(v.Journals) > 0 {
		d.SetFontStyle("B", 9)
		d.Cell(190, 5, l.L("label_history"), "B", 0, "", false)
		d.Ln()
		for _, j := range v.Journals {
			d.SetFontStyle("B", 8)
			title := "#" + strconv.Itoa(j.Indice) + " - " + l.formatTime(j.CreatedAt) + " - " + l.principalName(j.User)
			if j.PrivateNotes {
				title += " (" + l.L("field_private_notes") + ")"
			}
			d.Cell(190, 5, title, "", 0, "", false)
			d.Ln()
			d.SetFontStyle("I", 8)
			for _, s := range l.detailsToStrings(j.VisibleDetails(), m, true, false) {
				d.MultiCell(190, 5, "- "+string(s), "", "", false, 1)
			}
			if j.HasNotes() {
				if len(j.Details) > 0 {
					d.Ln()
				}
				d.SetFontStyle("", 8)
				obj := &redmine.Object{Kind: "journal", ID: j.ID, Project: m.Project, JournalizedID: j.IssueID}
				d.WriteHTMLCell(190, 5, l.pdfText(j.NotesString(), obj), "", images)
			}
			d.Ln()
		}
	}

	if len(atts) > 0 {
		d.SetFontStyle("B", 9)
		d.Cell(190, 5, l.L("label_attachment_plural"), "B", 0, "", false)
		d.Ln()
		for _, att := range atts {
			pdfAttachmentRow(d, att.Filename, c.Loc.NumberToHumanSize(att.Filesize), l.formatDate(att.CreatedOn), l.principalName(l.principal(att.AuthorID)))
		}
	}
	return d, nil
}

// ---------------------------------------------------------------- issues_to_pdf

// renderIssuesIndexPDF は index の format.pdf（@query.issues(:limit => Setting.issues_export_limit)）。
func (a *App) renderIssuesIndexPDF(c *Req, q *query.Query) {
	rows, err := q.Issues(c.Ctx(), query.ListOptions{Limit: a.Settings.Int("issues_export_limit")})
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	name := a.filenameForExport(c, q, "issues") + ".pdf"
	a.renderPDF(c, name, false, func() (*pdf.Doc, error) { return a.issuesToPDF(c, q, rows) })
}

// pdfIssueRow は issue_list で並べた 1 行。
type pdfIssueRow struct {
	r     *query.IssueRow
	level int
}

// issuesToPDF は issues_to_pdf(issues, project, query)。
func (a *App) issuesToPDF(c *Req, q *query.Query, rows []*query.IssueRow) (*pdf.Doc, error) {
	ctx := c.Ctx()
	l := a.newIssueLookup(c)
	l.addIssues(rows)
	l.markVisible(rows)
	ids := make([]int64, len(rows))
	var pids []int64
	for i, r := range rows {
		ids[i] = r.ID
		pids = append(pids, r.AuthorID)
		if r.AssignedToID != nil {
			pids = append(pids, *r.AssignedToID)
		}
		if r.LastUpdatedByID != nil {
			pids = append(pids, *r.LastUpdatedByID)
		}
		pids = append(pids, r.WatcherIDs...)
	}
	l.preloadChildren(ids)
	l.preloadPrincipals(pids)
	inline, err := q.InlineColumns(ctx)
	if err != nil {
		return nil, err
	}
	block, err := q.BlockColumns(ctx)
	if err != nil {
		return nil, err
	}
	env := q.Env()

	d := a.newPDF(c, "L")
	title := l.L("label_issue_plural")
	if q.ID != 0 {
		title = q.Name
	}
	if c.Project != nil {
		title = c.Project.Name + " - " + title
	}
	d.SetTitle(title)
	d.SetAutoPageBreak(false)
	d.AddPage("L")

	pageHeight := d.PageHeight()
	pageWidth := d.PageWidth()
	leftMargin, rightMargin := d.LeftMargin(), d.RightMargin()
	bottomMargin := d.FooterMargin()
	const rowHeight = 4.0

	// issue_list（階層の深さ）
	var list []pdfIssueRow
	var ancestors []*query.IssueRow
	for _, r := range rows {
		for len(ancestors) > 0 && !isDescendantOf(r, ancestors[len(ancestors)-1]) {
			ancestors = ancestors[:len(ancestors)-1]
		}
		list = append(list, pdfIssueRow{r, len(ancestors)})
		if l.hasChildren(r.ID) {
			ancestors = append(ancestors, r)
		}
	}
	captions := make([]string, len(inline))
	for i, col := range inline {
		captions[i] = col.CaptionText(env)
	}
	values := make([][]string, len(list))
	for i, it := range list {
		values[i] = l.pdfRowValues(inline, it.r, it.level)
	}

	tableWidth := pageWidth - rightMargin - leftMargin
	var colWidth []float64
	if len(inline) > 0 {
		colWidth = calcPDFColWidth(d, captions, values, tableWidth)
		tableWidth = 0
		for _, w := range colWidth {
			tableWidth += w
		}
	}
	if tableWidth > 0 && len(block) > 0 {
		full := pageWidth - rightMargin - leftMargin
		var sum float64
		for i := range colWidth {
			colWidth[i] = colWidth[i] * full / tableWidth
			sum += colWidth[i]
		}
		tableWidth = sum
	}

	d.SetFontStyle("B", 11)
	d.Cell(190, 8, title, "", 0, "", false)
	d.Ln()

	totals, err := q.Totals(ctx)
	if err != nil {
		return nil, err
	}
	if len(totals) > 0 {
		var parts []string
		for _, t := range totals {
			parts = append(parts, t.Column.CaptionText(env)+": "+pdfTotalToS(t.Column, t.Value))
		}
		d.SetFontStyle("B", 10)
		d.Cell(tableWidth, 6, strings.Join(parts, "  "), "", 1, "R", false)
	}

	gcol, err := q.GroupByColumn(ctx)
	if err != nil {
		return nil, err
	}
	var counts map[query.GroupKey]int64
	var groupTotals []query.GroupTotal
	if gcol != nil {
		if counts, err = q.ResultCountByGroup(ctx); err != nil {
			return nil, err
		}
		if groupTotals, err = q.TotalsByGroup(ctx); err != nil {
			return nil, err
		}
	}

	renderHeader := func() {
		d.SetFontStyle("B", 8)
		d.SetFillColor(230, 230, 230)
		baseX, baseY := d.X(), d.Y()
		maxH := pdfCellsHeight(d, captions, colWidth)
		pdfWriteCells(d, captions, colWidth, maxH)
		d.SetXY(baseX, baseY+maxH)
		d.SetFontStyle("", 8)
		d.SetFillColor(255, 255, 255)
	}
	renderHeader()

	var prev query.GroupKey
	first := true
	for i, it := range list {
		r := it.r
		if gcol != nil {
			key, val := l.groupValue(gcol, r)
			if first || key != prev {
				d.SetFontStyle("B", 10)
				label := "None"
				if !(key.Null || rails.IsBlank(val) && val != false) {
					label = rails.ToS(l.formatObject(val, false, gcol.CustomField))
				}
				if n, ok := counts[key]; ok {
					label += " (" + strconv.FormatInt(n, 10) + ")"
				} else {
					label += " ()"
				}
				d.Bookmark(label, 0)
				d.Cell(tableWidth, rowHeight*2, label, "LR", 1, "L", false)
				d.SetFontStyle("", 8)
				var parts []string
				for _, t := range groupTotals {
					parts = append(parts, t.Column.CaptionText(env)+": "+pdfTotalToS(t.Column, t.ByGroup[key]))
				}
				if s := strings.Join(parts, "  "); s != "" {
					d.Cell(tableWidth, rowHeight, s, "LR", 1, "L", false)
				}
				prev, first = key, false
			}
		}
		vals := values[i]
		baseY := d.Y()
		maxH := pdfCellsHeight(d, vals, colWidth)
		if maxH > pageHeight-baseY-bottomMargin {
			d.AddPage("L")
			renderHeader()
			baseY = d.Y()
		}
		pdfWriteCells(d, vals, colWidth, maxH)
		d.SetY(baseY + maxH)

		for _, col := range block {
			isHTML := false
			text := ""
			if col.Kind == query.ColumnCustomField {
				if vs, ok := r.CustomValues[col.CustomField.ID]; ok && l.cfVisibleBy(col.CustomField, l.project(r.ProjectID)) {
					isHTML = col.CustomField.FullTextFormatting()
					text = rails.ToS(l.cfColumnContent(col.CustomField, r, vs, r.ProjectID, l.customized(r), isHTML))
				}
			} else {
				src := r.Description
				if col.Name == "last_notes" {
					src = strPtrValue(r.LastNotes)
				}
				obj := &redmine.Object{Kind: "issue", ID: r.ID, Project: l.project(r.ProjectID)}
				if strings.TrimSpace(src) != "" {
					text = l.pdfText(src, obj)
				}
				isHTML = true
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			d.SetX(10)
			d.SetAutoPageBreak(true, bottomMargin)
			d.SetFontStyle("B", 9)
			d.Cell(0, 5, col.CaptionText(env), "LRT", 1, "", false)
			d.SetFontStyle("", 9)
			var images pdf.ImageLoader
			if isHTML {
				atts, err := repository.ContainerAttachmentList(ctx, a.DB, domain.AttachmentContainerIssue, r.ID)
				if err != nil {
					return nil, err
				}
				images = a.pdfImageLoader(atts)
			} else {
				text = l.pdfText(text, nil)
			}
			d.WriteHTMLCell(0, 5, text, "LRB", images)
			d.SetAutoPageBreak(false)
		}
	}
	if len(rows) == a.Settings.Int("issues_export_limit") {
		d.SetFontStyle("B", 10)
		d.Cell(0, rowHeight, "...", "", 0, "", false)
	}
	return d, l.err
}

// pdfTotalToS は "#{total}"（Float#to_s / Integer#to_s）。
func pdfTotalToS(c *query.Column, v float64) string {
	if c.CustomField != nil && c.CustomField.FieldFormat == "int" {
		return strconv.FormatInt(int64(v), 10)
	}
	return issues.RubyFloatToS(v)
}

// pdfCellsHeight は get_issues_to_pdf_write_cells（MultiCell の高さの最大）。
func pdfCellsHeight(d *pdf.Doc, vals []string, widths []float64) float64 {
	var h float64
	for i, v := range vals {
		h = math.Max(h, d.StringHeight(widths[i], v))
	}
	return h
}

// pdfWriteCells は issues_to_pdf_write_cells。
func pdfWriteCells(d *pdf.Doc, vals []string, widths []float64, h float64) {
	for i, v := range vals {
		d.MultiCell(widths[i], h, strings.TrimSpace(v), "1", "", true, 0)
	}
}

// pdfRowValues は fetch_row_values(issue, query, level)。
func (l *issueLookup) pdfRowValues(cols []*query.Column, r *query.IssueRow, level int) []string {
	out := make([]string, len(cols))
	float2 := func(v *float64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatFloat(*v, 'f', 2, 64)
	}
	for i, col := range cols {
		var s string
		switch {
		case col.Kind == query.ColumnCustomField:
			if vals, ok := r.CustomValues[col.CustomField.ID]; ok && l.cfVisibleBy(col.CustomField, l.project(r.ProjectID)) {
				s = rails.ToS(l.cfColumnContent(col.CustomField, r, vals, r.ProjectID, l.customized(r), false))
			}
		case col.Kind == query.ColumnAssocCF || col.Kind == query.ColumnAssociation:
			s = l.csvContent(col, r).Value
		default:
			switch col.Name {
			case "subject":
				s = strings.Repeat("  ", level) + r.Subject
			case "parent":
				if r.ParentID != nil {
					if p := l.issue(*r.ParentID); p != nil {
						s = l.tracker(p.TrackerID).Name + " #" + strconv.FormatInt(p.ID, 10) + ": " + p.Subject
					}
				}
			case "estimated_hours":
				s = float2(r.EstimatedHours)
			case "total_estimated_hours":
				s = float2(l.totalEstimatedHours(r))
			case "estimated_remaining_hours":
				if r.EstimatedHours == nil {
					s = "0"
				} else {
					v := l.estimatedRemainingHours(r)
					s = float2(&v)
				}
			case "spent_hours":
				s = float2(zeroIfNil(r.SpentHours))
			case "total_spent_hours":
				s = float2(zeroIfNil(r.TotalSpentHours))
			case "is_private":
				s = strconv.FormatBool(r.IsPrivate)
			default:
				s = l.csvContent(col, r).Value
			}
		}
		out[i] = s
	}
	return out
}

// calcPDFColWidth は calc_col_width(issues, query, table_width, pdf)。
func calcPDFColWidth(d *pdf.Doc, captions []string, values [][]string, tableWidth float64) []float64 {
	n := len(captions)
	d.SetFontStyle("B", 8)
	pad := d.CellMargin()
	colMin := make([]float64, n)
	for i, c := range captions {
		colMin[i] = d.StringWidth(c) + pad
	}
	colMax := append([]float64(nil), colMin...)
	colAvg := append([]float64(nil), colMin...)
	cmin := d.StringWidth("OO") + pad*2
	if tableWidth > cmin*float64(n) {
		tableWidth -= cmin * float64(n)
	} else {
		cmin = d.StringWidth("O") + pad*2
		if tableWidth > cmin*float64(n) {
			tableWidth -= cmin * float64(n)
		} else {
			var sum float64
			for _, w := range colAvg {
				sum += w
			}
			ratio := tableWidth / sum
			out := make([]float64, n)
			for i, w := range colAvg {
				out[i] = w * ratio
			}
			return out
		}
	}
	wordMax := make([]float64, n)
	for i, c := range captions {
		m := 10.0
		for _, w := range strings.Fields(c) {
			if x := d.StringWidth(w) + pad; m < x {
				m = x
			}
		}
		wordMax[i] = m
	}
	d.SetFontStyle("", 8)
	k := 1.0
	for _, vals := range values {
		k++
		for i, v := range vals {
			w := d.StringWidth(v) + pad*2
			colMax[i] = math.Max(colMax[i], w)
			colMin[i] = math.Min(colMin[i], w)
			colAvg[i] += w
			for _, word := range strings.Fields(v) {
				if x := d.StringWidth(word) + pad; wordMax[i] < x {
					wordMax[i] = x
				}
			}
		}
	}
	sum := func(xs []float64) float64 {
		var s float64
		for _, x := range xs {
			s += x
		}
		return s
	}
	for i := range colAvg {
		colAvg[i] /= k
	}
	ratio := tableWidth / sum(colAvg)
	colWidth := make([]float64, n)
	for i, w := range colAvg {
		colWidth[i] = w * ratio
	}
	if ratio = tableWidth / sum(wordMax); ratio < 1 {
		for i := range wordMax {
			wordMax[i] *= ratio
		}
	}
	done := true
	fix := make([]bool, n)
	for i, w := range colWidth {
		switch {
		case w > colMax[i]:
			colWidth[i], fix[i], done = colMax[i], true, false
		case w < wordMax[i]:
			colWidth[i], fix[i], done = wordMax[i], true, false
		}
	}
	for iter := 0; !done && iter < 100; iter++ {
		done = true
		ratio = tableWidth / sum(colWidth)
		for i, w := range colWidth {
			if fix[i] {
				continue
			}
			colWidth[i] = w * ratio
			switch {
			case colWidth[i] < wordMax[i]:
				colWidth[i], fix[i], done = wordMax[i], true, false
			case colWidth[i] > colMax[i]:
				colWidth[i], fix[i], done = colMax[i], true, false
			}
		}
	}
	ratio = tableWidth / sum(colWidth)
	for i := range colWidth {
		colWidth[i] = colWidth[i]*ratio + cmin
	}
	return colWidth
}
