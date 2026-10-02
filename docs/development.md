# Development

## Requirements

- Go 1.26 (see `go.mod`). Pure Go, `CGO_ENABLED=0`.
- Optional: PostgreSQL for the PostgreSQL test targets (`BUROPHER_TEST_PG_DSN`).
- Optional, for the compatibility harness and generators only: a Redmine 6.1.2 checkout with Ruby
  and its bundle under `_reference/` (git-ignored).

```sh
make build     # bin/buropher (version/commit/date via -ldflags)
make vet       # go vet ./...
make test      # go test ./...
make lint      # golangci-lint run
go run ./cmd/buropher serve -config config.toml
```

Set `dev_web_dir = "web"` (or `BUROPHER_DEV_WEB_DIR=web`) to load templates and assets from disk
with reload on each request instead of the embedded copies.

Some packages (`internal/server`, `internal/query`, `internal/issues`) run several minutes; use
`go test ./internal/<pkg>/...` while iterating and `-run` to narrow down. Tests that need
PostgreSQL run against SQLite only unless `BUROPHER_TEST_PG_DSN` is set, e.g.
`postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`.

## Conventions

- Code comments are written in Japanese; user-facing docs in `docs/` are English (older design
  notes such as `schema.md`, `import.md`, `export-format.md`, `pdf.md` are Japanese).
- Ports of Redmine code reference the original file/method in the comment (e.g.
  `Issue#visible?`, `app/helpers/application_helper.rb`), so behaviour can be compared with upstream.
- Behaviour should match Redmine 6.1.2 byte-for-byte where the compat harness can observe it. Any
  intentional difference goes into `testdata/compat/allowlist.yml` with a reason.

## Architecture

```
cmd/buropher            CLI: serve, migrate, init, redmine export/import/verify, setting, reminders, jobs, version
  └ internal/server     builds the app: config, DB, settings, assets, templates, sessions, router, workers
      ├ internal/httpx      Rack/Rails-compatible request layer: params, sessions, CSRF, flash, remote IP, errors
      ├ internal/handler    controllers (ports of app/controllers), one file per controller/area
      │   ├ internal/view + view/rails   Go templates ported from ERB; ActionView helpers re-implemented byte-compatibly
      │   └ internal/helper              view helpers (ApplicationHelper etc.)
      ├ domain services    issues, timelog, query, authz, customfield, activity, search, notify, scmsync, ...
      ├ internal/repository  table-level DB access
      └ internal/db          connection, SQLite/PostgreSQL dialects, transactions, embedded goose migrations
web/                    embedded assets (from Redmine), locales, templates, static error pages
```

Request flow: Recover → RequestID → RemoteIP → default headers → params parsing / `_method`
override → (`/healthz`, `/assets` stop here) → session → handler (authentication, authorization,
render via `internal/view`).

Background work (notification e-mails, Discord DMs, cleanup, scheduled reminders, SCM fetches)
goes through the persistent job queue in `internal/jobs` (table `jobs`), processed by workers in
`serve` or by `buropher jobs run`.

The database schema is buropher's own ([schema.md](schema.md)), not Redmine's: e.g. nested sets are
replaced by `project_closure` and `issues.hier_path`, polymorphic columns use `*_kind`, settings are
JSON. Migrations are generated for both dialects from templates in
`internal/db/migrations/src/*.sql.tmpl` (`go generate ./internal/db`).

## Package map

| Package | Purpose |
|---|---|
| `internal/config` | TOML + environment configuration |
| `internal/server` | Server assembly, routing, workers, schedulers |
| `internal/httpx` | HTTP foundation compatible with Rails/Rack (params, session, CSRF, flash, proxies) |
| `internal/handler` | Controllers |
| `internal/view`, `internal/view/rails` | Template engine and ActionView helper ports |
| `internal/helper`, `internal/menu`, `internal/pagination` | View helpers, menus, paginator |
| `internal/assets` | Propshaft-compatible asset pipeline (digested URLs) |
| `internal/i18n` | Rails I18n / Redmine::I18n compatible localization, time zones |
| `internal/domain` | Model types |
| `internal/repository`, `internal/db` | Data access, dialects, migrations |
| `internal/settings` | Setting model (`settings.yml` embedded) |
| `internal/permission`, `internal/authz` | AccessControl definitions and permission checks |
| `internal/auth/*` | Passwords (Argon2id, Redmine SHA-1 compatible), TOTP, LDAP, OIDC |
| `internal/crypto/secretbox` | Encryption of stored secrets with `server.secret_key` |
| `internal/issues`, `internal/timelog`, `internal/customfield`, `internal/query` | Issue/time entry logic, custom fields, queries and filters |
| `internal/activity`, `internal/search`, `internal/calendar` | Activity stream, search, calendar |
| `internal/textformat/*` | Textile, CommonMark, sanitizer, Nokogiri-compatible HTML DOM, Rouge-compatible highlighting, textilizable/macros |
| `internal/textdiff`, `internal/wikidiff`, `internal/unifieddiff` | Diffs (wiki, journals, repository) |
| `internal/attachments`, `internal/mimetype` | Attachment storage and MIME types |
| `internal/scm`, `internal/scmsync` | Git adapter, changeset fetching and issue references |
| `internal/notify`, `internal/mail`, `internal/discord`, `internal/jobs` | Notifications, mail delivery, Discord client, job queue |
| `internal/pdf`, `internal/csvexport`, `internal/csvimport` | Exports and CSV import |
| `internal/bootstrap`, `internal/defaultdata` | `buropher init` and default data |
| `internal/redmineimport/*` | Redmine export (`export`), archive format (`archive`), import (`importer`), verification (`verify`), Ruby YAML decoding (`rubyyaml`), Redmine ciphering (`rediscipher`) |
| `internal/testfixtures`, `*/…test` | Redmine test fixtures loader and test servers (SMTP, LDAP, OIDC, Discord) |
| `tools/compat` | Compatibility harness |
| `tools/gen`, `tools/golden`, `tools/textformat`, `tools/*.rb` | Generators run against the Redmine reference (schema, permissions, routes, fixtures for formatters) |

## Compatibility harness

`tools/compat` compares buropher with a real Redmine 6.1.2 loaded with Redmine's test fixtures,
with time frozen so that output is deterministic. Full documentation (Japanese):
[`tools/compat/README.md`](../tools/compat/README.md).

```sh
tools/compat/redmine-ref.sh start            # reference Redmine on :3998 (needs _reference/ and Ruby)
go run ./tools/compat diff -ref http://127.0.0.1:3998 -cand http://127.0.0.1:3000 \
  -reset-ref 'tools/compat/redmine-ref.sh reset'
go run ./tools/compat snapshot -ref http://127.0.0.1:3998 -reset-ref 'tools/compat/redmine-ref.sh reset'
go run ./tools/compat check -cand http://127.0.0.1:3000   # against golden files, no Ruby needed
```

- Scenarios: `testdata/compat/scenarios/*.yml` (requests, users, forms, normalization rules).
- Golden outputs: `testdata/compat/golden/<scenario>/` (normalized reference responses).
- Accepted differences: `testdata/compat/allowlist.yml`; every entry needs a `reason`.
- Reports: `compat-report/` (Markdown/HTML summary and per-case diffs).

Typical workflow for a feature: add or extend a scenario, `snapshot` the reference, implement until
`diff`/`check` is clean, and allowlist only intentional differences.

## Upstream sync

`web/assets` (images, fonts, JavaScript, stylesheets, themes), `web/locales/redmine`,
`web/locales/rails` and `web/public` are copied from Redmine 6.1.2 and the gems it bundles:

```sh
tools/sync-upstream.sh _reference/redmine _reference/vendor_bundle/ruby/3.3.0/gems
git diff --stat web/    # review, then run the tests and the compat harness
```

The synced version is recorded in `web/UPSTREAM_VERSION`. Generated Go tables derived from Redmine
(e.g. `internal/redmineimport/export/schema_gen.go` from `tools/gen/dump-schema.rb`,
`internal/i18n/zones_gen.go`) are regenerated with the scripts referenced in their headers.

## Releases

Tags `v*` trigger `.github/workflows/release.yml`, which runs the tests and
[GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`): static binaries for linux/darwin
(amd64, arm64) and windows/amd64, archives with `LICENSE`, `README.md`, `config.example.toml`,
`docs/` and the systemd unit, and `checksums.txt`. The GitHub release is created as a draft.
Local dry run: `goreleaser release --snapshot --clean`.
