// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
)

// Validate は valid? (before_validation コールバックを実行してから検証する)。
// エラーは iss.Errors に入る。
func (e *Env) Validate(ctx context.Context, iss *Issue) (bool, error) {
	iss.Errors = domain.ValidationErrors{}
	if iss.NewRecord() {
		if err := e.defaultAssign(ctx, iss); err != nil {
			return false, err
		}
		if err := e.forceDefaultValueOnNoneditableCustomFields(ctx, iss); err != nil {
			return false, err
		}
	}
	if err := e.clearDisabledFields(ctx, iss); err != nil {
		return false, err
	}
	steps := []func(context.Context, *Issue) error{
		e.validateCustomFieldValues, e.validateBasics, e.validateIssue, e.validateRequiredFields, e.validatePermissions,
	}
	for _, f := range steps {
		if err := f(ctx, iss); err != nil {
			return false, err
		}
	}
	return !iss.Errors.Any(), nil
}

// defaultAssign は default_assign (担当者未設定ならカテゴリの担当者、なければプロジェクトの既定担当者)。
func (e *Env) defaultAssign(ctx context.Context, iss *Issue) error {
	if iss.AssignedToID != nil {
		if p, err := e.Principal(ctx, *iss.AssignedToID); err != nil || p != nil {
			return err
		}
	}
	if cat, err := e.Category(ctx, iss.CategoryID); err != nil {
		return err
	} else if cat != nil && cat.AssignedToID != nil {
		if p, err := e.Principal(ctx, *cat.AssignedToID); err != nil {
			return err
		} else if p != nil {
			iss.AssignedToID = ptrInt64(p.ID)
			return nil
		}
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return err
	}
	if p != nil && p.DefaultAssignedToID != nil {
		if pr, err := e.Principal(ctx, *p.DefaultAssignedToID); err != nil {
			return err
		} else if pr != nil {
			iss.AssignedToID = ptrInt64(pr.ID)
		}
	}
	return nil
}

// forceDefaultValueOnNoneditableCustomFields は作成者が編集できないカスタムフィールドを既定値にする。
func (e *Env) forceDefaultValueOnNoneditableCustomFields(ctx context.Context, iss *Issue) error {
	if !iss.cfvChanged {
		return nil
	}
	author, err := e.UserByID(ctx, iss.AuthorID)
	if err != nil {
		return err
	}
	if author == nil {
		if author, err = e.currentUser(ctx); err != nil {
			return err
		}
	}
	ed, err := e.EditableCustomFields(ctx, iss, author)
	if err != nil {
		return err
	}
	vs, err := e.CustomFieldValues(ctx, iss)
	if err != nil {
		return err
	}
	for _, v := range vs {
		if !slices.Contains(ed, v.Field) {
			dv := e.cfDefaultValue(ctx, v.Field)
			if dv == nil {
				v.Value = castCustomFieldInput(v.Field, nil)
			} else {
				v.Value = castCustomFieldInput(v.Field, *dv)
			}
		}
	}
	return nil
}

// clearDisabledFields は clear_disabled_fields (トラッカーで無効な標準フィールドを nil、優先度と進捗率の既定値)。
func (e *Env) clearDisabledFields(ctx context.Context, iss *Issue) error {
	t, err := e.TrackerOf(ctx, iss)
	if err != nil || t == nil {
		return err
	}
	for _, f := range t.DisabledCoreFields {
		switch f {
		case "assigned_to_id":
			iss.AssignedToID = nil
		case "category_id":
			iss.CategoryID = nil
		case "fixed_version_id":
			iss.FixedVersionID = nil
		case "parent_issue_id":
			iss.SetParentIssue(nil)
		case "start_date":
			iss.SetStartDate(nil)
		case "due_date":
			iss.SetDueDate(nil)
		case "estimated_hours":
			iss.SetEstimatedHours(nil)
		case "done_ratio":
			iss.DoneRatio = 0
		case "description":
			iss.Description = nil
		case "priority_id":
			iss.PriorityID = 0
		}
	}
	if iss.PriorityID == 0 {
		def, err := e.DefaultPriority(ctx)
		if err != nil {
			return err
		}
		if def != nil {
			iss.PriorityID = def.ID
		} else {
			ps, err := e.Priorities(ctx)
			if err != nil {
				return err
			}
			for _, p := range ps {
				if p.Active {
					iss.PriorityID = p.ID
					break
				}
			}
		}
	}
	return nil
}

// validateBasics は validates_* の宣言 (存在・長さ・範囲・数値・日付)。
func (e *Env) validateBasics(ctx context.Context, iss *Issue) error {
	errs := &iss.Errors
	if strings.TrimSpace(iss.Subject) == "" {
		errs.Add("subject", "blank", nil)
	}
	if p, err := e.ProjectOf(ctx, iss); err != nil {
		return err
	} else if p == nil {
		errs.Add("project", "blank", nil)
	}
	if t, err := e.TrackerOf(ctx, iss); err != nil {
		return err
	} else if t == nil {
		errs.Add("tracker", "blank", nil)
	}
	if iss.NewRecord() || iss.AttrChanged("priority_id") {
		if p, err := e.PriorityOf(ctx, iss); err != nil {
			return err
		} else if p == nil {
			errs.Add("priority", "blank", nil)
		}
	}
	if iss.NewRecord() || iss.AttrChanged("status_id") {
		if s, err := e.StatusOf(ctx, iss); err != nil {
			return err
		} else if s == nil {
			errs.Add("status", "blank", nil)
		}
	}
	if iss.NewRecord() || iss.AttrChanged("author_id") {
		if a, err := e.UserByID(ctx, iss.AuthorID); err != nil {
			return err
		} else if a == nil {
			errs.Add("author", "blank", nil)
		}
	}
	if utf8.RuneCountInString(iss.Subject) > 255 {
		errs.Add("subject", "too_long", map[string]any{"count": 255})
	}
	if iss.DoneRatio < 0 || iss.DoneRatio > 100 {
		errs.Add("done_ratio", "inclusion", nil)
	}
	if iss.estimatedHoursRaw != "" || (iss.EstimatedHours != nil && *iss.EstimatedHours < 0) {
		errs.Add("estimated_hours", "invalid", nil)
	}
	if iss.startDateRaw != "" {
		errs.Add("start_date", "not_a_date", nil)
	}
	if iss.dueDateRaw != "" {
		errs.Add("due_date", "not_a_date", nil)
	}
	return nil
}

// validateIssue は validate_issue。
func (e *Env) validateIssue(ctx context.Context, iss *Issue) error {
	errs := &iss.Errors
	if iss.DueDate != nil && iss.StartDate != nil && (iss.AttrChanged("start_date") || iss.AttrChanged("due_date")) &&
		iss.DueDate.Before(*iss.StartDate) {
		errs.Add("due_date", "greater_than_start_date", nil)
	}
	if iss.StartDate != nil && iss.AttrChanged("start_date") {
		ss, err := e.SoonestStart(ctx, iss)
		if err != nil {
			return err
		}
		if ss != nil && iss.StartDate.Before(*ss) {
			errs.Add("start_date", "earlier_than_minimum_start_date", map[string]any{"date": e.formatDate(*ss)})
		}
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return err
	}
	if p != nil && iss.FixedVersionID != nil {
		fv, err := e.Version(ctx, iss.FixedVersionID)
		if err != nil {
			return err
		}
		av, err := e.AssignableVersions(ctx, iss)
		if err != nil {
			return err
		}
		if fv == nil || !slices.ContainsFunc(av, func(v *domain.Version) bool { return v.ID == fv.ID }) {
			errs.Add("fixed_version_id", "inclusion", nil)
		} else if re, err := e.Reopening(ctx, iss); err != nil {
			return err
		} else if re && fv.IsClosed() {
			errs.AddMessage("base", e.translator()("error_can_not_reopen_issue_on_closed_version"))
		}
	}
	if p != nil && iss.CategoryID != nil {
		ids, err := e.ProjectCategoryIDs(ctx, p.ID)
		if err != nil {
			return err
		}
		if !containsID(ids, *iss.CategoryID) {
			errs.Add("category_id", "inclusion", nil)
		}
	}
	if p != nil && (iss.AttrChanged("tracker_id") || iss.AttrChanged("project_id")) {
		t, err := e.TrackerOf(ctx, iss)
		if err != nil {
			return err
		}
		if t != nil && !slices.Contains(t.ProjectIDs, p.ID) {
			errs.Add("tracker_id", "inclusion", nil)
		}
	}
	if p != nil && iss.AttrChanged("assigned_to_id") && iss.AssignedToID != nil {
		users, err := e.AssignableUsers(ctx, iss)
		if err != nil {
			return err
		}
		if !containsPrincipal(users, *iss.AssignedToID) {
			errs.Add("assigned_to_id", "invalid", nil)
		}
	}
	// 親チケット
	if iss.invalidParentIssueID != "" {
		errs.Add("parent_issue_id", "invalid", nil)
	} else if iss.parentIssueSet && iss.parentIssue != nil {
		pi := iss.parentIssue
		ok, err := e.ValidParentProject(ctx, iss, pi)
		if err != nil {
			return err
		}
		sameAsParent := iss.ParentID != nil && *iss.ParentID == pi.ID
		if !ok {
			errs.Add("parent_issue_id", "invalid", nil)
			return nil
		}
		if !sameAsParent {
			bad, err := e.WouldReschedule(ctx, iss, pi.ID)
			if err != nil {
				return err
			}
			if !bad {
				ids, err := e.ancestorIDs(ctx, pi)
				if err != nil {
					return err
				}
				for _, a := range append(ids, pi.ID) {
					rs, err := e.relationsFrom(ctx, a)
					if err != nil {
						return err
					}
					for _, r := range rs {
						if r.RelationType != domain.RelationPrecedes {
							continue
						}
						to, err := e.Find(ctx, r.IssueToID)
						if err != nil {
							return err
						}
						if to == nil || iss.ID == 0 {
							continue
						}
						if w, err := e.WouldReschedule(ctx, to, iss.ID); err != nil {
							return err
						} else if w {
							bad = true
						}
					}
				}
			}
			if bad {
				errs.Add("parent_issue_id", "invalid", nil)
				return nil
			}
		}
		closed, err := e.Closed(ctx, iss)
		if err != nil {
			return err
		}
		pclosed, err := e.Closed(ctx, pi)
		if err != nil {
			return err
		}
		if !closed && pclosed {
			errs.Add("base", "open_issue_with_closed_parent", nil)
		} else if !iss.NewRecord() {
			// move_possible?: 自身または子孫は親にできない
			anc, err := e.IsOrIsAncestorOf(ctx, iss, pi)
			if err != nil {
				return err
			}
			if anc {
				errs.Add("parent_issue_id", "invalid", nil)
			}
		}
	}
	return nil
}

// validateRequiredFields は validate_required_fields (ワークフローで必須の項目)。
func (e *Env) validateRequiredFields(ctx context.Context, iss *Issue) error {
	u, err := e.validationUser(ctx, iss)
	if err != nil {
		return err
	}
	names, err := e.RequiredAttributeNames(ctx, iss, u)
	if err != nil {
		return err
	}
	dis, err := e.DisabledCoreFields(ctx, iss)
	if err != nil {
		return err
	}
	cfv, err := e.CustomFieldValues(ctx, iss)
	if err != nil {
		return err
	}
	// Redmine はハッシュの順 (ワークフロー規則の順) だが、ここではソート済みの名前順
	for _, a := range names {
		if digitsRe.MatchString(a) {
			id, _ := strconv.ParseInt(a, 10, 64)
			for _, v := range cfv {
				if v.Field.ID == id && !v.Value.Present() {
					iss.Errors.Add(v.Field.Name, "blank", nil)
				}
			}
			continue
		}
		blank, known, err := e.attributeBlank(ctx, iss, a)
		if err != nil {
			return err
		}
		if !known || !blank || slices.Contains(dis, a) {
			continue
		}
		if a == "category_id" {
			ids, err := e.ProjectCategoryIDs(ctx, iss.ProjectID)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				continue
			}
		}
		if a == "fixed_version_id" {
			av, err := e.AssignableVersions(ctx, iss)
			if err != nil {
				return err
			}
			if len(av) == 0 {
				continue
			}
		}
		iss.Errors.Add(a, "blank", nil)
	}
	return nil
}

var digitsRe = regexp.MustCompile(`^\d+$`)

// attributeBlank は send(attribute).blank? (known = 属性が存在する)。
func (e *Env) attributeBlank(ctx context.Context, iss *Issue, a string) (blank, known bool, err error) {
	switch a {
	case "project_id":
		return iss.ProjectID == 0, true, nil
	case "tracker_id":
		return iss.TrackerID == 0, true, nil
	case "subject":
		return strings.TrimSpace(iss.Subject) == "", true, nil
	case "is_private":
		// false.blank? は true
		return !iss.IsPrivate, true, nil
	case "assigned_to_id":
		return iss.AssignedToID == nil, true, nil
	case "category_id":
		return iss.CategoryID == nil, true, nil
	case "fixed_version_id":
		return iss.FixedVersionID == nil, true, nil
	case "parent_issue_id":
		return iss.ParentIssueID() == "", true, nil
	case "start_date":
		return iss.StartDate == nil, true, nil
	case "due_date":
		return iss.DueDate == nil, true, nil
	case "estimated_hours":
		return iss.EstimatedHours == nil, true, nil
	case "done_ratio":
		return false, true, nil
	case "description":
		return iss.Description == nil || strings.TrimSpace(*iss.Description) == "", true, nil
	case "priority_id":
		return iss.PriorityID == 0, true, nil
	}
	return false, false, nil
}

// validationUser は検証に使うユーザ (新規は作成者、既存は current_journal のユーザ。無ければ User.current)。
func (e *Env) validationUser(ctx context.Context, iss *Issue) (*domain.User, error) {
	var id int64
	if iss.NewRecord() {
		id = iss.AuthorID
	} else if iss.currentJournal != nil {
		id = iss.currentJournal.UserID
	}
	u, err := e.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return e.currentUser(ctx)
	}
	return u, nil
}

// validatePermissions は validate_permissions (コピー時、許可されていないトラッカーは不可)。
func (e *Env) validatePermissions(ctx context.Context, iss *Issue) error {
	if iss.attributesSetBy == nil || !iss.NewRecord() || !iss.IsCopy() {
		return nil
	}
	ts, err := e.AllowedTargetTrackers(ctx, iss, iss.attributesSetBy)
	if err != nil {
		return err
	}
	if !containsTracker(ts, iss.TrackerID) {
		iss.Errors.Add("tracker", "invalid", nil)
	}
	return nil
}

// validateCustomFieldValues は validate_custom_field_values (編集可能なカスタムフィールドのみ検証)。
func (e *Env) validateCustomFieldValues(ctx context.Context, iss *Issue) error {
	if !iss.NewRecord() && !iss.cfvChanged {
		return nil
	}
	u, err := e.validationUser(ctx, iss)
	if err != nil {
		return err
	}
	ed, err := e.EditableCustomFieldValues(ctx, iss, u)
	if err != nil {
		return err
	}
	for _, v := range ed {
		msgs, err := e.ValidateCustomValue(ctx, iss, v)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			iss.Errors.List = append(iss.Errors.List, domain.ValidationError{Attr: v.Field.Name, Key: m.Key, Vars: m.Vars})
		}
	}
	return nil
}

// CFError はカスタムフィールド値の検証エラー (activerecord.errors.messages.<Key>)。
type CFError struct {
	Key  string
	Vars map[string]any
}

// ValidateCustomValue は CustomField#validate_custom_value(custom_field_value)。
func (e *Env) ValidateCustomValue(ctx context.Context, iss *Issue, v *CustomFieldValue) ([]CFError, error) {
	cf := v.Field
	var errs []CFError
	add := func(key string, vars map[string]any) {
		for _, x := range errs {
			if x.Key == key {
				return
			}
		}
		errs = append(errs, CFError{Key: key, Vars: vars})
	}
	var values []string
	for _, s := range v.Value.Strings() {
		if s != "" {
			values = append(values, s)
		}
	}
	switch cf.FieldFormat {
	case "list":
		was := v.ValueWas.Strings()
		for _, s := range values {
			if !slices.Contains(was, s) && !slices.Contains(cf.PossibleValues, s) {
				add("inclusion", nil)
				break
			}
		}
	case "user", "version", "enumeration":
		opts, err := e.possibleCustomValueOptions(ctx, iss, v)
		if err != nil {
			return nil, err
		}
		for _, s := range values {
			if !slices.Contains(opts, s) {
				add("inclusion", nil)
				break
			}
		}
	case "bool", "attachment":
	default:
		for _, s := range values {
			for _, x := range validateSingleValue(cf, s) {
				add(x.Key, x.Vars)
			}
		}
	}
	if len(errs) == 0 {
		if v.Value.isArray {
			if !cf.Multiple {
				add("invalid", nil)
			}
			if cf.IsRequired && !v.Value.Present() {
				add("blank", nil)
			}
		} else if cf.IsRequired && v.Value.Blank() {
			add("blank", nil)
		}
	}
	return errs, nil
}

var (
	intValueRe      = regexp.MustCompile(`^[+-]?\d+$`)
	progressValueRe = regexp.MustCompile(`^\d*$`)
	cfDateRe        = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// cfRegexpTimeout はカスタムフィールドの正規表現 1 回の照合の上限時間。
const cfRegexpTimeout = time.Second

// validateSingleValue は validate_single_value (Unbounded / Int / Float / Date / Progressbar)。
func validateSingleValue(cf *customfield.CustomField, value string) []CFError {
	var errs []CFError
	if cf.FieldFormat == "date" {
		if cfDateRe.MatchString(value) {
			if _, err := time.Parse("2006-01-02", value); err == nil {
				return nil
			}
		}
		return []CFError{{Key: "not_a_date"}}
	}
	if cf.Regexp != nil && *cf.Regexp != "" {
		if re, err := regexp2.Compile(*cf.Regexp, regexp2.None); err == nil {
			// 管理者が設定した正規表現（(a+)+$ 等）で利用者の値の照合が指数時間にならないよう上限を設ける。
			// 時間切れ（err != nil）は不一致として扱う。
			re.MatchTimeout = cfRegexpTimeout
			if ok, _ := re.MatchString(value); !ok {
				errs = append(errs, CFError{Key: "invalid"})
			}
		}
	}
	n := utf8.RuneCountInString(value)
	if cf.MinLength != nil && n < *cf.MinLength {
		errs = append(errs, CFError{Key: "too_short", Vars: map[string]any{"count": *cf.MinLength}})
	}
	if cf.MaxLength != nil && *cf.MaxLength > 0 && n > *cf.MaxLength {
		errs = append(errs, CFError{Key: "too_long", Vars: map[string]any{"count": *cf.MaxLength}})
	}
	switch cf.FieldFormat {
	case "int":
		if !intValueRe.MatchString(strings.TrimSpace(value)) {
			errs = append(errs, CFError{Key: "not_a_number"})
		}
	case "float":
		if _, ok := kernelFloat(value); !ok {
			errs = append(errs, CFError{Key: "invalid"})
		}
	case "progressbar":
		if !progressValueRe.MatchString(strings.TrimSpace(value)) {
			errs = append(errs, CFError{Key: "not_a_number"})
		}
		if x := rubyToI(value); x < 0 || x > 100 {
			errs = append(errs, CFError{Key: "invalid"})
		}
	}
	return errs
}

// possibleCustomValueOptions は RecordList#possible_custom_value_options の値 (id 文字列) の一覧。
func (e *Env) possibleCustomValueOptions(ctx context.Context, iss *Issue, v *CustomFieldValue) ([]string, error) {
	cf := v.Field
	var ids []int64
	switch cf.FieldFormat {
	case "enumeration":
		if err := e.Q.Select(ctx, &ids, `SELECT id FROM custom_field_enumerations WHERE custom_field_id = ? AND active = ? ORDER BY position`, cf.ID, true); err != nil {
			return nil, err
		}
	case "user":
		if iss.ProjectID != 0 {
			q := `SELECT DISTINCT m.principal_id FROM members m JOIN principals p ON p.id = m.principal_id
WHERE m.project_id = ? AND p.kind = 'user' AND p.status = 1`
			args := []any{iss.ProjectID}
			var roles []int64
			for _, s := range cf.SettingList("user_role") {
				if s != "" {
					roles = append(roles, rubyToI(s))
				}
			}
			if len(roles) > 0 {
				q += ` AND m.id IN (SELECT DISTINCT member_id FROM member_roles WHERE role_id IN (` + inIDs(roles) + `))`
			}
			if err := e.Q.Select(ctx, &ids, q, args...); err != nil {
				return nil, err
			}
		}
	case "version":
		p, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return nil, err
		}
		if p != nil {
			vs, err := e.SharedVersions(ctx, p, "")
			if err != nil {
				return nil, err
			}
			var statuses []string
			for _, s := range cf.SettingList("version_status") {
				if s != "" {
					statuses = append(statuses, s)
				}
			}
			for _, x := range vs {
				if len(statuses) == 0 || slices.Contains(statuses, x.Status) {
					ids = append(ids, x.ID)
				}
			}
		}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	// value_was に含まれる (現在は選択肢に無い) 既存レコード
	for _, s := range v.ValueWas.Strings() {
		if s == "" || slices.Contains(out, s) {
			continue
		}
		var n int
		table := map[string]string{"enumeration": "custom_field_enumerations", "user": "principals", "version": "versions"}[cf.FieldFormat]
		if err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM `+table+` WHERE id = ?`, rubyToI(s)); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, strconv.FormatInt(rubyToI(s), 10))
		}
	}
	return out, nil
}
