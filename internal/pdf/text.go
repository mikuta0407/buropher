package pdf

// 文字列の折り返し（TCPDF の MultiCell / writeHTML の行分割の近似）。

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// isWideBreak は前後で改行できる文字（CJK の文字・全角記号）か。
func isWideBreak(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x11ff, // ハングル字母
		r >= 0x2e80 && r <= 0x9fff, // CJK 部首・記号・かな・漢字
		r >= 0xa000 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7af, // ハングル
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe4f,
		r >= 0xff00 && r <= 0xffef,
		r >= 0x20000 && r <= 0x3ffff:
		return true
	}
	return false
}

// tokenKind は折り返しの単位の種類。
type tokenKind int

const (
	tokWord tokenKind = iota
	tokSpace
	tokBreak
	tokImage
)

// token は折り返しの単位（単語・空白・改行・画像）。
type token struct {
	kind tokenKind
	text string
	ts   TextStyle
	w    float64
	// 画像
	img  string
	imgH float64
	// link はリンク先（HTML の a）。
	link string
}

// height は行の高さへの寄与（ベースラインより上・下）。
func (t token) ascentDescent() (asc, desc float64) {
	if t.kind == tokImage {
		return t.imgH, 0
	}
	lh := t.ts.lineHeight()
	asc = lh/2 + 0.3*t.ts.sizeMM()
	return asc, lh - asc
}

// tokenize は文字列を単語・空白・CJK の 1 文字・改行に分ける（collapse なら空白の連続を 1 つにする）。
func (d *Doc) tokenize(s string, ts TextStyle, collapse bool, link string) []token {
	var out []token
	var b strings.Builder
	kind := tokWord
	flush := func() {
		if b.Len() == 0 {
			return
		}
		text := b.String()
		if kind == tokSpace && collapse {
			text = " "
		}
		out = append(out, token{kind: kind, text: text, ts: ts, w: d.textWidth(text, ts), link: link})
		b.Reset()
	}
	for _, r := range s {
		switch {
		case r == '\n':
			if collapse {
				if kind != tokSpace {
					flush()
					kind = tokSpace
				}
				b.WriteRune(' ')
				continue
			}
			flush()
			out = append(out, token{kind: tokBreak, ts: ts})
			kind = tokWord
		case r == '\r':
		case r == ' ' || r == '\t' || r == '　' && false || (collapse && unicode.IsSpace(r) && r != ' '):
			if kind != tokSpace {
				flush()
				kind = tokSpace
			}
			b.WriteRune(r)
		case isWideBreak(r):
			flush()
			kind = tokWord
			b.WriteRune(r)
			flush()
		default:
			if kind != tokWord {
				flush()
				kind = tokWord
			}
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

// splitWord は幅 maxW に収まらない単語を文字単位で分ける（先頭は first の幅に収める）。
func (d *Doc) splitWord(t token, first, maxW float64) []token {
	var out []token
	var b strings.Builder
	var w float64
	limit := first
	for _, r := range t.text {
		cw := d.textWidth(string(r), t.ts)
		if w+cw > limit && b.Len() > 0 {
			out = append(out, token{kind: tokWord, text: b.String(), ts: t.ts, w: w, link: t.link})
			b.Reset()
			w = 0
			limit = maxW
		}
		b.WriteRune(r)
		w += cw
	}
	if b.Len() > 0 {
		out = append(out, token{kind: tokWord, text: b.String(), ts: t.ts, w: w, link: t.link})
	}
	return out
}

// breakLines は token を幅 maxW の行に分ける。行頭の空白は（段落の先頭を除き）落とし、行末の空白は除く。
func (d *Doc) breakLines(toks []token, maxW float64) [][]token {
	var lines [][]token
	var line []token
	var w float64
	paraStart := true
	endLine := func(hard bool) {
		for len(line) > 0 && line[len(line)-1].kind == tokSpace {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
		line, w = nil, 0
		paraStart = hard
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case tokBreak:
			if len(line) == 0 {
				// 空行は高さを持たせるために改行トークンを残す
				line = append(line, t)
			}
			endLine(true)
			continue
		case tokSpace:
			if len(line) == 0 && !paraStart {
				continue
			}
			line = append(line, t)
			w += t.w
			continue
		}
		if w+t.w <= maxW+1e-9 || len(line) == 0 && t.w <= maxW+1e-9 {
			line = append(line, t)
			w += t.w
			paraStart = false
			continue
		}
		// 収まらない
		hasContent := false
		for _, x := range line {
			if x.kind != tokSpace {
				hasContent = true
				break
			}
		}
		if t.kind == tokWord && t.w > maxW {
			// 長い単語は文字単位で分ける
			if hasContent {
				endLine(false)
			}
			parts := d.splitWord(t, maxW-w, maxW)
			for j, p := range parts {
				if j > 0 {
					endLine(false)
				}
				line = append(line, p)
				w += p.w
			}
			paraStart = false
			continue
		}
		if hasContent {
			endLine(false)
		}
		line = append(line, t)
		w += t.w
		paraStart = false
	}
	if len(line) > 0 || len(lines) == 0 {
		endLine(true)
	}
	return lines
}

// wrapPlain は txt を幅 maxW で折り返した行（MultiCell 用）。
func (d *Doc) wrapPlain(txt string, maxW float64, ts TextStyle) []string {
	txt = strings.ReplaceAll(txt, "\r\n", "\n")
	txt = strings.TrimSuffix(txt, "\n")
	toks := d.tokenize(txt, ts, false, "")
	lines := d.breakLines(toks, maxW)
	out := make([]string, len(lines))
	for i, l := range lines {
		var b strings.Builder
		for _, t := range l {
			if t.kind != tokBreak {
				b.WriteString(t.text)
			}
		}
		out[i] = b.String()
	}
	return out
}

// Truncate は String#truncate(n)（"..." を含めて n 文字）。
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	if n < 3 {
		return "..."
	}
	return string(r[:n-3]) + "..."
}
