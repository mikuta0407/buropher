// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

func FuzzFormat(f *testing.F) {
	for _, s := range []string{
		"h1. Title\n\n* a\n** b\n# c\n\n|_. h |\n| c |\n\n<pre><code class=\"ruby\">x</code></pre>\n\nbq. q\n\n\"link\":http://example.com !img.png! fn1. x",
		"p(class#id){color:red}[en]. text @code@ *bold* _em_ -del- +ins+ ^sup^ ~sub~ ??cite?? %span%",
	} {
		f.Add(s)
	}
	for _, s := range []string{
		"\"x\":javascript:alert(1) \"y\":JaVaScRiPt:x !javascript:x! !data:text/html,x! \"z\":vbscript:x",
		"<script>alert(1)</script><img src=x onerror=alert(1)> <a href=\"javascript:x\">a</a> <notextile><b onclick=x>b</b></notextile>",
		"p{background:url(javascript:x)}. x\n\np{color:expression(alert(1))}. y\n\n%{color:red\"onmouseover=\"x}z%",
		"p[en\" onclick=\"x]. a\n\np(cls\" onclick=\"x#id\"x). b\n\n!(cls\"x)img.png(t\"itle)!:http://x\" onclick=\"y",
		"<pre><code class=\"ruby\" onclick=\"x\">a</code></pre> <pre onclick=x>y</pre> <redpre#1> @<script>@",
		"\"a\":http://x/\"onmouseover=\"alert(1) http://x/\"><script>alert(1)</script> www.x.com/\"<b>",
		"\"x\":http://a/onmouseover=alert(1)// ABC(q :redsh#1:) <pre><code class=\"q :redsh#1:\">z</code></pre>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		var out string
		secoracle.Bounded(t, len(src), 0, func() { out = Format(src, nil) })
		if err := secoracle.CheckHTML(out, secoracle.HTMLPolicy{BareElement: secoracle.TextileBareElement}); err != nil {
			t.Fatalf("input %q\noutput %q\n%v", src, out, err)
		}
		_, _ = GetSection(src, 1)
		_, _ = UpdateSection(src, 1, "x", "")
	})
}
