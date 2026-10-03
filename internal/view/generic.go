// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package view

import (
	"text/template"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// genericFuncs はリクエストに依存しない汎用のテンプレート関数（Ruby の式を移植するための最小限）。
func genericFuncs() template.FuncMap {
	return template.FuncMap{
		"add":  func(a, b int) int { return a + b },
		"sub":  func(a, b int) int { return a - b },
		"mul":  func(a, b int) int { return a * b },
		"even": func(i int) bool { return i%2 == 0 },
		"odd":  func(i int) bool { return i%2 != 0 },
		// to_s は Ruby の to_s（nil → ""、Float は "1.0" 形式）
		"to_s": rails.ToS,
	}
}
