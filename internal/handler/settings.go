package handler

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/permission"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// SettingsController（app/controllers/settings_controller.rb）。layout 'admin'、main_menu false、
// menu_item :plugins, :only => :plugin。
var SettingsController = &Controller{Name: "settings", MainMenu: false}

// routesSettings は settings コントローラのルートを登録する。
//
//	match 'settings', :controller => 'settings', :action => 'index', :via => :get
//	match 'settings/edit', :controller => 'settings', :action => 'edit', :via => [:get, :post]
//	match 'settings/plugin/:id', :controller => 'settings', :action => 'plugin', :via => [:get, :post], :as => 'plugin_settings'
//
// require_sudo_mode :index, :edit, :plugin は buropher に sudo モードが無いため適用しない。
func (a *App) routesSettings(r Router) {
	a.Handle(r, http.MethodGet, "/settings", SettingsController, "index", a.SettingsIndex, RequireAdmin())
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/settings/edit", SettingsController, "edit", a.SettingsEdit, RequireAdmin())
		a.Handle(r, m, "/settings/plugin/{id}", SettingsController, "plugin", a.SettingsPlugin, RequireAdmin())
	}
}

// SettingsIndex は settings#index（edit と同じ処理で edit テンプレートを描画する）。
func (a *App) SettingsIndex(c *Req) { a.SettingsEdit(c) }

// SettingsPlugin は settings#plugin。buropher はプラグインに対応しないため、Redmine::PluginNotFound と同じく 404
// （組み込みの Discord 通知 buropher_discord の設定画面を除く）。
func (a *App) SettingsPlugin(c *Req) {
	if c.Params().String("id") == DiscordPluginID {
		a.DiscordSettingsPage(c)
		return
	}
	c.Render404("")
}

// SettingsEdit は settings#edit（GET は表示、POST は Setting.set_all_from_params）。
func (a *App) SettingsEdit(c *Req) {
	var errs []helper.SettingError
	if c.R.Method == http.MethodPost {
		var params map[string]any
		if p := c.Params().Map("settings"); p != nil {
			params = p.ToMap()
		}
		changed, ferrs, err := a.Settings.SetAllFromParams(c.Ctx(), params, map[string]func(any) any{
			// Setting.twofa_from_params: 2FA を無効にしたら全ユーザーのペアリングを解除する
			"twofa": func(v any) any {
				if rails.ToS(v) == "0" && a.Settings.String("twofa") != "0" {
					if err := repository.UnpairAllTwofa(c.Ctx(), a.DB); err != nil {
						a.logger().Error("unpair all twofa", "err", err)
					}
				}
				return v
			},
		})
		if err != nil {
			a.logger().Error("settings update", "err", err)
			c.RenderError(http.StatusInternalServerError, err.Error())
			return
		}
		if len(ferrs) == 0 {
			if len(changed) > 0 {
				a.deliverSettingsUpdated(c, changed)
			}
			c.Flash().SetNotice(c.L("notice_successful_update"))
			path := "/settings"
			if t := c.Params().String("tab"); t != "" {
				path += "?tab=" + url.QueryEscape(t)
			}
			c.Redirect(path)
			return
		}
		for _, e := range ferrs {
			msg := c.L(e.Key)
			if e.Detail != "" {
				msg += " (" + e.Detail + ")"
			}
			errs = append(errs, helper.SettingError{Name: e.Name, Message: msg})
		}
	}
	v, err := a.newSettingsView(c, errs)
	if err != nil {
		a.logger().Error("settings view", "err", err)
		c.RenderError(http.StatusInternalServerError, err.Error())
		return
	}
	c.renderAdmin("settings/edit", v, false)
}

// deliverSettingsUpdated は Mailer.deliver_settings_updated(User.current, changes)（セキュリティ通知）。
func (a *App) deliverSettingsUpdated(c *Req, changed []string) {
	a.Notify.SettingsUpdated(c.Ctx(), c.User, changed, c.remoteIP())
}

// settingsView は settings/edit とタブの部分テンプレートに渡す値（コントローラのインスタンス変数と
// SettingsHelper のうち DB を要する選択肢）。
type settingsView struct {
	c *Req
	a *App
	// SettingErrors は @setting_errors。
	SettingErrors []helper.SettingError
	// Tabs は administration_settings_tabs。
	Tabs []helper.Tab
	// Notifiables は @notifiables。
	Notifiables []helper.Notifiable
	// Deliveries は @deliveries（ActionMailer::Base.perform_deliveries）。
	Deliveries bool
	// GuessedHostAndPath は @guessed_host_and_path。
	GuessedHostAndPath string
	// CommitUpdateKeywords は @commit_update_keywords（空なら [{}]）。
	CommitUpdateKeywords []map[string]any
	// CommonMarkHardbreaks は Redmine::Configuration['common_mark_enable_hardbreaks'] == true。
	CommonMarkHardbreaks bool

	ProjectQuery   *queryView
	IssueQuery     *queryView
	RelatedQuery   *queryView
	TimeEntryQuery *queryView

	trackers []*domain.Tracker
	roles    []*domain.Role
	statuses []*domain.IssueStatus
}

func (a *App) newSettingsView(c *Req, errs []helper.SettingError) (*settingsView, error) {
	ctx := c.Ctx()
	v := &settingsView{c: c, a: a, SettingErrors: errs, Notifiables: helper.Notifiables(), Deliveries: true,
		CommonMarkHardbreaks: true}
	v.Tabs = []helper.Tab{
		{Name: "general", Partial: "settings/general", Label: "label_general"},
		{Name: "display", Partial: "settings/display", Label: "label_display"},
		{Name: "authentication", Partial: "settings/authentication", Label: "label_authentication"},
		{Name: "api", Partial: "settings/api", Label: "label_api"},
		{Name: "projects", Partial: "settings/projects", Label: "label_project_plural"},
		{Name: "users", Partial: "settings/users", Label: "label_user_plural"},
		{Name: "issues", Partial: "settings/issues", Label: "label_issue_tracking"},
		{Name: "timelog", Partial: "settings/timelog", Label: "label_time_tracking"},
		{Name: "attachments", Partial: "settings/attachments", Label: "label_attachment_plural"},
		{Name: "notifications", Partial: "settings/notifications", Label: "field_mail_notification"},
		{Name: "mail_handler", Partial: "settings/mail_handler", Label: "label_incoming_emails"},
		{Name: "repositories", Partial: "settings/repositories", Label: "label_repository_plural"},
	}
	v.GuessedHostAndPath = httpx.RequestHostWithPort(c.R)
	v.CommitUpdateKeywords = commitUpdateKeywordsForForm(a.Settings.Get("commit_update_keywords"))

	var err error
	if v.trackers, err = repository.ListTrackers(ctx, a.DB); err != nil {
		return nil, err
	}
	if v.roles, err = repository.GivableRoles(ctx, a.DB); err != nil {
		return nil, err
	}
	if v.statuses, err = repository.ListIssueStatuses(ctx, a.DB); err != nil {
		return nil, err
	}
	env, err := a.queryEnv(c)
	if err != nil {
		return nil, err
	}
	newQV := func(kind query.Kind, typ string, columns []string, totals []string, setTotals bool) (*queryView, error) {
		q, err := query.New(ctx, env, kind, nil)
		if err != nil {
			return nil, err
		}
		if err := q.SetColumnNames(ctx, columns); err != nil {
			return nil, err
		}
		if setTotals {
			q.SetTotalableNames(totals)
		}
		return &queryView{c: c, ctx: ctx, Q: q, Type: typ}, nil
	}
	pld := hashSetting(a.Settings.Get("project_list_defaults"))
	if v.ProjectQuery, err = newQV(query.KindProject, "ProjectQuery", stringsOf(pld["column_names"]), nil, false); err != nil {
		return nil, err
	}
	if v.IssueQuery, err = newQV(query.KindIssue, "IssueQuery", a.Settings.Strings("issue_list_default_columns"),
		a.Settings.Strings("issue_list_default_totals"), true); err != nil {
		return nil, err
	}
	if v.RelatedQuery, err = newQV(query.KindIssue, "IssueQuery", a.Settings.Strings("related_issues_default_columns"), nil, false); err != nil {
		return nil, err
	}
	ted := hashSetting(a.Settings.Get("time_entry_list_defaults"))
	_, hasTotals := ted["totalable_names"]
	if v.TimeEntryQuery, err = newQV(query.KindTimeEntry, "TimeEntryQuery", stringsOf(ted["column_names"]),
		stringsOf(ted["totalable_names"]), hasTotals); err != nil {
		return nil, err
	}
	return v, nil
}

// hashSetting は serialized ハッシュ設定（project_list_defaults 等）を map で返す。
func hashSetting(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func stringsOf(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			out = append(out, rails.ToS(e))
		}
	case []string:
		out = x
	}
	return out
}

// commitUpdateKeywordsForForm は @commit_update_keywords（配列でないか空なら [{}]）。
func commitUpdateKeywordsForForm(v any) []map[string]any {
	var out []map[string]any
	if a, ok := v.([]any); ok {
		for _, e := range a {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			} else {
				out = append(out, map[string]any{})
			}
		}
	}
	if len(out) == 0 {
		out = []map[string]any{{}}
	}
	return out
}

// ---------------------------------------------------------------- 選択肢

// UserFormatOptions は @options[:user_format]（setting_order 順に [User.current.name(f), f]）。
func (v *settingsView) UserFormatOptions() []any {
	var out []any
	for _, f := range []string{"firstname_lastname", "firstname_lastinitial", "firstinitial_lastname", "firstname",
		"lastname_firstname", "lastnamefirstname", "lastname_comma_firstname", "lastname", "username"} {
		out = append(out, []any{v.c.User.Name(f), f})
	}
	return out
}

// ThemeOptions は Redmine::Themes.themes.collect {|t| [t.name, t.id]}。
func (v *settingsView) ThemeOptions() []any {
	var out []any
	if v.a.Assets != nil {
		for _, t := range v.a.Assets.Themes() {
			out = append(out, []any{t.Name, t.ID})
		}
	}
	return out
}

// TextFormattingOptions は Redmine::WikiFormatting.formats_for_select。
func (v *settingsView) TextFormattingOptions() []any {
	return []any{[]any{"Textile", "textile"}, []any{"CommonMark Markdown (GitHub Flavored)", "common_mark"}}
}

// StartOfWeekOptions は [[day_name(1),'1'], [day_name(6),'6'], [day_name(7),'7']]。
func (v *settingsView) StartOfWeekOptions() []any {
	l := v.c.Loc
	return []any{[]any{l.DayName(1), "1"}, []any{l.DayName(6), "6"}, []any{l.DayName(7), "7"}}
}

// formatLocale は settings/_display の locale（User.current.language が空なら I18n.locale）。
func (v *settingsView) formatLocale() string {
	if v.c.User.Language != "" {
		return v.c.User.Language
	}
	return v.c.Loc.Lang
}

var reDMY = regexp.MustCompile(`[dmY]`)

// DateFormatOptions は date_format_setting_options(locale)。
func (v *settingsView) DateFormatOptions() []any {
	today := v.a.userToday(v.c)
	var out []any
	for _, f := range settings.DateFormats {
		s := v.a.Bundle.LocalizeDate(v.formatLocale(), today, f)
		format := reDMY.ReplaceAllStringFunc(strings.ReplaceAll(f, "%", ""), func(m string) string {
			return map[string]string{"d": "dd", "m": "mm", "Y": "yyyy"}[m]
		})
		out = append(out, []any{s + " (" + format + ")", f})
	}
	return out
}

// TimeFormatOptions は Setting::TIME_FORMATS.collect {|f| [::I18n.l(Time.now, :locale => locale, :format => f), f]}。
func (v *settingsView) TimeFormatOptions() []any {
	now := v.a.now().In(time.Local)
	var out []any
	for _, f := range settings.TimeFormats {
		out = append(out, []any{v.a.Bundle.LocalizeTime(v.formatLocale(), now, f), f})
	}
	return out
}

// TimespanFormatOptions は [["%.2f" % 0.75, 'decimal'], ['0:45 h', 'minutes']]。
func (v *settingsView) TimespanFormatOptions() []any {
	return []any{[]any{"0.75", "decimal"}, []any{"0:45 h", "minutes"}}
}

// NewItemMenuTabOptions は new_item_menu_tab の選択肢。
func (v *settingsView) NewItemMenuTabOptions() []any {
	return []any{[]any{v.c.L("label_none"), "0"}, []any{v.c.L("label_new_project_issue_tab_enabled"), "1"},
		[]any{v.c.L("label_new_object_tab_enabled"), "2"}}
}

// AnonymousRolePath は edit_role_path(Role.anonymous)。
func (v *settingsView) AnonymousRolePath() (string, error) {
	r, err := repository.BuiltinRole(v.c.Ctx(), v.a.DB, domain.RoleBuiltinAnonymous)
	if err != nil {
		return "", err
	}
	return "/roles/" + strconv.FormatInt(r.ID, 10) + "/edit", nil
}

// PasswordCharClassOptions は Setting::PASSWORD_CHAR_CLASSES.keys.collect {|c| [l("label_password_char_class_#{c}"), c]}。
func (v *settingsView) PasswordCharClassOptions() []any {
	var out []any
	for _, c := range settings.PasswordCharClasses {
		out = append(out, []any{v.c.L("label_password_char_class_" + c.Name), c.Name})
	}
	return out
}

// TwofaOptions は twofa の選択肢。
func (v *settingsView) TwofaOptions() []any {
	l := v.c.L
	return []any{[]any{l("label_disabled"), "0"}, []any{l("label_optional"), "1"},
		[]any{l("label_required_administrators"), "3"}, []any{l("label_required_lower"), "2"}}
}

// ProjectModuleOptions は Redmine::AccessControl.available_project_modules の選択肢。
func (v *settingsView) ProjectModuleOptions() []any {
	var out []any
	for _, m := range permission.AvailableProjectModules() {
		out = append(out, []any{v.c.Loc.LOrHumanize(m, "project_module_"), m})
	}
	return out
}

// TrackerOptions は Tracker.sorted.collect {|t| [t.name, t.id.to_s]}。
func (v *settingsView) TrackerOptions() []any {
	var out []any
	for _, t := range v.trackers {
		out = append(out, []any{t.Name, strconv.FormatInt(t.ID, 10)})
	}
	return out
}

// GivableRoleOptions は Role.find_all_givable.collect {|r| [r.name, r.id.to_s]}。
func (v *settingsView) GivableRoleOptions() []any {
	var out []any
	for _, r := range v.roles {
		out = append(out, []any{r.Name, strconv.FormatInt(r.ID, 10)})
	}
	return out
}

// BlankOption は "--- l(:actionview_instancetag_blank_option) ---"。
func (v *settingsView) BlankOption() string {
	return "--- " + v.c.L("actionview_instancetag_blank_option") + " ---"
}

// ProjectListDisplayTypes は ProjectQuery.new(Setting.project_list_defaults).available_display_types。
func (v *settingsView) ProjectListDisplayTypes() []string {
	return v.ProjectQuery.Q.AvailableDisplayTypes()
}

func (v *settingsView) globalQueryOptions(kind string) ([]any, error) {
	qs, err := repository.GlobalQueryOptions(v.c.Ctx(), v.a.DB, kind)
	if err != nil {
		return nil, err
	}
	out := []any{[]any{v.c.L("label_none"), ""}}
	for _, q := range qs {
		if q.Visibility == domain.QueryVisibilityPublic {
			out = append(out, []any{q.Name, q.ID})
		}
	}
	return out, nil
}

// DefaultGlobalProjectQueryOptions は default_global_project_query_options。
func (v *settingsView) DefaultGlobalProjectQueryOptions() ([]any, error) {
	return v.globalQueryOptions("project")
}

// DefaultGlobalIssueQueryOptions は default_global_issue_query_options。
func (v *settingsView) DefaultGlobalIssueQueryOptions() ([]any, error) {
	return v.globalQueryOptions("issue")
}

// NotificationOptions は User.valid_notification_options.collect {|o| [l(o.last), o.first.to_s]}（グローバル：selected を除く）。
func (v *settingsView) NotificationOptions() []any {
	l := v.c.L
	return []any{
		[]any{l("label_user_mail_option_all"), "all"},
		[]any{l("label_user_mail_option_only_my_events"), "only_my_events"},
		[]any{l("label_user_mail_option_only_assigned"), "only_assigned"},
		[]any{l("label_user_mail_option_only_owner"), "only_owner"},
		[]any{l("label_user_mail_option_none"), "none"},
	}
}

// AutoWatchOnOptions は UserPreference::AUTO_WATCH_ON_OPTIONS の選択肢。
func (v *settingsView) AutoWatchOnOptions() []any {
	var out []any
	for _, o := range []string{"issue_created", "issue_contributed_to"} {
		out = append(out, []any{v.c.L("label_auto_watch_on_" + o), o})
	}
	return out
}

// DoneRatioOptions は Issue::DONE_RATIO_OPTIONS の選択肢。
func (v *settingsView) DoneRatioOptions() []any {
	var out []any
	for _, o := range []string{"issue_field", "issue_status"} {
		out = append(out, []any{v.c.L("setting_issue_done_ratio_" + o), o})
	}
	return out
}

// NonWorkingWeekDayOptions は (1..7).map {|d| [day_name(d), d.to_s]}。
func (v *settingsView) NonWorkingWeekDayOptions() []any {
	var out []any
	for d := 1; d <= 7; d++ {
		out = append(out, []any{v.c.Loc.DayName(d), strconv.Itoa(d)})
	}
	return out
}

// ParentDatesLabel は "#{l(:field_start_date)} / #{l(:field_due_date)}"。
func (v *settingsView) ParentDatesLabel() string {
	return v.c.L("field_start_date") + " / " + v.c.L("field_due_date")
}

// IssueTotalOptions は IssueQuery.new(:totalable_names => ...).available_totalable_columns.map {|c| [c.caption, c.name.to_s]}。
func (v *settingsView) IssueTotalOptions() ([]any, error) {
	cols, err := v.IssueQuery.Q.AvailableTotalableColumns(v.c.Ctx())
	if err != nil {
		return nil, err
	}
	var out []any
	for _, c := range cols {
		out = append(out, []any{v.IssueQuery.Caption(c), c.Name})
	}
	return out, nil
}

// TimeEntryTotalsTags は available_totalable_columns_tags(query, :name => 'settings[time_entry_list_defaults][totalable_names]')。
func (v *settingsView) TimeEntryTotalsTags() (template.HTML, error) {
	return v.TimeEntryQuery.totalableColumnsTagsNamed("settings[time_entry_list_defaults][totalable_names]")
}

// totalableColumnsTagsNamed は available_totalable_columns_tags(query, :name => name)。
func (qv *queryView) totalableColumnsTagsNamed(name string) (template.HTML, error) {
	cols, err := qv.Q.AvailableTotalableColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	totals, err := qv.Q.TotalableColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	tag := name + "[]"
	var b strings.Builder
	for _, c := range cols {
		b.WriteString(string(rails.ContentTag("label",
			rails.CheckBoxTag(tag, c.Name, slices.Contains(totals, c), rails.NewHash("id", nil))+rails.H(" "+qv.Caption(c)),
			rails.NewHash("class", "inline"))))
	}
	b.WriteString(string(rails.HiddenFieldTag(tag, "", nil)))
	return template.HTML(b.String()), nil
}

// MailHandlerBodyPartOptions は mail_handler_preferred_body_part の選択肢。
func (v *settingsView) MailHandlerBodyPartOptions() []any {
	return []any{[]any{v.c.L("label_preferred_body_part_text"), "plain"}, []any{v.c.L("label_preferred_body_part_html"), "html"}}
}

// ---------------------------------------------------------------- リポジトリ

// SCM は settings/_repositories の 1 行（Redmine::Scm::Base.all の要素）。
type SCM struct {
	Name      string
	Enabled   bool
	Available bool
	Command   string
	Version   string
}

var gitVersion struct {
	once      sync.Once
	available bool
	version   string
}

var reGitVersion = regexp.MustCompile(`\d+(?:\.\d+)+`)

// gitCommandVersion は Repository::Git.scm_available / scm_version_string（git --version を 1 回だけ実行する）。
func gitCommandVersion(ctx context.Context) (bool, string) {
	gitVersion.once.Do(func() {
		path, err := exec.LookPath("git")
		if err != nil {
			return
		}
		out, err := exec.CommandContext(context.WithoutCancel(ctx), path, "--version").Output()
		if err != nil {
			return
		}
		gitVersion.available = true
		gitVersion.version = reGitVersion.FindString(string(out))
	})
	return gitVersion.available, gitVersion.version
}

// SCMs は有効化できる SCM の一覧。
//
// 意図的な差異: buropher は Git のみ対応するため（docs: D-15）、Redmine の表
// （Subversion / Mercurial / Cvs / Bazaar / Git / Filesystem）のうち Git の行だけを出す。
func (v *settingsView) SCMs() []SCM {
	enabled := slices.Contains(v.a.Settings.Strings("enabled_scm"), "Git")
	s := SCM{Name: "Git", Enabled: enabled, Command: "git"}
	if enabled {
		s.Available, s.Version = gitCommandVersion(v.c.Ctx())
	}
	return []SCM{s}
}

// CommitLogtimeActivityOptions は [[l(:label_default), 0]] + TimeEntryActivity.shared.active.collect {...}。
func (v *settingsView) CommitLogtimeActivityOptions() ([]any, error) {
	acts, err := repository.ListEnumerations(v.c.Ctx(), v.a.DB, domain.EnumTimeEntryActivity, true)
	if err != nil {
		return nil, err
	}
	out := []any{[]any{v.c.L("label_default"), 0}}
	for _, e := range acts {
		if e.Active {
			out = append(out, []any{e.Name, strconv.FormatInt(e.ID, 10)})
		}
	}
	return out, nil
}

// CommitTrackerOptions は [[l(:label_all), ""]] + Tracker.sorted.map {|t| [t.name, t.id.to_s]}。
func (v *settingsView) CommitTrackerOptions() []any {
	return append([]any{[]any{v.c.L("label_all"), ""}}, v.TrackerOptions()...)
}

// CommitStatusOptions は [["", 0]] + IssueStatus.sorted.collect {...}。
func (v *settingsView) CommitStatusOptions() []any {
	out := []any{[]any{"", 0}}
	for _, s := range v.statuses {
		out = append(out, []any{s.Name, strconv.FormatInt(s.ID, 10)})
	}
	return out
}

// CommitDoneRatioOptions は [["", ""]] + (0..100).step(Setting.issue_done_ratio_interval.to_i)...。
func (v *settingsView) CommitDoneRatioOptions() []any {
	out := []any{[]any{"", ""}}
	step := v.a.Settings.Int("issue_done_ratio_interval")
	if step <= 0 {
		step = 10
	}
	for r := 0; r <= 100; r += step {
		out = append(out, []any{strconv.Itoa(r) + " %", r})
	}
	return out
}

// TextFormatting は Setting.text_formatting。
func (v *settingsView) TextFormatting() string { return v.a.Settings.String("text_formatting") }

// GravatarDefault は Setting.gravatar_default。
func (v *settingsView) GravatarDefault() string { return v.a.Settings.String("gravatar_default") }

// SelfRegistration は Setting.self_registration?。
func (v *settingsView) SelfRegistration() bool { return v.a.Settings.Bool("self_registration") }

// MailHandlerAPIEnabled は Setting.mail_handler_api_enabled?。
func (v *settingsView) MailHandlerAPIEnabled() bool {
	return v.a.Settings.Bool("mail_handler_api_enabled")
}

// SysAPIEnabled は Setting.sys_api_enabled?。
func (v *settingsView) SysAPIEnabled() bool { return v.a.Settings.Bool("sys_api_enabled") }

// CommitLogtimeEnabled は Setting.commit_logtime_enabled?。
func (v *settingsView) CommitLogtimeEnabled() bool {
	return v.a.Settings.Bool("commit_logtime_enabled")
}

// ProjectListDisplayType は Setting.project_list_display_type。
func (v *settingsView) ProjectListDisplayType() string {
	return v.a.Settings.String("project_list_display_type")
}

// AvatarServerURL は Redmine::Configuration['avatar_server_url']。
func (v *settingsView) AvatarServerURL() string { return "https://www.gravatar.com" }
