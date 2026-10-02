package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Wiki は wikis 行（Redmine Wiki）。
type Wiki struct {
	ID        int64
	ProjectID int64
	StartPage string
}

// WikiPage は wiki_pages 行（Redmine WikiPage）。本文（WikiContent）は wiki_page_versions の
// current_version の行で、UpdatedOn / HasContent はその版の値を読み込んだもの（with_updated_on）。
type WikiPage struct {
	ID        int64
	WikiID    int64
	Title     string
	ParentID  *int64
	Protected bool
	// CurrentVersion は最新版の番号（WikiContent#version。0 なら本文なし）。
	CurrentVersion int
	CreatedAt      time.Time
	// UpdatedOn は最新版の更新日時（WikiPage#updated_on）。
	UpdatedOn time.Time
	// HasContent は本文があるか（page.content.present?）。
	HasContent bool

	// Project はページのプロジェクト（wiki.project。リポジトリが読み込む）。
	Project *Project
}

// PrettyTitle は WikiPage#pretty_title。
func (p *WikiPage) PrettyTitle() string { return WikiPrettyTitle(p.Title) }

// NewRecord は new_record?（id が未採番）。
func (p *WikiPage) NewRecord() bool { return p == nil || p.ID == 0 }

// WikiPrettyTitle は WikiPage.pretty_title。
func WikiPrettyTitle(s string) string { return strings.ReplaceAll(s, "_", " ") }

// WikiDefaultProtectedPages は WikiPage::DEFAULT_PROTECTED_PAGES。
var WikiDefaultProtectedPages = []string{"sidebar"}

// WikiContentVersion は wiki_page_versions 行（WikiContent / WikiContentVersion を統合したもの）。
type WikiContentVersion struct {
	ID       int64
	PageID   int64
	Version  int
	AuthorID *int64
	Text     string
	// Comments は comments（NULL なら nil）。
	Comments  *string
	UpdatedOn time.Time

	// Author は author（リポジトリが読み込む。author_id が NULL か見つからなければ nil）。
	Author *User
}

// CommentsString は comments.to_s。
func (v *WikiContentVersion) CommentsString() string {
	if v == nil || v.Comments == nil {
		return ""
	}
	return *v.Comments
}

var wikiTitleSpaceRe = regexp.MustCompile(`[ \t\n\v\f\r]+`)

// WikiTitleize は Wiki.titleize（空白を _ に、, . / ? ; | : を削除し、先頭を大文字にする）。
func WikiTitleize(title string) string {
	title = wikiTitleSpaceRe.ReplaceAllString(title, "_")
	title = strings.Map(func(r rune) rune {
		if strings.ContainsRune(",./?;|:", r) {
			return -1
		}
		return r
	}, title)
	if title == "" {
		return title
	}
	r, n := utf8.DecodeRuneInString(title)
	return strings.ToUpper(string(r)) + title[n:]
}

var wikiTitleFormatRe = regexp.MustCompile(`^[^,./?;|\s]*$`)

// ValidWikiTitleFormat は validates_format_of :title, :with => /\A[^,\.\/\?\;\|\s]*\z/。
func ValidWikiTitleFormat(title string) bool { return wikiTitleFormatRe.MatchString(title) }
