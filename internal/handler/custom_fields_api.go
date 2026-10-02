package handler

import (
	"slices"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/repository"
)

// renderCustomFieldsAPI は custom_fields/index.api.rsb（@custom_fields = CustomField.all）。
func (a *App) renderCustomFieldsAPI(c *Req) {
	ctx := c.Ctx()
	cfs, err := customfield.Load(ctx, a.DB, "")
	if err != nil {
		a.renderErr(c, "custom fields api", err)
		return
	}
	// CustomField.all は順序指定なし（id 順）
	slices.SortStableFunc(cfs, func(x, y *customfield.CustomField) int { return int(x.ID - y.ID) })
	trackers, err := repository.ListTrackers(ctx, a.DB)
	if err != nil {
		a.renderErr(c, "custom fields api", err)
		return
	}
	roles, err := repository.ListRoles(ctx, a.DB)
	if err != nil {
		a.renderErr(c, "custom fields api", err)
		return
	}
	trackerByID := map[int64]*domain.Tracker{}
	for _, t := range trackers {
		trackerByID[t.ID] = t
	}
	roleByID := map[int64]*domain.Role{}
	for _, r := range roles {
		roleByID[r.ID] = r
	}
	env := a.cfEnv(c)
	page := c.Page()
	c.RenderAPI(0, func(b apibuilder.Builder) {
		b.Array("custom_fields", nil, func() {
			for _, f := range cfs {
				b.Object("custom_field", func() {
					b.Value("id", f.ID)
					b.Value("name", f.Name)
					b.Value("description", f.Description)
					// customized_type は customized_class.name.underscore（基底の CustomField は出さない）
					if f.OwnerKind != "" {
						b.Value("customized_type", string(f.OwnerKind))
					}
					b.Value("field_format", f.FieldFormat)
					b.Value("regexp", f.Regexp)
					b.Value("min_length", intPtrValue(f.MinLength))
					b.Value("max_length", intPtrValue(f.MaxLength))
					b.Value("is_required", f.IsRequired)
					b.Value("is_filter", f.IsFilter)
					b.Value("searchable", f.Searchable)
					b.Value("multiple", f.Multiple)
					b.Value("default_value", f.DefaultValue)
					b.Value("visible", f.Visible)
					b.Value("editable", f.Editable)
					if opts := customfield.PossibleValuesOptionsFor(env, f, nil); len(opts) > 0 {
						b.Array("possible_values", nil, func() {
							for _, o := range opts {
								b.Object("possible_value", func() {
									// api.value value || label
									v := o.Value
									if v == "" {
										v = o.Label
									}
									b.Value("value", v)
									b.Value("label", o.Label)
								})
							}
						})
					}
					if f.OwnerKind == customfield.KindIssue {
						b.Array("trackers", nil, func() {
							for _, id := range f.TrackerIDs {
								if t := trackerByID[id]; t != nil {
									b.Attrs("tracker", apibuilder.A("id", t.ID, "name", t.Name))
								}
							}
						})
						b.Array("roles", nil, func() {
							for _, id := range f.RoleIDs {
								if r := roleByID[id]; r != nil {
									b.Attrs("role", apibuilder.A("id", r.ID, "name", helper.RoleName(page, r)))
								}
							}
						})
					}
				})
			}
		})
	})
}

// intPtrValue は *int を API の値（nil は null）にする。
func intPtrValue(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
