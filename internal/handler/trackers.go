package handler

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// TrackersController（app/controllers/trackers_controller.rb）。
var TrackersController = &Controller{Name: "trackers", MainMenu: false}

// routesTrackers は trackers コントローラのルートを登録する。
//
//	resources :trackers, :except => :show do
//	  collection do
//	    match 'fields', :via => [:get, :post]
//	  end
//	end
func (a *App) routesTrackers(r Router) {
	// before_action :require_admin, :except => :index
	// before_action :require_admin_or_api_request, :only => :index
	// accept_api_auth :index
	a.Handle(r, http.MethodGet, "/trackers/fields", TrackersController, "fields", a.TrackersFields, RequireAdmin())
	a.Handle(r, http.MethodPost, "/trackers/fields", TrackersController, "fields", a.TrackersFields, RequireAdmin())
	a.Handle(r, http.MethodGet, "/trackers", TrackersController, "index", a.TrackersIndex, RequireAdminOrAPIRequest(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/trackers", TrackersController, "create", a.TrackersCreate, RequireAdmin())
	a.Handle(r, http.MethodGet, "/trackers/new", TrackersController, "new", a.TrackersNew, RequireAdmin())
	a.Handle(r, http.MethodGet, "/trackers/{id}/edit", TrackersController, "edit", a.TrackersEdit, RequireAdmin())
	a.Handle(r, http.MethodPatch, "/trackers/{id}", TrackersController, "update", a.TrackersUpdate, RequireAdmin())
	a.Handle(r, http.MethodPut, "/trackers/{id}", TrackersController, "update", a.TrackersUpdate, RequireAdmin())
	a.Handle(r, http.MethodDelete, "/trackers/{id}", TrackersController, "destroy", a.TrackersDestroy, RequireAdmin())
}

// trackerForm は trackers/_form のモデル（@tracker）。
type trackerForm struct {
	formModel
	*domain.Tracker
}

func newTrackerForm(c *Req, t *domain.Tracker) *trackerForm {
	return &trackerForm{formModel: newFormModel(c, "tracker", t.ID), Tracker: t}
}

// Description は description（テキストエリアの値）。
func (f *trackerForm) Description() any {
	if f.Tracker.Description == nil {
		return nil
	}
	return *f.Tracker.Description
}

// DefaultStatusID は default_status_id（未設定なら nil）。
func (f *trackerForm) DefaultStatusID() any {
	if f.Tracker.DefaultStatusID == 0 {
		return nil
	}
	return f.Tracker.DefaultStatusID
}

// HasDefaultStatus は !@tracker.default_status.nil?。
func (f *trackerForm) HasDefaultStatus() bool { return f.Tracker.DefaultStatusID != 0 }

// CoreFieldEnabled は @tracker.core_fields.include?(field)。
func (f *trackerForm) CoreFieldEnabled(field string) bool {
	return slices.Contains(f.CoreFields(), field)
}

// HasCustomField は @tracker.custom_fields.include?(field)。
func (f *trackerForm) HasCustomField(id int64) bool { return slices.Contains(f.CustomFieldIDs, id) }

// assign は @tracker.safe_attributes = params[:tracker]。
func (f *trackerForm) assign(p *httpx.Params) {
	if p == nil {
		return
	}
	t := f.Tracker
	if v, ok := p.StringOK("name"); ok {
		t.Name = v
	}
	if v, ok := p.StringOK("default_status_id"); ok {
		if id := optionalID(v); id != nil {
			t.DefaultStatusID = *id
		} else {
			t.DefaultStatusID = 0
		}
	}
	if v, ok := p.StringOK("is_in_roadmap"); ok {
		t.IsInRoadmap = castBool(v)
	}
	if p.Has("core_fields") {
		t.SetCoreFields(p.Strings("core_fields"))
	}
	if v, ok := p.StringOK("position"); ok {
		t.Position = int(httpx.RubyToI(v))
	}
	if p.Has("custom_field_ids") {
		t.CustomFieldIDs = paramIDs(p.Strings("custom_field_ids"))
	}
	if p.Has("project_ids") {
		t.ProjectIDs = paramIDs(p.Strings("project_ids"))
	}
	if v, ok := p.StringOK("description"); ok {
		t.Description = &v
	}
}

// copyFrom は Tracker#copy_from（id / name / position 以外の属性・カスタムフィールド・プロジェクト）。
func (f *trackerForm) copyFrom(src *domain.Tracker) {
	t := f.Tracker
	t.DefaultStatusID = src.DefaultStatusID
	t.IsInRoadmap = src.IsInRoadmap
	t.DisabledCoreFields = slices.Clone(src.DisabledCoreFields)
	t.Description = src.Description
	t.CustomFieldIDs = slices.Clone(src.CustomFieldIDs)
	t.ProjectIDs = slices.Clone(src.ProjectIDs)
}

// validateTracker は Tracker のバリデーション（default_status, name の必須・一意・長さ、description の長さ）。
func (a *App) validateTracker(c *Req, f *trackerForm) error {
	f.errs = &domain.ValidationErrors{}
	t := f.Tracker
	if t.DefaultStatusID == 0 {
		f.errs.Add("default_status", "blank", nil)
	} else if _, err := repository.GetIssueStatus(c.Ctx(), a.DB, t.DefaultStatusID); err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		f.errs.Add("default_status", "blank", nil)
	}
	if strings.TrimSpace(t.Name) == "" {
		f.errs.Add("name", "blank", nil)
	}
	taken, err := repository.TrackerNameTaken(c.Ctx(), a.DB, t.Name, t.ID)
	if err != nil {
		return err
	}
	if taken {
		f.errs.Add("name", "taken", nil)
	}
	if len([]rune(t.Name)) > 30 {
		f.errs.Add("name", "too_long", map[string]any{"count": 30})
	}
	if len([]rune(t.DescriptionString())) > 255 {
		f.errs.Add("description", "too_long", map[string]any{"count": 255})
	}
	return nil
}

// saveTracker はトラッカーと関連を保存する。
func (a *App) saveTracker(c *Req, f *trackerForm, oldPos *int, copyWorkflowFrom *int64) error {
	return a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		ctx := c.Ctx()
		t := f.Tracker
		isNew := t.ID == 0
		if isNew && t.Position == 0 {
			pos, err := repository.NextPosition(ctx, tx, repository.TrackerPositionScope)
			if err != nil {
				return err
			}
			t.Position = pos
		}
		if err := repository.SaveTracker(ctx, tx, t); err != nil {
			return err
		}
		f.id = t.ID
		if isNew {
			if err := repository.InsertPosition(ctx, tx, repository.TrackerPositionScope, t.ID, t.Position); err != nil {
				return err
			}
		} else if oldPos != nil && *oldPos != t.Position {
			if err := repository.ShiftPositions(ctx, tx, repository.TrackerPositionScope, t.ID, *oldPos, t.Position); err != nil {
				return err
			}
		}
		if err := repository.SetTrackerProjects(ctx, tx, t.ID, t.ProjectIDs); err != nil {
			return err
		}
		if err := repository.SetTrackerCustomFields(ctx, tx, t.ID, t.CustomFieldIDs); err != nil {
			return err
		}
		if copyWorkflowFrom != nil {
			if err := repository.CopyWorkflowRules(ctx, tx, copyWorkflowFrom, nil, []int64{t.ID}, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// coreFieldItem は標準フィールドの表示（l("field_#{field}".delete_suffix('_id'))）。
type coreFieldItem struct {
	Name  string
	Label string
}

func coreFieldItems(c *Req) []coreFieldItem {
	out := make([]coreFieldItem, len(domain.TrackerCoreFields))
	for i, f := range domain.TrackerCoreFields {
		out[i] = coreFieldItem{Name: f, Label: c.L("field_" + strings.TrimSuffix(f, "_id"))}
	}
	return out
}

func (a *App) renderTrackerForm(c *Req, tmpl string, f *trackerForm, copyFrom *domain.Tracker) {
	ctx := c.Ctx()
	statuses, err := repository.ListIssueStatuses(ctx, a.DB)
	if err != nil {
		a.internalError(c, "list statuses", err)
		return
	}
	opts := make([][]any, len(statuses))
	for i, s := range statuses {
		opts[i] = []any{s.Name, s.ID}
	}
	cfs, err := repository.CustomFieldInfosByKind(ctx, a.DB, "issue")
	if err != nil {
		a.internalError(c, "custom fields", err)
		return
	}
	projects, err := repository.ListProjects(ctx, a.DB)
	if err != nil {
		a.internalError(c, "list projects", err)
		return
	}
	data := map[string]any{
		"Tracker":           f,
		"StatusOptions":     opts,
		"CoreFields":        coreFieldItems(c),
		"IssueCustomFields": cfs,
		"Projects":          projects,
	}
	if f.ID == 0 {
		trackers, err := repository.ListTrackers(ctx, a.DB)
		if err != nil {
			a.internalError(c, "list trackers", err)
			return
		}
		data["CopyTrackers"] = trackers
		var sel any
		if v := c.Params().String("copy_workflow_from"); v != "" {
			sel = v
		} else if copyFrom != nil {
			sel = copyFrom.ID
		}
		data["CopyWorkflowFrom"] = sel
	}
	c.renderAdmin(tmpl, data, false)
}

// findTracker は Tracker.find(params[:id])（無ければ 404）。
func (a *App) findTracker(c *Req) *domain.Tracker {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return nil
	}
	t, err := repository.GetTracker(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find tracker", err)
		}
		return nil
	}
	return t
}

// ---------------------------------------------------------------- アクション

// TrackersIndex は trackers#index（GET /trackers）。
func (a *App) TrackersIndex(c *Req) {
	ctx := c.Ctx()
	trackers, err := repository.ListTrackers(ctx, a.DB)
	if err != nil {
		a.internalError(c, "list trackers", err)
		return
	}
	switch httpx.Negotiate(c.R, "html", "xml", "json") {
	case "html":
		a.renderTrackersIndex(c, trackers, httpx.IsXHR(c.R))
	case "xml", "json":
		statuses, err := a.statusesByID(c)
		if err != nil {
			a.internalError(c, "list statuses", err)
			return
		}
		a.renderTrackersAPI(c, trackers, statuses)
	default:
		c.unknownFormat()
	}
}

func (a *App) statusesByID(c *Req) (map[int64]*domain.IssueStatus, error) {
	statuses, err := repository.ListIssueStatuses(c.Ctx(), a.DB)
	if err != nil {
		return nil, err
	}
	out := map[int64]*domain.IssueStatus{}
	for _, s := range statuses {
		out[s.ID] = s
	}
	return out, nil
}

func (a *App) renderTrackersIndex(c *Req, trackers []*domain.Tracker, noLayout bool) {
	statuses, err := a.statusesByID(c)
	if err != nil {
		a.internalError(c, "list statuses", err)
		return
	}
	names := map[int64]string{}
	for id, s := range statuses {
		names[id] = s.Name
	}
	wf, err := repository.TrackerIDsWithWorkflow(c.Ctx(), a.DB)
	if err != nil {
		a.internalError(c, "tracker workflows", err)
		return
	}
	c.renderAdmin("trackers/index", map[string]any{"Trackers": trackers, "StatusNames": names, "WorkflowTrackers": wf}, noLayout)
}

// newTracker は Tracker.new(:default_status => IssueStatus.sorted.first)。
func (a *App) newTracker(c *Req, withDefaultStatus bool) (*domain.Tracker, error) {
	t := &domain.Tracker{IsInRoadmap: true, DisabledCoreFields: []string{}}
	if withDefaultStatus {
		statuses, err := repository.ListIssueStatuses(c.Ctx(), a.DB)
		if err != nil {
			return nil, err
		}
		if len(statuses) > 0 {
			t.DefaultStatusID = statuses[0].ID
		}
	}
	return t, nil
}

// trackersNew は TrackersController#new の本体（create の失敗時にも同じ @tracker で呼ばれる）。
func (a *App) trackersNew(c *Req, f *trackerForm) {
	f.assign(c.Params().Map("tracker"))
	var copyFrom *domain.Tracker
	if v := c.Params().String("copy"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			if src, err := repository.GetTracker(c.Ctx(), a.DB, id); err == nil {
				copyFrom = src
				f.copyFrom(src)
			}
		}
	}
	a.renderTrackerForm(c, "trackers/new", f, copyFrom)
}

// TrackersNew は trackers#new（GET /trackers/new）。
func (a *App) TrackersNew(c *Req) {
	t, err := a.newTracker(c, true)
	if err != nil {
		a.internalError(c, "new tracker", err)
		return
	}
	a.trackersNew(c, newTrackerForm(c, t))
}

// TrackersCreate は trackers#create（POST /trackers）。
func (a *App) TrackersCreate(c *Req) {
	t, _ := a.newTracker(c, false)
	f := newTrackerForm(c, t)
	f.assign(c.Params().Map("tracker"))
	if err := a.validateTracker(c, f); err != nil {
		a.internalError(c, "validate tracker", err)
		return
	}
	if !f.errs.Any() {
		var copyFrom *int64
		if v := c.Params().String("copy_workflow_from"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				if _, err := repository.GetTracker(c.Ctx(), a.DB, id); err == nil {
					copyFrom = &id
				}
			}
		}
		if err := a.saveTracker(c, f, nil, copyFrom); err != nil {
			a.internalError(c, "save tracker", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_create"))
		c.Redirect("/trackers")
		return
	}
	a.trackersNew(c, f)
}

// TrackersEdit は trackers#edit（GET /trackers/:id/edit）。
func (a *App) TrackersEdit(c *Req) {
	t := a.findTracker(c)
	if t == nil {
		return
	}
	a.renderTrackerForm(c, "trackers/edit", newTrackerForm(c, t), nil)
}

// TrackersUpdate は trackers#update（PATCH/PUT /trackers/:id）。
func (a *App) TrackersUpdate(c *Req) {
	t := a.findTracker(c)
	if t == nil {
		return
	}
	oldPos := t.Position
	f := newTrackerForm(c, t)
	f.assign(c.Params().Map("tracker"))
	format := httpx.Negotiate(c.R, "html", "js")
	if err := a.validateTracker(c, f); err != nil {
		a.internalError(c, "validate tracker", err)
		return
	}
	if !f.errs.Any() {
		if err := a.saveTracker(c, f, &oldPos, nil); err != nil {
			a.internalError(c, "save tracker", err)
			return
		}
		if format == "js" {
			c.head(http.StatusOK)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.Redirect(pagePath(c, "/trackers"))
		return
	}
	if format == "js" {
		c.head(http.StatusUnprocessableEntity)
		return
	}
	a.renderTrackerForm(c, "trackers/edit", f, nil)
}

// TrackersDestroy は trackers#destroy（DELETE /trackers/:id）。
func (a *App) TrackersDestroy(c *Req) {
	t := a.findTracker(c)
	if t == nil {
		return
	}
	ctx := c.Ctx()
	used, err := repository.TrackerIssuesExist(ctx, a.DB, t.ID)
	if err != nil {
		a.internalError(c, "tracker issues", err)
		return
	}
	if !used {
		if err := a.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.DestroyTracker(ctx, tx, t) }); err != nil {
			a.internalError(c, "destroy tracker", err)
			return
		}
		c.Redirect("/trackers")
		return
	}
	projects, err := repository.ProjectsWithTrackerIssues(ctx, a.DB, t.ID)
	if err != nil {
		a.internalError(c, "tracker projects", err)
		return
	}
	links := make([]string, len(projects))
	for i, p := range projects {
		q := url.Values{"set_filter": {"1"}, "tracker_id": {strconv.FormatInt(t.ID, 10)}, "status_id": {"*"}}
		links[i] = string(rails.LinkTo(p.Name, "/projects/"+p.Identifier+"/issues?"+q.Encode(), nil))
	}
	c.Flash().Now("error", c.L("error_can_not_delete_tracker_html", map[string]any{"projects": strings.Join(links, ", ")}))
	trackers, err := repository.ListTrackers(ctx, a.DB)
	if err != nil {
		a.internalError(c, "list trackers", err)
		return
	}
	a.renderTrackersIndex(c, trackers, false)
}

// TrackersFields は trackers#fields（GET / POST /trackers/fields）。
func (a *App) TrackersFields(c *Req) {
	ctx := c.Ctx()
	if c.R.Method == http.MethodPost {
		if m := c.Params().Map("trackers"); m != nil {
			err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
				for _, k := range m.Keys() {
					id, err := strconv.ParseInt(k, 10, 64)
					if err != nil {
						continue
					}
					t, err := repository.GetTracker(ctx, tx, id)
					if errors.Is(err, repository.ErrNotFound) {
						continue
					} else if err != nil {
						return err
					}
					tp := m.Map(k)
					t.SetCoreFields(tp.Strings("core_fields"))
					if err := repository.SaveTracker(ctx, tx, t); err != nil {
						return err
					}
					if err := repository.SetTrackerCustomFields(ctx, tx, t.ID, paramIDs(tp.Strings("custom_field_ids"))); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				a.internalError(c, "update tracker fields", err)
				return
			}
			c.Flash().SetNotice(c.L("notice_successful_update"))
			c.Redirect("/trackers/fields")
			return
		}
	}
	trackers, err := repository.ListTrackers(ctx, a.DB)
	if err != nil {
		a.internalError(c, "list trackers", err)
		return
	}
	cft, err := repository.CustomFieldsTrackers(ctx, a.DB)
	if err != nil {
		a.internalError(c, "custom fields trackers", err)
		return
	}
	forms := make([]*trackerForm, len(trackers))
	for i, t := range trackers {
		for id := range cft[t.ID] {
			t.CustomFieldIDs = append(t.CustomFieldIDs, id)
		}
		forms[i] = newTrackerForm(c, t)
	}
	cfs, err := repository.CustomFieldInfosByKind(ctx, a.DB, "issue")
	if err != nil {
		a.internalError(c, "custom fields", err)
		return
	}
	c.renderAdmin("trackers/fields", map[string]any{
		"Trackers":     forms,
		"CoreFields":   coreFieldItems(c),
		"CustomFields": cfs,
	}, false)
}
