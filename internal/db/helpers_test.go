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
