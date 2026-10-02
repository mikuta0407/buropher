package domain

import (
	"slices"
	"strconv"
)

// カスタムフィールドの対象（custom_fields.owner_kind。Redmine の STI type に対応）。
const (
	CFOwnerIssue             = "issue"
	CFOwnerTimeEntry         = "time_entry"
	CFOwnerProject           = "project"
	CFOwnerVersion           = "version"
	CFOwnerDocument          = "document"
	CFOwnerUser              = "user"
	CFOwnerGroup             = "group"
	CFOwnerTimeEntryActivity = "time_entry_activity"
	CFOwnerIssuePriority     = "issue_priority"
	CFOwnerDocumentCategory  = "document_category"
)

// CustomFieldType は owner_kind と Redmine のクラス名・関連情報の対応。
type CustomFieldType struct {
	// OwnerKind は custom_fields.owner_kind。
	OwnerKind string
	// ClassName は Redmine のクラス名（IssueCustomField など。params[:type] / タブ名）。
	ClassName string
	// CustomizedClass は CustomField.customized_class の名前（Issue, TimeEntry, ... 。API の customized_type）。
	CustomizedClass string
	// CustomizedKind は custom_values.customized_kind。
	CustomizedKind string
	// TypeName は CustomField#type_name の翻訳キー。
	TypeName string
	// TabLabel は CustomFieldsHelper::CUSTOM_FIELDS_TABS の :label（翻訳キー）。
	TabLabel string
}

// CustomFieldTypes は CustomFieldsHelper::CUSTOM_FIELDS_TABS の順（タブ・種類選択の順序）。
var CustomFieldTypes = []CustomFieldType{
	{CFOwnerIssue, "IssueCustomField", "Issue", "issue", "label_issue_plural", "label_issue_plural"},
	{CFOwnerTimeEntry, "TimeEntryCustomField", "TimeEntry", "time_entry", "label_spent_time", "label_spent_time"},
	{CFOwnerProject, "ProjectCustomField", "Project", "project", "label_project_plural", "label_project_plural"},
	{CFOwnerVersion, "VersionCustomField", "Version", "version", "label_version_plural", "label_version_plural"},
	{CFOwnerDocument, "DocumentCustomField", "Document", "document", "label_document_plural", "label_document_plural"},
	{CFOwnerUser, "UserCustomField", "User", "principal", "label_user_plural", "label_user_plural"},
	{CFOwnerGroup, "GroupCustomField", "Group", "principal", "label_group_plural", "label_group_plural"},
	{CFOwnerTimeEntryActivity, "TimeEntryActivityCustomField", "TimeEntryActivity", "enumeration", "enumeration_activities", "enumeration_activities"},
	{CFOwnerIssuePriority, "IssuePriorityCustomField", "IssuePriority", "enumeration", "enumeration_issue_priorities", "enumeration_issue_priorities"},
	{CFOwnerDocumentCategory, "DocumentCategoryCustomField", "DocumentCategory", "enumeration", "enumeration_doc_categories", "enumeration_doc_categories"},
}

// CustomFieldTypeByClass はクラス名（IssueCustomField 等）から種類を返す（無ければ nil。CustomField.new_subclass_instance）。
func CustomFieldTypeByClass(class string) *CustomFieldType {
	for i := range CustomFieldTypes {
		if CustomFieldTypes[i].ClassName == class {
			return &CustomFieldTypes[i]
		}
	}
	return nil
}

// CustomFieldTypeByOwner は owner_kind から種類を返す（無ければ nil）。
func CustomFieldTypeByOwner(owner string) *CustomFieldType {
	for i := range CustomFieldTypes {
		if CustomFieldTypes[i].OwnerKind == owner {
			return &CustomFieldTypes[i]
		}
	}
	return nil
}

// CustomField は custom_fields 行（Redmine CustomField とその STI サブクラス）。
//
// Redmine で nil と空文字列が区別される列（description, regexp, default_value, min_length, max_length）は
// ポインタで持つ（フォームの value 属性の有無や API の null に影響するため）。
type CustomField struct {
	ID           int64
	OwnerKind    string
	Name         string
	Description  *string
	FieldFormat  string
	Regexp       *string
	MinLength    *int
	MaxLength    *int
	IsRequired   bool
	IsForAll     bool
	IsFilter     bool
	Searchable   bool
	DefaultValue *string
	Editable     bool
	Visible      bool
	Multiple     bool
	Position     int
	// PossibleValues は list 形式の選択肢（CustomField#possible_values。未設定なら空）。
	PossibleValues []string
	// FormatSettings は format_store（url_pattern, text_formatting, edit_tag_style, user_role, version_status,
	// extensions_allowed, full_width_layout, thousands_delimiter, ratio_interval）。キーが無ければ nil。
	FormatSettings map[string]any

	// RoleIDs / TrackerIDs / ProjectIDs は custom_fields_roles / _trackers / _projects（リポジトリが読み込む）。
	RoleIDs    []int64
	TrackerIDs []int64
	ProjectIDs []int64
}

// Type は owner_kind に対応する種類（不明なら nil）。
func (cf *CustomField) Type() *CustomFieldType { return CustomFieldTypeByOwner(cf.OwnerKind) }

// ClassName は cf.class.name（IssueCustomField 等）。
func (cf *CustomField) ClassName() string {
	if t := cf.Type(); t != nil {
		return t.ClassName
	}
	return "CustomField"
}

// IsNewRecord は new_record?。
func (cf *CustomField) IsNewRecord() bool { return cf.ID == 0 }

// Setting は format_store[key]（無ければ nil）。
func (cf *CustomField) Setting(key string) any {
	if cf.FormatSettings == nil {
		return nil
	}
	return cf.FormatSettings[key]
}

// SetSetting は format_store[key] = v。
func (cf *CustomField) SetSetting(key string, v any) {
	if cf.FormatSettings == nil {
		cf.FormatSettings = map[string]any{}
	}
	cf.FormatSettings[key] = v
}

// SettingString は format_store[key] を文字列で返す（nil は ""）。
func (cf *CustomField) SettingString(key string) string {
	switch v := cf.Setting(key).(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// SettingList は format_store[key] を文字列配列で返す（user_role / version_status。配列でなければ nil, false）。
func (cf *CustomField) SettingList(key string) ([]string, bool) {
	switch v := cf.Setting(key).(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			switch s := x.(type) {
			case string:
				out = append(out, s)
			case float64:
				out = append(out, strconv.FormatFloat(s, 'f', -1, 64))
			}
		}
		return out, true
	}
	return nil, false
}

// URLPattern は url_pattern。
func (cf *CustomField) URLPattern() string { return cf.SettingString("url_pattern") }

// TextFormatting は text_formatting。
func (cf *CustomField) TextFormatting() string { return cf.SettingString("text_formatting") }

// EditTagStyle は edit_tag_style。
func (cf *CustomField) EditTagStyle() string { return cf.SettingString("edit_tag_style") }

// FullTextFormatting は full_text_formatting?。
func (cf *CustomField) FullTextFormatting() bool { return cf.TextFormatting() == "full" }

// FullWidthLayout は full_width_layout?。
func (cf *CustomField) FullWidthLayout() bool { return cf.SettingString("full_width_layout") == "1" }

// ThousandsDelimiter は thousands_delimiter?。
func (cf *CustomField) ThousandsDelimiter() bool {
	return cf.SettingString("thousands_delimiter") == "1"
}

// DescriptionString は description（nil は ""）。
func (cf *CustomField) DescriptionString() string { return strOrEmpty(cf.Description) }

// RegexpString は regexp（nil は ""）。
func (cf *CustomField) RegexpString() string { return strOrEmpty(cf.Regexp) }

// DefaultValueString は default_value（nil は ""）。
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

// CustomFieldEnumeration は custom_field_enumerations 行（key/value リストの選択肢）。
type CustomFieldEnumeration struct {
	ID            int64
	CustomFieldID int64
	Name          string
	Active        bool
	Position      int
}

// String は to_s。
func (e *CustomFieldEnumeration) String() string { return e.Name }
