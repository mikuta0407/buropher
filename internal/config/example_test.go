// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestExampleConfig は config.example.toml の各キーのコメントを外して読めること、
// Config の全キーが例に記載されていることを確認する。
func TestExampleConfig(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// "#key = value" の行だけコメントを外す
	re := regexp.MustCompile(`(?m)^#([a-z_]+ = .*)$`)
	src := re.ReplaceAllString(string(b), "$1")
	var c Config
	md, err := toml.Decode(src, &c)
	if err != nil {
		t.Fatalf("decode uncommented example: %v", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		t.Errorf("unknown keys in example: %v", u)
	}
	defined := map[string]bool{}
	for _, k := range md.Keys() {
		defined[k.String()] = true
	}
	for _, k := range tomlKeys(reflect.TypeFor[Config](), "") {
		if !defined[k] {
			t.Errorf("config key %q is not documented in config.example.toml", k)
		}
	}
}

// tomlKeys は構造体の toml タグからドット区切りのキー一覧を作る（map は親キーのみ）。
func tomlKeys(t reflect.Type, prefix string) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name == "" || name == "-" {
			continue
		}
		key := prefix + name
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			out = append(out, tomlKeys(ft, key+".")...)
			continue
		}
		out = append(out, key)
	}
	return out
}
