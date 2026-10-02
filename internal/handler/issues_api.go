package handler

// issues/index.api.rsb と issues/show.api.rsb（REST API の JSON / XML）。

import (
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

// renderIssuesIndexAPI は IssuesController#index の format.api。
func (a *App) renderIssuesIndexAPI(c *Req, q *query.Query) {
	ctx := c.Ctx()
	offset, limit := c.APIOffsetAndLimit()
	if err := q.SetColumnNames(ctx, []string{"author"}); err != nil {
		a.queryFailed(c, err)
		return
	}
	count, err := q.IssueCount(ctx)
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	rows, err := q.Issues(ctx, query.ListOptions{Offset: offset, Limit: limit})
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	l := a.newIssueLookup(c)
	l.addIssues(rows)
	l.markVisible(rows)
	if c.AllowedToGlobally(domain.Perm("view_time_entries")) {
		l.loadVisibleSpentHours(rows)
	}
	incAttachments := c.IncludeInAPIResponse("attachments")
	incRelations := c.IncludeInAPIResponse("relations")
	var models []*issueModel
	for _, r := range rows {
		models = append(models, l.model(r))
	}
	if l.err != nil {
		a.internalError(c, "issues api", l.err)
		return
	}
	c.RenderAPI(0, func(b apibuilder.Builder) {
		b.Array("issues", c.APIMeta(apibuilder.A("total_count", count, "offset", offset, "limit", limit)), func() {
			for _, m := range models {
				b.Object("issue", func() {
					l.renderAPIIssueCore(b, m)
					if incAttachments {
						b.Array("attachments", nil, func() {
							for _, at := range l.issueAttachments(m.Row.ID) {
								l.renderAPIAttachment(b, at)
							}
						})
					}
					if incRelations {
						b.Array("relations", nil, func() {
							for _, rel := range l.visibleRelations(m) {
								renderAPIRelation(b, rel)
							}
						})
					}
				})
			}
		})
	})
}

// renderAPIIssueCore は index / show 共通の属性（id 〜 closed_on）。
func (l *issueLookup) renderAPIIssueCore(b apibuilder.Builder, m *issueModel) {
	r := m.Row
	b.Value("id", r.ID)
	if m.Project != nil {
		b.Attrs("project", apibuilder.A("id", r.ProjectID, "name", m.Project.Name))
	}
	b.Attrs("tracker", apibuilder.A("id", r.TrackerID, "name", m.Tracker.Name))
	b.Attrs("status", apibuilder.A("id", r.StatusID, "name", m.Status.Name, "is_closed", m.Status.IsClosed))
	b.Attrs("priority", apibuilder.A("id", r.PriorityID, "name", m.Priority.Name))
	if u := l.principal(r.AuthorID); u != nil {
		b.Attrs("author", apibuilder.A("id", r.AuthorID, "name", l.principalName(u)))
	}
	if u := l.principalPtr(r.AssignedToID); u != nil {
		b.Attrs("assigned_to", apibuilder.A("id", u.ID, "name", l.principalName(u)))
	}
	if cat := l.category(r.CategoryID); cat != nil {
		b.Attrs("category", apibuilder.A("id", cat.ID, "name", cat.Name))
	}
	if v := l.version(r.FixedVersionID); v != nil {
		b.Attrs("fixed_version", apibuilder.A("id", v.ID, "name", v.Name))
	}
	if r.ParentID != nil && l.issue(*r.ParentID) != nil {
		b.Attrs("parent", apibuilder.A("id", *r.ParentID))
	}
	b.Value("subject", r.Subject)
	if m.I != nil && m.I.Description == nil {
		b.Value("description", nil)
	} else {
		b.Value("description", r.Description)
	}
	b.Value("start_date", apiDate(r.StartDate))
	b.Value("due_date", apiDate(r.DueDate))
	b.Value("done_ratio", l.doneRatio(r))
	b.Value("is_private", r.IsPrivate)
	b.Value("estimated_hours", floatOrNil(r.EstimatedHours))
	b.Value("total_estimated_hours", floatOrNil(m.TotalEstimatedHours()))
	timeProject := m.Project
	if l.apiTimeProjectSet {
		// create の show.api.rsb は User.current.allowed_to?(:view_time_entries, @project)（@project はパラメータのプロジェクト）
		timeProject = l.c.Project
	}
	if timeProject != nil && l.c.AllowedTo(domain.Perm("view_time_entries"), timeProject) {
		b.Value("spent_hours", m.SpentHours())
		b.Value("total_spent_hours", m.TotalSpentHours())
	}
	renderAPIIssueCustomValues(b, m.VisibleCustomFieldValues())
	b.Value("created_on", r.CreatedAt)
	b.Value("updated_on", r.UpdatedAt)
	b.Value("closed_on", r.ClosedAt)
}

// loadVisibleSpentHours は Issue.load_visible_spent_hours と load_visible_total_spent_hours
// （TimeEntry.visible に限った工数を rows の SpentHours / TotalSpentHours に設定する）。
func (l *issueLookup) loadVisibleSpentHours(rows []*query.IssueRow) {
	if len(rows) == 0 {
		return
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	cond := l.timeEntryVisibleCondition()
	spent, err := repository.VisibleSpentHours(l.ctx, l.a.DB, ids, cond)
	l.fail(err)
	total, err := repository.VisibleTotalSpentHours(l.ctx, l.a.DB, ids, cond)
	l.fail(err)
	for _, r := range rows {
		s, t := spent[r.ID], total[r.ID]
		r.SpentHours, r.TotalSpentHours = &s, &t
	}
}

// apiDate は Date の値（nil なら nil。文字列 "YYYY-MM-DD"）。
func apiDate(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

func floatOrNil(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// renderAPIIssueCustomValues は render_api_custom_values(issue.visible_custom_field_values, api)。
func renderAPIIssueCustomValues(b apibuilder.Builder, vs []*issueCFValue) {
	if len(vs) == 0 {
		return
	}
	b.Array("custom_fields", nil, func() {
		for _, v := range vs {
			attrs := apibuilder.A("id", v.CF.ID, "name", v.CF.Name)
			if v.CF.Multiple {
				attrs = append(attrs, apibuilder.KV{Key: "multiple", Value: true})
			}
			b.ObjectAttrs("custom_field", attrs, func() {
				if v.Multi {
					b.Array("value", nil, func() {
						for _, s := range v.Values {
							if s != "" {
								b.Value("value", s)
							}
						}
					})
				} else if len(v.Values) == 0 {
					b.Value("value", nil)
				} else {
					b.Value("value", v.Values[0])
				}
			})
		}
	})
}

// renderAPIAttachment は render_api_attachment(attachment, api)。
func (l *issueLookup) renderAPIAttachment(b apibuilder.Builder, at *repository.ReadAttachment) {
	base := httpx.RequestBaseURL(l.c.R)
	b.Object("attachment", func() {
		b.Value("id", at.ID)
		b.Value("filename", at.Filename)
		b.Value("filesize", at.Filesize)
		b.Value("content_type", strOrNil(at.ContentType))
		b.Value("description", strOrNil(at.Description))
		b.Value("content_url", base+urlroot.Path("/attachments/download/"+itoaID(at.ID)+"/"+url.PathEscape(at.Filename)))
		if attachmentThumbnailable(at) {
			b.Value("thumbnail_url", base+urlroot.Path("/attachments/thumbnail/"+itoaID(at.ID)))
		}
		if u := l.principal(at.AuthorID); u != nil {
			b.Attrs("author", apibuilder.A("id", u.ID, "name", l.principalName(u)))
		}
		b.Value("created_on", at.CreatedAt.Time)
	})
}

func strOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// renderAPIRelation は api.relation(...)。
func renderAPIRelation(b apibuilder.Builder, rel *repository.IssueRelation) {
	var delay any
	if rel.Delay != nil {
		delay = *rel.Delay
	}
	b.Attrs("relation", apibuilder.A("id", rel.ID, "issue_id", rel.IssueFromID, "issue_to_id", rel.IssueToID,
		"relation_type", rel.RelationType, "delay", delay))
}

// issuesShowAPI は IssuesController#show の format.api。
func (a *App) issuesShowAPI(c *Req) { a.issuesShowAPIStatus(c, 0) }

// issuesShowAPIStatus は show.api.rsb をステータス status で返す（create の 201 でも使う）。
func (a *App) issuesShowAPIStatus(c *Req, status int) {
	ctx := c.Ctx()
	l := a.newIssueLookup(c)
	l.apiTimeProjectSet = status == http.StatusCreated
	if status != http.StatusCreated && c.AllowedTo(domain.Perm("view_time_entries"), c.Project) {
		// show の Issue.load_visible_spent_hours / load_visible_total_spent_hours（create の応答では行わない）
		l.loadVisibleSpentHours([]*query.IssueRow{c.currentIssue()})
	}
	m := l.model(c.currentIssue())
	inc := func(k string) bool { return c.IncludeInAPIResponse(k) }
	var journals []*journalView
	if inc("journals") {
		journals = m.visibleJournals()
		if c.Pref().CommentsSorting == "desc" {
			slices.Reverse(journals)
		}
	}
	var relations []*repository.IssueRelation
	if inc("relations") {
		relations = l.visibleRelations(m)
	}
	var statuses []*domain.IssueStatus
	if inc("allowed_statuses") {
		statuses = m.NewStatusesAllowed()
	}
	var changesets []*repository.ReadChangeset
	if inc("changesets") {
		vis, err := a.changesetVisibleCondition(c)
		if err != nil {
			a.internalError(c, "changesets", err)
			return
		}
		cs, err := repository.IssueChangesets(ctx, a.DB, m.Row.ID, vis)
		if err != nil {
			a.internalError(c, "changesets", err)
			return
		}
		changesets = cs
		if c.Pref().CommentsSorting == "desc" {
			slices.Reverse(changesets)
		}
	}
	if l.err != nil {
		a.internalError(c, "issue api", l.err)
		return
	}
	c.RenderAPI(status, func(b apibuilder.Builder) {
		b.Object("issue", func() {
			l.renderAPIIssueCore(b, m)
			if inc("children") && !m.Leaf() {
				l.renderAPIIssueChildren(b, m.Row)
			}
			if inc("attachments") {
				b.Array("attachments", nil, func() {
					for _, at := range l.issueAttachments(m.Row.ID) {
						l.renderAPIAttachment(b, at)
					}
				})
			}
			if inc("relations") && len(relations) > 0 {
				b.Array("relations", nil, func() {
					for _, rel := range relations {
						renderAPIRelation(b, rel)
					}
				})
			}
			if inc("changesets") {
				b.Array("changesets", nil, func() {
					for _, cs := range changesets {
						b.ObjectAttrs("changeset", apibuilder.A("revision", cs.Revision), func() {
							if cs.UserID != nil {
								if u := l.principal(*cs.UserID); u != nil {
									b.Attrs("user", apibuilder.A("id", u.ID, "name", l.principalName(u)))
								}
							}
							b.Value("comments", strOrNil(cs.Comments))
							b.Value("committed_on", cs.CommittedAt.Time)
						})
					}
				})
			}
			if inc("journals") {
				b.Array("journals", nil, func() {
					for _, j := range journals {
						b.ObjectAttrs("journal", apibuilder.A("id", j.ID), func() {
							if j.User != nil {
								b.Attrs("user", apibuilder.A("id", j.UserID, "name", l.principalName(j.User)))
							}
							if j.Journal.Notes == "" && !j.hasNotesColumn() {
								b.Value("notes", nil)
							} else {
								b.Value("notes", j.Journal.Notes)
							}
							b.Value("created_on", j.CreatedAt)
							b.Value("updated_on", j.UpdatedOn())
							if j.UpdatedByID != nil {
								if u := l.principal(*j.UpdatedByID); u != nil {
									b.Attrs("updated_by", apibuilder.A("id", u.ID, "name", l.principalName(u)))
								}
							}
							b.Value("private_notes", j.PrivateNotes)
							b.Array("details", nil, func() {
								for _, d := range j.VisibleDetails() {
									b.ObjectAttrs("detail", apibuilder.A("property", d.Property, "name", d.PropKey), func() {
										b.Value("old_value", strOrNil(d.OldValue))
										b.Value("new_value", strOrNil(d.Value))
									})
								}
							})
						})
					}
				})
			}
			if inc("watchers") && c.AllowedTo(domain.Perm("view_issue_watchers"), m.Project) {
				b.Array("watchers", nil, func() {
					ids, err := m.env().WatcherIDs(ctx, m.I)
					l.fail(err)
					l.preloadPrincipals(ids)
					for _, id := range ids {
						if u := l.principal(id); u != nil {
							b.Attrs("user", apibuilder.A("id", u.ID, "name", l.principalName(u)))
						}
					}
				})
			}
			if inc("allowed_statuses") {
				b.Array("allowed_statuses", nil, func() {
					for _, s := range statuses {
						b.Attrs("status", apibuilder.A("id", s.ID, "name", s.Name, "is_closed", s.IsClosed))
					}
				})
			}
		})
	})
}

// hasNotesColumn はノートが NULL でないか（NULL なら Redmine は "notes": null を出す）。
func (j *journalView) hasNotesColumn() bool { return !j.NotesNull }

// renderAPIIssueChildren は render_api_issue_children(issue, api)。
func (l *issueLookup) renderAPIIssueChildren(b apibuilder.Builder, r *query.IssueRow) {
	if !l.hasChildren(r.ID) {
		return
	}
	children, err := repository.ReadIssuesWhere(l.ctx, l.a.DB, "issues.parent_id = ?", "issues.hier_path", r.ID)
	l.fail(err)
	b.Array("children", nil, func() {
		for _, ch := range children {
			cr := issueRowFromRead(ch)
			l.addIssues([]*query.IssueRow{cr})
			b.ObjectAttrs("issue", apibuilder.A("id", cr.ID), func() {
				b.Attrs("tracker", apibuilder.A("id", cr.TrackerID, "name", l.tracker(cr.TrackerID).Name))
				b.Value("subject", cr.Subject)
				l.renderAPIIssueChildren(b, cr)
			})
		}
	})
}
