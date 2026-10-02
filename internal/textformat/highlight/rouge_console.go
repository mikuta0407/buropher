package highlight

import (
	"regexp"
	"strings"
)

// Rouge 4.7 の console.rb の移植（既定オプション: lang=shell, prompt=$ # > ;）。

var (
	reConsoleSnip = regexp.MustCompile(`\A[ \t\n\v\f\r]*(?:<[.]+>|[.]+)[ \t\n\v\f\r]*\z`)
	reConsoleLine = regexp.MustCompile(`(?s)\A(.*?)(\n|\z)`)
)

func init() {
	registerRouge("console", func() *rlexer {
		l := &rlexer{tag: "console"}
		l.stream = func(c *rctx, text string) {
			shell := newCtx(rougeLexerByTag("shell"))
			for len(text) > 0 {
				m := reConsoleLine.FindStringIndex(text)
				line := text[:m[1]]
				text = text[m[1]:]
				if line == "" {
					// (.*?)(\n|$) が空文字列に一致した場合（Ruby では $ が行末に一致する）
					nl := strings.IndexByte(text, '\n')
					if nl < 0 {
						line, text = text, ""
					} else {
						line, text = text[:nl+1], text[nl+1:]
					}
				}
				switch {
				case reConsoleSnip.MatchString(line):
					shell.reset()
					c.emit("c", line)
				case strings.ContainsAny(line, "$#>;"):
					i := strings.IndexAny(line, "$#>;")
					c.emit("gp", line[:i+1])
					rest := line[i+1:]
					ws := len(rest) - len(strings.TrimLeft(rest, " \t\n\v\f\r"))
					if ws > 0 {
						c.emit("w", rest[:ws])
					}
					shell.continueLex(rest[ws:], c.emit)
				default:
					shell.reset()
					c.emit("go", line)
				}
			}
		}
		return l
	})
}
