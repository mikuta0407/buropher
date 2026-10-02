package testfixtures

import (
	"encoding/json"
	"fmt"
)

// cfOwnerKinds は custom_fields.type → owner_kind（type が CustomField の異常行は捨てる。インポータと同じ）。
var cfOwnerKinds = map[string]string{
	"IssueCustomField": "issue", "TimeEntryCustomField": "time_entry", "ProjectCustomField": "project",
	"VersionCustomField": "version", "DocumentCustomField": "document", "UserCustomField": "user",
	"GroupCustomField": "group", "TimeEntryActivityCustomField": "time_entry_activity",
	"IssuePriorityCustomField": "issue_priority", "DocumentCategoryCustomField": "document_category",
}

func loadCustomFields(c *loadCtx, rows []row) error {
	if c.customFields == nil {
		c.customFields = map[int64]bool{}
	}
	for _, r := range rows {
		owner, ok := cfOwnerKinds[r.str("type")]
		if !ok {
			continue
		}
		id := r.int("id", 0)
		var pv any
		if l, ok := r.lists["possible_values"]; ok {
			b, _ := json.Marshal(l)
			pv = string(b)
		} else if r.has("possible_values") {
			pv = "[]"
		}
		// description / regexp / default_value は nil と "" を区別する（Redmine の表示・API と同じ）
		str := func(k string) any {
			if v := r.cols[k]; v != nil {
				return *v
			}
			return nil
		}
		if err := c.exec(`INSERT INTO custom_fields (id, owner_kind, name, description, field_format, regexp, min_length, max_length,
  is_required, is_for_all, is_filter, searchable, default_value, editable, visible, multiple, position, possible_values, format_settings)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, owner, r.str("name"), str("description"), r.str("field_format"), str("regexp"), r.nint("min_length"), r.nint("max_length"),
			r.bool("is_required", false), r.bool("is_for_all", false), r.bool("is_filter", false), r.bool("searchable", false),
			str("default_value"), r.bool("editable", true), r.bool("visible", true), r.bool("multiple", false),
			r.int("position", 1), pv, "{}"); err != nil {
			return err
		}
		c.customFields[id] = true
	}
	return nil
}

func loadCustomFieldLinks(table, col string) func(c *loadCtx, rows []row) error {
	return func(c *loadCtx, rows []row) error {
		for _, r := range rows {
			cf := r.int("custom_field_id", 0)
			if !c.customFields[cf] {
				continue
			}
			if err := c.exec(`INSERT INTO `+table+` (custom_field_id, `+col+`) VALUES (?, ?)`, cf, r.int(col, 0)); err != nil {
				return err
			}
		}
		return nil
	}
}

var customizedKinds = map[string]string{
	"Issue": "issue", "Project": "project", "TimeEntry": "time_entry", "Version": "version",
	"Document": "document", "Principal": "principal", "User": "principal", "Group": "principal",
	"Enumeration": "enumeration",
}

func loadCustomValues(c *loadCtx, rows []row) error {
	for _, r := range rows {
		cf := r.int("custom_field_id", 0)
		if !c.customFields[cf] {
			continue
		}
		kind, ok := customizedKinds[r.str("customized_type")]
		if !ok {
			return fmt.Errorf("%s: customized_type %q is not supported", r.label, r.str("customized_type"))
		}
		var val any
		if v := r.cols["value"]; v != nil {
			val = *v
		}
		if err := c.exec(`INSERT INTO custom_values (id, customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?, ?)`,
			r.int("id", 0), kind, r.int("customized_id", 0), cf, val); err != nil {
			return err
		}
	}
	return nil
}
