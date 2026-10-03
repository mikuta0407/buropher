// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package export

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// 書き出し元 DB の種別。
const (
	KindMySQL     = "mysql"
	KindPostgres  = "postgres"
	KindSQLite    = "sqlite"
	KindSQLServer = "sqlserver"
)

// connInfo は database/sql で開くための情報。
type connInfo struct {
	Kind       string // KindXxx
	DriverName string // sql.Open に渡すドライバ名
	DSN        string // ドライバ固有 DSN
}

// detectKind は DSN のスキームから DB 種別を判定する。
func detectKind(dsn string) (string, bool) {
	i := strings.Index(dsn, ":")
	if i < 0 {
		return "", false
	}
	switch strings.ToLower(dsn[:i]) {
	case "mysql", "mysql2", "mariadb", "trilogy":
		return KindMySQL, true
	case "postgres", "postgresql", "pgsql":
		return KindPostgres, true
	case "sqlite", "sqlite3", "file":
		return KindSQLite, true
	case "sqlserver", "mssql":
		return KindSQLServer, true
	}
	return "", false
}

// normalizeKind は Options.Driver の表記ゆれを正規化する。
func normalizeKind(s string) (string, error) {
	switch strings.ToLower(s) {
	case "":
		return "", nil
	case "mysql", "mariadb", "mysql2":
		return KindMySQL, nil
	case "postgres", "postgresql", "pg", "pgx":
		return KindPostgres, nil
	case "sqlite", "sqlite3":
		return KindSQLite, nil
	case "sqlserver", "mssql":
		return KindSQLServer, nil
	}
	return "", fmt.Errorf("unknown driver %q (mysql/postgres/sqlite/sqlserver)", s)
}

// resolveConn は DSN と(任意の)ドライバ指定から接続情報を作る。
// スキーム付き URL 形式に加え、ドライバ指定時はドライバ固有のネイティブ DSN も受け付ける。
func resolveConn(dsn, driver string) (*connInfo, error) {
	kind, err := normalizeKind(driver)
	if err != nil {
		return nil, err
	}
	urlKind, isURL := detectKind(dsn)
	if kind == "" {
		if !isURL {
			return nil, fmt.Errorf("cannot detect database kind from DSN; use mysql://, postgres://, sqlite:// or sqlserver:// or specify --driver")
		}
		kind = urlKind
	}
	if isURL && urlKind != kind {
		return nil, fmt.Errorf("DSN scheme (%s) does not match driver %s", urlKind, kind)
	}
	switch kind {
	case KindSQLite:
		p := dsn
		if isURL {
			p = sqlitePath(dsn)
		}
		if p == "" {
			return nil, fmt.Errorf("sqlite DSN has no file path")
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		// 読み取り専用で開く(元 DB へは絶対に書かない)
		u := "file:" + (&url.URL{Path: filepath.ToSlash(abs)}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(1)"
		return &connInfo{Kind: kind, DriverName: "sqlite", DSN: u}, nil
	case KindMySQL:
		if !isURL {
			return &connInfo{Kind: kind, DriverName: "mysql", DSN: dsn}, nil
		}
		c, err := mysqlConfigFromURL(dsn)
		if err != nil {
			return nil, err
		}
		return &connInfo{Kind: kind, DriverName: "mysql", DSN: c.FormatDSN()}, nil
	case KindPostgres:
		d := dsn
		if isURL && strings.HasPrefix(strings.ToLower(d), "pgsql:") {
			d = "postgres:" + d[len("pgsql:"):]
		}
		return &connInfo{Kind: kind, DriverName: "pgx", DSN: d}, nil
	case KindSQLServer:
		d := dsn
		if isURL && strings.HasPrefix(strings.ToLower(d), "mssql:") {
			d = "sqlserver:" + d[len("mssql:"):]
		}
		return &connInfo{Kind: kind, DriverName: "sqlserver", DSN: d}, nil
	}
	return nil, fmt.Errorf("unsupported kind %s", kind)
}

// sqlitePath は sqlite:///abs/path, sqlite://rel/path, sqlite:path, file:path からパスを取り出す。
func sqlitePath(dsn string) string {
	i := strings.Index(dsn, ":")
	rest := dsn[i+1:]
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		rest = rest[:q]
	}
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		// sqlite://localhost/abs/path の形
		if strings.HasPrefix(rest, "localhost/") {
			rest = rest[len("localhost"):]
		}
	}
	if p, err := url.PathUnescape(rest); err == nil {
		rest = p
	}
	return rest
}

// mysqlConfigFromURL は mysql://user:pass@host:port/db?params を go-sql-driver の Config へ変換する。
// クエリ socket=/path で UNIX ソケット接続。その他のクエリは go-sql-driver の DSN パラメータとして解釈する。
func mysqlConfigFromURL(dsn string) (*mysql.Config, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid mysql DSN: %w", err)
	}
	q := u.Query()
	socket := q.Get("socket")
	q.Del("socket")
	// 時刻は文字列のまま受け取る(TZ 変換をさせない)
	q.Del("parseTime")
	q.Del("loc")
	native := "/"
	if len(q) > 0 {
		native += "?" + q.Encode()
	}
	c, err := mysql.ParseDSN(native)
	if err != nil {
		return nil, fmt.Errorf("invalid mysql DSN parameters: %w", err)
	}
	if u.User != nil {
		c.User = u.User.Username()
		c.Passwd, _ = u.User.Password()
	}
	c.DBName = strings.TrimPrefix(u.Path, "/")
	if socket != "" {
		c.Net = "unix"
		c.Addr = socket
	} else {
		c.Net = "tcp"
		host := u.Host
		if host == "" {
			host = "127.0.0.1"
		}
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(strings.Trim(host, "[]"), "3306")
		}
		c.Addr = host
	}
	c.ParseTime = false
	return c, nil
}
