// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package web は Redmine 由来のアセット・ロケール・テンプレートを埋め込む。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var assetsFS embed.FS

//go:embed all:locales
var localesFS embed.FS

//go:embed all:templates
var templatesFS embed.FS

//go:embed public
var publicFS embed.FS

func sub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}

// Assets は web/assets を返す（images, javascripts, stylesheets, fonts, themes, javascript, vendor）。
func Assets() fs.FS { return sub(assetsFS, "assets") }

// Locales は web/locales を返す（redmine/*.yml と overlay/*.yml）。
func Locales() fs.FS { return sub(localesFS, "locales") }

// Templates は web/templates を返す。
func Templates() fs.FS { return sub(templatesFS, "templates") }

// Public は web/public を返す（404.html, 500.html）。
func Public() fs.FS { return sub(publicFS, "public") }
