// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package password

import "testing"

func TestArgon2(t *testing.T) {
	h, err := Hash("secret")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := Verify(h, "secret"); !ok {
		t.Error("verify failed")
	}
	if ok, _ := Verify(h, "wrong"); ok {
		t.Error("wrong password accepted")
	}
	if NeedsRehash(h) {
		t.Error("fresh hash needs rehash")
	}
}

// 期待値は Redmine 6.1.2 の User#salt_password("admin") の実出力。
func TestRedmineSHA1(t *testing.T) {
	h := RedmineHash("bd92efa3f59b61eed3d225ea45d136f7", "8fcd16713a435b2855edbeaea383b1b4bd103d65")
	if ok, _ := Verify(h, "admin"); !ok {
		t.Error("redmine hash verify failed")
	}
	if ok, _ := Verify(h, "Admin"); ok {
		t.Error("wrong password accepted")
	}
	if !NeedsRehash(h) {
		t.Error("redmine hash should need rehash")
	}
	if ok, _ := Verify("redmine-sha1-nosalt$$"+sha1hex("pw"), "pw"); !ok {
		t.Error("nosalt verify failed")
	}
	if ok, _ := Verify("", "x"); ok {
		t.Error("empty hash accepted")
	}
}
