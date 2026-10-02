package testfixtures

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// Redmine の test/object_helpers.rb (User.generate! 等) に相当する、テスト用のレコード生成。
// 生成は生 SQL で行い、ドメインのコールバックは実行しない (メンバーシップ等は repository を使うこと)。

var seq atomic.Int64

func next() int64 { return seq.Add(1) }

// GenerateUser は有効なユーザ (login "user<N>", メール "user<N>@example.net") を作成して id を返す。
func GenerateUser(t testing.TB, d db.Queryer) int64 {
	t.Helper()
	return GenerateUserWith(t, d, UserAttrs{})
}

// UserAttrs は GenerateUserWith の属性。ゼロ値は既定値。
type UserAttrs struct {
	Login  string
	Admin  bool
	Status int // 0 なら有効 (1)
}

// GenerateUserWith は属性を指定してユーザを作成する。
func GenerateUserWith(t testing.TB, d db.Queryer, a UserAttrs) int64 {
	t.Helper()
	ctx := context.Background()
	n := next()
	if a.Login == "" {
		a.Login = fmt.Sprintf("user%d", n)
	}
	if a.Status == 0 {
		a.Status = 1
	}
	now := db.Now()
	id, err := d.InsertReturningID(ctx, `INSERT INTO principals (kind, status, firstname, lastname, created_at, updated_at) VALUES ('user', ?, 'Bob', ?, ?, ?)`,
		a.Status, fmt.Sprintf("Smith%d", n), now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO user_accounts (principal_id, login, admin, language) VALUES (?, ?, ?, 'en')`, id, a.Login, a.Admin); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO email_addresses (user_id, address, is_default, notify, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, a.Login+"@example.net", true, true, now, now); err != nil {
		t.Fatal(err)
	}
	return id
}

// GenerateGroup はグループ ("Group<N>") を作成して id を返す。
func GenerateGroup(t testing.TB, d db.Queryer) int64 {
	t.Helper()
	now := db.Now()
	id, err := d.InsertReturningID(context.Background(), `INSERT INTO principals (kind, status, name, created_at, updated_at) VALUES ('group', 1, ?, ?, ?)`,
		fmt.Sprintf("Group%d", next()), now, now)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// IssueAttrs は GenerateIssue の属性。ゼロ値は Redmine の Issue.generate! と同じ既定値
// (project 1, tracker 1, status 1, author 2, 優先度は既定または先頭)。
type IssueAttrs struct {
	ProjectID    int64
	TrackerID    int64
	StatusID     int64
	AuthorID     int64
	AssignedToID int64
	ParentID     int64
	IsPrivate    bool
	Subject      string
}

// GenerateIssue はチケットを作成して id を返す (hier_path / root_id も設定する)。
func GenerateIssue(t testing.TB, d db.Queryer, a IssueAttrs) int64 {
	t.Helper()
	ctx := context.Background()
	if a.ProjectID == 0 {
		a.ProjectID = 1
	}
	if a.TrackerID == 0 {
		a.TrackerID = 1
	}
	if a.StatusID == 0 {
		a.StatusID = 1
	}
	if a.AuthorID == 0 {
		a.AuthorID = 2
	}
	if a.Subject == "" {
		a.Subject = fmt.Sprintf("Generated issue %d", next())
	}
	var priority int64
	if err := d.Get(ctx, &priority, `SELECT id FROM issue_priorities ORDER BY is_default DESC, position, id LIMIT 1`); err != nil {
		t.Fatalf("GenerateIssue: no issue priority (load the enumerations fixture): %v", err)
	}
	var assigned, parent any
	if a.AssignedToID != 0 {
		assigned = a.AssignedToID
	}
	rootPath := ""
	var rootID int64
	if a.ParentID != 0 {
		parent = a.ParentID
		if err := d.QueryRow(ctx, `SELECT root_id, hier_path FROM issues WHERE id = ?`, a.ParentID).Scan(&rootID, &rootPath); err != nil {
			t.Fatal(err)
		}
	}
	now := db.Now()
	var id int64
	err := withTx(ctx, d, func(q db.Queryer) error {
		var err error
		id, err = q.InsertReturningID(ctx, `INSERT INTO issues (project_id, tracker_id, status_id, priority_id, author_id, assigned_to_id,
  parent_id, root_id, hier_path, subject, is_private, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?)`,
			a.ProjectID, a.TrackerID, a.StatusID, priority, a.AuthorID, assigned, parent, rootID, a.Subject, a.IsPrivate, now, now)
		if err != nil {
			return err
		}
		if rootID == 0 {
			rootID = id
		}
		_, err = q.Exec(ctx, `UPDATE issues SET root_id = ?, hier_path = ? WHERE id = ?`, rootID, fmt.Sprintf("%s%010d/", rootPath, id), id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// withTx は d が *db.DB ならトランザクション (自己参照 FK の遅延検査のため) 内で fn を実行する。
func withTx(ctx context.Context, d db.Queryer, fn func(q db.Queryer) error) error {
	if x, ok := d.(*db.DB); ok {
		return x.WithTx(ctx, func(tx *db.Tx) error { return fn(tx) })
	}
	return fn(d)
}
