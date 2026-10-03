// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package totp

import (
	"testing"
	"time"
)

// RFC 6238 付録 B のテストベクタ（SHA1, 鍵 "12345678901234567890"）の下 6 桁。
func TestCodeAtRFC6238(t *testing.T) {
	key := b32.EncodeToString([]byte("12345678901234567890"))
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"}
	for ts, want := range cases {
		got, err := CodeAt(key, ts/Interval)
		if err != nil || got != want {
			t.Errorf("CodeAt(%d) = %q, %v; want %q", ts, got, err, want)
		}
	}
}

func TestVerifyDriftAndReplay(t *testing.T) {
	key := RandomKey()
	if len(key) != 32 {
		t.Fatalf("key length %d", len(key))
	}
	now := time.Unix(1_800_000_015, 0)
	cur := Now(key, now)
	prev := Now(key, now.Add(-30*time.Second))
	old := Now(key, now.Add(-60*time.Second))
	if at, ok := Verify(key, cur[:3]+" "+cur[3:], now, nil); !ok || at != now.Unix()/30*30 {
		t.Fatalf("current code with space: %d %v", at, ok)
	}
	if _, ok := Verify(key, prev, now, nil); !ok {
		t.Fatal("previous step should be accepted (drift_behind 30)")
	}
	if _, ok := Verify(key, old, now, nil); ok && old != cur && old != prev {
		t.Fatal("2 steps behind should be rejected")
	}
	last := now.Unix() / 30 * 30
	if _, ok := Verify(key, cur, now, &last); ok {
		t.Fatal("replay of the same step must be rejected")
	}
	lastPrev := last - 30
	if _, ok := Verify(key, prev, now, &lastPrev); ok && prev != cur {
		t.Fatal("step equal to last used must be rejected")
	}
	if _, ok := Verify(key, cur, now, &lastPrev); !ok {
		t.Fatal("newer step must be accepted")
	}
}

func TestProvisioningURI(t *testing.T) {
	got := ProvisioningURI("JBSWY3DPEHPK3PXP", "localhost:3000", "jsmith")
	want := "otpauth://totp/localhost_3000:jsmith?secret=JBSWY3DPEHPK3PXP&issuer=localhost_3000"
	if got != want {
		t.Fatalf("got %s", got)
	}
	if got := ProvisioningURI("K", "a b", "x@y"); got != "otpauth://totp/a%20b:x%40y?secret=K&issuer=a%20b" {
		t.Fatalf("got %s", got)
	}
}

func TestQRCodeDataURL(t *testing.T) {
	u, err := QRCodeDataURL(ProvisioningURI(RandomKey(), "localhost:3000", "admin"))
	if err != nil || len(u) < 100 || u[:22] != "data:image/png;base64," {
		t.Fatalf("%v %q", err, u[:30])
	}
}
