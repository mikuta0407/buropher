package issues

// フォームの再表示（IssuesController#new / edit / update の検証エラー時）で使う読み取り用のアクセサ。

// EstimatedHoursBeforeTypeCast は estimated_hours_before_type_cast（解釈できなかった入力値。無ければ ""）。
func (i *Issue) EstimatedHoursBeforeTypeCast() string { return i.estimatedHoursRaw }

// StartDateBeforeTypeCast は start_date_before_type_cast（不正な入力値。無ければ ""）。
func (i *Issue) StartDateBeforeTypeCast() string { return i.startDateRaw }

// DueDateBeforeTypeCast は due_date_before_type_cast（不正な入力値。無ければ ""）。
func (i *Issue) DueDateBeforeTypeCast() string { return i.dueDateRaw }

// DeletedAttachmentIDs は deleted_attachment_ids。
func (i *Issue) DeletedAttachmentIDs() []int64 { return i.deletedAttachmentIDs }

// DetachSavedAttachments は detach_saved_attachments（保存に失敗したときに紐付け予定の添付を外す）。
func (i *Issue) DetachSavedAttachments() { i.attachIDs = nil }

// SavedAttachmentIDs は保存時に紐付ける添付（saved_attachments）。
func (i *Issue) SavedAttachmentIDs() []int64 { return i.attachIDs }

// Notes は notes（current_journal に委譲。ジャーナルが無ければ nil）。
func (i *Issue) Notes() *string {
	if i.currentJournal == nil {
		return nil
	}
	s := i.currentJournal.Notes
	return &s
}

// PrivateNotes は private_notes（current_journal に委譲）。
func (i *Issue) PrivateNotes() bool {
	return i.currentJournal != nil && i.currentJournal.PrivateNotes
}

// TrackerIDWas は tracker_id_in_database（新規なら 0）。
func (i *Issue) TrackerIDWas() int64 {
	if i.orig == nil {
		return 0
	}
	return i.orig.TrackerID
}
