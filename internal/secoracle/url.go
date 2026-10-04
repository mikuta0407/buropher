// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package secoracle

import (
	"fmt"
	"net/url"
	"strings"
)

// BrowserResolve は href をブラウザ（WHATWG URL、http/https の特別スキーム）と同じ前処理をして base に対して解決する。
//
//   - 前後の C0 制御文字・空白を除く
//   - 途中のタブ・改行（\t \n \r）を除く
//   - "\" を "/" とみなす（特別スキームの規則）
//
// 解決できない値はエラーにする（ブラウザは遷移しない）。
func BrowserResolve(base, href string) (*url.URL, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	v := strings.TrimFunc(href, func(r rune) bool { return r <= 0x20 })
	v = strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r':
			return -1
		case '\\':
			return '/'
		}
		return r
	}, v)
	// ブラウザは不正なパーセントエスケープ（"%" の後が 16 進 2 桁でない）をそのまま残すが、net/url は解析を拒否する。
	// 同じ結果になるよう "%" を "%25" にしてから解析する
	ref, err := url.Parse(fixPercent(v))
	if err != nil {
		return nil, err
	}
	return b.ResolveReference(ref), nil
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// fixPercent は後ろに 16 進 2 桁が続かない "%" を "%25" にする。
func fixPercent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && (i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2])) {
			b.WriteString("%25")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// SameOriginPath は href を base に対してブラウザと同じく解決した結果が base と同じオリジン
// （スキーム・ホスト・ポート）に留まることを確かめる。
func SameOriginPath(base, href string) error {
	u, err := BrowserResolve(base, href)
	if err != nil {
		// net/url が解析できない値でもブラウザは寛容に解析する。曖昧なのでスキーム相対だけは別に判定する
		v := strings.TrimLeft(strings.NewReplacer("\t", "", "\n", "", "\r", "", `\`, "/").Replace(href), "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
		if strings.HasPrefix(v, "//") || URLScheme(href) != "" {
			return fmt.Errorf("%q leaves the origin (unparsable by net/url: %v)", href, err)
		}
		return nil
	}
	b, _ := url.Parse(base)
	if !strings.EqualFold(u.Scheme, b.Scheme) || !strings.EqualFold(u.Host, b.Host) || u.User != nil {
		return fmt.Errorf("%q resolves to %q (origin %s://%s), not %s://%s", href, u.String(), u.Scheme, u.Host, b.Scheme, b.Host)
	}
	return nil
}
