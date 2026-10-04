// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// sqlMarker は利用者由来の文字列に付ける目印。SQL 文（プレースホルダの引数ではなく文そのもの）に現れたら、
// 利用者の入力が SQL に埋め込まれたことになる。
const sqlMarker = "zqx"

// markerQueryer は発行される SQL 文に目印が含まれないことを確かめる db.Queryer。
type markerQueryer struct {
	db.Queryer
	mu  sync.Mutex
	bad []string
}

func (m *markerQueryer) check(q string) {
	if strings.Contains(strings.ToLower(q), sqlMarker) {
		m.mu.Lock()
		m.bad = append(m.bad, q)
		m.mu.Unlock()
	}
}

func (m *markerQueryer) Exec(ctx context.Context, q string, args ...any) (db.Result, error) {
	m.check(q)
	return m.Queryer.Exec(ctx, q, args...)
}
func (m *markerQueryer) Get(ctx context.Context, dest any, q string, args ...any) error {
	m.check(q)
	return m.Queryer.Get(ctx, dest, q, args...)
}
func (m *markerQueryer) Select(ctx context.Context, dest any, q string, args ...any) error {
	m.check(q)
	return m.Queryer.Select(ctx, dest, q, args...)
}
func (m *markerQueryer) Query(ctx context.Context, q string, args ...any) (*sqlx.Rows, error) {
	m.check(q)
	return m.Queryer.Query(ctx, q, args...)
}
func (m *markerQueryer) QueryRow(ctx context.Context, q string, args ...any) *sqlx.Row {
	m.check(q)
	return m.Queryer.QueryRow(ctx, q, args...)
}
func (m *markerQueryer) InsertReturningID(ctx context.Context, q string, args ...any) (int64, error) {
	m.check(q)
	return m.Queryer.InsertReturningID(ctx, q, args...)
}
func (m *markerQueryer) sq(s db.Sqlizer) {
	if q, _, err := s.ToSql(); err == nil {
		m.check(q)
	}
}
func (m *markerQueryer) ExecSQ(ctx context.Context, s db.Sqlizer) (db.Result, error) {
	m.sq(s)
	return m.Queryer.ExecSQ(ctx, s)
}
func (m *markerQueryer) GetSQ(ctx context.Context, dest any, s db.Sqlizer) error {
	m.sq(s)
	return m.Queryer.GetSQ(ctx, dest, s)
}
func (m *markerQueryer) SelectSQ(ctx context.Context, dest any, s db.Sqlizer) error {
	m.sq(s)
	return m.Queryer.SelectSQ(ctx, dest, s)
}

// FuzzQuerySQL は URL パラメータから組み立てたクエリ（フィルタ・演算子・値・並び順・グループ・列）を実行し、
// 利用者由来の文字列が SQL 文に埋め込まれず、常にプレースホルダの引数として渡されることを確かめる。
// フィルタ名は実在するもの（AvailableFilters）から選び、値・並び順・グループ・列名には目印を付ける。
//
//	go test -run '^$' -fuzz FuzzQuerySQL ./internal/query
func FuzzQuerySQL(f *testing.F) {
	f.Add(uint8(0), uint16(0), "=", "1", "' OR 1=1 --", "id:desc", "tracker", "subject")
	f.Add(uint8(0), uint16(3), "~", "%'", "\\", "subject,id", "status", "estimated_hours")
	f.Add(uint8(1), uint16(5), "><", "2024-01-01", "2024-02-01' --", "spent_on", "user", "hours")
	f.Add(uint8(2), uint16(1), "!*", "x", "1) OR (1=1", "name", "", "identifier")
	f.Add(uint8(3), uint16(2), "!~", "a", "b", "login", "", "mail")
	f.Add(uint8(0), uint16(40), "*~", "cf", "1|2", "cf_1:asc", "cf_2", "cf_1")

	d := dbtest.NewSQLite(f)
	testfixtures.LoadAt(f, d, frozenNow, testfixtures.All()...)
	st, err := settings.New(context.Background(), repository.SettingsStore{DB: d})
	if err != nil {
		f.Fatal(err)
	}
	kinds := []Kind{KindIssue, KindTimeEntry, KindProject, KindUser}
	f.Fuzz(func(t *testing.T, kindIdx uint8, fieldIdx uint16, op, v1, v2, sort, group, col string) {
		ctx := context.Background()
		kind := kinds[int(kindIdx)%len(kinds)]
		tdb := &testDB{t: t, d: d, st: st}
		userID := int64(2)
		if kind == KindUser {
			userID = 1
		}
		e := tdb.env(userID)
		mq := &markerQueryer{Queryer: e.Q}
		e.Q = mq
		q, err := New(ctx, e, kind, nil)
		if err != nil {
			t.Fatal(err)
		}
		af, err := q.AvailableFilters(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fields := af.Keys()
		field := fields[int(fieldIdx)%len(fields)]
		mark := func(s string) string { return sqlMarker + s }
		v := url.Values{}
		v.Set("set_filter", "1")
		v.Add("f[]", field)
		v.Set("op["+field+"]", op)
		v.Add("v["+field+"][]", mark(v1))
		v.Add("v["+field+"][]", mark(v2))
		v.Add("v["+field+"][]", v1) // 置換（"me" など）の経路も通す。目印なしなので判定対象外
		v.Set("sort", sort+","+mark(sort)+":desc")
		v.Set("group_by", mark(group))
		v.Add("c[]", col)
		v.Add("c[]", mark(col))
		v.Add("t[]", mark(col))
		if err := q.BuildFromParams(ctx, ParseParams(v), nil); err != nil {
			return
		}
		_, _ = q.IDs(ctx, ListOptions{})
		_, _ = q.Count(ctx)
		if s, _, err := q.Statement(ctx); err == nil {
			mq.check(s)
		}
		if len(mq.bad) > 0 {
			t.Fatalf("%s field=%s op=%q: user input reached SQL text:\n%s", kind, field, op, fmt.Sprint(mq.bad))
		}
	})
}
