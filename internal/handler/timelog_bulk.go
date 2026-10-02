package handler

import (
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは timelog#bulk_edit / bulk_update（timelog/bulk_edit.html, bulk_edit.js）の移植。

// teBulkView は bulk_edit の表示データ。
type teBulkView struct {
	Entries         []*teBulkEntry
	Unsaved         []string
	FailedMessage   string
	ProjectOptions  template.HTML
	IssueID         string
	IssueNone       bool
	SpentOn         string
	Hours           string
	Comments        string
	Activities      []*domain.Enumeration
	ActivityOptions template.HTML
	ActivityID      string
	CustomFields    []template.HTML
	Autocomplete    bool
	ProjectID       string
}

// teBulkEntry は #bulk-selection の 1 行。
type teBulkEntry struct {
	ID    int64
	Label string
}

// teBulkParams は params[:time_entry]（未指定なら空）。
func teBulkParams(c *Req) *httpx.Params {
	if m := c.Params().Map("time_entry"); m != nil {
		return m
	}
	return httpx.NewParams()
}

// teBulkEditView は bulk_edit アクションの本体（@target_projects 等の計算）。
func (a *App) teBulkEditView(c *Req, entries []*timelog.Entry, unsaved []*timelog.Entry, savedCount int) (*teBulkView, error) {
	ctx := c.Ctx()
	env := a.teEnv(c)
	tp := teBulkParams(c)
	v := &teBulkView{}
	l, err := a.newTELookup(c, entries)
	if err != nil {
		return nil, err
	}
	for _, t := range entries {
		label := ""
		if t.SpentOn != nil {
			label = helper.FormatDate(c.Page(), *t.SpentOn)
		}
		pname := ""
		if p := l.project(t.ProjectID); p != nil {
			pname = p.Name
		}
		h := 0.0
		if rh := t.RoundedHours(); rh != nil {
			h = *rh
		}
		uname := ""
		if u := l.user(t.UserID); u != nil {
			uname = helper.PrincipalName(c.Page(), u)
		}
		label += " - " + pname + ": " + c.L("label_f_hour_plural", map[string]any{"value": c.Loc.FormatHours(h)}) + " (" + uname + ")"
		v.Entries = append(v.Entries, &teBulkEntry{ID: t.ID, Label: label})
	}
	if len(unsaved) > 0 {
		var ids []string
		for _, t := range unsaved {
			ids = append(ids, "#"+strconv.FormatInt(t.ID, 10))
		}
		v.FailedMessage = c.L("notice_failed_to_save_time_entries", map[string]any{"count": len(unsaved), "total": savedCount, "ids": strings.Join(ids, ", ")})
		var order []string
		byMsg := map[string][]string{}
		for _, t := range unsaved {
			for _, m := range t.Errors.FullMessages(c.Loc) {
				if _, ok := byMsg[m]; !ok {
					order = append(order, m)
				}
				byMsg[m] = append(byMsg[m], "#"+strconv.FormatInt(t.ID, 10))
			}
		}
		for _, m := range order {
			v.Unsaved = append(v.Unsaved, m+": "+strings.Join(byMsg[m], ", "))
		}
	}
	// @target_projects / @target_project
	targetID := tp.String("project_id")
	var target *domain.Project
	if targetID != "" {
		if id, err := strconv.ParseInt(targetID, 10, 64); err == nil {
			p, err := env.Project(ctx, &id)
			if err != nil {
				return nil, err
			}
			if p != nil && c.AllowedTo(domain.Perm("log_time"), p) {
				target = p
			}
		}
	}
	var sel *int64
	if target != nil {
		sel = &target.ID
	}
	opts, err := a.teProjectTreeOptions(c, sel)
	if err != nil {
		return nil, err
	}
	// include_blank は label_no_change_option
	opts = template.HTML(strings.Replace(string(opts), `<option value="">&nbsp;</option>`,
		string(rails.ContentTag("option", c.L("label_no_change_option"), rails.NewHash("value", ""))), 1))
	v.ProjectOptions = opts
	// @available_activities
	if target != nil {
		if v.Activities, err = repository.TimelogProjectActivities(ctx, a.DB, target.ID, false); err != nil {
			return nil, err
		}
	} else {
		for i, p := range c.Projects {
			acts, err := repository.TimelogProjectActivities(ctx, a.DB, p.ID, false)
			if err != nil {
				return nil, err
			}
			if i == 0 {
				v.Activities = acts
			} else {
				v.Activities = slices.DeleteFunc(v.Activities, func(e *domain.Enumeration) bool {
					return !slices.ContainsFunc(acts, func(x *domain.Enumeration) bool { return x.ID == e.ID })
				})
			}
		}
	}
	issue := tp.String("issue_id")
	v.IssueNone = issue == "none"
	if !v.IssueNone {
		v.IssueID = issue
	}
	v.SpentOn = tp.String("spent_on")
	v.Hours = tp.String("hours")
	v.Comments = tp.String("comments")
	v.ActivityID = tp.String("activity_id")
	// content_tag('option', l(:label_no_change_option), :value => '') + options_from_collection_for_select(...)
	var aopts [][]any
	for _, act := range v.Activities {
		aopts = append(aopts, []any{act.Name, act.ID})
	}
	v.ActivityOptions = rails.ContentTag("option", c.L("label_no_change_option"), rails.NewHash("value", "")) +
		rails.OptionsForSelect(aopts, teNilIfEmpty(v.ActivityID))
	// @custom_fields（TimeEntry.first.available_custom_fields の bulk_edit_supported なもの）
	cfs, err := customfield.ListByKind(ctx, a.DB, customfield.OwnerKind("time_entry"))
	if err != nil {
		return nil, err
	}
	cfParams := tp.Map("custom_field_values")
	for _, cf := range cfs {
		if !cf.Format().BulkEditSupported {
			continue
		}
		val := ""
		if cfParams != nil {
			val = cfParams.String(strconv.FormatInt(cf.ID, 10))
		}
		v.CustomFields = append(v.CustomFields, a.teBulkEditCFTag(c, cf, val))
	}
	v.Autocomplete = c.Project != nil || target != nil
	if c.Project != nil {
		v.ProjectID = strconv.FormatInt(c.Project.ID, 10)
	}
	return v, nil
}

// teBulkEditCFTag は <label> と custom_field_tag_for_bulk_edit('time_entry', field, objects, value)。
func (a *App) teBulkEditCFTag(c *Req, cf *customfield.CustomField, value string) template.HTML {
	id := "time_entry_custom_field_values_" + strconv.FormatInt(cf.ID, 10)
	name := "time_entry[custom_field_values][" + strconv.FormatInt(cf.ID, 10) + "]"
	css := cf.FieldFormat + "_cf cf_" + strconv.FormatInt(cf.ID, 10)
	label := rails.ContentTag("label", cf.Name, nil)
	var tag template.HTML
	switch cf.FieldFormat {
	case "list", "bool", "enumeration", "user", "version":
		var opts [][]any
		if !cf.Multiple {
			opts = append(opts, []any{c.L("label_no_change_option"), ""})
		}
		if !cf.IsRequired {
			opts = append(opts, []any{c.L("label_none"), "__none__"})
		}
		env := &customfield.Env{T: c.L, CurrentUserID: c.User.ID}
		for _, o := range cf.Format().PossibleValuesOptions(env, cf, nil) {
			opts = append(opts, []any{o.Label, o.Value})
		}
		if cf.Multiple {
			name += "[]"
		}
		tag = rails.SelectTag(name, rails.OptionsForSelect(opts, teNilIfEmpty(value)), rails.NewHash("class", css, "id", id, "multiple", cf.Multiple))
	default:
		tag = rails.TextFieldTag(name, value, rails.NewHash("class", css, "id", id))
		if !cf.IsRequired {
			tag += rails.ContentTag("label", rails.CheckBoxTag(name, "__none__", value == "__none__", rails.NewHash("id", nil, "data", rails.NewHash("disables", "#"+id)))+template.HTML(rails.H(c.L("button_clear"))), rails.NewHash("class", "inline"))
		}
	}
	return label + "\n        " + tag
}

// TimelogBulkEdit は timelog#bulk_edit（POST /time_entries/bulk_edit.js はフォームの更新）。
func (a *App) TimelogBulkEdit(c *Req) {
	entries := c.value(teEntriesKey{}).([]*timelog.Entry)
	v, err := a.teBulkEditView(c, entries, nil, 0)
	if err != nil {
		a.internalError(c, "bulk edit", err)
		return
	}
	a.teRenderBulkEdit(c, v)
}

func (a *App) teRenderBulkEdit(c *Req, v *teBulkView) {
	data := map[string]any{"V": v}
	if teFormat(c, "html", "js") == "js" {
		// bulk_edit.js.erb: $('#content').html('<%= escape_javascript(render :template => 'timelog/bulk_edit', :formats => [:html]) %>');
		out, err := a.Views.Render(c.ViewContext(), "timelog/bulk_edit", data, view.RenderOptions{Format: "html", Layout: view.NoLayout})
		if err != nil {
			a.internalError(c, "render bulk_edit", err)
			return
		}
		c.halted = true
		httpx.SetContentType(c.W, "js", true)
		c.W.WriteHeader(http.StatusOK)
		_, _ = c.W.Write([]byte("$('#content').html('" + rails.EscapeJavascriptString(string(out)) + "');\n"))
		return
	}
	c.Render("timelog/bulk_edit", data)
}

// teParseBulkParams は parse_params_for_bulk_update(params[:time_entry])。
func teParseBulkParams(p *httpx.Params) *httpx.Params {
	out := httpx.NewParams()
	if p == nil {
		return out
	}
	p.Each(func(k string, v any) {
		if httpx.IsBlank(v) {
			return
		}
		if k == "custom_field_values" {
			if m, ok := v.(*httpx.Params); ok {
				cm := httpx.NewParams()
				m.Each(func(ck string, cv any) {
					if httpx.IsBlank(cv) {
						return
					}
					switch x := cv.(type) {
					case []any:
						var arr []any
						none := false
						for _, e := range x {
							if httpx.ValueString(e) == "__none__" {
								none = true
								continue
							}
							arr = append(arr, e)
						}
						if none {
							arr = append(arr, "")
						}
						cm.Set(ck, arr)
					default:
						if httpx.ValueString(cv) == "__none__" {
							cm.Set(ck, "")
						} else {
							cm.Set(ck, cv)
						}
					}
				})
				out.Set(k, cm)
				return
			}
		}
		if s, ok := v.(string); ok && s == "none" {
			out.Set(k, "")
			return
		}
		out.Set(k, v)
	})
	return out
}

// TimelogBulkUpdate は timelog#bulk_update。
func (a *App) TimelogBulkUpdate(c *Req) {
	ctx := c.Ctx()
	entries := c.value(teEntriesKey{}).([]*timelog.Entry)
	attrs := teParseBulkParams(c.Params().Map("time_entry"))
	env := a.teEnv(c)
	var saved, unsaved []*timelog.Entry
	for _, t := range entries {
		var ok bool
		err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
			q := env.Q
			env.Q = tx
			defer func() { env.Q = q }()
			fresh, err := env.Reload(ctx, t)
			if err != nil {
				return err
			}
			if err := env.SafeAssign(ctx, fresh, attrs, c.User); err != nil {
				return err
			}
			ok, err = env.Save(ctx, fresh)
			t.Errors = fresh.Errors
			return err
		})
		if err != nil {
			a.internalError(c, "bulk update", err)
			return
		}
		if ok {
			saved = append(saved, t)
		} else {
			unsaved = append(unsaved, t)
		}
	}
	if len(unsaved) == 0 {
		if len(saved) > 0 {
			c.Flash().SetNotice(c.L("notice_successful_update"))
		}
		var first *domain.Project
		if len(c.Projects) > 0 {
			first = c.Projects[0]
		}
		c.RedirectBackOrDefault(teTimeEntriesPath(first), false)
		return
	}
	// @time_entries = TimeEntry.where(:id => unsaved ids)
	var ids []int64
	for _, t := range unsaved {
		ids = append(ids, t.ID)
	}
	rs, err := repository.TimeEntriesByIDs(ctx, a.DB, ids)
	if err != nil {
		a.internalError(c, "bulk update", err)
		return
	}
	var list []*timelog.Entry
	for _, r := range rs {
		list = append(list, timelog.FromRecord(r))
	}
	v, err := a.teBulkEditView(c, list, unsaved, len(entries))
	if err != nil {
		a.internalError(c, "bulk edit", err)
		return
	}
	c.Render("timelog/bulk_edit", map[string]any{"V": v}, RenderOptions{Status: http.StatusOK})
}

// teNilIfEmpty は "" を nil にする（options_for_select の selected が nil の場合）。
func teNilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
