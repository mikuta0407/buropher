package query

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
)

// dateColumns は DATE 型の列 (それ以外の日時フィルタ対象列は UTC タイムスタンプ)。
var dateColumns = map[string]bool{
	"issues.start_date": true, "issues.due_date": true, "versions.effective_date": true,
	"time_entries.spent_on": true,
}

// intColumns は整数列。PostgreSQL で文字列値を比較して型エラーにならないよう、
// "=" / "!" の値を整数に解釈できるものだけに絞る (SQLite の Redmine では一致しないだけ)。
func isIntColumn(table, col string) bool {
	if table == "custom_values" || table == "issue_journal_details" || (table == "versions" && col == "status") {
		return false
	}
	switch col {
	case "id", "status", "done_ratio", "position", "tyear", "tmonth", "tweek":
		return true
	}
	return strings.HasSuffix(col, "_id")
}

// boolColumns は真偽列 (値 "1"/"0"/"t"/"f"/"true"/"false" を真偽に変換して比較する)。
func isBoolColumn(table, col string) bool {
	switch col {
	case "is_public", "is_private", "admin", "is_closed":
		return table != "custom_values"
	}
	return false
}

var reInts = regexp.MustCompile(`[+-]?\d+`)

// sqlForField は sql_for_field(field, operator, value, db_table, db_field, is_custom_filter)。
// db_field は新スキーマの列名。
func (q *Query) sqlForField(ctx context.Context, field, operator string, value []string, table, column string, isCustom bool) (frag, error) {
	col := table + "." + column
	typ, err := q.TypeFor(ctx, field)
	if err != nil {
		return frag{}, err
	}
	first := ""
	if len(value) > 0 {
		first = value[0]
	}
	isDate := typ == "date" || typ == "date_past"
	switch operator {
	case "=":
		if len(value) == 0 {
			return raw("1=0"), nil
		}
		switch typ {
		case "date", "date_past":
			d, ok := q.parseDate(first)
			return q.dateClause(table, column, d, ok, d, ok, isCustom), nil
		case "integer":
			ints := intsOf(first)
			if len(ints) == 0 {
				return raw("1=0"), nil
			}
			list := joinInts(ints)
			if isCustom {
				return raw("(" + col + " <> '' AND CAST(CASE " + col + " WHEN '' THEN '0' ELSE " + col + " END AS decimal(30,3)) IN (" + list + "))"), nil
			}
			return raw(col + " IN (" + list + ")"), nil
		case "float":
			f := customfield.RubyToF(first)
			if isCustom {
				return sqlf("("+col+" <> '' AND CAST(CASE "+col+" WHEN '' THEN '0' ELSE "+col+" END AS decimal(30,3)) BETWEEN ? AND ?)", f-1e-5, f+1e-5), nil
			}
			return sqlf(col+" BETWEEN ? AND ?", f-1e-5, f+1e-5), nil
		}
		in := q.typedIn(table, column, value)
		if in.empty() {
			return raw("1=0"), nil
		}
		return concat(raw(col+" IN ("), in, raw(")")), nil
	case "!":
		if len(value) == 0 {
			return raw("1=1"), nil
		}
		in := q.typedIn(table, column, value)
		if in.empty() {
			// 型に合う値が無い: NOT IN は常に真 (NULL も含む)
			return raw("1=1"), nil
		}
		return concat(raw("("+col+" IS NULL OR "+col+" NOT IN ("), in, raw("))")), nil
	case "!*":
		s := col + " IS NULL"
		if isCustom || typ == "text" || typ == "string" {
			s += " OR " + col + " = ''"
		}
		return raw(s), nil
	case "*":
		s := col + " IS NOT NULL"
		if isCustom || typ == "text" || typ == "string" {
			s += " AND " + col + " <> ''"
		}
		return raw(s), nil
	case ">=", "<=":
		if isDate {
			d, ok := q.parseDate(first)
			if operator == ">=" {
				return q.dateClause(table, column, d, ok, dateArg{}, false, isCustom), nil
			}
			return q.dateClause(table, column, dateArg{}, false, d, ok, isCustom), nil
		}
		f := customfield.RubyToF(first)
		if isCustom {
			return sqlf("("+col+" <> '' AND CAST(CASE "+col+" WHEN '' THEN '0' ELSE "+col+" END AS decimal(30,3)) "+operator+" ?)", f), nil
		}
		return sqlf(col+" "+operator+" ?", f), nil
	case "><":
		if isDate {
			var second string
			if len(value) > 1 {
				second = value[1]
			}
			d1, ok1 := q.parseDate(first)
			d2, ok2 := q.parseDate(second)
			return q.dateClause(table, column, d1, ok1, d2, ok2, isCustom), nil
		}
		var second string
		if len(value) > 1 {
			second = value[1]
		}
		f1, f2 := customfield.RubyToF(first), customfield.RubyToF(second)
		if isCustom {
			return sqlf("("+col+" <> '' AND CAST(CASE "+col+" WHEN '' THEN '0' ELSE "+col+" END AS decimal(30,3)) BETWEEN ? AND ?)", f1, f2), nil
		}
		return sqlf(col+" BETWEEN ? AND ?", f1, f2), nil
	case "o", "c":
		if field == "status_id" {
			return raw(q.impl.queriedTable() + ".status_id IN (SELECT id FROM issue_statuses WHERE is_closed=" + q.env.boolLit(operator == "c") + ")"), nil
		}
		return raw(""), nil
	case "><t-":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, -n, true, 0, true, isCustom), nil
	case ">t-":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, -n, true, 0, false, isCustom), nil
	case "<t-":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, 0, false, -n, true, isCustom), nil
	case "t-":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, -n, true, -n, true, isCustom), nil
	case "><t+":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, 0, true, n, true, isCustom), nil
	case ">t+":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, n, true, 0, false, isCustom), nil
	case "<t+":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, 0, false, n, true, isCustom), nil
	case "t+":
		n := int(customfield.RubyToI(first))
		return q.relativeDateClause(table, column, n, true, n, true, isCustom), nil
	case "t":
		return q.relativeDateClause(table, column, 0, true, 0, true, isCustom), nil
	case "ld":
		return q.relativeDateClause(table, column, -1, true, -1, true, isCustom), nil
	case "nd":
		return q.relativeDateClause(table, column, 1, true, 1, true, isCustom), nil
	case "w", "lw", "l2w", "nw":
		daysAgo := q.daysSinceFirstDayOfWeek()
		switch operator {
		case "w":
			return q.relativeDateClause(table, column, -daysAgo, true, -daysAgo+6, true, isCustom), nil
		case "lw":
			return q.relativeDateClause(table, column, -daysAgo-7, true, -daysAgo-1, true, isCustom), nil
		case "l2w":
			return q.relativeDateClause(table, column, -daysAgo-14, true, -daysAgo-1, true, isCustom), nil
		default:
			from := -daysAgo + 7
			return q.relativeDateClause(table, column, from, true, from+6, true, isCustom), nil
		}
	case "m", "lm", "nm":
		today := q.env.Today()
		y, m, _ := today.Date()
		switch operator {
		case "lm":
			m--
		case "nm":
			m++
		}
		bom := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		eom := bom.AddDate(0, 1, -1)
		return q.dateClause(table, column, dateArg{date: bom}, true, dateArg{date: eom}, true, isCustom), nil
	case "y":
		y := q.env.Today().Year()
		return q.dateClause(table, column, dateArg{date: time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)}, true,
			dateArg{date: time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC)}, true, isCustom), nil
	case "~":
		return q.sqlContains(col, first, containsOpts{}), nil
	case "!~":
		// '' は「含まない」に一致し、NULL は一致しない (Redmine と同じく保存値のまま評価する。D-17)
		return q.sqlContains(col, first, containsOpts{notMatch: true}), nil
	case "*~":
		return q.sqlContains(col, first, containsOpts{anyWord: true}), nil
	case "^":
		return q.sqlContains(col, first, containsOpts{startsWith: true}), nil
	case "$":
		return q.sqlContains(col, first, containsOpts{endsWith: true}), nil
	case "ev", "!ev", "cf":
		if q.Kind != KindIssue || len(value) == 0 {
			return raw("1=0"), nil
		}
		notes, err := q.visibleNotesCondition(ctx, "issue_journals")
		if err != nil {
			return frag{}, err
		}
		neg := ""
		if strings.HasPrefix(operator, "!") {
			neg = "NOT"
		}
		sub := concat(raw("SELECT 1 FROM issue_journals INNER JOIN issue_journal_details ON issue_journals.id = issue_journal_details.journal_id"+
			" WHERE ("+notes+" AND issue_journals.issue_id = "+table+".id AND issue_journal_details.property = 'attr'"+
			" AND issue_journal_details.prop_key = "), sqlf("?", column), raw(" AND issue_journal_details.old_value IN ("), inList(value), raw("))"))
		ev := frag{}
		if operator == "ev" || operator == "!ev" {
			in := q.typedIn(table, column, value)
			if in.empty() {
				ev = raw(" OR 1=0")
			} else {
				ev = concat(raw(" OR "+col+" IN ("), in, raw(")"))
			}
		}
		return concat(raw(neg+" (EXISTS ("), sub, raw(")"), ev, raw(")")), nil
	}
	return frag{}, fmt.Errorf("query: unknown query operator %s", operator)
}

// typedIn は列の型に合わせて IN 句の値を作る (整数列は整数に解釈できる値だけ、真偽列は真偽値)。
func (q *Query) typedIn(table, column string, values []string) frag {
	switch {
	case isIntColumn(table, column):
		var ints []string
		for _, v := range values {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				ints = append(ints, strconv.FormatInt(n, 10))
			}
		}
		if len(ints) == 0 {
			return frag{}
		}
		return raw(strings.Join(slices.Compact(ints), ", "))
	case isBoolColumn(table, column):
		var lits []string
		for _, v := range values {
			switch strings.ToLower(v) {
			case "1", "t", "true":
				lits = append(lits, q.env.boolLit(true))
			case "0", "f", "false":
				lits = append(lits, q.env.boolLit(false))
			}
		}
		if len(lits) == 0 {
			return frag{}
		}
		return raw(strings.Join(lits, ", "))
	}
	return inList(values)
}

func intsOf(s string) []int64 {
	var out []int64
	for _, m := range reInts.FindAllString(s, -1) {
		n, err := strconv.ParseInt(m, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func joinInts(ints []int64) string {
	parts := make([]string, len(ints))
	for i, n := range ints {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, ",")
}

// daysSinceFirstDayOfWeek は今日がロケールの週初め (general_first_day_of_week) から何日目か。
func (q *Query) daysSinceFirstDayOfWeek() int {
	first := int(customfield.RubyToI(q.env.l("general_first_day_of_week")))
	dow := cwday(q.env.Today())
	if dow >= first {
		return dow - first
	}
	return dow + 7 - first
}

// cwday は ISO 曜日 (月曜 = 1 .. 日曜 = 7)。
func cwday(t time.Time) int {
	if wd := int(t.Weekday()); wd != 0 {
		return wd
	}
	return 7
}

// dateArg は parse_date の結果 (Date または Time)。
type dateArg struct {
	date   time.Time // Date (UTC 0 時の暦日)
	t      time.Time // Time
	isTime bool
}

var reDateTimePrefix = regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}T`)

// parseDate は parse_date: "YYYY-MM-DDT..." なら Time.parse、それ以外は Date.parse。
func (q *Query) parseDate(arg string) (dateArg, bool) {
	if reDateTimePrefix.MatchString(arg) {
		t, ok := parseRubyTime(arg, q.env.serverLoc())
		return dateArg{t: t, isTime: true}, ok
	}
	s := strings.TrimSpace(arg)
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		if len(s) > 10 {
			if t2, err2 := time.Parse("2006-01-02", s[:10]); err2 == nil && !unicode.IsDigit(rune(s[10])) {
				return dateArg{date: t2}, true
			}
		}
		return dateArg{}, false
	}
	return dateArg{date: t}, true
}

var reRubyTime = regexp.MustCompile(`\A(\d{4})-(\d{2})-(\d{2})T(\d{2})(?::?(\d{2}))?(?::?(\d{2}))?(Z|[+-]?\d{2}:?\d{2})?\z`)

// parseRubyTime は validate_query_filters を通る "YYYY-MM-DDTHH[:MM[:SS]][Z|HHMM]" を Time.parse と同様に解釈する
// (オフセット無しはサーバのローカル時刻)。
func parseRubyTime(s string, loc *time.Location) (time.Time, bool) {
	m := reRubyTime.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	atoi := func(x string) int { n, _ := strconv.Atoi(x); return n }
	y, mo, d, h, mi, se := atoi(m[1]), atoi(m[2]), atoi(m[3]), atoi(m[4]), atoi(m[5]), atoi(m[6])
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || se > 60 {
		return time.Time{}, false
	}
	switch z := m[7]; {
	case z == "Z":
		loc = time.UTC
	case z != "":
		sign := 1
		if z[0] == '-' {
			sign = -1
		}
		z = strings.TrimLeft(z, "+-")
		z = strings.ReplaceAll(z, ":", "")
		off := sign * (atoi(z[:2])*3600 + atoi(z[2:])*60)
		loc = time.FixedZone("", off)
	}
	t := time.Date(y, time.Month(mo), d, h, mi, se, 0, loc)
	if t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

// endOfDay は暦日 d のユーザのタイムゾーンでの 23:59:59.999999999。
func (q *Query) endOfDay(d time.Time) time.Time {
	y, m, dd := d.Date()
	return time.Date(y, m, dd, 23, 59, 59, 999999999, q.env.userLoc())
}

// dateClause は date_clause(table, field, from, to, is_custom_filter)。
//   - from: Date なら前日の end_of_day (ユーザのタイムゾーン)、Time なら 1 秒前。「より大きい」。
//   - to: Date なら当日の end_of_day、Time ならそのまま。「以下」。
//
// タイムスタンプ列は UTC の固定長文字列と比較する。DATE 列は Redmine と同じく境界時刻を
// サーバのローカル時刻にした日付部分と比較する (DB の暗黙変換と同じ結果になる)。
// カスタムフィールドの値 (文字列) はユーザのタイムゾーンでの "%Y-%m-%d %H:%M:%S" と文字列比較する。
func (q *Query) dateClause(table, column string, from dateArg, hasFrom bool, to dateArg, hasTo bool, isCustom bool) frag {
	col := table + "." + column
	var parts []frag
	if hasFrom {
		var t time.Time
		if from.isTime {
			t = from.t.Add(-time.Second)
		} else {
			t = q.endOfDay(from.date.AddDate(0, 0, -1))
		}
		parts = append(parts, sqlf(col+" > ?", q.quotedTime(t, table, column, isCustom)))
	}
	if hasTo {
		var t time.Time
		if to.isTime {
			t = to.t
		} else {
			t = q.endOfDay(to.date)
		}
		parts = append(parts, sqlf(col+" <= ?", q.quotedTime(t, table, column, isCustom)))
	}
	return joinFrags(" AND ", parts...)
}

// quotedTime は比較値を列の表現に合わせて作る。
func (q *Query) quotedTime(t time.Time, table, column string, isCustom bool) string {
	if isCustom {
		return t.Format("2006-01-02 15:04:05")
	}
	if dateColumns[table+"."+column] {
		return t.In(q.env.serverLoc()).Format("2006-01-02")
	}
	return db.FormatTime(t)
}

// relativeDateClause は relative_date_clause (今日からの相対日数)。
func (q *Query) relativeDateClause(table, column string, daysFrom int, hasFrom bool, daysTo int, hasTo bool, isCustom bool) frag {
	today := q.env.Today()
	return q.dateClause(table, column, dateArg{date: today.AddDate(0, 0, daysFrom)}, hasFrom,
		dateArg{date: today.AddDate(0, 0, daysTo)}, hasTo, isCustom)
}

// containsOpts は sql_contains のオプション。
type containsOpts struct {
	notMatch   bool // :match => false
	startsWith bool
	endsWith   bool
	anyWord    bool // :all_words => false
}

// sqlContains は sql_contains / Query.tokenized_like_conditions。
func (q *Query) sqlContains(col, value string, o containsOpts) frag {
	return tokenizedLike(q.env.dialect(), col, value, o)
}

func tokenizedLike(d db.Dialect, col, value string, o containsOpts) frag {
	tokens := Tokenize(value)
	if len(tokens) == 0 {
		tokens = []string{value}
	}
	prefix, suffix, sep := "%", "%", " AND "
	switch {
	case o.startsWith:
		prefix, sep = "", " OR "
	case o.endsWith:
		suffix, sep = "", " OR "
	case o.anyWord:
		sep = " OR "
	}
	var parts []frag
	for _, t := range tokens {
		like := d.ILike(col)
		if o.notMatch {
			like = "NOT (" + like + ")"
		}
		parts = append(parts, sqlf(like, prefix+db.EscapeLike(t)+suffix))
	}
	return joinFrags(sep, parts...)
}

var reToken = regexp.MustCompile(`"[^"]+"|[^\p{Zs}]+`)
var reTokenQuotes = regexp.MustCompile(`\A"\p{Zs}*|\p{Zs}*"\z`)

// Tokenize は Redmine::Search::Tokenizer#tokens: 空白区切り (引用符で句)、重複除去、
// 1 文字の語は漢字のみ残し、先頭 5 語。
func Tokenize(question string) []string {
	var tokens []string
	for _, m := range reToken.FindAllString(question, -1) {
		t := reTokenQuotes.ReplaceAllString(m, "")
		if !slices.Contains(tokens, t) {
			tokens = append(tokens, t)
		}
	}
	var out []string
	for _, t := range tokens {
		if len([]rune(t)) > 1 || containsHan(t) {
			out = append(out, t)
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// TokenizedLikeCondition は Query.tokenized_like_conditions(col, value)（全語 AND の部分一致）を
// プレースホルダ付きの SQL 断片と引数で返す（Issue.like 等、Query の外で使う）。
func TokenizedLikeCondition(d db.Dialect, col, value string) (string, []any) {
	f := tokenizedLike(d, col, value, containsOpts{})
	return f.SQL, f.Args
}
