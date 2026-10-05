// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"context"
	"html/template"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/csvimport"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
)

// このファイルは IssueImport（app/models/issue_import.rb）の移植。

// issueImportState は IssueImport のリクエスト内キャッシュ。
type issueImportState struct {
	env      *issues.Env
	projects []*domain.Project
	loaded   bool
}

// issueRelationTypes は IssueRelation::TYPES のキーの順。
var issueRelationTypes = []string{
	domain.RelationRelates, domain.RelationDuplicates, domain.RelationDuplicated, domain.RelationBlocks,
	domain.RelationBlocked, domain.RelationPrecedes, domain.RelationFollows, domain.RelationCopiedTo, domain.RelationCopiedFrom,
}

func (m *importModel) issueState() *issueImportState {
	if m.issue == nil {
		env := issues.NewEnv(m.a.DB, m.a.Settings, m.c.User)
		env.Now = m.a.now
		// 保存後の env.Dispatch の配送先（通知の有無は Issue#notify = settings['notifications']）
		env.Notifier = m.a.issueNotifier()
		env.Translate = m.c.L
		env.DateFormat = m.c.Loc.FormatDate
		m.issue = &issueImportState{env: env}
	}
	return m.issue
}

// issueAllowedTargetProjects は allowed_target_projects（Project.allowed_to(user, :import_issues)。id 順）。
func (m *importModel) issueAllowedTargetProjects() ([]*domain.Project, error) {
	st := m.issueState()
	if st.loaded {
		return st.projects, nil
	}
	ps, err := m.allowedProjects("import_issues")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	st.projects, st.loaded = ps, true
	return ps, nil
}

// allowedProjects は Project.allowed_to(user, perm)。
func (m *importModel) allowedProjects(perm string) ([]*domain.Project, error) {
	cond, err := authz.New(m.a.DB, m.user).AllowedToCondition(m.c.Ctx(), perm, authz.ConditionOptions{}, nil)
	if err != nil {
		return nil, err
	}
	return repository.LoadProjects(m.c.Ctx(), m.a.DB, cond)
}

// issueProject は IssueImport#project（mapping の project_id、無ければ先頭）。
func (m *importModel) issueProject() (*domain.Project, error) {
	ps, err := m.issueAllowedTargetProjects()
	if err != nil {
		return nil, err
	}
	return importFindProject(ps, m.mappingValue("project_id")), nil
}

func importFindProject(ps []*domain.Project, v any) *domain.Project {
	id := rubyStrToI(importToS(v))
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	if len(ps) > 0 {
		return ps[0]
	}
	return nil
}

// rubyStrToI は String#to_i。
func rubyStrToI(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	m := regexp.MustCompile(`^[+-]?\d+`).FindString(strings.ReplaceAll(s, "_", ""))
	n, _ := strconv.ParseInt(m, 10, 64)
	return n
}

// issueAllowedTargetTrackers は allowed_target_trackers（Issue.allowed_target_trackers(project, user)）。
func (m *importModel) issueAllowedTargetTrackers() ([]*domain.Tracker, error) {
	p, err := m.issueProject()
	if err != nil || p == nil {
		return nil, err
	}
	env := m.issueState().env
	iss := &issues.Issue{}
	iss.ProjectID = p.ID
	return env.AllowedTargetTrackers(m.c.Ctx(), iss, m.user)
}

var importValueRe = regexp.MustCompile(`\Avalue:(\d+)\z`)

// issueTracker は IssueImport#tracker（mapping['tracker'] が "value:<id>" のとき）。
func (m *importModel) issueTracker() (*domain.Tracker, error) {
	mm := importValueRe.FindStringSubmatch(importToS(m.mappingValue("tracker")))
	if mm == nil {
		return nil, nil
	}
	id, _ := strconv.ParseInt(mm[1], 10, 64)
	ts, err := m.issueAllowedTargetTrackers()
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

// issueAllowedTo は user.allowed_to?(perm, import.project)。
func (m *importModel) issueAllowedTo(perm string) bool {
	p, err := m.issueProject()
	if err != nil || p == nil {
		return false
	}
	ok, err := authz.New(m.a.DB, m.user).AllowedTo(m.c.Ctx(), domain.Perm(perm), p)
	return err == nil && ok
}

// issueCreateCategories は create_categories?。
func (m *importModel) issueCreateCategories() bool {
	return m.issueAllowedTo("manage_categories") && importToS(m.mappingValue("create_categories")) == "1"
}

// issueCreateVersions は create_versions?。
func (m *importModel) issueCreateVersions() bool {
	return m.issueAllowedTo("manage_versions") && importToS(m.mappingValue("create_versions")) == "1"
}

// issueMappableCustomFields は IssueImport#mappable_custom_fields。
func (m *importModel) issueMappableCustomFields() ([]importCF, error) {
	ctx := m.c.Ctx()
	env := m.issueState().env
	t, err := m.issueTracker()
	if err != nil {
		return nil, err
	}
	p, err := m.issueProject()
	if err != nil {
		return nil, err
	}
	var cfs []*customfield.CustomField
	switch {
	case t != nil:
		iss, err := env.NewBlank(ctx)
		if err != nil {
			return nil, err
		}
		if p != nil {
			if err := env.SetProject(ctx, iss, p, false); err != nil {
				return nil, err
			}
		}
		if err := env.SetTracker(ctx, iss, t); err != nil {
			return nil, err
		}
		if cfs, err = env.EditableCustomFields(ctx, iss, m.user); err != nil {
			return nil, err
		}
	case p != nil:
		// project.all_issue_custom_fields
		all, err := env.IssueCustomFields(ctx)
		if err != nil {
			return nil, err
		}
		for _, cf := range all {
			if cf.IsForAll || slices.Contains(cf.ProjectIDs, p.ID) {
				cfs = append(cfs, cf)
			}
		}
	}
	out := make([]importCF, 0, len(cfs))
	for _, cf := range cfs {
		out = append(out, importCF{ID: cf.ID, Name: cf.Name, IsRequired: cf.IsRequired, Format: cf.FieldFormat})
	}
	return out, nil
}

// importNamed は scope :named（LOWER(name) = LOWER(arg.strip)）。
func importNamed(name, arg string) bool {
	return asciiLower(name) == asciiLower(strings.TrimSpace(arg))
}

// issueBuildAndSave は IssueImport#build_object と object.save。
func (m *importModel) issueBuildAndSave(ctx context.Context, row csvimport.Row, item *repository.ImportItem) (importResult, error) {
	st := m.issueState()
	env := st.env
	iss, err := env.NewBlank(ctx)
	if err != nil {
		return importResult{}, err
	}
	iss.AuthorID = m.user.ID
	iss.SetNotify(importCastBool(m.Settings.V["notifications"]))

	var trackerID any
	if t, err := m.issueTracker(); err != nil {
		return importResult{}, err
	} else if t != nil {
		trackerID = t.ID
	} else if name := m.rowValue(row, "tracker"); name != nil {
		ts, err := m.issueAllowedTargetTrackers()
		if err != nil {
			return importResult{}, err
		}
		for _, t := range ts {
			if importNamed(t.Name, *name) {
				trackerID = t.ID
				break
			}
		}
	}
	// チケットのプロジェクトは import.project に揃える（権限・カテゴリ・バージョンの作成可否と同じプロジェクト）
	var importProjectID any
	if ip, err := m.issueProject(); err != nil {
		return importResult{}, err
	} else if ip != nil {
		importProjectID = strconv.FormatInt(ip.ID, 10)
	}
	attrs := issues.Params{
		"project_id":  importProjectID,
		"tracker_id":  trackerID,
		"subject":     strPtrAny(m.rowValue(row, "subject")),
		"description": strPtrAny(m.rowValue(row, "description")),
	}
	if name := m.rowValue(row, "status"); name != nil {
		sts, err := env.Statuses(ctx)
		if err != nil {
			return importResult{}, err
		}
		// IssueStatus.named(name).first（id 順）
		var found *domain.IssueStatus
		for _, s := range sts {
			if importNamed(s.Name, *name) && (found == nil || s.ID < found.ID) {
				found = s
			}
		}
		if found != nil {
			attrs["status_id"] = found.ID
		}
	}
	if err := env.SafeAssign(ctx, iss, attrs, m.user); err != nil {
		return importResult{}, err
	}

	attrs = issues.Params{}
	if name := m.rowValue(row, "priority"); name != nil {
		ps, err := env.Priorities(ctx)
		if err != nil {
			return importResult{}, err
		}
		for _, p := range ps {
			if p.Active && importNamed(p.Name, *name) {
				attrs["priority_id"] = p.ID
				break
			}
		}
	}
	var project *domain.Project
	if iss.ProjectID != 0 {
		if project, err = env.Project(ctx, iss.ProjectID); err != nil {
			return importResult{}, err
		}
	}
	if name := m.rowValue(row, "category"); project != nil && name != nil {
		cats, err := repository.ProjectIssueCategories(ctx, m.a.DB, project.ID)
		if err != nil {
			return importResult{}, err
		}
		var found *repository.IssueCategoryInfo
		for _, cat := range cats {
			if importNamed(cat.Name, *name) {
				found = cat
				break
			}
		}
		if found != nil {
			attrs["category_id"] = found.ID
		} else if m.issueCreateCategories() {
			if id, ok, err := m.importCreateCategory(ctx, project.ID, *name); err != nil {
				return importResult{}, err
			} else if ok {
				attrs["category_id"] = id
			}
		}
	}
	if name := m.rowValue(row, "assigned_to"); name != nil {
		refs, err := env.AssignableUsers(ctx, iss)
		if err != nil {
			return importResult{}, err
		}
		ps, err := m.keywordPrincipalsFromRefs(ctx, refs)
		if err != nil {
			return importResult{}, err
		}
		if p := detectByKeyword(ps, *name); p != nil {
			attrs["assigned_to_id"] = p.ID
		}
	}
	if name := m.rowValue(row, "fixed_version"); project != nil && name != nil {
		version, err := m.importFindVersion(ctx, project, *name)
		if err != nil {
			return importResult{}, err
		}
		if version != nil {
			attrs["fixed_version_id"] = version.ID
		} else if m.issueCreateVersions() {
			if id, ok, err := m.importCreateVersion(ctx, project, *name); err != nil {
				return importResult{}, err
			} else if ok {
				attrs["fixed_version_id"] = id
			}
		}
	}
	if v := m.rowValue(row, "is_private"); v != nil && m.yes(*v) {
		attrs["is_private"] = "1"
	}
	if pv := m.rowValue(row, "parent_issue_id"); pv != nil {
		parent := *pv
		switch {
		case strings.HasPrefix(parent, "#"):
			// 既存のチケット
			attrs["parent_issue_id"] = parent[1:]
		case m.useUniqueID():
			// unique id で他の行を参照
			id, err := repository.ImportItemObjIDByUniqueID(ctx, m.a.DB, m.ID, parent)
			if err != nil {
				return importResult{}, err
			}
			if id != nil {
				attrs["parent_issue_id"] = *id
			} else if err := m.addCallback(parent, "set_as_parent", item.Position); err != nil {
				return importResult{}, err
			}
		case regexp.MustCompile(`\A\d+\z`).MatchString(parent):
			// 位置で他の行を参照
			pos, _ := strconv.Atoi(parent)
			if pos > item.Position {
				if err := m.addCallback(pos, "set_as_parent", item.Position); err != nil {
					return importResult{}, err
				}
			} else if id, err := repository.ImportItemObjIDByPosition(ctx, m.a.DB, m.ID, pos); err != nil {
				return importResult{}, err
			} else if id != nil {
				attrs["parent_issue_id"] = *id
			}
		default:
			// 不正な値（検証エラーにするためそのまま代入する）
			attrs["parent_issue_id"] = parent
		}
	}
	if v := m.rowDate(row, "start_date"); v != nil {
		attrs["start_date"] = *v
	}
	if v := m.rowDate(row, "due_date"); v != nil {
		attrs["due_date"] = *v
	}
	if v := m.rowValue(row, "estimated_hours"); v != nil {
		attrs["estimated_hours"] = *v
	}
	if v := m.rowValue(row, "done_ratio"); v != nil {
		attrs["done_ratio"] = *v
	}
	cfvs, err := env.CustomFieldValues(ctx, iss)
	if err != nil {
		return importResult{}, err
	}
	cfAttrs := map[string]any{}
	for _, v := range cfvs {
		cf := v.Field
		key := "cf_" + strconv.FormatInt(cf.ID, 10)
		var value *string
		if cf.FieldFormat == "date" {
			value = m.rowDate(row, key)
		} else {
			value = m.rowValue(row, key)
		}
		if value != nil {
			cz := &customfield.Customized{Kind: "issue", ProjectID: iss.ProjectID}
			if project != nil {
				cz.ProjectIdentifier = project.Identifier
			}
			cfAttrs[strconv.FormatInt(cf.ID, 10)] = m.cfValueFromKeyword(ctx, cf, *value, cz)
		}
	}
	attrs["custom_field_values"] = cfAttrs
	if err := env.SafeAssign(ctx, iss, attrs, m.user); err != nil {
		return importResult{}, err
	}
	if tid, ok := trackerID.(int64); !ok || iss.TrackerID != tid {
		if ok || iss.TrackerID != 0 {
			if err := env.SetTrackerID(ctx, iss, 0); err != nil {
				return importResult{}, err
			}
		}
	}

	saved, res, err := env.Save(ctx, iss)
	if err != nil {
		return importResult{}, err
	}
	if !saved {
		// human_attribute_name は訳が無ければ属性名（カスタムフィールド名）そのもの
		tr := func(key string, args ...any) string {
			if strings.HasPrefix(key, "field_") && !m.a.Bundle.Exists(m.c.Loc.Lang, key) {
				return strings.TrimPrefix(key, "field_")
			}
			return m.c.L(key, args...)
		}
		return importResult{obj: iss, message: strings.Join(iss.Errors.FullMessages(tr), "\n")}, nil
	}
	if res != nil {
		if err := env.Dispatch(ctx, res.Notifications); err != nil {
			m.a.logger().Error("import issue notification", "err", err)
		}
	}
	return importResult{obj: iss, persisted: true, objID: iss.ID}, nil
}

func strPtrAny(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// importCreateCategory は issue.project.issue_categories.build + save（検証に失敗したら ok=false）。
func (m *importModel) importCreateCategory(ctx context.Context, projectID int64, name string) (int64, bool, error) {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 60 {
		return 0, false, nil
	}
	taken, err := repository.IssueCategoryNameTaken(ctx, m.a.DB, projectID, name, 0)
	if err != nil || taken {
		return 0, false, err
	}
	cat := &repository.IssueCategoryInfo{ProjectID: projectID, Name: name}
	if err := repository.SaveIssueCategory(ctx, m.a.DB, cat); err != nil {
		return 0, false, err
	}
	return cat.ID, true, nil
}

// importFindVersion は project.versions.named(name).first || project.shared_versions.named(name).first。
func (m *importModel) importFindVersion(ctx context.Context, p *domain.Project, name string) (*domain.Version, error) {
	own, err := repository.LoadVersionsWhere(ctx, m.a.DB, "versions.project_id = ?", p.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range own {
		if importNamed(v.Name, name) {
			return v, nil
		}
	}
	shared, err := repository.LoadVersionsWhere(ctx, m.a.DB, repository.SharedVersionsCondition(p))
	if err != nil {
		return nil, err
	}
	for _, v := range shared {
		if importNamed(v.Name, name) {
			return v, nil
		}
	}
	return nil, nil
}

// importCreateVersion は issue.project.versions.build + save（検証に失敗したら ok=false）。
func (m *importModel) importCreateVersion(ctx context.Context, p *domain.Project, name string) (int64, bool, error) {
	f, err := m.a.newVersionForm(m.c, &domain.Version{ProjectID: p.ID, Status: "open", Sharing: "none"}, p)
	if err != nil {
		return 0, false, err
	}
	f.V.Name = name
	if err := m.a.validateVersion(m.c, f); err != nil {
		return 0, false, err
	}
	if f.errs.Any() {
		return 0, false, nil
	}
	if err := m.a.saveVersion(m.c, f, nil); err != nil {
		return 0, false, err
	}
	return f.V.ID, true, nil
}

// ---------------------------------------------------------------- 関連・親子

// issueRelationDecl は relation_values の 1 件。
type issueRelationDecl struct {
	matches  bool
	delay    *string
	otherID  string
	otherPos any // 位置（int）または unique id（string）
}

var issueRelationRe = regexp.MustCompile(`\A(?:(#)?(\d+)|(.+?))(?:\s+(-?\d+)d)?\z`)

// issueRelationValues は relation_values(row, name)。
func (m *importModel) issueRelationValues(row csvimport.Row, name string) []issueRelationDecl {
	content := m.rowValue(row, name)
	if content == nil || strings.TrimSpace(*content) == "" {
		return nil
	}
	var out []issueRelationDecl
	for _, decl := range strings.Split(*content, ",") {
		decl = strings.TrimSpace(decl)
		d := issueRelationDecl{}
		mm := issueRelationRe.FindStringSubmatch(decl)
		if mm != nil {
			d.matches = true
			if mm[4] != "" {
				delay := mm[4]
				d.delay = &delay
			}
			isID, id := mm[1] != "", mm[2]
			uid := mm[3]
			if uid == "" {
				uid = mm[1] + mm[2]
			}
			switch {
			case isID && id != "":
				d.otherID = id
			case m.useUniqueID() && uid != "":
				d.otherPos = uid
			case id != "":
				n, _ := strconv.Atoi(id)
				d.otherPos = n
			default:
				d.matches = false
			}
		}
		out = append(out, d)
	}
	return out
}

// issueBuildRelations は build_relations（extend_object）。
func (m *importModel) issueBuildRelations(ctx context.Context, row csvimport.Row, item *repository.ImportItem, res importResult) error {
	iss := res.obj.(*issues.Issue)
	for _, typ := range issueRelationTypes {
		hasDelay := typ == domain.RelationPrecedes || typ == domain.RelationFollows
		for _, decl := range m.issueRelationValues(row, "relation_"+typ) {
			if !decl.matches {
				continue
			}
			if decl.delay != nil && !hasDelay {
				continue
			}
			var toID int64
			switch {
			case decl.otherID != "":
				toID, _ = strconv.ParseInt(decl.otherID, 10, 64)
			case decl.otherPos != nil:
				if m.useUniqueID() {
					uid := importToS(decl.otherPos)
					id, err := repository.ImportItemObjIDByUniqueID(ctx, m.a.DB, m.ID, uid)
					if err != nil {
						return err
					}
					if id == nil {
						if err := m.addCallback(uid, "set_relation", item.Position, typ, strPtrAny(decl.delay)); err != nil {
							return err
						}
						continue
					}
					toID = *id
				} else if pos, _ := decl.otherPos.(int); pos > item.Position {
					if err := m.addCallback(pos, "set_relation", item.Position, typ, strPtrAny(decl.delay)); err != nil {
						return err
					}
					continue
				} else {
					id, err := repository.ImportItemObjIDByPosition(ctx, m.a.DB, m.ID, pos)
					if err != nil {
						return err
					}
					if id != nil {
						toID = *id
					}
				}
			}
			// relation.save! の例外は無視する
			if _, err := m.issueCreateRelation(ctx, iss.ID, toID, typ, strPtrAny(decl.delay)); err != nil {
				return err
			}
		}
	}
	return nil
}

// issueCreateRelation は IssueRelation.new(relation_type, issue_from_id, issue_to_id, delay).save（検証エラーは false）。
func (m *importModel) issueCreateRelation(ctx context.Context, fromID, toID int64, typ string, delay any) (bool, error) {
	env := m.issueState().env
	from, err := env.Find(ctx, fromID)
	if err != nil || from == nil {
		return false, err
	}
	r := &issues.Relation{From: from}
	r.RelationType = typ
	if toID != 0 {
		if r.To, err = env.Find(ctx, toID); err != nil {
			return false, err
		}
		// 見えないチケットは無いものとして扱う（関連は検証エラーで保存されない）
		if r.To != nil {
			vis, err := env.Visible(ctx, r.To, m.user)
			if err != nil {
				return false, err
			}
			if !vis {
				r.To = nil
			}
		}
	}
	if s := importToS(delay); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			r.Delay = &n
		}
	}
	ok, res, err := env.CreateRelation(ctx, r)
	if err != nil {
		return false, err
	}
	if ok && res != nil {
		if err := env.Dispatch(ctx, res.Notifications); err != nil {
			m.a.logger().Error("import relation notification", "err", err)
		}
	}
	return ok, nil
}

// issueSetAsParentCallback は set_as_parent_callback(issue, child_position)。
func (m *importModel) issueSetAsParentCallback(ctx context.Context, res importResult, args []any) error {
	if len(args) < 1 || !res.persisted {
		return nil
	}
	childID, err := repository.ImportItemObjIDByPosition(ctx, m.a.DB, m.ID, int(rubyStrToI(importToS(args[0]))))
	if err != nil || childID == nil {
		return err
	}
	env := m.issueState().env
	child, err := env.Find(ctx, *childID)
	if err != nil || child == nil {
		return err
	}
	if err := env.SetParentIssueID(ctx, child, strconv.FormatInt(res.objID, 10)); err != nil {
		return err
	}
	ok, sr, err := env.Save(ctx, child)
	if err != nil {
		return err
	}
	if !ok {
		// child.save! の失敗（Redmine では例外になる）
		m.a.logger().Warn("import: set_as_parent failed", "child", child.ID, "errors", strings.Join(child.Errors.FullMessages(m.c.L), ", "))
		return nil
	}
	if sr != nil {
		_ = env.Dispatch(ctx, sr.Notifications)
	}
	return nil
}

// issueSetRelationCallback は set_relation_callback(to_issue, from_position, type, delay)。
func (m *importModel) issueSetRelationCallback(ctx context.Context, res importResult, args []any) error {
	if !res.persisted || len(args) < 2 {
		return nil
	}
	fromID, err := repository.ImportItemObjIDByPosition(ctx, m.a.DB, m.ID, int(rubyStrToI(importToS(args[0]))))
	if err != nil || fromID == nil {
		return err
	}
	var delay any
	if len(args) > 2 {
		delay = args[2]
	}
	ok, err := m.issueCreateRelation(ctx, *fromID, res.objID, importToS(args[1]), delay)
	if err == nil && !ok {
		m.a.logger().Warn("import: set_relation failed", "from", *fromID, "to", res.objID)
	}
	return err
}

// ---------------------------------------------------------------- Principal.detect_by_keyword

// keywordPrincipal は detect_by_keyword の比較に使う属性。
type keywordPrincipal struct {
	ID        int64
	IsUser    bool
	Login     string
	Mail      string
	Firstname string
	Lastname  string
	Name      string
}

// detectByKeyword は Principal.detect_by_keyword(principals, keyword)。
func detectByKeyword(ps []keywordPrincipal, keyword string) *keywordPrincipal {
	if strings.TrimSpace(keyword) == "" {
		return nil
	}
	eq := func(a, b string) bool { return asciiLower(a) == asciiLower(b) }
	for i := range ps {
		if eq(keyword, ps[i].Login) {
			return &ps[i]
		}
	}
	for i := range ps {
		if eq(keyword, ps[i].Mail) {
			return &ps[i]
		}
	}
	if strings.Contains(keyword, " ") {
		f := strings.Fields(keyword)
		first, last := "", ""
		if len(f) > 0 {
			first = f[0]
		}
		if len(f) > 1 {
			last = f[1]
		}
		for i := range ps {
			if ps[i].IsUser && eq(first, ps[i].Firstname) && eq(last, ps[i].Lastname) {
				return &ps[i]
			}
		}
	}
	for i := range ps {
		if eq(keyword, ps[i].Name) {
			return &ps[i]
		}
	}
	return nil
}

// keywordPrincipalsFromRefs は担当者候補（PrincipalRef）にメールアドレスを補う。
func (m *importModel) keywordPrincipalsFromRefs(ctx context.Context, refs []*issues.PrincipalRef) ([]keywordPrincipal, error) {
	var ids []int64
	for _, r := range refs {
		ids = append(ids, r.ID)
	}
	users, err := repository.UsersByIDs(ctx, m.a.DB, ids)
	if err != nil {
		return nil, err
	}
	format := m.a.Settings.String("user_format")
	out := make([]keywordPrincipal, 0, len(refs))
	for _, r := range refs {
		kp := keywordPrincipal{ID: r.ID, IsUser: r.IsUser(), Login: r.Login, Firstname: r.Firstname, Lastname: r.Lastname,
			Name: r.DisplayName(format)}
		if u := users[r.ID]; u != nil {
			kp.Mail = u.Mail
		}
		out = append(out, kp)
	}
	return out, nil
}

// keywordPrincipalsFromUsers は User の列を keywordPrincipal にする。
func (m *importModel) keywordPrincipalsFromUsers(us []*domain.User) []keywordPrincipal {
	format := m.a.Settings.String("user_format")
	out := make([]keywordPrincipal, 0, len(us))
	for _, u := range us {
		out = append(out, keywordPrincipal{ID: u.ID, IsUser: true, Login: u.Login, Mail: u.Mail, Firstname: u.Firstname,
			Lastname: u.Lastname, Name: u.Name(format)})
	}
	return out
}

// cfValueFromKeyword は custom_field.value_from_keyword(keyword, object)。
func (m *importModel) cfValueFromKeyword(ctx context.Context, cf *customfield.CustomField, keyword string, cz *customfield.Customized) any {
	l := m.a.newIssueLookup(m.c)
	if cf.FieldFormat == "user" {
		// UserFormat#value_from_keyword: Principal.detect_by_keyword(possible_values_records, k)
		var users []*domain.User
		if cz != nil && cz.ProjectID != 0 {
			var roleIDs []int64
			for _, r := range cf.SettingList("user_role") {
				if strings.TrimSpace(r) != "" {
					roleIDs = append(roleIDs, customfield.RubyToI(r))
				}
			}
			ids, err := repository.ProjectMemberUserIDs(ctx, m.a.DB, cz.ProjectID, roleIDs)
			if err != nil {
				m.a.logger().Error("import user cf", "err", err)
			}
			um, err := repository.UsersByIDs(ctx, m.a.DB, ids)
			if err != nil {
				m.a.logger().Error("import user cf", "err", err)
			}
			for _, id := range ids {
				if u := um[id]; u != nil && u.Kind == domain.KindUser {
					users = append(users, u)
				}
			}
			l.sortUsersByFormat(users)
		}
		ps := m.keywordPrincipalsFromUsers(users)
		find := func(k string) (string, bool) {
			if p := detectByKeyword(ps, k); p != nil {
				return strconv.FormatInt(p.ID, 10), true
			}
			return "", false
		}
		return importParseKeyword(cf, keyword, find)
	}
	env := l.cfEnv(cf)
	env.ProjectUsers = func(projectID int64, roleIDs []int64) []customfield.Option {
		return l.projectUserOptions(projectID, roleIDs)
	}
	env.SharedVersions = func(projectID int64, statuses []string) []customfield.Option {
		return l.sharedVersionOptions(projectID, statuses)
	}
	var obj any
	if cz != nil {
		obj = cz
	}
	return customfield.FindFormat(cf.FieldFormat).ValueFromKeyword(env, cf, keyword, obj)
}

// importParseKeyword は Base#parse_keyword（複数値はカンマ区切りを最長一致で分割する）。
func importParseKeyword(cf *customfield.CustomField, keyword string, find func(string) (string, bool)) any {
	if !cf.Multiple {
		if v, ok := find(strings.TrimSpace(keyword)); ok {
			return v
		}
		return nil
	}
	values := []string{}
	for len(keyword) > 0 {
		k := keyword
		for {
			if v, ok := find(strings.TrimSpace(k)); ok {
				values = append(values, v)
				break
			}
			i := strings.LastIndex(k, ",")
			if i < 0 {
				break
			}
			k = k[:i]
		}
		keyword = strings.TrimPrefix(keyword, k)
		keyword = strings.TrimPrefix(keyword, ",")
	}
	return values
}

// ---------------------------------------------------------------- 表示

// importSavedIssues は saved_objects（Issue.where(:id => ids).order(:id)）の link_to_issue と
// 「すべてのチケットを表示」の URL（issues_path(:set_filter => 1, :status_id => '*', :issue_id => ids)）。
func (a *App) importSavedIssues(c *Req, ids []int64) ([]template.HTML, string, error) {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	l := a.newIssueLookup(c)
	l.preloadIssues(sorted)
	l.preloadChildren(sorted)
	if l.err != nil {
		return nil, "", l.err
	}
	var links []template.HTML
	var strIDs []string
	for _, id := range sorted {
		r := l.issue(id)
		if r == nil {
			continue
		}
		strIDs = append(strIDs, strconv.FormatInt(id, 10))
		links = append(links, l.linkToIssue(r, redmine.LinkToIssueOptions{}))
	}
	href := "/issues?issue_id=" + strings.Join(strIDs, "%2C") + "&set_filter=1&status_id=%2A"
	return links, href, l.err
}

// importIssueMappingData は _issues_fields_mapping の値。
func (m *importModel) importIssueMappingData(data map[string]any) error {
	ps, err := m.issueAllowedTargetProjects()
	if err != nil {
		return err
	}
	p, err := m.issueProject()
	if err != nil {
		return err
	}
	ts, err := m.issueAllowedTargetTrackers()
	if err != nil {
		return err
	}
	var trackerValues []any
	for _, t := range ts {
		trackerValues = append(trackerValues, []any{t.Name, t.ID})
	}
	data["Projects"] = ps
	data["Project"] = p
	data["TrackerValues"] = trackerValues
	data["CanManageCategories"] = m.issueAllowedTo("manage_categories")
	data["CreateCategories"] = m.issueCreateCategories()
	data["CanManageVersions"] = m.issueAllowedTo("manage_versions")
	data["CreateVersions"] = m.issueCreateVersions()
	return nil
}
