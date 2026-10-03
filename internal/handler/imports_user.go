// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/csvimport"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは UserImport（app/models/user_import.rb）の移植。

// importUserStatuses は User::LABEL_BY_STATUS（ラベル → 状態）。
var importUserStatuses = map[string]int{
	"anon": domain.StatusAnonymous, "active": domain.StatusActive, "registered": domain.StatusRegistered, "locked": domain.StatusLocked,
}

// userBuildAndSave は UserImport#build_object と object.save。
func (m *importModel) userBuildAndSave(ctx context.Context, row csvimport.Row, item *repository.ImportItem) (importResult, error) {
	um := m.a.newUserModel(m.c)
	attrs := httpx.NewParams()
	attrs.Set("login", strPtrAny(m.rowValue(row, "login")))
	attrs.Set("firstname", strPtrAny(m.rowValue(row, "firstname")))
	attrs.Set("lastname", strPtrAny(m.rowValue(row, "lastname")))
	attrs.Set("mail", strPtrAny(m.rowValue(row, "mail")))
	lang := ""
	if v := m.rowValue(row, "language"); v != nil {
		lang = m.a.Bundle.FindLanguage(*v)
	}
	if lang == "" {
		lang = m.a.Settings.String("default_language")
	}
	attrs.Set("language", lang)
	if v := m.rowValue(row, "admin"); v != nil && m.yes(*v) {
		attrs.Set("admin", "1")
	}
	if v := m.rowValue(row, "auth_source"); v != nil {
		sources, err := repository.ListAuthSources(ctx, m.a.DB)
		if err != nil {
			return importResult{}, err
		}
		// AuthSource.find_by(:name => name)（id 順の最初）
		var found int64
		for _, s := range sources {
			if s.Name == *v && (found == 0 || s.ID < found) {
				found = s.ID
			}
		}
		if found != 0 {
			attrs.Set("auth_source_id", strconv.FormatInt(found, 10))
		}
	}
	if v := m.rowValue(row, "password"); v != nil {
		pw := *v
		um.password, um.passwordConfirmation = &pw, &pw
	}
	if v := m.rowValue(row, "must_change_passwd"); v != nil && m.yes(*v) {
		attrs.Set("must_change_passwd", "1")
	}
	if v := m.rowValue(row, "status"); v != nil {
		if st, ok := importUserStatuses[*v]; ok {
			attrs.Set("status", strconv.Itoa(st))
		}
	}
	cfAttrs := httpx.NewParams()
	cfs, err := customfield.ListByKind(ctx, m.a.DB, customfield.KindUser)
	if err != nil {
		return importResult{}, err
	}
	for _, cv := range um.customValues {
		f := cv.Field
		key := "cf_" + strconv.FormatInt(f.ID, 10)
		var value *string
		if f.FieldFormat == "date" {
			value = m.rowDate(row, key)
		} else {
			value = m.rowValue(row, key)
		}
		if value == nil {
			continue
		}
		idx := slices.IndexFunc(cfs, func(x *customfield.CustomField) bool { return x.ID == f.ID })
		if idx < 0 {
			continue
		}
		val := m.cfValueFromKeyword(ctx, cfs[idx], *value, nil)
		switch x := val.(type) {
		case []string:
			arr := make([]any, len(x))
			for i, s := range x {
				arr[i] = s
			}
			cfAttrs.Set(strconv.FormatInt(f.ID, 10), arr)
		case nil:
			cfAttrs.Set(strconv.FormatInt(f.ID, 10), "")
		default:
			cfAttrs.Set(strconv.FormatInt(f.ID, 10), importToS(x))
		}
	}
	attrs.Set("custom_field_values", cfAttrs)
	um.assignSafeAttributes(attrs, m.user)
	ok, err := m.a.saveUser(m.c, um)
	if err != nil {
		return importResult{}, err
	}
	if !ok {
		return importResult{obj: um, message: strings.Join(um.errors.FullMessages(m.c.Loc), "\n")}, nil
	}
	return importResult{obj: um, persisted: true, objID: um.ID}, nil
}

// userExtendObject は extend_object（通知設定なら Mailer.deliver_account_information）。
func (m *importModel) userExtendObject(res importResult) {
	if !m.yes(m.setting("notifications")) {
		return
	}
	if um, ok := res.obj.(*userModel); ok {
		m.a.deliverAccountInformation(m.c, um)
	}
}

// importSavedUsers は saved_objects（User.where(:id => ids).order(:id)）。
func (a *App) importSavedUsers(c *Req, ids []int64) ([]*domain.User, error) {
	um, err := repository.UsersByIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		return nil, err
	}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	var out []*domain.User
	for _, id := range sorted {
		if u := um[id]; u != nil {
			out = append(out, u)
		}
	}
	return out, nil
}

// importUserStatusLabel は l("status_#{User::LABEL_BY_STATUS[user.status]}")。
func importUserStatusLabel(c *Req, status int) string {
	for k, v := range importUserStatuses {
		if v == status {
			return c.L("status_" + k)
		}
	}
	return c.L("status_")
}
