package server_test

// test/functional/mail_handler_controller_test.rb の移植。

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
)

func mailFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "mailhandler", "testdata", "mail_handler", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newMailHandlerServer は with_settings(:mail_handler_api_enabled => enabled, :mail_handler_api_key => 'secret') のサーバー。
func newMailHandlerServer(t *testing.T, enabled string) (string, *db.DB) {
	t.Helper()
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) {
		ctx := context.Background()
		if err := a.Settings.Set(ctx, "mail_handler_api_enabled", enabled); err != nil {
			t.Fatal(err)
		}
		if err := a.Settings.Set(ctx, "mail_handler_api_key", "secret"); err != nil {
			t.Fatal(err)
		}
	})
	return ts.URL, d
}

func mailCount(t *testing.T, d *db.DB, q string) int {
	t.Helper()
	var n int
	if err := d.Get(context.Background(), &n, q); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMailHandlerShouldCreateIssue(t *testing.T) {
	base, d := newMailHandlerServer(t, "1")
	before := mailCount(t, d, `SELECT COUNT(*) FROM issues`)
	// verify_authenticity_token は行わない（CSRF トークンなしで受け付ける）
	res, _ := post(t, newClient(t), base+"/mail_handler", url.Values{"key": {"secret"}, "email": {mailFixture(t, "ticket_on_given_project.eml")}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if after := mailCount(t, d, `SELECT COUNT(*) FROM issues`); after != before+1 {
		t.Errorf("issues %d -> %d", before, after)
	}
}

func TestMailHandlerShouldCreateIssueWithOptions(t *testing.T) {
	base, d := newMailHandlerServer(t, "1")
	res, _ := post(t, newClient(t), base+"/mail_handler", url.Values{"key": {"secret"},
		"email": {mailFixture(t, "ticket_on_given_project.eml")}, "issue[is_private]": {"1"}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if n := mailCount(t, d, `SELECT CASE WHEN is_private THEN 1 ELSE 0 END FROM issues ORDER BY id DESC LIMIT 1`); n != 1 {
		t.Error("issue should be private")
	}
}

func TestMailHandlerShouldUpdateIssue(t *testing.T) {
	base, d := newMailHandlerServer(t, "1")
	issues := mailCount(t, d, `SELECT COUNT(*) FROM issues`)
	journals := mailCount(t, d, `SELECT COUNT(*) FROM issue_journals`)
	res, _ := post(t, newClient(t), base+"/mail_handler", url.Values{"key": {"secret"}, "email": {mailFixture(t, "ticket_reply.eml")}})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if mailCount(t, d, `SELECT COUNT(*) FROM issues`) != issues || mailCount(t, d, `SELECT COUNT(*) FROM issue_journals`) != journals+1 {
		t.Error("journal not created")
	}
}

func TestMailHandlerShouldRespondWith422IfNotCreated(t *testing.T) {
	base, d := newMailHandlerServer(t, "1")
	// Project.find('onlinestore').destroy の代わりに識別子を変えて見つからなくする
	if _, err := d.Exec(context.Background(), `UPDATE projects SET identifier = 'onlinestore-gone' WHERE identifier = 'onlinestore'`); err != nil {
		t.Fatal(err)
	}
	before := mailCount(t, d, `SELECT COUNT(*) FROM issues`)
	res, _ := post(t, newClient(t), base+"/mail_handler", url.Values{"key": {"secret"}, "email": {mailFixture(t, "ticket_on_given_project.eml")}})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if after := mailCount(t, d, `SELECT COUNT(*) FROM issues`); after != before {
		t.Errorf("issues %d -> %d", before, after)
	}
}

func TestMailHandlerShouldNotAllowWithAPIDisabled(t *testing.T) {
	base, d := newMailHandlerServer(t, "0")
	before := mailCount(t, d, `SELECT COUNT(*) FROM issues`)
	res, body := post(t, newClient(t), base+"/mail_handler", url.Values{"key": {"secret"}, "email": {mailFixture(t, "ticket_on_given_project.eml")}})
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Access denied") {
		t.Fatalf("status = %d body = %q", res.StatusCode, body)
	}
	if after := mailCount(t, d, `SELECT COUNT(*) FROM issues`); after != before {
		t.Errorf("issues %d -> %d", before, after)
	}
}

func TestMailHandlerShouldNotAllowWithWrongKey(t *testing.T) {
	base, d := newMailHandlerServer(t, "1")
	before := mailCount(t, d, `SELECT COUNT(*) FROM issues`)
	res, body := post(t, newClient(t), base+"/mail_handler", url.Values{"key": {"wrong"}, "email": {mailFixture(t, "ticket_on_given_project.eml")}})
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Access denied") {
		t.Fatalf("status = %d body = %q", res.StatusCode, body)
	}
	if after := mailCount(t, d, `SELECT COUNT(*) FROM issues`); after != before {
		t.Errorf("issues %d -> %d", before, after)
	}
}

func TestMailHandlerNew(t *testing.T) {
	base, _ := newMailHandlerServer(t, "1")
	res, body := getRaw(t, newClient(t), base+"/mail_handler?key=secret")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if !strings.Contains(body, "<h1>Buropher Mail Handler</h1>") || !strings.Contains(body, `<input type="hidden" name="key" id="key" value="secret" autocomplete="off" />`) {
		t.Errorf("body = %s", body)
	}
}
