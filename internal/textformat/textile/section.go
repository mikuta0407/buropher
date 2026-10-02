// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

// セクション編集 (Redmine::WikiFormatting::SectionHelper と
// Textile::Formatter#extract_sections の移植)。

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrStaleSection はセクション更新時にハッシュが一致しない場合のエラー
// (Redmine::WikiFormatting::StaleSectionError 相当)。
var ErrStaleSection = errors.New("textile: stale section")

// SectionHash は ActiveSupport::Digest.hexdigest 相当 (Redmine の設定では MD5)。
func SectionHash(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// GetSection は index 番目 (1 始まり) の見出しセクションとそのハッシュを返す
// (SectionHelper#get_section, section_helper.rb:23-27)。
func GetSection(src string, index int) (section, hash string) {
	section = extractSections(src, index)[1]
	return section, SectionHash(section)
}

// UpdateSection は index 番目のセクションを update で置き換えた全文を返す
// (SectionHelper#update_section, section_helper.rb:29-37)。
// hash が空でなく現在のセクションのハッシュと異なる場合は ErrStaleSection を返す。
func UpdateSection(src string, index int, update, hash string) (string, error) {
	t := extractSections(src, index)
	if !blank(hash) && hash != SectionHash(t[1]) {
		return "", ErrStaleSection
	}
	if !blank(t[1]) {
		t[1] = update
	}
	var parts []string
	for _, s := range t {
		if !blank(s) {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

var reSections = rxm(`(((?:.*?)(\A|\r?\n` + reS + `*\r?\n))(h([0-9]+)(` + reA + reC + `)\.(?::(` + reNS + `+))?[ \t](.*?)$)|.*)`)

// extractSections は Formatter#extract_sections (formatter.rb:47-93) の移植。
// [前, 対象セクション, 後] を返す。<pre> 等の中の見出しは無視する。
func extractSections(src string, index int) [3]string {
	rc := newFormatter(nil)
	rc.preList = nil
	text := rc.ripOfftags(src, false, false)
	var before, s, after strings.Builder
	i := 0
	l := 1
	started := false
	ended := false
loop:
	for _, m := range scan(reSections, text) {
		all := m.s(1)
		if !m.ok(4) {
			switch {
			case ended:
				after.WriteString(all)
			case started:
				s.WriteString(all)
			default:
				before.WriteString(all)
			}
			break loop
		}
		content, heading, level := m.s(2), m.s(4), atoiRuby(m.s(5))
		i++
		switch {
		case ended:
			after.WriteString(all)
		case i == index:
			l = level
			before.WriteString(content)
			s.WriteString(heading)
			started = true
		case i > index:
			s.WriteString(content)
			if level > l {
				s.WriteString(heading)
			} else {
				after.WriteString(heading)
				ended = true
			}
		default:
			before.WriteString(all)
		}
	}
	sections := [3]string{rubyStrip(before.String()), rubyStrip(s.String()), rubyStrip(after.String())}
	for k := range sections {
		sections[k] = rc.smoothOfftagsPlain(sections[k])
	}
	return sections
}

// atoiRuby は String#to_i 相当 (先頭の数字列のみ解釈、桁あふれは考慮しない)。
func atoiRuby(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
