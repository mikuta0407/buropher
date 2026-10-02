package helper

import (
	"slices"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// WorkflowItem はワークフロー画面の選択肢（ロール・トラッカー。Name は表示名＝組込ロールは翻訳済み）。
type WorkflowItem struct {
	ID   int64
	Name string
	// Builtin は組込ロール（一覧の見出しを <em> にする）。
	Builtin bool
}

// WorkflowField はフィールド権限の行（標準フィールドまたはカスタムフィールド）。
type WorkflowField struct {
	// Key は permissions[status_id][key] のキー（標準フィールド名またはカスタムフィールド ID）。
	Key   string
	Label string
	// Required は field_required?（必須のため「必須」の選択肢を出さない）。
	Required bool
	// Hidden は非表示のカスタムフィールドで、選択ロールのいずれにも表示されない（field_permission_tag の hidden）。
	Hidden bool
}

func (h adminH) workflowFuncs() ttemplate.FuncMap {
	return ttemplate.FuncMap{
		"options_for_workflow_select": h.optionsForWorkflowSelect,
		"transition_tag":              h.transitionTag,
		"field_permission_tag":        h.fieldPermissionTag,
		// join_rules は @permissions[status.id][field].try(:join, ' ')。
		"join_rules": func(perms map[string][]string, key string) string { return strings.Join(perms[key], " ") },
	}
}

// optionsForWorkflowSelect は WorkflowsHelper#options_for_workflow_select(name, objects, selected, options)。
// selected は選択中の項目（nil なら先頭を選択）。
func (h adminH) optionsForWorkflowSelect(name string, objects, selected []WorkflowItem, opts *rails.Hash) html {
	multiple := false
	var sel any
	all := false
	if selected != nil {
		if len(selected) == len(objects) {
			all = true
		} else {
			ids := make([]any, len(selected))
			for i, s := range selected {
				ids[i] = s.ID
			}
			sel = ids
			multiple = len(selected) > 1
		}
	} else if len(objects) > 0 {
		sel = objects[0].ID
	}
	allOpts := rails.NewHash("value", "all", "selected", all)
	if multiple {
		allOpts.Set("style", "display:none;")
	}
	tags := rails.ContentTag("option", h.l("label_all"), allOpts)
	if all {
		sel = "all"
	}
	tags += rails.OptionsFromCollectionForSelect(objects, "id", "name", sel)
	return rails.SelectTag(name, tags, rails.NewHash("multiple", multiple).Update(opts))
}

// transitionTag は WorkflowsHelper#transition_tag(transition_count, old_status, new_status, name)。
// oldID は遷移元ステータス（新規チケットは 0）、total は @roles.size * @trackers.size。
func (h adminH) transitionTag(count int, oldID, newID int64, name string, total int) html {
	o, n := strconv.FormatInt(oldID, 10), strconv.FormatInt(newID, 10)
	tagName := "transitions[" + o + "][" + n + "][" + name + "]"
	class := "old-status-" + o + " new-status-" + n
	switch {
	case oldID == newID:
		return rails.CheckBoxTag(tagName, "1", true, rails.NewHash("disabled", true, "class", class))
	case count == 0 || count == total:
		return rails.HiddenFieldTag(tagName, "0", rails.NewHash("id", nil)) +
			rails.CheckBoxTag(tagName, "1", count != 0, rails.NewHash("class", class))
	default:
		return rails.SelectTag(tagName, rails.OptionsForSelect([]any{
			[]any{h.l("general_text_Yes"), "1"},
			[]any{h.l("general_text_No"), "0"},
			[]any{h.l("label_no_change_option"), "no_change"},
		}, "no_change"), nil)
	}
}

// fieldPermissionTag は WorkflowsHelper#field_permission_tag(permissions, status, field, roles)。
// perms は permissions[status.id]（フィールド → 規則の配列）、total は @roles.size * @trackers.size。
func (h adminH) fieldPermissionTag(perms map[string][]string, statusID int64, field WorkflowField, total int) html {
	options := []any{[]any{"", ""}, []any{h.l("label_readonly"), "readonly"}}
	if !field.Required {
		options = append(options, []any{h.l("label_required"), "required"})
	}
	htmlOpts := rails.NewHash()
	var selected any
	if perm, ok := perms[field.Key]; ok && perm != nil {
		uniq := slices.Compact(slices.Sorted(slices.Values(perm)))
		if len(uniq) > 1 || len(perm) < total {
			options = append(options, []any{h.l("label_no_change_option"), "no_change"})
			selected = "no_change"
		} else {
			selected = perm[0]
		}
	}
	if field.Hidden {
		options[0] = []any{h.l("label_hidden"), ""}
		selected = ""
		htmlOpts.Set("disabled", true)
	}
	return rails.SelectTag("permissions["+strconv.FormatInt(statusID, 10)+"]["+field.Key+"]",
		rails.OptionsForSelect(options, selected), htmlOpts)
}
