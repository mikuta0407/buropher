package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikuta0407/buropher/internal/db/internal/migrationgen"
)

// テンプレートとコミット済みの生成物が一致していることを確認する。
// 失敗したら `go generate ./internal/db` を実行すること。
func TestGeneratedMigrationsUpToDate(t *testing.T) {
	tmpls, err := filepath.Glob("migrations/src/*.sql.tmpl")
	if err != nil || len(tmpls) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	for _, f := range tmpls {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		for _, d := range migrationgen.Dialects {
			want, err := migrationgen.Render(name, string(src), d)
			if err != nil {
				t.Fatal(err)
			}
			got, err := migrationsFS.ReadFile("migrations/" + d + "/" + migrationgen.OutputName(name))
			if err != nil {
				t.Fatalf("%s/%s: %v (run go generate ./internal/db)", d, name, err)
			}
			if string(got) != want {
				t.Errorf("%s/%s is stale; run `go generate ./internal/db`", d, migrationgen.OutputName(name))
			}
		}
	}
}
