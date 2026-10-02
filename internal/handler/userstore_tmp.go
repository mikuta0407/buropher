package handler

// ============================================================================
// 暫定実装（TEMPORARY）
//
// internal/domain・internal/authz とユーザー/プロジェクトのリポジトリは別途開発中のため、
// ログイン・レイアウトに必要な最小限のユーザー型と SQL をここに置く。
// それらが揃ったら UserStore の実装と User 型を差し替え、このファイルは削除すること。
// ============================================================================

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/settings"
)

// ユーザーの状態（Principal::STATUS_*）。
const (
	StatusAnonymous  = 0
	StatusActive     = 1
	StatusRegistered = 2
	StatusLocked     = 3
)

// User は User / AnonymousUser の暫定モデル（helper.User を実装する）。
type User struct {
	id                 int64
	Status             int
	Firstname          string
	Lastname           string
	LoginName          string
	PasswordHash       string
	MustChangePasswd   bool
	PasswdChangedOn    time.Time
	IsAdmin            bool
	Language           string
	AuthSourceID       int64
	TwofaScheme        string
	MailAddress        string
	PrefWarnOnLeaving  bool
	PrefTextareaFont   string
	PrefTimeZone       string
	GroupTwofaRequired bool // 所属グループのいずれかが twofa_required

	anonymous bool
	settings  *settings.Settings
	atomKey   func() string
}

var _ helper.User = (*User)(nil)

func (u *User) ID() int64 { return u.id }

// Logged は User#logged?。
func (u *User) Logged() bool { return !u.anonymous }

// Anonymous は User#anonymous?。
func (u *User) Anonymous() bool { return u.anonymous }

func (u *User) Admin() bool      { return u.IsAdmin && !u.anonymous }
func (u *User) Active() bool     { return u.Status == StatusActive }
func (u *User) Registered() bool { return u.Status == StatusRegistered }
func (u *User) Locked() bool     { return u.Status == StatusLocked }
func (u *User) Login() string    { return u.LoginName }
func (u *User) Mail() string     { return u.MailAddress }
func (u *User) TextareaFont() string {
	return u.PrefTextareaFont
}
func (u *User) WarnOnLeavingUnsaved() bool { return u.PrefWarnOnLeaving }

// AtomKey は User#atom_key（ログインユーザーは feeds トークンがなければ作る）。
func (u *User) AtomKey() string {
	if u.anonymous || u.atomKey == nil {
		return ""
	}
	return u.atomKey()
}

// labelByStatus は User::LABEL_BY_STATUS。
var labelByStatus = map[int]string{0: "anon", 1: "active", 2: "registered", 3: "locked"}

// CSSClasses は User#css_classes。
func (u *User) CSSClasses() string { return "user " + labelByStatus[u.Status] }

func (u *User) userFormat(format string) string {
	if format == "" && u.settings != nil {
		format = u.settings.String("user_format")
	}
	if _, ok := userFormats[format]; !ok {
		format = "firstname_lastname"
	}
	return format
}

// Name は User#name(formatter)（匿名は AnonymousUser#name = l(:label_user_anonymous) だが呼び出し側で扱う）。
func (u *User) Name(format string) string {
	if u.anonymous {
		return "Anonymous"
	}
	return userFormats[u.userFormat(format)].name(u)
}

// Initials は User#initials（大文字化）。
func (u *User) Initials() string {
	return strings.ToUpper(userFormats[u.userFormat("")].initials(u))
}

// MustChangePassword は User#must_change_password?（LDAP 等の外部認証は変更不可とみなす）。
func (u *User) MustChangePassword() bool {
	if u.AuthSourceID != 0 {
		return false
	}
	return u.MustChangePasswd || u.passwordExpired()
}

func (u *User) passwordExpired() bool {
	if u.settings == nil {
		return false
	}
	period := u.settings.Int("password_max_age")
	if period == 0 {
		return false
	}
	return u.PasswdChangedOn.Before(time.Now().AddDate(0, 0, -period))
}

// TwofaActive は User#twofa_active?。
func (u *User) TwofaActive() bool { return u.TwofaScheme != "" }

// MustActivateTwofa は User#must_activate_twofa?。
func (u *User) MustActivateTwofa() bool {
	if u.TwofaActive() || u.settings == nil {
		return false
	}
	switch {
	case u.settings.TwofaRequired():
		return true
	case u.settings.TwofaRequiredForAdministrators() && u.Admin():
		return true
	case u.settings.TwofaOptional() && u.GroupTwofaRequired:
		return true
	}
	return false
}

// EffectiveLanguage は User#language（force_default_language_for_loggedin なら既定言語）。
func (u *User) EffectiveLanguage() string {
	if u.settings != nil && u.settings.Bool("force_default_language_for_loggedin") {
		return u.settings.String("default_language")
	}
	return u.Language
}

// ---- User::USER_FORMATS ----

type userFormat struct {
	name, initials func(u *User) string
}

func firstRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

func firstRune(s string) string { return firstRunes(s, 1) }

var initialRe = regexp.MustCompile(`(([\p{L}])[\p{L}]*\.?)`)

func firstInitials(s string) string { return initialRe.ReplaceAllString(s, "$2.") }

var userFormats = map[string]userFormat{
	"firstname_lastname": {
		func(u *User) string { return u.Firstname + " " + u.Lastname },
		func(u *User) string { return firstRune(u.Firstname) + firstRune(u.Lastname) }},
	"firstname_lastinitial": {
		func(u *User) string { return u.Firstname + " " + firstRune(u.Lastname) + "." },
		func(u *User) string { return firstRune(u.Firstname) + firstRune(u.Lastname) }},
	"firstinitial_lastname": {
		func(u *User) string { return firstInitials(u.Firstname) + " " + u.Lastname },
		func(u *User) string { return firstRune(firstInitials(u.Firstname)) + firstRune(u.Lastname) }},
	"firstname": {
		func(u *User) string { return u.Firstname },
		func(u *User) string { return firstRunes(u.Firstname, 2) }},
	"lastname_firstname": {
		func(u *User) string { return u.Lastname + " " + u.Firstname },
		func(u *User) string { return firstRune(u.Lastname) + firstRune(u.Firstname) }},
	"lastnamefirstname": {
		func(u *User) string { return u.Lastname + u.Firstname },
		func(u *User) string { return firstRune(u.Lastname) + firstRune(u.Firstname) }},
	"lastname_comma_firstname": {
		func(u *User) string { return u.Lastname + ", " + u.Firstname },
		func(u *User) string { return firstRune(u.Lastname) + firstRune(u.Firstname) }},
	"lastname": {
		func(u *User) string { return u.Lastname },
		func(u *User) string { return firstRunes(u.Lastname, 2) }},
	"username": {
		func(u *User) string { return u.LoginName },
		func(u *User) string { return firstRunes(u.LoginName, 2) }},
}

// ---- UserStore ----

// UserStore はハンドラが使うユーザー関連の永続化操作（domain/repository 完成までの差し替え点）。
type UserStore interface {
	// Anonymous は User.anonymous。
	Anonymous(ctx context.Context) (*User, error)
	// FindActive は User.active.find_by_id(id)（なければ nil）。
	FindActive(ctx context.Context, id int64) (*User, error)
	// FindByLogin は User.find_by_login（大文字小文字を区別する一致を優先）。
	FindByLogin(ctx context.Context, login string) (*User, error)
	// UpdateLastLogin は update_last_login_on!。
	UpdateLastLogin(ctx context.Context, id int64, t time.Time) error
	// UpdatePasswordHash はパスワードハッシュを再ハッシュ結果で更新する。
	UpdatePasswordHash(ctx context.Context, id int64, hash string) error
	// CreateToken は Token.create!(user:, action:)（max_instances を超えた古いものを消す）。
	CreateToken(ctx context.Context, userID int64, action string) (string, error)
	// FindTokenUser は Token.find_active_user(action, key, validity_days)。
	FindTokenUser(ctx context.Context, action, key string, validityDays int) (*User, error)
	// DeleteToken は user の action / value のトークンを消す。
	DeleteToken(ctx context.Context, userID int64, action, value string) error
	// FeedsKey は atom_key（なければ作成）。
	FeedsKey(ctx context.Context, userID int64) (string, error)
	// JumpBox はプロジェクトジャンプボックスの一覧。
	JumpBox(ctx context.Context, u *User) (*helper.JumpBox, error)
	// LatestNews は News.latest(user, count)。
	LatestNews(ctx context.Context, u *User, count int) ([]*News, error)
}

// SQLUserStore は UserStore の暫定 SQL 実装。
type SQLUserStore struct {
	DB       *db.DB
	Settings *settings.Settings
}

var _ UserStore = (*SQLUserStore)(nil)

type userRow struct {
	ID              int64          `db:"id"`
	Kind            string         `db:"kind"`
	Status          int            `db:"status"`
	Firstname       string         `db:"firstname"`
	Lastname        string         `db:"lastname"`
	Login           sql.NullString `db:"login"`
	PasswordHash    sql.NullString `db:"password_hash"`
	PwdChangedAt    db.NullTime    `db:"password_changed_at"`
	MustChange      sql.NullBool   `db:"must_change_password"`
	Admin           sql.NullBool   `db:"admin"`
	Language        sql.NullString `db:"language"`
	AuthSourceID    sql.NullInt64  `db:"auth_source_id"`
	TwofaScheme     sql.NullString `db:"twofa_scheme"`
	Mail            sql.NullString `db:"mail"`
	WarnOnLeaving   sql.NullBool   `db:"warn_on_leaving_unsaved"`
	TextareaFont    sql.NullString `db:"textarea_font"`
	TimeZone        sql.NullString `db:"time_zone"`
	GroupTwofaCount int            `db:"group_twofa"`
}

const userSelect = `SELECT p.id, p.kind, p.status, p.firstname, p.lastname,
  a.login, a.password_hash, a.password_changed_at, a.must_change_password, a.admin, a.language, a.auth_source_id, a.twofa_scheme,
  (SELECT e.address FROM email_addresses e WHERE e.user_id = p.id AND e.is_default = ? LIMIT 1) AS mail,
  up.warn_on_leaving_unsaved, up.textarea_font, up.time_zone,
  (SELECT COUNT(*) FROM group_users gu JOIN principals g ON g.id = gu.group_id WHERE gu.user_id = p.id AND g.twofa_required = ?) AS group_twofa
FROM principals p
LEFT JOIN user_accounts a ON a.principal_id = p.id
LEFT JOIN user_preferences up ON up.user_id = p.id
`

func (s *SQLUserStore) toUser(r *userRow) *User {
	u := &User{
		id: r.ID, Status: r.Status, Firstname: r.Firstname, Lastname: r.Lastname,
		LoginName: r.Login.String, PasswordHash: r.PasswordHash.String,
		MustChangePasswd: r.MustChange.Bool, IsAdmin: r.Admin.Bool, Language: r.Language.String,
		AuthSourceID: r.AuthSourceID.Int64, TwofaScheme: r.TwofaScheme.String, MailAddress: r.Mail.String,
		PrefWarnOnLeaving: !r.WarnOnLeaving.Valid || r.WarnOnLeaving.Bool,
		PrefTextareaFont:  r.TextareaFont.String, PrefTimeZone: r.TimeZone.String,
		GroupTwofaRequired: r.GroupTwofaCount > 0,
		anonymous:          r.Kind == "anonymous_user",
		settings:           s.Settings,
	}
	if r.PwdChangedAt.Valid {
		u.PasswdChangedOn = r.PwdChangedAt.Time
	}
	if !u.anonymous {
		id := u.id
		var cached string
		u.atomKey = func() string {
			if cached == "" {
				cached, _ = s.FeedsKey(context.Background(), id)
			}
			return cached
		}
	}
	return u
}

func (s *SQLUserStore) one(ctx context.Context, where string, args ...any) (*User, error) {
	var rows []userRow
	all := append([]any{true, true}, args...)
	if err := s.DB.Select(ctx, &rows, userSelect+where, all...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return s.toUser(&rows[0]), nil
}

func (s *SQLUserStore) Anonymous(ctx context.Context) (*User, error) {
	u, err := s.one(ctx, `WHERE p.kind = 'anonymous_user' LIMIT 1`)
	if err != nil {
		return nil, err
	}
	if u == nil {
		// 組込みの匿名ユーザーが無い DB（init 前）でも描画できるようにする
		u = &User{Status: StatusAnonymous, Lastname: "Anonymous", anonymous: true, PrefWarnOnLeaving: true, settings: s.Settings}
	}
	return u, nil
}

func (s *SQLUserStore) FindActive(ctx context.Context, id int64) (*User, error) {
	return s.one(ctx, `WHERE p.id = ? AND p.kind = 'user' AND p.status = 1`, id)
}

func (s *SQLUserStore) FindByLogin(ctx context.Context, login string) (*User, error) {
	if login == "" {
		return nil, nil
	}
	if u, err := s.one(ctx, `WHERE p.kind = 'user' AND a.login = ? LIMIT 1`, login); u != nil || err != nil {
		return u, err
	}
	return s.one(ctx, `WHERE p.kind = 'user' AND lower(a.login) = lower(?) ORDER BY p.id LIMIT 1`, login)
}

func (s *SQLUserStore) UpdateLastLogin(ctx context.Context, id int64, t time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE user_accounts SET last_login_at = ? WHERE principal_id = ?`, db.NewTime(t), id)
	return err
}

func (s *SQLUserStore) UpdatePasswordHash(ctx context.Context, id int64, hash string) error {
	_, err := s.DB.Exec(ctx, `UPDATE user_accounts SET password_hash = ? WHERE principal_id = ?`, hash, id)
	return err
}

// tokenMaxInstances は Token.add_action の max_instances。
var tokenMaxInstances = map[string]int{"api": 1, "autologin": 10, "feeds": 1, "recovery": 1, "register": 1}

// generateTokenValue は Token.generate_token_value（Redmine::Utils.random_hex(20)）。
func generateTokenValue() string {
	var b [20]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *SQLUserStore) CreateToken(ctx context.Context, userID int64, action string) (string, error) {
	value := generateTokenValue()
	err := s.DB.WithTx(ctx, func(tx *db.Tx) error {
		// delete_previous_tokens
		max := tokenMaxInstances[action]
		if max == 0 {
			max = 1
		}
		if max > 1 {
			var ids []int64
			if err := tx.Select(ctx, &ids, `SELECT id FROM tokens WHERE user_id = ? AND action = ? ORDER BY updated_at DESC, id DESC`, userID, action); err != nil {
				return err
			}
			if len(ids) > max-1 {
				for _, id := range ids[max-1:] {
					if _, err := tx.Exec(ctx, `DELETE FROM tokens WHERE id = ?`, id); err != nil {
						return err
					}
				}
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM tokens WHERE user_id = ? AND action = ?`, userID, action); err != nil {
			return err
		}
		now := db.Now()
		_, err := tx.Exec(ctx, `INSERT INTO tokens (user_id, action, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, userID, action, value, now, now)
		return err
	})
	return value, err
}

var tokenKeyRe = regexp.MustCompile(`\A[a-zA-Z0-9]+\z`)

func (s *SQLUserStore) FindTokenUser(ctx context.Context, action, key string, validityDays int) (*User, error) {
	if action == "" || !tokenKeyRe.MatchString(key) {
		return nil, nil
	}
	var rows []struct {
		UserID    int64   `db:"user_id"`
		Value     string  `db:"value"`
		CreatedAt db.Time `db:"created_at"`
	}
	if err := s.DB.Select(ctx, &rows, `SELECT user_id, value, created_at FROM tokens WHERE action = ? AND value = ?`, action, key); err != nil {
		return nil, err
	}
	if len(rows) == 0 || subtle.ConstantTimeCompare([]byte(rows[0].Value), []byte(key)) != 1 {
		return nil, nil
	}
	if validityDays > 0 && !rows[0].CreatedAt.After(time.Now().AddDate(0, 0, -validityDays)) {
		return nil, nil
	}
	return s.FindActive(ctx, rows[0].UserID)
}

func (s *SQLUserStore) DeleteToken(ctx context.Context, userID int64, action, value string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM tokens WHERE user_id = ? AND action = ? AND value = ?`, userID, action, value)
	return err
}

func (s *SQLUserStore) FeedsKey(ctx context.Context, userID int64) (string, error) {
	var v string
	err := s.DB.Get(ctx, &v, `SELECT value FROM tokens WHERE user_id = ? AND action = 'feeds' ORDER BY id LIMIT 1`, userID)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return s.CreateToken(ctx, userID, "feeds")
}

// ---- projects (jump box) ----

type projRow struct {
	ID         int64         `db:"id"`
	ParentID   sql.NullInt64 `db:"parent_id"`
	Name       string        `db:"name"`
	Identifier string        `db:"identifier"`
	Status     int           `db:"status"`
	IsPublic   bool          `db:"is_public"`
}

// projectTreeIndex は全プロジェクトから lft/rgt（名前順のネストセット）を計算する。
func projectTreeIndex(rows []projRow) map[int64][2]int {
	children := map[int64][]projRow{}
	for _, r := range rows {
		children[r.ParentID.Int64] = append(children[r.ParentID.Int64], r)
	}
	for k := range children {
		c := children[k]
		sort.SliceStable(c, func(i, j int) bool {
			if c[i].Name != c[j].Name {
				return c[i].Name < c[j].Name
			}
			return c[i].ID < c[j].ID
		})
	}
	idx := map[int64][2]int{}
	n := 0
	var walk func(parent int64)
	walk = func(parent int64) {
		for _, c := range children[parent] {
			n++
			lft := n
			walk(c.ID)
			n++
			idx[c.ID] = [2]int{lft, n}
		}
	}
	walk(0)
	return idx
}

func (s *SQLUserStore) JumpBox(ctx context.Context, u *User) (*helper.JumpBox, error) {
	jb := &helper.JumpBox{}
	if u == nil || u.anonymous {
		return jb, nil
	}
	var all []projRow
	if err := s.DB.Select(ctx, &all, `SELECT id, parent_id, name, identifier, status, is_public FROM projects`); err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return jb, nil
	}
	idx := projectTreeIndex(all)
	byID := map[int64]projRow{}
	for _, r := range all {
		byID[r.ID] = r
	}
	var memberIDs []int64
	if err := s.DB.Select(ctx, &memberIDs, `SELECT project_id FROM members WHERE principal_id = ?`, u.id); err != nil {
		return nil, err
	}
	member := map[int64]bool{}
	for _, id := range memberIDs {
		member[id] = true
	}
	jp := func(r projRow) helper.JumpProject {
		i := idx[r.ID]
		return helper.JumpProject{ID: r.ID, Name: r.Name, Identifier: r.Identifier, Lft: i[0], Rgt: i[1]}
	}
	// TODO(authz): Project.visible の正確な判定（ロール・モジュール・アーカイブ親）は authz 完成後に置き換える。
	visible := func(r projRow) bool {
		if r.Status == 9 || r.Status == 10 {
			return false
		}
		return u.Admin() || r.IsPublic || member[r.ID]
	}
	// user.projects.active（メンバーで status=1）
	for _, r := range all {
		if member[r.ID] && r.Status == 1 {
			jb.Projects = append(jb.Projects, jp(r))
		}
	}
	var bookmarks []int64
	if err := s.DB.Select(ctx, &bookmarks, `SELECT project_id FROM user_project_bookmarks WHERE user_id = ? ORDER BY project_id`, u.id); err != nil {
		return nil, err
	}
	bm := map[int64]bool{}
	for _, id := range bookmarks {
		bm[id] = true
		if r, ok := byID[id]; ok && visible(r) {
			jb.Bookmarked = append(jb.Bookmarked, jp(r))
		}
	}
	var recents []int64
	if err := s.DB.Select(ctx, &recents, `SELECT project_id FROM user_recent_projects WHERE user_id = ? ORDER BY position`, u.id); err != nil {
		return nil, err
	}
	var limit int
	if err := s.DB.Get(ctx, &limit, `SELECT recently_used_projects FROM user_preferences WHERE user_id = ?`, u.id); err != nil {
		limit = 3
	}
	if len(recents) > limit {
		recents = recents[:limit]
	}
	for _, id := range recents {
		if r, ok := byID[id]; ok && visible(r) {
			jb.Recents = append(jb.Recents, jp(r))
		}
	}
	return jb, nil
}

// ---- news ----

// News は welcome#index が表示するニュースの暫定モデル。
type News struct {
	ID            int64
	Title         string
	Summary       string
	CommentsCount int
	CreatedOn     time.Time
	Project       *Project
	Author        *User
}

// Path は news_path(news)。
func (n *News) Path() string { return "/news/" + itoa(n.ID) }

// Project は helper.Project の暫定実装。
type Project struct {
	id         int64
	name       string
	identifier string
	status     int
	leaf       bool
}

var _ helper.Project = (*Project)(nil)

func (p *Project) ID() int64          { return p.id }
func (p *Project) Name() string       { return p.name }
func (p *Project) Identifier() string { return p.identifier }
func (p *Project) Archived() bool     { return p.status == 9 }
func (p *Project) Leaf() bool         { return p.leaf }

func (s *SQLUserStore) LatestNews(ctx context.Context, u *User, count int) ([]*News, error) {
	// TODO(authz): News.visible（view_news 権限）を正確に判定する。暫定で「有効な公開プロジェクトで
	// news モジュールが有効」または管理者・メンバーを可視とする。
	var rows []struct {
		ID            int64          `db:"id"`
		Title         string         `db:"title"`
		Summary       sql.NullString `db:"summary"`
		CommentsCount int            `db:"comments_count"`
		CreatedAt     db.Time        `db:"created_at"`
		AuthorID      int64          `db:"author_id"`
		ProjectID     int64          `db:"project_id"`
		PName         string         `db:"pname"`
		PIdentifier   string         `db:"pidentifier"`
		PStatus       int            `db:"pstatus"`
	}
	uid := int64(0)
	admin := false
	if u != nil && !u.anonymous {
		uid, admin = u.id, u.Admin()
	}
	err := s.DB.Select(ctx, &rows, `SELECT n.id, n.title, n.summary, n.comments_count, n.created_at, n.author_id, n.project_id,
  pr.name AS pname, pr.identifier AS pidentifier, pr.status AS pstatus
FROM news n JOIN projects pr ON pr.id = n.project_id
WHERE pr.status = 1
  AND EXISTS (SELECT 1 FROM project_modules m WHERE m.project_id = pr.id AND m.name = 'news')
  AND (? OR pr.is_public = ? OR EXISTS (SELECT 1 FROM members mb WHERE mb.project_id = pr.id AND mb.principal_id = ?))
ORDER BY n.created_at DESC LIMIT ?`, admin, true, uid, count)
	if err != nil {
		return nil, err
	}
	out := make([]*News, 0, len(rows))
	for _, r := range rows {
		author, err := s.one(ctx, `WHERE p.id = ?`, r.AuthorID)
		if err != nil {
			return nil, err
		}
		out = append(out, &News{
			ID: r.ID, Title: r.Title, Summary: r.Summary.String, CommentsCount: r.CommentsCount, CreatedOn: r.CreatedAt.Time,
			Project: &Project{id: r.ProjectID, name: r.PName, identifier: r.PIdentifier, status: r.PStatus},
			Author:  author,
		})
	}
	return out, nil
}
