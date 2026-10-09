# Buropher

[日本語](README.md) | English

Buropher is a re-implementation of [Redmine](https://www.redmine.org/) 7.0.2 in Go: a
Redmine-compatible project management and issue tracking web application that runs as a single
binary (`buropher`).

- Looks and behaves like Redmine 7.0.2: same screens, standard features and REST API
- Its own database schema (SQLite / PostgreSQL); existing Redmine instances migrate via export → import
- Extras: improved LDAP authentication, OIDC SSO (Microsoft Entra ID and others), Discord DM notifications
- Redmine plugins are not supported

## Quick start

### Docker

```sh
docker volume create buropher-data
docker run --rm -v buropher-data:/data ghcr.io/mikuta0407/buropher:edge init -admin-password 'change-me'
docker run -d --name buropher -p 3000:3000 -v buropher-data:/data ghcr.io/mikuta0407/buropher:edge
```

Then open http://localhost:3000/ and log in as `admin`. Images are published for `linux/amd64` and
`linux/arm64`: `edge` follows the main branch, `X.Y.Z` / `X.Y` / `latest` are releases.
See [docs/install.md](docs/install.md) for docker compose, PostgreSQL, reverse proxies and backups.

### Build from source

Requires Go (see `go.mod` for the version).

```sh
make build        # bin/buropher
./bin/buropher init -admin-password 'change-me'
./bin/buropher serve
```

Tagged releases attach binaries for Linux, macOS and Windows to
[GitHub Releases](https://github.com/mikuta0407/buropher/releases).

## Documentation

- [Installation](docs/install.md) — binary, Docker, systemd, reverse proxy, HTTPS, backups
- [Configuration](docs/configuration.md) — config keys, environment variables, relevant settings
  (example: [config.example.toml](config.example.toml))
- [Migrating from Redmine](docs/migration-from-redmine.md) — export / import / verify, cutover checklist
- [Compatibility](docs/compatibility.md) — what matches Redmine 7.0.2, features added in Redmine 7.0 and known deviations
- [Development](docs/development.md) — architecture, package map, compat harness, upstream sync, releases
- Extensions: [OIDC SSO / Entra ID](docs/sso-entra-id.md), [LDAP](docs/ldap.md), [Discord DM notifications](docs/discord.md), [PDF](docs/pdf.md)
- [Performance](docs/performance.md)
- Internals (Japanese): [schema](docs/schema.md), [export format](docs/export-format.md), [import rules](docs/import.md)

## Syncing upstream assets

`web/assets` and `web/locales/redmine` are taken from Redmine 7.0.2 (the synced version is recorded in
`web/UPSTREAM_VERSION`). License texts of the bundled libraries, icons and fonts are copied from
Redmine's `doc/licenses` to `docs/licenses/`.

```sh
tools/sync-upstream.sh <redmine-src> <gems-dir>
```

## License

Copyright (C) 2026 mikuta0407 and Buropher contributors

GNU General Public License version 2 or later (GPL-2.0-or-later, see [LICENSE](LICENSE)).
Buropher is a derivative work of Redmine (Copyright (C) 2006- Jean-Philippe Lang). The parts taken
from Redmine (templates, CSS/JS/images, locales, ported logic, test fixtures) are listed in
[NOTICE](NOTICE). Bundled third-party components (JavaScript libraries, fonts, Go modules, ...),
their licenses and the reasoning for GPL compatibility are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Go source files carry SPDX license identifiers.

"Redmine" is the name of the original project. Buropher is an independent project and is not
affiliated with or endorsed by the Redmine project. For compatibility, machine-facing identifiers
such as the X-Redmine-* headers keep the Redmine name (see [docs/compatibility.md](docs/compatibility.md)).
