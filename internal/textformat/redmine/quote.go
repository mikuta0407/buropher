// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package redmine

// Redmine::QuoteReply::Builder（lib/redmine/quote_reply.rb）。
// 見出しは Setting.default_language で作る（ll(Setting.default_language, ...)）。

import (
	"regexp"

	"github.com/mikuta0407/buropher/internal/i18n"
)

// QuoteBuilder は引用返信の本文を組み立てる。
type QuoteBuilder struct {
	Bundle *i18n.Bundle
	// DefaultLanguage は Setting.default_language。
	DefaultLanguage string
}

func (q QuoteBuilder) ll(key string, arg any) string {
	b := q.Bundle
	if b == nil {
		b = i18n.Default()
	}
	return b.LL(q.DefaultLanguage, key, arg)
}

// QuoteIssue は quote_issue(issue, partial_quote:)。author はチケット作成者の表示名（User#to_s）。
func (q QuoteBuilder) QuoteIssue(author, description, partialQuote string) string {
	return buildQuote(q.ll("text_user_wrote", author)+"\n> ", description, partialQuote)
}

// QuoteIssueJournal は quote_issue_journal(journal, indice:, partial_quote:)。
func (q QuoteBuilder) QuoteIssueJournal(user, notes, indice, partialQuote string) string {
	return buildQuote(q.ll("text_user_wrote_in", i18n.Vars{"value": user, "link": "#note-" + indice})+"\n> ", notes, partialQuote)
}

// QuoteRootMessage は quote_root_message(message, partial_quote:)。
func (q QuoteBuilder) QuoteRootMessage(author, content, partialQuote string) string {
	return buildQuote(q.ll("text_user_wrote", author)+"\n> ", content, partialQuote)
}

// QuoteMessage は quote_message(message, partial_quote:)。messageID は message.id。
func (q QuoteBuilder) QuoteMessage(author, content, messageID, partialQuote string) string {
	return buildQuote(q.ll("text_user_wrote_in", i18n.Vars{"value": author, "link": "message#" + messageID})+"\n> ", content, partialQuote)
}

var (
	reQuotePre     = regexp.MustCompile(`(?s)<pre>(.*?)</pre>`)
	reQuoteNewline = regexp.MustCompile(`(\r?\n|\r\n?)`)
)

// buildQuote は build_quote。
func buildQuote(header, text, partialQuote string) string {
	quote := partialQuote
	if blank(quote) {
		quote = reQuotePre.ReplaceAllString(rubyStrip(text), "[...]")
	}
	return header + reQuoteNewline.ReplaceAllString(quote, "\n> ") + "\n\n"
}
