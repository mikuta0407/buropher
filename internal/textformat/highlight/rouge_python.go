package highlight

import (
	"strings"
)

// Rouge 4.7 の python.rb の移植。

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
		l := &rlexer{tag: "python"}
		l.state("root",
			rule(`(?m)\n+`, ""),
			ruleG(`(?mi)^(:)(\s*)([ru]{,2}""".*?""")`, toks("p", "", "sd")),
			rule(`\.\.\.\B$`, "bp"),
			rule(`[^\S\n]+`, ""),
			rule(`#(.*)?\n?`, "c1"),
			rule(`[\[\]{}:(),;.]`, "p"),
			rule(`\\\n`, ""),
			rule(`\\`, ""),
			rule(`(?i)@`+pyDottedIdent, "nd"),
			rule(`(in|is|and|or|not)\b`, "ow"),
			rule(`(<<|>>|\/\/|\*\*)=?`, "o"),
			rule(`[-~+\/*%=<>&^|@]=?|!=`, "o"),
			ruleG(`(from)((?:\\\s|\s)+)(`+pyDottedIdent+`)((?:\\\s|\s)+)(import)`, toks("kn", "", "n", "", "kn")),
			ruleG(`(import)(\s+)(`+pyDottedIdent+`)`, toks("kn", "", "n")),
			ruleF(`(def)((?:\s|\\\s)+)`, func(c *rctx) { c.groups("k", ""); c.push("funcname") }),
			ruleF(`(class)((?:\s|\\\s)+)`, func(c *rctx) { c.groups("k", ""); c.push("classname") }),
			rule(`(?m)([a-z_]\w*)[ \t]*(?=(\(.*\)))`, "nf"),
			rule(`(?m)([A-Z_]\w*)[ \t]*(?=(\(.*\)))`, "nc"),
			rule("`.*?`", "sb"),
			ruleF(`(?i)([rtfbu]{0,2})('''|"""|['"])`, func(c *rctx) {
				c.groups("sa", "sh")
				r := pyReg(c)
				*r = append(*r, [2]string{strings.ToLower(c.group(1)), c.group(2)})
				c.push("generic_string")
			}),
			mixin("soft_keywords"),
			ruleF(`(?<!\.)`+pyIdent, func(c *rctx) {
				w := c.m.String()
				switch {
				case pyKeywords[w]:
					c.token("k")
				case pyExceptions[w]:
					c.token("nb")
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
		l.state("funcname", rule(pyIdent, "nf", "#pop"))
		l.state("classname", rule(pyIdent, "nc", "#pop"))
		l.state("soft_keywords",
			ruleF(`(?x)
          (^[ \t]*)
          (match|case)\b
          (?![ \t]*
            (?:[:,;=^&|@~)\]}] |
              (?:`+strings.Join(pyKeywordList, "|")+`)\b))
        `, func(c *rctx) {
				c.token("w", c.group(1))
				c.token("k", c.group(2))
				c.push("soft_keywords_inner")
			}),
		)
		l.state("soft_keywords_inner",
			ruleG(`(\s+)([^\n_]*)(_\b)`, toks("w", "", "k")),
			ruleF(``, func(c *rctx) { c.pop() }),
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
			rule(`^\s*(>>>|\.\.\.)\B`, "gp", "doctest"),
			rule(`[^'"\\{]+?`, "s"),
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
