// Package search は Redmine の検索（Redmine::Search::Fetcher / Tokenizer と acts_as_searchable の
// search_result_ranks_and_ids）の移植。
//
// 検索対象の種別は lib/redmine/preparation.rb の Search.map と同じ順序:
// issues, news, documents, changesets, wiki_pages, messages, projects。
// 結果は [日時(秒), id] の降順（同値は種別の登録順）で、表示用のレコードは internal/activity の
// Loader で acts_as_event の属性を評価した Event として返す。
// Redmine の結果 id キャッシュ（Rails cache）は持たず、毎回計算する（結果は同じ）。
package search

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/query"
)

// AvailableSearchTypes は Redmine::Search.available_search_types。
func AvailableSearchTypes() []string {
	return []string{"issues", "news", "documents", "changesets", "wiki_pages", "messages", "projects"}
}

// Tokenize は Redmine::Search::Tokenizer#tokens（"..." の句・空白区切り、重複除去、
// 2 文字以上（漢字は 1 文字可）、最大 5 個）。
func Tokenize(question string) []string { return query.Tokenize(question) }

// Options は Fetcher のオプション（:all_words, :titles_only, :attachments, :open_issues）。
type Options struct {
	AllWords   bool
	TitlesOnly bool
	// Attachments は "0"（検索しない）/ "1"（含める）/ "only"（添付のみ）。
	Attachments string
	OpenIssues  bool
}

// Fetcher は Redmine::Search::Fetcher。
type Fetcher struct {
	Q    db.Queryer
	Auth *authz.Authorizer
	Loc  *i18n.Localizer

	tokens []string
	scope  []string
	// projects は検索対象のプロジェクト id（nil なら全プロジェクト、空なら結果なし）。
	projects []int64
	opts     Options

	ids    []resultID
	loaded bool
}

type resultID struct {
	scope string
	ts    int64
	id    int64
}

// NewFetcher は Fetcher.new(question, user, scope, projects, options)。
// projects が nil なら全プロジェクト、空でない（または空の）スライスならその id に限定する。
func NewFetcher(q db.Queryer, auth *authz.Authorizer, loc *i18n.Localizer, question string, scope []string, projects []int64, opts Options) *Fetcher {
	return &Fetcher{Q: q, Auth: auth, Loc: loc, tokens: Tokenize(strings.TrimSpace(question)), scope: scope, projects: projects, opts: opts}
}

// Tokens は tokens。
func (f *Fetcher) Tokens() []string { return f.tokens }

// ResultCount は result_count。
func (f *Fetcher) ResultCount(ctx context.Context) (int, error) {
	if err := f.load(ctx); err != nil {
		return 0, err
	}
	return len(f.ids), nil
}

// ResultCountByType は result_count_by_type。
func (f *Fetcher) ResultCountByType(ctx context.Context) (map[string]int, error) {
	if err := f.load(ctx); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range f.ids {
		out[r.scope]++
	}
	return out, nil
}

// TypeOrder は result_count_by_type のキーの順（result_ids に最初に現れた順）。
func (f *Fetcher) TypeOrder() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range f.ids {
		if !seen[r.scope] {
			seen[r.scope] = true
			out = append(out, r.scope)
		}
	}
	return out
}

// Results は results(offset, limit)（結果の順序で Event を返す）。
func (f *Fetcher) Results(ctx context.Context, offset, limit int) ([]*activity.Event, error) {
	if err := f.load(ctx); err != nil {
		return nil, err
	}
	if offset < 0 || offset > len(f.ids) {
		return nil, nil
	}
	end := offset + limit
	if end > len(f.ids) {
		end = len(f.ids)
	}
	page := f.ids[offset:end]
	byScope := map[string][]int64{}
	var order []string
	for _, r := range page {
		if _, ok := byScope[r.scope]; !ok {
			order = append(order, r.scope)
		}
		byScope[r.scope] = append(byScope[r.scope], r.id)
	}
	l := &activity.Loader{Q: f.Q, Auth: f.Auth, Loc: f.Loc}
	loaded := map[string]map[int64]*activity.Event{}
	for _, s := range order {
		es, err := loadEvents(ctx, l, s, byScope[s])
		if err != nil {
			return nil, err
		}
		m := map[int64]*activity.Event{}
		for _, e := range es {
			m[e.ID] = e
		}
		loaded[s] = m
	}
	var out []*activity.Event
	for _, r := range page {
		if e := loaded[r.scope][r.id]; e != nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// loadEvents は klass.search_results_from_ids(ids)。
func loadEvents(ctx context.Context, l *activity.Loader, scope string, ids []int64) ([]*activity.Event, error) {
	in := idList(ids)
	switch scope {
	case "issues":
		return l.Issues(ctx, "issues.id IN ("+in+")", nil, "")
	case "news":
		return l.News(ctx, "news.id IN ("+in+")", nil, "")
	case "documents":
		return l.Documents(ctx, "documents.id IN ("+in+")", nil, "")
	case "changesets":
		return l.Changesets(ctx, "changesets.id IN ("+in+")", nil, "")
	case "wiki_pages":
		return l.WikiPages(ctx, "wiki_pages.id IN ("+in+")", nil, "")
	case "messages":
		return l.Messages(ctx, "messages.id IN ("+in+")", nil, "")
	case "projects":
		return l.Projects(ctx, "projects.id IN ("+in+")", nil, "")
	}
	return nil, nil
}

// load は result_ids（load_result_ids）。
func (f *Fetcher) load(ctx context.Context) error {
	if f.loaded {
		return nil
	}
	var all []resultID
	for _, s := range f.scope {
		def, ok := searchables[s]
		if !ok {
			continue
		}
		rs, err := f.rankAndIDs(ctx, def)
		if err != nil {
			return err
		}
		for _, r := range rs {
			all = append(all, resultID{scope: s, ts: r.ts, id: r.id})
		}
	}
	// ret.sort! {|a, b| b.last <=> a.last}（参照環境の Ruby の sort は安定）
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].ts != all[j].ts {
			return all[i].ts > all[j].ts
		}
		return all[i].id > all[j].id
	})
	f.ids = all
	f.loaded = true
	return nil
}

func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}
