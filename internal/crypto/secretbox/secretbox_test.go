package secretbox

import (
	"errors"
	"strings"
	"testing"
)

func TestSealOpen(t *testing.T) {
	b, err := New("secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "JBSWY3DPEHPK3PXP", "パスワード🎉"} {
		s, err := b.Seal(p)
		if err != nil {
			t.Fatal(err)
		}
		if !IsSealed(s) || strings.Contains(s, p) && p != "" {
			t.Fatalf("sealed = %q", s)
		}
		got, err := b.Open(s)
		if err != nil || got != p {
			t.Fatalf("Open(%q) = %q, %v", s, got, err)
		}
	}
	// 毎回 nonce が変わる
	s1, _ := b.Seal("x")
	s2, _ := b.Seal("x")
	if s1 == s2 {
		t.Error("nonce reused")
	}
	// 鍵違い・改ざん・形式違い
	other, _ := New("other")
	if _, err := other.Open(s1); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := b.Open(s1[:len(s1)-2] + "AA"); !errors.Is(err, ErrDecrypt) {
		t.Errorf("tampered: %v", err)
	}
	if _, err := b.Open("plain"); !errors.Is(err, ErrNotSealed) {
		t.Errorf("not sealed: %v", err)
	}
	if _, err := New(""); !errors.Is(err, ErrNoKey) {
		t.Errorf("empty key: %v", err)
	}
}
