// Package defaultdata は Redmine::DefaultData::Loader（lib/redmine/default_data/loader.rb）の移植。
//
// DB には依存せず、投入すべきデータ一式（Plan）を組み立てるだけにする。
// 実際の INSERT は呼び出し側（buropher init）が Plan を元に行う。
package defaultdata

import "github.com/mikuta0407/buropher/internal/permission"

// Role は作成するロール。Builtin が 0 以外の行は既存の組込みロールの権限更新を表す。
type Role struct {
	Name             string
	Position         int
	Builtin          int // 0: 通常, 1: 非メンバー, 2: 匿名
	IssuesVisibility string
	UsersVisibility  string
	Permissions      []string
}

// Status はチケットステータス。
type Status struct {
	Name     string
	IsClosed bool
	Position int
}

// Tracker はトラッカー。DefaultStatus は Statuses のインデックス。
type Tracker struct {
	Name          string
	DefaultStatus int
	IsInRoadmap   bool
	Position      int
}

// Transition はワークフロー遷移。各値は Plan 内スライスのインデックス。
type Transition struct {
	Tracker, Role, OldStatus, NewStatus int
}

// Enumeration は優先度・文書カテゴリ・作業分類。
type Enumeration struct {
	Name      string
	Position  int
	IsDefault bool
}

// Filter はクエリのフィルタ条件。
type Filter struct {
	Field    string
	Operator string
	Values   []string
}

// Query は公開クエリ（Kind: issue / project / time_entry）。
type Query struct {
	Kind           string
	Name           string
	Filters        []Filter
	SortCriteria   [][2]string
	TotalableNames []string
	Visibility     int // Query::VISIBILITY_PUBLIC = 2
}

// Plan は投入データ一式。
type Plan struct {
	Roles              []Role // 先頭3つが manager, developer, reporter。続いて非メンバー・匿名の権限更新
	Statuses           []Status
	Trackers           []Tracker
	Transitions        []Transition // Role は Roles のインデックス（0..2）
	Priorities         []Enumeration
	DocumentCategories []Enumeration
	Activities         []Enumeration
	Queries            []Query
	// DefaultProjectsTrackers は Setting.default_projects_tracker_ids に入れる Trackers のインデックス。
	DefaultProjectsTrackers []int
}

const visibilityPublic = 2

// Build は Loader.load と同じ内容の Plan を作る。l は現在言語での l(key)。
// workflow=false なら遷移を作らない（options[:workflow] == false）。
func Build(l func(string) string, workflow bool) *Plan {
	p := &Plan{}

	// Role#setable_permissions（通常ロール: 公開権限以外すべて）
	var managerPerms []string
	for _, perm := range permission.All() {
		if !perm.Public {
			managerPerms = append(managerPerms, perm.Name)
		}
	}
	p.Roles = []Role{
		{Name: l("default_role_manager"), Position: 1, IssuesVisibility: "all", UsersVisibility: "all", Permissions: managerPerms},
		{Name: l("default_role_developer"), Position: 2, IssuesVisibility: "default", UsersVisibility: "members_of_visible_projects", Permissions: []string{
			"manage_versions", "manage_categories", "view_issues", "add_issues", "edit_issues",
			"view_private_notes", "set_notes_private", "manage_issue_relations", "manage_subtasks",
			"add_issue_notes", "save_queries", "view_gantt", "view_calendar", "log_time",
			"view_time_entries", "view_news", "comment_news", "view_documents", "view_wiki_pages",
			"view_wiki_edits", "edit_wiki_pages", "delete_wiki_pages", "view_messages", "add_messages",
			"edit_own_messages", "view_files", "manage_files", "browse_repository", "view_changesets",
			"commit_access", "manage_related_issues",
		}},
		{Name: l("default_role_reporter"), Position: 3, IssuesVisibility: "default", UsersVisibility: "members_of_visible_projects", Permissions: []string{
			"view_issues", "add_issues", "add_issue_notes", "save_queries", "view_gantt", "view_calendar",
			"log_time", "view_time_entries", "view_news", "comment_news", "view_documents",
			"view_wiki_pages", "view_wiki_edits", "view_messages", "add_messages", "edit_own_messages",
			"view_files", "browse_repository", "view_changesets",
		}},
		{Builtin: 1, Permissions: []string{
			"view_issues", "add_issues", "add_issue_notes", "save_queries", "view_gantt", "view_calendar",
			"view_time_entries", "view_news", "comment_news", "view_documents", "view_wiki_pages",
			"view_wiki_edits", "view_messages", "add_messages", "view_files", "browse_repository",
			"view_changesets",
		}},
		{Builtin: 2, Permissions: []string{
			"view_issues", "view_gantt", "view_calendar", "view_time_entries", "view_news",
			"view_documents", "view_wiki_pages", "view_wiki_edits", "view_messages", "view_files",
			"browse_repository", "view_changesets",
		}},
	}

	const (
		stNew = iota
		stInProgress
		stResolved
		stFeedback
		stClosed
		stRejected
	)
	p.Statuses = []Status{
		{l("default_issue_status_new"), false, 1},
		{l("default_issue_status_in_progress"), false, 2},
		{l("default_issue_status_resolved"), false, 3},
		{l("default_issue_status_feedback"), false, 4},
		{l("default_issue_status_closed"), true, 5},
		{l("default_issue_status_rejected"), true, 6},
	}
	p.Trackers = []Tracker{
		{l("default_tracker_bug"), stNew, false, 1},
		{l("default_tracker_feature"), stNew, true, 2},
		{l("default_tracker_support"), stNew, false, 3},
	}
	p.DefaultProjectsTrackers = []int{0, 1, 2}

	if workflow {
		const manager, developer, reporter = 0, 1, 2
		all := []int{stNew, stInProgress, stResolved, stFeedback, stClosed, stRejected}
		for t := range p.Trackers {
			for _, os := range all {
				for _, ns := range all {
					if os != ns {
						p.Transitions = append(p.Transitions, Transition{t, manager, os, ns})
					}
				}
			}
		}
		for t := range p.Trackers {
			for _, os := range []int{stNew, stInProgress, stResolved, stFeedback} {
				for _, ns := range []int{stInProgress, stResolved, stFeedback, stClosed} {
					if os != ns {
						p.Transitions = append(p.Transitions, Transition{t, developer, os, ns})
					}
				}
			}
		}
		for t := range p.Trackers {
			for _, os := range []int{stNew, stInProgress, stResolved, stFeedback} {
				p.Transitions = append(p.Transitions, Transition{t, reporter, os, stClosed})
			}
			p.Transitions = append(p.Transitions, Transition{t, reporter, stResolved, stFeedback})
		}
	}

	p.Priorities = []Enumeration{
		{l("default_priority_low"), 1, false},
		{l("default_priority_normal"), 2, true},
		{l("default_priority_high"), 3, false},
		{l("default_priority_urgent"), 4, false},
		{l("default_priority_immediate"), 5, false},
	}
	p.DocumentCategories = []Enumeration{
		{l("default_doc_category_user"), 1, false},
		{l("default_doc_category_tech"), 2, false},
	}
	p.Activities = []Enumeration{
		{l("default_activity_design"), 1, false},
		{l("default_activity_development"), 2, false},
	}

	openMine := func(field string) []Filter {
		return []Filter{
			{"status_id", "o", []string{""}},
			{field, "=", []string{"me"}},
			{"project.status", "=", []string{"1"}},
		}
	}
	p.Queries = []Query{
		{Kind: "issue", Name: l("label_assigned_to_me_issues"), Filters: openMine("assigned_to_id"),
			SortCriteria: [][2]string{{"priority", "desc"}, {"updated_on", "desc"}}, Visibility: visibilityPublic},
		{Kind: "issue", Name: l("label_reported_issues"), Filters: openMine("author_id"),
			SortCriteria: [][2]string{{"updated_on", "desc"}}, Visibility: visibilityPublic},
		{Kind: "issue", Name: l("label_updated_issues"), Filters: openMine("updated_by"),
			SortCriteria: [][2]string{{"updated_on", "desc"}}, Visibility: visibilityPublic},
		{Kind: "issue", Name: l("label_watched_issues"), Filters: openMine("watcher_id"),
			SortCriteria: [][2]string{{"updated_on", "desc"}}, Visibility: visibilityPublic},
		{Kind: "project", Name: l("label_my_projects"), Filters: []Filter{
			{"status", "=", []string{"1"}}, {"id", "=", []string{"mine"}}}, Visibility: visibilityPublic},
		{Kind: "project", Name: l("label_my_bookmarks"), Filters: []Filter{
			{"status", "=", []string{"1"}}, {"id", "=", []string{"bookmarks"}}}, Visibility: visibilityPublic},
		{Kind: "time_entry", Name: l("label_spent_time"), Filters: []Filter{
			{"spent_on", "*", []string{""}}, {"user_id", "=", []string{"me"}}},
			SortCriteria: [][2]string{{"spent_on", "desc"}}, TotalableNames: []string{"hours"}, Visibility: visibilityPublic},
	}
	return p
}
