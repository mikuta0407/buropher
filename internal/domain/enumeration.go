package domain

// 列挙の種類（Redmine の Enumeration の STI サブクラス名）。
// buropher は種類ごとに別テーブル（issue_priorities / time_entry_activities / document_categories）に持つが、
// id は種類をまたいで一意（Redmine の enumerations.id を保持し、新規作成時も 3 テーブルの最大値 + 1 を使う）。
const (
	EnumDocumentCategory  = "DocumentCategory"
	EnumIssuePriority     = "IssuePriority"
	EnumTimeEntryActivity = "TimeEntryActivity"
)

// EnumerationKinds は Enumeration.get_subclasses（管理画面の表示順）。
var EnumerationKinds = []string{EnumDocumentCategory, EnumIssuePriority, EnumTimeEntryActivity}

// EnumerationKindOf は Enumeration.get_subclass(name)（クラス名か複数形の下線区切り名。不明なら空）。
func EnumerationKindOf(name string) string {
	switch name {
	case EnumDocumentCategory, "document_categories", "document_category", "DocumentCategories":
		return EnumDocumentCategory
	case EnumIssuePriority, "issue_priorities", "issue_priority", "IssuePriorities":
		return EnumIssuePriority
	case EnumTimeEntryActivity, "time_entry_activities", "time_entry_activity", "TimeEntryActivities":
		return EnumTimeEntryActivity
	}
	return ""
}

// EnumerationOptionName は klass::OptionName（種類の表示名の翻訳キー）。
func EnumerationOptionName(kind string) string {
	switch kind {
	case EnumDocumentCategory:
		return "enumeration_doc_categories"
	case EnumIssuePriority:
		return "enumeration_issue_priorities"
	case EnumTimeEntryActivity:
		return "enumeration_activities"
	}
	return ""
}

// EnumerationCustomFieldKind は種類に対応する custom_fields.owner_kind
// （IssuePriorityCustomField 等）。
func EnumerationCustomFieldKind(kind string) string {
	switch kind {
	case EnumDocumentCategory:
		return "document_category"
	case EnumIssuePriority:
		return "issue_priority"
	case EnumTimeEntryActivity:
		return "time_entry_activity"
	}
	return ""
}

// Enumeration は列挙の 1 行（種類は Kind）。
type Enumeration struct {
	ID        int64
	Kind      string
	Name      string
	Position  int
	IsDefault bool
	Active    bool
	// ProjectID / ParentID は TimeEntryActivity のプロジェクト別上書き行のみ非 nil。
	ProjectID *int64
	ParentID  *int64
	// PositionName は IssuePriority の CSS 用名（lowest/default/high2/highest ...）。
	PositionName *string
}

// OptionName は Enumeration#option_name。
func (e *Enumeration) OptionName() string { return EnumerationOptionName(e.Kind) }

// String は to_s。
func (e *Enumeration) String() string { return e.Name }
