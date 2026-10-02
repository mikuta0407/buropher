package highlight

import (
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2/v2"
)

// このファイルは Rouge::RegexLexer の動作（状態スタック、mixin、groups、delegate、
// 同種トークンの結合、マッチしない文字の Error 化など）を再現する小さなエンジン。
// 主要言語のレキサーは Rouge のソースからこのエンジン向けに移植している（rouge_*.go）。
//
// 正規表現は Ruby（Onigmo）の記法で書き、rubyRegexp で regexp2 用に変換する。
// StringScanner と同様、現在位置を文字列の先頭とみなして照合する
// （\A・^・後読みは現在位置より前を見ない）。

// rtok は Rouge のトークン（短縮クラス名。"" は Text）。
type rtok = string

type rrule struct {
	re    *regexp2.Regexp
	bol   bool   // パターンが ^ で始まる（行頭でのみ試す）
	mixin string // 空でなければ他の状態を取り込む
	act   func(c *rctx, m *regexp2.Match)
}

type rstate struct {
	name  string
	rules []rrule
}

// rlexer は Rouge の RegexLexer のクラス相当。
type rlexer struct {
	tag    string
	states map[string]*rstate
	start  func(c *rctx) // start ブロック
	init   func(c *rctx) // インスタンス変数の初期化
	// stream は独自の字句解析（PlainText など）を行う場合に設定する
	stream func(c *rctx, text string)
}

// rctx はレキサーのインスタンス（状態スタックなど）。
type rctx struct {
	lx    *rlexer
	stack []*rstate
	runes []rune
	pos   int
	m     *regexp2.Match
	emit  func(tok rtok, val string)
	vars  map[string]any
	subs  map[string]*rctx // delegate 先のレキサー（インスタンスを保持する）
	// budget は字句解析全体の時間制限（異常に遅い正規表現への対策）
	budget *lexBudget
}

// lexBudget は 1 回の字句解析の時間制限。
type lexBudget struct {
	deadline time.Time
	steps    int
	exceeded bool
}

// child は時間制限を引き継いだ別レキサーのインスタンスを作る。
func (c *rctx) child(lx *rlexer) *rctx {
	n := newCtx(lx)
	n.budget = c.budget
	return n
}

func newCtx(lx *rlexer) *rctx {
	c := &rctx{lx: lx, vars: map[string]any{}, subs: map[string]*rctx{}}
	if lx.init != nil {
		lx.init(c)
	}
	c.reset()
	return c
}

func (c *rctx) reset() {
	c.stack = nil
	if root, ok := c.lx.states["root"]; ok {
		c.stack = []*rstate{root}
	}
	if c.lx.start != nil {
		c.lx.start(c)
	}
}

func (c *rctx) state() *rstate { return c.stack[len(c.stack)-1] }

// token は現在のマッチ全体（または値）を出力する。
func (c *rctx) token(tok rtok, val ...string) {
	v := c.m.String()
	if len(val) > 0 {
		v = val[0]
	}
	if v != "" {
		c.emit(tok, v)
	}
}

// groups はキャプチャグループごとにトークンを出力する。
func (c *rctx) groups(toks ...rtok) {
	for i, t := range toks {
		g := c.m.GroupByNumber(i + 1)
		if g != nil && len(g.Captures) > 0 && g.String() != "" {
			c.emit(t, g.String())
		}
	}
}

// group は n 番目のキャプチャ（無ければ ""）。
func (c *rctx) group(n int) string {
	g := c.m.GroupByNumber(n)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return g.String()
}

func (c *rctx) push(name ...string) {
	if len(name) == 0 {
		c.stack = append(c.stack, c.state())
		return
	}
	c.stack = append(c.stack, c.lx.get(name[0]))
}

func (c *rctx) pop(times ...int) {
	n := 1
	if len(times) > 0 {
		n = times[0]
	}
	for i := 0; i < n && len(c.stack) > 0; i++ {
		c.stack = c.stack[:len(c.stack)-1]
	}
}

func (c *rctx) gotoState(name string) {
	c.stack[len(c.stack)-1] = c.lx.get(name)
}

func (c *rctx) resetStack() {
	c.stack = []*rstate{c.lx.get("root")}
}

func (c *rctx) inState(name string) bool {
	for _, s := range c.stack {
		if s.name == name {
			return true
		}
	}
	return false
}

func (c *rctx) isState(name string) bool { return c.state().name == name }

// delegate は他のレキサー（インスタンスを保持）で text を字句解析する。
func (c *rctx) delegate(tag string, text ...string) {
	v := c.m.String()
	if len(text) > 0 {
		v = text[0]
	}
	sub := c.subs[tag]
	if sub == nil {
		lx := rougeLexerByTag(tag)
		if lx == nil {
			c.emit("", v)
			return
		}
		sub = c.child(lx)
		c.subs[tag] = sub
	}
	sub.continueLex(v, c.emit)
}

// recurse は同じレキサーの新しいインスタンスで text を字句解析する（Rouge の recurse）。
func (c *rctx) recurse(text string) {
	saved := c.runes
	savedPos := c.pos
	savedM := c.m
	sub := c.child(c.lx)
	sub.continueLex(text, c.emit)
	c.runes, c.pos, c.m = saved, savedPos, savedM
}

// delegateFresh はクラス指定の delegate（毎回新しいインスタンス）。
func (c *rctx) delegateFresh(tag, text string) {
	lx := rougeLexerByTag(tag)
	if lx == nil {
		c.emit("", text)
		return
	}
	c.child(lx).continueLex(text, c.emit)
}

func (lx *rlexer) get(name string) *rstate {
	s, ok := lx.states[name]
	if !ok {
		panic("rouge: unknown state " + name + " in " + lx.tag)
	}
	return s
}

const maxNullScans = 5

// continueLex は Rouge の continue_lex（stream_tokens と同種トークンの結合）。
func (c *rctx) continueLex(text string, out func(rtok, string)) {
	var lastTok rtok
	var lastVal strings.Builder
	has := false
	emit := func(tok rtok, val string) {
		if val == "" {
			return
		}
		if has && tok == lastTok {
			lastVal.WriteString(val)
			return
		}
		if has {
			out(lastTok, lastVal.String())
		}
		lastTok = tok
		lastVal.Reset()
		lastVal.WriteString(val)
		has = true
	}
	saved := c.emit
	c.emit = emit
	if c.lx.stream != nil {
		c.lx.stream(c, text)
	} else {
		c.streamTokens(text)
	}
	c.emit = saved
	if has {
		out(lastTok, lastVal.String())
	}
}

func (c *rctx) streamTokens(text string) {
	c.runes = []rune(text)
	c.pos = 0
	nullSteps := 0
	for c.pos < len(c.runes) {
		if b := c.budget; b != nil {
			if b.exceeded {
				return
			}
			b.steps++
			if b.steps%256 == 0 && time.Now().After(b.deadline) {
				b.exceeded = true
				return
			}
		}
		if !c.step(c.state(), &nullSteps) {
			c.emit("err", string(c.runes[c.pos]))
			c.pos++
		}
	}
}

func (c *rctx) step(st *rstate, nullSteps *int) bool {
	for _, r := range st.rules {
		if r.mixin != "" {
			if c.step(c.lx.get(r.mixin), nullSteps) {
				return true
			}
			continue
		}
		if r.bol && !(c.pos == 0 || c.runes[c.pos-1] == '\n') {
			continue
		}
		m, err := r.re.FindRunesMatch(c.runes[c.pos:])
		if err != nil || m == nil {
			continue
		}
		size := m.RuneLength
		c.m = m
		c.pos += size
		r.act(c, m)
		if size == 0 {
			*nullSteps++
			if *nullSteps > maxNullScans {
				return false
			}
		} else {
			*nullSteps = 0
		}
		return true
	}
	return false
}

// ---- 定義用の DSL ----

// rule は Rouge の rule re, Token[, next_state]。next は "#pop", "#push", 状態名（複数可）。
func rule(pattern string, tok rtok, next ...string) rrule {
	re, bol := rubyRegexp(pattern)
	return rrule{re: re, bol: bol, act: func(c *rctx, m *regexp2.Match) {
		c.token(tok)
		for _, n := range next {
			switch n {
			case "#pop":
				c.pop()
			case "#push":
				c.push()
			default:
				c.push(n)
			}
		}
	}}
}

// ruleF は Rouge の rule re do |m| ... end。
func ruleF(pattern string, f func(c *rctx)) rrule {
	re, bol := rubyRegexp(pattern)
	return rrule{re: re, bol: bol, act: func(c *rctx, m *regexp2.Match) { f(c) }}
}

// ruleG は rule re do groups(...) end（続けて状態遷移も可能）。
func ruleG(pattern string, toks []rtok, next ...string) rrule {
	re, bol := rubyRegexp(pattern)
	return rrule{re: re, bol: bol, act: func(c *rctx, m *regexp2.Match) {
		c.groups(toks...)
		for _, n := range next {
			switch n {
			case "#pop":
				c.pop()
			case "#push":
				c.push()
			default:
				c.push(n)
			}
		}
	}}
}

func mixin(name string) rrule { return rrule{mixin: name} }

func toks(t ...rtok) []rtok { return t }

func (lx *rlexer) state(name string, rules ...rrule) {
	if lx.states == nil {
		lx.states = map[string]*rstate{}
	}
	lx.states[name] = &rstate{name: name, rules: rules}
}

// prependRules / appendRules は Rouge の prepend / append（サブクラスでの状態拡張）。
func (lx *rlexer) prependRules(name string, rules ...rrule) {
	s := lx.get(name)
	s.rules = append(append([]rrule{}, rules...), s.rules...)
}

func (lx *rlexer) appendRules(name string, rules ...rrule) {
	s := lx.get(name)
	s.rules = append(s.rules, rules...)
}

// clone は状態を複製したレキサーを返す（Rouge のサブクラス化）。
func (lx *rlexer) clone(tag string) *rlexer {
	n := &rlexer{tag: tag, states: map[string]*rstate{}, start: lx.start, init: lx.init, stream: lx.stream}
	for k, v := range lx.states {
		n.states[k] = &rstate{name: v.name, rules: append([]rrule{}, v.rules...)}
	}
	return n
}

// ---- 正規表現の変換 ----

const (
	rxWord  = `a-zA-Z0-9_`
	rxSpace = `\x20\t\n\v\f\r`
	rxDigit = `0-9`
	rxHex   = `0-9a-fA-F`
)

var rxCache sync.Map

// rubyRegexp は Ruby の正規表現を regexp2 用に変換してコンパイルする。
// 先頭に (?flags) を付けられる（i, x, m=dotall）。
func rubyRegexp(pattern string) (*regexp2.Regexp, bool) {
	if v, ok := rxCache.Load(pattern); ok {
		e := v.(rxEntry)
		return e.re, e.bol
	}
	opts := regexp2.Multiline
	p := pattern
	// 先頭のフラグ指定（Ruby の /imx に相当）
	if strings.HasPrefix(p, "(?") {
		end := strings.IndexByte(p, ')')
		flags := p[2:end]
		if strings.Trim(flags, "imx") == "" {
			for _, f := range flags {
				switch f {
				case 'i':
					opts |= regexp2.IgnoreCase
				case 'x':
					opts |= regexp2.IgnorePatternWhitespace
				case 'm':
					opts |= regexp2.Singleline
				}
			}
			p = p[end+1:]
		}
	}
	bol := strings.HasPrefix(p, "^")
	conv := translateRegexp(p)
	re := regexp2.MustCompile(`\A(?:`+conv+`)`, opts)
	re.MatchTimeout = regexTimeout
	rxCache.Store(pattern, rxEntry{re, bol})
	return re, bol
}

type rxEntry struct {
	re  *regexp2.Regexp
	bol bool
}

// translateRegexp は \h \w \d \s \b などを Ruby の（ASCII の）意味に置き換える。
func translateRegexp(p string) string {
	var sb strings.Builder
	inClass := 0
	rs := []rune(p)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			n := rs[i+1]
			i++
			cls := ""
			neg := false
			switch n {
			case 'w':
				cls = rxWord
			case 'W':
				cls, neg = rxWord, true
			case 's':
				cls = rxSpace
			case 'S':
				cls, neg = rxSpace, true
			case 'd':
				cls = rxDigit
			case 'D':
				cls, neg = rxDigit, true
			case 'h':
				cls = rxHex
			case 'H':
				cls, neg = rxHex, true
			}
			if cls != "" {
				if inClass > 0 {
					if neg {
						// クラス内の否定は表現できないため Unicode 版で近似する
						sb.WriteRune('\\')
						sb.WriteRune(n)
					} else {
						sb.WriteString(cls)
					}
				} else if neg {
					sb.WriteString("[^" + cls + "]")
				} else {
					sb.WriteString("[" + cls + "]")
				}
				continue
			}
			if n == 'b' && inClass == 0 {
				sb.WriteString(`(?:(?<=[` + rxWord + `])(?![` + rxWord + `])|(?<![` + rxWord + `])(?=[` + rxWord + `]))`)
				continue
			}
			if n == 'B' && inClass == 0 {
				sb.WriteString(`(?:(?<=[` + rxWord + `])(?=[` + rxWord + `])|(?<![` + rxWord + `])(?![` + rxWord + `]))`)
				continue
			}
			sb.WriteRune('\\')
			sb.WriteRune(n)
		case c == '[':
			if inClass > 0 && i+1 < len(rs) && rs[i+1] == ':' {
				// POSIX ブラケット [[:alpha:]] など
				end := strings.Index(string(rs[i:]), ":]")
				if end > 0 {
					name := string(rs[i+2 : i+end])
					sb.WriteString(posixClass(name))
					i += end + 1
					continue
				}
			}
			inClass++
			sb.WriteRune(c)
			if i+1 < len(rs) && rs[i+1] == '^' {
				sb.WriteRune('^')
				i++
			}
			// クラス先頭の ']' はリテラル
			if i+1 < len(rs) && rs[i+1] == ']' {
				sb.WriteString(`\]`)
				i++
			}
		case c == ']' && inClass > 0:
			inClass--
			sb.WriteRune(c)
		case c == '{' && inClass == 0 && i+2 < len(rs) && rs[i+1] == ',' && rs[i+2] >= '0' && rs[i+2] <= '9':
			// Ruby の {,n} は {0,n}
			sb.WriteString("{0")
		default:
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func posixClass(name string) string {
	neg := strings.HasPrefix(name, "^")
	name = strings.TrimPrefix(name, "^")
	var cls string
	switch name {
	case "alpha":
		cls = `\p{L}\p{M}`
	case "alnum":
		cls = `\p{L}\p{M}\p{Nd}`
	case "digit":
		cls = `0-9`
	case "upper":
		cls = `\p{Lu}`
	case "lower":
		cls = `\p{Ll}`
	case "space":
		cls = `\s`
	case "word":
		cls = `\w`
	case "punct":
		cls = `\p{P}`
	case "xdigit":
		cls = `0-9a-fA-F`
	default:
		cls = `\w`
	}
	if neg {
		// 否定の POSIX クラスはクラス内で表現しにくいため近似
		return cls
	}
	return cls
}

// ---- レキサーの登録 ----

var (
	rougeImplsMu sync.Mutex
	rougeImpls   = map[string]func() *rlexer{}
	rougeBuilt   = map[string]*rlexer{}
)

// registerRouge は移植済みレキサーを登録する（タグ単位）。
func registerRouge(tag string, build func() *rlexer) {
	rougeImpls[tag] = build
}

func rougeLexerByTag(tag string) *rlexer {
	rougeImplsMu.Lock()
	defer rougeImplsMu.Unlock()
	if lx, ok := rougeBuilt[tag]; ok {
		return lx
	}
	build, ok := rougeImpls[tag]
	if !ok {
		return nil
	}
	lx := build()
	rougeBuilt[tag] = lx
	return lx
}

// rougeTokens は移植済みレキサーで字句解析する（未移植なら ok=false）。
func rougeTokens(text, tag string) ([][2]string, bool) {
	lx := rougeLexerByTag(tag)
	if lx == nil {
		return nil, false
	}
	c := newCtx(lx)
	c.budget = &lexBudget{deadline: time.Now().Add(lexTimeout)}
	var out [][2]string
	c.continueLex(text, func(t rtok, v string) {
		out = append(out, [2]string{t, v})
	})
	if c.budget.exceeded {
		// 時間切れ: Redmine の例外時と同様に装飾なしのテキストにする
		return [][2]string{{"", text}}, true
	}
	return out, true
}

const (
	// lexTimeout は 1 回のハイライト全体の上限時間。
	lexTimeout = 3 * time.Second
	// regexTimeout は 1 回の正規表現照合の上限時間。
	regexTimeout = 500 * time.Millisecond
)
