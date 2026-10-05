// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from Rouge (https://github.com/rouge-ruby/rouge),
// Copyright (c) 2012 Jeanine Adkisson and contributors, MIT License.

package highlight

// Rouge 5.1 の java.rb / go.rb の移植。

func init() {
	registerRouge("java", func() *rlexer {
		const (
			keywords     = `assert|break|case|catch|continue|default|do|else|finally|for|if|goto|instanceof|new|return|switch|this|throw|try|while|yield|when`
			declarations = `abstract|const|extends|final|implements|native|permits|private|protected|public|sealed|static|strictfp|super|synchronized|throws|transient|volatile`
			types        = `boolean|byte|char|double|float|int|long|short|var|void`
			id           = `[[:alpha:]_][[:word:]]*`
			constName    = `[\p{Lu}][\p{Lu}0-9_]*\b`
			className    = `[\p{Lu}][\p{L}\p{M}\p{Nd}]*\b`
			digit        = `(?:[0-9]_+[0-9]|[0-9])`
			binDigit     = `(?:[01]_+[01]|[01])`
			octDigit     = `(?:[0-7]_+[0-7]|[0-7])`
			hexDigit     = `(?i:[0-9a-f]_+[0-9a-f]|[0-9a-f])`
		)
		l := &rlexer{tag: "java"}
		l.state("root",
			rule(`[^\S\n]+`, ""),
			rule(`//.*?$`, "c1"),
			rule(`(?m)/\*.*?\*/`, "cm"),
			rule(`(?:`+keywords+`)\b`, "k"),
			ruleF(`(?mx)
          (\s*(?:[a-zA-Z_][a-zA-Z0-9_.\[\]<>]*\s+)+?) # return arguments
          ([a-zA-Z_][a-zA-Z0-9_]*)                  # method name
          (\s*)(\()                                 # signature start
        `, func(c *rctx) {
				m1, m2, m3, m4 := c.group(1), c.group(2), c.group(3), c.group(4)
				c.delegateFresh("java", m1)
				c.token("nf", m2)
				c.token("", m3)
				c.token("o", m4)
			}),
			rule(`non-sealed\b`, "kd"),
			rule(`@interface\b`, "kd", "class"),
			rule(`@`+id, "nd"),
			rule(`(?:`+declarations+`)\b`, "kd"),
			rule(`(?:`+types+`)\b`, "kt"),
			rule(`(?:true|false|null)\b`, "kc"),
			rule(`(?:class|enum|interface|record)\b`, "kd", "class"),
			rule(`(?:import(?:\s+(?:static|module))?|package)\b`, "kn", "import"),
			rule(`(?m)"""\s*\n.*?(?<!\\)"""`, "sh"),
			rule(`"(\\\\|\\"|[^"])*"`, "s"),
			rule(`'(?:\\.|[^\\]|\\u[0-9a-f]{4})'`, "sc"),
			ruleG(`(\.)(`+id+`)`, toks("o", "na")),
			rule(id+`:`, "nl"),
			rule(constName, "no"),
			rule(className, "nc"),
			rule(`\$?`+id, "n"),
			rule(`[~^*!%&\[\](){}<>\|+=:;,.\/?-]`, "o"),
			rule(digit+`+\.`+digit+`+([eE]`+digit+`+)?[fd]?`, "mf"),
			rule(`(?i)0b`+binDigit+`+`, "mb"),
			rule(`(?i)0x`+hexDigit+`+`, "mh"),
			rule(`0`+octDigit+`+`, "mo"),
			rule(digit+`+L?`, "mi"),
			rule(`\n`, ""),
		)
		l.state("class",
			rule(`(?m)\s+`, ""),
			rule(id, "nc", "#pop"),
		)
		l.state("import",
			rule(`(?m)\s+`, ""),
			rule(`(?i)[a-z0-9_.]+\*?`, "nn", "#pop"),
		)
		return l
	})

	registerRouge("go", func() *rlexer {
		const (
			letter         = `(?:[[:alpha:]]|_)`
			lineComment    = `\/\/(?:(?!\n).)*`
			generalComment = `(?s:\/\*(?:(?!\*\/).)*\*\/)`
			comment        = `(?:` + lineComment + `|` + generalComment + `)`
			keyword        = `(?:\b(?:break|default|func|interface|select|case|defer|go|map|struct|chan|else|goto|package|switch|const|fallthrough|if|range|type|continue|for|import|return|var)\b)`
			identifier     = `(?:(?!` + keyword + `)` + letter + `(?:` + letter + `|[[:digit:]])*)`
			operator       = `(?:\+=|\+\+|\+|&\^=|&\^|&=|&&|&|==|=|\!=|\!|-=|--|-|\|=|\|\||\||<=|<-|<<=|<<|<|\*=|\*|\^=|\^|>>=|>>|>=|>|\/|\/=|:=|%|%=|\.\.\.|\.|:)`
			separator      = `(?:\(|\)|\[|\]|\{|\}|,|;)`
			decimalLit     = `(?:[0-9](?:_?[0-9])*)`
			binaryLit      = `(?:0[bB]_*[01](?:_?[01])*)`
			octalLit       = `(?:0[oO]?_*[0-7](?:_?[0-7])*)`
			hexLit         = `(?:0[xX]_*[0-9A-Fa-f](?:_?[0-9A-Fa-f])*)`
			intLit         = `(?:` + binaryLit + `|` + hexLit + `|` + octalLit + `|` + decimalLit + `)`
			decimals       = decimalLit
			exponent       = `(?:[eE][+\-]?` + decimals + `)`
			floatLit       = `(?:` + decimals + `\.` + decimals + `?` + exponent + `?|` + decimals + exponent + `|\.` + decimals + exponent + `?)`
			imaginaryLit   = `(?:(?:` + decimals + `|` + floatLit + `)i)`
			escapedChar    = `(?:\\[abfnrtv\\'"])`
			littleU        = `(?:\\u[0-9A-Fa-f]{4})`
			bigU           = `(?:\\U[0-9A-Fa-f]{8})`
			unicodeValue   = `(?:[^\n]|` + littleU + `|` + bigU + `|` + escapedChar + `)`
			octalByte      = `(?:\\[0-7]{3})`
			hexByte        = `(?:\\x[0-9A-Fa-f]{2})`
			byteValue      = `(?:` + octalByte + `|` + hexByte + `)`
			charLit        = `(?:'(?:` + unicodeValue + `|` + byteValue + `)')`
			escapeSequence = `(?:` + escapedChar + `|` + littleU + `|` + bigU + `|` + hexByte + `)`
			predeclTypes   = `(?:\b(?:bool|byte|complex64|complex128|error|float32|float64|int8|int16|int32|int64|int|rune|string|uint8|uint16|uint32|uint64|uintptr|uint)\b)`
			predeclConsts  = `\b(?:true|false|iota|nil)\b`
			predeclFuncs   = `(?:\b(?:append|cap|close|complex|copy|delete|imag|len|make|new|panic|print|println|real|recover)\b)`
		)
		l := &rlexer{tag: "go"}
		l.state("simple_tokens",
			rule(comment, "c"),
			rule(keyword, "k"),
			rule(predeclTypes, "kt"),
			rule(predeclFuncs, "nb"),
			rule(predeclConsts, "no"),
			rule(imaginaryLit, "m"),
			rule(floatLit, "m"),
			rule(intLit, "m"),
			rule(charLit, "sc"),
			rule(operator, "o"),
			rule(separator, "p"),
			rule(identifier, "n"),
			rule(`\s+`, ""),
		)
		l.state("root",
			mixin("simple_tokens"),
			rule("`", "s", "raw_string"),
			rule(`"`, "s", "interpreted_string"),
		)
		l.state("interpreted_string",
			rule(escapeSequence, "se"),
			rule(`\\.`, "err"),
			rule(`"`, "s", "#pop"),
			rule(`[^"\\]+`, "s"),
		)
		l.state("raw_string",
			rule("`", "s", "#pop"),
			rule("(?m)[^`]+", "s"),
		)
		return l
	})
}
