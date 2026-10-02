package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/redmineimport/rediscipher"
	"github.com/mikuta0407/buropher/internal/redmineimport/rubyyaml"
	"github.com/mikuta0407/buropher/internal/settings"
)

// ---------------------------------------------------------------- 秘密値

// secret は暗号化列の値を buropher 形式に変換する。
// 戻り値 lost は値を復元できず捨てたことを示す(呼び出し側で 2FA リセット等を行う)。
func (im *imp) secret(t *TableReport, id any, col string, v any) (out any, lost bool, err error) {
	s, ok := toStr(v)
	if !ok || strings.TrimSpace(s) == "" {
		return nil, false, nil
	}
	plain := s
	encrypted := rediscipher.IsEncrypted(s)
	if encrypted {
		if im.opt.CipherKey == "" {
			t.repair(id, "%s is encrypted but no --cipher-key was given; value discarded", col)
			return nil, true, nil
		}
		p, derr := rediscipher.Decrypt(im.opt.CipherKey, s)
		if derr != nil {
			t.repair(id, "%s cannot be decrypted with the given cipher key (%v); value discarded", col, derr)
			return nil, true, nil
		}
		plain = p
	}
	if im.box == nil {
		if encrypted {
			return nil, false, fmt.Errorf("%s: decrypted secret values need a buropher secret key (NewCipherKey / --secret-key) for re-encryption", col)
		}
		t.repair(id, "%s stored as plain text (no buropher secret key given)", col)
		return plain, false, nil
	}
	sealed, err := im.box.Seal(plain)
	if err != nil {
		return nil, false, err
	}
	return sealed, false, nil
}

// ---------------------------------------------------------------- settings

func (im *imp) importSettings() error {
	t := im.table("settings")
	rows, err := im.src.all("settings")
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id() < rows[j].id() })
	ins := im.ins(t, "settings", "name", "value", "updated_at")
	leg := im.ins(t, "legacy_settings", "name", "raw_value", "updated_at")
	seen := map[string]bool{}
	for _, r := range rows {
		id := r.id()
		name := r.str("name")
		raw, hasRaw := toStr(r.Row["value"])
		upd := im.tsNull(t, id, r, "updated_on")
		if seen[name] {
			t.drop(id, "duplicate setting name (first row wins)")
			continue
		}
		seen[name] = true
		def := settings.Lookup(name)
		if def == nil {
			if err := leg.add(name, r.strNull("value", false), upd); err != nil {
				return err
			}
			t.repair(id, "unknown/obsolete setting moved to legacy_settings")
			continue
		}
		var js string
		if def.Serialized {
			v, derr := decodeYAML(raw)
			if derr != nil {
				t.drop(id, "unparseable YAML value (moved to legacy_settings)")
				if err := leg.add(name, r.strNull("value", false), upd); err != nil {
					return err
				}
				continue
			}
			js = toJSON(v)
		} else {
			if !hasRaw {
				raw = ""
			}
			js = toJSON(raw)
		}
		im.st.settings[name] = raw
		if upd == nil {
			upd = im.nowStr
		}
		if err := ins.add(name, js, upd); err != nil {
			return err
		}
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	_, err = leg.close()
	return err
}

// setting はソースの設定値(なければ settings.yml の既定値)を文字列で返す。
func (im *imp) setting(name string) string {
	if v, ok := im.st.settings[name]; ok {
		return v
	}
	if d := settings.Lookup(name); d != nil {
		if s, ok := d.Default.(string); ok {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- auth_sources

func (im *imp) importAuthSources() error {
	t := im.table("auth_sources")
	ins := im.ins(t, "auth_sources", "id", "kind", "name", "enabled", "position", "onthefly_register", "config", "secret", "created_at", "updated_at")
	names := map[string]bool{}
	pos := int64(0)
	err := im.src.each("auth_sources", func(r rec) error {
		id := r.id()
		if typ := r.str("type"); typ != "AuthSourceLdap" {
			t.drop(id, "unsupported auth source type %q", typ)
			return nil
		}
		name := strings.TrimSpace(r.str("name"))
		if name == "" {
			name = fmt.Sprintf("LDAP %d", id)
			t.repair(id, "empty name; generated")
		}
		if names[name] {
			name = fmt.Sprintf("%s (%d)", name, id)
			t.repair(id, "duplicate name; renamed")
		}
		names[name] = true
		cfg := rubyyaml.OrderedMap{}
		put := func(k string, v any) {
			if v != nil {
				cfg = append(cfg, rubyyaml.MapItem{Key: k, Value: v})
			}
		}
		// 空文字列も保持する（Redmine のフォームは nil と "" で value 属性の有無が異なる）
		put("host", r.strNull("host", false))
		if p, ok := r.int("port"); ok {
			put("port", p)
		}
		put("account", r.strNull("account", false))
		put("base_dn", r.strNull("base_dn", false))
		put("filter", r.strNull("filter", false))
		if to, ok := r.int("timeout"); ok {
			put("timeout", to)
		}
		put("tls", r.bool("tls", false))
		put("verify_peer", r.bool("verify_peer", true))
		put("attr_login", r.strNull("attr_login", false))
		put("attr_firstname", r.strNull("attr_firstname", false))
		put("attr_lastname", r.strNull("attr_lastname", false))
		put("attr_mail", r.strNull("attr_mail", false))
		sec, _, err := im.secret(t, id, "account_password", r.Row["account_password"])
		if err != nil {
			return err
		}
		pos++
		im.st.authSources.add(id)
		return ins.add(id, "ldap", name, true, pos, r.bool("onthefly_register", false), toJSON(cfg), sec, im.nowStr, im.nowStr)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- users

var principalKinds = map[string]string{
	"User":           "user",
	"AnonymousUser":  "anonymous_user",
	"Group":          "group",
	"GroupAnonymous": "group_anonymous",
	"GroupNonMember": "group_non_member",
}

var mailNotificationValues = map[string]bool{
	"all": true, "selected": true, "only_my_events": true, "only_assigned": true, "only_owner": true, "none": true,
}

func (im *imp) importUsers() error {
	t := im.table("users")
	pins := im.ins(t, "principals", "id", "kind", "status", "firstname", "lastname", "name", "twofa_required", "created_at", "updated_at")
	uins := im.ins(t, "user_accounts", "principal_id", "login", "password_hash", "password_changed_at", "must_change_password",
		"admin", "language", "auth_source_id", "last_login_at", "twofa_scheme", "twofa_totp_key", "twofa_totp_last_used_at")
	logins := map[string]bool{}
	builtin := map[string]bool{}
	var maxID int64
	err := im.src.each("users", func(r rec) error {
		id := r.id()
		if id > maxID {
			maxID = id
		}
		typ := r.str("type")
		kind, ok := principalKinds[typ]
		if !ok {
			if typ == "" && r.str("login") != "" {
				kind = "user"
				t.repair(id, "type NULL; treated as User")
			} else {
				t.drop(id, "unknown principal type %q", typ)
				return nil
			}
		}
		if kind == "anonymous_user" || kind == "group_anonymous" || kind == "group_non_member" {
			if builtin[kind] {
				if kind == "anonymous_user" {
					kind = "user"
				} else {
					kind = "group"
				}
				t.repair(id, "duplicate built-in principal; converted to %s", kind)
			}
			builtin[kind] = true
		}
		status := r.intOr("status", 1)
		if status < 0 || status > 3 {
			def := int64(1)
			if kind == "anonymous_user" {
				def = 0
			}
			t.repair(id, "invalid status %d; set to %d", status, def)
			status = def
		}
		created := im.tsOr(t, id, r, "created_on", "updated_on")
		updated := im.tsOr(t, id, r, "updated_on", "created_on")
		first, last, name := r.str("firstname"), r.str("lastname"), ""
		if kind == "group" || kind == "group_anonymous" || kind == "group_non_member" {
			name, first, last = last, "", ""
		}
		im.st.principals[id] = kind
		if err := pins.add(id, kind, status, first, last, name, r.bool("twofa_required", false), created, updated); err != nil {
			return err
		}
		if kind != "user" && kind != "anonymous_user" {
			return nil
		}
		// user_accounts
		login := r.str("login")
		if kind == "anonymous_user" {
			login = ""
		}
		if login != "" {
			low := strings.ToLower(login)
			if logins[low] {
				login = fmt.Sprintf("%s-%d", login, id)
				low = strings.ToLower(login)
				t.repair(id, "duplicate login (case-insensitive); renamed to <login>-<id>")
			}
			logins[low] = true
		}
		var hash any
		if hp := r.str("hashed_password"); hp != "" && kind == "user" {
			if salt := r.str("salt"); salt != "" {
				hash = "redmine-sha1$" + salt + "$" + hp
			} else {
				hash = "redmine-sha1-nosalt$$" + hp
			}
		}
		authSource := r.ref("auth_source_id")
		if authSource != 0 && !im.st.authSources.has(authSource) {
			t.repair(id, "auth_source_id refers to a missing/unsupported auth source; set NULL")
			authSource = 0
		}
		scheme := r.strNull("twofa_scheme", true)
		if scheme != nil && scheme != "totp" {
			t.repair(id, "unknown twofa_scheme %v; 2FA reset", scheme)
			scheme = nil
		}
		var key any
		if scheme != nil {
			k, lost, err := im.secret(t, id, "twofa_totp_key", r.Row["twofa_totp_key"])
			if err != nil {
				return err
			}
			if lost || k == nil {
				t.repair(id, "TOTP secret unavailable; 2FA reset (user must re-enroll)")
				scheme = nil
			} else {
				key = k
			}
		}
		var lastUsed any
		if scheme != nil {
			if n, ok := r.int("twofa_totp_last_used_at"); ok {
				lastUsed = n
			}
		}
		lang := r.strNull("language", true)
		mn := r.str("mail_notification")
		if mn != "" && !mailNotificationValues[mn] {
			t.repair(id, "unknown mail_notification %q; set NULL (default)", mn)
			mn = ""
		}
		im.st.mailNotification[id] = mn
		im.st.users.add(id)
		im.st.userOrder = append(im.st.userOrder, id)
		if kind == "anonymous_user" {
			im.st.anonymousID = id
		}
		return uins.add(id, login, hash, im.tsNull(t, id, r, "passwd_changed_on"), r.bool("must_change_passwd", false),
			r.bool("admin", false), lang, nullIfZero(authSource), im.tsNull(t, id, r, "last_login_on"), scheme, key, lastUsed)
	})
	if err != nil {
		return err
	}
	// 組込プリンシパルがなければ作る(Redmine は初回参照時に作成する)
	for _, b := range []struct{ kind, name string }{
		{"anonymous_user", "Anonymous"}, {"group_non_member", "Non member users"}, {"group_anonymous", "Anonymous users"},
	} {
		if builtin[b.kind] {
			continue
		}
		maxID++
		first, last, name := "", "", b.name
		if b.kind == "anonymous_user" {
			last, name = b.name, ""
			im.st.anonymousID = maxID
		}
		status := int64(1)
		if b.kind == "anonymous_user" {
			status = 0
		}
		im.st.principals[maxID] = b.kind
		if err := pins.add(maxID, b.kind, status, first, last, name, false, im.nowStr, im.nowStr); err != nil {
			return err
		}
		if b.kind == "anonymous_user" {
			im.st.users.add(maxID)
			im.st.userOrder = append(im.st.userOrder, maxID)
			im.st.mailNotification[maxID] = "only_my_events"
			if err := uins.add(maxID, "", nil, nil, false, false, nil, nil, nil, nil, nil, nil); err != nil {
				return err
			}
		}
		t.repair(maxID, "built-in principal %s was missing; created", b.kind)
	}
	if _, err := pins.close(); err != nil {
		return err
	}
	_, err = uins.close()
	return err
}

// ---------------------------------------------------------------- email_addresses

func (im *imp) importEmailAddresses() error {
	t := im.table("email_addresses")
	ins := im.ins(t, "email_addresses", "id", "user_id", "address", "is_default", "notify", "created_at", "updated_at")
	seen := map[string]bool{}
	hasDefault := map[int64]bool{}
	first := map[int64]int64{}
	err := im.src.each("email_addresses", func(r rec) error {
		id := r.id()
		uid := r.ref("user_id")
		if !im.st.users.has(uid) {
			t.drop(id, "user_id refers to a missing user")
			return nil
		}
		addr := strings.TrimSpace(r.str("address"))
		if addr == "" {
			t.drop(id, "empty address")
			return nil
		}
		low := strings.ToLower(addr)
		if seen[low] {
			t.drop(id, "duplicate address (case-insensitive)")
			return nil
		}
		seen[low] = true
		def := r.bool("is_default", false)
		if def && hasDefault[uid] {
			def = false
			t.repair(id, "second default address of the user; is_default cleared")
		}
		if def {
			hasDefault[uid] = true
		}
		if f, ok := first[uid]; !ok || id < f {
			first[uid] = id
		}
		return ins.add(id, uid, addr, def, r.bool("notify", true), im.tsOr(t, id, r, "created_on", "updated_on"), im.tsOr(t, id, r, "updated_on", "created_on"))
	})
	if err != nil {
		return err
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	for uid, eid := range first {
		if hasDefault[uid] {
			continue
		}
		if _, err := im.exec(`UPDATE email_addresses SET is_default = ? WHERE id = ?`, true, eid); err != nil {
			return err
		}
		t.repair(eid, "user had no default address; oldest address made default")
	}
	return nil
}

// ---------------------------------------------------------------- groups_users

func (im *imp) importGroupsUsers() error {
	t := im.table("groups_users")
	ins := im.ins(t, "group_users", "group_id", "user_id")
	seen := map[[2]int64]bool{}
	err := im.src.each("groups_users", func(r rec) error {
		g, u := r.ref("group_id"), r.ref("user_id")
		key := fmt.Sprintf("%d-%d", g, u)
		if im.st.principals[g] != "group" {
			t.drop(key, "group_id is not an existing (non built-in) group")
			return nil
		}
		if im.st.principals[u] != "user" {
			t.drop(key, "user_id is not an existing user")
			return nil
		}
		if seen[[2]int64{g, u}] {
			t.drop(key, "duplicate")
			return nil
		}
		seen[[2]int64{g, u}] = true
		im.st.groupUsers[g] = append(im.st.groupUsers[g], u)
		return ins.add(g, u)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- tokens

func (im *imp) importTokens() error {
	t := im.table("tokens")
	ins := im.ins(t, "tokens", "id", "user_id", "action", "value", "created_at", "updated_at")
	bins := im.ins(t, "twofa_backup_codes", "user_id", "code_digest", "created_at")
	values := map[string]bool{}
	codes := map[string]bool{}
	autologinDays, _ := strconv.Atoi(strings.TrimSpace(im.setting("autologin")))
	err := im.src.each("tokens", func(r rec) error {
		id := r.id()
		uid := r.ref("user_id")
		action := r.str("action")
		if !im.st.users.has(uid) {
			t.drop(id, "user_id refers to a missing user")
			return nil
		}
		value := r.str("value")
		if value == "" {
			t.drop(id, "empty value")
			return nil
		}
		created, ok := im.tz.ts(r.Row["created_on"])
		if !ok {
			created = im.nowStr
			t.repair(id, "created_on missing/invalid; used import time")
		}
		createdT, _ := time.Parse("2006-01-02T15:04:05.000000Z", created)
		expired := func(validity time.Duration) bool { return createdT.Add(validity).Before(im.now) }
		switch action {
		case "api", "feeds":
		case "autologin":
			if autologinDays <= 0 {
				t.drop(id, "autologin token (autologin is disabled)")
				return nil
			}
			if expired(time.Duration(autologinDays) * 24 * time.Hour) {
				t.drop(id, "expired %s token", action)
				return nil
			}
		case "recovery", "register":
			if expired(24 * time.Hour) {
				t.drop(id, "expired %s token", action)
				return nil
			}
		case "session", "twofa_session":
			t.drop(id, "%s token (sessions are not migrated; users log in again)", action)
			return nil
		case "twofa_backup_code":
			sum := sha256.Sum256([]byte(value))
			dg := hex.EncodeToString(sum[:])
			k := fmt.Sprintf("%d-%s", uid, dg)
			if codes[k] {
				t.drop(id, "duplicate 2FA backup code")
				return nil
			}
			codes[k] = true
			return bins.add(uid, dg, created)
		default:
			t.drop(id, "unknown token action %q", action)
			return nil
		}
		if values[value] {
			t.drop(id, "duplicate token value")
			return nil
		}
		values[value] = true
		return ins.add(id, uid, action, value, created, im.tsNull(t, id, r, "updated_on"))
	})
	if err != nil {
		return err
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	_, err = bins.close()
	return err
}

// ---------------------------------------------------------------- user_preferences

// prefKnown は user_preferences の専用列に展開する others のキー。
var prefKnown = map[string]bool{
	"comments_sorting": true, "warn_on_leaving_unsaved": true, "no_self_notified": true,
	"notify_about_high_priority_issues": true, "textarea_font": true, "recently_used_projects": true,
	"history_default_tab": true, "toolbar_language_options": true, "default_issue_query": true,
	"default_project_query": true, "auto_watch_on": true, "my_page_layout": true, "my_page_settings": true,
	"bookmarked_project_ids": true, "recently_used_project_ids": true,
}

// truthyPref は others の真偽値(Redmine: value == true || value == '1')。
func truthyPref(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "1" || x == "true"
	case int64:
		return x == 1
	}
	return false
}

// prefNotify は通知設定に使う user_preferences の値。
type prefNotify struct {
	noSelf, highPriority bool
}

func (im *imp) importUserPreferences() error {
	t := im.table("user_preferences")
	ins := im.ins(t, "user_preferences", "user_id", "hide_mail", "time_zone", "comments_sorting", "warn_on_leaving_unsaved",
		"textarea_font", "recently_used_projects", "history_default_tab", "toolbar_language_options",
		"default_issue_query_id", "default_project_query_id", "auto_watch_on", "my_page_layout", "my_page_settings", "extra")
	bm := im.ins(t, "user_project_bookmarks", "user_id", "project_id", "position")
	rp := im.ins(t, "user_recent_projects", "user_id", "project_id", "position")
	im.st.prefNotify = map[int64]prefNotify{}
	err := im.src.each("user_preferences", func(r rec) error {
		id := r.id()
		uid := r.ref("user_id")
		if !im.st.users.has(uid) {
			t.drop(id, "user_id refers to a missing user")
			return nil
		}
		if _, dup := im.st.prefNotify[uid]; dup {
			t.drop(id, "duplicate preferences row for the user (first wins)")
			return nil
		}
		raw, derr := decodeYAML(r.Row["others"])
		others, isMap := asMap(raw)
		if derr != nil || (raw != nil && !isMap) {
			t.repair(id, "unparseable others YAML; ignored")
			others = nil
		}
		get := func(k string) any { v, _ := others.Get(k); return v }
		has := func(k string) bool { _, ok := others.Get(k); return ok }

		pn := prefNotify{noSelf: truthyPref(get("no_self_notified")), highPriority: truthyPref(get("notify_about_high_priority_issues"))}
		im.st.prefNotify[uid] = pn

		sorting := "asc"
		if s, _ := toStr(rubyyaml.Plain(get("comments_sorting"))); s == "desc" {
			sorting = "desc"
		}
		warn := true
		if has("warn_on_leaving_unsaved") && get("warn_on_leaving_unsaved") != nil {
			warn = truthyPref(get("warn_on_leaving_unsaved"))
		}
		var font any
		if s, _ := toStr(rubyyaml.Plain(get("textarea_font"))); s == "monospace" || s == "proportional" {
			font = s
		}
		recent := int64(3)
		if v := get("recently_used_projects"); v != nil {
			if n, ok := toInt(rubyyaml.Plain(v)); ok {
				recent = n
			} else if s, ok := v.(string); ok {
				recent = int64(rubyToI(s))
			}
		}
		strOrNull := func(k string) any {
			s, ok := toStr(rubyyaml.Plain(get(k)))
			if !ok || s == "" {
				return nil
			}
			return s
		}
		queryRef := func(k, kind string) any {
			s, ok := toStr(rubyyaml.Plain(get(k)))
			if !ok || s == "" {
				return nil
			}
			n, ok := toInt(s)
			if !ok || im.st.queries[n] != kind {
				t.repair(id, "%s refers to a missing query; set NULL", k)
				return nil
			}
			return n
		}
		auto := "[]"
		if v := get("auto_watch_on"); v != nil {
			list := []string{}
			for _, s := range stringList(v) {
				if s != "" {
					list = append(list, s)
				}
			}
			auto = toJSON(list)
		}
		var layout, mps any
		if v := get("my_page_layout"); v != nil {
			layout = toJSON(v)
		}
		if v := get("my_page_settings"); v != nil {
			mps = toJSON(v)
		}
		extra := rubyyaml.OrderedMap{}
		for _, it := range others {
			if !prefKnown[it.Key] {
				extra = append(extra, it)
			}
		}
		hide := r.bool("hide_mail", true)
		if err := ins.add(uid, hide, r.strNull("time_zone", true), sorting, warn, font, recent,
			strOrNull("history_default_tab"), strOrNull("toolbar_language_options"),
			queryRef("default_issue_query", "issue"), queryRef("default_project_query", "project"),
			auto, layout, mps, toJSON(extra)); err != nil {
			return err
		}
		for _, lst := range []struct {
			key string
			in  *inserter
		}{{"bookmarked_project_ids", bm}, {"recently_used_project_ids", rp}} {
			s, _ := toStr(rubyyaml.Plain(get(lst.key)))
			if v, ok := get(lst.key).([]any); ok {
				s = strings.Join(stringList(v), ",")
			}
			seen := map[int64]bool{}
			pos := int64(0)
			for _, p := range strings.Split(s, ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				pid, ok := toInt(p)
				if !ok || !im.projectExists(pid) || seen[pid] {
					t.repair(id, "%s: missing/duplicate project %q skipped", lst.key, p)
					continue
				}
				seen[pid] = true
				pos++
				if err := lst.in.add(uid, pid, pos); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, in := range []*inserter{ins, bm, rp} {
		if _, err := in.close(); err != nil {
			return err
		}
	}
	return nil
}

// rubyToI は Ruby の String#to_i(先頭の整数部分、なければ 0)。
func rubyToI(s string) int {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || (end == 0 && (s[end] == '-' || s[end] == '+'))) {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

// importUserNotifications は user_notification_settings(全ユーザー)と user_notified_projects を作る。
func (im *imp) importUserNotifications() error {
	t := im.table("(user notifications)")
	ins := im.ins(t, "user_notification_settings", "user_id", "mail_notification", "no_self_notified", "notify_about_high_priority_issues", "channels")
	defNoSelf := truthyPref(strings.TrimSpace(im.setting("default_users_no_self_notified")))
	for _, uid := range im.st.userOrder {
		pn, ok := im.st.prefNotify[uid]
		if !ok {
			// 設定行のないユーザーは Redmine と同じく Setting の既定値
			pn = prefNotify{noSelf: defNoSelf}
		}
		if err := ins.add(uid, nullStr(im.st.mailNotification[uid]), pn.noSelf, pn.highPriority, "email"); err != nil {
			return err
		}
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	np := im.ins(t, "user_notified_projects", "user_id", "project_id")
	for _, mid := range im.st.memberNotify {
		m := im.st.members[mid]
		if !im.st.users.has(m[0]) {
			continue // グループ等(Redmine でも意味を持たない)
		}
		if err := np.add(m[0], m[1]); err != nil {
			return err
		}
	}
	_, err := np.close()
	return err
}
