package handler

// JournalsController の書き込み系: new（引用返信）・edit（注記の編集フォーム）・update（注記の更新・削除）。
//
//	match '/issues/:id/quoted', :to => 'journals#new', :id => /\d+/, :via => :post, :as => 'quoted_issue'
//	resources :journals, :only => [:edit, :update]

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// routesJournalsWrite は journals#new / edit / update のルートを登録する。
func (a *App) routesJournalsWrite(r Router) {
	// before_action :find_journal, :only => [:edit, :update, :diff]
	// before_action :find_issue, :only => [:new]
	// before_action :authorize, :only => [:new, :edit, :update, :diff]
	// accept_api_auth :update
	a.Handle(r, http.MethodPost, "/issues/{id}/quoted", JournalsController, "new", a.JournalsNew,
		Before(a.findIssue), Authorize())
	a.Handle(r, http.MethodGet, "/journals/{id}/edit", JournalsController, "edit", a.JournalsEdit,
		Before(a.findJournal), Authorize())
	for _, m := range []string{http.MethodPut, http.MethodPatch} {
		a.Handle(r, m, "/journals/{id}", JournalsController, "update", a.JournalsUpdate,
			Before(a.findJournal), Authorize(), AcceptAPIAuth())
	}
}

// visibleJournalByID は Journal.visible.find(id)（チケットが見え、非公開ノートは view_private_notes か自分のもの）。
// 見えなければ nil。
func (a *App) visibleJournalByID(c *Req, id int64) (*repository.IssueJournal, error) {
	j, err := repository.GetIssueJournal(c.Ctx(), a.DB, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := repository.ReadIssue(c.Ctx(), a.DB, j.IssueID)
	if err != nil {
		return nil, err
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, r.ProjectID)
	if err != nil {
		return nil, err
	}
	vis, err := c.Authz().IssueVisible(c.Ctx(), &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
		StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, p)
	if err != nil {
		return nil, err
	}
	if vis && j.PrivateNotes && !(c.User.Logged() && j.UserID == c.User.ID) {
		vis = c.AllowedTo(domain.Perm("view_private_notes"), p)
	}
	if !vis {
		return nil, nil
	}
	return j, nil
}

// JournalsNew は JournalsController#new（POST /issues/:id/quoted。new.js.erb で注記欄に引用を入れる）。
func (a *App) JournalsNew(c *Req) {
	issue := c.currentIssue()
	p := c.Params()
	l := a.newIssueLookup(c)
	q := redmine.QuoteBuilder{DefaultLanguage: a.Settings.String("default_language")}
	if c.Loc != nil {
		q.Bundle = c.Loc.Bundle
	}
	quote := p.String("quote")
	var content string
	private := false
	if p.Has("journal_id") {
		id, ok := p.IntStrict("journal_id")
		var j *repository.IssueJournal
		if ok {
			var err error
			if j, err = a.visibleJournalByID(c, id); err != nil {
				a.internalError(c, "quoted journal", err)
				return
			}
		}
		if j == nil {
			c.Render404("")
			return
		}
		private = j.PrivateNotes
		content = q.QuoteIssueJournal(l.principalName(l.principal(j.UserID)), j.NotesString(), p.String("journal_indice"), quote)
	} else {
		content = q.QuoteIssue(l.principalName(l.principal(issue.AuthorID)), issue.Description, quote)
	}
	if l.err != nil {
		a.internalError(c, "quoted", l.err)
		return
	}
	// new.js.erb のみ（それ以外の形式はテンプレートが無い: 406）
	if httpx.Negotiate(c.R, "js") != "js" {
		c.unknownFormat()
		return
	}
	c.Render("journals/new", map[string]any{"Content": content, "PrivateJournal": private},
		RenderOptions{Format: "js", Layout: view.NoLayout})
}

// journalNotesForm は journals/_notes_form のデータ。
type journalNotesForm struct {
	Journal       *repository.IssueJournal
	Rows          int
	Data          *rails.Hash
	ShowPrivate   bool
	PreviewURL    string
	CancelOnclick string
}

// JournalsEdit は JournalsController#edit（GET /journals/:id/edit.js）。
func (a *App) JournalsEdit(c *Req) {
	j, _ := c.local(ctxJournal).(*repository.IssueJournal)
	if !a.journalEditable(c, j) {
		if !c.Halted() {
			c.Render403("")
		}
		return
	}
	// respond_to { format.js }
	if httpx.Negotiate(c.R, "js") != "js" {
		c.unknownFormat()
		return
	}
	l := a.newIssueLookup(c)
	notes := j.NotesString()
	rows := 10
	if rails.IsPresent(notes) {
		rows = min(max(10, utf8.RuneCountInString(notes)/50), 100)
	}
	id := strconv.FormatInt(j.ID, 10)
	f := &journalNotesForm{Journal: j, Rows: rows, ShowPrivate: c.AllowedTo(domain.Perm("set_notes_private"), c.Project),
		PreviewURL:    "/issues/preview?" + "issue_id=" + strconv.FormatInt(j.IssueID, 10) + "&project_id=" + c.Project.Identifier,
		CancelOnclick: "$('#journal-" + id + "-form').remove(); $('#journal-" + id + "-notes').show(); return false;"}
	f.Data = rails.NewHash("auto_complete", true)
	f.Data.Update(listAutofillHash(l))
	c.Render("journals/edit", map[string]any{"F": f}, RenderOptions{Format: "js", Layout: view.NoLayout})
}

// journalEditable は @journal.editable_by?(User.current)。
func (a *App) journalEditable(c *Req, j *repository.IssueJournal) bool {
	e := a.writeIssuesEnv(c, a.DB)
	ij, err := e.FindJournal(c.Ctx(), j.ID)
	if err != nil || ij == nil {
		a.internalError(c, "journal editable", err)
		return false
	}
	ok, err := e.JournalEditableBy(c.Ctx(), ij, c.User)
	if err != nil {
		a.internalError(c, "journal editable", err)
		return false
	}
	return ok
}

// JournalsUpdate は JournalsController#update（PUT / PATCH /journals/:id）。
func (a *App) JournalsUpdate(c *Req) {
	rj, _ := c.local(ctxJournal).(*repository.IssueJournal)
	if !a.journalEditable(c, rj) {
		if !c.Halted() {
			c.Render403("")
		}
		return
	}
	ctx := c.Ctx()
	e := a.writeIssuesEnv(c, a.DB)
	j, err := e.FindJournal(ctx, rj.ID)
	if err != nil || j == nil {
		a.internalError(c, "journal update", err)
		return
	}
	p := c.Params()
	notes := j.Notes
	if v, ok := p.Lookup("journal", "notes"); ok {
		notes = rails.ToS(v)
	}
	var priv *bool
	if v, ok := p.Lookup("journal", "private_notes"); ok {
		b := castParamBool(v)
		priv = &b
	}
	if err := e.UpdateJournalNotes(ctx, j, notes, priv, c.User); err != nil {
		if errors.Is(err, issues.ErrJournalNotEditable) {
			c.Render403("")
			return
		}
		a.internalError(c, "journal update", err)
		return
	}
	destroyed := strings.TrimSpace(j.Notes) == "" && len(j.Details) == 0
	switch httpx.Negotiate(c.R, "html", "js", "xml", "json") {
	case "html":
		c.Redirect("/issues/" + strconv.FormatInt(j.IssueID, 10))
	case "js":
		a.renderJournalUpdateJS(c, j, destroyed)
	case "xml", "json":
		c.RenderAPIOK()
	default:
		c.unknownFormat()
	}
}

// castParamBool は Rails の真偽値キャスト。
func castParamBool(v any) bool {
	switch rails.ToS(v) {
	case "", "0", "f", "F", "false", "FALSE", "off", "OFF":
		return false
	}
	return true
}

// renderJournalUpdateJS は update.js.erb。
func (a *App) renderJournalUpdateJS(c *Req, j *issues.Journal, destroyed bool) {
	data := map[string]any{"ID": j.ID, "Destroyed": destroyed}
	if !destroyed {
		l := a.newIssueLookup(c)
		row := l.issue(j.IssueID)
		if row == nil {
			a.internalError(c, "journal update", l.err)
			return
		}
		m := l.model(row)
		var jv *journalView
		for _, x := range m.visibleJournals() {
			if x.ID == j.ID {
				jv = x
				break
			}
		}
		if jv == nil {
			jv = &journalView{Journal: j, l: l, issue: m, User: l.principal(j.UserID)}
		}
		reply, err := c.Authz().AllowedTo(c.Ctx(), domain.ControllerAction("issues", "edit"), c.Project)
		if err != nil {
			a.internalError(c, "journal update", err)
			return
		}
		data["J"] = jv
		data["ReplyLinks"] = reply
		if l.err != nil {
			a.internalError(c, "journal update", l.err)
			return
		}
	}
	c.Render("journals/update", data, RenderOptions{Format: "js", Layout: view.NoLayout})
}
