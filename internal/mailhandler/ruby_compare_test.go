package mailhandler

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestParseAgainstMailGem は Parse の結果を mail gem と比較する（全フィクスチャ）。
// BUROPHER_RUBY_COMPARE=1 と GEM_PATH（mail gem を含む。例: _reference/vendor_bundle/ruby/3.3.0）を
// 設定したときだけ動く（参照環境での確認用）。
func TestParseAgainstMailGem(t *testing.T) {
	script := rubyScript(t, "mail.rb")
	files, _ := filepath.Glob(filepath.Join("testdata", "mail_handler", "*.eml"))
	out, err := exec.Command("ruby", append([]string{script}, files...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		Subject     string   `json:"subject"`
		From        []string `json:"from"`
		To          []string `json:"to"`
		Cc          []string `json:"cc"`
		IDs         []string `json:"ids"`
		Plain       string   `json:"plain"`
		Attachments [][]any  `json:"attachments"`
		Error       string   `json:"error"`
	}
	var want map[string]result
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	addrs := func(as []Address) []string {
		out := []string{}
		for _, a := range as {
			out = append(out, a.Address)
		}
		return out
	}
	nz := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	for _, f := range files {
		name := filepath.Base(f)
		w := want[name]
		if w.Error != "" {
			t.Logf("%s: mail gem error %s", name, w.Error)
			continue
		}
		p := Parse(readFixture(t, name))
		// subject_japanese_3: mail gem は ISO-2022-JP の NEC 特殊文字（①）を U+FFFD にするが、
		// x/text の ISO2022JP は ① に変換する（意図的な差）
		if got := p.Subject(); got != w.Subject && name != "subject_japanese_3.eml" {
			t.Errorf("%s: subject %q, want %q", name, got, w.Subject)
		}
		if got := addrs(p.From()); !slices.Equal(got, nz(w.From)) {
			t.Errorf("%s: from %q, want %q", name, got, w.From)
		}
		if got := addrs(p.To()); !slices.Equal(got, nz(w.To)) {
			t.Errorf("%s: to %q, want %q", name, got, w.To)
		}
		if got := addrs(p.Cc()); !slices.Equal(got, nz(w.Cc)) {
			t.Errorf("%s: cc %q, want %q", name, got, w.Cc)
		}
		ids := append(nz(p.MessageIDs("In-Reply-To")), p.MessageIDs("References")...)
		if !slices.Equal(ids, nz(w.IDs)) {
			t.Errorf("%s: ids %q, want %q", name, ids, w.IDs)
		}
		r := &receiver{email: p}
		var parts []*Part
		for _, c := range p.AllParts() {
			if c.MimeType() == "text/plain" {
				parts = append(parts, c)
			}
		}
		plain := rawPartsText(r, parts)
		if strings.TrimSpace(plain) == "" && len(p.AllParts()) == 0 {
			plain = rawPartsText(r, []*Part{p})
		}
		if plain != w.Plain {
			t.Errorf("%s: plain\n got %q\nwant %q", name, plain, w.Plain)
		}
		var atts [][]any
		for _, a := range p.Attachments() {
			atts = append(atts, []any{a.Filename(), a.MimeType(), float64(len(a.DecodedBody()))})
		}
		gotJSON, _ := json.Marshal(atts)
		wantJSON, _ := json.Marshal(w.Attachments)
		if string(gotJSON) != string(wantJSON) && !(len(atts) == 0 && len(w.Attachments) == 0) {
			t.Errorf("%s: attachments %s, want %s", name, gotJSON, wantJSON)
		}
	}
}

func rubyScript(t *testing.T, name string) string {
	t.Helper()
	if os.Getenv("BUROPHER_RUBY_COMPARE") == "" {
		t.Skip("BUROPHER_RUBY_COMPARE is not set")
	}
	return filepath.Join("testdata", "ruby", name)
}

// rawPartsText は email_parts_to_text の文字コード変換まで（plain_text_body_to_text をかける前）。
func rawPartsText(_ *receiver, parts []*Part) string {
	var out []string
	for _, p := range parts {
		if p.IsAttachment() {
			continue
		}
		out = append(out, toUTF8(p.DecodedBody(), p.Charset(), "?"))
	}
	return strings.Join(out, "\r\n")
}

// TestHTMLToTextAgainstLoofah は HTMLToText を Redmine の HtmlParser（Loofah / Nokogiri）と比較する。
// BUROPHER_RUBY_COMPARE=1 と GEM_PATH（loofah を含む）を設定したときだけ動く。
func TestHTMLToTextAgainstLoofah(t *testing.T) {
	script := rubyScript(t, "html.rb")
	cases := [][2]string{
		{`<html><head><title>T</title><style>p{}</style></head><body>Hello <b>bold</b> <i>it</i> <a href="http://x/">link</a> <a href="http://y/"></a><br><h2>Head</h2><p>para</p><table><tr><th>A</th><td>1</td></tr></table><!-- c --></body></html>`, "textile"},
		{`<html><head><title>T</title><style>p{}</style></head><body>Hello <b>bold</b> <i>it</i><br><h2>Head</h2><p>para</p><table><tr><th>A</th><td>1</td></tr></table></body></html>`, "base"},
		{`<div>line1<div>nested <strong>x</strong></div></div><ul><li>a</li><li>b</li></ul>  text   with   spaces<pre>  pre  </pre>`, "textile"},
		{`<p>caf&eacute; &amp; &lt;tag&gt; &nbsp;nbsp</p><blockquote>quoted<br/>line</blockquote>`, "base"},
		{`<html><body><p class=MsoNormal>Hi<o:p></o:p></p><p class=MsoNormal><o:p>&nbsp;</o:p></p><p>Bye</p></body></html>`, "textile"},
	}
	for _, name := range []string{"outlook_2010_html_only.eml", "outlook_web_access_2010_html_only.eml", "ticket_html_only.eml"} {
		p := Parse(readFixture(t, name))
		for _, c := range p.AllParts() {
			if c.MimeType() == "text/html" {
				p = c
			}
		}
		cases = append(cases, [2]string{toUTF8(p.DecodedBody(), p.Charset(), "?"), "textile"})
	}
	in, _ := json.Marshal(cases)
	cmd := exec.Command("ruby", script)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ruby: %v\n%s", err, stderr.String())
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		f := c[1]
		if f == "base" {
			f = ""
		}
		if got := HTMLToText(c[0], f); got != want[i] {
			t.Errorf("case %d:\n got %q\nwant %q", i, got, want[i])
		}
	}
}
