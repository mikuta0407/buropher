// Package secretbox は DB に保存する秘密値(TOTP シークレット、LDAP バインドパスワード、
// リポジトリのパスワード、OIDC クライアントシークレット等)を buropher の秘密鍵で暗号化する。
//
// 形式(版 1):
//
//	"sb1:" + base64url_nopad( nonce(12 バイト) || AES-256-GCM 暗号文 || タグ(16 バイト) )
//
// 鍵は HMAC-SHA256(secret, "buropher.secretbox.v1") の 32 バイト(secret は設定の server.secret_key)。
// 追加認証データ(AAD)はなし。nonce は暗号化ごとに crypto/rand で生成する。
//
// Redmine 形式("aes-256-cbc:<b64>--<b64iv>")は rediscipher パッケージで扱う。
// 接頭辞で判別できるため、移行途中の値が混在しても Open 前に IsSealed で区別できる。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// Prefix は暗号化値の接頭辞(版 1)。
const Prefix = "sb1:"

const keyPurpose = "buropher.secretbox.v1"

var (
	// ErrNoKey は秘密鍵が空。
	ErrNoKey = errors.New("secretbox: secret key is empty")
	// ErrNotSealed は値が secretbox 形式でない。
	ErrNotSealed = errors.New("secretbox: value is not sealed")
	// ErrDecrypt は復号(認証)に失敗した(鍵違い・改ざん)。
	ErrDecrypt = errors.New("secretbox: decryption failed")
)

var b64 = base64.RawURLEncoding

// Box は 1 つの秘密鍵に対する暗号化・復号器。
type Box struct {
	aead cipher.AEAD
}

// New は secret から Box を作る。secret が空なら ErrNoKey。
func New(secret string) (*Box, error) {
	if secret == "" {
		return nil, ErrNoKey
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(keyPurpose))
	block, err := aes.NewCipher(h.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// IsSealed は値が secretbox 形式かどうかを返す。
func IsSealed(s string) bool { return strings.HasPrefix(s, Prefix) }

// Seal は平文を暗号化して "sb1:..." 形式の文字列を返す。
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return Prefix + b64.EncodeToString(ct), nil
}

// Open は "sb1:..." 形式の値を復号する。
func (b *Box) Open(sealed string) (string, error) {
	rest, ok := strings.CutPrefix(sealed, Prefix)
	if !ok {
		return "", ErrNotSealed
	}
	raw, err := b64.DecodeString(rest)
	if err != nil || len(raw) < b.aead.NonceSize()+b.aead.Overhead() {
		return "", ErrDecrypt
	}
	ns := b.aead.NonceSize()
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(pt), nil
}
