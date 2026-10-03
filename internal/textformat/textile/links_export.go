// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

// LinksHelper の公開版（NullFormatter など Textile 以外からも使う）。

// AutoLink は Redmine::WikiFormatting::LinksHelper#auto_link!。
func AutoLink(text string) string { return autoLink(text) }

// AutoMailto は LinksHelper#auto_mailto!。
func AutoMailto(text string) string { return autoMailto(text) }

// RestoreRedmineLinks は LinksHelper#restore_redmine_links。
func RestoreRedmineLinks(html string) string { return restoreRedmineLinks(html) }
