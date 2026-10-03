// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package i18n

import (
	"fmt"
	"io/fs"
)

// WalkUpstreamStrings は Redmine 本体の訳文（fsys の redmine/*.yml）の文字列値を、ブランド置換を
// 適用する前の原文のまま fn(key, value) に渡す（key はロケールを除いたドット区切り。配列要素は同じ key）。
// 互換テストハーネスが参照 Redmine 側の文言を製品名だけ正規化するのに使う。
func WalkUpstreamStrings(fsys fs.FS, fn func(key, value string)) error {
	files, err := fs.Glob(fsys, "redmine/*.yml")
	if err != nil {
		return err
	}
	var walk func(m map[string]any, prefix string)
	walk = func(m map[string]any, prefix string) {
		for k, v := range m {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			switch x := v.(type) {
			case string:
				fn(key, x)
			case map[string]any:
				walk(x, key)
			case []any:
				for _, e := range x {
					if s, ok := e.(string); ok {
						fn(key, s)
					}
				}
			}
		}
	}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return err
		}
		tree, err := parseYAML(data)
		if err != nil {
			return fmt.Errorf("i18n: %s: %w", f, err)
		}
		for _, v := range tree {
			if m, ok := v.(map[string]any); ok {
				walk(m, "")
			}
		}
	}
	return nil
}
