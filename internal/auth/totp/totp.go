// Package totp は Redmine の 2 要素認証 TOTP 方式（lib/redmine/twofa/totp.rb, ROTP 6.3）の移植。
//
//   - 鍵: ROTP::Base32.random（20 バイト乱数の Base32。32 文字・パディングなし・大文字）
//   - コード: HMAC-SHA1・6 桁・30 秒間隔（RFC 6238）
//   - 検証: 空白を除去し、drift_behind = 30 秒（現在と 1 つ前のタイムステップ）を試す。
//     after（twofa_totp_last_used_at）以前のタイムステップは拒否する（リプレイ防止）。
//     一致したタイムステップの UNIX 秒（timecode * 30）を返し、呼び出し側はそれを保存する。
//   - 登録用 URI: ROTP::OTP::URI と同じ書式（otpauth://totp/<issuer>:<account>?secret=..&issuer=..）
//   - QR コード: Redmine は rqrcode（誤り訂正レベル H、余白 0、280px、透明背景の PNG data URL）。
//     ここでは boombuler/barcode の QR エンコーダで同じ条件の PNG を作る。
//     エンコーダの実装差（マスクパターン選択など）により画像のバイト列は Redmine と一致しない場合がある。
package totp

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"time"
	"unicode"

	"github.com/boombuler/barcode/qr"
)

// Interval は TOTP の間隔（秒）。
const Interval = 30

// AllowedDrift は Redmine::Twofa::Base#allowed_drift（秒）。
const AllowedDrift = 30

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// RandomKey は ROTP::Base32.random（20 バイト → 32 文字）。
func RandomKey() string {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b[:])
}

// decodeKey は Base32 の鍵をバイト列にする（ROTP::Base32.decode と同様に大文字小文字・パディングを許容）。
func decodeKey(key string) ([]byte, error) {
	k := strings.ToUpper(strings.TrimRight(strings.TrimSpace(key), "="))
	return b32.DecodeString(k)
}

// CodeAt は timecode（UNIX 秒 / 30）のコード（6 桁の 0 詰め文字列）を返す。
func CodeAt(key string, timecode int64) (string, error) {
	secret, err := decodeKey(key)
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(timecode))
	h := hmac.New(sha1.New, secret)
	h.Write(msg[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", v%1000000), nil
}

// Now は時刻 t のコード（テスト用）。
func Now(key string, t time.Time) string {
	c, _ := CodeAt(key, t.Unix()/Interval)
	return c
}

// stripSpace は code.remove(/[[:space:]]/)。
func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// Verify は Redmine::Twofa::Totp#verify_otp! の検証部分（ROTP::TOTP#verify(code, drift_behind: 30, after: lastUsed)）。
// 一致すれば一致したタイムステップの UNIX 秒と true を返す。lastUsed が nil なら after を使わない。
func Verify(key, code string, now time.Time, lastUsed *int64) (int64, bool) {
	code = stripSpace(code)
	if code == "" {
		return 0, false
	}
	start := (now.Unix() - AllowedDrift) / Interval
	end := now.Unix() / Interval
	var result int64
	ok := false
	for t := start; t <= end; t++ {
		if lastUsed != nil && t <= *lastUsed/Interval {
			continue
		}
		c, err := CodeAt(key, t)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 {
			// ROTP は後のタイムステップで上書きする（最後に一致したもの）
			result = t * Interval
			ok = true
		}
	}
	return result, ok
}

// urlEncode は ERB::Util.url_encode（英数字と _-.~ 以外を %XX にする）。
func urlEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ProvisioningURI は ROTP::TOTP.new(key, issuer: issuer).provisioning_uri(account)。
func ProvisioningURI(key, issuer, account string) string {
	var labels []string
	iss := ""
	// Setting.host_name は常に文字列（nil にならない）ため issuer は常に付ける
	hasIssuer := true
	if hasIssuer {
		iss = strings.ReplaceAll(strings.TrimSpace(issuer), ":", "_")
		labels = append(labels, urlEncode(iss))
	}
	labels = append(labels, urlEncode(strings.ReplaceAll(strings.TrimRight(account, " \t\r\n\f\v\x00"), ":", "_")))
	params := "secret=" + urlEncode(key)
	if hasIssuer {
		params += "&issuer=" + urlEncode(iss)
	}
	return "otpauth://totp/" + strings.Join(labels, ":") + "?" + params
}

// QRCodePNG は RQRCode::QRCode.new(uri).as_png(fill: TRANSPARENT, resize_exactly_to: 280, border_modules: 0)。
func QRCodePNG(content string, size int) ([]byte, error) {
	code, err := qr.Encode(content, qr.H, qr.Auto)
	if err != nil {
		return nil, err
	}
	n := code.Bounds().Dx()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	black := color.NRGBA{A: 0xff}
	for y := 0; y < size; y++ {
		my := y * n / size
		for x := 0; x < size; x++ {
			mx := x * n / size
			if r, _, _, _ := code.At(mx, my).RGBA(); r == 0 {
				img.SetNRGBA(x, y, black)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// QRCodeDataURL は QR コードの PNG の data URL（ChunkyPNG::Image#to_data_url）。
func QRCodeDataURL(content string) (string, error) {
	b, err := QRCodePNG(content, 280)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b), nil
}
