// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

// Rouge 4.7 の scala.rb / groovy.rb の移植。

var (
	groovyKeywords = wordset(`assert break case catch continue default do else finally for
if goto instanceof new return switch this throw try while in as`)
	groovyDeclarations = wordset(`abstract const extends final implements native private
protected public static strictfp super synchronized throws
transient volatile`)
	groovyTypes     = wordset(`def var boolean byte char double float int long short void`)
	groovyConstants = wordset(`true false null`)
)

// delegateClone は Rouge の delegate self.clone（状態スタックを共有する複製で字句解析）。
func (c *rctx) delegateClone(text string) {
	sub := &rctx{lx: c.lx, stack: append([]*rstate(nil), c.stack...), vars: c.vars, subs: c.subs}
	sub.continueLex(text, c.emit)
	c.stack = sub.stack
}

func init() {
	registerRouge("scala", func() *rlexer {
		const (
			whitespace = `[\p{Z}\t\n\v\f\r\x85]`
			letter     = `[\p{L}$_]`
			upper      = `[\p{Lu}$_]`
			digits     = `[0-9]`
			parens     = `[(){}\[\]]`
			delims     = `[‘’".;,]`
			op         = `(?:(?!` + whitespace + `|` + letter + `|` + digits + `|` + parens + `|` + delims + `)[-!#%&*/:?@\\^\p{Sm}\p{So}])`
			idrest     = `(?:` + letter + `(?:` + letter + `|` + digits + `)*(?:(?<=_)` + op + `+)?)`
			keywords   = `abstract|case|catch|def|do|else|extends|final|finally|for|forSome|if|implicit|lazy|match|new|override|private|protected|requires|return|sealed|super|this|throw|try|val|var|while|with|yield`
			typechunk  = `(?:` + idrest + `|` + op + "+`[^`]+`)"
		)
		l := &rlexer{tag: "scala"}
		l.state("root",
			ruleF(`(class|trait|object)(\s+)`, func(c *rctx) { c.groups("k", ""); c.push("class") }),
			rule(`'`+idrest+`(?!')`, "ss"),
			rule(`[^\S\n]+`, ""),
			rule(`//.*`, "c1"),
			rule(`/\*`, "cm", "comment"),
			rule(`@`+idrest, "nd"),
			ruleG(`(def)(\s+)(`+idrest+`|`+op+"+|`[^`]+`)(\\s*)", toks("k", "", "nf", "")),
			ruleG(`(val)(\s+)(`+idrest+`|`+op+"+|`[^`]+`)(\\s*)", toks("k", "", "nv", "")),
			ruleG(`(this)(\n*)(\.)(`+idrest+`)`, toks("k", "", "o", "py")),
			ruleG(`(`+idrest+`|_)(\n*)(\.)(`+idrest+`)`, toks("nv", "", "o", "py")),
			rule(upper+idrest+`\b`, "nc"),
			ruleG(`(`+idrest+`)(`+whitespace+`*)(\()`, toks("nf", "", "o")),
			ruleG(`(\.)(`+idrest+`)`, toks("o", "py")),
			rule(`(`+keywords+`)\b|(<[%:-]|=>|>:|[#=@_⇒←])(\b|(?=\s)|$)`, "k"),
			rule(`:(?!`+op+`)`, "k", "type"),
			rule(`(true|false|null)\b`, "kc"),
			ruleF(`(import|package)(\s+)`, func(c *rctx) { c.groups("k", ""); c.push("import") }),
			ruleF(`(type)(\s+)`, func(c *rctx) { c.groups("k", ""); c.push("type") }),
			rule(`(?m)""".*?"""(?!")`, "s"),
			rule(`"(\\\\|\\"|[^"])*"`, "s"),
			rule(`'\\.'|'[^\\]'|'\\u[0-9a-fA-F]{4}'`, "sc"),
			rule(idrest, "n"),
			rule("`[^`]+`", "n"),
			rule(`\[`, "o", "typeparam"),
			rule(`[\(\)\{\};,.#]`, "o"),
			rule(op+`+`, "o"),
			rule(`([0-9][0-9]*\.[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?[fFdD]?`, "mf"),
			rule(`([0-9][0-9]*[fFdD])`, "mf"),
			rule(`0x[0-9a-fA-F]+`, "mh"),
			rule(`[0-9]+L?`, "mi"),
			rule(`\n`, ""),
		)
		l.state("class",
			ruleF(`(`+idrest+`|`+op+"+|`[^`]+`)(\\s*)(\\[)", func(c *rctx) { c.groups("nc", "", "o"); c.push("typeparam") }),
			rule(`\s+`, ""),
			rule(`{`, "o", "#pop"),
			rule(`\(`, "o", "#pop"),
			rule(`//.*`, "c1", "#pop"),
			rule(idrest+`|`+op+"+|`[^`]+`", "nc", "#pop"),
		)
		l.state("type",
			rule(`\s+`, ""),
			rule(`<[%:]|>:|[#_⇒]|forSome|type`, "k"),
			ruleF(`([,\);}]|=>|=)(\s*)`, func(c *rctx) { c.groups("o", ""); c.pop() }),
			rule(`[\(\{]`, "o", "type"),
			ruleF(`(`+typechunk+`(?:\.`+typechunk+`)*)(\s*)(\[)`, func(c *rctx) {
				c.groups("kt", "", "o")
				c.pop()
				c.push("typeparam")
			}),
			ruleF(`(`+typechunk+`(?:\.`+typechunk+`)*)(\s*)$`, func(c *rctx) { c.groups("kt", ""); c.pop() }),
			rule(`//.*`, "c1", "#pop"),
			rule(`\.|`+idrest+`|`+op+"+|`[^`]+`", "kt"),
		)
		l.state("typeparam",
			rule(`[\s,]+`, ""),
			rule(`<[%:]|=>|>:|[#_⇒]|forSome|type`, "k"),
			rule(`([\]\)\}])`, "o", "#pop"),
			rule(`[\(\[\{]`, "o", "typeparam"),
			rule(`\.|`+idrest+`|`+op+"+|`[^`]+`", "kt"),
		)
		l.state("comment",
			rule(`[^/\*]+`, "cm"),
			rule(`/\*`, "cm", "comment"),
			rule(`\*/`, "cm", "#pop"),
			rule(`[*/]`, "cm"),
		)
		l.state("import", rule(`(`+idrest+`|\.)+`, "nn", "#pop"))
		return l
	})

	registerRouge("groovy", func() *rlexer {
		l := &rlexer{tag: "groovy"}
		l.state("root",
			ruleF(`(?x)^
          (\s*(?:\w[\w.\[\]]*\s+)+?) # return arguments
          (\w\w*) # method name
          (\s*) (\() # signature start
        `, func(c *rctx) {
				m1, m2, m3, m4 := c.group(1), c.group(2), c.group(3), c.group(4)
				c.delegateClone(m1)
				c.token("nf", m2)
				c.token("", m3)
				c.token("o", m4)
			}),
			rule(`[^\S\n]+`, ""),
			rule(`//.*?$`, "c1"),
			rule(`(?m)/[*].*?[*]/`, "cm"),
			rule(`@\w[\w.]*`, "nd"),
			rule(`(class|interface|trait|enum|record)\b`, "kd", "class"),
			rule(`package\b`, "kn", "import"),
			rule(`import\b`, "kn", "import"),
			rule(`(?m)""".*?"""`, "s2"),
			rule(`(?m)'''.*?'''`, "s1"),
			rule(`"(\\.|\\\n|.)*?"`, "s2"),
			rule(`'(\\.|\\\n|.)*?'`, "s1"),
			rule(`(?m)\$/(\$.|.)*?/\$`, "s"),
			rule(`/(\\.|\\\n|.)*?/`, "s"),
			rule(`'\\.'|'[^\\]'|'\\u[0-9a-f]{4}'`, "sc"),
			ruleG(`(\.)([a-zA-Z_][a-zA-Z0-9_]*)`, toks("o", "na")),
			rule(`[a-zA-Z_][a-zA-Z0-9_]*:`, "nl"),
			ruleF(`[a-zA-Z_\$][a-zA-Z0-9_]*`, func(c *rctx) {
				w := c.m.String()
				switch {
				case groovyKeywords[w]:
					c.token("k")
				case groovyDeclarations[w]:
					c.token("kd")
				case groovyTypes[w]:
					c.token("kt")
				case groovyConstants[w]:
					c.token("kc")
				default:
					c.token("n")
				}
			}),
			rule(`[~^*!%&\[\](){}<>\|+=:;,./?-]`, "o"),
			rule(`\d+\.\d+([eE]\d+)?[fd]?`, "mf"),
			rule(`0x[0-9a-f]+`, "mh"),
			rule(`[0-9]+L?`, "mi"),
			rule(`\n`, ""),
		)
		l.state("class",
			rule(`\s+`, ""),
			rule(`\w\w*`, "nc", "#pop"),
		)
		l.state("import",
			rule(`\s+`, ""),
			rule(`[\w.]+[*]?`, "nn", "#pop"),
		)
		return l
	})
}
