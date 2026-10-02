package issues

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
)

// ParseBulkParams は parse_params_for_bulk_update (空の値を除き、"none" / "__none__" を空にする)。
func ParseBulkParams(p Params) Params {
	out := Params{}
	for k, v := range p {
		if present(v) {
			out[k] = v
		}
	}
	if cf, ok := out["custom_field_values"].(map[string]any); ok {
		m := map[string]any{}
		for k, v := range cf {
			if present(v) {
				m[k] = v
			}
		}
		out["custom_field_values"] = m
	}
	return ReplaceNoneValuesWithBlank(out)
}

// ReplaceNoneValuesWithBlank は replace_none_values_with_blank。
func ReplaceNoneValuesWithBlank(p Params) Params {
	for k, v := range p {
		if s, ok := v.(string); ok && s == "none" {
			p[k] = ""
		}
	}
	if cf, ok := p["custom_field_values"].(map[string]any); ok {
		for k, v := range cf {
			switch x := v.(type) {
			case []string:
				if i := slices.Index(x, "__none__"); i >= 0 {
					x = slices.Delete(slices.Clone(x), i, i+1)
					cf[k] = append(x, "")
				}
			case string:
				if x == "__none__" {
					cf[k] = ""
				}
			}
		}
	}
	return p
}

// BulkOptions は BulkUpdate のオプション (IssuesController#bulk_update)。
type BulkOptions struct {
	Notes          string
	Copy           bool
	CopySubtasks   bool
	CopyWatchers   bool
	CopyAttachment bool
	Link           bool
}

// BulkResult は一括更新の結果。
type BulkResult struct {
	Saved   []*Issue
	Unsaved []*Issue // 元のチケット
	SaveResult
}

// ErrUnauthorized は権限が無い (::Unauthorized)。
var ErrUnauthorized = errors.New("issues: unauthorized")

// BulkUpdate は IssuesController#bulk_update の本体。attrs は ParseBulkParams 済みの params[:issue]。
// チケットごとに保存し (失敗しても続ける)、保存できたものと失敗した元チケットを返す。
func (e *Env) BulkUpdate(ctx context.Context, issues []*Issue, attrs Params, opts BulkOptions, u *domain.User) (*BulkResult, error) {
	// @issues.sort! (root_id, lft 順)
	sorted := slices.Clone(issues)
	slices.SortStableFunc(sorted, func(a, b *Issue) int {
		if a.RootID != b.RootID {
			return int(a.RootID - b.RootID)
		}
		return strings.Compare(a.HierPath, b.HierPath)
	})
	var projects []*domain.Project
	for _, iss := range sorted {
		p, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(projects, func(x *domain.Project) bool { return x.ID == p.ID }) {
			projects = append(projects, p)
		}
	}
	copyWatchers := opts.CopyWatchers
	if opts.Copy {
		if ok, err := e.authz(u).AllowedToProjects(ctx, domain.Perm("copy_issues"), projects, nil); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrUnauthorized
		}
		targets := projects
		if pid, ok := attrs["project_id"]; ok && present(pid) {
			p, err := e.Project(ctx, rubyToI(rubyToS(pid)))
			if err != nil {
				return nil, err
			}
			targets = nil
			if p != nil {
				targets = []*domain.Project{p}
			}
		}
		if ok, err := e.authz(u).AllowedToProjects(ctx, domain.Perm("add_issues"), targets, nil); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrUnauthorized
		}
		if ok, err := e.authz(u).AllowedToProjects(ctx, domain.Perm("add_issue_watchers"), projects, nil); err != nil {
			return nil, err
		} else if !ok {
			copyWatchers = false
		}
	} else {
		for _, iss := range sorted {
			ok, err := e.AttributesEditable(ctx, iss, u)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, ErrUnauthorized
			}
		}
	}
	if opts.Copy && opts.CopySubtasks {
		var kept []*Issue
		for _, iss := range sorted {
			desc := false
			for _, other := range sorted {
				d, err := e.IsDescendantOf(ctx, iss, other)
				if err != nil {
					return nil, err
				}
				if d {
					desc = true
					break
				}
			}
			if !desc {
				kept = append(kept, iss)
			}
		}
		sorted = kept
	}
	res := &BulkResult{}
	for _, orig := range sorted {
		if err := e.Reload(ctx, orig); err != nil {
			return nil, err
		}
		iss := orig
		if opts.Copy {
			c, err := e.Copy(ctx, orig, Params{}, CopyOptions{NoAttachments: !opts.CopyAttachment, NoSubtasks: !opts.CopySubtasks,
				NoWatchers: !copyWatchers, NoLink: !opts.Link})
			if err != nil {
				return nil, err
			}
			iss = c
		}
		if _, err := e.InitJournal(ctx, iss, u, opts.Notes); err != nil {
			return nil, err
		}
		if err := e.SafeAssign(ctx, iss, cloneParams(attrs), u); err != nil {
			return nil, err
		}
		ok, sr, err := e.Save(ctx, iss)
		if err != nil {
			return nil, err
		}
		if ok {
			res.Saved = append(res.Saved, iss)
			res.Notifications = append(res.Notifications, sr.Notifications...)
		} else {
			res.Unsaved = append(res.Unsaved, orig)
		}
	}
	return res, nil
}

func cloneParams(p Params) Params {
	out := Params{}
	for k, v := range p {
		if m, ok := v.(map[string]any); ok {
			c := map[string]any{}
			for k2, v2 := range m {
				c[k2] = v2
			}
			v = c
		}
		out[k] = v
	}
	return out
}
