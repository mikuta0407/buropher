package handler

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルはマイページ（MyController#page / add_block / remove_block / order_blocks / update_page）と
// Redmine::MyPage（lib/redmine/my_page.rb）、UserPreference のマイページ関連メソッド
// （my_page_layout / my_page_settings / add_block / remove_block / order_blocks / update_block_settings /
// clear_unused_block_settings）、MyHelper#render_blocks / render_block / block_select_tag の移植。

// myPageGroups は Redmine::MyPage::CORE_GROUPS。
var myPageGroups = []string{"top", "left", "right"}

// myBlockDef は Redmine::MyPage::CORE_BLOCKS の 1 件。
type myBlockDef struct {
	Name      string
	Label     string
	MaxOccurs int
}

// myPageBlocks は Redmine::MyPage::CORE_BLOCKS（定義順）。
// TODO(plugin): プラグインの my/blocks/_*.erb による追加ブロック（additional_blocks）。
var myPageBlocks = []myBlockDef{
	{"issuesassignedtome", "label_assigned_to_me_issues", 1},
	{"issuesreportedbyme", "label_reported_issues", 1},
	{"issuesupdatedbyme", "label_updated_issues", 1},
	{"issueswatched", "label_watched_issues", 1},
	{"issuequery", "label_issue_plural", 3},
	{"news", "label_news_latest", 1},
	{"calendar", "label_calendar", 1},
	{"documents", "label_document_plural", 1},
	{"timelog", "label_spent_time", 1},
	{"activity", "label_activity", 1},
}

var reMyBlockID = regexp.MustCompile(`^(.*?)(__\d+)?$`)

// findMyBlock は Redmine::MyPage.find_block(block)。
func findMyBlock(block string) *myBlockDef {
	m := reMyBlockID.FindStringSubmatch(block)
	if m == nil {
		return nil
	}
	for i := range myPageBlocks {
		if myPageBlocks[i].Name == m[1] {
			return &myPageBlocks[i]
		}
	}
	return nil
}

// myBlockOption は block_options の 1 件（ID が "" なら上限に達して選べない）。
type myBlockOption struct {
	Label string
	ID    string
}

// myBlockOptions は Redmine::MyPage.block_options(blocks_in_use)。
func (c *Req) myBlockOptions(inUse []string) []myBlockOption {
	var out []myBlockOption
	for _, def := range myPageBlocks {
		re := regexp.MustCompile(`^` + regexp.QuoteMeta(def.Name) + `(__(\d+))?$`)
		var idx []int
		for _, n := range inUse {
			if m := re.FindStringSubmatch(n); m != nil {
				i, _ := strconv.Atoi(m[2])
				idx = append(idx, i)
			}
		}
		id := def.Name
		if len(idx) > 0 {
			id = def.Name + "__" + strconv.Itoa(slices.Max(idx)+1)
		}
		if len(idx) >= def.MaxOccurs {
			id = ""
		}
		// l("my.blocks.#{label}", :default => [label, label.to_s.humanize])
		out = append(out, myBlockOption{Label: c.L(def.Label), ID: id})
	}
	return out
}

// validMyBlock は Redmine::MyPage.valid_block?(block, blocks_in_use)。
func (c *Req) validMyBlock(block string, inUse []string) bool {
	if strings.TrimSpace(block) == "" {
		return false
	}
	for _, o := range c.myBlockOptions(inUse) {
		if o.ID != "" && o.ID == block {
			return true
		}
	}
	return false
}

// underscore は String#underscore。
func underscore(s string) string {
	s = strings.ReplaceAll(s, "::", "/")
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]) ||
				(unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		if r == '-' {
			r = '_'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ---------------------------------------------------------------- UserPreference

// myPagePref は UserPreference のマイページ部分（my_page_layout / my_page_settings）。
type myPagePref struct {
	userID int64
	exists bool
	// layout はグループ → ブロックの列（nil = 既定レイアウト）。
	layout map[string][]string
	// keys は layout のグループの順序（Ruby の Hash の挿入順）。
	keys     []string
	settings map[string]map[string]any
}

// loadMyPagePref は user.pref のマイページ部分を読み込む。
func (a *App) loadMyPagePref(c *Req, userID int64) (*myPagePref, error) {
	raw, err := repository.GetMyPagePrefs(c.Ctx(), a.DB, userID)
	if err != nil {
		return nil, err
	}
	p := &myPagePref{userID: userID, exists: raw.Exists, settings: map[string]map[string]any{}}
	if raw.Layout != "" && raw.Layout != "null" {
		var m map[string][]string
		if json.Unmarshal([]byte(raw.Layout), &m) == nil && m != nil {
			p.layout = m
			p.keys = orderedGroupKeys(m)
		}
	}
	if raw.Settings != "" && raw.Settings != "null" {
		var m map[string]map[string]any
		if json.Unmarshal([]byte(raw.Settings), &m) == nil && m != nil {
			p.settings = m
		}
	}
	return p, nil
}

// orderedGroupKeys はグループのキーを top / left / right を先に並べる。
func orderedGroupKeys(m map[string][]string) []string {
	var keys []string
	for _, g := range myPageGroups {
		if _, ok := m[g]; ok {
			keys = append(keys, g)
		}
	}
	var rest []string
	for k := range m {
		if !slices.Contains(myPageGroups, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(keys, rest...)
}

// Layout は my_page_layout（未設定なら既定レイアウトを設定して返す）。
func (p *myPagePref) Layout() map[string][]string {
	if p.layout == nil {
		// Redmine::MyPage.default_layout
		p.layout = map[string][]string{"left": {"issuesassignedtome"}, "right": {"issuesreportedbyme"}}
		p.keys = []string{"left", "right"}
	}
	return p.layout
}

// blocksInUse は my_page_layout.values.flatten。
func (p *myPagePref) blocksInUse() []string {
	l := p.Layout()
	var out []string
	for _, k := range p.keys {
		out = append(out, l[k]...)
	}
	return out
}

// blockSettings は my_page_settings(block)。
func (p *myPagePref) blockSettings(block string) map[string]any {
	s := p.settings[block]
	if s == nil {
		s = map[string]any{}
		p.settings[block] = s
	}
	return s
}

// removeBlock は remove_block(block)。
func (p *myPagePref) removeBlock(block string) {
	block = underscore(block)
	l := p.Layout()
	for _, k := range p.keys {
		l[k] = slices.DeleteFunc(l[k], func(b string) bool { return b == block })
	}
}

// setGroup は my_page_layout[group] = blocks（新しいキーは末尾に追加）。
func (p *myPagePref) setGroup(group string, blocks []string) {
	l := p.Layout()
	if _, ok := l[group]; !ok {
		p.keys = append(p.keys, group)
	}
	l[group] = blocks
}

// addBlock は add_block(block)（無効なブロックなら false）。
func (p *myPagePref) addBlock(c *Req, block string) bool {
	block = underscore(block)
	if !c.validMyBlock(block, p.blocksInUse()) {
		return false
	}
	p.removeBlock(block)
	group := myPageGroups[0]
	p.setGroup(group, append([]string{block}, p.Layout()[group]...))
	return true
}

// orderBlocks は order_blocks(group, blocks)。
func (p *myPagePref) orderBlocks(group string, blocks []string) {
	if !slices.Contains(myPageGroups, group) || len(blocks) == 0 {
		return
	}
	inUse := p.blocksInUse()
	var ordered []string
	for _, b := range blocks {
		b = underscore(b)
		if slices.Contains(inUse, b) && !slices.Contains(ordered, b) {
			ordered = append(ordered, b)
		}
	}
	for _, b := range ordered {
		p.removeBlock(b)
	}
	if ordered == nil {
		ordered = []string{}
	}
	p.setGroup(group, ordered)
}

// updateBlockSettings は update_block_settings(block, settings)。
func (p *myPagePref) updateBlockSettings(block string, settings map[string]any) {
	s := p.blockSettings(block)
	for k, v := range settings {
		s[k] = v
	}
}

// clearUnused は clear_unused_block_settings（before_save）。
func (p *myPagePref) clearUnused() {
	inUse := p.blocksInUse()
	for k := range p.settings {
		if !slices.Contains(inUse, k) {
			delete(p.settings, k)
		}
	}
}

// saveMyPagePref は pref.save（行が無ければ UserPreference.new の既定値で作る）。
func (a *App) saveMyPagePref(c *Req, p *myPagePref) error {
	p.clearUnused()
	if !p.exists {
		if err := repository.SaveUserPreferenceDetail(c.Ctx(), a.DB, a.newPreference(p.userID)); err != nil {
			return err
		}
		p.exists = true
	}
	layout, err := json.Marshal(p.Layout())
	if err != nil {
		return err
	}
	settings, err := json.Marshal(p.settings)
	if err != nil {
		return err
	}
	return repository.SaveMyPagePrefs(c.Ctx(), a.DB, p.userID, string(layout), string(settings))
}

// clearUnusedMyPageSettings は個人設定の保存時の clear_unused_block_settings。
func (a *App) clearUnusedMyPageSettings(c *Req, userID int64) error {
	p, err := a.loadMyPagePref(c, userID)
	if err != nil {
		return err
	}
	return a.saveMyPagePref(c, p)
}

// ---------------------------------------------------------------- 描画

// myGroupView は page.html の 1 グループ。
type myGroupView struct {
	Name   string
	Blocks []*myBlockView
}

// blockSelectTag は MyHelper#block_select_tag(user)。
func (c *Req) blockSelectTag(p *myPagePref) rails.HTML {
	opts := rails.ContentTag("option", nil, nil)
	for _, o := range c.myBlockOptions(p.blocksInUse()) {
		var value any
		if o.ID != "" {
			value = o.ID
		}
		opts += rails.ContentTag("option", o.Label, rails.NewHash("value", value, "disabled", o.ID == ""))
	}
	return rails.SelectTag("block", opts, rails.NewHash("id", "block-select", "onchange", "$('#block-form').submit();"))
}

// renderBlocks は render_blocks(blocks, user) の各ブロックのデータ。
func (a *App) renderBlocks(c *Req, p *myPagePref, blocks []string) ([]*myBlockView, error) {
	var out []*myBlockView
	for _, b := range blocks {
		v, err := a.myBlockContent(c, p, b)
		if err != nil {
			return nil, err
		}
		if v != nil {
			out = append(out, v)
		}
	}
	return out, nil
}

// MyIndex は my#index（GET /my。page を描画する）。
func (a *App) MyIndex(c *Req) { a.MyPage(c) }

// MyPage は my#page（GET /my/page）。
func (a *App) MyPage(c *Req) {
	p, err := a.loadMyPagePref(c, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	layout := p.Layout()
	var groups []myGroupView
	for _, g := range myPageGroups {
		blocks, err := a.renderBlocks(c, p, layout[g])
		if err != nil {
			a.serverError(c, err)
			return
		}
		groups = append(groups, myGroupView{Name: g, Blocks: blocks})
	}
	c.Render("my/page", map[string]any{
		"Groups":      groups,
		"BlockSelect": c.blockSelectTag(p),
	})
}

// myRespondJS は respond_to { format.html; format.js } の判定（js なら true）。
func myRespondJS(c *Req) bool {
	return httpx.Negotiate(c.R, "html", "js") == "js"
}

// MyAddBlock は my#add_block（POST /my/add_block）。
func (a *App) MyAddBlock(c *Req) {
	p, err := a.loadMyPagePref(c, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	block := c.Params().String("block")
	if !p.addBlock(c, block) {
		c.RenderError(http.StatusUnprocessableEntity, "")
		return
	}
	if err := a.saveMyPagePref(c, p); err != nil {
		a.serverError(c, err)
		return
	}
	if !myRespondJS(c) {
		c.Redirect("/my/page")
		return
	}
	block = underscore(block)
	blocks, err := a.renderBlocks(c, p, []string{block})
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("my/add_block", map[string]any{
		"Block": block, "Blocks": blocks, "BlockSelect": c.blockSelectTag(p),
	}, RenderOptions{Format: "js", Layout: view.NoLayout})
}

// MyRemoveBlock は my#remove_block（POST /my/remove_block）。
func (a *App) MyRemoveBlock(c *Req) {
	p, err := a.loadMyPagePref(c, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	block := c.Params().String("block")
	p.removeBlock(block)
	if err := a.saveMyPagePref(c, p); err != nil {
		a.serverError(c, err)
		return
	}
	if !myRespondJS(c) {
		c.Redirect("/my/page")
		return
	}
	c.Render("my/remove_block", map[string]any{
		"Block": block, "BlockSelect": c.blockSelectTag(p),
	}, RenderOptions{Format: "js", Layout: view.NoLayout})
}

// MyOrderBlocks は my#order_blocks（POST /my/order_blocks。head :ok）。
func (a *App) MyOrderBlocks(c *Req) {
	p, err := a.loadMyPagePref(c, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	var blocks []string
	for _, v := range c.Params().Slice("blocks") {
		blocks = append(blocks, httpx.ValueString(v))
	}
	p.orderBlocks(c.Params().String("group"), blocks)
	if err := a.saveMyPagePref(c, p); err != nil {
		a.serverError(c, err)
		return
	}
	c.W.WriteHeader(http.StatusOK)
	c.Halt()
}

// MyUpdatePage は my#update_page（POST /my/page。ブロックの設定を保存し、update_page.js で描画し直す）。
func (a *App) MyUpdatePage(c *Req) {
	p, err := a.loadMyPagePref(c, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	var updated []string
	if s := c.Params().Map("settings"); s != nil {
		s.Each(func(block string, v any) {
			m := map[string]any{}
			if hp, ok := v.(*httpx.Params); ok {
				m = hp.ToMap()
			}
			p.updateBlockSettings(block, m)
			updated = append(updated, block)
		})
	}
	if err := a.saveMyPagePref(c, p); err != nil {
		a.serverError(c, err)
		return
	}
	type updatedBlock struct {
		Block string
		// Views は render_block の結果（未知のブロックなら空）。
		Views []*myBlockView
	}
	var blocks []updatedBlock
	for _, b := range updated {
		v, err := a.myBlockContent(c, p, b)
		if err != nil {
			a.serverError(c, err)
			return
		}
		ub := updatedBlock{Block: b}
		if v != nil {
			ub.Views = []*myBlockView{v}
		}
		blocks = append(blocks, ub)
	}
	c.Render("my/update_page", map[string]any{"Blocks": blocks}, RenderOptions{Format: "js", Layout: view.NoLayout})
}
