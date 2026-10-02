// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package redmine

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// 参照解決に使うデータ型（repository の Ref* をそのまま使う）。
type (
	Issue      = repository.RefIssue
	Wiki       = repository.RefWiki
	WikiPage   = repository.RefWikiPage
	Attachment = repository.RefAttachment
	Repository = repository.RefRepository
	Changeset  = repository.RefChangeset
	Named      = repository.RefNamed
	Message    = repository.RefMessage
)

// Store は textilizable がデータを読むためのインタフェース（User.current の可視性込み）。
// 見つからない・見えない場合は (nil, nil) を返す。エラーは描画を止めずにログへ記録される。
type Store interface {
	// AllowedTo は User.current.allowed_to?(perm, project)。
	AllowedTo(perm string, p *domain.Project) (bool, error)
	// UserGroupIDs は User.current.groups の id。
	UserGroupIDs() ([]int64, error)

	// ProjectByIdentifier は Project.find_by_identifier（状態を問わない）。
	ProjectByIdentifier(identifier string) (*domain.Project, error)
	// ProjectByName は Project.find_by_name。
	ProjectByName(name string) (*domain.Project, error)
	// ProjectByID は Project.find（状態を問わない）。
	ProjectByID(id int64) (*domain.Project, error)
	// VisibleProjectByIdentifier は Project.visible.find_by_identifier。
	VisibleProjectByIdentifier(identifier string) (*domain.Project, error)
	// VisibleProjectByID は Project.visible.find_by_id。
	VisibleProjectByID(id int64) (*domain.Project, error)
	// VisibleProjectByIdentifierOrLowerName は Project.visible.where("identifier = :s OR LOWER(name) = :s").first。
	VisibleProjectByIdentifierOrLowerName(s string) (*domain.Project, error)
	// ProjectIDsAllowedInTree は project.self_and_descendants.allowed_to(User.current, perm).pluck(:id)。
	ProjectIDsAllowedInTree(p *domain.Project, perm string) ([]int64, error)

	// Wiki は project.wiki。
	Wiki(projectID int64) (*Wiki, error)
	// FindWikiPage は Wiki#find_page(title)（title は titleize 済み。リダイレクトも辿る）。
	FindWikiPage(w *Wiki, title string) (*WikiPage, error)
	// WikiPageChildren は page.children（title 順）。
	WikiPageChildren(pageID int64) ([]*WikiPage, error)
	// WikiPageText は page.content.text。
	WikiPageText(pageID int64) (string, bool, error)
	// RecentWikiPages は recent_pages マクロのページ一覧。
	RecentWikiPages(projectIDs []int64, since time.Time, limit int) ([]*WikiPage, error)

	// VisibleIssue は Issue.visible.find_by_id。
	VisibleIssue(id int64) (*Issue, error)
	// Repositories は project.repositories。
	Repositories(projectID int64) ([]*Repository, error)
	// VisibleChangeset は Changeset.visible.find_by_repository_id_and_revision。
	VisibleChangeset(repoID int64, revision string) (*Changeset, error)
	// VisibleChangesetByScmidPrefix は Changeset.visible.where("scmid LIKE ?", prefix%).first。
	VisibleChangesetByScmidPrefix(repoID int64, prefix string) (*Changeset, error)

	// VisibleNamed は Document / Version / Board / News の visible.find_by_id（kind は document|version|board|news）。
	VisibleNamed(kind string, id int64) (*Named, error)
	// VisibleNamedByName は project.documents.visible.find_by_title 等。
	VisibleNamedByName(kind string, projectID int64, name string) (*Named, error)
	// VisibleMessage は Message.visible.find_by_id。
	VisibleMessage(id int64) (*Message, error)
	// VisibleUser は User.visible.find_by(id:, type: 'User')。
	VisibleUser(id int64) (*domain.User, error)
	// VisibleUserByLogin は User.visible.find_by("LOWER(login) = ? AND type = 'User'")。login は小文字化済み。
	VisibleUserByLogin(login string) (*domain.User, error)

	// Attachments は container.attachments（kind は issue|wiki_page|news|message|document|version|project）。
	Attachments(kind string, id int64) ([]*Attachment, error)
}

// DBStore は Store の DB 実装（internal/repository + internal/authz）。
type DBStore struct {
	Ctx context.Context
	Q   db.Queryer
	// Az は User.current の Authorizer。
	Az *authz.Authorizer

	conds map[string]string
}

// NewDBStore は DBStore を作る。
func NewDBStore(ctx context.Context, q db.Queryer, az *authz.Authorizer) *DBStore {
	if ctx == nil {
		ctx = context.Background()
	}
	return &DBStore{Ctx: ctx, Q: q, Az: az, conds: map[string]string{}}
}

// none は ErrNotFound を (nil, nil) に変換する。
func none[T any](v *T, err error) (*T, error) {
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// cond は Project.allowed_to_condition(User.current, perm)（キャッシュ付き）。
func (s *DBStore) cond(perm string) (string, error) {
	if c, ok := s.conds[perm]; ok {
		return c, nil
	}
	var c string
	var err error
	switch perm {
	case "#issue":
		c, err = s.Az.IssueVisibleCondition(s.Ctx, authz.ConditionOptions{})
	case "#principal":
		c, err = s.Az.PrincipalVisibleCondition(s.Ctx)
	default:
		c, err = s.Az.AllowedToCondition(s.Ctx, perm, authz.ConditionOptions{}, nil)
	}
	if err != nil {
		return "", err
	}
	s.conds[perm] = c
	return c, nil
}

func (s *DBStore) AllowedTo(perm string, p *domain.Project) (bool, error) {
	return s.Az.AllowedTo(s.Ctx, domain.Perm(perm), p)
}

func (s *DBStore) UserGroupIDs() ([]int64, error) {
	u := s.Az.User()
	if u == nil || !u.Logged() {
		return nil, nil
	}
	return repository.UserGroupIDs(s.Ctx, s.Q, u.ID)
}

func (s *DBStore) ProjectByIdentifier(identifier string) (*domain.Project, error) {
	return none(repository.FindProjectWhere(s.Ctx, s.Q, `projects.identifier = ?`, identifier))
}

func (s *DBStore) ProjectByName(name string) (*domain.Project, error) {
	return none(repository.FindProjectWhere(s.Ctx, s.Q, `projects.name = ?`, name))
}

func (s *DBStore) ProjectByID(id int64) (*domain.Project, error) {
	return none(repository.GetProject(s.Ctx, s.Q, id))
}

func (s *DBStore) visibleProject(where string, args ...any) (*domain.Project, error) {
	c, err := s.cond("view_project")
	if err != nil {
		return nil, err
	}
	return none(repository.FindProjectWhere(s.Ctx, s.Q, "("+c+") AND ("+where+")", args...))
}

func (s *DBStore) VisibleProjectByIdentifier(identifier string) (*domain.Project, error) {
	return s.visibleProject(`projects.identifier = ?`, identifier)
}

func (s *DBStore) VisibleProjectByID(id int64) (*domain.Project, error) {
	return s.visibleProject(`projects.id = ?`, id)
}

func (s *DBStore) VisibleProjectByIdentifierOrLowerName(v string) (*domain.Project, error) {
	return s.visibleProject(`projects.identifier = ? OR LOWER(projects.name) = ?`, v, v)
}

func (s *DBStore) ProjectIDsAllowedInTree(p *domain.Project, perm string) ([]int64, error) {
	c, err := s.cond(perm)
	if err != nil {
		return nil, err
	}
	var ids []int64
	err = s.Q.Select(s.Ctx, &ids, `SELECT projects.id FROM projects WHERE projects.id IN
  (SELECT descendant_id FROM project_closure WHERE ancestor_id = ?) AND (`+c+`) ORDER BY projects.id`, p.ID)
	return ids, err
}

func (s *DBStore) Wiki(projectID int64) (*Wiki, error) {
	return none(repository.FindWikiByProject(s.Ctx, s.Q, projectID))
}

func (s *DBStore) FindWikiPage(w *Wiki, title string) (*WikiPage, error) {
	p, err := none(repository.FindWikiPageByTitle(s.Ctx, s.Q, w.ID, title))
	if p != nil || err != nil {
		return p, err
	}
	return none(repository.FindWikiRedirect(s.Ctx, s.Q, w.ID, title))
}

func (s *DBStore) WikiPageChildren(pageID int64) ([]*WikiPage, error) {
	return repository.WikiPageChildren(s.Ctx, s.Q, pageID)
}

func (s *DBStore) WikiPageText(pageID int64) (string, bool, error) {
	return repository.WikiPageText(s.Ctx, s.Q, pageID)
}

func (s *DBStore) RecentWikiPages(projectIDs []int64, since time.Time, limit int) ([]*WikiPage, error) {
	return repository.RecentWikiPages(s.Ctx, s.Q, projectIDs, since, limit)
}

func (s *DBStore) VisibleIssue(id int64) (*Issue, error) {
	c, err := s.cond("#issue")
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleIssue(s.Ctx, s.Q, id, c))
}

func (s *DBStore) Repositories(projectID int64) ([]*Repository, error) {
	return repository.ProjectRepositories(s.Ctx, s.Q, projectID)
}

func (s *DBStore) VisibleChangeset(repoID int64, revision string) (*Changeset, error) {
	c, err := s.cond("view_changesets")
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleChangeset(s.Ctx, s.Q, repoID, revision, c))
}

func (s *DBStore) VisibleChangesetByScmidPrefix(repoID int64, prefix string) (*Changeset, error) {
	c, err := s.cond("view_changesets")
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleChangesetByScmidPrefix(s.Ctx, s.Q, repoID, prefix, c))
}

var namedPerm = map[string]string{"document": "view_documents", "version": "view_issues", "board": "view_messages", "news": "view_news"}

func (s *DBStore) VisibleNamed(kind string, id int64) (*Named, error) {
	c, err := s.cond(namedPerm[kind])
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleNamed(s.Ctx, s.Q, kind, id, c))
}

func (s *DBStore) VisibleNamedByName(kind string, projectID int64, name string) (*Named, error) {
	c, err := s.cond(namedPerm[kind])
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleNamedByName(s.Ctx, s.Q, kind, projectID, name, c))
}

func (s *DBStore) VisibleMessage(id int64) (*Message, error) {
	c, err := s.cond("view_messages")
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleMessage(s.Ctx, s.Q, id, c))
}

func (s *DBStore) VisibleUser(id int64) (*domain.User, error) {
	c, err := s.cond("#principal")
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleUserWhere(s.Ctx, s.Q, c, `principals.id = ?`, id))
}

func (s *DBStore) VisibleUserByLogin(login string) (*domain.User, error) {
	c, err := s.cond("#principal")
	if err != nil {
		return nil, err
	}
	return none(repository.FindVisibleUserWhere(s.Ctx, s.Q, c, repository.LowerLoginCondition(), strings.ToLower(login)))
}

func (s *DBStore) Attachments(kind string, id int64) ([]*Attachment, error) {
	return repository.ContainerAttachments(s.Ctx, s.Q, kind, id)
}

// LoadObject は種類と id から Object を組み立てる（プレビュー・テスト用）。
// kind は issue|journal|wiki_content|news|message|document|version|project。
func (s *DBStore) LoadObject(kind string, id int64) (*Object, error) {
	o, err := repository.ResolveObject(s.Ctx, s.Q, kind, id)
	if err != nil {
		return nil, err
	}
	obj := &Object{Kind: kind, ID: id, JournalizedID: o.JournalizedID}
	if kind == "project" {
		p, err := repository.GetProject(s.Ctx, s.Q, id)
		if err != nil {
			return nil, err
		}
		obj.Self = p
		return obj, nil
	}
	if o.ProjectID != 0 {
		p, err := repository.GetProject(s.Ctx, s.Q, o.ProjectID)
		if err != nil {
			return nil, err
		}
		obj.Project = p
	}
	if o.PageID != 0 {
		obj.Page, err = repository.GetWikiPage(s.Ctx, s.Q, o.PageID)
		if err != nil {
			return nil, err
		}
	}
	return obj, nil
}
