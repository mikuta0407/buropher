package normalize

import (
	"strings"
	"testing"
)

func TestBrand(t *testing.T) {
	pairs := [][2]string{
		{"<title>Issues - eCookbook - Redmine</title>", "<title>Issues - eCookbook - Buropher</title>"},
		{"<title>Redmine</title>", "<title>Buropher</title>"},
		{"<title>Redmine: Issues</title>", "<title>Buropher: Issues</title>"},
		{"<title>Redmine 404 error</title>", "<title>Buropher 404 error</title>"},
		{`<meta content="Redmine" name="description">`, `<meta content="Buropher" name="description">`},
		{"<h1>Redmine</h1>", "<h1>Buropher</h1>"},
		{`<input id="settings_app_title" name="settings[app_title]" size="30" type="text" value="Redmine">`, `<input id="settings_app_title" name="settings[app_title]" size="30" type="text" value="Buropher">`},
		{`<generator uri="https://www.redmine.org/">&#xA;Redmine  </generator>`, `<generator uri="https://github.com/mikuta0407/buropher">&#xA;Buropher  </generator>`},
		{"<div id=\"footer\">\n  Powered by\n  <a href=\"https://www.redmine.org/\" rel=\"noopener\" target=\"_blank\">Redmine</a>\n  © 2006-2026 Jean-Philippe Lang\n</div>",
			"<div id=\"footer\">\n  Powered by\n  <a href=\"https://github.com/mikuta0407/buropher\" rel=\"noopener\" target=\"_blank\">Buropher</a>\n  , based on\n  <a href=\"https://www.redmine.org/\" rel=\"noopener\" target=\"_blank\">Redmine</a>\n  © 2006-2026 Jean-Philippe Lang\n</div>"},
		{"Select or update the Redmine user mapped to each username found in the repository log.", "Select or update the Buropher user mapped to each username found in the repository log."},
		{"<p>Welcome to Redmine, an open-source, flexible project management software.</p>", "<p>Welcome to Buropher, an open-source, flexible project management software.</p>"},
	}
	for _, p := range pairs {
		a, b := Brand(p[0]), Brand(p[1])
		if a != b {
			t.Errorf("Brand mismatch:\n ref  %q\n cand %q", a, b)
		}
		if !strings.Contains(a, BrandPlaceholder) && !strings.Contains(a, "{{FOOTER}}") {
			t.Errorf("no placeholder: %q", a)
		}
	}
	// データ（利用者名）や互換識別子は変えない
	for _, s := range []string{`<a class="user active" href="/users/1">Redmine Admin</a>`, "X-Redmine-API-Key", `<firstname>Redmine</firstname>`} {
		if got := Brand(s); got != s {
			t.Errorf("Brand(%q) = %q", s, got)
		}
	}
}
