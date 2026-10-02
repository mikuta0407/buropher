// Package rediscipher は Redmine の暗号化列(Redmine::Ciphering)と同じ形式の
// 暗号化・復号を実装する。
//
// 対象列: users.twofa_totp_key, repositories.password, auth_sources.account_password。
// 形式: "aes-256-cbc:" + Base64(暗号文) + "--" + Base64(IV)
// 鍵:   SHA256_hex(database_cipher_key) の先頭 32 文字(16 進文字列)をそのまま 32 バイト鍵として使う。
// パディング: PKCS#7(OpenSSL 既定)。
// 根拠: lib/redmine/ciphering.rb
package rediscipher

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Prefix は暗号化値の接頭辞。
const Prefix = "aes-256-cbc:"

// ErrNoKey は暗号化値を復号しようとしたが鍵が空のときに返る。
var ErrNoKey = errors.New("rediscipher: cipher key is empty")

// Ruby の /\Aaes-256-cbc:(.+)\Z/ に相当(\Z は末尾改行 1 個を許容)。
var cipherRe = regexp.MustCompile(`\Aaes-256-cbc:(.+)\n?\z`)

// DeriveKey は database_cipher_key から AES-256 鍵(32 バイト)を導出する。
// Ruby: Digest::SHA256.hexdigest(key)[0..31]
func DeriveKey(cipherKey string) []byte {
	sum := sha256.Sum256([]byte(cipherKey))
	return []byte(hex.EncodeToString(sum[:])[:32])
}

// IsEncrypted は値が暗号化形式かどうかを返す。
func IsEncrypted(s string) bool {
	return cipherRe.MatchString(s)
}

// Decrypt は Redmine::Ciphering.decrypt_text 相当。暗号化形式でなければ平文としてそのまま返す。
// cipherKey が空(Ruby の blank?)で暗号化値が渡された場合は ErrNoKey を返す。
func Decrypt(cipherKey, text string) (string, error) {
	m := cipherRe.FindStringSubmatch(text)
	if m == nil {
		return text, nil
	}
	if strings.TrimSpace(cipherKey) == "" {
		return text, ErrNoKey
	}
	parts := strings.Split(m[1], "--")
	if len(parts) < 2 {
		return "", fmt.Errorf("rediscipher: malformed ciphertext (missing iv)")
	}
	e, err := decodeB64Lenient(parts[0])
	if err != nil {
		return "", fmt.Errorf("rediscipher: ciphertext base64: %w", err)
	}
	iv, err := decodeB64Lenient(parts[1])
	if err != nil {
		return "", fmt.Errorf("rediscipher: iv base64: %w", err)
	}
	block, err := aes.NewCipher(DeriveKey(cipherKey))
	if err != nil {
		return "", err
	}
	if len(iv) != aes.BlockSize {
		return "", fmt.Errorf("rediscipher: invalid iv length %d", len(iv))
	}
	if len(e) == 0 || len(e)%aes.BlockSize != 0 {
		return "", fmt.Errorf("rediscipher: invalid ciphertext length %d", len(e))
	}
	out := make([]byte, len(e))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, e)
	out, err = unpad(out)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Encrypt は Redmine::Ciphering.encrypt_text 相当。鍵または平文が空(blank)ならそのまま返す。
func Encrypt(cipherKey, text string) (string, error) {
	if strings.TrimSpace(cipherKey) == "" || strings.TrimSpace(text) == "" {
		return text, nil
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	return encryptWithIV(cipherKey, text, iv)
}

func encryptWithIV(cipherKey, text string, iv []byte) (string, error) {
	block, err := aes.NewCipher(DeriveKey(cipherKey))
	if err != nil {
		return "", err
	}
	p := pad([]byte(text))
	out := make([]byte, len(p))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, p)
	return Prefix + base64.StdEncoding.EncodeToString(out) + "--" + base64.StdEncoding.EncodeToString(iv), nil
}

func pad(b []byte) []byte {
	n := aes.BlockSize - len(b)%aes.BlockSize
	return append(b, bytes.Repeat([]byte{byte(n)}, n)...)
}

func unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("rediscipher: empty plaintext block")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, errors.New("rediscipher: bad decrypt (wrong key?)")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("rediscipher: bad decrypt (wrong key?)")
		}
	}
	return b[:len(b)-n], nil
}

// decodeB64Lenient は Ruby の Base64.decode64 のように改行・不正文字を無視して復号する。
func decodeB64Lenient(s string) ([]byte, error) {
	var sb strings.Builder
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' {
			sb.WriteRune(c)
		}
	}
	clean := sb.String()
	return base64.RawStdEncoding.DecodeString(clean)
}
