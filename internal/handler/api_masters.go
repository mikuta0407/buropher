package handler

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/permission"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルはマスタ系 API（roles / trackers / issue_statuses / enumerations）の
// .json / .xml 出力（app/views/*/index.api.rsb）。
//
// TODO: Redmine::Views::Builders の共通移植（別タスク）が入ったらそちらに置き換える。
// ここでは必要な形（スカラー・属性付き空要素・配列・入れ子）だけを持つ最小の木で出力する。

type apiKind int

const (
	apiScalar apiKind = iota
	apiObject
	apiArray
	apiAttrs
)

// apiEl は API 出力の 1 要素。
type apiEl struct {
	name     string
	kind     apiKind
	value    any
	attrs    [][2]any
	children []*apiEl
}

func apiField(name string, v any) *apiEl { return &apiEl{name: name, kind: apiScalar, value: v} }
func apiObj(name string, children ...*apiEl) *apiEl {
	return &apiEl{name: name, kind: apiObject, children: children}
}
func apiArr(name string, children ...*apiEl) *apiEl {
	return &apiEl{name: name, kind: apiArray, children: children}
}
func apiAttr(name string, attrs ...[2]any) *apiEl {
	return &apiEl{name: name, kind: apiAttrs, attrs: attrs}
}

// renderAPI は root（配列またはオブジェクト）を format（json / xml）で返す。
func (c *Req) renderAPI(root *apiEl, status int) {
	format := httpx.Format(c.R)
	var b strings.Builder
	if format == "xml" {
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
		writeXML(&b, root)
	} else {
		format = "json"
		b.WriteString("{")
		writeJSONKey(&b, root.name)
		writeJSON(&b, root)
		b.WriteString("}")
	}
	httpx.SetContentType(c.W, format, true)
	c.W.WriteHeader(status)
	_, _ = c.W.Write([]byte(b.String()))
	c.Halt()
}

func writeJSONKey(b *strings.Builder, k string) {
	kb, _ := json.Marshal(k)
	b.Write(kb)
	b.WriteString(":")
}

func jsonScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case *string:
		if x == nil {
			return "null"
		}
		return jsonScalar(*x)
	case *int64:
		if x == nil {
			return "null"
		}
		return strconv.FormatInt(*x, 10)
	case *int:
		if x == nil {
			return "null"
		}
		return strconv.Itoa(*x)
	}
	bs, _ := json.Marshal(v)
	return string(bs)
}

// writeJSON は要素の値部分（キーを除く）を書く。
func writeJSON(b *strings.Builder, e *apiEl) {
	switch e.kind {
	case apiScalar:
		b.WriteString(jsonScalar(e.value))
	case apiAttrs:
		b.WriteString("{")
		for i, a := range e.attrs {
			if i > 0 {
				b.WriteString(",")
			}
			writeJSONKey(b, a[0].(string))
			b.WriteString(jsonScalar(a[1]))
		}
		b.WriteString("}")
	case apiObject:
		b.WriteString("{")
		n := 0
		for _, a := range e.attrs {
			if n > 0 {
				b.WriteString(",")
			}
			n++
			writeJSONKey(b, a[0].(string))
			b.WriteString(jsonScalar(a[1]))
		}
		for _, ch := range e.children {
			if n > 0 {
				b.WriteString(",")
			}
			n++
			writeJSONKey(b, ch.name)
			writeJSON(b, ch)
		}
		b.WriteString("}")
	case apiArray:
		b.WriteString("[")
		for i, ch := range e.children {
			if i > 0 {
				b.WriteString(",")
			}
			writeJSON(b, ch)
		}
		b.WriteString("]")
	}
}

var xmlTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
var xmlAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func xmlScalar(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case *string:
		if x == nil {
			return "", false
		}
		return *x, true
	case *int64:
		if x == nil {
			return "", false
		}
		return strconv.FormatInt(*x, 10), true
	case *int:
		if x == nil {
			return "", false
		}
		return strconv.Itoa(*x), true
	case bool:
		return strconv.FormatBool(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case int:
		return strconv.Itoa(x), true
	case string:
		return x, true
	}
	return "", false
}

func writeXML(b *strings.Builder, e *apiEl) {
	switch e.kind {
	case apiScalar:
		s, ok := xmlScalar(e.value)
		if !ok {
			b.WriteString("<" + e.name + "/>")
			return
		}
		b.WriteString("<" + e.name + ">" + xmlTextEscaper.Replace(s) + "</" + e.name + ">")
	case apiAttrs:
		b.WriteString("<" + e.name)
		for _, a := range e.attrs {
			s, _ := xmlScalar(a[1])
			b.WriteString(" " + a[0].(string) + `="` + xmlAttrEscaper.Replace(s) + `"`)
		}
		b.WriteString("/>")
	case apiObject, apiArray:
		b.WriteString("<" + e.name)
		if e.kind == apiArray {
			b.WriteString(` type="array"`)
		}
		for _, a := range e.attrs {
			s, _ := xmlScalar(a[1])
			b.WriteString(" " + a[0].(string) + `="` + xmlAttrEscaper.Replace(s) + `"`)
		}
		if len(e.children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteString(">")
		for _, ch := range e.children {
			writeXML(b, ch)
		}
		b.WriteString("</" + e.name + ">")
	}
}

// ---------------------------------------------------------------- 各 API

// renderRolesAPI は roles/index.api.rsb。
func (a *App) renderRolesAPI(c *Req, roles []*domain.Role) {
	page := c.Page()
	arr := apiArr("roles")
	for _, r := range roles {
		arr.children = append(arr.children, apiObj("role", apiField("id", r.ID), apiField("name", helper.RoleName(page, r))))
	}
	c.renderAPI(arr, http.StatusOK)
}

// renderRoleAPI は roles/show.api.rsb。
func (a *App) renderRoleAPI(c *Req, r *domain.Role) {
	perms := apiArr("permissions")
	// buropher は権限の保存順を持たないため、Redmine::AccessControl の定義順で出す
	// （フィクスチャ・既定データの順序。Redmine で画面から保存した場合はモジュール名順になる）
	order := map[string]int{}
	for i, p := range permission.All() {
		order[p.Name] = i
	}
	names := slices.Clone(r.Permissions)
	slices.SortStableFunc(names, func(x, y string) int { return order[x] - order[y] })
	for _, p := range names {
		perms.children = append(perms.children, apiField("permission", p))
	}
	c.renderAPI(apiObj("role",
		apiField("id", r.ID),
		apiField("name", helper.RoleName(c.Page(), r)),
		apiField("assignable", r.Assignable),
		apiField("issues_visibility", r.IssuesVisibility),
		apiField("time_entries_visibility", r.TimeEntriesVisibility),
		apiField("users_visibility", r.UsersVisibility),
		perms,
	), http.StatusOK)
}

// renderTrackersAPI は trackers/index.api.rsb。
func (a *App) renderTrackersAPI(c *Req, trackers []*domain.Tracker, statuses map[int64]*domain.IssueStatus) {
	arr := apiArr("trackers")
	for _, t := range trackers {
		el := apiObj("tracker", apiField("id", t.ID), apiField("name", t.Name))
		if s := statuses[t.DefaultStatusID]; s != nil {
			el.children = append(el.children, apiAttr("default_status", [2]any{"id", s.ID}, [2]any{"name", s.Name}))
		}
		el.children = append(el.children, apiField("description", t.Description))
		fields := apiArr("enabled_standard_fields")
		for _, f := range t.CoreFields() {
			fields.children = append(fields.children, apiField("field", f))
		}
		el.children = append(el.children, fields)
		arr.children = append(arr.children, el)
	}
	c.renderAPI(arr, http.StatusOK)
}

// renderIssueStatusesAPI は issue_statuses/index.api.rsb。
func (a *App) renderIssueStatusesAPI(c *Req, statuses []*domain.IssueStatus) {
	arr := apiArr("issue_statuses")
	for _, s := range statuses {
		arr.children = append(arr.children, apiObj("issue_status",
			apiField("id", s.ID), apiField("name", s.Name), apiField("is_closed", s.IsClosed), apiField("description", s.Description)))
	}
	c.renderAPI(arr, http.StatusOK)
}

// renderEnumerationsAPI は enumerations/index.api.rsb（visible_custom_field_values を含む）。
func (a *App) renderEnumerationsAPI(c *Req, kind string, enums []*domain.Enumeration) {
	root := map[string][2]string{
		domain.EnumIssuePriority:     {"issue_priorities", "issue_priority"},
		domain.EnumTimeEntryActivity: {"time_entry_activities", "time_entry_activity"},
		domain.EnumDocumentCategory:  {"document_categories", "document_category"},
	}[kind]
	cfs, err := repository.CustomFieldInfosByKind(c.Ctx(), a.DB, domain.EnumerationCustomFieldKind(kind))
	if err != nil {
		a.internalError(c, "enumeration custom fields", err)
		return
	}
	arr := apiArr(root[0])
	for _, e := range enums {
		el := apiObj(root[1], apiField("id", e.ID), apiField("name", e.Name), apiField("is_default", e.IsDefault), apiField("active", e.Active))
		var visible []*domain.CustomFieldInfo
		for _, cf := range cfs {
			if cf.Visible || c.User.IsAdmin() {
				visible = append(visible, cf)
			}
		}
		if len(visible) > 0 {
			vals, err := repository.CustomValues(c.Ctx(), a.DB, "enumeration", e.ID)
			if err != nil {
				a.internalError(c, "enumeration custom values", err)
				return
			}
			el.children = append(el.children, apiCustomFields(visible, vals))
		}
		arr.children = append(arr.children, el)
	}
	c.renderAPI(arr, http.StatusOK)
}

// apiCustomFields は render_api_custom_values。
func apiCustomFields(cfs []*domain.CustomFieldInfo, vals map[int64][]string) *apiEl {
	arr := apiArr("custom_fields")
	for _, cf := range cfs {
		el := &apiEl{name: "custom_field", kind: apiObject, attrs: [][2]any{{"id", cf.ID}, {"name", cf.Name}}}
		vs := vals[cf.ID]
		if cf.Multiple {
			el.attrs = append(el.attrs, [2]any{"multiple", true})
			va := apiArr("value")
			for _, v := range vs {
				va.children = append(va.children, apiField("value", v))
			}
			el.children = append(el.children, va)
		} else {
			var v any
			if len(vs) > 0 {
				v = vs[0]
			}
			el.children = append(el.children, apiField("value", v))
		}
		arr.children = append(arr.children, el)
	}
	return arr
}
