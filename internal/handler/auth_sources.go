package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/auth/ldap"
	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
)

// AuthSourcesController（app/controllers/auth_sources_controller.rb）。layout 'admin'、menu_item :ldap_authentication。
//
// 対応する種類は AuthSourceLdap（kind = ldap）のみ。一覧は OIDC（kind = oidc。別タスク）の行も表示する。
// require_sudo_mode :update, :destroy は buropher に sudo モードが無いため未実装。
var AuthSourcesController = &Controller{Name: "auth_sources", MainMenu: false}

// routesAuthSources は auth_sources コントローラのルートを登録する。
//
//	resources :auth_sources do
//	  member do
//	    get 'test_connection', :as => 'try_connection'
//	  end
//	  collection do
//	    get 'autocomplete_for_new_user'
//	  end
//	end
func (a *App) routesAuthSources(r Router) {
	find := Before(a.findAuthSource)
	a.Handle(r, http.MethodGet, "/auth_sources/autocomplete_for_new_user", AuthSourcesController, "autocomplete_for_new_user", a.AuthSourcesAutocompleteForNewUser, RequireAdmin())
	a.Handle(r, http.MethodGet, "/auth_sources", AuthSourcesController, "index", a.AuthSourcesIndex, RequireAdmin())
	a.Handle(r, http.MethodPost, "/auth_sources", AuthSourcesController, "create", a.AuthSourcesCreate, RequireAdmin())
	a.Handle(r, http.MethodGet, "/auth_sources/new", AuthSourcesController, "new", a.AuthSourcesNew, RequireAdmin())
	a.Handle(r, http.MethodGet, "/auth_sources/{id}/edit", AuthSourcesController, "edit", a.AuthSourcesEdit, RequireAdmin(), find)
	a.Handle(r, http.MethodGet, "/auth_sources/{id}/test_connection", AuthSourcesController, "test_connection", a.AuthSourcesTestConnection, RequireAdmin(), find)
	// show はアクションが無い（AbstractController::ActionNotFound → 404）
	a.Handle(r, http.MethodPatch, "/auth_sources/{id}", AuthSourcesController, "update", a.AuthSourcesUpdate, RequireAdmin(), find)
	a.Handle(r, http.MethodPut, "/auth_sources/{id}", AuthSourcesController, "update", a.AuthSourcesUpdate, RequireAdmin(), find)
	a.Handle(r, http.MethodDelete, "/auth_sources/{id}", AuthSourcesController, "destroy", a.AuthSourcesDestroy, RequireAdmin(), find)
}

const ctxAuthSource = "auth_source"

// findAuthSource は before_action :find_auth_source。
func (a *App) findAuthSource(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	rec, err := repository.GetAuthSource(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find auth source", err)
		}
		return
	}
	c.setLocal(ctxAuthSource, rec)
}

func (c *Req) authSource() *domain.AuthSourceRecord {
	rec, _ := c.local(ctxAuthSource).(*domain.AuthSourceRecord)
	return rec
}

// ---------------------------------------------------------------- フォームモデル（AuthSourceLdap）

// ldapSourceForm は編集中の AuthSourceLdap（@auth_source）。文字列属性の nil は *string の nil、
// 整数属性（port / timeout）は代入された生の値（before_type_cast）を持つ。
type ldapSourceForm struct {
	rec                                    *domain.AuthSourceRecord
	name                                   string
	host, account, baseDN, filter          *string
	attrLogin, attrFirstname, attrLastname *string
	attrMail                               *string
	port, timeout                          any
	tls, verifyPeer, onthefly              bool
	password                               string
	passwordChanged                        bool
	errors                                 *validation.Errors
	loc                                    *i18n.Localizer
}

// ParamKey は form の as: :auth_source。
func (f *ldapSourceForm) ParamKey() string       { return "auth_source" }
func (f *ldapSourceForm) Persisted() bool        { return f.rec.ID != 0 }
func (f *ldapSourceForm) ToParam() string        { return strconv.FormatInt(f.rec.ID, 10) }
func (f *ldapSourceForm) ID() int64              { return f.rec.ID }
func (f *ldapSourceForm) Name() string           { return f.name }
func (f *ldapSourceForm) Type() string           { return "AuthSourceLdap" }
func (f *ldapSourceForm) NewRecord() bool        { return f.rec.ID == 0 }
func (f *ldapSourceForm) AuthMethodName() string { return ldap.AuthMethodName }

// PasswordDisplay は account_password 欄の value（保存済みのパスワードがあれば 'x'*15）。
func (f *ldapSourceForm) PasswordDisplay() string {
	if f.rec.ID == 0 || f.password == "" {
		return ""
	}
	return strings.Repeat("x", 15)
}

// ValidationErrors は errors（error_messages_for 用）。
func (f *ldapSourceForm) ValidationErrors() *validation.Errors { return f.errors }

// ErrorsOn は errors[attr]。
func (f *ldapSourceForm) ErrorsOn(attr string) []string { return f.errors.Messages(f.loc, attr) }

// HumanAttributeName は AuthSourceLdap.human_attribute_name（field_auth_source_ldap_<attr> → field_<attr>）。
func (f *ldapSourceForm) HumanAttributeName(attr string) string {
	return validation.HumanAttributeName(f.loc, "auth_source_ldap", attr)
}

func strPtrOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// LDAPMode は ldap_mode。
func (f *ldapSourceForm) LDAPMode() string {
	switch {
	case f.tls && f.verifyPeer:
		return "ldaps_verify_peer"
	case f.tls:
		return "ldaps_verify_none"
	}
	return "ldap"
}

// Send はフォームビルダの属性参照（value_before_type_cast）。
func (f *ldapSourceForm) Send(method string) (any, bool) {
	switch method {
	case "name":
		return f.name, true
	case "host":
		return strPtrOrNil(f.host), true
	case "port":
		return f.port, true
	case "account":
		return strPtrOrNil(f.account), true
	case "base_dn":
		return strPtrOrNil(f.baseDN), true
	case "filter":
		return strPtrOrNil(f.filter), true
	case "timeout":
		return f.timeout, true
	case "attr_login":
		return strPtrOrNil(f.attrLogin), true
	case "attr_firstname":
		return strPtrOrNil(f.attrFirstname), true
	case "attr_lastname":
		return strPtrOrNil(f.attrLastname), true
	case "attr_mail":
		return strPtrOrNil(f.attrMail), true
	case "onthefly_register":
		return f.onthefly, true
	case "tls":
		return f.tls, true
	case "verify_peer":
		return f.verifyPeer, true
	case "ldap_mode":
		return f.LDAPMode(), true
	}
	return nil, false
}

func configStrPtr(rec *domain.AuthSourceRecord, key string) *string {
	if s, ok := rec.ConfigString(key); ok {
		return &s
	}
	return nil
}

func configIntValue(rec *domain.AuthSourceRecord, key string) any {
	if n, ok := rec.ConfigInt(key); ok {
		return n
	}
	return nil
}

// newLDAPSourceForm は保存済み（または新規）の行からフォームモデルを作る。
func (a *App) newLDAPSourceForm(c *Req, rec *domain.AuthSourceRecord) *ldapSourceForm {
	f := &ldapSourceForm{rec: rec, name: rec.Name, errors: validation.New("auth_source_ldap"), loc: c.Loc,
		host: configStrPtr(rec, "host"), account: configStrPtr(rec, "account"), baseDN: configStrPtr(rec, "base_dn"),
		filter: configStrPtr(rec, "filter"), attrLogin: configStrPtr(rec, "attr_login"),
		attrFirstname: configStrPtr(rec, "attr_firstname"), attrLastname: configStrPtr(rec, "attr_lastname"),
		attrMail: configStrPtr(rec, "attr_mail"), port: configIntValue(rec, "port"), timeout: configIntValue(rec, "timeout"),
		tls: rec.ConfigBool("tls"), verifyPeer: rec.ConfigBool("verify_peer"), onthefly: rec.OntheflyRegister}
	if rec.ID == 0 {
		// verify_peer の DB 既定値は true
		f.verifyPeer = true
	}
	f.password = a.openSecret(rec.Secret)
	return f
}

// openSecret は read_ciphered_attribute（復号できなければ空文字列）。
func (a *App) openSecret(s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	if !secretbox.IsSealed(*s) {
		// 平文で保存されている（暗号化鍵の無い Redmine から移行した値など）
		return *s
	}
	if a.Secrets == nil {
		return ""
	}
	p, err := a.Secrets.Open(*s)
	if err != nil {
		a.logger().Error("auth source secret", "err", err)
		return ""
	}
	return p
}

// assign は @auth_source.safe_attributes = params[:auth_source]（パラメータの順に代入する）。
func (f *ldapSourceForm) assign(p *httpx.Params) {
	if p == nil {
		return
	}
	for _, k := range p.Keys() {
		v, _ := p.Get(k)
		if _, ok := v.(string); !ok && v != nil {
			// 配列・ハッシュは文字列属性に代入しない（strong parameters で除かれる）
			continue
		}
		s := httpx.ValueString(v)
		sp := &s
		if v == nil {
			sp = nil
		}
		switch k {
		case "name":
			f.name = s
		case "host":
			f.host = sp
		case "port":
			f.port = v
		case "account":
			f.account = sp
		case "account_password":
			f.password = s
			f.passwordChanged = true
		case "base_dn":
			f.baseDN = sp
		case "attr_login":
			f.attrLogin = sp
		case "attr_firstname":
			f.attrFirstname = sp
		case "attr_lastname":
			f.attrLastname = sp
		case "attr_mail":
			f.attrMail = sp
		case "onthefly_register":
			f.onthefly = castBool(s)
		case "tls":
			f.tls = castBool(s)
		case "verify_peer":
			f.verifyPeer = castBool(s)
		case "filter":
			f.filter = sp
		case "timeout":
			f.timeout = v
		case "ldap_mode":
			switch s {
			case "ldaps_verify_peer":
				f.tls, f.verifyPeer = true, true
			case "ldaps_verify_none":
				f.tls, f.verifyPeer = true, false
			default:
				f.tls, f.verifyPeer = false, false
			}
		}
	}
}

var (
	reInteger = regexp.MustCompile(`\A[+-]?\d+\z`)
	reNumber  = regexp.MustCompile(`\A[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?\z`)
)

// castInteger は ActiveModel::Type::Integer の cast（"" → nil、数値でない文字列は to_i）。
func castInteger(v any) (int, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int:
		return x, true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		if reNumber.MatchString(s) && !reInteger.MatchString(s) {
			fl, _ := strconv.ParseFloat(s, 64)
			return int(fl), true
		}
		return int(httpx.RubyToI(s)), true
	}
	return 0, false
}

// validateNumericality は validates_numericality_of（only_integer）。allowBlank は allow_blank。
func validateNumericality(e *validation.Errors, attr string, v any, allowBlank bool) {
	var raw string
	switch x := v.(type) {
	case nil:
		if !allowBlank {
			e.Add(attr, "not_a_number")
		}
		return
	case int:
		return
	case string:
		raw = strings.TrimSpace(x)
	}
	if raw == "" {
		if !allowBlank {
			e.Add(attr, "not_a_number")
		}
		return
	}
	if !reNumber.MatchString(raw) {
		e.Add(attr, "not_a_number")
	} else if !reInteger.MatchString(raw) {
		e.Add(attr, "not_an_integer")
	}
}

func stripPtr(p *string) *string {
	if p == nil {
		return nil
	}
	s := strings.TrimSpace(*p)
	return &s
}

func blankPtr(p *string) bool { return p == nil || strings.TrimSpace(*p) == "" }

func runeLen(p *string) int {
	if p == nil {
		return 0
	}
	return utf8.RuneCountInString(*p)
}

// validate は AuthSource と AuthSourceLdap の検証（宣言順）。before_validation :strip_ldap_attributes を含む。
func (a *App) validateLDAPSource(c *Req, f *ldapSourceForm) error {
	f.attrLogin, f.attrFirstname, f.attrLastname, f.attrMail = stripPtr(f.attrLogin), stripPtr(f.attrFirstname), stripPtr(f.attrLastname), stripPtr(f.attrMail)
	e := f.errors
	e.Clear()
	// AuthSource
	if strings.TrimSpace(f.name) == "" {
		e.Add("name", "blank")
	}
	taken, err := repository.AuthSourceNameTaken(c.Ctx(), a.DB, f.name, f.rec.ID)
	if err != nil {
		return err
	}
	if taken {
		e.Add("name", "taken")
	}
	if utf8.RuneCountInString(f.name) > 60 {
		e.Add("name", "too_long", "count", 60)
	}
	// AuthSourceLdap
	if blankPtr(f.host) {
		e.Add("host", "blank")
	}
	if _, ok := castInteger(f.port); !ok {
		e.Add("port", "blank")
	}
	if blankPtr(f.attrLogin) {
		e.Add("attr_login", "blank")
	}
	if utf8.RuneCountInString(f.name) > 60 {
		e.Add("name", "too_long", "count", 60)
	}
	if runeLen(f.host) > 60 {
		e.Add("host", "too_long", "count", 60)
	}
	if runeLen(f.account) > 255 {
		e.Add("account", "too_long", "count", 255)
	}
	if utf8.RuneCountInString(f.password) > 255 {
		e.Add("account_password", "too_long", "count", 255)
	}
	if runeLen(f.baseDN) > 255 {
		e.Add("base_dn", "too_long", "count", 255)
	}
	for _, x := range []struct {
		attr string
		v    *string
	}{{"attr_login", f.attrLogin}, {"attr_firstname", f.attrFirstname}, {"attr_lastname", f.attrLastname}, {"attr_mail", f.attrMail}} {
		if runeLen(x.v) > 30 {
			e.Add(x.attr, "too_long", "count", 30)
		}
	}
	validateNumericality(e, "port", f.port, false)
	validateNumericality(e, "timeout", f.timeout, true)
	if f.filter != nil && strings.TrimSpace(*f.filter) != "" && !ldap.ValidFilter(*f.filter) {
		e.Add("filter", "invalid")
	}
	return nil
}

// save は @auth_source.save（検証に失敗したら false）。
func (a *App) saveLDAPSource(c *Req, f *ldapSourceForm) (bool, error) {
	if err := a.validateLDAPSource(c, f); err != nil {
		return false, err
	}
	if f.errors.Any() {
		return false, nil
	}
	rec := f.rec
	cfg := map[string]any{}
	for k, v := range rec.Config {
		cfg[k] = v
	}
	setStr := func(key string, p *string) {
		if p == nil {
			delete(cfg, key)
		} else {
			cfg[key] = *p
		}
	}
	setInt := func(key string, v any) {
		if n, ok := castInteger(v); ok {
			cfg[key] = n
		} else {
			delete(cfg, key)
		}
	}
	setStr("host", f.host)
	setInt("port", f.port)
	setStr("account", f.account)
	setStr("base_dn", f.baseDN)
	setStr("filter", f.filter)
	setInt("timeout", f.timeout)
	cfg["tls"] = f.tls
	cfg["verify_peer"] = f.verifyPeer
	setStr("attr_login", f.attrLogin)
	setStr("attr_firstname", f.attrFirstname)
	setStr("attr_lastname", f.attrLastname)
	setStr("attr_mail", f.attrMail)
	rec.Config = cfg
	rec.Name = f.name
	rec.OntheflyRegister = f.onthefly
	rec.Kind = domain.AuthSourceKindLDAP
	if rec.ID == 0 {
		rec.Enabled = true
		rec.CreatedAt = a.now()
	}
	rec.UpdatedAt = a.now()
	// write_ciphered_attribute(:account_password, arg)
	if !f.passwordChanged {
		// 変更なし（dummy_password のまま）
	} else if f.password == "" {
		rec.Secret = nil
	} else if a.Secrets != nil {
		sealed, err := a.Secrets.Seal(f.password)
		if err != nil {
			return false, err
		}
		rec.Secret = &sealed
	} else {
		p := f.password
		rec.Secret = &p
	}
	if err := repository.SaveAuthSource(c.Ctx(), a.DB, rec); err != nil {
		return false, err
	}
	return true, nil
}

// ldapSource は保存済みの行から LDAP 接続設定を作る（kind が ldap でなければ nil）。
func (a *App) ldapSource(rec *domain.AuthSourceRecord) *ldap.Source {
	if rec.Kind != domain.AuthSourceKindLDAP {
		return nil
	}
	str := func(k string) string { s, _ := rec.ConfigString(k); return s }
	port, _ := rec.ConfigInt("port")
	timeout, _ := rec.ConfigInt("timeout")
	return &ldap.Source{
		ID: rec.ID, Name: rec.Name, Host: str("host"), Port: port, Account: str("account"),
		AccountPassword: a.openSecret(rec.Secret), BaseDN: str("base_dn"), Filter: str("filter"), Timeout: timeout,
		TLS: rec.ConfigBool("tls"), VerifyPeer: rec.ConfigBool("verify_peer"), StartTLS: rec.ConfigBool("starttls"),
		AttrLogin: str("attr_login"), AttrFirstname: str("attr_firstname"), AttrLastname: str("attr_lastname"),
		AttrMail: str("attr_mail"), OntheflyRegister: rec.OntheflyRegister,
	}
}

// ---------------------------------------------------------------- アクション

// authSourceRow は一覧の 1 行。
type authSourceRow struct {
	*domain.AuthSourceRecord
	UserCount int
}

// AuthSourcesIndex は auth_sources#index（paginate AuthSource, :per_page => 25）。
func (a *App) AuthSourcesIndex(c *Req) {
	ctx := c.Ctx()
	count, err := repository.CountAuthSources(ctx, a.DB)
	if err != nil {
		a.internalError(c, "count auth sources", err)
		return
	}
	pages := pagination.New(count, 25, c.Params().String("page"))
	recs, err := repository.AuthSourcePage(ctx, a.DB, pages.PerPage, pages.Offset())
	if err != nil {
		a.internalError(c, "list auth sources", err)
		return
	}
	counts, err := repository.AuthSourceUserCounts(ctx, a.DB)
	if err != nil {
		a.internalError(c, "auth source users", err)
		return
	}
	rows := make([]authSourceRow, len(recs))
	for i, r := range recs {
		rows[i] = authSourceRow{AuthSourceRecord: r, UserCount: counts[r.ID]}
	}
	c.renderAdmin("auth_sources/index", map[string]any{"AuthSources": rows, "Pages": pages}, false)
}

// buildNewAuthSource は before_action :build_new_auth_source（type は AuthSourceLdap のみ）。
func (a *App) buildNewAuthSource(c *Req) *ldapSourceForm {
	if t := c.Params().String("type"); t != "" && t != "AuthSourceLdap" {
		c.Render404("")
		return nil
	}
	f := a.newLDAPSourceForm(c, &domain.AuthSourceRecord{Kind: domain.AuthSourceKindLDAP, Config: map[string]any{}})
	f.assign(c.Params().Map("auth_source"))
	return f
}

// AuthSourcesNew は auth_sources#new。
func (a *App) AuthSourcesNew(c *Req) {
	f := a.buildNewAuthSource(c)
	if f == nil {
		return
	}
	c.NoStore()
	c.renderAdmin("auth_sources/new", map[string]any{"AuthSource": f}, false)
}

// AuthSourcesCreate は auth_sources#create。
func (a *App) AuthSourcesCreate(c *Req) {
	f := a.buildNewAuthSource(c)
	if f == nil {
		return
	}
	ok, err := a.saveLDAPSource(c, f)
	if err != nil {
		a.internalError(c, "create auth source", err)
		return
	}
	if ok {
		c.Flash().SetNotice(c.L("notice_successful_create"))
		c.Redirect("/auth_sources")
		return
	}
	c.NoStore()
	c.renderAdmin("auth_sources/new", map[string]any{"AuthSource": f}, false)
}

// AuthSourcesEdit は auth_sources#edit。
func (a *App) AuthSourcesEdit(c *Req) {
	rec := c.authSource()
	if rec.Kind != domain.AuthSourceKindLDAP {
		// TODO(oidc): OIDC の編集画面は別タスク
		c.Render404("")
		return
	}
	c.NoStore()
	c.renderAdmin("auth_sources/edit", map[string]any{"AuthSource": a.newLDAPSourceForm(c, rec)}, false)
}

// AuthSourcesUpdate は auth_sources#update。
func (a *App) AuthSourcesUpdate(c *Req) {
	rec := c.authSource()
	if rec.Kind != domain.AuthSourceKindLDAP {
		c.Render404("")
		return
	}
	f := a.newLDAPSourceForm(c, rec)
	f.assign(c.Params().Map("auth_source"))
	ok, err := a.saveLDAPSource(c, f)
	if err != nil {
		a.internalError(c, "update auth source", err)
		return
	}
	if ok {
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.Redirect("/auth_sources")
		return
	}
	c.NoStore()
	c.renderAdmin("auth_sources/edit", map[string]any{"AuthSource": f}, false)
}

// AuthSourcesTestConnection は auth_sources#test_connection。
func (a *App) AuthSourcesTestConnection(c *Req) {
	src := a.ldapSource(c.authSource())
	var err error
	if src != nil {
		err = src.TestConnection(c.L("error_ldap_bind_credentials"))
	}
	if err != nil {
		c.Flash().SetError(c.L("error_unable_to_connect", map[string]any{"value": err.Error()}))
	} else {
		c.Flash().SetNotice(c.L("notice_successful_connection"))
	}
	c.Redirect("/auth_sources")
}

// AuthSourcesDestroy は auth_sources#destroy（ユーザーが使っていれば削除しない）。
func (a *App) AuthSourcesDestroy(c *Req) {
	rec := c.authSource()
	counts, err := repository.AuthSourceUserCounts(c.Ctx(), a.DB)
	if err != nil {
		a.internalError(c, "auth source users", err)
		return
	}
	if counts[rec.ID] == 0 {
		if err := repository.DeleteAuthSource(c.Ctx(), a.DB, rec.ID); err != nil {
			a.internalError(c, "delete auth source", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_delete"))
	} else {
		c.Flash().SetError(c.L("error_can_not_delete_auth_source"))
	}
	c.Redirect("/auth_sources")
}

// autocompleteEntry は autocomplete_for_new_user の 1 件（キーの順序は Redmine と同じ）。
type autocompleteEntry struct {
	Value        string `json:"value"`
	Label        string `json:"label"`
	Login        string `json:"login"`
	Firstname    string `json:"firstname"`
	Lastname     string `json:"lastname"`
	Mail         string `json:"mail"`
	AuthSourceID string `json:"auth_source_id"`
}

// searchAuthSources は AuthSource.search(q)（検索可能なソースを順に検索し、例外は記録して続ける）。
func (a *App) searchAuthSources(c *Req, q string) ([]ldap.Attrs, error) {
	recs, err := repository.AllAuthSources(c.Ctx(), a.DB)
	if err != nil {
		return nil, err
	}
	var out []ldap.Attrs
	for _, rec := range recs {
		src := a.ldapSource(rec)
		if src == nil || !src.Searchable() {
			continue
		}
		res, err := src.Search(q)
		if err != nil {
			a.logger().Error("Error while searching users in "+rec.Name, "err", err)
			continue
		}
		out = append(out, res...)
	}
	return out, nil
}

// AuthSourcesAutocompleteForNewUser は auth_sources#autocomplete_for_new_user（render :json）。
func (a *App) AuthSourcesAutocompleteForNewUser(c *Req) {
	res, err := a.searchAuthSources(c, c.Params().String("term"))
	if err != nil {
		a.internalError(c, "search auth sources", err)
		return
	}
	entries := make([]autocompleteEntry, 0, len(res))
	for _, r := range res {
		entries = append(entries, autocompleteEntry{
			Value: r.Login, Label: r.Login + " (" + r.Firstname + " " + r.Lastname + ")", Login: r.Login,
			Firstname: r.Firstname, Lastname: r.Lastname, Mail: r.Mail, AuthSourceID: strconv.FormatInt(r.AuthSourceID, 10),
		})
	}
	b, _ := json.Marshal(entries)
	c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(b)
	c.Halt()
}
