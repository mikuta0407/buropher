package doorkeeper

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// 参照 Redmine 6.1.2（Doorkeeper 5.8.2）が発行・保存した値（ref の DB から取得）。
const (
	refPlainToken  = "8_0EFTDnqUVlCUZ9J_doiYXbjW1QIL3zyyzeDqN-LHk"
	refHashedToken = "4b8d4da385b213f25d57703f9cee7bac0c84918e80807f0b032d1b27ea2ee2de"
	refPlainCode   = "2EcN82YCnUWGNX0NE1vuilC9gUpWpLuqZs-UELefd5c"
	refHashedCode  = "02ca826a973f8cac6884369ec81dc74ef5e73a805415e48b399d9ba3eaa42e12"
	refPlainSecret = "9ArJc1xqulN-245jG7rHZrxIzOQqQydnH5P07591V-g"
	refBCrypt      = "$2a$12$IMXEuvPu3VJdFfIy7WWQu.TVu4qpnAwatXVEjE0UVYaFpxCQWYdGK"
)

func TestTokenFormat(t *testing.T) {
	re := regexp.MustCompile(`\A[A-Za-z0-9_-]{43}\z`)
	seen := map[string]bool{}
	for range 20 {
		tok := GenerateToken()
		if !re.MatchString(tok) {
			t.Fatalf("token %q is not urlsafe_base64(32)", tok)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
}

func TestHashTokenMatchesRedmine(t *testing.T) {
	if got := HashToken(refPlainToken); got != refHashedToken {
		t.Errorf("HashToken(access token) = %s, want %s", got, refHashedToken)
	}
	if got := HashToken(refPlainCode); got != refHashedCode {
		t.Errorf("HashToken(code) = %s, want %s", got, refHashedCode)
	}
}

func TestSecretMatchesRedmineBCrypt(t *testing.T) {
	if !SecretMatches(refPlainSecret, refBCrypt) {
		t.Error("secret hashed by Redmine does not match")
	}
	if SecretMatches("wrong", refBCrypt) || SecretMatches(refPlainSecret, "plain") || SecretMatches("", "") {
		t.Error("unexpected match")
	}
	h, err := HashSecret("abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$2a$12$") || !SecretMatches("abc", h) {
		t.Errorf("HashSecret = %q", h)
	}
}

func TestCodeChallengeS256(t *testing.T) {
	// RFC 7636 Appendix B
	if got := CodeChallengeS256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("CodeChallengeS256 = %s", got)
	}
}

func TestScopes(t *testing.T) {
	def := DefaultScopes()
	if !slices.Equal(def, []string{"view_project", "search_project", "view_members"}) {
		t.Errorf("DefaultScopes = %v", def)
	}
	server := ServerScopes()
	if !slices.Contains(server, "admin") || !slices.Contains(server, "view_issues") || server[0] != "view_project" {
		t.Errorf("ServerScopes = %v", server)
	}
	if got := ParseScopes(" view_issues  add_issues view_issues "); !slices.Equal(got, []string{"view_issues", "add_issues"}) {
		t.Errorf("ParseScopes = %v", got)
	}
	app := []string{"view_project", "view_issues"}
	cases := []struct {
		scope string
		app   []string
		want  bool
	}{
		{"view_issues", app, true},
		{"view_issues add_issues", app, false},
		{"admin", nil, true},
		{"bogus", nil, false},
		{"", app, false},
		{"view_issues\tview_project", app, false},
	}
	for _, c := range cases {
		if got := ScopeValid(c.scope, server, c.app); got != c.want {
			t.Errorf("ScopeValid(%q, %v) = %v", c.scope, c.app, got)
		}
	}
	if got := AllowedScopes(def, []string{"view_issues", "view_members", "view_project"}); !slices.Equal(got, []string{"view_members", "view_project"}) {
		t.Errorf("AllowedScopes = %v", got)
	}
	if !ScopesMatch([]string{"view_project", "view_issues"}, []string{"view_issues", "view_project"}, app) {
		t.Error("ScopesMatch: same set")
	}
	if ScopesMatch([]string{"view_issues"}, []string{"view_issues", "view_project"}, app) {
		t.Error("ScopesMatch: different set")
	}
}

func TestURIBuilder(t *testing.T) {
	params := []Param{{"code", "a b*~"}, {"state", ""}}
	if got := URIWithQuery("http://127.0.0.1:12345/cb", params); got != "http://127.0.0.1:12345/cb?code=a+b*%7E" {
		t.Errorf("URIWithQuery = %s", got)
	}
	if got := URIWithQuery("https://c.example/cb?x=1&code=old", []Param{{"code", "new"}, {"state", "s"}}); got != "https://c.example/cb?x=1&code=new&state=s" {
		t.Errorf("URIWithQuery merge = %s", got)
	}
	if got := URIWithFragment("https://c.example/cb?x=1", []Param{{"error", "access_denied"}}); got != "https://c.example/cb?x=1#error=access_denied" {
		t.Errorf("URIWithFragment = %s", got)
	}
	if got := URIWithQuery("http://evil.example:80/x", []Param{{"error", "e"}}); got != "http://evil.example/x?error=e" {
		t.Errorf("URIWithQuery default port = %s", got)
	}
}
