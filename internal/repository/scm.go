// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはリポジトリ（SCM）・チェンジセットの SQL（app/models/repository.rb, changeset.rb, change.rb）。

type scmRepositoryRow struct {
	ID           int64                   `db:"id"`
	ProjectID    int64                   `db:"project_id"`
	SCM          string                  `db:"scm"`
	URL          string                  `db:"url"`
	RootURL      sql.NullString          `db:"root_url"`
	Login        sql.NullString          `db:"login"`
	PathEncoding sql.NullString          `db:"path_encoding"`
	LogEncoding  sql.NullString          `db:"log_encoding"`
	ExtraInfo    db.JSON[map[string]any] `db:"extra_info"`
	Identifier   sql.NullString          `db:"identifier"`
	IsDefault    bool                    `db:"is_default"`
	CreatedAt    db.Time                 `db:"created_at"`
}

const scmRepositoryCols = `id, project_id, scm, url, root_url, login, path_encoding, log_encoding, extra_info, identifier, is_default, created_at`

func (r *scmRepositoryRow) toDomain() *domain.Repository {
	return &domain.Repository{ID: r.ID, ProjectID: r.ProjectID, SCM: r.SCM, URL: r.URL, RootURL: r.RootURL.String,
		Login: r.Login.String, PathEncoding: r.PathEncoding.String, LogEncoding: r.LogEncoding.String,
		ExtraInfo: r.ExtraInfo.V, Identifier: r.Identifier.String, IsDefault: r.IsDefault, CreatedOn: r.CreatedAt.Time}
}

// GetScmRepository は Repository.find(id)。
func GetScmRepository(ctx context.Context, q db.Queryer, id int64) (*domain.Repository, error) {
	var r scmRepositoryRow
	if err := q.Get(ctx, &r, `SELECT `+scmRepositoryCols+` FROM repositories WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return r.toDomain(), nil
}

// ProjectScmRepositories は project.repositories（id 順）。
func ProjectScmRepositories(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.Repository, error) {
	var rows []*scmRepositoryRow
	if err := q.Select(ctx, &rows, `SELECT `+scmRepositoryCols+` FROM repositories WHERE project_id = ? ORDER BY id`, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.Repository, len(rows))
	for i, r := range rows {
		out[i] = r.toDomain()
	}
	return out, nil
}

// ProjectHasDefaultRepository は project.repository（is_default の行）があるか。
func ProjectHasDefaultRepository(ctx context.Context, q db.Queryer, projectID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM repositories WHERE project_id = ? AND is_default = ?`, projectID, true)
}

// ScmRepositoryIdentifierTaken は validates_uniqueness_of :identifier, :scope => :project_id。
func ScmRepositoryIdentifierTaken(ctx context.Context, q db.Queryer, projectID int64, identifier string, exceptID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM repositories WHERE project_id = ? AND COALESCE(identifier, '') = ? AND id <> ?`, projectID, identifier, exceptID)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func extraInfoValue(m map[string]any) any {
	if m == nil {
		return nil
	}
	return db.NewJSON(m)
}

// ClearDefaultScmRepository は Repository.where(project_id).update_all(is_default: false)。
func ClearDefaultScmRepository(ctx context.Context, q db.Queryer, projectID int64) error {
	_, err := q.Exec(ctx, `UPDATE repositories SET is_default = ? WHERE project_id = ?`, false, projectID)
	return err
}

// InsertScmRepository はリポジトリを作成して id を設定する。
func InsertScmRepository(ctx context.Context, q db.Queryer, r *domain.Repository) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO repositories (project_id, scm, url, root_url, login, path_encoding, log_encoding,
extra_info, identifier, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ProjectID, r.SCM, r.URL, nullIfEmpty(r.RootURL), nullIfEmpty(r.Login), nullIfEmpty(r.PathEncoding), nullIfEmpty(r.LogEncoding),
		extraInfoValue(r.ExtraInfo), nullIfEmpty(r.Identifier), r.IsDefault, db.NewTime(r.CreatedOn))
	if err != nil {
		return err
	}
	r.ID = id
	return nil
}

// UpdateScmRepository はリポジトリの属性を保存する。
func UpdateScmRepository(ctx context.Context, q db.Queryer, r *domain.Repository) error {
	_, err := q.Exec(ctx, `UPDATE repositories SET url = ?, root_url = ?, login = ?, path_encoding = ?, log_encoding = ?,
extra_info = ?, identifier = ?, is_default = ? WHERE id = ?`,
		r.URL, nullIfEmpty(r.RootURL), nullIfEmpty(r.Login), nullIfEmpty(r.PathEncoding), nullIfEmpty(r.LogEncoding),
		extraInfoValue(r.ExtraInfo), nullIfEmpty(r.Identifier), r.IsDefault, r.ID)
	return err
}

// UpdateScmRepositoryExtraInfo は extra_info だけを保存する（save(:validate => false)）。
func UpdateScmRepositoryExtraInfo(ctx context.Context, q db.Queryer, id int64, extra map[string]any) error {
	_, err := q.Exec(ctx, `UPDATE repositories SET extra_info = ? WHERE id = ?`, extraInfoValue(extra), id)
	return err
}

// UpdateScmRepositoryRootURL は update_attribute(:root_url, ...)。
func UpdateScmRepositoryRootURL(ctx context.Context, q db.Queryer, id int64, rootURL string) error {
	_, err := q.Exec(ctx, `UPDATE repositories SET root_url = ? WHERE id = ?`, nullIfEmpty(rootURL), id)
	return err
}

// ClearChangesets は clear_changesets（チェンジセットと関連行を削除する）。
func ClearChangesets(ctx context.Context, q db.Queryer, repoID int64) error {
	sub := `SELECT id FROM changesets WHERE repository_id = ?`
	for _, s := range []string{
		`DELETE FROM changeset_files WHERE changeset_id IN (` + sub + `)`,
		`DELETE FROM changesets_issues WHERE changeset_id IN (` + sub + `)`,
		`DELETE FROM changeset_parents WHERE changeset_id IN (` + sub + `)`,
		`DELETE FROM changeset_parents WHERE parent_id IN (` + sub + `)`,
	} {
		if _, err := q.Exec(ctx, s, repoID); err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `DELETE FROM changesets WHERE repository_id = ?`, repoID)
	return err
}

// DeleteScmRepository は repository.destroy（チェンジセットも削除する）。
func DeleteScmRepository(ctx context.Context, q db.Queryer, id int64) error {
	if err := ClearChangesets(ctx, q, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM repositories WHERE id = ?`, id)
	return err
}

// ---------------------------------------------------------------- チェンジセット

type changesetRow struct {
	ID           int64          `db:"id"`
	RepositoryID int64          `db:"repository_id"`
	Revision     string         `db:"revision"`
	Committer    sql.NullString `db:"committer"`
	CommittedAt  db.Time        `db:"committed_at"`
	Comments     sql.NullString `db:"comments"`
	CommitDate   db.NullDate    `db:"commit_date"`
	Scmid        sql.NullString `db:"scmid"`
	UserID       sql.NullInt64  `db:"user_id"`
	SCM          string         `db:"scm"`
}

const changesetCols = `changesets.id, changesets.repository_id, changesets.revision, changesets.committer, changesets.committed_at,
changesets.comments, changesets.commit_date, changesets.scmid, changesets.user_id, repositories.scm`

const changesetFrom = ` FROM changesets JOIN repositories ON repositories.id = changesets.repository_id`

// ChangesetOrder は has_many :changesets の既定の順序（committed_on DESC, id DESC）。
const ChangesetOrder = ` ORDER BY changesets.committed_at DESC, changesets.id DESC`

func (r *changesetRow) toDomain() *domain.Changeset {
	c := &domain.Changeset{ID: r.ID, RepositoryID: r.RepositoryID, Revision: r.Revision, Committer: r.Committer.String,
		CommittedOn: r.CommittedAt.Time, Comments: r.Comments.String, Scmid: r.Scmid.String, RepositorySCM: r.SCM}
	if r.CommitDate.Valid {
		d := r.CommitDate.Date.Time
		c.CommitDate = &d
	}
	if r.UserID.Valid {
		id := r.UserID.Int64
		c.UserID = &id
	}
	return c
}

func selectChangesets(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Changeset, error) {
	var rows []*changesetRow
	if err := q.Select(ctx, &rows, `SELECT `+changesetCols+changesetFrom+` WHERE `+where, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Changeset, len(rows))
	for i, r := range rows {
		out[i] = r.toDomain()
	}
	return out, nil
}

func getChangeset(ctx context.Context, q db.Queryer, where string, args ...any) (*domain.Changeset, error) {
	cs, err := selectChangesets(ctx, q, where+` LIMIT 1`, args...)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, ErrNotFound
	}
	return cs[0], nil
}

// GetChangeset は Changeset.find(id)。
func GetChangeset(ctx context.Context, q db.Queryer, id int64) (*domain.Changeset, error) {
	return getChangeset(ctx, q, `changesets.id = ?`, id)
}

// CountChangesets は repository.changesets.count。
func CountChangesets(ctx context.Context, q db.Queryer, repoID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM changesets WHERE repository_id = ?`, repoID)
	return n, err
}

// ListChangesets は repository.changesets.limit(limit).offset(offset)（limit 0 なら全件）。
func ListChangesets(ctx context.Context, q db.Queryer, repoID int64, limit, offset int) ([]*domain.Changeset, error) {
	where := `changesets.repository_id = ?` + ChangesetOrder
	args := []any{repoID}
	if limit > 0 {
		where += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	return selectChangesets(ctx, q, where, args...)
}

func placeholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// ChangesetsByScmids は changesets.where(:scmid => scmids)（既定の順序）。
func ChangesetsByScmids(ctx context.Context, q db.Queryer, repoID int64, scmids []string) ([]*domain.Changeset, error) {
	if len(scmids) == 0 {
		return nil, nil
	}
	args := append([]any{repoID}, stringArgs(scmids)...)
	return selectChangesets(ctx, q, `changesets.repository_id = ? AND changesets.scmid IN (`+placeholders(len(scmids))+`)`+ChangesetOrder, args...)
}

// ExistingChangesetScmids は scmids のうち DB にあるもの。
func ExistingChangesetScmids(ctx context.Context, q db.Queryer, repoID int64, scmids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(scmids) == 0 {
		return out, nil
	}
	var found []string
	args := append([]any{repoID}, stringArgs(scmids)...)
	if err := q.Select(ctx, &found, `SELECT scmid FROM changesets WHERE repository_id = ? AND scmid IN (`+placeholders(len(scmids))+`)`, args...); err != nil {
		return nil, err
	}
	for _, s := range found {
		out[s] = true
	}
	return out, nil
}

// FindChangesetByRevision は changesets.find_by(:revision => name)（既定の順序の先頭）。
func FindChangesetByRevision(ctx context.Context, q db.Queryer, repoID int64, revision string) (*domain.Changeset, error) {
	return getChangeset(ctx, q, `changesets.repository_id = ? AND changesets.revision = ?`+ChangesetOrder, repoID, revision)
}

// likeEscape は LIKE のワイルドカードを含めずに前方一致させるための値（Rails は LIKE の値をエスケープしないため素通し）。
func likePrefix(s string) string { return s + "%" }

// FindChangesetByScmidPrefix は changesets.where('scmid LIKE ?', "#{name}%").first。
func FindChangesetByScmidPrefix(ctx context.Context, q db.Queryer, repoID int64, prefix string) (*domain.Changeset, error) {
	return getChangeset(ctx, q, `changesets.repository_id = ? AND changesets.scmid LIKE ?`+ChangesetOrder, repoID, likePrefix(prefix))
}

// FindChangesetByRevisionPrefix は changesets.where("revision LIKE ?", s + '%').first。
func FindChangesetByRevisionPrefix(ctx context.Context, q db.Queryer, repoID int64, prefix string) (*domain.Changeset, error) {
	return getChangeset(ctx, q, `changesets.repository_id = ? AND changesets.revision LIKE ?`+ChangesetOrder, repoID, likePrefix(prefix))
}

// PreviousChangeset は Changeset#previous（id が小さい直前のもの）。
func PreviousChangeset(ctx context.Context, q db.Queryer, cs *domain.Changeset) (*domain.Changeset, error) {
	c, err := getChangeset(ctx, q, `changesets.id < ? AND changesets.repository_id = ? ORDER BY changesets.id DESC`, cs.ID, cs.RepositoryID)
	if err == ErrNotFound {
		return nil, nil
	}
	return c, err
}

// NextChangeset は Changeset#next（id が大きい直後のもの）。
func NextChangeset(ctx context.Context, q db.Queryer, cs *domain.Changeset) (*domain.Changeset, error) {
	c, err := getChangeset(ctx, q, `changesets.id > ? AND changesets.repository_id = ? ORDER BY changesets.id ASC`, cs.ID, cs.RepositoryID)
	if err == ErrNotFound {
		return nil, nil
	}
	return c, err
}

// ChangesetParents は changeset.parents（changeset_parents の挿入順 ≒ parent_id 順）。
func ChangesetParents(ctx context.Context, q db.Queryer, id int64) ([]*domain.Changeset, error) {
	return selectChangesets(ctx, q, `changesets.id IN (SELECT parent_id FROM changeset_parents WHERE changeset_id = ?) ORDER BY changesets.id`, id)
}

// ChangesetChildren は changeset.children。
func ChangesetChildren(ctx context.Context, q db.Queryer, id int64) ([]*domain.Changeset, error) {
	return selectChangesets(ctx, q, `changesets.id IN (SELECT changeset_id FROM changeset_parents WHERE parent_id = ?) ORDER BY changesets.id`, id)
}

// ChangesetParentScmids は changeset_id → 親の scmid の一覧（リビジョングラフ用。parents の includes）。
func ChangesetParentScmids(ctx context.Context, q db.Queryer, ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ChangesetID int64          `db:"changeset_id"`
		Scmid       sql.NullString `db:"scmid"`
	}
	if err := q.Select(ctx, &rows, `SELECT changeset_parents.changeset_id, changesets.scmid FROM changeset_parents
JOIN changesets ON changesets.id = changeset_parents.parent_id
WHERE changeset_parents.changeset_id IN (`+joinIDs(ids)+`) ORDER BY changeset_parents.changeset_id, changesets.id`); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ChangesetID] = append(out[r.ChangesetID], r.Scmid.String)
	}
	return out, nil
}

type changesetFileRow struct {
	ID           int64          `db:"id"`
	ChangesetID  int64          `db:"changeset_id"`
	Action       string         `db:"action"`
	Path         string         `db:"path"`
	FromPath     sql.NullString `db:"from_path"`
	FromRevision sql.NullString `db:"from_revision"`
	Revision     sql.NullString `db:"revision"`
	Branch       sql.NullString `db:"branch"`
}

// ChangesetFiles は changeset.filechanges（order が空なら id 順）。
func ChangesetFiles(ctx context.Context, q db.Queryer, changesetID int64, byPath bool, limit int) ([]*domain.ChangesetFile, error) {
	order := ` ORDER BY id`
	if byPath {
		order = ` ORDER BY path, id`
	}
	query := `SELECT id, changeset_id, action, path, from_path, from_revision, revision, branch FROM changeset_files WHERE changeset_id = ?` + order
	args := []any{changesetID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	var rows []*changesetFileRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.ChangesetFile, len(rows))
	for i, r := range rows {
		out[i] = &domain.ChangesetFile{ID: r.ID, ChangesetID: r.ChangesetID, Action: r.Action, Path: r.Path,
			FromPath: r.FromPath.String, FromRevision: r.FromRevision.String, Revision: r.Revision.String, Branch: r.Branch.String}
	}
	return out, nil
}

// InsertChangeset はチェンジセットを作成して id を設定する。
func InsertChangeset(ctx context.Context, q db.Queryer, c *domain.Changeset) error {
	var commitDate any
	if c.CommitDate != nil {
		commitDate = db.NewDate(c.CommitDate.Year(), c.CommitDate.Month(), c.CommitDate.Day())
	}
	var userID any
	if c.UserID != nil {
		userID = *c.UserID
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO changesets (repository_id, revision, committer, committed_at, comments, commit_date, scmid, user_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, c.RepositoryID, c.Revision, c.Committer, db.NewTime(c.CommittedOn), c.Comments, commitDate,
		nullIfEmpty(c.Scmid), userID)
	if err != nil {
		return err
	}
	c.ID = id
	return nil
}

// InsertChangesetParent は changeset_parents に行を追加する。
func InsertChangesetParent(ctx context.Context, q db.Queryer, changesetID, parentID int64) error {
	_, err := q.Exec(ctx, `INSERT INTO changeset_parents (changeset_id, parent_id) VALUES (?, ?)`, changesetID, parentID)
	return err
}

// InsertChangesetFile は Change.create。
func InsertChangesetFile(ctx context.Context, q db.Queryer, f *domain.ChangesetFile) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO changeset_files (changeset_id, action, path, from_path, from_revision, revision, branch)
VALUES (?, ?, ?, ?, ?, ?, ?)`, f.ChangesetID, f.Action, f.Path, nullIfEmpty(f.FromPath), nullIfEmpty(f.FromRevision),
		nullIfEmpty(f.Revision), nullIfEmpty(f.Branch))
	if err != nil {
		return err
	}
	f.ID = id
	return nil
}

// ChangesetIssueIDs は changeset.issues の id（id 順）。
func ChangesetIssueIDs(ctx context.Context, q db.Queryer, changesetID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT issue_id FROM changesets_issues WHERE changeset_id = ? ORDER BY issue_id`, changesetID)
	return ids, err
}

// AddChangesetIssue は changeset.issues << issue。
func AddChangesetIssue(ctx context.Context, q db.Queryer, changesetID, issueID int64) error {
	if ok, err := exists(ctx, q, `SELECT 1 FROM changesets_issues WHERE changeset_id = ? AND issue_id = ?`, changesetID, issueID); err != nil || ok {
		return err
	}
	_, err := q.Exec(ctx, `INSERT INTO changesets_issues (changeset_id, issue_id) VALUES (?, ?)`, changesetID, issueID)
	return err
}

// RemoveChangesetIssue は changeset.issues.delete(issue)。
func RemoveChangesetIssue(ctx context.Context, q db.Queryer, changesetID, issueID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM changesets_issues WHERE changeset_id = ? AND issue_id = ?`, changesetID, issueID)
	return err
}

// IssueLinkedToSameCommit は issue_linked_to_same_commit?（同じ url / root_url / scm の別リポジトリで
// 同じコミットが既に同じチケットに関連付いているか）。
func IssueLinkedToSameCommit(ctx context.Context, q db.Queryer, issueID int64, repo *domain.Repository, cs *domain.Changeset) (bool, error) {
	query := `SELECT 1 FROM changesets JOIN changesets_issues ON changesets_issues.changeset_id = changesets.id
JOIN repositories ON repositories.id = changesets.repository_id
WHERE changesets_issues.issue_id = ? AND repositories.url = ? AND COALESCE(repositories.root_url, '') = ? AND repositories.scm = ?`
	args := []any{issueID, repo.URL, repo.RootURL, repo.SCM}
	if cs.Scmid != "" {
		query += ` AND changesets.scmid = ?`
		args = append(args, cs.Scmid)
	} else {
		query += ` AND changesets.revision = ?`
		args = append(args, cs.Revision)
	}
	return exists(ctx, q, query, args...)
}

// Committer は repository.committers の 1 件（committer と user_id）。
type Committer struct {
	Committer string        `db:"committer"`
	UserID    sql.NullInt64 `db:"user_id"`
}

// RepositoryCommitters は Changeset.where(:repository_id => id).distinct.pluck(:committer, :user_id)。
func RepositoryCommitters(ctx context.Context, q db.Queryer, repoID int64) ([]Committer, error) {
	var rows []Committer
	err := q.Select(ctx, &rows, `SELECT DISTINCT COALESCE(committer, '') AS committer, user_id FROM changesets WHERE repository_id = ? ORDER BY 1, 2`, repoID)
	return rows, err
}

// UpdateCommitterUser は Changeset.where(repository_id, committer).update_all(user_id)。
func UpdateCommitterUser(ctx context.Context, q db.Queryer, repoID int64, committer string, userID *int64) error {
	var v any
	if userID != nil {
		v = *userID
	}
	_, err := q.Exec(ctx, `UPDATE changesets SET user_id = ? WHERE repository_id = ? AND COALESCE(committer, '') = ?`, v, repoID, committer)
	return err
}

// CommitterMappedUserID は changesets.where(:committer => committer).first の user_id（無ければ nil）。
func CommitterMappedUserID(ctx context.Context, q db.Queryer, repoID int64, committer string) (*int64, error) {
	var v sql.NullInt64
	err := q.Get(ctx, &v, `SELECT changesets.user_id FROM changesets LEFT JOIN principals ON principals.id = changesets.user_id
WHERE changesets.repository_id = ? AND changesets.committer = ?`+ChangesetOrder+` LIMIT 1`, repoID, committer)
	if err == sql.ErrNoRows || !v.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id := v.Int64
	return &id, nil
}

// FindUserIDByLoginOrMail は User.find_by_login(username) || User.find_by_mail(email)。
func FindUserIDByLoginOrMail(ctx context.Context, q db.Queryer, login, mail string) (*int64, error) {
	var id int64
	err := q.Get(ctx, &id, `SELECT principals.id FROM principals JOIN user_accounts ON user_accounts.principal_id = principals.id
WHERE principals.kind = 'user' AND LOWER(user_accounts.login) = LOWER(?) ORDER BY principals.id LIMIT 1`, login)
	if err == nil {
		return &id, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	if mail == "" {
		return nil, nil
	}
	err = q.Get(ctx, &id, `SELECT principals.id FROM principals JOIN email_addresses ON email_addresses.user_id = principals.id
WHERE principals.kind = 'user' AND LOWER(email_addresses.address) = LOWER(?) ORDER BY email_addresses.is_default DESC, principals.id LIMIT 1`, mail)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// CommitCount は日付ごとのコミット数・変更数（グラフ用。commit_date BETWEEN from AND to）。
type CommitCount struct {
	Date  db.Date `db:"commit_date"`
	Count int     `db:"n"`
}

// ChangesetCountsByDate は Changeset.where(repository_id, commit_date BETWEEN).group(:commit_date).count。
func ChangesetCountsByDate(ctx context.Context, q db.Queryer, repoID int64, from, to time.Time) ([]CommitCount, error) {
	var rows []CommitCount
	err := q.Select(ctx, &rows, `SELECT commit_date, COUNT(*) AS n FROM changesets
WHERE repository_id = ? AND commit_date BETWEEN ? AND ? AND commit_date IS NOT NULL GROUP BY commit_date`,
		repoID, from.Format(db.DateLayout), to.Format(db.DateLayout))
	return rows, err
}

// ChangeCountsByDate は Change.joins(:changeset).where(...).group(:commit_date).count。
func ChangeCountsByDate(ctx context.Context, q db.Queryer, repoID int64, from, to time.Time) ([]CommitCount, error) {
	var rows []CommitCount
	err := q.Select(ctx, &rows, `SELECT changesets.commit_date, COUNT(*) AS n FROM changeset_files
JOIN changesets ON changesets.id = changeset_files.changeset_id
WHERE changesets.repository_id = ? AND changesets.commit_date BETWEEN ? AND ? AND changesets.commit_date IS NOT NULL
GROUP BY changesets.commit_date`, repoID, from.Format(db.DateLayout), to.Format(db.DateLayout))
	return rows, err
}

// AuthorCount は committer, user_id ごとの件数（stats_by_author）。
type AuthorCount struct {
	Committer sql.NullString `db:"committer"`
	UserID    sql.NullInt64  `db:"user_id"`
	Count     int            `db:"n"`
}

// ChangesetCountsByAuthor は Changeset.where(repository_id).select("committer, user_id, count(*)").group("committer, user_id")。
func ChangesetCountsByAuthor(ctx context.Context, q db.Queryer, repoID int64) ([]AuthorCount, error) {
	var rows []AuthorCount
	err := q.Select(ctx, &rows, `SELECT committer, user_id, COUNT(*) AS n FROM changesets WHERE repository_id = ?
GROUP BY committer, user_id ORDER BY committer, user_id`, repoID)
	return rows, err
}

// ChangeCountsByAuthor は Change.joins(:changeset).where(...).select("committer, user_id, count(*)").group(...)。
func ChangeCountsByAuthor(ctx context.Context, q db.Queryer, repoID int64) ([]AuthorCount, error) {
	var rows []AuthorCount
	err := q.Select(ctx, &rows, `SELECT changesets.committer, changesets.user_id, COUNT(*) AS n FROM changeset_files
JOIN changesets ON changesets.id = changeset_files.changeset_id WHERE changesets.repository_id = ?
GROUP BY changesets.committer, changesets.user_id ORDER BY changesets.committer, changesets.user_id`, repoID)
	return rows, err
}

// ChangesetUserIDs は changesets.filter_map(&:user_id).uniq（既定の順序）。
func ChangesetUserIDs(ctx context.Context, q db.Queryer, repoID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT DISTINCT user_id FROM changesets WHERE repository_id = ? AND user_id IS NOT NULL ORDER BY user_id`, repoID)
	return ids, err
}

// RefIssueProject はチケットの id とプロジェクト（コミットメッセージからの参照判定用）。
type RefIssueProject struct {
	ID        int64 `db:"id"`
	ProjectID int64 `db:"project_id"`
}

// GetRefIssueProject は Issue.find_by_id(id)（id とプロジェクトのみ）。
func GetRefIssueProject(ctx context.Context, q db.Queryer, id int64) (*RefIssueProject, error) {
	var r RefIssueProject
	if err := q.Get(ctx, &r, `SELECT id, project_id FROM issues WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// ProjectsRelated は a.is_ancestor_of?(b) || a.is_descendant_of?(b)。
func ProjectsRelated(ctx context.Context, q db.Queryer, a, b int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM project_closure WHERE depth > 0 AND ((ancestor_id = ? AND descendant_id = ?) OR (ancestor_id = ? AND descendant_id = ?))`, a, b, b, a)
}

// SysProject は sys#projects の 1 件。
type SysProject struct {
	ID         int64          `db:"id"`
	Identifier string         `db:"identifier"`
	Name       string         `db:"name"`
	IsPublic   bool           `db:"is_public"`
	Status     int            `db:"status"`
	RepoID     sql.NullInt64  `db:"repo_id"`
	RepoURL    sql.NullString `db:"repo_url"`
}

// SysProjects は Project.active.has_module(:repository).order(identifier).preload(:repository)。
func SysProjects(ctx context.Context, q db.Queryer) ([]*SysProject, error) {
	var rows []*SysProject
	err := q.Select(ctx, &rows, `SELECT projects.id, projects.identifier, projects.name, projects.is_public, projects.status,
repositories.id AS repo_id, repositories.url AS repo_url
FROM projects LEFT JOIN repositories ON repositories.project_id = projects.id AND repositories.is_default = ?
WHERE projects.status = ? AND EXISTS (SELECT 1 FROM project_modules WHERE project_modules.project_id = projects.id AND project_modules.name = 'repository')
ORDER BY projects.identifier`, true, domain.ProjectStatusActive)
	return rows, err
}

// ActiveRepositoryProjectIDs は Project.active.has_module(:repository) の id。
func ActiveRepositoryProjectIDs(ctx context.Context, q db.Queryer) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT projects.id FROM projects WHERE projects.status = ?
AND EXISTS (SELECT 1 FROM project_modules WHERE project_modules.project_id = projects.id AND project_modules.name = 'repository')
ORDER BY projects.id`, domain.ProjectStatusActive)
	return ids, err
}
