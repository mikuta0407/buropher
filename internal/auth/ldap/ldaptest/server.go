// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package ldaptest はテスト用の最小限の LDAP サーバ（プロセス内）。
//
// 対応する操作は simple bind / search（フィルタは and / or / not / equality / substrings / present /
// extensible match の LDAP_MATCHING_RULE_IN_CHAIN）/ unbind / StartTLS（extended operation）のみ。
// エントリとパスワードはメモリ上に持つ。Delay を設定すると各応答の前に待つ（タイムアウトの試験用）。
// StartTLS / LDAPS は Start 時に作る自己署名証明書（CertPEM）を使う。
package ldaptest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
)

// StartTLSOID は StartTLS の extended operation の OID。
const StartTLSOID = "1.3.6.1.4.1.1466.20037"

// InChainOID は Active Directory の LDAP_MATCHING_RULE_IN_CHAIN（ネストしたグループの所属判定）。
const InChainOID = "1.2.840.113556.1.4.1941"

// Entry はディレクトリのエントリ。属性名は大文字小文字を区別しない。
type Entry struct {
	DN       string
	Password string
	Attrs    map[string][]string
}

// Server は LDAP サーバ。
type Server struct {
	Entries []*Entry
	// Delay は各要求への応答前の待ち時間。
	Delay time.Duration
	// TLS が true なら接続直後から TLS（LDAPS）で待ち受ける。
	TLS bool
	// RequireTLS が true なら TLS でない接続の bind を confidentialityRequired（13）で拒否する。
	RequireTLS bool
	// CertPEM は Start が作る自己署名証明書（127.0.0.1 / localhost 用）の PEM。
	CertPEM []byte
	// RejectStartTLS が空でなければ、StartTLS を unavailable（52）で拒否し、この文字列を
	// diagnosticMessage に入れる（悪意あるサーバ・平文区間の中間者の再現用）。
	RejectStartTLS string

	mu    sync.Mutex
	ln    net.Listener
	binds []string
	// searchFailCode が 0 でなければ、検索ベースが searchFailBase で終わる検索（空ならすべて）は
	// エントリを返さずにこの結果コードで失敗する（SetSearchResultCode）。
	searchFailCode int64
	searchFailBase string
	// Searches は受け付けた検索のフィルタ（デバッグ・検証用）。
	searches []string
	tlsConf  *tls.Config
}

// newCert は自己署名証明書を作る。
func (s *Server) newCert() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ldaptest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	s.CertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	s.tlsConf = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	return nil
}

// Start は 127.0.0.1 のランダムなポートで待ち受けを始める。
func (s *Server) Start() (string, error) {
	if err := s.newCert(); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	if s.TLS {
		ln = tls.NewListener(ln, s.tlsConf)
	}
	s.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return ln.Addr().String(), nil
}

// Close は待ち受けを止める。
func (s *Server) Close() {
	if s.ln != nil {
		s.ln.Close()
	}
}

// Binds は受け付けた bind の DN（成否を問わない）。
func (s *Server) Binds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.binds...)
}

// Searches は受け付けた検索のフィルタ（文字列化したもの）。
func (s *Server) Searches() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.searches...)
}

func (s *Server) serve(c net.Conn) {
	defer func() { c.Close() }()
	_, secure := c.(*tls.Conn)
	for {
		p, err := ber.ReadPacket(c)
		if err != nil || len(p.Children) < 2 {
			return
		}
		id := p.Children[0].Value
		op := p.Children[1]
		if s.Delay > 0 {
			time.Sleep(s.Delay)
		}
		switch op.Tag {
		case 0: // BindRequest
			dn := ber.DecodeString(op.Children[1].Data.Bytes())
			pw := ""
			if len(op.Children) > 2 {
				pw = ber.DecodeString(op.Children[2].Data.Bytes())
			}
			s.mu.Lock()
			s.binds = append(s.binds, dn)
			s.mu.Unlock()
			code := int64(49) // invalidCredentials
			if s.RequireTLS && !secure {
				code = 13 // confidentialityRequired
			} else if dn == "" || pw == "" {
				code = 0 // 匿名・未認証 bind は許可
			} else if e := s.find(dn); e != nil && e.Password == pw {
				code = 0
			}
			s.write(c, id, result(1, code))
		case 2: // UnbindRequest
			return
		case 3: // SearchRequest
			base := strings.ToLower(ber.DecodeString(op.Children[0].Data.Bytes()))
			sizeLimit, _ := op.Children[3].Value.(int64)
			filter := op.Children[6]
			s.mu.Lock()
			s.searches = append(s.searches, describe(filter))
			s.mu.Unlock()
			var wanted []string
			for _, a := range op.Children[7].Children {
				wanted = append(wanted, ber.DecodeString(a.Data.Bytes()))
			}
			n := int64(0)
			s.mu.Lock()
			code := s.searchFailCode
			if !strings.HasSuffix(base, strings.ToLower(s.searchFailBase)) {
				code = 0
			}
			s.mu.Unlock()
			if code != 0 {
				s.write(c, id, result(5, code))
				continue
			}
			for _, e := range s.Entries {
				if base != "" && !strings.HasSuffix(strings.ToLower(e.DN), base) {
					continue
				}
				if !s.match(e, filter) {
					continue
				}
				if sizeLimit > 0 && n >= sizeLimit {
					code = 4 // sizeLimitExceeded
					break
				}
				n++
				s.write(c, id, entryPacket(e, wanted))
			}
			s.write(c, id, result(5, code))
		case 23: // ExtendedRequest
			oid := ""
			if len(op.Children) > 0 {
				oid = ber.DecodeString(op.Children[0].Data.Bytes())
			}
			if oid != StartTLSOID || secure {
				s.write(c, id, result(24, 2)) // protocolError
				continue
			}
			if s.RejectStartTLS != "" {
				s.write(c, id, resultMsg(24, 52, s.RejectStartTLS)) // unavailable
				continue
			}
			s.write(c, id, result(24, 0))
			tc := tls.Server(c, s.tlsConf)
			if err := tc.Handshake(); err != nil {
				return
			}
			c, secure = tc, true
		default:
			return
		}
	}
}

// SetSearchResultCode は以降の検索（検索ベースが base で終わるもの。空ならすべて）を
// エントリなし・結果コード code で失敗させる（0 で元に戻す）。
// 一時的な障害（busy / unavailable）や権限の誤り（insufficientAccessRights）の再現用。
func (s *Server) SetSearchResultCode(code int64, base string) {
	s.mu.Lock()
	s.searchFailCode, s.searchFailBase = code, base
	s.mu.Unlock()
}

func (s *Server) find(dn string) *Entry {
	for _, e := range s.Entries {
		if strings.EqualFold(e.DN, dn) {
			return e
		}
	}
	return nil
}

func (s *Server) write(c net.Conn, id any, op *ber.Packet) {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP Response")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	msg.AppendChild(op)
	_, _ = c.Write(msg.Bytes())
}

func result(tag ber.Tag, code int64) *ber.Packet { return resultMsg(tag, code, "") }

// resultMsg は diagnosticMessage 付きの LDAPResult。
func resultMsg(tag ber.Tag, code int64, msg string) *ber.Packet {
	p := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "Result")
	p.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "resultCode"))
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, msg, "diagnosticMessage"))
	return p
}

func entryPacket(e *Entry, wanted []string) *ber.Packet {
	p := ber.Encode(ber.ClassApplication, ber.TypeConstructed, 4, nil, "SearchResultEntry")
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.DN, "objectName"))
	attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")
	for name, vals := range e.Attrs {
		if len(wanted) > 0 && !containsFold(wanted, name) {
			continue
		}
		a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attribute")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "type"))
		set := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "vals")
		for _, v := range vals {
			set.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "val"))
		}
		a.AppendChild(set)
		attrs.AppendChild(a)
	}
	p.AppendChild(attrs)
	return p
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func (e *Entry) values(attr string) []string {
	for k, v := range e.Attrs {
		if strings.EqualFold(k, attr) {
			return v
		}
	}
	if strings.EqualFold(attr, "objectClass") {
		return []string{"top", "person"}
	}
	return nil
}

// inChain は e の attr（member 等の DN 値）が、直接またはグループ（attr を持つエントリ）を介して dn を含むか。
func (s *Server) inChain(e *Entry, attr, dn string, seen map[string]bool) bool {
	if seen[strings.ToLower(e.DN)] {
		return false
	}
	seen[strings.ToLower(e.DN)] = true
	for _, v := range e.values(attr) {
		if strings.EqualFold(v, dn) {
			return true
		}
		if sub := s.find(v); sub != nil && s.inChain(sub, attr, dn, seen) {
			return true
		}
	}
	return false
}

func str(p *ber.Packet) string { return ber.DecodeString(p.Data.Bytes()) }

func (s *Server) match(e *Entry, f *ber.Packet) bool {
	switch f.Tag {
	case 0: // and
		for _, c := range f.Children {
			if !s.match(e, c) {
				return false
			}
		}
		return true
	case 1: // or
		for _, c := range f.Children {
			if s.match(e, c) {
				return true
			}
		}
		return false
	case 2: // not
		return !s.match(e, f.Children[0])
	case 3: // equalityMatch
		want := str(f.Children[1])
		for _, v := range e.values(str(f.Children[0])) {
			if strings.EqualFold(v, want) {
				return true
			}
		}
		return false
	case 4: // substrings
		for _, v := range e.values(str(f.Children[0])) {
			if matchSubstrings(strings.ToLower(v), f.Children[1].Children) {
				return true
			}
		}
		return false
	case 7: // present
		return len(e.values(ber.DecodeString(f.Data.Bytes()))) > 0
	case 9: // extensibleMatch
		rule, attr, value := extensibleParts(f)
		if rule == InChainOID {
			return s.inChain(e, attr, value, map[string]bool{})
		}
		for _, v := range e.values(attr) {
			if strings.EqualFold(v, value) {
				return true
			}
		}
		return false
	}
	return false
}

// extensibleParts は MatchingRuleAssertion（[1] matchingRule, [2] type, [3] matchValue）を取り出す。
func extensibleParts(f *ber.Packet) (rule, attr, value string) {
	for _, c := range f.Children {
		v := ber.DecodeString(c.Data.Bytes())
		switch c.Tag {
		case 1:
			rule = v
		case 2:
			attr = v
		case 3:
			value = v
		}
	}
	return
}

func matchSubstrings(v string, parts []*ber.Packet) bool {
	pos := 0
	for _, p := range parts {
		s := strings.ToLower(ber.DecodeString(p.Data.Bytes()))
		switch p.Tag {
		case 0:
			if !strings.HasPrefix(v, s) {
				return false
			}
			pos = len(s)
		case 1:
			i := strings.Index(v[pos:], s)
			if i < 0 {
				return false
			}
			pos += i + len(s)
		case 2:
			if !strings.HasSuffix(v[pos:], s) {
				return false
			}
		}
	}
	return true
}

func describe(f *ber.Packet) string {
	switch f.Tag {
	case 0, 1, 2:
		op := map[ber.Tag]string{0: "&", 1: "|", 2: "!"}[f.Tag]
		var b strings.Builder
		b.WriteString("(" + op)
		for _, c := range f.Children {
			b.WriteString(describe(c))
		}
		return b.String() + ")"
	case 3:
		return "(" + str(f.Children[0]) + "=" + str(f.Children[1]) + ")"
	case 4:
		var b strings.Builder
		b.WriteString("(" + str(f.Children[0]) + "=")
		for i, p := range f.Children[1].Children {
			if p.Tag != 0 || i > 0 {
				b.WriteString("*")
			}
			b.WriteString(ber.DecodeString(p.Data.Bytes()))
		}
		last := f.Children[1].Children
		if len(last) == 0 || last[len(last)-1].Tag != 2 {
			b.WriteString("*")
		}
		return b.String() + ")"
	case 7:
		return "(" + ber.DecodeString(f.Data.Bytes()) + "=*)"
	case 9:
		rule, attr, value := extensibleParts(f)
		r := attr
		if rule != "" {
			r += ":" + rule
		}
		return "(" + r + ":=" + value + ")"
	}
	return "(?)"
}
