package issues

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// Params はフォーム / API から渡されるチケット属性 (params[:issue])。
// 値は string / []string / int / int64 / bool / nil、custom_field_values は map[string]any、
// custom_fields は []map[string]any。
type Params map[string]any

// SafeAttributeNames は safe_attribute_names(user)。
func (e *Env) SafeAttributeNames(ctx context.Context, iss *Issue, u *domain.User) ([]string, error) {
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return nil, err
		}
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	var names []string
	add := func(n ...string) {
		for _, x := range n {
			if !slices.Contains(names, x) {
				names = append(names, x)
			}
		}
	}
	editable := iss.NewRecord()
	if !editable {
		if editable, err = e.AttributesEditable(ctx, iss, u); err != nil {
			return nil, err
		}
	}
	if editable {
		add("project_id", "tracker_id", "status_id", "category_id", "assigned_to_id", "priority_id", "fixed_version_id",
			"subject", "description", "start_date", "due_date", "done_ratio", "estimated_hours", "custom_field_values",
			"custom_fields", "lock_version")
	}
	if ok, err := e.NotesAddable(ctx, iss, u); err != nil {
		return nil, err
	} else if ok {
		add("notes")
	}
	if !iss.NewRecord() {
		if ok, err := e.allowedTo(ctx, u, "set_notes_private", p); err != nil {
			return nil, err
		} else if ok {
			add("private_notes")
		}
	}
	if iss.NewRecord() {
		if ok, err := e.allowedTo(ctx, u, "add_issue_watchers", p); err != nil {
			return nil, err
		} else if ok {
			add("watcher_user_ids")
		}
	}
	priv, err := e.allowedTo(ctx, u, "set_issues_private", p)
	if err != nil {
		return nil, err
	}
	if !priv && iss.AuthorID == u.ID {
		if priv, err = e.allowedTo(ctx, u, "set_own_issues_private", p); err != nil {
			return nil, err
		}
	}
	if priv {
		add("is_private")
	}
	if editable {
		if ok, err := e.allowedTo(ctx, u, "manage_subtasks", p); err != nil {
			return nil, err
		} else if ok {
			add("parent_issue_id")
		}
	}
	if ok, err := e.AttachmentsDeletable(ctx, iss, u); err != nil {
		return nil, err
	} else if ok {
		add("deleted_attachment_ids")
	}
	dis, err := e.DisabledCoreFields(ctx, iss)
	if err != nil {
		return nil, err
	}
	ro, err := e.ReadOnlyAttributeNames(ctx, iss, u)
	if err != nil {
		return nil, err
	}
	names = slices.DeleteFunc(names, func(n string) bool { return slices.Contains(dis, n) || slices.Contains(ro, n) })
	if iss.NewRecord() {
		add("project_id")
	}
	if d, err := e.DatesDerived(ctx, iss); err != nil {
		return nil, err
	} else if d {
		names = slices.DeleteFunc(names, func(n string) bool { return n == "start_date" || n == "due_date" })
	}
	if d, err := e.PriorityDerived(ctx, iss); err != nil {
		return nil, err
	} else if d {
		names = slices.DeleteFunc(names, func(n string) bool { return n == "priority_id" })
	}
	if d, err := e.DoneRatioDerived(ctx, iss); err != nil {
		return nil, err
	} else if d {
		names = slices.DeleteFunc(names, func(n string) bool { return n == "done_ratio" })
	}
	return names, nil
}

// SafeAttribute は safe_attribute?(attr, user)。
func (e *Env) SafeAttribute(ctx context.Context, iss *Issue, attr string, u *domain.User) (bool, error) {
	names, err := e.SafeAttributeNames(ctx, iss, u)
	return slices.Contains(names, attr), err
}

var digitsOnlyRe = regexp.MustCompile(`^\d*$`)

// SafeAssign は safe_attributes=(attrs, user)。
//
// Redmine と同じく project_id / tracker_id / status_id / assigned_to_id の可否 (safe_attribute?) は
// User.current (e.User) で、それ以外の属性の可否は user で判定する。
func (e *Env) SafeAssign(ctx context.Context, iss *Issue, params Params, u *domain.User) error {
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return err
		}
	}
	iss.attributesSetBy = u
	if params == nil {
		return nil
	}
	attrs := Params{}
	for k, v := range params {
		attrs[k] = v
	}
	cur, err := e.currentUser(ctx)
	if err != nil {
		return err
	}
	safeCur := func(attr string) (bool, error) { return e.SafeAttribute(ctx, iss, attr, cur) }

	if p, ok := attrs["project_id"]; ok {
		delete(attrs, "project_id")
		if p != nil {
			if ok, err := safeCur("project_id"); err != nil {
				return err
			} else if ok {
				var pid int64
				if s, isStr := p.(string); isStr && !digitsOnlyRe.MatchString(s) {
					pr, err := repository.FindProjectByIdentifier(ctx, e.Q, s)
					if err == nil {
						pid = pr.ID
					} else if err != repository.ErrNotFound {
						return err
					}
				} else {
					pid = rubyToI(rubyToS(p))
				}
				allowed, err := e.projectAllowedAsTarget(ctx, iss, u, pid)
				if err != nil {
					return err
				}
				if allowed {
					if err := e.SetProjectID(ctx, iss, pid); err != nil {
						return err
					}
				}
				if iss.AttrChanged("project_id") {
					if c, ok := attrs["category_id"]; ok && present(c) {
						was := ""
						if iss.orig != nil && iss.orig.CategoryID != nil {
							was = strconv.FormatInt(*iss.orig.CategoryID, 10)
						}
						if rubyToS(c) == was {
							delete(attrs, "category_id")
						}
					}
				}
			}
		}
	}

	if t, ok := attrs["tracker_id"]; ok {
		delete(attrs, "tracker_id")
		if t != nil {
			if ok, err := safeCur("tracker_id"); err != nil {
				return err
			} else if ok {
				ts, err := e.AllowedTargetTrackers(ctx, iss, u)
				if err != nil {
					return err
				}
				tid := rubyToI(rubyToS(t))
				if containsTracker(ts, tid) {
					if err := e.SetTrackerID(ctx, iss, tid); err != nil {
						return err
					}
				}
			}
		}
	}
	if iss.ProjectID != 0 && iss.TrackerID == 0 {
		ts, err := e.AllowedTargetTrackers(ctx, iss, u)
		if err != nil {
			return err
		}
		if pid, ok := attrs["parent_issue_id"]; ok && present(pid) {
			var found *domain.Tracker
			for _, t := range ts {
				if slices.Contains(t.CoreFields(), "parent_issue_id") {
					found = t
					break
				}
			}
			if err := e.SetTracker(ctx, iss, found); err != nil {
				return err
			}
		}
		if iss.TrackerID == 0 && len(ts) > 0 {
			if err := e.SetTracker(ctx, iss, ts[0]); err != nil {
				return err
			}
		}
	}

	allowed, err := e.NewStatusesAllowedTo(ctx, iss, u, false)
	if err != nil {
		return err
	}
	if s, ok := attrs["status_id"]; ok {
		delete(attrs, "status_id")
		if s != nil {
			if ok, err := safeCur("status_id"); err != nil {
				return err
			} else if ok {
				sid := rubyToI(rubyToS(s))
				if slices.ContainsFunc(allowed, func(x *domain.IssueStatus) bool { return x.ID == sid }) {
					if err := e.SetStatusID(ctx, iss, sid); err != nil {
						return err
					}
				}
			}
		}
	}
	if iss.NewRecord() && !slices.ContainsFunc(allowed, func(x *domain.IssueStatus) bool { return x.ID == iss.StatusID }) {
		if len(allowed) > 0 {
			iss.StatusID = allowed[0].ID
		} else {
			ds, err := e.DefaultStatus(ctx, iss)
			if err != nil {
				return err
			}
			iss.StatusID = 0
			if ds != nil {
				iss.StatusID = ds.ID
			}
		}
	}
	if a, ok := attrs["assigned_to_id"]; ok {
		delete(attrs, "assigned_to_id")
		if a != nil {
			if ok, err := safeCur("assigned_to_id"); err != nil {
				return err
			} else if ok {
				iss.AssignedToID = castID(a)
			}
		}
	}

	safe, err := e.SafeAttributeNames(ctx, iss, u)
	if err != nil {
		return err
	}
	for k := range attrs {
		if !slices.Contains(safe, k) {
			delete(attrs, k)
		}
	}
	if len(attrs) == 0 {
		return nil
	}

	if pid, ok := attrs["parent_issue_id"]; ok && present(pid) {
		s := rubyToS(pid)
		valid := false
		if m := parentIDRe.FindStringSubmatch(s); m != nil {
			cur := ""
			if iss.ParentID != nil {
				cur = strconv.FormatInt(*iss.ParentID, 10)
			}
			if m[1] == cur {
				valid = true
			} else {
				id, _ := strconv.ParseInt(m[1], 10, 64)
				if p, err := e.Find(ctx, id); err != nil {
					return err
				} else if p != nil {
					if valid, err = e.Visible(ctx, p, u); err != nil {
						return err
					}
				}
			}
		}
		if !valid {
			iss.invalidParentIssueID = s
			delete(attrs, "parent_issue_id")
		}
	}

	if cfv, ok := attrs["custom_field_values"].(map[string]any); ok && len(cfv) > 0 {
		ed, err := e.EditableCustomFieldValues(ctx, iss, u)
		if err != nil {
			return err
		}
		filtered := map[string]any{}
		for k, v := range cfv {
			if slices.ContainsFunc(ed, func(x *CustomFieldValue) bool { return strconv.FormatInt(x.Field.ID, 10) == k }) {
				filtered[k] = v
			}
		}
		attrs["custom_field_values"] = filtered
	}
	if cfs, ok := attrs["custom_fields"].([]map[string]any); ok && len(cfs) > 0 {
		ed, err := e.EditableCustomFieldValues(ctx, iss, u)
		if err != nil {
			return err
		}
		var filtered []map[string]any
		for _, c := range cfs {
			if slices.ContainsFunc(ed, func(x *CustomFieldValue) bool { return strconv.FormatInt(x.Field.ID, 10) == rubyToS(c["id"]) }) {
				filtered = append(filtered, c)
			}
		}
		attrs["custom_fields"] = filtered
	}
	return e.AssignAttributes(ctx, iss, attrs)
}

// assignOrder は AssignAttributes で属性を代入する順 (Ruby はハッシュの順。依存のない順に固定する)。
var assignOrder = []string{"project_id", "project", "tracker_id", "tracker", "status_id", "category_id", "assigned_to_id",
	"priority_id", "fixed_version_id", "subject", "description", "start_date", "due_date", "done_ratio", "estimated_hours",
	"is_private", "lock_version", "parent_issue_id", "custom_field_values", "custom_fields", "watcher_user_ids",
	"notes", "private_notes", "deleted_attachment_ids", "author_id"}

// AssignAttributes は assign_attributes (attributes=)。権限の確認は行わない。
// project / tracker を先に代入し、その他は assignOrder の順に代入する。
func (e *Env) AssignAttributes(ctx context.Context, iss *Issue, attrs Params) error {
	for _, k := range assignOrder {
		v, ok := attrs[k]
		if !ok {
			continue
		}
		if err := e.assignAttribute(ctx, iss, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (e *Env) assignAttribute(ctx context.Context, iss *Issue, k string, v any) error {
	switch k {
	case "project_id":
		id := castID(v)
		if id == nil {
			return e.SetProject(ctx, iss, nil, false)
		}
		return e.SetProjectID(ctx, iss, *id)
	case "project":
		p, _ := v.(*domain.Project)
		return e.SetProject(ctx, iss, p, false)
	case "tracker_id":
		id := castID(v)
		if id == nil {
			return e.SetTracker(ctx, iss, nil)
		}
		return e.SetTrackerID(ctx, iss, *id)
	case "tracker":
		t, _ := v.(*domain.Tracker)
		return e.SetTracker(ctx, iss, t)
	case "status_id":
		id := castID(v)
		if id == nil {
			iss.StatusID = 0
			return nil
		}
		return e.SetStatusID(ctx, iss, *id)
	case "category_id":
		iss.CategoryID = castID(v)
	case "assigned_to_id":
		iss.AssignedToID = castID(v)
	case "priority_id":
		iss.PriorityID = 0
		if id := castID(v); id != nil {
			iss.PriorityID = *id
		}
	case "fixed_version_id":
		iss.FixedVersionID = castID(v)
	case "author_id":
		iss.AuthorID = 0
		if id := castID(v); id != nil {
			iss.AuthorID = *id
		}
	case "subject":
		iss.Subject = rubyToS(v)
	case "description":
		if v == nil {
			iss.SetDescription(nil)
		} else {
			s := rubyToS(v)
			iss.SetDescription(&s)
		}
	case "start_date":
		switch x := v.(type) {
		case time.Time:
			iss.SetStartDate(&x)
		case *time.Time:
			iss.SetStartDate(x)
		case nil:
			iss.SetStartDate(nil)
		default:
			iss.SetStartDateString(rubyToS(v))
		}
	case "due_date":
		switch x := v.(type) {
		case time.Time:
			iss.SetDueDate(&x)
		case *time.Time:
			iss.SetDueDate(x)
		case nil:
			iss.SetDueDate(nil)
		default:
			iss.SetDueDateString(rubyToS(v))
		}
	case "done_ratio":
		iss.DoneRatio = 0
		if id := castID(v); id != nil {
			iss.DoneRatio = int(*id)
		}
	case "estimated_hours":
		switch x := v.(type) {
		case float64:
			iss.SetEstimatedHours(&x)
		case int:
			f := float64(x)
			iss.SetEstimatedHours(&f)
		case nil:
			iss.SetEstimatedHours(nil)
		default:
			iss.SetEstimatedHoursString(rubyToS(v))
		}
	case "is_private":
		iss.IsPrivate = castBool(v)
	case "lock_version":
		if id := castID(v); id != nil {
			iss.LockVersion = int(*id)
		}
	case "parent_issue_id":
		switch x := v.(type) {
		case *Issue:
			iss.SetParentIssue(x)
			return nil
		}
		return e.SetParentIssueID(ctx, iss, rubyToS(v))
	case "custom_field_values":
		m, _ := v.(map[string]any)
		if m == nil {
			if ms, ok := v.(map[string]string); ok {
				m = map[string]any{}
				for k, s := range ms {
					m[k] = s
				}
			}
		}
		return e.SetCustomFieldValues(ctx, iss, m)
	case "custom_fields":
		l, _ := v.([]map[string]any)
		return e.SetCustomFields(ctx, iss, l)
	case "watcher_user_ids":
		var ids []int64
		for _, s := range toStrings(v) {
			if id := castID(s); id != nil {
				ids = append(ids, *id)
			}
		}
		iss.SetWatcherUserIDs(ids)
	case "notes":
		if iss.currentJournal != nil {
			iss.currentJournal.Notes = rubyToS(v)
		}
	case "private_notes":
		if iss.currentJournal != nil {
			iss.currentJournal.PrivateNotes = castBool(v)
		}
	case "deleted_attachment_ids":
		var ids []int64
		for _, s := range toStrings(v) {
			ids = append(ids, rubyToI(s))
		}
		iss.deletedAttachmentIDs = ids
	}
	return nil
}

// present は Object#present?。
func present(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case []string:
		return len(x) > 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case bool:
		return x
	}
	return true
}

var numericPrefixRe = regexp.MustCompile(`^\s*[+-]?\d`)

// castID は Rails の整数型キャスト ("" / 数字で始まらない文字列は nil)。
func castID(v any) *int64 {
	switch x := v.(type) {
	case nil:
		return nil
	case int:
		n := int64(x)
		return &n
	case int64:
		return &x
	case *int64:
		return x
	case float64:
		n := int64(x)
		return &n
	case bool:
		if x {
			n := int64(1)
			return &n
		}
		n := int64(0)
		return &n
	}
	s := rubyToS(v)
	if strings.TrimSpace(s) == "" || !numericPrefixRe.MatchString(s) {
		return nil
	}
	n := rubyToI(s)
	return &n
}

// castBool は Rails の真偽値キャスト。
func castBool(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case int:
		return x != 0
	case int64:
		return x != 0
	}
	switch rubyToS(v) {
	case "", "0", "f", "F", "false", "FALSE", "off", "OFF":
		return false
	}
	return true
}

func toStrings(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = rubyToS(e)
		}
		return out
	case []int64:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = strconv.FormatInt(e, 10)
		}
		return out
	case nil:
		return nil
	}
	return []string{rubyToS(v)}
}
