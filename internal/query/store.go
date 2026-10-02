package query

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// 保存クエリ (queries テーブル) の読み書きと可視性。

type queryRow struct {
	ID             int64          `db:"id"`
	Kind           string         `db:"kind"`
	ProjectID      sql.NullInt64  `db:"project_id"`
	UserID         sql.NullInt64  `db:"user_id"`
	Name           string         `db:"name"`
	Description    sql.NullString `db:"description"`
	Visibility     int            `db:"visibility"`
	Filters        db.RawJSON     `db:"filters"`
	ColumnNames    db.RawJSON     `db:"column_names"`
	SortCriteria   db.RawJSON     `db:"sort_criteria"`
	GroupBy        sql.NullString `db:"group_by"`
	TotalableNames db.RawJSON     `db:"totalable_names"`
	DisplayType    sql.NullString `db:"display_type"`
	Options        db.RawJSON     `db:"options"`
}

const queryCols = `queries.id, queries.kind, queries.project_id, queries.user_id, queries.name, queries.description,
  queries.visibility, queries.filters, queries.column_names, queries.sort_criteria, queries.group_by,
  queries.totalable_names, queries.display_type, queries.options`

// Load は id の保存クエリを読み込み、env を束縛して返す (無ければ nil, nil)。
// kind が空でなければその種別のクエリのみ (IssueQuery.find 等)。可視性は確認しない。
func Load(ctx context.Context, env *Env, id int64, kind Kind) (*Query, error) {
	var r queryRow
	where := "queries.id = ?"
	args := []any{id}
	if kind != "" {
		where += " AND queries.kind = ?"
		args = append(args, string(kind))
	}
	if err := env.Q.Get(ctx, &r, `SELECT `+queryCols+` FROM queries WHERE `+where, args...); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return fromRow(ctx, env, &r)
}

func fromRow(ctx context.Context, env *Env, r *queryRow) (*Query, error) {
	impl, err := implFor(Kind(r.Kind))
	if err != nil {
		return nil, err
	}
	q := &Query{ID: r.ID, Kind: Kind(r.Kind), Name: r.Name, Description: r.Description.String, Visibility: r.Visibility,
		GroupBy: r.GroupBy.String, displayType: r.DisplayType.String, Options: map[string]string{}, env: env, impl: impl}
	if r.ProjectID.Valid {
		p, err := repository.GetProject(ctx, env.Q, r.ProjectID.Int64)
		if err != nil {
			return nil, err
		}
		q.Project = p
		pid := r.ProjectID.Int64
		q.savedProjectID = &pid
	}
	if r.UserID.Valid {
		u := r.UserID.Int64
		q.UserID = &u
	}
	q.Filters, err = parseFilters(r.Filters)
	if err != nil {
		return nil, fmt.Errorf("query %d: filters: %w", r.ID, err)
	}
	if len(r.ColumnNames) > 0 {
		var cn []any
		if err := json.Unmarshal(r.ColumnNames, &cn); err == nil && cn != nil {
			q.columnNames = []string{}
			for _, c := range cn {
				q.columnNames = append(q.columnNames, fmt.Sprint(c))
			}
		}
	}
	if len(r.SortCriteria) > 0 {
		var sc []any
		if err := json.Unmarshal(r.SortCriteria, &sc); err == nil {
			for _, e := range sc {
				switch x := e.(type) {
				case []any:
					var pair [2]string
					if len(x) > 0 {
						pair[0] = fmt.Sprint(x[0])
					}
					if len(x) > 1 {
						if b, ok := x[len(x)-1].(bool); ok && !b {
							pair[1] = "desc"
						} else {
							pair[1] = fmt.Sprint(x[len(x)-1])
						}
					}
					q.sortCriteria = append(q.sortCriteria, pair)
				case string:
					q.sortCriteria = append(q.sortCriteria, [2]string{x, x})
				}
			}
			q.sortCriteria = q.sortCriteria.normalize()
		}
	}
	if len(r.TotalableNames) > 0 {
		var tn []any
		if err := json.Unmarshal(r.TotalableNames, &tn); err == nil && tn != nil {
			names := []string{}
			for _, t := range tn {
				names = append(names, fmt.Sprint(t))
			}
			q.SetTotalableNames(names)
		}
	}
	if len(r.Options) > 0 {
		var opts map[string]any
		if err := json.Unmarshal(r.Options, &opts); err == nil {
			for k, v := range opts {
				if v != nil {
					q.Options[k] = fmt.Sprint(v)
				}
			}
		}
	}
	if err := q.loadRoleIDs(ctx); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Query) loadRoleIDs(ctx context.Context) error {
	q.RoleIDs = nil
	return q.env.Q.Select(ctx, &q.RoleIDs, `SELECT role_id FROM queries_roles WHERE query_id = ? ORDER BY role_id`, q.ID)
}

// parseFilters は filters JSON ({"field": {"operator": .., "values": [..]}}) をキー順を保って読む。
func parseFilters(raw db.RawJSON) (*Filters, error) {
	f := NewFilters()
	if len(raw) == 0 || string(raw) == "null" {
		return f, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return f, nil
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		var v struct {
			Operator any   `json:"operator"`
			Values   []any `json:"values"`
		}
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		flt := Filter{Operator: anyToS(v.Operator)}
		if v.Values != nil {
			flt.Values = make([]string, len(v.Values))
			for i, x := range v.Values {
				flt.Values[i] = anyToS(x)
			}
		}
		f.Set(key, flt)
	}
	return f, nil
}

func anyToS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return rawString(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

// filtersJSON はキー順を保った filters の JSON。
func (q *Query) filtersJSON() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range q.Filters.Keys() {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		f, _ := q.Filters.Get(k)
		vals := f.Values
		if vals == nil {
			vals = []string{}
		}
		vb, _ := json.Marshal(map[string]any{"operator": f.Operator, "values": vals})
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.String()
}

// ErrInvalid は検証エラーで保存できない場合のエラー。
type ErrInvalid struct{ Messages []string }

func (e *ErrInvalid) Error() string { return "query: invalid: " + strings.Join(e.Messages, ", ") }

// Save はクエリを保存する (新規なら INSERT、既存なら UPDATE)。検証に失敗したら *ErrInvalid。
// 可視性がロール限定以外なら queries_roles を消し、IssueQuery が公開でなくなったら
// プロジェクトの既定クエリ参照を外す (after_save / after_update)。
func (q *Query) Save(ctx context.Context) error {
	errs, err := q.Errors(ctx)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return &ErrInvalid{Messages: errs}
	}
	var columns, sortCrit, totalable, display any
	if q.columnNames != nil {
		b, _ := json.Marshal(q.columnNames)
		columns = string(b)
	}
	if len(q.sortCriteria) > 0 {
		b, _ := json.Marshal(q.sortCriteria)
		sortCrit = string(b)
	}
	if t := q.storedTotalableNames(); t != nil {
		b, _ := json.Marshal(t)
		totalable = string(b)
	}
	if q.displayType != "" {
		display = q.displayType
	}
	opts, _ := json.Marshal(q.Options)
	// group_by は params に無ければ既存値（新規は nil）のまま、フォームの空選択なら "" になる (Redmine は両方あり得る)。
	// buropher の Query は両者を区別せず、どちらも blank として扱われ表示に差が無いため空は NULL で保存する。
	var groupBy any
	if q.GroupBy != "" {
		groupBy = q.GroupBy
	}
	// description はフォームの text_field から常に送られるので "" もそのまま保存する (D-17)
	desc := q.Description
	return withTx(ctx, q.env.Q, func(tx db.Queryer) error {
		if q.ID == 0 {
			id, err := tx.InsertReturningID(ctx, `INSERT INTO queries (kind, project_id, user_id, name, description, visibility, filters,
  column_names, sort_criteria, group_by, totalable_names, display_type, options) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				string(q.Kind), q.ProjectID(), q.UserID, q.Name, desc, q.Visibility, q.filtersJSON(), columns, sortCrit, groupBy,
				totalable, display, string(opts))
			if err != nil {
				return err
			}
			q.ID = id
		} else {
			if _, err := tx.Exec(ctx, `UPDATE queries SET kind = ?, project_id = ?, user_id = ?, name = ?, description = ?, visibility = ?,
  filters = ?, column_names = ?, sort_criteria = ?, group_by = ?, totalable_names = ?, display_type = ?, options = ? WHERE id = ?`,
				string(q.Kind), q.ProjectID(), q.UserID, q.Name, desc, q.Visibility, q.filtersJSON(), columns, sortCrit, groupBy,
				totalable, display, string(opts), q.ID); err != nil {
				return err
			}
			if q.Kind == KindIssue && q.Visibility != VisibilityPublic {
				if _, err := tx.Exec(ctx, `UPDATE projects SET default_issue_query_id = NULL WHERE default_issue_query_id = ?`, q.ID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM queries_roles WHERE query_id = ?`, q.ID); err != nil {
			return err
		}
		if q.Visibility == VisibilityRoles {
			for _, rid := range slices.Compact(slices.Sorted(slices.Values(q.RoleIDs))) {
				if _, err := tx.Exec(ctx, `INSERT INTO queries_roles (query_id, role_id) VALUES (?, ?)`, q.ID, rid); err != nil {
					return err
				}
			}
		} else {
			q.RoleIDs = nil
		}
		if pid := q.ProjectID(); pid != nil {
			v := *pid
			q.savedProjectID = &v
		} else {
			q.savedProjectID = nil
		}
		return nil
	})
}

// Delete は保存クエリを削除する (queries_roles と既定クエリ参照は FK で消える)。
func (q *Query) Delete(ctx context.Context) error {
	if q.ID == 0 {
		return nil
	}
	_, err := q.env.Q.Exec(ctx, `DELETE FROM queries WHERE id = ?`, q.ID)
	return err
}

func withTx(ctx context.Context, q db.Queryer, fn func(db.Queryer) error) error {
	if d, ok := q.(*db.DB); ok {
		return d.WithTx(ctx, func(tx *db.Tx) error { return fn(tx) })
	}
	return fn(q)
}

// ---------------------------------------------------------------- 可視性

// VisibleCondition は <Kind>Query.visible(user) スコープの WHERE 句断片
// (queries と、LEFT OUTER JOIN projects ON queries.project_id = projects.id の projects を参照)。
// ProjectQuery は種別も絞る。ProjectAdminQuery / UserQuery は管理者のみ。
func VisibleCondition(ctx context.Context, a *authz.Authorizer, kind Kind) (string, error) {
	u := a.User()
	kindCond := "queries.kind = '" + string(kind) + "'"
	switch kind {
	case KindProjectAdmin, KindUser:
		if u.IsAdmin() {
			return kindCond, nil
		}
		return "1=0", nil
	}
	impl, err := implFor(kind)
	if err != nil {
		return "", err
	}
	base, err := a.AllowedToCondition(ctx, impl.viewPermission(), authz.ConditionOptions{}, nil)
	if err != nil {
		return "", err
	}
	s := kindCond + " AND (queries.project_id IS NULL OR (" + base + "))"
	uid := itoa(u.ID)
	pids, err := a.ProjectIDs(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case u.IsAdmin():
		s += " AND (queries.visibility <> 0 OR queries.user_id = " + uid + ")"
	case len(pids) > 0:
		s += " AND (queries.visibility = 2" +
			" OR (queries.visibility = 1 AND EXISTS (SELECT 1 FROM queries_roles qr" +
			" INNER JOIN member_roles mr ON mr.role_id = qr.role_id" +
			" INNER JOIN members m ON m.id = mr.member_id AND m.principal_id = " + uid +
			" INNER JOIN projects p ON p.id = m.project_id AND p.status <> " + itoa(domain.ProjectStatusArchived) +
			" WHERE qr.query_id = queries.id AND (queries.project_id IS NULL OR queries.project_id = m.project_id)))" +
			" OR queries.user_id = " + uid + ")"
	case u.Logged():
		s += " AND (queries.visibility = 2 OR queries.user_id = " + uid + ")"
	default:
		s += " AND queries.visibility = 2"
	}
	return s, nil
}

// SavedQuery は保存クエリの一覧項目。
type SavedQuery struct {
	ID          int64
	Kind        Kind
	ProjectID   *int64
	UserID      *int64
	Name        string
	Description string
	Visibility  int
}

// ListVisible は user に見える保存クエリを sorted (name, id) 順で返す。
// project が nil でなければ global_or_on_project(project) で絞る (onlyGlobal なら全体クエリのみ)。
func ListVisible(ctx context.Context, a *authz.Authorizer, kind Kind, project *domain.Project, onlyGlobal bool) ([]SavedQuery, error) {
	cond, err := VisibleCondition(ctx, a, kind)
	if err != nil {
		return nil, err
	}
	switch {
	case onlyGlobal:
		cond += " AND queries.project_id IS NULL"
	case project != nil:
		cond += " AND (queries.project_id IS NULL OR queries.project_id = " + itoa(project.ID) + ")"
	}
	var rows []struct {
		ID          int64          `db:"id"`
		Kind        string         `db:"kind"`
		ProjectID   sql.NullInt64  `db:"project_id"`
		UserID      sql.NullInt64  `db:"user_id"`
		Name        string         `db:"name"`
		Description sql.NullString `db:"description"`
		Visibility  int            `db:"visibility"`
	}
	if err := a.Queryer().Select(ctx, &rows, `SELECT queries.id, queries.kind, queries.project_id, queries.user_id, queries.name,
  queries.description, queries.visibility FROM queries LEFT OUTER JOIN projects ON queries.project_id = projects.id
WHERE `+cond+` ORDER BY queries.name, queries.id`); err != nil {
		return nil, err
	}
	out := make([]SavedQuery, len(rows))
	for i, r := range rows {
		out[i] = SavedQuery{ID: r.ID, Kind: Kind(r.Kind), Name: r.Name, Description: r.Description.String, Visibility: r.Visibility}
		if r.ProjectID.Valid {
			v := r.ProjectID.Int64
			out[i].ProjectID = &v
		}
		if r.UserID.Valid {
			v := r.UserID.Int64
			out[i].UserID = &v
		}
	}
	return out, nil
}

// ListVisiblePage は queries#index の <Kind>Query.visible.order(name).limit(limit).offset(offset) と件数を返す
// （同名の並びを安定させるため id を第 2 キーにする）。limit が 0 以下なら全件。
func ListVisiblePage(ctx context.Context, a *authz.Authorizer, kind Kind, offset, limit int) ([]SavedQuery, int, error) {
	all, err := ListVisible(ctx, a, kind, nil, false)
	if err != nil {
		return nil, 0, err
	}
	total := len(all)
	if offset > total {
		offset = total
	}
	if offset < 0 {
		offset = 0
	}
	all = all[offset:]
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all, total, nil
}

// VisibleTo は Query#visible?(user)。
func (q *Query) VisibleTo(ctx context.Context, a *authz.Authorizer) (bool, error) {
	u := a.User()
	switch q.Kind {
	case KindProjectAdmin, KindUser:
		return u.IsAdmin(), nil
	}
	if u.IsAdmin() {
		return true, nil
	}
	if q.Project != nil {
		ok, err := a.AllowedTo(ctx, domain.Perm(q.ViewPermission()), q.Project)
		if err != nil || !ok {
			return false, err
		}
	}
	switch q.Visibility {
	case VisibilityPublic:
		return true, nil
	case VisibilityRoles:
		if q.Project != nil {
			roles, err := a.RolesForProject(ctx, q.Project)
			if err != nil {
				return false, err
			}
			for _, r := range roles {
				if slices.Contains(q.RoleIDs, r.ID) {
					return true, nil
				}
			}
			return false, nil
		}
		if len(q.RoleIDs) == 0 || u.ID == 0 {
			return false, nil
		}
		var n int
		err := a.Queryer().Get(ctx, &n, `SELECT COUNT(*) FROM members m JOIN projects p ON p.id = m.project_id
JOIN member_roles mr ON mr.member_id = m.id WHERE m.principal_id = ? AND p.status <> ? AND mr.role_id IN (`+idList(q.RoleIDs)+`)`,
			u.ID, domain.ProjectStatusArchived)
		return n > 0, err
	}
	return q.UserID != nil && u.ID != 0 && *q.UserID == u.ID && u.Logged(), nil
}

// EditableBy は Query#editable_by?(user)。
func (q *Query) EditableBy(ctx context.Context, a *authz.Authorizer) (bool, error) {
	u := a.User()
	if u == nil {
		return false, nil
	}
	switch q.Kind {
	case KindProjectAdmin, KindUser:
		return u.IsAdmin(), nil
	}
	if u.IsAdmin() || (q.IsPrivate() && q.UserID != nil && *q.UserID == u.ID) {
		return true, nil
	}
	if !q.IsPublic() || q.IsGlobal() {
		return false, nil
	}
	return a.AllowedTo(ctx, domain.Perm("manage_public_queries"), q.Project)
}

// Default は <Kind>Query.default(project:, user:) (env.User() が user)。
//   - IssueQuery: 個人設定の既定クエリ (可視なら) → プロジェクトの既定クエリ (公開のみ) → Setting.default_issue_query (公開のみ)
//   - ProjectQuery: 個人設定の既定クエリ (可視なら) → Setting.default_project_query (公開のみ)
//   - その他: nil
func Default(ctx context.Context, env *Env, kind Kind, project *domain.Project) (*Query, error) {
	u := env.User()
	var prefCol, settingName string
	switch kind {
	case KindIssue:
		prefCol, settingName = "default_issue_query_id", "default_issue_query"
	case KindProject:
		prefCol, settingName = "default_project_query_id", "default_project_query"
	default:
		return nil, nil
	}
	if u.Logged() && u.ID != 0 {
		var qid sql.NullInt64
		err := env.Q.Get(ctx, &qid, `SELECT `+prefCol+` FROM user_preferences WHERE user_id = ?`, u.ID)
		if err != nil && !isNoRows(err) {
			return nil, err
		}
		if qid.Valid {
			q, err := Load(ctx, env, qid.Int64, kind)
			if err != nil {
				return nil, err
			}
			if q != nil {
				ok, err := q.VisibleTo(ctx, env.Auth)
				if err != nil {
					return nil, err
				}
				if ok {
					return q, nil
				}
			}
		}
	}
	if kind == KindIssue && project != nil && project.DefaultIssueQueryID != nil {
		q, err := Load(ctx, env, *project.DefaultIssueQueryID, KindIssue)
		if err != nil {
			return nil, err
		}
		if q != nil && q.Visibility == VisibilityPublic {
			return q, nil
		}
	}
	if s := strings.TrimSpace(env.setting(settingName)); s != "" {
		if id, ok := parseID(s); ok {
			q, err := Load(ctx, env, id, kind)
			if err != nil {
				return nil, err
			}
			if q != nil && q.Visibility == VisibilityPublic {
				return q, nil
			}
		}
	}
	return nil, nil
}
