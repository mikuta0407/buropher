package sanitize

import (
	"strings"
	"unicode/utf8"
)

// このファイルは Sanitize::CSS#properties（Crass による CSS 宣言リストの解析と
// 許可プロパティの抽出）を移植したもの。style 属性の値を整形する。

type cssTokType int

const (
	ctWhitespace cssTokType = iota
	ctString
	ctBadString
	ctHash
	ctDelim
	ctLParen
	ctRParen
	ctLBracket
	ctRBracket
	ctLBrace
	ctRBrace
	ctComma
	ctColon
	ctSemicolon
	ctCDO
	ctCDC
	ctAtKeyword
	ctIdent
	ctFunction
	ctURL
	ctBadURL
	ctNumber
	ctPercentage
	ctDimension
	ctUnicodeRange
	ctMatch // ~= |= ^= $= *= ||
)

type cssToken struct {
	typ   cssTokType
	raw   string
	value string // ident/function/url/string の値
}

type cssTokenizer struct {
	s   []rune
	pos int
}

func (t *cssTokenizer) eos() bool { return t.pos >= len(t.s) }

func (t *cssTokenizer) peekAt(i int) rune {
	if t.pos+i < len(t.s) && t.pos+i >= 0 {
		return t.s[t.pos+i]
	}
	return 0
}

func (t *cssTokenizer) peekStr(n int) []rune {
	end := t.pos + n
	if end > len(t.s) {
		end = len(t.s)
	}
	return t.s[t.pos:end]
}

func isCSSWhitespace(r rune) bool { return r == '\n' || r == '\t' || r == ' ' }

func isCSSDigit(r rune) bool { return r >= '0' && r <= '9' }

func isCSSHex(r rune) bool {
	return isCSSDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isNameStart(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || r >= 0x80
}

func isNameChar(r rune) bool { return isNameStart(r) || isCSSDigit(r) || r == '-' }

func isNonPrintable(r rune) bool {
	return (r >= 0 && r <= 8) || r == 0x0b || (r >= 0x0e && r <= 0x1f) || r == 0x7f
}


// validEscapeText は Crass の valid_escape?（2 文字目が無い場合も "\" 単独なら true）。
func validEscapeText(text []rune) bool {
	if len(text) == 0 || text[0] != '\\' {
		return false
	}
	if len(text) < 2 {
		return true
	}
	return text[1] != '\n'
}

func startIdentifier(text []rune) bool {
	if len(text) == 0 {
		return false
	}
	switch {
	case text[0] == '-':
		if len(text) < 2 {
			return false
		}
		return text[1] == '-' || isNameStart(text[1]) || validEscapeText(text[1:min(3, len(text))])
	case isNameStart(text[0]):
		return true
	case text[0] == '\\':
		return validEscapeText(text[:min(2, len(text))])
	}
	return false
}

func startNumber(text []rune) bool {
	if len(text) == 0 {
		return false
	}
	switch {
	case text[0] == '+' || text[0] == '-':
		if len(text) < 2 {
			return false
		}
		return isCSSDigit(text[1]) || (text[1] == '.' && len(text) > 2 && isCSSDigit(text[2]))
	case text[0] == '.':
		return len(text) > 1 && isCSSDigit(text[1])
	case isCSSDigit(text[0]):
		return true
	}
	return false
}

// current+peek(n) 相当（直前に消費した文字を先頭に含む）。
func (t *cssTokenizer) fromCurrent(n int) []rune {
	start := t.pos - 1
	end := start + n
	if end > len(t.s) {
		end = len(t.s)
	}
	if start < 0 {
		start = 0
	}
	return t.s[start:end]
}

func (t *cssTokenizer) consumeEscaped() string {
	if t.eos() {
		return "�"
	}
	start := t.pos
	for t.pos < len(t.s) && t.pos-start < 6 && isCSSHex(t.s[t.pos]) {
		t.pos++
	}
	if t.pos > start {
		hex := string(t.s[start:t.pos])
		if isCSSWhitespace(t.peekAt(0)) && !t.eos() {
			t.pos++
		}
		cp := 0
		for _, c := range hex {
			cp *= 16
			switch {
			case c >= '0' && c <= '9':
				cp += int(c - '0')
			case c >= 'a' && c <= 'f':
				cp += int(c-'a') + 10
			default:
				cp += int(c-'A') + 10
			}
		}
		if cp == 0 || (cp >= 0xD800 && cp <= 0xDFFF) || cp > 0x10FFFF {
			return "�"
		}
		return string(rune(cp))
	}
	c := t.s[t.pos]
	t.pos++
	return string(c)
}

func (t *cssTokenizer) consumeName() string {
	var sb strings.Builder
	for !t.eos() {
		c := t.s[t.pos]
		if isNameChar(c) {
			sb.WriteRune(c)
			t.pos++
			continue
		}
		t.pos++
		if validEscapeText(t.fromCurrent(2)) {
			sb.WriteString(t.consumeEscaped())
		} else {
			t.pos--
			return sb.String()
		}
	}
	return sb.String()
}

func (t *cssTokenizer) consumeNumber() {
	if c := t.peekAt(0); c == '+' || c == '-' {
		t.pos++
	}
	for isCSSDigit(t.peekAt(0)) {
		t.pos++
	}
	if t.peekAt(0) == '.' && isCSSDigit(t.peekAt(1)) {
		t.pos++
		for isCSSDigit(t.peekAt(0)) {
			t.pos++
		}
	}
	if c := t.peekAt(0); c == 'e' || c == 'E' {
		i := 1
		if s := t.peekAt(1); s == '+' || s == '-' {
			i = 2
		}
		if isCSSDigit(t.peekAt(i)) {
			t.pos += i
			for isCSSDigit(t.peekAt(0)) {
				t.pos++
			}
		}
	}
}

func (t *cssTokenizer) consumeNumeric() cssTokType {
	t.consumeNumber()
	if startIdentifier(t.peekStr(3)) {
		t.consumeName()
		return ctDimension
	}
	if t.peekAt(0) == '%' && !t.eos() {
		t.pos++
		return ctPercentage
	}
	return ctNumber
}

func (t *cssTokenizer) consumeString(ending rune) (cssTokType, string) {
	var sb strings.Builder
	for !t.eos() {
		c := t.s[t.pos]
		t.pos++
		switch {
		case c == ending:
			return ctString, sb.String()
		case c == '\n':
			t.pos--
			return ctBadString, sb.String()
		case c == '\\':
			if t.eos() {
				continue
			}
			if t.peekAt(0) == '\n' {
				t.pos++
			} else {
				sb.WriteString(t.consumeEscaped())
			}
		default:
			sb.WriteRune(c)
		}
	}
	return ctString, sb.String()
}

func (t *cssTokenizer) consumeBadURL() {
	for !t.eos() {
		if validEscapeText(t.peekStr(2)) && t.peekAt(0) == '\\' {
			t.pos++
			t.consumeEscaped()
			continue
		}
		c := t.s[t.pos]
		t.pos++
		if c == ')' {
			break
		}
	}
}

func (t *cssTokenizer) consumeURL() (cssTokType, string) {
	var sb strings.Builder
	for isCSSWhitespace(t.peekAt(0)) && !t.eos() {
		t.pos++
	}
	for !t.eos() {
		c := t.s[t.pos]
		t.pos++
		switch {
		case c == ')':
			return ctURL, sb.String()
		case isCSSWhitespace(c):
			for isCSSWhitespace(t.peekAt(0)) && !t.eos() {
				t.pos++
			}
			if t.eos() || t.peekAt(0) == ')' {
				if !t.eos() {
					t.pos++
				}
				return ctURL, sb.String()
			}
			t.consumeBadURL()
			return ctBadURL, ""
		case c == '"' || c == '\'' || c == '(' || isNonPrintable(c):
			t.consumeBadURL()
			return ctBadURL, ""
		case c == '\\':
			if validEscapeText(t.fromCurrent(2)) {
				sb.WriteString(t.consumeEscaped())
			} else {
				t.consumeBadURL()
				return ctBadURL, ""
			}
		default:
			sb.WriteRune(c)
		}
	}
	return ctURL, sb.String()
}

func (t *cssTokenizer) consumeIdentLike() (cssTokType, string) {
	value := t.consumeName()
	if t.peekAt(0) == '(' && !t.eos() {
		t.pos++
		if strings.EqualFold(value, "url") {
			for {
				p := t.peekStr(2)
				allWS := len(p) > 0
				for _, c := range p {
					if !isCSSWhitespace(c) {
						allWS = false
					}
				}
				if !allWS {
					break
				}
				t.pos++
			}
			p := t.peekStr(2)
			quoted := false
			if len(p) > 0 && (p[0] == '"' || p[0] == '\'') {
				quoted = true
			} else if len(p) > 1 && isCSSWhitespace(p[0]) && (p[1] == '"' || p[1] == '\'') {
				quoted = true
			}
			if quoted {
				return ctFunction, value
			}
			return t.consumeURL()
		}
		return ctFunction, value
	}
	return ctIdent, value
}

// next はコメントを読み飛ばして次のトークンを返す（コメントは保持しない）。
func (t *cssTokenizer) next() (cssToken, bool) {
	var mark int
	for {
		if t.eos() {
			return cssToken{}, false
		}
		mark = t.pos
		if t.peekAt(0) == '/' && t.peekAt(1) == '*' {
			t.pos += 2
			found := false
			for t.pos+1 < len(t.s) {
				if t.s[t.pos] == '*' && t.s[t.pos+1] == '/' {
					t.pos += 2
					found = true
					break
				}
				t.pos++
			}
			if !found {
				t.pos = len(t.s)
			}
			continue
		}
		break
	}
	tok := func(typ cssTokType, value string) (cssToken, bool) {
		return cssToken{typ: typ, raw: string(t.s[mark:t.pos]), value: value}, true
	}
	if isCSSWhitespace(t.peekAt(0)) {
		for !t.eos() && isCSSWhitespace(t.peekAt(0)) {
			t.pos++
		}
		return tok(ctWhitespace, "")
	}
	c := t.s[t.pos]
	t.pos++
	switch c {
	case '"', '\'':
		typ, v := t.consumeString(c)
		return tok(typ, v)
	case '#':
		if isNameChar(t.peekAt(0)) && !t.eos() || validEscapeText(t.peekStr(2)) {
			v := t.consumeName()
			return tok(ctHash, v)
		}
		return tok(ctDelim, "#")
	case '$', '^', '~', '*':
		if t.peekAt(0) == '=' && !t.eos() {
			t.pos++
			return tok(ctMatch, "")
		}
		return tok(ctDelim, string(c))
	case '(':
		return tok(ctLParen, "")
	case ')':
		return tok(ctRParen, "")
	case '+':
		if startNumber(t.fromCurrent(3)) {
			t.pos--
			return tok(t.consumeNumeric(), "")
		}
		return tok(ctDelim, "+")
	case ',':
		return tok(ctComma, "")
	case '-':
		next3 := t.fromCurrent(3)
		switch {
		case startNumber(next3):
			t.pos--
			return tok(t.consumeNumeric(), "")
		case t.peekAt(0) == '-' && t.peekAt(1) == '>' && t.pos+1 < len(t.s):
			t.pos += 2
			return tok(ctCDC, "")
		case startIdentifier(next3):
			t.pos--
			typ, v := t.consumeIdentLike()
			return tok(typ, v)
		}
		return tok(ctDelim, "-")
	case '.':
		if startNumber(t.fromCurrent(3)) {
			t.pos--
			return tok(t.consumeNumeric(), "")
		}
		return tok(ctDelim, ".")
	case ':':
		return tok(ctColon, "")
	case ';':
		return tok(ctSemicolon, "")
	case '<':
		if string(t.peekStr(3)) == "!--" {
			t.pos += 3
			return tok(ctCDO, "")
		}
		return tok(ctDelim, "<")
	case '@':
		if startIdentifier(t.peekStr(3)) {
			v := t.consumeName()
			return tok(ctAtKeyword, v)
		}
		return tok(ctDelim, "@")
	case '[':
		return tok(ctLBracket, "")
	case '\\':
		if validEscapeText(t.fromCurrent(2)) {
			t.pos--
			typ, v := t.consumeIdentLike()
			return tok(typ, v)
		}
		return tok(ctDelim, "\\")
	case ']':
		return tok(ctRBracket, "")
	case '{':
		return tok(ctLBrace, "")
	case '}':
		return tok(ctRBrace, "")
	case 'U', 'u':
		if p := t.peekStr(2); len(p) == 2 && p[0] == '+' && (isCSSHex(p[1]) || p[1] == '?') {
			t.pos++
			n := 0
			for n < 6 && isCSSHex(t.peekAt(0)) && !t.eos() {
				t.pos++
				n++
			}
			hasQ := false
			for n < 6 && t.peekAt(0) == '?' && !t.eos() {
				t.pos++
				n++
				hasQ = true
			}
			if !hasQ && t.peekAt(0) == '-' && isCSSHex(t.peekAt(1)) {
				t.pos++
				for k := 0; k < 6 && isCSSHex(t.peekAt(0)) && !t.eos(); k++ {
					t.pos++
				}
			}
			return tok(ctUnicodeRange, "")
		}
		t.pos--
		typ, v := t.consumeIdentLike()
		return tok(typ, v)
	case '|':
		if (t.peekAt(0) == '=' || t.peekAt(0) == '|') && !t.eos() {
			t.pos++
			return tok(ctMatch, "")
		}
		return tok(ctDelim, "|")
	}
	if isCSSDigit(c) {
		t.pos--
		return tok(t.consumeNumeric(), "")
	}
	if isNameStart(c) {
		t.pos--
		typ, v := t.consumeIdentLike()
		return tok(typ, v)
	}
	return tok(ctDelim, string(c))
}

func cssPreprocess(s string) []rune {
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.NewReplacer("\r", "\n", "\f", "\n", "\x00", "�").Replace(s)
	out := make([]rune, 0, utf8.RuneCountInString(s))
	for _, r := range s {
		out = append(out, r)
	}
	return out
}

func tokenizeCSS(s string) []cssToken {
	t := &cssTokenizer{s: cssPreprocess(s)}
	var toks []cssToken
	for {
		tk, ok := t.next()
		if !ok {
			break
		}
		toks = append(toks, tk)
	}
	return toks
}

// cssComponent はコンポーネント値（トークン、関数、単純ブロック）。
type cssComponent struct {
	tok      cssToken
	isFunc   bool
	isBlock  bool
	funcName string
	children []cssComponent // 関数・ブロックの中身
	raw      string         // 直列化結果
}

type cssScanner struct {
	toks []cssToken
	pos  int
}

func (s *cssScanner) consume() (cssToken, bool) {
	if s.pos >= len(s.toks) {
		return cssToken{}, false
	}
	s.pos++
	return s.toks[s.pos-1], true
}

func (s *cssScanner) peek() (cssToken, bool) {
	if s.pos >= len(s.toks) {
		return cssToken{}, false
	}
	return s.toks[s.pos], true
}

const cssMaxDepth = 25

func (s *cssScanner) consumeComponent(depth int) (cssComponent, bool) {
	tk, ok := s.consume()
	if !ok {
		return cssComponent{}, false
	}
	switch tk.typ {
	case ctLBrace, ctLBracket, ctLParen:
		return s.consumeBlock(tk, depth+1), true
	case ctFunction:
		return s.consumeFunction(tk, depth+1), true
	}
	return cssComponent{tok: tk, raw: tk.raw}, true
}

func blockEnd(t cssTokType) cssTokType {
	switch t {
	case ctLBrace:
		return ctRBrace
	case ctLBracket:
		return ctRBracket
	}
	return ctRParen
}

func blockChars(t cssTokType) (string, string) {
	switch t {
	case ctLBrace:
		return "{", "}"
	case ctLBracket:
		return "[", "]"
	}
	return "(", ")"
}

// discard は深すぎる入れ子を読み捨てる（Crass の discard_block）。
func (s *cssScanner) discard(end cssTokType) {
	for {
		tk, ok := s.consume()
		if !ok || tk.typ == end {
			return
		}
	}
}

func (s *cssScanner) consumeBlock(start cssToken, depth int) cssComponent {
	end := blockEnd(start.typ)
	if depth > cssMaxDepth {
		s.discard(end)
		return cssComponent{tok: cssToken{typ: ctBadURL}}
	}
	c := cssComponent{tok: start, isBlock: true}
	for {
		tk, ok := s.consume()
		if !ok || tk.typ == end {
			break
		}
		s.pos--
		ch, _ := s.consumeComponent(depth)
		c.children = append(c.children, ch)
	}
	open, closing := blockChars(start.typ)
	var sb strings.Builder
	sb.WriteString(open)
	for _, ch := range c.children {
		sb.WriteString(ch.raw)
	}
	sb.WriteString(closing)
	c.raw = sb.String()
	return c
}

func (s *cssScanner) consumeFunction(start cssToken, depth int) cssComponent {
	if depth > cssMaxDepth {
		s.discard(ctRParen)
		return cssComponent{tok: cssToken{typ: ctBadURL}}
	}
	c := cssComponent{tok: start, isFunc: true, funcName: start.value}
	from := s.pos
	for {
		tk, ok := s.consume()
		if !ok || tk.typ == ctRParen {
			break
		}
		s.pos--
		ch, _ := s.consumeComponent(depth)
		c.children = append(c.children, ch)
	}
	var sb strings.Builder
	sb.WriteString(start.raw)
	for _, tk := range s.toks[from:s.pos] {
		sb.WriteString(tk.raw)
	}
	c.raw = sb.String()
	return c
}

// cleanProperties は Sanitize::CSS#properties。allowed は許可するプロパティ名。
func cleanProperties(css string, allowed map[string]bool) string {
	sc := &cssScanner{toks: tokenizeCSS(css)}
	var out strings.Builder
	precededByProperty := false
	for {
		tk, ok := sc.consume()
		if !ok {
			break
		}
		switch tk.typ {
		case ctWhitespace:
			out.WriteString(tk.raw)
		case ctSemicolon:
			if precededByProperty {
				precededByProperty = false
				out.WriteString(tk.raw)
			}
		case ctAtKeyword:
			// at-rule は許可しない（; または {} ブロックまで読み捨てる）
			precededByProperty = false
			for {
				t2, ok := sc.consume()
				if !ok || t2.typ == ctSemicolon {
					break
				}
				if t2.typ == ctLBrace {
					sc.consumeBlock(t2, 1)
					break
				}
				sc.pos--
				sc.consumeComponent(0)
			}
		case ctIdent:
			comps := []cssComponent{{tok: tk, raw: tk.raw}}
			for {
				nt, ok := sc.peek()
				if !ok || nt.typ == ctSemicolon {
					break
				}
				c, _ := sc.consumeComponent(0)
				comps = append(comps, c)
			}
			raw, keep := checkDeclaration(comps, allowed)
			if keep {
				out.WriteString(raw)
				precededByProperty = true
			} else if raw != "\x00error" {
				precededByProperty = false
			}
		default:
			// 不正なプロパティ名: ; まで読み捨てる（error ノードは preceded_by_property を変えない）
			sc.pos--
			for {
				nt, ok := sc.peek()
				if !ok || nt.typ == ctSemicolon {
					break
				}
				sc.consumeComponent(0)
			}
		}
	}
	return out.String()
}

// checkDeclaration は consume_declaration と Sanitize::CSS#property! の組み合わせ。
// 戻り値の raw が "\x00error" の場合は宣言として成立しなかった（error ノード）。
func checkDeclaration(comps []cssComponent, allowed map[string]bool) (string, bool) {
	name := comps[0].tok.value
	i := 1
	for i < len(comps) && !comps[i].isFunc && !comps[i].isBlock && comps[i].tok.typ == ctWhitespace {
		i++
	}
	if i >= len(comps) || comps[i].isFunc || comps[i].isBlock || comps[i].tok.typ != ctColon {
		return "\x00error", false
	}
	value := comps[i+1:]
	var raw strings.Builder
	for _, c := range comps {
		raw.WriteString(c.raw)
	}
	if !allowed[strings.ToLower(name)] {
		return raw.String(), false
	}
	// 値の検査
	nodes := append([]cssComponent(nil), value...)
	combined := ""
	for k := 0; k < len(nodes); k++ {
		c := nodes[k]
		switch {
		case c.isFunc:
			fn := strings.ToLower(c.funcName)
			if fn == "url" || fn == "image" || fn == "image-set" || fn == "-webkit-image-set" {
				// CSS の protocols は空のため url()/image() はすべて不可
				return raw.String(), false
			}
			combined += fn
			if fn == "expression" || combined == "expression" {
				return raw.String(), false
			}
			nodes = append(nodes, c.children...)
		case c.isBlock:
			// 単純ブロックは値（:value）を持たないため検査対象外
		case c.tok.typ == ctIdent:
			combined += strings.ToLower(c.tok.value)
		case c.tok.typ == ctURL, c.tok.typ == ctBadURL:
			return raw.String(), false
		}
	}
	return raw.String(), true
}
