package rails

import (
	"html/template"
	"strings"
)

var jsEscaper = strings.NewReplacer(
	`\`, `\\`,
	"</", `<\/`,
	"\r\n", `\n`,
	" ", "&#x2028;",
	" ", "&#x2029;",
	"\n", `\n`,
	"\r", `\n`,
	`"`, `\"`,
	"'", `\'`,
	"`", "\\`",
	"$", `\$`,
)

// EscapeJavascriptString は escape_javascript の変換本体（安全性の型は扱わない）。
func EscapeJavascriptString(s string) string {
	if s == "" {
		return ""
	}
	return jsEscaper.Replace(s)
}

// EscapeJavascript は escape_javascript / j。
// 入力が html_safe（template.HTML）なら結果も template.HTML、そうでなければ string を返す
// （Rails と同じく、安全でない入力の結果は ERB 出力時にさらに HTML エスケープされる）。
func EscapeJavascript(v any) any {
	if s, ok := v.(template.HTML); ok {
		return template.HTML(EscapeJavascriptString(string(s)))
	}
	return EscapeJavascriptString(ToS(v))
}

// JavascriptCDATASection は javascript_cdata_section。
func JavascriptCDATASection(content any) HTML {
	return HTML("\n//" + string(CDATASection("\n"+ToS(content)+"\n//")) + "\n")
}

// JavascriptTag は javascript_tag(content, html_options)。
// 出力は "<script>\n//<![CDATA[\n...\n//]]>\n</script>"（content はエスケープしない）。
func JavascriptTag(content any, htmlOptions *Hash) HTML {
	return ContentTag("script", JavascriptCDATASection(content), htmlOptions)
}
