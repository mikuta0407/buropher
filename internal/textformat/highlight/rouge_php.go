// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import (
	"regexp"
	"strings"
)

// Rouge 4.7 の php.rb の移植（TemplateLexer の親は HTML）。

var phpKeywords = wordset(`old_function cfunction
__class__ __dir__ __file__ __function__ __halt_compiler __line__
__method__ __namespace__ __trait__ abstract and array as break case
catch clone continue declare default die do echo else elseif empty
enddeclare endfor endforeach endif endswitch endwhile eval exit
extends final finally fn for foreach global goto if implements
include include_once instanceof insteadof isset list match new or
parent print private protected public readonly require require_once
return self static switch throw try unset var while xor yield`)

var (
	rePHPMagic    = regexp.MustCompile(`(?m)^__.*?__$`)
	rePHPConstEP  = regexp.MustCompile(`(?m)^(E|PHP)(_\p{Lu}+)+$`)
	rePHPConstant = regexp.MustCompile(`(?m)(\\|^)\p{Lu}[\p{Lu}0-9_]+$`)
	rePHPClass    = regexp.MustCompile(`(?m)(\\|^)\p{Lu}[\p{L}\p{M}\p{Nd}]*?$`)
)

func init() {
	registerRouge("php", func() *rlexer {
		const (
			id       = `[\p{L}_][\p{L}\p{N}_]*`
			ns       = `(?:` + id + `\\)+`
			idWithNs = `(?:\\?(?:` + ns + `)?` + id + `)`
		)
		l := &rlexer{tag: "php"}
		l.start = func(c *rctx) {
			if s := c.sub("html"); s != nil {
				s.reset()
			}
			c.push("start")
		}
		l.state("escape",
			ruleF(`\?>`, func(c *rctx) { c.token("cp"); c.resetStack() }),
		)
		l.state("return", ruleF(``, func(c *rctx) { c.pop() }))
		l.state("start",
			ruleF(`(?m)\s*(?=<)`, func(c *rctx) { c.delegate("html"); c.pop() }),
			ruleF(`(?i)[^$]+(?=<\?(php|=))`, func(c *rctx) { c.delegate("html"); c.pop() }),
			ruleF(``, func(c *rctx) { c.gotoState("php") }),
		)
		l.state("names",
			ruleF(`(?i)(?:public|protected|private)\(set\)`, func(c *rctx) { c.push("in_visibility"); c.token("k") }),
			ruleF(idWithNs+`(?=\s*\()`, func(c *rctx) {
				name := strings.ToLower(c.m.String())
				switch {
				case phpKeywords[name]:
					c.token("k")
				case phpBuiltins[name]:
					c.token("nb")
				default:
					c.token("nf")
				}
			}),
			ruleF(idWithNs, func(c *rctx) {
				m0 := c.m.String()
				name := strings.ToLower(m0)
				switch {
				case name == "use":
					c.push("in_use")
					c.token("kn")
				case name == "const":
					c.push("in_const")
					c.token("k")
				case name == "catch":
					c.push("in_catch")
					c.token("k")
				case name == "public" || name == "protected" || name == "private":
					c.push("in_visibility")
					c.token("k")
				case phpKeywords[name]:
					c.token("k")
				case rePHPMagic.MatchString(m0):
					c.token("nb")
				case rePHPConstEP.MatchString(m0):
					c.token("kc")
				case rePHPConstant.MatchString(m0):
					c.token("no")
				case rePHPClass.MatchString(m0):
					c.token("nc")
				default:
					c.token("n")
				}
			}),
		)
		l.state("operators", rule(`[~!%^&*+\|:.<>\/@-]+`, "o"))
		l.state("string",
			rule(`"`, "s2", "#pop"),
			rule(`[^\\{$"]+`, "s2"),
			rule(`\\u\{[0-9a-fA-F]+\}`, "se"),
			rule(`\\([efrntv\"$\\]|[0-7]{1,3}|[xX][0-9a-fA-F]{1,2})`, "se"),
			rule(`\$`+id+`(\[\S+\]|->`+id+`)?`, "nv"),
			rule(`\{\$\{`, "si", "string_interp_double"),
			rule(`\{(?=\$)`, "si", "string_interp_single"),
			ruleG(`(\{)(\S+)(\})`, toks("si", "nv", "si")),
			rule(`[${\\]+`, "s2"),
		)
		l.state("string_interp_double",
			rule(`\}\}`, "si", "#pop"),
			mixin("php"),
		)
		l.state("string_interp_single",
			rule(`\}`, "si", "#pop"),
			mixin("php"),
		)
		l.state("values",
			rule(`(?im)<<<(["']?)(`+id+`)\1\n.*?\n\s*\2;?`, "sh"),
			rule(`(?i)(\d[_\d]*)?\.(\d[_\d]*)?(e[+-]?\d[_\d]*)?`, "mf"),
			rule(`(?i)0o?[0-7][0-7_]*`, "mo"),
			rule(`(?i)0b[01][01_]*`, "mb"),
			rule(`(?i)0x[a-f0-9][a-f0-9_]*`, "mh"),
			rule(`\d[_\d]*`, "mi"),
			rule(`'([^'\\]*(?:\\.[^'\\]*)*)'`, "s1"),
			rule("`([^`\\\\]*(?:\\\\.[^`\\\\]*)*)`", "sb"),
			rule(`"`, "s2", "string"),
			ruleF(`(?i)(function|fn)\b`, func(c *rctx) {
				c.push("in_function_return")
				c.push("in_function_params")
				c.push("in_function_name")
				c.token("k")
			}),
			rule(`(?i)(true|false|null)\b`, "kc"),
			rule(`(?i)new\b`, "k", "in_new"),
		)
		l.state("variables",
			rule(`\$\{\$+`+id+`\}`, "nv"),
			rule(`\$+`+id, "nv"),
		)
		l.state("whitespace",
			rule(`\s+`, ""),
			rule(`#[^\[].*?$`, "c1"),
			rule(`//.*?$`, "c1"),
			rule(`(?m)/\*\*(?!/).*?\*/`, "cd"),
			rule(`(?m)/\*.*?\*/`, "cm"),
		)
		l.state("root",
			rule(`(?i)<\?(php|=)?`, "cp", "php"),
			ruleF(`(?m).*?(?=<\?)|.*`, func(c *rctx) { c.delegate("html") }),
		)
		l.state("php",
			mixin("escape"),
			mixin("whitespace"),
			mixin("variables"),
			mixin("values"),
			ruleG(`(?i)(namespace)(\s+)(`+idWithNs+`)`, toks("kn", "", "nn")),
			rule(`#\[.*\]$`, "na"),
			ruleG(`(?i)(class|interface|trait|extends|implements)(\s+)(`+idWithNs+`)`, toks("kd", "", "nc")),
			ruleF(`(?i)(enum)(\s+)(`+idWithNs+`)`, func(c *rctx) { c.groups("kd", "", "nc"); c.push("in_enum") }),
			mixin("names"),
			rule(`[;,\(\)\{\}\[\]]`, "p"),
			mixin("operators"),
			rule(`[=?]`, "o"),
		)
		l.state("in_assign",
			rule(`,`, "p", "#pop"),
			rule(`[\[\]]`, "p"),
			rule(`\(`, "p", "in_assign_function"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("values"),
			mixin("variables"),
			mixin("names"),
			mixin("operators"),
			mixin("return"),
		)
		l.state("in_assign_function",
			rule(`\)`, "p", "#pop"),
			rule(`,`, "p"),
			mixin("in_assign"),
		)
		l.state("in_catch",
			rule(`\(`, "p"),
			rule(`\|`, "o"),
			rule(idWithNs, "nc"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_const",
			ruleG(`(?i)(\??`+id+`)(\s+)(`+id+`)`, toks("kt", "", "no")),
			rule(id, "no"),
			rule(`=`, "o", "in_assign"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_function_body",
			rule(`{`, "p", "#push"),
			rule(`}`, "p", "#pop"),
			mixin("php"),
		)
		l.state("in_function_name",
			rule(`&`, "o"),
			rule(id, "n"),
			rule(`\(`, "p", "#pop"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_function_params",
			rule(`\)`, "p", "#pop"),
			rule(`,`, "p"),
			rule(`[.]{3}`, "p"),
			rule(`=`, "o", "in_assign"),
			rule(`(?i)\b(?:public|protected|private|readonly)(?:\(set\)|\b)`, "k"),
			rule(`(?i)\breadonly\b`, "k"),
			rule(`\??`+id, "kt", "in_assign"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("variables"),
			mixin("return"),
		)
		l.state("in_function_return",
			rule(`:`, "p"),
			rule(`(?i)use\b`, "k", "in_function_use"),
			rule(`\??`+id, "kt", "in_assign"),
			ruleF(`\{`, func(c *rctx) { c.token("p"); c.gotoState("in_function_body") }),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_function_use",
			rule(`[,\(]`, "p"),
			rule(`&`, "o"),
			rule(`\)`, "p", "#pop"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("variables"),
			mixin("return"),
		)
		l.state("in_new",
			ruleF(`(?i)class\b`, func(c *rctx) { c.token("kd"); c.gotoState("in_new_class") }),
			rule(idWithNs, "nc", "#pop"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_new_class",
			rule(`\}`, "p", "#pop"),
			rule(`\{`, "p"),
			mixin("php"),
		)
		l.state("in_use",
			rule(`[,\}]`, "p"),
			rule(`(?i)(function|const)\b`, "k"),
			ruleG(`(`+ns+`)(\{)`, toks("nn", "p")),
			rule(idWithNs+`(_`+id+`)+`, "nf"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("names"),
			mixin("return"),
		)
		l.state("in_visibility",
			rule(`(?i)\b(?:public|protected|private)(?:\(set\)|\b)`, "k"),
			rule(`(?i)\b(?:readonly|static)\b`, "k"),
			rule(`(?i)(?=(abstract|const|function)\b)`, "k", "#pop"),
			rule(`\??`+id, "kt", "#pop"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_enum",
			rule(`:`, "p", "in_enum_base_type"),
			rule(`\{`, "p", "in_enum_body"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_enum_base_type",
			rule(id, "kt", "#pop"),
			mixin("escape"),
			mixin("whitespace"),
			mixin("return"),
		)
		l.state("in_enum_body",
			rule(`\}`, "p", "#pop"),
			ruleG(`(?i)(case)(\s+)(`+id+`)`, toks("k", "", "no")),
			mixin("php"),
		)
		return l
	})
}
