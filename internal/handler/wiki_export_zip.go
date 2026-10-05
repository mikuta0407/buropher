// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"archive/zip"
	"bytes"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// wikiExportZIP は wiki#export の format.zip（#43978: 全ページの本文を 1 ページ 1 ファイルの ZIP で送る）。
func (a *App) wikiExportZIP(c *Req, pages []*domain.WikiPage) {
	// User#convert_time_to_user_timezone: タイムゾーン未設定ならサーバーのローカル時刻
	loc := time.Local
	if c.Loc != nil && c.Loc.Location != nil {
		loc = c.Loc.Location
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var archived []string
	for _, p := range pages {
		text := ""
		if p.CurrentVersion > 0 {
			v, err := repository.WikiPageVersion(c.Ctx(), a.DB, p.ID, p.CurrentVersion)
			if err != nil {
				a.wikiError(c, err)
				return
			}
			text = v.Text
		}
		fh := &zip.FileHeader{Name: archivedWikiPageFilename(p.Title, &archived), Method: zip.Deflate}
		if !p.UpdatedOn.IsZero() {
			// DOS 日時はユーザーの表示上のローカル時刻、拡張タイムスタンプ（UT）は UTC の絶対時刻
			// （archive/zip は Modified の地域時刻で DOS 日時を、Unix 時刻で UT フィールドを書く）
			fh.Modified = p.UpdatedOn.In(loc)
		}
		w, err := zw.CreateHeader(fh)
		if err == nil {
			_, err = w.Write([]byte(text))
		}
		if err != nil {
			a.wikiError(c, err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		a.wikiError(c, err)
		return
	}
	sendData(c, buf.Bytes(), "application/zip", c.Project.Identifier+"-wiki.zip")
}

// wikiZIPUnsafeRe は Attachment#sanitize_filename と揃えた、ZIP のエントリ名に使えない文字。
var wikiZIPUnsafeRe = regexp.MustCompile("[/?%*:|\"'<>\n\r\x00]+")

// archivedWikiPageFilename は WikiController#archived_wiki_page_filename。
// 添付ファイルと違いパス風の要素は落とさず、重複したら "(n)" を付ける。
func archivedWikiPageFilename(title string, archived *[]string) string {
	const ext = ".txt"
	sanitized := wikiZIPUnsafeRe.ReplaceAllString(strings.ReplaceAll(title, `\`, "_"), "_")
	name := sanitized + ext
	for n := 1; slices.Contains(*archived, name); n++ {
		name = sanitized + "(" + strconv.Itoa(n) + ")" + ext
	}
	*archived = append(*archived, name)
	return name
}
