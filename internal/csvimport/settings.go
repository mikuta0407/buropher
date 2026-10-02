package csvimport

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// DateFormats は Import::DATE_FORMATS。
var DateFormats = []string{
	"%Y-%m-%d",
	"%d/%m/%Y",
	"%m/%d/%Y",
	"%Y/%m/%d",
	"%d.%m.%Y",
	"%d-%m-%Y",
}

// DateFormatOptions は ImportsHelper#date_format_options（[表示, 値] の列）。
func DateFormatOptions() [][2]string {
	out := make([][2]string, 0, len(DateFormats))
	for _, f := range DateFormats {
		label := strings.ReplaceAll(f, "%", "")
		label = strings.NewReplacer("d", "DD", "m", "MM", "Y", "YYYY").Replace(label)
		out = append(out, [2]string{label, f})
	}
	return out
}

// ValidDateFormat は DATE_FORMATS に含まれれば true。
func ValidDateFormat(f string) bool {
	for _, x := range DateFormats {
		if x == f {
			return true
		}
	}
	return false
}

// GenerateFilename は Redmine::Utils.random_hex(16)。
func GenerateFilename() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ValidFilename は filepath の /\A[0-9a-f]+\z/ の判定。
func ValidFilename(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ReadHead は read_file_head(max_read_bytes)（max を超える場合は最後の LF までに切り詰める）。
func ReadHead(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	chunk := data[:max]
	if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
		return chunk[:i+1]
	}
	return chunk
}

// GuessSeparator は [',', ';'].max_by {|sep| content.count(sep)}（同数なら先の ","）。
func GuessSeparator(content []byte) string {
	if bytes.Count(content, []byte(";")) > bytes.Count(content, []byte(",")) {
		return ";"
	}
	return ","
}

// GuessWrapper は ['"', "'"].max_by {|q| content.count(q)}。
func GuessWrapper(content []byte) string {
	if bytes.Count(content, []byte("'")) > bytes.Count(content, []byte(`"`)) {
		return "'"
	}
	return `"`
}

// GuessEncoding は Redmine::CodesetUtil.guess_encoding（Setting.repositories_encodings を順に試し、
// 正しいバイト列と判定された最初のものを返す。無ければ ""）。ok=false は解釈できない名前があった
// （Ruby では例外になり、set_default_settings の rescue で推定をやめる）。
func GuessEncoding(content []byte, repositoriesEncodings string) (string, bool) {
	var encs []string
	for _, e := range strings.Split(repositoriesEncodings, ",") {
		if e = strings.TrimSpace(e); e != "" {
			encs = append(encs, e)
		}
	}
	if len(encs) == 0 {
		encs = []string{"UTF-8"}
	}
	for _, e := range encs {
		if !Known(e) {
			return "", false
		}
		c, _ := lookup(e)
		var ok bool
		switch {
		case c.utf8:
			// force_encoding は BOM を除かない
			_, err := Decode(append([]byte("\xEF\xBB\xBF"), content...), e)
			ok = err == nil
		default:
			ok = Valid(content, e)
		}
		if ok {
			return e, true
		}
	}
	return "", true
}

// CanonicalEncoding は Setting::ENCODINGS のうち enc と同じもの（大文字小文字の違い、または同じ
// Encoding の別名）を返す（無ければ ""）。
func CanonicalEncoding(enc string, encodings []string) string {
	for _, e := range encodings {
		if strings.EqualFold(e, enc) {
			return e
		}
	}
	key := encodingKey(enc)
	for _, e := range encodings {
		if encodingKey(e) == key && key != "" {
			return e
		}
	}
	return ""
}

// encodingKey は Encoding.find の同一判定用のキー（別名を正規化する）。
func encodingKey(name string) string {
	n := strings.ToUpper(strings.TrimSpace(name))
	switch n {
	case "ISO8859-1":
		return "ISO-8859-1"
	case "CP65001", "UTF8":
		return "UTF-8"
	case "SJIS":
		return "WINDOWS-31J"
	case "CP1252":
		return "WINDOWS-1252"
	case "CP1251":
		return "WINDOWS-1251"
	case "CP1250":
		return "WINDOWS-1250"
	case "EUCJP":
		return "EUC-JP"
	case "ASCII":
		return "US-ASCII"
	}
	if strings.HasPrefix(n, "ISO8859-") {
		return "ISO-8859-" + strings.TrimPrefix(n, "ISO8859-")
	}
	return n
}

// ParseDate は Date.strptime(s, format)（DATE_FORMATS の書式のみ）。解釈できなければ ok=false。
func ParseDate(s, format string) (time.Time, bool) {
	si := 0
	y, m, d := 0, 0, 0
	readDigits := func(max int) (int, bool) {
		start := si
		v := 0
		for si < len(s) && s[si] >= '0' && s[si] <= '9' && (max <= 0 || si-start < max) {
			v = v*10 + int(s[si]-'0')
			si++
		}
		return v, si > start
	}
	for fi := 0; fi < len(format); fi++ {
		c := format[fi]
		if c == '%' && fi+1 < len(format) {
			fi++
			switch format[fi] {
			case 'Y':
				sign := 1
				if si < len(s) && (s[si] == '+' || s[si] == '-') {
					if s[si] == '-' {
						sign = -1
					}
					si++
				}
				// 次の書式が数字を読む指示子なら 4 桁、それ以外は桁数制限なし
				max := 0
				if fi+2 < len(format) && format[fi+1] == '%' && strings.ContainsRune("dmY", rune(format[fi+2])) {
					max = 4
				}
				v, ok := readDigits(max)
				if !ok {
					return time.Time{}, false
				}
				y = sign * v
			case 'm':
				v, ok := readDigits(2)
				if !ok {
					return time.Time{}, false
				}
				m = v
			case 'd':
				if si < len(s) && s[si] == ' ' {
					si++
				}
				v, ok := readDigits(2)
				if !ok {
					return time.Time{}, false
				}
				d = v
			default:
				return time.Time{}, false
			}
			continue
		}
		if si >= len(s) || s[si] != c {
			return time.Time{}, false
		}
		si++
	}
	if si != len(s) {
		// leftover があれば Date.strptime は ArgumentError
		return time.Time{}, false
	}
	if m < 1 || m > 12 || d < 1 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d || int(t.Month()) != m {
		return time.Time{}, false
	}
	return t, true
}
