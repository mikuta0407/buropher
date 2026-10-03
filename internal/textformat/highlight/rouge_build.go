// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

// Rouge 4.7 の docker.rb / make.rb / toml.rb の移植。

func init() {
	registerRouge("docker", func() *rlexer {
		const keywords = `FROM|MAINTAINER|CMD|LABEL|EXPOSE|ENV|ADD|COPY|ENTRYPOINT|VOLUME|USER|WORKDIR|ARG|STOPSIGNAL|HEALTHCHECK|SHELL`
		l := &rlexer{tag: "docker"}
		l.start = func(c *rctx) { delete(c.subs, "shell") }
		l.state("root",
			rule(`\s+`, ""),
			ruleG(`(?i)^(FROM)(\s+)(.*)(\s+)(AS)(\s+)(.*)`, toks("k", "w", "s", "w", "k", "w", "s")),
			ruleG(`(?i)^(ONBUILD)(\s+)(`+keywords+`)(.*)`, toks("k", "w", "k", "s")),
			ruleG(`(?i)^(`+keywords+`)\b(.*)`, toks("k", "s")),
			rule(`#.*?$`, "c"),
			ruleF(`(?i)^(ONBUILD\s+)?RUN(\s+)`, func(c *rctx) {
				c.token("k")
				c.push("run")
				if s := c.sub("shell"); s != nil {
					s.reset()
				}
			}),
			rule(`\w+`, ""),
			rule(`[^\w]+`, ""),
			rule(`.`, ""),
		)
		l.state("run",
			rule(`\n`, "", "#pop"),
			rule(`(?m)\\.`, "se"),
			ruleF(`(\\.|[^\n\\])+`, func(c *rctx) { c.delegate("shell") }),
		)
		return l
	})

	registerRouge("make", func() *rlexer {
		const functions = `abspath|addprefix|addsuffix|and|basename|call|dir|error|eval|file|filter|filter-out|findstring|firstword|flavor|foreach|if|join|lastword|notdir|or|origin|patsubst|realpath|shell|sort|strip|subst|suffix|value|warning|wildcard|word|wordlist|words`
		const stop = `[\$]{1,2}\(|[\$]{1,2}\{|\(|\)|\}|\\|$`
		l := &rlexer{tag: "make"}
		l.start = func(c *rctx) {
			if s := c.sub("shell"); s != nil {
				s.reset()
			}
		}
		l.state("root",
			rule(`\s+`, ""),
			rule(`#.*?\n`, "c"),
			ruleG(`([-s]?include)((?:[\t ]+[^\t\n #]+)+)`, toks("k", "sx")),
			ruleG(`((?:ifn?def|ifn?eq|unexport)\b)([\t ]+)([^#\n]+)`, toks("k", "", "nv")),
			ruleG(`(else\b)([\t ]+)((?:ifn?def|ifn?eq)\b)([\t ]+)([^#\n]+)`, toks("k", "", "k", "", "nv")),
			rule(`(?:else|endif|endef|endfor)[\t ]*(?=[#\n])`, "k"),
			ruleF(`(export)([\t ]+)(?=[\w\${}()\t -]+\n)`, func(c *rctx) { c.groups("k", ""); c.push("export") }),
			rule(`export[\t ]+`, "k"),
			ruleF(`(?m)(override\b)*([\t ]*)([\w${}().-]+)([\t ]*)([!?:+]?=)`, func(c *rctx) {
				c.groups("nb", "", "nv", "", "o")
				c.push("shell_line")
			}),
			rule(`"(\\\\|\\.|[^"\\])*"`, "s2"),
			rule(`'(\\\\|\\.|[^'\\])*'`, "s1"),
			ruleF(`([^\n:]+)(:+)([ \t]*)`, func(c *rctx) { c.groups("nl", "o", ""); c.push("block_header") }),
			ruleG(`(override\b)*([\t ])*(define)([\t ]+)([^#\n]+)`, toks("nb", "", "k", "", "nv")),
			ruleF(`(?m)(\$[({])([\t ]*)(`+functions+`)([\t ]+)`, func(c *rctx) {
				c.groups("nf", "", "nb", "")
				c.push("shell_expr")
			}),
		)
		l.state("export",
			rule(`[\w\${}()-]`, "nv"),
			rule(`\n`, "", "#pop"),
			rule(`[\t ]+`, ""),
		)
		l.state("block_header",
			rule(`[^,\\\n#]+`, "nf"),
			rule(`,`, "p"),
			rule(`#.*?`, "c"),
			rule(`\\\n`, ""),
			rule(`\\.`, ""),
			ruleF(`\n`, func(c *rctx) { c.token(""); c.gotoState("block_body") }),
		)
		l.state("block_body",
			ruleG(`(ifn?def|ifn?eq)([\t ]+)([^#\n]+)(#.*)?(\n)`, toks("k", "", "nv", "c", "")),
			ruleG(`(else|endif)([\t ]*)(#.*)?(\n)`, toks("k", "", "c", "")),
			ruleF(`(\t[\t ]*)([@-]?)`, func(c *rctx) { c.groups("", "p"); c.push("shell_line") }),
			ruleF(``, func(c *rctx) {
				if s := c.sub("shell"); s != nil {
					s.reset()
				}
				c.pop()
			}),
		)
		l.state("shell",
			rule(`[\$]{1,2}[({]`, "p", "macro_expr"),
			ruleF(`(?m)(\$[({])([\t ]*)(`+functions+`)([\t ]+)`, func(c *rctx) {
				c.groups("p", "", "nb", "")
				c.push("shell_expr")
			}),
			ruleF(`(?m)\\.`, func(c *rctx) { c.delegate("shell") }),
			ruleF(`(?m).+?(?=`+stop+`)`, func(c *rctx) { c.delegate("shell") }),
			ruleF(stop, func(c *rctx) { c.delegate("shell") }),
		)
		l.state("macro_expr",
			rule(`[)}]`, "p", "#pop"),
			rule(`\n`, "", "#pop"),
			mixin("shell"),
		)
		l.state("shell_expr",
			ruleF(`[({]`, func(c *rctx) { c.delegate("shell"); c.push() }),
			rule(`[)}]`, "p", "#pop"),
			mixin("shell"),
		)
		l.state("shell_line",
			rule(`\n`, "", "#pop"),
			mixin("shell"),
		)
		return l
	})

	registerRouge("toml", func() *rlexer {
		l := &rlexer{tag: "toml"}
		l.state("root",
			mixin("whitespace"),
			mixin("key"),
			ruleF(`(=)(\s*)`, func(c *rctx) { c.groups("o", "w"); c.push("value") }),
			rule(`\[\[?`, "k", "table_key"),
		)
		l.state("key",
			rule(`[A-Za-z0-9_-]+`, "n"),
			rule(`"`, "s", "dq"),
			rule(`'`, "s", "sq"),
			rule(`\.`, "p"),
		)
		l.state("table_key",
			rule(`[A-Za-z0-9_-]+`, "n"),
			rule(`"`, "s", "dq"),
			rule(`'`, "s", "sq"),
			rule(`\.`, "k"),
			rule(`\]\]?`, "k", "#pop"),
			rule(`[ \t]+`, "w"),
		)
		l.state("value",
			rule(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`, "ld", "#pop"),
			rule(`\d\d:\d\d:\d\d(\.\d+)?`, "ld", "#pop"),
			rule(`[+-]?\d+(?:_\d+)*\.\d+(?:_\d+)*(?:[eE][+-]?\d+(?:_\d+)*)?`, "mf", "#pop"),
			rule(`[+-]?\d+(?:_\d+)*[eE][+-]?\d+(?:_\d+)*`, "mf", "#pop"),
			rule(`[+-]?(?:nan|inf)`, "mf", "#pop"),
			rule(`0x\h+(?:_\h+)*`, "mh", "#pop"),
			rule(`0o[0-7]+(?:_[0-7]+)*`, "mo", "#pop"),
			rule(`0b[01]+(?:_[01]+)*`, "mb", "#pop"),
			rule(`[+-]?\d+(?:_\d+)*`, "mi", "#pop"),
			rule(`"""`, "s", "#pop", "mdq"),
			rule(`"`, "s", "#pop", "dq"),
			rule(`'''`, "s", "#pop", "msq"),
			rule(`'`, "s", "#pop", "sq"),
			rule(`(true|false)`, "kc", "#pop"),
			rule(`\[`, "p", "#pop", "array"),
			rule(`\{`, "p", "#pop", "inline"),
		)
		l.state("dq",
			rule(`"`, "s", "#pop"),
			rule(`\n`, "err", "#pop"),
			mixin("esc_str"),
			rule(`[^\\"\n]+`, "s"),
		)
		l.state("mdq",
			rule(`"""`, "s", "#pop"),
			mixin("esc_str"),
			rule(`[^\\"]+`, "s"),
			rule(`"+`, "s"),
		)
		l.state("sq",
			rule(`'`, "s", "#pop"),
			rule(`\n`, "err", "#pop"),
			rule(`[^'\n]+`, "s"),
		)
		l.state("msq",
			rule(`'''`, "s", "#pop"),
			rule(`[^']+`, "s"),
			rule(`'+`, "s"),
		)
		l.state("esc_str", rule(`\\[0t\tn\n "\\r]`, "se"))
		l.state("array",
			mixin("whitespace"),
			rule(`,`, "p"),
			rule(`\]`, "p", "#pop"),
			rule(``, "", "value"),
		)
		l.state("inline",
			rule(`[ \t]+`, "w"),
			mixin("key"),
			ruleF(`(=)(\s*)`, func(c *rctx) { c.groups("p", "w"); c.push("value") }),
			rule(`,`, "p"),
			rule(`\}`, "p", "#pop"),
		)
		l.state("whitespace",
			rule(`\s+`, ""),
			rule(`#.*?$`, "c"),
		)
		return l
	})
}
