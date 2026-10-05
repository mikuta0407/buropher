// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"html/template"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// principalOption は principals_options_for_select の 1 要素（ユーザーまたはグループ）。
type principalOption struct {
	ID       int64
	Name     string
	Group    bool
	Disabled bool
}

// principalsOptionTags は ApplicationHelper#principals_options_for_select の本体（Redmine 7.0）。
//
// collection は collection.sort 済み（ユーザーが先、名前順）であること。me は collection.include?(User.current)、
// involved は「関係者」optgroup（既存チケットの編集時のみ）。selected は選択値の id 文字列。
// 担当者ドロップダウンの並び（Setting.assignee_dropdown_display_format）に従って optgroup を組み立てる
// （#43996 / #44015）。"users_by_group" のときだけグループの所属ユーザーを DB から読む。
func (a *App) principalsOptionTags(c *Req, meID int64, collection, involved []principalOption, selected string) template.HTML {
	var s strings.Builder
	if meID != 0 {
		s.WriteString(string(rails.ContentTag("option", "<< "+c.L("label_me")+" >>", rails.NewHash("value", meID))))
	}
	var users, groups []principalOption
	for _, p := range collection {
		if p.Group {
			groups = append(groups, p)
		} else {
			users = append(users, p)
		}
	}
	if len(involved) == 0 && len(groups) == 0 {
		s.WriteString(principalOptionsHTML(users, selected))
		return template.HTML(s.String())
	}
	type optgroup struct {
		label string
		items []principalOption
	}
	optgroups := []optgroup{{c.L("label_involved_principals"), involved}}
	switch a.Settings.String("assignee_dropdown_display_format") {
	case "groups_then_users":
		optgroups = append(optgroups, optgroup{c.L("label_group_plural"), groups}, optgroup{c.L("label_user_plural"), users})
	case "users_by_group":
		// グループ → グループごとの所属ユーザー → どのグループにも属さないユーザー
		optgroups = append(optgroups, optgroup{c.L("label_group_plural"), groups})
		grouped := map[int64]bool{}
		for _, g := range groups {
			ids, err := repository.GroupUserIDs(c.Ctx(), a.DB, g.ID)
			if err != nil {
				a.logger().Error("group user ids", "err", err)
			}
			var members []principalOption
			for _, u := range users {
				if slices.Contains(ids, u.ID) {
					members = append(members, u)
					grouped[u.ID] = true
				}
			}
			if len(members) > 0 {
				optgroups = append(optgroups, optgroup{g.Name, members})
			}
		}
		var ungrouped []principalOption
		for _, u := range users {
			if !grouped[u.ID] {
				ungrouped = append(ungrouped, u)
			}
		}
		optgroups = append(optgroups, optgroup{c.L("label_user_plural"), ungrouped})
	default: // users_then_groups
		optgroups = append(optgroups, optgroup{c.L("label_user_plural"), users}, optgroup{c.L("label_group_plural"), groups})
	}
	for _, g := range optgroups {
		if h := principalOptionsHTML(g.items, selected); h != "" {
			s.WriteString(`<optgroup label="` + string(rails.H(g.label)) + `">` + h + `</optgroup>`)
		}
	}
	return template.HTML(s.String())
}

// principalOptionsHTML は principals_option_tags（selected / disabled 属性付きの option 群）。
func principalOptionsHTML(ps []principalOption, selected string) string {
	var b strings.Builder
	for _, p := range ps {
		id := strconv.FormatInt(p.ID, 10)
		b.WriteString(`<option value="` + id + `"`)
		if id == selected {
			b.WriteString(` selected="selected"`)
		}
		if p.Disabled {
			b.WriteString(` disabled="disabled"`)
		}
		b.WriteString(`>` + string(rails.H(p.Name)) + `</option>`)
	}
	return b.String()
}
