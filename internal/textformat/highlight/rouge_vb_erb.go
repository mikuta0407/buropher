package highlight

// Rouge 4.7 の vb.rb / erb.rb の移植。

var (
	vbKeywords = wordset(`AddHandler Alias ByRef ByVal CBool CByte CChar CDate CDbl CDec
CInt CLng CObj CSByte CShort CSng CStr CType CUInt CULng CUShort
Call Case Catch Class Const Continue Declare Default Delegate
Dim DirectCast Do Each Else ElseIf End EndIf Enum Erase Error
Event Exit False Finally For Friend Function Get Global GoSub
GoTo Handles If Implements Imports Inherits Interface Let
Lib Loop Me Module MustInherit MustOverride MyBase MyClass
Namespace Narrowing New Next Not NotInheritable NotOverridable
Nothing Of On Operator Option Optional Overloads Overridable
Overrides ParamArray Partial Private Property Protected Public
RaiseEvent ReDim ReadOnly RemoveHandler Resume Return Select Set
Shadows Shared Single Static Step Stop Structure Sub SyncLock
Then Throw To True Try TryCast Using Wend When While Widening
With WithEvents WriteOnly`)
	vbKeywordsType = wordset(`Boolean Byte Char Date Decimal Double Integer Long Object
SByte Short Single String Variant UInteger ULong UShort`)
	vbOperatorWords = wordset(`AddressOf And AndAlso As GetType In Is IsNot Like Mod Or OrElse
TypeOf Xor`)
	vbBuiltins = wordset(`Console ConsoleColor`)
)

func init() {
	registerRouge("vb", func() *rlexer {
		const (
			id      = `(?i:[a-z_]\w*)`
			upperID = `[A-Z]\w*`
		)
		l := &rlexer{tag: "vb"}
		l.state("whitespace",
			rule(`\s+`, ""),
			rule(`\n`, "", "bol"),
			rule(`(?i)rem\b.*?$`, "c1"),
			rule(`(?m)%\{.*?%\}`, "cm"),
			rule(`'.*$`, "c1"),
		)
		l.state("bol",
			rule(`\s+`, ""),
			rule(`<.*?>`, "na"),
			// Rouge の実装は { :pop! }（シンボルを返すだけで pop しない）
			ruleF(``, func(c *rctx) {}),
		)
		l.state("root",
			mixin("whitespace"),
			rule(`(?x)
            [#]If\b .*? \bThen
          | [#]ElseIf\b .*? \bThen
          | [#]End \s+ If
          | [#]Const
          | [#]ExternalSource .*? \n
          | [#]End \s+ ExternalSource
          | [#]Region .*? \n
          | [#]End \s+ Region
          | [#]ExternalChecksum
        `, "cp"),
			rule(`[.]`, "p", "dotted"),
			rule(`[(){}!#,:]`, "p"),
			rule(`Option\s+(Strict|Explicit|Compare)\s+(On|Off|Binary|Text)`, "kd"),
			rule(`End\b`, "k", "end"),
			rule(`(Dim|Const)\b`, "k", "dim"),
			rule(`(Function|Sub|Property)\b`, "k", "funcname"),
			rule(`(Class|Structure|Enum)\b`, "k", "classname"),
			rule(`(Module|Namespace|Imports)\b`, "k", "namespace"),
			ruleF(upperID, func(c *rctx) {
				w := c.m.String()
				switch {
				case vbKeywords[w]:
					c.token("k")
				case vbKeywordsType[w]:
					c.token("kt")
				case vbOperatorWords[w]:
					c.token("ow")
				case vbBuiltins[w]:
					c.token("nb")
				default:
					c.token("n")
				}
			}),
			rule(`&=|[*]=|/=|\\=|\^=|\+=|-=|<<=|>>=|<<|>>|:=|<=|>=|<>|[-&*/\\^+=<>.]`, "o"),
			rule(`"`, "s", "string"),
			rule(id+`[%&@!#\$]?`, "n"),
			rule(`#.*?#`, "ld"),
			rule(`(?i)(\d+\.\d*|\d*\.\d+)(f[+-]?\d+)?`, "mf"),
			rule(`\d+([SILDFR]|US|UI|UL)?`, "mi"),
			rule(`&H[0-9a-f]+([SILDFR]|US|UI|UL)?`, "mi"),
			rule(`&O[0-7]+([SILDFR]|US|UI|UL)?`, "mi"),
			rule(`_\n`, "k"),
		)
		l.state("dotted", mixin("whitespace"), rule(id, "n", "#pop"))
		l.state("string",
			rule(`""`, "se"),
			rule(`"C?`, "s", "#pop"),
			rule(`[^"]+`, "s"),
		)
		l.state("dim",
			mixin("whitespace"),
			rule(id, "nv", "#pop"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		l.state("funcname", mixin("whitespace"), rule(id, "nf", "#pop"))
		l.state("classname", mixin("whitespace"), rule(id, "nc", "#pop"))
		l.state("namespace", mixin("whitespace"), rule(id+`([.]`+id+`)*`, "nn", "#pop"))
		l.state("end",
			mixin("whitespace"),
			rule(`(Function|Sub|Property|Class|Structure|Enum|Module|Namespace)\b`, "k", "#pop"),
			ruleF(``, func(c *rctx) { c.pop() }),
		)
		return l
	})

	registerRouge("erb", func() *rlexer {
		const (
			open  = `(?:<%%|<%=|<%#|<%-|<%)`
			close = `(?:%%>|-%>|%>)`
		)
		l := &rlexer{tag: "erb"}
		l.start = func(c *rctx) {
			if s := c.sub("html"); s != nil {
				s.reset()
			}
			if s := c.sub("ruby"); s != nil {
				s.reset()
			}
		}
		l.state("root",
			rule(`<%#`, "c", "comment"),
			rule(open, "cp", "ruby"),
			ruleF(`(?m).+?(?=`+open+`)|.+`, func(c *rctx) { c.delegate("html") }),
		)
		l.state("comment",
			rule(close, "c", "#pop"),
			rule(`(?m).+?(?=`+close+`)|.+`, "c"),
		)
		l.state("ruby",
			rule(close, "cp", "#pop"),
			ruleF(`(?m).+?(?=`+close+`)|.+`, func(c *rctx) { c.delegate("ruby") }),
		)
		return l
	})
}
