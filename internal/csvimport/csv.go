// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package csvimport は Redmine の CSV インポート（app/models/import.rb）のうち、モデルや DB に依存しない部分
// （Ruby の CSV ライブラリと同じ規則での CSV 解析・文字コード変換、Date.strptime 互換の日付解析、
// set_default_settings の区切り文字・文字コードの推定）を移植する。
package csvimport

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MalformedError は CSV::MalformedCSVError（メッセージは "#{message} in line #{lineno}."）。
type MalformedError struct {
	Message string
	Line    int
}

func (e *MalformedError) Error() string { return fmt.Sprintf("%s in line %d.", e.Message, e.Line) }

// InvalidEncoding はメッセージが "Invalid byte sequence" を含む（文字コードの誤り）なら true。
func (e *MalformedError) InvalidEncoding() bool {
	return strings.Contains(e.Message, "Invalid byte sequence")
}

// Row は CSV の 1 行。値が nil（*string が nil）は引用符なしの空欄（Ruby の nil）。
type Row []*string

// Get は row[i]（範囲外・nil は nil）。
func (r Row) Get(i int) *string {
	if i < 0 || i >= len(r) {
		return nil
	}
	return r[i]
}

// Options は CSV.foreach のオプション（col_sep / quote_char / encoding）。
type Options struct {
	// Separator は col_sep（1 文字でなければ既定の ","）。
	Separator string
	// Wrapper は quote_char（1 文字でなければ既定の '"'）。
	Wrapper string
	// Encoding は外部エンコーディング（空なら UTF-8。UTF-8 は BOM を除く 'bom|UTF-8'）。
	Encoding string
}

// Parse は data を CSV として解析する（Ruby の csv 3.x の parse_quotable_robust と同じ規則）。
// fn が false を返すと読み込みを打ち切る。
func Parse(data []byte, o Options, fn func(Row) bool) error {
	text, err := Decode(data, o.Encoding)
	if err != nil {
		return err
	}
	sep := ","
	if utf8.RuneCountInString(o.Separator) == 1 {
		sep = o.Separator
	}
	quote := `"`
	if utf8.RuneCountInString(o.Wrapper) == 1 {
		quote = o.Wrapper
	}
	p := &parser{s: text, sep: sep, quote: quote, rowSep: detectRowSeparator(text)}
	return p.parse(fn)
}

// detectRowSeparator は row_sep: :auto（最初に現れる改行の種類。無ければ "\n"）。
func detectRowSeparator(s string) string {
	lf := strings.IndexByte(s, '\n')
	var cr int
	if lf >= 0 {
		cr = strings.IndexByte(s[:lf], '\r')
	} else {
		cr = strings.IndexByte(s, '\r')
	}
	switch {
	case cr >= 0 && lf >= 0:
		if cr+1 == lf {
			return "\r\n"
		}
		return "\r"
	case cr >= 0:
		return "\r"
	}
	return "\n"
}

type parser struct {
	s      string
	pos    int
	sep    string
	quote  string
	rowSep string
	lineno int
}

func (p *parser) eos() bool { return p.pos >= len(p.s) }

func (p *parser) rest() string { return p.s[p.pos:] }

// lineEnd は @line_end（\r\n|\n|\r）に一致する長さ（無ければ 0）。
func (p *parser) lineEnd() int {
	r := p.rest()
	switch {
	case strings.HasPrefix(r, "\r\n"):
		return 2
	case strings.HasPrefix(r, "\n"), strings.HasPrefix(r, "\r"):
		return 1
	}
	return 0
}

// ignoreBrokenLine は ignore_broken_line（行末まで読み飛ばして lineno を進める）。
func (p *parser) ignoreBrokenLine() {
	for !p.eos() && p.s[p.pos] != '\r' && p.s[p.pos] != '\n' {
		p.pos++
	}
	p.pos += p.lineEnd()
	p.lineno++
}

func (p *parser) fail(msg string) error {
	p.ignoreBrokenLine()
	return &MalformedError{Message: msg, Line: p.lineno}
}

// unquotedValue は @unquoted_value（引用符・区切り文字の先頭文字・改行以外の連続）。
func (p *parser) unquotedValue() (string, bool) {
	start := p.pos
	sepFirst, _ := utf8.DecodeRuneInString(p.sep)
	quote, _ := utf8.DecodeRuneInString(p.quote)
	for p.pos < len(p.s) {
		r, n := utf8.DecodeRuneInString(p.s[p.pos:])
		if r == quote || r == sepFirst || r == '\r' || r == '\n' {
			break
		}
		p.pos += n
	}
	if p.pos == start {
		return "", false
	}
	return p.s[start:p.pos], true
}

func (p *parser) scanQuotes() int {
	n := 0
	for strings.HasPrefix(p.s[p.pos:], p.quote) {
		p.pos += len(p.quote)
		n++
	}
	return n
}

// quotedValue は parse_quoted_column_value。ok=false は引用符で始まらない。
func (p *parser) quotedValue() (string, bool, error) {
	nq := p.scanQuotes()
	if nq == 0 {
		return "", false, nil
	}
	if nq%2 == 0 {
		return strings.Repeat(p.quote, (nq-2)/2), true, nil
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(p.quote, nq/2))
	for {
		if i := strings.Index(p.rest(), p.quote); i > 0 {
			b.WriteString(p.s[p.pos : p.pos+i])
			p.pos += i
		} else if i < 0 {
			b.WriteString(p.rest())
			p.pos = len(p.s)
		}
		nq := p.scanQuotes()
		if nq == 0 {
			return "", true, p.fail("Unclosed quoted field")
		}
		if nq == 1 {
			break
		}
		b.WriteString(strings.Repeat(p.quote, nq/2))
		if nq%2 == 1 {
			break
		}
	}
	return b.String(), true, nil
}

func (p *parser) parse(fn func(Row) bool) error {
	var row Row
	emit := func(r Row) bool {
		p.lineno++
		return fn(r)
	}
	for {
		quoted, unquoted := false, false
		var value *string
		if v, ok, err := p.quotedValue(); err != nil {
			return err
		} else if ok {
			quoted = true
			value = &v
		} else if v, ok := p.unquotedValue(); ok {
			unquoted = true
			value = &v
		}
		switch {
		case strings.HasPrefix(p.rest(), p.sep):
			p.pos += len(p.sep)
			row = append(row, value)
		case strings.HasPrefix(p.rest(), p.rowSep):
			p.pos += len(p.rowSep)
			if len(row) == 0 && value == nil {
				if !emit(Row{}) {
					return nil
				}
			} else {
				row = append(row, value)
				if !emit(row) {
					return nil
				}
				row = nil
			}
		case p.eos():
			if len(row) == 0 && value == nil {
				return nil
			}
			row = append(row, value)
			emit(row)
			return nil
		default:
			switch {
			case quoted:
				return p.fail("Any value after quoted field isn't allowed")
			case unquoted && p.lineEnd() > 0:
				nl := p.rest()[:p.lineEnd()]
				p.pos += len(nl)
				return p.fail("Unquoted fields do not allow new line <" + rubyInspect(nl) + ">")
			case strings.HasPrefix(p.rest(), p.quote):
				return p.fail("Illegal quoting")
			case p.lineEnd() > 0:
				nl := p.rest()[:p.lineEnd()]
				p.pos += len(nl)
				return p.fail("New line must be <" + rubyInspect(p.rowSep) + "> not <" + rubyInspect(nl) + ">")
			default:
				return p.fail("TODO: Meaningful message")
			}
		}
	}
}

// rubyInspect は改行文字列の String#inspect。
func rubyInspect(s string) string {
	q := strconv.Quote(s)
	return q
}

// ErrEncoding は文字コードとして不正なバイト列（EncodingError）。
var ErrEncoding = errors.New("csvimport: invalid byte sequence")

// FirstRows は first_rows(count)（ヘッダを含む先頭 count 行）。
func FirstRows(data []byte, o Options, count int) ([]Row, error) {
	var rows []Row
	err := Parse(data, o, func(r Row) bool {
		rows = append(rows, r)
		return len(rows) < count
	})
	return rows, err
}

// stripBOM は 'bom|UTF-8' の BOM 除去。
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
}
