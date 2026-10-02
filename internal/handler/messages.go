package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
)

// MessagesController（app/controllers/messages_controller.rb）。
//
//	menu_item :boards
//	default_search_scope :messages
//	before_action :find_board, :only => [:new, :preview]
//	before_action :find_attachments, :only => [:preview]
//	before_action :find_message, :except => [:new, :preview]
//	before_action :authorize, :except => [:preview, :edit, :destroy]
var MessagesController = &Controller{Name: "messages", MainMenu: true, DefaultSearchScope: "messages",
	MenuItem: func(string) string { return "boards" }}

// messagesRepliesPerPage は REPLIES_PER_PAGE。
const messagesRepliesPerPage = 25

// routesMessages は messages コントローラのルートを登録する。
//
//	match 'boards/:board_id/topics/new', :to => 'messages#new', :via => [:get, :post]
//	get 'boards/:board_id/topics/:id', :to => 'messages#show'
//	match 'boards/:board_id/topics/quote/:id', :to => 'messages#quote', :via => [:get, :post]
//	get 'boards/:board_id/topics/:id/edit', :to => 'messages#edit'
//	post 'boards/:board_id/topics/preview', :to => 'messages#preview'
//	post 'boards/:board_id/topics/:id/replies', :to => 'messages#reply'
//	post 'boards/:board_id/topics/:id/edit', :to => 'messages#edit'
//	post 'boards/:board_id/topics/:id/destroy', :to => 'messages#destroy'
func (a *App) routesMessages(r Router) {
	m := MessagesController
	findBoard := Before(a.findMessageBoard)
	findMessage := Before(a.findMessage)
	base := "/boards/{board_id}/topics"
	a.Handle(r, http.MethodGet, base+"/new", m, "new", a.MessagesNew, findBoard, Authorize())
	a.Handle(r, http.MethodPost, base+"/new", m, "new", a.MessagesNew, findBoard, Authorize())
	a.Handle(r, http.MethodPost, base+"/preview", m, "preview", a.MessagesPreview, findBoard, Before(findAttachments))
	a.Handle(r, http.MethodGet, base+"/quote/{id}", m, "quote", a.MessagesQuote, findMessage, Authorize())
	a.Handle(r, http.MethodPost, base+"/quote/{id}", m, "quote", a.MessagesQuote, findMessage, Authorize())
	a.Handle(r, http.MethodGet, base+"/{id}", m, "show", a.MessagesShow, findMessage, Authorize())
	a.Handle(r, http.MethodGet, base+"/{id}/edit", m, "edit", a.MessagesEdit, findMessage)
	a.Handle(r, http.MethodPost, base+"/{id}/edit", m, "edit", a.MessagesEdit, findMessage)
	a.Handle(r, http.MethodPost, base+"/{id}/replies", m, "reply", a.MessagesReply, findMessage, Authorize())
	a.Handle(r, http.MethodPost, base+"/{id}/destroy", m, "destroy", a.MessagesDestroy, findMessage)
}

type messageCtxKey struct{}

// messageState は find_board / find_message の結果（@board, @message, @topic）。
type messageState struct {
	Board   *domain.Board
	Message *domain.Message
	Topic   *domain.Message
}

func (c *Req) messageState() *messageState {
	s, _ := c.value(messageCtxKey{}).(*messageState)
	if s == nil {
		s = &messageState{}
	}
	return s
}

// messageForm は @message / @reply（form_for のモデル）。
type messageForm struct {
	contentForm
	*domain.Message
	// canEdit は user.allowed_to?(:edit_messages, message.project)（sticky / locked / board_id の safe_attribute?）。
	canEdit bool
}

// SafeAttribute は safe_attribute?(name)。
func (f *messageForm) SafeAttribute(name string) bool {
	switch name {
	case "subject", "content":
		return true
	case "locked", "sticky", "board_id":
		return f.canEdit
	}
	return false
}

func (a *App) newMessageForm(c *Req, m *domain.Message) *messageForm {
	return &messageForm{contentForm: newContentForm(c, "message", m.ID), Message: m,
		canEdit: c.Project != nil && c.AllowedTo(domain.Perm("edit_messages"), c.Project)}
}

// findMessageBoard は find_board（Board.find(params[:board_id]); @project = @board.project）。
func (a *App) findMessageBoard(c *Req) {
	a.loadMessageBoard(c)
}

func (a *App) loadMessageBoard(c *Req) *domain.Board {
	id, ok := paramInt64(c, "board_id")
	if !ok {
		c.Render404("")
		return nil
	}
	b, err := repository.GetBoard(c.Ctx(), a.DB, id)
	if err != nil {
		a.notFoundOr500(c, "find board", err)
		return nil
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, b.ProjectID)
	if err != nil {
		a.notFoundOr500(c, "board project", err)
		return nil
	}
	b.Project = p
	c.Project = p
	c.setValue(messageCtxKey{}, &messageState{Board: b})
	return b
}

// findMessage は find_message（@board.messages.find(params[:id]); @topic = @message.root）。
func (a *App) findMessage(c *Req) {
	b := a.loadMessageBoard(c)
	if b == nil {
		return
	}
	id, ok := paramInt64(c, "id")
	if !ok {
		c.Render404("")
		return
	}
	m, err := repository.BoardMessage(c.Ctx(), a.DB, b.ID, id)
	if err != nil {
		a.notFoundOr500(c, "find message", err)
		return
	}
	m.Board = b
	topic := m
	if m.ParentID != nil {
		if topic, err = repository.GetMessage(c.Ctx(), a.DB, *m.ParentID); err != nil {
			a.notFoundOr500(c, "find topic", err)
			return
		}
		topic.Board = b
	}
	c.setValue(messageCtxKey{}, &messageState{Board: b, Message: m, Topic: topic})
}

// messageEditableBy は Message#editable_by?(User.current)。
func (c *Req) messageEditableBy(m *domain.Message) bool {
	u := c.User
	if !u.Logged() {
		return false
	}
	return c.AllowedTo(domain.Perm("edit_messages"), c.Project) ||
		(m.AuthorIs(u) && c.AllowedTo(domain.Perm("edit_own_messages"), c.Project))
}

// messageDestroyableBy は Message#destroyable_by?(User.current)。
func (c *Req) messageDestroyableBy(m *domain.Message) bool {
	u := c.User
	if !u.Logged() {
		return false
	}
	return c.AllowedTo(domain.Perm("delete_messages"), c.Project) ||
		(m.AuthorIs(u) && c.AllowedTo(domain.Perm("delete_own_messages"), c.Project))
}

// messageReplyView は返信 1 件の表示用データ。
type messageReplyView struct {
	*domain.Message
	Editable    bool
	Destroyable bool
}

// MessagesShow は messages#show。
func (a *App) MessagesShow(c *Req) {
	st := c.messageState()
	topic := st.Topic
	page := c.Params().String("page")
	if _, ok := c.Params().Get("page"); !ok && c.Params().Present("r") {
		n, err := repository.CountRepliesBefore(c.Ctx(), a.DB, topic.ID, httpx.RubyToI(c.Params().String("r")))
		if err != nil {
			a.internalError(c, "count replies", err)
			return
		}
		page = strconv.Itoa(1 + n/messagesRepliesPerPage)
	}
	count, err := repository.CountReplies(c.Ctx(), a.DB, topic.ID)
	if err != nil {
		a.internalError(c, "count replies", err)
		return
	}
	pages := pagination.New(count, messagesRepliesPerPage, page)
	replies, err := repository.TopicReplies(c.Ctx(), a.DB, topic.ID, pages.PerPage, pages.Offset())
	if err != nil {
		a.internalError(c, "replies", err)
		return
	}
	topicAtts, err := repository.ContainerAttachmentList(c.Ctx(), a.DB, domain.AttachmentContainerMessage, topic.ID)
	if err != nil {
		a.internalError(c, "topic attachments", err)
		return
	}
	views := make([]messageReplyView, len(replies))
	for i, r := range replies {
		r.Board = st.Board
		views[i] = messageReplyView{Message: r, Editable: c.messageEditableBy(r), Destroyable: c.messageDestroyableBy(r)}
	}
	reply := a.newMessageForm(c, &domain.Message{Subject: "RE: " + st.Message.Subject, BoardID: st.Board.ID})
	reply.key = "reply"
	canReply := !topic.Locked && c.AllowedTo(domain.ControllerAction("messages", "reply"), c.Project)
	watchers, err := repository.Watchers(c.Ctx(), a.DB, "message", topic.ID)
	if err != nil {
		a.internalError(c, "watchers", err)
		return
	}
	attachEditable := c.AllowedTo(domain.Perm("edit_messages"), c.Project)
	ancestors, err := a.boardAncestors(c, st.Board)
	if err != nil {
		a.internalError(c, "board ancestors", err)
		return
	}
	data := map[string]any{
		"Ancestors":        ancestors,
		"Board":            st.Board,
		"Message":          st.Message,
		"Topic":            topic,
		"TopicAttachments": topicAtts,
		"Replies":          views,
		"ReplyCount":       count,
		"Pages":            pages,
		"Reply":            reply,
		"PreviewMessage":   st.Message,
		"CanReply":         canReply,
		"Editable":         c.messageEditableBy(st.Message),
		"Destroyable":      c.messageDestroyableBy(st.Message),
		"AttachEditable":   attachEditable,
		"ShowWatchers": c.AllowedTo(domain.Perm("add_message_watchers"), c.Project) ||
			(len(watchers) > 0 && c.AllowedTo(domain.Perm("view_message_watchers"), c.Project)),
	}
	opts := RenderOptions{}
	if httpx.IsXHR(c.R) {
		opts.Layout = view.NoLayout
	}
	c.Render("messages/show", data, opts)
}

// assignMessage は message.safe_attributes = params[key]（subject / content と、edit_messages 権限が
// あれば locked / sticky / board_id）。
func assignMessage(c *Req, f *messageForm, key string) {
	p := c.Params().Map(key)
	if p == nil {
		return
	}
	m := f.Message
	if v, ok := p.StringOK("subject"); ok {
		m.Subject = v
	}
	if v, ok := p.StringOK("content"); ok {
		m.Content = v
	}
	if !f.canEdit {
		return
	}
	if v, ok := p.StringOK("locked"); ok {
		m.Locked = castBool(v)
	}
	if v, ok := p.StringOK("sticky"); ok {
		// sticky=(arg): true か "1" のときだけ 1
		m.Sticky = v == "1" || v == "true"
	}
	if v, ok := p.StringOK("board_id"); ok {
		m.BoardID = httpx.RubyToI(v)
	}
}

// validateMessage は Message の検証（board・subject・content 必須、subject 255 文字以内、
// ロックされたトピックへの返信不可）。
func (a *App) validateMessage(c *Req, f *messageForm, topic *domain.Message, res *attachments.SaveResult) error {
	e := f.errs
	m := f.Message
	b, err := repository.GetBoard(c.Ctx(), a.DB, m.BoardID)
	if err != nil {
		e.Add("board", "blank")
	} else if m.Board == nil || m.Board.ID != b.ID {
		b.Project = c.Project
		m.Board = b
	}
	if httpx.IsBlank(m.Subject) {
		e.Add("subject", "blank")
	}
	if httpx.IsBlank(m.Content) {
		e.Add("content", "blank")
	}
	if len([]rune(m.Subject)) > 255 {
		e.Add("subject", "too_long", "count", 255)
	}
	if m.ID == 0 && topic != nil && topic.Locked {
		e.AddMessage("base", "Topic is locked")
	}
	if msg := res.FailedMessage(c.Loc); msg != "" {
		e.AddMessage("base", msg)
	}
	return nil
}

// MessagesNew は messages#new（GET はフォーム、POST は作成）。
func (a *App) MessagesNew(c *Req) {
	st := c.messageState()
	m := &domain.Message{BoardID: st.Board.ID, Board: st.Board, AuthorID: authorIDOf(c.User), Author: c.User}
	f := a.newMessageForm(c, m)
	assignMessage(c, f, "message")
	var saved []*domain.Attachment
	if c.R.Method == http.MethodPost {
		res, err := a.saveContainerAttachments(c, paramAttachments(c))
		if err != nil {
			a.internalError(c, "save attachments", err)
			return
		}
		saved = res.Files
		if err := a.validateMessage(c, f, nil, res); err != nil {
			a.internalError(c, "validate message", err)
			return
		}
		if f.errs.Empty() {
			if err := a.createMessage(c, m, nil, res); err != nil {
				a.internalError(c, "create message", err)
				return
			}
			c.AttachFilesWarning(res)
			c.Flash().SetNotice(c.L("notice_successful_create"))
			c.Redirect("/boards/" + itoa(m.BoardID) + "/topics/" + itoa(m.ID))
			return
		}
	}
	c.Render("messages/new", map[string]any{"Board": st.Board, "Message": f, "SavedAttachments": saved})
}

// createMessage は Message の保存（after_create :add_author_as_watcher, :reset_counters!）。
func (a *App) createMessage(c *Req, m *domain.Message, topic *domain.Message, res *attachments.SaveResult) error {
	now := a.now()
	m.CreatedAt, m.UpdatedAt = now, now
	err := a.withTx(c, func(tx *db.Tx) error {
		if err := repository.InsertMessage(c.Ctx(), tx, m); err != nil {
			return err
		}
		if err := a.AttachmentStore.AttachSaved(c.Ctx(), tx, res, domain.AttachmentContainerMessage, m.ID); err != nil {
			return err
		}
		root := m.ID
		if topic != nil {
			root = topic.ID
		}
		if m.AuthorID != nil {
			if err := repository.AddWatcher(c.Ctx(), tx, "message", root, *m.AuthorID); err != nil {
				return err
			}
		}
		if topic != nil {
			if err := repository.UpdateTopicCounters(c.Ctx(), tx, topic.ID); err != nil {
				return err
			}
		}
		return repository.ResetBoardCounters(c.Ctx(), tx, m.BoardID)
	})
	if err != nil {
		return err
	}
	a.notify(c, "message_posted", "message_posted", m)
	return nil
}

// MessagesReply は messages#reply。
func (a *App) MessagesReply(c *Req) {
	st := c.messageState()
	topic := st.Topic
	m := &domain.Message{BoardID: st.Board.ID, Board: st.Board, AuthorID: authorIDOf(c.User), Author: c.User, ParentID: &topic.ID}
	f := a.newMessageForm(c, m)
	f.key = "reply"
	// @reply.safe_attributes = params[:reply]（返信でも edit_messages 権限があれば locked / sticky / board_id を受け付ける）
	assignMessage(c, f, "reply")
	// @topic.children << @reply（board_id は親と同じにはならない: safe_attributes で変わりうる）
	res, err := a.saveContainerAttachments(c, paramAttachments(c))
	if err != nil {
		a.internalError(c, "save attachments", err)
		return
	}
	if err := a.validateMessage(c, f, topic, res); err != nil {
		a.internalError(c, "validate message", err)
		return
	}
	if f.errs.Empty() {
		if err := a.createMessage(c, m, topic, res); err != nil {
			a.internalError(c, "create reply", err)
			return
		}
		c.AttachFilesWarning(res)
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	u := "/boards/" + itoa(st.Board.ID) + "/topics/" + itoa(topic.ID)
	if m.ID != 0 {
		u += "?r=" + itoa(m.ID)
	}
	c.Redirect(u)
}

// MessagesEdit は messages#edit（GET はフォーム、POST は更新）。
func (a *App) MessagesEdit(c *Req) {
	st := c.messageState()
	m := st.Message
	if !c.messageEditableBy(m) {
		c.Render403("")
		return
	}
	orig := *m
	f := a.newMessageForm(c, m)
	assignMessage(c, f, "message")
	var saved []*domain.Attachment
	if c.R.Method == http.MethodPost {
		res, err := a.saveContainerAttachments(c, paramAttachments(c))
		if err != nil {
			a.internalError(c, "save attachments", err)
			return
		}
		saved = res.Files
		if err := a.validateMessage(c, f, nil, res); err != nil {
			a.internalError(c, "validate message", err)
			return
		}
		if f.errs.Empty() {
			m.UpdatedAt = a.now()
			err := a.withTx(c, func(tx *db.Tx) error {
				if err := repository.UpdateMessage(c.Ctx(), tx, m); err != nil {
					return err
				}
				if err := a.AttachmentStore.AttachSaved(c.Ctx(), tx, res, domain.AttachmentContainerMessage, m.ID); err != nil {
					return err
				}
				// after_update :update_messages_board
				if m.BoardID != orig.BoardID {
					if err := repository.MoveTopicToBoard(c.Ctx(), tx, m.RootID(), m.BoardID); err != nil {
						return err
					}
					if err := repository.ResetBoardCounters(c.Ctx(), tx, orig.BoardID); err != nil {
						return err
					}
					return repository.ResetBoardCounters(c.Ctx(), tx, m.BoardID)
				}
				return nil
			})
			if err != nil {
				a.internalError(c, "update message", err)
				return
			}
			c.AttachFilesWarning(res)
			c.Flash().SetNotice(c.L("notice_successful_update"))
			u := "/boards/" + itoa(m.BoardID) + "/topics/" + itoa(m.RootID())
			if m.ParentID != nil {
				u += "?r=" + itoa(m.ID)
			}
			c.Redirect(u)
			return
		}
	}
	boards, err := repository.ProjectBoards(c.Ctx(), a.DB, c.Project.ID, false)
	if err != nil {
		a.internalError(c, "boards", err)
		return
	}
	ancestors, err := a.boardAncestors(c, st.Board)
	if err != nil {
		a.internalError(c, "board ancestors", err)
		return
	}
	c.Render("messages/edit", map[string]any{
		"Ancestors": ancestors, "Board": st.Board, "Message": f, "Topic": st.Topic, "Replying": m.ParentID != nil,
		"ProjectBoards": boards, "SavedAttachments": saved, "PreviewMessage": m,
	})
}

// MessagesDestroy は messages#destroy。
func (a *App) MessagesDestroy(c *Req) {
	st := c.messageState()
	m := st.Message
	if !c.messageDestroyableBy(m) {
		c.Render403("")
		return
	}
	var deleted []*domain.Attachment
	err := a.withTx(c, func(tx *db.Tx) error {
		ids, err := repository.DeleteMessage(c.Ctx(), tx, m.ID)
		if err != nil {
			return err
		}
		if deleted, err = repository.DeleteContainerAttachments(c.Ctx(), tx, domain.AttachmentContainerMessage, ids); err != nil {
			return err
		}
		// after_destroy :reset_counters!
		if m.ParentID != nil {
			if err := repository.UpdateTopicCounters(c.Ctx(), tx, *m.ParentID); err != nil {
				return err
			}
		}
		return repository.ResetBoardCounters(c.Ctx(), tx, m.BoardID)
	})
	if err != nil {
		a.internalError(c, "destroy message", err)
		return
	}
	a.deleteAttachmentsAfterCommit(c, deleted)
	c.Flash().SetNotice(c.L("notice_successful_delete"))
	if m.ParentID != nil {
		c.Redirect("/boards/" + itoa(st.Board.ID) + "/topics/" + itoa(*m.ParentID) + "?r=" + itoa(m.ID))
		return
	}
	c.Redirect("/projects/" + c.Project.Identifier + "/boards/" + itoa(st.Board.ID))
}

// MessagesQuote は messages#quote（JS で返信フォームに引用を入れる。HTML は 404）。
func (a *App) MessagesQuote(c *Req) {
	st := c.messageState()
	m := st.Message
	if httpx.Negotiate(c.R, "html", "js") != "js" {
		c.Render404("")
		return
	}
	// verify_same_origin_request: XHR でない GET への JavaScript の応答は
	// ActionController::InvalidCrossOriginRequest（422、本文なし）
	if c.R.Method == http.MethodGet && !httpx.IsXHR(c.R) {
		c.W.Header().Set("Content-Type", "text/html")
		c.W.WriteHeader(http.StatusUnprocessableEntity)
		c.Halt()
		return
	}
	subject := m.Subject
	if !strings.HasPrefix(subject, "RE:") {
		subject = "RE: " + subject
	}
	qb := redmine.QuoteBuilder{Bundle: a.Bundle, DefaultLanguage: a.Settings.String("default_language")}
	author := ""
	if m.Author != nil {
		author = c.Page().UserName(m.Author)
	}
	partial := c.Params().String("quote")
	var content string
	if m.ParentID == nil {
		content = qb.QuoteRootMessage(author, m.Content, partial)
	} else {
		content = qb.QuoteMessage(author, m.Content, itoa(m.ID), partial)
	}
	c.Render("messages/quote", map[string]any{"Subject": subject, "Content": content}, RenderOptions{Format: "js"})
}

// MessagesPreview は messages#preview（common/_preview）。
func (a *App) MessagesPreview(c *Req) {
	st := c.messageState()
	var previewed *redmine.Object
	if id, ok := paramInt64(c, "id"); ok {
		if m, err := repository.BoardMessage(c.Ctx(), a.DB, st.Board.ID, id); err == nil {
			previewed = &redmine.Object{Kind: "message", ID: m.ID, Project: c.Project}
		}
	}
	a.renderPreview(c, previewed)
}
