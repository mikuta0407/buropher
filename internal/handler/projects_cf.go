// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"context"
	"html/template"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルはプロジェクトのカスタムフィールド（ProjectCustomField）の値の読み込み・表示・検証・保存
// （acts_as_customizable の custom_field_values / visible_custom_field_values / show_value の簡易移植）。
//
// TODO(customfield): Redmine::FieldFormat の完全な移植（user / version / enumeration / attachment 書式、
// text_formatting、url_pattern など）が入ったら置き換える。

// cfDisplay は render_custom_field_values が yield する (custom_field, formatted)。
type cfDisplay struct {
	Field     *domain.CustomFieldInfo
	Formatted template.HTML
}

// projectCFValue はプロジェクトのカスタムフィールドの値（CustomFieldValue）。
type projectCFValue = domain.CustomFieldValue

// projectCustomFieldValues は project.custom_field_values（全 ProjectCustomField、position 順）。
// 新規プロジェクト（p.ID == 0）は既定値で初期化する。
func (a *App) projectCustomFieldValues(c *Req, p *domain.Project) ([]*projectCFValue, error) {
	ctx := c.Ctx()
	infos, err := repository.ProjectCustomFieldInfos(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	var stored map[int64][]string
	if p.ID != 0 {
		stored, err = repository.CustomValues(ctx, a.DB, "project", p.ID)
		if err != nil {
			return nil, err
		}
	}
	out := make([]*projectCFValue, len(infos))
	for i, cf := range infos {
		v := &projectCFValue{Field: cf}
		if vals, ok := stored[cf.ID]; ok {
			v.Values = vals
		} else if p.ID == 0 && cf.DefaultValue != nil && *cf.DefaultValue != "" {
			v.Values = []string{*cf.DefaultValueOn(a.userToday(c))}
		}
		out[i] = v
	}
	return out, nil
}

// visibleCustomFieldValues は values のうち custom_field.visible_by?(project, User.current) のもの。
func (a *App) visibleCustomFieldValues(c *Req, p *domain.Project, values []*projectCFValue) ([]*projectCFValue, error) {
	ctx := c.Ctx()
	cfs, err := customfield.ListByKind(ctx, a.DB, customfield.KindProject)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*customfield.CustomField{}
	for _, cf := range cfs {
		byID[cf.ID] = cf
	}
	var out []*projectCFValue
	for _, v := range values {
		cf := byID[v.Field.ID]
		if cf == nil {
			continue
		}
		var ok bool
		if p.ID == 0 {
			// 新規プロジェクトはロールが無い（visible? または管理者のみ）
			ok = cf.Visible || c.User.IsAdmin()
		} else {
			ok, err = customfield.VisibleBy(ctx, c.Authz(), cf, p)
			if err != nil {
				return nil, err
			}
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// projectVisibleCustomFieldValues は project.visible_custom_field_values。
func (a *App) projectVisibleCustomFieldValues(c *Req, p *domain.Project) ([]*projectCFValue, error) {
	all, err := a.projectCustomFieldValues(c, p)
	if err != nil {
		return nil, err
	}
	return a.visibleCustomFieldValues(c, p, all)
}

// assignProjectCFValues は custom_field_values=（params[:project][:custom_field_values]）。
// editable（表示可能）な値だけを更新する。
func assignProjectCFValues(values []*projectCFValue, p *httpx.Params) {
	if p == nil {
		return
	}
	for _, v := range values {
		key := strconv.FormatInt(v.Field.ID, 10)
		if !p.Has(key) {
			continue
		}
		if v.Field.Multiple {
			var vals []string
			for _, s := range p.Strings(key) {
				if s != "" {
					vals = append(vals, s)
				}
			}
			v.Values = vals
		} else {
			s := p.String(key)
			if s == "" {
				v.Values = nil
			} else {
				v.Values = []string{s}
			}
		}
	}
}

var (
	cfIntRe   = regexp.MustCompile(`^[+-]?\d+$`)
	cfFloatRe = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)
	cfDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// validateProjectCFValues は CustomFieldValue#validate_value（必須・書式・選択肢・長さ・正規表現）。
// エラーは custom_field の名前を属性名として errs に追加する（Redmine の errors.add(:base, ...) と同じ表示）。
func validateProjectCFValues(errs *domain.ValidationErrors, values []*projectCFValue, l func(string, ...any) string) {
	for _, v := range values {
		cf := v.Field
		nonBlank := []string{}
		for _, s := range v.Values {
			if strings.TrimSpace(s) != "" {
				nonBlank = append(nonBlank, s)
			}
		}
		name := cf.Name
		add := func(key string, vars map[string]any) {
			msg := l("activerecord.errors.messages."+key, vars)
			errs.AddMessage("base", name+" "+msg)
		}
		if cf.IsRequired && len(nonBlank) == 0 {
			add("blank", nil)
			continue
		}
		for _, s := range nonBlank {
			switch cf.FieldFormat {
			case "int":
				if !cfIntRe.MatchString(strings.TrimSpace(s)) {
					add("not_a_number", nil)
				}
			case "float":
				if !cfFloatRe.MatchString(strings.TrimSpace(s)) {
					add("invalid", nil)
				}
			case "date":
				if !cfDateRe.MatchString(s) {
					add("not_a_date", nil)
				} else if _, err := time.Parse("2006-01-02", s); err != nil {
					add("not_a_date", nil)
				}
			case "list":
				if !slices.Contains(cf.PossibleValues, s) {
					add("inclusion", nil)
				}
			case "bool":
				if s != "0" && s != "1" {
					add("inclusion", nil)
				}
			}
		}
	}
}

// saveCustomFieldValues は保存（custom_values の置き換え）。
func saveCustomFieldValues(ctx context.Context, q db.Queryer, projectID int64, values []*projectCFValue) error {
	for _, v := range values {
		vals := v.Values
		if len(vals) == 0 {
			vals = []string{""}
		}
		if err := repository.SetCustomValues(ctx, q, "project", projectID, v.Field.ID, vals); err != nil {
			return err
		}
	}
	return nil
}

// showCFValue は show_value(custom_value)（HTML 表示用の書式化）。
func showCFValue(c *Req, v *projectCFValue) template.HTML {
	var parts []string
	for _, s := range v.Values {
		if s == "" {
			continue
		}
		parts = append(parts, string(formatCFSingle(c, v.Field, s, true)))
	}
	return template.HTML(strings.Join(parts, ", "))
}

// csvCFValue は CSV 用の書式化（format_object(value, html: false)）。
func csvCFValue(c *Req, v *projectCFValue) string {
	var parts []string
	for _, s := range v.Values {
		if s == "" {
			continue
		}
		parts = append(parts, string(formatCFSingle(c, v.Field, s, false)))
	}
	return strings.Join(parts, ", ")
}

func formatCFSingle(c *Req, cf *domain.CustomFieldInfo, s string, html bool) template.HTML {
	esc := func(x string) template.HTML {
		if html {
			return rails.H(x)
		}
		return template.HTML(x)
	}
	switch cf.FieldFormat {
	case "bool":
		switch s {
		case "1":
			return esc(c.L("general_text_Yes"))
		case "0":
			return esc(c.L("general_text_No"))
		}
	case "date":
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return esc(c.Loc.FormatDate(t))
		}
	case "link":
		if html {
			// javascript: などのスキームを href に出さない（Redmine は sanitize_html で除く）
			return customfield.LinkValueHTML(s)
		}
	case "text":
		if html {
			return template.HTML(strings.ReplaceAll(string(rails.H(s)), "\n", "<br />"))
		}
	}
	return esc(s)
}
