// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import (
	"strings"
	"sync"
)

// Rouge 5.1 の ruby.rb の移植。

// rubyRegexpEscape は Ruby の Regexp.escape。
func rubyRegexpEscape(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '[', ']', '{', '}', '(', ')', '|', '-', '*', '.', '\\', '?', '+', '^', '$', '#':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case ' ':
			sb.WriteString(`\x20`)
		case '\t':
			sb.WriteString(`\t`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\f':
			sb.WriteString(`\f`)
		case '\v':
			sb.WriteString(`\v`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

const (
	rbLower    = `\p{Ll}\p{Lu}\p{Lt}` // /i 付きの \p{Ll}
	rbKeywords = `BEGIN|END|alias|begin|break|case|defined?|do|else|elsif|end|` +
		`ensure|for|if|in|next|redo|rescue|raise|retry|return|super|then|` +
		`undef|unless|until|when|while|yield`
	rbKeywordsPseudo = `loop|include|extend|raise|` +
		`alias_method|attr|catch|throw|private|module_function|` +
		`public|protected|true|false|nil|__FILE__|__LINE__`
	rbBuiltinsQ = `autoload|block_given|const_defined|eql|equal|frozen|` +
		`include|instance_of|is_a|iterator|kind_of|method_defined|` +
		`nil|private_method_defined|protected_method_defined|` +
		`public_method_defined|respond_to|tainted`
	rbBuiltinsB = `chomp|chop|exit|gsub|sub`
)

var rbBuiltinsG = strings.Join(strings.Fields(`attr_reader attr_writer attr_accessor
__id__ __send__ abort ancestors at_exit autoload binding callcc
caller catch chomp chop class_eval class_variables clone
const_defined\? const_get const_missing const_set constants
display dup eval exec exit extend fail fork format freeze
getc gets global_variables gsub hash id included_modules
inspect instance_eval instance_method instance_methods
instance_variable_get instance_variable_set instance_variables
lambda load local_variables loop method method_missing
methods module_eval name object_id open p print printf
private_class_method private_instance_methods private_methods proc
protected_instance_methods protected_methods public_class_method
public_instance_methods public_methods putc puts raise rand
readline readlines require require_relative scan select self send set_trace_func
singleton_methods sleep split sprintf srand sub syscall system
taint test throw to_a to_s trace_var trap untaint untrace_var warn`), "|")

// Rouge 5.1 では語を切り出してから集合で判定する（%w の const_defined\? は \ を含むため一致しない）
var (
	rbKeywordSet       = wordset(strings.ReplaceAll(rbKeywords, "|", " "))
	rbKeywordPseudoSet = wordset(strings.ReplaceAll(rbKeywordsPseudo, "|", " "))
	rbBuiltinQSet      = wordset(strings.ReplaceAll(rbBuiltinsQ, "|", " "))
	rbBuiltinBSet      = wordset(strings.ReplaceAll(rbBuiltinsB, "|", " "))
	rbBuiltinGSet      = wordset(strings.ReplaceAll(rbBuiltinsG, "|", " "))
)

type rbHeredoc struct {
	tolerant bool
	name     string
}

func rbQueue(c *rctx) *[]rbHeredoc {
	q, _ := c.vars["heredoc_queue"].(*[]rbHeredoc)
	if q == nil {
		q = &[]rbHeredoc{}
		c.vars["heredoc_queue"] = q
	}
	return q
}

var (
	rbSigilMu     sync.Mutex
	rbSigilStates = map[string]*rstate{}
)

// rbSigilState は sigil_strings の push do ... end で作られる無名状態。
func rbSigilState(l *rlexer, open, close, toktype string, interp bool) *rstate {
	key := open + "\x00" + close + "\x00" + toktype
	if interp {
		key += "\x00i"
	}
	rbSigilMu.Lock()
	defer rbSigilMu.Unlock()
	if s, ok := rbSigilStates[key]; ok {
		return s
	}
	uniq := open
	if close != open {
		uniq += close
	}
	if open == close && open == `\#` {
		uniq = ""
	}
	st := &rstate{name: "sigil:" + key}
	st.rules = append(st.rules, rule(`\\[#`+uniq+`\\]`, "se"))
	if open != close {
		st.rules = append(st.rules, ruleF(open, func(c *rctx) {
			c.token(toktype)
			c.push()
		}))
	}
	st.rules = append(st.rules, rule(close, toktype, "#pop"))
	if interp {
		st.rules = append(st.rules, mixin("string_intp_escaped"), rule(`#`, toktype))
	} else {
		st.rules = append(st.rules, rule(`[\\#]`, toktype))
	}
	st.rules = append(st.rules, rule(`(?m)[^#`+uniq+`\\]+`, toktype))
	rbSigilStates[key] = st
	return st
}

func init() {
	registerRouge("ruby", func() *rlexer {
		l := &rlexer{tag: "ruby"}
		l.start = func(c *rctx) {
			c.push("expr_start")
			q := []rbHeredoc{}
			c.vars["heredoc_queue"] = &q
		}
		l.state("symbols",
			rule(`(?x)
          :  # initial :
          @{0,2} # optional ivar, for :@foo and :@@foo
          [`+rbLower+`_][`+pWord+`]*[!?]? # the symbol
        `, "ss"),
			rule(":(?:\\*\\*|[-+]@|[/\\%&\\|^`~]|\\[\\]=?|<<|>>|<=?>|<=?|===?)", "ss"),
			rule(`:'(\\\\|\\'|[^'])*'`, "ss"),
			rule(`:"`, "ss", "simple_sym"),
		)
		delimiterMap := map[string]string{"{": "}", "[": "]", "(": ")", "<": ">"}
		l.state("sigil_strings",
			ruleF(`%([rqswQWxiI])?([^`+pWord+`\s])`, func(c *rctx) {
				m1, m2 := c.group(1), c.group(2)
				cl, ok := delimiterMap[m2]
				if !ok {
					cl = m2
				}
				open := rubyRegexpEscape(m2)
				closeRe := rubyRegexpEscape(cl)
				interp := strings.ContainsAny(m1, "rQWxI") && m1 != "" || m1 == ""
				toktype := "sx"
				if m1 == "r" {
					toktype = "sr"
					c.push("regex_flags")
				}
				c.token(toktype)
				c.stack = append(c.stack, rbSigilState(l, open, closeRe, toktype, interp))
			}),
		)
		l.state("strings",
			mixin("symbols"),
			rule(`\b[\p{Ll}_][`+pWord+`]*?[?!]?:\s+`, "ss", "expr_start"),
			rule(`'(\\\\|\\'|[^'])*'`, "s1"),
			rule(`"`, "s2", "simple_string"),
			rule("(?<!\\.)`", "sb", "simple_backtick"),
		)
		l.state("regex_flags", rule(`[mixounse]*`, "sr", "#pop"))
		for _, s := range [][3]string{{"string", "s2", `"`}, {"sym", "ss", `"`}, {"backtick", "sb", "`"}} {
			l.state("simple_"+s[0],
				mixin("string_intp_escaped"),
				rule(`(?m)[^\\`+s[2]+`#]+`, s[1]),
				rule(`[\\#]`, s[1]),
				rule(s[2], s[1], "#pop"),
			)
		}
		l.state("whitespace",
			mixin("inline_whitespace"),
			rule(`(?m)\n\s*`, "", "expr_start"),
			rule(`#.*$`, "c1"),
			rule(`(?m)=begin\b.*?\n=end\b`, "cm"),
		)
		l.state("inline_whitespace", rule(`[ \t\r]+`, ""))
		const decimal = `[\d]+(?:_\d+)*`
		const exp = `[eE][+-]?\d+`
		l.state("root",
			mixin("whitespace"),
			rule(`__END__`, "cp", "end_part"),
			rule(`0_?[0-7]+(?:_[0-7]+)*`, "mo"),
			rule(`0x[0-9A-Fa-f]+(?:_[0-9A-Fa-f]+)*`, "mh"),
			rule(`0b[01]+(?:_[01]+)*`, "mb"),
			rule(decimal+`(?:\.`+decimal+`(?:`+exp+`)?|`+exp+`)`, "mf"),
			rule(decimal, "mi"),
			rule(`@@[`+rbLower+`_][`+pWord+`]*`, "vc"),
			rule(`@[`+rbLower+`_][`+pWord+`]*`, "vi"),
			rule(`\$[`+pWord+`]+`, "vg"),
			rule("\\$[!@&`'+~=/\\\\,;.<>_*\\$?:\"]", "vg"),
			rule(`\$-[0adFiIlpvw]`, "vg"),
			rule(`::`, "o"),
			mixin("strings"),
			ruleF(`\w+[?]?`, func(c *rctx) {
				switch w := c.m.String(); {
				case rbKeywordSet[w]:
					c.token("k")
				case rbKeywordPseudoSet[w]:
					c.token("kp")
				default:
					c.fallThrough()
					return
				}
				c.push("expr_start")
			}),
			rule(`(not|and|or)\b`, "ow", "expr_start"),
			ruleG(`(?x)
          (module)
          (\s+)
          ([\p{L}_][\p{L}0-9_]*(::[\p{L}_][\p{L}0-9_]*)*)
        `, toks("k", "", "nn")),
			ruleF(`(def\b)(\s*)`, func(c *rctx) { c.groups("k", ""); c.push("funcname") }),
			ruleF(`(class\b)(\s*)`, func(c *rctx) { c.groups("k", ""); c.push("classname") }),
			ruleF(`(\w+)([?!])?`, func(c *rctx) {
				switch w, q := c.group(1), c.group(2); {
				case q == "?" && rbBuiltinQSet[w], q == "!" && rbBuiltinBSet[w]:
					c.token("nb")
				default:
					c.fallThrough()
					return
				}
				c.push("expr_start")
			}),
			ruleF(`(?<![.])\w+`, func(c *rctx) {
				if rbBuiltinGSet[c.m.String()] {
					c.token("nb")
					c.push("method_call")
				} else {
					c.fallThrough()
				}
			}),
			mixin("has_heredocs"),
			rule(`\.{2,3}`, "o", "expr_start"),
			rule(`[\p{Lu}][\p{L}0-9_]*`, "no", "method_call"),
			ruleF("(\\.|::)(\\s*)([\\p{Ll}_]["+pWord+"]*[!?]?|[*%&^`~+-\\/\\[<>=])", func(c *rctx) {
				c.groups("p", "", "nf")
				c.push("method_call")
			}),
			rule(`[\p{L}_][`+pWord+`]*[?!]`, "n", "expr_start"),
			rule(`[\p{L}_][`+pWord+`]*`, "n", "method_call"),
			rule(`\*\*|<<?|>>?|>=|<=|<=>|=~|={3}|!~|&&?|\|\||\.`, "o", "expr_start"),
			rule(`[-+\/*%=<>&!^|~]=?`, "o", "expr_start"),
			ruleF(`[?]`, func(c *rctx) { c.token("p"); c.push("ternary"); c.push("expr_start") }),
			rule(`[\[({,:\\;/]`, "p", "expr_start"),
			rule(`[\])}]`, "p"),
		)
		heredocAction := func(name func(c *rctx) string, const3 func(c *rctx) string) func(c *rctx) {
			return func(c *rctx) {
				c.token("o", c.group(1))
				c.token("no", const3(c))
				q := rbQueue(c)
				*q = append(*q, rbHeredoc{tolerant: c.group(1) == "<<-" || c.group(1) == "<<~", name: name(c)})
				if !c.isState("heredoc_queue") {
					c.push("heredoc_queue")
				}
			}
		}
		l.state("has_heredocs",
			ruleF("(?<![\\p{L}\\p{M}\\p{Nd}\\p{Pc}])(<<[-~]?)([\"`']?)([\\p{L}_]["+pWord+"]*)(\\2)", heredocAction(
				func(c *rctx) string { return c.group(3) },
				func(c *rctx) string { return c.group(2) + c.group(3) + c.group(4) })),
			ruleF(`(<<[-~]?)(["'])(\2)`, heredocAction(
				func(c *rctx) string { return "" },
				func(c *rctx) string { return c.group(2) + c.group(3) })),
		)
		l.state("heredoc_queue",
			ruleF(`(?=\n)`, func(c *rctx) { c.gotoState("resolve_heredocs") }),
			mixin("root"),
		)
		l.state("resolve_heredocs",
			mixin("string_intp_escaped"),
			rule(`\n`, "sh", "test_heredoc"),
			rule(`[#\\\n]`, "sh"),
			rule(`[^#\\\n]+`, "sh"),
		)
		l.state("test_heredoc",
			ruleF(`[^#\\\n]*$`, func(c *rctx) {
				q := rbQueue(c)
				if len(*q) == 0 {
					c.token("sh")
					c.pop()
					return
				}
				h := (*q)[0]
				check := strings.TrimRight(c.m.String(), " \t\n\v\f\r\x00")
				if h.tolerant {
					check = strings.Trim(c.m.String(), " \t\n\v\f\r\x00")
				}
				if check == h.name {
					*q = (*q)[1:]
					if len(*q) == 0 {
						c.pop()
					}
					c.token("no")
				} else {
					c.token("sh")
				}
				c.pop()
			}),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("funcname",
			rule(`\s+`, ""),
			rule(`\(`, "p", "defexpr"),
			ruleF("(?x)\n          (?:([\\p{L}_]["+pWord+"]*)(\\.))?\n          (\n            [\\p{L}_]["+pWord+"]*[!?]? |\n            \\*\\*? | [-+]@? | [/%&\\|^`~] | \\[\\]=? |\n            <=>? | <<? | >>? | >= | ===?\n          )\n        ", func(c *rctx) {
				c.groups("nc", "o", "nf")
				c.pop()
			}),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("classname",
			rule(`\s+`, ""),
			rule(`[`+pWord+`]+(::[`+pWord+`]+)+`, "nc"),
			ruleF(`\(`, func(c *rctx) { c.token("p"); c.push("defexpr"); c.push("expr_start") }),
			ruleF(`<<`, func(c *rctx) { c.token("o"); c.gotoState("expr_start") }),
			rule(`[\p{Lu}_][`+pWord+`]*`, "nc", "#pop"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("ternary",
			ruleF(`(:)(\s+)`, func(c *rctx) { c.groups("p", ""); c.gotoState("expr_start") }),
			ruleF(`:(?![^#\n]*?[:\\])`, func(c *rctx) { c.token("p"); c.gotoState("expr_start") }),
			mixin("root"),
		)
		l.state("defexpr",
			ruleF(`(\))(\.|::)?`, func(c *rctx) { c.groups("p", "o"); c.pop() }),
			ruleF(`\(`, func(c *rctx) { c.token("p"); c.push("defexpr"); c.push("expr_start") }),
			mixin("root"),
		)
		l.state("in_interp",
			rule(`}`, "si", "#pop"),
			mixin("root"),
		)
		l.state("string_intp",
			rule(`[#][{]`, "si", "in_interp"),
			rule(`#(@@?|\$)[`+rbLower+`_][`+pWord+`]*`, "si"),
		)
		l.state("string_intp_escaped",
			mixin("string_intp"),
			rule(`\\([\\abefnrstv#"']|x[a-fA-F0-9]{1,2}|[0-7]{1,3})`, "se"),
			rule(`\\.`, "se"),
		)
		l.state("method_call",
			ruleF(`/|%`, func(c *rctx) { c.token("o"); c.gotoState("expr_start") }),
			ruleF(`(?=\n)`, func(c *rctx) { c.pop() }),
			ruleF(``, func(c *rctx) { c.gotoState("method_call_spaced") }),
		)
		l.state("method_call_spaced",
			mixin("whitespace"),
			ruleF(`[%/]=`, func(c *rctx) { c.token("o"); c.gotoState("expr_start") }),
			ruleF(`(/)(?=\S|\s*/)`, func(c *rctx) { c.token("sr"); c.gotoState("slash_regex") }),
			mixin("sigil_strings"),
			ruleF(`(?=\s*/)`, func(c *rctx) { c.pop() }),
			ruleF(`\s+`, func(c *rctx) { c.token(""); c.gotoState("expr_start") }),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("expr_start",
			mixin("inline_whitespace"),
			ruleF(`/`, func(c *rctx) { c.token("sr"); c.gotoState("slash_regex") }),
			rule(`(?x)
          [?](\\[MC]-)*     # modifiers
          (\\([\\abefnrstv\#"']|x[a-fA-F0-9]{1,2}|[0-7]{1,3})|\S)
          (?![`+pWord+`])
        `, "sc", "#pop"),
			ruleG(`(\s*)(%[rqswQWxiI]? \S* )`, toks("", "sx"), "#pop"),
			mixin("sigil_strings"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("slash_regex",
			mixin("string_intp"),
			rule(`\\\\`, "sr"),
			rule(`\\/`, "sr"),
			rule(`[\\#]`, "sr"),
			rule(`(?m)[^\\/#]+`, "sr"),
			ruleF(`/`, func(c *rctx) { c.token("sr"); c.gotoState("regex_flags") }),
		)
		l.state("end_part", rule(`(?m).+`, "cp", "#pop"))
		return l
	})
}
