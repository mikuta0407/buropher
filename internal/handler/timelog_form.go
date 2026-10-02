package handler

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは timelog#new / create / edit / update（timelog/_form, new, edit, new.js, edit.js）の移植。

// teForm は labelled_form_for @time_entry のモデル。
type teForm struct {
	E   *timelog.Entry
	loc func(attr string) string
}

func (f *teForm) ParamKey() string { return "time_entry" }
func (f *teForm) Persisted() bool  { return !f.E.NewRecord() }
func (f *teForm) ToParam() string  { return strconv.FormatInt(f.E.ID, 10) }
func (f *teForm) ErrorsOn(attr string) []string {
	var out []string
	for _, x := range f.E.Errors.List() {
		if x.Attr == attr {
			out = append(out, x.Key+x.Message)
		}
	}
	return out
}
func (f *teForm) HumanAttributeName(attr string) string { return f.loc(attr) }

// Send は属性の値（FormBuilder が参照する）。
func (f *teForm) Send(method string) (any, bool) {
	e := f.E
	id := func(p *int64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	switch method {
	case "project_id":
		return id(e.ProjectID), true
	case "issue_id":
		return id(e.IssueID), true
	case "user_id":
		return id(e.UserID), true
	case "activity_id":
		return id(e.ActivityID), true
	case "comments":
		if e.Comments == nil {
			return nil, true
		}
		return *e.Comments, true
	case "issue_id_before_type_cast":
		if e.IssueIDBeforeTypeCast != nil {
			return e.IssueIDBeforeTypeCast, true
		}
		return id(e.IssueID), true
	case "spent_on":
		// date_field はキャスト後の値（解釈できなければ nil）
		if e.SpentOn == nil {
			return nil, true
		}
		return e.SpentOn.Format("2006-01-02"), true
	case "hours":
		if e.HoursBeforeTypeCast != nil {
			return e.HoursBeforeTypeCast, true
		}
		if e.Hours == nil {
			return nil, true
		}
		return *e.Hours, true
	}
	return nil, false
}

// teFormData は timelog/_form の表示データ。
type teFormData struct {
	Form                  *teForm
	Entry                 *timelog.Entry
	Errors                []string
	New                   bool
	URL                   string
	FormJS                string
	HiddenProjectID       any
	HiddenIssueID         any
	ProjectOptions        template.HTML
	IssueLink             template.HTML
	ShowUserSelect        bool
	UserOptions           template.HTML
	UserLink              template.HTML
	HoursValue            any
	HoursHasError         bool
	ActivityChoices       [][]any
	ActivitySelected      any
	CustomValues          []*domain.CustomFieldValue
	IssueRequired         bool
	CommentsRequired      bool
	AutocompleteProjectID string
	CancelURL             string
}

// teBuildFormData は _form に必要な値を組み立てる。
func (a *App) teBuildFormData(c *Req, t *timelog.Entry) (*teFormData, error) {
	ctx := c.Ctx()
	env := a.teEnv(c)
	p := c.Params()
	d := &teFormData{Entry: t, New: t.NewRecord(), Form: &teForm{E: t, loc: func(attr string) string { return t.Errors.HumanAttributeName(c.Loc, attr) }}}
	d.Errors = t.Errors.FullMessages(c.Loc)
	if d.New {
		d.URL = "/time_entries"
		d.FormJS = "/time_entries/new.js"
	} else {
		d.URL = "/time_entries/" + strconv.FormatInt(t.ID, 10)
		d.FormJS = "/time_entries/" + strconv.FormatInt(t.ID, 10) + "/edit.js"
	}
	switch {
	case d.New && p.Has("project_id"):
		d.HiddenProjectID = p.String("project_id")
	case d.New && p.Has("issue_id"):
		d.HiddenIssueID = p.String("issue_id")
	default:
		opts, err := a.teProjectTreeOptions(c, t.ProjectID)
		if err != nil {
			return nil, err
		}
		d.ProjectOptions = opts
	}
	link, err := a.teIssueLink(c, t.IssueID)
	if err != nil {
		return nil, err
	}
	d.IssueLink = link
	if c.AllowedTo(domain.Perm("log_time_for_other_users"), c.Project) {
		d.ShowUserSelect = true
		users, err := env.AssignableUsers(ctx, t)
		if err != nil {
			return nil, err
		}
		if t.UserID != nil && !slices.ContainsFunc(users, func(u *domain.User) bool { return u.ID == *t.UserID }) {
			if u, err := repository.GetUser(ctx, a.DB, *t.UserID); err == nil {
				users = append(users, u)
			}
		}
		sel := ""
		if t.UserID != nil {
			sel = strconv.FormatInt(*t.UserID, 10)
		}
		involved, err := a.teInvolvedPrincipals(c)
		if err != nil {
			return nil, err
		}
		d.UserOptions = tePrincipalsOptions(c, users, sel, involved)
	} else if !d.New && t.UserID != nil {
		if u, err := repository.GetUser(ctx, a.DB, *t.UserID); err == nil {
			d.UserLink = a.Helpers.LinkToPrincipal(c.Page(), u, "")
		}
	}
	d.HoursHasError = len(d.Form.ErrorsOn("hours")) > 0
	if !d.HoursHasError {
		if h := t.RoundedHours(); h != nil {
			d.HoursValue = c.Loc.FormatHours(*h)
		} else {
			d.HoursValue = ""
		}
	}
	project, err := env.Project(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		project = c.Project
	}
	if d.ActivityChoices, err = a.teActivityChoices(c, t, project); err != nil {
		return nil, err
	}
	if t.ActivityID != nil {
		d.ActivitySelected = *t.ActivityID
	}
	if d.CustomValues, err = env.VisibleCustomFieldValues(ctx, t, c.User); err != nil {
		return nil, err
	}
	req := a.Settings.Strings("timelog_required_fields")
	d.IssueRequired = slices.Contains(req, "issue_id")
	d.CommentsRequired = slices.Contains(req, "comments")
	if d.New && c.Project != nil {
		d.AutocompleteProjectID = strconv.FormatInt(c.Project.ID, 10)
	}
	d.CancelURL = teCancelURL(c)
	return d, nil
}

// teCancelURL は cancel_button_tag_for_time_entry(@project) の URL。
func teCancelURL(c *Req) string {
	back := c.Params().String("back_url")
	if back == "" {
		if ref := c.R.Header.Get("Referer"); ref != "" {
			if u, err := url.QueryUnescape(ref); err == nil {
				back = u
			} else {
				back = ref
			}
		}
	}
	if u, ok := httpx.ValidateBackURL(c.R, back, ""); ok && u != "" {
		return u
	}
	return teTimeEntriesPath(c.Project)
}

// teIssueLink は link_to_issue(@time_entry.issue) if @time_entry.issue.try(:visible?)。
func (a *App) teIssueLink(c *Req, issueID *int64) (template.HTML, error) {
	if issueID == nil {
		return "", nil
	}
	cond, err := c.Authz().IssueVisibleCondition(c.Ctx(), authz.ConditionOptions{})
	if err != nil {
		return "", err
	}
	is, err := repository.TimelogRefIssues(c.Ctx(), a.DB, []int64{*issueID}, cond)
	if err != nil || is[*issueID] == nil {
		return "", err
	}
	return a.Helpers.WikiRenderer(c.Page()).LinkToIssue(is[*issueID], redmine.LinkToIssueOptions{}), nil
}

// teActivityChoices は activity_collection_for_select_options(time_entry, project)。
func (a *App) teActivityChoices(c *Req, t *timelog.Entry, project *domain.Project) ([][]any, error) {
	env := a.teEnv(c)
	acts, err := env.AvailableActivities(c.Ctx(), project)
	if err != nil {
		return nil, err
	}
	blank := []any{"--- " + c.L("actionview_instancetag_blank_option") + " ---", ""}
	var out [][]any
	var cur *domain.Enumeration
	if t != nil && t.ActivityID != nil {
		if cur, err = repository.TimelogActivityByID(c.Ctx(), a.DB, *t.ActivityID); err != nil {
			return nil, err
		}
	}
	if cur != nil && !cur.Active {
		out = append(out, blank)
	} else if !slices.ContainsFunc(acts, func(e *domain.Enumeration) bool { return e.IsDefault }) {
		out = append(out, blank)
	}
	for _, e := range acts {
		out = append(out, []any{e.Name, e.ID})
	}
	return out, nil
}

// teProjectTreeOptions は project_tree_options_for_select(Project.allowed_to(:log_time), selected:, include_blank: true)。
func (a *App) teProjectTreeOptions(c *Req, selected *int64) (template.HTML, error) {
	ctx := c.Ctx()
	cond, err := c.Authz().AllowedToCondition(ctx, "log_time", authz.ConditionOptions{}, nil)
	if err != nil {
		return "", err
	}
	projects, err := repository.LoadProjects(ctx, a.DB, cond)
	if err != nil {
		return "", err
	}
	ns, err := repository.ProjectNestedSet(ctx, a.DB)
	if err != nil {
		return "", err
	}
	sort.SliceStable(projects, func(i, j int) bool { return ns[projects[i].ID].Lft < ns[projects[j].ID].Lft })
	var b strings.Builder
	b.WriteString(string(rails.ContentTag("option", template.HTML("&nbsp;"), rails.NewHash("value", ""))))
	var stack []repository.NestedSetValue
	for _, p := range projects {
		v := ns[p.ID]
		for len(stack) > 0 && !(stack[len(stack)-1].Lft < v.Lft && v.Rgt < stack[len(stack)-1].Rgt) {
			stack = stack[:len(stack)-1]
		}
		prefix := ""
		if n := len(stack); n > 0 {
			prefix = strings.Repeat("&nbsp;", 2*n) + "&#187; "
		}
		var sel any
		if selected != nil && *selected == p.ID {
			sel = "selected"
		}
		b.WriteString(string(rails.ContentTag("option", template.HTML(prefix)+rails.H(p.Name), rails.NewHash("value", p.ID, "selected", sel))))
		stack = append(stack, v)
	}
	return template.HTML(b.String()), nil
}

// tePrincipalsOptions は principals_options_for_select(collection, selected)。
func tePrincipalsOptions(c *Req, users []*domain.User, selected string, involved []*domain.User) template.HTML {
	page := c.Page()
	var b strings.Builder
	if c.User.Logged() && slices.ContainsFunc(users, func(u *domain.User) bool { return u.ID == c.User.ID }) {
		b.WriteString(string(rails.ContentTag("option", "<< "+c.L("label_me")+" >>", rails.NewHash("value", c.User.ID))))
	}
	sorted := slices.Clone(users)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.ToLower(helper.PrincipalName(page, sorted[i])) < strings.ToLower(helper.PrincipalName(page, sorted[j]))
	})
	var involvedHTML strings.Builder
	for _, p := range involved {
		disabled := !slices.ContainsFunc(users, func(u *domain.User) bool { return u.ID == p.ID })
		involvedHTML.WriteString(string(rails.ContentTag("option", helper.PrincipalName(page, p), rails.NewHash("value", p.ID, "disabled", disabled))))
	}
	var usersHTML strings.Builder
	for _, u := range sorted {
		sel := ""
		if strconv.FormatInt(u.ID, 10) == selected {
			sel = ` selected="selected"`
		}
		usersHTML.WriteString(`<option value="` + strconv.FormatInt(u.ID, 10) + `"` + sel + `>` + string(rails.H(helper.PrincipalName(page, u))) + `</option>`)
	}
	if involvedHTML.Len() == 0 {
		b.WriteString(usersHTML.String())
	} else {
		b.WriteString(`<optgroup label="` + string(rails.H(c.L("label_involved_principals"))) + `">` + involvedHTML.String() + `</optgroup>`)
		if usersHTML.Len() > 0 {
			b.WriteString(`<optgroup label="` + string(rails.H(c.L("label_user_plural"))) + `">` + usersHTML.String() + `</optgroup>`)
		}
	}
	return template.HTML(b.String())
}

// teInvolvedPrincipals は @issue（チケット配下の new / create のみ）の [author, prior_assigned_to].uniq.compact。
func (a *App) teInvolvedPrincipals(c *Req) ([]*domain.User, error) {
	iid := teIssueID(c)
	if iid == nil {
		return nil, nil
	}
	ctx := c.Ctx()
	iss, err := a.teEnv(c).Issues().Find(ctx, *iid)
	if err != nil || iss == nil {
		return nil, err
	}
	ids := []int64{iss.AuthorID}
	prior, err := repository.TimelogPriorAssignedToID(ctx, a.DB, iss.ID)
	if err != nil {
		return nil, err
	}
	if prior != nil && *prior != iss.AuthorID {
		ids = append(ids, *prior)
	}
	var out []*domain.User
	for _, id := range ids {
		u, err := repository.GetUser(ctx, a.DB, id)
		if err != nil {
			// グループ・削除済みは Principal として名前だけ使う
			p, perr := repository.GetPrincipal(ctx, a.DB, id)
			if perr != nil {
				continue
			}
			out = append(out, &domain.User{Principal: *p})
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// ---------------------------------------------------------------- actions

// teNewEntry は TimeEntry.new(:project => @project, :issue => @issue, :author => User.current, :spent_on => today)。
func (a *App) teNewEntry(c *Req) (*timelog.Entry, error) {
	return a.teEnv(c).New(c.Ctx(), c.Project, teIssueID(c))
}

// TimelogNew は timelog#new（GET は html、POST /time_entries/new.js はフォームの更新）。
func (a *App) TimelogNew(c *Req) {
	t, err := a.teNewEntry(c)
	if err != nil {
		a.internalError(c, "new time entry", err)
		return
	}
	if err := a.teEnv(c).SafeAssign(c.Ctx(), t, c.Params().Map("time_entry"), c.User); err != nil {
		a.internalError(c, "assign time entry", err)
		return
	}
	switch teFormat(c, "html", "js") {
	case "js":
		a.teRenderFormJS(c, t, true)
	case "html":
		a.teRenderForm(c, t, "timelog/new", 0)
	default:
		c.head(http.StatusNotAcceptable)
	}
}

// teRenderForm は new / edit を描画する。
func (a *App) teRenderForm(c *Req, t *timelog.Entry, tmpl string, status int) {
	d, err := a.teBuildFormData(c, t)
	if err != nil {
		a.internalError(c, "time entry form", err)
		return
	}
	opts := RenderOptions{Status: status}
	c.Render(tmpl, map[string]any{"F": d}, opts)
}

// teRenderFormJS は new.js / edit.js。
func (a *App) teRenderFormJS(c *Req, t *timelog.Entry, isNew bool) {
	ctx := c.Ctx()
	env := a.teEnv(c)
	project, err := env.Project(ctx, t.ProjectID)
	if err != nil {
		a.internalError(c, "time entry project", err)
		return
	}
	if project == nil {
		project = c.Project
	}
	choices, err := a.teActivityChoices(c, t, project)
	if err != nil {
		a.internalError(c, "activities", err)
		return
	}
	var selected any
	if isNew {
		// default_activity(time_entry)
		if c.Project != nil {
			if t.ActivityID != nil {
				selected = *t.ActivityID
			}
		} else {
			id, err := env.DefaultActivityID(ctx, c.User, project)
			if err != nil {
				a.internalError(c, "default activity", err)
				return
			}
			if id != nil {
				selected = *id
			}
		}
	} else if t.ActivityID != nil {
		selected = *t.ActivityID
	}
	link, err := a.teIssueLink(c, t.IssueID)
	if err != nil {
		a.internalError(c, "issue link", err)
		return
	}
	c.Render("timelog/new", map[string]any{"Choices": choices, "Selected": selected, "IssueLink": link},
		RenderOptions{Format: "js", Layout: view.NoLayout})
}

// TimelogCreate は timelog#create。
func (a *App) TimelogCreate(c *Req) {
	ctx := c.Ctx()
	t, err := a.teNewEntry(c)
	if err != nil {
		a.internalError(c, "new time entry", err)
		return
	}
	t.SetUser(c.User)
	env := a.teEnv(c)
	attrs := c.Params().Map("time_entry")
	if err := env.SafeAssign(ctx, t, attrs, c.User); err != nil {
		a.internalError(c, "assign time entry", err)
		return
	}
	project, err := env.Project(ctx, t.ProjectID)
	if err != nil {
		a.internalError(c, "time entry project", err)
		return
	}
	if project != nil && !c.AllowedTo(domain.Perm("log_time"), project) {
		c.Render403("")
		return
	}
	var saved bool
	err = a.DB.WithTx(ctx, func(tx *db.Tx) error {
		env := env.WithQ(tx)
		var err error
		saved, err = env.Save(ctx, t)
		return err
	})
	if err != nil {
		a.internalError(c, "save time entry", err)
		return
	}
	api := httpx.IsAPIRequest(c.R)
	if !saved {
		if api {
			c.RenderValidationErrors(t.Errors)
			return
		}
		a.teRenderForm(c, t, "timelog/new", 0)
		return
	}
	if api {
		c.W.Header().Set("Location", httpx.RequestBaseURL(c.R)+urlroot.Path("/time_entries/"+strconv.FormatInt(t.ID, 10)))
		a.teRenderShowAPI(c, t, http.StatusCreated, true)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_create"))
	p := c.Params()
	if p.Present("continue") {
		q := rails.NewHash()
		te := rails.NewHash()
		te.Set("project_id", p.String("time_entry", "project_id"))
		if t.IssueID != nil {
			te.Set("issue_id", *t.IssueID)
		} else {
			te.Set("issue_id", "")
		}
		te.Set("spent_on", t.SpentOn.Format("2006-01-02"))
		if t.ActivityID != nil {
			te.Set("activity_id", *t.ActivityID)
		} else {
			te.Set("activity_id", "")
		}
		var path string
		switch {
		case p.Present("project_id") && project != nil:
			if !p.Present("time_entry", "project_id") {
				te.Set("project_id", project.ID)
			}
			path = "/projects/" + project.Identifier + "/time_entries/new"
		case p.Present("issue_id") && t.IssueID != nil:
			path = "/issues/" + strconv.FormatInt(*t.IssueID, 10) + "/time_entries/new"
		default:
			path = "/time_entries/new"
		}
		q.Set("time_entry", te)
		if bu := p.String("back_url"); p.Has("back_url") {
			q.Set("back_url", bu)
		}
		c.Redirect(helper.URLWithQuery(path, q))
		return
	}
	c.RedirectBackOrDefault(teTimeEntriesPath(project), false)
}

// TimelogEdit は timelog#edit（PATCH /time_entries/:id/edit.js はフォームの更新）。
func (a *App) TimelogEdit(c *Req) {
	t := c.value(teEntryKey{}).(*timelog.Entry)
	if err := a.teEnv(c).SafeAssign(c.Ctx(), t, c.Params().Map("time_entry"), c.User); err != nil {
		a.internalError(c, "assign time entry", err)
		return
	}
	switch teFormat(c, "html", "js") {
	case "js":
		a.teRenderFormJS(c, t, false)
	case "html":
		a.teRenderForm(c, t, "timelog/edit", 0)
	default:
		c.head(http.StatusNotAcceptable)
	}
}

// TimelogUpdate は timelog#update。
func (a *App) TimelogUpdate(c *Req) {
	ctx := c.Ctx()
	t := c.value(teEntryKey{}).(*timelog.Entry)
	env := a.teEnv(c)
	if err := env.SafeAssign(ctx, t, c.Params().Map("time_entry"), c.User); err != nil {
		a.internalError(c, "assign time entry", err)
		return
	}
	var saved bool
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		env := env.WithQ(tx)
		var err error
		saved, err = env.Save(ctx, t)
		return err
	})
	if err != nil {
		a.internalError(c, "save time entry", err)
		return
	}
	api := httpx.IsAPIRequest(c.R)
	if !saved {
		if api {
			c.RenderValidationErrors(t.Errors)
			return
		}
		a.teRenderForm(c, t, "timelog/edit", 0)
		return
	}
	if api {
		c.RenderAPIOK()
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	project, _ := env.Project(ctx, t.ProjectID)
	c.RedirectBackOrDefault(teTimeEntriesPath(project), false)
}
