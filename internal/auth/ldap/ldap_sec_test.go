// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package ldap_test

import (
	"strings"
	"testing"
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
