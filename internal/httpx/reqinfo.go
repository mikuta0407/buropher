package httpx

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DefaultTrustedProxies は ActionDispatch::RemoteIp::TRUSTED_PROXIES と同じ。
var DefaultTrustedProxies = mustPrefixes(
	"127.0.0.0/8", "::1/128", "fc00::/7", "10.0.0.0/8",
	"172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fe80::/10",
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// ProxyConfig はリバースプロキシの信頼設定。
type ProxyConfig struct {
	// TrustedProxies は信頼するプロキシのアドレス範囲（nil なら DefaultTrustedProxies）。
	TrustedProxies []netip.Prefix
	// TrustAllForwarded が true なら Rails と同様に、直接の接続元が信頼プロキシでなくても
	// X-Forwarded-For / Client-IP を解釈する（Rails の RemoteIp は常にそうする）。
	// false（既定）なら接続元が信頼プロキシの場合のみ転送ヘッダを使う（なりすまし対策）。
	// X-Forwarded-Proto / Host / Port は常に接続元が信頼プロキシの場合のみ使う。
	TrustAllForwarded bool
}

func (c *ProxyConfig) trusted() []netip.Prefix {
	if c == nil || c.TrustedProxies == nil {
		return DefaultTrustedProxies
	}
	return c.TrustedProxies
}

func (c *ProxyConfig) isTrusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range c.trusted() {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

type remoteInfo struct {
	ip          string
	peerTrusted bool
	proxyConfig *ProxyConfig
}

var ipSplitRe = regexp.MustCompile(`[,\s]+`)

// ipsFrom は Rails の ips_from: カンマ/空白区切りから有効な単一 IP のみ返す。
func ipsFrom(header string) []netip.Addr {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	var out []netip.Addr
	for _, s := range ipSplitRe.Split(header, -1) {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a.Unmap())
		}
	}
	return out
}

func peerAddr(r *http.Request) (netip.Addr, bool) {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// computeRemoteIP は ActionDispatch::RemoteIp::GetIp#calculate_ip の移植。
// 2 つ目の戻り値は直接の接続元が信頼プロキシかどうか。
func computeRemoteIP(r *http.Request, cfg *ProxyConfig) (string, bool) {
	remote, ok := peerAddr(r)
	if !ok {
		return r.RemoteAddr, false
	}
	peerTrusted := cfg.isTrusted(remote)
	if !peerTrusted && (cfg == nil || !cfg.TrustAllForwarded) {
		return remote.String(), false
	}
	clientIPs := ipsFrom(r.Header.Get("Client-Ip"))
	slices.Reverse(clientIPs)
	forwarded := ipsFrom(strings.Join(r.Header.Values("X-Forwarded-For"), ","))
	slices.Reverse(forwarded)
	// IP なりすましチェック: Client-IP が X-Forwarded-For に含まれなければ Client-IP を無視する
	// （Rails は IpSpoofAttackError を送出するが、ここでは安全側に倒して無視する）
	if len(clientIPs) > 0 && len(forwarded) > 0 && !slices.Contains(forwarded, clientIPs[len(clientIPs)-1]) {
		clientIPs = nil
	}
	ips := append(forwarded, clientIPs...)
	// filter_proxies(ips + [remote_addr]).first || ips.last || remote_addr
	for _, a := range append(append([]netip.Addr(nil), ips...), remote) {
		if !cfg.isTrusted(a) {
			return a.String(), peerTrusted
		}
	}
	if len(ips) > 0 {
		return ips[len(ips)-1].String(), peerTrusted
	}
	return remote.String(), peerTrusted
}

// RemoteIPMiddleware はクライアント IP（request.remote_ip）を計算してコンテキストに格納する。
// RequestScheme / RequestHost 等が転送ヘッダを信頼するかどうかもここで決まる。
func RemoteIPMiddleware(cfg *ProxyConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, trusted := computeRemoteIP(r, cfg)
			ctx := context.WithValue(r.Context(), ctxRemoteIP, &remoteInfo{ip: ip, peerTrusted: trusted, proxyConfig: cfg})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RemoteIP は request.remote_ip を返す（RemoteIPMiddleware 未実行なら既定設定で計算）。
func RemoteIP(r *http.Request) string {
	if ri, ok := r.Context().Value(ctxRemoteIP).(*remoteInfo); ok {
		return ri.ip
	}
	ip, _ := computeRemoteIP(r, nil)
	return ip
}

// peerIsTrustedProxy は直接の接続元が信頼プロキシかを返す。
func peerIsTrustedProxy(r *http.Request) bool {
	var cfg *ProxyConfig
	if ri, ok := r.Context().Value(ctxRemoteIP).(*remoteInfo); ok {
		if ri.peerTrusted {
			return true
		}
		cfg = ri.proxyConfig
	}
	a, ok := peerAddr(r)
	return ok && cfg.isTrusted(a)
}

func firstForwarded(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func lastForwarded(v string) string {
	if i := strings.LastIndexByte(v, ','); i >= 0 {
		v = v[i+1:]
	}
	return strings.TrimSpace(v)
}

// RequestScheme は "http" / "https" を返す（Rack の scheme）。
// TLS 接続、または信頼プロキシからの X-Forwarded-Ssl: on / X-Forwarded-Scheme / X-Forwarded-Proto を見る。
func RequestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if peerIsTrustedProxy(r) {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on") {
			return "https"
		}
		if s := firstForwarded(r.Header.Get("X-Forwarded-Scheme")); s == "http" || s == "https" {
			return s
		}
		if s := firstForwarded(r.Header.Get("X-Forwarded-Proto")); s == "http" || s == "https" {
			return s
		}
	}
	return "http"
}

// requestAuthority は host[:port] を返す（信頼プロキシなら X-Forwarded-Host の最後の値）。
func requestAuthority(r *http.Request) string {
	if peerIsTrustedProxy(r) {
		if h := lastForwarded(r.Header.Get("X-Forwarded-Host")); h != "" {
			return h
		}
	}
	return r.Host
}

func splitAuthority(a string) (host string, port int) {
	if strings.HasPrefix(a, "[") {
		if i := strings.Index(a, "]"); i >= 0 {
			host = a[:i+1]
			if p, err := strconv.Atoi(strings.TrimPrefix(a[i+1:], ":")); err == nil {
				port = p
			}
			return
		}
	}
	if i := strings.LastIndexByte(a, ':'); i >= 0 && strings.Count(a, ":") == 1 {
		if p, err := strconv.Atoi(a[i+1:]); err == nil {
			return a[:i], p
		}
		return a[:i], 0
	}
	return a, 0
}

// RequestHost は request.host を返す（ポートを除く）。
func RequestHost(r *http.Request) string {
	h, _ := splitAuthority(requestAuthority(r))
	return h
}

// RequestPort は request.port を返す（明示されていなければスキームの既定ポート）。
func RequestPort(r *http.Request) int {
	if _, p := splitAuthority(requestAuthority(r)); p != 0 {
		return p
	}
	if peerIsTrustedProxy(r) {
		if p, err := strconv.Atoi(firstForwarded(r.Header.Get("X-Forwarded-Port"))); err == nil && p > 0 {
			return p
		}
	}
	if RequestScheme(r) == "https" {
		return 443
	}
	return 80
}

// RequestHostWithPort は request.host_with_port（既定ポートなら省略）を返す。
func RequestHostWithPort(r *http.Request) string {
	host, port := RequestHost(r), RequestPort(r)
	scheme := RequestScheme(r)
	if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
		return host
	}
	return host + ":" + strconv.Itoa(port)
}

// RequestBaseURL は request.base_url（"https://example.com:8080"）を返す。
func RequestBaseURL(r *http.Request) string {
	return RequestScheme(r) + "://" + RequestHostWithPort(r)
}

// URLOptions は Setting.host_name / protocol から作る URL 生成オプション（Mailer.default_url_options）。
type URLOptions struct {
	Protocol   string // "http" / "https"
	Host       string
	Port       string // 空なら既定
	ScriptName string // サブディレクトリ（例 "/redmine"）
}

var hostNameRe = regexp.MustCompile(`(?i)\A(https?://)?(.+?)(:(\d+))?(/.+)?\z`)

// URLOptionsFromSettings は Redmine の Mailer.default_url_options を移植したもの。
// hostName は Setting.host_name（"example.com:3000/redmine" のような値もありうる）。
func URLOptionsFromSettings(protocol, hostName string) URLOptions {
	o := URLOptions{Protocol: protocol}
	if m := hostNameRe.FindStringSubmatch(hostName); m != nil {
		o.Host, o.Port, o.ScriptName = m[2], m[4], m[5]
	} else {
		o.Host = hostName
	}
	return o
}

// BaseURL は "protocol://host[:port]script_name" を返す（メール内リンク等の絶対 URL の基点）。
func (o URLOptions) BaseURL() string {
	p := o.Protocol
	if p == "" {
		p = "http"
	}
	s := p + "://" + o.Host
	if o.Port != "" {
		s += ":" + o.Port
	}
	return s + o.ScriptName
}

// ---- リクエスト ID ----

var requestIDSanitizeRe = regexp.MustCompile(`[^\w\-@]`)

// RequestIDMiddleware は ActionDispatch::RequestId の移植。
// 受信した X-Request-Id を [\w\-@] 以外を除去して 255 文字に切り詰め、空なら UUID を生成し、
// レスポンスヘッダ X-Request-Id に設定する。
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestIDSanitizeRe.ReplaceAllString(r.Header.Get("X-Request-Id"), "")
		if len(id) > 255 {
			id = id[:255]
		}
		if id == "" {
			id = newUUID()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestID はリクエスト ID を返す（RequestIDMiddleware 未実行なら ""）。
func RequestID(r *http.Request) string {
	id, _ := r.Context().Value(ctxRequestID).(string)
	return id
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
