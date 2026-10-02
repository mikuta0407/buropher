package helper

import (
	"context"
	"os"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// TestProjectMenuMatchesRedmine はプロジェクトメニュー（権限・モジュール・表示条件）が参照 Redmine の
// projects#show の #main-menu と一致することを確認する
// （testdata/project_menu_*.html は `compat fetch -raw -user <user> /projects/<id>` の #main-menu の中身）。
func TestProjectMenuMatchesRedmine(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	testfixtures.Load(t, d, testfixtures.All()...)
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	anon, err := repository.AnonymousUser(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	users := map[string]*domain.User{"anonymous": anon}
	for _, login := range []string{"admin", "jsmith"} {
		u, err := repository.FindUserByLogin(ctx, d, login)
		if err != nil {
			t.Fatal(err)
		}
		users[login] = u
	}
	for _, c := range []struct{ project, user string }{
		{"ecookbook", "anonymous"}, {"ecookbook", "jsmith"}, {"ecookbook", "admin"}, {"onlinestore", "jsmith"},
	} {
		t.Run(c.project+"_"+c.user, func(t *testing.T) {
			p, err := repository.FindProjectByIdentifier(ctx, d, c.project)
			if err != nil {
				t.Fatal(err)
			}
			u := users[c.user]
			az := authz.New(d, u)
			page := &Page{
				Settings: st, Loc: i18n.Default().NewLocalizer("en", i18n.Settings{}, nil),
				User: u, Project: p, Controller: "projects", Action: "show", MainMenu: true,
				DB: d, Authz: func() *authz.Authorizer { return az },
			}
			got := string((&Deps{}).renderMainMenu(page, p))
			want, err := os.ReadFile("testdata/project_menu_" + c.project + "_" + c.user + ".html")
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("menu mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
