// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// wiki#show / wiki#export の format.pdf（Redmine::Export::PDF::WikiPdfHelper）。

import (
	"strconv"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/pdf"
	"github.com/mikuta0407/buropher/internal/repository"
)

// wikiShowPDF は show の format.pdf（wiki_page_to_pdf(@page, @project)。内容は常に最新版）。
func (a *App) wikiShowPDF(c *Req, page *domain.WikiPage) {
	a.renderPDF(c, page.Title+".pdf", true, func() (*pdf.Doc, error) {
		d := a.newPDF(c, "P")
		version := 0
		if page.CurrentVersion > 0 {
			version = page.CurrentVersion
		}
		d.SetTitle(c.Project.Name + " - " + page.Title)
		d.AddPage()
		d.SetFontStyle("B", 11)
		d.MultiCell(190, 5, c.Project.Name+" - "+page.Title+" - # "+strconv.Itoa(version), "", "", false, 1)
		d.Ln()
		d.SetImageScale(1.6)
		d.SetFontStyle("", 9)
		if err := a.writeWikiPagePDF(c, d, page); err != nil {
			return nil, err
		}
		return d, nil
	})
}

// wikiExportPDF は export の format.pdf（wiki_pages_to_pdf(@pages, @project)、filename は "#{@project.identifier}.pdf"）。
func (a *App) wikiExportPDF(c *Req, pages []*domain.WikiPage) {
	a.renderPDF(c, c.Project.Identifier+".pdf", false, func() (*pdf.Doc, error) {
		d := a.newPDF(c, "P")
		d.SetTitle(c.Project.Name)
		d.AddPage()
		d.SetFontStyle("B", 11)
		d.MultiCell(190, 5, c.Project.Name, "", "", false, 1)
		d.Ln()
		d.SetImageScale(1.6)
		d.SetFontStyle("", 9)
		byParent := map[int64][]*domain.WikiPage{}
		for _, p := range pages {
			var k int64
			if p.ParentID != nil {
				k = *p.ParentID
			}
			byParent[k] = append(byParent[k], p)
		}
		// write_page_hierarchy
		var walk func(node int64, level int) error
		walk = func(node int64, level int) error {
			for i, p := range byParent[node] {
				if !(level == 0 && i == 0) {
					d.AddPage()
				}
				d.Bookmark(p.Title, level)
				if err := a.writeWikiPagePDF(c, d, p); err != nil {
					return err
				}
				if len(byParent[p.ID]) > 0 {
					if err := walk(p.ID, level+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := walk(0, 0); err != nil {
			return nil, err
		}
		return d, nil
	})
}

// writeWikiPagePDF は write_wiki_page(pdf, page)。
func (a *App) writeWikiPagePDF(c *Req, d *pdf.Doc, page *domain.WikiPage) error {
	ctx := c.Ctx()
	var text string
	if page.CurrentVersion > 0 {
		v, err := repository.WikiPageVersion(ctx, a.DB, page.ID, page.CurrentVersion)
		if err != nil && !errorsIsNotFound(err) {
			return err
		}
		if v != nil {
			obj, err := a.wikiTextObject(c, page, v, true)
			if err != nil {
				return err
			}
			text = string(a.Helpers.WikiRenderer(c.Page()).Textilizable(v.Text, pdfTextOptions(obj)))
		}
	}
	atts, err := repository.ContainerAttachmentList(ctx, a.DB, domain.AttachmentContainerWikiPage, page.ID)
	if err != nil {
		return err
	}
	d.WriteHTMLCell(190, 5, text, "", a.pdfImageLoader(atts))
	if len(atts) > 0 {
		d.Ln(5)
		d.SetFontStyle("B", 9)
		d.Cell(190, 5, c.L("label_attachment_plural"), "B", 0, "", false)
		d.Ln()
		for _, att := range atts {
			author := ""
			if att.Author != nil {
				author = att.Author.Name(a.Settings.String("user_format"))
			}
			pdfAttachmentRow(d, att.Filename, c.Loc.NumberToHumanSize(att.Filesize), c.Loc.FormatDate(att.CreatedOn), author)
		}
	}
	return nil
}
