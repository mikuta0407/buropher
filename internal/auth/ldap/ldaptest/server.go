// Package ldaptest はテスト用の最小限の LDAP サーバ（プロセス内）。
//
// 対応する操作は simple bind / search（フィルタは and / or / not / equality / substrings / present）/ unbind のみ。
// エントリとパスワードはメモリ上に持つ。Delay を設定すると各応答の前に待つ（タイムアウトの試験用）。
package ldaptest

import (
	"net"
	"strings"
	"sync"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
)

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

	mu    sync.Mutex
	ln    net.Listener
	binds []string
	// Searches は受け付けた検索のフィルタ（デバッグ・検証用）。
	searches []string
}

// Start は 127.0.0.1 のランダムなポートで待ち受けを始める。
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
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
	defer c.Close()
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
			if dn == "" || pw == "" {
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
			code := int64(0)
			for _, e := range s.Entries {
				if base != "" && !strings.HasSuffix(strings.ToLower(e.DN), base) {
					continue
				}
				if !match(e, filter) {
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
		default:
			return
		}
	}
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

func result(tag ber.Tag, code int64) *ber.Packet {
	p := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "Result")
	p.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "resultCode"))
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
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
	if strings.EqualFold(attr, "objectClass") {
		return []string{"top", "person"}
	}
	for k, v := range e.Attrs {
		if strings.EqualFold(k, attr) {
			return v
		}
	}
	return nil
}

func str(p *ber.Packet) string { return ber.DecodeString(p.Data.Bytes()) }

func match(e *Entry, f *ber.Packet) bool {
	switch f.Tag {
	case 0: // and
		for _, c := range f.Children {
			if !match(e, c) {
				return false
			}
		}
		return true
	case 1: // or
		for _, c := range f.Children {
			if match(e, c) {
				return true
			}
		}
		return false
	case 2: // not
		return !match(e, f.Children[0])
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
	}
	return false
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
	}
	return "(?)"
}
