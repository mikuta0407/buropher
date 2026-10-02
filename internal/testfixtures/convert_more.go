package testfixtures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// カスタムフィールド・カスタム値・ジャーナル・工数・保存クエリ・関連・添付・個人設定の変換。

// customFieldOwnerKinds は custom_fields.type → owner_kind。
// type が CustomField (基底クラス) の異常行は行き先が無いので捨てる。
var customFieldOwnerKinds = map[string]string{
	"IssueCustomField":             "issue",
	"ProjectCustomField":           "project",
	"TimeEntryCustomField":         "time_entry",
	"VersionCustomField":           "version",
	"DocumentCustomField":          "document",
	"UserCustomField":              "user",
	"GroupCustomField":             "group",
	"IssuePriorityCustomField":     "issue_priority",
	"TimeEntryActivityCustomField": "time_entry_activity",
	"DocumentCategoryCustomField":  "document_category",
}

func loadCustomFields(c *loadCtx, rows []row) error {
	for _, r := range rows {
		kind, ok := customFieldOwnerKinds[r.str("type")]
		if !ok {
			continue
		}
		id := r.int("id", 0)
		c.customFields[id] = true
		var pv any
		if l, ok := r.lists["possible_values"]; ok {
			b, _ := json.Marshal(l)
			pv = string(b)
		}
		fs := "{}"
		if s := r.str("format_store"); s != "" {
			m, err := rubyYAMLToJSON(s)
			if err != nil {
				return fmt.Errorf("%s: format_store: %w", r.label, err)
			}
			fs = m
		}
		if err := c.exec(`INSERT INTO custom_fields (id, owner_kind, name, description, field_format, regexp, min_length, max_length,
  is_required, is_for_all, is_filter, searchable, default_value, editable, visible, multiple, position, possible_values, format_settings)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, kind, r.str("name"), r.strOrNil("description"), r.str("field_format"), r.strOrNil("regexp"),
			r.nint("min_length"), r.nint("max_length"), r.bool("is_required", false), r.bool("is_for_all", false),
			r.bool("is_filter", false), r.bool("searchable", false), r.strOrNil("default_value"), r.bool("editable", true),
			r.bool("visible", true), r.bool("multiple", false), r.int("position", 1), pv, fs); err != nil {
			return err
		}
	}
	return nil
}

func loadCustomFieldsProjects(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if !c.customFields[r.int("custom_field_id", 0)] {
			continue
		}
		if err := c.exec(`INSERT INTO custom_fields_projects (custom_field_id, project_id) VALUES (?, ?)`,
			r.int("custom_field_id", 0), r.int("project_id", 0)); err != nil {
			return err
		}
	}
	return nil
}

func loadCustomFieldsTrackers(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if !c.customFields[r.int("custom_field_id", 0)] {
			continue
		}
		if err := c.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`,
			r.int("custom_field_id", 0), r.int("tracker_id", 0)); err != nil {
			return err
		}
	}
	return nil
}

// customizedKinds は custom_values.customized_type → customized_kind。
var customizedKinds = map[string]string{
	"Issue": "issue", "Project": "project", "TimeEntry": "time_entry", "Version": "version",
	"Document": "document", "Principal": "principal", "User": "principal", "Group": "principal",
	"Enumeration": "enumeration",
}

func loadCustomValues(c *loadCtx, rows []row) error {
	for _, r := range rows {
		kind, ok := customizedKinds[r.str("customized_type")]
		if !ok {
			return fmt.Errorf("%s: unknown customized_type %q", r.label, r.str("customized_type"))
		}
		if !c.customFields[r.int("custom_field_id", 0)] {
			continue
		}
		// '' は NULL に変換する (付録 A)
		if err := c.exec(`INSERT INTO custom_values (id, customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?, ?)`,
			r.int("id", 0), kind, r.int("customized_id", 0), r.int("custom_field_id", 0), r.nstr("value")); err != nil {
			return err
		}
	}
	return nil
}

func loadJournals(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if t := r.str("journalized_type"); t != "Issue" {
			return fmt.Errorf("%s: journalized_type %q is not supported", r.label, t)
		}
		if err := c.exec(`INSERT INTO issue_journals (id, issue_id, user_id, notes, private_notes, created_at, updated_at, updated_by_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("journalized_id", 0), r.int("user_id", 0), r.nstr("notes"), r.bool("private_notes", false),
			ts(c, r, "created_on"), nts(r, "updated_on"), r.nint("updated_by_id")); err != nil {
			return err
		}
	}
	return nil
}

func loadJournalDetails(c *loadCtx, rows []row) error {
	for _, r := range rows {
		var cfID any
		if r.str("property") == "cf" {
			n, err := strconv.ParseInt(r.str("prop_key"), 10, 64)
			if err != nil {
				return fmt.Errorf("%s: cf prop_key %q", r.label, r.str("prop_key"))
			}
			cfID = n
		}
		// 値は原文のまま保持する (付録 A)
		var old, val any
		if r.has("old_value") {
			old = r.str("old_value")
		}
		if r.has("value") {
			val = r.str("value")
		}
		if err := c.exec(`INSERT INTO issue_journal_details (id, journal_id, property, prop_key, custom_field_id, old_value, value)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("journal_id", 0), r.str("property"), r.str("prop_key"), cfID, old, val); err != nil {
			return err
		}
	}
	return nil
}

func loadTimeEntries(c *loadCtx, rows []row) error {
	for _, r := range rows {
		author := r.int("author_id", 0)
		if author == 0 {
			author = r.int("user_id", 0)
		}
		if err := c.exec(`INSERT INTO time_entries (id, project_id, user_id, author_id, issue_id, hours, comments, activity_id, spent_on,
  tyear, tmonth, tweek, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), r.int("user_id", 0), author, r.nint("issue_id"), r.float("hours"),
			r.nstr("comments"), r.int("activity_id", 0), r.date("spent_on"), r.int("tyear", 0), r.int("tmonth", 0),
			r.int("tweek", 0), ts(c, r, "created_on"), ts(c, r, "updated_on")); err != nil {
			return err
		}
	}
	return nil
}

var queryKinds = map[string]string{
	"IssueQuery": "issue", "ProjectQuery": "project", "ProjectAdminQuery": "project_admin",
	"TimeEntryQuery": "time_entry", "UserQuery": "user",
}

func loadQueries(c *loadCtx, rows []row) error {
	for _, r := range rows {
		kind, ok := queryKinds[r.str("type")]
		if !ok {
			return fmt.Errorf("%s: unknown query type %q", r.label, r.str("type"))
		}
		filters := "{}"
		if s := r.str("filters"); s != "" {
			j, err := rubyYAMLToJSON(s)
			if err != nil {
				return fmt.Errorf("%s: filters: %w", r.label, err)
			}
			filters = j
		}
		var columns, sortCrit, totalable, display any
		if l, ok := r.lists["column_names"]; ok {
			b, _ := json.Marshal(l)
			columns = string(b)
		} else if s := r.str("column_names"); s != "" {
			j, err := rubyYAMLToJSON(s)
			if err != nil {
				return fmt.Errorf("%s: column_names: %w", r.label, err)
			}
			columns = j
		}
		if s := r.str("sort_criteria"); s != "" {
			j, err := rubyYAMLToJSON(s)
			if err != nil {
				return fmt.Errorf("%s: sort_criteria: %w", r.label, err)
			}
			sortCrit = j
		}
		options := "{}"
		if s := r.str("options"); s != "" {
			var m yaml.Node
			if err := yaml.Unmarshal([]byte(s), &m); err != nil {
				return fmt.Errorf("%s: options: %w", r.label, err)
			}
			rest := &yaml.Node{Kind: yaml.MappingNode}
			if len(m.Content) > 0 && m.Content[0].Kind == yaml.MappingNode {
				top := m.Content[0]
				for i := 0; i+1 < len(top.Content); i += 2 {
					k := strings.TrimPrefix(top.Content[i].Value, ":")
					v := top.Content[i+1]
					switch k {
					case "display_type":
						display = strings.TrimPrefix(v.Value, ":")
					case "totalable_names":
						b, err := nodeToJSON(v)
						if err != nil {
							return err
						}
						totalable = string(b)
					default:
						rest.Content = append(rest.Content, top.Content[i], v)
					}
				}
			}
			b, err := nodeToJSON(rest)
			if err != nil {
				return err
			}
			options = string(b)
		}
		var user any
		if u := r.int("user_id", 0); u != 0 {
			user = u
		}
		if err := c.exec(`INSERT INTO queries (id, kind, project_id, user_id, name, description, visibility, filters, column_names,
  sort_criteria, group_by, totalable_names, display_type, options) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), kind, r.nint("project_id"), user, r.str("name"), r.nstr("description"), r.int("visibility", 0),
			filters, columns, sortCrit, r.nstr("group_by"), totalable, display, options); err != nil {
			return err
		}
	}
	return nil
}

func loadIssueRelations(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type, delay) VALUES (?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("issue_from_id", 0), r.int("issue_to_id", 0), r.str("relation_type"), r.nint("delay")); err != nil {
			return err
		}
	}
	return nil
}

var containerKinds = map[string]string{
	"Issue": "issue", "Project": "project", "Version": "version", "WikiPage": "wiki_page", "Message": "message",
	"News": "news", "Document": "document", "CustomValue": "custom_value",
}

func loadUserPreferences(c *loadCtx, rows []row) error {
	for _, r := range rows {
		uid := r.int("user_id", 0)
		var others map[string]any
		if s := r.str("others"); s != "" {
			j, err := rubyYAMLToJSON(s)
			if err != nil {
				return fmt.Errorf("%s: others: %w", r.label, err)
			}
			if err := json.Unmarshal([]byte(j), &others); err != nil {
				return err
			}
		}
		var layout, font any
		extra := map[string]any{}
		var bookmarks, recentIDs []int64
		warn, sorting, recent := true, "asc", int64(3)
		for k, v := range others {
			switch k {
			case "my_page_layout":
				b, _ := json.Marshal(v)
				layout = string(b)
			case "bookmarked_project_ids":
				bookmarks = prefIDs(v)
			case "recently_used_project_ids":
				recentIDs = prefIDs(v)
			case "warn_on_leaving_unsaved":
				warn = fmt.Sprint(v) != "0"
			case "textarea_font":
				if s := fmt.Sprint(v); v != nil && s != "" {
					font = s
				}
			case "recently_used_projects":
				if n, err := strconv.ParseInt(fmt.Sprint(v), 10, 64); err == nil {
					recent = n
				}
			case "comments_sorting":
				if s := fmt.Sprint(v); v != nil && s != "" {
					sorting = s
				}
			case "no_self_notified":
				if err := c.exec(`UPDATE user_notification_settings SET no_self_notified = ? WHERE user_id = ?`, v == true, uid); err != nil {
					return err
				}
			default:
				extra[k] = v
			}
		}
		eb, _ := json.Marshal(extra)
		if err := c.exec(`INSERT INTO user_preferences (user_id, hide_mail, time_zone, comments_sorting, warn_on_leaving_unsaved, textarea_font, recently_used_projects, my_page_layout, extra)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, r.bool("hide_mail", true), r.nstr("time_zone"), sorting, warn, font, recent, layout, string(eb)); err != nil {
			return err
		}
		for i, pid := range bookmarks {
			if err := c.exec(`INSERT INTO user_project_bookmarks (user_id, project_id, position) VALUES (?, ?, ?)`, uid, pid, i); err != nil {
				return err
			}
		}
		for i, pid := range recentIDs {
			if err := c.exec(`INSERT INTO user_recent_projects (user_id, project_id, position) VALUES (?, ?, ?)`, uid, pid, i); err != nil {
				return err
			}
		}
	}
	return nil
}

// rubyYAMLToJSON は Ruby が serialize した YAML (シンボルキー ":values" 等) を JSON 文字列にする。
// マッピングのキー順は保持し、シンボルの先頭 ':' は取り除く。
func rubyYAMLToJSON(s string) (string, error) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		return "", err
	}
	if len(n.Content) == 0 {
		return "null", nil
	}
	b, err := nodeToJSON(n.Content[0])
	return string(b), err
}

func nodeToJSON(n *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeNodeJSON(&buf, n); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeNodeJSON(buf *bytes.Buffer, n *yaml.Node) error {
	switch n.Kind {
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				buf.WriteByte(',')
			}
			k, _ := json.Marshal(strings.TrimPrefix(n.Content[i].Value, ":"))
			buf.Write(k)
			buf.WriteByte(':')
			if err := writeNodeJSON(buf, n.Content[i+1]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, c := range n.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeNodeJSON(buf, c); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			buf.WriteString("null")
		case "!!bool":
			if strings.EqualFold(n.Value, "true") {
				buf.WriteString("true")
			} else {
				buf.WriteString("false")
			}
		case "!!int", "!!float":
			buf.WriteString(n.Value)
		default:
			v, _ := json.Marshal(strings.TrimPrefix(n.Value, ":"))
			buf.Write(v)
		}
	case yaml.AliasNode:
		return writeNodeJSON(buf, n.Alias)
	default:
		return fmt.Errorf("unsupported yaml node kind %d", n.Kind)
	}
	return nil
}

func loadNews(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO news (id, project_id, title, summary, description, author_id, comments_count, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), r.str("title"), r.nstr("summary"), r.nstr("description"),
			r.int("author_id", 0), r.int("comments_count", 0), ts(c, r, "created_on")); err != nil {
			return err
		}
	}
	return nil
}

// prefIDs は "1,5" 形式の id 列。
func prefIDs(v any) []int64 {
	var out []int64
	for _, s := range strings.Split(fmt.Sprint(v), ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func loadWikis(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO wikis (id, project_id, start_page) VALUES (?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), r.str("start_page")); err != nil {
			return err
		}
	}
	return nil
}

func loadBoards(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO boards (id, project_id, parent_id, name, description, position, topics_count, messages_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), r.nint("parent_id"), r.str("name"), r.str("description"),
			r.int("position", 1), r.int("topics_count", 0), r.int("messages_count", 0)); err != nil {
			return err
		}
	}
	return nil
}

func loadRepositories(c *loadCtx, rows []row) error {
	for _, r := range rows {
		scm := strings.ToLower(strings.TrimPrefix(r.str("type"), "Repository::"))
		if err := c.exec(`INSERT INTO repositories (id, project_id, scm, url, root_url, login, password, path_encoding, log_encoding, identifier, is_default, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), scm, r.str("url"), r.nstr("root_url"), r.nstr("login"), r.nstr("password"),
			r.nstr("path_encoding"), r.nstr("log_encoding"), r.nstr("identifier"), r.bool("is_default", false),
			ts(c, r, "created_on")); err != nil {
			return err
		}
	}
	return nil
}
