package testfixtures

import (
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

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

func loadUserPreferences(c *loadCtx, rows []row) error {
	for _, r := range rows {
		uid := r.int("user_id", 0)
		others := map[string]any{}
		if s := r.str("others"); s != "" {
			if err := yaml.Unmarshal([]byte(s), &others); err != nil {
				return fmt.Errorf("%s: others: %w", r.label, err)
			}
		}
		get := func(k string) (any, bool) {
			v, ok := others[":"+k]
			if !ok {
				v, ok = others[k]
			}
			return v, ok && v != nil
		}
		warn := true
		if v, ok := get("warn_on_leaving_unsaved"); ok {
			warn = fmt.Sprint(v) != "0"
		}
		var font any
		if v, ok := get("textarea_font"); ok && fmt.Sprint(v) != "" {
			font = fmt.Sprint(v)
		}
		recent := int64(3)
		if v, ok := get("recently_used_projects"); ok {
			if n, err := strconv.ParseInt(fmt.Sprint(v), 10, 64); err == nil {
				recent = n
			}
		}
		sorting := "asc"
		if v, ok := get("comments_sorting"); ok && fmt.Sprint(v) != "" {
			sorting = fmt.Sprint(v)
		}
		if err := c.exec(`INSERT INTO user_preferences (user_id, hide_mail, time_zone, comments_sorting, warn_on_leaving_unsaved, textarea_font, recently_used_projects)
VALUES (?, ?, ?, ?, ?, ?, ?)`, uid, r.bool("hide_mail", true), r.nstr("time_zone"), sorting, warn, font, recent); err != nil {
			return err
		}
		if v, ok := get("bookmarked_project_ids"); ok {
			for i, pid := range prefIDs(v) {
				if err := c.exec(`INSERT INTO user_project_bookmarks (user_id, project_id, position) VALUES (?, ?, ?)`, uid, pid, i); err != nil {
					return err
				}
			}
		}
		if v, ok := get("recently_used_project_ids"); ok {
			for i, pid := range prefIDs(v) {
				if err := c.exec(`INSERT INTO user_recent_projects (user_id, project_id, position) VALUES (?, ?, ?)`, uid, pid, i); err != nil {
					return err
				}
			}
		}
	}
	return nil
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
