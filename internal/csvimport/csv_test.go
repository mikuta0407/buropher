package csvimport

import (
	"errors"
	"strings"
	"testing"
)

func rowsOf(t *testing.T, data string, o Options) ([][]string, error) {
	t.Helper()
	var out [][]string
	err := Parse([]byte(data), o, func(r Row) bool {
		var row []string
		for _, v := range r {
			if v == nil {
				row = append(row, "<nil>")
			} else {
				row = append(row, *v)
			}
		}
		out = append(out, row)
		return true
	})
	return out, err
}

func TestParseRubyCompatible(t *testing.T) {
	cases := []struct {
		in   string
		sep  string
		want string
	}{
		{"a;b;c\n1;;\"x;y\"\n", ";", "a|b|c / 1|<nil>|x;y"},
		{"a,\"\"\r\n\r\nb", ",", "a| / [] / b"},
		{"\"a\"\"b\",c\n", ",", "a\"b|c"},
		{"\"multi\nline\";x\n", ";", "multi\nline|x"},
		{";\n", ";", "<nil>|<nil>"},
	}
	for _, c := range cases {
		rows, err := rowsOf(t, c.in, Options{Separator: c.sep})
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		var parts []string
		for _, r := range rows {
			if len(r) == 0 {
				parts = append(parts, "[]")
			} else {
				parts = append(parts, strings.Join(r, "|"))
			}
		}
		if got := strings.Join(parts, " / "); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"subject;description\nfoo;\"Unclosed quoted field": "Unclosed quoted field in line 2.",
		"a;b\"c\n":     "Illegal quoting in line 1.",
		"\"a\"b;c\n":   "Any value after quoted field isn't allowed in line 1.",
		"a;b\r\nc\nd;": "Unquoted fields do not allow new line <\"\\n\"> in line 2.",
	}
	for in, want := range cases {
		_, err := rowsOf(t, in, Options{Separator: ";"})
		var me *MalformedError
		if !errors.As(err, &me) || err.Error() != want {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}
}

func TestDecode(t *testing.T) {
	if s, err := Decode([]byte("\xEF\xBB\xBFabc"), "UTF-8"); err != nil || s != "abc" {
		t.Errorf("bom: %q %v", s, err)
	}
	if s, err := Decode([]byte("fran\xe7ais"), "ISO-8859-1"); err != nil || s != "français" {
		t.Errorf("latin1: %q %v", s, err)
	}
	var me *MalformedError
	if _, err := Decode([]byte("fran\xe7ais"), "UTF-8"); !errors.As(err, &me) || !me.InvalidEncoding() {
		t.Errorf("utf8 invalid: %v", err)
	}
	if _, err := Decode([]byte("\xc8\x80"), "Shift_JIS"); !errors.As(err, &me) || !me.InvalidEncoding() {
		t.Errorf("sjis invalid: %v", err)
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		s, f, want string
	}{
		{"10/07/2015", "%d/%m/%Y", "2015-07-10"},
		{"04/15/2015", "%d/%m/%Y", ""},
		{"2019/05/28", "%Y/%m/%d", "2019-05-28"},
		{"2015-7-8", "%Y-%m-%d", "2015-07-08"},
		{"2015-02-30", "%Y-%m-%d", ""},
		{"2015-02-03x", "%Y-%m-%d", ""},
	}
	for _, c := range cases {
		d, ok := ParseDate(c.s, c.f)
		got := ""
		if ok {
			got = d.Format("2006-01-02")
		}
		if got != c.want {
			t.Errorf("%q %q: got %q want %q", c.s, c.f, got, c.want)
		}
	}
}

func TestGuess(t *testing.T) {
	if GuessSeparator([]byte("a;b,c;d")) != ";" || GuessSeparator([]byte("a;b,c")) != "," {
		t.Error("separator")
	}
	if GuessWrapper([]byte("'a'")) != "'" || GuessWrapper([]byte("abc")) != `"` {
		t.Error("wrapper")
	}
	if e, _ := GuessEncoding([]byte("fran\xe7ais"), "UTF-8,ISO-8859-1"); e != "ISO-8859-1" {
		t.Errorf("guess %q", e)
	}
	if e, _ := GuessEncoding([]byte("fran\xe7ais"), ""); e != "" {
		t.Errorf("guess %q", e)
	}
	if CanonicalEncoding("iso8859-1", []string{"UTF-8", "ISO-8859-1"}) != "ISO-8859-1" {
		t.Error("canonical")
	}
	opts := DateFormatOptions()
	if opts[0][0] != "YYYY-MM-DD" || opts[4][0] != "DD.MM.YYYY" {
		t.Errorf("date format options %v", opts)
	}
}
