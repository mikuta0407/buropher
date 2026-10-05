// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
	"github.com/mikuta0407/buropher/internal/textformat/internal/fixtures"
)

// frozenNow は参照 Redmine（COMPAT_FROZEN_TIME）と同じ固定時刻。
var frozenNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// iconsPath は参照環境の asset_path("icons.svg")。
const iconsPath = "/assets/icons-0476c1d7.svg"

type corpusCase struct {
	ID         string         `json:"id"`
	Formatting *string        `json:"formatting"`
	User       string         `json:"user"`
	Project    string         `json:"project"`
	Object     *corpusObject  `json:"object"`
	Options    map[string]any `json:"options"`
	Text       string         `json:"text"`
	HTML       *string        `json:"html"`
}

type corpusObject struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

var (
	fixtureOnce sync.Once
	fixtureDB   *db.DB
	fixtureErr  error
	fixtureDir  string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if fixtureDB != nil {
		fixtureDB.Close()
	}
	if fixtureDir != "" {
		os.RemoveAll(fixtureDir)
	}
	os.Exit(code)
}

// fixtureDatabase は公式フィクスチャを固定時刻で投入した DB（パッケージ内で共有。読み取り専用で使う）。
func fixtureDatabase(t testing.TB) *db.DB {
	t.Helper()
	fixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "redmine-textformat-*")
		if err != nil {
			fixtureErr = err
			return
		}
		fixtureDir = dir
		d, err := db.Open(context.Background(), string(db.SQLite), dir+"/test.db")
		if err != nil {
			fixtureErr = err
			return
		}
		if _, err := db.Migrate(context.Background(), d, db.Up); err != nil {
			fixtureErr = err
			return
		}
		fixtureErr = testfixtures.LoadContext(context.Background(), d, frozenNow, testfixtures.All()...)
		fixtureDB = d
	})
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	return fixtureDB
}

// testEnv は 1 ケース分の Renderer を作る。
type testEnv struct {
	d     *db.DB
	users map[string]*domain.User
}

func newTestEnv(t testing.TB) *testEnv {
	return &testEnv{d: fixtureDatabase(t), users: map[string]*domain.User{}}
}

func (e *testEnv) user(t testing.TB, login string) *domain.User {
	if u, ok := e.users[login]; ok {
		return u
	}
	ctx := context.Background()
	var u *domain.User
	var err error
	if login == "" || login == "anonymous" {
		u, err = repository.AnonymousUser(ctx, e.d)
	} else {
		u, err = repository.FindUserByLogin(ctx, e.d, login)
	}
	if err != nil {
		t.Fatalf("user %q: %v", login, err)
	}
	e.users[login] = u
	return u
}

func (e *testEnv) renderer(t testing.TB, login, project, formatting string) (*Renderer, *DBStore) {
	ctx := context.Background()
	u := e.user(t, login)
	st := NewDBStore(ctx, e.d, authz.New(e.d, u))
	var p *domain.Project
	if project != "" {
		var err error
		p, err = repository.FindProject(ctx, e.d, project)
		if err != nil {
			t.Fatalf("project %q: %v", project, err)
		}
	}
	n := 0
	r := &Renderer{
		Store: st, User: u, Project: p, TextFormatting: formatting, IconsPath: iconsPath,
		UserFormat: "firstname_lastname", BaseURL: "http://test.host",
		Now: func() time.Time { return frozenNow },
		RandomHex: func(k int) string {
			n++
			return fmt.Sprintf("%0*x", k*2, n)
		},
	}
	return r, st
}

func (e *testEnv) options(t *testing.T, st *DBStore, c *corpusCase) Options {
	var o Options
	if c.Object != nil && c.Object.Type != "" {
		obj, err := st.LoadObject(c.Object.Type, c.Object.ID)
		if err != nil {
			t.Fatalf("%s: object: %v", c.ID, err)
		}
		o.Object = obj
	}
	for k, v := range c.Options {
		switch k {
		case "headings":
			o.NoHeadings = v == false
		case "inline_attachments":
			o.NoInlineAttachments = v == false
		case "only_path":
			o.FullURL = v == false
		case "formatting":
			o.NoFormatting = v == false
		case "wiki_links":
			o.WikiLinks, _ = v.(string)
		case "edit_section_links":
			m := v.(map[string]any)
			o.EditSectionLinks = &EditSectionLinks{ProjectID: fmt.Sprint(m["project_id"]), ID: fmt.Sprint(m["id"])}
		case "attachments":
			var ids []int64
			for _, x := range v.([]any) {
				ids = append(ids, int64(x.(float64)))
			}
			atts, err := repository.AttachmentsByIDs(context.Background(), e.d, ids)
			if err != nil {
				t.Fatal(err)
			}
			o.Attachments = atts
		default:
			t.Fatalf("%s: unknown option %q", c.ID, k)
		}
	}
	return o
}

// loadCorpus は testdata/corpus*.json（corpus.json: Redmine のテストの移植と境界ケース、
// corpus_random.json: 固定シードのランダムケース）を読み込む。
func loadCorpus(t *testing.T) []*corpusCase {
	files, err := filepath.Glob("testdata/corpus*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("corpus files: %v", err)
	}
	var all []*corpusCase
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var cs []*corpusCase
		if err := json.Unmarshal(b, &cs); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		all = append(all, cs...)
	}
	return all
}

// corpusDeviations は意図して Redmine と異なる出力にしているケース（ID → 理由）。
var corpusDeviations = map[string]string{
	// a 要素の href の中に置いた {{include}} を実行しない（TestMacroInsideAttributeIsNotExecuted）
	"random/0046-tx": "macro inside an attribute value",
}

// TestCorpus は参照 Redmine の textilizable 出力（testdata/corpus.json）と比較する。
func TestCorpus(t *testing.T) {
	e := newTestEnv(t)
	cases := loadCorpus(t)
	pass := 0
	only := os.Getenv("CORPUS_ONLY")
	var failed []string
	for _, c := range cases {
		if only != "" && !strings.Contains(c.ID, only) {
			continue
		}
		if c.HTML == nil {
			continue
		}
		if _, ok := corpusDeviations[c.ID]; ok {
			pass++
			continue
		}
		f := "textile"
		if c.Formatting != nil {
			f = *c.Formatting
		}
		r, st := e.renderer(t, c.User, c.Project, f)
		got := string(r.Textilizable(c.Text, e.options(t, st, c)))
		if os.Getenv("CORPUS_DUMP") != "" {
			t.Logf("%s\n%s", c.ID, got)
		}
		// 属性値の < > は buropher ではエスケープする（Redmine リンクの置換で属性の中に差し込まれた
		// タグは対象外のため、元の期待値との一致も認める）
		want := fixtures.EscapeAttrAngles(*c.HTML)
		if got == want || got == *c.HTML {
			pass++
			continue
		}
		failed = append(failed, c.ID)
		if os.Getenv("CORPUS_VERBOSE") != "" {
			t.Errorf("%s\ntext: %q\nwant: %s\ngot:  %s", c.ID, c.Text, want, got)
		}
	}
	t.Logf("corpus: %d/%d exact match", pass, len(cases))
	if len(failed) > 0 {
		t.Errorf("%d cases differ: %s", len(failed), strings.Join(failed, " "))
	}
}
