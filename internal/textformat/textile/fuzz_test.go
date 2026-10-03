package textile

import "testing"

func FuzzFormat(f *testing.F) {
	for _, s := range []string{
		"h1. Title\n\n* a\n** b\n# c\n\n|_. h |\n| c |\n\n<pre><code class=\"ruby\">x</code></pre>\n\nbq. q\n\n\"link\":http://example.com !img.png! fn1. x",
		"p(class#id){color:red}[en]. text @code@ *bold* _em_ -del- +ins+ ^sup^ ~sub~ ??cite?? %span%",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_ = Format(src, nil)
		_, _ = GetSection(src, 1)
		_, _ = UpdateSection(src, 1, "x", "")
	})
}
