package handler

import (
	"errors"
	"html/template"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/permission"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは Project のフォーム（projects/_form, new / create / update）:
// safe_attributes=、バリデーション、allowed_parents、parent_project_select_tag、保存。

// projectIdentifierMaxLength は Project::IDENTIFIER_MAX_LENGTH。
const projectIdentifierMaxLength = 100

// projectForm は @project（フォームのモデル）。
type projectForm struct {
	formModel
	*domain.Project
	// TrackerIDs は tracker_ids。
	TrackerIDs []int64
	// IssueCustomFieldIDs は issue_custom_field_ids。
	IssueCustomFieldIDs []int64
	// CFValues は custom_field_values（全 ProjectCustomField）。
	CFValues []*projectCFValue
	// VisibleCFValues は visible_custom_field_values。
	VisibleCFValues []*projectCFValue

	// identifierAssigned は identifier が代入された（nil ではなく "" を表示する）。
	identifierAssigned bool
	// identifierWas は保存済みの識別子（identifier_frozen? の判定と変更検出に使う）。
	identifierWas string
	parentWas     *int64
	// unallowedParentID は safe_attributes= で許可されない親が指定された（@unallowed_parent_id）。
	unallowedParentID bool
	// parentParam は params の parent_id（parent_project_select_tag の選択値）。
	allowedParents      []*domain.Project
	allowedParentsNil   bool
	allowedParentsReady bool
	// safe は safe_attribute? の結果。
	safeIsPublic, safeModules, safeInheritMembers bool
	cfChanged                                     bool
}

// Description は description（テキストエリアの値）。
func (f *projectForm) Description() any { return f.Project.Description }

// IdentifierFrozen は identifier_frozen?。
func (f *projectForm) IdentifierFrozen() bool {
	return len(f.errs.On("identifier")) == 0 && !(f.id == 0 || f.identifierWas == "")
}

// IdentifierValue は text_field :identifier の値（凍結されていなければ現在値）。
func (f *projectForm) Identifier() any {
	if f.Project.Identifier == "" && f.id == 0 && !f.identifierAssigned {
		return nil
	}
	return f.Project.Identifier
}

// ToParamPath は Project#to_param（保存済みの識別子。数字のみの識別子なら id）。
func (f *projectForm) ToParamPath() string {
	if digitsOnlyRe.MatchString(f.identifierWas) || f.identifierWas == "" {
		return strconv.FormatInt(f.id, 10)
	}
	return f.identifierWas
}

// Name / Homepage は text_field の値（新規で空なら value="" を出す）。
func (f *projectForm) Name() any     { return f.Project.Name }
func (f *projectForm) Homepage() any { return f.Project.Homepage }

// DefaultVersionID / DefaultAssignedToID / DefaultIssueQueryID は select の値。
func (f *projectForm) DefaultVersionID() any      { return derefID(f.Project.DefaultVersionID) }
func (f *projectForm) DefaultAssignedToID() any   { return derefID(f.Project.DefaultAssignedToID) }
func (f *projectForm) DefaultIssueQueryID() any   { return derefID(f.Project.DefaultIssueQueryID) }
func (f *projectForm) SafeIsPublic() bool         { return f.safeIsPublic }
func (f *projectForm) SafeModules() bool          { return f.safeModules }
func (f *projectForm) SafeInheritMembers() bool   { return f.safeInheritMembers }
func (f *projectForm) HasTracker(id int64) bool   { return slices.Contains(f.TrackerIDs, id) }
func (f *projectForm) HasModule(name string) bool { return f.ModuleEnabled(name) }

func derefID(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// newProjectForm は Project.new（Setting の既定値で初期化）。
func (a *App) newProjectForm(c *Req) (*projectForm, error) {
	ctx := c.Ctx()
	p := &domain.Project{IsPublic: a.Settings.Bool("default_projects_public"), Status: domain.ProjectStatusActive}
	if a.Settings.Bool("sequential_project_identifiers") {
		if last, ok, err := repository.LastProjectIdentifier(ctx, a.DB); err != nil {
			return nil, err
		} else if ok {
			p.Identifier = rubySucc(last)
		}
	}
	p.EnabledModuleNames = enabledModuleNames(a.Settings.Strings("default_projects_modules"))
	f := &projectForm{formModel: newFormModel(c, "project", 0), Project: p}
	trackers, err := repository.ListTrackers(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	def := a.Settings.Get("default_projects_tracker_ids")
	if arr, ok := def.([]any); ok {
		want := map[int64]bool{}
		for _, v := range arr {
			want[rubyToI(rails.ToS(v))] = true
		}
		for _, t := range trackers {
			if want[t.ID] {
				f.TrackerIDs = append(f.TrackerIDs, t.ID)
			}
		}
	} else {
		for _, t := range trackers {
			f.TrackerIDs = append(f.TrackerIDs, t.ID)
		}
	}
	if f.CFValues, err = a.projectCustomFieldValues(ctx, p); err != nil {
		return nil, err
	}
	return f, nil
}

// loadProjectForm は保存済みプロジェクトのフォームモデル。
func (a *App) loadProjectForm(c *Req, p *domain.Project) (*projectForm, error) {
	ctx := c.Ctx()
	cp := *p
	cp.EnabledModuleNames = slices.Clone(p.EnabledModuleNames)
	f := &projectForm{formModel: newFormModel(c, "project", p.ID), Project: &cp, identifierWas: p.Identifier, parentWas: p.ParentID}
	var err error
	if f.TrackerIDs, err = repository.ProjectTrackerIDs(ctx, a.DB, p.ID); err != nil {
		return nil, err
	}
	if f.IssueCustomFieldIDs, err = repository.ProjectIssueCustomFieldIDs(ctx, a.DB, p.ID); err != nil {
		return nil, err
	}
	if f.CFValues, err = a.projectCustomFieldValues(ctx, p); err != nil {
		return nil, err
	}
	return f, nil
}

func enabledModuleNames(names []string) []string {
	known := permission.AvailableProjectModules()
	out := []string{}
	for _, n := range names {
		if n != "" && slices.Contains(known, n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// defaultMemberRole は Project.default_member_role（Setting.new_project_user_role_id の givable ロール、無ければ最初の givable）。
func (a *App) defaultMemberRole(c *Req) (*domain.Role, error) {
	roles, err := repository.GivableRoles(c.Ctx(), a.DB)
	if err != nil {
		return nil, err
	}
	id := int64(a.Settings.Int("new_project_user_role_id"))
	for _, r := range roles {
		if r.ID == id {
			return r, nil
		}
	}
	if len(roles) > 0 {
		return roles[0], nil
	}
	return nil, nil
}

// computeSafe は safe_attribute?(is_public / enabled_module_names / inherit_members) を計算する。
func (a *App) computeSafe(c *Req, f *projectForm) error {
	if f.id == 0 {
		if c.User.IsAdmin() {
			f.safeIsPublic, f.safeModules = true, true
		} else {
			r, err := a.defaultMemberRole(c)
			if err != nil {
				return err
			}
			f.safeIsPublic = r != nil && r.HasPermission("select_project_publicity")
			f.safeModules = r != nil && r.HasPermission("select_project_modules")
		}
	} else {
		f.safeIsPublic = c.AllowedTo(domain.Perm("select_project_publicity"), f.Project)
		f.safeModules = c.AllowedTo(domain.Perm("select_project_modules"), f.Project)
	}
	f.safeInheritMembers = true
	if f.ParentID != nil {
		parent, err := repository.GetProject(c.Ctx(), a.DB, *f.ParentID)
		if err == nil {
			f.safeInheritMembers = c.AllowedTo(domain.Perm("view_project"), parent)
		} else if !errors.Is(err, repository.ErrNotFound) {
			return err
		}
	}
	return nil
}

// allowedParents は Project#allowed_parents(User.current)。nil を含むかは別に返す。
func (a *App) allowedParents(c *Req, f *projectForm) ([]*domain.Project, bool, error) {
	if f.allowedParentsReady {
		return f.allowedParents, f.allowedParentsNil, nil
	}
	ctx := c.Ctx()
	cond, err := c.Authz().AllowedToCondition(ctx, "add_subprojects", authz.ConditionOptions{}, nil)
	if err != nil {
		return nil, false, err
	}
	ps, err := repository.LoadProjects(ctx, a.DB, cond)
	if err != nil {
		return nil, false, err
	}
	var self []int64
	if f.id != 0 {
		if self, err = repository.ProjectSelfAndDescendantIDs(ctx, a.DB, f.id); err != nil {
			return nil, false, err
		}
	}
	var out []*domain.Project
	for _, p := range ps {
		if !slices.Contains(self, p.ID) {
			out = append(out, p)
		}
	}
	withNil := c.AllowedToGlobally(domain.Perm("add_project")) || (f.id != 0 && f.parentWas == nil)
	// unless parent.nil? || @allowed_parents.empty? || @allowed_parents.include?(parent): << parent
	if f.parentWas != nil && (len(out) > 0 || withNil) {
		found := false
		for _, p := range out {
			if p.ID == *f.parentWas {
				found = true
			}
		}
		if !found {
			if parent, err := repository.GetProject(ctx, a.DB, *f.parentWas); err == nil {
				out = append(out, parent)
			} else if !errors.Is(err, repository.ErrNotFound) {
				return nil, false, err
			}
		}
	}
	f.allowedParents, f.allowedParentsNil, f.allowedParentsReady = out, withNil, true
	return out, withNil, nil
}

// assignProject は @project.safe_attributes = params[:project]。
func (a *App) assignProject(c *Req, f *projectForm, attrs *httpx.Params) error {
	if attrs == nil {
		return nil
	}
	ctx := c.Ctx()
	// 親プロジェクトの検査（safe_attributes= の @unallowed_parent_id）
	f.unallowedParentID = false
	if f.id == 0 || attrs.Has("parent_id") {
		param := attrs.String("parent_id")
		cur := ""
		if f.ParentID != nil {
			cur = strconv.FormatInt(*f.ParentID, 10)
		}
		if f.id == 0 || param != cur {
			var target *domain.Project
			if strings.TrimSpace(param) != "" {
				if id, ok := parseIntStrict(param); ok {
					if p, err := repository.GetProject(ctx, a.DB, id); err == nil {
						target = p
					} else if !errors.Is(err, repository.ErrNotFound) {
						return err
					}
				}
			}
			allowed, withNil, err := a.allowedParents(c, f)
			if err != nil {
				return err
			}
			ok := false
			if target == nil {
				ok = withNil
			} else {
				for _, p := range allowed {
					if p.ID == target.ID {
						ok = true
					}
				}
			}
			if !ok {
				attrs = attrs.Except("parent_id")
				f.unallowedParentID = true
			}
		}
	}
	if err := a.computeSafe(c, f); err != nil {
		return err
	}
	p := f.Project
	for _, key := range attrs.Keys() {
		switch key {
		case "name":
			p.Name = attrs.String(key)
		case "description":
			p.Description = attrs.String(key)
		case "homepage":
			p.Homepage = attrs.String(key)
		case "identifier":
			if !f.IdentifierFrozen() {
				p.Identifier = attrs.String(key)
				f.identifierAssigned = true
			}
		case "parent_id":
			p.ParentID = optionalID(attrs.String(key))
			// safe_attribute?('inherit_members') は親に依存する
			if err := a.computeSafe(c, f); err != nil {
				return err
			}
		case "tracker_ids":
			f.TrackerIDs = paramIDs(attrs.Strings(key))
		case "issue_custom_field_ids":
			f.IssueCustomFieldIDs = paramIDs(attrs.Strings(key))
		case "default_version_id":
			p.DefaultVersionID = optionalID(attrs.String(key))
		case "default_assigned_to_id":
			p.DefaultAssignedToID = optionalID(attrs.String(key))
		case "default_issue_query_id":
			p.DefaultIssueQueryID = optionalID(attrs.String(key))
		case "is_public":
			if f.safeIsPublic {
				p.IsPublic = castBool(attrs.String(key))
			}
		case "enabled_module_names":
			if f.safeModules {
				p.EnabledModuleNames = enabledModuleNames(attrs.Strings(key))
			}
		case "inherit_members":
			if f.safeInheritMembers {
				p.InheritMembers = castBool(attrs.String(key))
			}
		case "custom_field_values":
			vis, err := a.visibleCustomFieldValues(c, f.Project, f.CFValues)
			if err != nil {
				return err
			}
			assignCustomFieldValues(vis, attrs.Map(key))
			f.cfChanged = true
		}
	}
	return nil
}

func parseIntStrict(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

var projectIdentifierRe = regexp.MustCompile(`^[a-z0-9\-_]*$`)
var digitsOnlyRe = regexp.MustCompile(`^\d+$`)

// validateProject は Project の valid?。
func (a *App) validateProject(c *Req, f *projectForm) error {
	ctx := c.Ctx()
	f.errs = &domain.ValidationErrors{}
	p := f.Project
	// acts_as_customizable の validate_custom_field_values（最初に登録される）
	if f.id == 0 || f.cfChanged {
		vis, err := a.visibleCustomFieldValues(c, p, f.CFValues)
		if err != nil {
			return err
		}
		validateCustomFieldValues(f.errs, vis, c.L)
	}
	if strings.TrimSpace(p.Name) == "" {
		f.errs.Add("name", "blank", nil)
	}
	if strings.TrimSpace(p.Identifier) == "" {
		f.errs.Add("identifier", "blank", nil)
	}
	identifierChanged := p.Identifier != f.identifierWas
	if identifierChanged {
		taken, err := repository.ProjectIdentifierTaken(ctx, a.DB, p.Identifier, f.id)
		if err != nil {
			return err
		}
		if taken {
			f.errs.Add("identifier", "taken", nil)
		}
	}
	if len([]rune(p.Name)) > 255 {
		f.errs.Add("name", "too_long", map[string]any{"count": 255})
	}
	if len([]rune(p.Homepage)) > 255 {
		f.errs.Add("homepage", "too_long", map[string]any{"count": 255})
	}
	if len([]rune(p.Identifier)) > projectIdentifierMaxLength {
		f.errs.Add("identifier", "too_long", map[string]any{"count": projectIdentifierMaxLength})
	}
	if identifierChanged && (!projectIdentifierRe.MatchString(p.Identifier) || digitsOnlyRe.MatchString(p.Identifier)) {
		f.errs.Add("identifier", "invalid", nil)
	}
	if p.Identifier == "new" {
		f.errs.Add("identifier", "exclusion", nil)
	}
	// validate_parent
	if f.unallowedParentID {
		f.errs.Add("parent_id", "invalid", nil)
	} else if !sameIDPtr(p.ParentID, f.parentWas) && p.ParentID != nil {
		parent, err := repository.GetProject(ctx, a.DB, *p.ParentID)
		switch {
		case errors.Is(err, repository.ErrNotFound):
			f.errs.Add("parent_id", "invalid", nil)
		case err != nil:
			return err
		default:
			movable := true
			if f.id != 0 {
				ids, err := repository.ProjectSelfAndDescendantIDs(ctx, a.DB, f.id)
				if err != nil {
					return err
				}
				movable = !slices.Contains(ids, parent.ID)
			}
			if !parent.Active() || !movable {
				f.errs.Add("parent_id", "invalid", nil)
			}
		}
	}
	return nil
}

func sameIDPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// saveProject は @project.save（作成時は閉包・モジュール・トラッカー・CF・継承メンバーも保存）。
func (a *App) saveProject(c *Req, f *projectForm, tx *db.Tx) error {
	ctx := c.Ctx()
	p := f.Project
	if f.id == 0 {
		if err := repository.CreateProject(ctx, tx, p, repository.CreateProjectOptions{
			EnabledModules: p.EnabledModuleNames, TrackerIDs: f.TrackerIDs,
		}); err != nil {
			return err
		}
		f.id = p.ID
		if p.ParentID != nil && p.InheritMembers {
			// after_save :update_inherited_members（inherit_members の変化）と
			// :remove_inherited_member_roles, :add_inherited_member_roles（parent_id の変化）の 2 回目
			if err := repository.ReapplyInheritedMemberRoles(ctx, tx, p); err != nil {
				return err
			}
		}
	} else {
		if err := repository.UpdateProject(ctx, tx, p); err != nil {
			return err
		}
		if err := repository.SetEnabledModules(ctx, tx, p.ID, p.EnabledModuleNames); err != nil {
			return err
		}
		if err := repository.SetProjectTrackers(ctx, tx, p.ID, f.TrackerIDs); err != nil {
			return err
		}
	}
	if err := repository.SetProjectIssueCustomFields(ctx, tx, p.ID, f.IssueCustomFieldIDs); err != nil {
		return err
	}
	if f.cfChanged || f.identifierWas == "" {
		if err := saveCustomFieldValues(ctx, tx, p.ID, f.CFValues); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- parent_project_select_tag

// parentProjectSelectTag は ProjectsHelper#parent_project_select_tag(project)。
func (a *App) parentProjectSelectTag(c *Req, f *projectForm) (template.HTML, error) {
	ctx := c.Ctx()
	selected := f.ParentID
	paramParent, hasParam := "", false
	if pm := c.Params().Map("project"); pm != nil && pm.Has("parent_id") {
		paramParent, hasParam = pm.String("parent_id"), true
	} else if c.Params().Has("parent_id") {
		paramParent, hasParam = c.Params().String("parent_id"), true
	}
	if hasParam {
		if strings.TrimSpace(paramParent) == "" {
			selected = nil
		} else if p, err := repository.FindProject(ctx, a.DB, paramParent); err == nil {
			id := p.ID
			selected = &id
		} else if !errors.Is(err, repository.ErrNotFound) {
			return "", err
		} else {
			selected = nil
		}
	}
	allowed, withNil, err := a.allowedParents(c, f)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if withNil {
		b.WriteString("<option value=''>&nbsp;</option>")
	}
	opts, err := a.projectTreeOptionsForSelect(c, allowed, selected)
	if err != nil {
		return "", err
	}
	b.WriteString(string(opts))
	return rails.ContentTag("select", template.HTML(b.String()), rails.NewHash("name", "project[parent_id]", "id", "project_parent_id")), nil
}

// projectTreeOptionsForSelect は ApplicationHelper#project_tree_options_for_select（:selected のみ）。
func (a *App) projectTreeOptionsForSelect(c *Req, projects []*domain.Project, selected *int64) (template.HTML, error) {
	tree, err := a.loadProjectTree(c)
	if err != nil {
		return "", err
	}
	sorted := slices.Clone(projects)
	slices.SortStableFunc(sorted, func(x, y *domain.Project) int { return tree.ns[x.ID].Lft - tree.ns[y.ID].Lft })
	var b strings.Builder
	var ancestors []*domain.Project
	for _, p := range sorted {
		for len(ancestors) > 0 && !tree.isDescendantOf(p, ancestors[len(ancestors)-1]) {
			ancestors = ancestors[:len(ancestors)-1]
		}
		level := len(ancestors)
		name := string(rails.H(p.Name))
		if level > 0 {
			name = strings.Repeat("&nbsp;&nbsp;", level) + "&#187; " + name
		}
		attrs := rails.NewHash("value", p.ID)
		if selected != nil && *selected == p.ID {
			attrs.Set("selected", "selected")
		}
		b.WriteString(string(rails.ContentTag("option", template.HTML(name), attrs)))
		ancestors = append(ancestors, p)
	}
	return template.HTML(b.String()), nil
}

// rubySucc は String#succ（英数字の末尾から繰り上げる簡易版）。
func rubySucc(s string) string {
	if s == "" {
		return ""
	}
	b := []byte(s)
	hasAlnum := false
	for _, ch := range b {
		if isAlnum(ch) {
			hasAlnum = true
			break
		}
	}
	if !hasAlnum {
		b[len(b)-1]++
		return string(b)
	}
	i := len(b) - 1
	for i >= 0 && !isAlnum(b[i]) {
		i--
	}
	for {
		switch {
		case b[i] == 'z':
			b[i] = 'a'
		case b[i] == 'Z':
			b[i] = 'A'
		case b[i] == '9':
			b[i] = '0'
		default:
			b[i]++
			return string(b)
		}
		// 繰り上げ: 左の英数字を探す
		j := i - 1
		for j >= 0 && !isAlnum(b[j]) {
			j--
		}
		if j < 0 {
			var ins byte
			switch {
			case b[i] == 'a':
				ins = 'a'
			case b[i] == 'A':
				ins = 'A'
			default:
				ins = '1'
			}
			return string(b[:i]) + string(ins) + string(b[i:])
		}
		i = j
	}
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
