// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import (
	"regexp"
	"strings"
)

// Rouge 5.1 の shell.rb の移植。

const shellKeywords = `if|fi|else|while|do|done|for|then|return|function|select|continue|until|esac|elif|in`

var shellBuiltins = strings.Join(strings.Fields(`
alias bg bind break builtin caller cd command compgen
complete declare dirs disown enable eval exec exit
export false fc fg getopts hash help history jobs let
local logout mapfile popd pushd pwd read readonly set
shift shopt source suspend test time times trap true type
typeset ulimit umask unalias unset wait

cat tac nl od base32 base64 fmt pr fold head tail split csplit
wc sum cksum b2sum md5sum sha1sum sha224sum sha256sum sha384sum
sha512sum sort shuf uniq comm ptx tsort cut paste join tr expand
unexpand ls dir vdir dircolors cp dd install mv rm shred link ln
mkdir mkfifo mknod readlink rmdir unlink chown chgrp chmod touch
df du stat sync truncate echo printf yes expr tee basename dirname
pathchk mktemp realpath pwd stty printenv tty id logname whoami
groups users who date arch nproc uname hostname hostid uptime chcon
runcon chroot env nice nohup stdbuf timeout kill sleep factor numfmt
seq tar grep sudo awk sed gzip gunzip`), "|")

func init() {
	registerRouge("shell", func() *rlexer {
		l := &rlexer{tag: "shell"}
		l.state("basic",
			rule(`#.*$`, "c"),
			rule(`(`+shellKeywords+`)\s*\b`, "k"),
			rule(`case\b`, "k", "case"),
			rule(`(`+shellBuiltins+`)\s*\b(?!(\.|-))`, "nb"),
			rule(`[.](?=\s)`, "nb"),
			ruleG(`(\w+)(=)`, toks("nv", "o")),
			rule(`[\[\]{}()!=>]`, "o"),
			rule(`&&|\|\|`, "o"),
			rule(`<<<`, "o"),
			ruleF(`(<<-?)(\s*)(['"]?)(\\?)(\w+)(\3)`, func(c *rctx) {
				c.groups("o", "", "sh", "sh", "no", "sh")
				c.vars["heredocstr"] = regexp.QuoteMeta(c.group(5))
				c.push("heredoc")
			}),
		)
		l.state("heredoc",
			rule(`\n`, "sh", "heredoc_nl"),
			rule(`[^$\n\\]+`, "sh"),
			mixin("interp"),
			rule(`[$]`, "sh"),
		)
		l.state("heredoc_nl",
			ruleF(`\s*(\w+)\s*\n`, func(c *rctx) {
				if s, _ := c.vars["heredocstr"].(string); c.group(1) == s {
					c.token("no")
					c.pop(2)
				} else {
					c.token("sh")
				}
			}),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("double_quotes",
			rule(`(?:\$#?)?"`, "s2", "#pop"),
			mixin("interp"),
			rule("[^\"`\\\\$]+", "s2"),
		)
		l.state("ansi_string",
			rule(`\\.`, "se"),
			rule(`[^\\']+`, "s1"),
			mixin("single_quotes"),
		)
		l.state("single_quotes",
			rule(`'`, "s1", "#pop"),
			rule(`[^']+`, "s1"),
		)
		l.state("data",
			rule(`\s+`, ""),
			rule(`\\.`, "se"),
			rule(`\$?"`, "s2", "double_quotes"),
			rule(`\$'`, "s1", "ansi_string"),
			rule(`'`, "s1", "single_quotes"),
			rule(`\*`, "k"),
			rule(`;`, "p"),
			rule(`--?[\w-]+`, "nt"),
			rule("[^=\\*\\s{}()$\"'`;\\\\<]+", ""),
			rule(`\d+(?= |\Z)`, "m"),
			rule(`<`, ""),
			mixin("interp"),
		)
		l.state("curly",
			rule(`}`, "k", "#pop"),
			rule(`:-`, "k"),
			rule(`[a-zA-Z0-9_]+`, "nv"),
			rule("[^}:\"`'$]+", "p"),
			mixin("root"),
		)
		l.state("paren_interp",
			rule(`\)`, "si", "#pop"),
			rule(`\(`, "o", "paren_inner"),
			mixin("root"),
		)
		l.state("paren_inner",
			rule(`\(`, "o", "#push"),
			rule(`\)`, "o", "#pop"),
			mixin("root"),
		)
		l.state("curly_interp",
			rule(`[}]`, "si", "#pop"),
			rule(`[{]`, "o", "curly_inner"),
			mixin("root"),
		)
		l.state("curly_inner",
			rule(`[{]`, "o", "#push"),
			rule(`[}]`, "o", "#pop"),
			mixin("root"),
		)
		l.state("math",
			rule(`\)\)`, "k", "#pop"),
			rule(`[-+*/%^|&!]|\*\*|\|\||<<|>>`, "o"),
			rule(`\d+(#\w+)?`, "m"),
			mixin("root"),
		)
		l.state("case",
			rule(`esac\b`, "k", "#pop"),
			rule(`\|`, "p"),
			rule(`\)`, "p", "case_stanza"),
			mixin("root"),
		)
		l.state("case_stanza",
			rule(`;;`, "p", "#pop"),
			mixin("root"),
		)
		l.state("backticks",
			rule("`", "sb", "#pop"),
			mixin("root"),
		)
		l.state("interp",
			rule(`\\$`, "se"),
			rule(`\\.`, "se"),
			rule(`\$\(\(`, "k", "math"),
			rule(`\$[(]`, "si", "paren_interp"),
			// https://www.gnu.org/software/bash/manual/bash.html#Command-Substitution-1
			rule(`\$[{][\s|]`, "si", "curly_interp"),
			rule(`\$\{#?`, "k", "curly"),
			rule("`", "sb", "backticks"),
			rule(`\$#?(\w+|.)`, "nv"),
			rule(`\$[*@]`, "nv"),
		)
		l.state("root",
			mixin("basic"),
			mixin("data"),
		)
		return l
	})
}
