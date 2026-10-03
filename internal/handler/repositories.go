// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/scm"
	"github.com/mikuta0407/buropher/internal/scmsync"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/validation"
)

// RepositoriesController（app/controllers/repositories_controller.rb）。buropher は Git のみ対応する（D-15）。
//
//	menu_item :repository
//	menu_item :settings, :only => [:new, :create, :edit, :update, :destroy, :committers]
//	default_search_scope :changesets
//	before_action :find_project_by_project_id, :only => [:new, :create]
//	before_action :build_new_repository_from_params, :only => [:new, :create]
//	before_action :find_repository, :only => [:edit, :update, :destroy, :committers]
//	before_action :find_project_repository, :except => [:new, :create, :edit, :update, :destroy, :committers]
//	before_action :find_changeset, :only => [:revision, :add_related_issue, :remove_related_issue]
//	before_action :authorize
//	accept_atom_auth :revisions
//	accept_api_auth :add_related_issue, :remove_related_issue
var RepositoriesController = &Controller{Name: "repositories", MainMenu: true, DefaultSearchScope: "changesets"}

// routesRepositories は repositories コントローラのルートを登録する（config/routes.rb の repositories routes）。
func (a *App) routesRepositories(r Router) {
	rc := RepositoriesController
	build := Before(a.buildNewRepositoryFromParams)
	findRepo := Before(a.findRepository)
	findPR := Before(a.findProjectRepository)
	findCS := Before(a.findChangeset)
	auth := Authorize()

	// shallow resources :repositories（:except => [:index, :show]）+ committers
	a.Handle(r, http.MethodGet, "/projects/{project_id}/repositories/new", rc, "new", a.RepositoriesNew, FindProjectByProjectID(), build, auth)
	a.Handle(r, http.MethodPost, "/projects/{project_id}/repositories", rc, "create", a.RepositoriesCreate, FindProjectByProjectID(), build, auth)
	a.Handle(r, http.MethodGet, "/repositories/{id}/edit", rc, "edit", a.RepositoriesEdit, findRepo, auth)
	a.Handle(r, http.MethodPatch, "/repositories/{id}", rc, "update", a.RepositoriesUpdate, findRepo, auth)
	a.Handle(r, http.MethodPut, "/repositories/{id}", rc, "update", a.RepositoriesUpdate, findRepo, auth)
	a.Handle(r, http.MethodDelete, "/repositories/{id}", rc, "destroy", a.RepositoriesDestroy, findRepo, auth)
	a.Handle(r, http.MethodGet, "/repositories/{id}/committers", rc, "committers", a.RepositoriesCommitters, findRepo, auth)
	a.Handle(r, http.MethodPost, "/repositories/{id}/committers", rc, "committers", a.RepositoriesCommitters, findRepo, auth)

	base := "/projects/{id}/repository/{repository_id}"
	a.Handle(r, http.MethodGet, base+"/statistics", rc, "stats", a.RepositoriesStats, findPR, auth)
	a.Handle(r, http.MethodGet, base+"/graph", rc, "graph", a.RepositoriesGraph, findPR, auth)
	a.Handle(r, http.MethodPost, base+"/fetch_changesets", rc, "fetch_changesets", a.RepositoriesFetchChangesets, findPR, auth)
	a.Handle(r, http.MethodGet, base+"/revisions/{rev}", rc, "revision", a.RepositoriesRevision, findPR, findCS, auth)
	a.Handle(r, http.MethodGet, base+"/revision", rc, "revision", a.RepositoriesRevision, findPR, findCS, auth)
	a.Handle(r, http.MethodPost, base+"/revisions/{rev}/issues", rc, "add_related_issue", a.RepositoriesAddRelatedIssue,
		AcceptAPIAuth(), findPR, findCS, auth)
	a.Handle(r, http.MethodDelete, base+"/revisions/{rev}/issues/{issue_id}", rc, "remove_related_issue", a.RepositoriesRemoveRelatedIssue,
		AcceptAPIAuth(), findPR, findCS, auth)
	a.Handle(r, http.MethodGet, base+"/revisions", rc, "revisions", a.RepositoriesRevisions, AcceptAtomAuth(), findPR, auth)
	revCheck := Before(checkRevConstraint)
	// :format => 'html' のルート（url_for が format を引き継ぐ: ロードマップのメニューが versions.html になる）
	rf := r.With(routeFormat("html"))
	for _, action := range []string{"browse", "show", "entry", "raw", "annotate"} {
		fn := a.repositoryAction(action)
		a.Handle(rf, http.MethodGet, base+"/revisions/{rev}/"+action, rc, action, fn, revCheck, findPR, auth)
		a.Handle(rf, http.MethodGet, base+"/revisions/{rev}/"+action+"/*", rc, action, fn, revCheck, findPR, auth)
	}
	for _, action := range []string{"browse", "entry", "raw", "changes", "annotate"} {
		fn := a.repositoryAction(action)
		a.Handle(rf, http.MethodGet, base+"/"+action, rc, action, fn, findPR, auth)
		a.Handle(rf, http.MethodGet, base+"/"+action+"/*", rc, action, fn, findPR, auth)
	}
	a.Handle(rf, http.MethodGet, base+"/revisions/{rev}/diff", rc, "diff", a.RepositoriesDiff, revCheck, findPR, auth)
	a.Handle(rf, http.MethodGet, base+"/revisions/{rev}/diff/*", rc, "diff", a.RepositoriesDiff, revCheck, findPR, auth)
	a.Handle(rf, http.MethodGet, base+"/diff", rc, "diff", a.RepositoriesDiff, findPR, auth)
	a.Handle(rf, http.MethodGet, base+"/diff/*", rc, "diff", a.RepositoriesDiff, findPR, auth)
	a.Handle(rf, http.MethodGet, base+"/show/*", rc, "show", a.RepositoriesShow, findPR, auth)
	a.Handle(r, http.MethodGet, base, rc, "show", a.RepositoriesShow, findPR, auth)
	a.Handle(r, http.MethodGet, "/projects/{id}/repository", rc, "show", a.RepositoriesShow, findPR, auth)
}

// repositoryAction はアクション名から処理を選ぶ（browse は show の別名）。
func (a *App) repositoryAction(action string) func(c *Req) {
	switch action {
	case "browse", "show":
		return a.RepositoriesShow
	case "entry":
		return a.RepositoriesEntry
	case "raw":
		return a.RepositoriesRaw
	case "annotate":
		return a.RepositoriesAnnotate
	case "changes":
		return a.RepositoriesChanges
	}
	return a.RepositoriesShow
}

// reRevRoute は routes.rb の :constraints => {:rev => /[a-z0-9\.\-_]+/}。
var reRevRoute = regexp.MustCompile(`\A[a-z0-9\.\-_]+\z`)

// checkRevConstraint はルートの rev の制約（一致しなければルートが無いので 404）。
func checkRevConstraint(c *Req) {
	if !reRevRoute.MatchString(c.Params().String("rev")) {
		c.Render404("")
	}
}

// ---------------------------------------------------------------- 状態

type repoStateKey struct{}

// repoState は @repository / @path / @rev / @rev_to / @changeset。
type repoState struct {
	repo      *domain.Repository
	git       *scm.Git
	path      string
	rev       string
	revTo     string
	changeset *domain.Changeset
	// form は new / create / edit / update のフォーム。
	form *repositoryForm
}

func (c *Req) repoState() *repoState {
	s, _ := c.value(repoStateKey{}).(*repoState)
	return s
}

// scmService はチェンジセット取り込みの Service（チケットと同じ通知先 a.issueNotifier() を使う。
// App.Notifier はテスト用の上書きで、本番では nil のため、それを渡すとキーワードによる更新が通知されない）。
func (a *App) scmService() *scmsync.Service {
	return &scmsync.Service{DB: a.DB, Settings: a.Settings, GitCommand: a.GitCommand, Bundle: a.Bundle, Now: a.Now,
		Notifier: a.issueNotifier(), Logger: a.Logger}
}

// SCMService は定期取り込み（server の scm.fetch_interval）用の scmService。通知先は呼び出し時点の設定で決まる。
func (a *App) SCMService() *scmsync.Service { return a.scmService() }

// gitAdapter は repository.scm。
func (a *App) gitAdapter(repo *domain.Repository) *scm.Git {
	return scm.NewGit(a.GitCommand, repo.URL, repo.RootURL, repo.PathEncoding)
}

// reHexRev は REV_PARAM_RE。
var reHexRev = regexp.MustCompile(`(?i)\A[a-f0-9]*\z`)

// validRevName は valid_name?(rev)（16 進数、またはブランチ・タグとして存在する名前）。
func (s *repoState) validRevName(c *Req, rev string) bool {
	if rev == "" || reHexRev.MatchString(rev) {
		return true
	}
	if !s.repo.IsGit() {
		return regexp.MustCompile(`\A[0-9]*\z`).MatchString(rev)
	}
	return s.git.ValidName(c.Ctx(), rev)
}

// findProjectRepository は find_project_repository。
func (a *App) findProjectRepository(c *Req) {
	if !c.FindProject(c.Params().String("id")) {
		return
	}
	repos, err := repository.ProjectScmRepositories(c.Ctx(), a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "project repositories", err)
		return
	}
	var repo *domain.Repository
	if rid := c.Params().String("repository_id"); strings.TrimSpace(rid) != "" {
		repo = findByIdentifierParam(repos, rid)
	} else {
		for _, r := range repos {
			if r.IsDefault {
				repo = r
				break
			}
		}
		if repo == nil && len(repos) > 0 {
			repo = repos[0]
		}
	}
	if repo == nil {
		c.Render404("")
		return
	}
	repo.Project = c.Project
	if err := scmsync.EnsureRootURL(c.Ctx(), a.DB, repo); err != nil {
		a.internalError(c, "repository root_url", err)
		return
	}
	s := &repoState{repo: repo, git: a.gitAdapter(repo)}
	c.setValue(repoStateKey{}, s)
	s.path = repoPathParam(c)
	s.rev = strings.TrimSpace(c.Params().String("rev"))
	if s.rev == "" && repo.IsGit() {
		s.rev = s.git.DefaultBranch(c.Ctx())
	}
	if !s.validRevName(c, s.rev) {
		a.showErrorNotFound(c)
		return
	}
	s.revTo = strings.TrimSpace(c.Params().String("rev_to"))
	if !s.validRevName(c, s.revTo) {
		a.showErrorNotFound(c)
		return
	}
}

// repoPathParam は params[:path]（ルートのグロブ、無ければクエリ）。
func repoPathParam(c *Req) string {
	if v, ok := c.Params().Get("*"); ok {
		if s := httpx.ValueString(v); s != "" {
			return strings.TrimSuffix(s, "/")
		}
	}
	p := c.Params()
	if vs := p.Strings("path"); len(vs) > 1 {
		return strings.Join(vs, "/")
	}
	return p.String("path")
}

// findByIdentifierParam は project.repositories.find_by_identifier_param(param)。
func findByIdentifierParam(repos []*domain.Repository, param string) *domain.Repository {
	if regexp.MustCompile(`^\d+$`).MatchString(param) {
		id, _ := strconv.ParseInt(param, 10, 64)
		for _, r := range repos {
			if r.ID == id {
				return r
			}
		}
		return nil
	}
	for _, r := range repos {
		if r.Identifier == param {
			return r
		}
	}
	return nil
}

// findChangeset は find_changeset（@rev のチェンジセット。無ければ error_scm_not_found）。
func (a *App) findChangeset(c *Req) {
	s := c.repoState()
	if s.rev != "" {
		cs, err := scmsync.FindChangesetByName(c.Ctx(), a.DB, s.repo, s.rev)
		if err != nil {
			a.internalError(c, "find changeset", err)
			return
		}
		s.changeset = cs
	}
	if s.changeset == nil {
		a.showErrorNotFound(c)
	}
}

// showErrorNotFound は show_error_not_found。
func (a *App) showErrorNotFound(c *Req) {
	c.RenderError(http.StatusNotFound, c.L("error_scm_not_found"))
}

// findRepository は find_repository（Repository.find(params[:id]) と @project）。
func (a *App) findRepository(c *Req) {
	id, ok := paramInt64(c, "id")
	if !ok {
		c.Render404("")
		return
	}
	repo, err := repository.GetScmRepository(c.Ctx(), a.DB, id)
	if err != nil {
		a.notFoundOr500(c, "find repository", err)
		return
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, repo.ProjectID)
	if err != nil {
		a.notFoundOr500(c, "repository project", err)
		return
	}
	repo.Project = p
	c.Project = p
	c.setValue(repoStateKey{}, &repoState{repo: repo, git: a.gitAdapter(repo), form: newRepositoryForm(repo)})
}

// ---------------------------------------------------------------- new / create / edit / update

// repositoryForm は labelled_form_for :repository のモデル。
type repositoryForm struct {
	*domain.Repository
	newRecord bool
	// identifierWas は保存済みの identifier（identifier_frozen? の判定）。
	identifierWas    string
	isDefaultChanged bool
	identifierSet    bool
	errors           *validation.Errors
}

func newRepositoryForm(r *domain.Repository) *repositoryForm {
	return &repositoryForm{Repository: r, newRecord: r.ID == 0, identifierWas: r.Identifier, errors: repositoryErrors()}
}

func repositoryErrors() *validation.Errors {
	return &validation.Errors{Model: "repository", AttrNames: map[string]string{
		"url": "field_path_to_repository", "log_encoding": "field_commit_logs_encoding"}}
}

// ValidationErrors は error_messages_for のエラー。
func (f *repositoryForm) ValidationErrors() *validation.Errors { return f.errors }

// ErrorsOn は errors[attr]（ラベルの class="error"）。
func (f *repositoryForm) ErrorsOn(attr string) []string {
	var out []string
	for _, e := range f.errors.List() {
		if e.Attr == attr {
			out = append(out, e.Key+e.Message)
		}
	}
	return out
}

// NewRecord は new_record?。
func (f *repositoryForm) NewRecord() bool { return f.newRecord }

// Persisted は persisted?（フォームの class / method の判定）。
func (f *repositoryForm) Persisted() bool { return !f.newRecord }

// IdentifierFrozen は identifier_frozen?（保存済みで identifier が空でなく、identifier のエラーが無い）。
func (f *repositoryForm) IdentifierFrozen() bool {
	return !f.errors.Include("identifier") && !(f.newRecord || strings.TrimSpace(f.identifierWas) == "")
}

// Send は labelled_form_for のフィールド値（rails.Sender）。
func (f *repositoryForm) Send(name string) (any, bool) {
	switch name {
	case "is_default":
		return f.IsDefault, true
	case "identifier":
		if f.newRecord && !f.identifierSet {
			return nil, true
		}
		return f.Identifier, true
	case "url":
		return f.URL, true
	case "path_encoding":
		return nilIfEmptyString(f.PathEncoding), true
	case "log_encoding":
		return nilIfEmptyString(f.LogEncoding), true
	case "login":
		return f.Login, true
	case "report_last_commit":
		return f.ReportLastCommit(), true
	case "persisted?":
		return !f.newRecord, true
	}
	return nil, false
}

// assign は repository.safe_attributes = params[:repository]。
func (f *repositoryForm) assign(c *Req) {
	p := c.Params().Map("repository")
	if p == nil {
		return
	}
	r := f.Repository
	if v, ok := p.StringOK("identifier"); ok && !f.IdentifierFrozen() {
		r.Identifier = v
		f.identifierSet = true
	}
	if v, ok := p.StringOK("login"); ok {
		r.Login = v
	}
	if v, ok := p.StringOK("path_encoding"); ok {
		r.PathEncoding = v
	}
	if v, ok := p.StringOK("log_encoding"); ok {
		r.LogEncoding = v
	}
	if v, ok := p.StringOK("is_default"); ok {
		b := castBool(v)
		if b != r.IsDefault {
			f.isDefaultChanged = true
		}
		r.IsDefault = b
	}
	if v, ok := p.StringOK("url"); ok && f.newRecord {
		r.URL = strings.TrimSpace(v)
	}
	if v, ok := p.StringOK("report_last_commit"); ok && r.IsGit() {
		r.MergeExtraInfo(map[string]any{"extra_report_last_commit": v})
	}
}

// validate は Repository / Repository::Git の検証。
func (a *App) validateRepository(c *Req, f *repositoryForm) error {
	r := f.Repository
	f.errors = repositoryErrors()
	errs := f.errors
	r.Identifier = strings.TrimSpace(r.Identifier)
	if n := len([]rune(r.Login)); n > 60 {
		errs.Add("login", "too_long", "count", 60)
	}
	if len([]rune(r.RootURL)) > 255 {
		errs.Add("root_url", "too_long", "count", 255)
	}
	if len([]rune(r.URL)) > 255 {
		errs.Add("url", "too_long", "count", 255)
	}
	if r.Identifier != "" && len([]rune(r.Identifier)) > domain.RepositoryIdentifierMaxLength {
		errs.Add("identifier", "too_long", "count", domain.RepositoryIdentifierMaxLength)
	}
	taken, err := repository.ScmRepositoryIdentifierTaken(c.Ctx(), a.DB, r.ProjectID, r.Identifier, r.ID)
	if err != nil {
		return err
	}
	if taken {
		errs.Add("identifier", "taken")
	}
	if slices.Contains(domain.RepositoryReservedIdentifiers, r.Identifier) {
		errs.Add("identifier", "exclusion")
	}
	if r.Identifier != "" && (!domain.RepositoryIdentifierRe.MatchString(r.Identifier) || domain.RepositoryIdentifierAllDigits.MatchString(r.Identifier)) {
		errs.Add("identifier", "invalid")
	}
	if f.newRecord && !slices.Contains(a.Settings.Strings("enabled_scm"), "Git") {
		errs.Add("type", "invalid")
	}
	if strings.TrimSpace(r.URL) == "" {
		errs.Add("url", "blank")
	}
	return nil
}

// saveRepository は repository.save（検証 → check_default → INSERT / UPDATE）。
func (a *App) saveRepository(c *Req, f *repositoryForm) (bool, error) {
	if err := a.validateRepository(c, f); err != nil {
		return false, err
	}
	if f.errors.Any() {
		return false, nil
	}
	r := f.Repository
	err := a.withTx(c, func(tx *db.Tx) error {
		// check_default
		if !r.IsDefault && f.newRecord {
			repos, err := repository.ProjectScmRepositories(c.Ctx(), tx, r.ProjectID)
			if err != nil {
				return err
			}
			if len(repos) == 0 {
				r.IsDefault = true
				f.isDefaultChanged = true
			}
		}
		if r.IsDefault && (f.isDefaultChanged || f.newRecord) {
			if err := repository.ClearDefaultScmRepository(c.Ctx(), tx, r.ProjectID); err != nil {
				return err
			}
		}
		if f.newRecord {
			r.CreatedOn = a.now()
			return repository.InsertScmRepository(c.Ctx(), tx, r)
		}
		return repository.UpdateScmRepository(c.Ctx(), tx, r)
	})
	if err != nil {
		return false, err
	}
	f.newRecord = false
	return true, nil
}

// buildNewRepositoryFromParams は build_new_repository_from_params（Repository.factory(scm)。Git 以外は 404）。
func (a *App) buildNewRepositoryFromParams(c *Req) {
	scmName := c.Params().String("repository_scm")
	if !c.Params().Has("repository_scm") {
		scmName = "Git"
	}
	if scmName != "Git" {
		// 意図的な差異: buropher は Git のみ対応（D-15）。他の SCM の factory は nil 扱いで 404。
		c.Render404("")
		return
	}
	r := &domain.Repository{ProjectID: c.Project.ID, SCM: "git", Project: c.Project}
	f := newRepositoryForm(r)
	f.assign(c)
	c.setValue(repoStateKey{}, &repoState{repo: r, form: f})
}

// repositoryFormData は new / edit のビューのデータ。
func (a *App) repositoryFormData(c *Req, f *repositoryForm) map[string]any {
	return map[string]any{
		"Repository":       f,
		"SCMAvailable":     scm.ClientAvailable(a.GitCommand),
		"Encodings":        settings.Encodings,
		"NewRepositoryURL": "/projects/" + c.Project.Identifier + "/repositories/new",
	}
}

// RepositoriesNew は repositories#new（XHR は new.js で #content を差し替える）。
func (a *App) RepositoriesNew(c *Req) {
	f := c.repoState().form
	has, err := repository.ProjectHasDefaultRepository(c.Ctx(), a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "default repository", err)
		return
	}
	f.IsDefault = !has
	c.NoStore()
	if httpx.Format(c.R) == "js" || (httpx.IsXHR(c.R) && httpx.Negotiate(c.R, "js", "html") == "js") {
		data := a.repositoryFormData(c, f)
		html, err := c.renderFragment("repositories/new", data)
		if err != nil {
			a.internalError(c, "render new repository", err)
			return
		}
		data["Content"] = html
		c.Render("repositories/new", data, RenderOptions{Format: "js"})
		return
	}
	c.Render("repositories/new", a.repositoryFormData(c, f))
}

// RepositoriesCreate は repositories#create。
func (a *App) RepositoriesCreate(c *Req) {
	f := c.repoState().form
	ok, err := a.saveRepository(c, f)
	if err != nil {
		a.internalError(c, "create repository", err)
		return
	}
	if ok {
		c.Redirect("/projects/" + c.Project.Identifier + "/settings/repositories")
		return
	}
	c.NoStore()
	c.Render("repositories/new", a.repositoryFormData(c, f))
}

// RepositoriesEdit は repositories#edit。
func (a *App) RepositoriesEdit(c *Req) {
	c.NoStore()
	c.Render("repositories/edit", a.repositoryFormData(c, c.repoState().form))
}

// RepositoriesUpdate は repositories#update。
func (a *App) RepositoriesUpdate(c *Req) {
	f := c.repoState().form
	f.assign(c)
	ok, err := a.saveRepository(c, f)
	if err != nil {
		a.internalError(c, "update repository", err)
		return
	}
	if ok {
		c.Redirect("/projects/" + c.Project.Identifier + "/settings/repositories")
		return
	}
	c.NoStore()
	c.Render("repositories/edit", a.repositoryFormData(c, f))
}

// RepositoriesDestroy は repositories#destroy。
func (a *App) RepositoriesDestroy(c *Req) {
	s := c.repoState()
	if err := a.withTx(c, func(tx *db.Tx) error { return repository.DeleteScmRepository(c.Ctx(), tx, s.repo.ID) }); err != nil {
		a.internalError(c, "destroy repository", err)
		return
	}
	c.Redirect("/projects/" + c.Project.Identifier + "/settings/repositories")
}

// committerRow は committers の 1 行。
type committerRow struct {
	Index     int
	Committer string
	UserID    int64
}

// RepositoriesCommitters は repositories#committers（GET は対応表、POST で保存）。
func (a *App) RepositoriesCommitters(c *Req) {
	s := c.repoState()
	ctx := c.Ctx()
	committers, err := repository.RepositoryCommittersByFirstAppearance(ctx, a.DB, s.repo.ID)
	if err != nil {
		a.internalError(c, "committers", err)
		return
	}
	if c.R.Method == http.MethodPost {
		if m := c.Params().Map("committers"); m != nil && len(m.Keys()) > 0 {
			// params[:committers].values.inject({}) {|h, c| h[c.first] = c.last; h}
			h := map[string]string{}
			for _, k := range m.Keys() {
				vs := m.Strings(k)
				if len(vs) == 0 {
					continue
				}
				h[vs[0]] = vs[len(vs)-1]
			}
			err := a.withTx(c, func(tx *db.Tx) error {
				for _, cm := range committers {
					nv, ok := h[cm.Committer]
					if !ok {
						continue
					}
					n := httpx.RubyToI(nv)
					if n == cm.UserID.Int64 {
						continue
					}
					var uid *int64
					if n > 0 {
						uid = &n
					}
					if err := repository.UpdateCommitterUser(ctx, tx, s.repo.ID, cm.Committer, uid); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				a.internalError(c, "update committers", err)
				return
			}
			c.Flash().SetNotice(c.L("notice_successful_update"))
			c.Redirect("/projects/" + c.Project.Identifier + "/settings/repositories")
			return
		}
	}
	// @users = @project.users + マッピング済みの他のユーザー（sort!）
	users, err := repository.ProjectActiveMemberUsers(ctx, a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "project users", err)
		return
	}
	have := map[int64]bool{}
	for _, u := range users {
		have[u.ID] = true
	}
	for _, cm := range committers {
		if cm.UserID.Valid && !have[cm.UserID.Int64] {
			if u, err := repository.GetUser(ctx, a.DB, cm.UserID.Int64); err == nil {
				users = append(users, u)
				have[u.ID] = true
			}
		}
	}
	page := c.Page()
	slices.SortStableFunc(users, func(x, y *domain.User) int {
		return strings.Compare(strings.ToLower(page.UserName(x)), strings.ToLower(page.UserName(y)))
	})
	rows := make([]committerRow, len(committers))
	for i, cm := range committers {
		rows[i] = committerRow{Index: i, Committer: cm.Committer, UserID: cm.UserID.Int64}
	}
	c.Render("repositories/committers", map[string]any{"Committers": rows, "Users": users, "Repository": s.repo})
}

// ---------------------------------------------------------------- fetch_changesets / related issues

// RepositoriesFetchChangesets は repositories#fetch_changesets（自動取得が無効なときだけ取得する）。
func (a *App) RepositoriesFetchChangesets(c *Req) {
	s := c.repoState()
	if c.Project.Active() && s.path == "" && !a.Settings.Bool("autofetch_changesets") {
		if err := a.scmService().Fetch(c.Ctx(), s.repo, c.User); err != nil {
			a.renderCommandFailed(c, err)
			return
		}
	}
	c.Redirect(helperRepositoryURL(c.Project, s.repo, "show", "", ""))
}

// renderCommandFailed は show_error_command_failed（それ以外のエラーは 500）。
func (a *App) renderCommandFailed(c *Req, err error) {
	var cf *scm.CommandFailed
	if errors.As(err, &cf) {
		c.RenderError(http.StatusInternalServerError, c.L("error_scm_command_failed", cf.Message))
		return
	}
	a.internalError(c, "scm", err)
}

// RepositoriesAddRelatedIssue は repositories#add_related_issue（JS / API）。
func (a *App) RepositoriesAddRelatedIssue(c *Req) {
	s := c.repoState()
	ctx := c.Ctx()
	issueID := strings.TrimPrefix(c.Params().String("issue_id"), "#")
	var issue *redmine.Issue
	if n := httpx.RubyToI(issueID); strings.TrimSpace(issueID) != "" && n > 0 {
		ref, err := a.scmService().FindReferencedIssueByID(ctx, a.DB, c.Project, n)
		if err != nil {
			a.internalError(c, "referenced issue", err)
			return
		}
		if ref != nil {
			is, err := redmine.NewDBStore(ctx, a.DB, c.Authz()).VisibleIssue(ref.ID)
			if err != nil {
				a.internalError(c, "visible issue", err)
				return
			}
			issue = is
		}
	}
	if issue != nil {
		ids, err := repository.ChangesetIssueIDs(ctx, a.DB, s.changeset.ID)
		if err != nil {
			a.internalError(c, "changeset issues", err)
			return
		}
		if slices.Contains(ids, issue.ID) {
			issue = nil
		}
	}
	if issue != nil {
		if err := repository.AddChangesetIssue(ctx, a.DB, s.changeset.ID, issue.ID); err != nil {
			a.internalError(c, "add changeset issue", err)
			return
		}
	}
	if httpx.IsAPIRequest(c.R) {
		if issue != nil {
			c.RenderAPIOK()
		} else {
			c.RenderAPIErrors(c.L("label_issue") + " " + c.L("activerecord.errors.messages.invalid"))
		}
		return
	}
	data, err := a.relatedIssuesData(c)
	if err != nil {
		a.internalError(c, "related issues", err)
		return
	}
	data["Issue"] = issue
	c.Render("repositories/add_related_issue", data, RenderOptions{Format: "js"})
}

// RepositoriesRemoveRelatedIssue は repositories#remove_related_issue（JS / API）。
func (a *App) RepositoriesRemoveRelatedIssue(c *Req) {
	s := c.repoState()
	ctx := c.Ctx()
	var issue *redmine.Issue
	if n := httpx.RubyToI(c.Params().String("issue_id")); n > 0 {
		is, err := redmine.NewDBStore(ctx, a.DB, c.Authz()).VisibleIssue(n)
		if err != nil {
			a.internalError(c, "visible issue", err)
			return
		}
		issue = is
	}
	if issue != nil {
		if err := repository.RemoveChangesetIssue(ctx, a.DB, s.changeset.ID, issue.ID); err != nil {
			a.internalError(c, "remove changeset issue", err)
			return
		}
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderAPIOK()
		return
	}
	c.Render("repositories/remove_related_issue", map[string]any{"Issue": issue}, RenderOptions{Format: "js"})
}
