// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package pdftest は buropher が生成した PDF をテストで調べるための簡易な解析器
// （ページ数と、各ページの Tj で描いた文字列。fpdf の UTF-8 フォントは UTF-16BE で書かれる）。
package pdftest

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
)

var (
	reStream = regexp.MustCompile(`(?s)<<([^>]*(?:>[^>][^>]*)*)>>\s*stream\r?\n`)
	reTj     = regexp.MustCompile(`(?s)\(((?:\\.|[^\\)])*)\)\s*Tj`)
	rePage   = regexp.MustCompile(`/Type\s*/Page[^s]`)
)

// Doc は解析した PDF。
type Doc struct {
	// Pages はページ数。
	Pages int
	// Streams は展開したストリームのうち文字を含むもの（ページ順）。
	Streams []string
}

// Parse は PDF を解析する。
func Parse(data []byte) *Doc {
	d := &Doc{Pages: len(rePage.FindAll(data, -1))}
	s := data
	for {
		loc := reStream.FindSubmatchIndex(s)
		if loc == nil {
			break
		}
		dict := string(s[loc[2]:loc[3]])
		rest := s[loc[1]:]
		end := bytes.Index(rest, []byte("endstream"))
		if end < 0 {
			break
		}
		body := rest[:end]
		if strings.Contains(dict, "/FlateDecode") {
			if zr, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
				if out, err := io.ReadAll(zr); err == nil || len(out) > 0 {
					body = out
				}
			}
		}
		if !strings.Contains(dict, "/Length1") && bytes.Contains(body, []byte("BT ")) {
			d.Streams = append(d.Streams, string(body))
		}
		s = rest[end:]
	}
	return d
}

// Text は全ページの文字列（Tj ごとに改行で区切る）。
func (d *Doc) Text() string {
	var b strings.Builder
	for _, s := range d.Streams {
		for _, m := range reTj.FindAllStringSubmatch(s, -1) {
			b.WriteString(decode(unescape(m[1])))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// Joined は Tj の区切りを除いた文字列（フォントの切り替えで分かれた文字列もつながる）。
func (d *Doc) Joined() string { return strings.ReplaceAll(d.Text(), "\n", "") }

func unescape(s string) []byte {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			out = append(out, c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		default:
			if s[i] >= '0' && s[i] <= '7' {
				v := 0
				j := i
				for ; j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7'; j++ {
					v = v*8 + int(s[j]-'0')
				}
				out = append(out, byte(v))
				i = j - 1
				continue
			}
			out = append(out, s[i])
		}
	}
	return out
}

// decode は UTF-16BE（fpdf の UTF-8 フォント）の文字列を UTF-8 にする。
func decode(b []byte) string {
	if len(b)%2 != 0 {
		return string(b)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(u))
}
