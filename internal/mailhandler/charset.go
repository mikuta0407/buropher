// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

// このファイルは文字コードの変換（Mail::Utilities.pick_encoding と Redmine::CodesetUtil.to_utf8）。

var (
	iso8859Re   = regexp.MustCompile(`^iso[-_]?8859-(\d+)(-i)?$`)
	isoShortRe  = regexp.MustCompile(`^iso[-_]?(\d{4})-?(\w{1,2})$`)
	isoLongRe   = regexp.MustCompile(`^iso[-_]?(\d{4})-?(\w{1,2})-?(\w*)$`)
	utfRe       = regexp.MustCompile(`^utf[\-_]?(\d{1,2})?(\w{1,2})$`)
	windowsRe   = regexp.MustCompile(`^windows-?(.*)$`)
	ansiX3Re    = regexp.MustCompile(`ansi_x3.110-1983`)
	windows1258 = regexp.MustCompile(`(?i)Windows-?1258`)
)

// pickEncoding は Mail::Utilities.pick_encoding（BestEffortCharsetEncoder#pick_encoding を含む）。
// 文字コード名を正規化した名前を返す（空なら UTF-8 扱い）。
func pickEncoding(charset string) string {
	switch {
	case ansiX3Re.MatchString(charset):
		charset = "ISO-8859-1"
	case windows1258.MatchString(charset):
		charset = "Windows-1252"
	}
	lc := strings.ToLower(charset)
	switch {
	case iso8859Re.MatchString(lc):
		m := iso8859Re.FindStringSubmatch(lc)
		return "ISO-8859-" + m[1]
	case isoShortRe.MatchString(lc):
		m := isoShortRe.FindStringSubmatch(lc)
		return "ISO-" + m[1] + "-" + m[2]
	case isoLongRe.MatchString(lc):
		m := isoLongRe.FindStringSubmatch(lc)
		return "ISO-" + m[1] + "-" + m[2] + "-" + m[3]
	case utfRe.MatchString(lc):
		m := utfRe.FindStringSubmatch(lc)
		s := "UTF-" + m[1] + m[2]
		if s == "UTF-16" || s == "UTF-32" {
			s += "BE"
		}
		return s
	case windowsRe.MatchString(lc):
		return "Windows-" + windowsRe.FindStringSubmatch(lc)[1]
	case lc == "8bit":
		return "ASCII-8BIT"
	case lc == "iso646" || lc == "iso-646" || lc == "iso_646" || lc == "iso646-us" || lc == "iso-646-us" || lc == "us=ascii":
		return "US-ASCII"
	case lc == "macintosh":
		return "macRoman"
	case lc == "ks_c_5601-1987":
		return "CP949"
	case lc == "shift-jis":
		return "Shift_JIS"
	case lc == "gb2312":
		return "GB18030"
	case lc == "cp-850":
		return "CP850"
	case lc == "latin2":
		return "ISO-8859-2"
	}
	return charset
}

// lookupEncoding は正規化済みの文字コード名に対応する x/text の encoding（UTF-8・不明なら nil）。
func lookupEncoding(name string) encoding.Encoding {
	switch strings.ToLower(name) {
	case "", "utf-8", "utf8", "us-ascii", "ascii", "ascii-8bit", "binary":
		return nil
	case "iso-2022-jp", "iso-2022-jp-ms", "cp50220", "cp50221", "iso-2022-jp-2", "iso-2022-jp-3":
		return japanese.ISO2022JP
	case "shift_jis", "sjis", "cp932", "windows-31j":
		return japanese.ShiftJIS
	case "euc-jp", "eucjp", "eucjp-ms", "cp51932":
		return japanese.EUCJP
	case "cp949", "euc-kr", "ks_c_5601-1987":
		return korean.EUCKR
	case "gb18030":
		return simplifiedchinese.GB18030
	case "gbk", "cp936":
		return simplifiedchinese.GBK
	case "macroman":
		return charmap.Macintosh
	case "cp850":
		return charmap.CodePage850
	case "utf-16be":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	case "utf-16le":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	}
	if e, err := htmlindex.Get(name); err == nil && e != nil {
		if e == unicode.UTF8 {
			return nil
		}
		return e
	}
	if e, err := ianaindex.IANA.Encoding(name); err == nil && e != nil {
		if e == unicode.UTF8 {
			return nil
		}
		return e
	}
	return nil
}

// toUTF8 は Redmine::CodesetUtil.to_utf8(str, Mail::Utilities.pick_encoding(charset))。
// 変換できない文字は replacement に置き換える（本文は "?"、ヘッダは ""）。
// 不明な文字コード名は UTF-8 として扱う（Ruby では例外になり受信全体が失敗する）。
func toUTF8(b []byte, charset, replacement string) string {
	if len(b) == 0 {
		return ""
	}
	enc := lookupEncoding(pickEncoding(charset))
	if enc == nil {
		return replaceInvalidUTF8(b, replacement)
	}
	out, err := enc.NewDecoder().Bytes(b)
	if err != nil {
		return replaceInvalidUTF8(b, replacement)
	}
	s := string(out)
	if strings.ContainsRune(s, utf8.RuneError) {
		s = strings.ReplaceAll(s, string(utf8.RuneError), replacement)
	}
	return s
}

// replaceInvalidUTF8 は Redmine::CodesetUtil.replace_invalid_utf8（不正なバイトを replacement に置き換える）。
func replaceInvalidUTF8(b []byte, replacement string) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n <= 1 {
			sb.WriteString(replacement)
			b = b[1:]
			continue
		}
		sb.Write(b[:n])
		b = b[n:]
	}
	return sb.String()
}
