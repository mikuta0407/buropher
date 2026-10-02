package ldap_test

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/auth/ldap"
	"github.com/mikuta0407/buropher/internal/auth/ldap/ldaptest"
)

func newServer(t *testing.T) (*ldaptest.Server, string, int) {
	t.Helper()
	s := &ldaptest.Server{Entries: []*ldaptest.Entry{
		{DN: "cn=admin,dc=redmine,dc=org", Password: "secret"},
		{DN: "uid=example1,ou=Person,dc=redmine,dc=org", Password: "123456", Attrs: map[string][]string{
			"uid": {"example1"}, "givenName": {"Example"}, "sn": {"One"}, "mail": {"example1@redmine.org", "other@redmine.org"}}},
		{DN: "uid=edavis,ou=Person,dc=redmine,dc=org", Password: "123456", Attrs: map[string][]string{
			"uid": {"edavis"}, "givenName": {"Eric"}, "sn": {"Davis"}, "mail": {"edavis@redmine.org"}, "description": {"locked"}}},
		{DN: "uid=a\\,b,ou=Person,dc=redmine,dc=org", Password: "pw", Attrs: map[string][]string{"uid": {"a,b"}}},
	}}
	addr, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return s, host, p
}

func source(host string, port int) *ldap.Source {
	return &ldap.Source{ID: 1, Name: "LDAP", Host: host, Port: port, BaseDN: "ou=Person,dc=redmine,dc=org",
		AttrLogin: "uid", AttrFirstname: "givenName", AttrLastname: "sn", AttrMail: "mail"}
}

func TestAuthenticate(t *testing.T) {
	srv, host, port := newServer(t)
	s := source(host, port)

	// 匿名検索 → DN で bind
	a, err := s.Authenticate("example1", "123456")
	if err != nil || a == nil {
		t.Fatalf("authenticate: %v %v", a, err)
	}
	// オンザフライ登録でなければ属性は返さない
	if a.Firstname != "" || a.DN != "" {
		t.Errorf("attrs = %+v", a)
	}
	if a, err := s.Authenticate("example1", "wrong"); a != nil || err != nil {
		t.Errorf("wrong password: %v %v", a, err)
	}
	if a, err := s.Authenticate("nobody", "123456"); a != nil || err != nil {
		t.Errorf("unknown user: %v %v", a, err)
	}
	if a, err := s.Authenticate("example1", ""); a != nil || err != nil {
		t.Errorf("blank password: %v %v", a, err)
	}

	// オンザフライ登録: 属性（複数値は先頭）
	s.OntheflyRegister = true
	a, err = s.Authenticate("example1", "123456")
	if err != nil || a == nil || a.Firstname != "Example" || a.Lastname != "One" || a.Mail != "example1@redmine.org" || a.AuthSourceID != 1 {
		t.Fatalf("onthefly attrs = %+v %v", a, err)
	}

	// フィルタ
	s.Filter = "(description=locked)"
	if a, _ := s.Authenticate("example1", "123456"); a != nil {
		t.Error("filter not applied")
	}
	if a, _ := s.Authenticate("edavis", "123456"); a == nil {
		t.Error("filter excluded edavis")
	}
	got := srv.Searches()
	if want := "(&(&(objectClass=*)(description=locked))(uid=edavis))"; got[len(got)-1] != want {
		t.Errorf("filter = %s, want %s", got[len(got)-1], want)
	}
	// 括弧なしのフィルタも受け付ける（Net::LDAP::Filter.construct）
	s.Filter = "description=locked"
	if a, _ := s.Authenticate("edavis", "123456"); a == nil {
		t.Error("bare filter")
	}
}

func TestAuthenticateWithServiceAccount(t *testing.T) {
	srv, host, port := newServer(t)
	s := source(host, port)
	s.Account, s.AccountPassword = "cn=admin,dc=redmine,dc=org", "secret"
	if a, err := s.Authenticate("example1", "123456"); a == nil || err != nil {
		t.Fatalf("authenticate: %v %v", a, err)
	}
	if b := srv.Binds(); len(b) != 2 || b[0] != "cn=admin,dc=redmine,dc=org" || b[1] != "uid=example1,ou=Person,dc=redmine,dc=org" {
		t.Errorf("binds = %v", b)
	}
	// サービスアカウントの bind 失敗は「該当なし」
	s.AccountPassword = "bad"
	if a, err := s.Authenticate("example1", "123456"); a != nil || err != nil {
		t.Errorf("bad service account: %v %v", a, err)
	}
}

func TestAuthenticateWithLoginSubstitution(t *testing.T) {
	srv, host, port := newServer(t)
	s := source(host, port)
	s.Account = "uid=$login,ou=Person,dc=redmine,dc=org"
	if a, err := s.Authenticate("example1", "123456"); a == nil || err != nil {
		t.Fatalf("authenticate: %v %v", a, err)
	}
	if b := srv.Binds(); b[0] != "uid=example1,ou=Person,dc=redmine,dc=org" {
		t.Errorf("binds = %v", b)
	}
	// DN エスケープ
	if a, err := s.Authenticate("a,b", "pw"); a == nil || err != nil {
		t.Fatalf("escaped login: %v %v", a, err)
	}
	if b := srv.Binds(); b[2] != `uid=a\,b,ou=Person,dc=redmine,dc=org` {
		t.Errorf("binds = %v", b)
	}
	if a, _ := s.Authenticate("example1", "bad"); a != nil {
		t.Error("bad password accepted")
	}
	if s.Searchable() {
		t.Error("$login source must not be searchable")
	}
}

func TestSearch(t *testing.T) {
	_, host, port := newServer(t)
	s := source(host, port)
	res, err := s.Search("ex")
	if err != nil || len(res) != 1 || res[0].Login != "example1" || res[0].Mail != "example1@redmine.org" || res[0].AuthSourceID != 1 {
		t.Fatalf("search = %+v %v", res, err)
	}
	if res, _ := s.Search("  "); len(res) != 0 {
		t.Error("blank query")
	}
	s.AttrMail = ""
	if res, _ := s.Search("ex"); len(res) != 0 {
		t.Error("not searchable source returned results")
	}
}

func TestTestConnection(t *testing.T) {
	_, host, port := newServer(t)
	s := source(host, port)
	if err := s.TestConnection("bind failed"); err != nil {
		t.Fatal(err)
	}
	s.Account, s.AccountPassword = "cn=admin,dc=redmine,dc=org", "bad"
	if err := s.TestConnection("bind failed"); err == nil || err.Error() != "bind failed" {
		t.Errorf("err = %v", err)
	}
	// 接続できない
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s2 := source("127.0.0.1", closed)
	err := s2.TestConnection("x")
	if !ldap.IsAuthSourceError(err) || err.Error() != "LDAP: Connection refused - connect(2) for 127.0.0.1:"+strconv.Itoa(closed) {
		t.Errorf("err = %v", err)
	}
	if _, err := s2.Authenticate("example1", "123456"); !ldap.IsAuthSourceError(err) {
		t.Errorf("authenticate err = %v", err)
	}
}

func TestTimeout(t *testing.T) {
	srv, host, port := newServer(t)
	srv.Delay = 2 * time.Second
	s := source(host, port)
	s.Timeout = 1
	start := time.Now()
	_, err := s.Authenticate("example1", "123456")
	var e *ldap.Error
	if err == nil || !ldap.IsAuthSourceError(err) || err.Error() != "LDAP: execution expired" {
		t.Fatalf("err = %v", err)
	}
	_ = e
	if d := time.Since(start); d > 1900*time.Millisecond {
		t.Errorf("took %v", d)
	}
}

func TestEscapeDNAndFilter(t *testing.T) {
	for in, want := range map[string]string{"a,b": `a\,b`, " x ": `\ x\ `, "#a": `\#a`, `a+b"c\d<e>f;`: `a\+b\"c\\d\<e\>f\;`} {
		if got := ldap.EscapeDN(in); got != want {
			t.Errorf("EscapeDN(%q) = %q, want %q", in, got, want)
		}
	}
	for f, ok := range map[string]bool{"": true, "(uid=x)": true, "uid=x": true, "(&(a=b)(c=d))": true, "(uid=x": false, "((": false} {
		if ldap.ValidFilter(f) != ok {
			t.Errorf("ValidFilter(%q) != %v", f, ok)
		}
	}
}
