// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

import "slices"

// TrackerCoreFieldsUndisablable は Tracker::CORE_FIELDS_UNDISABLABLE。
var TrackerCoreFieldsUndisablable = []string{"project_id", "tracker_id", "subject", "is_private"}

// TrackerCoreFields は Tracker::CORE_FIELDS（トラッカーごとに無効化できる標準フィールド）。
var TrackerCoreFields = []string{"assigned_to_id", "category_id", "fixed_version_id", "parent_issue_id",
	"start_date", "due_date", "estimated_hours", "done_ratio", "description", "priority_id"}

// Tracker は trackers 行（+ project_trackers / custom_fields_trackers）。
type Tracker struct {
	ID          int64
	Name        string
	Description *string
	Position    int
	IsInRoadmap bool
	// PrivateByDefault は新規チケットの「プライベート」の既定値（Redmine 7.0 #9432）。
	PrivateByDefault bool
	DefaultStatusID  int64
	// DisabledCoreFields は無効化された標準フィールド（Tracker::CORE_FIELDS の順）。
	DisabledCoreFields []string

	// ProjectIDs / CustomFieldIDs は関連（読み込んだ場合のみ）。
	ProjectIDs     []int64
	CustomFieldIDs []int64
}

// CoreFields は Tracker#core_fields（CORE_FIELDS - disabled_core_fields）。
func (t *Tracker) CoreFields() []string {
	var out []string
	for _, f := range TrackerCoreFields {
		if !slices.Contains(t.DisabledCoreFields, f) {
			out = append(out, f)
		}
	}
	return out
}

// SetCoreFields は Tracker#core_fields=（fields に無い CORE_FIELDS を無効にする）。
func (t *Tracker) SetCoreFields(fields []string) {
	t.DisabledCoreFields = []string{}
	for _, f := range TrackerCoreFields {
		if !slices.Contains(fields, f) {
			t.DisabledCoreFields = append(t.DisabledCoreFields, f)
		}
	}
}

// DescriptionString は description（nil なら空文字列）。
func (t *Tracker) DescriptionString() string {
	if t.Description == nil {
		return ""
	}
	return *t.Description
}
