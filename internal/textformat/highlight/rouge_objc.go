// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

// Rouge 4.7 の objective_c.rb / objective_c/common.rb の移植。

var (
	objcAtKeywords = wordset(`selector private protected public encode synchronized try
throw catch finally end property synthesize dynamic optional
interface implementation import autoreleasepool`)
	objcAtBuiltins = wordset(`true false YES NO`)
)

func init() {
	registerRouge("objective_c", func() *rlexer {
		l := buildC("objective_c", wordset(cKeywords), wordset(cKeywordsType), wordset(cReserved), wordset(`YES NO nil`))
		const id = `(?i:[a-z$_][a-z0-9$_]*)`
		l.prependRules("statements",
			rule(`@"`, "s", "string"),
			rule(`@'(\\[0-7]{1,3}|\\x[a-fA-F0-9]{1,2}|\\.|[^\\'\n]')`, "sc"),
			rule(`(?i)@(\d+[.]\d*|[.]\d+|\d+)e[+-]?\d+l?`, "mf"),
			rule(`(?i)@(\d+[.]\d*|[.]\d+|\d+f)f?`, "mf"),
			rule(`@0x\h+[lL]?`, "mh"),
			rule(`(?i)@0[0-7]+l?`, "mo"),
			rule(`@\d+l?`, "mi"),
			rule(`\bin\b`, "k"),
			rule(`@(?:interface|implementation)\b`, "k", "objc_classname"),
			rule(`@(?:class|protocol)\b`, "k", "forward_classname"),
			ruleF(`@([[:alnum:]]+)`, func(c *rctx) {
				switch w := c.group(1); {
				case objcAtKeywords[w]:
					c.token("k")
				case objcAtBuiltins[w]:
					c.token("nb")
				default:
					c.token("err")
				}
			}),
			rule(`[?]`, "p", "ternary"),
			rule(`\[`, "p", "message"),
			rule(`@\[`, "p", "array_literal"),
			rule(`@\{`, "p", "dictionary_literal"),
		)
		l.state("ternary",
			rule(`:`, "p", "#pop"),
			mixin("statements"),
		)
		l.state("message_shared",
			rule(`\]`, "p", "#pop"),
			rule(`\{`, "p", "#pop"),
			rule(`;`, "err"),
			mixin("statements"),
		)
		l.state("message",
			ruleF(`(`+id+`)(\s*)(:)`, func(c *rctx) { c.groups("nf", "", "p"); c.gotoState("message_with_args") }),
			ruleF(`(`+id+`)(\s*)(\])`, func(c *rctx) { c.groups("nf", "", "p"); c.pop() }),
			mixin("message_shared"),
		)
		l.state("message_with_args",
			rule(`\{`, "p", "function"),
			ruleF(`(`+id+`)(\s*)(:)`, func(c *rctx) { c.groups("nf", "", "p"); c.pop() }),
			mixin("message_shared"),
		)
		l.state("array_literal",
			rule(`]`, "p", "#pop"),
			rule(`,`, "p"),
			mixin("statements"),
		)
		l.state("dictionary_literal",
			rule(`}`, "p", "#pop"),
			rule(`,`, "p"),
			mixin("statements"),
		)
		l.state("objc_classname",
			mixin("whitespace"),
			ruleF(`(`+id+`)(\s*)(:)(\s*)(`+id+`)`, func(c *rctx) { c.groups("nc", "", "p", "", "nc"); c.pop() }),
			ruleF(`(`+id+`)(\s*)([(])(\s*)(`+id+`)(\s*)([)])`, func(c *rctx) {
				c.groups("nc", "", "p", "", "nl", "", "p")
				c.pop()
			}),
			rule(id, "nc", "#pop"),
		)
		l.state("forward_classname",
			mixin("whitespace"),
			ruleF(`(`+id+`)(\s*)(,)(\s*)`, func(c *rctx) { c.groups("nc", "", "p", ""); c.push() }),
			ruleF(`(`+id+`)(\s*)(;?)`, func(c *rctx) { c.groups("nc", "", "p"); c.pop() }),
		)
		l.prependRules("root",
			ruleF(`(?ix)
            ([-+])(\s*)
            ([(].*?[)])?(\s*)
            (?=`+id+`:?)
          `, func(c *rctx) {
				c.token("k", c.group(1))
				c.token("", c.group(2))
				if g := c.m.GroupByNumber(3); g != nil && len(g.Captures) > 0 {
					c.recurse(g.String())
				}
				c.token("", c.group(4))
				c.push("method_definition")
			}),
		)
		l.state("method_definition",
			rule(`,`, "p"),
			rule(`[.][.][.]`, "p"),
			ruleF(`([(].*?[)])(`+id+`)`, func(c *rctx) {
				m1, m2 := c.group(1), c.group(2)
				c.recurse(m1)
				c.token("nv", m2)
			}),
			ruleG(`(?m)(`+id+`)(\s*)(:)`, toks("nf", "", "p")),
			rule(`;`, "p", "#pop"),
			ruleF(`{`, func(c *rctx) { c.token("p"); c.gotoState("function") }),
			mixin("inline_whitespace"),
			rule(`//.*?\n`, "c1"),
			rule(`(?m)\s+`, ""),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		return l
	})
}
