package httpx

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

// MimeType は Rails の Mime::Type 相当の登録情報。
type MimeType struct {
	// String は代表 MIME 文字列（例 "application/json"）。
	String string
	// Symbol はフォーマット名（例 "json"）。
	Symbol string
	// Synonyms は別名の MIME 文字列。
	Synonyms []string
	// Extensions は Symbol 以外に対応する拡張子。
	Extensions []string
}

// FormatAll は Accept: */* を表すフォーマット（Mime::ALL）。
const FormatAll = "*/*"

// mimeSet は action_dispatch/http/mime_types.rb の登録順（Mime::SET）。
var mimeSet = []MimeType{
	{"text/html", "html", []string{"application/xhtml+xml"}, []string{"xhtml"}},
	{"text/plain", "text", nil, []string{"txt"}},
	{"text/javascript", "js", []string{"application/javascript", "application/x-javascript"}, nil},
	{"text/css", "css", nil, nil},
	{"text/calendar", "ics", nil, nil},
	{"text/csv", "csv", nil, nil},
	{"text/vcard", "vcf", nil, nil},
	{"text/vtt", "vtt", nil, []string{"vtt"}},
	{"image/png", "png", nil, []string{"png"}},
	{"image/jpeg", "jpeg", nil, []string{"jpg", "jpeg", "jpe", "pjpeg"}},
	{"image/gif", "gif", nil, []string{"gif"}},
	{"image/bmp", "bmp", nil, []string{"bmp"}},
	{"image/tiff", "tiff", nil, []string{"tif", "tiff"}},
	{"image/svg+xml", "svg", nil, nil},
	{"image/webp", "webp", nil, []string{"webp"}},
	{"video/mpeg", "mpeg", nil, []string{"mpg", "mpeg", "mpe"}},
	{"audio/mpeg", "mp3", nil, []string{"mp1", "mp2", "mp3"}},
	{"audio/ogg", "ogg", nil, []string{"oga", "ogg", "spx", "opus"}},
	{"audio/aac", "m4a", []string{"audio/mp4"}, []string{"m4a", "mpg4", "aac"}},
	{"video/webm", "webm", nil, []string{"webm"}},
	{"video/mp4", "mp4", nil, []string{"mp4", "m4v"}},
	{"font/otf", "otf", nil, []string{"otf"}},
	{"font/ttf", "ttf", nil, []string{"ttf"}},
	{"font/woff", "woff", nil, []string{"woff"}},
	{"font/woff2", "woff2", nil, []string{"woff2"}},
	{"application/xml", "xml", []string{"text/xml", "application/x-xml"}, nil},
	{"application/rss+xml", "rss", nil, nil},
	{"application/atom+xml", "atom", nil, nil},
	{"application/x-yaml", "yaml", []string{"text/yaml"}, []string{"yml", "yaml"}},
	{"multipart/form-data", "multipart_form", nil, nil},
	{"application/x-www-form-urlencoded", "url_encoded_form", nil, nil},
	{"application/json", "json", []string{"text/x-json", "application/jsonrequest", "application/problem+json"}, nil},
	{"application/pdf", "pdf", nil, []string{"pdf"}},
	{"application/zip", "zip", nil, []string{"zip"}},
	{"application/gzip", "gzip", []string{"application/x-gzip"}, []string{"gz"}},
}

var (
	mimeLookup      = map[string]*MimeType{} // MIME 文字列 → 型
	mimeExtLookup   = map[string]*MimeType{} // 拡張子 → 型
	mimeSymbolIndex = map[string]*MimeType{} // シンボル → 型
)

func init() {
	for i := range mimeSet {
		m := &mimeSet[i]
		mimeLookup[m.String] = m
		for _, s := range m.Synonyms {
			mimeLookup[s] = m
		}
		mimeExtLookup[m.Symbol] = m
		for _, e := range m.Extensions {
			mimeExtLookup[e] = m
		}
		mimeSymbolIndex[m.Symbol] = m
	}
}

// LookupMimeByExtension は拡張子（フォーマット名）から型を返す（Mime::Type.lookup_by_extension）。
func LookupMimeByExtension(ext string) *MimeType { return mimeExtLookup[ext] }

// MimeFor はフォーマット名から代表 MIME 文字列を返す（未知なら ""）。
func MimeFor(format string) string {
	if m := mimeSymbolIndex[format]; m != nil {
		return m.String
	}
	return ""
}

// lookupMimeSymbol は MIME 文字列からシンボルを返す（Mime::Type.lookup。未登録は ""、*/* は FormatAll）。
func lookupMimeSymbol(s string) string {
	if s == "*/*" {
		return FormatAll
	}
	if m, ok := mimeLookup[s]; ok {
		return m.Symbol
	}
	// パラメータ付きならメディアタイプ部分で再検索
	if i := strings.IndexByte(s, ';'); i >= 0 {
		if m, ok := mimeLookup[strings.TrimRight(s[:i], " \t")]; ok {
			return m.Symbol
		}
	}
	return ""
}

// ContentTypeFor はレンダリング時の Content-Type ヘッダ値（"; charset=utf-8" 付き）を返す。
// Rails の render は常に charset=utf-8 を付ける。未知のフォーマットは text/html。
func ContentTypeFor(format string) string {
	m := MimeFor(format)
	if m == "" {
		m = "text/html"
	}
	return m + "; charset=utf-8"
}

// SetContentType は Content-Type をフォーマットに応じて設定する。
// withCharset=false は head / send_data 相当（charset なし）。
func SetContentType(w http.ResponseWriter, format string, withCharset bool) {
	if withCharset {
		w.Header().Set("Content-Type", ContentTypeFor(format))
		return
	}
	m := MimeFor(format)
	if m == "" {
		m = "text/html"
	}
	w.Header().Set("Content-Type", m)
}

// ---- Accept ヘッダ解析（Mime::Type.parse） ----

var (
	trailingStarRe   = regexp.MustCompile(`^(text|application)/\*`)
	paramSepRe       = regexp.MustCompile(`;\s*q="?`)
	acceptHeaderRe   = regexp.MustCompile(`[^,\s"](?:[^,"]|"[^"]*")*`)
	browserLikeAccRe = regexp.MustCompile(`,\s*\*/\*|\*/\*\s*,`)
)

type acceptItem struct {
	index int
	name  string
	q     int
}

// ParseAccept は Accept ヘッダを Rails と同じ規則で解析し、MIME 文字列の優先順リストを返す
// （未登録の型も文字列のまま含む。*/* は "*/*"）。
func ParseAccept(header string) []string {
	if !strings.Contains(header, ",") {
		if loc := paramSepRe.FindStringIndex(header); loc != nil {
			header = strings.TrimSpace(header[:loc[0]])
		}
		if strings.TrimSpace(header) == "" {
			return nil
		}
		if star := parseTrailingStar(header); star != nil {
			return star
		}
		return []string{mimeLookupString(header)}
	}

	var list []acceptItem
	idx := 0
	for _, h := range acceptHeaderRe.FindAllString(header, -1) {
		parts := splitOnce(paramSepRe, h)
		params := strings.TrimSpace(parts[0])
		if params == "" {
			continue
		}
		var q *float64
		if len(parts) > 1 {
			// Ruby の String#to_f
			f := rubyToF(parts[1])
			q = &f
		}
		names := parseTrailingStar(params)
		if names == nil {
			names = []string{params}
		}
		for _, n := range names {
			list = append(list, newAcceptItem(idx, n, q))
			idx++
		}
	}
	return sortAcceptList(list)
}

func splitOnce(re *regexp.Regexp, s string) []string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return []string{s}
	}
	// Ruby の split は全分割だが、最初の 2 要素のみ使うので以降は捨てる
	rest := s[loc[1]:]
	if loc2 := re.FindStringIndex(rest); loc2 != nil {
		rest = rest[:loc2[0]]
	}
	return []string{s[:loc[0]], rest}
}

func newAcceptItem(index int, name string, q *float64) acceptItem {
	qq := 1.0
	if q != nil {
		qq = *q
	} else if name == "*/*" {
		qq = 0.0 // ワイルドカードは末尾へ
	}
	return acceptItem{index: index, name: name, q: int(qq * 100)}
}

// mimeLookupString は Mime::Type.lookup の結果を代表文字列で返す。
func mimeLookupString(s string) string {
	if s == "*/*" {
		return s
	}
	if m, ok := mimeLookup[s]; ok {
		return m.String
	}
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = strings.TrimRight(s[:i], " \t")
		if m, ok := mimeLookup[s]; ok {
			return m.String
		}
	}
	return s
}

func parseTrailingStar(h string) []string {
	m := trailingStarRe.FindStringSubmatch(h)
	if m == nil {
		return nil
	}
	var out []string
	for _, t := range mimeSet {
		match := strings.Contains(t.String, m[1])
		for _, s := range t.Synonyms {
			if strings.Contains(s, m[1]) {
				match = true
			}
		}
		if match {
			out = append(out, t.String)
		}
	}
	return out
}

// sortAcceptList は Mime::Type::AcceptList.sort! の移植。
func sortAcceptList(list []acceptItem) []string {
	// 安定ソート: q 降順、index 昇順
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && acceptLess(list[j], list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	find := func(name string) int {
		for i, it := range list {
			if it.name == name {
				return i
			}
		}
		return -1
	}
	textXML := find("text/xml")
	appXML := find("application/xml")
	if textXML >= 0 && appXML >= 0 {
		if list[textXML].q > list[appXML].q {
			list[appXML].q = list[textXML].q
		}
		if appXML > textXML {
			list[appXML], list[textXML] = list[textXML], list[appXML]
			appXML, textXML = textXML, appXML
		}
		list = append(list[:textXML], list[textXML+1:]...)
	} else if textXML >= 0 {
		list[textXML].name = "application/xml"
	}
	if appXML >= 0 {
		idx := appXML
		for idx < len(list) {
			t := list[idx]
			if t.q < list[appXML].q {
				break
			}
			if strings.HasSuffix(t.name, "+xml") {
				list[appXML], list[idx] = list[idx], list[appXML]
				appXML = idx
			}
			idx++
		}
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, it := range list {
		s := mimeLookupString(it.name)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func acceptLess(a, b acceptItem) bool {
	if a.q != b.q {
		return a.q > b.q
	}
	return a.index < b.index
}

// ---- リクエストフォーマット判定（ActionDispatch::Http::MimeNegotiation#formats） ----

// IsXHR は X-Requested-With: XMLHttpRequest かを返す。
func IsXHR(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

var pathExtRe = regexp.MustCompile(`\.(\w+)$`)

// paramFormat は params[:format] を返す（ルートの {format} → クエリ/ボディの format）。
func paramFormat(r *http.Request) (string, bool) {
	if rctx := chi.RouteContext(r.Context()); rctx != nil && len(rctx.RoutePatterns) > 0 {
		for i, k := range rctx.URLParams.Keys {
			if k == "format" && i < len(rctx.URLParams.Values) {
				return rctx.URLParams.Values[i], true
			}
		}
	} else {
		// ルーティング前: パス拡張子を params[:format] の近似として使う
		if m := pathExtRe.FindStringSubmatch(r.URL.Path); m != nil {
			return m[1], true
		}
	}
	// ルーティング前後とも、クエリ/ボディの format はパスより優先度が低い（Rails はパスパラメータが上書き）
	p := BodyParams(r).Merge(QueryParams(r))
	if v, ok := p.Get("format"); ok && v != nil {
		return ValueString(v), true
	}
	return "", false
}

func validAcceptHeader(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	present := strings.TrimSpace(accept) != ""
	if IsXHR(r) && (present || MediaType(r) != "") {
		return true
	}
	return present && !browserLikeAccRe.MatchString(accept)
}

// Formats は Rails の request.formats をフォーマット名（"html", "json", "*/*" 等）のリストで返す。
// 判定順: params[:format] → 妥当な Accept ヘッダ → パス拡張子 → XHR なら js → html。
// 未登録の型は除外される（結果が空 = 未知フォーマット。respond_to なら 406）。
// フォーマットを明示的に上書きした場合（SetFormat）はそれを返す。
func Formats(r *http.Request) []string {
	if f, ok := r.Context().Value(ctxFormats).([]string); ok {
		return f
	}
	if f, ok := paramFormat(r); ok {
		if m := LookupMimeByExtension(f); m != nil {
			return []string{m.Symbol}
		}
		return []string{}
	}
	if validAcceptHeader(r) {
		var mimes []string
		if strings.TrimSpace(r.Header.Get("Accept")) == "" {
			mimes = []string{mimeLookupString(MediaType(r))}
		} else {
			mimes = ParseAccept(strings.TrimSpace(r.Header.Get("Accept")))
		}
		out := []string{}
		for _, m := range mimes {
			if m == "*/*" {
				out = append(out, FormatAll)
			} else if s := lookupMimeSymbol(m); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if m := pathExtRe.FindStringSubmatch(r.URL.Path); m != nil {
		if mt := LookupMimeByExtension(m[1]); mt != nil {
			return []string{mt.Symbol}
		}
	}
	if IsXHR(r) {
		return []string{"js"}
	}
	return []string{"html"}
}

// Format は request.format（Formats の先頭、なければ ""）を返す。
func Format(r *http.Request) string {
	f := Formats(r)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// WithFormats はフォーマットを固定したリクエストを返す（request.format= 相当）。
func WithFormats(r *http.Request, formats ...string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxFormats, formats))
}

// Negotiate は respond_to の決定（request.negotiate_mime）を移植したもの。
// order はアクションが対応するフォーマット（FormatAll は format.any）。
// 一致しなければ ""（Rails は ActionController::UnknownFormat → 406）。
func Negotiate(r *http.Request, order ...string) string {
	formats := Formats(r)
	for _, f := range formats {
		if f == FormatAll {
			if len(order) > 0 {
				return order[0]
			}
			return ""
		}
		for _, o := range order {
			if o == f {
				return f
			}
		}
	}
	for _, o := range order {
		if o == FormatAll {
			return Format(r)
		}
	}
	return ""
}

// ShouldVaryAccept は Rails の should_apply_vary_header? 相当（Accept でネゴシエーションした場合に true）。
// true なら Vary: Accept を付ける。
func ShouldVaryAccept(r *http.Request) bool {
	if _, ok := paramFormat(r); ok {
		return false
	}
	return validAcceptHeader(r)
}

// IsAPIRequest は Redmine の api_request?（params[:format] が xml か json）を返す。
func IsAPIRequest(r *http.Request) bool {
	f, ok := paramFormat(r)
	return ok && (f == "xml" || f == "json")
}

// ---- ルート登録ヘルパ ----

// routeFormatRe は Rails の動的セグメント既定制約 [^/.?]+ に相当。
var routeFormatRe = regexp.MustCompile(`^[^/.?]+$`)

// RouteOption は Route の挙動を変える。
type RouteOption func(*routeConfig)

type routeConfig struct {
	format         bool
	formatRequired bool
	allowed        []string
}

// NoFormat は .:format を付けない（Rails の format: false）。
func NoFormat() RouteOption { return func(c *routeConfig) { c.format = false } }

// FormatRequired は拡張子付きのパスのみ登録する（例 "/issues/{id}/relations" ではなく
// "/projects/{id}/issues.{format}" だけが欲しい場合）。
func FormatRequired() RouteOption { return func(c *routeConfig) { c.formatRequired = true } }

// AllowedFormats は拡張子として許可するフォーマットを制限する（Rails の constraints: {format: ...}）。
// 許可外の拡張子は 404。
func AllowedFormats(formats ...string) RouteOption {
	return func(c *routeConfig) { c.allowed = formats }
}

// Route は Rails の `get '/issues(.:format)'` 相当のルートを chi に登録する。
// pattern と pattern+".{format}" の両方を登録し、format の値が Rails の制約（[^/.?]+）に
// 合わない場合は 404 にする。GET の場合は HEAD も登録する（Rails は GET ルートが HEAD に一致する）。
// pattern が "/" で終わる、"*" を含む、または NoFormat 指定の場合は拡張子なしのみ。
func Route(r chi.Router, method, pattern string, h http.HandlerFunc, opts ...RouteOption) {
	cfg := routeConfig{format: true}
	for _, o := range opts {
		o(&cfg)
	}
	if strings.HasSuffix(pattern, "/") || strings.Contains(pattern, "*") {
		cfg.format = false
	}
	methods := []string{method}
	if method == http.MethodGet {
		methods = append(methods, http.MethodHead)
	}
	for _, m := range methods {
		if !cfg.formatRequired || !cfg.format {
			r.MethodFunc(m, pattern, h)
		}
		if cfg.format {
			r.MethodFunc(m, pattern+".{format}", formatGuard(h, cfg.allowed))
		}
	}
}

// formatGuard は {format} の値を検証する。
func formatGuard(h http.HandlerFunc, allowed []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := chi.URLParam(r, "format")
		ok := routeFormatRe.MatchString(f)
		if ok && allowed != nil {
			ok = false
			for _, a := range allowed {
				if a == f {
					ok = true
					break
				}
			}
		}
		if ok {
			// 他の動的セグメントも空文字は Rails では一致しない
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				for i, k := range rctx.URLParams.Keys {
					if k != "*" && i < len(rctx.URLParams.Values) && rctx.URLParams.Values[i] == "" {
						ok = false
					}
				}
			}
		}
		if !ok {
			notFound(w, r)
			return
		}
		h(w, r)
	}
}

func notFound(w http.ResponseWriter, r *http.Request) {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if nf, ok := rctx.Routes.(interface{ NotFoundHandler() http.HandlerFunc }); ok {
			nf.NotFoundHandler()(w, r)
			return
		}
	}
	http.NotFound(w, r)
}
