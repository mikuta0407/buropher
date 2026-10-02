// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

// Redmine::WikiFormatting::LinksHelper (lib/redmine/wiki_formatting/links_helper.rb) の移植。
// Formatter では inline_auto_link / inline_auto_mailto / inline_restore_redmine_links
// として textile の規則の後に適用される。

import (
	"strings"
	"sync"

	"github.com/dlclark/regexp2"
)

// AUTO_LINK_RE (links_helper.rb:25-42)
var reAutoLink = rx(`(` + // leading text
	`<` + reW + `+[^>]*?>|` + // leading HTML tag, or
	`[` + spIn + `\(\[,;]|` + // leading punctuation, or
	`^` + // beginning of line
	`)` +
	`(` +
	`(?:https?://)|` + // protocol spec, or
	`(?:s?ftps?://)|` +
	`(?:www\.)` + // www.*
	`)` +
	`(` +
	`([^<]` + reNS + `*?)` + // url
	`(/)?` + // slash
	`)` +
	`((?:&gt;)?|[^` + alnumIn + `_=/;\(\)\-]*?)` + // post
	`(?=<|` + reS + `|$)`)

var (
	reLeadingA     = rxi(`<a` + reS)
	reLeadingImage = rx(`![<>=]?`)
)

// autoLink は auto_link! (links_helper.rb:45-66): URL をリンクに置き換える
func autoLink(text string) string {
	// 高速化: 正規表現が必要とするリテラルを含まなければマッチしない
	if !strings.Contains(text, "://") && !strings.Contains(text, "www.") {
		return text
	}
	return gsub(reAutoLink, text, func(m md) string {
		all, leading, proto, url, post := m.all(), m.s(1), m.s(2), m.s(3), m.s(6)
		if matches(reLeadingA, leading) || matches(reLeadingImage, leading) {
			// リンク済みの URL と ! !> !< != (textile の画像) が前置された URL は置換しない
			return all
		}
		// 括弧の対応が取れておらず ')' で終わる URL は外側の括弧とみなす
		if strings.HasSuffix(url, ")") && strings.Count(url, "(")-strings.Count(url, ")") < 0 {
			url = url[:len(url)-1]
			post = ")" + post
		}
		content := proto + url
		href := proto
		if proto == "www." {
			href = "http://www."
		}
		href += url
		return leading + `<a class="external" href="` + htmlEscapeERB(href) + `">` + htmlEscapeERB(content) + `</a>` + post
	})
}

var reMail = rx(`([` + wIn + `\.!#\$%\-+./]{1,64}@[A-Za-z0-9\-]{1,63}(\.[A-Za-z0-9\-]{1,63})+)`)

var mailInLinkCache sync.Map // map[string]*regexp2.Regexp

func mailInLinkRe(mail string) *regexp2.Regexp {
	if v, ok := mailInLinkCache.Load(mail); ok {
		return v.(*regexp2.Regexp)
	}
	re := rx(`<a` + reB + `[^>]*>(.*)(` + regexp2.Escape(mail) + `)(.*)</a>`)
	mailInLinkCache.Store(mail, re)
	return re
}

// autoMailto は auto_mailto! (links_helper.rb:69-82): メールアドレスをリンクに置き換える
func autoMailto(text string) string {
	// 高速化: 正規表現が必要とするリテラルを含まなければマッチしない
	if !strings.Contains(text, "@") {
		return text
	}
	orig := text
	// 判定用の正規表現は "<a" を必須とするので、含まなければ判定を省略できる
	hasA := strings.Contains(orig, "<a")
	linked := map[string]bool{}
	return gsub(reMail, text, func(m md) string {
		mail := m.s(1)
		if hasA {
			l, ok := linked[mail]
			if !ok {
				l = matches(mailInLinkRe(mail), orig)
				linked[mail] = l
			}
			if l {
				return mail
			}
		}
		return `<a class="email" href="mailto:` + htmlEscapeERB(mail) + `">` + htmlEscapeERB(mail) + `</a>`
	})
}

var (
	reRestoreWiki    = rx(`\[<a href="(.*?)">(.*?)</a>\]`)
	reRestoreQuoted  = rx(`(` + reW + `):&quot;(.+?)&quot;`)
	reRestoreAtUser  = rx(`[@A]<a(` + reS + `class="email")? href="mailto:(.*?)">(.*?)</a>`)
	reRestoreUser    = rx(reB + `user:<a(` + reS + `class="email")? href="mailto:(.*?)">(.*?)</a>`)
	reRestoreAttach  = rx(reB + `attachment:<a(` + reS + `class="email")? href="mailto:(.*?)">(.*?)</a>`)
	reRestoreHiresIm = rx(`<a(` + reS + `class="email")? href="mailto:[^"]+@[0-9]x\.(bmp|gif|jpg|jpe|jpeg|png)">(.*?)</a>`)
)

// restoreRedmineLinks は restore_redmine_links (links_helper.rb:84-108)
func restoreRedmineLinks(html string) string {
	// 高速化: 正規表現が必要とするリテラルを含まなければマッチしない
	if !strings.Contains(html, "<a") && !strings.Contains(html, ":&quot;") {
		return html
	}
	// wiki リンクを戻す 例: [[Foo]]
	html = gsub(reRestoreWiki, html, func(m md) string { return "[[" + m.s(2) + "]]" })
	// ダブルクォート付きの Redmine リンクを戻す 例: version:"1.0"
	html = gsub(reRestoreQuoted, html, func(m md) string { return m.s(1) + `:"` + m.s(2) + `"` })
	// ログイン名に @ を含むユーザリンクを戻す 例: [@jsmith@somenet.foo]
	// (原典の [@\A] は文字クラス内で \A がリテラル A になる)
	html = gsub(reRestoreAtUser, html, func(m md) string { return "@" + m.s(2) })
	// 例: [user:jsmith@somenet.foo]
	html = gsub(reRestoreUser, html, func(m md) string { return "user:" + m.s(2) })
	// ファイル名に @ を含む添付リンクを戻す 例: [attachment:image@2x.png]
	html = gsub(reRestoreAttach, html, func(m md) string { return "attachment:" + m.s(2) })
	// メールアドレスと誤認された高解像度画像を戻す 例: [printscreen@2x.png]
	html = gsub(reRestoreHiresIm, html, func(m md) string { return m.s(3) })
	return html
}
