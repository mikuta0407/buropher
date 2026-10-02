package handler

// IssuesController#show（html / atom / pdf / api）と issue_tab、およびチケット詳細画面の HTML 部品
// （issues/show・_action_menu・_relations・_subtasks・tabs/_history・watchers/_watchers・attachments/_links）。

import (
	"errors"
	"html/template"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// issueShowView はチケット詳細画面のデータ（@issue, @journals, @relations, @allowed_statuses ...）。
type issueShowView struct {
	l *issueLookup
	M *issueModel

	relatedQuery *query.Query
	// sessionQuery は retrieve_query_from_session の @query（サイドバーの選択表示に使う）。
	sessionQuery *query.Query

	Journals        []*journalView
	Relations       []*repository.IssueRelation
	AllowedStatuses []*domain.IssueStatus

	PrevIssueID   any
	NextIssueID   any
	IssuePosition any
	IssueCount    any
	QueryPath     any

	HasChangesets bool
	AtomKey       string
	CanViewTime   bool

	// issue_relations#create.js で issue_relations/_form に渡す状態（@relation と @unsaved_relations）。
	relationForm          *relationFormModel
	RelationErrorMessages []string
	UnsavedRelationsIDs   string
}

// IssuesShow は IssuesController#show。
func (a *App) IssuesShow(c *Req) {
	switch formatOf(c) {
	case "html":
		a.issuesShowHTML(c)
	case "json", "xml":
		a.issuesShowAPI(c)
	case "atom":
		a.issuesShowAtom(c)
	case "pdf":
		a.issuesShowPDF(c)
	default:
		renderUnknownFormat(c)
	}
}

// newIssueShowView は show の共通部分（@journals / @relations / @allowed_statuses）。
func (a *App) newIssueShowView(c *Req, l *issueLookup) *issueShowView {
	m := l.model(c.currentIssue())
	v := &issueShowView{l: l, M: m}
	v.Journals = m.visibleJournals()
	if c.Pref().CommentsSorting == "desc" {
		slices.Reverse(v.Journals)
	}
	v.Relations = l.visibleRelations(m)
	v.AllowedStatuses = m.NewStatusesAllowed()
	v.CanViewTime = c.AllowedTo(domain.Perm("view_time_entries"), m.Project)
	return v
}

func (a *App) issuesShowHTML(c *Req) {
	l := a.newIssueLookup(c)
	if c.AllowedTo(domain.Perm("view_time_entries"), c.Project) {
		// Issue.load_visible_spent_hours / load_visible_total_spent_hours
		l.loadVisibleSpentHours([]*query.IssueRow{c.currentIssue()})
	}
	v := a.newIssueShowView(c, l)
	m := v.M
	vis, err := a.changesetVisibleCondition(c)
	if err != nil {
		a.internalError(c, "changesets", err)
		return
	}
	cs, err := repository.IssueChangesets(c.Ctx(), a.DB, m.Row.ID, vis)
	if err != nil {
		a.internalError(c, "changesets", err)
		return
	}
	v.HasChangesets = len(cs) > 0
	v.AtomKey = c.AtomKey()
	a.retrievePreviousAndNextIssueIDs(c, l, v)
	form := a.newIssueEditForm(c, l, v)
	if l.err != nil {
		a.internalError(c, "issue show", l.err)
		return
	}
	data := map[string]any{"V": v, "M": m, "F": form, "Title": m.Heading() + ": " + m.Row.Subject}
	cur := v.sessionQuery
	if cur == nil {
		env, err := a.queryEnv(c)
		if err != nil {
			a.internalError(c, "query env", err)
			return
		}
		if cur, err = query.New(c.Ctx(), env, query.KindIssue, c.Project); err != nil {
			a.internalError(c, "query", err)
			return
		}
	}
	sidebar, err := a.sidebarQueriesHTML(c, query.KindIssue, cur, issuesPath(c.Project))
	if err != nil {
		a.internalError(c, "sidebar queries", err)
		return
	}
	data["SidebarQueries"] = sidebar
	c.Render("issues/show", data)
}

// changesetVisibleCondition は Changeset.visible の条件（view_changesets）。
func (a *App) changesetVisibleCondition(c *Req) (string, error) {
	return c.Authz().AllowedToCondition(c.Ctx(), "view_changesets", issueVisOpts(), nil)
}

// visibleRelations は @issue.relations.select {|r| r.other_issue(@issue)&.visible? }（IssueRelation#<=> の順）。
func (l *issueLookup) visibleRelations(m *issueModel) []*repository.IssueRelation {
	if m.I == nil {
		return nil
	}
	rels, err := l.issuesEnv().VisibleRelations(l.ctx, m.I, l.c.User)
	l.fail(err)
	var ids []int64
	out := make([]*repository.IssueRelation, 0, len(rels))
	for _, r := range rels {
		rr := &repository.IssueRelation{ID: r.ID, IssueFromID: r.IssueFromID, IssueToID: r.IssueToID, RelationType: r.RelationType, Delay: r.Delay}
		l.relations[r.ID] = rr
		out = append(out, rr)
		ids = append(ids, r.IssueFromID, r.IssueToID)
	}
	l.preloadIssues(ids)
	return out
}

func otherIssueID(r *repository.IssueRelation, id int64) int64 {
	if r.IssueFromID == id {
		return r.IssueToID
	}
	return r.IssueFromID
}

func joinInt64(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func errorsIsNotFound(err error) bool {
	return errors.Is(err, repository.ErrNotFound) || errors.Is(err, query.ErrNotFound)
}

// timeLike は日付（time.Time）。
type timeLike = time.Time

// userToday は User.current.today。
func (l *issueLookup) userToday() time.Time {
	now := l.a.now()
	if l.c.Loc != nil && l.c.Loc.Location != nil {
		now = now.In(l.c.Loc.Location)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// newAuthorizerFor は user の Authorizer。
func newAuthorizerFor(l *issueLookup, u *domain.User) *authz.Authorizer { return authz.New(l.a.DB, u) }

// ---------------------------------------------------------------- 前後のチケット

// flashPrevNextKey は flash[:previous_and_next_issue_ids]。
const flashPrevNextKey = "previous_and_next_issue_ids"

// retrievePreviousAndNextIssueIDs は IssuesController#retrieve_previous_and_next_issue_ids。
func (a *App) retrievePreviousAndNextIssueIDs(c *Req, l *issueLookup, v *issueShowView) {
	// flash[:previous_and_next_issue_ids]（更新後のリダイレクトで設定される）。buropher の flash は文字列のみを
	// 保持するため、値はクエリ文字列（prev_issue_id=..&next_issue_id=..&issue_position=..&issue_count=..）で持つ。
	if f := c.Flash(); f != nil && f.Has(flashPrevNextKey) {
		info, _ := url.ParseQuery(f.Get(flashPrevNextKey))
		toI := func(k string) any {
			if s := info.Get(k); rails.IsPresent(s) {
				return int64(customfield.RubyToI(s))
			}
			return nil
		}
		v.PrevIssueID, v.NextIssueID = toI("prev_issue_id"), toI("next_issue_id")
		v.IssuePosition, v.IssueCount = toI("issue_position"), toI("issue_count")
		f.Delete(flashPrevNextKey)
		return
	}
	q, err := a.retrieveIssueQueryFromSession(c)
	if err != nil {
		l.fail(err)
		return
	}
	if q == nil {
		return
	}
	v.sessionQuery = q
	perPage := c.PerPageOption()
	const limit = 500
	ids, err := q.IssueIDs(c.Ctx(), query.ListOptions{Limit: limit + 1})
	if err != nil {
		// Query::StatementInvalid は show 自体を失敗させない（Redmine では rescue_from で 500 になるが通常起きない）
		l.fail(err)
		return
	}
	id := v.M.Row.ID
	if idx := slices.Index(ids, id); idx >= 0 && idx < limit {
		if len(ids) < limit {
			v.IssuePosition = int64(idx + 1)
			v.IssueCount = int64(len(ids))
		}
		if idx > 0 {
			v.PrevIssueID = ids[idx-1]
		}
		if idx < len(ids)-1 {
			v.NextIssueID = ids[idx+1]
		}
	}
	params := q.AsParams()
	if pos, ok := v.IssuePosition.(int64); ok {
		params.Set("page", strconv.FormatInt(pos/int64(perPage)+1, 10))
		params.Set("per_page", strconv.Itoa(perPage))
	}
	path := issuesPath(q.Project)
	if qs := helper.ToQuery(params); qs != "" {
		path += "?" + qs
	}
	v.QueryPath = path
}

// retrieveIssueQueryFromSession は QueriesHelper#retrieve_query_from_session。
func (a *App) retrieveIssueQueryFromSession(c *Req) (*query.Query, error) {
	env, err := a.queryEnv(c)
	if err != nil {
		return nil, err
	}
	sess := c.issueQuerySession()
	if sess == nil {
		return query.Default(c.Ctx(), env, query.KindIssue, c.Project)
	}
	var q *query.Query
	if sess.ID != 0 {
		q, err = query.Load(c.Ctx(), env, sess.ID, query.KindIssue)
		if err != nil || q == nil {
			if errorsIsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
	} else {
		q, err = query.New(c.Ctx(), env, query.KindIssue, nil)
		if err != nil {
			return nil, err
		}
		if sess.Filters != nil {
			q.Filters = query.NewFilters()
			for _, f := range sess.Filters {
				q.Filters.Set(f.Field, query.Filter{Operator: f.Operator, Values: f.Values})
			}
		}
		q.GroupBy = sess.GroupBy
		if err := q.SetColumnNames(c.Ctx(), sess.ColumnNames); err != nil {
			return nil, err
		}
		q.SetTotalableNames(sess.TotalableNames)
		q.SetSortCriteria(sess.Sort)
	}
	// session_data.has_key?(:project_id) なら project_id（nil を含む）、無ければ @project
	var p *domain.Project
	if sess.ProjectID != nil {
		p, err = repository.GetProject(c.Ctx(), a.DB, *sess.ProjectID)
		if err != nil && !errorsIsNotFound(err) {
			return nil, err
		}
	}
	q.SetProject(p)
	return q, nil
}

// ---------------------------------------------------------------- テンプレート用メソッド

// Issue は @issue の行。
func (v *issueShowView) Issue() *query.IssueRow { return v.M.Row }

// Heading は issue_heading(@issue)。
func (v *issueShowView) Heading() string { return v.M.Heading() }

// CSSClasses は @issue.css_classes。
func (v *issueShowView) CSSClasses() string { return v.l.cssClasses(v.M.Row) }

// StatusBadge は issue_status_type_badge(@issue.status)。
func (v *issueShowView) StatusBadge() template.HTML {
	if v.M.Status.IsClosed {
		return rails.ContentTag("span", v.l.L("label_closed_issues"), rails.NewHash("class", "badge badge-status-closed"))
	}
	return rails.ContentTag("span", v.l.L("label_open_issues"), rails.NewHash("class", "badge badge-status-open"))
}

// Author / AssignedTo は @issue.author / assigned_to。
func (v *issueShowView) Author() *domain.User     { return v.l.principal(v.M.Row.AuthorID) }
func (v *issueShowView) AssignedTo() *domain.User { return v.l.principalPtr(v.M.Row.AssignedToID) }

// AuthorAvatarTitle は author_avatar の title。
func (v *issueShowView) AuthorAvatarTitle() string {
	return v.l.L("field_author") + ": " + v.l.principalName(v.Author())
}

// AssigneeAvatarTitle は assignee_avatar の title。
func (v *issueShowView) AssigneeAvatarTitle() string {
	return v.l.L("field_assigned_to") + ": " + v.l.principalName(v.AssignedTo())
}

// SubjectWithTree は render_issue_subject_with_tree(@issue)。
func (v *issueShowView) SubjectWithTree() template.HTML {
	var b strings.Builder
	var ancestors []*query.IssueRow
	for _, a := range v.M.Ancestors() {
		if v.l.issueVisible(a) {
			ancestors = append(ancestors, a)
		}
	}
	for _, a := range ancestors {
		b.WriteString("<div>")
		b.WriteString(string(rails.ContentTag("p", v.l.linkToIssue(a, redmine.LinkToIssueOptions{Project: a.ProjectID != v.M.Row.ProjectID}), nil)))
	}
	b.WriteString("<div>")
	b.WriteString(string(rails.ContentTag("h3", rails.H(v.M.Row.Subject), nil)))
	b.WriteString(strings.Repeat("</div>", len(ancestors)+1))
	return template.HTML(b.String())
}

// ReactionButton は reaction_button(@issue)。
func (v *issueShowView) ReactionButton() template.HTML {
	return v.l.reactionButton("issue", v.M.Row.ID, v.M)
}

// Updated は @issue.created_on != @issue.updated_on。
func (v *issueShowView) Updated() bool { return !v.M.Row.CreatedAt.Equal(v.M.Row.UpdatedAt) }

// UpdatedTime は l(:label_updated_time, time_tag(@issue.updated_on))。
func (v *issueShowView) UpdatedTime() template.HTML {
	return template.HTML(v.l.L("label_updated_time", string(v.l.a.Helpers.TimeTag(v.l.page, v.M.Row.UpdatedAt))))
}

// attrRow は IssueFieldsRows#cells(label, text, options)。
func attrRow(label string, text any, class string) template.HTML {
	return rails.ContentTag("div",
		rails.ContentTag("div", rails.H(label)+":", rails.NewHash("class", "label"))+rails.ContentTag("div", text, rails.NewHash("class", "value")),
		rails.NewHash("class", class+" attribute"))
}

// fieldsRows は issue_fields_rows（左右 2 列）。
func fieldsRows(left, right []template.HTML) template.HTML {
	join := func(xs []template.HTML) template.HTML {
		var s template.HTML
		for _, x := range xs {
			s += x
		}
		return s
	}
	content := rails.ContentTag("div", join(left), rails.NewHash("class", "splitcontentleft")) +
		rails.ContentTag("div", join(right), rails.NewHash("class", "splitcontentleft"))
	return rails.ContentTag("div", content, rails.NewHash("class", "splitcontent"))
}

// AttributesRows は show.html.erb の issue_fields_rows ブロック。
func (v *issueShowView) AttributesRows() template.HTML {
	l, m := v.l, v.M
	r := m.Row
	var left, right []template.HTML
	left = append(left, attrRow(l.L("field_status"), m.Status.Name, "status"))
	if !m.FieldDisabled("priority_id") {
		left = append(left, attrRow(l.L("field_priority"), m.Priority.Name, "priority"))
	}
	if !m.FieldDisabled("assigned_to_id") {
		var val any = "-"
		if u := v.AssignedTo(); u != nil {
			val = l.linkToPrincipal(u)
		}
		left = append(left, attrRow(l.L("field_assigned_to"), val, "assigned-to"))
	}
	cat := l.category(r.CategoryID)
	if !m.FieldDisabled("category_id") && !(cat == nil && len(l.projectCategories(m.Project.ID)) == 0) {
		var val any = "-"
		if cat != nil {
			val = cat.Name
		}
		left = append(left, attrRow(l.L("field_category"), val, "category"))
	}
	ver := l.version(r.FixedVersionID)
	if !m.FieldDisabled("fixed_version_id") && !(ver == nil && len(m.AssignableVersions()) == 0) {
		var val any = "-"
		if ver != nil {
			val = l.linkToVersion(ver)
		}
		left = append(left, attrRow(l.L("field_fixed_version"), val, "fixed-version"))
	}
	if !m.FieldDisabled("start_date") {
		val := ""
		if r.StartDate != nil {
			val = l.formatDate(*r.StartDate)
		}
		right = append(right, attrRow(l.L("field_start_date"), val, "start-date"))
	}
	if !m.FieldDisabled("due_date") {
		var val any
		if r.DueDate != nil {
			s := l.formatDate(*r.DueDate)
			if !m.Closed() {
				s += " (" + l.dueDateDistanceInWords(*r.DueDate) + ")"
			}
			val = s
		}
		right = append(right, attrRow(l.L("field_due_date"), val, "due-date"))
	}
	if !m.FieldDisabled("done_ratio") {
		dr := l.doneRatio(r)
		right = append(right, attrRow(l.L("field_done_ratio"), helper.ProgressBar(dr, strconv.Itoa(dr)+"%"), "progress"))
	}
	if !m.FieldDisabled("estimated_hours") {
		right = append(right, attrRow(l.L("field_estimated_hours"), v.estimatedHoursDetails(), "estimated-hours"))
	}
	if v.CanViewTime && m.TotalSpentHours() > 0 {
		right = append(right, attrRow(l.L("label_spent_time"), v.spentHoursDetails(), "spent-time"))
	}
	return fieldsRows(left, right)
}

// projectCategories は project.issue_categories。
func (l *issueLookup) projectCategories(projectID int64) []*repository.IssueCategory {
	cs, err := repository.ProjectIssueCategories(l.ctx, l.a.DB, projectID)
	l.fail(err)
	out := make([]*repository.IssueCategory, len(cs))
	for i, c := range cs {
		out[i] = &repository.IssueCategory{ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, AssignedToID: c.AssignedToID}
	}
	return out
}

// dueDateDistanceInWords は due_date_distance_in_words(date)。
func (l *issueLookup) dueDateDistanceInWords(d timeLike) string {
	today := l.userToday()
	key := "label_roadmap_due_in"
	if d.Before(today) {
		key = "label_roadmap_overdue"
	}
	return l.L(key, l.c.Loc.DistanceOfDateInWords(today, d))
}

// estimatedHoursDetails は issue_estimated_hours_details(issue)。
func (v *issueShowView) estimatedHoursDetails() any {
	m, l := v.M, v.l
	total := m.TotalEstimatedHours()
	if total == nil {
		return nil
	}
	est := m.Row.EstimatedHours
	if est != nil && *total == *est {
		return l.c.Loc.LHoursShort(*est)
	}
	s := ""
	if est != nil {
		s = l.c.Loc.LHoursShort(*est)
	}
	s += " (" + l.L("label_total") + ": " + l.c.Loc.LHoursShort(*total) + ")"
	return template.HTML(s)
}

// spentHoursDetails は issue_spent_hours_details(issue)。
func (v *issueShowView) spentHoursDetails() any {
	m, l := v.M, v.l
	total := m.TotalSpentHours()
	if total <= 0 {
		return nil
	}
	path := "/projects/" + m.Project.Identifier + "/time_entries?issue_id=" + url.QueryEscape("~"+strconv.FormatInt(m.Row.ID, 10))
	spent := m.SpentHours()
	if total == spent {
		return rails.LinkTo(l.c.Loc.LHoursShort(spent), path, nil)
	}
	switch l.a.Settings.String("cross_project_subtasks") {
	case "system", "tree", "hierarchy":
		path = "/time_entries?issue_id=" + url.QueryEscape("~"+strconv.FormatInt(m.Row.ID, 10))
	}
	s := ""
	if spent > 0 {
		s = l.c.Loc.LHoursShort(spent)
	}
	s += " (" + l.L("label_total") + ": " + string(rails.LinkTo(l.c.Loc.LHoursShort(total), path, nil)) + ")"
	return template.HTML(s)
}

// customFieldNameTag は custom_field_name_tag(custom_field)。
func customFieldNameTag(cf *customfield.CustomField) template.HTML {
	var title, css any
	if d := cf.DescriptionString(); strings.TrimSpace(d) != "" {
		title, css = d, "field-description"
	}
	return rails.ContentTag("span", cf.Name, rails.NewHash("title", title, "class", css))
}

// customFieldValueTag は custom_field_value_tag(value)。
func (v *issueShowView) customFieldValueTag(cv *issueCFValue) template.HTML {
	s := rails.H(v.l.formatCustomValue(cv.CF, cv.Value(), v.M.customized(), true))
	if rails.IsPresent(string(s)) && cv.CF.FullTextFormatting() {
		return rails.ContentTag("div", s, rails.NewHash("class", "wiki"))
	}
	return s
}

// HalfWidthCustomFieldsRows は render_half_width_custom_fields_rows(@issue)。
func (v *issueShowView) HalfWidthCustomFieldsRows() template.HTML {
	var values []*issueCFValue
	for _, cv := range v.M.VisibleCustomFieldValues() {
		if !cv.CF.FullWidthLayout() {
			values = append(values, cv)
		}
	}
	if len(values) == 0 {
		return ""
	}
	half := (len(values) + 1) / 2
	var left, right []template.HTML
	for i, cv := range values {
		row := rails.ContentTag("div",
			rails.ContentTag("div", customFieldNameTag(cv.CF)+":", rails.NewHash("class", "label"))+
				rails.ContentTag("div", v.customFieldValueTag(cv), rails.NewHash("class", "value")),
			rails.NewHash("class", cv.CF.CSSClasses()+" attribute"))
		if i < half {
			left = append(left, row)
		} else {
			right = append(right, row)
		}
	}
	return fieldsRows(left, right)
}

// FullWidthCustomFieldsRows は render_full_width_custom_fields_rows(@issue)。
func (v *issueShowView) FullWidthCustomFieldsRows() template.HTML {
	var s template.HTML
	for _, cv := range v.M.VisibleCustomFieldValues() {
		if !cv.CF.FullWidthLayout() {
			continue
		}
		val := v.customFieldValueTag(cv)
		if !rails.IsPresent(string(val)) {
			continue
		}
		content := rails.ContentTag("hr", "", nil) +
			rails.ContentTag("p", rails.ContentTag("strong", customFieldNameTag(cv.CF), nil), nil) +
			rails.ContentTag("div", val, rails.NewHash("class", "value"))
		s += rails.ContentTag("div", content, rails.NewHash("class", cv.CF.CSSClasses()+" attribute"))
	}
	return s
}

// HasDescription は @issue.description?。
func (v *issueShowView) HasDescription() bool { return rails.IsPresent(v.M.Row.Description) }

// Description は textilizable @issue, :description, :attachments => @issue.attachments。
func (v *issueShowView) Description() template.HTML {
	return v.l.textilizeIssue(v.M.Row, v.M.Row.Description)
}

// QuoteButton は quote_reply_button(url: quoted_issue_path(@issue))（notes_addable? のとき）。
func (v *issueShowView) QuoteButton() template.HTML {
	if !v.M.NotesAddable() {
		return ""
	}
	return v.l.quoteReplyButton("/issues/"+strconv.FormatInt(v.M.Row.ID, 10)+"/quoted", false)
}

// Attachments は @issue.attachments。
func (v *issueShowView) Attachments() []*repository.ReadAttachment {
	return v.l.issueAttachments(v.M.Row.ID)
}

// AttachmentLinks は link_to_attachments @issue, :thumbnails => true（attachments/_links）。
func (v *issueShowView) AttachmentLinks() template.HTML {
	return v.l.attachmentLinks("issues", v.M.Row.ID, v.Attachments(), v.M.AttributesEditable(), v.M.AttachmentsDeletable(), true)
}

// attachmentLinks は attachments/_links。
func (l *issueLookup) attachmentLinks(containerPath string, id int64, atts []*repository.ReadAttachment, editable, deletable, thumbnails bool) template.HTML {
	if len(atts) == 0 {
		return ""
	}
	sid := strconv.FormatInt(id, 10)
	var b strings.Builder
	b.WriteString("<div class=\"attachments\">\n<div class=\"contextual\">\n  ")
	if editable {
		b.WriteString(string(rails.LinkTo(l.icon("edit", l.L("label_edit_attachments")), "/attachments/"+containerPath+"/"+sid+"/edit",
			rails.NewHash("title", l.L("label_edit_attachments"), "class", "icon-only icon-edit "))))
	}
	b.WriteString("\n  ")
	if len(atts) > 1 {
		b.WriteString(string(rails.LinkTo(l.icon("download", l.L("label_download_all_attachments")), "/attachments/"+containerPath+"/"+sid+"/download",
			rails.NewHash("title", l.L("label_download_all_attachments"), "class", "icon-only icon-download "))))
	}
	b.WriteString("\n</div>\n<table>\n")
	var uids []int64
	for _, a := range atts {
		uids = append(uids, a.AuthorID)
	}
	l.preloadPrincipals(uids)
	for _, a := range atts {
		aid := strconv.FormatInt(a.ID, 10)
		b.WriteString("<tr>\n  <td>\n    ")
		b.WriteString(string(rails.LinkTo(l.icon("attachment", a.Filename), "/attachments/"+aid, rails.NewHash("class", "icon icon-attachment "))))
		b.WriteString("    <span class=\"size\">(")
		b.WriteString(string(rails.H(l.c.Loc.NumberToHumanSize(a.Filesize))))
		b.WriteString(")</span>\n    ")
		b.WriteString(string(rails.LinkTo(l.icon("download", a.Filename), "/attachments/download/"+aid+"/"+url.PathEscape(a.Filename),
			rails.NewHash("class", "icon-only icon-download ", "title", l.L("button_download")))))
		b.WriteString("  </td>\n  <td>")
		if a.Description != nil && rails.IsPresent(*a.Description) {
			b.WriteString(string(rails.H(*a.Description)))
		}
		b.WriteString("</td>\n  <td>\n      <span class=\"author\">")
		b.WriteString(string(rails.H(l.principalName(l.principal(a.AuthorID)))))
		b.WriteString(", ")
		b.WriteString(string(rails.H(l.formatTime(a.CreatedAt.Time))))
		b.WriteString("</span>\n  </td>\n  <td>\n")
		if deletable {
			b.WriteString("      ")
			b.WriteString(string(rails.LinkTo(l.icon("del", l.L("button_delete")), "/attachments/"+aid,
				rails.NewHash("data", rails.NewHash("confirm", l.L("text_are_you_sure")), "method", "delete",
					"class", "delete icon-only icon-del ", "title", l.L("button_delete")))))
			b.WriteString("\n")
		}
		b.WriteString("  </td>\n</tr>\n")
	}
	b.WriteString("</table>\n")
	if thumbnails && l.a.Settings.Bool("thumbnails_enabled") {
		var imgs []*repository.ReadAttachment
		for _, a := range atts {
			if attachmentThumbnailable(a) {
				imgs = append(imgs, a)
			}
		}
		if len(imgs) > 0 {
			b.WriteString("  <div class=\"thumbnails\">\n")
			for _, a := range imgs {
				b.WriteString("      ")
				b.WriteString(string(l.thumbnailTag(a)))
				b.WriteString("\n")
			}
			b.WriteString("  </div>\n")
		}
	}
	b.WriteString("</div>\n")
	return template.HTML(b.String())
}

// attachmentThumbnailable は Attachment#thumbnailable?（画像のみ。PDF のサムネイルは未対応）。
func attachmentThumbnailable(a *repository.ReadAttachment) bool {
	ct := ""
	if a.ContentType != nil {
		ct = *a.ContentType
	}
	lower := strings.ToLower(a.Filename)
	img := strings.HasSuffix(lower, ".bmp") || strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpe") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".webp")
	return img || strings.HasPrefix(ct, "image/")
}

// thumbnailTag は thumbnail_tag(attachment)。
func (l *issueLookup) thumbnailTag(a *repository.ReadAttachment) template.HTML {
	size, _ := strconv.Atoi(l.a.Settings.String("thumbnails_size"))
	ref := &redmine.Attachment{ID: a.ID, Filename: a.Filename}
	return l.renderer().ThumbnailTag(ref, size)
}

// ---------------------------------------------------------------- サブタスク・関連

// ShowSubtasks は !@issue.leaf? || User.current.allowed_to?(:manage_subtasks, @project)。
func (v *issueShowView) ShowSubtasks() bool {
	return !v.M.Leaf() || v.M.allowedTo("manage_subtasks")
}

// ShowRelations は @relations.present? || User.current.allowed_to?(:manage_issue_relations, @project)。
func (v *issueShowView) ShowRelations() bool {
	return len(v.Relations) > 0 || v.M.allowedTo("manage_issue_relations")
}

// RelatedHeadersClass は class: { 'with-related-issues-table-headers': Setting.display_related_issues_table_headers? }。
func (v *issueShowView) RelatedHeadersClass() string {
	if v.l.a.Settings.Bool("display_related_issues_table_headers") {
		return "with-related-issues-table-headers"
	}
	return ""
}

// CanManageSubtasks / CanManageRelations は権限。
func (v *issueShowView) CanManageSubtasks() bool  { return v.M.allowedTo("manage_subtasks") }
func (v *issueShowView) CanManageRelations() bool { return v.M.allowedTo("manage_issue_relations") }

// NewSubtaskURL は url_for_new_subtask(@issue)。
func (v *issueShowView) NewSubtaskURL() string {
	m := v.M
	h := rails.NewHash("issue", rails.NewHash("parent_issue_id", m.Row.ID))
	if !m.FieldDisabled("parent_issue_id") {
		h.Get("issue").(*rails.Hash).Set("tracker_id", m.Row.TrackerID)
	}
	if v.l.c.Controller.Name == "issues" && v.l.c.Action == "show" {
		h.Set("back_url", "/issues/"+strconv.FormatInt(m.Row.ID, 10))
	}
	return "/projects/" + m.Project.Identifier + "/issues/new?" + helper.ToQuery(h)
}

// relatedColumns は get_related_issues_columns_for_project(issue)。
func (v *issueShowView) relatedColumns() []*query.Column {
	env, err := v.l.a.queryEnv(v.l.c)
	if err != nil {
		v.l.fail(err)
		return nil
	}
	q, err := query.New(v.l.ctx, env, query.KindIssue, v.M.Project)
	if err != nil {
		v.l.fail(err)
		return nil
	}
	avail, err := q.AvailableInlineColumns(v.l.ctx)
	v.l.fail(err)
	var out []*query.Column
	for _, name := range v.l.a.Settings.Strings("related_issues_default_columns") {
		if name == "tracker" || name == "subject" {
			continue
		}
		for _, c := range avail {
			if c.Name == name {
				out = append(out, c)
				break
			}
		}
	}
	v.relatedQuery = q
	return out
}

// relatedHeaders は display_related_issues_table_headers? のときの thead。
func (v *issueShowView) relatedHeaders(cols []*query.Column) template.HTML {
	if !v.l.a.Settings.Bool("display_related_issues_table_headers") {
		return ""
	}
	ths := rails.ContentTag("th", v.l.L("field_subject"), nil)
	for _, c := range cols {
		ths += rails.ContentTag("th", c.CaptionText(v.relatedQuery.Env()), nil)
	}
	return rails.ContentTag("thead", rails.ContentTag("tr", ths, nil), rails.NewHash("class", "related-issues"))
}

// loadRowColumns は関連・サブタスクの行に列の値（spent_hours 等）を読み込む。
func (v *issueShowView) loadRowColumns(cols []*query.Column, rows []*query.IssueRow) {
	if len(rows) == 0 || len(cols) == 0 || v.relatedQuery == nil {
		return
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	if err := v.relatedQuery.SetColumnNames(v.l.ctx, append([]string{"id"}, names...)); err != nil {
		v.l.fail(err)
		return
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	v.relatedQuery.Filters = query.NewFilters()
	loaded, err := v.relatedQuery.Issues(v.l.ctx, query.ListOptions{Conditions: "issues.id IN (" + joinInt64(ids) + ")"})
	if err != nil {
		v.l.fail(err)
		return
	}
	byID := map[int64]*query.IssueRow{}
	for _, r := range loaded {
		byID[r.ID] = r
	}
	for _, r := range rows {
		if x := byID[r.ID]; x != nil {
			r.SpentHours, r.TotalSpentHours, r.LastUpdatedByID, r.LastNotes = x.SpentHours, x.TotalSpentHours, x.LastUpdatedByID, x.LastNotes
			r.RelationIDs, r.CustomValues, r.WatcherIDs = x.RelationIDs, x.CustomValues, x.WatcherIDs
		}
	}
}

// DescendantsStats は render_descendants_stats(@issue)（leaf なら空）。
func (v *issueShowView) DescendantsStats() template.HTML {
	if v.M.Leaf() {
		return ""
	}
	open, closed := 0, 0
	for _, d := range v.M.VisibleDescendants() {
		if v.l.status(d.StatusID).IsClosed {
			closed++
		} else {
			open++
		}
	}
	return v.l.issuesStats(open, closed, rails.NewHash("parent_id", "~"+strconv.FormatInt(v.M.Row.ID, 10)))
}

// RelationsStats は render_relations_stats(@issue, @relations)。
func (v *issueShowView) RelationsStats() template.HTML {
	open, closed := 0, 0
	var ids []string
	for _, r := range v.Relations {
		o := v.l.issue(otherIssueID(r, v.M.Row.ID))
		if v.l.status(o.StatusID).IsClosed {
			closed++
		} else {
			open++
		}
		ids = append(ids, strconv.FormatInt(o.ID, 10))
	}
	return v.l.issuesStats(open, closed, rails.NewHash("issue_id", strings.Join(ids, ",")))
}

// issuesStats は render_issues_stats(open, closed, issues_path_attr)。
func (l *issueLookup) issuesStats(open, closed int, attrs *rails.Hash) template.HTML {
	total := open + closed
	if total == 0 {
		return ""
	}
	path := func(status string) string {
		h := attrs.Clone()
		h.Set("set_filter", true)
		h.Set("status_id", status)
		return "/issues?" + helper.ToQuery(h)
	}
	all := rails.ContentTag("span", rails.LinkTo(strconv.Itoa(total), path("*"), nil), rails.NewHash("class", "badge badge-issues-count"))
	closedB := rails.ContentTag("span", rails.LinkToIf(closed > 0, l.L("label_x_closed_issues_abbr", i18n.Vars{"count": closed}), path("c"), nil),
		rails.NewHash("class", "closed"))
	openB := rails.ContentTag("span", rails.LinkToIf(open > 0, l.L("label_x_open_issues_abbr", i18n.Vars{"count": open}), path("o"), nil),
		rails.NewHash("class", "open"))
	return rails.ContentTag("span", template.HTML(string(all)+" ("+string(openB)+" &#8212; "+string(closedB)+")"), rails.NewHash("class", "issues-stat"))
}

// DescendantsTree は render_descendants_tree(@issue)（leaf なら空）。
func (v *issueShowView) DescendantsTree() template.HTML {
	if v.M.Leaf() {
		return ""
	}
	l := v.l
	cols := v.relatedColumns()
	children := v.M.VisibleDescendants()
	v.loadRowColumns(cols, children)
	manage := v.CanManageSubtasks()
	var b strings.Builder
	b.WriteString(`<table class="list issues odd-even">`)
	b.WriteString(string(v.relatedHeaders(cols)))
	var ancestors []*query.IssueRow
	for _, child := range children {
		for len(ancestors) > 0 && !isDescendantOf(child, ancestors[len(ancestors)-1]) {
			ancestors = ancestors[:len(ancestors)-1]
		}
		level := len(ancestors)
		if l.hasChildren(child.ID) {
			ancestors = append(ancestors, child)
		}
		css := "issue issue-" + strconv.FormatInt(child.ID, 10) + " hascontextmenu " + l.cssClasses(child)
		if level > 0 {
			css += " idnt idnt-" + strconv.Itoa(level)
		}
		var buttons template.HTML
		if manage {
			u := "/issues/" + strconv.FormatInt(child.ID, 10) + "?" + helper.ToQuery(rails.NewHash(
				"back_url", "/issues/"+strconv.FormatInt(v.M.Row.ID, 10), "issue", rails.NewHash("parent_issue_id", ""), "no_flash", "1"))
			buttons = rails.LinkTo(l.icon("link-break", l.L("label_delete_link_to_subtask")), u,
				rails.NewHash("method", "put", "data", rails.NewHash("confirm", l.L("text_are_you_sure")),
					"title", l.L("label_delete_link_to_subtask"), "class", "icon-only icon-link-break"))
		}
		buttons += l.a.Helpers.LinkToContextMenu(l.page)
		row := rails.ContentTag("td", rails.CheckBoxTag("ids[]", child.ID, false, rails.NewHash("id", nil)), rails.NewHash("class", "checkbox")) +
			rails.ContentTag("td", l.linkToIssue(child, redmine.LinkToIssueOptions{Project: child.ProjectID != v.M.Row.ProjectID}), rails.NewHash("class", "subject"))
		for _, c := range cols {
			row += rails.ContentTag("td", l.columnContent(c, child), rails.NewHash("class", c.CSSClasses()))
		}
		row += rails.ContentTag("td", buttons, rails.NewHash("class", "buttons"))
		b.WriteString(string(rails.ContentTag("tr", row, rails.NewHash("class", css, "id", "issue-"+strconv.FormatInt(child.ID, 10)))))
	}
	b.WriteString("</table>")
	return template.HTML(b.String())
}

// IssueRelations は render_issue_relations(@issue, @relations)。
func (v *issueShowView) IssueRelations() template.HTML {
	l := v.l
	cols := v.relatedColumns()
	var others []*query.IssueRow
	for _, r := range v.Relations {
		others = append(others, l.issue(otherIssueID(r, v.M.Row.ID)))
	}
	v.loadRowColumns(cols, others)
	manage := v.CanManageRelations()
	s := v.relatedHeaders(cols)
	cross := l.a.Settings.Bool("cross_project_issue_relations")
	for i, rel := range v.Relations {
		other := others[i]
		css := "issue hascontextmenu " + l.cssClasses(other) + " rel-" + relationTypeFor(rel, other.ID)
		var buttons template.HTML
		if manage {
			buttons = rails.LinkTo(l.icon("link-break", l.L("label_relation_delete")),
				"/relations/"+strconv.FormatInt(rel.ID, 10)+"?issue_id="+strconv.FormatInt(v.M.Row.ID, 10),
				rails.NewHash("remote", true, "method", "delete", "data", rails.NewHash("confirm", l.L("text_are_you_sure")),
					"title", l.L("label_relation_delete"), "class", "icon-only icon-link-break"))
		}
		buttons += l.a.Helpers.LinkToContextMenu(l.page)
		subject := l.relationToS(rel, v.M.Row.ID, string(l.linkToIssue(other, redmine.LinkToIssueOptions{Project: cross})))
		row := rails.ContentTag("td", rails.CheckBoxTag("ids[]", other.ID, false, rails.NewHash("id", nil)), rails.NewHash("class", "checkbox")) +
			rails.ContentTag("td", template.HTML(subject), rails.NewHash("class", "subject"))
		for _, c := range cols {
			row += rails.ContentTag("td", l.columnContent(c, other), rails.NewHash("class", c.CSSClasses()))
		}
		row += rails.ContentTag("td", buttons, rails.NewHash("class", "buttons"))
		s += rails.ContentTag("tr", row, rails.NewHash("id", "relation-"+strconv.FormatInt(rel.ID, 10), "class", css))
	}
	return rails.ContentTag("table", s, rails.NewHash("class", "list issues odd-even"))
}

// RelationTypeChoices は collection_for_relation_type_select。
func (v *issueShowView) RelationTypeChoices() []any {
	var items []any
	for _, k := range []string{"relates", "duplicates", "duplicated", "blocks", "blocked", "precedes", "follows", "copied_to", "copied_from"} {
		items = append(items, []any{v.l.L(relationTypes[k].name), k})
	}
	return items
}

// NewRelation は @relation（show では IssueRelation.new、issue_relations#create.js では最後に保存を試みた関連）。
func (v *issueShowView) NewRelation() *relationFormModel {
	if v.relationForm != nil {
		return v.relationForm
	}
	return &relationFormModel{}
}

// relationFormModel は form_for @relation のモデル。
type relationFormModel struct {
	persisted    bool
	relationType string
	delay        any
}

func (r *relationFormModel) ParamKey() string { return "relation" }
func (r *relationFormModel) Persisted() bool  { return r.persisted }
func (r *relationFormModel) Send(method string) (any, bool) {
	switch method {
	case "relation_type":
		if r.relationType == "" {
			return "relates", true
		}
		return r.relationType, true
	case "delay":
		return r.delay, true
	case "issue_to_id":
		return nil, true
	}
	return nil, false
}

// AutoCompleteRelationURL は auto_complete_issues_path(:project_id => @project, :issue_id => @issue)。
func (v *issueShowView) AutoCompleteRelationURL() string {
	return "/issues/auto_complete?issue_id=" + strconv.FormatInt(v.M.Row.ID, 10) + "&project_id=" + url.QueryEscape(v.M.Project.Identifier)
}

// ---------------------------------------------------------------- アクションメニュー

// EditPath は edit_issue_path(@issue)。
func (v *issueShowView) EditPath() string {
	return "/issues/" + strconv.FormatInt(v.M.Row.ID, 10) + "/edit"
}

// WatcherLink は watcher_link(@issue, User.current)。
func (v *issueShowView) WatcherLink() template.HTML {
	l := v.l
	u := l.c.User
	if !u.Logged() {
		return ""
	}
	watched := slices.Contains(v.watcherIDs(), u.ID)
	css := "issue-" + strconv.FormatInt(v.M.Row.ID, 10) + "-watcher " + map[bool]string{true: "icon icon-fav", false: "icon icon-fav-off"}[watched]
	text, icon, method := l.L("button_watch"), "watch", "post"
	if watched {
		text, icon, method = l.L("button_unwatch"), "unwatch", "delete"
	}
	return rails.LinkTo(l.icon(icon, text), "/watchers/watch?object_id="+strconv.FormatInt(v.M.Row.ID, 10)+"&object_type=issue",
		rails.NewHash("remote", true, "method", method, "class", css))
}

// CanCopy は User.current.allowed_to?(:copy_issues, @project) && Issue.allowed_target_projects.any?。
func (v *issueShowView) CanCopy() bool {
	return v.M.allowedTo("copy_issues") && v.l.allowedTargetProjectsAny()
}

// CopyPath は project_copy_issue_path(@project, @issue)。
func (v *issueShowView) CopyPath() string {
	return "/projects/" + v.M.Project.Identifier + "/issues/" + strconv.FormatInt(v.M.Row.ID, 10) + "/copy"
}

// ActionsDropdown は _action_menu の actions_dropdown（リンクのコピー・削除）。
func (v *issueShowView) ActionsDropdown() template.HTML {
	l := v.l
	content := "\n  " + string(l.copyObjectURLLink(issueURL(l.c, v.M.Row.ID))) + "\n  "
	if v.M.Deletable() {
		content += string(rails.LinkTo(l.icon("del", RubyCapitalize(l.L("button_delete_object", i18n.Vars{"object_name": l.L("label_issue")}))),
			"/issues/"+strconv.FormatInt(v.M.Row.ID, 10),
			rails.NewHash("data", rails.NewHash("confirm", v.destroyConfirmation()), "method", "delete", "class", "icon icon-del ")))
	}
	content += "\n"
	return l.actionsDropdown(template.HTML(content))
}

// RubyCapitalize は String#capitalize。
func RubyCapitalize(s string) string { return helper.RubyCapitalize(s) }

// destroyConfirmation は issues_destroy_confirmation_message(@issue)。
func (v *issueShowView) destroyConfirmation() string {
	msg := v.l.L("text_issues_destroy_confirmation")
	if n := len(v.M.Descendants()); n > 0 {
		msg += "\n" + v.l.L("text_issues_destroy_descendants_confirmation", i18n.Vars{"count": n})
	}
	return msg
}

// watcherIDs は @issue.watcher_users の id。
func (v *issueShowView) watcherIDs() []int64 {
	if v.M.I == nil {
		return nil
	}
	ids, err := v.l.issuesEnv().WatcherIDs(v.l.ctx, v.M.I)
	v.l.fail(err)
	return ids
}

// ---------------------------------------------------------------- サイドバー（ウォッチャー）

// ShowWatchers は sidebar の #watchers の表示条件。
func (v *issueShowView) ShowWatchers() bool {
	return v.M.allowedTo("add_issue_watchers") || (len(v.watcherIDs()) > 0 && v.M.allowedTo("view_issue_watchers"))
}

// CanAddWatchers / CanViewWatchers は権限。
func (v *issueShowView) CanAddWatchers() bool  { return v.M.allowedTo("add_issue_watchers") }
func (v *issueShowView) CanViewWatchers() bool { return v.M.allowedTo("view_issue_watchers") }

// WatcherCount は watched.watcher_users.size。
func (v *issueShowView) WatcherCount() int { return len(v.watcherIDs()) }

// watcherItem は watchers_list の 1 件。
type watcherItem struct {
	User    *domain.User
	IsUser  bool
	Link    template.HTML
	Warning template.HTML
	Delete  template.HTML
}

// WatcherItems は watchers_list(@issue) の項目（User.sorted の順）。
func (v *issueShowView) WatcherItems() []watcherItem {
	l := v.l
	ids := v.watcherIDs()
	l.preloadPrincipals(ids)
	var users []*domain.User
	for _, id := range ids {
		if u := l.principal(id); u != nil {
			users = append(users, u)
		}
	}
	l.sortUsersByFormat(users)
	remove := v.M.allowedTo("delete_issue_watchers")
	var out []watcherItem
	for _, u := range users {
		it := watcherItem{User: u, IsUser: u.Kind.IsUser()}
		cls := "user"
		if !it.IsUser {
			cls = "group"
		}
		it.Link = l.a.Helpers.LinkToPrincipal(l.page, u, cls)
		if it.IsUser && !l.issueVisibleTo(v.M, u) {
			it.Warning = rails.ContentTag("span", l.icon("warning", l.L("notice_invalid_watcher")),
				rails.NewHash("class", "icon-only icon-warning", "title", l.L("notice_invalid_watcher")))
		}
		if remove {
			it.Delete = rails.LinkTo(l.icon("del", l.L("button_delete")),
				"/issues/"+strconv.FormatInt(v.M.Row.ID, 10)+"/watchers/"+strconv.FormatInt(u.ID, 10),
				rails.NewHash("remote", true, "method", "delete", "class", "delete icon-only icon-del", "title", l.L("button_delete")))
		}
		out = append(out, it)
	}
	return out
}

// issueVisibleTo は issue.visible?(user)（ウォッチャーの警告用）。
func (l *issueLookup) issueVisibleTo(m *issueModel, u *domain.User) bool {
	az := newAuthorizerFor(l, u)
	r := m.Row
	ok, err := az.IssueVisible(l.ctx, &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
		StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, m.Project)
	l.fail(err)
	return ok
}

// sortUsersByFormat は Principal.sorted（type DESC = ユーザーが先、Setting.user_format の並び → lastname → id。
// グループの firstname は空、lastname は名前）。
func (l *issueLookup) sortUsersByFormat(us []*domain.User) {
	f := l.a.Settings.String("user_format")
	key := func(u *domain.User) []string {
		first, last, login := u.Firstname, u.Lastname, u.Login
		if u.Kind.IsGroup() {
			first, login = "", ""
			if u.Principal.Name != "" {
				last = u.Principal.Name
			}
		}
		switch f {
		case "lastname_firstname", "lastname_comma_firstname", "lastnamefirstname":
			return []string{last, first}
		case "lastname":
			return []string{last}
		case "firstname":
			return []string{first, last}
		case "username":
			return []string{login, last}
		}
		return []string{first, last}
	}
	slices.SortStableFunc(us, func(a, b *domain.User) int {
		if ga, gb := a.Kind.IsGroup(), b.Kind.IsGroup(); ga != gb {
			if ga {
				return 1
			}
			return -1
		}
		ka, kb := key(a), key(b)
		for i := range ka {
			if c := strings.Compare(ka[i], kb[i]); c != 0 {
				return c
			}
		}
		return int(a.ID - b.ID)
	})
}

// ---------------------------------------------------------------- 履歴タブ

// HistoryTabs は issue_history_tabs。
func (v *issueShowView) HistoryTabs() []helper.Tab {
	l := v.l
	var tabs []helper.Tab
	id := strconv.FormatInt(v.M.Row.ID, 10)
	if len(v.Journals) > 0 {
		hasDetails, hasNotes := false, false
		for _, j := range v.Journals {
			if len(j.Details) > 0 {
				hasDetails = true
			}
			if j.HasNotes() {
				hasNotes = true
			}
		}
		tabs = append(tabs, helper.Tab{Name: "history", Label: "label_history", Onclick: `showIssueHistory("history", this.href)`,
			Partial: "issues/tabs/history", Locals: map[string]any{"V": v}})
		if hasNotes {
			tabs = append(tabs, helper.Tab{Name: "notes", Label: "label_issue_history_notes", Onclick: `showIssueHistory("notes", this.href)`})
		}
		if hasDetails {
			tabs = append(tabs, helper.Tab{Name: "properties", Label: "label_issue_history_properties", Onclick: `showIssueHistory("properties", this.href)`})
		}
	}
	if v.CanViewTime && v.M.SpentHours() > 0 {
		tabs = append(tabs, helper.Tab{Name: "time_entries", Label: "label_time_entry_plural", Remote: true,
			Onclick: "getRemoteTab('time_entries', '/issues/" + id + "/tab/time_entries', '/issues/" + id + "?tab=time_entries')"})
	}
	if v.HasChangesets {
		tabs = append(tabs, helper.Tab{Name: "changesets", Label: "label_associated_revisions", Remote: true,
			Onclick: "getRemoteTab('changesets', '/issues/" + id + "/tab/changesets', '/issues/" + id + "?tab=changesets')"})
	}
	_ = l
	return tabs
}

// HistoryDefaultTab は issue_history_default_tab。
func (v *issueShowView) HistoryDefaultTab() string {
	c := v.l.c
	if t := c.Params().String("tab"); rails.IsPresent(t) {
		return t
	}
	switch t := c.Pref().HistoryDefaultTab; t {
	case "last_tab_visited":
		if ck, err := c.R.Cookie("history_last_tab"); err == nil && rails.IsPresent(ck.Value) {
			return ck.Value
		}
		return "notes"
	case "":
		// NULL（nil）は render_tabs で先頭のタブになる。buropher は NULL と '' を区別しない
		return ""
	default:
		return t
	}
}

// ReplyLinks は issue.notes_addable?（tabs/_history の reply_links）。
func (v *issueShowView) ReplyLinks() bool { return v.M.NotesAddable() }

// ThumbnailsEnabled は Setting.thumbnails_enabled?。
func (v *issueShowView) ThumbnailsEnabled() bool { return v.l.a.Settings.Bool("thumbnails_enabled") }

// JournalThumbnails は journal_thumbnail_attachments(journal) のサムネイル。
func (v *issueShowView) JournalThumbnails(j *journalView) []template.HTML {
	var out []template.HTML
	for _, a := range j.Attachments() {
		if attachmentThumbnailable(a) {
			out = append(out, v.l.thumbnailTag(a))
		}
	}
	return out
}

// CanEditNotes は edit_issue_notes / edit_own_issue_notes（heads_for_wiki_formatter の条件）。
func (v *issueShowView) CanEditNotes() bool {
	return v.M.allowedTo("edit_issue_notes") || v.M.allowedTo("edit_own_issue_notes")
}

// ---------------------------------------------------------------- その他

// AtomURL は auto_discovery_link_tag(:atom, {:format => 'atom', :key => User.current.atom_key})。
func (v *issueShowView) AtomURL() string {
	u := httpx.RequestBaseURL(v.l.c.R) + "/issues/" + strconv.FormatInt(v.M.Row.ID, 10) + ".atom"
	if v.AtomKey != "" {
		u += "?key=" + v.AtomKey
	}
	return u
}

// AtomLinkURL は other_formats_links の Atom（:url => {:key => User.current.atom_key}）。
func (v *issueShowView) AtomLinkURL() string {
	u := "/issues/" + strconv.FormatInt(v.M.Row.ID, 10) + ".atom"
	if v.AtomKey != "" {
		u += "?key=" + v.AtomKey
	}
	return u
}

// AtomTitle は "#{@issue.project} - #{@issue.tracker} ##{@issue.id}: #{@issue.subject}"。
func (v *issueShowView) AtomTitle() string {
	return v.M.Project.Name + " - " + v.M.Heading() + ": " + v.M.Row.Subject
}

// PrevPath / NextPath は issue_path(@prev_issue_id) / issue_path(@next_issue_id)。
func (v *issueShowView) PrevPath() any {
	if v.PrevIssueID == nil {
		return nil
	}
	return "/issues/" + rails.ToS(v.PrevIssueID)
}

func (v *issueShowView) NextPath() any {
	if v.NextIssueID == nil {
		return nil
	}
	return "/issues/" + rails.ToS(v.NextIssueID)
}

// PositionLabel は l(:label_item_position, :position => @issue_position, :count => @issue_count)。
func (v *issueShowView) PositionLabel() string {
	return v.l.L("label_item_position", i18n.Vars{"position": v.IssuePosition, "count": v.IssueCount})
}

// PrevTitle / NextTitle は "##{@prev_issue_id}" / "##{@next_issue_id}"。
func (v *issueShowView) PrevTitle() string {
	if v.PrevIssueID == nil {
		return "#"
	}
	return "#" + rails.ToS(v.PrevIssueID)
}

func (v *issueShowView) NextTitle() string {
	if v.NextIssueID == nil {
		return "#"
	}
	return "#" + rails.ToS(v.NextIssueID)
}

// ReverseComments は User.current.wants_comments_in_reverse_order?。
func (v *issueShowView) ReverseComments() bool { return v.l.c.Pref().CommentsSorting == "desc" }
