// Package highlight は Redmine::SyntaxHighlighting（Rouge アダプタ）を移植したもの。
//
// 対応言語の判定（language_supported?）とファイル名からのレキサー推定は
// Rouge 4.7 のレキサー定義（rouge_lexers_gen.go）を用いて Redmine と同一の結果を返す。
// 実際の字句解析は alecthomas/chroma で行い、トークン種別を Rouge の短縮 CSS クラス名
// （k, nf, s2 など）に対応付けて Rouge::Formatters::HTML と同じ形式で出力する。
// レキサー自体は Rouge と別実装のため、トークン分割が Rouge と異なる場合がある。
package highlight

import (
	"path"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// langAliases は CodeRay 由来で Rouge が対応しない別名（Redmine の LANG_ALIASES）。
var langAliases = map[string]string{
	"delphi":      "pascal",
	"cplusplus":   "cpp",
	"ecmascript":  "javascript",
	"ecma_script": "javascript",
	"java_script": "javascript",
	"xhtml":       "html",
}

var registry = func() map[string]*rougeLexer {
	m := map[string]*rougeLexer{}
	for i := range rougeLexers {
		l := &rougeLexers[i]
		m[l.Tag] = l
		for _, a := range l.Aliases {
			m[a] = l
		}
	}
	return m
}()

// findLexer は Redmine の find_lexer（Rouge::Lexer.find、なければ別名で再検索）。
func findLexer(language string) *rougeLexer {
	if l := registry[language]; l != nil {
		return l
	}
	if a, ok := langAliases[language]; ok {
		return registry[a]
	}
	return nil
}

// LanguageSupported は Redmine::SyntaxHighlighting.language_supported?。
func LanguageSupported(language string) bool {
	return findLexer(strings.ToLower(language)) != nil
}

// chromaNames は Rouge のタグと chroma のレキサー名が異なるものの対応。
var chromaNames = map[string]string{
	"shell":                 "bash",
	"plaintext":             "plaintext",
	"console":               "console",
	"objective_c":           "objective-c",
	"objective_cpp":         "objective-c",
	"javascript":            "javascript",
	"common_lisp":           "common-lisp",
	"literate_haskell":      "haskell",
	"literate_coffeescript": "coffeescript",
	"cpp":                   "c++",
	"csharp":                "c#",
	"fsharp":                "fsharp",
	"vb":                    "vb.net",
	"viml":                  "vim",
	"make":                  "makefile",
	"docker":                "docker",
	"html":                  "html",
	"xml":                   "xml",
	"erb":                   "rhtml",
	"irb":                   "ruby",
	"jinja":                 "django",
	"liquid":                "liquid",
	"tsx":                   "tsx",
	"jsx":                   "react",
	"sass":                  "sass",
	"batchfile":             "batchfile",
	"powershell":            "powershell",
	"plsql":                 "plsql",
	"sql":                   "sql",
	"ini":                   "ini",
	"conf":                  "plaintext",
	"properties":            "properties",
	"apache":                "apacheconf",
	"nginx":                 "nginx",
	"diff":                  "diff",
	"email":                 "plaintext",
	"escape":                "plaintext",
	"tap":                   "plaintext",
	"terraform":             "terraform",
	"hcl":                   "hcl",
	"protobuf":              "protocol buffer",
	"armasm":                "armasm",
	"nasm":                  "nasm",
	"llvm":                  "llvm",
	"gherkin":               "gherkin",
	"json_doc":              "json",
	"json5":                 "json",
	"ghc_core":              "haskell",
	"ghc_cmm":               "haskell",
	"isbl":                  "plaintext",
	"systemd":               "systemd",
	"ssh":                   "ssh config",
	"q":                     "q",
	"r":                     "r",
}

func chromaLexer(l *rougeLexer) chroma.Lexer {
	name := l.Tag
	if n, ok := chromaNames[name]; ok {
		name = n
	}
	if lx := lexers.Get(name); lx != nil {
		return lx
	}
	for _, a := range l.Aliases {
		if lx := lexers.Get(a); lx != nil {
			return lx
		}
	}
	return lexers.Get("plaintext")
}

// tokens は text を字句解析し (Rouge の短縮クラス名, 値) の列を返す（同種トークンは結合）。
func tokens(text string, l *rougeLexer) [][2]string {
	tag := "plaintext"
	if l != nil {
		tag = l.Tag
	}
	if out, ok := rougeTokens(text, tag); ok {
		return out
	}
	return chromaTokens(text, l)
}

// chromaTokens は chroma で字句解析する（Rouge のレキサーを移植していない言語用）。
func chromaTokens(text string, l *rougeLexer) [][2]string {
	var lx chroma.Lexer
	if l == nil || l.Tag == "plaintext" {
		lx = lexers.Get("plaintext")
	} else {
		lx = chromaLexer(l)
	}
	lx = chroma.Coalesce(lx)
	it, err := lx.Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, text)
	if err != nil {
		return [][2]string{{"", text}}
	}
	var out [][2]string
	for _, t := range it.Tokens() {
		if t.Value == "" {
			continue
		}
		cls := shortName(t.Type, l)
		if n := len(out); n > 0 && out[n-1][0] == cls {
			out[n-1][1] += t.Value
			continue
		}
		out = append(out, [2]string{cls, t.Value})
	}
	return out
}

// wsLexers は空白を Text::Whitespace（w）として出力する Rouge レキサー。
var wsLexers = map[string]bool{"json": true, "json_doc": true}

// shortName は chroma のトークン種別を Rouge の短縮クラス名にする。
func shortName(tt chroma.TokenType, l *rougeLexer) string {
	switch tt {
	case chroma.Text:
		return ""
	case chroma.TextWhitespace:
		if l != nil && wsLexers[l.Tag] {
			return "w"
		}
		return ""
	case chroma.OperatorReserved:
		return "o"
	case chroma.GenericUnderline:
		return "g"
	}
	for t := tt; ; t = t.Parent() {
		if s, ok := chroma.StandardTypes[t]; ok {
			return s
		}
		if t == 0 {
			return ""
		}
	}
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// HighlightByLanguage は Redmine::SyntaxHighlighting.highlight_by_language（外側の pre/code は含まない）。
func HighlightByLanguage(text, language string) string {
	l := findLexer(strings.ToLower(language))
	var sb strings.Builder
	for _, t := range tokens(text, l) {
		if t[0] == "" {
			sb.WriteString(htmlEscaper.Replace(t[1]))
		} else {
			sb.WriteString(`<span class="` + t[0] + `">` + htmlEscaper.Replace(t[1]) + `</span>`)
		}
	}
	return sb.String()
}

// Nodes は HighlightByLanguage の結果を DOM ノード列として返す（node.inner_html= 用）。
// 制御文字など libxml2 が捨てる文字はここでは考慮しない（入力は DOM のテキストのため既に除去済み）。
func Nodes(text, language string) []*htmldom.Node {
	l := findLexer(strings.ToLower(language))
	var out []*htmldom.Node
	for _, t := range tokens(text, l) {
		if t[0] == "" {
			if n := len(out); n > 0 && out[n-1].Type == htmldom.TextNode {
				out[n-1].Data += t[1]
				continue
			}
			out = append(out, htmldom.NewText(t[1]))
			continue
		}
		span := htmldom.NewElement("span", htmldom.Attr{Name: "class", Value: t[0]})
		span.AppendChild(htmldom.NewText(t[1]))
		out = append(out, span)
	}
	return out
}

// ---- ファイル名による推定（highlight_by_filename / filename_supported?） ----

// globMatch は Rouge の test_glob（File.fnmatch(pattern, path, File::FNM_DOTMATCH | File::FNM_EXTGLOB) 相当の近似）。
func globMatch(pattern, name string) bool {
	if strings.Contains(pattern, "{") {
		// {a,b} 展開
		i := strings.Index(pattern, "{")
		j := strings.Index(pattern[i:], "}")
		if j > 0 {
			pre, body, post := pattern[:i], pattern[i+1:i+j], pattern[i+j+1:]
			for _, alt := range strings.Split(body, ",") {
				if globMatch(pre+alt+post, name) {
					return true
				}
			}
			return false
		}
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// guessByFilename は Rouge の Filename ガッサー（最も具体的なパターンに一致したレキサー群）。
func guessByFilename(filename string) []*rougeLexer {
	base := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	var best []*rougeLexer
	bestScore := 0
	have := false
	for i := range rougeLexers {
		l := &rougeLexers[i]
		score, matched := 0, false
		for _, p := range l.Filenames {
			if globMatch(p, base) {
				s := -strings.Count(p, "*") - strings.Count(p, "?") - strings.Count(p, "[")
				if !matched || s < score {
					score = s
				}
				matched = true
			}
		}
		if !matched {
			continue
		}
		switch {
		case !have || score > bestScore:
			best = []*rougeLexer{l}
			bestScore = score
			have = true
		case score == bestScore:
			best = append(best, l)
		}
	}
	return best
}

// disambiguate は Rouge の Disambiguation ガッサーの移植。
func disambiguate(filename, source string, cands []*rougeLexer) []*rougeLexer {
	base := path.Base(filename)
	has := func(s string) bool { return strings.Contains(source, s) }
	match := func(re string) bool { return regexp.MustCompile(re).MatchString(source) }
	pick := func(tag string) []*rougeLexer {
		if l := registry[tag]; l != nil {
			return []*rougeLexer{l}
		}
		return cands
	}
	switch {
	case globMatch("*.cfg", base):
		if match(`\A\s*(version|banner|interface)\b`) {
			return pick("cisco_ios")
		}
		return pick("ini")
	case globMatch("*.pl", base):
		if has("my $") {
			return pick("perl")
		}
		if has(":-") || match(`\A\w+(\(\w+\,\s*\w+\))*\.`) {
			return pick("prolog")
		}
	case globMatch("*.h", base):
		if match(`@(end|implementation|protocol|property)\b`) || has(`@"`) {
			return pick("objective_c")
		}
		if match(`(?m)^\s*(?:catch|class|constexpr|namespace|private|protected|public|template|throw|try|using)\b`) {
			return pick("cpp")
		}
		return pick("c")
	case globMatch("*.m", base):
		if match(`@(end|implementation|protocol|property)\b`) || has(`@"`) {
			return pick("objective_c")
		}
		if match(`(?m)^\s*\(\*`) {
			return pick("mathematica")
		}
		if match(`(?m)^\s*(if|while|for|switch|do)\s*\([^)]*\*[^)]*\)`) {
			return pick("objective_c")
		}
		if has(":=") {
			return pick("mathematica")
		}
		if match(`<%(def|method|text|doc|args|flags|attr|init|once|shared|perl|cleanup|filter)([^>]*)(>)`) {
			return pick("mason")
		}
		if match(`(?m)^\s*?%`) || match(`(?m)^\s*[a-zA-Z]\w*\s*=\s*\{`) {
			return pick("matlab")
		}
		if match(`(</?%|<&)`) {
			return pick("mason")
		}
	case globMatch("*.php", base):
		return pick("php")
	case globMatch("*.hh", base):
		if match(`(?m)^\s*#include`) {
			return pick("cpp")
		}
		if match(`(?m)^<\?hh`) || match(`(\(|, ?)\$\$`) {
			return pick("hack")
		}
		return pick("cpp")
	case globMatch("*.plist", base):
		if match(`\A<\?xml\b`) {
			return pick("xml")
		}
		return pick("plist")
	case globMatch("*.sc", base):
		if match(`(?m)^#`) {
			return pick("python")
		}
		if match(`(?m)(?:^~|;$)`) {
			return pick("supercollider")
		}
		return pick("python")
	case base == "Messages":
		if match(`(?m)^[^\s:]+:[^\s:]+`) {
			return pick("msgtrans")
		}
		return pick("plaintext")
	case globMatch("*.cls", base):
		if match(`\A\s*(?:\\|%)`) {
			return pick("tex")
		}
		if match(`(?i)(no\-undo|BLOCK\-LEVEL|ROUTINE\-LEVEL|&ANALYZE\-SUSPEND)`) {
			return pick("openedge")
		}
		return pick("apex")
	case globMatch("*.pp", base):
		if match(`(::)?([a-z]\w*::)`) {
			return pick("puppet")
		}
		if match(`(?m)^(function|begin|var)\b`) || match(`\b(end(;|\.))`) {
			return pick("pascal")
		}
		return pick("puppet")
	case globMatch("*.p", base):
		if has(":-") || match(`\A\w+(\(\w+\,\s*\w+\))*\.`) {
			return pick("prolog")
		}
		return pick("openedge")
	case globMatch("*.st", base):
		if match(`(?mi)^\s*END_`) {
			return pick("iecst")
		}
		return pick("smalltalk")
	}
	return cands
}

// FilenameSupported は Redmine::SyntaxHighlighting.filename_supported?。
func FilenameSupported(filename string) bool {
	return len(guessByFilename(filename)) > 0
}

var (
	reEmacsModeline = regexp.MustCompile(`(?i)-\*-\s*(?:(?:[\w-]+\s*:\s*(?:[\w+-]+)\s*;?\s*)*?(?:mode\s*:)?\s*([\w+-]+)\s*(?:;\s*[\w-]+\s*:\s*[\w+-]+\s*)*;?\s*)-\*-`)
	reVimModeline1  = regexp.MustCompile(`(?i)(?:vim|vi|ex):\s*(?:ft|filetype|syntax)=(\w+)\s?`)
	reVimModeline2  = regexp.MustCompile(`(?:vim|vi|Vim|ex):\s*se(?:t)?.*\s(?:ft|filetype|syntax)=(\w+)\s?.*:`)
	reShebang       = regexp.MustCompile(`(?m)\A[ \t\n\v\f\r]*#!(.*)$`)
	reDoctype       = regexp.MustCompile(`(?s)\A[ \t\n\v\f\r]*(?:<\?.*?\?>[ \t\n\v\f\r]*)?<!DOCTYPE[ \t\n\v\f\r]+(.+?)>`)
)

// textAnalyzer は Rouge の TextAnalyzer（シバンと DOCTYPE の判定）。
type textAnalyzer struct {
	text    string
	shebang *string
	doctype *string
}

func (t *textAnalyzer) shebangText() (string, bool) {
	if t.shebang == nil {
		s := ""
		if m := reShebang.FindStringSubmatch(t.text); m != nil {
			s = m[1]
			t.shebang = &s
		} else {
			t.shebang = new(string)
			*t.shebang = "\x00none"
		}
	}
	if *t.shebang == "\x00none" {
		return "", false
	}
	return *t.shebang, true
}

// shebangIs は shebang?(match)（/\b#{match}(\s|$)/ に一致するか）。
func (t *textAnalyzer) shebangIs(match string) bool {
	s, ok := t.shebangText()
	if !ok {
		return false
	}
	return regexp.MustCompile(`\b(?:` + match + `)(\s|$)`).MatchString(s)
}

func (t *textAnalyzer) doctypeText() (string, bool) {
	if t.doctype == nil {
		s := "\x00none"
		if m := reDoctype.FindStringSubmatch(t.text); m != nil {
			s = m[1]
		}
		t.doctype = &s
	}
	if *t.doctype == "\x00none" {
		return "", false
	}
	return *t.doctype, true
}

// rougeDetectors は detect? を持つ Rouge レキサーの判定関数。
var rougeDetectors = map[string]func(t *textAnalyzer) bool{
	"awk":          func(t *textAnalyzer) bool { return t.shebangIs("awk") },
	"biml":         func(t *textAnalyzer) bool { return regexp.MustCompile(`<\s*Biml\b`).MatchString(t.text) },
	"coffeescript": func(t *textAnalyzer) bool { return t.shebangIs("coffee") },
	"crystal":      func(t *textAnalyzer) bool { return t.shebangIs("crystal") },
	"diff": func(t *textAnalyzer) bool {
		return strings.HasPrefix(t.text, "Index: ") ||
			regexp.MustCompile(`\Adiff[^\n]*?\ba/[^\n]*\bb/`).MatchString(t.text) ||
			regexp.MustCompile(`---.*?\n[+][+][+]`).MatchString(t.text) ||
			regexp.MustCompile(`[+][+][+].*?\n---`).MatchString(t.text)
	},
	"elixir":  func(t *textAnalyzer) bool { return t.shebangIs("elixir") },
	"factor":  func(t *textAnalyzer) bool { return t.shebangIs("factor") },
	"gherkin": func(t *textAnalyzer) bool { return t.shebangIs("cucumber") },
	"groovy":  func(t *textAnalyzer) bool { return t.shebangIs("groovy") || t.shebangIs("nextflow") },
	"hack": func(t *textAnalyzer) bool {
		return strings.Contains(t.text, "<?hh") || t.shebangIs("hhvm") ||
			regexp.MustCompile(`async function [a-zA-Z]`).MatchString(t.text) || strings.Contains(t.text, "): Awaitable<")
	},
	"haskell": func(t *textAnalyzer) bool { return t.shebangIs("runhaskell") },
	"haxe":    func(t *textAnalyzer) bool { return t.shebangIs("haxe") },
	"html": func(t *textAnalyzer) bool {
		if d, ok := t.doctypeText(); ok && regexp.MustCompile(`(?i)\bhtml\b`).MatchString(d) {
			return true
		}
		if regexp.MustCompile(`\A<\?xml\b`).MatchString(t.text) {
			return false
		}
		return regexp.MustCompile(`<\s*html\b`).MatchString(t.text)
	},
	"io":         func(t *textAnalyzer) bool { return t.shebangIs("io") },
	"javascript": func(t *textAnalyzer) bool { return t.shebangIs("node") || t.shebangIs("jsc") },
	"julia":      func(t *textAnalyzer) bool { return t.shebangIs("julia") },
	"lasso": func(t *textAnalyzer) bool {
		return t.shebangIs("lasso9") || regexp.MustCompile(`\A.*?<\?(lasso(script)?|=)`).MatchString(t.text)
	},
	"livescript": func(t *textAnalyzer) bool { return t.shebangIs("lsc") },
	"lua":        func(t *textAnalyzer) bool { return t.shebangIs("lua") },
	"mojo":       func(t *textAnalyzer) bool { return t.shebangIs(`mojow?(?:[23](?:\.\d+)?)?`) },
	"moonscript": func(t *textAnalyzer) bool { return t.shebangIs("moon") },
	"perl":       func(t *textAnalyzer) bool { return t.shebangIs("perl") },
	"php": func(t *textAnalyzer) bool {
		if t.shebangIs("php") {
			return true
		}
		if regexp.MustCompile(`(?m)^<\?hh`).MatchString(t.text) {
			return false
		}
		return regexp.MustCompile(`(?m)^<\?php`).MatchString(t.text)
	},
	"postscript": func(t *textAnalyzer) bool { return regexp.MustCompile(`(?m)^%!`).MatchString(t.text) },
	"praat":      func(t *textAnalyzer) bool { return t.shebangIs("praat") },
	"puppet":     func(t *textAnalyzer) bool { return t.shebangIs("puppet-apply") || t.shebangIs("puppet") },
	"python":     func(t *textAnalyzer) bool { return t.shebangIs(`pythonw?(?:[23](?:\.\d+)?)?`) },
	"r":          func(t *textAnalyzer) bool { return t.shebangIs("Rscript") },
	"racket": func(t *textAnalyzer) bool {
		m := regexp.MustCompile(`(?m)\A#lang\s*(.*?)$`).FindStringSubmatch(t.text)
		return m != nil && regexp.MustCompile(`racket|scribble`).MatchString(m[1])
	},
	"ruby": func(t *textAnalyzer) bool { return t.shebangIs("ruby") },
	"rust": func(t *textAnalyzer) bool { return t.shebangIs("rustc") },
	"sed":  func(t *textAnalyzer) bool { return t.shebangIs("sed") },
	"shell": func(t *textAnalyzer) bool {
		return t.shebangIs(`(ba|z|k)?sh`) || strings.HasPrefix(t.text, "#compdef") || strings.HasPrefix(t.text, "#autoload")
	},
	"tcl": func(t *textAnalyzer) bool { return t.shebangIs("tclsh") || t.shebangIs("wish") || t.shebangIs("jimsh") },
	"tex": func(t *textAnalyzer) bool {
		return regexp.MustCompile(`\A\s*\\(documentclass|input|documentstyle|relax|ProvidesPackage|ProvidesClass)`).MatchString(t.text)
	},
	"tulip": func(t *textAnalyzer) bool { return t.shebangIs("tulip") },
	"xml": func(t *textAnalyzer) bool {
		d, ok := t.doctypeText()
		if ok && strings.Contains(d, "html") {
			return false
		}
		return regexp.MustCompile(`\A<\?xml\b`).MatchString(t.text) || ok
	},
	"yaml": func(t *textAnalyzer) bool { return regexp.MustCompile(`(?s)\A\s*%YAML`).MatchString(t.text) },
}

// guessLexer は Rouge の Lexer.guess(filename:, source:)。
// 一意に定まらない場合は ambiguous=true（Redmine はエスケープしたテキストを返す）。
func guessLexer(filename, source string) (l *rougeLexer, ambiguous bool) {
	all := make([]*rougeLexer, len(rougeLexers))
	for i := range rougeLexers {
		all[i] = &rougeLexers[i]
	}
	lexers := all
	apply := func(next []*rougeLexer) {
		if len(next) > 0 {
			lexers = next
		}
	}
	// Filename
	apply(guessByFilename(filename))
	// Modeline
	if len(lexers) > 1 {
		lines := strings.Split(source, "\n")
		first, last := lines, lines
		if len(first) > 5 {
			first = first[:5]
		}
		if len(last) > 5 {
			last = last[len(last)-5:]
		}
		space := strings.Join(append(append([]string{}, first...), last...), "\n")
		set := map[string]bool{}
		for _, re := range []*regexp.Regexp{reEmacsModeline, reVimModeline1, reVimModeline2} {
			if m := re.FindStringSubmatch(space); m != nil {
				set[m[1]] = true
			}
		}
		if len(set) > 0 {
			var sel []*rougeLexer
			for _, x := range lexers {
				ok := set[x.Tag]
				for _, a := range x.Aliases {
					ok = ok || set[a]
				}
				if ok {
					sel = append(sel, x)
				}
			}
			apply(sel)
		}
	}
	// Source
	if len(lexers) > 1 {
		ta := &textAnalyzer{text: source}
		var sel []*rougeLexer
		for _, x := range lexers {
			if d, ok := rougeDetectors[x.Tag]; ok && d(ta) {
				sel = append(sel, x)
			}
		}
		apply(sel)
	}
	// Disambiguation
	if len(lexers) > 1 && len(lexers) != len(all) {
		apply(disambiguate(filename, source, lexers))
	}
	if len(lexers) == len(all) {
		lexers = nil
	}
	switch len(lexers) {
	case 0:
		return registry["plaintext"], false
	case 1:
		return lexers[0], false
	}
	return nil, true
}

// HighlightByFilename は Redmine::SyntaxHighlighting.highlight_by_filename。
// 行ごとに span を閉じる形式（CustomHTMLLinewise）で出力する。
// 推定が一意に定まらない場合は Redmine と同様にエスケープしたテキストを返す。
func HighlightByFilename(text, filename string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	l, ambiguous := guessLexer(filename, text)
	if ambiguous {
		return htmlEscapeAll(text)
	}
	var sb strings.Builder
	var line [][2]string
	flush := func() {
		for _, t := range line {
			if t[0] == "" {
				sb.WriteString(htmlEscaper.Replace(t[1]))
			} else {
				sb.WriteString(`<span class="` + t[0] + `">` + htmlEscaper.Replace(t[1]) + `</span>`)
			}
		}
		sb.WriteString("\n")
		line = line[:0]
	}
	for _, t := range tokens(text, l) {
		v := t[1]
		for v != "" {
			if v[0] == '\n' {
				flush()
				v = v[1:]
				continue
			}
			i := strings.IndexByte(v, '\n')
			if i < 0 {
				i = len(v)
			}
			line = append(line, [2]string{t[0], v[:i]})
			v = v[i:]
		}
	}
	if len(line) > 0 {
		flush()
	}
	return sb.String()
}

// htmlEscapeAll は ERB::Util.h（& < > " ' をエスケープ）。
func htmlEscapeAll(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}
