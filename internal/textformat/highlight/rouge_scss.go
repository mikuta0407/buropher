// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

// Rouge 5.1 の scss.rb / sass/common.rb の移植。

func init() {
	registerRouge("scss", func() *rlexer {
		const id = `[\w-]+`
		l := &rlexer{tag: "scss"}
		l.state("content_common",
			rule(`@for\b`, "k", "for"),
			rule(`@(debug|warn|if|each|while|else|return|media)`, "k", "value"),
			ruleF(`(@mixin)(\s+)(`+id+`)`, func(c *rctx) { c.groups("k", "", "nf"); c.push("value") }),
			ruleF(`(@function)(\s+)(`+id+`)`, func(c *rctx) { c.groups("k", "", "nf"); c.push("value") }),
			rule(`@extend\b`, "k", "selector"),
			ruleF(`(@include)(\s+)(`+id+`)`, func(c *rctx) { c.groups("k", "", "nd"); c.push("value") }),
			rule(`@`+id, "k", "selector"),
			rule(`&`, "k", "selector"),
			ruleF(`([$]`+id+`)([ \t]*)(:)`, func(c *rctx) { c.groups("nv", "", "p"); c.push("value") }),
		)
		l.state("value",
			mixin("end_section"),
			rule(`[ \t]+`, ""),
			rule(`[$]`+id, "nv"),
			rule(`url[(]`, "sx", "string_url"),
			rule(id+`(?=\s*[(])`, "nf"),
			rule(`%`+id, "nd"),
			rule(`(true|false)\b`, "bp"),
			rule(`(and|or|not)\b`, "ow"),
			rule(`(?i)#[a-z0-9]{1,6}`, "mh"),
			rule(`-?\d+(%|[a-z]+)?`, "m"),
			rule(`-?\d*\.\d+(%|[a-z]+)?`, "mi"),
			mixin("has_strings"),
			mixin("has_interp"),
			rule(`[~^*!&%<>\|+=@:,.\/?-]+`, "o"),
			rule(`[\[\]()]+`, "p"),
			rule(`/[*]`, "cm", "inline_comment"),
			rule(`//[^\n]*`, "c1"),
			ruleF(id, func(c *rctx) {
				if cssBuiltins[c.m.String()] {
					c.token("nb")
				} else if cssColors[c.m.String()] {
					c.token("no")
				} else {
					c.token("n")
				}
			}),
		)
		l.state("has_interp", rule(`[#][{]`, "si", "interpolation"))
		l.state("has_strings",
			rule(`"`, "s2", "dq"),
			rule(`'`, "s1", "sq"),
		)
		l.state("interpolation",
			rule(`}`, "si", "#pop"),
			mixin("value"),
		)
		l.state("selector",
			mixin("end_section"),
			mixin("has_strings"),
			mixin("has_interp"),
			rule(`[ \t]+`, ""),
			rule(`:`, "nd", "pseudo_class"),
			rule(`[.]`, "nc", "class"),
			rule(`#`, "nn", "id"),
			rule(`%`, "nv", "placeholder"),
			rule(id, "nt"),
			rule(`&`, "k"),
			rule(`[~^*!&\[\]()<>\|+=@:;,.\/?-]`, "o"),
		)
		l.state("dq",
			rule(`"`, "s2", "#pop"),
			mixin("has_interp"),
			rule(`(\\.|#(?![{])|[^\n"#])+`, "s2"),
		)
		l.state("sq",
			rule(`'`, "s1", "#pop"),
			mixin("has_interp"),
			rule(`(\\.|#(?![{])|[^\n'#])+`, "s1"),
		)
		l.state("string_url",
			rule(`[)]`, "sx", "#pop"),
			rule(`(\\.|#(?![{])|[^\n)#])+`, "sx"),
			mixin("has_interp"),
		)
		l.state("selector_piece",
			mixin("has_interp"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("pseudo_class", rule(id, "nd"), mixin("selector_piece"))
		l.state("class", rule(id, "nc"), mixin("selector_piece"))
		l.state("id", rule(id, "nn"), mixin("selector_piece"))
		l.state("placeholder", rule(id, "nv"), mixin("selector_piece"))
		l.state("for",
			rule(`(from|to|through)`, "ow"),
			mixin("value"),
		)
		l.state("attr_common",
			mixin("has_interp"),
			ruleF(id, func(c *rctx) {
				if cssProperties[c.m.String()] {
					c.token("nl")
				} else {
					c.token("na")
				}
			}),
		)
		l.state("attribute",
			mixin("attr_common"),
			ruleF(`([ \t]*)(:)`, func(c *rctx) { c.groups("", "p"); c.push("value") }),
		)
		l.state("inline_comment",
			rule(`(\\#|#(?=[^\n{])|\*(?=[^\n\/])|[^\n#*])+`, "cm"),
			mixin("has_interp"),
			rule(`[*]/`, "cm", "#pop"),
		)
		l.state("root",
			rule(`\s+`, ""),
			rule(`//.*?$`, "c1"),
			rule(`(?m)/[*].*?[*]/`, "cm"),
			rule(`@import\b`, "k", "value"),
			mixin("content_common"),
			ruleF(`(?=[^;{}][;}])`, func(c *rctx) { c.push("attribute") }),
			ruleF(`(?=[^;{}:\[]+:[^a-z])`, func(c *rctx) { c.push("attribute") }),
			ruleF(``, func(c *rctx) { c.push("selector") }),
		)
		l.state("end_section",
			rule(`\n`, ""),
			ruleF(`[;{}]`, func(c *rctx) { c.token("p"); c.resetStack() }),
		)
		return l
	})
}
