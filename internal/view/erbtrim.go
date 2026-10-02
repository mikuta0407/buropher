package view

import (
	"fmt"
	"regexp"
	"strings"
)

// ERB（Erubi, trim モード）の空白処理を Go テンプレートで再現する前処理。
//
// Redmine の ERB は Rails の Erubi ハンドラ（trim: true）で処理され、次の規則で空白が消える:
//
//  1. <% code %>（出力しないタグ）が「行に単独で」置かれている場合、行頭の空白（スペース・タブ）と
//     直後の [ \t]*\n が取り除かれ、行ごと消える。
//  2. <%= expr -%> は直後の [ \t]*\n を取り除く（行頭側は取り除かない）。
//  3. <%= expr %>（出力タグ）の前後の空白・改行はそのまま残る。
//
// Go の {{- / -}} は前後の空白を「改行を含めて全部」取り除くため ERB と互換にならない。そこで
// テンプレートを text/template に渡す前に、各アクションを分類して上の規則を適用する:
//
//   - 出力しないアクション（if / else / end / range / with / define / block / break / continue、
//     コメント {{/* */}}、変数宣言・代入 {{$x := ...}}、文として使う関数 content_for 等）は規則 1。
//   - 右側のトリムマーカー " -}}" は規則 2（ERB の -%>）の意味に読み替える（Go 本来の貪欲な
//     トリムではなく、直後の [ \t]*\n だけを取り除く）。
//   - 左側の "{{- " は Go 本来の意味（直前の空白・改行をすべて削除）のまま残す（非推奨）。
//
// 取り除いた改行はエラーメッセージの行番号がずれないよう、アクションの内側に移す。

// actionKeywords は出力しない制御構文のキーワード。
var actionKeywords = map[string]bool{
	"if": true, "else": true, "end": true, "range": true, "with": true,
	"define": true, "block": true, "break": true, "continue": true,
}

var declRe = regexp.MustCompile(`^\$[A-Za-z0-9_]*(\s*,\s*\$[A-Za-z0-9_]*)?\s*(:=|=)`)

// StatementFunc は「文」として扱う関数の分類。
type StatementFunc int

const (
	// StatementAlways は常に出力しないアクションとして扱う（content_for など）。
	StatementAlways StatementFunc = iota + 1
	// StatementWithArgs は引数がある場合だけ出力しないアクションとして扱う
	// （html_title: 引数ありは設定、引数なしはタイトル文字列の出力）。
	StatementWithArgs
)

// segment は前処理中のテンプレート断片。
type segment struct {
	text   string // テキスト断片（isAction=false）またはアクション全体 "{{...}}"
	action bool
	start  int // 元ソース中の開始位置（エラー表示用）
}

// splitActions はソースをテキストとアクションに分割する。
// アクション内の文字列リテラル・raw 文字列・文字定数・コメント中の "}}" は終端とみなさない。
func splitActions(src string) ([]segment, error) {
	var segs []segment
	i := 0
	for i < len(src) {
		j := strings.Index(src[i:], "{{")
		if j < 0 {
			segs = append(segs, segment{text: src[i:], start: i})
			break
		}
		if j > 0 {
			segs = append(segs, segment{text: src[i : i+j], start: i})
		}
		start := i + j
		k := start + 2
		end := -1
		for k < len(src) {
			c := src[k]
			switch {
			case strings.HasPrefix(src[k:], "/*"):
				e := strings.Index(src[k+2:], "*/")
				if e < 0 {
					return nil, fmt.Errorf("閉じられていないコメント（位置 %d）", k)
				}
				k += 2 + e + 2
				continue
			case c == '"':
				k++
				for k < len(src) && src[k] != '"' {
					if src[k] == '\\' {
						k++
					}
					k++
				}
				k++
				continue
			case c == '`':
				e := strings.IndexByte(src[k+1:], '`')
				if e < 0 {
					return nil, fmt.Errorf("閉じられていない raw 文字列（位置 %d）", k)
				}
				k += 1 + e + 1
				continue
			case c == '\'':
				k++
				for k < len(src) && src[k] != '\'' {
					if src[k] == '\\' {
						k++
					}
					k++
				}
				k++
				continue
			case strings.HasPrefix(src[k:], "}}"):
				end = k + 2
			}
			if end >= 0 {
				break
			}
			k++
		}
		if end < 0 {
			return nil, fmt.Errorf("閉じられていないアクション（位置 %d）", start)
		}
		segs = append(segs, segment{text: src[start:end], action: true, start: start})
		i = end
	}
	return segs, nil
}

// actionInner はアクションの中身（区切りとトリムマーカーを除き、前後の空白を落としたもの）を返す。
func actionInner(a string) (inner string, leftTrim, rightTrim bool) {
	s := a[2 : len(a)-2]
	if strings.HasPrefix(s, "- ") || s == "-" {
		leftTrim = true
		s = s[1:]
	}
	if strings.HasSuffix(s, " -") {
		rightTrim = true
		s = s[:len(s)-2]
	}
	return strings.TrimSpace(s), leftTrim, rightTrim
}

// isCodeAction はアクションが出力を伴わない（ERB の <% %> 相当）かを判定する。
func isCodeAction(inner string, stmts map[string]StatementFunc) bool {
	if strings.HasPrefix(inner, "/*") {
		return true
	}
	if declRe.MatchString(inner) {
		return true
	}
	word := inner
	if i := strings.IndexAny(inner, " \t\r\n()|"); i >= 0 {
		word = inner[:i]
	}
	if actionKeywords[word] {
		return true
	}
	switch stmts[word] {
	case StatementAlways:
		return true
	case StatementWithArgs:
		return strings.TrimSpace(inner[len(word):]) != ""
	case StatementWithArgsAfterName:
		return countArgs(inner[len(word):]) >= 2
	}
	return false
}

// countArgs はトップレベルの引数（空白区切り、括弧・文字列は 1 つとして数える）の数を返す。
// パイプ（|）以降は数えない。
func countArgs(s string) int {
	n, depth := 0, 0
	inArg := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '`' || c == '\'':
			q := c
			i++
			for i < len(s) && s[i] != q {
				if s[i] == '\\' && q != '`' {
					i++
				}
				i++
			}
			if !inArg && depth == 0 {
				n++
			}
			inArg = depth == 0 || inArg
		case c == '(':
			if depth == 0 && !inArg {
				n++
				inArg = true
			}
			depth++
		case c == ')':
			depth--
		case c == '|' && depth == 0:
			return n
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if depth == 0 {
				inArg = false
			}
		default:
			if depth == 0 && !inArg {
				n++
				inArg = true
			}
		}
	}
	return n
}

var rspaceRe = regexp.MustCompile(`^[ \t]*\r?\n`)

// ERBTrim は ERB（Erubi trim モード）互換の空白規則を適用したテンプレートソースを返す。
// stmts は「文」として扱う関数名（行単独で置かれたら行ごと消える）。
func ERBTrim(src string, stmts map[string]StatementFunc) (string, error) {
	segs, err := splitActions(src)
	if err != nil {
		return "", err
	}
	isBOL := true // 直前のタグの直後が改行だったか（Erubi の is_bol）
	for idx := range segs {
		seg := &segs[idx]
		if !seg.action {
			continue
		}
		inner, _, rightTrim := actionInner(seg.text)
		code := isCodeAction(inner, stmts)

		// 直前のテキスト断片（同じ行の行頭空白 = lspace を求める）
		var prev *segment
		if idx > 0 && !segs[idx-1].action {
			prev = &segs[idx-1]
		}
		hasLspace := false
		lspaceLen := 0
		if code {
			switch {
			case prev == nil || prev.text == "":
				hasLspace = isBOL
			case strings.HasSuffix(prev.text, "\n"):
				hasLspace = true
			default:
				if r := strings.LastIndexByte(prev.text, '\n'); r >= 0 {
					if isBlankLine(prev.text[r+1:]) {
						hasLspace = true
						lspaceLen = len(prev.text) - r - 1
					}
				} else if isBOL && isBlankLine(prev.text) {
					hasLspace = true
					lspaceLen = len(prev.text)
				}
			}
		}

		// 直後のテキスト断片の先頭の [ \t]*\n（rspace）
		var next *segment
		if idx+1 < len(segs) && !segs[idx+1].action {
			next = &segs[idx+1]
		}
		rspace := ""
		if next != nil {
			rspace = rspaceRe.FindString(next.text)
		}
		isBOL = rspace != ""

		removeR := false
		if code && hasLspace && rspace != "" {
			// 規則 1: 行ごと削除
			if prev != nil && lspaceLen > 0 {
				prev.text = prev.text[:len(prev.text)-lspaceLen]
			}
			removeR = true
		}
		if rightTrim {
			// " -}}" を取り除き、規則 2（-%>）として直後の改行を削除
			seg.text = seg.text[:len(seg.text)-4] + "}}"
			if rspace != "" {
				removeR = true
			}
		}
		if removeR {
			next.text = next.text[len(rspace):]
			// 行番号を保つため、削除した改行をアクション内に移す
			seg.text = moveNewlinesInside(seg.text, strings.Count(rspace, "\n"))
		}
	}
	var b strings.Builder
	b.Grow(len(src))
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String(), nil
}

func isBlankLine(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
}

// moveNewlinesInside は n 個の改行をアクションの閉じ括弧（コメントなら */）の直前に挿入する。
func moveNewlinesInside(a string, n int) string {
	if n == 0 {
		return a
	}
	nl := strings.Repeat("\n", n)
	inner, _, _ := actionInner(a)
	if strings.HasPrefix(inner, "/*") {
		i := strings.LastIndex(a, "*/")
		return a[:i] + nl + a[i:]
	}
	return a[:len(a)-2] + nl + "}}"
}
