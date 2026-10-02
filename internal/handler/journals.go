package handler

// JournalsController（app/controllers/journals_controller.rb）の参照系: index（Atom）と diff。
// new / edit / update は書き込み系の移植で追加する。

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textdiff"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

// JournalsController（menu_item :issues）。
var JournalsController = &Controller{Name: "journals", MainMenu: true, MenuItem: func(string) string { return "issues" }}

// routesJournals は journals コントローラ（参照系）のルートを登録する。
//
//	match '/issues/changes', :to => 'journals#index', :as => 'issue_changes', :via => :get
//	resources :journals, :only => [:edit, :update] do member { get 'diff' } end
func (a *App) routesJournals(r Router) {
	// before_action :find_optional_project, :only => [:index]
	// before_action :find_journal, :only => [:edit, :update, :diff]
	// before_action :authorize, :only => [:new, :edit, :update, :diff]
	// accept_atom_auth :index
	a.Handle(r, http.MethodGet, "/issues/changes", JournalsController, "index", a.JournalsIndex,
		FindOptionalProject(), AcceptAtomAuth())
	a.Handle(r, http.MethodGet, "/journals/{id}/diff", JournalsController, "diff", a.JournalsDiff,
		Before(a.findJournal), Authorize())
}

const ctxJournal = "journal"

// findJournal は JournalsController#find_journal（Journal.visible.find(params[:id])）。
func (a *App) findJournal(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	j, err := repository.GetIssueJournal(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find journal", err)
		}
		return
	}
	r, err := repository.ReadIssue(c.Ctx(), a.DB, j.IssueID)
	if err != nil {
		a.internalError(c, "find journal issue", err)
		return
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, r.ProjectID)
	if err != nil {
		a.internalError(c, "find journal project", err)
		return
	}
	// Journal.visible: チケットが見え、非公開ノートは view_private_notes か自分のもの
	vis, err := c.Authz().IssueVisible(c.Ctx(), &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
		StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, p)
	if err != nil {
		a.internalError(c, "journal visible", err)
		return
	}
	if vis && j.PrivateNotes && !(c.User.Logged() && j.UserID == c.User.ID) {
		vis = c.AllowedTo(domain.Perm("view_private_notes"), p)
	}
	if !vis {
		c.Render404("")
		return
	}
	c.Project = p
	c.setLocal(ctxJournal, j)
	c.setLocal(ctxIssue, issueRowFromRead(r))
}

// JournalsIndex は JournalsController#index（Atom）。
func (a *App) JournalsIndex(c *Req) {
	q, ok := a.retrieveIssueQuery(c, true)
	if !ok {
		return
	}
	valid, err := q.Valid(c.Ctx())
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	l := a.newIssueLookup(c)
	var journals []*journalView
	if valid {
		// 同時刻のジャーナルの順序は DB 任せ（参照の SQLite では id の降順になる）なので id DESC で揃える
		ids, err := q.JournalIDs(c.Ctx(), query.ListOptions{Order: []string{"issue_journals.created_at DESC", "issue_journals.id DESC"}, Limit: 25})
		if err != nil {
			a.queryFailed(c, err)
			return
		}
		journals = l.journalViewsByIDs(ids)
	}
	title := a.Settings.String("app_title")
	if c.Project != nil {
		title = c.Project.Name
	}
	if q.ID == 0 {
		title += ": " + c.L("label_changes_details")
	} else {
		title += ": " + q.Name
	}
	self := httpx.RequestBaseURL(c.R) + urlroot.Path("/issues/changes.atom")
	if k := c.AtomKey(); k != "" {
		self += "?key=" + k
	}
	if l.err != nil {
		a.internalError(c, "journals atom", l.err)
		return
	}
	a.renderJournalsAtom(c, l, &title, self, journals)
}

// journalViewsByIDs は id のジャーナル（ids の順）を表示用にする。
func (l *issueLookup) journalViewsByIDs(ids []int64) []*journalView {
	e := l.issuesEnv()
	models := map[int64]*issueModel{}
	var out []*journalView
	for _, id := range ids {
		j, err := e.FindJournal(l.ctx, id)
		if err != nil || j == nil {
			l.fail(err)
			continue
		}
		m := models[j.IssueID]
		if m == nil {
			r := l.issue(j.IssueID)
			if r == nil {
				continue
			}
			m = l.model(r)
			models[j.IssueID] = m
		}
		out = append(out, &journalView{Journal: j, l: l, issue: m, User: l.principal(j.UserID)})
	}
	return out
}

// JournalsDiff は JournalsController#diff。
func (a *App) JournalsDiff(c *Req) {
	j, _ := c.local(ctxJournal).(*repository.IssueJournal)
	issue := c.currentIssue()
	l := a.newIssueLookup(c)
	var detail *repository.IssueJournalDetail
	if s := c.Params().String("detail_id"); s != "" {
		id := customfield.RubyToI(s)
		for _, d := range j.Details {
			if d.ID == id {
				detail = d
			}
		}
	} else {
		for _, d := range j.Details {
			if d.Property == "attr" && d.PropKey == "description" {
				detail = d
				break
			}
		}
	}
	if issue == nil || detail == nil {
		c.Render404("")
		return
	}
	if detail.Property == "cf" {
		cf := l.customField(customfield.RubyToI(detail.PropKey))
		if cf == nil || !l.cfVisibleBy(cf, c.Project) {
			c.DenyAccess()
			return
		}
	}
	m := l.model(issue)
	diff := textdiff.ToHTML(strPtrValue(detail.Value), strPtrValue(detail.OldValue))
	data := map[string]any{
		"Heading": m.Tracker.Name + " #" + strconv.FormatInt(issue.ID, 10),
		"Journal": j, "User": l.principal(j.UserID), "Diff": template.HTML(diff),
		"IssuePath": "/issues/" + strconv.FormatInt(issue.ID, 10),
		"Title":     m.Tracker.Name + " #" + strconv.FormatInt(issue.ID, 10) + ": " + issue.Subject,
	}
	if l.err != nil {
		a.internalError(c, "journal diff", l.err)
		return
	}
	c.Render("journals/diff", data)
}

var _ = issues.ErrNotFound
