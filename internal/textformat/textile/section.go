// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

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
	// 正規表現（regexp2）はルーン単位で照合し、不正な UTF-8 は U+FFFD（3 バイト）として返すため、
	// バイト位置の計算がずれる（範囲外アクセス）。Ruby も不正なバイト列には正規表現を適用できない。
	src = strings.ToValidUTF8(src, "�")
	rc := newFormatter(nil)
	rc.preList = nil
	text := rc.ripOfftags(src, false, false)
	var before, s, after strings.Builder
	i := 0
	l := 1
	started := false
	ended := false
loop:
	for _, m := range scanSections(text) {
		all := m.all
		if !m.isHeading {
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
		content, heading, level := m.content, m.heading, atoiRuby(m.level)
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

// sectionMatch は text.scan(reSections) の 1 要素 ([all, content, lf, heading, level, ...])。
type sectionMatch struct {
	all, content, heading, level string
	isHeading                    bool
}

var reSectionHeading = rxm(`\Ah([0-9]+)(` + reA + reC + `)\.(?::(` + reNS + `+))?[ \t](.*?)$`)

// scanSections は text.scan(reSections) と同じ結果を返す。
// 正規表現版は空白行が多い入力で O(n^2) になるため、区切り
// (\A|\r?\n\s*\r?\n) の候補を線形に走査する。見出しは "h" で始まるので、
// \s* は空白の連続の末尾までしか伸ばせない点を利用している。
// 正規表現版 (reSections) との一致はテストで検証している。
func scanSections(text string) []sectionMatch {
	n := len(text)
	// runEnd[i]: i 以上で最初の非空白 (Ruby の \s) の位置
	runEnd := make([]int, n+1)
	runEnd[n] = n
	for i := n - 1; i >= 0; i-- {
		if isRubySpace(text[i]) {
			runEnd[i] = runEnd[i+1]
		} else {
			runEnd[i] = i
		}
	}
	headingCache := map[int]*md{}
	headingAt := func(y int) *md {
		if y+1 >= n || text[y] != 'h' || text[y+1] < '0' || text[y+1] > '9' {
			return nil
		}
		if m, ok := headingCache[y]; ok {
			return m
		}
		m := match(reSectionHeading, text[y:])
		headingCache[y] = m
		return m
	}
	var res []sectionMatch
	pos := 0
	for {
		found := false
		for x := pos; x <= n; x++ {
			// \A の選択肢
			if x == 0 {
				if hm := headingAt(0); hm != nil {
					h := hm.all()
					res = append(res, sectionMatch{all: h, content: "", heading: h, level: hm.s(1), isHeading: true})
					pos = len(h)
					found = true
					break
				}
			}
			// \r?\n\s*\r?\n の選択肢
			firstNL := -1
			if x < n && text[x] == '\n' {
				firstNL = x
			} else if x+1 < n && text[x] == '\r' && text[x+1] == '\n' {
				firstNL = x + 1
			}
			if firstNL < 0 {
				continue
			}
			y := runEnd[firstNL]
			if y-1 <= firstNL || text[y-1] != '\n' {
				continue
			}
			if hm := headingAt(y); hm != nil {
				h := hm.all()
				content := text[pos:y]
				res = append(res, sectionMatch{all: content + h, content: content, heading: h, level: hm.s(1), isHeading: true})
				pos = y + len(h)
				found = true
				break
			}
		}
		if !found {
			// |.* の選択肢 (残り全部)
			res = append(res, sectionMatch{all: text[pos:]})
			return res
		}
	}
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
