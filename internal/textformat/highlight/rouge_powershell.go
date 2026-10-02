package highlight

import "strings"

// Rouge 4.7 の powershell.rb の移植。

var psAttributes = wordset(`ConfirmImpact DefaultParameterSetName HelpURI PositionalBinding
SupportsPaging SupportsShouldProcess`)

func init() {
	registerRouge("powershell", func() *rlexer {
		join := func(s string) string { return strings.Join(strings.Fields(s), "|") }
		autoVars := join(`\$\$ \$\? \$\^ \$_
\$args \$ConsoleFileName \$Error \$Event \$EventArgs \$EventSubscriber
\$ExecutionContext \$false \$foreach \$HOME \$Host \$input \$IsCoreCLR
\$IsLinux \$IsMacOS \$IsWindows \$LastExitCode \$Matches \$MyInvocation
\$NestedPromptLevel \$null \$PID \$PROFILE \$PSBoundParameters \$PSCmdlet
\$PSCommandPath \$PSCulture \$PSDebugContext \$PSHOME \$PSItem
\$PSScriptRoot \$PSSenderInfo \$PSUICulture \$PSVersionTable \$PWD
\$REPORTERRORSHOWEXCEPTIONCLASS \$REPORTERRORSHOWINNEREXCEPTION
\$REPORTERRORSHOWSOURCE \$REPORTERRORSHOWSTACKTRACE
\$SENDER \$ShellId \$StackTrace \$switch \$this \$true`)
		keywords := join(`assembly exit process base filter public begin finally return break for
sequence catch foreach static class from switch command function throw
configuration hidden trap continue if try data in type define
inlinescript until do interface using dynamicparam module var else
namespace while elseif parallel workflow end param enum private`)
		operators := join(`-split -isplit -csplit -join -is -isnot -as -eq -ieq -ceq -ne -ine -cne
-gt -igt -cgt -ge -ige -cge -lt -ilt -clt -le -ile -cle -like -ilike
-clike -notlike -inotlike -cnotlike -match -imatch -cmatch -notmatch
-inotmatch -cnotmatch -contains -icontains -ccontains -notcontains
-inotcontains -cnotcontains -replace -ireplace -creplace -shl -shr -band
-bor -bxor -and -or -xor -not \+= -= \*= \/= %=`)
		multiline := join(`synopsis description parameter example inputs outputs notes link
component role functionality forwardhelptargetname forwardhelpcategory
remotehelprunspace externalhelp`)
		l := &rlexer{tag: "powershell"}
		l.state("variable",
			rule(autoVars, "bp"),
			ruleG("(\\$)(?:(\\w+)(:))?(\\w+|\\{(?:[^`]|`.)+?\\})", toks("nv", "nn", "p", "nv")),
			rule(`\$\w+`, "nv"),
			rule("\\$\\{(?:[^`]|`.)+?\\}", "nv"),
		)
		l.state("multiline",
			rule(`(?i)\.(?:`+multiline+`)`, "cs"),
			rule(`#>`, "cm", "#pop"),
			rule(`(?m)[^#.]+?`, "cm"),
			rule(`[#.]+`, "cm"),
		)
		l.state("interpol",
			rule(`\)`, "si", "#pop"),
			mixin("root"),
		)
		l.state("dq",
			rule(`(?:\$#?)?"`, "s2", "#pop"),
			rule(`\$\(`, "si", "interpol"),
			rule("`$", "se"),
			rule("`.", "se"),
			rule("[^\"`$]+", "s2"),
			mixin("variable"),
		)
		l.state("sq",
			rule(`'`, "s1", "#pop"),
			rule(`[^']+`, "s1"),
		)
		l.state("heredoc",
			rule(`(?:\$#?)?"@`, "sh", "#pop"),
			rule(`\$\(`, "si", "interpol"),
			rule("`$", "se"),
			rule("`.", "se"),
			rule("(?m)[^\"`$]+?", "sh"),
			rule(`"+`, "sh"),
			mixin("variable"),
		)
		l.state("class",
			rule(`\{`, "p", "#pop"),
			rule(`\s+`, "w"),
			rule(`\w+`, "nc"),
			rule(`[:,]`, "p"),
		)
		l.state("expr",
			mixin("comments"),
			rule(`"`, "s2", "dq"),
			rule(`'`, "s1", "sq"),
			rule(`@"`, "sh", "heredoc"),
			rule(`(?m)@'.*?'@`, "sh"),
			rule(`\d*\.\d+`, "mf"),
			rule(`\d+`, "mi"),
			rule(`@\{`, "p", "hasht"),
			rule(`@\(`, "p", "array"),
			rule(`{`, "p", "brace"),
			rule(`\[`, "p", "bracket"),
		)
		l.state("hasht",
			rule(`\}`, "p", "#pop"),
			rule(`=`, "o"),
			rule(`[,;]`, "p"),
			mixin("expr"),
			rule(`\w+`, "nx"),
			mixin("variable"),
		)
		l.state("array",
			rule(`\s+`, "w"),
			rule(`\)`, "p", "#pop"),
			rule(`[,;]`, "p"),
			mixin("expr"),
			mixin("variable"),
		)
		l.state("brace",
			rule(`[}]`, "p", "#pop"),
			mixin("root"),
		)
		l.state("bracket",
			rule(`\]`, "p", "#pop"),
			rule(`[A-Za-z]\w+\.`, "n"),
			ruleF(`([A-Za-z]\w+)`, func(c *rctx) {
				if psAttributes[c.m.String()] {
					c.token("bp")
				} else {
					c.token("n")
				}
			}),
			mixin("root"),
		)
		l.state("parameters",
			rule("(?m)`.", "se"),
			ruleF(`\)`, func(c *rctx) {
				c.token("p")
				if c.inState("interpol") {
					c.pop(2)
				}
			}),
			rule(`\s*?\n`, "w", "#pop"),
			rule(`[;(){}\]]`, "p", "#pop"),
			rule(`[|=]`, "o", "#pop"),
			rule(`[\/\\~\w][-.:\/\\~\w]*`, "nx"),
			rule(`\w[-\w]+`, "nx"),
			mixin("root"),
		)
		l.state("comments",
			rule(`\s+`, "w"),
			rule(`#.*`, "c"),
			rule(`<#`, "cm", "multiline"),
		)
		l.state("root",
			mixin("comments"),
			rule(`#requires\s-version \d(?:\.\d+)?`, "cp"),
			rule(`\.\.(?=\.?\d)`, "o"),
			rule(`(?i)(?:`+operators+`)\b`, "o"),
			ruleF(`(?i)(class)(\s+)(\w+)`, func(c *rctx) { c.groups("kr", "w", "nc"); c.push("class") }),
			ruleG(`(?i)(function)(\s+)(?:(\w+)(:))?(\w[-\w]+)`, toks("kr", "w", "nn", "p", "nf")),
			rule(`(?i)(?:`+keywords+`)\b(?![-.])`, "kr"),
			rule(`-{1,2}\w+`, "nt"),
			ruleF(`(\.)?([-\w]+)(\[)`, func(c *rctx) { c.groups("o", "n", "p"); c.push("bracket") }),
			ruleF(`(?i)([\/\\~a-z][-.:\/\\~\w]*)(\n)?`, func(c *rctx) { c.groups("n", "w"); c.push("parameters") }),
			ruleF(`(\.)([-\w]+)(?:(\()|(\n))?`, func(c *rctx) {
				c.groups("o", "nf", "p", "w")
				if g := c.m.GroupByNumber(3); g != nil && len(g.Captures) > 0 {
					c.push("parameters")
				}
			}),
			rule(`\?`, "nf", "parameters"),
			mixin("expr"),
			mixin("variable"),
			rule(`[-+*\/%=!.&|]`, "o"),
			rule(`[{}(),:;]`, "p"),
			rule("`$", "se"),
		)
		return l
	})
}
