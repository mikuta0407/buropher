// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package webhook

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Validator は WebhookEndpointValidator（送信先 URL の検証。SSRF 対策）。
//
// Redmine と同じく、スキーム（http / https）・ポート（WHATWG Fetch の bad ports 以外）・ホスト
// （名前解決した全アドレスがループバック・リンクローカル・未指定・マルチキャスト・設定の
// webhook_blocklist に当たらないこと）を検査する。送信時は再度名前解決し、検証を通ったアドレスに
// だけ接続する（DNS rebinding 対策。executor.go）。
//
// buropher 独自の強化: マルチキャストは 224.0.0.0/24 だけでなく全域（224.0.0.0/4・ff00::/8）、
// 0.0.0.0/8 と 255.255.255.255 も拒否する。ホスト名の末尾の "." は除いてからブロックリストと照合する。
// inet_aton 形式の数値ホスト（"2130706433"・"0x7f000001"・"127.1" など）は IPv4 として検査する。
type Validator struct {
	blockedNets  []netip.Prefix
	blockedHosts []string // 完全一致（小文字）
	blockedWild  []string // "*.example.com" の "example.com"（小文字）
	// Resolver はホスト名を IP アドレスに解決する（nil なら net.DefaultResolver.LookupNetIP）。テスト用。
	Resolver func(ctx context.Context, host string) ([]netip.Addr, error)
	// LookupTimeout は名前解決のタイムアウト（0 なら 10 秒）。
	LookupTimeout time.Duration
}

// NewValidator は設定の webhook_blocklist（IP アドレス・CIDR・ホスト名・"*.ドメイン"）から Validator を作る。
// IP / CIDR として解釈できない項目はホスト名として扱う（Redmine の blocked_hosts と同じ）。
func NewValidator(blocklist []string) *Validator {
	v := &Validator{}
	for _, b := range blocklist {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if p, err := netip.ParsePrefix(b); err == nil {
			v.blockedNets = append(v.blockedNets, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(b); err == nil {
			a = a.Unmap()
			v.blockedNets = append(v.blockedNets, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		h := strings.TrimSuffix(strings.ToLower(b), ".")
		if rest, ok := strings.CutPrefix(h, "*."); ok {
			v.blockedWild = append(v.blockedWild, rest)
		} else {
			v.blockedHosts = append(v.blockedHosts, h)
		}
	}
	return v
}

// errInvalidHost は検証で拒否したホスト。
var errInvalidHost = errors.New("webhook: host is not allowed")

// BadPorts は WebhookEndpointValidator::BAD_PORTS（WHATWG Fetch の port blocking）。
var BadPorts = map[int]bool{
	1: true, 7: true, 9: true, 11: true, 13: true, 15: true, 17: true, 19: true, 20: true, 21: true,
	22: true, 23: true, 25: true, 37: true, 42: true, 43: true, 53: true, 69: true, 77: true, 79: true,
	87: true, 95: true, 101: true, 102: true, 103: true, 104: true, 109: true, 110: true, 111: true,
	113: true, 115: true, 117: true, 119: true, 123: true, 135: true, 137: true, 139: true, 143: true,
	161: true, 179: true, 389: true, 427: true, 465: true, 512: true, 513: true, 514: true, 515: true,
	526: true, 530: true, 531: true, 532: true, 540: true, 548: true, 554: true, 556: true, 563: true,
	587: true, 601: true, 636: true, 989: true, 990: true, 993: true, 995: true, 1719: true, 1720: true,
	1723: true, 2049: true, 3659: true, 4045: true, 4190: true, 5060: true, 5061: true, 6000: true,
	6566: true, 6665: true, 6666: true, 6667: true, 6668: true, 6669: true, 6679: true, 6697: true,
	10080: true,
}

// Endpoint は検証用に分解した URL。
type Endpoint struct {
	URL  *url.URL
	Host string // 角括弧を除いたホスト（URI#hostname）
	Port int
}

// ParseEndpoint は URL を分解する（スキーム・ホスト・ポートの形式だけを見る。名前解決はしない）。
func ParseEndpoint(raw string) (*Endpoint, bool) {
	if strings.ContainsAny(raw, " \t\r\n") {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return nil, false
	}
	if !ValidScheme(u.Scheme) {
		return nil, false
	}
	host := u.Hostname()
	if host == "" {
		return nil, false
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if ps := u.Port(); ps != "" {
		p, err := strconv.Atoi(ps)
		if err != nil {
			return nil, false
		}
		port = p
	}
	return &Endpoint{URL: u, Host: host, Port: port}, true
}

// ValidScheme は valid_scheme?（http / https）。
func ValidScheme(s string) bool { return s == "http" || s == "https" }

// ValidPort は valid_port?。
func ValidPort(p int) bool { return p >= 1 && p <= 65535 && !BadPorts[p] }

// SafeURL は safe_webhook_uri?（スキーム・ホスト・ポートがすべて有効か）。
func (v *Validator) SafeURL(ctx context.Context, raw string) bool {
	e, ok := ParseEndpoint(raw)
	if !ok || !ValidPort(e.Port) {
		return false
	}
	_, err := v.ValidIPs(ctx, e.Host)
	return err == nil
}

// IPsForURL は ips_for_uri（送信時に接続してよいアドレス。無効なら空）。
func (v *Validator) IPsForURL(ctx context.Context, raw string) []netip.Addr {
	e, ok := ParseEndpoint(raw)
	if !ok {
		return nil
	}
	ips, err := v.ValidIPs(ctx, e.Host)
	if err != nil {
		return nil
	}
	return ips
}

// hostBlocked はホスト名がブロックリストに当たるか。
func (v *Validator) hostBlocked(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	for _, b := range v.blockedHosts {
		if h == b {
			return true
		}
	}
	for _, w := range v.blockedWild {
		if h == w || strings.HasSuffix(h, "."+w) {
			return true
		}
	}
	return false
}

// ValidIPs は valid_ips(host): 名前解決した全アドレスが許可されていればその一覧を返す。
// 1 つでも禁止されたアドレスがあれば（または解決できなければ）エラー。
func (v *Validator) ValidIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	if host == "" || v.hostBlocked(host) {
		return nil, errInvalidHost
	}
	addrs, err := v.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, errInvalidHost
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		a = a.Unmap().WithZone("")
		if !v.AllowedAddr(a) {
			return nil, errInvalidHost
		}
		out = append(out, a)
	}
	return out, nil
}

// AllowedAddr は 1 アドレスが送信先として許可されるか。
func (v *Validator) AllowedAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsUnspecified() || a.IsLoopback() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return false
	}
	if a.Is4() {
		b := a.As4()
		if b[0] == 0 || a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return false
		}
	}
	for _, n := range v.blockedNets {
		if n.Contains(a) {
			return false
		}
	}
	return true
}

func (v *Validator) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	if a, ok := parseInetAton(host); ok {
		return []netip.Addr{a}, nil
	}
	timeout := v.LookupTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if v.Resolver != nil {
		return v.Resolver(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// parseInetAton は inet_aton 形式の IPv4（"127.1"・"2130706433"・"0x7f000001"・"0177.0.1"）を解釈する。
// getaddrinfo はこれらを IPv4 アドレスとして返すため、名前解決に回さずに同じアドレスとして検査する。
func parseInetAton(s string) (netip.Addr, bool) {
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return netip.Addr{}, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return netip.Addr{}, false
		}
		base := 10
		digits := p
		switch {
		case strings.HasPrefix(p, "0x") || strings.HasPrefix(p, "0X"):
			base, digits = 16, p[2:]
			if digits == "" {
				digits = "0"
			}
		case len(p) > 1 && p[0] == '0':
			base, digits = 8, p[1:]
		}
		n, err := strconv.ParseUint(digits, base, 32)
		if err != nil {
			return netip.Addr{}, false
		}
		nums[i] = n
	}
	var v uint64
	last := len(nums) - 1
	for i := 0; i < last; i++ {
		if nums[i] > 255 {
			return netip.Addr{}, false
		}
		v |= nums[i] << (24 - 8*uint(i))
	}
	if nums[last] >= 1<<(8*uint(4-last)) {
		return netip.Addr{}, false
	}
	v |= nums[last]
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
}
