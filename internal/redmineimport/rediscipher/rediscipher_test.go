// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rediscipher

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// testdata/ruby_ciphertexts.json は Redmine 6.1.2 の Redmine::Ciphering.encrypt_text で生成した値。
type rubyCase struct {
	Key    string `json:"key"`
	Plain  string `json:"plain"`
	Cipher string `json:"cipher"`
}

func loadRubyCases(t *testing.T) []rubyCase {
	t.Helper()
	b, err := os.ReadFile("testdata/ruby_ciphertexts.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []rubyCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestDecryptRubyGenerated(t *testing.T) {
	for _, c := range loadRubyCases(t) {
		got, err := Decrypt(c.Key, c.Cipher)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", c.Cipher, err)
		}
		if got != c.Plain {
			t.Errorf("Decrypt = %q, want %q", got, c.Plain)
		}
		// Ruby の \Z と同様に末尾改行 1 個は許容
		if got, err := Decrypt(c.Key, c.Cipher+"\n"); err != nil || got != c.Plain {
			t.Errorf("trailing newline: %q %v", got, err)
		}
	}
}

func TestEncryptMatchesRubyWithSameIV(t *testing.T) {
	for _, c := range loadRubyCases(t) {
		ivB64 := c.Cipher[strings.LastIndex(c.Cipher, "--")+2:]
		iv, _ := base64.StdEncoding.DecodeString(ivB64)
		got, err := encryptWithIV(c.Key, c.Plain, iv)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.Cipher {
			t.Errorf("encrypt = %q, want %q", got, c.Cipher)
		}
	}
}

func TestRoundTripAndPassthrough(t *testing.T) {
	enc, err := Encrypt("key", "hello")
	if err != nil || !IsEncrypted(enc) {
		t.Fatalf("Encrypt: %q %v", enc, err)
	}
	dec, err := Decrypt("key", enc)
	if err != nil || dec != "hello" {
		t.Fatalf("Decrypt: %q %v", dec, err)
	}
	if _, err := Decrypt("wrong", enc); err == nil {
		// 誤った鍵ではパディング検証でほぼ確実に失敗する
		t.Log("wrong key happened to unpad cleanly")
	}
	if got, _ := Decrypt("key", "plain-text"); got != "plain-text" {
		t.Errorf("plaintext passthrough = %q", got)
	}
	if got, err := Decrypt("", enc); !errors.Is(err, ErrNoKey) || got != enc {
		t.Errorf("no key: %q %v", got, err)
	}
	if got, _ := Encrypt("", "x"); got != "x" {
		t.Errorf("blank key encrypt = %q", got)
	}
	if got, _ := Encrypt("k", "  "); got != "  " {
		t.Errorf("blank text encrypt = %q", got)
	}
}
