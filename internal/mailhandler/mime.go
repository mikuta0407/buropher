package mailhandler

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
)

// このファイルは受信メールの解析（Ruby の mail gem のうち MailHandler が使う部分）。
//
// mail gem と同じく寛容に解析する: ヘッダの行末は CRLF / LF のどちらでもよく、
// 不正な構文でも可能な限り値を取り出す。

// HeaderField はヘッダの 1 行（折り返しを戻した生の値）。
type HeaderField struct {
	Name  string
	Value string
}

// Part はメール（または MIME パート）。mail gem の Mail::Message / Mail::Part に相当する。
type Part struct {
	Header []HeaderField
	// Body は本文の生のバイト列（転送エンコーディングを戻す前）。
	Body []byte
	// Parts は multipart の子パート。
	Parts []*Part
	// asciiOnly はメール全体が ASCII のみか（mail gem は ASCII のみのとき改行を CRLF に揃える）。
	asciiOnly bool
}

// Parse は生のメールを解析する（Mail.new(raw_mail.b)）。
func Parse(raw []byte) *Part {
	ascii := isASCII(raw)
	// mbox 形式の先頭行（"From addr date"）は除く（set_envelope_header）
	if bytes.HasPrefix(raw, []byte("From ")) {
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			line := strings.TrimRight(string(raw[:i]), "\r")
			if !strings.Contains(strings.SplitN(line, " ", 3)[1], ":") && len(strings.Fields(line)) > 1 {
				raw = raw[i+1:]
			}
		}
	}
	return parsePart(bytes.TrimLeft(raw, " \t\r\n"), ascii, 0)
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

// maxDepth は入れ子の multipart の上限（異常なメールで無限に再帰しないため）。
const maxDepth = 50

func parsePart(raw []byte, ascii bool, depth int) *Part {
	p := &Part{asciiOnly: ascii}
	headerPart, body := splitHeaderBody(raw)
	p.Header = parseHeader(headerPart)
	p.Body = body
	if depth < maxDepth && p.MainType() == "multipart" {
		if b := p.contentTypeParam("boundary"); b != "" {
			p.Parts = splitMultipart(body, b, ascii, depth+1)
		}
	}
	return p
}

// splitHeaderBody はヘッダと本文を最初の空行で分ける（HEADER_SEPARATOR = /\r?\n\r?\n/）。
func splitHeaderBody(raw []byte) ([]byte, []byte) {
	// 先頭が空行ならヘッダなし
	if bytes.HasPrefix(raw, []byte("\r\n")) {
		return nil, raw[2:]
	}
	if bytes.HasPrefix(raw, []byte("\n")) {
		return nil, raw[1:]
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\n' {
			continue
		}
		j := i + 1
		if j < len(raw) && raw[j] == '\r' {
			j++
		}
		if j < len(raw) && raw[j] == '\n' {
			end := i
			if end > 0 && raw[end-1] == '\r' {
				end--
			}
			return raw[:end], raw[j+1:]
		}
		if j >= len(raw) {
			// ヘッダのみ（末尾が改行）
			return raw[:i], nil
		}
	}
	return raw, nil
}

// parseHeader はヘッダを行ごとに分け、折り返しを戻す（継続行の改行のみを除く）。
func parseHeader(b []byte) []HeaderField {
	var fields []HeaderField
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && len(fields) > 0 {
			fields[len(fields)-1].Value += line
			continue
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		fields = append(fields, HeaderField{Name: name, Value: line[i+1:]})
	}
	for i := range fields {
		fields[i].Value = strings.TrimSpace(fields[i].Value)
	}
	return fields
}

// splitMultipart は Mail::Body#split!（境界で分割し、前文と後文を捨てる）。
func splitMultipart(body []byte, boundary string, ascii bool, depth int) []*Part {
	delim := []byte("--" + boundary)
	var starts []int // 各境界行の次の行の先頭
	var ends []int   // 各境界行の直前（改行を含まない）
	closed := false
	pos := 0
	for pos <= len(body) && !closed {
		i := bytes.Index(body[pos:], delim)
		if i < 0 {
			break
		}
		at := pos + i
		// 行頭でなければ境界ではない
		if at > 0 && body[at-1] != '\n' {
			pos = at + len(delim)
			continue
		}
		after := at + len(delim)
		rest := body[after:]
		isClose := bytes.HasPrefix(rest, []byte("--"))
		if isClose {
			after += 2
		}
		// 境界の後は行末まで空白のみ（(?=\s*$)）
		eol := bytes.IndexByte(body[after:], '\n')
		var tail []byte
		next := len(body)
		if eol >= 0 {
			tail = body[after : after+eol]
			next = after + eol + 1
		} else {
			tail = body[after:]
		}
		if len(bytes.TrimSpace(tail)) != 0 {
			pos = after
			continue
		}
		end := at
		if end > 0 && body[end-1] == '\n' {
			end--
			if end > 0 && body[end-1] == '\r' {
				end--
			}
		}
		ends = append(ends, end)
		starts = append(starts, next)
		if isClose {
			closed = true
		}
		pos = next
	}
	var parts []*Part
	for k := 0; k+1 < len(starts); k++ {
		seg := body[starts[k]:ends[k+1]]
		if k > 0 && len(bytes.TrimSpace(seg)) == 0 {
			continue
		}
		parts = append(parts, parsePart(seg, ascii, depth))
	}
	if !closed && len(starts) > 0 {
		// 終端の境界が無い場合は最後のパートを末尾まで
		seg := body[starts[len(starts)-1]:]
		if len(bytes.TrimSpace(seg)) != 0 {
			parts = append(parts, parsePart(seg, ascii, depth))
		}
	}
	return parts
}

// ---------------------------------------------------------------- ヘッダの取得

// HeaderValue は名前（大文字小文字を区別しない）が一致する最初のヘッダの値。
func (p *Part) HeaderValue(name string) (string, bool) {
	for _, f := range p.Header {
		if strings.EqualFold(f.Name, name) {
			return f.Value, true
		}
	}
	return "", false
}

// HeaderValues は名前が一致するすべてのヘッダの値。
func (p *Part) HeaderValues(name string) []string {
	var out []string
	for _, f := range p.Header {
		if strings.EqualFold(f.Name, name) {
			out = append(out, f.Value)
		}
	}
	return out
}

// Subject は件名（エンコードされた語を戻したもの。無ければ ""）。
func (p *Part) Subject() string {
	v, ok := p.HeaderValue("Subject")
	if !ok {
		return ""
	}
	return decodeWords(v)
}

// From は From ヘッダのアドレス。
func (p *Part) From() []Address { return p.addresses("From") }

// To は To ヘッダのアドレス。
func (p *Part) To() []Address { return p.addresses("To") }

// Cc は Cc ヘッダのアドレス。
func (p *Part) Cc() []Address { return p.addresses("Cc") }

// Bcc は Bcc ヘッダのアドレス。
func (p *Part) Bcc() []Address { return p.addresses("Bcc") }

func (p *Part) addresses(name string) []Address {
	var out []Address
	for _, v := range p.HeaderValues(name) {
		out = append(out, ParseAddressList(v)...)
	}
	return out
}

var msgIDRe = regexp.MustCompile(`<([^<>]*)>`)

// MessageIDs は In-Reply-To / References のようなメッセージ ID の列（山括弧を除いたもの）。
func (p *Part) MessageIDs(name string) []string {
	var out []string
	for _, v := range p.HeaderValues(name) {
		ms := msgIDRe.FindAllStringSubmatch(v, -1)
		if len(ms) == 0 {
			for _, w := range strings.Fields(v) {
				out = append(out, w)
			}
			continue
		}
		for _, m := range ms {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

// ---------------------------------------------------------------- Content-Type / Content-Disposition

// MimeType は mime_type（"type/subtype" の小文字。Content-Type が無ければ ""）。
func (p *Part) MimeType() string {
	v, ok := p.HeaderValue("Content-Type")
	if !ok {
		return ""
	}
	t, _ := parseParamHeader(v)
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return ""
	}
	if !strings.Contains(t, "/") {
		// mail gem は "text" のような不完全な値を補う（text → text/plain）
		switch t {
		case "text":
			return "text/plain"
		case "multipart":
			return "multipart/mixed"
		}
		return t + "/"
	}
	return t
}

// MainType は Content-Type の主タイプ。
func (p *Part) MainType() string {
	t := p.MimeType()
	if i := strings.IndexByte(t, '/'); i >= 0 {
		return t[:i]
	}
	return t
}

func (p *Part) contentTypeParam(name string) string {
	v, ok := p.HeaderValue("Content-Type")
	if !ok {
		return ""
	}
	_, params := parseParamHeader(v)
	return params.get(name)
}

// Charset は charset（Content-Type が無い・charset が無ければ ""）。
func (p *Part) Charset() string { return p.contentTypeParam("charset") }

// Filename は find_attachment（Content-Disposition の filename、Content-Type の name、Content-Location の順。
// エンコードされた語は戻す）。添付でなければ ""。
func (p *Part) Filename() string {
	name := ""
	if v, ok := p.HeaderValue("Content-Disposition"); ok {
		_, params := parseParamHeader(v)
		name = params.filename()
	}
	if name == "" {
		if v, ok := p.HeaderValue("Content-Type"); ok {
			_, params := parseParamHeader(v)
			name = params.filename()
		}
	}
	if name == "" {
		if v, ok := p.HeaderValue("Content-Location"); ok {
			name = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	if name == "" {
		return ""
	}
	return decodeWords(name)
}

// IsAttachment は attachment?。
func (p *Part) IsAttachment() bool { return p.Filename() != "" }

// AllParts は all_parts（子孫のパートを深さ優先で）。
func (p *Part) AllParts() []*Part {
	var out []*Part
	for _, c := range p.Parts {
		out = append(out, c)
		out = append(out, c.AllParts()...)
	}
	return out
}

// Attachments は attachments（Mail::AttachmentsList。message/rfc822 は中のメールの添付）。
func (p *Part) Attachments() []*Part {
	var out []*Part
	for _, c := range p.Parts {
		switch {
		case c.MimeType() == "message/rfc822":
			out = append(out, Parse(c.DecodedBody()).Attachments()...)
		case len(c.Parts) == 0:
			if c.IsAttachment() {
				out = append(out, c)
			}
		default:
			out = append(out, c.Attachments()...)
		}
	}
	return out
}

// ---------------------------------------------------------------- 本文

// transferEncoding は Content-Transfer-Encoding（無ければ本文が ASCII のみなら 7bit、それ以外は 8bit）。
func (p *Part) transferEncoding() string {
	if v, ok := p.HeaderValue("Content-Transfer-Encoding"); ok {
		if e := strings.ToLower(strings.TrimSpace(v)); e != "" {
			return e
		}
	}
	if isASCII(p.Body) {
		return "7bit"
	}
	return "8bit"
}

// DecodedBody は body.decoded（転送エンコーディングを戻したバイト列）。
func (p *Part) DecodedBody() []byte {
	body := p.Body
	if p.asciiOnly {
		body = toCRLF(body)
	}
	switch p.transferEncoding() {
	case "base64":
		return decodeBase64(body)
	case "quoted-printable":
		// mail gem の to_lf は ASCII のみの文字列（バイナリ文字列で安全に変換できるもの）だけを変換する
		b := decodeQuotedPrintable(toCRLF(body))
		if isASCII(b) {
			return toLF(b)
		}
		return b
	case "7bit":
		return toLF(body)
	}
	return body
}

func toCRLF(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
}

func toLF(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
}

// decodeBase64 は String#unpack("m")（base64 以外の文字は無視し、不完全な末尾は捨てる）。
func decodeBase64(b []byte) []byte {
	clean := make([]byte, 0, len(b))
	for _, c := range b {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' {
			clean = append(clean, c)
		} else if c == '=' {
			break
		}
	}
	if len(clean)%4 == 1 {
		// 1 文字だけ余った末尾は復号できない
		clean = clean[:len(clean)-1]
	}
	out, err := base64.RawStdEncoding.DecodeString(string(clean))
	if err != nil {
		return nil
	}
	return out
}

// decodeQuotedPrintable は String#unpack("M")（ソフト改行 "=\r\n" を除き =XX を戻す。不正な列はそのまま）。
func decodeQuotedPrintable(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c != '=' {
			out = append(out, c)
			continue
		}
		if i+1 < len(b) && b[i+1] == '\n' {
			i++
			continue
		}
		if i+2 < len(b) && b[i+1] == '\r' && b[i+2] == '\n' {
			i += 2
			continue
		}
		if i+2 < len(b) && isHex(b[i+1]) && isHex(b[i+2]) {
			out = append(out, unhex(b[i+1])<<4|unhex(b[i+2]))
			i += 2
			continue
		}
		// 行末の "=" と空白（"=  \r\n"）もソフト改行
		j := i + 1
		for j < len(b) && (b[j] == ' ' || b[j] == '\t') {
			j++
		}
		if j < len(b) && (b[j] == '\n' || (b[j] == '\r' && j+1 < len(b) && b[j+1] == '\n')) {
			if b[j] == '\r' {
				j++
			}
			i = j
			continue
		}
		if j >= len(b) {
			i = j
			continue
		}
		out = append(out, c)
	}
	return out
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// ---------------------------------------------------------------- パラメータ付きヘッダ

type paramList struct {
	keys   []string
	values map[string]string
}

func (pl *paramList) set(k, v string) {
	if pl.values == nil {
		pl.values = map[string]string{}
	}
	if _, ok := pl.values[k]; !ok {
		pl.keys = append(pl.keys, k)
	}
	pl.values[k] = v
}

// get は名前のパラメータ（RFC 2231 の継続・文字コード指定を解決する）。
func (pl paramList) get(name string) string {
	name = strings.ToLower(name)
	if v, ok := pl.values[name]; ok {
		return v
	}
	// name* / name*0 / name*0* ...
	if v, ok := pl.values[name+"*"]; ok {
		return decodeRFC2231(v, true)
	}
	var sb strings.Builder
	charset := ""
	found := false
	for i := 0; ; i++ {
		idx := itoa(i)
		if v, ok := pl.values[name+"*"+idx+"*"]; ok {
			if i == 0 {
				parts := strings.SplitN(v, "'", 3)
				if len(parts) == 3 {
					charset = parts[0]
					v = parts[2]
				}
			}
			s, err := url.PathUnescape(strings.ReplaceAll(v, "+", "%2B"))
			if err != nil {
				s = v
			}
			sb.WriteString(s)
			found = true
			continue
		}
		if v, ok := pl.values[name+"*"+idx]; ok {
			sb.WriteString(v)
			found = true
			continue
		}
		break
	}
	if !found {
		return ""
	}
	return toUTF8([]byte(sb.String()), charset, "")
}

func (pl paramList) filename() string {
	if v := pl.get("filename"); v != "" {
		return v
	}
	return pl.get("name")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// decodeRFC2231 は charset'lang'%XX 形式の値を戻す。
func decodeRFC2231(v string, extended bool) string {
	charset := ""
	if extended {
		parts := strings.SplitN(v, "'", 3)
		if len(parts) == 3 {
			charset = parts[0]
			v = parts[2]
		}
	}
	s, err := url.PathUnescape(strings.ReplaceAll(v, "+", "%2B"))
	if err != nil {
		s = v
	}
	return toUTF8([]byte(s), charset, "")
}

// parseParamHeader は "value; a=b; c="d"" 形式のヘッダを値とパラメータに分ける（寛容に解析する）。
func parseParamHeader(v string) (string, paramList) {
	var pl paramList
	segs := splitParams(v)
	if len(segs) == 0 {
		return "", pl
	}
	value := strings.TrimSpace(segs[0])
	for _, s := range segs[1:] {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(s[:i]))
		val := strings.TrimSpace(s[i+1:])
		if len(val) >= 2 && val[0] == '"' {
			val = unquote(val)
		}
		pl.set(k, val)
	}
	return value, pl
}

// splitParams は引用符の外の ";" で分ける。
func splitParams(v string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\\' && inQuote && i+1 < len(v):
			cur.WriteByte(c)
			cur.WriteByte(v[i+1])
			i++
			continue
		case c == '"':
			inQuote = !inQuote
		case c == ';' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	out = append(out, cur.String())
	return out
}

// unquote は引用符で囲まれた文字列の中身（\x のエスケープを戻す。閉じ引用符が無くてもよい）。
func unquote(s string) string {
	if strings.HasPrefix(s, `"`) {
		s = s[1:]
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			sb.WriteByte(s[i+1])
			i++
			continue
		}
		if c == '"' {
			break
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// ---------------------------------------------------------------- エンコードされた語（RFC 2047）

var encodedWordRe = regexp.MustCompile(`=\?([^?\s]+)\?([BbQq])\?([^?]*)\?=`)

// decodeWords は Mail::Encodings.value_decode（隣接するエンコードされた語の間の空白は除き、
// 同じ文字コードの語はバイト列を連結してから変換する）。生の 8 ビット文字は UTF-8 として扱う。
func decodeWords(s string) string {
	locs := encodedWordRe.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return replaceInvalidUTF8([]byte(s), "?")
	}
	var sb strings.Builder
	prevEnd := 0
	var pendCharset string
	var pend []byte
	flush := func() {
		if pend != nil {
			cs := pendCharset
			if i := strings.IndexByte(cs, '*'); i >= 0 {
				cs = cs[:i] // RFC 2231 の言語指定
			}
			sb.WriteString(toUTF8(pend, cs, ""))
			pend = nil
		}
	}
	for _, loc := range locs {
		between := s[prevEnd:loc[0]]
		adjacent := pend != nil && strings.TrimSpace(between) == ""
		if !adjacent {
			flush()
			sb.WriteString(replaceInvalidUTF8([]byte(between), "?"))
		}
		charset := s[loc[2]:loc[3]]
		enc := s[loc[4]:loc[5]]
		text := s[loc[6]:loc[7]]
		var raw []byte
		if enc == "B" || enc == "b" {
			raw = decodeBase64([]byte(text))
		} else {
			raw = decodeQuotedPrintable([]byte(strings.ReplaceAll(text, "_", " ")))
		}
		if pend != nil && !strings.EqualFold(pendCharset, charset) {
			flush()
		}
		if pend == nil {
			pendCharset = charset
			pend = []byte{}
		}
		pend = append(pend, raw...)
		prevEnd = loc[1]
	}
	flush()
	sb.WriteString(replaceInvalidUTF8([]byte(s[prevEnd:]), "?"))
	return sb.String()
}

// ---------------------------------------------------------------- アドレス

// Address はアドレス（Mail::Address の address / local / domain / display_name / comments）。
type Address struct {
	// Address は "local@domain"。
	Address     string
	Local       string
	Domain      string
	DisplayName string
	Comments    []string
}

// ParseAddressList はアドレスのリストを寛容に解析する。
func ParseAddressList(v string) []Address {
	var out []Address
	for _, item := range splitAddressList(v) {
		if a, ok := parseAddress(item); ok {
			out = append(out, a)
		}
	}
	return out
}

// splitAddressList は引用符・山括弧・コメントの外の "," で分ける（グループ構文 "name: a, b;" も展開する）。
func splitAddressList(v string) []string {
	var out []string
	var cur strings.Builder
	inQuote, depth, angle := false, 0, false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\\' && i+1 < len(v):
			cur.WriteByte(c)
			cur.WriteByte(v[i+1])
			i++
			continue
		case inQuote:
			if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case depth > 0:
		case c == '<':
			angle = true
		case c == '>':
			angle = false
		case !angle && c == ':':
			// グループ名を捨てる
			cur.Reset()
			continue
		case !angle && (c == ',' || c == ';'):
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	out = append(out, cur.String())
	return out
}

// parseAddress は 1 件のアドレスを解析する。
func parseAddress(s string) (Address, bool) {
	var a Address
	// コメントを取り出す
	var rest strings.Builder
	inQuote, depth := false, 0
	var comment strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			if depth > 0 {
				comment.WriteByte(s[i+1])
			} else {
				rest.WriteByte(c)
				rest.WriteByte(s[i+1])
			}
			i++
			continue
		}
		switch {
		case depth == 0 && c == '"':
			inQuote = !inQuote
			rest.WriteByte(c)
		case !inQuote && c == '(':
			if depth > 0 {
				comment.WriteByte(c)
			}
			depth++
		case !inQuote && c == ')' && depth > 0:
			depth--
			if depth == 0 {
				a.Comments = append(a.Comments, decodeWords(strings.TrimSpace(comment.String())))
				comment.Reset()
				rest.WriteByte(' ')
			} else {
				comment.WriteByte(c)
			}
		case depth > 0:
			comment.WriteByte(c)
		default:
			rest.WriteByte(c)
		}
	}
	str := strings.TrimSpace(rest.String())
	var spec string
	if i := lastUnquotedIndex(str, '<'); i >= 0 {
		j := strings.IndexByte(str[i:], '>')
		if j < 0 {
			spec = str[i+1:]
		} else {
			spec = str[i+1 : i+j]
		}
		name := strings.TrimSpace(str[:i])
		a.DisplayName = displayName(name)
	} else {
		spec = str
	}
	spec = strings.TrimSpace(spec)
	// route-addr（@a,@b:user@host）の経路を除く
	if strings.HasPrefix(spec, "@") {
		if k := strings.IndexByte(spec, ':'); k >= 0 {
			spec = spec[k+1:]
		}
	}
	spec = strings.Join(strings.Fields(spec), "")
	if spec == "" {
		return a, false
	}
	if at := strings.LastIndexByte(spec, '@'); at >= 0 {
		a.Local = unquoteLocal(spec[:at])
		a.Domain = spec[at+1:]
		a.Address = a.Local + "@" + a.Domain
	} else {
		a.Local = unquoteLocal(spec)
		a.Address = a.Local
	}
	return a, true
}

func unquoteLocal(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return unquote(s)
	}
	return s
}

func lastUnquotedIndex(s string, ch byte) int {
	idx := -1
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && c == ch {
			idx = i
		}
	}
	return idx
}

// displayName は表示名（引用符を外し、エンコードされた語を戻す）。
func displayName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) >= 2 && s[0] == '"' {
		return decodeWords(unquote(s))
	}
	return decodeWords(strings.Join(strings.Fields(s), " "))
}
