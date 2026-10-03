// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// Journal は編集中のジャーナル (Redmine の Journal インスタンス)。
type Journal struct {
	domain.Journal

	persisted bool
	// attrsBefore / cfBefore は start 時点の値 (@attributes_before_change / @custom_values_before_change)。
	attrsBefore map[string]*string
	attrOrder   []string
	cfBefore    map[int64]CFValue
	cfOrder     []int64
	started     bool
	notifyOff   bool
	// newDetails はまだ保存していない詳細。
	newDetails []*domain.JournalDetail
}

// Persisted は保存済みか。
func (j *Journal) Persisted() bool { return j.persisted }

// AllDetails は保存済みと未保存の詳細。
func (j *Journal) AllDetails() []*domain.JournalDetail {
	return append(slices.Clone(j.Details), j.newDetails...)
}

// NotesAndDetailsEmpty は notes_and_details_empty?。
func (j *Journal) NotesAndDetailsEmpty() bool {
	return strings.TrimSpace(j.Notes) == "" && len(j.Details) == 0 && len(j.newDetails) == 0
}

// SetNotify は notify=。
func (j *Journal) SetNotify(v bool) { j.notifyOff = !v }

// InitJournal は init_journal(user, notes): 既に開始していればそれを返す。
func (e *Env) InitJournal(ctx context.Context, iss *Issue, u *domain.User, notes string) (*Journal, error) {
	if iss.currentJournal != nil {
		return iss.currentJournal, nil
	}
	j := &Journal{}
	j.IssueID = iss.ID
	if u != nil {
		j.UserID = u.ID
	}
	j.Notes = notes
	if iss.NewRecord() {
		j.notifyOff = true
	} else if err := e.startJournal(ctx, j, iss); err != nil {
		return nil, err
	}
	iss.currentJournal = j
	return j, nil
}

// startJournal は Journal#start (属性とカスタムフィールド値のスナップショット)。
func (e *Env) startJournal(ctx context.Context, j *Journal, iss *Issue) error {
	names, err := e.JournalizedAttributeNames(ctx, iss)
	if err != nil {
		return err
	}
	j.attrsBefore = map[string]*string{}
	j.attrOrder = nil
	for _, a := range names {
		v, err := e.journalValue(ctx, iss, a)
		if err != nil {
			return err
		}
		j.attrsBefore[a] = v
		j.attrOrder = append(j.attrOrder, a)
	}
	cfv, err := e.CustomFieldValues(ctx, iss)
	if err != nil {
		return err
	}
	j.cfBefore = map[int64]CFValue{}
	j.cfOrder = nil
	for _, c := range cfv {
		j.cfBefore[c.Field.ID] = c.Value.clone()
		j.cfOrder = append(j.cfOrder, c.Field.ID)
	}
	j.started = true
	return nil
}

func idStr(id int64) *string {
	if id == 0 {
		return nil
	}
	s := strconv.FormatInt(id, 10)
	return &s
}

func idPtrStr(id *int64) *string {
	if id == nil {
		return nil
	}
	s := strconv.FormatInt(*id, 10)
	return &s
}

func dateStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// journalValue は journalized.send(attr) を JournalDetail の値 (normalize + to_s) に変換したもの。
func (e *Env) journalValue(ctx context.Context, iss *Issue, attr string) (*string, error) {
	switch attr {
	case "tracker_id":
		return idStr(iss.TrackerID), nil
	case "project_id":
		return idStr(iss.ProjectID), nil
	case "subject":
		return ptrString(iss.Subject), nil
	case "description":
		if iss.Description == nil {
			return nil, nil
		}
		return ptrString(*iss.Description), nil
	case "due_date":
		return dateStr(iss.DueDate), nil
	case "category_id":
		return idPtrStr(iss.CategoryID), nil
	case "status_id":
		return idStr(iss.StatusID), nil
	case "assigned_to_id":
		return idPtrStr(iss.AssignedToID), nil
	case "priority_id":
		return idStr(iss.PriorityID), nil
	case "fixed_version_id":
		return idPtrStr(iss.FixedVersionID), nil
	case "author_id":
		return idStr(iss.AuthorID), nil
	case "start_date":
		return dateStr(iss.StartDate), nil
	case "done_ratio":
		dr, err := e.DoneRatio(ctx, iss)
		if err != nil {
			return nil, err
		}
		return ptrString(strconv.Itoa(dr)), nil
	case "estimated_hours":
		if iss.EstimatedHours == nil {
			return nil, nil
		}
		return ptrString(RubyFloatToS(*iss.EstimatedHours)), nil
	case "parent_id":
		return idPtrStr(iss.ParentID), nil
	case "is_private":
		if iss.IsPrivate {
			return ptrString("1"), nil
		}
		return ptrString("0"), nil
	}
	return nil, nil
}

func blankStr(s *string) bool { return s == nil || strings.TrimSpace(*s) == "" }

// journalizeChanges は Journal#journalize_changes (属性とカスタムフィールドの変更を詳細に追加し、スナップショットを取り直す)。
func (e *Env) journalizeChanges(ctx context.Context, j *Journal, iss *Issue) error {
	if j.started {
		names, err := e.JournalizedAttributeNames(ctx, iss)
		if err != nil {
			return err
		}
		attrs := slices.Clone(names)
		for _, a := range j.attrOrder {
			if !slices.Contains(attrs, a) {
				attrs = append(attrs, a)
			}
		}
		for _, a := range attrs {
			before := j.attrsBefore[a]
			after, err := e.journalValue(ctx, iss, a)
			if err != nil {
				return err
			}
			if eqPtr(before, after) || (blankStr(before) && blankStr(after)) {
				continue
			}
			j.addDetail("attr", a, before, after)
		}
		cfv, err := e.CustomFieldValues(ctx, iss)
		if err != nil {
			return err
		}
		order := slices.Clone(j.cfOrder)
		after := map[int64]CFValue{}
		for _, c := range cfv {
			if _, ok := j.cfBefore[c.Field.ID]; !ok && !slices.Contains(order, c.Field.ID) {
				order = append(order, c.Field.ID)
			}
			after[c.Field.ID] = c.Value
		}
		for _, id := range order {
			b := j.cfBefore[id]
			a := after[id]
			if b.Equal(a) || (b.Blank() && a.Blank()) {
				continue
			}
			key := strconv.FormatInt(id, 10)
			if b.isArray || a.isArray {
				bs, as := b.Strings(), a.Strings()
				for _, v := range bs {
					if !slices.Contains(as, v) && strings.TrimSpace(v) != "" {
						j.addDetail("cf", key, ptrString(v), nil)
					}
				}
				for _, v := range as {
					if !slices.Contains(bs, v) && strings.TrimSpace(v) != "" {
						j.addDetail("cf", key, nil, ptrString(v))
					}
				}
			} else {
				j.addDetail("cf", key, b.s, a.s)
			}
		}
	}
	return e.startJournal(ctx, j, iss)
}

// addDetail は add_detail(property, prop_key, old_value, value)。
func (j *Journal) addDetail(property, key string, old, value *string) {
	j.newDetails = append(j.newDetails, &domain.JournalDetail{Property: property, PropKey: key, OldValue: old, Value: value})
}

// AddAttributeDetail は add_attribute_detail (create_parent_issue_journal の child_id 等)。
func (j *Journal) AddAttributeDetail(attr string, old, value *string) {
	j.addDetail("attr", attr, old, value)
}

// JournalizeAttachment は journalize_attachment(attachment, :added / :removed)。
func (j *Journal) JournalizeAttachment(attachmentID int64, filename string, added bool) {
	key := strconv.FormatInt(attachmentID, 10)
	if added {
		j.addDetail("attachment", key, nil, ptrString(filename))
	} else {
		j.addDetail("attachment", key, ptrString(filename), nil)
	}
}

// JournalizeRelation は journalize_relation(relation, :added / :removed)。
func (j *Journal) JournalizeRelation(r *domain.IssueRelation, issueID int64, added bool) {
	other := strconv.FormatInt(r.OtherIssueID(issueID), 10)
	if added {
		j.addDetail("relation", r.RelationTypeFor(issueID), nil, &other)
	} else {
		j.addDetail("relation", r.RelationTypeFor(issueID), &other, nil)
	}
}

// saveJournal は Journal#save: 変更を詳細にして、ノートも詳細も空でなければ保存する。
// 保存したら true。新規作成されたジャーナル (split で作られたものを含む) を created に追加する。
func (e *Env) saveJournal(ctx context.Context, j *Journal, iss *Issue, st *saveState) (bool, error) {
	if err := e.journalizeChanges(ctx, j, iss); err != nil {
		return false, err
	}
	if j.NotesAndDetailsEmpty() {
		return false, nil
	}
	j.IssueID = iss.ID
	if !j.persisted {
		// before_create: split_private_notes, add_watcher
		if j.PrivateNotes {
			if strings.TrimSpace(j.Notes) != "" {
				if len(j.Details)+len(j.newDetails) > 0 {
					inner := &Journal{}
					inner.IssueID, inner.UserID = iss.ID, j.UserID
					inner.notifyOff = j.notifyOff
					inner.newDetails = j.AllDetails()
					inner.started = false
					if _, err := e.insertJournal(ctx, inner, iss, st); err != nil {
						return false, err
					}
					j.Details, j.newDetails = nil, nil
					j.CreatedAt = inner.CreatedAt
				}
			} else {
				j.PrivateNotes = false
			}
		}
		return e.insertJournal(ctx, j, iss, st)
	}
	// 既存ジャーナルの再保存 (新しい詳細のみ追加)
	if err := e.insertDetails(ctx, j); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Env) insertJournal(ctx context.Context, j *Journal, iss *Issue, st *saveState) (bool, error) {
	if err := e.journalAddWatcher(ctx, j, iss); err != nil {
		return false, err
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = e.now()
	}
	notes := journalNotesArg(&j.Journal)
	// Rails のタイムスタンプは作成時に updated_on も created_on と同じ値にする (編集表示は両者の比較で判定)
	if j.UpdatedAt == nil {
		t := j.CreatedAt
		j.UpdatedAt = &t
	}
	id, err := e.Q.InsertReturningID(ctx, `INSERT INTO issue_journals (issue_id, user_id, notes, private_notes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		iss.ID, j.UserID, notes, j.PrivateNotes, db.NewTime(j.CreatedAt), db.NewTime(*j.UpdatedAt))
	if err != nil {
		return false, err
	}
	j.ID = id
	j.persisted = true
	if err := e.insertDetails(ctx, j); err != nil {
		return false, err
	}
	if st != nil {
		st.records = append(st.records, &commitRecord{journal: j, journalIssue: iss})
	}
	return true, nil
}

func (e *Env) insertDetails(ctx context.Context, j *Journal) error {
	for _, d := range j.newDetails {
		var cfID any
		if d.Property == "cf" {
			if n, err := strconv.ParseInt(d.PropKey, 10, 64); err == nil {
				cfID = n
			}
		}
		id, err := e.Q.InsertReturningID(ctx, `INSERT INTO issue_journal_details (journal_id, property, prop_key, custom_field_id, old_value, value)
VALUES (?, ?, ?, ?, ?, ?)`, j.ID, d.Property, d.PropKey, cfID, d.OldValue, d.Value)
		if err != nil {
			return err
		}
		d.ID, d.JournalID = id, j.ID
		j.Details = append(j.Details, d)
	}
	j.newDetails = nil
	return nil
}

// journalAddWatcher は Journal#add_watcher (before_create): 作成者を自動ウォッチャーにする。
func (e *Env) journalAddWatcher(ctx context.Context, j *Journal, iss *Issue) error {
	u, err := e.UserByID(ctx, j.UserID)
	if err != nil || u == nil || !u.Active() {
		return err
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return err
	}
	ok, err := e.allowedTo(ctx, u, "add_issue_watchers", p)
	if err != nil || !ok {
		return err
	}
	aw, err := e.autoWatchOn(ctx, u.ID)
	if err != nil || !slices.Contains(aw, "issue_contributed_to") {
		return err
	}
	watched, err := e.directlyWatchedBy(ctx, iss.ID, u.ID)
	if err != nil || watched {
		return err
	}
	return e.AddWatcher(ctx, iss, u.ID)
}

// autoWatchOn は user.pref.auto_watch_on (設定行が無ければ Setting.default_users_auto_watch_on)。
func (e *Env) autoWatchOn(ctx context.Context, userID int64) ([]string, error) {
	var raw []string
	if err := e.Q.Select(ctx, &raw, `SELECT auto_watch_on FROM user_preferences WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		if e.Settings == nil {
			return []string{"issue_created", "issue_contributed_to"}, nil
		}
		return e.Settings.Strings("default_users_auto_watch_on"), nil
	}
	var out []string
	_ = json.Unmarshal([]byte(raw[0]), &out)
	return out, nil
}

// ---------------------------------------------------------------- 読み込み・表示

// journalNotesArg は notes の保存値（NotesNull で空なら NULL、それ以外は "" もそのまま。D-17）。
func journalNotesArg(j *domain.Journal) any {
	if j.NotesNull && j.Notes == "" {
		return nil
	}
	return j.Notes
}

type journalRow struct {
	ID           int64          `db:"id"`
	IssueID      int64          `db:"issue_id"`
	UserID       int64          `db:"user_id"`
	Notes        sql.NullString `db:"notes"`
	PrivateNotes bool           `db:"private_notes"`
	CreatedAt    db.Time        `db:"created_at"`
	UpdatedAt    db.NullTime    `db:"updated_at"`
	UpdatedByID  sql.NullInt64  `db:"updated_by_id"`
}

func (r *journalRow) journal() *Journal {
	j := &Journal{persisted: true}
	j.ID, j.IssueID, j.UserID, j.Notes, j.PrivateNotes = r.ID, r.IssueID, r.UserID, r.Notes.String, r.PrivateNotes
	j.NotesNull = !r.Notes.Valid
	j.CreatedAt, j.UpdatedAt, j.UpdatedByID = r.CreatedAt.Time, r.UpdatedAt.Ptr(), nullID(r.UpdatedByID)
	return j
}

// Journals はチケットのジャーナル (詳細付き、id 順)。
func (e *Env) Journals(ctx context.Context, issueID int64) ([]*Journal, error) {
	return e.loadJournals(ctx, `issue_id = ?`, issueID)
}

// FindJournal は id のジャーナル (無ければ nil)。
func (e *Env) FindJournal(ctx context.Context, id int64) (*Journal, error) {
	js, err := e.loadJournals(ctx, `id = ?`, id)
	if err != nil || len(js) == 0 {
		return nil, err
	}
	return js[0], nil
}

func (e *Env) loadJournals(ctx context.Context, where string, args ...any) ([]*Journal, error) {
	var rows []journalRow
	if err := e.Q.Select(ctx, &rows, `SELECT id, issue_id, user_id, notes, private_notes, created_at, updated_at, updated_by_id
FROM issue_journals WHERE `+where+` ORDER BY id`, args...); err != nil {
		return nil, err
	}
	out := make([]*Journal, len(rows))
	ids := make([]int64, len(rows))
	for i := range rows {
		out[i] = rows[i].journal()
		ids[i] = out[i].ID
	}
	details, err := e.journalDetailsMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, j := range out {
		j.Details = details[j.ID]
		if j.Details == nil {
			j.Details = []*domain.JournalDetail{}
		}
	}
	return out, nil
}

// journalDetailsMap は ids の各ジャーナルの details（id 順）をまとめて読み込む。
func (e *Env) journalDetailsMap(ctx context.Context, ids []int64) (map[int64][]*domain.JournalDetail, error) {
	out := make(map[int64][]*domain.JournalDetail, len(ids))
	for len(ids) > 0 {
		n := min(len(ids), 500)
		chunk := ids[:n]
		ids = ids[n:]
		parts := make([]string, len(chunk))
		for i, id := range chunk {
			parts[i] = strconv.FormatInt(id, 10)
		}
		var rows []journalDetailRow
		if err := e.Q.Select(ctx, &rows, `SELECT id, journal_id, property, prop_key, old_value, value FROM issue_journal_details
WHERE journal_id IN (`+strings.Join(parts, ",")+`) ORDER BY id`); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.JournalID] = append(out[r.JournalID], r.detail())
		}
	}
	return out, nil
}

type journalDetailRow struct {
	ID        int64          `db:"id"`
	JournalID int64          `db:"journal_id"`
	Property  string         `db:"property"`
	PropKey   string         `db:"prop_key"`
	OldValue  sql.NullString `db:"old_value"`
	Value     sql.NullString `db:"value"`
}

func (r journalDetailRow) detail() *domain.JournalDetail {
	d := &domain.JournalDetail{ID: r.ID, JournalID: r.JournalID, Property: r.Property, PropKey: r.PropKey}
	if r.OldValue.Valid {
		d.OldValue = ptrString(r.OldValue.String)
	}
	if r.Value.Valid {
		d.Value = ptrString(r.Value.String)
	}
	return d
}

// journalDetails は 1 件のジャーナルの details（id 順）。
func (e *Env) journalDetails(ctx context.Context, journalID int64) ([]*domain.JournalDetail, error) {
	m, err := e.journalDetailsMap(ctx, []int64{journalID})
	if err != nil {
		return nil, err
	}
	if m[journalID] == nil {
		return []*domain.JournalDetail{}, nil
	}
	return m[journalID], nil
}

// LastJournalID は last_journal_id (新規または無ければ 0)。
func (e *Env) LastJournalID(ctx context.Context, iss *Issue) (int64, error) {
	if iss.NewRecord() {
		return 0, nil
	}
	var id sql.NullInt64
	err := e.Q.Get(ctx, &id, `SELECT MAX(id) FROM issue_journals WHERE issue_id = ?`, iss.ID)
	return id.Int64, err
}

// JournalsAfter は journals_after(journal_id) (id 昇順)。
func (e *Env) JournalsAfter(ctx context.Context, iss *Issue, journalID int64) ([]*Journal, error) {
	return e.loadJournals(ctx, `issue_id = ? AND id > ?`, iss.ID, journalID)
}

// VisibleJournalsWithIndex は visible_journals_with_index(user): created_on, id 順に通し番号を振り、
// 見えない非公開ノートと、ノートも見える詳細も無いジャーナルを除く。
func (e *Env) VisibleJournalsWithIndex(ctx context.Context, iss *Issue, u *domain.User) ([]*Journal, error) {
	var rows []journalRow
	if err := e.Q.Select(ctx, &rows, `SELECT id, issue_id, user_id, notes, private_notes, created_at, updated_at, updated_by_id
FROM issue_journals WHERE issue_id = ? ORDER BY created_at, id`, iss.ID); err != nil {
		return nil, err
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	viewPrivate, err := e.allowedTo(ctx, u, "view_private_notes", p)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	details, err := e.journalDetailsMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	var out []*Journal
	for i := range rows {
		j := rows[i].journal()
		j.Indice = i + 1
		if !viewPrivate && j.PrivateNotes && j.UserID != u.ID {
			continue
		}
		j.Details = details[j.ID]
		if j.Details == nil {
			j.Details = []*domain.JournalDetail{}
		}
		vd, err := e.VisibleDetails(ctx, j, iss, u)
		if err != nil {
			return nil, err
		}
		if j.HasNotes() || len(vd) > 0 {
			out = append(out, j)
		}
	}
	return out, nil
}

// VisibleDetails は visible_details(user) (CF の可視性・関連先チケットの可視性で絞る)。
func (e *Env) VisibleDetails(ctx context.Context, j *Journal, iss *Issue, u *domain.User) ([]*domain.JournalDetail, error) {
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	var out []*domain.JournalDetail
	for _, d := range j.AllDetails() {
		switch d.Property {
		case "cf":
			id, _ := strconv.ParseInt(d.PropKey, 10, 64)
			cf, err := e.CustomField(ctx, id)
			if err != nil {
				return nil, err
			}
			if cf == nil {
				continue
			}
			ok, err := e.cfVisibleBy(ctx, cf, p, u)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		case "relation":
			v := d.Value
			if v == nil {
				v = d.OldValue
			}
			if v == nil {
				continue
			}
			other, err := e.Find(ctx, rubyToI(*v))
			if err != nil {
				return nil, err
			}
			if other == nil {
				continue
			}
			ok, err := e.Visible(ctx, other, u)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// JournalEditableBy は Journal#editable_by?(user)。
func (e *Env) JournalEditableBy(ctx context.Context, j *Journal, u *domain.User) (bool, error) {
	if u == nil || !u.Logged() {
		return false, nil
	}
	iss, err := e.Find(ctx, j.IssueID)
	if err != nil || iss == nil {
		return false, err
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return false, err
	}
	ok, err := e.allowedTo(ctx, u, "edit_issue_notes", p)
	if err != nil || ok {
		return ok, err
	}
	if j.UserID != u.ID {
		return false, nil
	}
	return e.allowedTo(ctx, u, "edit_own_issue_notes", p)
}

// JournalVisibleNotesCondition は Journal.visible_notes_condition(user) (issue_journals / projects を参照する SQL)。
func (e *Env) JournalVisibleNotesCondition(ctx context.Context, u *domain.User) (string, error) {
	cond, err := e.authz(u).AllowedToCondition(ctx, "view_private_notes", authz.ConditionOptions{}, nil)
	if err != nil {
		return "", err
	}
	return "(issue_journals.private_notes = " + e.Q.Dialect().BoolLiteral(false) +
		" OR issue_journals.user_id = " + strconv.FormatInt(u.ID, 10) + " OR (" + cond + "))", nil
}

// ErrJournalNotEditable は編集権限の無いジャーナルを更新しようとしたときのエラー。
var ErrJournalNotEditable = errors.New("issues: journal is not editable")

// UpdateJournalNotes はジャーナルのノートを編集する (JournalsController#update の safe_attributes と保存)。
// privateNotes が nil なら変更しない (set_notes_private 権限が無い場合も変更しない)。
// ノートが空になり詳細も無ければジャーナルを削除する (Journal の after_update ... destroy 相当)。
func (e *Env) UpdateJournalNotes(ctx context.Context, j *Journal, notes string, privateNotes *bool, u *domain.User) error {
	ok, err := e.JournalEditableBy(ctx, j, u)
	if err != nil {
		return err
	}
	if !ok {
		return ErrJournalNotEditable
	}
	iss, err := e.Find(ctx, j.IssueID)
	if err != nil {
		return err
	}
	changed := notes != j.Notes
	if privateNotes != nil {
		p, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return err
		}
		if ok, err := e.allowedTo(ctx, u, "set_notes_private", p); err != nil {
			return err
		} else if ok && j.PrivateNotes != *privateNotes {
			j.PrivateNotes = *privateNotes
			changed = true
		}
	}
	if j.UpdatedByID == nil || *j.UpdatedByID != u.ID {
		j.UpdatedByID = ptrInt64(u.ID)
		changed = true
	}
	j.Notes, j.NotesNull = notes, false
	return e.inTx(ctx, func() error {
		if strings.TrimSpace(j.Notes) == "" && len(j.Details) == 0 {
			// save は空のジャーナルを保存せず、コントローラが destroy する
			_, err := e.Q.Exec(ctx, `DELETE FROM issue_journals WHERE id = ?`, j.ID)
			return err
		}
		if !changed {
			return nil
		}
		now := e.now()
		j.UpdatedAt = &now
		nv := journalNotesArg(&j.Journal)
		var ua any
		if j.UpdatedAt != nil {
			ua = db.NewTime(*j.UpdatedAt)
		}
		_, err := e.Q.Exec(ctx, `UPDATE issue_journals SET notes = ?, private_notes = ?, updated_at = ?, updated_by_id = ? WHERE id = ?`,
			nv, j.PrivateNotes, ua, j.UpdatedByID, j.ID)
		return err
	})
}
