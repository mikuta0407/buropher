package handler

import (
	"net/http"
	"net/url"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
)

// BoardsController（app/controllers/boards_controller.rb）。
//
//	default_search_scope :messages
//	before_action :find_project_by_project_id, :find_board_if_available, :authorize
//	accept_atom_auth :index, :show
var BoardsController = &Controller{Name: "boards", MainMenu: true, DefaultSearchScope: "messages"}

// routesBoards は boards コントローラのルートを登録する（resources :projects do resources :boards end）。
func (a *App) routesBoards(r Router) {
	b := BoardsController
	opts := func(extra ...ActionOption) []ActionOption {
		return append([]ActionOption{FindProjectByProjectID(), Before(a.findBoardIfAvailable), Authorize()}, extra...)
	}
	base := "/projects/{project_id}/boards"
	a.Handle(r, http.MethodGet, base, b, "index", a.BoardsIndex, opts(AcceptAtomAuth())...)
	a.Handle(r, http.MethodPost, base, b, "create", a.BoardsCreate, opts()...)
	a.Handle(r, http.MethodGet, base+"/new", b, "new", a.BoardsNew, opts()...)
	a.Handle(r, http.MethodGet, base+"/{id}/edit", b, "edit", a.BoardsEdit, opts()...)
	a.Handle(r, http.MethodGet, base+"/{id}", b, "show", a.BoardsShow, opts(AcceptAtomAuth())...)
	a.Handle(r, http.MethodPatch, base+"/{id}", b, "update", a.BoardsUpdate, opts()...)
	a.Handle(r, http.MethodPut, base+"/{id}", b, "update", a.BoardsUpdate, opts()...)
	a.Handle(r, http.MethodDelete, base+"/{id}", b, "destroy", a.BoardsDestroy, opts()...)
}

type boardCtxKey struct{}

// boardForm は @board（labelled_form_for のモデル）。
type boardForm struct {
	contentForm
	*domain.Board
	// ValidParents は valid_parents（project.boards - self_and_descendants）。
	ValidParents []*domain.Board
	// descriptionGiven は params[:board][:description] が渡された（新規でも value を出す）。
	descriptionGiven bool
}

// Send は rails.Sender（新規の board の description は nil: Redmine の列は NULL 可で既定値が無い）。
func (f *boardForm) Send(method string) (any, bool) {
	if method == "description" && f.Board.ID == 0 && f.Board.Description == "" && !f.descriptionGiven {
		return nil, true
	}
	return nil, false
}

func (c *Req) board() *boardForm {
	f, _ := c.value(boardCtxKey{}).(*boardForm)
	return f
}

// findBoardIfAvailable は find_board_if_available（params[:id] があれば @project.boards.find）。
func (a *App) findBoardIfAvailable(c *Req) {
	if !c.Params().Present("id") {
		return
	}
	id, ok := paramInt64(c, "id")
	if !ok {
		c.Render404("")
		return
	}
	b, err := repository.GetBoard(c.Ctx(), a.DB, id)
	if err != nil || b.ProjectID != c.Project.ID {
		if err == nil {
			err = repository.ErrNotFound
		}
		a.notFoundOr500(c, "find board", err)
		return
	}
	b.Project = c.Project
	c.setValue(boardCtxKey{}, &boardForm{contentForm: newContentForm(c, "board", b.ID), Board: b})
}

// BoardsIndex は boards#index（フォーラムが 1 つならそれを表示する）。
func (a *App) BoardsIndex(c *Req) {
	boards, err := repository.ProjectBoardList(c.Ctx(), a.DB, c.Project.ID, true)
	if err != nil {
		a.internalError(c, "boards", err)
		return
	}
	if len(boards) == 1 {
		b := boards[0]
		b.Project = c.Project
		c.setValue(boardCtxKey{}, &boardForm{contentForm: newContentForm(c, "board", b.ID), Board: b})
		a.BoardsShow(c)
		return
	}
	tree := domain.BoardTree(boards)
	c.Render("boards/index", map[string]any{
		"Tree":         tree,
		"AtomKey":      c.AtomKey(),
		"ManageBoards": c.AllowedTo(domain.Perm("manage_boards"), c.Project),
	})
}

// BoardsShow は boards#show。
func (a *App) BoardsShow(c *Req) {
	bf := c.board()
	b := bf.Board
	switch httpx.Negotiate(c.R, "html", "atom") {
	case "html":
		st := c.sortUpdate(query.SortCriteria{{"updated_on", "desc"}}, map[string][]string{
			"created_on": {"messages.id"},
			"replies":    {"messages.replies_count"},
			"updated_on": {"COALESCE(messages.last_reply_id, messages.id)"},
		})
		count, err := repository.CountBoardTopics(c.Ctx(), a.DB, b.ID)
		if err != nil {
			a.internalError(c, "count topics", err)
			return
		}
		pages := pagination.New(count, c.PerPageOption(), c.Params().String("page"))
		topics, err := repository.BoardTopics(c.Ctx(), a.DB, b.ID, st.Clause(), pages.PerPage, pages.Offset())
		if err != nil {
			a.internalError(c, "topics", err)
			return
		}
		msg := a.newMessageForm(c, &domain.Message{BoardID: b.ID, Board: b})
		opts := RenderOptions{}
		if httpx.IsXHR(c.R) {
			opts.Layout = view.NoLayout
		}
		ancestors, err := a.boardAncestors(c, b)
		if err != nil {
			a.internalError(c, "board ancestors", err)
			return
		}
		c.Render("boards/show", map[string]any{
			"Board":        b,
			"Ancestors":    ancestors,
			"Topics":       topics,
			"TopicCount":   count,
			"Pages":        pages,
			"Sort":         st.Criteria,
			"Message":      msg,
			"AtomKey":      c.AtomKey(),
			"CanAdd":       c.AllowedTo(domain.Perm("add_messages"), c.Project),
			"ManageBoards": c.AllowedTo(domain.Perm("manage_boards"), c.Project),
			"AtomPath":     boardAtomPath(c, b),
		}, opts)
	case "atom":
		limit := int(settings.RubyToI(a.Settings.String("feeds_limit")))
		cond := "messages.board_id = ?"
		loader := &activity.Loader{Q: a.DB, Auth: c.Authz(), Loc: c.Loc}
		events, err := loader.Messages(c.Ctx(), cond, []any{b.ID}, "ORDER BY messages.id DESC "+a.DB.Dialect().LimitOffset(limit, 0))
		if err != nil {
			a.internalError(c, "board feed", err)
			return
		}
		a.renderFeed(c, events, c.Project.Name+": "+b.Name)
	default:
		respondNotAcceptable(c)
	}
}

// boardAtomPath は url_for(:format => 'atom', :key => ...) のパス部分（index から show を描画した場合は
// boards#index の URL になる）。
func boardAtomPath(c *Req, b *domain.Board) string {
	if c.Action == "index" {
		return "/projects/" + c.Project.Identifier + "/boards.atom"
	}
	return "/projects/" + c.Project.Identifier + "/boards/" + itoa(b.ID) + ".atom"
}

// boardAncestors は board.ancestors.reverse（ルートから親まで）。
func (a *App) boardAncestors(c *Req, b *domain.Board) ([]*domain.Board, error) {
	var out []*domain.Board
	seen := map[int64]bool{b.ID: true}
	cur := b
	for cur.ParentID != nil && !seen[*cur.ParentID] {
		p, err := repository.GetBoard(c.Ctx(), a.DB, *cur.ParentID)
		if err != nil {
			return nil, err
		}
		p.Project = c.Project
		seen[p.ID] = true
		out = append([]*domain.Board{p}, out...)
		cur = p
	}
	return out, nil
}

// boardValidParents は valid_parents（project.boards - self_and_descendants）。
func (a *App) boardValidParents(c *Req, b *domain.Board) ([]*domain.Board, error) {
	boards, err := repository.ProjectBoardList(c.Ctx(), a.DB, c.Project.ID, false)
	if err != nil {
		return nil, err
	}
	excluded := map[int64]bool{}
	if b.ID != 0 {
		excluded[b.ID] = true
		changed := true
		for changed {
			changed = false
			for _, x := range boards {
				if x.ParentID != nil && excluded[*x.ParentID] && !excluded[x.ID] {
					excluded[x.ID] = true
					changed = true
				}
			}
		}
	}
	var out []*domain.Board
	for _, x := range boards {
		if !excluded[x.ID] {
			out = append(out, x)
		}
	}
	return out, nil
}

// assignBoard は board.safe_attributes = params[:board]（name / description / parent_id / position）。
func assignBoard(c *Req, b *domain.Board) (positionGiven bool) {
	p := c.Params().Map("board")
	if p == nil {
		return false
	}
	if v, ok := p.StringOK("name"); ok {
		b.Name = v
	}
	if v, ok := p.StringOK("description"); ok {
		b.Description = v
	}
	if v, ok := p.StringOK("parent_id"); ok {
		if n := httpx.RubyToI(v); n > 0 {
			b.ParentID = &n
		} else {
			b.ParentID = nil
		}
	}
	if v, ok := p.StringOK("position"); ok {
		b.Position = int(httpx.RubyToI(v))
		positionGiven = true
	}
	return positionGiven
}

// validateBoard は Board の検証（name・description 必須、長さ、親の妥当性）。
func validateBoard(f *boardForm, parentChanged bool) {
	e := f.errs
	b := f.Board
	if httpx.IsBlank(b.Name) {
		e.Add("name", "blank")
	}
	if httpx.IsBlank(b.Description) {
		e.Add("description", "blank")
	}
	if len([]rune(b.Name)) > 30 {
		e.Add("name", "too_long", "count", 30)
	}
	if len([]rune(b.Description)) > 255 {
		e.Add("description", "too_long", "count", 255)
	}
	if b.ParentID != nil && parentChanged {
		ok := false
		for _, p := range f.ValidParents {
			if p.ID == *b.ParentID {
				ok = true
			}
		}
		if !ok {
			e.Add("parent_id", "invalid")
		}
	}
}

func boardPositionScope(b *domain.Board) repository.PositionScope {
	if b.ParentID == nil {
		return repository.PositionScope{Table: "boards", Where: "project_id = ? AND parent_id IS NULL", Args: []any{b.ProjectID}}
	}
	return repository.PositionScope{Table: "boards", Where: "project_id = ? AND parent_id = ?", Args: []any{b.ProjectID, *b.ParentID}}
}

func (a *App) renderBoardForm(c *Req, tmpl string, f *boardForm) {
	c.Render(tmpl, map[string]any{"Board": f})
}

// settingsBoardsPath は settings_project_path(@project, :tab => 'boards')。
func settingsBoardsPath(p *domain.Project) string {
	return "/projects/" + url.PathEscape(p.Identifier) + "/settings/boards"
}

// BoardsNew は boards#new。
func (a *App) BoardsNew(c *Req) {
	b := &domain.Board{ProjectID: c.Project.ID, Project: c.Project}
	assignBoard(c, b)
	vp, err := a.boardValidParents(c, b)
	if err != nil {
		a.internalError(c, "valid parents", err)
		return
	}
	a.renderBoardForm(c, "boards/new", &boardForm{contentForm: newContentForm(c, "board", 0), Board: b, ValidParents: vp})
}

// BoardsCreate は boards#create。
func (a *App) BoardsCreate(c *Req) {
	b := &domain.Board{ProjectID: c.Project.ID, Project: c.Project}
	positionGiven := assignBoard(c, b)
	vp, err := a.boardValidParents(c, b)
	if err != nil {
		a.internalError(c, "valid parents", err)
		return
	}
	f := &boardForm{contentForm: newContentForm(c, "board", 0), Board: b, ValidParents: vp}
	if p := c.Params().Map("board"); p != nil {
		f.descriptionGiven = p.Has("description")
	}
	validateBoard(f, b.ParentID != nil)
	if f.errs.Any() {
		a.renderBoardForm(c, "boards/new", f)
		return
	}
	err = a.withTx(c, func(tx *db.Tx) error {
		scope := boardPositionScope(b)
		if !positionGiven || b.Position <= 0 {
			pos, err := repository.NextPosition(c.Ctx(), tx, scope)
			if err != nil {
				return err
			}
			b.Position = pos
		}
		if err := repository.InsertBoard(c.Ctx(), tx, b); err != nil {
			return err
		}
		return repository.InsertPosition(c.Ctx(), tx, scope, b.ID, b.Position)
	})
	if err != nil {
		a.internalError(c, "create board", err)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_create"))
	c.Redirect(settingsBoardsPath(c.Project))
}

// BoardsEdit は boards#edit。
func (a *App) BoardsEdit(c *Req) {
	f := c.board()
	vp, err := a.boardValidParents(c, f.Board)
	if err != nil {
		a.internalError(c, "valid parents", err)
		return
	}
	f.ValidParents = vp
	a.renderBoardForm(c, "boards/edit", f)
}

// BoardsUpdate は boards#update。
func (a *App) BoardsUpdate(c *Req) {
	f := c.board()
	b := f.Board
	orig := *b
	assignBoard(c, b)
	vp, err := a.boardValidParents(c, &orig)
	if err != nil {
		a.internalError(c, "valid parents", err)
		return
	}
	f.ValidParents = vp
	parentChanged := !sameInt64Ptr(orig.ParentID, b.ParentID)
	validateBoard(f, parentChanged)
	js := httpx.Format(c.R) == "js"
	if f.errs.Any() {
		if js {
			httpx.Head(c.W, c.R, http.StatusUnprocessableEntity)
			c.Halt()
			return
		}
		a.renderBoardForm(c, "boards/edit", f)
		return
	}
	err = a.withTx(c, func(tx *db.Tx) error {
		if parentChanged {
			// acts_as_positioned: 旧スコープから外し、新スコープの末尾（または指定位置）に入れる
			if err := repository.RemovePosition(c.Ctx(), tx, boardPositionScope(&orig), b.ID, orig.Position); err != nil {
				return err
			}
			if b.Position == orig.Position || b.Position <= 0 {
				pos, err := repository.NextPosition(c.Ctx(), tx, boardPositionScope(b))
				if err != nil {
					return err
				}
				b.Position = pos
			}
			if err := repository.UpdateBoard(c.Ctx(), tx, b); err != nil {
				return err
			}
			return repository.InsertPosition(c.Ctx(), tx, boardPositionScope(b), b.ID, b.Position)
		}
		if err := repository.UpdateBoard(c.Ctx(), tx, b); err != nil {
			return err
		}
		return repository.ShiftPositions(c.Ctx(), tx, boardPositionScope(b), b.ID, orig.Position, b.Position)
	})
	if err != nil {
		a.internalError(c, "update board", err)
		return
	}
	if js {
		httpx.Head(c.W, c.R, http.StatusOK)
		c.Halt()
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.Redirect(settingsBoardsPath(c.Project))
}

// BoardsDestroy は boards#destroy。
func (a *App) BoardsDestroy(c *Req) {
	b := c.board().Board
	var deleted []*domain.Attachment
	err := a.withTx(c, func(tx *db.Tx) error {
		mids, err := repository.DeleteBoard(c.Ctx(), tx, b.ID)
		if err != nil {
			return err
		}
		if deleted, err = repository.DeleteContainerAttachments(c.Ctx(), tx, domain.AttachmentContainerMessage, mids); err != nil {
			return err
		}
		return repository.RemovePosition(c.Ctx(), tx, boardPositionScope(b), b.ID, b.Position)
	})
	if err != nil {
		a.internalError(c, "destroy board", err)
		return
	}
	a.deleteAttachmentsAfterCommit(c, deleted)
	c.Flash().SetNotice(c.L("notice_successful_delete"))
	c.Redirect(settingsBoardsPath(c.Project))
}

func sameInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
