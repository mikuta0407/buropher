package handler

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// WorkflowsController（app/controllers/workflows_controller.rb）。
var WorkflowsController = &Controller{Name: "workflows", MainMenu: false}

// routesWorkflows は workflows コントローラのルートを登録する。
func (a *App) routesWorkflows(r Router) {
	// before_action :find_trackers_roles_and_statuses_for_edit は DB の読み込みのみで副作用がないため、
	// require_admin の後に各アクションの先頭で行う（workflowState）。
	// resources :workflows, only: [:index] do collection do ... end end
	a.Handle(r, http.MethodGet, "/workflows", WorkflowsController, "index", a.WorkflowsIndex, RequireAdmin())
	a.Handle(r, http.MethodGet, "/workflows/edit", WorkflowsController, "edit", a.WorkflowsEdit, RequireAdmin())
	a.Handle(r, http.MethodPatch, "/workflows/update", WorkflowsController, "update", a.WorkflowsUpdate, RequireAdmin())
	a.Handle(r, http.MethodGet, "/workflows/permissions", WorkflowsController, "permissions", a.WorkflowsPermissions, RequireAdmin())
	a.Handle(r, http.MethodPatch, "/workflows/update_permissions", WorkflowsController, "update_permissions", a.WorkflowsUpdatePermissions, RequireAdmin())
	a.Handle(r, http.MethodGet, "/workflows/copy", WorkflowsController, "copy", a.WorkflowsCopy, RequireAdmin())
	a.Handle(r, http.MethodPost, "/workflows/duplicate", WorkflowsController, "duplicate", a.WorkflowsDuplicate, RequireAdmin())
}

// adminLayout は layout 'admin'。
var adminLayout = RenderOptions{Layout: "admin"}

// workflowState は find_trackers_roles_and_statuses_for_edit が設定するインスタンス変数。
type workflowState struct {
	// Roles / Trackers は @roles / @trackers（未選択なら nil）。
	Roles    []*domain.Role
	Trackers []*domain.Tracker
	// Statuses は @statuses。
	Statuses []*domain.IssueStatus
	// UsedStatusesOnly は @used_statuses_only。
	UsedStatusesOnly bool
}

// roleName は Role#name（組込ロールは翻訳した名前）。
func (c *Req) roleName(r *domain.Role) string {
	switch r.Builtin {
	case domain.RoleBuiltinNonMember:
		return c.L("label_role_non_member")
	case domain.RoleBuiltinAnonymous:
		return c.L("label_role_anonymous")
	}
	return r.Name
}

func (c *Req) roleItems(roles []*domain.Role) []helper.WorkflowItem {
	if roles == nil {
		return nil
	}
	out := make([]helper.WorkflowItem, len(roles))
	for i, r := range roles {
		out[i] = helper.WorkflowItem{ID: r.ID, Name: c.roleName(r), Builtin: r.IsBuiltin()}
	}
	return out
}

func trackerItems(ts []*domain.Tracker) []helper.WorkflowItem {
	if ts == nil {
		return nil
	}
	out := make([]helper.WorkflowItem, len(ts))
	for i, t := range ts {
		out[i] = helper.WorkflowItem{ID: t.ID, Name: t.Name}
	}
	return out
}

// workflowRoles は Role.sorted.select(&:consider_workflow?)。
func (a *App) workflowRoles(c *Req) ([]*domain.Role, error) {
	roles, err := repository.ListRoles(c.Ctx(), a.DB)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(roles, func(r *domain.Role) bool { return !r.ConsiderWorkflow() }), nil
}

// paramIDs は Array.wrap(params[key]) を id として解釈できるものだけ返す（Rails の where(:id => ids) と同じく
// 数値でない値は一致しない）。
func paramIDs(p *httpx.Params, key string) (ids []int64, raw []string) {
	raw = p.Strings(key)
	for _, s := range raw {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, n)
		}
	}
	return ids, raw
}

// rolesWhereID は Role.where(:id => ids).to_a（主キー順）。
func (a *App) rolesWhereID(c *Req, ids []int64) ([]*domain.Role, error) {
	roles, err := repository.RolesByIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(roles, func(x, y *domain.Role) int { return cmpID(x.ID, y.ID) })
	return roles, nil
}

// trackersWhereID は Tracker.where(:id => ids).to_a（主キー順）。
func (a *App) trackersWhereID(c *Req, ids []int64) ([]*domain.Tracker, error) {
	ts, err := repository.TrackersByIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(ts, func(x, y *domain.Tracker) int { return cmpID(x.ID, y.ID) })
	return ts, nil
}

func cmpID(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// workflowState は find_trackers_roles_and_statuses_for_edit（find_roles / find_trackers / find_statuses）。
// 失敗したら 500 を描画して nil を返す。
func (a *App) workflowState(c *Req) *workflowState {
	st := &workflowState{}
	if err := a.loadWorkflowState(c, st); err != nil {
		a.renderErr(c, "workflows: find", err)
		return nil
	}
	return st
}

func (a *App) loadWorkflowState(c *Req, st *workflowState) error {
	p := c.Params()
	ids, raw := paramIDs(p, "role_id")
	switch {
	case len(raw) == 1 && raw[0] == "all":
		roles, err := a.workflowRoles(c)
		if err != nil {
			return err
		}
		st.Roles = roles
	case len(raw) > 0:
		roles, err := a.rolesWhereID(c, ids)
		if err != nil {
			return err
		}
		st.Roles = roles
	}
	if len(st.Roles) == 0 {
		st.Roles = nil
	}
	ids, raw = paramIDs(p, "tracker_id")
	switch {
	case len(raw) == 1 && raw[0] == "all":
		ts, err := repository.ListTrackers(c.Ctx(), a.DB)
		if err != nil {
			return err
		}
		st.Trackers = ts
	case len(raw) > 0:
		ts, err := a.trackersWhereID(c, ids)
		if err != nil {
			return err
		}
		st.Trackers = ts
	}
	if len(st.Trackers) == 0 {
		st.Trackers = nil
	}
	st.UsedStatusesOnly = p.String("used_statuses_only") != "0"
	if st.Trackers != nil && st.UsedStatusesOnly {
		roles, err := a.workflowRoles(c)
		if err != nil {
			return err
		}
		statusIDs, err := repository.WorkflowUsedStatusIDs(c.Ctx(), a.DB, trackerIDs(st.Trackers), roleIDs(roles))
		if err != nil {
			return err
		}
		statuses, err := repository.IssueStatusesByIDs(c.Ctx(), a.DB, statusIDs)
		if err != nil {
			return err
		}
		st.Statuses = statuses
	}
	if len(st.Statuses) == 0 {
		statuses, err := repository.ListIssueStatuses(c.Ctx(), a.DB)
		if err != nil {
			return err
		}
		st.Statuses = statuses
	}
	return nil
}

func roleIDs(rs []*domain.Role) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func trackerIDs(ts []*domain.Tracker) []int64 {
	out := make([]int64, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

// workflowSelectionQuery は edit_workflows_path(:role_id => @roles, :tracker_id => @trackers) のクエリ部。
func workflowSelectionQuery(st *workflowState) string {
	var parts []string
	for _, r := range st.Roles {
		parts = append(parts, url.QueryEscape("role_id[]")+"="+strconv.FormatInt(r.ID, 10))
	}
	for _, t := range st.Trackers {
		parts = append(parts, url.QueryEscape("tracker_id[]")+"="+strconv.FormatInt(t.ID, 10))
	}
	if len(parts) == 0 {
		return ""
	}
	q := "?"
	for i, p := range parts {
		if i > 0 {
			q += "&"
		}
		q += p
	}
	return q
}

// WorkflowsIndex は workflows#index（GET /workflows）。
func (a *App) WorkflowsIndex(c *Req) {
	roles, err := a.workflowRoles(c)
	if err != nil {
		a.renderErr(c, "workflows index", err)
		return
	}
	trackers, err := repository.ListTrackers(c.Ctx(), a.DB)
	if err != nil {
		a.renderErr(c, "workflows index", err)
		return
	}
	counts, err := repository.WorkflowTransitionCounts(c.Ctx(), a.DB)
	if err != nil {
		a.renderErr(c, "workflows index", err)
		return
	}
	type cell struct {
		RoleID int64
		Count  int
	}
	type row struct {
		Tracker *domain.Tracker
		Cells   []cell
	}
	rows := make([]row, len(trackers))
	for i, t := range trackers {
		rows[i].Tracker = t
		for _, r := range roles {
			rows[i].Cells = append(rows[i].Cells, cell{r.ID, counts[repository.WorkflowCountKey{TrackerID: t.ID, RoleID: r.ID}]})
		}
	}
	c.Render("workflows/index", map[string]any{
		"Roles":    c.roleItems(roles),
		"Trackers": trackers,
		"Rows":     rows,
	}, adminLayout)
}

// renderErr は DB エラーをログに残して 500 を返す。
func (a *App) renderErr(c *Req, what string, err error) {
	a.logger().Error(what, "err", err)
	c.renderInternalError()
	c.Halt()
}

// transitionRow は _form の 1 行（遷移元ステータス）。
type transitionRow struct {
	// Old は遷移元（nil = 新規チケット）。
	Old   *domain.IssueStatus
	OldID int64
	Cells []transitionCell
}

type transitionCell struct {
	New     *domain.IssueStatus
	Count   int
	Checked bool
}

type transitionTable struct {
	Name string
	Rows []transitionRow
}

// workflowsEditData は edit / permissions 共通の表示データ。
func (a *App) workflowsSelectionData(c *Req, st *workflowState) (map[string]any, error) {
	allRoles, err := a.workflowRoles(c)
	if err != nil {
		return nil, err
	}
	allTrackers, err := repository.ListTrackers(c.Ctx(), a.DB)
	if err != nil {
		return nil, err
	}
	var usedParam any
	if v, ok := c.Params().Get("used_statuses_only"); ok {
		usedParam = v
	}
	return map[string]any{
		"AllRoles":         c.roleItems(allRoles),
		"AllTrackers":      trackerItems(allTrackers),
		"Roles":            c.roleItems(st.Roles),
		"Trackers":         trackerItems(st.Trackers),
		"Statuses":         st.Statuses,
		"UsedStatusesOnly": st.UsedStatusesOnly,
		"UsedParam":        usedParam,
		"Total":            len(st.Roles) * len(st.Trackers),
		"Query":            workflowSelectionQuery(st),
		"Show":             st.Trackers != nil && st.Roles != nil && len(st.Statuses) > 0,
		"StatusWidth":      75 / max(len(st.Statuses), 1),
	}, nil
}

// WorkflowsEdit は workflows#edit（GET /workflows/edit）。
func (a *App) WorkflowsEdit(c *Req) {
	st := a.workflowState(c)
	if st == nil {
		return
	}
	data, err := a.workflowsSelectionData(c, st)
	if err != nil {
		a.renderErr(c, "workflows edit", err)
		return
	}
	if data["Show"] == true {
		ws, err := repository.WorkflowTransitions(c.Ctx(), a.DB, trackerIDs(st.Trackers), roleIDs(st.Roles))
		if err != nil {
			a.renderErr(c, "workflows edit", err)
			return
		}
		groups := map[string][]*domain.WorkflowTransition{}
		for _, w := range ws {
			if !w.Author && !w.Assignee {
				groups["always"] = append(groups["always"], w)
			}
			if w.Author {
				groups["author"] = append(groups["author"], w)
			}
			if w.Assignee {
				groups["assignee"] = append(groups["assignee"], w)
			}
		}
		statusExists := map[int64]bool{}
		for _, s := range st.Statuses {
			statusExists[s.ID] = true
		}
		build := func(name string) transitionTable {
			counts := map[[2]int64]int{}
			for _, w := range groups[name] {
				counts[[2]int64{w.OldStatusID, w.NewStatusID}]++
			}
			t := transitionTable{Name: name}
			olds := append([]*domain.IssueStatus{nil}, st.Statuses...)
			for _, old := range olds {
				if old == nil && name != "always" {
					continue
				}
				var oldID int64
				if old != nil {
					oldID = old.ID
				}
				row := transitionRow{Old: old, OldID: oldID}
				for _, n := range st.Statuses {
					cnt := counts[[2]int64{oldID, n.ID}]
					row.Cells = append(row.Cells, transitionCell{New: n, Count: cnt, Checked: (old != nil && old.ID == n.ID) || cnt > 0})
				}
				t.Rows = append(t.Rows, row)
			}
			return t
		}
		data["Always"] = build("always")
		data["Author"] = build("author")
		data["Assignee"] = build("assignee")
		data["AuthorPresent"] = len(groups["author"]) > 0
		data["AssigneePresent"] = len(groups["assignee"]) > 0
	}
	c.Render("workflows/edit", data, adminLayout)
}

// WorkflowsUpdate は workflows#update（PATCH /workflows/update）。
func (a *App) WorkflowsUpdate(c *Req) {
	st := a.workflowState(c)
	if st == nil {
		return
	}
	p := c.Params()
	if st.Roles != nil && st.Trackers != nil && p.Has("transitions") {
		var changes []repository.TransitionChange
		trans := p.Map("transitions")
		trans.Each(func(old string, v any) {
			byNew, ok := v.(*httpx.Params)
			if !ok {
				return
			}
			oldID := httpx.RubyToI(old)
			byNew.Each(func(nw string, v any) {
				byRule, ok := v.(*httpx.Params)
				if !ok {
					return
				}
				newID := httpx.RubyToI(nw)
				byRule.Each(func(rule string, v any) {
					val := httpx.ValueString(v)
					if val == "no_change" {
						return
					}
					changes = append(changes, repository.TransitionChange{OldStatusID: oldID, NewStatusID: newID, Rule: rule, Enabled: val == "1"})
				})
			})
		})
		err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
			return repository.ReplaceWorkflowTransitions(c.Ctx(), tx, trackerIDs(st.Trackers), roleIDs(st.Roles), changes)
		})
		if err != nil {
			a.renderErr(c, "workflows update", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
	}
	c.redirectToRefererOr("/workflows/edit")
}

// redirectToRefererOr は redirect_to_referer_or(path)。
func (c *Req) redirectToRefererOr(path string) {
	if ref := c.R.Header.Get("Referer"); ref != "" {
		c.Redirect(ref)
		return
	}
	c.Redirect(path)
}

// WorkflowsPermissions は workflows#permissions（GET /workflows/permissions）。
func (a *App) WorkflowsPermissions(c *Req) {
	st := a.workflowState(c)
	if st == nil {
		return
	}
	data, err := a.workflowsSelectionData(c, st)
	if err != nil {
		a.renderErr(c, "workflows permissions", err)
		return
	}
	if st.Roles != nil && st.Trackers != nil {
		// (Tracker::CORE_FIELDS_ALL - trackers.map(&:disabled_core_fields).reduce(:&))
		disabled := slices.Clone(st.Trackers[0].DisabledCoreFields)
		for _, t := range st.Trackers[1:] {
			disabled = slices.DeleteFunc(disabled, func(f string) bool { return !slices.Contains(t.DisabledCoreFields, f) })
		}
		var fields []helper.WorkflowField
		for _, f := range domain.TrackerCoreFieldsAll {
			if slices.Contains(disabled, f) {
				continue
			}
			name := f
			if len(name) > 3 && name[len(name)-3:] == "_id" {
				name = name[:len(name)-3]
			}
			fields = append(fields, helper.WorkflowField{Key: f, Label: c.L("field_" + name),
				Required: slices.Contains([]string{"project_id", "tracker_id", "subject", "priority_id", "is_private"}, f)})
		}
		cfs, err := customfield.TrackersCustomFields(c.Ctx(), a.DB, trackerIDs(st.Trackers))
		if err != nil {
			a.renderErr(c, "workflows permissions", err)
			return
		}
		var cfFields []helper.WorkflowField
		for _, cf := range cfs {
			hidden := !cf.Visible && !slices.ContainsFunc(st.Roles, func(r *domain.Role) bool { return cf.HasRole(r.ID) })
			cfFields = append(cfFields, helper.WorkflowField{Key: strconv.FormatInt(cf.ID, 10), Label: cf.Name, Required: cf.IsRequired, Hidden: hidden})
		}
		perms, err := repository.WorkflowRulesByStatusID(c.Ctx(), a.DB, trackerIDs(st.Trackers), roleIDs(st.Roles))
		if err != nil {
			a.renderErr(c, "workflows permissions", err)
			return
		}
		for _, s := range st.Statuses {
			if perms[s.ID] == nil {
				perms[s.ID] = map[string][]string{}
			}
		}
		data["Fields"] = fields
		data["CustomFields"] = cfFields
		data["Permissions"] = perms
	}
	c.Render("workflows/permissions", data, adminLayout)
}

// WorkflowsUpdatePermissions は workflows#update_permissions（PATCH /workflows/update_permissions）。
func (a *App) WorkflowsUpdatePermissions(c *Req) {
	st := a.workflowState(c)
	if st == nil {
		return
	}
	p := c.Params()
	if st.Roles != nil && st.Trackers != nil && p.Has("permissions") {
		var changes []repository.PermissionChange
		p.Map("permissions").Each(func(status string, v any) {
			byField, ok := v.(*httpx.Params)
			if !ok {
				return
			}
			byField.Each(func(field string, v any) {
				rule := httpx.ValueString(v)
				if rule == "no_change" {
					return
				}
				changes = append(changes, repository.PermissionChange{StatusID: status, Field: field, Rule: rule})
			})
		})
		err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
			return repository.ReplaceWorkflowPermissions(c.Ctx(), tx, trackerIDs(st.Trackers), roleIDs(st.Roles), changes)
		})
		if err != nil {
			a.renderErr(c, "workflows update_permissions", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
	}
	c.redirectToRefererOr("/workflows/permissions")
}

// workflowCopyState は find_sources_and_targets のインスタンス変数。
type workflowCopyState struct {
	Roles                                 []*domain.Role
	Trackers                              []*domain.Tracker
	SourceTracker                         *domain.Tracker
	SourceRole                            *domain.Role
	TargetTrackers                        []*domain.Tracker
	TargetRoles                           []*domain.Role
	targetTrackersGiven, targetRolesGiven bool
}

func (a *App) findSourcesAndTargets(c *Req) (*workflowCopyState, error) {
	st := &workflowCopyState{}
	var err error
	if st.Roles, err = a.workflowRoles(c); err != nil {
		return nil, err
	}
	if st.Trackers, err = repository.ListTrackers(c.Ctx(), a.DB); err != nil {
		return nil, err
	}
	p := c.Params()
	if s := p.String("source_tracker_id"); !httpx.IsBlank(s) && s != "any" {
		t, err := repository.GetTracker(c.Ctx(), a.DB, httpx.RubyToI(s))
		if err != nil && err != repository.ErrNotFound {
			return nil, err
		}
		st.SourceTracker = t
	}
	if s := p.String("source_role_id"); !httpx.IsBlank(s) && s != "any" {
		r, err := repository.GetRole(c.Ctx(), a.DB, httpx.RubyToI(s))
		if err != nil && err != repository.ErrNotFound {
			return nil, err
		}
		st.SourceRole = r
	}
	if v, _ := p.Get("target_tracker_ids"); !httpx.IsBlank(v) {
		ids, _ := paramIDs(p, "target_tracker_ids")
		if st.TargetTrackers, err = a.trackersWhereID(c, ids); err != nil {
			return nil, err
		}
		st.targetTrackersGiven = true
	}
	if v, _ := p.Get("target_role_ids"); !httpx.IsBlank(v) {
		ids, _ := paramIDs(p, "target_role_ids")
		if st.TargetRoles, err = a.rolesWhereID(c, ids); err != nil {
			return nil, err
		}
		st.targetRolesGiven = true
	}
	return st, nil
}

func (a *App) renderWorkflowCopy(c *Req, st *workflowCopyState) {
	var srcTracker, srcRole any
	if st.SourceTracker != nil {
		srcTracker = st.SourceTracker.ID
	}
	if st.SourceRole != nil {
		srcRole = st.SourceRole.ID
	}
	var tgtTrackers, tgtRoles any
	if st.targetTrackersGiven {
		ids := make([]any, len(st.TargetTrackers))
		for i, t := range st.TargetTrackers {
			ids[i] = t.ID
		}
		tgtTrackers = ids
	}
	if st.targetRolesGiven {
		ids := make([]any, len(st.TargetRoles))
		for i, r := range st.TargetRoles {
			ids[i] = r.ID
		}
		tgtRoles = ids
	}
	c.Render("workflows/copy", map[string]any{
		"Roles":          c.roleItems(st.Roles),
		"Trackers":       trackerItems(st.Trackers),
		"SourceTracker":  srcTracker,
		"SourceRole":     srcRole,
		"TargetTrackers": tgtTrackers,
		"TargetRoles":    tgtRoles,
	}, adminLayout)
}

// WorkflowsCopy は workflows#copy（GET /workflows/copy）。
func (a *App) WorkflowsCopy(c *Req) {
	st, err := a.findSourcesAndTargets(c)
	if err != nil {
		a.renderErr(c, "workflows copy", err)
		return
	}
	a.renderWorkflowCopy(c, st)
}

// WorkflowsDuplicate は workflows#duplicate（POST /workflows/duplicate）。
func (a *App) WorkflowsDuplicate(c *Req) {
	st, err := a.findSourcesAndTargets(c)
	if err != nil {
		a.renderErr(c, "workflows duplicate", err)
		return
	}
	p := c.Params()
	switch {
	case httpx.IsBlank(p.String("source_tracker_id")) || httpx.IsBlank(p.String("source_role_id")) ||
		(st.SourceTracker == nil && st.SourceRole == nil):
		c.Flash().Now("error", c.L("error_workflow_copy_source"))
		a.renderWorkflowCopy(c, st)
	case len(st.TargetTrackers) == 0 || len(st.TargetRoles) == 0:
		c.Flash().Now("error", c.L("error_workflow_copy_target"))
		a.renderWorkflowCopy(c, st)
	default:
		err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
			for _, tt := range st.TargetTrackers {
				for _, tr := range st.TargetRoles {
					srcT, srcR := tt.ID, tr.ID
					if st.SourceTracker != nil {
						srcT = st.SourceTracker.ID
					}
					if st.SourceRole != nil {
						srcR = st.SourceRole.ID
					}
					if err := repository.CopyWorkflow(c.Ctx(), tx, srcT, srcR, tt.ID, tr.ID); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			a.renderErr(c, "workflows duplicate", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		// copy_workflows_path(:source_tracker_id => @source_tracker, :source_role_id => @source_role)（nil は省かれ、キーはソート順）
		q := url.Values{}
		if st.SourceRole != nil {
			q.Set("source_role_id", strconv.FormatInt(st.SourceRole.ID, 10))
		}
		if st.SourceTracker != nil {
			q.Set("source_tracker_id", strconv.FormatInt(st.SourceTracker.ID, 10))
		}
		u := "/workflows/copy"
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		c.Redirect(u)
	}
}
