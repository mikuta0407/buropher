package handler

import (
	"context"
	"errors"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
	"github.com/mikuta0407/buropher/web"
)

// VersionsController（app/controllers/versions_controller.rb）。menu_item :roadmap。
var VersionsController = &Controller{Name: "versions", MainMenu: true,
	MenuItem: func(string) string { return "roadmap" }}

// versionSharings は Version::VERSION_SHARINGS。
var versionSharings = []string{"none", "descendants", "hierarchy", "tree", "system"}

// versionStatuses は Version::VERSION_STATUSES。
var versionStatuses = []string{"open", "locked", "closed"}

type versionKey struct{}

// routesVersions は versions コントローラのルートを登録する。
//
//	resources :projects do
//	  resources :versions, :except => [:index, :show, :edit, :update, :destroy] do
//	    collection do put 'close_completed' end
//	  end
//	  get 'versions.:format', :to => 'versions#index'
//	  get 'roadmap', :to => 'versions#index', :format => false
//	  get 'versions', :to => 'versions#index'
//	end
//	resources :versions, :only => [:show, :edit, :update, :destroy] do
//	  post 'status_by', :on => :member
//	end
func (a *App) routesVersions(r Router) {
	// before_action :find_project_by_project_id, :only => [:index, :new, :create, :close_completed]
	// before_action :authorize / accept_api_auth :index, :show, :create, :update, :destroy
	proj := []ActionOption{FindProjectByProjectID(), Authorize()}
	member := []ActionOption{a.findVersion(), Authorize()}
	api := AcceptAPIAuth()
	a.Handle(r, http.MethodPut, "/projects/{project_id}/versions/close_completed", VersionsController, "close_completed", a.VersionsCloseCompleted, proj...)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/versions/new", VersionsController, "new", a.VersionsNew, proj...)
	a.Handle(r, http.MethodPost, "/projects/{project_id}/versions", VersionsController, "create", a.VersionsCreate, append(proj, api)...)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/roadmap", VersionsController, "index", a.VersionsIndex, append(proj, api)...)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/versions", VersionsController, "index", a.VersionsIndex, append(proj, api)...)
	a.Handle(r, http.MethodGet, "/versions/{id}", VersionsController, "show", a.VersionsShow, append(member, api)...)
	a.Handle(r, http.MethodGet, "/versions/{id}/edit", VersionsController, "edit", a.VersionsEdit, member...)
	a.Handle(r, http.MethodPatch, "/versions/{id}", VersionsController, "update", a.VersionsUpdate, append(member, api)...)
	a.Handle(r, http.MethodPut, "/versions/{id}", VersionsController, "update", a.VersionsUpdate, append(member, api)...)
	a.Handle(r, http.MethodDelete, "/versions/{id}", VersionsController, "destroy", a.VersionsDestroy, append(member, api)...)
	a.Handle(r, http.MethodPost, "/versions/{id}/status_by", VersionsController, "status_by", a.VersionsStatusBy, member...)
}

// findVersion は find_model_object + find_project_from_association。
func (a *App) findVersion() ActionOption {
	return Before(func(c *Req) {
		id, ok := c.Params().IntStrict("id")
		if !ok {
			c.Render404("")
			return
		}
		v, err := repository.GetVersion(c.Ctx(), a.DB, id)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.Render404("")
			} else {
				a.internalError(c, "find version", err)
			}
			return
		}
		p, err := repository.GetProject(c.Ctx(), a.DB, v.ProjectID)
		if err != nil {
			a.internalError(c, "find version project", err)
			return
		}
		c.Project = p
		c.setValue(versionKey{}, v)
	})
}

func (c *Req) version() *domain.Version {
	v, _ := c.value(versionKey{}).(*domain.Version)
	return v
}

// ---------------------------------------------------------------- index（ロードマップ）

// roadmapVersion はロードマップの 1 バージョン。
type roadmapVersion struct {
	*versionModel
	Editable bool
	Anchor   string
	Link     template.HTML
	Overview *versionOverview
	Issues   []*relatedIssueRow
	WikiHTML template.HTML
}

// VersionsIndex は versions#index。
func (a *App) VersionsIndex(c *Req) {
	ctx := c.Ctx()
	if strings.Contains(c.R.URL.Path, "/roadmap.") {
		// get 'roadmap', :format => false（ルーティングエラー）
		versionsRoutingError(c)
		return
	}
	switch httpx.Negotiate(c.R, "html", "json", "xml") {
	case "json", "xml":
		vs, err := repository.LoadVersionsWhere(ctx, a.DB, repository.SharedVersionsCondition(c.Project))
		if err != nil {
			a.internalError(c, "shared versions", err)
			return
		}
		a.renderVersionsIndexAPI(c, vs)
		return
	case "html":
	default:
		c.unknownFormat()
		return
	}
	data, err := a.roadmapData(c)
	if err != nil {
		a.internalError(c, "roadmap", err)
		return
	}
	c.Render("versions/index", data)
}

func (a *App) roadmapData(c *Req) (map[string]any, error) {
	ctx := c.Ctx()
	p := c.Project
	vc, err := a.newVersionCtx(c)
	if err != nil {
		return nil, err
	}
	env := issues.NewEnv(a.DB, a.Settings, c.User)
	trackers, err := env.ProjectTrackers(ctx, p)
	if err != nil {
		return nil, err
	}
	// retrieve_selected_tracker_ids(@trackers, @trackers.select {|t| t.is_in_roadmap?})
	var selected []string
	if v, ok := c.Params().Get("tracker_ids"); ok {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				selected = append(selected, strconv.FormatInt(httpx.RubyToI(httpx.ValueString(e)), 10))
			}
		default:
			for _, s := range strings.Split(httpx.ValueString(x), "/") {
				selected = append(selected, strconv.FormatInt(httpx.RubyToI(s), 10))
			}
		}
	} else {
		for _, t := range trackers {
			if t.IsInRoadmap {
				selected = append(selected, strconv.FormatInt(t.ID, 10))
			}
		}
	}
	withSub := a.Settings.Bool("display_subprojects_issues")
	if v, ok := c.Params().StringOK("with_subprojects"); ok {
		withSub = v == "1"
	}
	projectIDs := []int64{p.ID}
	if withSub {
		if projectIDs, err = repository.ProjectSelfAndDescendantIDs(ctx, a.DB, p.ID); err != nil {
			return nil, err
		}
	}
	vs, err := repository.LoadVersionsWhere(ctx, a.DB, repository.SharedVersionsCondition(p))
	if err != nil {
		return nil, err
	}
	if withSub {
		vis, err := c.Authz().AllowedToCondition(ctx, "view_issues", authz.ConditionOptions{}, nil)
		if err != nil {
			return nil, err
		}
		rolled, err := repository.LoadVersionsWhere(ctx, a.DB, repository.RolledUpVersionsCondition(p)+" AND ("+vis+")")
		if err != nil {
			return nil, err
		}
		vs = append(vs, rolled...)
	}
	// uniq.sort
	var uniq []*domain.Version
	for _, v := range vs {
		if !slices.ContainsFunc(uniq, func(o *domain.Version) bool { return o.ID == v.ID }) {
			uniq = append(uniq, v)
		}
	}
	slices.SortStableFunc(uniq, domain.CompareVersions)
	if err := vc.preload(uniq); err != nil {
		return nil, err
	}
	models := make([]*versionModel, 0, len(uniq))
	for _, v := range uniq {
		m, err := vc.model(v)
		if err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	var completed []*versionModel
	if !c.Params().Has("completed") {
		var rest []*versionModel
		for _, m := range models {
			if m.Completed() {
				completed = append(completed, m)
			} else {
				rest = append(rest, m)
			}
		}
		slices.Reverse(completed)
		models = rest
	}
	issuesByVersion := map[int64][]*repository.VersionIssue{}
	if len(selected) > 0 && len(models) > 0 {
		var vids []int64
		for _, m := range models {
			vids = append(vids, m.ID)
		}
		var tids []string
		for _, s := range selected {
			tids = append(tids, strconv.Quote(s)[1:len(strconv.Quote(s))-1])
		}
		extra := "issues.tracker_id IN (" + strings.Join(tids, ",") + ") AND issues.project_id IN (" + joinInt64(projectIDs) + ")"
		list, err := repository.VersionIssues(ctx, a.DB, vids, vc.issueVis, extra, "issues.id")
		if err != nil {
			return nil, err
		}
		// order("projects.lft, trackers.position, issues.id")
		ns, err := repository.ProjectNestedSet(ctx, a.DB)
		if err != nil {
			return nil, err
		}
		slices.SortStableFunc(list, func(x, y *repository.VersionIssue) int {
			if lx, ly := ns[x.ProjectID].Lft, ns[y.ProjectID].Lft; lx != ly {
				return cmpInt(lx, ly)
			}
			if x.TrackerPos != y.TrackerPos {
				return cmpInt(x.TrackerPos, y.TrackerPos)
			}
			return cmpInt64(x.ID, y.ID)
		})
		for _, i := range list {
			id := i.RefIssue.ID
			_ = id
			fv := versionIDOfIssue(i)
			issuesByVersion[fv] = append(issuesByVersion[fv], i)
		}
	}
	var rows []*roadmapVersion
	for _, m := range models {
		if !slices.Contains(projectIDs, m.ProjectID) && len(issuesByVersion[m.ID]) == 0 {
			continue
		}
		editable, err := c.Authz().AllowedTo(ctx, domain.Perm("manage_versions"), m.Project)
		if err != nil {
			return nil, err
		}
		ov, err := vc.overview(m)
		if err != nil {
			return nil, err
		}
		irows, err := vc.relatedIssueRows(issuesByVersion[m.ID])
		if err != nil {
			return nil, err
		}
		anchor := versionAnchor(p, m)
		wiki, err := vc.wikiContent(m)
		if err != nil {
			return nil, err
		}
		rows = append(rows, &roadmapVersion{versionModel: m, Editable: editable, Anchor: anchor,
			Link: vc.linkToVersion(m, anchor), Overview: ov, Issues: irows, WikiHTML: wiki})
	}
	var completedLinks []template.HTML
	for _, m := range completed {
		completedLinks = append(completedLinks, vc.linkToVersion(m, ""))
	}
	manage, err := c.Authz().AllowedTo(ctx, domain.Perm("manage_versions"), p)
	if err != nil {
		return nil, err
	}
	settingsAllowed := manage && c.Authorize2("projects", "settings")
	// @project.descendants.allowed_to(:view_issues).any?
	vis, err := c.Authz().AllowedToCondition(ctx, "view_issues", authz.ConditionOptions{}, nil)
	if err != nil {
		return nil, err
	}
	hasSub, err := repository.ProjectDescendantsExist(ctx, a.DB, p.ID, vis)
	if err != nil {
		return nil, err
	}
	type trackerOpt struct {
		ID       int64
		Name     string
		Selected bool
	}
	var topts []trackerOpt
	for _, t := range trackers {
		topts = append(topts, trackerOpt{ID: t.ID, Name: t.Name, Selected: slices.Contains(selected, strconv.FormatInt(t.ID, 10))})
	}
	var sideVersions []map[string]any
	for _, r := range rows {
		sideVersions = append(sideVersions, map[string]any{"Name": formatVersionName(p, r.versionModel), "Anchor": r.Anchor})
	}
	return map[string]any{
		"Versions":          rows,
		"Manage":            manage,
		"SettingsAllowed":   settingsAllowed,
		"Trackers":          topts,
		"Completed":         c.Params().Has("completed"),
		"WithSubprojects":   withSub,
		"HasSubprojects":    hasSub,
		"SideVersions":      sideVersions,
		"CompletedVersions": completedLinks,
		"FormAction":        c.R.URL.Path,
	}, nil
}

// versionIDOfIssue はチケットの fixed_version_id（VersionIssues は対象バージョンで絞るので必ずある）。
func versionIDOfIssue(i *repository.VersionIssue) int64 { return i.FixedVersionID }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Authorize2 は authorize_for(controller, action)（@project で判定。描画せずに真偽値を返す）。
func (c *Req) Authorize2(ctrl, action string) bool {
	return c.allowedToActionIn(ctrl, action, c.Project)
}

// allowedToActionIn は User.current.allowed_to?({:controller => ctrl, :action => action}, p)。
func (c *Req) allowedToActionIn(ctrl, action string, p *domain.Project) bool {
	ok, err := c.Authz().AllowedTo(c.Ctx(), domain.ControllerAction(ctrl, action), p)
	if err != nil {
		c.App.logger().Error("authorize_for", "err", err)
	}
	return ok
}

// ---------------------------------------------------------------- show

// VersionsShow は versions#show（html / api / txt）。
func (a *App) VersionsShow(c *Req) {
	v := c.version()
	switch httpx.Negotiate(c.R, "html", "json", "xml", "text") {
	case "json", "xml":
		a.renderVersionShowAPI(c, v, 0)
	case "text":
		a.versionsShowText(c, v)
	case "html":
		a.renderVersionShow(c, v, "show")
	default:
		c.unknownFormat()
	}
}

func (a *App) renderVersionShow(c *Req, v *domain.Version, action string) {
	data, err := a.versionShowData(c, v)
	if err != nil {
		a.internalError(c, "version show", err)
		return
	}
	if action == "status_by" {
		// status_by は @issues を読み込まずに show を描画する
		delete(data, "Issues")
	}
	c.Render("versions/show", data)
}

func (a *App) versionShowData(c *Req, v *domain.Version) (map[string]any, error) {
	ctx := c.Ctx()
	vc, err := a.newVersionCtx(c)
	if err != nil {
		return nil, err
	}
	m, err := vc.model(v)
	if err != nil {
		return nil, err
	}
	ov, err := vc.overview(m)
	if err != nil {
		return nil, err
	}
	// @version.fixed_issues.visible.reorder("trackers.position, issues.id")
	list, err := repository.VersionIssues(ctx, a.DB, []int64{v.ID}, vc.issueVis, "", "trackers.position, issues.id")
	if err != nil {
		return nil, err
	}
	irows, err := vc.relatedIssueRows(list)
	if err != nil {
		return nil, err
	}
	manage, err := c.Authz().AllowedTo(ctx, domain.Perm("manage_versions"), m.Project)
	if err != nil {
		return nil, err
	}
	wiki, err := vc.wikiContent(m)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"Version": m, "Overview": ov, "Issues": irows, "Manage": manage, "WikiHTML": wiki}
	if t := m.WikiPageTitleString(); t != "" {
		hasWiki, err := repository.ProjectWikiExists(ctx, a.DB, m.ProjectID)
		if err != nil {
			return nil, err
		}
		if hasWiki && c.allowedToActionIn("wiki", "edit", m.Project) {
			data["WikiEditLink"] = rails.LinkTo(a.Helpers.SpriteIconHTML(vc.page(), "edit", c.L("button_edit_associated_wikipage", map[string]any{"page_title": t})),
				"/projects/"+m.Project.Identifier+"/wiki/"+wikiTitleParam(t)+"/edit", rails.NewHash("class", "icon icon-edit"))
		}
	}
	if manage {
		data["DeletePath"] = "/versions/" + strconv.FormatInt(v.ID, 10) + "?back_url=" + url.QueryEscape(urlroot.Path("/projects/"+m.Project.Identifier+"/roadmap"))
	}
	newIssue, err := vc.linkToNewIssue(m, c.Project)
	if err != nil {
		return nil, err
	}
	data["NewIssueLink"] = newIssue
	// 工数
	est, rem, err := repository.VersionEstimatedHours(ctx, a.DB, v.ID, vc.issueVis)
	if err != nil {
		return nil, err
	}
	viewTE, err := c.Authz().AllowedTo(ctx, domain.Perm("view_time_entries"), c.Project)
	if err != nil {
		return nil, err
	}
	if est > 0 || viewTE {
		pid := m.Project.Identifier
		vid := strconv.FormatInt(v.ID, 10)
		tt := map[string]any{
			"EstimatedLink": rails.LinkTo(versionHTMLHours(c.Loc.LHours(est)), "/projects/"+pid+"/issues?"+versionQuery(
				"c", []string{"tracker", "status", "subject", "estimated_hours"}, "fixed_version_id", vid, "set_filter", 1, "status_id", "*", "t", []string{"estimated_hours"}), nil),
			"RemainingLink": rails.LinkTo(versionHTMLHours(c.Loc.LHours(rem)), "/projects/"+pid+"/issues?"+versionQuery(
				"c", []string{"tracker", "status", "subject", "estimated_remaining_hours"}, "fixed_version_id", vid, "set_filter", 1, "status_id", "*", "t", []string{"estimated_remaining_hours"}), nil),
		}
		all, err := c.Authz().AllowedToViewAllTimeEntries(ctx, c.Project)
		if err != nil {
			return nil, err
		}
		if all {
			spent, err := repository.VersionSpentHours(ctx, a.DB, v.ID)
			if err != nil {
				return nil, err
			}
			tt["SpentLink"] = rails.LinkTo(versionHTMLHours(c.Loc.LHours(spent)), "/projects/"+pid+"/time_entries?"+versionQuery("issue.fixed_version_id", vid, "set_filter", 1), nil)
		}
		data["TimeTracking"] = tt
	}
	if m.All.Count() > 0 {
		sb, err := vc.renderIssueStatusBy(m, c.Params().String("status_by"))
		if err != nil {
			return nil, err
		}
		data["StatusBy"] = sb
	}
	data["FormPath"] = versionFormPath(c)
	data["TxtURL"] = versionFormPath(c) + ".txt"
	if q := c.R.URL.RawQuery; q != "" {
		data["TxtURL"] = data["TxtURL"].(string) + "?" + railsToQuery(c.R.URL.Query())
	}
	return data, nil
}

// wikiTitleParam は Wiki.titleize(title) を URL のパスにしたもの。
func wikiTitleParam(t string) string {
	return url.PathEscape(wikiTitleize(t))
}

var wikiTitleizeRe = regexp.MustCompile(`[ \t\r\n]+`)

// versionsShowText は format.text（version_to_text）。
func (a *App) versionsShowText(c *Req, v *domain.Version) {
	ctx := c.Ctx()
	vc, err := a.newVersionCtx(c)
	if err != nil {
		a.internalError(c, "version text", err)
		return
	}
	list, err := repository.VersionIssues(ctx, a.DB, []int64{v.ID}, vc.issueVis, "", "issues.id")
	if err != nil {
		a.internalError(c, "version text", err)
		return
	}
	// fixed_issues.visible は順序指定なし（参照環境の SQLite は fixed_version_id の索引順 = id 順）
	parts := []string{"# " + v.Name}
	if v.EffectiveDate != nil {
		parts = append(parts, c.Loc.FormatDate(*v.EffectiveDate))
	}
	if v.Description != nil {
		parts = append(parts, *v.Description)
	}
	var b strings.Builder
	for _, i := range list {
		b.WriteString("* " + i.TrackerName + " #" + strconv.FormatInt(i.ID, 10) + ": " + i.Subject + "\n")
	}
	parts = append(parts, b.String())
	body := strings.Join(parts, "\n\n")
	c.W.Header().Set("Content-Type", "text/plain")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+v.Name+`.txt"; filename*=UTF-8''`+url.PathEscape(v.Name)+".txt")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(body))
	c.Halt()
}

// ---------------------------------------------------------------- status_by

// VersionsStatusBy は versions#status_by（XHR は status_by.js、それ以外は show）。
func (a *App) VersionsStatusBy(c *Req) {
	v := c.version()
	if httpx.Negotiate(c.R, "html", "js") == "js" {
		vc, err := a.newVersionCtx(c)
		if err != nil {
			a.internalError(c, "status_by", err)
			return
		}
		m, err := vc.model(v)
		if err != nil {
			a.internalError(c, "status_by", err)
			return
		}
		sb, err := vc.renderIssueStatusBy(m, c.Params().String("status_by"))
		if err != nil {
			a.internalError(c, "status_by", err)
			return
		}
		c.Render("versions/status_by", map[string]any{"Version": m, "StatusBy": sb}, RenderOptions{Layout: view.NoLayout, Format: "js"})
		return
	}
	a.renderVersionShow(c, v, "status_by")
}

// ---------------------------------------------------------------- フォーム（new / create / edit / update）

// versionForm は versions/_form のモデル（@version）。
type versionForm struct {
	formModel
	V       *domain.Version
	Project *domain.Project
	// effectiveDateRaw は effective_date_before_type_cast（不正な日付の検証用）。
	effectiveDateRaw *string
	// DefaultProjectVersion は default_project_version。
	defaultProjectVersion *bool
	// CustomValues は visible_custom_field_values。
	CustomValues []*domain.CustomFieldValue
	// AllowedSharings は allowed_sharings。
	AllowedSharings []string
	origSharing     string
	cfChanged       bool
	nameSet         bool
}

// Send は rails.Sender（フォームの値）。
func (f *versionForm) Send(method string) (any, bool) {
	v := f.V
	switch method {
	case "name":
		if v.Name == "" && f.id == 0 && !f.nameSet {
			return nil, true
		}
		return v.Name, true
	case "description":
		if v.Description == nil {
			return nil, true
		}
		return *v.Description, true
	case "wiki_page_title":
		if v.WikiPageTitle == nil {
			return nil, true
		}
		return *v.WikiPageTitle, true
	case "effective_date":
		if v.EffectiveDate == nil {
			return nil, true
		}
		return *v.EffectiveDate, true
	case "status":
		return v.Status, true
	case "sharing":
		return v.Sharing, true
	case "default_project_version":
		return f.DefaultProjectVersion(), true
	}
	return nil, false
}

// DefaultProjectVersion は default_project_version（未設定なら project.default_version == self）。
func (f *versionForm) DefaultProjectVersion() bool {
	if f.defaultProjectVersion != nil {
		return *f.defaultProjectVersion
	}
	return f.Project != nil && f.Project.DefaultVersionID != nil && f.V.ID != 0 && *f.Project.DefaultVersionID == f.V.ID
}

// NewRecord は new_record?。
func (f *versionForm) NewRecord() bool { return f.id == 0 }

func (a *App) newVersionForm(c *Req, v *domain.Version, p *domain.Project) (*versionForm, error) {
	f := &versionForm{formModel: newFormModel(c, "version", v.ID), V: v, Project: p, origSharing: v.Sharing}
	f.AllowedSharings = a.allowedSharings(c, v, p)
	cvs, err := a.versionCustomValues(c, v, p, true)
	if err != nil {
		return nil, err
	}
	f.CustomValues = cvs
	return f, nil
}

// allowedSharings は allowed_sharings(User.current)。
func (a *App) allowedSharings(c *Req, v *domain.Version, p *domain.Project) []string {
	var out []string
	for _, s := range versionSharings {
		if v.Sharing == s {
			out = append(out, s)
			continue
		}
		switch s {
		case "system":
			if c.User.IsAdmin() {
				out = append(out, s)
			}
		case "hierarchy", "tree":
			if p == nil {
				out = append(out, s)
				continue
			}
			root := p
			if anc, err := repository.ProjectSelfAndAncestors(c.Ctx(), a.DB, p.ID); err == nil {
				for _, r := range anc {
					if r.ParentID == nil {
						root = r
					}
				}
			}
			if ok, _ := c.Authz().AllowedTo(c.Ctx(), domain.Perm("manage_versions"), root); ok {
				out = append(out, s)
			}
		default:
			out = append(out, s)
		}
	}
	return out
}

var versionDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}( 00:00:00)?$`)

// parseVersionDate は Date の型変換（YYYY-MM-DD のみ。不正なら nil）。
func parseVersionDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	m := regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})`).FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return nil
	}
	return &t
}

// assign は safe_attributes = params[:version]（allowed_sharings に無い sharing は無視する）。
func (f *versionForm) assign(c *Req, p *httpx.Params, checkSharing bool) {
	if p == nil {
		return
	}
	v := f.V
	if s, ok := p.StringOK("sharing"); ok && checkSharing && !slices.Contains(f.AllowedSharings, s) {
		p = p.Except("sharing")
	}
	if s, ok := p.StringOK("name"); ok {
		v.Name = s
		f.nameSet = true
	}
	if s, ok := p.StringOK("description"); ok {
		v.Description = &s
	}
	for _, k := range []string{"effective_date", "due_date"} {
		if s, ok := p.StringOK(k); ok {
			f.effectiveDateRaw = &s
			v.EffectiveDate = parseVersionDate(s)
		}
	}
	if s, ok := p.StringOK("wiki_page_title"); ok {
		v.WikiPageTitle = &s
	}
	if s, ok := p.StringOK("status"); ok {
		v.Status = s
	}
	if s, ok := p.StringOK("sharing"); ok {
		v.Sharing = s
	}
	if s, ok := p.StringOK("default_project_version"); ok {
		b := s == "1"
		f.defaultProjectVersion = &b
	}
	// custom_field_values（見えるフィールドだけ）
	if m := p.Map("custom_field_values"); m != nil {
		for _, cv := range f.CustomValues {
			key := strconv.FormatInt(cv.Field.ID, 10)
			if raw, ok := m.Get(key); ok {
				cv.Values = versionCFValues(raw)
				f.cfChanged = true
			}
		}
	}
	if list := p.Slice("custom_fields"); list != nil {
		for _, e := range list {
			em, ok := e.(*httpx.Params)
			if !ok {
				continue
			}
			for _, cv := range f.CustomValues {
				if em.String("id") == strconv.FormatInt(cv.Field.ID, 10) {
					if raw, ok := em.Get("value"); ok {
						cv.Values = versionCFValues(raw)
						f.cfChanged = true
					}
				}
			}
		}
	}
}

func versionCFValues(raw any) []string {
	switch x := raw.(type) {
	case []any:
		var out []string
		for _, e := range x {
			if s := httpx.ValueString(e); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		s := httpx.ValueString(x)
		if s == "" {
			return nil
		}
		return []string{s}
	}
}

// validate は Version の検証。
func (a *App) validateVersion(c *Req, f *versionForm) error {
	f.errs = &domain.ValidationErrors{}
	v := f.V
	for _, cv := range f.CustomValues {
		if cv.Field.IsRequired && len(cv.Values) == 0 {
			f.errs.AddMessage("base", cv.Field.Name+" "+c.L("activerecord.errors.messages.blank"))
		}
	}
	if strings.TrimSpace(v.Name) == "" {
		f.errs.Add("name", "blank", nil)
	}
	taken, err := repository.VersionNameTaken(c.Ctx(), a.DB, v.ProjectID, v.Name, v.ID)
	if err != nil {
		return err
	}
	if taken {
		f.errs.Add("name", "taken", nil)
	}
	if len([]rune(v.Name)) > 60 {
		f.errs.Add("name", "too_long", map[string]any{"count": 60})
	}
	if v.Description != nil && len([]rune(*v.Description)) > 255 {
		f.errs.Add("description", "too_long", map[string]any{"count": 255})
	}
	if v.WikiPageTitle != nil && len([]rune(*v.WikiPageTitle)) > 255 {
		f.errs.Add("wiki_page_title", "too_long", map[string]any{"count": 255})
	}
	if raw := f.effectiveDateRaw; raw != nil && strings.TrimSpace(*raw) != "" {
		if !versionDateRe.MatchString(*raw) || v.EffectiveDate == nil {
			f.errs.Add("effective_date", "not_a_date", nil)
		}
	}
	if !slices.Contains(versionStatuses, v.Status) {
		f.errs.Add("status", "inclusion", nil)
	}
	if !slices.Contains(versionSharings, v.Sharing) {
		f.errs.Add("sharing", "inclusion", nil)
	}
	return nil
}

// saveVersion は保存する（after_update :update_issues_from_sharing_change、after_save :update_default_project_version）。
func (a *App) saveVersion(c *Req, f *versionForm, orig *domain.Version) error {
	ctx := c.Ctx()
	v := f.V
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		if v.ID == 0 {
			if err := repository.InsertVersion(ctx, tx, v); err != nil {
				return err
			}
			f.id = v.ID
		} else if versionChanged(orig, v) {
			if err := repository.UpdateVersion(ctx, tx, v); err != nil {
				return err
			}
		}
		for _, cv := range f.CustomValues {
			if err := repository.SetCustomValues(ctx, tx, "version", v.ID, cv.Field.ID, cv.Values); err != nil {
				return err
			}
		}
		if f.defaultProjectVersion != nil && *f.defaultProjectVersion {
			if err := repository.SetProjectDefaultVersion(ctx, tx, v.ProjectID, v.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if orig != nil && orig.Sharing != v.Sharing {
		oi, ni := slices.Index(versionSharings, orig.Sharing), slices.Index(versionSharings, v.Sharing)
		if oi < 0 || ni < 0 || oi > ni {
			env := issues.NewEnv(a.DB, a.Settings, c.User)
			if _, err := env.UpdateVersionsFromSharingChange(ctx, v.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func versionChanged(o, v *domain.Version) bool {
	if o == nil {
		return true
	}
	return o.Name != v.Name || !eqStrPtr(o.Description, v.Description) || !eqDatePtr(o.EffectiveDate, v.EffectiveDate) ||
		!eqStrPtr(o.WikiPageTitle, v.WikiPageTitle) || o.Status != v.Status || o.Sharing != v.Sharing
}

func eqStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func eqDatePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (a *App) versionFormData(c *Req, f *versionForm) (map[string]any, error) {
	hasWiki, err := repository.ProjectWikiExists(c.Ctx(), a.DB, f.Project.ID)
	if err != nil {
		return nil, err
	}
	var statusOpts, sharingOpts [][]any
	for _, s := range versionStatuses {
		statusOpts = append(statusOpts, []any{c.L("version_status_" + s), s})
	}
	for _, s := range a.allowedSharings(c, f.V, f.Project) {
		sharingOpts = append(sharingOpts, []any{c.L("label_version_sharing_" + s), s})
	}
	return map[string]any{
		"Version":        f,
		"NoWiki":         !hasWiki,
		"StatusOptions":  statusOpts,
		"SharingOptions": sharingOpts,
		"CreatePath":     "/projects/" + f.Project.Identifier + "/versions",
	}, nil
}

func (a *App) renderVersionForm(c *Req, tmpl string, f *versionForm, opts ...RenderOptions) {
	data, err := a.versionFormData(c, f)
	if err != nil {
		a.internalError(c, "version form", err)
		return
	}
	c.Render(tmpl, data, opts...)
}

// newVersion は @project.versions.build。
func newVersion(p *domain.Project) *domain.Version {
	empty := ""
	return &domain.Version{ProjectID: p.ID, Status: "open", Sharing: "none", Description: &empty}
}

// VersionsNew は versions#new（html / js）。
func (a *App) VersionsNew(c *Req) {
	f, err := a.newVersionForm(c, newVersion(c.Project), c.Project)
	if err != nil {
		a.internalError(c, "new version", err)
		return
	}
	f.assign(c, c.Params().Map("version"), false)
	if httpx.Negotiate(c.R, "html", "js") == "js" {
		a.renderVersionForm(c, "versions/new", f, RenderOptions{Layout: view.NoLayout, Format: "js"})
		return
	}
	a.renderVersionForm(c, "versions/new", f)
}

// VersionsCreate は versions#create（html / js / api）。
func (a *App) VersionsCreate(c *Req) {
	f, err := a.newVersionForm(c, newVersion(c.Project), c.Project)
	if err != nil {
		a.internalError(c, "new version", err)
		return
	}
	f.assign(c, c.Params().Map("version"), true)
	if err := a.validateVersion(c, f); err != nil {
		a.internalError(c, "validate version", err)
		return
	}
	format := httpx.Negotiate(c.R, "html", "js", "json", "xml")
	if !f.errs.Any() {
		if err := a.saveVersion(c, f, nil); err != nil {
			a.internalError(c, "save version", err)
			return
		}
		switch format {
		case "js":
			a.versionsCreateJS(c, f.V)
		case "json", "xml":
			c.W.Header().Set("Location", httpx.RequestBaseURL(c.R)+urlroot.Path("/versions/"+strconv.FormatInt(f.V.ID, 10)))
			a.renderVersionShowAPI(c, f.V, http.StatusCreated)
		default:
			c.Flash().SetNotice(c.L("notice_successful_create"))
			c.RedirectBackOrDefault("/projects/"+c.Project.Identifier+"/settings/versions", false)
		}
		return
	}
	switch format {
	case "js":
		a.renderVersionForm(c, "versions/new", f, RenderOptions{Layout: view.NoLayout, Format: "js"})
	case "json", "xml":
		c.RenderAPIErrors(f.errs.FullMessages(c.L)...)
	default:
		a.renderVersionForm(c, "versions/new", f)
	}
}

// versionsCreateJS は create.js.erb（チケットフォームの対象バージョンの選択肢を置き換える）。
func (a *App) versionsCreateJS(c *Req, created *domain.Version) {
	ctx := c.Ctx()
	vs, err := repository.LoadVersionsWhere(ctx, a.DB, repository.SharedVersionsCondition(c.Project)+" AND versions.status = 'open'")
	if err != nil {
		a.internalError(c, "create.js", err)
		return
	}
	options, err := a.versionOptionsForSelect(c, vs, created)
	if err != nil {
		a.internalError(c, "create.js", err)
		return
	}
	sel := rails.ContentTag("select", rails.ContentTag("option", "", nil)+options,
		rails.NewHash("id", "issue_fixed_version_id", "name", "issue[fixed_version_id]"))
	c.Render("versions/create", map[string]any{"Select": sel}, RenderOptions{Layout: view.NoLayout, Format: "js"})
}

// versionOptionsForSelect は version_options_for_select(versions, selected)（プロジェクトごとにグループ化）。
func (a *App) versionOptionsForSelect(c *Req, versions []*domain.Version, selected *domain.Version) (template.HTML, error) {
	type group struct {
		project *domain.Project
		opts    []any
	}
	var groups []*group
	byProject := map[int64]*group{}
	for _, v := range versions {
		g, ok := byProject[v.ProjectID]
		if !ok {
			p, err := repository.GetProject(c.Ctx(), a.DB, v.ProjectID)
			if err != nil {
				return "", err
			}
			g = &group{project: p}
			byProject[v.ProjectID] = g
			groups = append(groups, g)
		}
		g.opts = append(g.opts, []any{v.Name, v.ID})
	}
	var sel any
	if selected != nil {
		sel = selected.ID
	}
	if len(groups) == 1 {
		return rails.OptionsForSelect(groups[0].opts, sel), nil
	}
	var out template.HTML
	for _, g := range groups {
		out += rails.ContentTag("optgroup", rails.OptionsForSelect(g.opts, sel), rails.NewHash("label", g.project.Name))
	}
	return out, nil
}

// VersionsEdit は versions#edit。
func (a *App) VersionsEdit(c *Req) {
	v := *c.version()
	f, err := a.newVersionForm(c, &v, c.Project)
	if err != nil {
		a.internalError(c, "edit version", err)
		return
	}
	a.renderVersionForm(c, "versions/edit", f)
}

// VersionsUpdate は versions#update（html / api）。
func (a *App) VersionsUpdate(c *Req) {
	orig := c.version()
	format := httpx.Negotiate(c.R, "html", "json", "xml")
	attrs := c.Params().Map("version")
	if attrs == nil {
		// if params[:version] が無ければ何も描画しない（Rails は update.html を探して 204/406 等になる）
		if format == "json" || format == "xml" {
			c.head(http.StatusNoContent)
		} else {
			c.head(http.StatusNoContent)
		}
		return
	}
	v := *orig
	f, err := a.newVersionForm(c, &v, c.Project)
	if err != nil {
		a.internalError(c, "edit version", err)
		return
	}
	f.assign(c, attrs, true)
	if err := a.validateVersion(c, f); err != nil {
		a.internalError(c, "validate version", err)
		return
	}
	if !f.errs.Any() {
		if err := a.saveVersion(c, f, orig); err != nil {
			a.internalError(c, "save version", err)
			return
		}
		if format == "json" || format == "xml" {
			c.RenderAPIOK()
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.RedirectBackOrDefault("/projects/"+c.Project.Identifier+"/settings/versions", false)
		return
	}
	if format == "json" || format == "xml" {
		c.RenderAPIErrors(f.errs.FullMessages(c.L)...)
		return
	}
	a.renderVersionForm(c, "versions/edit", f)
}

// VersionsCloseCompleted は versions#close_completed（PUT）。
func (a *App) VersionsCloseCompleted(c *Req) {
	ctx := c.Ctx()
	vc, err := a.newVersionCtx(c)
	if err != nil {
		a.internalError(c, "close completed", err)
		return
	}
	vs, err := repository.LoadVersionsWhere(ctx, a.DB, "versions.project_id = ? AND versions.status IN ('open', 'locked')", c.Project.ID)
	if err != nil {
		a.internalError(c, "close completed", err)
		return
	}
	err = a.DB.WithTx(ctx, func(tx *db.Tx) error {
		for _, v := range vs {
			m, err := vc.model(v)
			if err != nil {
				return err
			}
			if m.Completed() {
				if err := repository.SetVersionStatus(ctx, tx, v.ID, "closed"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		a.internalError(c, "close completed", err)
		return
	}
	c.Redirect("/projects/" + c.Project.Identifier + "/settings/versions")
}

// VersionsDestroy は versions#destroy（html / api）。
func (a *App) VersionsDestroy(c *Req) {
	ctx := c.Ctx()
	v := c.version()
	api := httpx.IsAPIRequest(c.R)
	// 削除できるか（チケット・カスタムフィールド・添付から参照されていないか）の判定と削除は同じトランザクションで行う
	// （判定の後に割り当てられたチケットの対象バージョンが、記録なしに消えないように）
	deletable := false
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		ok, err := a.versionDeletable(ctx, tx, v.ID)
		if err != nil || !ok {
			return err
		}
		deletable = true
		return repository.DeleteVersion(ctx, tx, v.ID)
	})
	if err != nil {
		a.internalError(c, "destroy version", err)
		return
	}
	if deletable {
		if api {
			c.RenderAPIOK()
			return
		}
		c.RedirectBackOrDefault("/projects/"+c.Project.Identifier+"/settings/versions", false)
		return
	}
	if api {
		c.head(http.StatusUnprocessableEntity)
		return
	}
	c.Flash().SetError(c.L("notice_unable_delete_version"))
	c.Redirect("/projects/" + c.Project.Identifier + "/settings/versions")
}

// versionDeletable は deletable?。
func (a *App) versionDeletable(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	for _, f := range []func(context.Context, db.Queryer, int64) (bool, error){
		repository.VersionHasFixedIssues, repository.VersionReferencedByCustomField, repository.VersionHasAttachments,
	} {
		b, err := f(ctx, q, id)
		if err != nil || b {
			return false, err
		}
	}
	return true, nil
}

// ---------------------------------------------------------------- カスタムフィールド

// versionCustomValues は custom_field_values（visibleOnly なら visible_custom_field_values）。
func (a *App) versionCustomValues(c *Req, v *domain.Version, p *domain.Project, visibleOnly bool) ([]*domain.CustomFieldValue, error) {
	ctx := c.Ctx()
	fields, err := repository.CustomFieldInfosByKind(ctx, a.DB, "version")
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	var stored map[int64][]string
	if v.ID != 0 {
		if stored, err = repository.CustomValues(ctx, a.DB, "version", v.ID); err != nil {
			return nil, err
		}
	}
	var out []*domain.CustomFieldValue
	for _, cf := range fields {
		if visibleOnly {
			full, err := customfield.Get(ctx, a.DB, cf.ID)
			if err != nil {
				return nil, err
			}
			ok, err := customfield.VisibleBy(ctx, c.Authz(), full, p)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		cv := &domain.CustomFieldValue{Field: cf}
		if vals, ok := stored[cf.ID]; ok {
			cv.Values = vals
		} else if v.ID == 0 && cf.DefaultValue != nil && *cf.DefaultValue != "" {
			cv.Values = []string{*cf.DefaultValue}
		}
		out = append(out, cv)
	}
	return out, nil
}

// ---------------------------------------------------------------- API

func (a *App) versionAPIFields(c *Req, b apibuilder.Builder, v *domain.Version, p *domain.Project) {
	b.Value("id", v.ID)
	if p != nil {
		b.Attrs("project", apibuilder.A("id", p.ID, "name", p.Name))
	}
	b.Value("name", v.Name)
	b.Value("description", ptrValue(v.Description))
	b.Value("status", v.Status)
	if v.EffectiveDate != nil {
		b.Value("due_date", v.EffectiveDate.Format("2006-01-02"))
	} else {
		b.Value("due_date", nil)
	}
	b.Value("sharing", v.Sharing)
	b.Value("wiki_page_title", ptrValue(v.WikiPageTitle))
}

func ptrValue(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func (a *App) versionAPICustomValues(c *Req, b apibuilder.Builder, v *domain.Version, p *domain.Project) {
	cvs, err := a.versionCustomValues(c, v, p, true)
	if err != nil {
		a.logger().Error("version custom values", "err", err)
	}
	if len(cvs) == 0 {
		return
	}
	b.Array("custom_fields", nil, func() {
		for _, cv := range cvs {
			attrs := apibuilder.A("id", cv.Field.ID, "name", cv.Field.Name)
			if cv.Field.Multiple {
				attrs = append(attrs, apibuilder.A("multiple", true)...)
			}
			b.ObjectAttrs("custom_field", attrs, func() {
				if cv.Field.Multiple {
					b.Array("value", nil, func() {
						for _, s := range cv.Values {
							if s != "" {
								b.Value("value", s)
							}
						}
					})
				} else if len(cv.Values) == 0 {
					b.Value("value", nil)
				} else {
					b.Value("value", cv.Values[0])
				}
			})
		}
	})
}

func (a *App) renderVersionsIndexAPI(c *Req, vs []*domain.Version) {
	projects := map[int64]*domain.Project{}
	for _, v := range vs {
		if _, ok := projects[v.ProjectID]; !ok {
			p, err := repository.GetProject(c.Ctx(), a.DB, v.ProjectID)
			if err != nil {
				a.internalError(c, "version project", err)
				return
			}
			projects[v.ProjectID] = p
		}
	}
	c.RenderAPI(0, func(b apibuilder.Builder) {
		b.Array("versions", c.APIMeta(apibuilder.A("total_count", len(vs))), func() {
			for _, v := range vs {
				p := projects[v.ProjectID]
				b.Object("version", func() {
					a.versionAPIFields(c, b, v, p)
					a.versionAPICustomValues(c, b, v, p)
					b.Value("created_on", v.CreatedAt)
					b.Value("updated_on", v.UpdatedAt)
				})
			}
		})
	})
}

func (a *App) renderVersionShowAPI(c *Req, v *domain.Version, status int) {
	ctx := c.Ctx()
	p, err := repository.GetProject(ctx, a.DB, v.ProjectID)
	if err != nil {
		a.internalError(c, "version project", err)
		return
	}
	viewTE, err := c.Authz().AllowedTo(ctx, domain.Perm("view_time_entries"), c.Project)
	if err != nil {
		a.internalError(c, "version api", err)
		return
	}
	var est, spent float64
	if viewTE {
		vis, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{})
		if err != nil {
			a.internalError(c, "version api", err)
			return
		}
		if est, _, err = repository.VersionEstimatedHours(ctx, a.DB, v.ID, vis); err != nil {
			a.internalError(c, "version api", err)
			return
		}
		if spent, err = repository.VersionSpentHours(ctx, a.DB, v.ID); err != nil {
			a.internalError(c, "version api", err)
			return
		}
	}
	c.RenderAPI(status, func(b apibuilder.Builder) {
		b.Object("version", func() {
			a.versionAPIFields(c, b, v, p)
			if viewTE {
				b.Value("estimated_hours", est)
				b.Value("spent_hours", spent)
			}
			a.versionAPICustomValues(c, b, v, p)
			b.Value("created_on", v.CreatedAt)
			b.Value("updated_on", v.UpdatedAt)
		})
	})
}

// versionsRoutingError は一致するルートが無いときの Rails の 404 応答（ActionDispatch::ShowExceptions。
// json / xml は {status, error}、それ以外は public/404.html）。
func versionsRoutingError(c *Req) {
	c.Halt()
	switch httpx.Format(c.R) {
	case "json":
		c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.Write([]byte(`{"status":404,"error":"Not Found"}`))
	case "xml":
		c.W.Header().Set("Content-Type", "application/xml; charset=utf-8")
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.Write([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<hash>\n  <status type=\"integer\">404</status>\n  <error>Not Found</error>\n</hash>\n"))
	default:
		b, err := fs.ReadFile(web.Public(), "404.html")
		if err != nil {
			http.NotFound(c.W, c.R)
			return
		}
		c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.Write(b)
	}
}

// versionFormPath は url_for({})（現在のパスから .format を除いたもの）。
func versionFormPath(c *Req) string {
	p := c.R.URL.Path
	if f := httpx.PathParams(c.R).String("format"); f != "" {
		p = strings.TrimSuffix(p, "."+f)
	}
	return p
}

// wikiTitleize は Wiki.titleize（internal/textformat/redmine の移植を使う）。
func wikiTitleize(t string) string { return redmine.Titleize(t) }
