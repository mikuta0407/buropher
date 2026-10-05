// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/customfield"
)

// ユーザーの複数値カスタムフィールドのキーワードは、一致しない要素が多くても線形時間で解析する
// （以前は長さの 3 乗 × メンバー数で、数 KB の値でメール受信が数分以上止まった）。
func TestUserKeywordMultipleIsLinear(t *testing.T) {
	var ps []keywordPrincipal
	for i := range 50 {
		s := strings.Repeat("u", i+1)
		ps = append(ps, keywordPrincipal{ID: int64(i + 1), IsUser: true, Login: s, Mail: s + "@example.net",
			Firstname: "F" + s, Lastname: "L" + s, Name: "F" + s + " L" + s})
	}
	cf := &customfield.CustomField{Multiple: true}
	kw := strings.Repeat("x,", 20000) + "uu,Fu Lu"
	find := func(k string) (string, bool) {
		if p := detectByKeyword(ps, k); p != nil {
			return strconv.FormatInt(p.ID, 10), true
		}
		return "", false
	}
	start := time.Now()
	got := customfield.ParseKeyword(cf, kw, keywordMaxCommas(ps), find).([]string)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("ParseKeyword took %v", d)
	}
	if !slices.Equal(got, []string{"2", "1"}) {
		t.Errorf("values = %q", got)
	}
}
