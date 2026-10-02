package httpx

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	absoluteLocationRe  = regexp.MustCompile(`(?i)\A([a-z][a-z\d\-+.]*:|//)`)
	unsafeHeaderCharsRe = regexp.MustCompile(`[\x00-\x08\x0A-\x1F]`)
)

// RedirectLocation は Rails の _compute_redirect_to_location 相当:
// スキーム付き・"//" 始まりはそのまま、それ以外（パス）は request.protocol + host_with_port を前置する。
// NUL・CR・LF は除去する。
func RedirectLocation(r *http.Request, location string) string {
	if !absoluteLocationRe.MatchString(location) {
		location = RequestBaseURL(r) + location
	}
	location = strings.NewReplacer("\x00", "", "\r", "", "\n", "").Replace(location)
	// Rails は残りの制御文字があると UnsafeRedirectError にする。ここでは除去する。
	return unsafeHeaderCharsRe.ReplaceAllString(location, "")
}

// Redirect は redirect_to 相当: Location を絶対 URL にし、空ボディで返す（既定 302）。
func Redirect(w http.ResponseWriter, r *http.Request, location string, status ...int) {
	code := http.StatusFound
	if len(status) > 0 && status[0] != 0 {
		code = status[0]
	}
	w.Header().Set("Location", RedirectLocation(r, location))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
}

// BackURLOptions は RedirectBackOrDefault の設定。
type BackURLOptions struct {
	// RelativeURLRoot はサブディレクトリ運用時のルート（例 "/redmine"、なければ空）。
	RelativeURLRoot string
	// Referer が true なら back_url が無効なとき Referer ヘッダへ戻る（redirect_to_referer_or）。
	Referer bool
	// Status は既定 302。
	Status int
}

// RedirectBackOrDefault は ApplicationController#redirect_back_or_default の移植。
// params[:back_url] が ValidateBackURL を通ればそこへ、Referer 指定なら Referer へ、
// それ以外は def へリダイレクトする。back_url を使った場合 true を返す。
func RedirectBackOrDefault(w http.ResponseWriter, r *http.Request, def string, opts BackURLOptions) bool {
	if back, ok := ValidateBackURL(r, ParamsOf(r).String("back_url"), opts.RelativeURLRoot); ok {
		Redirect(w, r, back, opts.Status)
		return true
	}
	if opts.Referer {
		RedirectToRefererOr(w, r, def, opts.Status)
		return false
	}
	Redirect(w, r, def, opts.Status)
	return false
}

// RedirectToRefererOr は redirect_to_referer_or 相当（Referer は検証しない。Redmine と同じ）。
func RedirectToRefererOr(w http.ResponseWriter, r *http.Request, def string, status int) {
	if ref := r.Header.Get("Referer"); ref != "" {
		Redirect(w, r, ref, status)
		return
	}
	Redirect(w, r, def, status)
}

// cgiUnescape は Ruby の CGI.unescape 相当（"+" は空白、不正な % はそのまま）。
func cgiUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '+':
			b.WriteByte(' ')
		case c == '%' && i+2 < len(s):
			h, ok1 := unhex(s[i+1])
			l, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(h<<4 | l)
				i += 2
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// addressableURI は Addressable::URI.parse の結果のうち validate_back_url に必要な部分。
type addressableURI struct {
	scheme    *string
	authority *string
	host      *string
	port      int // 0 = なし
	path      string
	query     *string
	fragment  *string
}

// Addressable::URI::URIREGEX（RFC 3986 付録 B）
var addressableURIRe = regexp.MustCompile(`\A(?s:(([^:/?#]+):)?(//([^/?#]*))?([^?#]*)(\?([^#]*))?(#(.*))?)\z`)

var (
	addrUserinfoRe   = regexp.MustCompile(`^([^\[\]]*)@`)
	addrPortRe       = regexp.MustCompile(`:([^:@\[\]]*?)$`)
	addrSchemeRe     = regexp.MustCompile(`(?i)\A[a-z][a-z0-9.+\-]*\z`)
	addrHostBadRe    = regexp.MustCompile(`[<>{}/\\?#@"\s]`)
	addrPortDigitsRe = regexp.MustCompile(`^\d+$`)
	addrIPv6Re       = regexp.MustCompile(`^\[(.*)\]$`)
	addrIPv6OKRe     = regexp.MustCompile(`^[A-Za-z0-9\-._~!$&'()*+,;=:]*$`)
)

// ip_based? なスキーム（Addressable::URI.port_mapping のキー）
var addrIPBasedSchemes = map[string]bool{
	"http": true, "https": true, "ftp": true, "tftp": true, "sftp": true, "ssh": true,
	"svn+ssh": true, "telnet": true, "nntp": true, "gopher": true, "wais": true,
	"ldap": true, "prospero": true,
}

func strp(s string) *string { return &s }

// parseAddressable は Addressable::URI.parse と validate を移植したもの（不正なら ok=false）。
func parseAddressable(s string) (*addressableURI, bool) {
	m := addressableURIRe.FindStringSubmatchIndex(s)
	if m == nil {
		return nil, false
	}
	group := func(i int) *string {
		if m[2*i] < 0 {
			return nil
		}
		return strp(s[m[2*i]:m[2*i+1]])
	}
	u := &addressableURI{}
	u.scheme = group(2)
	u.authority = group(4)
	if p := group(5); p != nil {
		u.path = *p
	}
	u.query = group(7)
	u.fragment = group(9)

	if u.scheme != nil {
		if !addrSchemeRe.MatchString(*u.scheme) {
			return nil, false
		}
		if strings.TrimSpace(*u.scheme) == "" {
			u.scheme = nil
		}
	}
	if u.authority != nil {
		a := *u.authority
		host := addrUserinfoRe.ReplaceAllString(a, "")
		var port *string
		if pm := addrPortRe.FindStringSubmatch(host); pm != nil {
			port = strp(pm[1])
		}
		host = addrPortRe.ReplaceAllString(host, "")
		u.host = strp(host)
		if port != nil && *port != "" {
			pu := cgiUnescapePath(*port)
			if !addrPortDigitsRe.MatchString(pu) {
				return nil, false
			}
			n, err := strconv.Atoi(pu)
			if err != nil {
				return nil, false
			}
			u.port = n // 0 は nil 扱い
		}
	}
	// validate
	if u.scheme != nil && addrIPBasedSchemes[strings.ToLower(*u.scheme)] &&
		(u.host == nil || *u.host == "") && u.path == "" {
		return nil, false
	}
	if u.path != "" && !strings.HasPrefix(u.path, "/") && u.authority != nil {
		return nil, false
	}
	if strings.HasPrefix(u.path, "//") && u.authority == nil {
		return nil, false
	}
	if u.host != nil {
		h := *u.host
		if addrHostBadRe.MatchString(h) {
			return nil, false
		}
		if im := addrIPv6Re.FindStringSubmatch(h); im != nil && !addrIPv6OKRe.MatchString(im[1]) {
			return nil, false
		}
	}
	return u, true
}

// cgiUnescapePath は Addressable::URI.unencode_component 相当（"+" は変換しない）。
func cgiUnescapePath(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			h, ok1 := unhex(s[i+1])
			l, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(h<<4 | l)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

var (
	backURLPathRe      = regexp.MustCompile(`\A/([^/]|\z)`)
	backURLForbiddenRe = regexp.MustCompile(`/(login|account/register|account/lost_password)`)
	normPathRe         = regexp.MustCompile(`^[^/:]*:`)
)

// ValidateBackURL は ApplicationController#validate_back_url の移植。
// 妥当ならリダイレクト先の相対 URL（パス?クエリ#フラグメント）と true を返す。
//
//   - 空・空白のみは不可
//   - CGI.unescape 後に ".." を含むものは不可
//   - スキーム・ホスト・ポートが指定されていればリクエストと一致すること
//   - スキームと authority を除いた結果が "/" + 非 "/" 文字（または "/" のみ）で始まること
//   - /login, /account/register, /account/lost_password を含むものは不可
//   - relativeURLRoot が設定されていればそれで始まること
func ValidateBackURL(r *http.Request, backURL, relativeURLRoot string) (string, bool) {
	if strings.TrimSpace(backURL) == "" {
		return "", false
	}
	if strings.Contains(cgiUnescape(backURL), "..") {
		return "", false
	}
	u, ok := parseAddressable(backURL)
	if !ok {
		return "", false
	}
	if u.scheme != nil && *u.scheme != "" && *u.scheme != RequestScheme(r) {
		return "", false
	}
	if u.host != nil && strings.TrimSpace(*u.host) != "" && *u.host != RequestHost(r) {
		return "", false
	}
	if u.port != 0 && u.port != RequestPort(r) {
		return "", false
	}
	// omit!(:scheme, :authority) 後の validate: "//" で始まるパスは不可
	if strings.HasPrefix(u.path, "//") {
		return "", false
	}
	// to_s: スキームなしで "foo:bar" のような曖昧なパスはエラー
	if u.path != "" && normPathRe.MatchString(u.path) {
		return "", false
	}
	path := u.path
	if u.query != nil {
		path += "?" + *u.query
	}
	if u.fragment != nil {
		path += "#" + *u.fragment
	}
	if !backURLPathRe.MatchString(path) {
		return "", false
	}
	if backURLForbiddenRe.MatchString(path) {
		return "", false
	}
	if relativeURLRoot != "" && !strings.HasPrefix(path, relativeURLRoot) {
		return "", false
	}
	return path, true
}
