// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import "strings"

// Rouge 4.7 の perl.rb の移植。

func init() {
	registerRouge("perl", func() *rlexer {
		join := func(s string) string { return strings.Join(strings.Fields(s), "|") }
		keywords := join(`case continue do else elsif for foreach if last my next our
redo reset then unless until while use print new BEGIN CHECK
INIT END return`)
		builtins := join(`abs accept alarm atan2 bind binmode bless caller chdir chmod
chomp chop chown chr chroot close closedir connect continue cos
crypt dbmclose dbmopen defined delete die dump each endgrent
endhostent endnetent endprotoent endpwent endservent eof eval
exec exists exit exp fcntl fileno flock fork format formline getc
getgrent getgrgid getgrnam gethostbyaddr gethostbyname gethostent
getlogin getnetbyaddr getnetbyname getnetent getpeername
getpgrp getppid getpriority getprotobyname getprotobynumber
getprotoent getpwent getpwnam getpwuid getservbyname getservbyport
getservent getsockname getsockopt glob gmtime goto grep hex
import index int ioctl join keys kill last lc lcfirst length
link listen local localtime log lstat map mkdir msgctl msgget
msgrcv msgsnd my next no oct open opendir ord our pack package
pipe pop pos printf prototype push quotemeta rand read readdir
readline readlink readpipe recv redo ref rename require reverse
rewinddir rindex rmdir scalar seek seekdir select semctl semget
semop send setgrent sethostent setnetent setpgrp setpriority
setprotoent setpwent setservent setsockopt shift shmctl shmget
shmread shmwrite shutdown sin sleep socket socketpair sort splice
split sprintf sqrt srand stat study substr symlink syscall sysopen
sysread sysseek system syswrite tell telldir tie tied time times
tr truncate uc ucfirst umask undef unlink unpack unshift untie
utime values vec wait waitpid wantarray warn write`)
		const re = "sr"
		l := &rlexer{tag: "perl"}
		l.state("balanced_regex",
			rule(`(?m)/(\\[\\/]|[^/])*/[egimosx]*`, re, "#pop"),
			rule(`(?m)!(\\[\\!]|[^!])*![egimosx]*`, re, "#pop"),
			rule(`(?m)\\(\\\\|[^\\])*\\[egimosx]*`, re, "#pop"),
			rule(`{(\\[\\}]|[^}])*}[egimosx]*`, re, "#pop"),
			rule(`<(\\[\\>]|[^>])*>[egimosx]*`, re, "#pop"),
			rule(`\[(\\[\\\]]|[^\]])*\][egimosx]*`, re, "#pop"),
			rule(`\((\\[\\\)]|[^\)])*\)[egimosx]*`, re, "#pop"),
			rule(`@(\\[\\@]|[^@])*@[egimosx]*`, re, "#pop"),
			rule(`%(\\[\\%]|[^%])*%[egimosx]*`, re, "#pop"),
			rule(`\$(\\[\\\$]|[^\$])*\$[egimosx]*`, re, "#pop"),
		)
		l.state("root",
			rule(`#.*`, "c1"),
			rule(`(?m)^=[a-zA-Z0-9]+\s+.*?\n=cut`, "cm"),
			rule(`(?:`+keywords+`)\b`, "k"),
			ruleF(`(format)(\s+)([a-zA-Z0-9_]+)(\s*)(=)(\s*\n)`, func(c *rctx) {
				c.groups("k", "", "n", "", "p", "")
				c.push("format")
			}),
			rule(`(?:eq|lt|gt|le|ge|ne|not|and|or|cmp)\b`, "ow"),
			rule(`(?:s|tr|y){(\\\\|\\}|[^}])*}\s*`, re, "balanced_regex"),
			rule(`(?:s|tr|y)<(\\\\|\\>|[^>])*>\s*`, re, "balanced_regex"),
			rule(`(?:s|tr|y)\[(\\\\|\\\]|[^\]])*\]\s*`, re, "balanced_regex"),
			rule(`(?:s|tr|y)\((\\\\|\\\)|[^\)])*\)\s*`, re, "balanced_regex"),
			rule(`(?m)(?:s|tr|y)\s*([^\w\s])((\\\\|\\\1)|[^\x01])*?\1((\\\\|\\\1)|[^\x01])*?\1[msixpodualngcr]*`, re),
			rule(`(?m)(?:s|tr|y)\s+(\w)((\\\\|\\\1)|[^\x01])*?\1((\\\\|\\\1)|[^\x01])*?\1[msixpodualngcr]*`, re),
			rule(`m?/(\\\\|\\/|[^/\n])*/[msixpodualngc]*`, re),
			rule(`m(?=[/!\\{<\[\(@%\$])`, re, "balanced_regex"),
			rule(`(?m)m\s*([^\w\s])((\\\\|\\\1)|[^\x01])*?\1[msixpodualngc]*`, re),
			rule(`(?m)m\s+(\w)((\\\\|\\\1)|[^\x01])*?\1[msixpodualngc]*`, re),
			rule(`((?<==~)|(?<=\())\s*/(\\\\|\\/|[^/])*/[msixpodualngc]*`, re, "balanced_regex"),
			rule(`\s+`, ""),
			ruleF(`(?i)(?=[a-z_]\w*(\s*#.*\n)*\s*=>)`, func(c *rctx) { c.push("fat_comma") }),
			rule(`(?:`+builtins+`)\b`, "nb"),
			rule(`((__(DIE|WARN)__)|(DATA|STD(IN|OUT|ERR)))\b`, "bp"),
			rule(`(?m)<<([\'"]?)([a-zA-Z_][a-zA-Z0-9_]*)\1;?\n.*?\n\2\n`, "s"),
			rule(`(__(END|DATA)__)\b`, "cp", "end_part"),
			rule(`\$\^[ADEFHILMOPSTWX]`, "vg"),
			rule("\\$[\\\\\"'\\[\\]&`+*.,;=%~?@$!<>(^\\|\\/_-](?!\\w)", "vg"),
			rule(`(?i)[$@%&*][$@%&*#_]*(?=[a-z{\[;])`, "nv", "varname"),
			rule(`[-+\/*%=<>&^\|!\\~]=?`, "o"),
			rule(`0_?[0-7]+(_[0-7]+)*`, "mo"),
			rule(`0x[0-9A-Fa-f]+(_[0-9A-Fa-f]+)*`, "mh"),
			rule(`0b[01]+(_[01]+)*`, "mb"),
			rule(`(?i)(\d*(_\d*)*\.\d+(_\d*)*|\d+(_\d*)*\.\d+(_\d*)*)(e[+-]?\d+)?`, "mf"),
			rule(`(?i)\d+(_\d*)*e[+-]?\d+(_\d*)*`, "mf"),
			rule(`\d+(_\d+)*`, "mi"),
			rule(`'`, "p", "sq"),
			rule(`"`, "p", "dq"),
			rule("`", "p", "bq"),
			rule(`<([^\s>]+)>`, re),
			rule(`(q|qq|qw|qr|qx)\{`, "sx", "cb_string"),
			rule(`(q|qq|qw|qr|qx)\(`, "sx", "rb_string"),
			rule(`(q|qq|qw|qr|qx)\[`, "sx", "sb_string"),
			rule(`(q|qq|qw|qr|qx)<`, "sx", "lt_string"),
			rule(`(q|qq|qw|qr|qx)(\W)(.|\n)*?\2`, "sx"),
			rule(`package\s+`, "k", "modulename"),
			rule(`sub\s+`, "k", "funcname"),
			rule(`\[\]|\*\*|::|<<|>>|>=|<=|<=>|={3}|!=|=~|!~|&&?|\|\||\.{1,3}`, "o"),
			rule(`[()\[\]:;,<>\/?{}]`, "p"),
			ruleF(`(?=\w)`, func(c *rctx) { c.push("name") }),
		)
		l.state("format",
			rule(`\.\n`, "si", "#pop"),
			rule(`.*?\n`, "si"),
		)
		l.state("fat_comma",
			rule(`#.*`, "c1"),
			rule(`\w+`, "s"),
			rule(`\s+`, ""),
			rule(`=>`, "o", "#pop"),
		)
		l.state("name_common",
			rule(`\w+::`, "nn"),
			rule(`[\w:]+`, "nv", "#pop"),
		)
		l.state("varname",
			rule(`\s+`, ""),
			rule(`[{\[]`, "p", "#pop"),
			rule(`[),]`, "p", "#pop"),
			rule(`[;]`, "p", "#pop"),
			mixin("name_common"),
		)
		l.state("name",
			mixin("name_common"),
			rule(`[A-Z_]+(?=[^a-zA-Z0-9_])`, "no", "#pop"),
			ruleF(`(?=\W)`, func(c *rctx) { c.pop() }),
		)
		l.state("modulename", rule(`(?i)[a-z_]\w*`, "nn", "#pop"))
		l.state("funcname",
			rule(`[a-zA-Z_]\w*[!?]?`, "nf"),
			rule(`\s+`, ""),
			ruleG(`(\([$@%]*\))(\s*)`, toks("p", "")),
			rule(`.*?{`, "p", "#pop"),
			rule(`;`, "p", "#pop"),
		)
		l.state("sq",
			rule(`\\[\\']`, "se"),
			rule(`[^\\']+`, "s1"),
			rule(`'`, "p", "#pop"),
			rule(`\\`, "s1"),
		)
		l.state("dq",
			mixin("string_intp"),
			rule(`\\[\\tnrabefluLUE"$@]`, "se"),
			rule(`\\0\d{2}`, "se"),
			rule(`\\o\{\d+\}`, "se"),
			rule(`\\x\h{2}`, "se"),
			rule(`\\x\{\h+\}`, "se"),
			rule(`\\c.`, "se"),
			rule(`\\N\{[^\}]+\}`, "se"),
			rule(`[^\\"]+?`, "s2"),
			rule(`"`, "p", "#pop"),
			rule(`\\`, "se"),
		)
		l.state("bq",
			mixin("string_intp"),
			rule("\\\\[\\\\tnr`]", "se"),
			rule("[^\\\\`]+?", "sb"),
			rule("`", "p", "#pop"),
		)
		for _, s := range [][3]string{{"cb", `\{`, `\}`}, {"rb", `\(`, `\)`}, {"sb", `\[`, `\]`}, {"lt", `<`, `>`}} {
			open, close := s[1], s[2]
			l.state(s[0]+"_string",
				rule(`\\[`+open+close+`\\]`, "sx"),
				rule(`\\`, "sx"),
				ruleF(open, func(c *rctx) { c.token("sx"); c.push() }),
				rule(close, "sx", "#pop"),
				rule(`[^`+open+close+`\\]+`, "sx"),
			)
		}
		l.state("in_interp",
			rule(`}`, "si", "#pop"),
			rule(`\s+`, ""),
			rule(`(?i)[a-z_]\w*`, "si"),
		)
		l.state("string_intp",
			rule(`[$@][{]`, "si", "in_interp"),
			rule(`(?i)[$@][a-z_]\w*`, "si"),
		)
		l.state("end_part", rule(`(?m).+`, "cp", "#pop"))
		return l
	})
}
