// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db_test

import (
	"io/fs"
	"slices"
)

func readDirNames(fsys fs.FS) ([]string, error) {
	es, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out, nil
}
