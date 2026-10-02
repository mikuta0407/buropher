package handler

import (
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/view"
)

// このファイルは lib/redmine/sudo_mode.rb（Redmine::SudoMode）の移植。
//
// Redmine と同じく既定では無効（config の [auth] sudo_mode = true で有効化）。有効なときは
// require_sudo_mode を宣言したアクションで、最後のパスワード確認から sudo_mode_timeout（既定 15 分）を
// 過ぎていればパスワード再入力フォーム（sudo_mode/new）を表示し、元のパラメータを hidden で引き継ぐ。
// セッションの sudo 時刻はログイン直後（handle_active_user）と sudo を使ったリクエストで更新する。

// sudoRequirement は require_sudo_mode の宣言（actions と only: のメソッド）。
type sudoRequirement struct {
	actions []string
	methods []string
}

// sudoModeTable は既存コントローラの require_sudo_mode 宣言（コントローラ名 → 宣言）。
// 各コントローラの before_action の後で適用する（Redmine では宣言順に実行される）。
var sudoModeTable = map[string][]sudoRequirement{
	"settings":        {{actions: []string{"index", "edit", "plugin"}}},
	"email_addresses": {{actions: []string{"create", "update", "destroy"}}},
	"auth_sources":    {{actions: []string{"update", "destroy"}}},
	"my": {
		{actions: []string{"account"}, methods: []string{http.MethodPut}},
		{actions: []string{"reset_atom_key", "reset_api_key", "show_api_key", "destroy"}},
	},
	"projects": {{actions: []string{"destroy", "bulk_destroy"}}},
	"groups":   {{actions: []string{"add_users", "remove_user", "create", "update", "destroy", "edit_membership", "destroy_membership"}}},
	"users":    {{actions: []string{"create", "update", "destroy"}}},
	"roles":    {{actions: []string{"create", "update", "destroy"}}},
	"members":  {{actions: []string{"create", "update", "destroy"}}},
}

// adminLayoutControllers は require_sudo_mode を宣言するコントローラのうち layout 'admin' のもの。
var adminLayoutControllers = []string{"settings", "auth_sources", "groups", "users", "roles"}

// sudoTimeout は SudoMode.timeout。
func (a *App) sudoTimeout() time.Duration {
	if a.SudoModeTimeout > 0 {
		return a.SudoModeTimeout
	}
	return 15 * time.Minute
}

// sudoModeTableFilter は sudoModeTable の宣言を適用する（runBeforeActions の最後）。
func (a *App) sudoModeTableFilter(c *Req) {
	if !a.SudoMode || c.Controller == nil {
		return
	}
	for _, req := range sudoModeTable[c.Controller.Name] {
		if slices.Contains(req.actions, c.Action) {
			a.sudoRequestFilter(c, req.methods)
			return
		}
	}
}

// RequireSudoMode は require_sudo_mode（新しく移植するコントローラで before_action の位置に置く）。
// methods は only: のメソッド（空ならすべて）。
func RequireSudoMode(methods ...string) ActionOption {
	return func(cfg *actionConfig) {
		cfg.before = append(cfg.before, func(c *Req) { c.App.sudoRequestFilter(c, methods) })
	}
}

// sudoRequestFilter は SudoRequestFilter#before。
func (a *App) sudoRequestFilter(c *Req, methods []string) {
	if httpx.IsAPIRequest(c.R) {
		return
	}
	if !a.sudoPossible(c) {
		return
	}
	if len(methods) > 0 && !slices.Contains(methods, c.R.Method) {
		return
	}
	a.requireSudoMode(c, nil)
}

// sudoPossible は SudoMode.possible?（有効かつログイン中）。
func (a *App) sudoPossible(c *Req) bool { return a.SudoMode && c.User.Logged() }

// sudoTimestampValid は sudo_timestamp_valid?。
func (a *App) sudoTimestampValid(c *Req) bool {
	s := c.Session()
	if s == nil || s.SudoAt().IsZero() {
		return false
	}
	return s.SudoAt().Unix() > a.now().Add(-a.sudoTimeout()).Unix()
}

// updateSudoTimestamp は update_sudo_timestamp!。
func (a *App) updateSudoTimestamp(c *Req) {
	if s := c.Session(); s != nil {
		s.SetSudoAt(a.now())
	}
}

// sudoExcludedParams は require_sudo_mode が引き継がないパラメータ。
var sudoExcludedParams = []string{"id", "action", "controller", "sudo_password", "_method", "authenticity_token", "utf8"}

// requireSudoMode は Redmine::SudoMode::Controller#require_sudo_mode。sudo が有効なら true。
// 無効ならパスワード入力フォームを描画して false を返す（c.Halted() になる）。
func (a *App) requireSudoMode(c *Req, paramNames []string) bool {
	if a.sudoTimestampValid(c) {
		// SudoMode.active? → was_used → update_sudo_timestamp!
		a.updateSudoTimestamp(c)
		return true
	}
	p := c.Params()
	if len(paramNames) == 0 {
		for _, k := range p.Keys() {
			if !slices.Contains(sudoExcludedParams, k) {
				paramNames = append(paramNames, k)
			}
		}
	}
	// process_sudo_form
	if p.Has("sudo_password") {
		pw := p.String("sudo_password")
		ok := false
		if pw != "" {
			var err error
			ok, err = a.checkPassword(c, c.User, pw)
			if err != nil {
				a.logger().Error("sudo mode password check", "err", err)
			}
		}
		if ok {
			a.updateSudoTimestamp(c)
			// 以降のアクションが sudo_password を見ないよう取り除く
			p.Delete("sudo_password")
			return true
		}
		c.Flash().Now("error", c.L("notice_account_wrong_password"))
	}
	a.renderSudoForm(c, p.Only(paramNames...))
	return false
}

// hiddenField は hash_to_hidden_fields の 1 件。
type hiddenField struct{ Name, Value string }

// toQueryPairs は Hash#to_query の (キー, 値) の列（エスケープした文字列でソート）。
func toQueryPairs(prefix string, v any, out *[]string) {
	switch x := v.(type) {
	case nil:
		if prefix != "" {
			*out = append(*out, url.QueryEscape(prefix)+"=")
		}
	case map[string]any:
		if len(x) == 0 && prefix != "" {
			*out = append(*out, url.QueryEscape(prefix)+"=")
			return
		}
		for k, e := range x {
			key := k
			if prefix != "" {
				key = prefix + "[" + k + "]"
			}
			toQueryPairs(key, e, out)
		}
	case []any:
		if len(x) == 0 {
			*out = append(*out, url.QueryEscape(prefix+"[]")+"=")
			return
		}
		for _, e := range x {
			toQueryPairs(prefix+"[]", e, out)
		}
	case *httpx.UploadedFile:
		// ファイルは引き継げない
	default:
		*out = append(*out, url.QueryEscape(prefix)+"="+url.QueryEscape(httpx.ValueString(x)))
	}
}

// hashToHiddenFields は Redmine::SudoMode::Helper#hash_to_hidden_fields。
func hashToHiddenFields(p *httpx.Params) []hiddenField {
	m := p.ToMap()
	// to_unsafe_h.compact（値が nil のトップレベルのキーを除く）
	for k, v := range m {
		if v == nil {
			delete(m, k)
		}
	}
	var pairs []string
	toQueryPairs("", m, &pairs)
	sort.Strings(pairs)
	out := make([]hiddenField, 0, len(pairs))
	for _, pair := range pairs {
		k, v, _ := strings.Cut(pair, "=")
		ku, _ := url.QueryUnescape(k)
		vu, _ := url.QueryUnescape(v)
		out = append(out, hiddenField{Name: ku, Value: vu})
	}
	return out
}

// renderSudoForm は render_sudo_form（HTML は sudo_mode/new、XHR は sudo_mode/new.js）。
func (a *App) renderSudoForm(c *Req, original *httpx.Params) {
	c.NoStore()
	method := c.R.Method
	data := map[string]any{
		"Method":         method,
		"OriginalFields": hashToHiddenFields(original),
		"Action":         c.R.URL.Path,
	}
	if httpx.Negotiate(c.R, "html", "js") == "js" {
		c.Render("sudo_mode/new", data, RenderOptions{Format: "js", Layout: view.NoLayout})
		return
	}
	// コントローラのレイアウト（layout 'admin' のコントローラは管理画面のレイアウト）で描画する
	if c.Controller != nil && slices.Contains(adminLayoutControllers, c.Controller.Name) {
		c.renderAdmin("sudo_mode/new", data, false)
		return
	}
	c.Render("sudo_mode/new", data)
}
