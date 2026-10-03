// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"regexp"
	"strings"
)

// このファイルは本文の取り出しと整形（plain_text_body / email_parts_to_text / cleanup_body）。

// space は Ruby の \p{Space}（Unicode の White_Space）。
const space = `[\t\n\v\f\r\x{85}\p{Z}]`

// PlainTextBodyToText は MailHandler.plain_text_body_to_text（textile で整形済みテキストとして
// 扱われないよう、行頭の空白を除く。"*" / "#" で始まる行は除かない）。
func PlainTextBodyToText(text string) string {
	// Ruby: text.gsub(/^ +(?![*#])/, '')。RE2 に先読みが無いため手で判定する。
	// " +" はバックトラックするので、"*" / "#" の前の空白の並びは 1 つだけ残る。
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		n := 0
		for n < len(line) && line[n] == ' ' {
			n++
		}
		if n == 0 {
			continue
		}
		if n < len(line) && (line[n] == '*' || line[n] == '#') {
			lines[i] = line[n-1:]
		} else {
			lines[i] = line[n:]
		}
	}
	return strings.Join(lines, "\n")
}

// plainTextBody は plain_text_body（Setting.mail_handler_preferred_body_part の順に text/plain・text/html の
// パートを探し、無ければパートの無いメール全体を、それも無ければ空文字列）。
func (r *receiver) plainTextBody() string {
	if r.plainBody != nil {
		return *r.plainBody
	}
	order := []string{"text/plain", "text/html"}
	if r.h.Settings.String("mail_handler_preferred_body_part") == "html" {
		order = []string{"text/html", "text/plain"}
	}
	all := r.email.AllParts()
	for _, mt := range order {
		var parts []*Part
		for _, p := range all {
			if p.MimeType() == mt {
				parts = append(parts, p)
			}
		}
		if s := r.emailPartsToText(parts); strings.TrimSpace(s) != "" {
			r.plainBody = &s
			return s
		}
	}
	if len(all) == 0 {
		if s := r.emailPartsToText([]*Part{r.email}); strings.TrimSpace(s) != "" {
			r.plainBody = &s
			return s
		}
	}
	s := ""
	r.plainBody = &s
	return s
}

// emailPartsToText は email_parts_to_text（添付を除き、文字コードを UTF-8 に変換して "\r\n" で連結する）。
func (r *receiver) emailPartsToText(parts []*Part) string {
	var out []string
	for _, p := range parts {
		if p.IsAttachment() {
			continue
		}
		body := toUTF8(p.DecodedBody(), p.Charset(), "?")
		if p.MimeType() == "text/html" {
			out = append(out, HTMLToText(body, r.h.Settings.String("text_formatting")))
		} else {
			out = append(out, PlainTextBodyToText(body))
		}
	}
	return strings.Join(out, "\r\n")
}

// cleanedUpTextBody は cleaned_up_text_body（区切り以降を除いた本文。キーワードの抽出で書き換わる）。
func (r *receiver) cleanedUpTextBody() string {
	if r.cleanBody == nil {
		s := r.cleanupBody(r.plainTextBody())
		r.cleanBody = &s
	}
	return *r.cleanBody
}

// cleanedUpSubject は cleaned_up_subject（件名の前後の空白を除き 255 文字まで）。
func (r *receiver) cleanedUpSubject() string {
	return truncateRunes(rubyStrip(r.email.Subject()), 255)
}

func truncateRunes(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

var delimiterSplitRe = regexp.MustCompile(`[\r\n]+`)

// cleanupBody は cleanup_body（Setting.mail_handler_body_delimiters の区切り以降を除く）。
func (r *receiver) cleanupBody(body string) string {
	var delimiters []string
	for _, s := range delimiterSplitRe.Split(r.h.Settings.String("mail_handler_body_delimiters"), -1) {
		if strings.TrimSpace(s) != "" {
			delimiters = append(delimiters, s)
		}
	}
	var patterns []string
	if r.h.Settings.Bool("mail_handler_enable_regex_delimiters") {
		ok := true
		for _, d := range delimiters {
			if _, err := regexp.Compile(d); err != nil {
				r.h.logger().Error("MailHandler: invalid regexp delimiter found in mail_handler_body_delimiters setting (" + err.Error() + ")")
				ok = false
				break
			}
		}
		for _, d := range delimiters {
			if ok {
				patterns = append(patterns, d)
			} else {
				// Ruby では変換に失敗すると文字列のまま Regexp.union に渡され、リテラルとして扱われる
				patterns = append(patterns, regexp.QuoteMeta(d))
			}
		}
	} else {
		// 通常の区切りでは、区切り中の空白が任意の空白、または改行と引用符（>）の並びに一致する
		for _, d := range delimiters {
			patterns = append(patterns, plainDelimiterPattern(d))
		}
	}
	if len(patterns) > 0 {
		alts := make([]string, len(patterns))
		for i, p := range patterns {
			// Regexp.union は各正規表現を (?-mix:...) として埋め込む（. は改行に一致しない）
			alts[i] = "(?-s:" + p + ")"
		}
		re, err := regexp.Compile(`(?ms)^(` + space + `|>)*(` + strings.Join(alts, "|") + `)` + space + `*[\r\n].*`)
		if err != nil {
			r.h.logger().Error("MailHandler: invalid delimiter (" + err.Error() + ")")
		} else {
			// TODO(mail): Setting.text_formatting == "common_mark" かつ common_mark_enable_hardbreaks が false のときの
			// AppendSpacesToLines（buropher は hardbreaks を常に有効として扱う）
			body = re.ReplaceAllString(body, "")
		}
	}
	return rubyStrip(body)
}

// plainDelimiterPattern は Regexp.escape(delimiter) の空白の並びを
// \p{Space}*(\p{Space}|[\r\n](\p{Space}|>)*) に置き換えたもの。
func plainDelimiterPattern(d string) string {
	var sb strings.Builder
	repl := space + `*(?:` + space + `|[\r\n](?:` + space + `|>)*)`
	inSpace := false
	for _, r := range d {
		if r == ' ' {
			if !inSpace {
				sb.WriteString(repl)
				inSpace = true
			}
			continue
		}
		inSpace = false
		sb.WriteString(regexp.QuoteMeta(string(r)))
	}
	return sb.String()
}
