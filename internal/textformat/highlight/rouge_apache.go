// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import "strings"

// Rouge 4.7 の apache.rb の移植。

func apacheTok(tok, tktype string) string {
	if apacheSections[tok] || apacheDirectives[tok] || apacheValues[tok] {
		return tktype
	}
	return ""
}

func init() {
	registerRouge("apache", func() *rlexer {
		l := &rlexer{tag: "apache"}
		l.state("whitespace",
			rule(`\#.*`, "c"),
			rule(`(?m)\s+`, ""),
		)
		l.state("root",
			mixin("whitespace"),
			ruleF(`(<\/?)(\w+)`, func(c *rctx) {
				c.groups("p", apacheTok(strings.ToLower(c.group(2)), "nl"))
				c.push("section")
			}),
			ruleF(`\w+`, func(c *rctx) {
				c.token(apacheTok(strings.ToLower(c.m.String()), "nc"))
				c.push("directive")
			}),
		)
		l.state("section",
			ruleF(`([^>]+)?(>(?:\r\n?|\n)?)`, func(c *rctx) { c.groups("sr", "p"); c.pop() }),
			mixin("whitespace"),
		)
		l.state("directive",
			rule(`\r\n?|\n`, "", "#pop"),
			mixin("whitespace"),
			ruleF(`\S+`, func(c *rctx) { c.token(apacheTok(strings.ToLower(c.m.String()), "ss")) }),
		)
		return l
	})
}
