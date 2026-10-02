package highlight

import (
	"strconv"
	"unicode/utf8"
)

// Rouge 4.7 の yaml.rb の移植。

type yamlState struct {
	indentStack       []int
	nextIndent        int
	blockScalarIndent int // -1 は nil
}

func yst(c *rctx) *yamlState {
	s, _ := c.vars["yaml"].(*yamlState)
	if s == nil {
		s = &yamlState{}
		c.vars["yaml"] = s
		s.reset()
	}
	return s
}

func (s *yamlState) reset() {
	s.indentStack = []int{0}
	s.nextIndent = 0
	s.blockScalarIndent = -1
}

func (s *yamlState) indent() int { return s.indentStack[len(s.indentStack)-1] }

// saveIndent は save_indent（字下げが減った場合はスタックを戻し、分割した文字列を返す）。
func (s *yamlState) saveIndent(m string) (string, string) {
	s.nextIndent = utf8.RuneCountInString(m)
	if s.nextIndent < s.indent() {
		for s.nextIndent < s.indent() {
			s.indentStack = s.indentStack[:len(s.indentStack)-1]
		}
		r := []rune(m)
		return string(r[:s.indent()]), string(r[s.indent():])
	}
	return m, ""
}

func (s *yamlState) continueIndent(m string) { s.nextIndent += utf8.RuneCountInString(m) }

func (s *yamlState) setIndent(m string, implicit bool) {
	if s.indent() < s.nextIndent {
		s.indentStack = append(s.indentStack, s.nextIndent)
	}
	if !implicit {
		s.nextIndent += utf8.RuneCountInString(m)
	}
}

func init() {
	registerRouge("yaml", func() *rlexer {
		const plainScalarStart = "[^ \\t\\n\\r\\f\\v?:,\\[\\]{}#&*!\\|>'\"%@`]"
		l := &rlexer{tag: "yaml"}
		l.start = func(c *rctx) { yst(c).reset() }
		l.state("basic", rule(`#.*$`, "c1"))
		l.state("root",
			mixin("basic"),
			rule(`\n+`, ""),
			rule(`[ ]+(?=#|$)`, ""),
			ruleF(`^%YAML\b`, func(c *rctx) { c.token("nt"); yst(c).reset(); c.push("yaml_directive") }),
			ruleF(`^%TAG\b`, func(c *rctx) { c.token("nt"); yst(c).reset(); c.push("tag_directive") }),
			ruleF(`^(?:---|\.\.\.)(?= |$)`, func(c *rctx) { c.token("nn"); yst(c).reset(); c.push("block_line") }),
			ruleF(`[ ]*(?!\s|$)`, func(c *rctx) {
				text, err := yst(c).saveIndent(c.m.String())
				c.token("", text)
				c.token("err", err)
				c.push("block_line")
				c.push("indentation")
			}),
		)
		l.state("indentation",
			ruleF(`\s*?\n`, func(c *rctx) { c.token(""); c.pop(2) }),
			ruleF(`[ ]+(?=[-:?](?:[ ]|$))`, func(c *rctx) { c.token(""); yst(c).continueIndent(c.m.String()) }),
			ruleF(`[?:-](?=[ ]|$)`, func(c *rctx) { yst(c).setIndent(c.m.String(), false); c.token("pi") }),
			ruleF(`[ ]*`, func(c *rctx) { c.token(""); yst(c).continueIndent(c.m.String()); c.pop() }),
		)
		l.state("block_line",
			rule(`[ ]*(?=#|$)`, "", "#pop"),
			rule(`[ ]+`, ""),
			mixin("descriptors"),
			mixin("block_nodes"),
			mixin("flow_nodes"),
			ruleF(`(?=`+plainScalarStart+`|[?:-][^ \t\n\r\f\v])`, func(c *rctx) {
				c.token("nv")
				c.push("plain_scalar_in_block_context")
			}),
		)
		l.state("descriptors",
			rule(`!<[0-9A-Za-z;\/?:@&=+$,_.!~*'()\[\]%-]+>`, "kt"),
			rule(`(?:![\w-]+)?!(?:[\w;/?:@&=+$,.!~*\'()\[\]%-]*)`, "kt"),
			rule(`&[\p{L}\p{Nl}\p{Nd}_-]+`, "nl"),
			rule(`\*[\p{L}\p{Nl}\p{Nd}_-]+`, "nv"),
		)
		l.state("block_nodes",
			ruleF(`([^#,?\[\]{}"'\n]+)(:)(?=\s|$)`, func(c *rctx) {
				c.groups("na", "pi")
				yst(c).setIndent(c.m.String(), true)
			}),
			ruleF(`[\|>][+-]?`, func(c *rctx) {
				c.token("pi")
				c.push("block_scalar_content")
				c.push("block_scalar_header")
			}),
		)
		l.state("flow_nodes",
			rule(`\[`, "pi", "flow_sequence"),
			rule(`\{`, "pi", "flow_mapping"),
			rule(`'`, "s1", "single_quoted_scalar"),
			rule(`"`, "s2", "double_quoted_scalar"),
		)
		l.state("flow_collection",
			rule(`(?m)\s+`, ""),
			mixin("basic"),
			rule(`[?:,]`, "pi"),
			mixin("descriptors"),
			mixin("flow_nodes"),
			ruleF(`(?=`+plainScalarStart+`)`, func(c *rctx) { c.push("plain_scalar_in_flow_context") }),
		)
		l.state("flow_sequence",
			rule(`\]`, "pi", "#pop"),
			mixin("flow_collection"),
		)
		l.state("flow_mapping",
			rule(`\}`, "pi", "#pop"),
			mixin("flow_collection"),
		)
		l.state("block_scalar_content",
			rule(`\n+`, ""),
			ruleF(`^[ ]+$`, func(c *rctx) {
				s := yst(c)
				text := []rune(c.m.String())
				mark := s.blockScalarIndent
				if mark < 0 {
					mark = len(text)
				}
				if mark > len(text) {
					mark = len(text)
				}
				c.token("", string(text[:mark]))
				c.token("no", string(text[mark:]))
			}),
			ruleF(`^[ ]*`, func(c *rctx) {
				s := yst(c)
				c.token("")
				size := utf8.RuneCountInString(c.m.String())
				dedent := s.blockScalarIndent
				if dedent < 0 {
					dedent = s.indent()
				}
				if s.blockScalarIndent < 0 {
					s.blockScalarIndent = size
				}
				if size < dedent {
					s.saveIndent(c.m.String())
					c.pop()
					c.push("indentation")
				}
			}),
			rule(`[^\n\r\f\v]+`, "s"),
		)
		l.state("block_scalar_header",
			ruleF(`(([1-9])[+-]?|[+-]?([1-9])?)(?=[ ]|$)`, func(c *rctx) {
				s := yst(c)
				s.blockScalarIndent = -1
				c.gotoState("ignored_line")
				if c.m.String() == "" {
					return
				}
				inc := c.group(2)
				if inc == "" {
					inc = c.group(3)
				}
				if inc != "" {
					n, _ := strconv.Atoi(inc)
					s.blockScalarIndent = s.indent() + n
				}
				c.token("pi")
			}),
		)
		l.state("ignored_line",
			mixin("basic"),
			rule(`[ ]+`, ""),
			rule(`\n`, "", "#pop"),
		)
		l.state("quoted_scalar_whitespaces",
			rule(`^[ ]+`, ""),
			rule(`[ ]+$`, ""),
			rule(`(?m)\n+`, ""),
			rule(`[ ]+`, "nv"),
		)
		l.state("single_quoted_scalar",
			mixin("quoted_scalar_whitespaces"),
			rule(`\\'`, "se"),
			rule(`'`, "s", "#pop"),
			rule(`[^\s']+`, "s"),
		)
		l.state("double_quoted_scalar",
			rule(`"`, "s", "#pop"),
			mixin("quoted_scalar_whitespaces"),
			rule(`\\[0abt\tn\nvfre "\\N_LP]`, "se"),
			rule(`\\(?:x[0-9A-Fa-f]{2}|u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8})`, "se"),
			rule(`[^ \t\n\r\f\v"\\]+`, "s"),
		)
		l.state("plain_scalar_in_block_context_new_line",
			rule(`^[ ]+\n`, ""),
			rule(`(?m)\n+`, ""),
			ruleF(`^(?=---|\.\.\.)`, func(c *rctx) { c.pop(3) }),
			ruleF(`^[ ]*`, func(c *rctx) {
				c.token("")
				c.pop()
				s := yst(c)
				if utf8.RuneCountInString(c.m.String()) <= s.indent() {
					c.pop()
					s.saveIndent(c.m.String())
					c.push("indentation")
				}
			}),
		)
		l.state("plain_scalar_in_block_context",
			rule(`[ ]*(?=:[ \n]|:$)`, "", "#pop"),
			rule(`[ ]*:\S+`, "s"),
			rule(`[ ]+(?=#)`, "", "#pop"),
			rule(`[ ]+$`, ""),
			ruleF(`\n+`, func(c *rctx) { c.token(""); c.push("plain_scalar_in_block_context_new_line") }),
			rule(`[ ]+`, "s"),
			rule(`(true|false|null)\b`, "kc"),
			rule(`\d+(?:\.\d+)?(?=(\r?\n)| +#)`, "m", "#pop"),
			rule(`[^\s:]+`, "s"),
		)
		l.state("plain_scalar_in_flow_context",
			rule(`[ ]*(?=[,:?\[\]{}])`, "", "#pop"),
			rule(`[ ]+(?=#)`, "", "#pop"),
			rule(`^[ ]+`, ""),
			rule(`[ ]+$`, ""),
			rule(`\n+`, ""),
			rule(`[ ]+`, "nv"),
			rule(`[^\s,:?\[\]{}]+`, "nv"),
		)
		l.state("yaml_directive",
			ruleF(`([ ]+)(\d+\.\d+)`, func(c *rctx) { c.groups("", "m"); c.gotoState("ignored_line") }),
		)
		l.state("tag_directive",
			ruleF(`([ ]+)(!|![\w-]*!)([ ]+)(!|!?[\w;/?:@&=+$,.!~*'()\[\]%-]+)`, func(c *rctx) {
				c.groups("", "kt", "", "kt")
				c.gotoState("ignored_line")
			}),
		)
		return l
	})
}
