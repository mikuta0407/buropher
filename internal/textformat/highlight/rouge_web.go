package highlight

import "strings"

// Rouge 4.7 の css.rb / html.rb / xml.rb の移植。

const (
	cssIdent  = `[\p{L}_-][` + pWord + `\p{Cf}-]*`
	cssNumber = `-?(?:[0-9]+(\.[0-9]+)?|\.[0-9]+)`
	xmlName   = `[\p{L}:_][` + pWord + `\p{Cf}:.·-]*`
)

// sub は保持している delegate 先のレキサーを返す（無ければ作る）。
func (c *rctx) sub(tag string) *rctx {
	s := c.subs[tag]
	if s == nil {
		lx := rougeLexerByTag(tag)
		if lx == nil {
			return nil
		}
		s = c.child(lx)
		c.subs[tag] = s
	}
	return s
}

func init() {
	registerRouge("css", func() *rlexer {
		l := &rlexer{tag: "css"}
		l.state("root",
			mixin("basics"),
			rule(`{`, "p", "stanza"),
			rule(`:[:]?`+cssIdent, "nd"),
			rule(`\.`+cssIdent, "nc"),
			rule(`#`+cssIdent, "nf"),
			rule(`@`+cssIdent, "k", "at_rule"),
			rule(cssIdent, "nt"),
			rule(`[~^*!%&\[\]()<>|+=@:;,./?-]`, "o"),
			rule(`"(\\\\|\\"|[^"])*"`, "s1"),
			rule(`'(\\\\|\\'|[^'])*'`, "s2"),
			rule(`[0-9]{1,3}\%`, "m"),
		)
		l.state("value",
			mixin("basics"),
			rule(`(?i)#[0-9a-f]{3,8}`, "nx"),
			rule(cssNumber+`(?:%|(?:px|pt|pc|in|cm|mm|Q|em|rem|ex|ch|vw|vh|vmin|vmax|fr|dpi|dpcm|dppx|deg|grad|rad|turn|s|ms|Hz|kHz)\b)?`, "m"),
			rule(`[\[\]():.,]`, "p"),
			rule(`"(\\\\|\\"|[^"])*"`, "s1"),
			rule(`'(\\\\|\\'|[^'])*'`, "s2"),
			rule(`(?i)(true|false)`, "no"),
			rule(`\-\-`+cssIdent, "l"),
			rule(`[*+/-]`, "o"),
			ruleF(cssIdent, func(c *rctx) {
				w := strings.ToLower(c.m.String())
				switch {
				case cssColors[w]:
					c.token("nx")
				case cssBuiltins[w]:
					c.token("nb")
				case cssFunctions[w]:
					c.token("nf")
				default:
					c.token("n")
				}
			}),
		)
		l.state("at_rule",
			rule(`(?:<=|>=|~=|\|=|\^=|\$=|\*=|<|>|=)`, "o"),
			rule(`(?m){(?=\s*`+cssIdent+`\s*:)`, "p", "at_stanza"),
			rule(`{`, "p", "at_body"),
			rule(`;`, "p", "#pop"),
			mixin("value"),
		)
		l.state("at_body", mixin("at_content"), mixin("root"))
		l.state("at_stanza", mixin("at_content"), mixin("stanza"))
		l.state("at_content",
			ruleF(`}`, func(c *rctx) { c.token("p"); c.pop(2) }),
		)
		l.state("basics",
			rule(`(?m)\s+`, ""),
			rule(`(?m)/\*(?:.*?)\*/`, "c"),
		)
		l.state("stanza",
			mixin("basics"),
			rule(`}`, "p", "#pop"),
			ruleF(`(?m)(`+cssIdent+`)(\s*)(:)`, func(c *rctx) {
				name := c.group(1)
				tok := "py"
				if cssProperties[name] {
					tok = "nl"
				} else {
					for p := range cssVendorPrefixes {
						if strings.HasPrefix(name, p) {
							tok = "nl"
							break
						}
					}
				}
				c.groups(tok, "", "p")
				c.push("stanza_value")
			}),
		)
		l.state("stanza_value",
			rule(`;`, "p", "#pop"),
			ruleF(`(?=})`, func(c *rctx) { c.pop() }),
			rule(`!\s*important\b`, "cp"),
			rule(`^@.*?$`, "cp"),
			mixin("value"),
		)
		return l
	})

	registerRouge("xml", func() *rlexer {
		l := &rlexer{tag: "xml"}
		l.state("root",
			rule(`[^<&]+`, ""),
			rule(`&\S*?;`, "ni"),
			rule(`<!\[CDATA\[.*?\]\]\>`, "cp"),
			rule(`<!--`, "c", "comment"),
			rule(`<\?.*?\?>`, "cp"),
			rule(`<![^>]*>`, "cp"),
			rule(`(?m)<\s*`+xmlName, "nt", "tag"),
			rule(`(?m)<\s*/\s*`+xmlName+`\s*>`, "nt"),
		)
		l.state("comment",
			rule(`(?m)[^-]+`, "c"),
			rule(`-->`, "c", "#pop"),
			rule(`-`, "c"),
		)
		l.state("tag",
			rule(`(?m)\s+`, ""),
			rule(`(?m)`+xmlName+`\s*=`, "na", "attr"),
			rule(`/?\s*>`, "nt", "#pop"),
		)
		l.state("attr",
			rule(`(?m)\s+`, ""),
			rule(`(?m)".*?"|'.*?'|[^\s>]+`, "s", "#pop"),
		)
		return l
	})

	registerRouge("html", func() *rlexer {
		const tagName = `[\p{L}:_-][` + pWord + `\p{Cf}:.·-]*`
		l := &rlexer{tag: "html"}
		l.start = func(c *rctx) {
			c.subs = map[string]*rctx{}
		}
		l.state("root",
			rule(`(?m)[^<&]+`, ""),
			rule(`&\S*?;`, "ni"),
			rule(`(?im)<!DOCTYPE .*?>`, "cp"),
			rule(`(?m)<!\[CDATA\[.*?\]\]>`, "cp"),
			rule(`<!--`, "c", "comment"),
			rule(`(?m)<\?.*?\?>`, "cp"),
			ruleF(`(?m)<\s*script\s*`, func(c *rctx) {
				c.token("nt")
				if s := c.sub("javascript"); s != nil {
					s.reset()
				}
				c.push("script_content")
				c.push("tag")
			}),
			ruleF(`(?m)<\s*style\s*`, func(c *rctx) {
				c.token("nt")
				if s := c.sub("css"); s != nil {
					s.reset()
				}
				c.vars["lang"] = "css"
				c.push("style_content")
				c.push("tag")
			}),
			rule(`</`, "nt", "tag_end"),
			rule(`<`, "nt", "tag_start"),
			rule(`<\s*`+tagName, "nt", "tag"),
			rule(`<\s*/\s*`+tagName+`\s*>`, "nt"),
		)
		l.state("tag_end",
			mixin("tag_end_end"),
			ruleF(tagName, func(c *rctx) { c.token("nt"); c.gotoState("tag_end_end") }),
		)
		l.state("tag_end_end",
			rule(`\s+`, ""),
			rule(`>`, "nt", "#pop"),
		)
		l.state("tag_start",
			rule(`\s+`, ""),
			ruleF(tagName, func(c *rctx) { c.token("nt"); c.gotoState("tag") }),
			ruleF(``, func(c *rctx) { c.gotoState("tag") }),
		)
		l.state("comment",
			rule(`[^-]+`, "c"),
			rule(`-->`, "c", "#pop"),
			rule(`-`, "c"),
		)
		l.state("tag",
			rule(`(?m)\s+`, ""),
			rule(`(?m)[\p{L}:_\[\]()*.-][`+pWord+`\p{Cf}:.·\[\]()*-]*\s*=\s*`, "na", "attr"),
			rule(`[\p{L}:_*#-][`+pWord+`\p{Cf}:.·*#-]*`, "na"),
			rule(`(?m)/?\s*>`, "nt", "#pop"),
		)
		l.state("attr",
			ruleF(`"`, func(c *rctx) { c.token("s"); c.gotoState("dq") }),
			ruleF(`'`, func(c *rctx) { c.token("s"); c.gotoState("sq") }),
			rule(`[^\s>]+`, "s", "#pop"),
		)
		l.state("dq",
			rule(`"`, "s", "#pop"),
			rule(`[^"]+`, "s"),
		)
		l.state("sq",
			rule(`'`, "s", "#pop"),
			rule(`[^']+`, "s"),
		)
		l.state("script_content",
			ruleF(`[^<]+`, func(c *rctx) { c.delegate("javascript") }),
			rule(`(?m)<\s*/\s*script\s*>`, "nt", "#pop"),
			ruleF(`<`, func(c *rctx) { c.delegate("javascript") }),
		)
		l.state("style_content",
			ruleF(`[^<]+`, func(c *rctx) { c.delegate("css") }),
			rule(`(?m)<\s*/\s*style\s*>`, "nt", "#pop"),
			ruleF(`<`, func(c *rctx) { c.delegate("css") }),
		)
		return l
	})
}
