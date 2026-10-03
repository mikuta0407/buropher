// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import "strings"

// Rouge 4.7 の lua.rb / r.rb の移植。

var (
	rKeywords         = wordset(`if else for while repeat in next break function`)
	rKeywordConstants = wordset(`NULL Inf TRUE FALSE NaN NA NA_integer_ NA_real_ NA_complex_ NA_character_`)
	rBuiltinConstants = wordset(`LETTERS letters month.abb month.name pi T F`)
)

func init() {
	registerRouge("lua", func() *rlexer {
		l := &rlexer{tag: "lua"}
		l.state("root",
			rule(`#!(.*?)$`, "cp"),
			rule(``, "", "base"),
		)
		l.state("base",
			rule(`(?m)--\[(=*)\[.*?\]\1\]`, "cm"),
			rule(`--.*$`, "c1"),
			rule(`(?i)(\d*\.\d+|\d+\.\d*)(e[+-]?\d+)?'`, "mf"),
			rule(`(?i)\d+e[+-]?\d+`, "mf"),
			rule(`(?i)0x[0-9a-f]*`, "mh"),
			rule(`\d+`, "mi"),
			rule(`\n`, ""),
			rule(`[^\S\n]`, ""),
			rule(`(?m)\[(=*)\[.*?\]\1\]`, "s"),
			rule(`(==|~=|<=|>=|\.\.\.|\.\.|[=+\-*/%^<>#])`, "o"),
			rule(`[\[\]\{\}\(\)\.,:;]`, "p"),
			rule(`(and|or|not)\b`, "ow"),
			rule(`(break|do|else|elseif|end|for|if|in|repeat|return|then|until|while)\b`, "k"),
			rule(`(local)\b`, "kd"),
			rule(`(true|false|nil)\b`, "kc"),
			rule(`(function)\b`, "k", "function_name"),
			ruleF(`[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?`, func(c *rctx) {
				name := c.m.String()
				switch {
				case name == "gsub":
					c.token("nb")
					c.push("gsub")
				case luaBuiltins[name]:
					c.token("nb")
				case strings.Contains(name, "."):
					a, b, _ := strings.Cut(name, ".")
					c.token("n", a)
					c.token("p", ".")
					c.token("n", b)
				default:
					c.token("n")
				}
			}),
			rule(`'`, "s1", "escape_sqs"),
			rule(`"`, "s2", "escape_dqs"),
		)
		l.state("function_name",
			rule(`\s+`, ""),
			ruleF(`(?:([A-Za-z_][A-Za-z0-9_]*)(\.))?([A-Za-z_][A-Za-z0-9_]*)`, func(c *rctx) {
				c.groups("nc", "p", "nf")
				c.pop()
			}),
			rule(`\(`, "p", "#pop"),
		)
		l.state("gsub",
			rule(`\)`, "p", "#pop"),
			rule(`[(,]`, "p"),
			rule(`\s+`, ""),
			rule(`'`, "sr", "regex_sq"),
			rule(`"`, "sr", "regex_dq"),
		)
		l.state("regex_sq",
			ruleF(`'`, func(c *rctx) { c.token("sr"); c.gotoState("regex_end") }),
			mixin("regex"),
		)
		l.state("regex_dq",
			ruleF(`"`, func(c *rctx) { c.token("sr"); c.gotoState("regex_end") }),
			mixin("regex"),
		)
		l.state("regex",
			ruleF(`"`, func(c *rctx) { c.token("sr"); c.gotoState("regex_end") }),
			rule(`\[\^?`, "se", "regex_group"),
			rule(`\\.`, "se"),
			rule(`[(][?][:=<!]`, "se"),
			rule(`[{][\d,]+[}]`, "se"),
			rule(`[()?]`, "se"),
			rule(`.`, "sr"),
		)
		l.state("regex_end",
			rule(`[$]+`, "sr", "#pop"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("regex_group",
			rule(`/`, "se"),
			rule(`\]`, "se", "#pop"),
			ruleG(`(\\)(.)`, toks("se", "sr")),
			rule(`.`, "sr"),
		)
		l.state("escape_sqs", mixin("string_escape"), mixin("sqs"))
		l.state("escape_dqs", mixin("string_escape"), mixin("dqs"))
		l.state("string_escape", rule(`\\([abfnrtv\\"']|\d{1,3})`, "se"))
		l.state("sqs",
			rule(`\\'`, "se"),
			rule(`'`, "s1", "#pop"),
			rule(`[^'\\]+`, "s1"),
		)
		l.state("dqs",
			rule(`\\"`, "se"),
			rule(`"`, "s2", "#pop"),
			rule(`[^"\\]+`, "s2"),
		)
		return l
	})

	registerRouge("r", func() *rlexer {
		primitive := strings.Join(strings.Fields(`abs acos acosh all any anyNA Arg as.call as.character
as.complex as.double as.environment as.integer as.logical
as.null.default as.numeric as.raw asin asinh atan atanh attr
attributes baseenv browser c call ceiling class Conj cos cosh
cospi cummax cummin cumprod cumsum digamma dim dimnames
emptyenv exp expression floor forceAndCall gamma gc.time
globalenv Im interactive invisible is.array is.atomic is.call
is.character is.complex is.double is.environment is.expression
is.finite is.function is.infinite is.integer is.language
is.list is.logical is.matrix is.na is.name is.nan is.null
is.numeric is.object is.pairlist is.raw is.recursive is.single
is.symbol lazyLoadDBfetch length lgamma list log max min
missing Mod names nargs nzchar oldClass on.exit pos.to.env
proc.time prod quote range Re rep retracemem return round
seq_along seq_len seq.int sign signif sin sinh sinpi sqrt
standardGeneric substitute sum switch tan tanh tanpi tracemem
trigamma trunc unclass untracemem UseMethod xtfrm`), "|")
		l := &rlexer{tag: "r"}
		l.state("root",
			rule(`#'.*?$`, "cd"),
			rule(`#.*?$`, "c1"),
			rule(`(?m)\s+`, "w"),
			rule("`[^`]+?`", "n"),
			rule(`(?m)'(\\.|.)*?'`, "s1"),
			rule(`(?m)"(\\.|.)*?"`, "s2"),
			rule(`%[^%]*?%`, "o"),
			rule(`0[xX][a-fA-F0-9]+([pP][0-9]+)?[Li]?`, "mh"),
			rule(`[+-]?(\d+([.]\d+)?|[.]\d+)([eE][+-]?\d+)?[Li]?`, "m"),
			rule(`\b(?<!.)(`+primitive+`)(?=\()`, "nf"),
			ruleF(`(?:(?:[[:alpha:]]|[.][._[:alpha:]])[._[:alnum:]]*)|[.]`, func(c *rctx) {
				w := c.m.String()
				switch {
				case rKeywords[w]:
					c.token("k")
				case rKeywordConstants[w]:
					c.token("kc")
				case rBuiltinConstants[w]:
					c.token("nb")
				default:
					c.token("n")
				}
			}),
			rule(`[\[\]{}();,]`, "p"),
			rule(`[-<>?*+^/!=~$@:%&|]`, "o"),
		)
		return l
	})
}
