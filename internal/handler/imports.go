package handler

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/csvimport"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは ImportsController（app/controllers/imports_controller.rb）と Import モデル
// （app/models/import.rb, import_item.rb）の移植。種類ごとの処理（IssueImport / TimeEntryImport /
// UserImport）は imports_issue.go / imports_time_entry.go / imports_user.go。

// ImportsController は imports コントローラ。
// MainMenu・MenuItem はインポートの種類で変わる（importsController を参照）。
var ImportsController = &Controller{Name: "imports", MainMenu: true}

// importType は Import のサブクラス（IssueImport / TimeEntryImport / UserImport）。
type importType struct {
	// Class はクラス名（params[:type]）。
	Class string
	// Kind は imports.kind。
	Kind string
	// Prefix は import_partial_prefix（issues / time_entries / users）。
	Prefix string
	// MenuItem は self.menu_item。
	MenuItem string
	// Layout は self.layout（base / admin）。
	Layout string
	// AutoMappable は AUTO_MAPPABLE_FIELDS（[field, label] の順序付き列）。
	AutoMappable [][2]string
}

var importTypes = []*importType{
	{Class: "IssueImport", Kind: "issue", Prefix: "issues", MenuItem: "issues", Layout: "base", AutoMappable: [][2]string{
		{"tracker", "field_tracker"}, {"subject", "field_subject"}, {"description", "field_description"},
		{"status", "field_status"}, {"priority", "field_priority"}, {"category", "field_category"},
		{"assigned_to", "field_assigned_to"}, {"fixed_version", "field_fixed_version"}, {"is_private", "field_is_private"},
		{"parent_issue_id", "field_parent_issue"}, {"start_date", "field_start_date"}, {"due_date", "field_due_date"},
		{"estimated_hours", "field_estimated_hours"}, {"done_ratio", "field_done_ratio"}, {"unique_id", "field_unique_id"},
		{"relation_duplicates", "label_duplicates"}, {"relation_duplicated", "label_duplicated_by"},
		{"relation_blocks", "label_blocks"}, {"relation_blocked", "label_blocked_by"},
		{"relation_relates", "label_relates_to"}, {"relation_precedes", "label_precedes"},
		{"relation_follows", "label_follows"}, {"relation_copied_to", "label_copied_to"},
		{"relation_copied_from", "label_copied_from"},
	}},
	{Class: "TimeEntryImport", Kind: "time_entry", Prefix: "time_entries", MenuItem: "time_entries", Layout: "base", AutoMappable: [][2]string{
		{"activity", "field_activity"}, {"user", "field_user"}, {"issue_id", "field_issue"},
		{"spent_on", "field_spent_on"}, {"hours", "field_hours"}, {"comments", "field_comments"},
	}},
	{Class: "UserImport", Kind: "user", Prefix: "users", MenuItem: "users", Layout: "admin", AutoMappable: [][2]string{
		{"login", "field_login"}, {"firstname", "field_firstname"}, {"lastname", "field_lastname"},
		{"mail", "field_mail"}, {"language", "field_language"}, {"admin", "field_admin"},
		{"auth_source", "field_auth_source"}, {"password", "field_password"},
		{"must_change_passwd", "field_must_change_passwd"}, {"status", "field_status"},
	}},
}

func importTypeByClass(name string) *importType {
	for _, t := range importTypes {
		if t.Class == name {
			return t
		}
	}
	return nil
}

func importTypeByKind(kind string) *importType {
	for _, t := range importTypes {
		if t.Kind == kind {
			return t
		}
	}
	return nil
}

// importMaxItemsPerRequest は max_items_per_request（テストで差し替える）。
var importMaxItemsPerRequest = 5

// importMaxTime は run の :max_time（10.seconds）。
const importMaxTime = 10 * time.Second

type importCtxKey struct{}
type importTypeCtxKey struct{}

// routesImports は imports コントローラのルートを登録する。
func (a *App) routesImports(r Router) {
	// get '/issues/imports/new', :to => 'imports#new', :defaults => {:type => 'IssueImport'}
	for _, d := range []struct{ path, class string }{
		{"/issues/imports/new", "IssueImport"},
		{"/time_entries/imports/new", "TimeEntryImport"},
		{"/users/imports/new", "UserImport"},
	} {
		class := d.class
		a.Handle(r, http.MethodGet, d.path, ImportsController, "new", a.ImportsNew,
			Before(func(c *Req) {
				c.R = helper.WithRouteType(c.R, class)
				a.setImportType(c, importTypeByClass(class))
			}), Before(a.authorizeImport))
	}
	a.Handle(r, http.MethodPost, "/imports", ImportsController, "create", a.ImportsCreate,
		Before(func(c *Req) { a.setImportType(c, importTypeByClass(c.Params().String("type"))) }), Before(a.authorizeImport))
	find := Before(a.findImport)
	auth := Before(a.authorizeImport)
	a.Handle(r, http.MethodGet, "/imports/{id}", ImportsController, "show", a.ImportsShow, find, auth)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/imports/{id}/settings", ImportsController, "settings", a.ImportsSettings, find, auth)
		a.Handle(r, m, "/imports/{id}/mapping", ImportsController, "mapping", a.ImportsMapping, find, auth)
		a.Handle(r, m, "/imports/{id}/run", ImportsController, "run", a.ImportsRun, find, auth)
	}
}

// setImportType は import_type を決め、menu_items（既定のメニュー項目）と current_menu
// （admin レイアウトならメインメニューなし）をコントローラに反映する。
func (a *App) setImportType(c *Req, t *importType) {
	c.setValue(importTypeCtxKey{}, t)
	ctrl := *ImportsController
	if t != nil {
		item := t.MenuItem
		ctrl.MenuItem = func(string) string { return item }
		ctrl.MainMenu = t.Layout != "admin"
	} else {
		ctrl.MenuItem = func(string) string { return "" }
	}
	c.Controller = &ctrl
}

func (c *Req) importType() *importType {
	t, _ := c.value(importTypeCtxKey{}).(*importType)
	return t
}

func (c *Req) importModel() *importModel {
	m, _ := c.value(importCtxKey{}).(*importModel)
	return m
}

// authorizeImport は authorize_import。
func (a *App) authorizeImport(c *Req) {
	t := c.importType()
	if t == nil {
		c.Render404("")
		return
	}
	if !a.importAuthorized(c, t) {
		c.Render403("")
	}
}

// importAuthorized は <Type>.authorized?(User.current)。
func (a *App) importAuthorized(c *Req, t *importType) bool {
	switch t.Kind {
	case "issue":
		return c.AllowedToGlobally(domain.Perm("import_issues")) && c.AllowedToGlobally(domain.Perm("add_issues"))
	case "time_entry":
		return c.AllowedToGlobally(domain.Perm("import_time_entries")) && c.AllowedToGlobally(domain.Perm("log_time"))
	}
	return c.User.IsAdmin()
}

// findImport は find_import（自分のインポートのみ。完了済みなら show へ。POST なら update_from_params）。
func (a *App) findImport(c *Req) {
	imp, err := repository.FindImport(c.Ctx(), a.DB, c.User.ID, c.Params().String("id"))
	if errors.Is(err, repository.ErrNotFound) {
		a.setImportType(c, nil)
		c.Render404("")
		return
	}
	if err != nil {
		a.internalError(c, "find import", err)
		return
	}
	m := a.newImportModel(c, imp)
	c.setValue(importCtxKey{}, m)
	a.setImportType(c, m.typ)
	if imp.Finished && c.Action != "show" {
		c.Redirect("/imports/" + imp.Filename)
		return
	}
	if c.R.Method == http.MethodPost {
		if err := m.updateFromParams(c.Params()); err != nil {
			a.internalError(c, "update import settings", err)
		}
	}
}

// ---------------------------------------------------------------- モデル

// importModel は Import のインスタンス（ファイルの読み込みと種類ごとのキャッシュを持つ）。
type importModel struct {
	*repository.Import
	a    *App
	c    *Req
	typ  *importType
	user *domain.User

	data    []byte
	dataErr error
	loaded  bool

	// 種類ごとの状態（キャッシュ）
	issue *issueImportState
	te    *timeEntryImportState
}

func (a *App) newImportModel(c *Req, imp *repository.Import) *importModel {
	if imp.Settings.V == nil {
		imp.Settings.V = map[string]any{}
	}
	return &importModel{Import: imp, a: a, c: c, typ: importTypeByKind(imp.Kind), user: c.User}
}

// importsDir は tmp/imports（Rails.root/tmp/imports 相当。添付ファイルの保存先の親の tmp/imports）。
func (a *App) importsDir() string {
	if a.AttachmentStore != nil && a.AttachmentStore.Root != "" {
		return filepath.Join(filepath.Dir(filepath.Clean(a.AttachmentStore.Root)), "tmp", "imports")
	}
	return filepath.Join(os.TempDir(), "buropher-imports")
}

// filepath は Import#filepath（ファイル名が 16 進数でなければ ""）。
func (m *importModel) filepath() string {
	if !csvimport.ValidFilename(m.Filename) {
		return ""
	}
	return filepath.Join(m.a.importsDir(), m.Filename)
}

// fileExists は file_exists?。
func (m *importModel) fileExists() bool {
	p := m.filepath()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// content はファイルの内容（無ければ nil, false）。
func (m *importModel) content() ([]byte, bool, error) {
	if !m.loaded {
		m.loaded = true
		if p := m.filepath(); p != "" {
			b, err := os.ReadFile(p)
			switch {
			case err == nil:
				m.data = b
			case errors.Is(err, os.ErrNotExist):
			default:
				m.dataErr = err
			}
		}
	}
	return m.data, m.data != nil, m.dataErr
}

// removeFile は remove_file。
func (m *importModel) removeFile() {
	if p := m.filepath(); p != "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.a.logger().Error("Unable to delete file", "path", p, "err", err)
		}
	}
	m.data, m.loaded = nil, true
}

func (m *importModel) settings() map[string]any { return m.Settings.V }

// SettingValue は settings[key]（テンプレートの選択値）。
func (m *importModel) SettingValue(key string) any { return m.Settings.V[key] }

// setting は settings[key].to_s。
func (m *importModel) setting(key string) string { return importToS(m.Settings.V[key]) }

// mapping は Import#mapping（settings['mapping'] || {}）。
func (m *importModel) mapping() map[string]any {
	if mp, ok := m.Settings.V["mapping"].(map[string]any); ok {
		return mp
	}
	return map[string]any{}
}

// mappingValue は mapping[key]。
func (m *importModel) mappingValue(key string) any { return m.mapping()[key] }

// csvOptions は read_rows の CSV オプション。
func (m *importModel) csvOptions() csvimport.Options {
	return csvimport.Options{Separator: m.setting("separator"), Wrapper: m.setting("wrapper"), Encoding: m.setting("encoding")}
}

// readRows は read_rows（ファイルが無ければ何もしない）。
func (m *importModel) readRows(fn func(csvimport.Row) bool) error {
	data, ok, err := m.content()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return csvimport.Parse(data, m.csvOptions(), fn)
}

// readItems は read_items（ヘッダ行を除き、行と 1 始まりの位置を渡す）。
func (m *importModel) readItems(fn func(row csvimport.Row, position int) bool) error {
	i := 0
	headers := true
	return m.readRows(func(r csvimport.Row) bool {
		if i == 0 && headers {
			headers = false
			return true
		}
		i++
		return fn(r, i)
	})
}

// parseFile は parse_file（total_items を更新する）。
func (m *importModel) parseFile() (int, error) {
	count := 0
	if err := m.readItems(func(_ csvimport.Row, i int) bool { count = i; return true }); err != nil {
		return 0, err
	}
	m.TotalItems = &count
	return count, m.save()
}

// FirstRows は first_rows（ヘッダを含む先頭 4 行）。
func (m *importModel) FirstRows() []csvimport.Row {
	var rows []csvimport.Row
	if err := m.readRows(func(r csvimport.Row) bool {
		rows = append(rows, r)
		return len(rows) < 4
	}); err != nil {
		m.a.logger().Error("import first_rows", "err", err)
	}
	return rows
}

// headers は headers（ヘッダ行。無ければ空）。
func (m *importModel) headers() []*string {
	var h csvimport.Row
	if err := m.readRows(func(r csvimport.Row) bool { h = r; return false }); err != nil {
		m.a.logger().Error("import headers", "err", err)
	}
	return h
}

// columnsOptions は columns_options（[ヘッダ, 位置] の列）。
func (m *importModel) columnsOptions() []any {
	var out []any
	for i, h := range m.headers() {
		text := ""
		if h != nil {
			text = *h
		}
		out = append(out, []any{text, i})
	}
	return out
}

// save は save!。
func (m *importModel) save() error {
	return repository.UpdateImport(m.c.Ctx(), m.a.DB, m.Import, m.a.now())
}

// updateFromParams は update_from_params（params[:import_settings] を settings に浅くマージして保存）。
func (m *importModel) updateFromParams(p *httpx.Params) error {
	if !p.Present("import_settings") {
		return nil
	}
	is := p.Map("import_settings")
	if is == nil {
		return nil
	}
	for k, v := range is.ToMap() {
		m.Settings.V[k] = v
	}
	return m.save()
}

// rowValue は row_value(row, key)（mapping[key].presence の位置の値の presence）。
func (m *importModel) rowValue(row csvimport.Row, key string) *string {
	idx := m.mappingValue(key)
	if importBlank(idx) {
		return nil
	}
	v := row.Get(int(httpx.RubyToI(importToS(idx))))
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return v
}

// rowDate は row_date(row, key)（date_format で解釈できれば "YYYY-MM-DD"、できなければ元の文字列）。
func (m *importModel) rowDate(row csvimport.Row, key string) *string {
	s := m.rowValue(row, key)
	if s == nil {
		return nil
	}
	format := m.setting("date_format")
	if !csvimport.ValidDateFormat(format) {
		format = csvimport.DateFormats[0]
	}
	if d, ok := csvimport.ParseDate(*s, format); ok {
		v := d.Format("2006-01-02")
		return &v
	}
	return s
}

// useUniqueID は use_unique_id?。
func (m *importModel) useUniqueID() bool { return !importBlank(m.mappingValue("unique_id")) }

// lu は lu(user, key)（インポートしたユーザーの言語、無ければ Setting.default_language）。
func (m *importModel) lu(key string) string {
	return m.a.Bundle.LL(m.userLang(), key)
}

func (m *importModel) userLang() string {
	lang := ""
	if m.user != nil {
		lang = m.user.Language
	}
	if strings.TrimSpace(lang) == "" {
		lang = m.a.Settings.String("default_language")
	}
	return lang
}

// yes は yes?(value)。
func (m *importModel) yes(v string) bool { return v == m.lu("general_text_yes") || v == "1" }

// setDefaultSettings は set_default_settings(:project_id => projectID)。
func (m *importModel) setDefaultSettings(projectID string) {
	separator := m.lu("general_csv_separator")
	wrapper := `"`
	encoding := m.lu("general_csv_encoding")
	if data, ok, _ := m.content(); ok {
		head := csvimport.ReadHead(data, 4096)
		separator = csvimport.GuessSeparator(head)
		wrapper = csvimport.GuessWrapper(head)
		if guessed, ok := csvimport.GuessEncoding(head, m.a.Settings.String("repositories_encodings")); ok {
			if c := csvimport.CanonicalEncoding(guessed, settings.Encodings); guessed != "" && c != "" {
				encoding = c
			}
		}
	}
	dateFormat := ""
	if v, ok := m.a.Bundle.Lookup(m.userLang(), "date.formats.default").(string); ok {
		dateFormat = v
	}
	if !csvimport.ValidDateFormat(dateFormat) {
		dateFormat = csvimport.DateFormats[0]
	}
	s := m.Settings.V
	s["separator"], s["wrapper"], s["encoding"], s["date_format"], s["notifications"] = separator, wrapper, encoding, dateFormat, "0"
	if strings.TrimSpace(projectID) != "" {
		// 存在しないプロジェクトでも失敗しない（Project.find は id か識別子）
		if p, err := m.c.lookupProjectAny(projectID); err == nil && p != nil {
			s["mapping"] = map[string]any{"project_id": p.ID}
		}
	}
}

// lookupProjectAny は Project.find(id_or_identifier)（アーカイブ済みも含む）。
func (c *Req) lookupProjectAny(id string) (*domain.Project, error) {
	ctx := c.Ctx()
	if regexp.MustCompile(`\A\d+\z`).MatchString(id) {
		ps, err := repository.LoadProjects(ctx, c.App.DB, "projects.id = ?", httpx.RubyToI(id))
		if err != nil || len(ps) == 0 {
			return nil, err
		}
		return ps[0], nil
	}
	p, err := repository.FindProjectByIdentifier(ctx, c.App.DB, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	return p, err
}

// ---------------------------------------------------------------- コールバック（settings['callbacks']）

// addCallback は add_callback(position, name, *args)。
func (m *importModel) addCallback(position any, name string, args ...any) error {
	cbs, _ := m.Settings.V["callbacks"].(map[string]any)
	if cbs == nil {
		cbs = map[string]any{}
		m.Settings.V["callbacks"] = cbs
	}
	key := importToS(position)
	list, _ := cbs[key].([]any)
	cbs[key] = append(list, []any{name, args})
	return m.save()
}

// takeCallbacks は (settings['callbacks'] || {}).delete(position)（[name, args] の列）。
func (m *importModel) takeCallbacks(position any) ([][2]any, bool) {
	cbs, _ := m.Settings.V["callbacks"].(map[string]any)
	if cbs == nil {
		return nil, false
	}
	key := importToS(position)
	list, ok := cbs[key].([]any)
	if !ok {
		return nil, false
	}
	delete(cbs, key)
	var out [][2]any
	for _, e := range list {
		if pair, ok := e.([]any); ok && len(pair) == 2 {
			out = append(out, [2]any{pair[0], pair[1]})
		}
	}
	return out, true
}

// ---------------------------------------------------------------- run

// importResult は build_object + save の結果。
type importResult struct {
	// obj は保存したオブジェクト（種類ごとの型）。nil なら build_object が nil。
	obj       any
	persisted bool
	objID     int64
	message   string
}

// run は Import#run（処理した最後の位置を返す）。
func (m *importModel) run(maxItems int, maxTime time.Duration) (int, error) {
	ctx := m.c.Ctx()
	current, imported := 0, 0
	resumeAfter, err := repository.ImportItemsMaxPosition(ctx, m.a.DB, m.ID)
	if err != nil {
		return 0, err
	}
	interrupted := false
	started := m.a.now()
	var runErr error
	err = m.readItems(func(row csvimport.Row, position int) bool {
		if (maxItems > 0 && imported >= maxItems) || (maxTime > 0 && !m.a.now().Before(started.Add(maxTime))) {
			interrupted = true
			return false
		}
		if position > resumeAfter {
			item := &repository.ImportItem{ImportID: m.ID, Position: position}
			if m.useUniqueID() {
				if v := m.rowValue(row, "unique_id"); v != nil {
					item.UniqueID.String, item.UniqueID.Valid = *v, true
				}
			}
			res, err := m.buildAndSave(ctx, row, item)
			if err != nil {
				runErr = err
				return false
			}
			if res.obj != nil {
				if res.persisted {
					id := res.objID
					item.ObjID = &id
				} else {
					item.Message.String, item.Message.Valid = res.message, true
				}
			}
			if err := repository.InsertImportItem(ctx, m.a.DB, item); err != nil {
				runErr = err
				return false
			}
			imported++
			if res.persisted {
				if err := m.extendObject(ctx, row, item, res); err != nil {
					runErr = err
					return false
				}
			}
			var key any = item.Position
			if m.useUniqueID() {
				key = item.UniqueID.String
				if !item.UniqueID.Valid {
					key = nil
				}
			}
			if err := m.doCallbacks(ctx, key, res); err != nil {
				runErr = err
				return false
			}
		}
		current = position
		return true
	})
	if runErr != nil {
		return 0, runErr
	}
	if err != nil {
		return 0, err
	}
	if imported == 0 || !interrupted {
		if m.TotalItems == nil {
			n := current
			m.TotalItems = &n
		}
		m.Finished = true
		if err := m.save(); err != nil {
			return 0, err
		}
		m.removeFile()
	}
	return current, nil
}

// doCallbacks は do_callbacks(position, object)。
func (m *importModel) doCallbacks(ctx context.Context, position any, res importResult) error {
	if position == nil {
		return nil
	}
	cbs, ok := m.takeCallbacks(position)
	if !ok {
		return nil
	}
	for _, cb := range cbs {
		name := importToS(cb[0])
		args, _ := cb[1].([]any)
		if err := m.callback(ctx, name, res, args); err != nil {
			return err
		}
	}
	return m.save()
}

func (m *importModel) buildAndSave(ctx context.Context, row csvimport.Row, item *repository.ImportItem) (importResult, error) {
	switch m.Kind {
	case "issue":
		return m.issueBuildAndSave(ctx, row, item)
	case "time_entry":
		return m.timeEntryBuildAndSave(ctx, row, item)
	case "user":
		return m.userBuildAndSave(ctx, row, item)
	}
	return importResult{}, nil
}

func (m *importModel) extendObject(ctx context.Context, row csvimport.Row, item *repository.ImportItem, res importResult) error {
	switch m.Kind {
	case "issue":
		return m.issueBuildRelations(ctx, row, item, res)
	case "user":
		m.userExtendObject(res)
	}
	return nil
}

func (m *importModel) callback(ctx context.Context, name string, res importResult, args []any) error {
	if m.Kind != "issue" {
		return nil
	}
	switch name {
	case "set_as_parent":
		return m.issueSetAsParentCallback(ctx, res, args)
	case "set_relation":
		return m.issueSetRelationCallback(ctx, res, args)
	}
	return fmt.Errorf("unknown import callback %q", name)
}

// ---------------------------------------------------------------- アクション

// ImportsNew は imports#new。
func (a *App) ImportsNew(c *Req) {
	t := c.importType()
	a.renderImport(c, "imports/new", t, nil, map[string]any{"Type": t.Class, "ProjectIDParam": importParamOrNil(c.Params(), "project_id")})
}

// ImportsCreate は imports#create。
func (a *App) ImportsCreate(c *Req) {
	t := c.importType()
	ctx := c.Ctx()
	imp := &repository.Import{Kind: t.Kind, UserID: c.User.ID}
	imp.Settings.V = map[string]any{}
	m := a.newImportModel(c, imp)
	if f := c.Params().File("file"); f != nil && f.Size > 0 {
		name := csvimport.GenerateFilename()
		if err := a.saveImportUpload(f, filepath.Join(a.importsDir(), name)); err != nil {
			a.internalError(c, "save import file", err)
			return
		}
		imp.Filename = name
	}
	m.setDefaultSettings(c.Params().String("project_id"))
	// validates_presence_of :filename, :user_id / validates_length_of :filename, :maximum => 255
	if imp.Filename != "" && c.User.ID != 0 {
		if err := repository.InsertImport(ctx, a.DB, imp, a.now()); err != nil {
			a.internalError(c, "create import", err)
			return
		}
		c.Redirect("/imports/" + imp.Filename + "/settings")
		return
	}
	a.renderImport(c, "imports/new", t, nil, map[string]any{"Type": t.Class, "ProjectIDParam": importParamOrNil(c.Params(), "project_id")})
}

// saveImportUpload は Redmine::Utils.save_upload。
func (a *App) saveImportUpload(f *httpx.UploadedFile, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// ImportsShow は imports#show。
func (a *App) ImportsShow(c *Req) {
	m := c.importModel()
	ctx := c.Ctx()
	saved, err := repository.CountImportItems(ctx, a.DB, m.ID, true)
	if err != nil {
		a.internalError(c, "import items", err)
		return
	}
	unsavedCount, err := repository.CountImportItems(ctx, a.DB, m.ID, false)
	if err != nil {
		a.internalError(c, "import items", err)
		return
	}
	data := map[string]any{"SavedCount": saved, "UnsavedCount": unsavedCount, "Total": importTotal(m)}
	if saved > 0 {
		html, err := a.importSavedObjects(c, m)
		if err != nil {
			a.internalError(c, "import saved objects", err)
			return
		}
		data["SavedObjects"] = html
	}
	if unsavedCount > 0 {
		items, err := repository.UnsavedImportItems(ctx, a.DB, m.ID)
		if err != nil {
			a.internalError(c, "import items", err)
			return
		}
		var rows []map[string]any
		for _, it := range items {
			rows = append(rows, map[string]any{"Position": it.Position, "Message": simpleFormatWithoutParagraph(it.Message.String)})
		}
		data["UnsavedItems"] = rows
	}
	a.renderImport(c, "imports/show", m.typ, m, data)
}

// ImportsSettings は imports#settings。
func (a *App) ImportsSettings(c *Req) {
	m := c.importModel()
	if c.R.Method == http.MethodPost {
		n, err := m.parseFile()
		if err == nil {
			if n == 0 {
				c.Flash().Now("error", c.L("error_no_data_in_file"))
			} else {
				c.Redirect("/imports/" + m.Filename + "/mapping")
				return
			}
		} else {
			var me *csvimport.MalformedError
			switch {
			case errors.As(err, &me) && !me.InvalidEncoding():
				c.Flash().Now("error", c.L("error_invalid_csv_file_or_settings", me.Error()))
			case errors.As(err, &me):
				c.Flash().Now("error", c.L("error_invalid_file_encoding", map[string]any{"encoding": string(rails.H(m.setting("encoding")))}))
			default:
				// SystemCallError
				c.Flash().Now("error", c.L("error_can_not_read_import_file"))
			}
		}
	}
	a.renderImport(c, "imports/settings", m.typ, m, map[string]any{
		"SeparatorOptions": []any{[]any{c.L("label_comma_char"), ","}, []any{c.L("label_semi_colon_char"), ";"}},
		"WrapperOptions":   []any{[]any{c.L("label_quote_char"), "'"}, []any{c.L("label_double_quote_char"), `"`}},
		"Encodings":        settings.Encodings,
		"DateFormats":      importDateFormatOptions(),
		"Notifications":    m.setting("notifications") == "1",
	})
}

// ImportsMapping は imports#mapping。
func (a *App) ImportsMapping(c *Req) {
	m := c.importModel()
	cfs, err := m.mappableCustomFields()
	if err != nil {
		a.internalError(c, "import custom fields", err)
		return
	}
	if c.R.Method == http.MethodGet {
		m.autoMapFields(cfs)
	} else {
		if httpx.Format(c.R) == "js" {
			// プロジェクト・トラッカーの変更でフォームを更新する（mapping.js.erb）
			view, err := a.importMappingView(c, m, cfs)
			if err != nil {
				a.internalError(c, "import mapping", err)
				return
			}
			c.Render("imports/mapping", view, RenderOptions{Format: "js", Layout: viewNoLayout})
			return
		}
		if c.Params().Has("previous") {
			c.Redirect("/imports/" + m.Filename + "/settings")
		} else {
			c.Redirect("/imports/" + m.Filename + "/run")
		}
		return
	}
	data, err := a.importMappingView(c, m, cfs)
	if err != nil {
		a.internalError(c, "import mapping", err)
		return
	}
	data["FirstRows"] = importPreviewRows(m.FirstRows())
	data["TotalItemsOrZero"] = importTotal(m)
	a.renderImport(c, "imports/mapping", m.typ, m, data)
}

// ImportsRun は imports#run。
func (a *App) ImportsRun(c *Req) {
	m := c.importModel()
	if c.R.Method == http.MethodPost {
		current, err := m.run(importMaxItemsPerRequest, importMaxTime)
		if err != nil {
			a.internalError(c, "run import", err)
			return
		}
		if httpx.Format(c.R) == "js" {
			c.Render("imports/run", map[string]any{"Current": current, "Total": importTotal(m), "Finished": m.Finished,
				"Filename": m.Filename}, RenderOptions{Format: "js", Layout: viewNoLayout})
			return
		}
		if m.Finished {
			c.Redirect("/imports/" + m.Filename)
		} else {
			c.Redirect("/imports/" + m.Filename + "/run")
		}
		return
	}
	a.renderImport(c, "imports/run", m.typ, m, map[string]any{"Total": importTotal(m)})
}

// viewNoLayout は view.NoLayout。
var viewNoLayout = view.NoLayout

// renderImport は layout :import_layout で描画する。
func (a *App) renderImport(c *Req, name string, t *importType, m *importModel, data map[string]any) {
	data["Title"] = c.L("label_import_" + t.Prefix)
	data["Prefix"] = t.Prefix
	data["Import"] = m
	if m != nil {
		data["Filename"] = m.Filename
	}
	opts := RenderOptions{}
	if t.Layout == "admin" {
		opts.Layout = "admin"
	}
	c.Render(name, data, opts)
}

// importMappingView は mapping.html / mapping.js（_<prefix>_mapping）の値。
func (a *App) importMappingView(c *Req, m *importModel, cfs []importCF) (map[string]any, error) {
	data := map[string]any{"CustomFields": cfs, "Import": m, "Filename": m.Filename, "Prefix": m.typ.Prefix}
	var err error
	switch m.Kind {
	case "issue":
		err = m.importIssueMappingData(data)
	case "time_entry":
		err = m.importTimeEntryMappingData(data)
	}
	return data, err
}

// importSavedObjects は _<prefix>_saved_objects の locals（saved_objects）。
func (a *App) importSavedObjects(c *Req, m *importModel) (map[string]any, error) {
	ids, err := repository.SavedImportObjIDs(c.Ctx(), a.DB, m.ID)
	if err != nil {
		return nil, err
	}
	switch m.Kind {
	case "issue":
		links, href, err := a.importSavedIssues(c, ids)
		return map[string]any{"links": links, "view_all_url": href}, err
	case "time_entry":
		rows, err := a.importSavedTimeEntries(c, ids)
		return map[string]any{"rows": rows}, err
	}
	users, err := a.importSavedUsers(c, ids)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	for _, u := range users {
		rows = append(rows, map[string]any{"user": u, "status": importUserStatusLabel(c, u.Status)})
	}
	return map[string]any{"rows": rows}, nil
}

// ---------------------------------------------------------------- 自動対応付け・選択肢

// autoMapFields は auto_map_fields（列名が内部名かラベルに一致する項目を対応付ける。保存はしない）。
func (m *importModel) autoMapFields(cfs []importCF) {
	if strings.TrimSpace(m.setting("encoding")) == "" {
		return
	}
	mp, ok := m.Settings.V["mapping"].(map[string]any)
	if !ok || mp == nil {
		mp = map[string]any{}
		m.Settings.V["mapping"] = mp
	}
	var headers []*string
	for _, h := range m.headers() {
		if h == nil {
			headers = append(headers, nil)
			continue
		}
		s := strings.ToLower(*h)
		headers = append(headers, &s)
	}
	index := func(name string) int {
		for i, h := range headers {
			if h != nil && *h == name {
				return i
			}
		}
		return -1
	}
	for _, f := range m.typ.AutoMappable {
		if _, ok := mp[f[0]]; ok {
			continue
		}
		i := index(f[0])
		if i < 0 {
			i = index(strings.ToLower(m.c.L(f[1])))
		}
		if i >= 0 {
			mp[f[0]] = i
		}
	}
	for _, cf := range cfs {
		key := "cf_" + strconv.FormatInt(cf.ID, 10)
		if _, ok := mp[key]; ok {
			continue
		}
		i := index(key)
		if i < 0 {
			i = index(strings.ToLower(cf.Name))
		}
		if i >= 0 {
			mp[key] = i
		}
	}
}

// importCF は mappable_custom_fields の 1 件。
type importCF struct {
	ID         int64
	Name       string
	IsRequired bool
	Format     string
}

// mappableCustomFields は mappable_custom_fields。
func (m *importModel) mappableCustomFields() ([]importCF, error) {
	switch m.Kind {
	case "issue":
		return m.issueMappableCustomFields()
	case "time_entry":
		return m.cfsByKind("time_entry")
	case "user":
		return m.cfsByKind("user")
	}
	return nil, nil
}

// MappingSelectTag は mapping_select_tag(import, field, options)。
func (m *importModel) MappingSelectTag(field string, options *rails.Hash) template.HTML {
	name := "import_settings[mapping][" + field + "]"
	return rails.SelectTag(name, m.optionsForMappingSelect(field, options), rails.NewHash("id", "import_mapping_"+field))
}

// optionsForMappingSelect は options_for_mapping_select。
func (m *importModel) optionsForMappingSelect(field string, options *rails.Hash) template.HTML {
	var blank any = template.HTML("&nbsp;")
	if options != nil && importTruthy(options.Get("required")) {
		blank = "-- " + m.c.L("actionview_instancetag_blank_option") + " --"
	}
	sel := m.mappingValue(field)
	tags := rails.ContentTag("option", blank, rails.NewHash("value", ""))
	tags += rails.OptionsForSelect(m.columnsOptions(), sel)
	if options != nil {
		if vals, ok := options.Get("values").([]any); ok {
			tags += rails.ContentTag("option", "--", rails.NewHash("disabled", true))
			var opts []any
			for _, v := range vals {
				pair := v.([]any)
				opts = append(opts, []any{pair[0], "value:" + importToS(pair[1])})
			}
			if sel == nil {
				sel = options.Get("default_value")
			}
			tags += rails.OptionsForSelect(opts, sel)
		}
	}
	return tags
}

// ---------------------------------------------------------------- 小物

// importDateFormatOptions は date_format_options（[表示, 値] の配列）。
func importDateFormatOptions() []any {
	var out []any
	for _, o := range csvimport.DateFormatOptions() {
		out = append(out, []any{o[0], o[1]})
	}
	return out
}

// importPreviewRows はプレビューのセル（truncate(c.to_s, :length => 50)）。
func importPreviewRows(rows []csvimport.Row) [][]string {
	var out [][]string
	for _, r := range rows {
		var cells []string
		for _, v := range r {
			s := ""
			if v != nil {
				s = *v
			}
			cells = append(cells, rails.StringTruncate(s, 50, "...", nil))
		}
		out = append(out, cells)
	}
	return out
}

// importTotal は total_items.to_i。
func importTotal(m *importModel) int {
	if m.TotalItems == nil {
		return 0
	}
	return *m.TotalItems
}

// simpleFormatWithoutParagraph は ApplicationHelper#simple_format_without_paragraph（エスケープしない）。
func simpleFormatWithoutParagraph(text string) template.HTML {
	s := strings.ReplaceAll(text, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = regexp.MustCompile(`\n\n+`).ReplaceAllString(s, "<br /><br />")
	// ([^\n]\n)(?=[^\n]) → '\1<br />'
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		b.WriteRune(r)
		if r == '\n' && i > 0 && rs[i-1] != '\n' && i+1 < len(rs) && rs[i+1] != '\n' {
			b.WriteString("<br />")
		}
	}
	return template.HTML(b.String())
}

// importParamOrNil は params[key]（無ければ nil）。
func importParamOrNil(p *httpx.Params, key string) any {
	v, ok := p.Get(key)
	if !ok {
		return nil
	}
	return httpx.ValueString(v)
}

// importToS は Ruby の to_s（JSON 由来の数値も整数表記）。
func importToS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return httpx.ValueString(v)
}

// importBlank は blank?。
func importBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	}
	return false
}

func importTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	return true
}

// importCastBool は ActiveRecord::Type::Boolean.new.cast（nil・"" は nil → false）。
func importCastBool(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	s := importToS(v)
	if s == "" {
		return false
	}
	return !slices.Contains([]string{"0", "f", "F", "false", "FALSE", "off", "OFF"}, s)
}
