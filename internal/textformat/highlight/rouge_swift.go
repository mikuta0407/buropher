package highlight

import (
	"regexp"
	"strings"
)

// Rouge 4.7 の swift.rb の移植。

var (
	swiftKeywords = wordset(`autoreleasepool await break case catch consume continue default defer discard do each else fallthrough guard if in for repeat return switch throw try where while
as dynamicType is new super self Self Type
associativity async didSet get infix inout isolated left mutating none nonmutating operator override postfix precedence precedencegroup prefix rethrows right set throws unowned weak willSet`)
	swiftDeclarations = wordset(`actor any associatedtype borrowing class consuming deinit distributed dynamic enum convenience extension fileprivate final func import indirect init internal lazy let macro nonisolated open optional package private protocol public required some static struct subscript typealias var`)
	swiftConstants    = wordset(`true false nil`)
	reUpperStart      = regexp.MustCompile(`^\p{Lu}`)
)

func init() {
	registerRouge("swift", func() *rlexer {
		const (
			alpha  = `\p{L}\p{Nl}\p{Mn}\p{Mc}`
			idHead = `(?:_|(?!\p{Mc})[` + alpha + `]|[^\u0000-￿])`
			idRest = `(?:[` + alpha + `\p{Nd}_]|[^\u0000-￿])`
			id     = `(?:` + idHead + idRest + `*)`
		)
		l := &rlexer{tag: "swift"}
		l.start = func(c *rctx) {
			c.push("bol")
			c.vars["re_delim"] = ""
		}
		l.state("bol",
			rule(`#(?![#"\/]).*`, "cp"),
			mixin("inline_whitespace"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("inline_whitespace",
			rule(`(?m)\s+`, ""),
			mixin("has_comments"),
		)
		l.state("whitespace",
			rule(`(?m)\n+`, "", "bol"),
			rule(`\/\/.*?$`, "c1", "bol"),
			mixin("inline_whitespace"),
		)
		l.state("has_comments", rule(`/[*]`, "cm", "nested_comment"))
		l.state("nested_comment",
			mixin("has_comments"),
			rule(`[*]/`, "cm", "#pop"),
			rule(`(?m)[^*/]+`, "cm"),
			rule(`.`, "cm"),
		)
		l.state("root",
			mixin("whitespace"),
			rule(`\$(([1-9]\d*)?\d)`, "nv"),
			rule(`\$`+id, "n"),
			rule(`~Copyable\b`, "kt"),
			rule(`[()\[\]{}:;,?\\]`, "p"),
			rule(`(#*)/(?!\s).*(?<![\s\\])/\1`, "sr"),
			rule(`[-/=+*%<>!&|^.~]+`, "o"),
			rule(`@?"`, "s", "dq"),
			rule(`'(\\.|.)'`, "sc"),
			rule(`(?i)(\d+(?:_\d+)*\*|(?:\d+(?:_\d+)*)*\.\d+(?:_\d)*)(e[+-]?\d+(?:_\d)*)?`, "mf"),
			rule(`(?i)\d+e[+-]?[0-9]+`, "mf"),
			rule(`0o?[0-7]+(?:_[0-7]+)*`, "mo"),
			rule(`0x[0-9A-Fa-f]+(?:_[0-9A-Fa-f]+)*((\.[0-9A-F]+(?:_[0-9A-F]+)*)?p[+-]?\d+)?`, "mh"),
			rule(`0b[01]+(?:_[01]+)*`, "mb"),
			rule(`[\d]+(?:_\d+)*`, "mi"),
			rule(`@`+id, "kd"),
			rule(`#`+id, "k"),
			ruleF(`(private|internal)(\([ ]*)(\w+)([ ]*\))`, func(c *rctx) {
				if c.group(3) == "set" {
					c.token("kd")
				} else {
					c.groups("kd", "kd", "err", "kd")
				}
			}),
			ruleF(`(unowned\([ ]*)(\w+)([ ]*\))`, func(c *rctx) {
				if g := c.group(2); g == "safe" || g == "unsafe" {
					c.token("kd")
				} else {
					c.groups("kd", "err", "kd")
				}
			}),
			ruleG(`(let|var)\b(\s*)(`+id+`)`, toks("k", "", "nv")),
			ruleF(`(let|var)\b(\s*)([(])`, func(c *rctx) { c.groups("k", "", "p"); c.push("tuple") }),
			ruleF(`(?!\b(if|while|for|private|internal|unowned|switch|case)\b)\b`+id+`(?=(\?|!)?\s*[(])`, func(c *rctx) {
				if reUpperStart.MatchString(c.m.String()) {
					c.token("kt")
				} else {
					c.token("nf")
				}
			}),
			rule(`as[?!]?(?=\s)`, "k"),
			rule(`try[!]?(?=\s)`, "k"),
			ruleG(`(#?(?!default)(?![\p{Lu}])`+id+`)(\s*)(:)`, toks("nv", "", "p")),
			ruleF(id, func(c *rctx) {
				w := c.m.String()
				switch {
				case swiftKeywords[w]:
					c.token("k")
				case swiftDeclarations[w]:
					c.token("kd")
				case swiftConstants[w]:
					c.token("kc")
				case reUpperStart.MatchString(w):
					c.token("kt")
				default:
					c.token("n")
				}
			}),
			ruleG("(`)("+id+")(`)", toks("p", "nv", "p")),
			ruleF(`(#+)/\n`, func(c *rctx) {
				c.vars["re_delim"] = c.group(1)
				c.token("sr")
				c.push("re_multi")
			}),
		)
		l.state("tuple",
			rule(`(`+id+`)`, "nv"),
			ruleG("(`)("+id+")(`)", toks("p", "nv", "p")),
			rule(`,`, "p"),
			rule(`[(]`, "p", "#push"),
			rule(`[)]`, "p", "#pop"),
			mixin("inline_whitespace"),
		)
		l.state("dq",
			rule(`\\[\\0tnr'"]`, "se"),
			rule(`\\[(]`, "se", "interp"),
			rule(`\\u\{\h{1,8}\}`, "se"),
			rule(`[^\\"]+`, "s"),
			rule(`"""`, "s", "#pop"),
			rule(`"`, "s", "#pop"),
		)
		l.state("interp",
			rule(`[(]`, "p", "interp_inner"),
			rule(`[)]`, "se", "#pop"),
			mixin("root"),
		)
		l.state("interp_inner",
			rule(`[(]`, "p", "#push"),
			rule(`[)]`, "p", "#pop"),
			mixin("root"),
		)
		l.state("re_multi",
			ruleF(`^\s*/#+`, func(c *rctx) {
				c.token("sr")
				d, _ := c.vars["re_delim"].(string)
				if strings.HasSuffix(c.m.String(), "/"+d) {
					c.vars["re_delim"] = ""
					c.pop()
				}
			}),
			rule(`#.*`, "c1"),
			rule(`(?m).`, "sr"),
		)
		return l
	})
}
