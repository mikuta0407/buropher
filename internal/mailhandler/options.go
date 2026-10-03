// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"regexp"
	"slices"
	"strings"
)

// Options は MailHandler.receive の options（rake の環境変数・/mail_handler のパラメータ）。
// 真偽値のオプションは Redmine と同じく文字列で受け取り、"1" のときだけ有効とする。
type Options struct {
	// Issue は options[:issue]（project / status / tracker / category / priority / assigned_to /
	// fixed_version / is_private の既定値）。キーが存在するかどうかにも意味がある（project）。
	Issue map[string]string
	// AllowOverride は allow_override（カンマ区切りの文字列、または一覧）。
	AllowOverride []string
	// UnknownUser は unknown_user（ignore / accept / create）。
	UnknownUser string
	// NoPermissionCheck は no_permission_check。
	NoPermissionCheck string
	// NoAccountNotice は no_account_notice。
	NoAccountNotice string
	// NoNotification は no_notification。
	NoNotification string
	// DefaultGroup は default_group（カンマ区切りのグループ名）。
	DefaultGroup string
	// ProjectFromSubaddress は project_from_subaddress（user@domain）。
	ProjectFromSubaddress string
}

// issueOptionKeys は extract_options_from_env が受け付けるチケット属性の既定値。
var issueOptionKeys = []string{"project", "status", "tracker", "category", "priority", "assigned_to", "fixed_version"}

// ExtractOptionsFromEnv は MailHandler.extract_options_from_env（rake の環境変数からオプションを作る）。
// get は環境変数の取得（値と存在）。
func ExtractOptionsFromEnv(get func(string) (string, bool)) Options {
	o := Options{Issue: map[string]string{}}
	for _, k := range issueOptionKeys {
		if v, ok := get(k); ok {
			o.Issue[k] = v
		}
	}
	if v, ok := get("allow_override"); ok {
		o.AllowOverride = []string{v}
	}
	str := func(k string) string { v, _ := get(k); return v }
	o.UnknownUser = str("unknown_user")
	o.NoPermissionCheck = str("no_permission_check")
	o.NoAccountNotice = str("no_account_notice")
	o.NoNotification = str("no_notification")
	o.DefaultGroup = str("default_group")
	o.ProjectFromSubaddress = str("project_from_subaddress")
	if _, ok := get("private"); ok {
		o.Issue["is_private"] = "1"
	}
	return o
}

// handlerOptions は MailHandler.receive で正規化したオプション（handler_options）。
type handlerOptions struct {
	issue                 map[string]string
	allowOverride         []string
	unknownUser           string
	noAccountNotice       bool
	noNotification        bool
	noPermissionCheck     bool
	defaultGroup          string
	projectFromSubaddress string
}

var spacesRe = regexp.MustCompile(`\s+`)

// normalize は MailHandler.receive のオプションの正規化。
func (o Options) normalize() *handlerOptions {
	h := &handlerOptions{issue: map[string]string{}}
	for k, v := range o.Issue {
		h.issue[k] = v
	}
	// allow_override が文字列ならカンマで分ける
	var list []string
	for _, s := range o.AllowOverride {
		list = append(list, strings.Split(s, ",")...)
	}
	for _, s := range list {
		h.allowOverride = append(h.allowOverride, spacesRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "_"))
	}
	// 既定のプロジェクトが無ければプロジェクトは常に上書き可能
	if _, ok := o.Issue["project"]; !ok {
		h.allowOverride = append(h.allowOverride, "project")
	}
	h.noAccountNotice = o.NoAccountNotice == "1"
	h.noNotification = o.NoNotification == "1"
	h.noPermissionCheck = o.NoPermissionCheck == "1"
	h.unknownUser = o.UnknownUser
	h.defaultGroup = o.DefaultGroup
	h.projectFromSubaddress = o.ProjectFromSubaddress
	return h
}

func (h *handlerOptions) overridable(attr string) bool {
	key := spacesRe.ReplaceAllString(strings.ToLower(attr), "_")
	return slices.Contains(h.allowOverride, key) || slices.Contains(h.allowOverride, "all")
}
