// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package csvimport

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// codec は Ruby のエンコーディング 1 つ分。
type codec struct {
	enc encoding.Encoding
	// singleByte は 1 バイト文字コード（Ruby では常に valid_encoding? が真）。
	singleByte bool
	// ascii は US-ASCII（0x80 以上は不正）。
	ascii bool
	utf8  bool
	// sjis は Shift_JIS / Windows-31J（x/text の復号器が通す 0x80 等も不正として検査する）。
	sjis bool
}

// lookup は Ruby のエンコーディング名（Encoding.find と同じく大文字小文字を区別しない。別名を含む）から codec を返す。
func lookup(name string) (codec, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "UTF-8", "UTF8", "CP65001":
		return codec{utf8: true}, true
	case "US-ASCII", "ASCII", "ANSI_X3.4-1968", "646":
		return codec{ascii: true}, true
	case "WINDOWS-1250", "CP1250":
		return codec{enc: charmap.Windows1250, singleByte: true}, true
	case "WINDOWS-1251", "CP1251":
		return codec{enc: charmap.Windows1251, singleByte: true}, true
	case "WINDOWS-1252", "CP1252":
		return codec{enc: charmap.Windows1252, singleByte: true}, true
	case "WINDOWS-1253", "CP1253":
		return codec{enc: charmap.Windows1253, singleByte: true}, true
	case "WINDOWS-1254", "CP1254":
		return codec{enc: charmap.Windows1254, singleByte: true}, true
	case "WINDOWS-1255", "CP1255":
		return codec{enc: charmap.Windows1255, singleByte: true}, true
	case "WINDOWS-1256", "CP1256":
		return codec{enc: charmap.Windows1256, singleByte: true}, true
	case "WINDOWS-1257", "CP1257":
		return codec{enc: charmap.Windows1257, singleByte: true}, true
	case "WINDOWS-1258", "CP1258":
		return codec{enc: charmap.Windows1258, singleByte: true}, true
	case "WINDOWS-874", "CP874", "TIS-620":
		return codec{enc: charmap.Windows874, singleByte: true}, true
	case "WINDOWS-31J", "CP932", "CSWINDOWS31J", "SJIS", "SHIFT_JIS", "PCK":
		return codec{enc: japanese.ShiftJIS, sjis: true}, true
	case "ISO-2022-JP", "ISO2022-JP":
		return codec{enc: japanese.ISO2022JP}, true
	case "EUC-JP", "EUCJP", "EUCJP-MS", "EUC-JP-MS", "CP51932":
		return codec{enc: japanese.EUCJP}, true
	case "ISO-8859-1", "ISO8859-1":
		return codec{enc: charmap.ISO8859_1, singleByte: true}, true
	case "ISO-8859-2", "ISO8859-2":
		return codec{enc: charmap.ISO8859_2, singleByte: true}, true
	case "ISO-8859-3", "ISO8859-3":
		return codec{enc: charmap.ISO8859_3, singleByte: true}, true
	case "ISO-8859-4", "ISO8859-4":
		return codec{enc: charmap.ISO8859_4, singleByte: true}, true
	case "ISO-8859-5", "ISO8859-5":
		return codec{enc: charmap.ISO8859_5, singleByte: true}, true
	case "ISO-8859-6", "ISO8859-6":
		return codec{enc: charmap.ISO8859_6, singleByte: true}, true
	case "ISO-8859-7", "ISO8859-7":
		return codec{enc: charmap.ISO8859_7, singleByte: true}, true
	case "ISO-8859-8", "ISO8859-8":
		return codec{enc: charmap.ISO8859_8, singleByte: true}, true
	case "ISO-8859-9", "ISO8859-9":
		return codec{enc: charmap.ISO8859_9, singleByte: true}, true
	case "ISO-8859-13", "ISO8859-13":
		return codec{enc: charmap.ISO8859_13, singleByte: true}, true
	case "ISO-8859-15", "ISO8859-15":
		return codec{enc: charmap.ISO8859_15, singleByte: true}, true
	case "KOI8-R", "CP878":
		return codec{enc: charmap.KOI8R, singleByte: true}, true
	case "UTF-16":
		return codec{enc: unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM)}, true
	case "UTF-16BE", "UCS-2BE":
		return codec{enc: unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)}, true
	case "UTF-16LE":
		return codec{enc: unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)}, true
	case "CP949", "EUC-KR", "EUCKR":
		return codec{enc: korean.EUCKR}, true
	case "GB18030":
		return codec{enc: simplifiedchinese.GB18030}, true
	case "GBK", "CP936":
		return codec{enc: simplifiedchinese.GBK}, true
	case "BIG5", "BIG5-HKSCS", "CP950":
		return codec{enc: traditionalchinese.Big5}, true
	}
	return codec{}, false
}

// Known は name が Ruby で解釈できるエンコーディング名なら true。
func Known(name string) bool {
	_, ok := lookup(name)
	return ok
}

// Valid は data が name のエンコーディングとして正しいバイト列なら true（String#valid_encoding?）。
func Valid(data []byte, name string) bool {
	_, err := Decode(data, name)
	return err == nil
}

// Decode は data を name のエンコーディングとして UTF-8 に変換する。UTF-8 は BOM を除く（'bom|UTF-8'）。
// 不正なバイト列は "Invalid byte sequence in <name>" の MalformedError。
func Decode(data []byte, name string) (string, error) {
	c, ok := lookup(name)
	if !ok {
		// Ruby では Encoding.find が失敗して ArgumentError（EncodingError ではない）になるが、
		// 画面上はどちらも読み込めないファイルとして扱う
		return "", &MalformedError{Message: "Invalid byte sequence in " + name, Line: 1}
	}
	if name == "" {
		name = "UTF-8"
	}
	switch {
	case c.utf8:
		data = stripBOM(data)
		if !utf8.Valid(data) {
			return "", &MalformedError{Message: "Invalid byte sequence in " + name, Line: invalidLine(data, utf8.Valid)}
		}
		return string(data), nil
	case c.ascii:
		for _, b := range data {
			if b >= 0x80 {
				return "", &MalformedError{Message: "Invalid byte sequence in " + name, Line: 1}
			}
		}
		return string(data), nil
	}
	if c.sjis && !validSJIS(data) {
		return "", &MalformedError{Message: "Invalid byte sequence in " + name, Line: 1}
	}
	out, err := c.enc.NewDecoder().Bytes(data)
	if err != nil {
		return "", &MalformedError{Message: "Invalid byte sequence in " + name, Line: 1}
	}
	if !c.singleByte && strings.ContainsRune(string(out), utf8.RuneError) {
		return "", &MalformedError{Message: "Invalid byte sequence in " + name, Line: 1}
	}
	return string(out), nil
}

// invalidLine は最初の不正なバイト列を含む行の番号（1 始まり）。
func invalidLine(data []byte, valid func([]byte) bool) int {
	line := 1
	for _, l := range strings.SplitAfter(string(data), "\n") {
		if !valid([]byte(l)) {
			return line
		}
		line++
	}
	return line
}

// validSJIS は Shift_JIS のバイト列の構造（1 バイト文字と 2 バイト文字の先頭・後続バイトの範囲）を検査する。
func validSJIS(b []byte) bool {
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c < 0x80, c >= 0xA1 && c <= 0xDF:
		case c >= 0x81 && c <= 0x9F, c >= 0xE0 && c <= 0xFC:
			if i+1 >= len(b) {
				return false
			}
			t := b[i+1]
			if !(t >= 0x40 && t <= 0x7E || t >= 0x80 && t <= 0xFC) {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}
