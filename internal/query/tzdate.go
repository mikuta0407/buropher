package query

import (
	"database/sql/driver"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"

	"github.com/mikuta0407/buropher/internal/db"
)

// SQLite にはタイムゾーン変換が無いため、UTC 固定長文字列のタイムスタンプを
// 指定タイムゾーン (IANA 名) の日付 'YYYY-MM-DD' に変換する関数 buropher_tz_date を登録する。
// (Redmine::Database.timestamp_to_date の PostgreSQL 版 "(col AT TIME ZONE tz)::date" に相当)
func init() {
	if err := sqlite.RegisterDeterministicScalarFunction("buropher_tz_date", 2, sqliteTZDate); err != nil {
		panic(err)
	}
}

var tzCache sync.Map

func loadTZ(name string) *time.Location {
	if v, ok := tzCache.Load(name); ok {
		return v.(*time.Location)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.UTC
	}
	tzCache.Store(name, loc)
	return loc
}

func sqliteTZDate(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	var s string
	switch v := args[0].(type) {
	case nil:
		return nil, nil
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return nil, nil
	}
	t, err := db.ParseTime(s)
	if err != nil {
		return nil, nil
	}
	name, _ := args[1].(string)
	return t.In(loadTZ(name)).Format("2006-01-02"), nil
}

// tsDateSQL はタイムスタンプ列 col をタイムゾーン tz の日付にする SQL 式。
func tsDateSQL(d db.DialectName, col, tz string) string {
	lit := "'" + strings.ReplaceAll(tz, "'", "''") + "'"
	if d == db.Postgres {
		return "((" + col + ") AT TIME ZONE " + lit + ")::date"
	}
	return "buropher_tz_date(" + col + ", " + lit + ")"
}
