// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package csvexport は Redmine::Export::CSV（lib/redmine/export/csv.rb）の移植。
//
// Ruby の CSV（:col_sep, :encoding）と同じ規則で出力する:
//   - UTF-8 なら先頭に BOM を付ける。それ以外（l(:general_csv_encoding) の ISO-8859-1 等）は
//     Redmine::CodesetUtil.from_utf8 と同じく変換できない文字を "?" にする
//   - フィールドは区切り文字・引用符・改行（\r, \n）を含む場合と空文字列の場合に引用符で囲む
//     （nil は空のまま）。引用符は 2 重にする。行末は "\n"
//   - Float は "%.2f" で小数点を l(:general_csv_decimal_separator) に置き換える（呼び出し側で文字列化する）
package csvexport

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// Options は Generate のオプション。
type Options struct {
	// Separator は列の区切り（params[:field_separator] が空なら l(:general_csv_separator)）。
	Separator string
	// Encoding は文字コード名（params[:encoding]。不明なら l(:general_csv_encoding)）。
	Encoding string
	// DefaultEncoding は l(:general_csv_encoding)。
	DefaultEncoding string
}

// Field は 1 フィールド。Nil が true なら nil（引用符なしの空）。
type Field struct {
	Value string
	Nil   bool
}

// S は文字列のフィールド。
func S(s string) Field { return Field{Value: s} }

// Nil は nil のフィールド。
func Nil() Field { return Field{Nil: true} }

// Writer は CSV を組み立てる。
type Writer struct {
	buf bytes.Buffer
	sep string
	enc encoding.Encoding // nil なら UTF-8
}

// New は Writer を返す。
func New(o Options) *Writer {
	w := &Writer{sep: o.Separator}
	if w.sep == "" {
		w.sep = ","
	}
	enc, utf := lookup(o.Encoding)
	if !utf && enc == nil {
		enc, utf = lookup(o.DefaultEncoding)
	}
	if utf || enc == nil {
		w.buf.WriteString("\xEF\xBB\xBF")
	} else {
		w.enc = enc
	}
	return w
}

// lookup は文字コード名からエンコーディングを返す（UTF-8 なら utf=true）。
func lookup(name string) (encoding.Encoding, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "UTF-8", "UTF8":
		return nil, true
	case "ISO-8859-1", "ISO8859-1":
		return charmap.ISO8859_1, false
	case "ISO-8859-2":
		return charmap.ISO8859_2, false
	case "ISO-8859-15":
		return charmap.ISO8859_15, false
	case "CP1250", "WINDOWS-1250":
		return charmap.Windows1250, false
	case "CP1251", "WINDOWS-1251":
		return charmap.Windows1251, false
	case "CP1252", "WINDOWS-1252":
		return charmap.Windows1252, false
	case "CP1253":
		return charmap.Windows1253, false
	case "CP1254":
		return charmap.Windows1254, false
	case "CP1257":
		return charmap.Windows1257, false
	case "CP932", "WINDOWS-31J", "SHIFT_JIS":
		return japanese.ShiftJIS, false
	case "EUC-JP":
		return japanese.EUCJP, false
	case "GB18030":
		return simplifiedchinese.GB18030, false
	case "GBK":
		return simplifiedchinese.GBK, false
	case "BIG5":
		return traditionalchinese.Big5, false
	case "EUC-KR", "CP949":
		return korean.EUCKR, false
	case "KOI8-R":
		return charmap.KOI8R, false
	}
	return nil, false
}

// Row は 1 行を追加する。
func (w *Writer) Row(fields ...Field) {
	for i, f := range fields {
		if i > 0 {
			w.buf.WriteString(w.sep)
		}
		if f.Nil {
			continue
		}
		v := w.convert(f.Value)
		if v == "" || strings.Contains(v, w.sep) || strings.ContainsAny(v, "\"\r\n") {
			w.buf.WriteByte('"')
			w.buf.WriteString(strings.ReplaceAll(v, `"`, `""`))
			w.buf.WriteByte('"')
		} else {
			w.buf.WriteString(v)
		}
	}
	w.buf.WriteByte('\n')
}

// Strings は文字列の行を追加する。
func (w *Writer) Strings(fields ...string) {
	fs := make([]Field, len(fields))
	for i, s := range fields {
		fs[i] = S(s)
	}
	w.Row(fs...)
}

// convert は Redmine::CodesetUtil.from_utf8（変換できない文字は "?"）。
func (w *Writer) convert(s string) string {
	if w.enc == nil {
		return s
	}
	enc := w.enc.NewEncoder()
	var out strings.Builder
	for _, r := range s {
		if r == utf8.RuneError {
			out.WriteByte('?')
			continue
		}
		b, err := enc.Bytes([]byte(string(r)))
		if err != nil || len(b) == 0 {
			out.WriteByte('?')
			continue
		}
		out.Write(b)
	}
	return out.String()
}

// Bytes は出力を返す。
func (w *Writer) Bytes() []byte { return w.buf.Bytes() }
