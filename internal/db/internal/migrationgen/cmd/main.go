// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// migrationgen コマンドは internal/db の go:generate から呼ばれ、
// migrations/src のテンプレートから dialect 別マイグレーションを生成する。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mikuta0407/buropher/internal/db/internal/migrationgen"
)

func main() {
	src := flag.String("src", "migrations/src", "テンプレートのディレクトリ")
	out := flag.String("out", "migrations", "出力先 (配下に sqlite/, postgres/ を作る)")
	flag.Parse()
	if err := migrationgen.Generate(*src, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
