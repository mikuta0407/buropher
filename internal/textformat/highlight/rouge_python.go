// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import (
	"strings"
)

// Rouge 5.1 の python.rb の移植。

var (
	pyKeywordList = strings.Fields(`assert break continue del elif else except exec
finally for global if lambda pass print raise
return try while yield as with from import
async await nonlocal`)
	pyKeywords = wordset(strings.Join(pyKeywordList, " "))
	pyBuiltins = wordset(`__import__ abs aiter all anext any apply ascii
basestring bin bool buffer breakpoint bytearray bytes
callable chr classmethod cmp coerce compile complex
delattr dict dir divmod enumerate eval exec execfile exit
file filter float format frozenset getattr globals
hasattr hash help hex
id input int intern isinstance issubclass iter len list locals long
map max memoryview min next object oct open ord pow print property
range raw_input reduce reload repr reversed round set setattr slice
sorted staticmethod str sum super tuple type unichr unicode vars
xrange zip`)
	pyBuiltinsPseudo = wordset(`None Ellipsis NotImplemented False True`)
	pyExceptions     = wordset(`ArithmeticError AssertionError AttributeError BaseException
BaseExceptionGroup BlockingIOError BrokenPipeError BufferError
BytesWarning ChildProcessError ConnectionAbortedError ConnectionError
ConnectionRefusedError ConnectionResetError DeprecationWarning
EOFError EnvironmentError EncodingWarning Exception ExceptionGroup
FileExistsError FileNotFoundError FloatingPointError FutureWarning
GeneratorExit IOError ImportError ImportWarning IndentationError
IndexError InterruptedError IsADirectoryError
KeyError KeyboardInterrupt LookupError
MemoryError ModuleNotFoundError
NameError NotADirectoryError NotImplemented NotImplementedError
OSError OverflowError OverflowWarning PendingDeprecationWarning
PermissionError ProcessLookupError PythonFinalizationError
RecursionError ReferenceError ResourceWarning RuntimeError RuntimeWarning
StandardError StopAsyncIteration StopIteration SyntaxError SyntaxWarning
SystemError SystemExit TabError TimeoutError TypeError
UnboundLocalError UnicodeDecodeError UnicodeEncodeError UnicodeError
UnicodeTranslateError UnicodeWarning UserWarning ValueError VMSError
Warning WindowsError
ZeroDivisionError`)
)

const (
	pyIdent       = `[[:alpha:]_][[:alnum:]_]*`
	pyDottedIdent = `[[:alpha:]_.][[:alnum:]_.]*`
	pyDigits      = `[0-9](_?[0-9])*`
	pyDecimal     = `((` + pyDigits + `)?\.` + pyDigits + `|` + pyDigits + `\.)`
	pyExponent    = `e[+-]?` + pyDigits
)

// pyStrings は Python レキサーの StringRegister（[種別, 区切り] のスタック）。
type pyStrings [][2]string

func pyReg(c *rctx) *pyStrings {
	r, _ := c.vars["strings"].(*pyStrings)
	if r == nil {
		r = &pyStrings{}
		c.vars["strings"] = r
	}
	return r
}

func (r *pyStrings) last() [2]string {
	if len(*r) == 0 {
		return [2]string{"", ""}
	}
	return (*r)[len(*r)-1]
}

func init() {
	registerRouge("python", func() *rlexer {
		const (
			// Ruby で埋め込まれる Regexp は (?-mix:...) のグループになるため (?:...) で囲む
			inlineWS      = `(?:(?:[ \t]|\\\n)*?)`
			inlineContent = `(?:(?:[^\\\n]|\\[\n.])*?)`
			operatorWords = `(in|is|and|or|not)\b`
			operators     = `(<<|>>|//|[*][*])=?|!=|[-~+\/*%=<>&^|@]=?|!=`
			inlineOps     = `(?:(?:` + operatorWords + `)|if\b|(?:` + operators + `))`
		)
		l := &rlexer{tag: "python"}
		l.start = func(c *rctx) { c.push("newline") }
		l.state("inline_whitespace",
			rule(`[ \t]+`, ""),
			rule(`\\\n`, "se"),
		)
		l.state("root",
			rule(`(?m)\n+`, "", "newline"),
			ruleG(`(?mi)^(:)(\s*)([ru]{,2}""".*?""")`, toks("p", "", "sd")),
			rule(`\.\.\.\B$`, "bp"),
			mixin("inline_whitespace"),
			rule(`#(.*)?\n?`, "c1", "newline"),
			rule(`[\[\]{}:(),;]`, "p"),
			rule(`[.]`, "p", "post_dot"),
			rule(`\\`, "se"),
			rule(`(?i)@`+pyDottedIdent, "nd"),
			rule(operatorWords, "ow"),
			rule(operators, "o"),
			rule(`def\b`, "k", "funcname"),
			rule(`class\b`, "k", "classname"),
			rule("`.*?`", "sb"),
			ruleF(`(?i)([rtfbu]{0,2})('''|"""|['"])`, func(c *rctx) {
				c.groups("sa", "sh")
				r := pyReg(c)
				*r = append(*r, [2]string{strings.ToLower(c.group(1)), c.group(2)})
				c.push("generic_string")
			}),
			ruleF(`(?<!\.)`+pyIdent, func(c *rctx) {
				w := c.m.String()
				switch {
				case pyKeywords[w]:
					c.token("k")
				case pyExceptions[w]:
					c.token("ne")
				case pyBuiltins[w]:
					c.token("nb")
				case pyBuiltinsPseudo[w]:
					c.token("bp")
				default:
					c.token("n")
				}
			}),
			rule(pyIdent, "n"),
			rule(`(?i)`+pyDecimal+`(`+pyExponent+`)?j?`, "mf"),
			rule(`(?i)`+pyDigits+pyExponent+`j?`, "mf"),
			rule(`(?i)`+pyDigits+`j`, "mf"),
			rule(`(?i)0b(_?[0-1])+`, "mb"),
			rule(`(?i)0o(_?[0-7])+`, "mo"),
			rule(`(?i)0x(_?[a-f0-9])+`, "mh"),
			rule(`\d+L`, "il"),
			rule(`([1-9](_?[0-9])*|0(_?0)*)`, "mi"),
		)
		popEmpty := ruleF(``, func(c *rctx) { c.pop() })
		l.state("import",
			mixin("inline_whitespace"),
			rule(pyDottedIdent, "nn", "#pop"),
			popEmpty,
		)
		l.state("from",
			mixin("inline_whitespace"),
			ruleF(pyDottedIdent, func(c *rctx) { c.token("nn"); c.gotoState("from_import") }),
			popEmpty,
		)
		// from の後の import（import 状態には入らない）
		l.state("from_import",
			mixin("inline_whitespace"),
			rule(`import\b`, "kn", "#pop"),
			popEmpty,
		)
		l.state("post_dot",
			mixin("inline_whitespace"),
			rule(`(?m)([A-Z]\w*)(?=`+inlineWS+`[(])`, "nc"),
			rule(`(?m)(`+pyIdent+`)(?=`+inlineWS+`[(])`, "nf"),
			popEmpty,
		)
		l.state("newline",
			mixin("inline_whitespace"),
			rule(`from\b`, "kn", "from"),
			rule(`import\b`, "kn", "import"),
			// ソフトキーワード（match / case）の判定は Rouge と同じく先読みによる近似
			rule(`(?:case|match)(?=`+inlineWS+inlineOps+`)`, "nx", "#pop"),
			ruleF(`(?:case|match)(?=`+inlineContent+`:`+inlineWS+`[#\n])`, func(c *rctx) {
				c.token("k")
				if c.m.String() == "case" {
					c.gotoState("case_pattern")
				} else {
					c.pop()
				}
			}),
			popEmpty,
		)
		l.state("funcname", mixin("inline_whitespace"), rule(pyIdent, "nf", "#pop"))
		l.state("classname", mixin("inline_whitespace"), rule(pyIdent, "nc", "#pop"))
		l.state("case_pattern",
			ruleF(`\n`, func(c *rctx) { c.token(""); c.gotoState("newline") }),
			rule(`_\b`, "k"),
			mixin("root"),
		)
		l.state("raise",
			rule(`from\b`, "k"),
			rule(`raise\b`, "k"),
			rule(`yield\b`, "k"),
			rule(`\n`, "", "#pop"),
			rule(`;`, "p", "#pop"),
			mixin("root"),
		)
		l.state("yield", mixin("raise"))
		l.state("generic_string",
			rule(`\n`, "s", "generic_string_newline"),
			rule(`[^'"\\{\n]+`, "s"),
			rule(`{{`, "s"),
			ruleF(`'''|"""|['"]`, func(c *rctx) {
				c.token("sh")
				r := pyReg(c)
				if r.last()[1] == c.m.String() {
					*r = (*r)[:len(*r)-1]
					c.pop()
				}
			}),
			rule(`(?=\\)`, "s", "generic_escape"),
			ruleF(`{`, func(c *rctx) {
				if strings.Contains(pyReg(c).last()[0], "f") {
					c.token("si")
					c.push("generic_interpol")
				} else {
					c.token("s")
				}
			}),
		)
		l.state("generic_string_newline",
			rule(`[ \t]+`, "s"),
			ruleF(`(>>>|\.\.\.)\B`, func(c *rctx) { c.token("gp"); c.gotoState("doctest") }),
			popEmpty,
		)
		l.state("generic_escape",
			ruleF(`(?x)\\
          ( [\\abfnrtv"']
          | \n
          | newline
          | N{[a-zA-Z][a-zA-Z ]+[a-zA-Z]}
          | u[a-fA-F0-9]{4}
          | U[a-fA-F0-9]{8}
          | x[a-fA-F0-9]{2}
          | [0-7]{1,3}
          )
        `, func(c *rctx) {
				if strings.Contains(pyReg(c).last()[0], "r") {
					c.token("s")
				} else {
					c.token("se")
				}
				c.pop()
			}),
			rule(`\\.`, "s", "#pop"),
		)
		l.state("doctest",
			rule(`\n\n`, "", "#pop"),
			ruleF(`'''|"""`, func(c *rctx) {
				c.token("sh")
				if c.inState("generic_string") {
					c.pop(2)
				}
			}),
			mixin("root"),
		)
		l.state("generic_interpol",
			ruleF(`[^{}!:]+`, func(c *rctx) { c.recurse(c.m.String()) }),
			rule(`![asr]`, "si"),
			rule(`:`, "si"),
			rule(`{`, "si", "generic_interpol"),
			rule(`}`, "si", "#pop"),
		)
		return l
	})
}
