// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scenario

import (
	"os"
	"path/filepath"
	"testing"
)

func TestYAMLCases(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.yml")
	src := `users:
  dlopper: {password: foo}
normalize:
  strip_selectors: ["#footer"]
requests:
  - path: /issues/1
    users: [anonymous, admin, dlopper]
    normalize:
      strip_selectors: ["#sidebar"]
  - path: /issues.json?limit=1
    users: [admin]
  - path: /issues
    method: post
    user: jsmith
    form: {a: b}
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "s" {
		t.Errorf("name = %q", f.Name)
	}
	cs, err := f.Cases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 5 {
		t.Fatalf("got %d cases", len(cs))
	}
	type exp struct{ id, auth, login, pw string }
	want := []exp{
		{"issues_1__anonymous", "none", "anonymous", "anonymous"},
		{"issues_1__admin", "session", "admin", "admin"},
		{"issues_1__dlopper", "session", "dlopper", "foo"},
		{"issues.json_limit_1__admin", "basic", "admin", "admin"},
		{"post_issues__jsmith", "session", "jsmith", "jsmith"},
	}
	for i, w := range want {
		c := cs[i]
		if c.ID != w.id || c.Auth != w.auth || c.Cred.Login != w.login || c.Cred.Password != w.pw {
			t.Errorf("case %d = %+v, want %+v", i, c, w)
		}
	}
	if len(cs[0].Normalize.StripSelectors) != 2 {
		t.Errorf("normalize not merged: %v", cs[0].Normalize.StripSelectors)
	}
	if cs[4].Method != "POST" {
		t.Errorf("method = %s", cs[4].Method)
	}
}

func TestYAMLUnknownField(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.yml")
	_ = os.WriteFile(p, []byte("requests:\n  - path: /\n    userz: [a]\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Error("expected unknown field error")
	}
}

func TestDuplicateID(t *testing.T) {
	f := &File{Name: "x", Requests: []Request{{Path: "/a"}, {Path: "/a"}}}
	if _, err := f.Cases(); err == nil {
		t.Error("expected duplicate id error")
	}
}

func TestTXT(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.txt")
	src := "# comment\n/projects admin,jsmith\nPOST /issues.json admin format=json\n/login\n"
	_ = os.WriteFile(p, []byte(src), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := f.Cases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 4 {
		t.Fatalf("got %d cases: %+v", len(cs), cs)
	}
	if cs[2].Method != "POST" || cs[2].Auth != "basic" || cs[3].User != Anonymous {
		t.Errorf("unexpected cases: %+v", cs)
	}
}
