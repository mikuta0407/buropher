// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/rubyyaml"
)

// ---------------------------------------------------------------- queries

var queryKinds = map[string]string{
	"IssueQuery":        "issue",
	"ProjectQuery":      "project",
	"ProjectAdminQuery": "project_admin",
	"TimeEntryQuery":    "time_entry",
	"UserQuery":         "user",
}

func (im *imp) importQueries() error {
	t := im.table("queries")
	ins := im.ins(t, "queries", "id", "kind", "project_id", "user_id", "name", "description", "visibility", "filters",
		"column_names", "sort_criteria", "group_by", "totalable_names", "display_type", "options")
	err := im.src.each("queries", func(r rec) error {
		id := r.id()
		kind, ok := queryKinds[r.str("type")]
		if !ok {
			t.drop(id, "unknown query type %q", r.str("type"))
			return nil
		}
		var project any
		if p := r.ref("project_id"); p != 0 {
			if !im.projectExists(p) {
				t.drop(id, "project_id refers to a missing project")
				return nil
			}
			project = p
		}
		var user any
		if u := r.intOr("user_id", 0); u != 0 {
			if im.principal(u) {
				user = u
			} else {
				t.repair(id, "user_id refers to a missing user; set to anonymous user")
				user = im.st.anonymousID
			}
		}
		vis := r.intOr("visibility", 0)
		if vis < 0 || vis > 2 {
			t.repair(id, "invalid visibility %d; set to 0 (private)", vis)
			vis = 0
		}
		yaml := func(col string) (any, bool) {
			v, err := decodeYAML(r.Row[col])
			if err != nil {
				t.repair(id, "unparseable %s YAML; ignored", col)
				return nil, false
			}
			return v, true
		}
		filters := "{}"
		if v, ok := yaml("filters"); ok && v != nil {
			if m, ok := asMap(v); ok {
				filters = toJSON(m)
			} else {
				t.repair(id, "filters is not a Hash; ignored")
			}
		}
		var columns, sort any
		if v, ok := yaml("column_names"); ok && v != nil {
			columns = toJSON(stringList(v))
		}
		if v, ok := yaml("sort_criteria"); ok && v != nil {
			if arr, ok := v.([]any); ok {
				out := make([]any, 0, len(arr))
				for _, e := range arr {
					if pair, ok := e.([]any); ok {
						out = append(out, stringList(pair))
					} else {
						s, _ := toStr(rubyyaml.Plain(e))
						out = append(out, []string{s})
					}
				}
				sort = toJSON(out)
			}
		}
		var totalable, display any
		options := rubyyaml.OrderedMap{}
		if v, ok := yaml("options"); ok && v != nil {
			m, _ := asMap(v)
			for _, it := range m {
				switch it.Key {
				case "totalable_names":
					totalable = toJSON(stringList(it.Value))
				case "display_type":
					if s, _ := toStr(rubyyaml.Plain(it.Value)); s != "" {
						display = s
					}
				default:
					options = append(options, it)
				}
			}
		}
		im.st.queries[id] = kind
		return ins.add(id, kind, project, user, r.str("name"), r.strNull("description", false), vis, filters, columns, sort,
			r.strNull("group_by", false), totalable, display, toJSON(options))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importQueriesRoles() error {
	return im.importPairs("queries_roles", "queries_roles", "query_id", "role_id",
		func(id int64) bool { _, ok := im.st.queries[id]; return ok },
		func(id int64) bool { _, ok := im.st.roles[id]; return ok })
}

// ---------------------------------------------------------------- custom_values

var customizedKinds = map[string]string{
	"Issue": "issue", "Project": "project", "TimeEntry": "time_entry", "Version": "version", "Document": "document",
	"Principal": "principal", "User": "principal", "Group": "principal", "AnonymousUser": "principal",
	"Enumeration": "enumeration", "IssuePriority": "enumeration", "TimeEntryActivity": "enumeration", "DocumentCategory": "enumeration",
}

// customizedExists は custom_values の参照先が存在し、カスタムフィールドの所有種別と整合するかを返す。
func (im *imp) customizedExists(kind, owner string, id int64) (ok bool, reason string) {
	compat := map[string][]string{
		"issue": {"issue"}, "project": {"project"}, "time_entry": {"time_entry"}, "version": {"version"},
		"document": {"document"}, "principal": {"user", "group"},
		"enumeration": {"issue_priority", "time_entry_activity", "document_category"},
	}
	match := false
	for _, o := range compat[kind] {
		match = match || o == owner
	}
	if !match {
		return false, fmt.Sprintf("customized_type does not match the custom field (%s value on a %s field)", kind, owner)
	}
	exists := false
	switch owner {
	case "issue":
		_, exists = im.st.issues[id]
	case "project":
		exists = im.projectExists(id)
	case "time_entry":
		exists = im.st.timeEntries.has(id)
	case "version":
		_, exists = im.st.versions[id]
	case "document":
		exists = im.st.documents.has(id)
	case "user":
		k := im.st.principals[id]
		exists = k == "user" || k == "anonymous_user"
	case "group":
		k := im.st.principals[id]
		exists = k == "group" || k == "group_anonymous" || k == "group_non_member"
	case "issue_priority":
		exists = im.st.priorities.has(id)
	case "time_entry_activity":
		_, exists = im.st.activities[id]
	case "document_category":
		exists = im.st.docCategories.has(id)
	}
	if !exists {
		return false, "customized object is missing"
	}
	return true, ""
}

// normalizeCFValue は §4.13 の正規化(空文字列はそのまま保持、数値の前後空白)。
// bool の表記揺れ('t' / 'true' 等)は正規化しない: Redmine は保存値のまま扱い、
// bool 形式は '1' / '0' とだけ比較する('t' は未選択・空表示になる)。
func normalizeCFValue(format string, v any) (any, bool) {
	s, ok := toStr(v)
	if !ok {
		return nil, false
	}
	switch format {
	case "int", "float", "progressbar":
		if ts := strings.TrimSpace(s); ts != s {
			return ts, true
		}
	}
	return s, false
}

func (im *imp) importCustomValues() error {
	t := im.table("custom_values")
	ins := im.ins(t, "custom_values", "id", "customized_kind", "customized_id", "custom_field_id", "value")
	err := im.src.each("custom_values", func(r rec) error {
		id := r.id()
		kind, ok := customizedKinds[r.str("customized_type")]
		if !ok {
			t.drop(id, "unknown customized_type %q", r.str("customized_type"))
			return nil
		}
		cf := im.st.customFields[r.ref("custom_field_id")]
		if cf == nil {
			t.drop(id, "custom_field_id refers to a missing custom field")
			return nil
		}
		cid := r.ref("customized_id")
		if ok, reason := im.customizedExists(kind, cf.ownerKind, cid); !ok {
			t.drop(id, "%s", reason)
			return nil
		}
		val, changed := normalizeCFValue(cf.format, r.Row["value"])
		if changed {
			t.repair(id, "%s value normalized", cf.format)
		}
		im.st.customValues.add(id)
		return ins.add(id, kind, cid, r.ref("custom_field_id"), val)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- attachments

var containerKinds = map[string]string{
	"Issue": "issue", "Project": "project", "Version": "version", "WikiPage": "wiki_page", "Message": "message",
	"News": "news", "Document": "document", "CustomValue": "custom_value",
}

func (im *imp) containerExists(kind string, id int64) bool {
	switch kind {
	case "issue":
		_, ok := im.st.issues[id]
		return ok
	case "project":
		return im.projectExists(id)
	case "version":
		_, ok := im.st.versions[id]
		return ok
	case "wiki_page":
		return im.st.wikiPages[id] != 0
	case "message":
		return im.st.messages.has(id)
	case "news":
		return im.st.news.has(id)
	case "document":
		return im.st.documents.has(id)
	case "custom_value":
		return im.st.customValues.has(id)
	}
	return false
}

func (im *imp) importAttachments() error {
	t := im.table("attachments")
	ins := im.ins(t, "attachments", "id", "container_kind", "container_id", "filename", "disk_directory", "disk_filename", "filesize",
		"content_type", "digest", "digest_algo", "downloads", "author_id", "description", "created_at")
	err := im.src.each("attachments", func(r rec) error {
		id := r.id()
		var kind, cid any
		typ := r.str("container_type")
		c := r.ref("container_id")
		switch {
		case typ == "" && c == 0:
			// 未紐付けの一時添付
		case typ == "" || c == 0:
			t.repair(id, "container_type/container_id half empty; imported as unattached")
		default:
			k, ok := containerKinds[typ]
			if !ok {
				t.drop(id, "unknown container_type %q", typ)
				return nil
			}
			if !im.containerExists(k, c) {
				t.drop(id, "container (%s) is missing", typ)
				return nil
			}
			kind, cid = k, c
		}
		diskFile := r.str("disk_filename")
		if diskFile == "" {
			t.drop(id, "empty disk_filename")
			return nil
		}
		dir := r.strNull("disk_directory", false)
		var digest, algo any
		if d := strings.ToLower(strings.TrimSpace(r.str("digest"))); d != "" {
			switch {
			case !isHex(d):
				// digest はサムネイルのファイル名に使われる（"../" などを含むとサムネイルの保存先の外に書き込める）
				t.repair(id, "digest is not hexadecimal; set NULL")
			case len(d) == 64:
				digest, algo = d, "sha256"
			case len(d) == 32:
				digest, algo = d, "md5"
			default:
				t.repair(id, "digest of unknown length; set NULL")
			}
		}
		rel := diskFile
		if dir != nil && dir != "" {
			rel = dir.(string) + "/" + diskFile
		}
		if c, err := cleanRelPath(path.Clean(rel)); err == nil {
			if _, dup := im.st.attachmentPaths[c]; !dup {
				im.st.attachmentPaths[c] = id
			}
		} else {
			// 保存先の外を指すパスを行に残すと、ダウンロード・削除で任意のファイルに触れてしまう
			t.drop(id, "unsafe disk path %q", rel)
			return nil
		}
		author := im.authorOr(t, id, "author_id", r.ref("author_id"))
		im.st.attachments.add(id)
		return ins.add(id, kind, cid, r.str("filename"), dir, diskFile, r.intOr("filesize", 0), r.strNull("content_type", false),
			digest, algo, r.intOr("downloads", 0), author, r.strNull("description", false), im.tsOr(t, id, r, "created_on"))
	})
	if err != nil {
		return err
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	return im.checkFiles()
}

// ---------------------------------------------------------------- watchers / reactions

var watchableKinds = map[string]string{
	"Issue": "issue", "News": "news", "Board": "board", "Message": "message", "Wiki": "wiki", "WikiPage": "wiki_page",
	"EnabledModule": "project_module",
}

func (im *imp) watchableExists(kind string, id int64) bool {
	switch kind {
	case "issue":
		_, ok := im.st.issues[id]
		return ok
	case "news":
		return im.st.news.has(id)
	case "board":
		return im.st.boards[id] != 0
	case "message":
		return im.st.messages.has(id)
	case "wiki":
		return im.st.wikis[id] != 0
	case "wiki_page":
		return im.st.wikiPages[id] != 0
	case "project_module":
		return im.st.modules.has(id)
	}
	return false
}

func (im *imp) importWatchers() error {
	t := im.table("watchers")
	ins := im.ins(t, "watchers", "id", "watchable_kind", "watchable_id", "principal_id")
	seen := map[string]bool{}
	err := im.src.each("watchers", func(r rec) error {
		id := r.id()
		kind, ok := watchableKinds[r.str("watchable_type")]
		if !ok {
			t.drop(id, "unknown watchable_type %q", r.str("watchable_type"))
			return nil
		}
		wid := r.ref("watchable_id")
		if !im.watchableExists(kind, wid) {
			t.drop(id, "watched object is missing")
			return nil
		}
		u := r.ref("user_id")
		if !im.principal(u) {
			t.drop(id, "user_id NULL or missing")
			return nil
		}
		k := fmt.Sprint(kind, wid, u)
		if seen[k] {
			t.drop(id, "duplicate")
			return nil
		}
		seen[k] = true
		return ins.add(id, kind, wid, u)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

var reactableKinds = map[string]string{"Issue": "issue", "Journal": "journal", "News": "news", "Message": "message", "Comment": "comment"}

func (im *imp) importReactions() error {
	t := im.table("reactions")
	ins := im.ins(t, "reactions", "id", "reactable_kind", "reactable_id", "user_id", "created_at", "updated_at")
	seen := map[string]bool{}
	err := im.src.each("reactions", func(r rec) error {
		id := r.id()
		kind, ok := reactableKinds[r.str("reactable_type")]
		if !ok {
			t.drop(id, "unknown reactable_type %q", r.str("reactable_type"))
			return nil
		}
		rid := r.ref("reactable_id")
		exists := false
		switch kind {
		case "issue":
			_, exists = im.st.issues[rid]
		case "journal":
			exists = im.st.journals.has(rid)
		case "news":
			exists = im.st.news.has(rid)
		case "message":
			exists = im.st.messages.has(rid)
		case "comment":
			exists = im.st.comments.has(rid)
		}
		if !exists {
			t.drop(id, "reacted object is missing")
			return nil
		}
		u := r.ref("user_id")
		if !im.st.users.has(u) {
			t.drop(id, "user_id refers to a missing user")
			return nil
		}
		k := fmt.Sprint(kind, rid, u)
		if seen[k] {
			t.drop(id, "duplicate")
			return nil
		}
		seen[k] = true
		return ins.add(id, kind, rid, u, im.tsOr(t, id, r, "created_at", "updated_at"), im.tsOr(t, id, r, "updated_at", "created_at"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- oauth

func (im *imp) importOAuth() error {
	t := im.table("oauth_applications")
	apps := newIDSet()
	ins := im.ins(t, "oauth_applications", "id", "name", "uid", "secret", "redirect_uri", "scopes", "confidential", "created_at", "updated_at")
	uids := map[string]bool{}
	err := im.src.each("oauth_applications", func(r rec) error {
		id := r.id()
		uid := r.str("uid")
		if uid == "" || uids[uid] {
			t.drop(id, "empty or duplicate uid")
			return nil
		}
		uids[uid] = true
		apps.add(id)
		return ins.add(id, r.str("name"), uid, r.str("secret"), r.str("redirect_uri"), r.str("scopes"), r.bool("confidential", true),
			im.tsOr(t, id, r, "created_at", "updated_at"), im.tsOr(t, id, r, "updated_at", "created_at"))
	})
	if err != nil {
		return err
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	parse := func(s string) time.Time { tm, _ := time.Parse(db.TimeLayout, s); return tm }
	revoked := func(t *TableReport, id int64, r rec) bool {
		v := im.tsNull(t, id, r, "revoked_at")
		return v != nil && !parse(v.(string)).After(im.now)
	}

	gt := im.table("oauth_access_grants")
	gins := im.ins(gt, "oauth_access_grants", "id", "resource_owner_id", "application_id", "token", "expires_in", "redirect_uri",
		"created_at", "revoked_at", "scopes", "code_challenge", "code_challenge_method")
	tokens := map[string]bool{}
	err = im.src.each("oauth_access_grants", func(r rec) error {
		id := r.id()
		if !apps.has(r.ref("application_id")) || !im.st.users.has(r.ref("resource_owner_id")) {
			gt.drop(id, "application or resource owner is missing")
			return nil
		}
		created := im.tsOr(gt, id, r, "created_at")
		exp := r.intOr("expires_in", 0)
		if revoked(gt, id, r) || parse(created).Add(time.Duration(exp)*time.Second).Before(im.now) {
			gt.drop(id, "expired or revoked grant")
			return nil
		}
		tok := r.str("token")
		if tokens[tok] {
			gt.drop(id, "duplicate token")
			return nil
		}
		tokens[tok] = true
		return gins.add(id, r.ref("resource_owner_id"), r.ref("application_id"), tok, exp, r.str("redirect_uri"), created,
			im.tsNull(gt, id, r, "revoked_at"), r.strNull("scopes", false), r.strNull("code_challenge", true), r.strNull("code_challenge_method", true))
	})
	if err != nil {
		return err
	}
	if _, err := gins.close(); err != nil {
		return err
	}

	at := im.table("oauth_access_tokens")
	ains := im.ins(at, "oauth_access_tokens", "id", "resource_owner_id", "application_id", "token", "refresh_token", "expires_in",
		"revoked_at", "created_at", "scopes", "previous_refresh_token")
	atok, rtok := map[string]bool{}, map[string]bool{}
	err = im.src.each("oauth_access_tokens", func(r rec) error {
		id := r.id()
		app, owner := r.ref("application_id"), r.ref("resource_owner_id")
		if (app != 0 && !apps.has(app)) || (owner != 0 && !im.st.users.has(owner)) {
			at.drop(id, "application or resource owner is missing")
			return nil
		}
		created := im.tsOr(at, id, r, "created_at")
		refresh := r.strNull("refresh_token", true)
		var exp any
		if e, ok := r.int("expires_in"); ok {
			exp = e
			if refresh == nil && parse(created).Add(time.Duration(e)*time.Second).Before(im.now) {
				at.drop(id, "expired access token without refresh token")
				return nil
			}
		}
		if revoked(at, id, r) {
			at.drop(id, "revoked access token")
			return nil
		}
		tok := r.str("token")
		if atok[tok] || (refresh != nil && rtok[refresh.(string)]) {
			at.drop(id, "duplicate token")
			return nil
		}
		atok[tok] = true
		if refresh != nil {
			rtok[refresh.(string)] = true
		}
		return ains.add(id, nullIfZero(owner), nullIfZero(app), tok, refresh, exp, im.tsNull(at, id, r, "revoked_at"), created,
			r.strNull("scopes", false), r.str("previous_refresh_token"))
	})
	if err != nil {
		return err
	}
	_, err = ains.close()
	return err
}

// isHex は s が 16 進数字（小文字）だけからなるか。
func isHex(s string) bool {
	for _, r := range s {
		if !('0' <= r && r <= '9' || 'a' <= r && r <= 'f') {
			return false
		}
	}
	return true
}
