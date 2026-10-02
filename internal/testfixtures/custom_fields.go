package testfixtures

import (
	"encoding/json"
	"strings"
)

// custom_fields.type（STI）→ custom_fields.owner_kind（redmineimport と同じ対応）。
// 型が CustomField そのもの（フィクスチャの id 11）など行き先の無い行は捨てる。
var cfOwnerKinds = map[string]string{
	"IssueCustomField": "issue", "ProjectCustomField": "project", "TimeEntryCustomField": "time_entry",
	"VersionCustomField": "version", "DocumentCustomField": "document", "UserCustomField": "user",
	"GroupCustomField": "group", "IssuePriorityCustomField": "issue_priority",
	"TimeEntryActivityCustomField": "time_entry_activity", "DocumentCategoryCustomField": "document_category",
}

// custom_values.customized_type → customized_kind。
var cfCustomizedKinds = map[string]string{
	"Issue": "issue", "Project": "project", "TimeEntry": "time_entry", "Version": "version",
	"Document": "document", "Principal": "principal", "User": "principal", "Group": "principal",
	"Enumeration": "enumeration",
}

func loadCustomFields(c *loadCtx, rows []row) error {
	for _, r := range rows {
		owner, ok := cfOwnerKinds[r.str("type")]
		if !ok {
			continue
		}
		var pv any
		if v := r.str("possible_values"); strings.HasPrefix(v, "[") {
			var list []string
			if err := json.Unmarshal([]byte(v), &list); err != nil {
				return err
			}
			kept := []string{}
			for _, s := range list {
				if s != "" {
					kept = append(kept, s)
				}
			}
			b, _ := json.Marshal(kept)
			pv = string(b)
		}
		if err := c.exec(`INSERT INTO custom_fields (id, owner_kind, name, description, field_format, regexp, min_length, max_length,
  is_required, is_for_all, is_filter, searchable, default_value, editable, visible, multiple, position, possible_values, format_settings)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}')`,
			r.int("id", 0), owner, r.str("name"), r.nstr("description"), r.str("field_format"), r.nstr("regexp"),
			r.nint("min_length"), r.nint("max_length"), r.bool("is_required", false), r.bool("is_for_all", false),
			r.bool("is_filter", false), r.bool("searchable", false), r.nstr("default_value"), r.bool("editable", true),
			r.bool("visible", true), r.bool("multiple", false), r.int("position", 1), pv); err != nil {
			return err
		}
	}
	return nil
}

func loadCustomFieldsTrackers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id)
  SELECT id, ? FROM custom_fields WHERE id = ?`, r.int("tracker_id", 0), r.int("custom_field_id", 0)); err != nil {
			return err
		}
	}
	return nil
}

func loadCustomFieldsProjects(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO custom_fields_projects (custom_field_id, project_id)
  SELECT id, ? FROM custom_fields WHERE id = ?`, r.int("project_id", 0), r.int("custom_field_id", 0)); err != nil {
			return err
		}
	}
	return nil
}

// loadCustomValues は custom_values を投入する（bool の値は redmineimport と同じく "1" / "0" に正規化）。
func loadCustomValues(c *loadCtx, rows []row) error {
	for _, r := range rows {
		kind, ok := cfCustomizedKinds[r.str("customized_type")]
		if !ok {
			continue
		}
		var format string
		if err := c.tx.Get(c.ctx, &format, `SELECT COALESCE(MAX(field_format), '') FROM custom_fields WHERE id = ?`, r.int("custom_field_id", 0)); err != nil {
			return err
		}
		if format == "" {
			continue
		}
		val := r.nstr("value")
		if s, ok := val.(string); ok && format == "bool" {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "1", "t", "true":
				val = "1"
			case "0", "f", "false":
				val = "0"
			}
		}
		if err := c.exec(`INSERT INTO custom_values (id, customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?, ?)`,
			r.int("id", 0), kind, r.int("customized_id", 0), r.int("custom_field_id", 0), val); err != nil {
			return err
		}
	}
	return nil
}
