// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

// Rouge 4.7 の c.rb / cpp.rb の移植。

const (
	cKeywords = `auto break case const continue default do else enum extern
for goto if register restricted return sizeof static struct
switch typedef union volatile virtual while
_Alignas _Alignof _Atomic _Generic _Imaginary
_Noreturn _Static_assert _Thread_local`
	cKeywordsType = `int long float short double char unsigned signed void
jmp_buf FILE DIR div_t ldiv_t mbstate_t sig_atomic_t fpos_t
clock_t time_t va_list size_t ssize_t off_t wchar_t ptrdiff_t
wctrans_t wint_t wctype_t
_Bool _Complex int8_t int16_t int32_t int64_t
uint8_t uint16_t uint32_t uint64_t int_least8_t
int_least16_t int_least32_t int_least64_t
uint_least8_t uint_least16_t uint_least32_t
uint_least64_t int_fast8_t int_fast16_t int_fast32_t
int_fast64_t uint_fast8_t uint_fast16_t uint_fast32_t
uint_fast64_t intptr_t uintptr_t intmax_t
uintmax_t
char16_t char32_t`
	cReserved = `__asm __int8 __based __except __int16 __stdcall __cdecl
__fastcall __int32 __declspec __finally __int61 __try __leave
inline _inline __inline naked _naked __naked restrict _restrict
__restrict thread _thread __thread typename _typename __typename`
	cWS = `(?:\s|//.*?\n|/[*].*?[*]/)+`
	cID = `[a-zA-Z_][a-zA-Z0-9_]*`
)

func buildC(tag string, keywords, keywordsType, reserved, builtins map[string]bool) *rlexer {
	l := &rlexer{tag: tag}
	l.start = func(c *rctx) { c.push("bol") }
	l.state("expr_bol",
		mixin("inline_whitespace"),
		rule(`#if\s0`, "c", "if_0"),
		rule(`#`, "cp", "macro"),
		ruleF(``, func(c *rctx) { c.pop() }),
	)
	l.state("bol",
		rule(cID+`:(?!:)`, "nl"),
		mixin("expr_bol"),
	)
	l.state("inline_whitespace",
		rule(`[ \t\r]+`, ""),
		rule(`\\\n`, ""),
		rule(`(?m)/(\\\n)?[*].*?[*](\\\n)?/`, "cm"),
	)
	l.state("whitespace",
		rule(`(?m)\n+`, "", "bol"),
		rule(`//(\\.|.)*?$`, "c1", "bol"),
		mixin("inline_whitespace"),
	)
	l.state("expr_whitespace",
		rule(`(?m)\n+`, "", "expr_bol"),
		mixin("whitespace"),
	)
	l.state("statements",
		mixin("whitespace"),
		rule(`(u8|u|U|L)?"`, "s", "string"),
		rule(`(?i)(u8|u|U|L)?'(\\.|\\[0-7]{1,3}|\\x[a-f0-9]{1,2}|[^\\'\n])'`, "sc"),
		rule(`(?i)(\d+[.]\d*|[.]?\d+)e[+-]?\d+[lu]*`, "mf"),
		rule(`(?i)\d+e[+-]?\d+[lu]*`, "mf"),
		rule(`(?i)0x[0-9a-f]+[lu]*`, "mh"),
		rule(`(?i)0[0-7]+[lu]*`, "mo"),
		rule(`(?i)\d+[lu]*`, "mi"),
		rule(`\*/`, "err"),
		rule(`[~!%^&*+=\|?:<>/-]`, "o"),
		rule(`[()\[\],.;]`, "p"),
		rule(`\bcase\b`, "k", "case"),
		rule(`(?:true|false|NULL)\b`, "nb"),
		ruleF(cID, func(c *rctx) {
			name := c.m.String()
			switch {
			case keywords[name]:
				c.token("k")
			case keywordsType[name]:
				c.token("kt")
			case reserved[name]:
				c.token("kr")
			case builtins[name]:
				c.token("nb")
			default:
				c.token("n")
			}
		}),
	)
	l.state("case",
		rule(`:`, "p", "#pop"),
		mixin("statements"),
	)
	l.state("root",
		mixin("expr_whitespace"),
		ruleF(`(?mx)
          ([\w*\s]+?[\s*]) # return arguments
          (`+cID+`)          # function name
          (\s*\([^;]*?\))  # signature
          (`+cWS+`?)({|;)    # open brace or semicolon
        `, func(c *rctx) {
			m1, m2, m3, m4, m5 := c.group(1), c.group(2), c.group(3), c.group(4), c.group(5)
			c.recurse(m1)
			c.token("nf", m2)
			c.recurse(m3)
			c.recurse(m4)
			c.token("p", m5)
			if m5 == "{" {
				c.push("function")
			}
		}),
		rule(`\{`, "p", "function"),
		mixin("statements"),
	)
	l.state("function",
		mixin("whitespace"),
		mixin("statements"),
		rule(`;`, "p"),
		rule(`{`, "p", "function"),
		rule(`}`, "p", "#pop"),
	)
	l.state("string",
		rule(`"`, "s", "#pop"),
		rule(`\\([\\abfnrtv"']|x[a-fA-F0-9]{2,4}|[0-7]{1,3})`, "se"),
		rule(`[^\\"\n]+`, "s"),
		rule(`\\\n`, "s"),
		rule(`\\`, "s"),
	)
	l.state("macro",
		mixin("include"),
		rule(`[^/\n\\]+`, "cp"),
		rule(`(?m)\\.`, "cp"),
		mixin("inline_whitespace"),
		rule(`/`, "cp"),
		rule(`\n`, "cp", "#pop"),
	)
	l.state("include",
		ruleG(`(include)(\s*)(<[^>]+>)([^\n]*)`, toks("cp", "", "cpf", "c1")),
		ruleG(`(include)(\s*)("[^"]+")([^\n]*)`, toks("cp", "", "cpf", "c1")),
	)
	l.state("if_0",
		rule(`^\s*#if`, "c", "if_0"),
		rule(`^\s*#\s*el(?:se|if)`, "c", "#pop"),
		rule(`(?m)^\s*#\s*endif\b.*?(?<!\\)\n`, "c", "#pop"),
		rule(`.*?\n`, "c"),
	)
	return l
}

func init() {
	registerRouge("c", func() *rlexer {
		return buildC("c", wordset(cKeywords), wordset(cKeywordsType), wordset(cReserved), nil)
	})
	registerRouge("cpp", func() *rlexer {
		l := buildC("cpp",
			wordset(cKeywords+` asm auto catch char8_t concept
consteval constexpr constinit const_cast co_await co_return co_yield
delete dynamic_cast explicit export friend
mutable namespace new operator private protected public
reinterpret_cast requires restrict size_of static_cast this throw throws
typeid typename using virtual final override import module
alignas alignof decltype noexcept static_assert
thread_local try`),
			wordset(cKeywordsType+` bool`),
			wordset(cReserved+` __virtual_inheritance __uuidof __super __single_inheritance
__multiple_inheritance __interface __event`), nil)
		const dq = `\d('?\d)*`
		l.prependRules("root",
			rule(`(?:__offload|__blockingoffload|__outer)\b`, "kp"),
		)
		l.prependRules("statements",
			rule(`(class|struct)\b`, "k", "classname"),
			rule(`template\b`, "k", "template"),
			rule(dq+`(\.`+dq+`)?(?:y|d|h|(?:min)|s|(?:ms)|(?:us)|(?:ns)|i|(?:if)|(?:il))\b`, "mx"),
			rule(`(?i)(`+dq+`[.]`+dq+`?|[.]`+dq+`)([ep][+-]?`+dq+`)?[luf]*`, "mf"),
			rule(`(?i)`+dq+`[ep][+-]?`+dq+`[luf]*`, "mf"),
			rule(`(?i)0x\h('?\h)*([ep][+-]?`+dq+`)?[lu]*`, "mh"),
			rule(`(?i)0x\h('?\h)*[.]\h+([ep][+-]?`+dq+`)[luf]*`, "mh"),
			rule(`0b[01]+('[01]+)*`, "mb"),
			rule(`(?i)0[0-7]('?[0-7])*[lu]*`, "mo"),
			rule(`(?i)`+dq+`[lu]*`, "mi"),
			rule(`\bnullptr\b`, "nb"),
			rule(`(?m)(?:u8|u|U|L)?R"([a-zA-Z0-9_{}\[\]#<>%:;.?*\+\-\/\^&|~!=,"']{,16})\(.*?\)\1"`, "s"),
			rule(`(::|<=>)`, "o"),
			rule(`[{]`, "p"),
			ruleF(`}`, func(c *rctx) {
				c.token("p")
				if c.inState("function") {
					c.pop()
				}
			}),
		)
		l.state("classname",
			rule(cID, "nc", "#pop"),
			mixin("whitespace"),
			rule(`[.]{3}`, "o"),
			rule(`,`, "p", "#pop"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("template",
			rule(`[>;]`, "p", "#pop"),
			rule(`typename\b`, "k", "classname"),
			mixin("statements"),
		)
		l.state("case",
			rule(`:(?!:)`, "p", "#pop"),
			mixin("statements"),
		)
		return l
	})
}
