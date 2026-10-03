// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package password

import (
	"testing"
	stdtime "time"
)

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

// TestArgon2Concurrency は argon2id の同時実行数が制限されること（並行ログインによるメモリ枯渇の防止）。
func TestArgon2Concurrency(t *testing.T) {
	h, err := Hash("secret")
	if err != nil {
		t.Fatal(err)
	}
	// 枠をすべて埋めると Verify は待たされる
	for range cap(argon2Sem) {
		argon2Sem <- struct{}{}
	}
	done := make(chan struct{})
	go func() {
		_, _ = Verify(h, "secret")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Verify ran beyond the concurrency limit")
	case <-stdtime.After(100 * stdtime.Millisecond):
	}
	for range cap(argon2Sem) {
		<-argon2Sem
	}
	<-done
}

// TestArgon2HugeParams は保存済みハッシュの過大なパラメータを拒否すること。
func TestArgon2HugeParams(t *testing.T) {
	for _, h := range []string{
		"$argon2id$v=19$m=4194304,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=19456,t=100000,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=19456,t=2,p=0$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
	} {
		if ok, err := Verify(h, "x"); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v", h, ok, err)
		}
	}
}
