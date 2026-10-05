// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// LDAP 認証方式の buropher 拡張（Redmine の AuthSourceLdap に無い設定）。
//
// 設定は auth_sources.config に Redmine の項目と並べて保存する（キーは ldapExtStringFields / ldapExtBoolFields）。
// フォームは Redmine の _form_auth_source_ldap の後ろに、class="buropher-extra" の fieldset として追加する
// （auth_sources/_form_auth_source_ldap_extra。Redmine の項目の HTML は変えない）。
//
//   - 接続: STARTTLS、予備のホスト（フェイルオーバ）、CA 証明書（PEM の本文またはファイルのパス）、ディレクトリの種類
//     （OpenLDAP / Active Directory。AD はネストしたグループの LDAP_MATCHING_RULE_IN_CHAIN と userAccountControl を使う）。
//   - グループ同期: memberOf 属性またはグループ検索で得た LDAP グループを auth_source_group_mappings に従って
//     buropher のグループに対応付け、ログインのたびに（対応表にあるグループだけ）追加・削除する。
//   - 定期同期（ジョブ JobLDAPSync。1 時間ごとに起動し、sync_interval 時間を過ぎた認証方式を処理する）:
//     ディレクトリに見つからない / AD で無効なユーザーのロック、氏名・メールアドレスの更新、グループ同期。
//     管理画面の「今すぐ同期」でも実行でき、最後の結果を config の sync_last_at / sync_last_log に残す。

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/auth/ldap"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/repository"
)

// JobLDAPSync は LDAP の定期同期ジョブの種類。
const JobLDAPSync = "ldap_sync"

// LDAPSyncCheckInterval は定期同期ジョブを積む間隔（各認証方式の sync_interval はジョブの中で判定する）。
const LDAPSyncCheckInterval = time.Hour

var ldapExtStringFields = []string{"failover_hosts", "ca_cert", "directory_type", "group_mode", "group_base_dn",
	"group_filter", "group_member_attr", "group_memberof_attr", "sync_interval"}

var ldapExtBoolFields = []string{"starttls", "starttls_verify_peer", "group_nested", "sync_enabled", "sync_lock_missing", "sync_update_attrs"}

// ldapExtFields は ldapSourceForm の拡張部分。
type ldapExtFields struct {
	extStrs         map[string]string
	extBools        map[string]bool
	mappings        []domain.AuthSourceGroupMapping
	mappingsChanged bool
	syncLastAt      string
	syncLastLog     string
}

func isLDAPExtField(attr string) bool {
	return slices.Contains(ldapExtStringFields, attr) || slices.Contains(ldapExtBoolFields, attr)
}

// initLDAPExt は保存済みの設定から拡張部分を読み込む。
func (a *App) initLDAPExt(c *Req, f *ldapSourceForm) {
	rec := f.rec
	f.extStrs, f.extBools = map[string]string{}, map[string]bool{}
	// 検証エラーの属性名も buropher.ldap.field_<attr> を使う
	f.errors.AttrNames = map[string]string{}
	for _, k := range append(slices.Clone(ldapExtStringFields), ldapExtBoolFields...) {
		f.errors.AttrNames[k] = "buropher.ldap.field_" + k
	}
	for _, k := range ldapExtStringFields {
		if s, ok := rec.ConfigString(k); ok {
			f.extStrs[k] = s
		} else if n, ok := rec.ConfigInt(k); ok {
			f.extStrs[k] = strconv.Itoa(n)
		}
	}
	for _, k := range ldapExtBoolFields {
		f.extBools[k] = rec.ConfigBool(k)
	}
	if _, ok := rec.Config["starttls_verify_peer"]; !ok && rec.ID == 0 {
		// 新規作成では STARTTLS の証明書を既定で検証する（未検証の STARTTLS は能動的な中間者に
		// サービスアカウント・ユーザーのパスワードを渡してしまう）
		f.extBools["starttls_verify_peer"] = true
	}
	f.syncLastAt, _ = rec.ConfigString("sync_last_at")
	f.syncLastLog, _ = rec.ConfigString("sync_last_log")
	if rec.ID != 0 {
		ms, err := repository.AuthSourceGroupMappings(c.Ctx(), a.DB, rec.ID)
		if err != nil {
			a.logger().Error("group mappings", "err", err)
		}
		f.mappings = ms
	}
}

// sendExt はフォームビルダの拡張属性の参照。
func (f *ldapSourceForm) sendExt(method string) (any, bool) {
	if slices.Contains(ldapExtStringFields, method) {
		return f.extStrs[method], true
	}
	if slices.Contains(ldapExtBoolFields, method) {
		return f.extBools[method], true
	}
	return nil, false
}

// assignExt は auth_source[<拡張属性>] の代入。
func (f *ldapSourceForm) assignExt(k, s string) {
	switch {
	case slices.Contains(ldapExtStringFields, k):
		f.extStrs[k] = s
	case slices.Contains(ldapExtBoolFields, k):
		f.extBools[k] = castBool(s)
	}
}

// assignMappings はグループ対応表（group_mapping_external[] / group_mapping_group_id[]）の代入。
func (f *ldapSourceForm) assignMappings(all *httpx.Params) {
	if all == nil || !all.Has("group_mapping_external") {
		return
	}
	ext := all.Strings("group_mapping_external")
	gids := all.Strings("group_mapping_group_id")
	var ms []domain.AuthSourceGroupMapping
	for i, e := range ext {
		e = strings.TrimSpace(e)
		if e == "" || i >= len(gids) {
			continue
		}
		gid, err := strconv.ParseInt(gids[i], 10, 64)
		if err != nil || gid <= 0 {
			continue
		}
		ms = append(ms, domain.AuthSourceGroupMapping{ExternalGroup: e, GroupID: gid})
	}
	f.mappings, f.mappingsChanged = ms, true
}

// Mappings は対応表の行（入力用に空行を 3 つ足す）。
func (f *ldapSourceForm) Mappings() []domain.AuthSourceGroupMapping {
	return append(slices.Clone(f.mappings), domain.AuthSourceGroupMapping{}, domain.AuthSourceGroupMapping{}, domain.AuthSourceGroupMapping{})
}

// SyncLastAt / SyncLastLog は最後の定期同期の時刻と結果。
func (f *ldapSourceForm) SyncLastAt() string  { return f.syncLastAt }
func (f *ldapSourceForm) SyncLastLog() string { return f.syncLastLog }

// validateLDAPExt は拡張属性の検証（Redmine の検証の後に行う）。
func (f *ldapSourceForm) validateLDAPExt() {
	e := f.errors
	for k, v := range f.extStrs {
		f.extStrs[k] = strings.TrimSpace(v)
	}
	if !slices.Contains([]string{ldap.DirectoryGeneric, ldap.DirectoryOpenLDAP, ldap.DirectoryActiveDirectory}, f.extStrs["directory_type"]) {
		e.Add("directory_type", "inclusion")
	}
	if !slices.Contains([]string{ldap.GroupModeNone, ldap.GroupModeMemberOf, ldap.GroupModeSearch}, f.extStrs["group_mode"]) {
		e.Add("group_mode", "inclusion")
	}
	if f.extBools["starttls"] && f.tls {
		// LDAPS と STARTTLS は併用できない
		e.Add("starttls", "invalid")
	}
	if !ldap.ValidCACert(f.extStrs["ca_cert"]) {
		e.Add("ca_cert", "invalid")
	}
	if g := f.extStrs["group_filter"]; g != "" && !ldap.ValidFilter(g) {
		e.Add("group_filter", "invalid")
	}
	if v := f.extStrs["sync_interval"]; v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			e.Add("sync_interval", "not_a_number")
		} else if n < 1 {
			e.Add("sync_interval", "greater_than_or_equal_to", "count", 1)
		}
	}
	if f.extBools["sync_enabled"] && f.account != nil && strings.Contains(*f.account, "$login") {
		e.Add("sync_enabled", "invalid")
	}
}

// saveLDAPExt は拡張属性を config に書く（空・false は保存しない）。
func (f *ldapSourceForm) saveLDAPExt(cfg map[string]any) {
	for _, k := range ldapExtStringFields {
		v := f.extStrs[k]
		switch {
		case v == "":
			delete(cfg, k)
		case k == "sync_interval":
			n, _ := strconv.Atoi(v)
			cfg[k] = n
		case k == "failover_hosts":
			cfg[k] = strings.Join(ldap.ParseHostList(v), "\n")
		default:
			cfg[k] = v
		}
	}
	for _, k := range ldapExtBoolFields {
		if f.extBools[k] {
			cfg[k] = true
		} else {
			delete(cfg, k)
		}
	}
}

// applyLDAPExt は保存済みの設定の拡張部分を接続設定に反映する。
func applyLDAPExt(rec *domain.AuthSourceRecord, src *ldap.Source) {
	str := func(k string) string { s, _ := rec.ConfigString(k); return s }
	src.FailoverHosts = ldap.ParseHostList(str("failover_hosts"))
	src.CACert = str("ca_cert")
	if src.StartTLS && !src.TLS && rec.ConfigBool("starttls_verify_peer") {
		// 接続方式「LDAP」は verify_peer を偽にするため、STARTTLS の証明書の検証は別の設定で持つ
		src.VerifyPeer = true
	}
	src.DirectoryType = str("directory_type")
	src.Groups = ldap.GroupConfig{Mode: str("group_mode"), BaseDN: str("group_base_dn"), Filter: str("group_filter"),
		MemberAttr: str("group_member_attr"), MemberOfAttr: str("group_memberof_attr"), Nested: rec.ConfigBool("group_nested")}
}

// ldapGroupOptions はグループ対応表の選択肢（グループ名, id）。
func (a *App) ldapGroupOptions(c *Req) ([][]any, error) {
	groups, err := repository.ListGroups(c.Ctx(), a.DB, false)
	if err != nil {
		return nil, err
	}
	opts := make([][]any, 0, len(groups))
	for _, g := range groups {
		opts = append(opts, []any{g.Name, g.ID})
	}
	return opts, nil
}

// renderLDAPSourceForm は auth_sources/new・edit を描画する。
func (a *App) renderLDAPSourceForm(c *Req, tmpl string, f *ldapSourceForm) {
	opts, err := a.ldapGroupOptions(c)
	if err != nil {
		a.internalError(c, "groups", err)
		return
	}
	c.NoStore()
	c.renderAdmin(tmpl, map[string]any{"AuthSource": f, "GroupOptions": opts}, false)
}

// ---------------------------------------------------------------- グループ同期

// syncMappedGroups は対応表にあるグループについて、member(外部グループ名) が真なら追加、偽なら削除する。
// 戻り値は追加・削除したグループ id。
func (a *App) syncMappedGroups(ctx context.Context, sourceID, userID int64, member func(ext string) bool) (added, removed []int64, err error) {
	ms, err := repository.AuthSourceGroupMappings(ctx, a.DB, sourceID)
	if err != nil || len(ms) == 0 {
		return nil, nil, err
	}
	want := map[int64]bool{}
	var managed []int64
	for _, m := range ms {
		if !slices.Contains(managed, m.GroupID) {
			managed = append(managed, m.GroupID)
		}
		if member(m.ExternalGroup) {
			want[m.GroupID] = true
		}
	}
	current, err := repository.UserGroupIDs(ctx, a.DB, userID)
	if err != nil {
		return nil, nil, err
	}
	err = a.DB.WithTx(ctx, func(tx *db.Tx) error {
		for _, g := range managed {
			has := slices.Contains(current, g)
			switch {
			case want[g] && !has:
				if err := repository.AddUserToGroup(ctx, tx, g, userID); err != nil {
					return err
				}
				added = append(added, g)
			case !want[g] && has:
				if err := repository.RemoveUserFromGroup(ctx, tx, g, userID); err != nil {
					return err
				}
				removed = append(removed, g)
			}
		}
		return nil
	})
	return added, removed, err
}

// syncLDAPGroups は LDAP グループの DN 一覧に従ってユーザーのグループを同期する（取得できていなければ何もしない）。
func (a *App) syncLDAPGroups(ctx context.Context, sourceID, userID int64, groups []string, fetched bool) (added, removed []int64, err error) {
	if !fetched {
		return nil, nil, nil
	}
	return a.syncMappedGroups(ctx, sourceID, userID, func(ext string) bool {
		for _, dn := range groups {
			if ldap.GroupMatches(ext, dn) {
				return true
			}
		}
		return false
	})
}

// syncLDAPGroupsOnLogin はログイン（LDAP 認証の成功）時のグループ同期。失敗はログに残してログインは続ける。
func (a *App) syncLDAPGroupsOnLogin(c *Req, sourceID, userID int64, attrs *ldap.Attrs) {
	if attrs == nil || !attrs.GroupsFetched {
		return
	}
	added, removed, err := a.syncLDAPGroups(c.Ctx(), sourceID, userID, attrs.Groups, true)
	if err != nil {
		a.logger().Error("LDAP group synchronization", "user_id", userID, "err", err)
		return
	}
	if len(added)+len(removed) > 0 {
		a.logger().Info("LDAP groups synchronized", "user_id", userID, "added", added, "removed", removed)
	}
}

// ---------------------------------------------------------------- 定期同期

// ldapSyncResult は 1 つの認証方式の同期結果。
type ldapSyncResult struct {
	Users, Updated, Locked, GroupChanges, Errors int
	Lines                                        []string
}

func (r *ldapSyncResult) logf(format string, args ...any) {
	if len(r.Lines) < 500 {
		r.Lines = append(r.Lines, fmt.Sprintf(format, args...))
	}
}

// Summary は結果の要約。
func (r *ldapSyncResult) Summary() string {
	return fmt.Sprintf("users: %d, updated: %d, locked: %d, group changes: %d, errors: %d",
		r.Users, r.Updated, r.Locked, r.GroupChanges, r.Errors)
}

// syncLDAPSource は 1 つの LDAP 認証方式のユーザーを同期し、結果を config に保存する。
func (a *App) syncLDAPSource(ctx context.Context, rec *domain.AuthSourceRecord) (*ldapSyncResult, error) {
	res := &ldapSyncResult{}
	src := a.ldapSource(rec)
	if src == nil {
		return nil, fmt.Errorf("auth source %d is not LDAP", rec.ID)
	}
	lockMissing, updateAttrs := rec.ConfigBool("sync_lock_missing"), rec.ConfigBool("sync_update_attrs")
	users, err := repository.UsersByAuthSource(ctx, a.DB, rec.ID)
	if err != nil {
		return nil, err
	}
	now := a.now()
	for _, u := range users {
		if !u.Active() {
			continue
		}
		res.Users++
		info, err := src.Lookup(u.Login)
		if err != nil {
			res.Errors++
			res.logf("%s: error: %v", u.Login, err)
			if errors.Is(err, ldap.ErrNotSearchable) {
				break
			}
			continue
		}
		if info == nil || info.Disabled {
			reason := "not found in the directory"
			if info != nil {
				reason = "disabled in Active Directory"
			}
			switch {
			case !lockMissing:
				res.logf("%s: %s (not locked)", u.Login, reason)
			case u.AdminFlag:
				res.logf("%s: %s (administrator, not locked)", u.Login, reason)
			default:
				if err := repository.SetUsersStatus(ctx, a.DB, []int64{u.ID}, domain.StatusLocked); err != nil {
					return nil, err
				}
				// ロックしたユーザーのセッション・自動ログイン・パスワード再設定のトークンを破棄する
				if err := repository.DeleteUserTokensByActions(ctx, a.DB, u.ID, "recovery", "autologin", "session"); err != nil {
					return nil, err
				}
				res.Locked++
				res.logf("%s: %s, locked", u.Login, reason)
			}
			continue
		}
		if updateAttrs {
			changed, err := a.updateLDAPUserAttrs(ctx, u, info, now, res)
			if err != nil {
				return nil, err
			}
			if changed {
				res.Updated++
			}
		}
		added, removed, err := a.syncLDAPGroups(ctx, rec.ID, u.ID, info.Groups, info.GroupsFetched)
		if err != nil {
			return nil, err
		}
		if n := len(added) + len(removed); n > 0 {
			res.GroupChanges += n
			res.logf("%s: groups added %v, removed %v", u.Login, added, removed)
		}
	}
	log := res.Summary()
	if len(res.Lines) > 0 {
		log += "\n" + strings.Join(res.Lines, "\n")
	}
	if err := repository.UpdateAuthSourceConfigValues(ctx, a.DB, rec.ID, map[string]any{
		"sync_last_at": now.UTC().Format(time.RFC3339), "sync_last_log": log}); err != nil {
		return nil, err
	}
	a.logger().Info("LDAP synchronization", "auth_source", rec.Name, "result", res.Summary())
	return res, nil
}

// updateLDAPUserAttrs は氏名・メールアドレスをディレクトリの値に合わせる（空の値では上書きしない）。
// 値はユーザーのモデルと同じ規則（氏名の長さ・メールアドレスの書式・長さ・許可／拒否ドメイン）で検証し、
// 通らない値は保存しない（ディレクトリの自分のエントリを編集できるユーザーが任意の値を入れられるため）。
func (a *App) updateLDAPUserAttrs(ctx context.Context, u *domain.User, info *ldap.UserInfo, now time.Time, res *ldapSyncResult) (bool, error) {
	changed := false
	info2 := *info
	info = &info2
	if utf8.RuneCountInString(info.Firstname) > 30 {
		res.logf("%s: invalid firstname in the directory (not updated)", u.Login)
		info.Firstname = ""
	}
	if utf8.RuneCountInString(info.Lastname) > 255 {
		res.logf("%s: invalid lastname in the directory (not updated)", u.Login)
		info.Lastname = ""
	}
	if info.Mail != "" {
		info.Mail = domain.NormalizeEmail(info.Mail)
		host := info.Mail[strings.LastIndex(info.Mail, "@")+1:]
		if !domain.EmailRegexp.MatchString(info.Mail) || utf8.RuneCountInString(info.Mail) > domain.MailLengthLimit ||
			!domain.ValidEmailDomain(host, a.Settings.String("email_domains_denied"), a.Settings.String("email_domains_allowed")) {
			res.logf("%s: invalid email %s in the directory (not updated)", u.Login, info.Mail)
			info.Mail = ""
		}
	}
	if (info.Firstname != "" && info.Firstname != u.Firstname) || (info.Lastname != "" && info.Lastname != u.Lastname) {
		// 氏名だけを保存する（同期の間に管理者が行ったロック・降格を読み込み時の値で戻さない）
		orig := *u
		if info.Firstname != "" {
			u.Firstname = info.Firstname
		}
		if info.Lastname != "" {
			u.Lastname = info.Lastname
		}
		if err := repository.UpdateUser(ctx, a.DB, u, &orig, true, now); err != nil {
			return false, err
		}
		changed = true
		res.logf("%s: name updated to %s %s", u.Login, u.Firstname, u.Lastname)
	}
	if info.Mail != "" {
		taken, err := repository.EmailTakenByOtherUser(ctx, a.DB, info.Mail, u.ID)
		if err != nil {
			return false, err
		}
		if taken {
			res.logf("%s: email %s is used by another user (not updated)", u.Login, info.Mail)
		} else {
			ok, err := repository.SetDefaultEmail(ctx, a.DB, u.ID, info.Mail, now)
			if err != nil {
				return false, err
			}
			if ok {
				changed = true
				res.logf("%s: email updated to %s", u.Login, info.Mail)
			}
		}
	}
	return changed, nil
}

// ldapSyncDue は定期同期の時期か（sync_interval 時間。既定 24）。
func ldapSyncDue(rec *domain.AuthSourceRecord, now time.Time) bool {
	if !rec.Enabled || !rec.ConfigBool("sync_enabled") {
		return false
	}
	hours, ok := rec.ConfigInt("sync_interval")
	if !ok || hours < 1 {
		hours = 24
	}
	last, _ := rec.ConfigString("sync_last_at")
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || !now.Before(t.Add(time.Duration(hours)*time.Hour))
}

// LDAPSyncJob はジョブ JobLDAPSync（定期同期が有効で時期を過ぎた LDAP 認証方式を同期する）。
func (a *App) LDAPSyncJob(ctx context.Context, _ *jobs.Job) error {
	recs, err := repository.AllAuthSources(ctx, a.DB)
	if err != nil {
		return err
	}
	now := a.now()
	for _, rec := range recs {
		if rec.Kind != domain.AuthSourceKindLDAP || !ldapSyncDue(rec, now) {
			continue
		}
		if _, err := a.syncLDAPSource(ctx, rec); err != nil {
			a.logger().Error("LDAP synchronization", "auth_source", rec.Name, "err", err)
		}
	}
	return nil
}

// RegisterLDAPSyncJob はジョブキューに JobLDAPSync を登録する。
func (a *App) RegisterLDAPSyncJob(q *jobs.Queue) { q.Register(JobLDAPSync, a.LDAPSyncJob) }

// routesAuthSourcesLDAPExt は buropher 拡張のルート（POST /auth_sources/:id/sync = 今すぐ同期）。
func (a *App) routesAuthSourcesLDAPExt(r Router) {
	a.Handle(r, http.MethodPost, "/auth_sources/{id}/sync", AuthSourcesController, "sync", a.AuthSourcesSync, RequireAdmin(), Before(a.findAuthSource))
}

// AuthSourcesSync は「今すぐ同期」（LDAP のユーザーを同期して編集画面に戻る）。
func (a *App) AuthSourcesSync(c *Req) {
	rec := c.authSource()
	if rec.Kind != domain.AuthSourceKindLDAP {
		c.Render404("")
		return
	}
	res, err := a.syncLDAPSource(c.Ctx(), rec)
	if err != nil {
		// フラッシュは raw HTML として描画されるため、LDAP 由来のエラー文言や
		// サマリ（ユーザー名等を含みうる）はエスケープする。
		c.Flash().SetError(c.L("buropher.ldap.error_sync", map[string]any{"value": html.EscapeString(err.Error())}))
	} else {
		c.Flash().SetNotice(c.L("buropher.ldap.notice_sync", map[string]any{"value": html.EscapeString(res.Summary())}))
	}
	c.Redirect("/auth_sources/" + strconv.FormatInt(rec.ID, 10) + "/edit")
}
