// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

import "slices"

// TrackerCoreFieldsAll は Tracker::CORE_FIELDS_ALL（CORE_FIELDS_UNDISABLABLE + CORE_FIELDS）。
var TrackerCoreFieldsAll = append(slices.Clone(TrackerCoreFieldsUndisablable), TrackerCoreFields...)

// WorkflowTransition は workflow_transitions 行（Redmine WorkflowTransition）。
type WorkflowTransition struct {
	ID        int64
	TrackerID int64
	RoleID    int64
	// OldStatusID は遷移元ステータス（0 = 新規チケット。DB では NULL）。
	OldStatusID int64
	NewStatusID int64
	Author      bool
	Assignee    bool
}

// WorkflowRuleReadonly / WorkflowRuleRequired は WorkflowPermission の rule。
const (
	WorkflowRuleReadonly = "readonly"
	WorkflowRuleRequired = "required"
)

// WorkflowFieldRule は workflow_field_rules 行（Redmine WorkflowPermission）。
type WorkflowFieldRule struct {
	ID        int64
	TrackerID int64
	RoleID    int64
	// StatusID は Redmine の old_status_id。
	StatusID int64
	// FieldName は標準フィールド名、またはカスタムフィールドの ID の 10 進文字列（Redmine の field_name）。
	FieldName string
	Rule      string
}
