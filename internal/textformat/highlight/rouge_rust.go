// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import "strings"

// Rouge 5.1 の rust.rb の移植。

var rustKeywords = strings.Fields(`as async await break const continue crate dyn else enum extern false
fn for if impl in let log loop match mod move mut pub ref return self
Self static struct super trait true type unsafe use where while
abstract become box do final macro
override priv typeof unsized virtual
yield try
union`)

var rustBuiltins = wordset(`Add BitAnd BitOr BitXor bool c_char c_double c_float char
c_int clock_t c_long c_longlong Copy c_schar c_short
c_uchar c_uint c_ulong c_ulonglong c_ushort c_void dev_t DIR
dirent Div Eq Err f32 f64 FILE float fpos_t
i16 i32 i64 i8 isize Index ino_t int intptr_t mode_t Mul
Neg None off_t Ok Option Ord Owned pid_t ptrdiff_t
Send Shl Shr size_t Some ssize_t str Sub time_t
u16 u32 u64 u8 usize uint uintptr_t
Box Vec String Rc Arc
u128 i128 Result Sync Pin Unpin Sized Drop drop Fn FnMut FnOnce
Clone PartialEq PartialOrd AsMut AsRef From Into Default
DoubleEndedIterator ExactSizeIterator Extend IntoIterator Iterator
FromIterator ToOwned ToString TryFrom TryInto`)

func init() {
	registerRouge("rust", func() *rlexer {
		const (
			xidStart    = `\p{L}\p{Nl}`
			xidContinue = `\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}`
			id          = `[` + xidStart + `_][` + xidContinue + `]*`
			hex         = `[0-9a-fA-F]`
			escapes     = `(?:\\([nrt'"\\0]|x` + hex + `{2}|u\{(` + hex + `_*){1,6}\}))`
			size        = `(?:8|16|32|64|128|size)`
			dot         = `[.][0-9][0-9_]*`
			exp         = `[eE][-+]?[0-9_]+`
			flt         = `(?:f32|f64)`
		)
		delimMap := map[string]string{"[": "]", "(": ")", "{": "}"}
		l := &rlexer{tag: "rust"}
		l.start = func(c *rctx) {
			c.vars["macro_delims"] = map[string]int{"]": 0, ")": 0, "}": 0}
			c.push("bol")
		}
		l.state("bol",
			mixin("whitespace"),
			rule(`#\s[^\n]*`, "cs"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("attribute",
			mixin("whitespace"),
			mixin("has_literals"),
			rule(`[(,)=:]`, "nd"),
			rule(`\]`, "nd", "#pop"),
			rule(id, "nd"),
		)
		l.state("whitespace",
			rule(`\s+`, ""),
			mixin("comments"),
		)
		l.state("comments",
			rule(`////+[^\n]*`, "c1"),
			rule(`//[/!][^\n]*`, "cd"),
			rule(`//[^\n]*`, "c1"),
			rule(`/\*\*\*?/`, "cm"),
			rule(`/\*\*\*+`, "cm", "nested_plain_block"),
			rule(`/[*][*!]`, "cd", "nested_doc_block"),
			rule(`/[*]`, "cm", "nested_plain_block"),
		)
		for _, s := range [][2]string{{"nested_plain_block", "cm"}, {"nested_doc_block", "cd"}} {
			l.state(s[0],
				rule(`\*/`, s[1], "#pop"),
				rule(`/\*`, s[1], s[0]),
				rule(`[^*/]+|[*/]`, s[1]),
			)
		}
		l.state("root",
			rule(`\n`, "", "bol"),
			mixin("whitespace"),
			rule(`#!?\[`, "nd", "attribute"),
			rule(`\b(?:`+strings.Join(rustKeywords, "|")+`)\b`, "k"),
			mixin("has_literals"),
			rule(`[=-]>`, "k"),
			rule(`<->`, "k"),
			rule(`[()\[\]{}|,:;]`, "p"),
			rule(`[*\/!@~&+%^<>=\?-]|\.{2,3}`, "o"),
			rule(`(?m)([.]\s*)?`+id+`(?=\s*[(])`, "nf"),
			rule(`[.]\s*await\b`, "k"),
			rule(`[.]\s*`+id, "py"),
			rule(`[.]\s*\d+`, "na"),
			ruleG(`(?m)(`+id+`)(::)`, toks("nn", "p")),
			rule(`\bmacro_rules!`, "nd", "macro_rules"),
			rule(id+`!`, "nd", "macro"),
			rule(`'static\b`, "k"),
			rule(`'`+id, "nv"),
			ruleF(id, func(c *rctx) {
				if rustBuiltins[c.m.String()] {
					c.token("nb")
				} else {
					c.token("n")
				}
			}),
		)
		l.state("macro",
			mixin("has_literals"),
			ruleF(`[\[{(]`, func(c *rctx) {
				d := c.vars["macro_delims"].(map[string]int)
				d[delimMap[c.m.String()]]++
				c.token("p")
			}),
			ruleF(`[\]})]`, func(c *rctx) {
				d := c.vars["macro_delims"].(map[string]int)
				d[c.m.String()]--
				closed := true
				for _, v := range d {
					if v != 0 {
						closed = false
					}
				}
				if closed {
					c.pop()
				}
				c.token("p")
			}),
			rule(id+`!`, "nd"),
			mixin("root"),
			rule(`.`, ""),
		)
		l.state("macro_rules",
			rule(`[$]`+id+`(:`+id+`)?`, "nv"),
			rule(`[$]`, "nv"),
			mixin("macro"),
		)
		l.state("has_literals",
			rule(`\b(?:true|false)\b`, "kc"),
			rule(`b?'(?:`+escapes+`|[^\\])'`, "sc"),
			rule(`b?"`, "s", "string"),
			rule(`(?m)b?r(#*)".*?"\1`, "s"),
			rule(`[0-9][0-9_]*(`+dot+`(?:`+exp+`)?`+flt+`?|(?:`+dot+`)?(?:`+exp+`)`+flt+`?|(?:`+dot+`)?(?:`+exp+`)?`+flt+`|[.](?![._`+xidStart+`]))`, "mf"),
			rule(`(0b[10_]+|0x[0-9a-fA-F_]+|0o[0-7_]+|[0-9][0-9_]*)(u`+size+`?|i`+size+`)?`, "mi"),
		)
		l.state("string",
			rule(`"`, "s", "#pop"),
			rule(escapes, "se"),
			rule(`\\\n[ \t\r\n]*`, "se"),
			rule(`(?m)[^"\\]+`, "s"),
		)
		return l
	})
}
