// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Repository は repositories 行（app/models/repository.rb, repository/git.rb）。
// buropher は Git のみ対応する（D-15）が、移行データの他の SCM の行も読める。
type Repository struct {
	ID        int64
	ProjectID int64
	// SCM は subversion / git / mercurial / cvs / bazaar / filesystem。
	SCM          string
	URL          string
	RootURL      string
	Login        string
	PathEncoding string
	LogEncoding  string
	// ExtraInfo は extra_info（nil 可。heads / db_consistent / extra_report_last_commit ...）。
	ExtraInfo  map[string]any
	Identifier string
	IsDefault  bool
	CreatedOn  time.Time

	Project *Project
}

// RepositoryIdentifierMaxLength は Repository::IDENTIFIER_MAX_LENGTH。
const RepositoryIdentifierMaxLength = 255

// RepositoryReservedIdentifiers は validates_exclusion_of :identifier の値。
var RepositoryReservedIdentifiers = []string{"browse", "show", "entry", "raw", "changes", "annotate", "diff",
	"statistics", "graph", "revisions", "revision"}

// RepositoryIdentifierRe は validates_format_of :identifier（小文字・数字・- _。数字のみは不可）。
var RepositoryIdentifierRe = regexp.MustCompile(`\A[a-z0-9\-_]*\z`)

// RepositoryIdentifierAllDigits は数字のみ（(?!\d+$) の否定先読み部分）。
var RepositoryIdentifierAllDigits = regexp.MustCompile(`\A\d+\z`)

// SCMName は scm_name（Repository::<SCM>.scm_name）。
func (r *Repository) SCMName() string {
	switch r.SCM {
	case "subversion":
		return "Subversion"
	case "git":
		return "Git"
	case "mercurial":
		return "Mercurial"
	case "cvs":
		return "CVS"
	case "bazaar":
		return "Bazaar"
	case "filesystem":
		return "Filesystem"
	}
	return r.SCM
}

// IdentifierParam は identifier_param（identifier が空なら id）。
func (r *Repository) IdentifierParam() string {
	if strings.TrimSpace(r.Identifier) != "" {
		return r.Identifier
	}
	return strconv.FormatInt(r.ID, 10)
}

// IsGit は Repository::Git か。
func (r *Repository) IsGit() bool { return r.SCM == "git" }

// ReportLastCommit は Repository::Git#report_last_commit（extra_info の extra_report_last_commit が "0" 以外）。
func (r *Repository) ReportLastCommit() bool {
	if !r.IsGit() {
		return true
	}
	if r.ExtraInfo == nil {
		return false
	}
	v, ok := r.ExtraInfo["extra_report_last_commit"]
	if !ok || v == nil {
		return false
	}
	return rubyValueString(v) != "0"
}

// MergeExtraInfo は merge_extra_info(h)。
func (r *Repository) MergeExtraInfo(h map[string]any) {
	if r.ExtraInfo == nil {
		r.ExtraInfo = map[string]any{}
	}
	for k, v := range h {
		r.ExtraInfo[k] = v
	}
}

// SupportsDirectoryRevisions は supports_directory_revisions?（Git は true）。
func (r *Repository) SupportsDirectoryRevisions() bool { return r.IsGit() }

// SupportsRevisionGraph は supports_revision_graph?（Git は true）。
func (r *Repository) SupportsRevisionGraph() bool { return r.IsGit() }

// RepoLogEncoding は repo_log_encoding（Git は常に UTF-8）。
func (r *Repository) RepoLogEncoding() string {
	if r.IsGit() {
		return "UTF-8"
	}
	if e := strings.TrimSpace(r.LogEncoding); e != "" {
		return e
	}
	return "UTF-8"
}

// rubyValueString は to_s（JSON の数値・真偽値も文字列にする）。
func rubyValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

// Changeset は changesets 行（app/models/changeset.rb）。
type Changeset struct {
	ID           int64
	RepositoryID int64
	Revision     string
	Committer    string
	CommittedOn  time.Time
	Comments     string
	CommitDate   *time.Time
	Scmid        string
	UserID       *int64

	// RepositorySCM は repository の scm（identifier / format_identifier の判定用）。
	RepositorySCM string
	// User は changeset.user（読み込み済みなら）。
	User *User
}

// Identifier は identifier（Git / Mercurial は scmid、それ以外は revision）。
func (c *Changeset) Identifier() string {
	switch c.RepositorySCM {
	case "git", "mercurial":
		return c.Scmid
	}
	return c.Revision
}

// FormatIdentifier は format_identifier（Git は revision の先頭 8 文字）。
func (c *Changeset) FormatIdentifier() string {
	switch c.RepositorySCM {
	case "git":
		if len(c.Revision) > 8 {
			return c.Revision[:8]
		}
		return c.Revision
	case "mercurial":
		s := c.Scmid
		if len(s) > 12 {
			s = s[:12]
		}
		return c.Revision + ":" + s
	}
	return c.Identifier()
}

// CommitterName は committer.to_s.split('<').first（author の user が無い場合）。
func (c *Changeset) CommitterName() string {
	s := c.Committer
	if i := strings.Index(s, "<"); i >= 0 {
		s = s[:i]
	}
	return s
}

var reSplitComments = regexp.MustCompile(`(?s)\A(.+?)\r?\n(.*)$`)

// ShortComments は short_comments（1 行目）。
func (c *Changeset) ShortComments() string {
	s, _ := c.splitComments()
	return s
}

// LongComments は long_comments（2 行目以降を strip）。
func (c *Changeset) LongComments() string {
	_, l := c.splitComments()
	return l
}

// splitComments は split_comments（comments =~ /\A(.+?)\r?\n(.*)$/m）。
func (c *Changeset) splitComments() (string, string) {
	m := reSplitComments.FindStringSubmatch(c.Comments)
	if m == nil {
		return c.Comments, ""
	}
	return m[1], strings.TrimSpace(m[2])
}

// ChangesetFile は changeset_files 行（旧 changes。app/models/change.rb）。
type ChangesetFile struct {
	ID           int64
	ChangesetID  int64
	Action       string
	Path         string
	FromPath     string
	FromRevision string
	Revision     string
	Branch       string
}
