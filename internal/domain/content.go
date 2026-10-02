package domain

import "time"

// このファイルはニュースのコメント・文書・フォーラム（掲示板・メッセージ）・リアクションのモデル
// （Redmine の Comment / Document / Board / Message / Reaction）。News は user.go。

// Comment は news_comments 行（Redmine Comment。commented は常に News）。
type Comment struct {
	ID        int64
	NewsID    int64
	AuthorID  int64
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time

	Author *User
}

// Document は documents 行（Redmine Document）。
type Document struct {
	ID          int64
	ProjectID   int64
	CategoryID  int64
	Title       string
	Description string
	CreatedAt   time.Time

	Project  *Project
	Category *Enumeration
	// Attachments は document.attachments（created_on, id 順。author 込み）。
	Attachments []*Attachment
}

// UpdatedOn は Document#updated_on（最後の添付の作成日時、無ければ作成日時）。
func (d *Document) UpdatedOn() time.Time {
	if n := len(d.Attachments); n > 0 {
		return d.Attachments[n-1].CreatedOn
	}
	return d.CreatedAt
}

// Board は boards 行（Redmine Board）。
type Board struct {
	ID            int64
	ProjectID     int64
	ParentID      *int64
	Name          string
	Description   string
	Position      int
	TopicsCount   int
	MessagesCount int
	LastMessageID *int64

	Project     *Project
	LastMessage *Message
}

// String は Board#to_s。
func (b *Board) String() string { return b.Name }

// BoardTree は Board.board_tree(boards)（parent_id ごとに position 順の深さ優先）。
func BoardTree(boards []*Board) []BoardLevel {
	var out []BoardLevel
	var walk func(parent *int64, level int)
	walk = func(parent *int64, level int) {
		var children []*Board
		for _, b := range boards {
			if (parent == nil && b.ParentID == nil) || (parent != nil && b.ParentID != nil && *b.ParentID == *parent) {
				children = append(children, b)
			}
		}
		// sort_by(&:position)（Ruby の sort_by は安定でないが同順位はまれ。id 順を保つ）
		for i := 1; i < len(children); i++ {
			for j := i; j > 0 && children[j].Position < children[j-1].Position; j-- {
				children[j], children[j-1] = children[j-1], children[j]
			}
		}
		for _, c := range children {
			out = append(out, BoardLevel{Board: c, Level: level})
			id := c.ID
			walk(&id, level+1)
		}
	}
	walk(nil, 0)
	return out
}

// BoardLevel は board_tree の 1 要素（[board, level]）。
type BoardLevel struct {
	Board *Board
	Level int
}

// Message は messages 行（Redmine Message）。
type Message struct {
	ID           int64
	BoardID      int64
	ParentID     *int64
	Subject      string
	Content      string
	AuthorID     *int64
	RepliesCount int
	LastReplyID  *int64
	Locked       bool
	Sticky       bool
	CreatedAt    time.Time
	UpdatedAt    time.Time

	Author    *User
	Board     *Board
	LastReply *Message
	// Attachments は message.attachments（読み込んだ場合のみ）。
	Attachments []*Attachment
}

// IsTopic は parent_id が nil（トピック）か。
func (m *Message) IsTopic() bool { return m.ParentID == nil }

// RootID は message.root.id。
func (m *Message) RootID() int64 {
	if m.ParentID != nil {
		return *m.ParentID
	}
	return m.ID
}

// AuthorIs は message.author == user。
func (m *Message) AuthorIs(u *User) bool {
	return u != nil && m.AuthorID != nil && *m.AuthorID == u.ID
}

// Reaction は reactions 行（Redmine Reaction）。
type Reaction struct {
	ID            int64
	ReactableKind string
	ReactableID   int64
	UserID        int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
