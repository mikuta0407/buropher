// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package mail はメールメッセージの組み立て（MIME）と配送（SMTP / sendmail）を行う。
//
// Redmine は ActionMailer（mail gem）でメールを組み立てる。ここでは Redmine の Mailer が出すヘッダと
// 本文構成（multipart/alternative の text/plain + text/html、plain_text_mail なら text/plain のみ）を
// 再現する。本文の描画は internal/handler（mailer.go）、HTML への CSS インライン化は inline.go（roadie 相当）。
package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Header はヘッダ 1 行（名前と値）。
type Header struct {
	Name  string
	Value string
}

// Message は 1 通のメール。
type Message struct {
	// From は From ヘッダの値（表示名付きの整形済みアドレス）。
	From string
	// To / Cc / Bcc はアドレスの一覧（Bcc はヘッダに出さず封筒にだけ使う）。
	To, Cc, Bcc []string
	Subject     string
	// MessageID は < > なしの Message-ID（空なら送信時に生成する）。
	MessageID string
	// InReplyTo / References は < > 付きの値（空なら出さない）。
	InReplyTo  string
	References string
	// Headers は上記以外のヘッダ（X-Redmine-* 等）。出力順を保つ。
	Headers []Header
	// Text は text/plain 本文。HTML が空なら text/plain だけのメールになる。
	Text string
	HTML string
	// Date は Date ヘッダ（ゼロなら送信時刻）。
	Date time.Time
}

// Header は名前（大文字小文字を区別しない）でヘッダ値を返す。
func (m *Message) Header(name string) string {
	for _, h := range m.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// SetHeader はヘッダを設定する（既存なら置き換え）。
func (m *Message) SetHeader(name, value string) {
	for i, h := range m.Headers {
		if strings.EqualFold(h.Name, name) {
			m.Headers[i].Value = value
			return
		}
	}
	m.Headers = append(m.Headers, Header{name, value})
}

// Recipients は封筒の宛先（To + Cc + Bcc、重複なし）。
func (m *Message) Recipients() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range [][]string{m.To, m.Cc, m.Bcc} {
		for _, a := range l {
			addr := a
			if p, err := mail.ParseAddress(a); err == nil {
				addr = p.Address
			}
			k := strings.ToLower(addr)
			if addr == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, addr)
		}
	}
	return out
}

// EnvelopeFrom は封筒の送信元アドレス（From のアドレス部）。
func (m *Message) EnvelopeFrom() string {
	if p, err := mail.ParseAddress(m.From); err == nil {
		return p.Address
	}
	s := strings.TrimSpace(m.From)
	if i := strings.LastIndex(s, "<"); i >= 0 {
		s = strings.TrimSuffix(s[i+1:], ">")
	}
	return s
}

// GenerateMessageID は mail gem と同じ形式の Message-ID（<ランダム@ホスト名.mail>）を作る。
func GenerateMessageID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return hex.EncodeToString(b[:]) + "@" + host + ".mail"
}

// Bytes は RFC 5322 形式のメッセージ（CRLF 改行）を返す。
// ヘッダの順序は mail gem の出力（Date, From, To, Cc, Message-ID, In-Reply-To, References, Subject,
// Mime-Version, Content-Type, Content-Transfer-Encoding, その他）に合わせる。
func (m *Message) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	mid := m.MessageID
	if mid == "" {
		mid = GenerateMessageID()
		m.MessageID = mid
	}
	w := func(name, value string) {
		if value == "" {
			return
		}
		// ヘッダインジェクション対策: 値中の CR/LF を除去する（mail gem も
		// フィールド値に生の改行を書かず符号化・畳み込みする）。件名や差出人表示名は
		// ASCII のまま検証をすり抜けうるため、折り畳み前にここで無害化する。
		value = stripHeaderCRLF(value)
		buf.WriteString(name)
		buf.WriteString(": ")
		buf.WriteString(foldHeader(len(name)+2, value))
		buf.WriteString("\r\n")
	}
	w("Date", date.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	w("From", encodeAddressList([]string{m.From}))
	w("To", encodeAddressList(m.To))
	w("Cc", encodeAddressList(m.Cc))
	w("Message-ID", "<"+mid+">")
	w("In-Reply-To", m.InReplyTo)
	w("References", m.References)
	w("Subject", encodeWord(m.Subject))
	w("Mime-Version", "1.0")
	var body bytes.Buffer
	if m.HTML == "" {
		cte, enc := encodeBody(m.Text)
		w("Content-Type", "text/plain; charset=UTF-8")
		w("Content-Transfer-Encoding", cte)
		body.WriteString(enc)
	} else {
		boundary := "--==_mimepart_" + randomHex(12)
		w("Content-Type", `multipart/alternative; boundary="`+boundary+`"; charset=UTF-8`)
		w("Content-Transfer-Encoding", "7bit")
		body.WriteString("\r\n")
		for _, p := range []struct{ ct, s string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
			cte, enc := encodeBody(p.s)
			body.WriteString("--" + boundary + "\r\n")
			body.WriteString("Content-Type: " + p.ct + "; charset=UTF-8\r\n")
			body.WriteString("Content-Transfer-Encoding: " + cte + "\r\n\r\n")
			body.WriteString(enc)
			if !strings.HasSuffix(enc, "\r\n") {
				body.WriteString("\r\n")
			}
			body.WriteString("\r\n")
		}
		body.WriteString("--" + boundary + "--\r\n")
	}
	for _, h := range m.Headers {
		w(h.Name, encodeWord(h.Value))
	}
	buf.WriteString("\r\n")
	buf.Write(body.Bytes())
	return buf.Bytes(), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// toCRLF は改行を CRLF にそろえる（mail gem の to_crlf と同じく単独の CR も改行とみなす）。
// 単独の CR を本文に残すと、"\r.\r\n" のように中継 MTA によっては本文の終わりと解釈される並びを
// 利用者の入力（チケットの説明等）から作れてしまう（SMTP smuggling）。
func toCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// encodeBody は本文の Content-Transfer-Encoding を決めて符号化する（ASCII かつ短い行なら 7bit、
// それ以外は quoted-printable と base64 の短い方。mail gem の TransferEncoding.negotiate と同じ方針）。
func encodeBody(s string) (cte, out string) {
	s = toCRLF(s)
	ascii, longLine := true, false
	for _, line := range strings.Split(s, "\r\n") {
		if len(line) > 998 {
			longLine = true
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || (s[i] < 0x20 && s[i] != '\r' && s[i] != '\n' && s[i] != '\t') {
			ascii = false
			break
		}
	}
	if ascii && !longLine {
		return "7bit", s
	}
	var qp bytes.Buffer
	qw := quotedprintable.NewWriter(&qp)
	_, _ = qw.Write([]byte(s))
	_ = qw.Close()
	b64 := wrapBase64(base64.StdEncoding.EncodeToString([]byte(s)))
	if qp.Len() <= len(b64) {
		return "quoted-printable", qp.String()
	}
	return "base64", b64
}

func wrapBase64(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76])
		b.WriteString("\r\n")
		s = s[76:]
	}
	if s != "" {
		b.WriteString(s)
		b.WriteString("\r\n")
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// encodeWord は非 ASCII のヘッダ値を RFC 2047 の encoded-word にする。
func encodeWord(s string) string {
	if isASCII(s) {
		return s
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "?")
	}
	return mime.BEncoding.Encode("UTF-8", s)
}

// encodeAddressList はアドレスの一覧をヘッダ値にする（表示名が非 ASCII なら encoded-word にする）。
func encodeAddressList(list []string) string {
	var out []string
	for _, a := range list {
		if a == "" {
			continue
		}
		if isASCII(a) {
			out = append(out, a)
			continue
		}
		if p, err := mail.ParseAddress(a); err == nil {
			out = append(out, p.String())
		} else {
			out = append(out, encodeWord(a))
		}
	}
	return strings.Join(out, ", ")
}

// foldHeader は 998 文字を超えないよう空白位置でヘッダを折り返す（78 文字目安）。
// stripHeaderCRLF はヘッダ値から CR/LF（および行区切りになりうる他の制御文字）を取り除く。
// これによりヘッダインジェクション（Subject/From 等への \r\n 混入）を防ぐ。
func stripHeaderCRLF(v string) string {
	if !strings.ContainsAny(v, "\r\n") {
		return v
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

func foldHeader(prefix int, v string) string {
	if prefix+len(v) <= 78 {
		return v
	}
	var b strings.Builder
	col := prefix
	words := strings.Split(v, " ")
	for i, w := range words {
		if i > 0 {
			if col+1+len(w) > 78 {
				b.WriteString("\r\n ")
				col = 1
			} else {
				b.WriteString(" ")
				col++
			}
		}
		b.WriteString(w)
		col += len(w)
	}
	return b.String()
}

// FormatAddress は表示名とアドレスから From ヘッダ用の値を作る（mail gem の Mail::Address#format）。
func FormatAddress(name, addr string) string {
	if name == "" {
		return addr
	}
	needQuote := strings.ContainsAny(name, `()<>[]:;@\,."`)
	if needQuote {
		name = `"` + strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `"`, `\"`) + `"`
	}
	return fmt.Sprintf("%s <%s>", name, addr)
}
