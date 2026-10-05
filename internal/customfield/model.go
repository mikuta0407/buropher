// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

import (
	"slices"
	"strconv"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
)

// Type は owner_kind と Redmine のクラス名・関連情報の対応（CustomFieldsHelper::CUSTOM_FIELDS_TABS）。
type Type struct {
	// Kind は custom_fields.owner_kind。
	Kind OwnerKind
	// ClassName は Redmine のクラス名（IssueCustomField など。params[:type] / タブ名）。
	ClassName string
	// CustomizedClass は CustomField.customized_class の名前（Issue, TimeEntry, ... 。API の customized_type の元）。
	CustomizedClass string
	// TypeName は CustomField#type_name の翻訳キー。
	TypeName string
	// TabLabel は CUSTOM_FIELDS_TABS の :label（翻訳キー）。
	TabLabel string
}

// Types は CustomFieldsHelper::CUSTOM_FIELDS_TABS の順（タブ・種類選択の順序）。
var Types = []Type{
	{KindIssue, "IssueCustomField", "Issue", "label_issue_plural", "label_issue_plural"},
	{KindTimeEntry, "TimeEntryCustomField", "TimeEntry", "label_spent_time", "label_spent_time"},
	{KindProject, "ProjectCustomField", "Project", "label_project_plural", "label_project_plural"},
	{KindVersion, "VersionCustomField", "Version", "label_version_plural", "label_version_plural"},
	{KindDocument, "DocumentCustomField", "Document", "label_document_plural", "label_document_plural"},
	{KindUser, "UserCustomField", "User", "label_user_plural", "label_user_plural"},
	{KindGroup, "GroupCustomField", "Group", "label_group_plural", "label_group_plural"},
	{KindTimeEntryActivity, "TimeEntryActivityCustomField", "TimeEntryActivity", "enumeration_activities", "enumeration_activities"},
	{KindIssuePriority, "IssuePriorityCustomField", "IssuePriority", "enumeration_issue_priorities", "enumeration_issue_priorities"},
	{KindDocumentCategory, "DocumentCategoryCustomField", "DocumentCategory", "enumeration_doc_categories", "enumeration_doc_categories"},
}

// TypeByClass はクラス名（IssueCustomField 等）から種類を返す（無ければ nil。CustomField.new_subclass_instance）。
func TypeByClass(class string) *Type {
	for i := range Types {
		if Types[i].ClassName == class {
			return &Types[i]
		}
	}
	return nil
}

// TypeOf は owner_kind の種類を返す（無ければ nil）。
func TypeOf(kind OwnerKind) *Type {
	for i := range Types {
		if Types[i].Kind == kind {
			return &Types[i]
		}
	}
	return nil
}

// Type は種類（不明なら nil）。
func (cf *CustomField) Type() *Type { return TypeOf(cf.OwnerKind) }

// ClassName は cf.class.name（IssueCustomField 等。不明なら CustomField）。
func (cf *CustomField) ClassName() string {
	if t := cf.Type(); t != nil {
		return t.ClassName
	}
	return "CustomField"
}

// IsNewRecord は new_record?。
func (cf *CustomField) IsNewRecord() bool { return cf.ID == 0 }

// SettingValue は format_store[key] の生の値（無ければ nil。Setting は文字列化した値）。
func (cf *CustomField) SettingValue(key string) any {
	if cf.Settings == nil {
		return nil
	}
	return cf.Settings[key]
}

// SetSetting は format_store[key] = v。
func (cf *CustomField) SetSetting(key string, v any) {
	if cf.Settings == nil {
		cf.Settings = map[string]any{}
	}
	cf.Settings[key] = v
}

// URLPattern は url_pattern。
func (cf *CustomField) URLPattern() string { return cf.Setting("url_pattern") }

// TextFormatting は text_formatting。
func (cf *CustomField) TextFormatting() string { return cf.Setting("text_formatting") }

// EditTagStyle は edit_tag_style。
func (cf *CustomField) EditTagStyle() string { return cf.Setting("edit_tag_style") }

// FullTextFormatting は full_text_formatting?。
func (cf *CustomField) FullTextFormatting() bool { return cf.TextFormatting() == "full" }

// ThousandsDelimiter は thousands_delimiter?。
func (cf *CustomField) ThousandsDelimiter() bool { return cf.Setting("thousands_delimiter") == "1" }

// DescriptionString は description（nil は ""）。
func (cf *CustomField) DescriptionString() string { return strOrEmpty(cf.Description) }

// RegexpString は regexp（nil は ""）。
func (cf *CustomField) RegexpString() string { return strOrEmpty(cf.Regexp) }

// DefaultValueMode は default_value_mode（日付書式の format_store。未設定は ""）。
func (cf *CustomField) DefaultValueMode() string { return cf.Setting("default_value_mode") }

// DefaultValueOn は CustomField#default_value（7.0.1 #44129: 日付書式の date_offset モードでは
// 保存値を today（User.current.today）からの日数として評価した日付）。
func (cf *CustomField) DefaultValueOn(today time.Time) *string {
	return domain.CustomFieldDefaultValue(cf.FieldFormat, cf.DefaultValueMode(), cf.DefaultValue, today)
}

// DefaultValueString は default_value（nil は ""）。保存値のまま（相対既定値は評価しない）。
func (cf *CustomField) DefaultValueString() string { return strOrEmpty(cf.DefaultValue) }

// CSSClasses は css_classes。
func (cf *CustomField) CSSClasses() string {
	return cf.FieldFormat + "_cf cf_" + strconv.FormatInt(cf.ID, 10)
}

// HasRole は role_ids.include?(id)。
func (cf *CustomField) HasRole(id int64) bool { return slices.Contains(cf.RoleIDs, id) }

// HasTracker は tracker_ids.include?(id)。
func (cf *CustomField) HasTracker(id int64) bool { return slices.Contains(cf.TrackerIDs, id) }

// HasProject は project_ids.include?(id)。
func (cf *CustomField) HasProject(id int64) bool { return slices.Contains(cf.ProjectIDs, id) }

// Compare は CustomField#<=>（position 順）。
func (cf *CustomField) Compare(o *CustomField) int { return cf.Position - o.Position }

// String は name。
func (cf *CustomField) String() string { return cf.Name }

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Enumeration は custom_field_enumerations 行（key/value リストの選択肢。CustomFieldEnumeration）。
type Enumeration struct {
	ID            int64
	CustomFieldID int64
	Name          string
	Active        bool
	Position      int
}

// String は to_s。
func (e *Enumeration) String() string { return e.Name }
