package activity

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
)

// Loader はモデルの行を読み、acts_as_event の属性を評価した Event を作る。
// where は FROM 句（各関数のコメント参照）に対する条件、order は ORDER BY 以降（LIMIT を含んでよい）。
type Loader struct {
	Q    db.Queryer
	Auth *authz.Authorizer
	Loc  *i18n.Localizer
}

// pending は Event のうち後でまとめて読み込む関連（project / author）。
type pending struct {
	ev        *Event
	projectID int64
	authorID  int64
	// authorText はユーザーが無いときの作成者（Changeset の committer）。nil なら作成者なし。
	authorText *string
}

// resolve は project / author をまとめて読み込んで Event に設定する。
func (l *Loader) resolve(ctx context.Context, ps []pending) ([]*Event, error) {
	var pids, uids []int64
	for _, p := range ps {
		if p.projectID != 0 {
			pids = append(pids, p.projectID)
		}
		if p.authorID != 0 {
			uids = append(uids, p.authorID)
		}
	}
	projects := map[int64]*domain.Project{}
	users := map[int64]*domain.User{}
	var err error
	if len(pids) > 0 {
		if projects, err = repository.ProjectsByIDs(ctx, l.Q, pids); err != nil {
			return nil, err
		}
	}
	if len(uids) > 0 {
		if users, err = repository.UsersByIDs(ctx, l.Q, uids); err != nil {
			return nil, err
		}
	}
	out := make([]*Event, len(ps))
	for i, p := range ps {
		p.ev.Project = projects[p.projectID]
		if u := users[p.authorID]; u != nil {
			p.ev.Author = u
		} else if p.authorText != nil {
			p.ev.Author = *p.authorText
		}
		out[i] = p.ev
	}
	return out, nil
}

func (l *Loader) l(key string, args ...any) string {
	if l.Loc == nil {
		return key
	}
	return l.Loc.L(key, args...)
}

func nullInt(n sql.NullInt64) int64 {
	if n.Valid {
		return n.Int64
	}
	return 0
}

func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// escapeSegment は Journey::Router::Utils.escape_segment（URL のパス 1 セグメント）。
func escapeSegment(s string) string {
	const keep = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~!$&'()*+,;=:@"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(keep, s[i]) >= 0 {
			b.WriteByte(s[i])
		} else {
			fmt.Fprintf(&b, "%%%02X", s[i])
		}
	}
	return b.String()
}

func where(cond string) string {
	if strings.TrimSpace(cond) == "" {
		return ""
	}
	return " WHERE " + cond
}

// ---------------------------------------------------------------- Issue

type issueRow struct {
	ID          int64          `db:"id"`
	ProjectID   int64          `db:"project_id"`
	Subject     string         `db:"subject"`
	Description sql.NullString `db:"description"`
	AuthorID    int64          `db:"author_id"`
	CreatedAt   db.Time        `db:"created_at"`
	TrackerName string         `db:"tracker_name"`
	StatusName  string         `db:"status_name"`
	IsClosed    bool           `db:"is_closed"`
	TrackerID   int64          `db:"tracker_id"`
	AssignedTo  sql.NullInt64  `db:"assigned_to_id"`
	IsPrivate   bool           `db:"is_private"`
}

const issueSelect = `SELECT issues.id, issues.project_id, issues.subject, issues.description, issues.author_id, issues.created_at,
  trackers.name AS tracker_name, issue_statuses.name AS status_name, issue_statuses.is_closed,
  issues.tracker_id, issues.assigned_to_id, issues.is_private
FROM issues INNER JOIN projects ON projects.id = issues.project_id
  INNER JOIN trackers ON trackers.id = issues.tracker_id
  INNER JOIN issue_statuses ON issue_statuses.id = issues.status_id`

// issueTitle は Issue の event_title（"#{tracker} ##{id} (#{status}): #{subject}"）。
func (r *issueRow) title() string {
	return r.TrackerName + " #" + itoa(r.ID) + " (" + r.StatusName + "): " + r.Subject
}

// Issues は Issue の Event を返す（FROM issues JOIN projects / trackers / issue_statuses）。
func (l *Loader) Issues(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []issueRow
	if err := l.Q.Select(ctx, &rows, issueSelect+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		typ := "issue"
		if r.IsClosed {
			typ = "issue-closed"
		}
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderIssue, ID: r.ID, Datetime: r.CreatedAt.Time, Title: r.title(),
				Description: r.Description.String, URL: "/issues/" + itoa(r.ID), Type: typ, Group: "Issue:" + itoa(r.ID),
			},
			projectID: r.ProjectID, authorID: r.AuthorID,
		}
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- Journal

type journalRow struct {
	ID        int64          `db:"id"`
	IssueID   int64          `db:"issue_id"`
	UserID    int64          `db:"user_id"`
	Notes     sql.NullString `db:"notes"`
	CreatedAt db.Time        `db:"created_at"`
}

// Journals は Journal の Event を返す（FROM issue_journals JOIN issues JOIN projects
// LEFT JOIN issue_journal_details。重複は除く）。
func (l *Loader) Journals(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []journalRow
	q := `SELECT DISTINCT issue_journals.id, issue_journals.issue_id, issue_journals.user_id, issue_journals.notes, issue_journals.created_at
FROM issue_journals INNER JOIN issues ON issues.id = issue_journals.issue_id
  INNER JOIN projects ON projects.id = issues.project_id
  LEFT OUTER JOIN issue_journal_details ON issue_journal_details.journal_id = issue_journals.id` + where(cond) + " " + order
	if err := l.Q.Select(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	var jids, iids []int64
	for _, r := range rows {
		jids = append(jids, r.ID)
		iids = append(iids, r.IssueID)
	}
	var issues []issueRow
	if err := l.Q.Select(ctx, &issues, issueSelect+" WHERE issues.id IN ("+idList(iids)+")"); err != nil {
		return nil, err
	}
	issueByID := map[int64]*issueRow{}
	for i := range issues {
		issueByID[issues[i].ID] = &issues[i]
	}
	// Journal#new_status（details.detect {|d| d.prop_key == 'status_id'}）
	var details []struct {
		ID        int64          `db:"id"`
		JournalID int64          `db:"journal_id"`
		Value     sql.NullString `db:"value"`
	}
	if err := l.Q.Select(ctx, &details, `SELECT id, journal_id, value FROM issue_journal_details
WHERE prop_key = 'status_id' AND journal_id IN (`+idList(jids)+`) ORDER BY id`); err != nil {
		return nil, err
	}
	newStatus := map[int64]string{}
	for _, d := range details {
		if _, ok := newStatus[d.JournalID]; !ok {
			newStatus[d.JournalID] = d.Value.String
		}
	}
	var statuses []struct {
		ID       int64  `db:"id"`
		Name     string `db:"name"`
		IsClosed bool   `db:"is_closed"`
	}
	if err := l.Q.Select(ctx, &statuses, `SELECT id, name, is_closed FROM issue_statuses`); err != nil {
		return nil, err
	}
	type st struct {
		name   string
		closed bool
	}
	statusByID := map[string]st{}
	for _, s := range statuses {
		statusByID[itoa(s.ID)] = st{s.Name, s.IsClosed}
	}
	var ps []pending
	for _, r := range rows {
		is := issueByID[r.IssueID]
		if is == nil {
			continue
		}
		status := ""
		typ := "issue-note"
		if v, ok := newStatus[r.ID]; ok {
			// IssueStatus.find_by_id(s.to_i)（見つからなければ nil）
			if s, ok := statusByID[itoa(repositoryToI(v))]; ok {
				status = " (" + s.name + ")"
				typ = "issue-edit"
				if s.closed {
					typ = "issue-closed"
				}
			}
		}
		ps = append(ps, pending{
			ev: &Event{
				Provider: ProviderJournal, ID: r.ID, Datetime: r.CreatedAt.Time,
				Title:       is.TrackerName + " #" + itoa(is.ID) + status + ": " + is.Subject,
				Description: r.Notes.String,
				URL:         "/issues/" + itoa(is.ID) + "#change-" + itoa(r.ID), Type: typ, Group: "Issue:" + itoa(is.ID),
			},
			projectID: is.ProjectID, authorID: r.UserID,
		})
	}
	return l.resolve(ctx, ps)
}

// repositoryToI は String#to_i。
func repositoryToI(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	n := int64(0)
	neg := false
	for i, c := range s {
		if i == 0 && (c == '-' || c == '+') {
			neg = c == '-'
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		return -n
	}
	return n
}

// ---------------------------------------------------------------- Changeset

type changesetRow struct {
	ID           int64          `db:"id"`
	RepositoryID int64          `db:"repository_id"`
	Revision     string         `db:"revision"`
	Committer    sql.NullString `db:"committer"`
	CommittedAt  db.Time        `db:"committed_at"`
	Comments     sql.NullString `db:"comments"`
	Scmid        sql.NullString `db:"scmid"`
	UserID       sql.NullInt64  `db:"user_id"`
	RepoIdent    sql.NullString `db:"repo_identifier"`
	Scm          string         `db:"scm"`
	ProjectID    int64          `db:"repo_project_id"`
	ProjectIdent string         `db:"project_identifier"`
}

var reSplitComments = regexp.MustCompile(`(?s)\A(.+?)\r?\n(.*)$`)

// splitComments は Changeset#split_comments（短いコメント, 長いコメント）。
func splitComments(c string, valid bool) (string, string, bool) {
	if !valid {
		return "", "", false
	}
	if m := reSplitComments.FindStringSubmatch(c); m != nil {
		return m[1], strings.TrimSpace(m[2]), true
	}
	return c, "", true
}

// identifier は Changeset#identifier（git / mercurial は scmid）。
func (r *changesetRow) identifier() string {
	if r.Scm == "git" || r.Scm == "mercurial" {
		return r.Scmid.String
	}
	return r.Revision
}

// formatIdentifier は Changeset#format_identifier。
func (r *changesetRow) formatIdentifier() string {
	switch r.Scm {
	case "git":
		return truncRunes(r.Revision, 8)
	case "mercurial":
		return r.Revision + ":" + truncRunes(r.Scmid.String, 12)
	}
	return r.identifier()
}

func truncRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) > n {
		return string(rs[:n])
	}
	return s
}

const changesetSelect = `SELECT changesets.id, changesets.repository_id, changesets.revision, changesets.committer,
  changesets.committed_at, changesets.comments, changesets.scmid, changesets.user_id,
  repositories.identifier AS repo_identifier, repositories.scm, repositories.project_id AS repo_project_id,
  projects.identifier AS project_identifier
FROM changesets INNER JOIN repositories ON repositories.id = changesets.repository_id
  INNER JOIN projects ON projects.id = repositories.project_id`

// Changesets は Changeset の Event を返す（FROM changesets JOIN repositories JOIN projects）。
func (l *Loader) Changesets(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []changesetRow
	if err := l.Q.Select(ctx, &rows, changesetSelect+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		short, long, _ := splitComments(r.Comments.String, r.Comments.Valid)
		repo := ""
		if strings.TrimSpace(r.RepoIdent.String) != "" {
			repo = " (" + r.RepoIdent.String + ")"
		}
		comm := ""
		if strings.TrimSpace(short) != "" {
			comm = ": " + short
		}
		repoParam := r.RepoIdent.String
		if strings.TrimSpace(repoParam) == "" {
			repoParam = itoa(r.RepositoryID)
		}
		p := pending{
			ev: &Event{
				Provider: ProviderChangeset, ID: r.ID, Datetime: r.CommittedAt.Time,
				Title:       l.l("label_revision") + " " + r.formatIdentifier() + repo + comm,
				Description: long,
				URL: "/projects/" + escapeSegment(r.ProjectIdent) + "/repository/" + escapeSegment(repoParam) +
					"/revisions/" + escapeSegment(r.identifier()),
				Type: "changeset", Group: "Changeset:" + itoa(r.ID),
			},
			projectID: r.ProjectID, authorID: nullInt(r.UserID),
		}
		// Changeset#author: user || committer.to_s.split('<').first
		if parts := strings.Split(r.Committer.String, "<"); parts[0] != "" {
			s := parts[0]
			p.authorText = &s
		}
		ps[i] = p
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- News

// News は News の Event を返す（FROM news JOIN projects）。
func (l *Loader) News(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID          int64          `db:"id"`
		ProjectID   int64          `db:"project_id"`
		Title       string         `db:"title"`
		Description sql.NullString `db:"description"`
		AuthorID    int64          `db:"author_id"`
		CreatedAt   db.Time        `db:"created_at"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT news.id, news.project_id, news.title, news.description, news.author_id, news.created_at
FROM news INNER JOIN projects ON projects.id = news.project_id`+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderNews, ID: r.ID, Datetime: r.CreatedAt.Time, Title: r.Title,
				Description: r.Description.String, URL: "/news/" + itoa(r.ID), Type: "news", Group: "News:" + itoa(r.ID),
			},
			projectID: r.ProjectID, authorID: r.AuthorID,
		}
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- Document

// Documents は Document の Event を返す（FROM documents JOIN projects）。
// 作成者は最初の添付ファイルの作成者（attachments.reorder(created_on ASC).first.try(:author)）。
func (l *Loader) Documents(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID          int64          `db:"id"`
		ProjectID   int64          `db:"project_id"`
		Title       string         `db:"title"`
		Description sql.NullString `db:"description"`
		CreatedAt   db.Time        `db:"created_at"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT documents.id, documents.project_id, documents.title, documents.description, documents.created_at
FROM documents INNER JOIN projects ON projects.id = documents.project_id`+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	var atts []struct {
		ContainerID int64 `db:"container_id"`
		AuthorID    int64 `db:"author_id"`
	}
	if err := l.Q.Select(ctx, &atts, `SELECT container_id, author_id FROM attachments
WHERE container_kind = 'document' AND container_id IN (`+idList(ids)+`) ORDER BY created_at ASC, id ASC`); err != nil {
		return nil, err
	}
	firstAuthor := map[int64]int64{}
	for _, a := range atts {
		if _, ok := firstAuthor[a.ContainerID]; !ok {
			firstAuthor[a.ContainerID] = a.AuthorID
		}
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderDocument, ID: r.ID, Datetime: r.CreatedAt.Time,
				Title:       l.l("label_document") + ": " + r.Title,
				Description: r.Description.String, URL: "/documents/" + itoa(r.ID), Type: "document", Group: "Document:" + itoa(r.ID),
			},
			projectID: r.ProjectID, authorID: firstAuthor[r.ID],
		}
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- Attachment

// Attachments は Attachment の Event を返す。from は FROM 句（attachments と、project を projects として結合したもの）。
func (l *Loader) Attachments(ctx context.Context, from, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID          int64          `db:"id"`
		Filename    string         `db:"filename"`
		Description sql.NullString `db:"description"`
		AuthorID    int64          `db:"author_id"`
		CreatedAt   db.Time        `db:"created_at"`
		ProjectID   sql.NullInt64  `db:"att_project_id"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT attachments.id, attachments.filename, attachments.description, attachments.author_id,
  attachments.created_at, projects.id AS att_project_id FROM `+from+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderAttachment, ID: r.ID, Datetime: r.CreatedAt.Time, Title: r.Filename,
				Description: r.Description.String,
				URL:         "/attachments/" + itoa(r.ID) + "/" + escapeSegment(r.Filename), Type: "attachment",
				Group: "Attachment:" + itoa(r.ID),
			},
			projectID: nullInt(r.ProjectID), authorID: r.AuthorID,
		}
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- WikiContentVersion

// WikiContentVersions は WikiContentVersion の Event を返す
// （FROM wiki_page_versions LEFT JOIN wiki_pages LEFT JOIN wikis LEFT JOIN projects）。
func (l *Loader) WikiContentVersions(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID           int64          `db:"id"`
		PageID       int64          `db:"page_id"`
		Version      int64          `db:"version"`
		AuthorID     sql.NullInt64  `db:"author_id"`
		Comments     sql.NullString `db:"comments"`
		UpdatedAt    db.Time        `db:"updated_at"`
		Title        sql.NullString `db:"title"`
		ProjectID    sql.NullInt64  `db:"wiki_project_id"`
		ProjectIdent sql.NullString `db:"project_identifier"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT wiki_page_versions.id, wiki_page_versions.page_id, wiki_page_versions.version,
  wiki_page_versions.author_id, wiki_page_versions.comments, wiki_page_versions.updated_at, wiki_pages.title,
  projects.id AS wiki_project_id, projects.identifier AS project_identifier
FROM wiki_page_versions LEFT JOIN wiki_pages ON wiki_pages.id = wiki_page_versions.page_id
  LEFT JOIN wikis ON wikis.id = wiki_pages.wiki_id
  LEFT JOIN projects ON projects.id = wikis.project_id`+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderWikiContentVersion, ID: r.ID, Datetime: r.UpdatedAt.Time,
				Title:       l.l("label_wiki_edit") + ": " + r.Title.String + " (#" + itoa(r.Version) + ")",
				Description: r.Comments.String,
				URL: "/projects/" + escapeSegment(r.ProjectIdent.String) + "/wiki/" + escapeSegment(r.Title.String) +
					"/" + itoa(r.Version),
				Type: "wiki-page", Group: "WikiPage:" + itoa(r.PageID),
			},
			projectID: nullInt(r.ProjectID), authorID: nullInt(r.AuthorID),
		}
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- WikiPage（検索結果）

// WikiPages は WikiPage の Event を返す（FROM wiki_pages JOIN 最新版 JOIN wikis JOIN projects）。
// event_title は "Wiki: タイトル"、説明は最新版の本文、日時は作成日時。
func (l *Loader) WikiPages(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID           int64          `db:"id"`
		Title        string         `db:"title"`
		Text         sql.NullString `db:"text"`
		CreatedAt    db.Time        `db:"created_at"`
		ProjectID    int64          `db:"wiki_project_id"`
		ProjectIdent string         `db:"project_identifier"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT wiki_pages.id, wiki_pages.title, wiki_contents.text, wiki_pages.created_at,
  projects.id AS wiki_project_id, projects.identifier AS project_identifier
FROM `+WikiPageFrom+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderWikiPage, ID: r.ID, Datetime: r.CreatedAt.Time,
				Title:       l.l("label_wiki") + ": " + r.Title,
				Description: r.Text.String,
				URL:         "/projects/" + escapeSegment(r.ProjectIdent) + "/wiki/" + escapeSegment(r.Title),
				Type:        "wiki-page", Group: "WikiPage:" + itoa(r.ID),
			},
			projectID: r.ProjectID,
		}
	}
	return l.resolve(ctx, ps)
}

// WikiPageFrom は WikiPage の検索スコープ（joins(:content, {:wiki => :project})）。
// 最新版（wiki_contents 相当）を wiki_contents という名前で結合する。
const WikiPageFrom = `wiki_pages INNER JOIN wiki_page_versions wiki_contents
    ON wiki_contents.page_id = wiki_pages.id AND wiki_contents.version = wiki_pages.current_version
  INNER JOIN wikis ON wikis.id = wiki_pages.wiki_id
  INNER JOIN projects ON projects.id = wikis.project_id`

// ---------------------------------------------------------------- Message

// Messages は Message の Event を返す（FROM messages JOIN boards JOIN projects）。
func (l *Loader) Messages(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID        int64          `db:"id"`
		BoardID   int64          `db:"board_id"`
		ParentID  sql.NullInt64  `db:"parent_id"`
		Subject   string         `db:"subject"`
		Content   sql.NullString `db:"content"`
		AuthorID  sql.NullInt64  `db:"author_id"`
		CreatedAt db.Time        `db:"created_at"`
		BoardName string         `db:"board_name"`
		ProjectID int64          `db:"board_project_id"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT messages.id, messages.board_id, messages.parent_id, messages.subject, messages.content,
  messages.author_id, messages.created_at, boards.name AS board_name, boards.project_id AS board_project_id
FROM messages INNER JOIN boards ON boards.id = messages.board_id
  INNER JOIN projects ON projects.id = boards.project_id`+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		typ := "message"
		url := "/boards/" + itoa(r.BoardID) + "/topics/" + itoa(r.ID)
		group := "Message:" + itoa(r.ID)
		if r.ParentID.Valid {
			typ = "reply"
			url = "/boards/" + itoa(r.BoardID) + "/topics/" + itoa(r.ParentID.Int64) + "?r=" + itoa(r.ID) + "#message-" + itoa(r.ID)
			group = "Message:" + itoa(r.ParentID.Int64)
		}
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderMessage, ID: r.ID, Datetime: r.CreatedAt.Time,
				Title: r.BoardName + ": " + r.Subject, Description: r.Content.String, URL: url, Type: typ, Group: group,
			},
			projectID: r.ProjectID, authorID: nullInt(r.AuthorID),
		}
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- TimeEntry

// TimeEntries は TimeEntry の Event を返す（FROM time_entries JOIN projects）。
// event_title は "#{l_hours(hours)} (#{related.event_title})"（related は可視なチケット、無ければプロジェクト）。
func (l *Loader) TimeEntries(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID           int64          `db:"id"`
		ProjectID    int64          `db:"project_id"`
		UserID       int64          `db:"user_id"`
		IssueID      sql.NullInt64  `db:"issue_id"`
		Hours        float64        `db:"hours"`
		Comments     sql.NullString `db:"comments"`
		CreatedAt    db.Time        `db:"created_at"`
		ProjectName  string         `db:"project_name"`
		ProjectIdent string         `db:"project_identifier"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT time_entries.id, time_entries.project_id, time_entries.user_id, time_entries.issue_id,
  time_entries.hours, time_entries.comments, time_entries.created_at, projects.name AS project_name,
  projects.identifier AS project_identifier
FROM time_entries INNER JOIN projects ON projects.id = time_entries.project_id`+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	var iids []int64
	for _, r := range rows {
		if r.IssueID.Valid {
			iids = append(iids, r.IssueID.Int64)
		}
	}
	issueByID := map[int64]*issueRow{}
	if len(iids) > 0 {
		var issues []issueRow
		if err := l.Q.Select(ctx, &issues, issueSelect+" WHERE issues.id IN ("+idList(iids)+")"); err != nil {
			return nil, err
		}
		for i := range issues {
			issueByID[issues[i].ID] = &issues[i]
		}
	}
	var projects map[int64]*domain.Project
	ps := make([]pending, len(rows))
	for i, r := range rows {
		related := l.l("label_project") + ": " + r.ProjectName
		url := "/projects/" + escapeSegment(r.ProjectIdent) + "/time_entries"
		group := "TimeEntry:" + itoa(r.ID)
		if is := issueByID[nullInt(r.IssueID)]; is != nil {
			url += "?issue_id=" + itoa(is.ID)
			group = "Issue:" + itoa(is.ID)
			if projects == nil {
				var err error
				if projects, err = repository.ProjectsByIDs(ctx, l.Q, []int64{is.ProjectID}); err != nil {
					return nil, err
				}
			} else if projects[is.ProjectID] == nil {
				more, err := repository.ProjectsByIDs(ctx, l.Q, []int64{is.ProjectID})
				if err != nil {
					return nil, err
				}
				projects[is.ProjectID] = more[is.ProjectID]
			}
			visible := false
			if l.Auth != nil && projects[is.ProjectID] != nil {
				di := &domain.Issue{ID: is.ID, ProjectID: is.ProjectID, TrackerID: is.TrackerID, AuthorID: is.AuthorID, IsPrivate: is.IsPrivate}
				if is.AssignedTo.Valid {
					v := is.AssignedTo.Int64
					di.AssignedToID = &v
				}
				ok, err := l.Auth.IssueVisible(ctx, di, projects[is.ProjectID])
				if err != nil {
					return nil, err
				}
				visible = ok
			}
			if visible {
				related = is.title()
			}
		}
		hours := strconv.FormatFloat(r.Hours, 'f', 2, 64)
		if l.Loc != nil {
			hours = l.Loc.LHours(r.Hours)
		}
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderTimeEntry, ID: r.ID, Datetime: r.CreatedAt.Time,
				Title: hours + " (" + related + ")", Description: r.Comments.String, URL: url, Type: "time-entry", Group: group,
			},
			projectID: r.ProjectID, authorID: r.UserID,
		}
	}
	return l.resolve(ctx, ps)
}

// ---------------------------------------------------------------- Project（検索結果）

// Projects は Project の Event を返す（FROM projects）。event_title は "Project: 名前"、作成者なし。
func (l *Loader) Projects(ctx context.Context, cond string, args []any, order string) ([]*Event, error) {
	var rows []struct {
		ID          int64          `db:"id"`
		Name        string         `db:"name"`
		Identifier  string         `db:"identifier"`
		Description sql.NullString `db:"description"`
		CreatedAt   db.Time        `db:"created_at"`
	}
	if err := l.Q.Select(ctx, &rows, `SELECT projects.id, projects.name, projects.identifier, projects.description, projects.created_at
FROM projects`+where(cond)+" "+order, args...); err != nil {
		return nil, err
	}
	ps := make([]pending, len(rows))
	for i, r := range rows {
		ps[i] = pending{
			ev: &Event{
				Provider: ProviderProject, ID: r.ID, Datetime: r.CreatedAt.Time,
				Title: l.l("label_project") + ": " + r.Name, Description: r.Description.String,
				URL: "/projects/" + escapeSegment(r.Identifier), Type: "project", Group: "Project:" + itoa(r.ID),
			},
			projectID: r.ID,
		}
	}
	return l.resolve(ctx, ps)
}
