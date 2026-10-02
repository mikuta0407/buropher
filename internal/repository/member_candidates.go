package repository

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
)

// principalTypeOrder は Redmine の users.type の降順（type DESC）を kind から再現する式。
const principalTypeOrder = `CASE principals.kind WHEN 'user' THEN 5 WHEN 'group_non_member' THEN 4 WHEN 'group_anonymous' THEN 3 WHEN 'group' THEN 2 ELSE 1 END DESC`

// principalLastname はグループ名を lastname として扱う式（Redmine のグループは lastname に名前を持つ）。
const principalLastname = `(CASE WHEN principals.kind IN ('group', 'group_anonymous', 'group_non_member') THEN principals.name ELSE principals.lastname END)`

// PrincipalOrderSQL は Principal.fields_for_order_statement（type DESC + 書式の並び + lastname, id）。
func PrincipalOrderSQL(userFormat string) string {
	var fields []string
	switch userFormat {
	case "lastname_firstname", "lastnamefirstname", "lastname_comma_firstname":
		fields = []string{principalLastname, "principals.firstname"}
	case "firstname":
		fields = []string{"principals.firstname"}
	case "lastname":
		fields = []string{principalLastname}
	case "username":
		fields = []string{"ua.login"}
	default:
		fields = []string{"principals.firstname", principalLastname}
	}
	out := []string{principalTypeOrder}
	seen := map[string]bool{}
	for _, f := range append(fields, principalLastname, "principals.id") {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return strings.Join(out, ", ")
}

// principalLikeCondition は Principal.like(q)（login・メールアドレス・姓名のトークン）。
func principalLikeCondition(q string) (string, []any) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "1=1", nil
	}
	pattern := "%" + likeEscapeSQL(q) + "%"
	sql := `LOWER(ua.login) LIKE LOWER(?) ESCAPE '\' OR principals.id IN (SELECT user_id FROM email_addresses WHERE LOWER(address) LIKE LOWER(?) ESCAPE '\')`
	args := []any{pattern, pattern}
	var toks []string
	for _, t := range strings.Fields(q) {
		tp := "%" + likeEscapeSQL(t) + "%"
		toks = append(toks, `(LOWER(principals.firstname) LIKE LOWER(?) ESCAPE '\' OR LOWER(`+principalLastname+`) LIKE LOWER(?) ESCAPE '\')`)
		args = append(args, tp, tp)
	}
	if len(toks) > 0 {
		sql += " OR (" + strings.Join(toks, " AND ") + ")"
	}
	return "(" + sql + ")", args
}

// NewMemberCandidates は Principal.active.visible.sorted.not_member_of(project).like(q) の件数と
// offset / limit のページ。visible は Principal.visible の条件（principals を参照）。
func NewMemberCandidates(ctx context.Context, q db.Queryer, projectID int64, visible, like, userFormat string, offset, limit int) (int, []int64, error) {
	likeSQL, args := principalLikeCondition(like)
	where := `principals.status = 1 AND principals.kind <> 'anonymous_user' AND (` + andConds(visible) + `)
  AND principals.id NOT IN (SELECT DISTINCT principal_id FROM members WHERE project_id = ?) AND ` + likeSQL
	allArgs := append([]any{projectID}, args...)
	from := ` FROM principals LEFT JOIN user_accounts ua ON ua.principal_id = principals.id WHERE ` + where
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*)`+from, allArgs...); err != nil {
		return 0, nil, err
	}
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT principals.id`+from+` ORDER BY `+PrincipalOrderSQL(userFormat)+` `+q.Dialect().LimitOffset(limit, offset), allArgs...); err != nil {
		return 0, nil, err
	}
	return n, ids, nil
}
