package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは projects / memberships / issue_categories の REST API（*.api.rsb）。
//
// TODO(dedupe): 共通の API ビルダー（Redmine::Views::Builders の移植）がマージされたら、
// api_masters.go の apiEl とここのヘルパー（renderAPIRoot / apiOffsetAndLimitMin / apiMetaMin）を置き換える。

// apiOffsetAndLimitMin は ApplicationController#api_offset_and_limit。
func apiOffsetAndLimitMin(c *Req) (offset, limit int) {
	p := c.Params()
	hasOffset := false
	if s := p.String("offset"); strings.TrimSpace(s) != "" {
		offset = int(rubyToI(s))
		if offset < 0 {
			offset = 0
		}
		hasOffset = true
	}
	limit = int(rubyToI(p.String("limit")))
	if limit < 1 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}
	if !hasOffset && strings.TrimSpace(p.String("page")) != "" {
		offset = (int(rubyToI(p.String("page"))) - 1) * limit
		if offset < 0 {
			offset = 0
		}
	}
	return offset, limit
}

// apiMetaMin は api_meta(:total_count => .., :offset => .., :limit => ..)（nometa なら nil）。
func apiMetaMin(c *Req, total, offset, limit int) [][2]any {
	if c.Params().Present("nometa") || c.R.Header.Get("X-Redmine-Nometa") != "" {
		return nil
	}
	return [][2]any{{"total_count", total}, {"offset", offset}, {"limit", limit}}
}

// renderAPIRoot は root を API 形式で返す（root が配列なら meta を JSON ではルートの兄弟キー、
// XML では配列要素の属性として出す）。
func (c *Req) renderAPIRoot(root *apiEl, meta [][2]any, status int) {
	format := httpx.Format(c.R)
	var b strings.Builder
	if format == "xml" {
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
		if root.kind == apiArray && len(meta) > 0 {
			el := *root
			el.attrs = meta
			writeXMLArrayMeta(&b, &el)
		} else {
			writeXML(&b, root)
		}
	} else {
		format = "json"
		b.WriteString("{")
		writeJSONKey(&b, root.name)
		writeJSON(&b, root)
		for _, m := range meta {
			b.WriteString(",")
			writeJSONKey(&b, m[0].(string))
			b.WriteString(jsonScalar(m[1]))
		}
		b.WriteString("}")
	}
	httpx.SetContentType(c.W, format, true)
	c.W.WriteHeader(status)
	_, _ = c.W.Write([]byte(b.String()))
	c.Halt()
}

// writeXMLArrayMeta は meta 属性を type="array" の前に出す配列要素。
func writeXMLArrayMeta(b *strings.Builder, e *apiEl) {
	b.WriteString("<" + e.name)
	for _, a := range e.attrs {
		s, _ := xmlScalar(a[1])
		b.WriteString(" " + a[0].(string) + `="` + xmlAttrEscaper.Replace(s) + `"`)
	}
	b.WriteString(` type="array"`)
	b.WriteString(">")
	for _, ch := range e.children {
		writeXML(b, ch)
	}
	b.WriteString("</" + e.name + ">")
}

// apiTime は API の日時（xmlschema、UTC）。
func apiTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// ---------------------------------------------------------------- projects

// projectAPIEl は projects/index.api.rsb・show.api.rsb の project 要素。
func (a *App) projectAPIEl(c *Req, p *domain.Project, show bool) (*apiEl, error) {
	ctx := c.Ctx()
	el := apiObj("project",
		apiField("id", p.ID),
		apiField("name", p.Name),
		apiField("identifier", p.Identifier),
		apiField("description", projectDescriptionValue(p)),
		apiField("homepage", p.Homepage),
	)
	if p.ParentID != nil {
		parent, err := repository.GetProject(ctx, a.DB, *p.ParentID)
		if err == nil && c.AllowedTo(domain.Perm("view_project"), parent) {
			el.children = append(el.children, apiAttr("parent", [2]any{"id", parent.ID}, [2]any{"name", parent.Name}))
		}
	}
	el.children = append(el.children,
		apiField("status", p.Status),
		apiField("is_public", p.IsPublic),
		apiField("inherit_members", p.InheritMembers),
	)
	if show {
		if p.DefaultVersionID != nil {
			if v, err := repository.GetVersionInfo(ctx, a.DB, *p.DefaultVersionID); err == nil {
				el.children = append(el.children, apiAttr("default_version", [2]any{"id", v.ID}, [2]any{"name", v.Name}))
			}
		}
		if p.DefaultAssignedToID != nil {
			users, groups, err := repository.PrincipalsByIDs(ctx, a.DB, []int64{*p.DefaultAssignedToID})
			if err != nil {
				return nil, err
			}
			page := c.Page()
			var name string
			if u := users[*p.DefaultAssignedToID]; u != nil {
				name = helper.PrincipalName(page, u)
			} else if g := groups[*p.DefaultAssignedToID]; g != nil {
				name = helper.PrincipalName(page, g)
			}
			if name != "" {
				el.children = append(el.children, apiAttr("default_assignee", [2]any{"id", *p.DefaultAssignedToID}, [2]any{"name", name}))
			}
		}
	}
	// render_api_custom_values project.visible_custom_field_values, api
	vis, err := a.projectVisibleCustomFieldValues(c, p)
	if err != nil {
		return nil, err
	}
	if len(vis) > 0 {
		var infos []*domain.CustomFieldInfo
		vals := map[int64][]string{}
		for _, v := range vis {
			infos = append(infos, v.Field)
			vals[v.Field.ID] = v.Values
		}
		el.children = append(el.children, apiCustomFields(infos, vals))
	}
	inc, err := a.projectAPIIncludes(c, p)
	if err != nil {
		return nil, err
	}
	el.children = append(el.children, inc...)
	el.children = append(el.children, apiField("created_on", apiTime(p.CreatedAt)), apiField("updated_on", apiTime(p.UpdatedAt)))
	return el, nil
}

// projectDescriptionValue は API の description（NULL なら null）。
func projectDescriptionValue(p *domain.Project) any {
	if p.DescriptionNull {
		return nil
	}
	return p.Description
}

// includeInAPIResponseMin は include_in_api_response?(arg)。
func includeInAPIResponseMin(c *Req, arg string) bool {
	for _, v := range c.Params().Strings("include") {
		for _, s := range strings.Split(v, ",") {
			if strings.TrimSpace(s) == arg {
				return true
			}
		}
	}
	return false
}

// projectAPIIncludes は ProjectsHelper#render_api_includes。
func (a *App) projectAPIIncludes(c *Req, p *domain.Project) ([]*apiEl, error) {
	ctx := c.Ctx()
	var out []*apiEl
	if includeInAPIResponseMin(c, "trackers") {
		vis, err := trackerVisibleCondition(c)
		if err != nil {
			return nil, err
		}
		ts, err := repository.RolledUpTrackers(ctx, a.DB, p, false, vis)
		if err != nil {
			return nil, err
		}
		arr := apiArr("trackers")
		for _, t := range ts {
			arr.children = append(arr.children, apiAttr("tracker", [2]any{"id", t.ID}, [2]any{"name", t.Name}))
		}
		out = append(out, arr)
	}
	if includeInAPIResponseMin(c, "issue_categories") {
		cats, err := repository.ProjectIssueCategories(ctx, a.DB, p.ID)
		if err != nil {
			return nil, err
		}
		arr := apiArr("issue_categories")
		for _, cat := range cats {
			arr.children = append(arr.children, apiAttr("issue_category", [2]any{"id", cat.ID}, [2]any{"name", cat.Name}))
		}
		out = append(out, arr)
	}
	if includeInAPIResponseMin(c, "time_entry_activities") {
		acts, err := repository.ProjectActivities(ctx, a.DB, p.ID, false)
		if err != nil {
			return nil, err
		}
		arr := apiArr("time_entry_activities")
		for _, e := range acts {
			arr.children = append(arr.children, apiAttr("time_entry_activity", [2]any{"id", e.ID}, [2]any{"name", e.Name}))
		}
		out = append(out, arr)
	}
	if includeInAPIResponseMin(c, "enabled_modules") {
		mods, err := repository.ProjectEnabledModules(ctx, a.DB, p.ID)
		if err != nil {
			return nil, err
		}
		arr := apiArr("enabled_modules")
		for _, m := range mods {
			arr.children = append(arr.children, apiAttr("enabled_module", [2]any{"id", m.ID}, [2]any{"name", m.Name}))
		}
		out = append(out, arr)
	}
	if includeInAPIResponseMin(c, "issue_custom_fields") {
		ids, err := repository.ProjectIssueCustomFieldIDs(ctx, a.DB, p.ID)
		if err != nil {
			return nil, err
		}
		cfs, err := repository.CustomFieldInfosByKind(ctx, a.DB, "issue")
		if err != nil {
			return nil, err
		}
		arr := apiArr("issue_custom_fields")
		for _, cf := range cfs {
			if cf.IsForAll || containsID(ids, cf.ID) {
				arr.children = append(arr.children, apiAttr("custom_field", [2]any{"id", cf.ID}, [2]any{"name", cf.Name}))
			}
		}
		out = append(out, arr)
	}
	return out, nil
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// renderProjectShowAPI は projects/show.api.rsb。
func (a *App) renderProjectShowAPI(c *Req, p *domain.Project, status int) {
	el, err := a.projectAPIEl(c, p, true)
	if err != nil {
		a.internalError(c, "project api", err)
		return
	}
	c.renderAPIRoot(el, nil, status)
}

// renderProjectsIndexAPI は projects/index.api.rsb。
func (a *App) renderProjectsIndexAPI(c *Req, q *query.Query) {
	ctx := c.Ctx()
	offset, limit := apiOffsetAndLimitMin(c)
	count, err := q.Count(ctx)
	if err != nil {
		a.queryStatementError(c, err)
		return
	}
	ps, err := q.Projects(ctx, query.ListOptions{Offset: offset, Limit: limit})
	if err != nil {
		a.queryStatementError(c, err)
		return
	}
	arr := apiArr("projects")
	for _, p := range ps {
		el, err := a.projectAPIEl(c, p, false)
		if err != nil {
			a.internalError(c, "project api", err)
			return
		}
		arr.children = append(arr.children, el)
	}
	c.renderAPIRoot(arr, apiMetaMin(c, int(count), offset, limit), http.StatusOK)
}

var _ = authz.ConditionOptions{}
