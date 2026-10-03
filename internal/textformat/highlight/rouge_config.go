// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

import "strings"

// Rouge 4.7 の csharp.rb / ini.rb / properties.rb / conf.rb の移植。

func init() {
	registerRouge("csharp", func() *rlexer {
		const id = `@?[_\p{Lu}\p{Ll}\p{Lt}\p{Lm}\p{Nl}][\p{Lu}\p{Ll}\p{Lt}\p{Lm}\p{Nl}\p{Nd}\p{Pc}\p{Cf}\p{Mn}\p{Mc}]*`
		keywords := strings.Join(strings.Fields(`abstract add alias and as ascending async await base
break by case catch checked const continue default delegate
descending do else enum equals event explicit extern false
finally fixed for foreach from get global goto group
if implicit in init interface internal into is join
let lock nameof new notnull null on operator orderby
out override params partial private protected public readonly
ref remove return sealed set sizeof stackalloc static
switch this throw true try typeof unchecked unsafe
unmanaged value virtual void volatile when where while
with yield`), "|")
		keywordsType := strings.Join(strings.Fields(`bool byte char decimal double dynamic float int long nint nuint
object sbyte short string uint ulong ushort var`), "|")
		cppKeywords := strings.Join(strings.Fields(`if endif else elif define undef line error warning region
endregion pragma nullable`), "|")
		l := &rlexer{tag: "csharp"}
		l.state("whitespace",
			rule(`(?m)\s+`, ""),
			rule(`//.*?$`, "c1"),
			rule(`(?m)/[*].*?[*]/`, "cm"),
		)
		l.state("nest",
			rule(`{`, "p", "nest"),
			rule(`}`, "p", "#pop"),
			mixin("root"),
		)
		l.state("splice_string",
			rule(`\\.`, "s"),
			rule(`{`, "p", "nest"),
			rule(`"|\n`, "s", "#pop"),
			rule(`.`, "s"),
		)
		l.state("splice_literal",
			rule(`""`, "s"),
			rule(`{`, "p", "nest"),
			rule(`"`, "s", "#pop"),
			rule(`.`, "s"),
		)
		l.state("root",
			mixin("whitespace"),
			rule(`[$]\s*"`, "s", "splice_string"),
			rule(`[$]@\s*"`, "s", "splice_literal"),
			rule(`(<\[)\s*(`+id+`:)?`, "k"),
			rule(`\]>`, "k"),
			rule(`[~!%^&*()+=|\[\]{}:;,.<>\/?-]`, "p"),
			rule(`(?m)@"(""|[^"])*"`, "s"),
			rule(`"(\\.|.)*?["\n]`, "s"),
			rule(`'(\\.|.)'`, "sc"),
			rule(`(?i)0b[_01]+[lu]?`, "m"),
			rule(`(?i)0x[_0-9a-f]+[lu]?`, "m"),
			rule(`(?i)[0-9](?:[_0-9]*[0-9])?([.][0-9](?:[_0-9]*[0-9])?)?(e[+-]?[0-9](?:[_0-9]*[0-9])?)?[fldum]?`, "m"),
			rule(`\b(?:class|record|struct|interface)\b`, "k", "class"),
			rule(`\b(?:namespace|using)\b`, "k", "namespace"),
			rule(`^#[ \t]*(`+cppKeywords+`)\b.*?\n`, "cp"),
			rule(`\b(`+keywords+`)\b`, "k"),
			rule(`\b(`+keywordsType+`)\b`, "kt"),
			rule(id+`(?=\s*[(])`, "nf"),
			rule(id, "n"),
		)
		l.state("class",
			mixin("whitespace"),
			rule(id, "nc", "#pop"),
		)
		l.state("namespace",
			mixin("whitespace"),
			rule(`(?=[(])`, "", "#pop"),
			rule(`(`+id+`|[.])+`, "nn", "#pop"),
		)
		return l
	})

	iniLike := func(tag, ident, comment, assign string, withNamespace bool, unicodeEscape bool) *rlexer {
		l := &rlexer{tag: tag}
		ws := "w"
		if tag == "properties" {
			ws = ""
		}
		l.state("basic",
			rule(`\s+`, ws),
			rule(comment, "c"),
			rule(`\\\n`, "se"),
		)
		if tag == "properties" {
			// properties はコメントが先
			l.state("basic",
				rule(comment, "c"),
				rule(`\s+`, ws),
				rule(`\\\n`, "se"),
			)
		}
		root := []rrule{
			mixin("basic"),
			ruleF(`(`+ident+`)(\s*)(`+assign+`)`, func(c *rctx) {
				c.groups("py", ws, "p")
				c.push("value")
			}),
		}
		if withNamespace {
			root = append(root, rule(`\[.*?\]`, "nn"), rule(`(.+?)`, "na"))
		}
		l.state("root", root...)
		l.state("value",
			rule(`\n`, "", "#pop"),
			mixin("basic"),
			rule(`"`, "s", "dq"),
			rule(`'.*?'`, "s"),
			mixin("esc_str"),
			rule(`[^\\\n]+`, "s"),
		)
		l.state("dq",
			rule(`"`, "s", "#pop"),
			mixin("esc_str"),
			rule(`(?m)[^\\"]+`, "s"),
		)
		if unicodeEscape {
			l.state("esc_str",
				rule(`\\u[0-9]{4}`, "se"),
				rule(`(?m)\\.`, "se"),
			)
		} else {
			l.state("esc_str", rule(`(?m)\\.`, "se"))
		}
		return l
	}
	registerRouge("ini", func() *rlexer { return iniLike("ini", `[\w\-.]+`, `[;#].*?\n`, `=`, true, false) })
	registerRouge("properties", func() *rlexer {
		return iniLike("properties", `[\w.-]+`, `[!#].*?\n`, `[=:]`, false, true)
	})

	registerRouge("conf", func() *rlexer {
		l := &rlexer{tag: "conf"}
		l.state("root",
			rule(`#.*?\n`, "c"),
			rule(`".*?"`, "s2"),
			rule(`'.*?'`, "s1"),
			rule(`(?i)[a-z]\w*`, "n"),
			rule(`\d+`, "m"),
			rule(`[^\w#"']+`, ""),
		)
		return l
	})
}
