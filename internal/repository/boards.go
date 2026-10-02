package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはフォーラム（Board）とメッセージ（Message）の読み書き。

type boardRow struct {
	ID            int64         `db:"id"`
	ProjectID     int64         `db:"project_id"`
	ParentID      sql.NullInt64 `db:"parent_id"`
	Name          string        `db:"name"`
	Description   string        `db:"description"`
	Position      int           `db:"position"`
	TopicsCount   int           `db:"topics_count"`
	MessagesCount int           `db:"messages_count"`
	LastMessageID sql.NullInt64 `db:"last_message_id"`
}

func nullInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func ptrNullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func (r *boardRow) board() *domain.Board {
	return &domain.Board{ID: r.ID, ProjectID: r.ProjectID, ParentID: nullInt64Ptr(r.ParentID), Name: r.Name,
		Description: r.Description, Position: r.Position, TopicsCount: r.TopicsCount, MessagesCount: r.MessagesCount,
		LastMessageID: nullInt64Ptr(r.LastMessageID)}
}

const boardSelect = `SELECT id, project_id, parent_id, name, description, position, topics_count, messages_count, last_message_id FROM boards`

// GetBoard は Board.find(id)。
func GetBoard(ctx context.Context, q db.Queryer, id int64) (*domain.Board, error) {
	var r boardRow
	if err := q.Get(ctx, &r, boardSelect+` WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return r.board(), nil
}

// ProjectBoardList は project.boards（position 順）。withLastMessage なら last_message と author を読み込む。
func ProjectBoardList(ctx context.Context, q db.Queryer, projectID int64, withLastMessage bool) ([]*domain.Board, error) {
	var rows []boardRow
	if err := q.Select(ctx, &rows, boardSelect+` WHERE project_id = ? ORDER BY position, id`, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.Board, len(rows))
	var mids []int64
	for i := range rows {
		out[i] = rows[i].board()
		if out[i].LastMessageID != nil {
			mids = append(mids, *out[i].LastMessageID)
		}
	}
	if withLastMessage && len(mids) > 0 {
		ms, err := messagesByIDs(ctx, q, mids)
		if err != nil {
			return nil, err
		}
		for _, b := range out {
			if b.LastMessageID != nil {
				b.LastMessage = ms[*b.LastMessageID]
			}
		}
	}
	return out, nil
}

// InsertBoard は boards 行を作成する（position は呼び出し側が決める）。
func InsertBoard(ctx context.Context, q db.Queryer, b *domain.Board) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO boards (project_id, parent_id, name, description, position) VALUES (?, ?, ?, ?, ?)`,
		b.ProjectID, ptrNullInt64(b.ParentID), b.Name, b.Description, b.Position)
	if err != nil {
		return err
	}
	b.ID = id
	return nil
}

// UpdateBoard は name / description / parent_id / position を保存する。
func UpdateBoard(ctx context.Context, q db.Queryer, b *domain.Board) error {
	_, err := q.Exec(ctx, `UPDATE boards SET name = ?, description = ?, parent_id = ?, position = ? WHERE id = ?`,
		b.Name, b.Description, ptrNullInt64(b.ParentID), b.Position, b.ID)
	return err
}

// DeleteBoard は board を削除する（acts_as_tree :dependent => :nullify で子の parent_id を NULL に、
// messages は CASCADE。メッセージの添付・ウォッチャー・リアクションは削除したメッセージ id を返して呼び出し側で消す）。
func DeleteBoard(ctx context.Context, q db.Queryer, id int64) ([]int64, error) {
	var mids []int64
	if err := q.Select(ctx, &mids, `SELECT id FROM messages WHERE board_id = ? ORDER BY id`, id); err != nil {
		return nil, err
	}
	if err := deleteMessageDependents(ctx, q, mids); err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `UPDATE boards SET parent_id = NULL WHERE parent_id = ?`, id); err != nil {
		return nil, err
	}
	if err := DeleteWatchers(ctx, q, "board", []int64{id}); err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `UPDATE boards SET last_message_id = NULL WHERE id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `UPDATE messages SET last_reply_id = NULL, parent_id = NULL WHERE board_id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `DELETE FROM messages WHERE board_id = ?`, id); err != nil {
		return nil, err
	}
	_, err := q.Exec(ctx, `DELETE FROM boards WHERE id = ?`, id)
	return mids, err
}

// ResetBoardCounters は Board.reset_counters!(board_id)。
func ResetBoardCounters(ctx context.Context, q db.Queryer, boardID int64) error {
	_, err := q.Exec(ctx, `UPDATE boards SET
  topics_count = (SELECT COUNT(*) FROM messages WHERE board_id = ? AND parent_id IS NULL),
  messages_count = (SELECT COUNT(*) FROM messages WHERE board_id = ?),
  last_message_id = (SELECT MAX(id) FROM messages WHERE board_id = ?)
WHERE id = ?`, boardID, boardID, boardID, boardID)
	return err
}

// ---------------------------------------------------------------- messages

type messageRow struct {
	ID           int64          `db:"id"`
	BoardID      int64          `db:"board_id"`
	ParentID     sql.NullInt64  `db:"parent_id"`
	Subject      string         `db:"subject"`
	Content      sql.NullString `db:"content"`
	AuthorID     sql.NullInt64  `db:"author_id"`
	RepliesCount int            `db:"replies_count"`
	LastReplyID  sql.NullInt64  `db:"last_reply_id"`
	Locked       bool           `db:"locked"`
	Sticky       bool           `db:"sticky"`
	CreatedAt    db.Time        `db:"created_at"`
	UpdatedAt    db.Time        `db:"updated_at"`
}

func (r *messageRow) message() *domain.Message {
	return &domain.Message{ID: r.ID, BoardID: r.BoardID, ParentID: nullInt64Ptr(r.ParentID), Subject: r.Subject,
		Content: r.Content.String, AuthorID: nullInt64Ptr(r.AuthorID), RepliesCount: r.RepliesCount,
		LastReplyID: nullInt64Ptr(r.LastReplyID), Locked: r.Locked, Sticky: r.Sticky,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

const messageCols = `messages.id, messages.board_id, messages.parent_id, messages.subject, messages.content, messages.author_id,
  messages.replies_count, messages.last_reply_id, messages.locked, messages.sticky, messages.created_at, messages.updated_at`

func selectMessages(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Message, error) {
	var rows []messageRow
	if err := q.Select(ctx, &rows, `SELECT `+messageCols+` FROM messages `+where, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Message, len(rows))
	for i := range rows {
		out[i] = rows[i].message()
	}
	if err := preloadMessageAuthors(ctx, q, out); err != nil {
		return nil, err
	}
	return out, nil
}

func preloadMessageAuthors(ctx context.Context, q db.Queryer, ms []*domain.Message) error {
	var uids []int64
	for _, m := range ms {
		if m.AuthorID != nil {
			uids = append(uids, *m.AuthorID)
		}
	}
	if len(uids) == 0 {
		return nil
	}
	users, err := UsersByIDs(ctx, q, uids)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.AuthorID != nil {
			m.Author = users[*m.AuthorID]
		}
	}
	return nil
}

func messagesByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*domain.Message, error) {
	query, args, err := db.In(`WHERE messages.id IN (?)`, ids)
	if err != nil {
		return nil, err
	}
	ms, err := selectMessages(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	out := map[int64]*domain.Message{}
	for _, m := range ms {
		out[m.ID] = m
	}
	return out, nil
}

// GetMessage は Message.find(id)（author を読み込む）。
func GetMessage(ctx context.Context, q db.Queryer, id int64) (*domain.Message, error) {
	ms, err := selectMessages(ctx, q, `WHERE messages.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrNotFound
	}
	return ms[0], nil
}

// BoardMessage は @board.messages.find(id)。
func BoardMessage(ctx context.Context, q db.Queryer, boardID, id int64) (*domain.Message, error) {
	ms, err := selectMessages(ctx, q, `WHERE messages.id = ? AND messages.board_id = ?`, id, boardID)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrNotFound
	}
	return ms[0], nil
}

// CountBoardTopics は board.topics.count。
func CountBoardTopics(ctx context.Context, q db.Queryer, boardID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM messages WHERE board_id = ? AND parent_id IS NULL`, boardID)
	return n, err
}

// BoardTopics は board.topics.reorder(sticky: :desc).order(sort_clause).limit.offset
// （author と last_reply（author 込み）を読み込む）。order は検証済みの ORDER BY 断片（空なら created_at DESC）。
func BoardTopics(ctx context.Context, q db.Queryer, boardID int64, order []string, limit, offset int) ([]*domain.Message, error) {
	ob := []string{"messages.sticky DESC"}
	ob = append(ob, order...)
	// has_many :messages の order(created_on DESC) は reorder で消える
	ms, err := selectMessages(ctx, q, `WHERE messages.board_id = ? AND messages.parent_id IS NULL ORDER BY `+strings.Join(ob, ", ")+` `+
		q.Dialect().LimitOffset(limit, offset), boardID)
	if err != nil {
		return nil, err
	}
	var rids []int64
	for _, m := range ms {
		if m.LastReplyID != nil {
			rids = append(rids, *m.LastReplyID)
		}
	}
	if len(rids) > 0 {
		rs, err := messagesByIDs(ctx, q, rids)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			if m.LastReplyID != nil {
				m.LastReply = rs[*m.LastReplyID]
			}
		}
	}
	return ms, nil
}

// BoardMessagesForFeed は board.messages.reorder(id: :desc).limit(n)。
func BoardMessagesForFeed(ctx context.Context, q db.Queryer, boardID int64, limit int) ([]*domain.Message, error) {
	return selectMessages(ctx, q, `WHERE messages.board_id = ? ORDER BY messages.id DESC `+q.Dialect().LimitOffset(limit, 0), boardID)
}

// CountReplies は topic.children.count。
func CountReplies(ctx context.Context, q db.Queryer, topicID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM messages WHERE parent_id = ?`, topicID)
	return n, err
}

// CountRepliesBefore は topic.children.where("id < ?", r).count。
func CountRepliesBefore(ctx context.Context, q db.Queryer, topicID, r int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM messages WHERE parent_id = ? AND id < ?`, topicID, r)
	return n, err
}

// TopicReplies は topic.children.reorder(created_on ASC, id ASC).limit.offset（author と attachments を読み込む）。
func TopicReplies(ctx context.Context, q db.Queryer, topicID int64, limit, offset int) ([]*domain.Message, error) {
	ms, err := selectMessages(ctx, q, `WHERE messages.parent_id = ? ORDER BY messages.created_at ASC, messages.id ASC `+
		q.Dialect().LimitOffset(limit, offset), topicID)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if m.Attachments, err = ContainerAttachmentList(ctx, q, domain.AttachmentContainerMessage, m.ID); err != nil {
			return nil, err
		}
	}
	return ms, nil
}

// InsertMessage は messages 行を作成する。
func InsertMessage(ctx context.Context, q db.Queryer, m *domain.Message) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO messages (board_id, parent_id, subject, content, author_id, replies_count,
  locked, sticky, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		m.BoardID, ptrNullInt64(m.ParentID), m.Subject, m.Content, ptrNullInt64(m.AuthorID), m.Locked, m.Sticky,
		db.NewTime(m.CreatedAt), db.NewTime(m.UpdatedAt))
	if err != nil {
		return err
	}
	m.ID = id
	return nil
}

// UpdateMessage は subject / content / locked / sticky / board_id / updated_at を保存する。
func UpdateMessage(ctx context.Context, q db.Queryer, m *domain.Message) error {
	_, err := q.Exec(ctx, `UPDATE messages SET board_id = ?, subject = ?, content = ?, locked = ?, sticky = ?, updated_at = ? WHERE id = ?`,
		m.BoardID, m.Subject, m.Content, m.Locked, m.Sticky, db.NewTime(m.UpdatedAt), m.ID)
	return err
}

// MoveTopicToBoard は update_messages_board（トピックと返信の board_id を変える）。
func MoveTopicToBoard(ctx context.Context, q db.Queryer, rootID, boardID int64) error {
	_, err := q.Exec(ctx, `UPDATE messages SET board_id = ? WHERE id = ? OR parent_id = ?`, boardID, rootID, rootID)
	return err
}

// UpdateTopicCounters は Message#reset_counters! の親側（replies_count（counter_cache）と last_reply_id）。
func UpdateTopicCounters(ctx context.Context, q db.Queryer, topicID int64) error {
	_, err := q.Exec(ctx, `UPDATE messages SET
  replies_count = (SELECT COUNT(*) FROM messages c WHERE c.parent_id = ?),
  last_reply_id = (SELECT MAX(c.id) FROM messages c WHERE c.parent_id = ?)
WHERE id = ?`, topicID, topicID, topicID)
	return err
}

// DeleteMessage はメッセージ（トピックなら返信も）を削除し、削除したメッセージ id を返す
// （acts_as_tree の dependent: :destroy。添付は呼び出し側が DeleteContainerAttachments で消す）。
func DeleteMessage(ctx context.Context, q db.Queryer, id int64) ([]int64, error) {
	ids := []int64{id}
	var children []int64
	if err := q.Select(ctx, &children, `SELECT id FROM messages WHERE parent_id = ? ORDER BY id`, id); err != nil {
		return nil, err
	}
	ids = append(ids, children...)
	if err := deleteMessageDependents(ctx, q, ids); err != nil {
		return nil, err
	}
	query, args, err := db.In(`UPDATE boards SET last_message_id = NULL WHERE last_message_id IN (?)`, ids)
	if err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return nil, err
	}
	query, args, err = db.In(`UPDATE messages SET last_reply_id = NULL WHERE last_reply_id IN (?)`, ids)
	if err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `DELETE FROM messages WHERE parent_id = ?`, id); err != nil {
		return nil, err
	}
	_, err = q.Exec(ctx, `DELETE FROM messages WHERE id = ?`, id)
	return ids, err
}

// deleteMessageDependents はメッセージのウォッチャー・リアクションを消す。
func deleteMessageDependents(ctx context.Context, q db.Queryer, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := DeleteWatchers(ctx, q, "message", ids); err != nil {
		return err
	}
	query, args, err := db.In(`DELETE FROM reactions WHERE reactable_kind = 'message' AND reactable_id IN (?)`, ids)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, query, args...)
	return err
}
