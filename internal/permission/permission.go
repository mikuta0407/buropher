// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package permission は Redmine::AccessControl（lib/redmine/access_control.rb）の移植。
//
// 権限定義は lib/redmine/preparation.rb を Redmine 実行時にダンプした permissions.json
// （tools/gen/dump-permissions.rb で生成）をそのまま埋め込んで使う。定義順も Redmine と同じ。
package permission

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

//go:embed permissions.json
var permissionsJSON []byte

// Permission は 1 つの権限定義（Redmine::AccessControl::Permission）。
type Permission struct {
	Name string `json:"name"`
	// Module はプロジェクトモジュール名。nil 相当は空文字（モジュールに属さない権限）。
	Module  string   `json:"module"`
	Actions []string `json:"actions"` // "controller/action" 形式
	Public  bool     `json:"public"`
	// Require は "", "member", "loggedin" のいずれか。
	Require string `json:"require"`
	Read    bool   `json:"read"`
}

// RequireMember は Permission#require_member? に相当する。
func (p *Permission) RequireMember() bool { return p.Require == "member" }

// RequireLoggedin は Permission#require_loggedin? に相当する（member も含む）。
func (p *Permission) RequireLoggedin() bool { return p.Require == "member" || p.Require == "loggedin" }

// Allows は権限が "controller/action" を許可するかを返す。
func (p *Permission) Allows(controller, action string) bool {
	return slices.Contains(p.Actions, controller+"/"+action)
}

var (
	all    []*Permission
	byName map[string]*Permission
)

func init() {
	var ps []*Permission
	if err := json.Unmarshal(permissionsJSON, &ps); err != nil {
		panic(fmt.Sprintf("permission: invalid permissions.json: %v", err))
	}
	all = ps
	byName = make(map[string]*Permission, len(ps))
	for _, p := range ps {
		byName[p.Name] = p
	}
}

// All は全権限を定義順で返す。
func All() []*Permission { return all }

// Get は名前で権限を返す（AccessControl.permission）。見つからなければ nil。
func Get(name string) *Permission { return byName[name] }

// AllowedActions は権限が許可するアクション一覧を返す（AccessControl.allowed_actions）。
func AllowedActions(name string) []string {
	if p := byName[name]; p != nil {
		return p.Actions
	}
	return nil
}

func filter(f func(*Permission) bool) []*Permission {
	var out []*Permission
	for _, p := range all {
		if f(p) {
			out = append(out, p)
		}
	}
	return out
}

// PublicPermissions は AccessControl.public_permissions。
func PublicPermissions() []*Permission { return filter(func(p *Permission) bool { return p.Public }) }

// MembersOnlyPermissions は AccessControl.members_only_permissions。
func MembersOnlyPermissions() []*Permission {
	return filter(func(p *Permission) bool { return p.RequireMember() })
}

// LoggedinOnlyPermissions は AccessControl.loggedin_only_permissions。
func LoggedinOnlyPermissions() []*Permission {
	return filter(func(p *Permission) bool { return p.RequireLoggedin() })
}

// IsReadPermission は AccessControl.read_action?(Symbol) に相当する。
func IsReadPermission(name string) bool {
	p := byName[name]
	return p != nil && p.Read
}

// IsReadAction は AccessControl.read_action?(Hash) に相当する。
func IsReadAction(controller, action string) bool {
	for _, p := range all {
		if p.Read && p.Allows(controller, action) {
			return true
		}
	}
	return false
}

// AvailableProjectModules は定義順のモジュール名一覧（AccessControl.available_project_modules）。
func AvailableProjectModules() []string {
	var mods []string
	for _, p := range all {
		if p.Module != "" && !slices.Contains(mods, p.Module) {
			mods = append(mods, p.Module)
		}
	}
	return mods
}

// ModulesPermissions はモジュールに属さない権限と、有効モジュールの権限を返す
// （AccessControl.modules_permissions）。
func ModulesPermissions(modules []string) []*Permission {
	return filter(func(p *Permission) bool { return p.Module == "" || slices.Contains(modules, p.Module) })
}

// PermissionsForAction は "controller/action" を許可する権限を全て返す。
func PermissionsForAction(controller, action string) []*Permission {
	return filter(func(p *Permission) bool { return p.Allows(controller, action) })
}
