package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// IssueStatusesController（app/controllers/issue_statuses_controller.rb）。
var IssueStatusesController = &Controller{Name: "issue_statuses", MainMenu: false}

// routesIssueStatuses は issue_statuses コントローラのルートを登録する。
//
//	resources :issue_statuses, :except => :show do
//	  collection do
//	    post 'update_issue_done_ratio'
//	  end
//	end
func (a *App) routesIssueStatuses(r Router) {
	// before_action :require_admin, :except => :index
	// before_action :require_admin_or_api_request, :only => :index
	// accept_api_auth :index
	a.Handle(r, http.MethodPost, "/issue_statuses/update_issue_done_ratio", IssueStatusesController, "update_issue_done_ratio", a.IssueStatusesUpdateIssueDoneRatio, RequireAdmin())
	a.Handle(r, http.MethodGet, "/issue_statuses", IssueStatusesController, "index", a.IssueStatusesIndex, RequireAdminOrAPIRequest(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/issue_statuses", IssueStatusesController, "create", a.IssueStatusesCreate, RequireAdmin())
	a.Handle(r, http.MethodGet, "/issue_statuses/new", IssueStatusesController, "new", a.IssueStatusesNew, RequireAdmin())
	a.Handle(r, http.MethodGet, "/issue_statuses/{id}/edit", IssueStatusesController, "edit", a.IssueStatusesEdit, RequireAdmin())
	a.Handle(r, http.MethodPatch, "/issue_statuses/{id}", IssueStatusesController, "update", a.IssueStatusesUpdate, RequireAdmin())
	a.Handle(r, http.MethodPut, "/issue_statuses/{id}", IssueStatusesController, "update", a.IssueStatusesUpdate, RequireAdmin())
	a.Handle(r, http.MethodDelete, "/issue_statuses/{id}", IssueStatusesController, "destroy", a.IssueStatusesDestroy, RequireAdmin())
}

// issueStatusForm は issue_statuses/_form のモデル（@issue_status）。
type issueStatusForm struct {
	formModel
	*domain.IssueStatus
}

func newIssueStatusForm(c *Req, s *domain.IssueStatus) *issueStatusForm {
	return &issueStatusForm{formModel: newFormModel(c, "issue_status", s.ID), IssueStatus: s}
}

// Description は description（テキストエリアの値）。
func (f *issueStatusForm) Description() any {
	if f.IssueStatus.Description == nil {
		return nil
	}
	return *f.IssueStatus.Description
}

// DefaultDoneRatio は default_done_ratio（nil 可）。
func (f *issueStatusForm) DefaultDoneRatio() any {
	if f.IssueStatus.DefaultDoneRatio == nil {
		return nil
	}
	return *f.IssueStatus.DefaultDoneRatio
}

// assign は @issue_status.safe_attributes = params[:issue_status]。
func (f *issueStatusForm) assign(p *httpx.Params) {
	if p == nil {
		return
	}
	s := f.IssueStatus
	if v, ok := p.StringOK("name"); ok {
		s.Name = v
	}
	if v, ok := p.StringOK("description"); ok {
		s.Description = &v
	}
	if v, ok := p.StringOK("is_closed"); ok {
		s.IsClosed = castBool(v)
	}
	if v, ok := p.StringOK("position"); ok {
		s.Position = int(httpx.RubyToI(v))
	}
	if v, ok := p.StringOK("default_done_ratio"); ok {
		if strings.TrimSpace(v) == "" {
			s.DefaultDoneRatio = nil
		} else {
			n := int(httpx.RubyToI(v))
			s.DefaultDoneRatio = &n
		}
	}
}

func (a *App) validateIssueStatus(c *Req, f *issueStatusForm) error {
	f.errs = &domain.ValidationErrors{}
	s := f.IssueStatus
	if strings.TrimSpace(s.Name) == "" {
		f.errs.Add("name", "blank", nil)
	}
	taken, err := repository.IssueStatusNameTaken(c.Ctx(), a.DB, s.Name, s.ID)
	if err != nil {
		return err
	}
	if taken {
		f.errs.Add("name", "taken", nil)
	}
	if len([]rune(s.Name)) > 30 {
		f.errs.Add("name", "too_long", map[string]any{"count": 30})
	}
	if len([]rune(s.DescriptionString())) > 255 {
		f.errs.Add("description", "too_long", map[string]any{"count": 255})
	}
	if r := s.DefaultDoneRatio; r != nil && (*r < 0 || *r > 100) {
		f.errs.Add("default_done_ratio", "inclusion", nil)
	}
	return nil
}

// useStatusForDoneRatio は Issue.use_status_for_done_ratio?。
func (a *App) useStatusForDoneRatio() bool {
	return a.Settings.String("issue_done_ratio") == "issue_status"
}

func (a *App) renderIssueStatusForm(c *Req, tmpl string, f *issueStatusForm) {
	interval := int(httpx.RubyToI(a.Settings.String("issue_done_ratio_interval")))
	var opts [][]any
	if interval > 0 {
		for r := 0; r <= 100; r += interval {
			opts = append(opts, []any{fmt.Sprintf("%d %%", r), r})
		}
	}
	c.renderAdmin(tmpl, map[string]any{
		"IssueStatus":           f,
		"UseStatusForDoneRatio": a.useStatusForDoneRatio(),
		"DoneRatioOptions":      opts,
	}, false)
}

func (a *App) findIssueStatus(c *Req) *domain.IssueStatus {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return nil
	}
	s, err := repository.GetIssueStatus(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find issue status", err)
		}
		return nil
	}
	return s
}

// saveIssueStatus はステータスを保存する（acts_as_positioned と handle_is_closed_change）。
func (a *App) saveIssueStatus(c *Req, f *issueStatusForm, orig *domain.IssueStatus) error {
	return a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		ctx := c.Ctx()
		s := f.IssueStatus
		isNew := s.ID == 0
		if isNew && s.Position == 0 {
			pos, err := repository.NextPosition(ctx, tx, repository.IssueStatusPositionScope)
			if err != nil {
				return err
			}
			s.Position = pos
		}
		if err := repository.SaveIssueStatus(ctx, tx, s); err != nil {
			return err
		}
		f.id = s.ID
		if isNew {
			return repository.InsertPosition(ctx, tx, repository.IssueStatusPositionScope, s.ID, s.Position)
		}
		if orig.Position != s.Position {
			if err := repository.ShiftPositions(ctx, tx, repository.IssueStatusPositionScope, s.ID, orig.Position, s.Position); err != nil {
				return err
			}
		}
		if !orig.IsClosed && s.IsClosed {
			return repository.HandleIssueStatusClosed(ctx, tx, s.ID)
		}
		return nil
	})
}

// ---------------------------------------------------------------- アクション

// IssueStatusesIndex は issue_statuses#index（GET /issue_statuses）。
func (a *App) IssueStatusesIndex(c *Req) {
	statuses, err := repository.ListIssueStatuses(c.Ctx(), a.DB)
	if err != nil {
		a.internalError(c, "list statuses", err)
		return
	}
	switch httpx.Negotiate(c.R, "html", "xml", "json") {
	case "html":
		wf, err := repository.StatusIDsInWorkflow(c.Ctx(), a.DB)
		if err != nil {
			a.internalError(c, "status workflows", err)
			return
		}
		c.renderAdmin("issue_statuses/index", map[string]any{
			"IssueStatuses":         statuses,
			"WorkflowStatuses":      wf,
			"UseStatusForDoneRatio": a.useStatusForDoneRatio(),
		}, httpx.IsXHR(c.R))
	case "xml", "json":
		a.renderIssueStatusesAPI(c, statuses)
	default:
		c.unknownFormat()
	}
}

// IssueStatusesNew は issue_statuses#new。
func (a *App) IssueStatusesNew(c *Req) {
	a.renderIssueStatusForm(c, "issue_statuses/new", newIssueStatusForm(c, &domain.IssueStatus{}))
}

// IssueStatusesCreate は issue_statuses#create。
func (a *App) IssueStatusesCreate(c *Req) {
	f := newIssueStatusForm(c, &domain.IssueStatus{})
	f.assign(c.Params().Map("issue_status"))
	if err := a.validateIssueStatus(c, f); err != nil {
		a.internalError(c, "validate issue status", err)
		return
	}
	if !f.errs.Any() {
		if err := a.saveIssueStatus(c, f, nil); err != nil {
			a.internalError(c, "save issue status", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_create"))
		c.Redirect("/issue_statuses")
		return
	}
	a.renderIssueStatusForm(c, "issue_statuses/new", f)
}

// IssueStatusesEdit は issue_statuses#edit。
func (a *App) IssueStatusesEdit(c *Req) {
	s := a.findIssueStatus(c)
	if s == nil {
		return
	}
	a.renderIssueStatusForm(c, "issue_statuses/edit", newIssueStatusForm(c, s))
}

// IssueStatusesUpdate は issue_statuses#update。
func (a *App) IssueStatusesUpdate(c *Req) {
	orig := a.findIssueStatus(c)
	if orig == nil {
		return
	}
	cp := *orig
	f := newIssueStatusForm(c, &cp)
	f.assign(c.Params().Map("issue_status"))
	format := httpx.Negotiate(c.R, "html", "js")
	if err := a.validateIssueStatus(c, f); err != nil {
		a.internalError(c, "validate issue status", err)
		return
	}
	if !f.errs.Any() {
		if err := a.saveIssueStatus(c, f, orig); err != nil {
			a.internalError(c, "save issue status", err)
			return
		}
		if format == "js" {
			c.head(http.StatusOK)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.Redirect(pagePath(c, "/issue_statuses"))
		return
	}
	if format == "js" {
		c.head(http.StatusUnprocessableEntity)
		return
	}
	a.renderIssueStatusForm(c, "issue_statuses/edit", f)
}

// IssueStatusesDestroy は issue_statuses#destroy。
func (a *App) IssueStatusesDestroy(c *Req) {
	s := a.findIssueStatus(c)
	if s == nil {
		return
	}
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.DestroyIssueStatus(c.Ctx(), tx, s) })
	if err != nil {
		var inUse *repository.ErrIssueStatusInUse
		if !errors.As(err, &inUse) {
			a.logger().Error("destroy issue status", "err", err)
		}
		c.Flash().SetError(c.L("error_unable_delete_issue_status", rails.EscapeString(err.Error())))
	}
	c.Redirect("/issue_statuses")
}

// IssueStatusesUpdateIssueDoneRatio は issue_statuses#update_issue_done_ratio（POST）。
func (a *App) IssueStatusesUpdateIssueDoneRatio(c *Req) {
	if a.useStatusForDoneRatio() {
		if err := repository.UpdateIssueDoneRatios(c.Ctx(), a.DB); err != nil {
			a.internalError(c, "update done ratios", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_issue_done_ratios_updated"))
	} else {
		c.Flash().SetError(c.L("error_issue_done_ratios_not_updated"))
	}
	c.Redirect("/issue_statuses")
}
