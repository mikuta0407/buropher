package normalize

import (
	"html"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/mikuta0407/buropher/internal/brand"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/web"
)

// BrandPlaceholder は製品名（参照 Redmine の "Redmine" / 候補 buropher の "Buropher"）を置き換える文字列。
const BrandPlaceholder = "{{BRAND}}"

// brandRules は製品名が出る特定の箇所（Setting.app_title の既定値・Redmine::Info.app_name / url・
// フッター・welcome_text の既定値・静的エラーページ）を両側同じ形に置き換える規則。
// 正規化済みの文書（1 要素 1 行の HTML / 整形済み XML）に対して適用する。
// "Redmine Admin"（fixtures の利用者名）や X-Redmine-* などの互換識別子は対象にしない。
var brandRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// html_title（"<ページ> - <app_title>"）と Atom の <title>"<app_title>: ..."、public/404.html 等
	{regexp.MustCompile(`(<title>(?:[^<\n]* - )?)(?:Redmine|Buropher)(</title>)`), "${1}" + BrandPlaceholder + "${2}"},
	{regexp.MustCompile(`<title>(?:Redmine|Buropher)(: | \d{3} error</title>)`), "<title>" + BrandPlaceholder + "${1}"},
	// <meta name="description" content="<app_name>">
	{regexp.MustCompile(`<meta content="(?:Redmine|Buropher)" name="description">`), `<meta content="` + BrandPlaceholder + `" name="description">`},
	// ヘッダの <h1><app_title></h1>
	{regexp.MustCompile(`<h1>(?:Redmine|Buropher)</h1>`), "<h1>" + BrandPlaceholder + "</h1>"},
	// Atom の <author><name><app_title></name></author>
	{regexp.MustCompile(`<name>(?:Redmine|Buropher)</name>`), "<name>" + BrandPlaceholder + "</name>"},
	// Atom の auto_discovery_link_tag（title="<app_title>: ..."）
	{regexp.MustCompile(`title="(?:Redmine|Buropher): `), `title="` + BrandPlaceholder + `: `},
	// 設定画面の app_title 入力欄
	{regexp.MustCompile(`(id="settings_app_title"[^>\n]*value=")(?:Redmine|Buropher)"`), "${1}" + BrandPlaceholder + `"`},
	// welcome_text の既定値
	{regexp.MustCompile(`Welcome to (?:Redmine|Buropher), an open-source`), "Welcome to " + BrandPlaceholder + ", an open-source"},
	// Atom の <generator uri="<url>"><app_name></generator>
	{regexp.MustCompile(`<generator uri="[^"]*">((?:&#xA;|\s)*)(?:Redmine|Buropher)`), `<generator uri="{{BRAND_URL}}">${1}` + BrandPlaceholder},
	// フッター（Redmine: "Powered by Redmine © 2006-... Jean-Philippe Lang"、
	// buropher: "Powered by Buropher, based on Redmine © 2006-... Jean-Philippe Lang"）
	{regexp.MustCompile(`(?s)<div id="footer">\n\s*Powered by\n.*?Jean-Philippe Lang\n`), `<div id="footer">{{FOOTER}}` + "\n"},
}

var (
	brandLocaleOnce     sync.Once
	brandLocaleReplacer *strings.Replacer
)

// brandLocaleSegLen は訳文の断片を置換対象にする最小の長さ（製品名以外の部分）。短い断片は誤置換を避けて使わない。
const brandLocaleSegLen = 8

var interpolationRe = regexp.MustCompile(`%\{[^}]*\}|\n`)

// localeReplacer は Redmine 本体の訳文（web/locales/redmine/*.yml）のうち、buropher が読み込み時に
// 製品名を置換する値（brand.SubstituteLocale）について、置換前（参照側）と置換後（候補側）の断片を
// どちらも製品名を BrandPlaceholder にした断片へ置き換える Replacer を作る。
func localeReplacer() *strings.Replacer {
	brandLocaleOnce.Do(func() {
		seen := map[string]string{}
		add := func(from, to string) {
			if from != to {
				seen[from] = to
			}
		}
		_ = i18n.WalkUpstreamStrings(web.Locales(), func(key, s string) {
			if brand.LocaleExcluded(key) {
				return
			}
			for _, seg := range interpolationRe.Split(s, -1) {
				seg = strings.TrimSpace(seg)
				sub := brand.SubstituteLocale(seg)
				if sub == seg || len(seg)-strings.Count(seg, brand.Upstream)*len(brand.Upstream) < brandLocaleSegLen {
					continue
				}
				masked := strings.ReplaceAll(sub, brand.Name, BrandPlaceholder)
				for _, esc := range []func(string) string{func(s string) string { return s }, html.EscapeString} {
					add(esc(seg), esc(masked))
					add(esc(sub), esc(masked))
				}
			}
		})
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		// 長い断片を優先する（strings.Replacer は引数順に比較する）
		sort.Slice(keys, func(i, j int) bool {
			if len(keys[i]) != len(keys[j]) {
				return len(keys[i]) > len(keys[j])
			}
			return keys[i] < keys[j]
		})
		var args []string
		for _, k := range keys {
			args = append(args, k, seen[k])
		}
		brandLocaleReplacer = strings.NewReplacer(args...)
	})
	return brandLocaleReplacer
}

// Brand は正規化済みの文書 doc に含まれる製品名を両側同じ形に置き換える。
// 参照 Redmine は "Redmine"、候補 buropher は "Buropher" を表示する箇所（既定の app_title・フッター・
// 製品名を置換した訳文など）だけを対象とし、利用者名などのデータや互換識別子は変えない。
// ゴールデン（Redmine の出力）は書き換えず、比較の直前に両側へ適用する。
func Brand(doc string) string {
	if !strings.Contains(doc, brand.Upstream) && !strings.Contains(doc, brand.Name) {
		return doc
	}
	for _, r := range brandRules {
		doc = r.re.ReplaceAllString(doc, r.repl)
	}
	return localeReplacer().Replace(doc)
}
