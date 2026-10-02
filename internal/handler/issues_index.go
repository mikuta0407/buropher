package handler

// IssuesController#index（html / atom / csv / pdf / api）。

import (
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

func issueVisOpts() authz.ConditionOptions { return authz.ConditionOptions{} }

// issuesPath は _project_issues_path(@project)。
func issuesPath(p *domain.Project) string {
	if p != nil {
		return "/projects/" + p.Identifier + "/issues"
	}
	return "/issues"
}

// IssuesIndex は IssuesController#index。
func (a *App) IssuesIndex(c *Req) {
	format := formatOf(c)
	switch format {
	case "html", "atom", "csv", "pdf", "json", "xml":
	default:
		renderUnknownFormat(c)
		return
	}
	useSession := format != "csv"
	q, ok := a.retrieveIssueQuery(c, useSession)
	if !ok {
		return
	}
	valid, err := q.Valid(c.Ctx())
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	if !valid {
		switch format {
		case "html":
			a.renderIssuesIndexHTML(c, q, false)
		case "atom", "csv", "pdf":
			httpx.Head(c.W, c.R, http.StatusUnprocessableEntity)
			c.Halt()
		default:
			msgs, _ := q.Errors(c.Ctx())
			c.RenderAPIErrors(msgs...)
		}
		return
	}
	switch format {
	case "html":
		a.renderIssuesIndexHTML(c, q, true)
	case "json", "xml":
		a.renderIssuesIndexAPI(c, q)
	case "atom":
		a.renderIssuesIndexAtom(c, q)
	case "csv":
		a.renderIssuesIndexCSV(c, q)
	case "pdf":
		// TODO(pdf): PDF 出力は未対応
		renderUnknownFormat(c)
	}
}

// renderUnknownFormat は ActionController::UnknownFormat（respond_to に無い形式。406、本文なし）。
func renderUnknownFormat(c *Req) {
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.W.WriteHeader(http.StatusNotAcceptable)
	c.Halt()
}

// renderIssuesIndexHTML は index.html（render :layout => !request.xhr?）。
func (a *App) renderIssuesIndexHTML(c *Req, q *query.Query, valid bool) {
	ctx := c.Ctx()
	qv, err := a.newQueryView(c, q, "IssueQuery", issuesPath(c.Project))
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	l := a.newIssueLookup(c)
	data := map[string]any{"qv": qv, "Valid": valid}
	if valid {
		count, err := q.IssueCount(ctx)
		if err != nil {
			a.queryFailed(c, err)
			return
		}
		pages := pagination.New(int(count), c.PerPageOption(), c.Params().String("page"))
		rows, err := q.Issues(ctx, query.ListOptions{Offset: pages.Offset(), Limit: pages.PerPage})
		if err != nil {
			a.queryFailed(c, err)
			return
		}
		lv, err := a.newIssueListView(l, qv, rows)
		if err != nil {
			a.queryFailed(c, err)
			return
		}
		totals, err := qv.issueTotalsHTML()
		if err != nil {
			a.queryFailed(c, err)
			return
		}
		data["List"] = lv
		data["Totals"] = totals
		data["Empty"] = len(rows) == 0
		data["Pages"] = pages
		data["Count"] = int(count)
		data["ExportLimit"] = a.Settings.Int("issues_export_limit")
		data["OverExportLimit"] = int(count) > a.Settings.Int("issues_export_limit")
		hidden, err := qv.AsHiddenFieldTags()
		if err != nil {
			a.queryFailed(c, err)
			return
		}
		data["CSVHidden"] = hidden
		blocks, err := q.AvailableBlockColumns(ctx)
		if err != nil {
			a.queryFailed(c, err)
			return
		}
		var bcs []map[string]any
		for _, b := range blocks {
			has, _ := q.HasColumn(ctx, b.Name)
			bcs = append(bcs, map[string]any{"Name": b.Name, "Caption": b.CaptionText(q.Env()), "Checked": has})
		}
		data["CSVBlockColumns"] = bcs
	}
	// 右上のリンク
	canAdd := c.allowedToGloballyOrProject("add_issues", c.Project)
	if canAdd && c.Project != nil {
		ids, err := c.Authz().AllowedTargetTrackerIDs(ctx, c.Project, 0)
		if err != nil {
			a.internalError(c, "allowed target trackers", err)
			return
		}
		canAdd = len(ids) > 0
	}
	data["CanAddIssue"] = canAdd
	data["CanImport"] = c.allowedToGloballyOrProject("import_issues", c.Project) && c.allowedToGloballyOrProject("add_issues", c.Project)
	data["CanEditProject"] = c.Project != nil && c.AllowedTo(domain.Perm("edit_project"), c.Project) &&
		c.AllowedTo(domain.ControllerAction("projects", "settings"), c.Project)
	data["NewIssuePath"] = newIssuePath(c.Project)
	data["ImportPath"] = "/issues/imports/new"
	if c.Project != nil {
		data["ImportPath"] = "/issues/imports/new?project_id=" + escapeQuery(c.Project.Identifier)
	}
	data["AtomKey"] = c.AtomKey()
	data["IssuesPath"] = issuesPath(c.Project)
	data["Title"] = c.L("label_issue_plural")
	if q.ID != 0 {
		data["Title"] = q.Name
	}
	sidebar, err := a.sidebarQueriesHTML(c, query.KindIssue, q, issuesPath(c.Project))
	if err != nil {
		a.internalError(c, "sidebar queries", err)
		return
	}
	data["SidebarQueries"] = sidebar
	data["AtomIssuesURL"], data["AtomJournalsURL"] = a.issuesAtomLinks(c, q)
	opts := RenderOptions{}
	if c.R.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		opts.Layout = view.NoLayout
	}
	c.Render("issues/index", data, opts)
}

// newIssuePath は _new_project_issue_path(@project)。
func escapeQuery(s string) string { return url.QueryEscape(s) }

func newIssuePath(p *domain.Project) string {
	if p != nil {
		return "/projects/" + p.Identifier + "/issues/new"
	}
	return "/issues/new"
}

// issuesAtomLinks は header_tags の auto_discovery_link_tag の URL（query_id, format atom, page nil, key）。
func (a *App) issuesAtomLinks(c *Req, q *query.Query) (string, string) {
	base := httpx.RequestBaseURL(c.R)
	qs := func(path string) string {
		h := rails.NewHash()
		// url_for は現在のパラメータ（project_id 等のパス部分）を引き継ぐ
		if q.ID != 0 {
			h.Set("query_id", q.ID)
		}
		if k := c.AtomKey(); k != "" {
			h.Set("key", k)
		}
		s := helper.ToQuery(h)
		if s != "" {
			path += "?" + s
		}
		return base + path
	}
	issues := qs(issuesPath(c.Project) + ".atom")
	// journals#index のルート（/issues/changes）は project_id を持たないので引き継がれない
	return issues, qs("/issues/changes.atom")
}

// ---------------------------------------------------------------- totals

// issueTotalsHTML は render_query_totals(query)。
func (qv *queryView) issueTotalsHTML() (template.HTML, error) {
	cols, err := qv.Q.TotalableColumns(qv.ctx)
	if err != nil || len(cols) == 0 {
		return "", err
	}
	var parts []string
	for _, c := range cols {
		v, err := qv.Q.TotalFor(qv.ctx, c.Name)
		if err != nil {
			return "", err
		}
		parts = append(parts, string(qv.issueTotalTag(c, v)))
	}
	return rails.ContentTag("p", template.HTML(strings.Join(parts, " ")), rails.NewHash("class", "query-totals")), nil
}

// issueTotalTag は total_tag(column, value)。
func (qv *queryView) issueTotalTag(c *query.Column, v float64) template.HTML {
	label := rails.ContentTag("span", qv.Caption(c)+":", nil)
	var value string
	switch {
	case c.Name == "hours" || c.Name == "spent_hours" || c.Name == "total_spent_hours" || c.Name == "estimated_hours" ||
		c.Name == "total_estimated_hours" || c.Name == "estimated_remaining_hours":
		value = qv.c.Loc.FormatHours(v)
	case c.CustomField != nil:
		s := ""
		if c.CustomField.FieldFormat == "int" {
			s = strconv.FormatInt(int64(v), 10)
		} else {
			s = strconv.FormatFloat(v, 'f', 2, 64)
		}
		if c.CustomField.ThousandsDelimiter() {
			s = qv.c.Loc.NumberWithDelimiter(s, nil)
		}
		value = s
	default:
		value = formatNumber(v)
	}
	val := rails.ContentTag("span", value, rails.NewHash("class", "value"))
	return rails.ContentTag("span", label+" "+val, rails.NewHash("class", "total-for-"+strings.ReplaceAll(c.Name, "_", "-")))
}
