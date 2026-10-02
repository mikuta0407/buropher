// Package issues は Redmine のチケット (app/models/issue.rb, issue_relation.rb, journal.rb,
// journal_detail.rb, watcher 関連) のドメインロジックを移植したサービス層。
//
// 主な入口:
//
//	e := issues.NewEnv(tx, settings, currentUser)      // User.current と DB (トランザクション) を束ねる
//	iss, _ := e.New(ctx, project, tracker, author)      // Issue.new + 既定値
//	e.InitJournal(iss, user, notes)                      // 変更履歴の開始 (init_journal)
//	e.SafeAssign(ctx, iss, params, user)                 // safe_attributes=
//	res, err := e.Save(ctx, iss)                         // 検証 + 保存 + コールバック群
//	e.Dispatch(ctx, res.Notifications)                   // コミット後に通知をキューへ
//
// Redmine のモデルが内部状態 (dirty tracking, @parent_issue, current_journal ...) として
// 持っていたものは Issue 構造体に、User.current や設定・マスタのキャッシュは Env に置く。
// Env は 1 リクエスト (1 トランザクション) 用で、並行利用は想定しない。
package issues

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
)

// ErrNotFound はチケット等が見つからないときのエラー。
var ErrNotFound = errors.New("issues: not found")

// ErrStale は楽観ロック (lock_version) の衝突 (ActiveRecord::StaleObjectError)。
var ErrStale = errors.New("issues: stale object")

// Env はチケット操作の実行環境。User.current・DB ハンドル・設定とマスタのキャッシュを持つ。
type Env struct {
	// Q は DB ハンドル。書き込みを伴う操作では *db.Tx であること
	// (*db.DB の場合は操作ごとにトランザクションを張る)。
	Q db.Queryer
	// Settings は Setting。
	Settings *settings.Settings
	// User は User.current (nil なら匿名ユーザとして扱う)。
	User *domain.User
	// Now は現在時刻 (既定 clock.Now)。
	Now func() time.Time
	// Notifier は Dispatch が通知を渡す先 (nil なら破棄)。
	Notifier Notifier
	// DateFormat はエラーメッセージ中の日付の書式 (format_date)。既定は YYYY-MM-DD。
	DateFormat func(time.Time) string
	// Translate は保存時に組み立てる翻訳済みメッセージ (error_move_of_child_not_possible 等) の翻訳関数。
	Translate domain.Translator

	statuses   []*domain.IssueStatus
	trackers   []*domain.Tracker
	priorities []*domain.Enumeration
	cfs        []*customfield.CustomField
	projects   map[int64]*domain.Project
	az         map[int64]*authz.Authorizer
	users      map[int64]*domain.User
	principals map[int64]*domain.Principal
}

// NewEnv は Env を作る。user が nil なら匿名ユーザを読み込む。
func NewEnv(q db.Queryer, st *settings.Settings, user *domain.User) *Env {
	return &Env{Q: q, Settings: st, User: user}
}

// Reset はマスタ・権限のキャッシュを破棄する (マスタやメンバーシップを変更した後に呼ぶ)。
func (e *Env) Reset() {
	e.statuses, e.trackers, e.priorities, e.cfs = nil, nil, nil, nil
	e.projects, e.az, e.users, e.principals = nil, nil, nil, nil
}

// WithQ は DB ハンドルだけを差し替えた Env を返す (キャッシュは共有しない)。
func (e *Env) WithQ(q db.Queryer) *Env {
	return &Env{Q: q, Settings: e.Settings, User: e.User, Now: e.Now, Notifier: e.Notifier, DateFormat: e.DateFormat, Translate: e.Translate}
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return clock.Now().UTC()
}

// today は User.current.today (タイムゾーンは UTC 固定)。
func (e *Env) today() time.Time {
	y, m, d := e.now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (e *Env) formatDate(t time.Time) string {
	if e.DateFormat != nil {
		return e.DateFormat(t)
	}
	return t.Format("2006-01-02")
}

// inTx は e.Q がトランザクションでなければトランザクションを張って fn を実行する。
func (e *Env) inTx(ctx context.Context, fn func() error) error {
	d, ok := e.Q.(*db.DB)
	if !ok {
		return fn()
	}
	return d.WithTx(ctx, func(tx *db.Tx) error {
		// Authorizer は DB ハンドルを保持するので、トランザクション中は別のキャッシュを使う
		az := e.az
		e.Q, e.az = tx, nil
		defer func() { e.Q, e.az = d, az }()
		return fn()
	})
}

// currentUser は User.current (未設定なら匿名ユーザ)。
func (e *Env) currentUser(ctx context.Context) (*domain.User, error) {
	if e.User == nil {
		u, err := repository.AnonymousUser(ctx, e.Q)
		if err != nil {
			return nil, err
		}
		e.User = u
	}
	return e.User, nil
}

// authz は user の Authorizer (キャッシュ)。
func (e *Env) authz(u *domain.User) *authz.Authorizer {
	if e.az == nil {
		e.az = map[int64]*authz.Authorizer{}
	}
	if a, ok := e.az[u.ID]; ok && a.User() == u {
		return a
	}
	a := authz.New(e.Q, u)
	e.az[u.ID] = a
	return a
}

// allowedTo は user.allowed_to?(perm, project)。
func (e *Env) allowedTo(ctx context.Context, u *domain.User, perm string, p *domain.Project) (bool, error) {
	if u == nil || p == nil {
		return false, nil
	}
	return e.authz(u).AllowedTo(ctx, domain.Perm(perm), p)
}

// ---------------------------------------------------------------- マスタ

// Statuses は IssueStatus.sorted。
func (e *Env) Statuses(ctx context.Context) ([]*domain.IssueStatus, error) {
	if e.statuses == nil {
		s, err := repository.ListIssueStatuses(ctx, e.Q)
		if err != nil {
			return nil, err
		}
		e.statuses = s
	}
	return e.statuses, nil
}

// Status は id のステータス (無ければ nil)。
func (e *Env) Status(ctx context.Context, id int64) (*domain.IssueStatus, error) {
	if id == 0 {
		return nil, nil
	}
	ss, err := e.Statuses(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range ss {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, nil
}

// Trackers は Tracker.sorted (project_ids / custom_field_ids 付き)。
func (e *Env) Trackers(ctx context.Context) ([]*domain.Tracker, error) {
	if e.trackers == nil {
		ts, err := repository.ListTrackers(ctx, e.Q)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if err := e.Q.Select(ctx, &t.ProjectIDs, `SELECT project_id FROM project_trackers WHERE tracker_id = ? ORDER BY project_id`, t.ID); err != nil {
				return nil, err
			}
			ids, err := repository.TrackerCustomFieldIDs(ctx, e.Q, t.ID)
			if err != nil {
				return nil, err
			}
			t.CustomFieldIDs = ids
		}
		e.trackers = ts
	}
	return e.trackers, nil
}

// Tracker は id のトラッカー (無ければ nil)。
func (e *Env) Tracker(ctx context.Context, id int64) (*domain.Tracker, error) {
	if id == 0 {
		return nil, nil
	}
	ts, err := e.Trackers(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

// ProjectTrackers は project.trackers.sorted。
func (e *Env) ProjectTrackers(ctx context.Context, p *domain.Project) ([]*domain.Tracker, error) {
	if p == nil {
		return nil, nil
	}
	ts, err := e.Trackers(ctx)
	if err != nil {
		return nil, err
	}
	var out []*domain.Tracker
	for _, t := range ts {
		if slices.Contains(t.ProjectIDs, p.ID) {
			out = append(out, t)
		}
	}
	return out, nil
}

// trackerIssueStatusIDs は Tracker#issue_status_ids。
func (e *Env) trackerIssueStatusIDs(ctx context.Context, trackerID int64) ([]int64, error) {
	var rows []struct {
		Old *int64 `db:"old_status_id"`
		New int64  `db:"new_status_id"`
	}
	if err := e.Q.Select(ctx, &rows, `SELECT DISTINCT old_status_id, new_status_id FROM workflow_transitions
WHERE tracker_id = ? AND (old_status_id IS NULL OR old_status_id <> new_status_id)`, trackerID); err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range rows {
		// 旧スキーマの old_status_id = 0 (新規) は Redmine の pluck に 0 として含まれる
		o := int64(0)
		if r.Old != nil {
			o = *r.Old
		}
		if !slices.Contains(ids, o) {
			ids = append(ids, o)
		}
		if !slices.Contains(ids, r.New) {
			ids = append(ids, r.New)
		}
	}
	return ids, nil
}

// Priorities は IssuePriority.sorted (共有のもののみ)。
func (e *Env) Priorities(ctx context.Context) ([]*domain.Enumeration, error) {
	if e.priorities == nil {
		ps, err := repository.ListEnumerations(ctx, e.Q, domain.EnumIssuePriority, true)
		if err != nil {
			return nil, err
		}
		e.priorities = ps
	}
	return e.priorities, nil
}

// Priority は id の優先度 (無ければ nil)。
func (e *Env) Priority(ctx context.Context, id int64) (*domain.Enumeration, error) {
	if id == 0 {
		return nil, nil
	}
	ps, err := e.Priorities(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil
}

// DefaultPriority は IssuePriority.default。
func (e *Env) DefaultPriority(ctx context.Context) (*domain.Enumeration, error) {
	ps, err := e.Priorities(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.IsDefault {
			return p, nil
		}
	}
	return nil, nil
}

// defaultOrMiddlePriority は IssuePriority.default_or_middle。
func (e *Env) defaultOrMiddlePriority(ctx context.Context) (*domain.Enumeration, error) {
	if p, err := e.DefaultPriority(ctx); err != nil || p != nil {
		return p, err
	}
	ps, err := e.Priorities(ctx)
	if err != nil {
		return nil, err
	}
	var active []*domain.Enumeration
	for _, p := range ps {
		if p.Active {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return nil, nil
	}
	return active[(len(active)-1)/2], nil
}

// PriorityHigh は IssuePriority#high?。
func (e *Env) PriorityHigh(ctx context.Context, p *domain.Enumeration) (bool, error) {
	if p == nil {
		return false, nil
	}
	base, err := e.defaultOrMiddlePriority(ctx)
	if err != nil || base == nil {
		return false, err
	}
	return p.Position > base.Position, nil
}

// IssueCustomFields は IssueCustomField.sorted。
func (e *Env) IssueCustomFields(ctx context.Context) ([]*customfield.CustomField, error) {
	if e.cfs == nil {
		cfs, err := customfield.ListByKind(ctx, e.Q, customfield.KindIssue)
		if err != nil {
			return nil, err
		}
		e.cfs = cfs
	}
	return e.cfs, nil
}

// CustomField は id のカスタムフィールド (チケット用以外も検索する)。
func (e *Env) CustomField(ctx context.Context, id int64) (*customfield.CustomField, error) {
	cfs, err := e.IssueCustomFields(ctx)
	if err != nil {
		return nil, err
	}
	for _, cf := range cfs {
		if cf.ID == id {
			return cf, nil
		}
	}
	return customfield.Get(ctx, e.Q, id)
}

// Project は id のプロジェクト (キャッシュ。無ければ nil)。
func (e *Env) Project(ctx context.Context, id int64) (*domain.Project, error) {
	if id == 0 {
		return nil, nil
	}
	if e.projects == nil {
		e.projects = map[int64]*domain.Project{}
	}
	if p, ok := e.projects[id]; ok {
		return p, nil
	}
	p, err := repository.GetProject(ctx, e.Q, id)
	if errors.Is(err, repository.ErrNotFound) {
		e.projects[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.projects[id] = p
	return p, nil
}

// UserByID はユーザ (User / AnonymousUser) を返す (無ければ nil)。
func (e *Env) UserByID(ctx context.Context, id int64) (*domain.User, error) {
	if id == 0 {
		return nil, nil
	}
	if e.users == nil {
		e.users = map[int64]*domain.User{}
	}
	if u, ok := e.users[id]; ok {
		return u, nil
	}
	u, err := repository.GetUser(ctx, e.Q, id)
	if errors.Is(err, repository.ErrNotFound) {
		e.users[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.users[id] = u
	return u, nil
}

// Principal は id のプリンシパル (無ければ nil)。
func (e *Env) Principal(ctx context.Context, id int64) (*domain.Principal, error) {
	if id == 0 {
		return nil, nil
	}
	if e.principals == nil {
		e.principals = map[int64]*domain.Principal{}
	}
	if p, ok := e.principals[id]; ok {
		return p, nil
	}
	p, err := repository.GetPrincipal(ctx, e.Q, id)
	if errors.Is(err, repository.ErrNotFound) {
		e.principals[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.principals[id] = p
	return p, nil
}

// groupIDs は user.group_ids。
func (e *Env) groupIDs(ctx context.Context, u *domain.User) ([]int64, error) {
	if u == nil || u.ID == 0 {
		return nil, nil
	}
	return e.authz(u).GroupIDs(ctx)
}

// isOrBelongsTo は user.is_or_belongs_to?(principal)。
func (e *Env) isOrBelongsTo(ctx context.Context, u *domain.User, principalID *int64) (bool, error) {
	if u == nil || principalID == nil {
		return false, nil
	}
	return e.authz(u).IsOrBelongsTo(ctx, *principalID)
}
