// Package normalize は Redmine と buropher のレスポンスを比較可能な形へ正規化する。
//
// HTML は DOM に変換したうえで 1 要素 1 行のインデント形式に整形し、
// CSRF トークン・アセットダイジェスト・ベース URL などセッション/環境依存の値を置換する。
// JSON/XML はキー順を保ったまま（オプションでソート）整形し、タイムスタンプ等をマスクできる。
package normalize

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Format はレスポンス本文の種類。
type Format string

const (
	FormatHTML Format = "html"
	FormatJSON Format = "json"
	FormatXML  Format = "xml"
	FormatText Format = "text"
)

// Ext はゴールデンファイル等に使う拡張子を返す。
func (f Format) Ext() string {
	switch f {
	case FormatHTML, FormatJSON, FormatXML:
		return string(f)
	default:
		return "txt"
	}
}

// DetectFormat は Content-Type からフォーマットを推定する。
func DetectFormat(contentType string) Format {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "html"):
		return FormatHTML
	case strings.Contains(ct, "json"):
		return FormatJSON
	case strings.Contains(ct, "xml"):
		return FormatXML
	default:
		return FormatText
	}
}

// AttrMask は selector に一致する要素の属性 attr の値を置換する指定。
type AttrMask struct {
	Selector string `yaml:"selector"`
	Attr     string `yaml:"attr"`
}

// RegexMask は正規表現置換の指定（Replace は regexp.ReplaceAllString の書式）。
type RegexMask struct {
	Pattern string `yaml:"pattern"`
	Replace string `yaml:"replace"`
}

// Config はシナリオ YAML に書く正規化設定。リスト項目は上位の設定に追記され、
// bool 項目は nil のとき上位の値を継承する。
type Config struct {
	// StripSelectors に一致する要素を丸ごと削除する（例: "#footer"）。
	StripSelectors []string `yaml:"strip_selectors"`
	// MaskTextSelectors に一致する要素の中身を "MASKED" に置き換える。
	MaskTextSelectors []string `yaml:"mask_text_selectors"`
	// MaskAttrs に一致する属性値を "MASKED" に置き換える。
	MaskAttrs []AttrMask `yaml:"mask_attrs"`
	// Regex は全テキスト・属性値・JSON/XML 文字列に適用する置換。
	Regex []RegexMask `yaml:"regex"`
	// MaskKeys は JSON のキー名 / XML の要素名・属性名で、値を "MASKED" にする。
	MaskKeys []string `yaml:"mask_keys"`

	// MaskRelativeTimes は「3 days」「about 1 hour」等の相対時間表記（英語）を RELTIME に置換する。
	MaskRelativeTimes *bool `yaml:"mask_relative_times"`
	// MaskTimestamps は ISO8601 形式の日時を TIMESTAMP に置換する（JSON/XML/HTML 全体）。
	MaskTimestamps *bool `yaml:"mask_timestamps"`
	// SortKeys は JSON オブジェクトのキーをソートする。
	SortKeys *bool `yaml:"sort_keys"`
	// SortClasses は class 属性のトークンをソートする。
	SortClasses *bool `yaml:"sort_classes"`
	// KeepComments は HTML コメントを残す。
	KeepComments *bool `yaml:"keep_comments"`
	// PreserveEdgeSpace はテキストノード前後の空白（1 個に圧縮）を残す。false なら trim する。
	PreserveEdgeSpace *bool `yaml:"preserve_edge_space"`
}

// Merge は c の上に o を重ねた新しい Config を返す。
func (c Config) Merge(o Config) Config {
	r := Config{
		StripSelectors:    concat(c.StripSelectors, o.StripSelectors),
		MaskTextSelectors: concat(c.MaskTextSelectors, o.MaskTextSelectors),
		MaskAttrs:         concat(c.MaskAttrs, o.MaskAttrs),
		Regex:             concat(c.Regex, o.Regex),
		MaskKeys:          concat(c.MaskKeys, o.MaskKeys),
		MaskRelativeTimes: pick(c.MaskRelativeTimes, o.MaskRelativeTimes),
		MaskTimestamps:    pick(c.MaskTimestamps, o.MaskTimestamps),
		SortKeys:          pick(c.SortKeys, o.SortKeys),
		SortClasses:       pick(c.SortClasses, o.SortClasses),
		KeepComments:      pick(c.KeepComments, o.KeepComments),
		PreserveEdgeSpace: pick(c.PreserveEdgeSpace, o.PreserveEdgeSpace),
	}
	return r
}

func concat[T any](a, b []T) []T {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	r := make([]T, 0, len(a)+len(b))
	r = append(r, a...)
	return append(r, b...)
}

func pick(a, b *bool) *bool {
	if b != nil {
		return b
	}
	return a
}

func val(b *bool) bool { return b != nil && *b }

// Normalizer はコンパイル済みの正規化ルール。
type Normalizer struct {
	cfg      Config
	regexes  []compiledRegex
	baseURLs []string
	hosts    []string
	maskKeys map[string]bool
}

type compiledRegex struct {
	re      *regexp.Regexp
	replace string
}

// 常に適用する組み込みルール。
var (
	// アセットのダイジェスト（propshaft: name-xxxxxxxx.ext）
	reAssetDigest = regexp.MustCompile(`-[0-9a-f]{8}(\.(?:css|js|map|png|gif|jpe?g|svg|ico|woff2?|ttf|eot|webp))\b`)
	// Atom/RSS キー・API キー（40 桁の hex）を含むクエリ
	reFeedKey = regexp.MustCompile(`([?&](?:amp;)?key=)[0-9a-f]{40}`)
	// ISO8601 日時
	reTimestamp = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	// Redmine (en) の distance_of_time_in_words
	reRelTime = regexp.MustCompile(`\b(?:less than a minute|less than \d+ seconds|half a minute|(?:about|over|almost) (?:1|an?|\d+) (?:hours?|months?|years?)|\d+ (?:seconds?|minutes?|hours?|days?|months?|years?))\b`)
)

const (
	// Placeholder は正規化で置換した値の表記。
	PlaceholderBase   = "{{BASE}}"
	PlaceholderHost   = "{{HOST}}"
	PlaceholderCSRF   = "{{CSRF}}"
	PlaceholderDigest = "-DIGEST"
	PlaceholderMasked = "MASKED"
)

// New は設定からノーマライザを作る。baseURLs はレスポンス中で {{BASE}} に置換するベース URL。
func New(cfg Config, baseURLs ...string) (*Normalizer, error) {
	n := &Normalizer{cfg: cfg, maskKeys: map[string]bool{}}
	for _, r := range cfg.Regex {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("regex %q: %w", r.Pattern, err)
		}
		n.regexes = append(n.regexes, compiledRegex{re, r.Replace})
	}
	for _, k := range cfg.MaskKeys {
		n.maskKeys[k] = true
	}
	for _, b := range baseURLs {
		b = strings.TrimRight(b, "/")
		if b == "" {
			continue
		}
		// 素の形・URL エンコード形（back_url パラメータ等）の両方を置換する
		n.baseURLs = append(n.baseURLs, b, url.QueryEscape(b))
		// ホスト名:ポート単体（設定画面の「例: 127.0.0.1:3998」等）
		if u, err := url.Parse(b); err == nil && u.Host != "" {
			n.hosts = append(n.hosts, u.Host)
		}
	}
	return n, nil
}

// String は文字列 1 つに文字列レベルの置換をすべて適用する。
func (n *Normalizer) String(s string) string {
	for _, b := range n.baseURLs {
		s = strings.ReplaceAll(s, b, PlaceholderBase)
	}
	for _, h := range n.hosts {
		s = strings.ReplaceAll(s, h, PlaceholderHost)
	}
	s = reAssetDigest.ReplaceAllString(s, PlaceholderDigest+"$1")
	s = reFeedKey.ReplaceAllString(s, "${1}KEY")
	if val(n.cfg.MaskTimestamps) {
		s = reTimestamp.ReplaceAllString(s, "TIMESTAMP")
	}
	if val(n.cfg.MaskRelativeTimes) {
		s = reRelTime.ReplaceAllString(s, "RELTIME")
	}
	for _, r := range n.regexes {
		s = r.re.ReplaceAllString(s, r.replace)
	}
	return s
}

// Normalize は本文を format に応じて正規化する。パースに失敗した JSON/XML はテキストとして扱う。
func (n *Normalizer) Normalize(format Format, body []byte) (string, error) {
	switch format {
	case FormatHTML:
		return n.HTML(body)
	case FormatJSON:
		out, err := n.JSON(body)
		if err != nil {
			return n.Text(body), nil
		}
		return out, nil
	case FormatXML:
		out, err := n.XML(body)
		if err != nil {
			return n.Text(body), nil
		}
		return out, nil
	default:
		return n.Text(body), nil
	}
}

// Text はテキストに文字列置換を適用し、改行コードを LF に揃える。
func (n *Normalizer) Text(body []byte) string {
	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	s = n.String(s)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}
