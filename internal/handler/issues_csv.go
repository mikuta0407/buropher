// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// IssuesController#index の format.csv（QueriesHelper#query_to_csv / csv_content / csv_value）。

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/csvexport"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// renderIssuesIndexCSV は send_data(query_to_csv(issues, @query, params[:csv]), ...)。
func (a *App) renderIssuesIndexCSV(c *Req, q *query.Query) {
	ctx := c.Ctx()
	rows, err := q.Issues(ctx, query.ListOptions{Limit: a.Settings.Int("issues_export_limit")})
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	cols, err := q.Columns(ctx)
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	l := a.newIssueLookup(c)
	l.addIssues(rows)
	l.markVisible(rows)
	p := c.Params()
	w := csvexport.New(csvexport.Options{
		Separator:       firstNonEmpty(p.String("field_separator"), c.L("general_csv_separator")),
		Encoding:        p.String("encoding"),
		DefaultEncoding: c.L("general_csv_encoding"),
	})
	var head []string
	for _, col := range cols {
		head = append(head, col.CaptionText(q.Env()))
	}
	w.Strings(head...)
	for _, r := range rows {
		var fields []csvexport.Field
		for _, col := range cols {
			fields = append(fields, l.csvContent(col, r))
		}
		w.Row(fields...)
	}
	if l.err != nil {
		a.internalError(c, "issues csv", l.err)
		return
	}
	name := a.filenameForExport(c, q, "issues")
	c.Halt()
	c.W.Header().Set("Content-Type", "text/csv; header=present")
	c.W.Header().Set("Content-Disposition", contentDisposition(name+".csv"))
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(w.Bytes())
}

// filenameForExport は filename_for_export(query, default_name)。
func (a *App) filenameForExport(c *Req, q *query.Query, def string) string {
	name := strings.TrimSpace(c.Params().String("query_name"))
	if name == "" {
		name = q.Name
	}
	if name == "_" || strings.TrimSpace(name) == "" {
		name = def
	}
	return strings.ToLower(redmine.Titleize(name))
}

// contentDisposition は send_data の Content-Disposition（attachment; filename="..."; filename*=UTF-8”...）。
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > 127 || r == '"' {
			return '?'
		}
		return r
	}, name)
	return `attachment; filename="` + ascii + `"; filename*=UTF-8''` + url.PathEscape(name)
}

// csvContent は csv_content(column, issue)。
func (l *issueLookup) csvContent(c *query.Column, r *query.IssueRow) csvexport.Field {
	s := csvexport.S
	switch c.Kind {
	case query.ColumnCustomField:
		vals, ok := r.CustomValues[c.CustomField.ID]
		if !ok || !l.cfVisibleBy(c.CustomField, l.project(r.ProjectID)) {
			return s("")
		}
		f := l.cfColumnContent(c.CustomField, r, vals, r.ProjectID, l.customized(r), false)
		if str := rails.ToS(f); str == "" && blankValues(vals) && !toSFormats[c.CustomField.FieldFormat] {
			// 値が空のカスタム値は formatted_custom_value が nil を返す（CSV では引用符なしの空）
			return csvexport.Nil()
		}
		return s(l.csvFloat(rails.ToS(f), c.CustomField.FieldFormat == "float"))
	case query.ColumnAssocCF:
		return s(rails.ToS(l.assocCFContent(c, r, false)))
	case query.ColumnAssociation:
		if r.ParentID != nil {
			if p := l.issue(*r.ParentID); p != nil && l.issueVisible(p) {
				return s(p.Subject)
			}
		}
		return s("")
	}
	switch c.Name {
	case "id":
		return s(strconv.FormatInt(r.ID, 10))
	case "subject":
		return s(r.Subject)
	case "project":
		if p := l.project(r.ProjectID); p != nil {
			return s(p.Name)
		}
	case "tracker":
		return s(l.tracker(r.TrackerID).Name)
	case "status":
		return s(l.status(r.StatusID).Name)
	case "priority":
		return s(l.priority(r.PriorityID).Name)
	case "author":
		return s(l.principalName(l.principal(r.AuthorID)))
	case "assigned_to":
		return s(l.principalName(l.principalPtr(r.AssignedToID)))
	case "category":
		if cat := l.category(r.CategoryID); cat != nil {
			return s(cat.Name)
		}
	case "fixed_version":
		if v := l.version(r.FixedVersionID); v != nil {
			return s(v.Name)
		}
	case "parent":
		if r.ParentID != nil {
			return s(strconv.FormatInt(*r.ParentID, 10))
		}
	case "start_date":
		if r.StartDate != nil {
			return s(l.formatDate(*r.StartDate))
		}
	case "due_date":
		if r.DueDate != nil {
			return s(l.formatDate(*r.DueDate))
		}
	case "estimated_hours":
		return s(l.csvHours(r.EstimatedHours))
	case "total_estimated_hours":
		return s(l.csvHours(l.totalEstimatedHours(r)))
	case "estimated_remaining_hours":
		if r.EstimatedHours == nil {
			// (nil || 0) は Integer の 0
			return s("0")
		}
		v := l.estimatedRemainingHours(r)
		return s(l.csvHours(&v))
	case "spent_hours":
		return s(l.csvHours(zeroIfNil(r.SpentHours)))
	case "total_spent_hours":
		return s(l.csvHours(zeroIfNil(r.TotalSpentHours)))
	case "done_ratio":
		return s(strconv.Itoa(l.doneRatio(r)))
	case "created_on":
		return s(l.formatTime(r.CreatedAt))
	case "updated_on":
		return s(l.formatTime(r.UpdatedAt))
	case "closed_on":
		if r.ClosedAt != nil {
			return s(l.formatTime(*r.ClosedAt))
		}
	case "last_updated_by":
		if u := l.principalPtr(r.LastUpdatedByID); u != nil {
			return s(l.principalName(u))
		}
	case "relations":
		var parts []string
		for _, rel := range l.sortedRelations(r.RelationIDs) {
			other := rel.IssueFromID
			if other == r.ID {
				other = rel.IssueToID
			}
			parts = append(parts, l.relationToSPlain(rel, r.ID, "#"+strconv.FormatInt(other, 10)))
		}
		return s(strings.Join(parts, ", "))
	case "attachments":
		var names []string
		for _, a := range l.issueAttachments(r.ID) {
			names = append(names, a.Filename)
		}
		return s(strings.Join(names, "\n"))
	case "watcher_users":
		if !l.c.AllowedTo(domain.Perm("view_issue_watchers"), l.project(r.ProjectID)) {
			return s("")
		}
		var names []string
		for _, id := range r.WatcherIDs {
			if u := l.principal(id); u != nil {
				names = append(names, l.principalName(u))
			}
		}
		return s(strings.Join(names, "\n"))
	case "is_private":
		return s(l.yesNo(r.IsPrivate))
	case "description":
		return s(r.Description)
	case "last_notes":
		if r.LastNotes != nil {
			return s(*r.LastNotes)
		}
	}
	return s("")
}

// toSFormats は formatted_value(html: false) が value.to_s を返す書式（空でも "" になる）。
var toSFormats = map[string]bool{"string": true, "text": true, "link": true, "progressbar": true}

func blankValues(vals []string) bool {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func zeroIfNil(p *float64) *float64 {
	if p == nil {
		var z float64
		return &z
	}
	return p
}

// csvHours は Float を sprintf("%.2f") し小数点を l(:general_csv_decimal_separator) にする（nil は ""）。
func (l *issueLookup) csvHours(v *float64) string {
	if v == nil {
		return ""
	}
	return strings.ReplaceAll(strconv.FormatFloat(*v, 'f', 2, 64), ".", l.L("general_csv_decimal_separator"))
}

// csvFloat は float 書式のカスタムフィールドの値（format_object(Float) の後に小数点を置き換える）。
func (l *issueLookup) csvFloat(s string, isFloat bool) string {
	if !isFloat {
		return s
	}
	return strings.ReplaceAll(s, ".", l.L("general_csv_decimal_separator"))
}

// relationToSPlain は relation.to_s(issue)（テキスト）。
func (l *issueLookup) relationToSPlain(rel *repository.IssueRelation, issueID int64, text string) string {
	parts := []string{l.L(relationLabelFor(rel, issueID))}
	if rel.Delay != nil && *rel.Delay != 0 {
		parts = append(parts, "("+l.L("datetime.distance_in_words.x_days", i18n.Vars{"count": *rel.Delay})+")")
	}
	parts = append(parts, text)
	return strings.Join(parts, " ")
}
