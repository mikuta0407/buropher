package brand

import "testing"

func TestSubstituteLocale(t *testing.T) {
	cases := map[string]string{
		"Administrate this Redmine":                   "Administrate this Buropher",
		"このRedmineの管理":                                "このBuropherの管理",
		"Redmine-Benutzer":                            "Buropher-Benutzer",
		"\nRedmine and repository":                    "\nBuropher and repository",
		"see https://www.redmine.org/ for details":    "see https://www.redmine.org/ for details",
		"Redmine.org":                                 "Redmine.org",
		"X-Redmine-API-Key":                           "X-Redmine-API-Key",
		"RedmineWikiFormatting":                       "RedmineWikiFormatting",
		"Redmineko":                                   "Redmineko",
		"/etc/redmine/x":                              "/etc/redmine/x",
		"to your Redmine administrator. Redmine, ok.": "to your Buropher administrator. Buropher, ok.",
		"": "",
	}
	for in, want := range cases {
		if got := SubstituteLocale(in); got != want {
			t.Errorf("SubstituteLocale(%q) = %q, want %q", in, got, want)
		}
	}
}
