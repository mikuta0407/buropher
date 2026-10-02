package domain

import "slices"

// TrackerCoreFieldsUndisablable は Tracker::CORE_FIELDS_UNDISABLABLE（無効化できない標準フィールド）。
var TrackerCoreFieldsUndisablable = []string{"project_id", "tracker_id", "subject", "is_private"}

// TrackerCoreFields は Tracker::CORE_FIELDS（トラッカーごとに無効化できる標準フィールド。順序は Redmine と同じ）。
var TrackerCoreFields = []string{"assigned_to_id", "category_id", "fixed_version_id", "parent_issue_id",
	"start_date", "due_date", "estimated_hours", "done_ratio", "description", "priority_id"}

// TrackerCoreFieldsAll は Tracker::CORE_FIELDS_ALL。
var TrackerCoreFieldsAll = append(slices.Clone(TrackerCoreFieldsUndisablable), TrackerCoreFields...)

// Tracker は trackers 行（Redmine Tracker）。
type Tracker struct {
	ID              int64
	Name            string
	Description     string
	Position        int
	IsInRoadmap     bool
	DefaultStatusID int64
	// DisabledCoreFields は無効化された標準フィールド名（Tracker#disabled_core_fields。旧 fields_bits）。
	DisabledCoreFields []string
}

// CoreFields は Tracker#core_fields（有効な標準フィールド。CORE_FIELDS の順）。
func (t *Tracker) CoreFields() []string {
	var out []string
	for _, f := range TrackerCoreFields {
		if !slices.Contains(t.DisabledCoreFields, f) {
			out = append(out, f)
		}
	}
	return out
}

// String は Tracker#to_s。
func (t *Tracker) String() string { return t.Name }
