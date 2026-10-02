package ldap_test

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/ldap"
	"github.com/mikuta0407/buropher/internal/auth/ldap/ldaptest"
)

// groupEntries はグループを含むディレクトリ（OpenLDAP 風の groupOfNames / posixGroup と AD 風の group）。
func groupEntries() []*ldaptest.Entry {
	const base = "dc=example,dc=com"
	return []*ldaptest.Entry{
		{DN: "cn=admin," + base, Password: "secret"},
		{DN: "uid=alice,ou=People," + base, Password: "pw", Attrs: map[string][]string{
			"uid": {"alice"}, "givenName": {"Alice"}, "sn": {"Liddell"}, "mail": {"alice@example.com"},
			"memberOf": {"cn=devs,ou=Groups," + base}, "userAccountControl": {"512"}}},
		{DN: "uid=bob,ou=People," + base, Password: "pw", Attrs: map[string][]string{
			"uid": {"bob"}, "givenName": {"Bob"}, "sn": {"Builder"}, "mail": {"bob@example.com"}, "userAccountControl": {"514"}}},
		{DN: "cn=devs,ou=Groups," + base, Attrs: map[string][]string{
			"objectClass": {"groupOfNames"}, "cn": {"devs"}, "member": {"uid=alice,ou=People," + base}}},
		{DN: "cn=staff,ou=Groups," + base, Attrs: map[string][]string{
			"objectClass": {"groupOfNames"}, "cn": {"staff"}, "member": {"cn=devs,ou=Groups," + base, "uid=bob,ou=People," + base}}},
		{DN: "cn=all,ou=Groups," + base, Attrs: map[string][]string{
			"objectClass": {"groupOfNames"}, "cn": {"all"}, "member": {"cn=staff,ou=Groups," + base}}},
		{DN: "cn=posix,ou=Groups," + base, Attrs: map[string][]string{
			"objectClass": {"posixGroup"}, "cn": {"posix"}, "memberUid": {"alice"}}},
		{DN: "cn=ad-devs,ou=Groups," + base, Attrs: map[string][]string{
			"objectClass": {"group"}, "cn": {"ad-devs"}, "member": {"uid=alice,ou=People," + base}}},
		{DN: "cn=ad-all,ou=Groups," + base, Attrs: map[string][]string{
			"objectClass": {"group"}, "cn": {"ad-all"}, "member": {"cn=ad-devs,ou=Groups," + base}}},
	}
}

func startServer(t *testing.T, srv *ldaptest.Server) (string, int) {
	t.Helper()
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return host, p
}

func groupSource(host string, port int) *ldap.Source {
	return &ldap.Source{ID: 1, Name: "LDAP", Host: host, Port: port, BaseDN: "dc=example,dc=com",
		Account: "cn=admin,dc=example,dc=com", AccountPassword: "secret",
		AttrLogin: "uid", AttrFirstname: "givenName", AttrLastname: "sn", AttrMail: "mail"}
}

func TestStartTLSAndCACert(t *testing.T) {
	srv := &ldaptest.Server{Entries: groupEntries(), RequireTLS: true}
	host, port := startServer(t, srv)
	s := groupSource(host, port)

	// 平文の bind は confidentialityRequired で拒否される（net-ldap と同じく「該当なし」）
	if a, err := s.Authenticate("alice", "pw"); a != nil || err != nil {
		t.Fatalf("plain: %v %v", a, err)
	}
	// STARTTLS・検証なし
	s.StartTLS = true
	if a, err := s.Authenticate("alice", "pw"); a == nil || err != nil {
		t.Fatalf("starttls: %v %v", a, err)
	}
	// 検証ありで CA 未指定なら自己署名証明書は失敗する
	s.VerifyPeer = true
	if _, err := s.Authenticate("alice", "pw"); err == nil || !ldap.IsAuthSourceError(err) {
		t.Fatalf("verify without CA: %v", err)
	}
	// CA 証明書（PEM の本文）
	s.CACert = string(srv.CertPEM)
	if a, err := s.Authenticate("alice", "pw"); a == nil || err != nil {
		t.Fatalf("verify with CA: %v %v", a, err)
	}
	// CA 証明書（ファイルのパス）
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, srv.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	s.CACert = path
	if err := s.TestConnection("bind failed"); err != nil {
		t.Fatalf("test connection with CA file: %v", err)
	}
	// 不正な CA
	s.CACert = "-----BEGIN CERTIFICATE-----\nxx\n-----END CERTIFICATE-----"
	if err := s.TestConnection("bind failed"); err == nil || !strings.Contains(err.Error(), "invalid CA certificate") {
		t.Fatalf("invalid CA: %v", err)
	}
	if ldap.ValidCACert(s.CACert) || !ldap.ValidCACert("") || !ldap.ValidCACert(path) {
		t.Error("ValidCACert")
	}
}

func TestLDAPSWithCACert(t *testing.T) {
	srv := &ldaptest.Server{Entries: groupEntries(), TLS: true}
	host, port := startServer(t, srv)
	s := groupSource(host, port)
	s.TLS, s.VerifyPeer, s.CACert = true, true, string(srv.CertPEM)
	if a, err := s.Authenticate("alice", "pw"); a == nil || err != nil {
		t.Fatalf("ldaps: %v %v", a, err)
	}
}

func TestFailoverHosts(t *testing.T) {
	srv := &ldaptest.Server{Entries: groupEntries()}
	host, port := startServer(t, srv)
	// 閉じたポートを主ホストにする
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s := groupSource(host, dead)
	if _, err := s.Authenticate("alice", "pw"); err == nil || !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("no failover: %v", err)
	}
	s.FailoverHosts = ldap.ParseHostList("127.0.0.1:" + strconv.Itoa(dead) + "\n" + net.JoinHostPort(host, strconv.Itoa(port)))
	if a, err := s.Authenticate("alice", "pw"); a == nil || err != nil {
		t.Fatalf("failover: %v %v", a, err)
	}
}

func TestGroups(t *testing.T) {
	srv := &ldaptest.Server{Entries: groupEntries()}
	host, port := startServer(t, srv)
	const g = ",ou=Groups,dc=example,dc=com"
	for _, tc := range []struct {
		name  string
		dir   string
		conf  ldap.GroupConfig
		login string
		want  []string
	}{
		{"memberof", "", ldap.GroupConfig{Mode: ldap.GroupModeMemberOf}, "alice", []string{"cn=devs" + g}},
		{"search", "", ldap.GroupConfig{Mode: ldap.GroupModeSearch, BaseDN: "ou=Groups,dc=example,dc=com"}, "alice", []string{"cn=devs" + g}},
		{"search nested", "", ldap.GroupConfig{Mode: ldap.GroupModeSearch, Nested: true}, "alice", []string{"cn=devs" + g, "cn=staff" + g, "cn=all" + g}},
		{"memberUid", "", ldap.GroupConfig{Mode: ldap.GroupModeSearch, Filter: "objectClass=posixGroup", MemberAttr: "memberUid"}, "alice", []string{"cn=posix" + g}},
		{"ad in chain", ldap.DirectoryActiveDirectory, ldap.GroupConfig{Mode: ldap.GroupModeMemberOf, Nested: true}, "alice", []string{"cn=ad-devs" + g, "cn=ad-all" + g}},
		{"ad direct", ldap.DirectoryActiveDirectory, ldap.GroupConfig{Mode: ldap.GroupModeSearch}, "alice", []string{"cn=ad-devs" + g}},
		{"bob", "", ldap.GroupConfig{Mode: ldap.GroupModeSearch}, "bob", []string{"cn=staff" + g}},
		{"no groups", "", ldap.GroupConfig{Mode: ldap.GroupModeMemberOf}, "bob", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := groupSource(host, port)
			s.DirectoryType, s.Groups = tc.dir, tc.conf
			a, err := s.Authenticate(tc.login, "pw")
			if err != nil || a == nil {
				t.Fatalf("authenticate: %v %v", a, err)
			}
			if !a.GroupsFetched || !reflect.DeepEqual(a.Groups, tc.want) {
				t.Errorf("groups = %v (%v), want %v", a.Groups, a.GroupsFetched, tc.want)
			}
		})
	}
	// グループを設定しなければ取得しない
	s := groupSource(host, port)
	if a, _ := s.Authenticate("alice", "pw"); a == nil || a.GroupsFetched || a.Groups != nil {
		t.Errorf("groups without config: %+v", a)
	}
	got := srv.Searches()
	found := false
	for _, f := range got {
		if strings.Contains(f, "member:"+ldap.InChainOID+":=uid=alice") {
			found = true
		}
	}
	if !found {
		t.Errorf("IN_CHAIN filter not used: %v", got)
	}
}

func TestLookup(t *testing.T) {
	srv := &ldaptest.Server{Entries: groupEntries()}
	host, port := startServer(t, srv)
	s := groupSource(host, port)
	s.DirectoryType = ldap.DirectoryActiveDirectory
	s.Groups = ldap.GroupConfig{Mode: ldap.GroupModeMemberOf}
	info, err := s.Lookup("alice")
	if err != nil || info == nil || info.Disabled || info.Firstname != "Alice" || info.Mail != "alice@example.com" ||
		!reflect.DeepEqual(info.Groups, []string{"cn=devs,ou=Groups,dc=example,dc=com"}) {
		t.Fatalf("alice: %+v %v", info, err)
	}
	if info, err := s.Lookup("bob"); err != nil || info == nil || !info.Disabled {
		t.Fatalf("bob: %+v %v", info, err)
	}
	if info, err := s.Lookup("nobody"); err != nil || info != nil {
		t.Fatalf("nobody: %+v %v", info, err)
	}
	// OpenLDAP では userAccountControl を見ない
	s.DirectoryType = ldap.DirectoryOpenLDAP
	if info, _ := s.Lookup("bob"); info == nil || info.Disabled {
		t.Errorf("openldap bob: %+v", info)
	}
	s.AccountPassword = "wrong"
	if _, err := s.Lookup("alice"); err == nil {
		t.Error("bad service account")
	}
	s.Account = "uid=$login,ou=People,dc=example,dc=com"
	if _, err := s.Lookup("alice"); err != ldap.ErrNotSearchable {
		t.Errorf("$login account: %v", err)
	}
}

func TestGroupMatches(t *testing.T) {
	dn := "CN=Devs,OU=Groups,DC=example,DC=com"
	for _, tc := range []struct {
		ext  string
		want bool
	}{
		{"cn=devs,ou=groups,dc=example,dc=com", true},
		{"cn=devs, ou=Groups, dc=example, dc=com", true},
		{"devs", true},
		{"DEVS", true},
		{"cn=devs", false},
		{"staff", false},
		{"", false},
	} {
		if got := ldap.GroupMatches(tc.ext, dn); got != tc.want {
			t.Errorf("GroupMatches(%q) = %v", tc.ext, got)
		}
	}
}
