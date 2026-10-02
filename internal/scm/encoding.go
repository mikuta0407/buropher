// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package scm

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

// lookupEncoding は Ruby の Encoding.find 相当（UTF-8 なら nil, true）。見つからなければ nil, false。
func lookupEncoding(name string) (encoding.Encoding, bool) {
	n := strings.TrimSpace(name)
	switch strings.ToUpper(n) {
	case "", "UTF-8", "UTF8":
		return nil, true
	case "CP932", "WINDOWS-31J":
		return japanese.ShiftJIS, true
	case "UTF-16":
		return unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM), true
	}
	e, err := ianaindex.IANA.Encoding(n)
	if err != nil || e == nil {
		if e, err = ianaindex.MIME.Encoding(n); err != nil || e == nil {
			return nil, false
		}
	}
	return e, true
}

// isUTF8 は名前が UTF-8 か。
func isUTF8(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "UTF-8", "UTF8":
		return true
	}
	return false
}

// Iconv は AbstractAdapter#scm_iconv(to, from, str)。変換できなければ ok=false（Ruby の nil）。
func Iconv(to, from, s string) (string, bool) {
	if strings.EqualFold(to, from) || (isUTF8(to) && isUTF8(from)) {
		return s, true
	}
	if isUTF8(to) {
		enc, ok := lookupEncoding(from)
		if !ok {
			return "", false
		}
		out, err := enc.NewDecoder().String(s)
		if err != nil || !utf8.ValidString(out) {
			return "", false
		}
		return out, true
	}
	if !utf8.ValidString(s) {
		return "", false
	}
	enc, ok := lookupEncoding(to)
	if !ok {
		return "", false
	}
	if enc == nil {
		return s, true
	}
	// 表現できない文字はエラーになる（Ruby の Encoding::UndefinedConversionError → nil）
	out, err := enc.NewEncoder().String(s)
	if err != nil {
		return "", false
	}
	return out, true
}

// ReplaceInvalidUTF8 は Redmine::CodesetUtil.replace_invalid_utf8（不正なバイトを "?" にする）。
func ReplaceInvalidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteByte('?')
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// ToUTF8 は Redmine::CodesetUtil.to_utf8(str, encoding)（変換できない文字は "?"）。
func ToUTF8(s, enc string) string {
	if s == "" || isUTF8(enc) {
		return ReplaceInvalidUTF8(s)
	}
	e, ok := lookupEncoding(enc)
	if !ok || e == nil {
		return ReplaceInvalidUTF8(s)
	}
	out, err := e.NewDecoder().String(s)
	if err != nil {
		return ReplaceInvalidUTF8(s)
	}
	return strings.ReplaceAll(out, "�", "?")
}

// ToUTF8BySetting は Redmine::CodesetUtil.to_utf8_by_setting（Setting.repositories_encodings の
// 文字コードを順に試し、どれでも変換できなければ不正なバイトを "?" にする）。
func ToUTF8BySetting(s, encodings string) string {
	if s == "" || isASCIIText(s) {
		return s
	}
	for _, name := range strings.Split(encodings, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if isUTF8(name) {
			if utf8.ValidString(s) {
				return s
			}
			continue
		}
		e, ok := lookupEncoding(name)
		if !ok || e == nil {
			continue
		}
		out, err := e.NewDecoder().String(s)
		if err == nil && utf8.ValidString(out) && !strings.ContainsRune(out, utf8.RuneError) {
			return out
		}
	}
	return ReplaceInvalidUTF8(s)
}

// isASCIIText は /\A[\r\n\t\x20-\x7e]*\Z/n。
func isASCIIText(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\r' && c != '\n' && c != '\t' && (c < 0x20 || c > 0x7e) {
			return false
		}
	}
	return true
}
