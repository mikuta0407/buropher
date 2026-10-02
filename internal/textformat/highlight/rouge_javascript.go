package highlight

// Rouge 4.7 の javascript.rb の移植。

var (
	jsKeywords = wordset(`async await break case catch continue debugger default delete
do else export finally from for if import in instanceof new of
return super switch this throw try typeof void while yield`)
	jsDeclarations = wordset(`var let const with function class
extends constructor get set static`)
	jsReserved  = wordset(`enum implements interface package private protected public`)
	jsConstants = wordset(`true false null NaN Infinity undefined`)
	jsBuiltins  = wordset(`Array Boolean Date Error Function Math netscape
Number Object Packages RegExp String sun decodeURI
decodeURIComponent encodeURI encodeURIComponent
Error eval isFinite isNaN parseFloat parseInt
document window navigator self global
Promise Set Map WeakSet WeakMap Symbol Proxy Reflect
Int8Array Uint8Array Uint8ClampedArray
Int16Array Uint16Array Uint16ClampedArray
Int32Array Uint32Array Uint32ClampedArray
Float32Array Float64Array DataView ArrayBuffer`)
)

// pWord は Ruby の \p{Word}。
const pWord = `\p{L}\p{M}\p{Nd}\p{Pc}`

const jsID = `[\p{L}\p{Nl}$_][` + pWord + `]*`

func buildJavascript(tag string) *rlexer {
	l := &rlexer{tag: tag}
	l.state("multiline_comment",
		rule(`[*]/`, "cm", "#pop"),
		rule(`[^*/]+`, "cm"),
		rule(`[*/]`, "cm"),
	)
	l.state("comments_and_whitespace",
		rule(`\s+`, ""),
		rule(`<!--`, "c"),
		rule(`//.*?$`, "c1"),
		rule(`/[*]`, "cm", "multiline_comment"),
	)
	l.state("expr_start",
		mixin("comments_and_whitespace"),
		ruleF(`/`, func(c *rctx) { c.token("sr"); c.gotoState("regex") }),
		ruleF(`[{]`, func(c *rctx) { c.token("p"); c.gotoState("object") }),
		rule(``, "", "#pop"),
	)
	l.state("regex",
		ruleF(`/`, func(c *rctx) { c.token("sr"); c.gotoState("regex_end") }),
		rule(`[^/]\n`, "err", "#pop"),
		rule(`\n`, "err", "#pop"),
		rule(`\[\^`, "se", "regex_group"),
		rule(`\[`, "se", "regex_group"),
		rule(`\\.`, "se"),
		rule(`[(][?][:=<!]`, "se"),
		rule(`[{][\d,]+[}]`, "se"),
		rule(`[()?]`, "se"),
		rule(`.`, "sr"),
	)
	l.state("regex_end",
		rule(`[gimuy]+`, "sr", "#pop"),
		ruleF(``, func(c *rctx) { c.pop() }),
	)
	l.state("regex_group",
		rule(`/`, "se"),
		ruleF(`[^/]\n`, func(c *rctx) { c.token("err"); c.pop(2) }),
		rule(`\]`, "se", "#pop"),
		rule(`\\.`, "se"),
		rule(`.`, "sr"),
	)
	l.state("bad_regex",
		rule(`[^\n]+`, "err", "#pop"),
	)
	l.state("root",
		rule(`(?m)\A\s*#!.*?\n`, "cp", "statement"),
		rule(`(?<=\n)(?=\s|/|<!--)`, "", "expr_start"),
		mixin("comments_and_whitespace"),
		rule(`(?x)\+\+ | -- | ~ | \?\?=? | && | \|\| | \\(?=\n) | << | >>>? | ===
               | !== `, "o", "expr_start"),
		rule(`[-<>+*%&|\^/!=]=?`, "o", "expr_start"),
		rule(`[(\[,]`, "p", "expr_start"),
		rule(`;`, "p", "statement"),
		rule(`[)\].]`, "p"),
		ruleF("`", func(c *rctx) { c.token("s2"); c.push("template_string") }),
		rule(`[?][.]`, "p"),
		ruleF(`[?]`, func(c *rctx) { c.token("p"); c.push("ternary"); c.push("expr_start") }),
		ruleF(`(\@)(\w+)?`, func(c *rctx) { c.groups("p", "nd"); c.push("expr_start") }),
		ruleF(`(class)((?:\s|\\\s)+)`, func(c *rctx) { c.groups("kd", ""); c.push("classname") }),
		rule(`(?m)([\p{Nl}$_]*\p{Lu}[`+pWord+`]*)[ \t]*(?=(\(.*\)))`, "nc"),
		ruleG(`(function)((?:\s|\\\s)+)(`+jsID+`)`, toks("kd", "", "nf")),
		rule(`function(?=(\(.*\)))`, "kd"),
		ruleF(`(?m)(#?`+jsID+`)[ \t]*(?=(\(.*\)))`, func(c *rctx) {
			if jsKeywords[c.group(1)] {
				c.token("k")
			} else {
				c.token("nf")
			}
		}),
		rule(`[{}]`, "p", "statement"),
		ruleF(`#?`+jsID, func(c *rctx) {
			w := c.m.String()
			switch {
			case jsKeywords[w]:
				c.token("k")
				c.push("expr_start")
			case jsDeclarations[w]:
				c.token("kd")
				c.push("expr_start")
			case jsReserved[w]:
				c.token("kr")
			case jsConstants[w]:
				c.token("kc")
			case jsBuiltins[w]:
				c.token("nb")
			default:
				c.token("nx")
			}
		}),
		rule(`[0-9][0-9]*\.[0-9]+([eE][0-9]+)?[fd]?`, "mf"),
		rule(`(?i)0x[0-9a-fA-F]+`, "mh"),
		rule(`(?i)0o[0-7][0-7_]*`, "mo"),
		rule(`(?i)0b[01][01_]*`, "mb"),
		rule(`[0-9]+`, "mi"),
		rule(`"`, "dl", "dq"),
		rule(`'`, "dl", "sq"),
		rule(`:`, "p"),
	)
	l.state("dq",
		rule(`\\[\\nrt"]?`, "se"),
		rule(`[^\\"]+`, "s2"),
		rule(`"`, "dl", "#pop"),
	)
	l.state("sq",
		rule(`\\[\\nrt']?`, "se"),
		rule(`[^\\']+`, "s1"),
		rule(`'`, "dl", "#pop"),
	)
	l.state("classname",
		ruleG(`(`+jsID+`)((?:\s|\\\s)+)(extends)((?:\s|\\\s)+)`, toks("nc", "", "kd", "")),
		rule(jsID, "nc", "#pop"),
	)
	l.state("statement",
		ruleF(`case\b`, func(c *rctx) { c.token("k"); c.gotoState("expr_start") }),
		ruleG(`(`+jsID+`)(\s*)(:)`, toks("nl", "", "p")),
		mixin("expr_start"),
	)
	l.state("object",
		mixin("comments_and_whitespace"),
		ruleF(`[{]`, func(c *rctx) { c.token("p"); c.push() }),
		ruleF(`[}]`, func(c *rctx) { c.token("p"); c.gotoState("statement") }),
		ruleF(`(`+jsID+`)(\s*)(:)`, func(c *rctx) { c.groups("na", "", "p"); c.push("expr_start") }),
		rule(`:`, "p"),
		mixin("root"),
	)
	l.state("ternary",
		ruleF(`:`, func(c *rctx) { c.token("p"); c.gotoState("expr_start") }),
		mixin("root"),
	)
	l.state("template_string",
		rule(`[$]{`, "p", "template_string_expr"),
		rule("`", "s2", "#pop"),
		rule("\\\\[$`\\\\]", "se"),
		rule("[^$`\\\\]+", "s2"),
		rule(`[\\$]`, "s2"),
	)
	l.state("template_string_expr",
		rule(`}`, "p", "#pop"),
		mixin("root"),
	)
	return l
}

func init() {
	registerRouge("javascript", func() *rlexer { return buildJavascript("javascript") })
}
