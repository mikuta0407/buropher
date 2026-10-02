package highlight

// Rouge 4.7 の plain_text.rb / json.rb / diff.rb の移植。

func init() {
	registerRouge("plaintext", func() *rlexer {
		return &rlexer{tag: "plaintext", stream: func(c *rctx, text string) { c.emit("", text) }}
	})

	registerRouge("json", func() *rlexer {
		l := &rlexer{tag: "json"}
		l.state("whitespace", rule(`\s+`, "w"))
		l.state("root",
			mixin("whitespace"),
			rule(`{`, "p", "object"),
			rule(`\[`, "p", "array"),
			mixin("name"),
			mixin("value"),
			rule(`[\]}]`, "p"),
		)
		l.state("object",
			mixin("whitespace"),
			mixin("name"),
			mixin("value"),
			rule(`}`, "p", "#pop"),
			rule(`,`, "p"),
		)
		l.state("name",
			ruleG(`("(?:\\.|[^"\\\n])*?")(\s*)(:)`, toks("nl", "w", "p")),
		)
		l.state("value",
			mixin("whitespace"),
			mixin("constants"),
			rule(`"`, "s2", "string"),
			rule(`\[`, "p", "array"),
			rule(`{`, "p", "object"),
		)
		l.state("string",
			rule(`[^\\"]+`, "s2"),
			rule(`\\.`, "se"),
			rule(`"`, "s2", "#pop"),
		)
		l.state("array",
			mixin("value"),
			rule(`\]`, "p", "#pop"),
			rule(`,`, "p"),
		)
		l.state("constants",
			rule(`(?:true|false|null)`, "kc"),
			rule(`(?i)-?(?:0|[1-9]\d*)\.\d+(?:e[+-]?\d+)?`, "mf"),
			rule(`(?i)-?(?:0|[1-9]\d*)(?:e[+-]?\d+)?`, "mi"),
		)
		return l
	})

	registerRouge("diff", func() *rlexer {
		l := &rlexer{tag: "diff"}
		l.state("root",
			rule(`^ .*$\n?`, ""),
			rule(`^---$\n?`, "p"),
			rule(`(?x)
          (^\++.*$\n?) |
          (^>+[ \t]+.*$\n?) |
          (^>+$\n?)
        `, "gi"),
			rule(`(?x)
          (^-+.*$\n?) |
          (^<+[ \t]+.*$\n?) |
          (^<+$\n?)
        `, "gd"),
			rule(`^!.*$\n?`, "gs"),
			rule(`^([Ii]ndex|diff).*$\n?`, "gh"),
			ruleG(`^(@@[^@]*@@)([^\n]*\n)`, toks("p", "")),
			rule(`^\w.*$\n?`, "p"),
			rule(`^=.*$\n?`, "gh"),
			rule(`.+$\n?`, ""),
		)
		return l
	})
}
