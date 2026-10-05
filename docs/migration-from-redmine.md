# Migrating from Redmine

buropher does not read a Redmine database directly. Migration is a three-step process, all built
into the `buropher` binary (no Ruby needed):

```
Redmine DB + files/  --(buropher redmine export)-->  redmine-export.tar.zst
redmine-export.tar.zst  --(buropher redmine import)-->  buropher DB + attachments
buropher redmine verify  -->  report (row counts, hierarchy, files, passwords)
```

The intermediate archive keeps the production Redmine connection short, lets you rehearse the
import as often as you like, and preserves the raw source data for auditing. Its format is
described in [export-format.md](export-format.md); the conversion rules in [import.md](import.md)
(both in Japanese).

## 1. Prerequisites

- **Redmine 6.1.x or 7.0.x** (6.1.0 – 6.1.5 share one database schema, 7.0.0 – 7.0.2 another;
  all are accepted). Older versions: upgrade Redmine to 6.1.x or 7.0.x and run
  `bundle exec rake db:migrate RAILS_ENV=production` first. The exporter compares `schema_migrations`
  with the 322 core migrations of 6.1 and the 327 of 7.0 and refuses any other set.
- Source database: MySQL/MariaDB, PostgreSQL, SQLite or SQL Server, reachable from the machine that
  runs the export (it can be the Redmine host or any other machine).
- Read access to Redmine's `files/` directory (attachments), or a copy of it.
- The value of `database_cipher_key` from Redmine's `config/configuration.yml`, if set (needed to
  carry over TOTP two-factor secrets, LDAP bind passwords and repository passwords).
- The **time zone of the Redmine server process** (see below).
- A fresh buropher installation ([install.md](install.md)) with `server.secret_key` set explicitly
  in the config. Do **not** run `buropher init` — the import needs an empty database.
- Disk space for the archive (roughly the size of the attachments plus the compressed tables) and
  for temporary files during export and import.

### Source time zone

Redmine stores timestamps as local wall-clock time of the Ruby process
(`active_record.default_timezone = :local`), without an offset. The exporter keeps these values
unchanged and records the zone you pass with `--source-timezone`; the importer converts them to
UTC. Use the IANA name of the zone the Redmine server ran in (the `TZ` of the process, usually the
OS time zone: `timedatectl` / `cat /etc/timezone`), e.g. `Asia/Tokyo` or `Europe/Berlin`, **not**
the "Time zone" user preference. A wrong value shifts every timestamp. Ambiguous or non-existent
times around DST changes are resolved like Ruby's `Time.local` and listed in the import warnings.

## 2. Export

```sh
buropher redmine export \
  --dsn 'mysql://redmine:secret@db.example.com:3306/redmine' \
  --source-timezone Asia/Tokyo \
  --redmine-root /srv/redmine \
  -o redmine-export.tar.zst
```

| Option | Description |
|---|---|
| `--dsn` | Source database: `mysql://user:pass@host:3306/db`, `postgres://user:pass@host:5432/db`, `sqlite:///path/to/redmine.sqlite3`, `sqlserver://user:pass@host?database=db`. Required. |
| `--driver` | `mysql` / `postgres` / `sqlite` / `sqlserver`; normally detected from the DSN scheme. |
| `--source-timezone` | IANA time zone of the Redmine server. Required. |
| `--redmine-root` | Redmine installation directory. Records the Redmine version, reads `config/configuration.yml` (`attachments_storage_path`, whether `database_cipher_key` is set) and defaults `--files`. |
| `--redmine-env` | Section of `configuration.yml` to read (default `production`). |
| `--files` | Attachments directory (default: `<redmine-root>/files` or `attachments_storage_path`). Required unless `--redmine-root` or `--no-files` is given. |
| `--no-files` | Do not put attachment files into the archive (copy them separately and pass `--source-files-dir` to the import). Missing files are still detected when `--files` is given. |
| `-o`, `--output` | Archive path (default `redmine-export-YYYYMMDD-HHMMSS.tar.zst`). |
| `--overwrite` | Overwrite an existing output file. |
| `--temp-dir` | Directory for temporary files (default: next to the output). |
| `--force` | Continue although the acceptance check failed. For development only; the archive is marked. |
| `--quiet` | Only print errors. |

The export runs in a single read-only, snapshot-consistent transaction. It is still recommended to
put Redmine into read-only mode (or stop it) during the final export so that no changes are lost.
The output lists warnings (plugins, extra tables/columns, unparsable values, missing files); read
them before importing. The cipher key itself is never written into the archive.

## 3. Import

```sh
buropher migrate -config /etc/buropher/config.toml       # create the schema (not `init`)
buropher redmine import \
  --driver sqlite --dsn /var/lib/buropher/buropher.db \
  --files-dir /var/lib/buropher/files \
  --cipher-key "$REDMINE_DATABASE_CIPHER_KEY" \
  --secret-key "$BUROPHER_SECRET_KEY" \
  --report-json import-report.json \
  redmine-export.tar.zst
```

| Option | Description |
|---|---|
| archive (argument) or `--archive` | The export archive. |
| `--driver`, `--dsn` | Target database (defaults `BUROPHER_DB_DRIVER` / `BUROPHER_DB_DSN`, else `sqlite` / `data/buropher.db`). The import does **not** read the config file; pass the same values as in it. |
| `--migrate` | Apply pending buropher migrations first (instead of a separate `buropher migrate`). |
| `--files-dir` | buropher attachments directory (default `BUROPHER_ATTACHMENTS_PATH` or `data/files`). Empty string: do not copy files. |
| `--source-files-dir` | Redmine `files/` directory, when the archive was created with `--no-files`. |
| `--cipher-key` | Redmine's `database_cipher_key` (default `REDMINE_CIPHER_KEY`). Without it, encrypted values are dropped: affected users must re-enroll 2FA, LDAP and repository passwords must be re-entered. |
| `--secret-key` | buropher's `server.secret_key` (default `BUROPHER_SECRET_KEY`). Secrets are re-encrypted with it. **It must be the key `serve` will use**; if you rely on the auto-generated `secret_key` file, pass its content. |
| `--dry-run` | Convert and check everything, then roll back (no files copied). Use it for rehearsals. |
| `--report-json` | Write the report as JSON (`-` = stdout). A text report is always printed. |
| `--temp-dir` | Directory for unpacked tables (default: next to the archive). |
| `--quiet` | Only print errors. |

The import requires an empty, fully migrated database and runs in one transaction: on any error
nothing is written. IDs are preserved, so issue numbers, URLs, bookmarks and links in texts keep
working. The report lists, per table, imported rows plus rows that were **dropped** (orphans,
duplicates, unsupported types) or **repaired** (missing values filled in, renamed duplicates), with
sample IDs.

## 4. Verify

```sh
buropher redmine verify --driver sqlite --dsn /var/lib/buropher/buropher.db \
  --files-dir /var/lib/buropher/files --password admin:'admin-password' \
  redmine-export.tar.zst
```

| Option | Description |
|---|---|
| `--files-dir` | Check that every attachment file exists with the right size. |
| `--digests` | Also compare digests (slow). |
| `--password login:pw` | Check that the given user can log in with the given password (repeatable). |
| `--strict` | Treat rows dropped during import as failures. |
| `--report-json` | JSON report. |

Verify compares IDs and row counts with the archive, checks project/issue hierarchies against
Redmine's nested sets, and checks attachments. Then start `buropher serve` and compare a few
pages (projects, issues, wiki, time entries, a saved query) with the old Redmine.

## 5. Cutover checklist

Rehearsal (with a copy of production):

1. [ ] Redmine is on 6.1.x or 7.0.x; note plugins in use (their data will not be migrated).
2. [ ] Export, import with `--dry-run`, then a real import into a scratch database; read all warnings.
3. [ ] `verify` passes; spot-check pages, attachments, logins (local, LDAP, 2FA), API access.
4. [ ] Measure export and import duration to plan the maintenance window.

Production:

1. [ ] Announce the window. Users will have to log in again (sessions are not migrated).
2. [ ] Put Redmine into read-only mode or stop it; stop incoming mail fetching and cron jobs
       (reminders, `fetch_changesets`).
3. [ ] Export (with `--redmine-root` and `--source-timezone`).
4. [ ] `buropher migrate`, `buropher redmine import`, `buropher redmine verify`.
5. [ ] Start `buropher serve`; check *Administration > Settings > General* (host name, protocol)
       and *Email notifications*; configure mail delivery in the config file.
6. [ ] Switch DNS / reverse proxy to buropher. URLs are the same as Redmine's.
7. [ ] Re-point integrations: REST API clients (API keys are kept), repository hooks calling
       `/sys/fetch_changesets`, incoming e-mail, Atom feed readers (feed keys are kept).
8. [ ] Re-create cron jobs or enable the built-in schedulers (`jobs.reminders`, `scm.fetch_interval`).
9. [ ] Make sure Git repositories referenced by repository URLs exist at the same paths on the
       buropher host (or edit them in project settings).
10. [ ] Keep the old Redmine and the archive (read-only) until you are confident.

## 6. What is migrated and what is not

Migrated: users, groups, passwords (Redmine hashes are accepted and upgraded on first login),
API/feed keys, 2FA (with the cipher key), LDAP authentication modes, projects, members and roles,
trackers, statuses, workflows, custom fields and values, issues with relations, journals and
watchers, time entries, versions, categories, documents, news, forums, wiki with full history,
repositories and changesets, saved queries, attachments, user preferences, settings,
OAuth applications and valid tokens, reactions.

Not migrated:

- **Plugin data**: plugin tables, plugin columns on core tables and plugin settings (unknown settings
  are kept aside in a `legacy_settings` table, unused). Plugins are not supported by buropher.
- Sessions and autologin cookies older than the autologin setting; short-lived tokens
  (password recovery/registration older than one day).
- CSV import history (`imports`, `import_items`) and generated thumbnails (re-created on demand).
- Authentication sources other than LDAP (custom `AuthSource` classes).
- Non-Git repositories (Subversion, Mercurial, CVS, Bazaar, Filesystem): rows and changesets are
  imported, but buropher can only browse Git repositories.
- Anything outside the database and `files/`: custom themes in `public/themes`,
  `configuration.yml` settings (mail delivery etc. go into the buropher config), cron jobs, web
  server configuration.

## 7. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `N core migrations of Redmine 6.1 (or 7.0) are missing` | Redmine is older than 6.1 or `db:migrate` was not run. Upgrade Redmine and migrate, then export again. |
| `N core migrations unknown to Redmine 6.1 (or 7.0) were found` | Redmine is newer than 7.0.x (or a plugin added unprefixed migrations). Not supported. |
| `--dsn and --source-timezone are required` | Both options are mandatory; there is no default time zone on purpose. |
| Timestamps are off by a fixed number of hours | Wrong `--source-timezone`. Re-export with the right zone and re-import into a fresh database. |
| Import fails with "not empty" | The target database was initialized (`buropher init`) or a previous import succeeded. Drop/recreate it and run `buropher migrate` only. |
| Import fails: encrypted values but no `--secret-key` | Provide `--secret-key`; buropher never stores decrypted secrets in plain text. |
| Users lost 2FA / LDAP or repository passwords are empty | The archive contained encrypted values but `--cipher-key` was missing or wrong. Re-import with the correct key, or have users re-enroll. |
| After import, 2FA/LDAP/OIDC secrets cannot be decrypted | `serve` uses a different `secret_key` than the one passed to the import. Use the same key. |
| Missing attachment files in the report | The files were already missing in Redmine (listed as `missing_files` in the archive) or `--files`/`--source-files-dir` pointed to the wrong directory. Copy the files into `<files-dir>/<disk_directory>/<disk_filename>`. |
| Many rows "dropped" | Orphans and duplicates left by plugins or manual SQL in Redmine. Check the sample IDs in the report; `verify --strict` fails on them. |
| Import fails: "target database has pending migrations" | Run `buropher migrate` first, or pass `--migrate`. |
| "integrity checks failed; nothing was imported" | A foreign key, polymorphic reference or hierarchy check failed after conversion. The report names the check; please file an issue with it (the archive is not modified). |
