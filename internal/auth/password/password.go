// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package password はパスワードハッシュの生成と検証を行う。
//
// 新規ハッシュは argon2id（"$argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>"）。
// Redmine から移行したハッシュ "redmine-sha1$<salt>$<hex>"（SHA1_hex(salt + SHA1_hex(pw))）と
// ソルトなしの "redmine-sha1-nosalt$$<hex>"（SHA1_hex(pw)）も検証でき、NeedsRehash で再ハッシュ要否を返す。
package password

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// argon2id のパラメータ（OWASP 推奨の m=19MiB, t=2, p=1）。
const (
	memory  = 19 * 1024
	time    = 2
	threads = 1
	keyLen  = 32
	saltLen = 16
)

var b64 = base64.RawStdEncoding

// Hash は argon2id でハッシュした文字列を返す。
func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, time, memory, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, time, threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// ErrUnknownFormat は未知のハッシュ形式。
var ErrUnknownFormat = errors.New("password: unknown hash format")

// dummyHash は DummyVerify が照合に使う argon2id ハッシュ（初回利用時に生成する）。
var dummyHash = sync.OnceValue(func() string {
	h, err := Hash("buropher-dummy-password")
	if err != nil {
		panic(err)
	}
	return h
})

// DummyVerify は結果を捨てて argon2id の照合を 1 回行う。存在しないユーザーやローカルのパスワードを
// 持たないユーザーのログイン試行でも、実在ユーザーのパスワード照合と同程度の時間をかけ、
// 応答時間の差からアカウントの有無を推測されないようにする。
func DummyVerify(pw string) {
	_, _ = verifyArgon2(dummyHash(), pw)
}

// Verify は pw が hash に一致するかを返す。hash が空ならパスワード未設定として false。
// 応答時間からハッシュの有無・形式を推測されないよう、argon2id 以外で不一致の場合も
// DummyVerify で argon2id 1 回分の時間をかける（一致した Redmine 形式は呼び出し側の再ハッシュで同程度になる）。
func Verify(hash, pw string) (bool, error) {
	switch {
	case hash == "":
		DummyVerify(pw)
		return false, nil
	case strings.HasPrefix(hash, "$argon2id$"):
		return verifyArgon2(hash, pw)
	case strings.HasPrefix(hash, "redmine-sha1$"), strings.HasPrefix(hash, "redmine-sha1-nosalt$"):
		parts := strings.SplitN(hash, "$", 3)
		if len(parts) != 3 {
			return false, ErrUnknownFormat
		}
		want := sha1hex(parts[1] + sha1hex(pw))
		if parts[0] == "redmine-sha1-nosalt" {
			want = sha1hex(pw)
		}
		ok := subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(parts[2]))) == 1
		if !ok {
			DummyVerify(pw)
		}
		return ok, nil
	}
	return false, ErrUnknownFormat
}

// NeedsRehash は現行方式（argon2id・同パラメータ）以外なら true。
func NeedsRehash(hash string) bool {
	return !strings.HasPrefix(hash, fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$", argon2.Version, memory, time, threads))
}

// RedmineHash は Redmine 形式のハッシュ文字列を組み立てる（移行・テスト用）。
func RedmineHash(salt, hexHash string) string { return "redmine-sha1$" + salt + "$" + hexHash }

func sha1hex(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func verifyArgon2(hash, pw string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		return false, ErrUnknownFormat
	}
	var v int
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil {
		return false, ErrUnknownFormat
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, ErrUnknownFormat
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, ErrUnknownFormat
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, ErrUnknownFormat
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
