package highlight

import "strings"

// Rouge 4.7 の kotlin.rb / nginx.rb の移植。

func init() {
	registerRouge("kotlin", func() *rlexer {
		keywords := strings.Join(strings.Fields(`abstract annotation as break by catch class companion const
constructor continue crossinline do dynamic else enum
external false final finally for fun get if import in infix
inline inner interface internal is lateinit noinline null
object open operator out override package private protected
public reified return sealed set super suspend tailrec this
throw true try typealias typeof val var vararg when where
while yield`), "|")
		const (
			nameChars = `(?:[-\p{Lu}\p{Ll}\p{Lt}\p{Lm}\p{Nl}\p{Nd}\p{Pc}\p{Cf}\p{Mn}\p{Mc}]*)`
			className = "(?:`?[\\p{Lu}]" + nameChars + "`?)"
			name      = "(?:`?[_\\p{Lu}\\p{Ll}\\p{Lt}\\p{Lm}\\p{Nl}]" + nameChars + "`?)"
			decDigits = `(?:([0-9][0-9_]*[0-9])|[0-9])`
			exponent  = `(?:[eE][+-]?(` + decDigits + `))`
			double    = `(?:((` + decDigits + `)?\.` + decDigits + `(` + exponent + `)?)|(` + decDigits + exponent + `))`
		)
		l := &rlexer{tag: "kotlin"}
		l.state("root",
			ruleG(`\b(companion)(\s+)(object)\b`, toks("k", "", "k")),
			ruleF(`\b(class|data\s+class|interface|object)(\s+)`, func(c *rctx) { c.groups("kd", ""); c.push("class") }),
			ruleF(`\b(fun)(\s+)`, func(c *rctx) { c.groups("k", ""); c.push("function") }),
			ruleF(`\b(package|import)(\s+)`, func(c *rctx) { c.groups("k", ""); c.push("package") }),
			ruleF(`\b(val|var)(\s+)(\()`, func(c *rctx) { c.groups("kd", "", "p"); c.push("destructure") }),
			ruleF(`\b(val|var)(\s+)`, func(c *rctx) { c.groups("kd", ""); c.push("property") }),
			ruleG(`(return|continue|break|this|super)(@`+name+`)?\b`, toks("k", "nd")),
			rule(`\bfun\b`, "k"),
			rule(`\b(?:`+keywords+`)\b`, "k"),
			rule(`^\s*\[.*?\]`, "na"),
			rule(`[^\S\n]+`, ""),
			rule(`\\\n`, ""),
			rule(`//.*?$`, "c1"),
			rule(`/[*].*[*]/`, "cm"),
			rule(`/[*].*`, "cm", "comment"),
			rule(`\n`, ""),
			ruleG(`(::)(class)`, toks("o", "k")),
			rule(`::|!!|\?[:.]`, "o"),
			rule(`(\.\.)`, "o"),
			rule(`(`+double+`[fF]?)|(`+decDigits+`[fF])`, "mf"),
			rule(`0[bB]([01][01_]*[01]|[01])[uU]?L?`, "mb"),
			rule(`0[xX]([0-9a-fA-F][0-9a-fA-F_]*[0-9a-fA-F]|[0-9a-fA-F])[uU]?L?`, "mh"),
			rule(`(([1-9][0-9_]*[0-9])|[0-9])[uU]?L?`, "mi"),
			rule(`[~!%^&*()+=|\[\]:;,.<>/?-]`, "p"),
			rule(`[{}]`, "p"),
			rule(`(?m)@"(""|[^"])*"`, "s"),
			rule(`(?m)""".*?"""`, "s"),
			rule(`(?m)"(\\\\|\\"|[^"\n])*["\n]`, "s"),
			rule(`'\\.'|'[^\\]'`, "sc"),
			rule(`(@`+className+`)`, "nd"),
			ruleF(`(`+className+`)(<)`, func(c *rctx) { c.groups("nc", "p"); c.push("generic_parameters") }),
			rule(className, "nc"),
			rule(`(`+name+`)(?=\s*[({])`, "nf"),
			rule(`(`+name+`)@`, "nd"),
			rule(name, "n"),
		)
		l.state("package", rule(`\S+`, "nn", "#pop"))
		l.state("class", rule(className, "nc", "#pop"))
		l.state("function",
			rule(`(<)`, "p", "generic_parameters"),
			rule(`(\s+)`, ""),
			ruleG(`(`+className+`)(\.)`, toks("nc", "p")),
			rule(name, "nf", "#pop"),
		)
		l.state("generic_parameters",
			rule(className, "nc"),
			rule(`(<)`, "p", "generic_parameters"),
			rule(`(reified|out|in)`, "k"),
			rule(`([,:.?])`, "p"),
			rule(`(\s+)`, ""),
			rule(`(>)`, "p", "#pop"),
		)
		l.state("property",
			rule(`(<)`, "p", "generic_parameters"),
			rule(`(\s+)`, ""),
			rule(name, "py", "#pop"),
		)
		l.state("destructure",
			rule(`(,)`, "p"),
			rule(`(\))`, "p", "#pop"),
			rule(`(\s+)`, ""),
			rule(name, "py"),
		)
		l.state("comment",
			rule(`/[*]`, "cm", "comment"),
			rule(`[*]/`, "cm", "#pop"),
			rule(`[^/*]+`, "cm"),
			rule(`[/*]`, "cm"),
		)
		return l
	})

	registerRouge("nginx", func() *rlexer {
		const id = `[^\s$;{}()#]+`
		l := &rlexer{tag: "nginx"}
		l.state("root",
			ruleG(`(include)(\s+)([^\s;]+)`, toks("k", "", "n")),
			rule(id, "k", "statement"),
			mixin("base"),
		)
		l.state("block",
			rule(`}`, "p", "#pop"),
			rule(id, "kn", "statement"),
			mixin("base"),
		)
		l.state("statement",
			ruleF(`{`, func(c *rctx) { c.token("p"); c.pop(); c.push("block") }),
			rule(`;`, "p", "#pop"),
			mixin("base"),
		)
		l.state("base",
			rule(`\s+`, ""),
			rule(`#.*`, "c1"),
			rule(`(?:on|off)\b`, "no"),
			rule(`[$][\w-]+`, "nv"),
			ruleG(`(?i)([a-z0-9.-]+)(:)([0-9]+)`, toks("nf", "p", "mi")),
			rule(`(?i)[a-z-]+/[a-z-]+`, "nc"),
			rule(`\d+\.\d+`, "mf"),
			rule(`(?i)[0-9]+[kmg]?\b`, "mi"),
			ruleG(`(~)(\s*)([^\s{]+)`, toks("p", "", "sr")),
			rule(`[:=~]`, "p"),
			rule(`/(?:`+id+`)?`, "n"),
			rule(`[^#\s;{}$\\]+`, "s"),
			rule(`[$;]`, ""),
		)
		return l
	})
}
