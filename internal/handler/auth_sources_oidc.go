package handler

import (
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/auth/oidc"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
)

// このファイルは認証方式「OIDC」（kind = oidc。buropher 拡張）の管理画面。
// Redmine の認証方式の画面（auth_sources/new・edit）と同じ部品（box tabular・fieldset）で作り、
// ルートは auth_sources のもの（new?type=AuthSourceOidc / create / edit / update / test_connection / destroy）を共有する。

// oidcTypeName は type パラメータの値（Redmine の STI のクラス名に合わせた名前）。
const oidcTypeName = "AuthSourceOidc"

// oidcStringFields は config に保存する文字列属性（フォームの表示順）。
var oidcStringFields = []string{
	"preset", "issuer", "tenant", "client_id", "scopes",
	"claim_subject", "claim_login", "claim_firstname", "claim_lastname", "claim_mail", "claim_groups", "claim_tenant",
	"match_by", "allowed_tenants", "allowed_groups", "button_label",
}

// oidcBoolFields は config に保存する真偽値属性。
var oidcBoolFields = []string{"group_sync", "sso_required", "skip_twofa", "rp_logout"}

// oidcSourceForm は編集中の OIDC 認証方式（@auth_source）。
type oidcSourceForm struct {
	rec             *domain.AuthSourceRecord
	name            string
	strs            map[string]string
	bools           map[string]bool
	enabled         bool
	onthefly        bool
	secret          string
	secretChanged   bool
	mappings        []domain.AuthSourceGroupMapping
	mappingsChanged bool
	errors          *validation.Errors
	loc             *i18n.Localizer
	callbackURL     string
}

func (f *oidcSourceForm) ParamKey() string                     { return "auth_source" }
func (f *oidcSourceForm) Persisted() bool                      { return f.rec.ID != 0 }
func (f *oidcSourceForm) ToParam() string                      { return strconv.FormatInt(f.rec.ID, 10) }
func (f *oidcSourceForm) ID() int64                            { return f.rec.ID }
func (f *oidcSourceForm) Name() string                         { return f.name }
func (f *oidcSourceForm) Type() string                         { return oidcTypeName }
func (f *oidcSourceForm) NewRecord() bool                      { return f.rec.ID == 0 }
func (f *oidcSourceForm) AuthMethodName() string               { return "OIDC" }
func (f *oidcSourceForm) ValidationErrors() *validation.Errors { return f.errors }
func (f *oidcSourceForm) ErrorsOn(attr string) []string        { return f.errors.Messages(f.loc, attr) }
func (f *oidcSourceForm) CallbackURL() string                  { return f.callbackURL }
func (f *oidcSourceForm) Mappings() []domain.AuthSourceGroupMapping {
	// 既存の対応表 + 空行 3 つ
	return append(slices.Clone(f.mappings), domain.AuthSourceGroupMapping{}, domain.AuthSourceGroupMapping{}, domain.AuthSourceGroupMapping{})
}

// SecretDisplay は client_secret 欄の value（保存済みなら 'x'*15）。
func (f *oidcSourceForm) SecretDisplay() string {
	if f.rec.ID == 0 || f.secret == "" {
		return ""
	}
	return strings.Repeat("x", 15)
}

// HumanAttributeName は属性名（name 等の Redmine の訳文があるものはそれ、ほかは buropher.sso.field_<attr>）。
func (f *oidcSourceForm) HumanAttributeName(attr string) string {
	switch attr {
	case "name", "onthefly_register":
		return validation.HumanAttributeName(f.loc, "auth_source", attr)
	case "enabled":
		return f.loc.L("field_active")
	}
	return f.loc.L("buropher.sso.field_" + attr)
}

// Send はフォームビルダの属性参照。
func (f *oidcSourceForm) Send(method string) (any, bool) {
	switch method {
	case "name":
		return f.name, true
	case "onthefly_register":
		return f.onthefly, true
	case "enabled":
		return f.enabled, true
	}
	if v, ok := f.strs[method]; ok {
		return v, true
	}
	if slices.Contains(oidcStringFields, method) {
		return "", true
	}
	if slices.Contains(oidcBoolFields, method) {
		return f.bools[method], true
	}
	return nil, false
}

// newOIDCSourceForm は保存済み（または新規）の行からフォームモデルを作る。
func (a *App) newOIDCSourceForm(c *Req, rec *domain.AuthSourceRecord) *oidcSourceForm {
	f := &oidcSourceForm{rec: rec, name: rec.Name, strs: map[string]string{}, bools: map[string]bool{},
		enabled: rec.Enabled, onthefly: rec.OntheflyRegister, errors: validation.New("auth_source"), loc: c.Loc}
	// 検証エラーの属性名は buropher.sso.field_<attr>
	f.errors.AttrNames = map[string]string{}
	for _, k := range append(slices.Clone(oidcStringFields), oidcBoolFields...) {
		f.errors.AttrNames[k] = "buropher.sso.field_" + k
	}
	for _, k := range oidcStringFields {
		if s, ok := rec.ConfigString(k); ok {
			f.strs[k] = s
		}
	}
	for _, k := range oidcBoolFields {
		f.bools[k] = rec.ConfigBool(k)
	}
	if rec.ID == 0 {
		f.enabled = true
		f.strs["preset"] = oidc.PresetGeneric
		f.strs["scopes"] = "openid profile email"
		f.strs["match_by"] = "mail"
		f.bools["skip_twofa"] = true
	} else {
		f.callbackURL = a.externalURL(c, "/auth/oidc/"+strconv.FormatInt(rec.ID, 10)+"/callback")
		ms, err := repository.AuthSourceGroupMappings(c.Ctx(), a.DB, rec.ID)
		if err != nil {
			a.logger().Error("group mappings", "err", err)
		}
		f.mappings = ms
	}
	f.secret = a.openSecret(rec.Secret)
	return f
}

// assign は params[:auth_source] とグループ対応表のパラメータを代入する。
func (f *oidcSourceForm) assign(p *httpx.Params, all *httpx.Params) {
	if p != nil {
		for _, k := range p.Keys() {
			v, _ := p.Get(k)
			if _, ok := v.(string); !ok && v != nil {
				continue
			}
			s := httpx.ValueString(v)
			switch {
			case k == "name":
				f.name = s
			case k == "client_secret":
				f.secret = s
				f.secretChanged = true
			case k == "enabled":
				f.enabled = castBoolAny(s)
			case k == "onthefly_register":
				f.onthefly = castBoolAny(s)
			case slices.Contains(oidcStringFields, k):
				f.strs[k] = s
			case slices.Contains(oidcBoolFields, k):
				f.bools[k] = castBoolAny(s)
			}
		}
	}
	if all != nil && all.Has("group_mapping_external") {
		ext := all.Strings("group_mapping_external")
		gids := all.Strings("group_mapping_group_id")
		var ms []domain.AuthSourceGroupMapping
		for i, e := range ext {
			e = strings.TrimSpace(e)
			if e == "" || i >= len(gids) {
				continue
			}
			gid, err := strconv.ParseInt(gids[i], 10, 64)
			if err != nil || gid <= 0 {
				continue
			}
			ms = append(ms, domain.AuthSourceGroupMapping{ExternalGroup: e, GroupID: gid})
		}
		f.mappings = ms
		f.mappingsChanged = true
	}
}

// validateOIDCSource は OIDC 認証方式の検証。
func (a *App) validateOIDCSource(c *Req, f *oidcSourceForm) error {
	e := f.errors
	e.Clear()
	if strings.TrimSpace(f.name) == "" {
		e.Add("name", "blank")
	} else {
		taken, err := repository.AuthSourceNameTaken(c.Ctx(), a.DB, f.name, f.rec.ID)
		if err != nil {
			return err
		}
		if taken {
			e.Add("name", "taken")
		}
		if len([]rune(f.name)) > 60 {
			e.Add("name", "too_long", "count", 60)
		}
	}
	switch f.strs["preset"] {
	case oidc.PresetGeneric:
		if strings.TrimSpace(f.strs["issuer"]) == "" {
			e.Add("issuer", "blank")
		}
	case oidc.PresetEntra:
		if strings.TrimSpace(f.strs["tenant"]) == "" && strings.TrimSpace(f.strs["issuer"]) == "" {
			e.Add("tenant", "blank")
		}
	default:
		e.Add("preset", "inclusion")
	}
	if iss := strings.TrimSpace(f.strs["issuer"]); iss != "" && !strings.HasPrefix(iss, "https://") && !strings.HasPrefix(iss, "http://") {
		e.Add("issuer", "invalid")
	}
	if strings.TrimSpace(f.strs["client_id"]) == "" {
		e.Add("client_id", "blank")
	}
	if m := f.strs["match_by"]; m != "" && m != "login" && m != "mail" && m != "none" {
		e.Add("match_by", "inclusion")
	}
	return nil
}

// saveOIDCSource は検証して保存する。検証エラーなら false。
func (a *App) saveOIDCSource(c *Req, f *oidcSourceForm) (bool, error) {
	if err := a.validateOIDCSource(c, f); err != nil {
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
	for _, k := range oidcStringFields {
		cfg[k] = strings.TrimSpace(f.strs[k])
	}
	for _, k := range oidcBoolFields {
		cfg[k] = f.bools[k]
	}
	rec.Config = cfg
	rec.Name = f.name
	rec.Kind = domain.AuthSourceKindOIDC
	rec.Enabled = f.enabled
	rec.OntheflyRegister = f.onthefly
	if rec.ID == 0 {
		rec.CreatedAt = a.now()
	}
	rec.UpdatedAt = a.now()
	if f.secretChanged {
		if f.secret == "" {
			rec.Secret = nil
		} else {
			sealed, err := a.sealKey(f.secret)
			if err != nil {
				return false, err
			}
			rec.Secret = &sealed
		}
	}
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if err := repository.SaveAuthSource(c.Ctx(), tx, rec); err != nil {
			return err
		}
		if f.mappingsChanged {
			return repository.ReplaceAuthSourceGroupMappings(c.Ctx(), tx, rec.ID, f.mappings)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	oidc.Forget(rec.ID)
	return true, nil
}

// renderOIDCSourceForm は auth_sources/new_oidc・edit_oidc を描画する。
func (a *App) renderOIDCSourceForm(c *Req, f *oidcSourceForm) {
	groups, err := repository.ListGroups(c.Ctx(), a.DB, false)
	if err != nil {
		a.internalError(c, "groups", err)
		return
	}
	c.NoStore()
	tmpl := "auth_sources/edit_oidc"
	if f.NewRecord() {
		tmpl = "auth_sources/new_oidc"
	}
	opts := make([][]any, 0, len(groups))
	for _, g := range groups {
		opts = append(opts, []any{g.Name, g.ID})
	}
	c.renderAdmin(tmpl, map[string]any{"AuthSource": f, "GroupOptions": opts}, false)
}

// oidcSourcesNew は auth_sources#new（type=AuthSourceOidc）。
func (a *App) oidcSourcesNew(c *Req) {
	f := a.newOIDCSourceForm(c, &domain.AuthSourceRecord{Kind: domain.AuthSourceKindOIDC, Config: map[string]any{}})
	f.assign(c.Params().Map("auth_source"), nil)
	a.renderOIDCSourceForm(c, f)
}

// oidcSourcesCreate は auth_sources#create（type=AuthSourceOidc）。
func (a *App) oidcSourcesCreate(c *Req) {
	f := a.newOIDCSourceForm(c, &domain.AuthSourceRecord{Kind: domain.AuthSourceKindOIDC, Config: map[string]any{}})
	f.assign(c.Params().Map("auth_source"), c.Params())
	ok, err := a.saveOIDCSource(c, f)
	if err != nil {
		a.internalError(c, "create auth source", err)
		return
	}
	if ok {
		c.Flash().SetNotice(c.L("notice_successful_create"))
		c.Redirect("/auth_sources")
		return
	}
	a.renderOIDCSourceForm(c, f)
}

// oidcSourcesEdit は auth_sources#edit（OIDC）。
func (a *App) oidcSourcesEdit(c *Req, rec *domain.AuthSourceRecord) {
	a.renderOIDCSourceForm(c, a.newOIDCSourceForm(c, rec))
}

// oidcSourcesUpdate は auth_sources#update（OIDC）。
func (a *App) oidcSourcesUpdate(c *Req, rec *domain.AuthSourceRecord) {
	f := a.newOIDCSourceForm(c, rec)
	f.assign(c.Params().Map("auth_source"), c.Params())
	ok, err := a.saveOIDCSource(c, f)
	if err != nil {
		a.internalError(c, "update auth source", err)
		return
	}
	if ok {
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.Redirect("/auth_sources")
		return
	}
	a.renderOIDCSourceForm(c, f)
}

// oidcSourcesTestConnection は auth_sources#test_connection（OIDC はディスカバリを試す）。
func (a *App) oidcSourcesTestConnection(c *Req, rec *domain.AuthSourceRecord) {
	oidc.Forget(rec.ID)
	p, err := oidc.New(c.Ctx(), a.OIDCHTTPClient, a.oidcConfig(c, rec))
	if err != nil {
		c.Flash().SetError(c.L("error_unable_to_connect", map[string]any{"value": err.Error()}))
	} else {
		c.Flash().SetNotice(c.L("buropher.sso.notice_discovery_succeeded", map[string]any{"issuer": p.Config().Issuer}))
	}
	c.Redirect("/auth_sources")
}
