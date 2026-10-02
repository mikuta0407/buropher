package domain

import "time"

// Issue は issues 行 (Redmine Issue の列)。
//
// 必須の参照 (ProjectID / TrackerID / StatusID / PriorityID / AuthorID) は 0 を
// 「未設定 (Ruby の nil)」として扱う (新規作成中のチケットでのみ起こる)。
// 日付列は UTC 0 時の time.Time。
type Issue struct {
	ID             int64
	ProjectID      int64
	TrackerID      int64
	StatusID       int64
	PriorityID     int64
	AuthorID       int64
	AssignedToID   *int64
	CategoryID     *int64
	FixedVersionID *int64
	ParentID       *int64
	// RootID はルートチケットの id (ルート自身は自分の id)。
	RootID int64
	// HierPath はルートから自身までの id を 10 桁ゼロ埋め + '/' で連結した経路。
	HierPath       string
	Subject        string
	Description    *string
	StartDate      *time.Time
	DueDate        *time.Time
	DoneRatio      int
	EstimatedHours *float64
	IsPrivate      bool
	LockVersion    int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClosedAt       *time.Time
}

// IssueCategory は issue_categories 行。
type IssueCategory struct {
	ID           int64
	ProjectID    int64
	Name         string
	AssignedToID *int64
}

// バージョンの状態 (Version::VERSION_STATUSES)。
const (
	VersionStatusOpen   = "open"
	VersionStatusLocked = "locked"
	VersionStatusClosed = "closed"
)

// Version は versions 行。
type Version struct {
	ID            int64
	ProjectID     int64
	Name          string
	Description   *string
	EffectiveDate *time.Time
	WikiPageTitle *string
	Status        string
	Sharing       string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// IsOpen は Version#open?。
func (v *Version) IsOpen() bool { return v.Status == VersionStatusOpen }

// IsClosed は Version#closed?。
func (v *Version) IsClosed() bool { return v.Status == VersionStatusClosed }

// IsLocked は status == 'locked'。
func (v *Version) IsLocked() bool { return v.Status == VersionStatusLocked }

// Shared は Version#shared?。
func (v *Version) Shared() bool { return v.Sharing != "none" }

// CompareVersions は Version#<=> (期日順、期日なしは後ろで名前順)。
func CompareVersions(a, b *Version) int {
	if a.EffectiveDate != nil {
		if b.EffectiveDate != nil {
			if a.EffectiveDate.Equal(*b.EffectiveDate) {
				if a.Name == b.Name {
					return cmpInt64(a.ID, b.ID)
				}
				return cmpString(a.Name, b.Name)
			}
			return a.EffectiveDate.Compare(*b.EffectiveDate)
		}
		return -1
	}
	if b.EffectiveDate != nil {
		return 1
	}
	if a.Name == b.Name {
		return cmpInt64(a.ID, b.ID)
	}
	return cmpString(a.Name, b.Name)
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// チケット関連の種別 (IssueRelation::TYPE_*)。
const (
	RelationRelates    = "relates"
	RelationDuplicates = "duplicates"
	RelationDuplicated = "duplicated"
	RelationBlocks     = "blocks"
	RelationBlocked    = "blocked"
	RelationPrecedes   = "precedes"
	RelationFollows    = "follows"
	RelationCopiedTo   = "copied_to"
	RelationCopiedFrom = "copied_from"
)

// RelationType は IssueRelation::TYPES の 1 エントリ。
type RelationType struct {
	Name    string // ラベルの i18n キー (from 側から見た名前)
	SymName string // ラベルの i18n キー (to 側から見た名前)
	Order   int
	Sym     string // to 側から見た種別
	Reverse string // 保存時に反転する種別 ("" なら反転しない)
}

// RelationTypes は IssueRelation::TYPES。
var RelationTypes = map[string]RelationType{
	RelationRelates:    {Name: "label_relates_to", SymName: "label_relates_to", Order: 1, Sym: RelationRelates},
	RelationDuplicates: {Name: "label_duplicates", SymName: "label_duplicated_by", Order: 2, Sym: RelationDuplicated},
	RelationDuplicated: {Name: "label_duplicated_by", SymName: "label_duplicates", Order: 3, Sym: RelationDuplicates, Reverse: RelationDuplicates},
	RelationBlocks:     {Name: "label_blocks", SymName: "label_blocked_by", Order: 4, Sym: RelationBlocked},
	RelationBlocked:    {Name: "label_blocked_by", SymName: "label_blocks", Order: 5, Sym: RelationBlocks, Reverse: RelationBlocks},
	RelationPrecedes:   {Name: "label_precedes", SymName: "label_follows", Order: 6, Sym: RelationFollows},
	RelationFollows:    {Name: "label_follows", SymName: "label_precedes", Order: 7, Sym: RelationPrecedes, Reverse: RelationPrecedes},
	RelationCopiedTo:   {Name: "label_copied_to", SymName: "label_copied_from", Order: 8, Sym: RelationCopiedFrom},
	RelationCopiedFrom: {Name: "label_copied_from", SymName: "label_copied_to", Order: 9, Sym: RelationCopiedTo, Reverse: RelationCopiedTo},
}

// IssueRelation は issue_relations 行。
type IssueRelation struct {
	ID           int64
	IssueFromID  int64
	IssueToID    int64
	RelationType string
	Delay        *int
}

// RelationTypeFor は IssueRelation#relation_type_for(issue)。
func (r *IssueRelation) RelationTypeFor(issueID int64) string {
	t, ok := RelationTypes[r.RelationType]
	if !ok {
		return ""
	}
	if r.IssueFromID == issueID {
		return r.RelationType
	}
	return t.Sym
}

// OtherIssueID は IssueRelation#other_issue(issue) の id。
func (r *IssueRelation) OtherIssueID(issueID int64) int64 {
	if r.IssueFromID == issueID {
		return r.IssueToID
	}
	return r.IssueFromID
}

// LabelFor は IssueRelation#label_for(issue) (i18n キー)。
func (r *IssueRelation) LabelFor(issueID int64) string {
	t, ok := RelationTypes[r.RelationType]
	if !ok {
		return "unknow"
	}
	if r.IssueFromID == issueID {
		return t.Name
	}
	return t.SymName
}

// CSSClassesFor は IssueRelation#css_classes_for(issue)。
func (r *IssueRelation) CSSClassesFor(issueID int64) string {
	return "rel-" + r.RelationTypeFor(issueID)
}

// CompareRelations は IssueRelation#<=> (種別の order、同じなら id)。
func CompareRelations(a, b *IssueRelation) int {
	if c := RelationTypes[a.RelationType].Order - RelationTypes[b.RelationType].Order; c != 0 {
		return c
	}
	return cmpInt64(a.ID, b.ID)
}

// Journal は issue_journals 行 (Redmine Journal, journalized_type = Issue)。
type Journal struct {
	ID      int64
	IssueID int64
	UserID  int64
	Notes   string // NULL は ""
	// NotesNull は notes が NULL（Redmine の nil。API で null を出す）。"" とは区別する (D-17)。
	NotesNull    bool
	PrivateNotes bool
	CreatedAt    time.Time
	UpdatedAt    *time.Time
	UpdatedByID  *int64

	Details []*JournalDetail
	// Indice は表示用の通し番号 (visible_journals_with_index で設定)。
	Indice int
}

// HasNotes は notes? (空でないノート)。
func (j *Journal) HasNotes() bool { return j.Notes != "" }

// DetailForAttribute は Journal#detail_for_attribute。
func (j *Journal) DetailForAttribute(attr string) *JournalDetail {
	for _, d := range j.Details {
		if d.PropKey == attr {
			return d
		}
	}
	return nil
}

// NewValueFor は Journal#new_value_for。
func (j *Journal) NewValueFor(prop string) *string {
	if d := j.DetailForAttribute(prop); d != nil {
		return d.Value
	}
	return nil
}

// CSSClasses は Journal#css_classes。
func (j *Journal) CSSClasses() string {
	s := "journal"
	if j.Notes != "" {
		s += " has-notes"
	}
	if len(j.Details) > 0 {
		s += " has-details"
	}
	if j.PrivateNotes {
		s += " private-notes"
	}
	return s
}

// JournalDetail は issue_journal_details 行。
type JournalDetail struct {
	ID        int64
	JournalID int64
	Property  string // attr / cf / attachment / relation
	PropKey   string
	OldValue  *string
	Value     *string
}
