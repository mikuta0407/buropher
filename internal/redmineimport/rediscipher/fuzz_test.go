// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rediscipher

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

// FuzzDecrypt は暗号化値の復号（不正な base64・iv・パディング）が panic せず、時間・割り当てが入力長に比例すること、
// 暗号化 → 復号で元の値に戻ることを確かめる。
//
//	go test -run '^$' -fuzz FuzzDecrypt ./internal/redmineimport/rediscipher
func FuzzDecrypt(f *testing.F) {
	f.Add("key", "aes-256-cbc:YWJj--AAAAAAAAAAAAAAAAAAAAAA==", "secret")
	f.Add("key", "aes-256-cbc:--", "")
	f.Add(" ", "aes-256-cbc:!!!!--????", "x")
	f.Add("k", "aes-256-cbc:"+string(make([]byte, 64))+"--AAAA", "\x00")
	f.Fuzz(func(t *testing.T, key, text, plain string) {
		secoracle.Bounded(t, len(key)+len(text)+len(plain), 32, func() {
			_, _ = Decrypt(key, text)
			_ = IsEncrypted(text)
			enc, err := Encrypt(key, plain)
			if err != nil {
				return
			}
			dec, err := Decrypt(key, enc)
			if err != nil || dec != plain {
				t.Fatalf("Encrypt/Decrypt round trip of %q with key %q: %q, %v", plain, key, dec, err)
			}
		})
	})
}
