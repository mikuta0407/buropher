package validation

import (
	"reflect"
	"testing"

	"github.com/mikuta0407/buropher/internal/i18n"
)

func TestFullMessages(t *testing.T) {
	l := i18n.Default().NewLocalizer("en", i18n.Settings{}, nil)
	e := New("user")
	e.Add("email_address.address", "blank")
	e.Add("login", "taken")
	e.Add("password", "too_short", "count", 8)
	e.Add("password", "confirmation")
	e.AddMessage("base", "Literal message")
	want := []string{"Email cannot be blank", "Login has already been taken", "Password is too short (minimum is 8 characters)",
		"Password doesn't match confirmation", "Literal message"}
	if got := e.FullMessages(l); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	if !e.Include("login") || e.Include("firstname") || !e.HasKey("password", "confirmation") {
		t.Error("Include / HasKey")
	}
	g := New("group")
	g.AttrNames = map[string]string{"lastname": "field_name"}
	g.Add("lastname", "blank")
	if got := g.FullMessages(l); !reflect.DeepEqual(got, []string{"Name cannot be blank"}) {
		t.Errorf("group: %q", got)
	}
	if got := HumanAttributeName(l, "user", "auth_source_id"); got != "Authentication mode" {
		t.Errorf("auth_source_id: %q", got)
	}
}
