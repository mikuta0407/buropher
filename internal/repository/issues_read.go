package repository

// このファイルはチケットの参照系画面（issues#index / show、journals#index / diff、context_menus#issues）が
// 読む行の取得。更新系は internal/issues（チケットのドメインサービス）が担う。
//
// TODO(dedupe): internal/issues 側に同等の読み取りが入ったら関数を統合する。名前の衝突を避けるため
// 型・関数には Read / IssueRead の接頭辞を付けている。

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// ---------------------------------------------------------------- チケット

// ReadIssueRow は issues の 1 行（参照用）。
type ReadIssueRow struct {
	ID             int64       `db:"id"`
	ProjectID      int64       `db:"project_id"`
	TrackerID      int64       `db:"tracker_id"`
	StatusID       int64       `db:"status_id"`
	PriorityID     int64       `db:"priority_id"`
	AuthorID       int64       `db:"author_id"`
	AssignedToID   *int64      `db:"assigned_to_id"`
	CategoryID     *int64      `db:"category_id"`
	FixedVersionID *int64      `db:"fixed_version_id"`
	ParentID       *int64      `db:"parent_id"`
	RootID         int64       `db:"root_id"`
	HierPath       string      `db:"hier_path"`
	Subject        string      `db:"subject"`
	Description    *string     `db:"description"`
	StartDate      db.NullDate `db:"start_date"`
	DueDate        db.NullDate `db:"due_date"`
	DoneRatio      int         `db:"done_ratio"`
	EstimatedHours *float64    `db:"estimated_hours"`
	IsPrivate      bool        `db:"is_private"`
	LockVersion    int         `db:"lock_version"`
	CreatedAt      db.Time     `db:"created_at"`
	UpdatedAt      db.Time     `db:"updated_at"`
	ClosedAt       db.NullTime `db:"closed_at"`
}

const readIssueCols = `issues.id, issues.project_id, issues.tracker_id, issues.status_id, issues.priority_id, issues.author_id,
  issues.assigned_to_id, issues.category_id, issues.fixed_version_id, issues.parent_id, issues.root_id, issues.hier_path,
  issues.subject, issues.description, issues.start_date, issues.due_date, issues.done_ratio, issues.estimated_hours,
  issues.is_private, issues.lock_version, issues.created_at, issues.updated_at, issues.closed_at`

// ReadIssue は Issue.find(id)（可視性は問わない）。
func ReadIssue(ctx context.Context, q db.Queryer, id int64) (*ReadIssueRow, error) {
	var r ReadIssueRow
	if err := q.Get(ctx, &r, `SELECT `+readIssueCols+` FROM issues WHERE issues.id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// ReadIssuesWhere は条件に合うチケットを order の順で返す（where / order は issues・projects を参照できる）。
func ReadIssuesWhere(ctx context.Context, q db.Queryer, where, order string, args ...any) ([]*ReadIssueRow, error) {
	if order == "" {
		order = "issues.id"
	}
	var rows []*ReadIssueRow
	if err := q.Select(ctx, &rows, `SELECT `+readIssueCols+` FROM issues JOIN projects ON projects.id = issues.project_id
WHERE `+where+` ORDER BY `+order, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

// ReadIssuesByIDs は id のチケット（id → 行）。
func ReadIssuesByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*ReadIssueRow, error) {
	out := map[int64]*ReadIssueRow{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT `+readIssueCols+` FROM issues WHERE issues.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []*ReadIssueRow
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r
		}
	}
	return out, nil
}

// IssueIDsWithChildren は ids のうち子チケットを持つものの集合（leaf? の判定）。
func IssueIDsWithChildren(ctx context.Context, q db.Queryer, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT DISTINCT parent_id FROM issues WHERE parent_id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var ps []int64
		if err := q.Select(ctx, &ps, query, args...); err != nil {
			return nil, err
		}
		for _, p := range ps {
			out[p] = true
		}
	}
	return out, nil
}

// IssueDescendants は issue.descendants（自身を除く。ツリー順 = root_id, hier_path）。
func IssueDescendants(ctx context.Context, q db.Queryer, rootID int64, hierPath string) ([]*ReadIssueRow, error) {
	var rows []*ReadIssueRow
	if err := q.Select(ctx, &rows, `SELECT `+readIssueCols+` FROM issues WHERE issues.root_id = ? AND issues.hier_path LIKE ? AND issues.hier_path <> ?
ORDER BY issues.hier_path`, rootID, hierPath+"%", hierPath); err != nil {
		return nil, err
	}
	return rows, nil
}

// IssueAncestorIDs は hier_path から祖先の id（ルートから順。自身を除く）を返す。
func IssueAncestorIDs(hierPath string) []int64 {
	parts := strings.Split(strings.TrimSuffix(hierPath, "/"), "/")
	var out []int64
	for i := 0; i < len(parts)-1; i++ {
		var n int64
		for _, c := range parts[i] {
			if c >= '0' && c <= '9' {
				n = n*10 + int64(c-'0')
			}
		}
		out = append(out, n)
	}
	return out
}

// ---------------------------------------------------------------- プリンシパル

// PrincipalsAsUsersByIDs は id のプリンシパル（ユーザー・グループとも domain.User で返す。グループは Principal 部分のみ）。
func PrincipalsAsUsersByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*domain.User, error) {
	out := make(map[int64]*domain.User, len(ids))
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(userSelect+` WHERE p.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []userRow
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for i := range rows {
			out[rows[i].ID] = rows[i].user()
		}
	}
	return out, nil
}

// VisibleSpentHours は Issue.load_visible_spent_hours（TimeEntry.visible に限った issue ごとの工数合計。cond は可視条件で
// time_entries・projects を参照）。工数の無いチケットは含まない。
func VisibleSpentHours(ctx context.Context, q db.Queryer, ids []int64, cond string) (map[int64]float64, error) {
	return sumHoursByID(ctx, q, `SELECT time_entries.issue_id AS id, SUM(time_entries.hours) AS total FROM time_entries
JOIN projects ON projects.id = time_entries.project_id
WHERE (`+condOr(cond)+`) AND time_entries.issue_id IN (`+joinIDs(ids)+`) GROUP BY time_entries.issue_id`, ids)
}

// VisibleTotalSpentHours は Issue.load_visible_total_spent_hours（自身と子孫の可視な工数の合計）。
func VisibleTotalSpentHours(ctx context.Context, q db.Queryer, ids []int64, cond string) (map[int64]float64, error) {
	return sumHoursByID(ctx, q, `SELECT parent.id AS id, SUM(time_entries.hours) AS total FROM time_entries
JOIN projects ON projects.id = time_entries.project_id JOIN issues ON issues.id = time_entries.issue_id
JOIN issues parent ON parent.root_id = issues.root_id AND issues.hier_path LIKE (parent.hier_path || '%')
WHERE (`+condOr(cond)+`) AND parent.id IN (`+joinIDs(ids)+`) GROUP BY parent.id`, ids)
}

func sumHoursByID(ctx context.Context, q db.Queryer, query string, ids []int64) (map[int64]float64, error) {
	m := map[int64]float64{}
	if len(ids) == 0 {
		return m, nil
	}
	var sums []struct {
		ID    int64   `db:"id"`
		Total float64 `db:"total"`
	}
	if err := q.Select(ctx, &sums, query); err != nil {
		return nil, err
	}
	for _, s := range sums {
		m[s.ID] = s.Total
	}
	return m, nil
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = itoa64(id)
	}
	return strings.Join(parts, ", ")
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ---------------------------------------------------------------- カテゴリ・バージョン

// IssueCategory は issue_categories の行。
type IssueCategory struct {
	ID           int64  `db:"id"`
	ProjectID    int64  `db:"project_id"`
	Name         string `db:"name"`
	AssignedToID *int64 `db:"assigned_to_id"`
}

// IssueCategoriesByIDs は id のカテゴリ。
func IssueCategoriesByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*IssueCategory, error) {
	out := map[int64]*IssueCategory{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT id, project_id, name, assigned_to_id FROM issue_categories WHERE id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []*IssueCategory
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r
		}
	}
	return out, nil
}

// Version は versions の行。
type Version struct {
	ID            int64       `db:"id"`
	ProjectID     int64       `db:"project_id"`
	ProjectName   string      `db:"project_name"`
	Name          string      `db:"name"`
	Description   *string     `db:"description"`
	EffectiveDate db.NullDate `db:"effective_date"`
	WikiPageTitle *string     `db:"wiki_page_title"`
	Status        string      `db:"status"`
	Sharing       string      `db:"sharing"`
	CreatedAt     db.Time     `db:"created_at"`
	UpdatedAt     db.Time     `db:"updated_at"`
}

// String は Version#to_s。
func (v *Version) String() string { return v.Name }

// Compare は Version#<=>。
func (v *Version) Compare(o *Version) int {
	switch {
	case v.EffectiveDate.Valid && o.EffectiveDate.Valid:
		if !v.EffectiveDate.Date.Equal(o.EffectiveDate.Date.Time) {
			return v.EffectiveDate.Date.Compare(o.EffectiveDate.Date.Time)
		}
	case v.EffectiveDate.Valid:
		return -1
	case o.EffectiveDate.Valid:
		return 1
	}
	if v.Name == o.Name {
		switch {
		case v.ID < o.ID:
			return -1
		case v.ID > o.ID:
			return 1
		}
		return 0
	}
	return strings.Compare(v.Name, o.Name)
}

// SortVersions は versions.sort（Version#<=>）。
func SortVersions(vs []*Version) {
	slices.SortStableFunc(vs, func(a, b *Version) int { return a.Compare(b) })
}

const versionSelect = `SELECT versions.id, versions.project_id, projects.name AS project_name, versions.name, versions.description,
  versions.effective_date, versions.wiki_page_title, versions.status, versions.sharing, versions.created_at, versions.updated_at
FROM versions JOIN projects ON projects.id = versions.project_id`

// VersionsByIDs は id のバージョン。
func VersionsByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*Version, error) {
	out := map[int64]*Version{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(versionSelect+` WHERE versions.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []*Version
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r
		}
	}
	return out, nil
}

// VersionsWhere は条件（versions・projects を参照）に合うバージョン（並びは呼び出し側）。
func VersionsWhere(ctx context.Context, q db.Queryer, where string, args ...any) ([]*Version, error) {
	var rows []*Version
	err := q.Select(ctx, &rows, versionSelect+` WHERE `+where+` ORDER BY versions.id`, args...)
	return rows, err
}

// ---------------------------------------------------------------- ジャーナル

// IssueJournal は issue_journals の行。
type IssueJournal struct {
	ID           int64       `db:"id"`
	IssueID      int64       `db:"issue_id"`
	UserID       int64       `db:"user_id"`
	Notes        *string     `db:"notes"`
	PrivateNotes bool        `db:"private_notes"`
	CreatedAt    db.Time     `db:"created_at"`
	UpdatedAt    db.NullTime `db:"updated_at"`
	UpdatedByID  *int64      `db:"updated_by_id"`

	Details []*IssueJournalDetail `db:"-"`
}

// NotesString は notes（nil は ""）。
func (j *IssueJournal) NotesString() string {
	if j.Notes == nil {
		return ""
	}
	return *j.Notes
}

// IssueJournalDetail は issue_journal_details の行。
type IssueJournalDetail struct {
	ID            int64   `db:"id"`
	JournalID     int64   `db:"journal_id"`
	Property      string  `db:"property"`
	PropKey       string  `db:"prop_key"`
	CustomFieldID *int64  `db:"custom_field_id"`
	OldValue      *string `db:"old_value"`
	Value         *string `db:"value"`
}

const journalCols = `issue_journals.id, issue_journals.issue_id, issue_journals.user_id, issue_journals.notes, issue_journals.private_notes,
  issue_journals.created_at, issue_journals.updated_at, issue_journals.updated_by_id`

// GetIssueJournal は Journal.find(id)（details 付き）。
func GetIssueJournal(ctx context.Context, q db.Queryer, id int64) (*IssueJournal, error) {
	var j IssueJournal
	if err := q.Get(ctx, &j, `SELECT `+journalCols+` FROM issue_journals WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	if err := LoadJournalDetails(ctx, q, []*IssueJournal{&j}); err != nil {
		return nil, err
	}
	return &j, nil
}

// LoadJournalDetails は journals の details を読み込む。
func LoadJournalDetails(ctx context.Context, q db.Queryer, js []*IssueJournal) error {
	if len(js) == 0 {
		return nil
	}
	byID := map[int64]*IssueJournal{}
	ids := make([]int64, len(js))
	for i, j := range js {
		byID[j.ID] = j
		ids[i] = j.ID
		j.Details = nil
	}
	for _, chunk := range chunkIDs(ids) {
		query, args, err := db.In(`SELECT id, journal_id, property, prop_key, custom_field_id, old_value, value FROM issue_journal_details
WHERE journal_id IN (?) ORDER BY id`, chunk)
		if err != nil {
			return err
		}
		var ds []*IssueJournalDetail
		if err := q.Select(ctx, &ds, query, args...); err != nil {
			return err
		}
		for _, d := range ds {
			byID[d.JournalID].Details = append(byID[d.JournalID].Details, d)
		}
	}
	return nil
}

// ---------------------------------------------------------------- 添付・関連・ウォッチャー

// ReadAttachment は attachments の行（一覧表示用）。
type ReadAttachment struct {
	ID            int64   `db:"id"`
	ContainerKind *string `db:"container_kind"`
	ContainerID   *int64  `db:"container_id"`
	Filename      string  `db:"filename"`
	DiskDirectory *string `db:"disk_directory"`
	DiskFilename  string  `db:"disk_filename"`
	Filesize      int64   `db:"filesize"`
	ContentType   *string `db:"content_type"`
	Digest        *string `db:"digest"`
	Downloads     int     `db:"downloads"`
	AuthorID      int64   `db:"author_id"`
	Description   *string `db:"description"`
	CreatedAt     db.Time `db:"created_at"`
}

const readAttachmentCols = `id, container_kind, container_id, filename, disk_directory, disk_filename, filesize, content_type, digest,
  downloads, author_id, description, created_at`

// ReadContainerAttachments は container.attachments（created_at, id 順）。
func ReadContainerAttachments(ctx context.Context, q db.Queryer, kind string, id int64) ([]*ReadAttachment, error) {
	var rows []*ReadAttachment
	err := q.Select(ctx, &rows, `SELECT `+readAttachmentCols+` FROM attachments WHERE container_kind = ? AND container_id = ? ORDER BY created_at, id`, kind, id)
	return rows, err
}

// ReadAttachmentsByIDs は id の添付。
func ReadAttachmentsByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*ReadAttachment, error) {
	out := map[int64]*ReadAttachment{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT `+readAttachmentCols+` FROM attachments WHERE id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []*ReadAttachment
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r
		}
	}
	return out, nil
}

// IssueRelation は issue_relations の行。
type IssueRelation struct {
	ID           int64  `db:"id"`
	IssueFromID  int64  `db:"issue_from_id"`
	IssueToID    int64  `db:"issue_to_id"`
	RelationType string `db:"relation_type"`
	Delay        *int   `db:"delay"`
}

// RelationsByIDs は id の関連。
func RelationsByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*IssueRelation, error) {
	out := map[int64]*IssueRelation{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT id, issue_from_id, issue_to_id, relation_type, delay FROM issue_relations WHERE id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []*IssueRelation
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- 作業時間・リビジョン

// ReadTimeEntry は time_entries の行（チケットの作業時間タブ用）。
type ReadTimeEntry struct {
	ID         int64   `db:"id"`
	ProjectID  int64   `db:"project_id"`
	UserID     int64   `db:"user_id"`
	AuthorID   int64   `db:"author_id"`
	IssueID    *int64  `db:"issue_id"`
	Hours      float64 `db:"hours"`
	Comments   *string `db:"comments"`
	ActivityID int64   `db:"activity_id"`
	SpentOn    db.Date `db:"spent_on"`
	CreatedAt  db.Time `db:"created_at"`
	UpdatedAt  db.Time `db:"updated_at"`
}

// IssueTimeEntries は issue.time_entries.visible（cond は可視条件。time_entries・projects を参照）。並びは spent_on DESC, id DESC
// ではなく Redmine の has_many の既定（id 順）。
func IssueTimeEntries(ctx context.Context, q db.Queryer, issueID int64, cond string) ([]*ReadTimeEntry, error) {
	var rows []*ReadTimeEntry
	err := q.Select(ctx, &rows, `SELECT time_entries.id, time_entries.project_id, time_entries.user_id, time_entries.author_id,
  time_entries.issue_id, time_entries.hours, time_entries.comments, time_entries.activity_id, time_entries.spent_on,
  time_entries.created_at, time_entries.updated_at
FROM time_entries JOIN projects ON projects.id = time_entries.project_id
WHERE time_entries.issue_id = ? AND (`+condOr(cond)+`) ORDER BY time_entries.id`, issueID)
	return rows, err
}

// ReadChangeset は changesets の行（チケットのリビジョンタブ用）。
type ReadChangeset struct {
	ID             int64          `db:"id"`
	RepositoryID   int64          `db:"repository_id"`
	Revision       string         `db:"revision"`
	Committer      *string        `db:"committer"`
	CommittedAt    db.Time        `db:"committed_at"`
	Comments       *string        `db:"comments"`
	Scmid          *string        `db:"scmid"`
	UserID         *int64         `db:"user_id"`
	RepoIdentifier sql.NullString `db:"repo_identifier"`
	RepoIsDefault  bool           `db:"repo_is_default"`
	RepoSCM        string         `db:"repo_scm"`
	ProjectID      int64          `db:"project_id"`
}

// IssueChangesets は issue.changesets.visible（cond は Changeset の可視条件。projects を参照）。committed_on, id 順。
func IssueChangesets(ctx context.Context, q db.Queryer, issueID int64, cond string) ([]*ReadChangeset, error) {
	var rows []*ReadChangeset
	err := q.Select(ctx, &rows, `SELECT changesets.id, changesets.repository_id, changesets.revision, changesets.committer,
  changesets.committed_at, changesets.comments, changesets.scmid, changesets.user_id,
  repositories.identifier AS repo_identifier, repositories.is_default AS repo_is_default, repositories.scm AS repo_scm,
  repositories.project_id
FROM changesets JOIN changesets_issues ON changesets_issues.changeset_id = changesets.id
JOIN repositories ON repositories.id = changesets.repository_id JOIN projects ON projects.id = repositories.project_id
WHERE changesets_issues.issue_id = ? AND (`+condOr(cond)+`) ORDER BY changesets.committed_at, changesets.id`, issueID)
	return rows, err
}

// ---------------------------------------------------------------- その他

// LastJournalID は issue.last_journal_id（無ければ nil）。
func LastJournalID(ctx context.Context, q db.Queryer, issueID int64) (*int64, error) {
	var v sql.NullInt64
	if err := q.Get(ctx, &v, `SELECT MAX(id) FROM issue_journals WHERE issue_id = ?`, issueID); err != nil {
		return nil, err
	}
	if !v.Valid {
		return nil, nil
	}
	return &v.Int64, nil
}

// ReactionRow は reactions の行。
type ReactionRow struct {
	ID     int64 `db:"id"`
	UserID int64 `db:"user_id"`
}

// ReactionsFor は Reaction.visible(user).for_reactable(object).order(id: :desc)（cond は principals を参照する可視条件）。
func ReactionsFor(ctx context.Context, q db.Queryer, kind string, id int64, cond string) ([]*ReactionRow, error) {
	var rows []*ReactionRow
	err := q.Select(ctx, &rows, `SELECT reactions.id, reactions.user_id FROM reactions
JOIN principals ON principals.id = reactions.user_id
WHERE reactions.reactable_kind = ? AND reactions.reactable_id = ? AND principals.kind = 'user' AND (`+condOr(cond)+`)
ORDER BY reactions.id DESC`, kind, id)
	return rows, err
}

// TimeEntryCustomFieldIDs は TimeEntryCustomField の id（position 順）。
func VisibleTimeEntryCustomFieldIDs(ctx context.Context, q db.Queryer) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT id FROM custom_fields WHERE owner_kind = 'time_entry' ORDER BY position, id`)
	return ids, err
}

// ProjectMemberUserIDs は project.users（有効なユーザーのメンバー。roleIDs が空でなければそのロールを持つもの）。
func ProjectMemberUserIDs(ctx context.Context, q db.Queryer, projectID int64, roleIDs []int64) ([]int64, error) {
	where := ""
	if len(roleIDs) > 0 {
		where = " AND members.id IN (SELECT member_id FROM member_roles WHERE role_id IN (" + joinIDs(roleIDs) + "))"
	}
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT DISTINCT p.id FROM principals p JOIN members ON members.principal_id = p.id
WHERE members.project_id = ? AND p.kind = 'user' AND p.status = 1`+where+` ORDER BY p.id`, projectID)
	return ids, err
}

// ChangesetFileCount は changeset.filechanges.count。
func ChangesetFileCount(ctx context.Context, q db.Queryer, changesetID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM changeset_files WHERE changeset_id = ?`, changesetID)
	return n, err
}

// unused guard
var _ = time.Time{}
