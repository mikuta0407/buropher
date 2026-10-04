// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package ldap_test

import (
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/ldap"
	"github.com/mikuta0407/buropher/internal/auth/ldap/ldaptest"
)

// TestAuthenticateRejectsInjectionAndBlankPassword は LDAP 認証に対する典型的な攻撃
// （フィルタへのワイルドカード・条件の注入、空白だけのパスワードによる未認証 bind、
// $login 置換での DN の構造の注入）が通らないことを確認する。
func TestAuthenticateRejectsInjectionAndBlankPassword(t *testing.T) {
	srv, host, port := newServer(t)
	s := source(host, port)
	for _, login := range []string{"*", "exam*", "example1)(uid=*", "*)(|(uid=*"} {
		if a, err := s.Authenticate(login, "123456"); a != nil || err != nil {
			t.Errorf("filter injection %q authenticated: %v %v", login, a, err)
		}
	}
	// （エスケープされていなければ "*" は example1 / edavis（パスワード 123456）に一致して認証が通る。
	// テスト用サーバの Searches はデコード後の値を表示するため文字列では判定しない）
	// 空白だけのパスワードは（未認証 bind として成功しうるため）認証に使わない
	for _, pw := range []string{"", " ", "\t"} {
		if a, err := s.Authenticate("example1", pw); a != nil || err != nil {
			t.Errorf("blank password %q authenticated: %v %v", pw, a, err)
		}
	}
	// $login 置換: カンマ等で RDN を追加して別のエントリとして bind できない
	s.Account = "uid=$login,ou=Person,dc=redmine,dc=org"
	before := len(srv.Binds())
	if a, _ := s.Authenticate("x,cn=admin,dc=redmine,dc=org", "secret"); a != nil {
		t.Error("DN injection via $login authenticated")
	}
	if b := srv.Binds(); len(b) > before && !strings.HasPrefix(b[before], `uid=x\,cn=admin\,dc=redmine\,dc=org,`) {
		t.Errorf("unescaped bind DN: %v", b[before:])
	}
}

// TestSearchErrorResultIsNotEmptyResult は定期同期用の Lookup とグループの取得が、検索の失敗
// （busy / unavailable / insufficientAccessRights / noSuchObject など結果コードが成功以外）を
// 「該当なし」と区別することを確認する。区別しないと、ディレクトリの一時的な障害や ACL・DIT の誤りで
// 定期同期が全ユーザーをロックし（sync_lock_missing）、グループの同期が所属を削除してしまう。
func TestSearchErrorResultIsNotEmptyResult(t *testing.T) {
	srv := &ldaptest.Server{Entries: groupEntries()}
	host, port := startServer(t, srv)
	s := groupSource(host, port)
	s.Groups = ldap.GroupConfig{Mode: ldap.GroupModeSearch, BaseDN: "ou=Groups,dc=example,dc=com"}
	if info, err := s.Lookup("alice"); err != nil || info == nil || !info.GroupsFetched || len(info.Groups) == 0 {
		t.Fatalf("alice before failure: %+v %v", info, err)
	}
	for _, code := range []int64{51 /* busy */, 52 /* unavailable */, 50 /* insufficientAccessRights */, 32 /* noSuchObject */} {
		srv.SetSearchResultCode(code, "")
		if info, err := s.Lookup("alice"); err == nil || info != nil {
			t.Errorf("user search result code %d: got %+v, %v; want an error", code, info, err)
		}
		// グループの検索だけが失敗: Lookup はエラー、ログイン時はグループを取得しなかった扱い
		srv.SetSearchResultCode(code, "ou=Groups,dc=example,dc=com")
		if info, err := s.Lookup("alice"); err == nil {
			t.Errorf("group search result code %d: got %+v; want an error", code, info)
		}
		a, err := s.Authenticate("alice", "pw")
		if err != nil || a == nil {
			t.Fatalf("authenticate with failing group search (%d): %+v %v", code, a, err)
		}
		if a.GroupsFetched {
			t.Errorf("group search result code %d: GroupsFetched = true with groups %v", code, a.Groups)
		}
	}
	srv.SetSearchResultCode(0, "")
	if info, err := s.Lookup("nobody"); err != nil || info != nil {
		t.Errorf("nobody: %+v %v", info, err)
	}
}
