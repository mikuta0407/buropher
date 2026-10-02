package query

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
)

// Params は Query#build_from_params が読むリクエストパラメータ (Rails の params 相当)。
// ParseParams で url.Values ("f[]", "op[status_id]", "v[status_id][]", "c[]", "t[]", "sort", "group_by",
// "query[group_by]" など。旧名 "fields[]" / "operators[x]" / "values[x][]" も受け付ける) から作る。
type Params struct {
	// Fields は f[] (または fields[])。HasFields はキーが存在したか (Ruby で params[:f] が真か)。
	Fields    []string
	HasFields bool
	// Operators は op[field] (または operators[field])。
	Operators map[string]string
	// Values は v[field][] (または values[field][])。配列で渡されなかった値は ValuesScalar に入る。
	Values       map[string][]string
	ValuesScalar map[string]string
	// Short はその他のパラメータ (短縮表記フィルタ "status_id=o" 等に使う)。
	Short map[string]string

	Columns    []string
	HasColumns bool
	Totals     []string
	HasTotals  bool
	Sort       string
	GroupBy    *string

	DisplayType         string
	DrawRelations       string
	DrawProgressLine    string
	DrawSelectedColumns string

	// Query は params[:query] (保存フォームの値)。
	Query *Defaults

	// TimeEntryQuery の from / to。
	From, To string

	// retrieve_query 用。
	QueryID        string
	SetFilter      bool
	WithoutDefault bool
}

// Defaults は build_from_params の defaults / params[:query] のハッシュ。
type Defaults struct {
	GroupBy             *string
	ColumnNames         []string
	HasColumnNames      bool
	TotalableNames      []string
	HasTotalableNames   bool
	SortCriteria        SortCriteria
	DisplayType         string
	DrawRelations       string
	DrawProgressLine    string
	DrawSelectedColumns string
}

// ParseParams は Rails 形式の url.Values を Params にする。
func ParseParams(v url.Values) Params {
	p := Params{Operators: map[string]string{}, Values: map[string][]string{}, ValuesScalar: map[string]string{}, Short: map[string]string{}}
	var q Defaults
	hasQuery := false
	for key, vals := range v {
		last := ""
		if len(vals) > 0 {
			last = vals[len(vals)-1]
		}
		switch {
		case key == "f[]" || key == "fields[]":
			if !p.HasFields || key == "fields[]" {
				p.Fields = append([]string(nil), vals...)
			}
			p.HasFields = true
		case key == "f" || key == "fields":
			p.HasFields = true
		case bracket(key, "op") != "" || bracket(key, "operators") != "":
			if f := bracket(key, "op"); f != "" {
				if _, legacy := v["operators["+f+"]"]; !legacy {
					p.Operators[f] = last
				}
			} else {
				// 旧名 operators は op より優先 (params[:operators] || params[:op])
				p.Operators[bracket(key, "operators")] = last
			}
		case strings.HasPrefix(key, "v[") || strings.HasPrefix(key, "values["):
			name, isArray := valuesKey(key)
			if name == "" {
				continue
			}
			if isArray {
				p.Values[name] = append(p.Values[name], vals...)
			} else {
				p.ValuesScalar[name] = last
			}
		case key == "c[]":
			p.Columns, p.HasColumns = append([]string(nil), vals...), true
		case key == "t[]":
			p.Totals, p.HasTotals = append([]string(nil), vals...), true
		case key == "sort":
			p.Sort = last
		case key == "group_by":
			g := last
			p.GroupBy = &g
		case key == "display_type":
			p.DisplayType = last
		case key == "draw_relations":
			p.DrawRelations = last
		case key == "draw_progress_line":
			p.DrawProgressLine = last
		case key == "draw_selected_columns":
			p.DrawSelectedColumns = last
		case key == "from":
			p.From = last
		case key == "to":
			p.To = last
		case key == "query_id":
			p.QueryID = last
		case key == "set_filter":
			p.SetFilter = true
		case key == "without_default":
			p.WithoutDefault = strings.TrimSpace(last) != ""
		case strings.HasPrefix(key, "query["):
			hasQuery = true
			switch key {
			case "query[group_by]":
				g := last
				q.GroupBy = &g
			case "query[column_names][]":
				q.ColumnNames, q.HasColumnNames = append([]string(nil), vals...), true
			case "query[totalable_names][]":
				q.TotalableNames, q.HasTotalableNames = append([]string(nil), vals...), true
			case "query[sort_criteria]":
				q.SortCriteria = ParseSortCriteria(last)
			case "query[display_type]":
				q.DisplayType = last
			case "query[draw_relations]":
				q.DrawRelations = last
			case "query[draw_progress_line]":
				q.DrawProgressLine = last
			case "query[draw_selected_columns]":
				q.DrawSelectedColumns = last
			}
		default:
			p.Short[key] = last
		}
	}
	// 旧名を優先する (params[:fields] || params[:f] 等)
	if fs, ok := v["fields[]"]; ok {
		p.Fields = append([]string(nil), fs...)
	}
	if hasQuery {
		p.Query = &q
	}
	return p
}

// bracket は "name[x]" の x を返す (形式が違えば "")。
func bracket(key, name string) string {
	if strings.HasPrefix(key, name+"[") && strings.HasSuffix(key, "]") && !strings.Contains(key[len(name)+1:len(key)-1], "[") {
		return key[len(name)+1 : len(key)-1]
	}
	return ""
}

// valuesKey は "v[field][]" / "v[field]" / "values[field][]" のフィールド名と配列かどうか。
func valuesKey(key string) (string, bool) {
	rest := ""
	switch {
	case strings.HasPrefix(key, "v["):
		rest = key[2:]
	case strings.HasPrefix(key, "values["):
		rest = key[7:]
	}
	i := strings.Index(rest, "]")
	if i < 0 {
		return "", false
	}
	name, tail := rest[:i], rest[i+1:]
	return name, tail == "[]"
}

// BuildFromParams は Query#build_from_params(params, defaults) (と IssueQuery / TimeEntryQuery の上書き)。
func (q *Query) BuildFromParams(ctx context.Context, p Params, defaults *Defaults) error {
	if p.HasFields {
		q.Filters = NewFilters()
		if len(p.Fields) > 0 && len(p.Operators) > 0 {
			for _, field := range p.Fields {
				var values []string
				if vs, ok := p.Values[field]; ok {
					values = vs
				} else if _, ok := p.ValuesScalar[field]; ok {
					// 配列でない値は add_filter が無視する
					continue
				}
				if err := q.AddFilter(ctx, field, p.Operators[field], values); err != nil {
					return err
				}
			}
		}
	} else {
		af, err := q.AvailableFilters(ctx)
		if err != nil {
			return err
		}
		for _, field := range af.Keys() {
			if expr, ok := p.Short[field]; ok {
				if err := q.AddShortFilter(ctx, field, expr); err != nil {
					return err
				}
			}
		}
	}
	qp := p.Query
	if qp == nil {
		qp = defaults
	}
	if qp == nil {
		qp = &Defaults{}
	}
	switch {
	case p.GroupBy != nil:
		q.GroupBy = *p.GroupBy
	case qp.GroupBy != nil:
		q.GroupBy = *qp.GroupBy
	}
	var err error
	switch {
	case p.HasColumns:
		err = q.SetColumnNames(ctx, p.Columns)
	case qp.HasColumnNames:
		err = q.SetColumnNames(ctx, qp.ColumnNames)
	default:
		err = q.SetColumnNames(ctx, q.columnNames)
	}
	if err != nil {
		return err
	}
	switch {
	case p.HasTotals:
		q.SetTotalableNames(p.Totals)
	case qp.HasTotalableNames:
		q.SetTotalableNames(qp.TotalableNames)
	default:
		q.SetTotalableNames(q.TotalableNames())
	}
	switch {
	case p.Sort != "":
		q.SetSortParam(p.Sort)
	case len(qp.SortCriteria) > 0:
		q.SetSortCriteria(qp.SortCriteria)
	default:
		q.SetSortCriteria(q.SortCriteria())
	}
	switch {
	case p.DisplayType != "":
		q.SetDisplayType(p.DisplayType)
	case qp.DisplayType != "":
		q.SetDisplayType(qp.DisplayType)
	default:
		q.SetDisplayType(q.DisplayType())
	}
	switch q.Kind {
	case KindIssue:
		firstOf := func(vs ...string) string {
			for _, v := range vs {
				if v != "" {
					return v
				}
			}
			return ""
		}
		var qDraw, qProg, qSel string
		if p.Query != nil {
			qDraw, qProg, qSel = p.Query.DrawRelations, p.Query.DrawProgressLine, p.Query.DrawSelectedColumns
		}
		q.SetDrawRelations(firstOf(p.DrawRelations, qDraw, q.Options["draw_relations"]))
		q.SetDrawProgressLine(firstOf(p.DrawProgressLine, qProg, q.Options["draw_progress_line"]))
		// Redmine は draw_selected_columns の既定値に options[:draw_progress_line] を使う (原文どおり)
		q.SetDrawSelectedColumns(firstOf(p.DrawSelectedColumns, qSel, q.Options["draw_progress_line"]))
	case KindTimeEntry:
		from, to := strings.TrimSpace(p.From), strings.TrimSpace(p.To)
		switch {
		case from != "" && to != "":
			return q.AddFilter(ctx, "spent_on", "><", []string{p.From, p.To})
		case from != "":
			return q.AddFilter(ctx, "spent_on", ">=", []string{p.From})
		case to != "":
			return q.AddFilter(ctx, "spent_on", "<=", []string{p.To})
		}
	}
	return nil
}

// AsParams は as_params (未保存ならフィルタ・列・グループ・合計・ソートを url.Values に、保存済みなら query_id)。
func (q *Query) AsParams() url.Values {
	v := url.Values{}
	if q.ID != 0 {
		v.Set("query_id", itoa(q.ID))
		return v
	}
	for _, field := range q.Filters.Keys() {
		f, _ := q.Filters.Get(field)
		v.Add("f[]", field)
		v.Set("op["+field+"]", f.Operator)
		for _, x := range f.Values {
			v.Add("v["+field+"][]", x)
		}
	}
	for _, c := range q.columnNames {
		v.Add("c[]", c)
	}
	if q.GroupBy != "" {
		v.Set("group_by", q.GroupBy)
	}
	for _, t := range q.TotalableNames() {
		v.Add("t[]", t)
	}
	v.Set("sort", q.SortCriteria().ToParam())
	v.Set("set_filter", "1")
	return v
}

// ---------------------------------------------------------------- retrieve_query (QueriesHelper)

// SessionState は session[:<kind>_query] の内容 (呼び出し側がセッションに保存する)。
type SessionState struct {
	ID             int64             `json:"id,omitempty"`
	ProjectID      *int64            `json:"project_id"`
	Filters        []SessionFilter   `json:"filters,omitempty"`
	GroupBy        string            `json:"group_by,omitempty"`
	ColumnNames    []string          `json:"column_names,omitempty"`
	TotalableNames []string          `json:"totalable_names,omitempty"`
	Sort           SortCriteria      `json:"sort,omitempty"`
	Extra          map[string]string `json:"extra,omitempty"`
}

// SessionFilter は SessionState のフィルタ (順序を保つため配列で持つ)。
type SessionFilter struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

// ErrNotFound は query_id のクエリが見つからない (ActiveRecord::RecordNotFound)。
var ErrNotFound = errors.New("query: not found")

// ErrUnauthorized は query_id のクエリが見えない (::Unauthorized)。
var ErrUnauthorized = errors.New("query: unauthorized")

// RetrieveOptions は retrieve_query のオプション。
type RetrieveOptions struct {
	// UseSession はセッションを使う (use_session)。
	UseSession bool
	// API は API リクエスト (api_request?)。
	API bool
	// Defaults は options[:defaults]。
	Defaults *Defaults
}

// Retrieve は QueriesHelper#retrieve_query。sess は session[session_key] (nil = 無し)。
// 戻り値の SessionState を呼び出し側がセッションに保存する (UseSession = false なら nil)。
func Retrieve(ctx context.Context, env *Env, kind Kind, project *domain.Project, p Params, sess *SessionState, o RetrieveOptions) (*Query, *SessionState, error) {
	var projectID *int64
	if project != nil {
		id := project.ID
		projectID = &id
	}
	var q *Query
	var err error
	newSess := sess
	switch {
	case strings.TrimSpace(p.QueryID) != "":
		id, ok := parseID(p.QueryID)
		if !ok {
			return nil, sess, ErrNotFound
		}
		q, err = Load(ctx, env, id, kind)
		if err != nil {
			return nil, sess, err
		}
		if q == nil || (q.savedProjectID != nil && (project == nil || *q.savedProjectID != project.ID)) {
			return nil, sess, ErrNotFound
		}
		ok, err = q.VisibleTo(ctx, env.Auth)
		if err != nil {
			return nil, sess, err
		}
		if !ok {
			return nil, sess, ErrUnauthorized
		}
		q.SetProject(project)
		if o.UseSession {
			newSess = &SessionState{ID: q.ID, ProjectID: q.ProjectID()}
		}
	case o.API || p.SetFilter || !o.UseSession || sess == nil || !sameID(sess.ProjectID, projectID):
		q, err = New(ctx, env, kind, project)
		if err != nil {
			return nil, sess, err
		}
		if err := q.BuildFromParams(ctx, p, o.Defaults); err != nil {
			return nil, sess, err
		}
		if o.UseSession {
			newSess = q.sessionState()
		}
	default:
		if sess.ID != 0 {
			q, err = Load(ctx, env, sess.ID, kind)
			if err != nil {
				return nil, sess, err
			}
		}
		if q == nil {
			q, err = New(ctx, env, kind, project)
			if err != nil {
				return nil, sess, err
			}
			if sess.Filters != nil {
				q.Filters = NewFilters()
				for _, f := range sess.Filters {
					q.Filters.Set(f.Field, Filter{Operator: f.Operator, Values: f.Values})
				}
			}
			q.GroupBy = sess.GroupBy
			if err := q.SetColumnNames(ctx, sess.ColumnNames); err != nil {
				return nil, sess, err
			}
			q.SetTotalableNames(sess.TotalableNames)
			q.SetSortCriteria(sess.Sort)
		}
		q.SetProject(project)
	}
	if strings.TrimSpace(p.Sort) != "" {
		q.SetSortParam(p.Sort)
		if o.UseSession {
			if newSess == nil {
				newSess = &SessionState{}
			}
			newSess.Sort = q.SortCriteria()
		}
	}
	if !o.UseSession {
		newSess = nil
	}
	return q, newSess, nil
}

func (q *Query) sessionState() *SessionState {
	s := &SessionState{ProjectID: q.ProjectID(), GroupBy: q.GroupBy, ColumnNames: q.ColumnNames(),
		TotalableNames: q.TotalableNames(), Sort: q.SortCriteria(), Filters: []SessionFilter{}}
	for _, k := range q.Filters.Keys() {
		f, _ := q.Filters.Get(k)
		s.Filters = append(s.Filters, SessionFilter{Field: k, Operator: f.Operator, Values: f.Values})
	}
	return s
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// DefaultQueryID は IssuesController / ProjectsController#retrieve_default_query:
// query_id / set_filter / API 指定が無く、(IssueQuery では) セッションに有効な保存クエリが無ければ
// 既定クエリの id を返す (0 = 適用しない)。without_default が指定されたら setFilter = true を返す。
func DefaultQueryID(ctx context.Context, env *Env, kind Kind, project *domain.Project, p Params, sess *SessionState, useSession, api bool) (id int64, setFilter bool, err error) {
	if strings.TrimSpace(p.QueryID) != "" || api || p.SetFilter {
		return 0, false, nil
	}
	if p.WithoutDefault {
		return 0, true, nil
	}
	if kind == KindIssue && useSession && sess != nil && sess.ID != 0 {
		var pid *int64
		if project != nil {
			v := project.ID
			pid = &v
		}
		if sameID(sess.ProjectID, pid) {
			var n int
			if err := env.Q.Get(ctx, &n, `SELECT COUNT(*) FROM queries WHERE id = ? AND kind = 'issue'`, sess.ID); err != nil {
				return 0, false, err
			}
			if n > 0 {
				return 0, false, nil
			}
		}
	}
	if kind == KindProject {
		project = nil
	}
	q, err := Default(ctx, env, kind, project)
	if err != nil || q == nil {
		return 0, false, err
	}
	return q.ID, false, nil
}
