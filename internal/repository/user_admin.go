package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはユーザー管理（UsersController / EmailAddressesController）の読み書き:
// ユーザーの一覧（UserQuery の検索条件）、作成・更新、削除（User#remove_references_before_destroy）、
// メールアドレス、個人設定、通知設定、認証方式。

// ---------------------------------------------------------------- 一覧（UserQuery）

// UserFilter はユーザー一覧の検索条件（UserQuery の filters のうち対応しているもの）。
// 各条件は Redmine の演算子（"=", "!", "*", "!*", "~", "!~", "^", "$" ...）と値で指定する。
type UserFilter struct {
	Conditions []UserCondition
}

// UserCondition は 1 つのフィルタ（field operator values）。
type UserCondition struct {
	Field    string // status / auth_source_id / is_member_of_group / admin / name / login / firstname / lastname / mail
	Operator string
	Values   []string
}

// userFilterSQL は UserFilter の WHERE 断片と引数を返す（principals p / user_accounts ua を参照）。
func userFilterSQL(d db.Dialect, f UserFilter) (string, []any, error) {
	where := []string{`p.kind = 'user'`}
	var args []any
	for _, c := range f.Conditions {
		s, a, err := userConditionSQL(d, c)
		if err != nil {
			return "", nil, err
		}
		if s != "" {
			where = append(where, s)
			args = append(args, a...)
		}
	}
	return strings.Join(where, " AND "), args, nil
}

// listCondition は "=" / "!" / "*" / "!*" の汎用条件（col IN (...) / NOT IN / IS NOT NULL / IS NULL）。
func listCondition(col, op string, values []any) (string, []any, error) {
	switch op {
	case "*":
		return col + " IS NOT NULL", nil, nil
	case "!*":
		return col + " IS NULL", nil, nil
	case "=", "!":
		if len(values) == 0 {
			if op == "=" {
				return "1=0", nil, nil
			}
			return "1=1", nil, nil
		}
		s, a, err := db.In(col+" IN (?)", values)
		if err != nil {
			return "", nil, err
		}
		if op == "!" {
			s = "(" + col + " IS NULL OR " + col + " NOT IN " + strings.TrimPrefix(s, col+" IN ") + ")"
		}
		return s, a, nil
	}
	return "", nil, nil
}

func anyStrings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func anyInts(ss []string) []any {
	var out []any
	for _, s := range ss {
		var n int64
		if _, err := fmt.Sscan(strings.TrimSpace(s), &n); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// likeEscape は LIKE のワイルドカードをエスケープする（Redmine の sanitize_sql_like）。
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// stringCondition は Query#sql_for_field の文字列演算子（~ !~ ^ $ = ! *~ * !*）。大文字小文字は区別しない。
func stringCondition(col, op string, values []string) (string, []any) {
	v := ""
	if len(values) > 0 {
		v = values[0]
	}
	lower := "LOWER(" + col + ")"
	switch op {
	case "~", "!~":
		// 空白区切りの各語を AND で含む（Redmine の sql_contains）
		words := strings.Fields(v)
		if len(words) == 0 {
			words = []string{v}
		}
		var parts []string
		var args []any
		for _, w := range words {
			parts = append(parts, lower+` LIKE ? ESCAPE '\'`)
			args = append(args, "%"+likeEscape(strings.ToLower(w))+"%")
		}
		s := "(" + strings.Join(parts, " AND ") + ")"
		if op == "!~" {
			s = "(" + col + " IS NULL OR NOT " + s + ")"
		}
		return s, args
	case "*~":
		words := strings.Fields(v)
		var parts []string
		var args []any
		for _, w := range words {
			parts = append(parts, lower+` LIKE ? ESCAPE '\'`)
			args = append(args, "%"+likeEscape(strings.ToLower(w))+"%")
		}
		if len(parts) == 0 {
			return "1=0", nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", args
	case "^":
		return lower + ` LIKE ? ESCAPE '\'`, []any{likeEscape(strings.ToLower(v)) + "%"}
	case "$":
		return lower + ` LIKE ? ESCAPE '\'`, []any{"%" + likeEscape(strings.ToLower(v))}
	case "=":
		return lower + " = ?", []any{strings.ToLower(v)}
	case "!":
		return "(" + col + " IS NULL OR " + lower + " <> ?)", []any{strings.ToLower(v)}
	case "*":
		return "(" + col + " IS NOT NULL AND " + col + " <> '')", nil
	case "!*":
		return "(" + col + " IS NULL OR " + col + " = '')", nil
	}
	return "", nil
}

func userConditionSQL(d db.Dialect, c UserCondition) (string, []any, error) {
	switch c.Field {
	case "status":
		return listCondition("p.status", c.Operator, anyInts(c.Values))
	case "auth_source_id":
		return listCondition("ua.auth_source_id", c.Operator, anyInts(c.Values))
	case "admin":
		if len(c.Values) == 0 {
			return "", nil, nil
		}
		trueValue := "0"
		if c.Operator == "=" {
			trueValue = "1"
		}
		if c.Values[0] == trueValue {
			return "ua.admin = TRUE", nil, nil
		}
		return "ua.admin = FALSE", nil, nil
	case "is_member_of_group":
		e := "EXISTS"
		if strings.HasPrefix(c.Operator, "!") {
			e = "NOT EXISTS"
		}
		if c.Operator == "*" || c.Operator == "!*" {
			return "(" + e + " (SELECT 1 FROM group_users gu JOIN principals g ON g.id = gu.group_id WHERE gu.user_id = p.id AND g.kind = 'group'))", nil, nil
		}
		ids := anyInts(c.Values)
		if len(ids) == 0 {
			ids = []any{int64(0)}
		}
		s, a, err := db.In("(SELECT 1 FROM group_users gu WHERE gu.user_id = p.id AND gu.group_id IN (?))", ids)
		if err != nil {
			return "", nil, err
		}
		return "(" + e + " " + s + ")", a, nil
	case "login", "firstname", "lastname":
		col := "ua.login"
		if c.Field != "login" {
			col = "p." + c.Field
		}
		s, a := stringCondition(col, c.Operator, c.Values)
		return s, a, nil
	case "mail":
		op := c.Operator
		match := true
		if op == "!*" {
			match, op = false, "*"
		}
		s, a := stringCondition("e.address", op, c.Values)
		e := "EXISTS"
		if !match {
			e = "NOT EXISTS"
		}
		return e + " (SELECT 1 FROM email_addresses e WHERE e.user_id = p.id AND " + s + ")", a, nil
	case "name":
		switch c.Operator {
		case "*":
			return "1=1", nil, nil
		case "!*":
			return "1=0", nil, nil
		}
		match := !strings.HasPrefix(c.Operator, "!")
		matching := strings.TrimPrefix(c.Operator, "!")
		var parts []string
		var args []any
		for _, col := range []string{"ua.login", "p.firstname", "p.lastname"} {
			s, a := stringCondition(col, c.Operator, c.Values)
			parts = append(parts, "("+s+")")
			args = append(args, a...)
		}
		s, a := stringCondition("e.address", matching, c.Values)
		e := "EXISTS"
		if !match {
			e = "NOT EXISTS"
		}
		parts = append(parts, "("+e+" (SELECT 1 FROM email_addresses e WHERE e.user_id = p.id AND "+s+"))")
		args = append(args, a...)
		op := " OR "
		if !match {
			op = " AND "
		}
		return "(" + strings.Join(parts, op) + ")", args, nil
	}
	return "", nil, nil
}

// UserSortColumns は UserQuery の並べ替え可能な列と SQL 式。
var UserSortColumns = map[string]string{
	"login":             "ua.login",
	"firstname":         "p.firstname",
	"lastname":          "p.lastname",
	"mail":              "dm.address",
	"admin":             "ua.admin",
	"created_on":        "p.created_at",
	"updated_on":        "p.updated_at",
	"last_login_on":     "ua.last_login_at",
	"passwd_changed_on": "ua.password_changed_at",
	"status":            "p.status",
	"auth_source":       "aus.name",
	"twofa_scheme":      "ua.twofa_scheme",
}

// SortCriterion は並べ替えの 1 要素。
type SortCriterion struct {
	Column string
	Desc   bool
}

const userListFrom = ` FROM principals p JOIN user_accounts ua ON ua.principal_id = p.id
LEFT JOIN email_addresses dm ON dm.user_id = p.id AND dm.is_default = TRUE
LEFT JOIN auth_sources aus ON aus.id = ua.auth_source_id`

// CountUsers は条件に一致するユーザー数（UserQuery#results_scope.count）。
func CountUsers(ctx context.Context, q db.Queryer, f UserFilter) (int, error) {
	where, args, err := userFilterSQL(q.Dialect(), f)
	if err != nil {
		return 0, err
	}
	var n int
	err = q.Get(ctx, &n, `SELECT COUNT(*)`+userListFrom+` WHERE `+where, args...)
	return n, err
}

// SearchUsers は条件に一致するユーザーを sort の順で返す（limit < 0 なら全件）。
// NULL は MySQL / SQLite と同じく昇順で先頭に並べる。
func SearchUsers(ctx context.Context, q db.Queryer, f UserFilter, sort []SortCriterion, limit, offset int) ([]*domain.User, error) {
	where, args, err := userFilterSQL(q.Dialect(), f)
	if err != nil {
		return nil, err
	}
	var order []string
	for _, s := range sort {
		col, ok := UserSortColumns[s.Column]
		if !ok {
			continue
		}
		if s.Desc {
			order = append(order, "("+col+" IS NULL) ASC", col+" DESC")
		} else {
			order = append(order, "("+col+" IS NULL) DESC", col+" ASC")
		}
	}
	order = append(order, "p.id ASC")
	query := userSelect[:strings.Index(userSelect, "\nFROM")] + userListFrom + ` WHERE ` + where + ` ORDER BY ` + strings.Join(order, ", ")
	if limit >= 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// LoggedUsersByIDs は User.logged.where(id: ids).where.not(id: exceptID)（id 順）。
func LoggedUsersByIDs(ctx context.Context, q db.Queryer, ids []int64, exceptID int64) ([]*domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(userSelect+` WHERE p.id IN (?) AND p.kind = 'user' AND p.status <> 0 AND p.id <> ? ORDER BY p.id`, ids, exceptID)
	if err != nil {
		return nil, err
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// UsersWhereIDs は User.where(id: ids)（匿名ユーザーを含む。id 順）。
func UsersWhereIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(userSelect+` WHERE p.id IN (?) AND p.kind IN ('user', 'anonymous_user') ORDER BY p.id`, ids)
	if err != nil {
		return nil, err
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// GetLoggedUser は User.logged.find(id)（匿名ユーザーは ErrNotFound）。
func GetLoggedUser(ctx context.Context, q db.Queryer, id int64) (*domain.User, error) {
	var r userRow
	if err := q.Get(ctx, &r, userSelect+` WHERE p.id = ? AND p.kind = 'user' AND p.status <> 0`, id); err != nil {
		return nil, notFound(err)
	}
	return r.user(), nil
}

// SetUsersStatus は users.update_all(status: status)（updated_on は変えない）。
func SetUsersStatus(ctx context.Context, q db.Queryer, ids []int64, status int) error {
	if len(ids) == 0 {
		return nil
	}
	query, args, err := db.In(`UPDATE principals SET status = ? WHERE id IN (?)`, status, ids)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, query, args...)
	return err
}

// ActiveAdminExistsExcept は User.active.admin.where("id <> ?", id).exists?。
func ActiveAdminExistsExcept(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM principals p JOIN user_accounts ua ON ua.principal_id = p.id
WHERE p.kind = 'user' AND p.status = 1 AND ua.admin = TRUE AND p.id <> ?`, id)
	return n > 0, err
}

// ---------------------------------------------------------------- 作成・更新

// LoginTaken はログイン名が（大文字小文字を無視して）他のユーザーに使われていれば true。
func LoginTaken(ctx context.Context, q db.Queryer, login string, exceptID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM user_accounts WHERE LOWER(login) = ? AND principal_id <> ?`, strings.ToLower(login), exceptID)
	return n > 0, err
}

// EmailTaken はメールアドレスが（大文字小文字を無視して）他の行に使われていれば true。exceptID は email_addresses.id。
func EmailTaken(ctx context.Context, q db.Queryer, address string, exceptID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM email_addresses WHERE LOWER(address) = ? AND id <> ?`, strings.ToLower(address), exceptID)
	return n > 0, err
}

// InsertUser はユーザー（principals + user_accounts + 通知設定）を作成し id を返す。
// 既定のメールアドレスは mail が空でなければ作成する。
func InsertUser(ctx context.Context, q db.Queryer, u *domain.User, mail string, n *domain.UserNotification, now time.Time) (int64, error) {
	t := db.NewTime(now)
	id, err := q.InsertReturningID(ctx, `INSERT INTO principals (kind, status, firstname, lastname, created_at, updated_at) VALUES ('user', ?, ?, ?, ?, ?)`,
		u.Status, u.Firstname, u.Lastname, t, t)
	if err != nil {
		return 0, err
	}
	if _, err := q.Exec(ctx, `INSERT INTO user_accounts (principal_id, login, password_hash, password_changed_at, must_change_password, admin, language, auth_source_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, u.Login, nullString(u.PasswordHash), nullTimePtr(u.PasswordChangedAt), u.MustChangePassword, u.AdminFlag,
		nullString(u.Language), u.AuthSourceID); err != nil {
		return 0, err
	}
	if mail != "" {
		if _, err := q.Exec(ctx, `INSERT INTO email_addresses (user_id, address, is_default, notify, created_at, updated_at) VALUES (?, ?, TRUE, TRUE, ?, ?)`,
			id, mail, t, t); err != nil {
			return 0, err
		}
	}
	if n == nil {
		n = &domain.UserNotification{NoSelfNotified: true}
	}
	if _, err := q.Exec(ctx, `INSERT INTO user_notification_settings (user_id, mail_notification, no_self_notified, notify_about_high_priority_issues) VALUES (?, ?, ?, ?)`,
		id, nullString(n.MailNotification), n.NoSelfNotified, n.NotifyAboutHighPriorityIssues); err != nil {
		return 0, err
	}
	u.ID = id
	return id, nil
}

func nullTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return db.NewTime(*t)
}

// UpdateUser はユーザーの属性（principals / user_accounts）を保存する。touch が true なら updated_at を now にする。
func UpdateUser(ctx context.Context, q db.Queryer, u *domain.User, touch bool, now time.Time) error {
	if touch {
		if _, err := q.Exec(ctx, `UPDATE principals SET status = ?, firstname = ?, lastname = ?, updated_at = ? WHERE id = ?`,
			u.Status, u.Firstname, u.Lastname, db.NewTime(now), u.ID); err != nil {
			return err
		}
	} else if _, err := q.Exec(ctx, `UPDATE principals SET status = ?, firstname = ?, lastname = ? WHERE id = ?`,
		u.Status, u.Firstname, u.Lastname, u.ID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE user_accounts SET login = ?, password_hash = ?, password_changed_at = ?, must_change_password = ?, admin = ?,
  language = ?, auth_source_id = ? WHERE principal_id = ?`,
		u.Login, nullString(u.PasswordHash), nullTimePtr(u.PasswordChangedAt), u.MustChangePassword, u.AdminFlag,
		nullString(u.Language), u.AuthSourceID, u.ID)
	return err
}

// SetDefaultEmail は既定のメールアドレスを address にする（行が無ければ作成）。変更があれば true。
func SetDefaultEmail(ctx context.Context, q db.Queryer, userID int64, address string, now time.Time) (bool, error) {
	var cur struct {
		ID      int64  `db:"id"`
		Address string `db:"address"`
	}
	err := q.Get(ctx, &cur, `SELECT id, address FROM email_addresses WHERE user_id = ? AND is_default = TRUE`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		if address == "" {
			return false, nil
		}
		t := db.NewTime(now)
		_, err := q.Exec(ctx, `INSERT INTO email_addresses (user_id, address, is_default, notify, created_at, updated_at) VALUES (?, ?, TRUE, TRUE, ?, ?)`,
			userID, address, t, t)
		return err == nil, err
	}
	if err != nil {
		return false, err
	}
	if cur.Address == address {
		return false, nil
	}
	_, err = q.Exec(ctx, `UPDATE email_addresses SET address = ?, updated_at = ? WHERE id = ?`, address, db.NewTime(now), cur.ID)
	return err == nil, err
}

// DeleteUserTokensByActions は Token.where(user_id:, action: actions).delete_all
// （'session' はサーバ側セッションの削除に読み替える）。
func DeleteUserTokensByActions(ctx context.Context, q db.Queryer, userID int64, actions ...string) error {
	var toks []any
	for _, a := range actions {
		if a == "session" {
			if _, err := q.Exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
				return err
			}
			continue
		}
		toks = append(toks, a)
	}
	if len(toks) == 0 {
		return nil
	}
	query, args, err := db.In(`DELETE FROM tokens WHERE user_id = ? AND action IN (?)`, userID, toks)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, query, args...)
	return err
}

// ---------------------------------------------------------------- 通知設定・個人設定

// GetUserNotification は user_notification_settings（行が無ければ既定値）。
func GetUserNotification(ctx context.Context, q db.Queryer, userID int64) (*domain.UserNotification, error) {
	var r struct {
		MailNotification sql.NullString `db:"mail_notification"`
		NoSelfNotified   bool           `db:"no_self_notified"`
		NotifyHigh       bool           `db:"notify_about_high_priority_issues"`
	}
	err := q.Get(ctx, &r, `SELECT mail_notification, no_self_notified, notify_about_high_priority_issues FROM user_notification_settings WHERE user_id = ?`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.UserNotification{UserID: userID, NoSelfNotified: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return &domain.UserNotification{UserID: userID, MailNotification: r.MailNotification.String, NoSelfNotified: r.NoSelfNotified,
		NotifyAboutHighPriorityIssues: r.NotifyHigh}, nil
}

// SaveUserNotification は user_notification_settings を保存する（行が無ければ作成）。
func SaveUserNotification(ctx context.Context, q db.Queryer, n *domain.UserNotification) error {
	res, err := q.Exec(ctx, `UPDATE user_notification_settings SET mail_notification = ?, no_self_notified = ?, notify_about_high_priority_issues = ? WHERE user_id = ?`,
		nullString(n.MailNotification), n.NoSelfNotified, n.NotifyAboutHighPriorityIssues, n.UserID)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k > 0 {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO user_notification_settings (user_id, mail_notification, no_self_notified, notify_about_high_priority_issues) VALUES (?, ?, ?, ?)`,
		n.UserID, nullString(n.MailNotification), n.NoSelfNotified, n.NotifyAboutHighPriorityIssues)
	return err
}

// NotifiedProjectIDs は User#notified_projects_ids（mail_notification が有効なメンバーシップのプロジェクト）。
func NotifiedProjectIDs(ctx context.Context, q db.Queryer, userID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT project_id FROM user_notified_projects WHERE user_id = ? ORDER BY project_id`, userID)
	return ids, err
}

// SetNotifiedProjects は User#update_notified_project_ids（ids のうちメンバーであるプロジェクトだけを残す）。
func SetNotifiedProjects(ctx context.Context, q db.Queryer, userID int64, ids []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM user_notified_projects WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	query, args, err := db.In(`INSERT INTO user_notified_projects (user_id, project_id)
SELECT ?, project_id FROM members WHERE principal_id = ? AND project_id IN (?)`, userID, userID, ids)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, query, args...)
	return err
}

type preferenceDetailRow struct {
	preferenceRow
	ToolbarLanguageOptions sql.NullString `db:"toolbar_language_options"`
	DefaultIssueQueryID    sql.NullInt64  `db:"default_issue_query_id"`
	DefaultProjectQueryID  sql.NullInt64  `db:"default_project_query_id"`
	AutoWatchOn            string         `db:"auto_watch_on"`
}

// GetUserPreferenceDetail は個人設定の全項目を返す。行が無ければ (nil, nil)
// （呼び出し側で UserPreference.new の既定値を作る）。
func GetUserPreferenceDetail(ctx context.Context, q db.Queryer, userID int64) (*domain.UserPreferenceDetail, error) {
	var r preferenceDetailRow
	err := q.Get(ctx, &r, `SELECT user_id, hide_mail, time_zone, comments_sorting, warn_on_leaving_unsaved, textarea_font,
  recently_used_projects, history_default_tab, toolbar_language_options, default_issue_query_id, default_project_query_id, auto_watch_on
FROM user_preferences WHERE user_id = ?`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := &domain.UserPreferenceDetail{
		UserPreference: domain.UserPreference{
			UserID: r.UserID, HideMail: r.HideMail, TimeZone: r.TimeZone.String, CommentsSorting: r.CommentsSorting,
			WarnOnLeavingUnsaved: r.WarnOnLeavingUnsaved, TextareaFont: r.TextareaFont.String,
			RecentlyUsedProjects: r.RecentlyUsedProjects, HistoryDefaultTab: r.HistoryDefaultTab.String,
		},
		Persisted:              true,
		ToolbarLanguageOptions: r.ToolbarLanguageOptions.String,
		DefaultIssueQueryID:    nullID(r.DefaultIssueQueryID),
		DefaultProjectQueryID:  nullID(r.DefaultProjectQueryID),
	}
	_ = json.Unmarshal([]byte(r.AutoWatchOn), &p.AutoWatchOn)
	return p, nil
}

// SaveUserPreferenceDetail は個人設定を保存する（行が無ければ作成）。
func SaveUserPreferenceDetail(ctx context.Context, q db.Queryer, p *domain.UserPreferenceDetail) error {
	aw := p.AutoWatchOn
	if aw == nil {
		aw = []string{}
	}
	awJSON, _ := json.Marshal(aw)
	comments := p.CommentsSorting
	if comments != "desc" {
		comments = "asc"
	}
	font := p.TextareaFont
	if font != "monospace" && font != "proportional" {
		font = ""
	}
	args := []any{p.HideMail, nullString(p.TimeZone), comments, p.WarnOnLeavingUnsaved, nullString(font),
		p.RecentlyUsedProjects, nullString(p.HistoryDefaultTab), nullString(p.ToolbarLanguageOptions),
		p.DefaultIssueQueryID, p.DefaultProjectQueryID, string(awJSON), p.UserID}
	res, err := q.Exec(ctx, `UPDATE user_preferences SET hide_mail = ?, time_zone = ?, comments_sorting = ?, warn_on_leaving_unsaved = ?,
  textarea_font = ?, recently_used_projects = ?, history_default_tab = ?, toolbar_language_options = ?,
  default_issue_query_id = ?, default_project_query_id = ?, auto_watch_on = ? WHERE user_id = ?`, args...)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k > 0 {
		p.Persisted = true
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO user_preferences (hide_mail, time_zone, comments_sorting, warn_on_leaving_unsaved, textarea_font,
  recently_used_projects, history_default_tab, toolbar_language_options, default_issue_query_id, default_project_query_id, auto_watch_on, user_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args...)
	if err == nil {
		p.Persisted = true
	}
	return err
}

// GlobalQueryOptions は個人設定の既定クエリの選択肢（project_id が NULL の kind のクエリ、名前順ではなく id 順）。
func GlobalQueryOptions(ctx context.Context, q db.Queryer, kind string) ([]domain.SavedQueryOption, error) {
	var rows []struct {
		ID         int64         `db:"id"`
		Name       string        `db:"name"`
		UserID     sql.NullInt64 `db:"user_id"`
		Visibility int           `db:"visibility"`
	}
	if err := q.Select(ctx, &rows, `SELECT id, name, user_id, visibility FROM queries WHERE kind = ? AND project_id IS NULL ORDER BY id`, kind); err != nil {
		return nil, err
	}
	out := make([]domain.SavedQueryOption, len(rows))
	for i, r := range rows {
		out[i] = domain.SavedQueryOption{ID: r.ID, Name: r.Name, UserID: nullID(r.UserID), Visibility: r.Visibility}
	}
	return out, nil
}

// ---------------------------------------------------------------- 認証方式

// ListAuthSources は AuthSource.all（id 順）。
func ListAuthSources(ctx context.Context, q db.Queryer) ([]domain.AuthSource, error) {
	var rows []domain.AuthSource
	err := q.Select(ctx, &rows, `SELECT id AS "id", kind AS "kind", name AS "name" FROM auth_sources ORDER BY id`)
	return rows, err
}

// ---------------------------------------------------------------- メールアドレス

type emailRow struct {
	ID        int64   `db:"id"`
	UserID    int64   `db:"user_id"`
	Address   string  `db:"address"`
	IsDefault bool    `db:"is_default"`
	Notify    bool    `db:"notify"`
	CreatedAt db.Time `db:"created_at"`
	UpdatedAt db.Time `db:"updated_at"`
}

func (r emailRow) domain() *domain.EmailAddress {
	return &domain.EmailAddress{ID: r.ID, UserID: r.UserID, Address: r.Address, IsDefault: r.IsDefault, Notify: r.Notify,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

// UserEmailAddresses はユーザーのメールアドレス（id 順）。additionalOnly なら既定以外のみ。
func UserEmailAddresses(ctx context.Context, q db.Queryer, userID int64, additionalOnly bool) ([]*domain.EmailAddress, error) {
	query := `SELECT id, user_id, address, is_default, notify, created_at, updated_at FROM email_addresses WHERE user_id = ?`
	if additionalOnly {
		query += ` AND is_default = FALSE`
	}
	var rows []emailRow
	if err := q.Select(ctx, &rows, query+` ORDER BY id`, userID); err != nil {
		return nil, err
	}
	out := make([]*domain.EmailAddress, len(rows))
	for i, r := range rows {
		out[i] = r.domain()
	}
	return out, nil
}

// CountUserEmailAddresses は user.email_addresses.count。
func CountUserEmailAddresses(ctx context.Context, q db.Queryer, userID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM email_addresses WHERE user_id = ?`, userID)
	return n, err
}

// GetAdditionalEmailAddress は user.email_addresses.where(is_default: false).find(id)。
func GetAdditionalEmailAddress(ctx context.Context, q db.Queryer, userID, id int64) (*domain.EmailAddress, error) {
	var r emailRow
	if err := q.Get(ctx, &r, `SELECT id, user_id, address, is_default, notify, created_at, updated_at FROM email_addresses
WHERE user_id = ? AND id = ? AND is_default = FALSE`, userID, id); err != nil {
		return nil, notFound(err)
	}
	return r.domain(), nil
}

// CreateEmailAddress は追加のメールアドレスを作成する。
func CreateEmailAddress(ctx context.Context, q db.Queryer, userID int64, address string, now time.Time) (int64, error) {
	t := db.NewTime(now)
	return q.InsertReturningID(ctx, `INSERT INTO email_addresses (user_id, address, is_default, notify, created_at, updated_at) VALUES (?, ?, FALSE, TRUE, ?, ?)`,
		userID, address, t, t)
}

// UpdateEmailNotify は notify を変更する（変わらなければ何もしない）。
func UpdateEmailNotify(ctx context.Context, q db.Queryer, id int64, notify bool, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE email_addresses SET notify = ?, updated_at = ? WHERE id = ? AND notify <> ?`, notify, db.NewTime(now), id, notify)
	return err
}

// DestroyEmailAddress は既定でないメールアドレスを削除する。
func DestroyEmailAddress(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `DELETE FROM email_addresses WHERE id = ? AND is_default = FALSE`, id)
	return err
}

// ---------------------------------------------------------------- 削除

// DestroyUser は User#destroy（remove_references_before_destroy を含む）。
// substituteID は User.anonymous の id。
func DestroyUser(ctx context.Context, q db.Queryer, userID, substituteID int64) error {
	sid := substituteID
	execs := []struct {
		query string
		args  []any
	}{
		// remove_references_before_destroy
		{`UPDATE attachments SET author_id = ? WHERE author_id = ?`, []any{sid, userID}},
		{`UPDATE news_comments SET author_id = ? WHERE author_id = ?`, []any{sid, userID}},
		{`UPDATE issues SET author_id = ? WHERE author_id = ?`, []any{sid, userID}},
		{`UPDATE issues SET assigned_to_id = NULL WHERE assigned_to_id = ?`, []any{userID}},
		{`UPDATE issue_journals SET user_id = ? WHERE user_id = ?`, []any{sid, userID}},
		{`UPDATE issue_journals SET updated_by_id = ? WHERE updated_by_id = ?`, []any{sid, userID}},
		{`UPDATE issue_journal_details SET old_value = ? WHERE property = 'attr' AND prop_key = 'assigned_to_id' AND old_value = ?`, []any{fmt.Sprint(sid), fmt.Sprint(userID)}},
		{`UPDATE issue_journal_details SET value = ? WHERE property = 'attr' AND prop_key = 'assigned_to_id' AND value = ?`, []any{fmt.Sprint(sid), fmt.Sprint(userID)}},
		{`UPDATE messages SET author_id = ? WHERE author_id = ?`, []any{sid, userID}},
		{`UPDATE news SET author_id = ? WHERE author_id = ?`, []any{sid, userID}},
		{`DELETE FROM queries WHERE user_id = ? AND visibility = 0`, []any{userID}},
		{`UPDATE queries SET user_id = ? WHERE user_id = ?`, []any{sid, userID}},
		{`UPDATE time_entries SET user_id = ? WHERE user_id = ?`, []any{sid, userID}},
		// buropher: time_entries.author_id は NOT NULL の外部キーのため同様に付け替える（Redmine は残す）
		{`UPDATE time_entries SET author_id = ? WHERE author_id = ?`, []any{sid, userID}},
		{`DELETE FROM tokens WHERE user_id = ?`, []any{userID}},
		{`DELETE FROM watchers WHERE principal_id = ?`, []any{userID}},
		{`UPDATE wiki_page_versions SET author_id = ? WHERE author_id = ?`, []any{sid, userID}},
		{`DELETE FROM custom_values WHERE custom_field_id IN (SELECT id FROM custom_fields WHERE field_format = 'user') AND value = ?`, []any{fmt.Sprint(userID)}},
		// Principal: nullify_projects_default_assigned_to
		{`UPDATE projects SET default_assigned_to_id = NULL WHERE default_assigned_to_id = ?`, []any{userID}},
	}
	for _, e := range execs {
		if _, err := q.Exec(ctx, e.query, e.args...); err != nil {
			return fmt.Errorf("repository: destroy user: %w", err)
		}
	}
	// has_many :members, dependent: :destroy（継承ロールの連鎖削除を含む）
	var memberIDs []int64
	if err := q.Select(ctx, &memberIDs, `SELECT id FROM members WHERE principal_id = ? ORDER BY id`, userID); err != nil {
		return err
	}
	for _, id := range memberIDs {
		if err := DestroyMember(ctx, q, id); err != nil {
			return err
		}
	}
	more := []struct {
		query string
		args  []any
	}{
		{`UPDATE issue_categories SET assigned_to_id = NULL WHERE assigned_to_id = ?`, []any{userID}},
		// has_many :changesets, dependent: :nullify
		{`UPDATE changesets SET user_id = NULL WHERE user_id = ?`, []any{userID}},
		{`DELETE FROM group_users WHERE user_id = ?`, []any{userID}},
		{`DELETE FROM custom_values WHERE customized_kind = 'principal' AND customized_id = ?`, []any{userID}},
		{`DELETE FROM principals WHERE id = ?`, []any{userID}},
	}
	for _, e := range more {
		if _, err := q.Exec(ctx, e.query, e.args...); err != nil {
			return fmt.Errorf("repository: destroy user: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- 表示用の集計

// GroupUserCounts は User.joins(:groups).group(:group_id).count（group_id → 人数）。
func GroupUserCounts(ctx context.Context, q db.Queryer) (map[int64]int, error) {
	var rows []struct {
		GroupID int64 `db:"group_id"`
		N       int   `db:"n"`
	}
	if err := q.Select(ctx, &rows, `SELECT gu.group_id AS group_id, COUNT(*) AS n FROM group_users gu
JOIN principals u ON u.id = gu.user_id AND u.kind IN ('user', 'anonymous_user') GROUP BY gu.group_id`); err != nil {
		return nil, err
	}
	out := map[int64]int{}
	for _, r := range rows {
		out[r.GroupID] = r.N
	}
	return out, nil
}

// PrincipalVisible は Principal.visible(user).find_by(id: id) が存在するか（cond は authz の PrincipalVisibleCondition）。
func PrincipalVisible(ctx context.Context, q db.Queryer, cond string, id int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM principals WHERE id = ? AND (`+cond+`)`, id)
	return n > 0, err
}

// CountIssues は Issue.visible の件数（visibleCond は authz の IssueVisibleCondition）。
// assignedIDs が空でなければ担当者、authorID が 0 でなければ作成者で絞る。openOnly なら未完了のみ。
func CountIssues(ctx context.Context, q db.Queryer, visibleCond string, assignedIDs []int64, authorID int64, openOnly bool) (int, error) {
	query := `SELECT COUNT(*) FROM issues JOIN projects ON projects.id = issues.project_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id WHERE (` + visibleCond + `)`
	var args []any
	if len(assignedIDs) > 0 {
		s, a, err := db.In(` AND issues.assigned_to_id IN (?)`, assignedIDs)
		if err != nil {
			return 0, err
		}
		query += s
		args = append(args, a...)
	}
	if authorID != 0 {
		query += ` AND issues.author_id = ?`
		args = append(args, authorID)
	}
	if openOnly {
		query += ` AND issue_statuses.is_closed = FALSE`
	}
	var n int
	err := q.Get(ctx, &n, query, args...)
	return n, err
}
