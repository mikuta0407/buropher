// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import (
	"strings"
)

// Rouge 4.7 の markdown.rb / pascal.rb の移植。

// findFancyTokens は Markdown のフェンス内コードを指定言語で字句解析する
// （Lexer.find_fancy 相当。未知の言語は Str::Backtick の PlainText）。
type fenceLexer struct {
	ctx      *rctx
	fallback string // ctx が nil の場合の扱い: "sb"（未知）または "chroma:<tag>"
}

func newFenceLexer(c *rctx, name string) *fenceLexer {
	if name == "" {
		// 推定（guess）は未対応: 検出できない場合と同じく PlainText（Text）
		return &fenceLexer{ctx: c.child(rougeLexerByTag("plaintext"))}
	}
	l := findLexer(name)
	if l == nil {
		return &fenceLexer{fallback: "sb"}
	}
	if lx := rougeLexerByTag(l.Tag); lx != nil {
		return &fenceLexer{ctx: c.child(lx)}
	}
	return &fenceLexer{fallback: "chroma:" + l.Tag}
}

func (f *fenceLexer) lex(c *rctx, text string) {
	switch {
	case f.ctx != nil:
		f.ctx.continueLex(text, c.emit)
	case f.fallback == "sb":
		c.emit("sb", text)
	default:
		tag := strings.TrimPrefix(f.fallback, "chroma:")
		for _, t := range chromaTokens(text, registry[tag]) {
			c.emit(t[0], t[1])
		}
	}
}

func init() {
	registerRouge("markdown", func() *rlexer {
		const edot = `(?:\\.|[^\\\n])`
		l := &rlexer{tag: "markdown"}
		l.start = func(c *rctx) {
			if s := c.sub("html"); s != nil {
				s.reset()
			}
		}
		l.state("root",
			ruleF(`(?m)\A(---\s*\n.*?\n?)^(---\s*$\n?)`, func(c *rctx) { c.delegateFresh("yaml", c.m.String()) }),
			rule(`\\.`, "se"),
			rule(`^[\S ]+\n(?:---*)\n`, "gh"),
			rule(`^[\S ]+\n(?:===*)\n`, "gu"),
			rule(`^#(?=[^#]).*?$`, "gh"),
			rule(`^##*.*?$`, "gu"),
			ruleF(`(?m)^([ \t]*)(`+"`"+`{3,}|~{3,})([^\n]*\n)((.*?)(\n\1)(\2))?`, func(c *rctx) {
				m1, m2, m3 := c.group(1), c.group(2), c.group(3)
				name := strings.Trim(m3, " \t\n\v\f\r\x00")
				fl := newFenceLexer(c, name)
				if fl.ctx != nil {
					fl.ctx.reset()
				}
				c.token("", m1)
				c.token("p", m2)
				c.token("nl", m3)
				g5 := c.m.GroupByNumber(5)
				if g5 != nil && len(g5.Captures) > 0 {
					fl.lex(c, g5.String())
				}
				c.token("", c.group(6))
				g7 := c.m.GroupByNumber(7)
				if g7 != nil && len(g7.Captures) > 0 {
					c.token("p", g7.String())
					return
				}
				st := &rstate{name: "fence:" + m2}
				st.rules = []rrule{
					ruleF(`^([ \t]*)(`+m2+`)`, func(c *rctx) {
						c.pop()
						c.token("", c.group(1))
						c.token("p", c.group(2))
					}),
					ruleF(`^.*\n`, func(c *rctx) {
						// Rouge は mb[1]（存在しないグループ = nil）を渡すため、マッチ全体が delegate される
						fl.lex(c, c.m.String())
					}),
				}
				c.stack = append(c.stack, st)
			}),
			rule(`\n\n((    |\t).*?\n|\n)+`, "sb"),
			rule("(`+)(?:"+edot+`|\n)+?\1`, "sb"),
			rule(`^(\s*[*]){3,}\s*$`, "p"),
			rule(`^(\s*[-]){3,}\s*$`, "p"),
			rule(`^\s*[*+-](?=\s)`, "p"),
			rule(`^\s*\d+\.`, "p"),
			rule(`^\s*>.*?$`, "gt"),
			ruleF(`^(\s*)(\[)(`+edot+`+?)(\])(\s*)(:)`, func(c *rctx) {
				c.groups("", "p", "ss", "p", "", "p")
				c.push("title")
				c.push("url")
			}),
			ruleF(`(!?\[)(`+edot+`*?|[^\]]*?)(\])(?=[\[(])`, func(c *rctx) {
				c.groups("p", "nv", "p")
				c.push("link")
			}),
			rule(`[*]{2}[^* \n][^*\n]*[*]{2}`, "gs"),
			rule(`[*]{3}[^* \n][^*\n]*[*]{3}`, "ges"),
			rule(`__`+edot+`*?__`, "gs"),
			rule(`[*]`+edot+`*?[*]`, "ge"),
			rule(`_`+edot+`*?_`, "ge"),
			rule(`<.*?@.+[.].+>`, "nv"),
			rule(`<(https?|mailto|ftp)://`+edot+`*?>`, "nv"),
			rule("[^\\\\`\\[*\\n&<]+", ""),
			ruleF(`&\S*;`, func(c *rctx) { c.delegate("html") }),
			ruleF(`<`+edot+`*?>`, func(c *rctx) { c.delegate("html") }),
			rule(`[&<]`, ""),
			rule(`\[`, ""),
			rule(`\n`, ""),
		)
		l.state("link",
			ruleF(`(\[)(`+edot+`*?)(\])`, func(c *rctx) { c.groups("p", "ss", "p"); c.pop() }),
			ruleF(`[(]`, func(c *rctx) { c.token("p"); c.push("inline_title"); c.push("inline_url") }),
			rule(`[ \t]+`, ""),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("url",
			rule(`[ \t]+`, ""),
			ruleF(`(<)(`+edot+`*?)(>)`, func(c *rctx) { c.groups("nt", "sx", "nt"); c.pop() }),
			rule(`\S+`, "sx", "#pop"),
		)
		l.state("title",
			rule(`"`+edot+`*?"`, "nn"),
			rule(`'`+edot+`*?'`, "nn"),
			rule(`[(]`+edot+`*?[)]`, "nn"),
			rule(`\s*(?=["'()])`, ""),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("inline_title",
			rule(`[)]`, "p", "#pop"),
			mixin("title"),
		)
		l.state("inline_url",
			rule(`[^<\s)]+`, "sx", "#pop"),
			rule(`(?m)\s+`, ""),
			mixin("url"),
		)
		return l
	})

	registerRouge("pascal", func() *rlexer {
		const (
			id           = `(?i:@?[_a-z]\w*)`
			keywords     = `absolute|abstract|all|and|and_then|array|as|asm|assembler|attribute|begin|bindable|case|class|const|constructor|delay|destructor|div|do|downto|else|end|except|exit|export|exports|external|far|file|finalization|finally|for|forward|function|goto|if|implementation|import|in|inc|index|inherited|initialization|inline|interface|interrupt|is|label|library|message|mod|module|near|nil|not|object|of|on|only|operator|or|or_else|otherwise|out|overload|override|packed|pascal|pow|private|procedure|program|property|protected|public|published|qualified|raise|read|record|register|repeat|resident|resourcestring|restricted|safecall|segment|set|shl|shr|stdcall|stored|string|then|threadvar|to|try|type|unit|until|uses|value|var|view|virtual|while|with|write|writeln|xor`
			keywordsType = `ansichar|ansistring|bool|boolean|byte|bytebool|cardinal|char|comp|currency|double|dword|extended|int64|integer|iunknown|longbool|longint|longword|pansichar|pansistring|pbool|pboolean|pbyte|pbytearray|pcardinal|pchar|pcomp|pcurrency|pdate|pdatetime|pdouble|pdword|pextended|phandle|pint64|pinteger|plongint|plongword|pointer|ppointer|pshortint|pshortstring|psingle|psmallint|pstring|pvariant|pwidechar|pwidestring|pword|pwordarray|pwordbool|real|real48|shortint|shortstring|single|smallint|string|tclass|tdate|tdatetime|textfile|thandle|tobject|ttime|variant|widechar|widestring|word|wordbool`
		)
		l := &rlexer{tag: "pascal"}
		l.state("whitespace",
			rule(`(?m)\s+`, ""),
			rule(`(//).*$\n?`, "c1"),
			rule(`(--).*$\n?`, "c1"),
			rule(`(?m)\(\*.*?\*\)`, "cm"),
			rule(`(?m)\{.*?\}`, "cm"),
		)
		l.state("root",
			mixin("whitespace"),
			rule(`((0(x|X)[0-9a-fA-F]*)|(([0-9]+\.?[0-9]*)|(\.[0-9]+))((e|E)(\+|-)?[0-9]+)?)(L|l|UL|ul|u|U|F|f|ll|LL|ull|ULL)?`, "m"),
			rule(`\$[0-9A-Fa-f]+`, "mh"),
			rule("[~!@#\\$%\\^&\\*\\(\\)\\+`\\-={}\\[\\]:;<>\\?,\\.\\/\\|\\\\]", "p"),
			rule(`'([^']|'')*'`, "s"),
			rule(`(?i)(true|false|nil)\b`, "nb"),
			rule(`(?i)\b(`+keywords+`)\b`, "k"),
			rule(`(?i)\b(`+keywordsType+`)\b`, "kt"),
			rule(id, "n"),
		)
		return l
	})
}
