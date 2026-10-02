package activity

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
)

// Options は Fetcher.new(user, options) のオプション。
type Options struct {
	// Project は :project（nil なら全プロジェクト）。
	Project *domain.Project
	// WithSubprojects は :with_subprojects。
	WithSubprojects bool
	// Author は :author（nil なら全員）。
	Author *domain.User
}

// Fetcher は Redmine::Activity::Fetcher の移植。
// User.current の Authorizer（Auth）で可視性を判定し、Loc で event_title の文言を作る。
type Fetcher struct {
	Q    db.Queryer
	Auth *authz.Authorizer
	Loc  *i18n.Localizer
	opts Options

	eventTypes []string
	scope      []string
}

// NewFetcher は Fetcher を作り、利用可能なイベント種別（event_types）を求める。
// 初期スコープは event_types。
func NewFetcher(ctx context.Context, q db.Queryer, auth *authz.Authorizer, loc *i18n.Localizer, opts Options) (*Fetcher, error) {
	f := &Fetcher{Q: q, Auth: auth, Loc: loc, opts: opts}
	types, err := f.computeEventTypes(ctx)
	if err != nil {
		return nil, err
	}
	f.eventTypes = types
	f.scope = slices.Clone(types)
	return f, nil
}

// computeEventTypes は Fetcher#event_types: プロジェクト指定時は自身と子孫のいずれかで
// view_<種別> を持つ種別だけに絞る。
func (f *Fetcher) computeEventTypes(ctx context.Context) ([]string, error) {
	all := AvailableEventTypes()
	if f.opts.Project == nil {
		return all, nil
	}
	projects, err := repository.ProjectSelfAndDescendants(ctx, f.Q, f.opts.Project.ID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range all {
		keep := false
		for _, pv := range providersOf(t) {
			perm := permissionFor(t, pv)
			for _, p := range projects {
				ok, err := f.Auth.AllowedTo(ctx, domain.Perm(perm), p)
				if err != nil {
					return nil, err
				}
				if ok {
					keep = true
					break
				}
			}
		}
		if keep {
			out = append(out, t)
		}
	}
	return out, nil
}

// EventTypes は event_types。
func (f *Fetcher) EventTypes() []string { return f.eventTypes }

// Scope は scope（取得対象の種別）。
func (f *Fetcher) Scope() []string { return f.scope }

// ScopeSelect は scope_select（スコープを fn で絞る）。
func (f *Fetcher) ScopeSelect(fn func(t string) bool) {
	var out []string
	for _, t := range f.scope {
		if fn(t) {
			out = append(out, t)
		}
	}
	f.scope = out
}

// SetScopeAll は scope = :all。
func (f *Fetcher) SetScopeAll() { f.scope = slices.Clone(f.eventTypes) }

// SetScopeDefault は scope = :default（default_scope!。event_types で絞らない点も Redmine と同じ）。
func (f *Fetcher) SetScopeDefault() { f.scope = DefaultEventTypes() }

// SetScope は scope = 配列（s & event_types）。
func (f *Fetcher) SetScope(s []string) {
	var out []string
	for _, t := range s {
		if slices.Contains(f.eventTypes, t) && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	f.scope = out
}

// Events は events(from, to, :limit => limit): 期間 [from, to) のイベントを新しい順に返す。
// from / to は日付（UTC の 0 時）で nil なら制限なし。limit が 0 なら件数制限なし。
//
// 並びは Redmine と同じく、プロバイダごとの取得結果を連結してから event_datetime の降順に
// 安定ソートする（参照環境の Ruby の sort は glibc の qsort_r = マージソートで安定）。
// プロバイダの取得順は、件数制限なしなら (日時, id) の昇順（参照環境の SQLite が日時の索引を
// 走査する順）、件数制限ありなら id の降順（reorder(id DESC)）。
func (f *Fetcher) Events(ctx context.Context, from, to *time.Time, limit int) ([]*Event, error) {
	var events []*Event
	for _, t := range f.scope {
		for _, pv := range providersOf(t) {
			es, err := f.findEvents(ctx, t, pv, from, to, limit)
			if err != nil {
				return nil, err
			}
			events = append(events, es...)
		}
	}
	SortByDatetimeDesc(events)
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

// SortByDatetimeDesc は e.sort! {|a, b| b.event_datetime <=> a.event_datetime}（安定）。
func SortByDatetimeDesc(events []*Event) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Datetime.After(events[j].Datetime) })
}
