# Configuration

buropher has two layers of configuration:

1. **Process configuration**: a TOML file passed with `-config <path>` plus `BUROPHER_*` environment
   variables. This covers what Redmine keeps in `config/database.yml`, `config/configuration.yml` and
   `config/additional_environment.rb` (database, mail delivery, storage paths, secrets, scheduler).
2. **Application settings**: stored in the database and edited in *Administration > Settings*, exactly
   as in Redmine (host name, protocol, authentication options, text formatting, notifications, ...).
   They can also be read and written from the command line with `buropher setting`.

A fully commented example with every key and its default is in [`config.example.toml`](../config.example.toml).

## Precedence

built-in defaults < config file < environment variables.

Every key is optional; without any configuration buropher listens on `:3000` and uses
`data/buropher.db` (SQLite) and `data/files` relative to the working directory.

## Keys

### Top level

| Key | Env | Default | Description |
|---|---|---|---|
| `dev_web_dir` | `BUROPHER_DEV_WEB_DIR` | `""` | Development only: read templates and assets from `<dir>/templates` and `<dir>/assets` on disk (reloaded per request) instead of the embedded copies. Top-level keys must appear before the first `[table]`. |

### `[server]`

| Key | Env | Default | Description |
|---|---|---|---|
| `addr` | `BUROPHER_ADDR` | `:3000` | Listen address (`host:port`). |
| `base_url` | `BUROPHER_BASE_URL` | `""` | Public URL without trailing slash (e.g. `https://tracker.example.com`). Used to build OIDC redirect URIs; when empty, the scheme and host of the request are used (honouring `X-Forwarded-*` from trusted proxies). A trailing `/` is removed. Give only scheme and host; the sub-path comes from `relative_url_root` (a `base_url` that already ends with the sub-path is accepted as well). |
| `relative_url_root` | `BUROPHER_RELATIVE_URL_ROOT` | `""` | Sub-path to serve buropher under, like Redmine's `RAILS_RELATIVE_URL_ROOT` (e.g. `/redmine`). Empty = served at the root of the host. See [Sub-path deployment](#sub-path-deployment). |
| `secret_key` | `BUROPHER_SECRET_KEY` | `""` | Key for session cookies, CSRF tokens and encryption of stored secrets (TOTP keys, LDAP bind passwords, repository passwords, OIDC and Discord client secrets). If empty, a random key is generated on first start and stored in `<data dir>/secret_key` (`<data dir>` is the directory of the SQLite database file, or `./data` otherwise). Changing it logs everyone out and makes stored secrets undecryptable. |
| `max_request_body_mb` | `BUROPHER_MAX_REQUEST_BODY_MB` | `0` | Upper limit (MiB) for multipart request bodies such as form uploads; larger requests get `413`. `0` = unlimited, like Redmine. Bodies are spooled to temporary files before authentication, so on public servers set this or a reverse-proxy limit (e.g. nginx `client_max_body_size`). Keep it above the *Maximum attachment size* setting. |
| `pprof` | `BUROPHER_PPROF` | `false` | Development / troubleshooting: serve Go's `net/http/pprof` (`/debug/pprof/`) and SQL statistics (`/debug/sqlstats`, see [performance.md](performance.md)) on a separate listener. |
| `pprof_addr` | `BUROPHER_PPROF_ADDR` | `127.0.0.1:6060` | Listen address of the profiling endpoint. Must be a loopback address (`127.0.0.1`, `::1`, `localhost`); the server refuses to start otherwise. |
| `auth_realm` | `BUROPHER_AUTH_REALM` | `Redmine` | Realm of the `WWW-Authenticate` response header: `Basic realm="<realm> API"` for the REST API, `Bearer realm="<realm>"` for OAuth. The default stays Redmine's value so existing API clients keep working. |

### `[database]`

| Key | Env | Default | Description |
|---|---|---|---|
| `driver` | `BUROPHER_DB_DRIVER` | `sqlite` | `sqlite` or `postgres`. |
| `dsn` | `BUROPHER_DB_DSN` | `data/buropher.db` | SQLite: path to the database file (parent directories are created; WAL mode, foreign keys on). PostgreSQL: a pgx DSN such as `postgres://user:pass@host:5432/buropher?sslmode=require`. |
| `max_open_conns` | `BUROPHER_DB_MAX_OPEN_CONNS` | `0` | Upper limit of open connections (`0` = unlimited). Up to `max(4, 2 × CPUs)` idle connections are kept for reuse. SQLite runs in WAL mode (readers in parallel, writers serialized by `BEGIN IMMEDIATE` and a 10 s busy timeout) with a 32 MiB page cache, 256 MiB mmap and in-memory temp store per connection; its planner statistics are refreshed in the background at startup and every 6 hours. |

### `[storage]`

| Key | Env | Default | Description |
|---|---|---|---|
| `attachments_path` | `BUROPHER_ATTACHMENTS_PATH` | `data/files` | Attachment directory (Redmine's `attachments_storage_path`). Same layout as Redmine (`YYYY/MM/<disk_filename>`). |

### `[mail]`, `[mail.smtp]`, `[mail.sendmail]`

Equivalent to `email_delivery` in Redmine's `configuration.yml`.

| Key | Env | Default | Description |
|---|---|---|---|
| `mail.delivery_method` | `BUROPHER_MAIL_DELIVERY` | `""` | `smtp`, `sendmail` or `none`. Empty means no e-mail is delivered (Redmine without `email_delivery`). |
| `mail.redmine_compat_headers` | `BUROPHER_MAIL_REDMINE_HEADERS` | `true` | Outgoing e-mails always carry `X-Buropher-*` headers. When `true`, the Redmine-compatible `X-Redmine-*` headers are sent too (each `X-Buropher-*` header right after its `X-Redmine-*` counterpart, same value), so existing mail filters keep working. `false` sends only `X-Buropher-*`. See [compatibility.md](compatibility.md). |
| `mail.message_id_prefix` | `BUROPHER_MAIL_MESSAGE_ID_PREFIX` | `redmine` | Prefix of the `Message-ID` / `References` tokens of notification e-mails (`redmine.issue-1.20260101000000.2@host`): `redmine` or `buropher`. The default keeps reply threading with e-mails sent by Redmine before the migration. Incoming replies are matched with either prefix. |
| `mail.smtp.address` | `BUROPHER_SMTP_ADDRESS` | `""` | SMTP server host. |
| `mail.smtp.port` | `BUROPHER_SMTP_PORT` | `0` | `0` = 25 (465 when `tls = true`). |
| `mail.smtp.domain` | `BUROPHER_SMTP_DOMAIN` | `""` | HELO/EHLO domain. |
| `mail.smtp.user_name` | `BUROPHER_SMTP_USER_NAME` | `""` | |
| `mail.smtp.password` | `BUROPHER_SMTP_PASSWORD` | `""` | |
| `mail.smtp.authentication` | `BUROPHER_SMTP_AUTH` | `""` | `plain`, `login` or `cram_md5`. |
| `mail.smtp.enable_starttls_auto` | | `true` | Use STARTTLS when offered. |
| `mail.smtp.tls` | | `false` | Implicit TLS (SMTPS). |
| `mail.smtp.openssl_verify_mode` | | `peer` | `peer` or `none` (skip certificate verification). |
| `mail.smtp.timeout` | | `30` | Seconds. |
| `mail.sendmail.location` | | `/usr/sbin/sendmail` | |
| `mail.sendmail.arguments` | | `["-i"]` | Recipients are appended. |

The sender address, footer, HTML/plain text format etc. are application settings
(*Administration > Settings > Email notifications*).

### `[jobs]`, `[jobs.reminders]`

Notifications (mail and Discord DMs) are queued in the database and delivered by background workers
started by `serve`.

| Key | Default | Description |
|---|---|---|
| `jobs.workers` | `0` (= 2) | Number of workers. Negative: start no workers; run `buropher jobs run` periodically (e.g. from cron) instead. |
| `jobs.poll_interval` | `2s` | Queue polling interval (Go duration). |
| `jobs.max_attempts` | `10` | Retries before a job is given up. |
| `jobs.cleanup_interval` | `1h` | How often expired sessions/tokens and old job records are deleted. |
| `jobs.jobs_retention_days` | `7` | Keep finished jobs this long. |
| `jobs.deliveries_retention_days` | `30` | Keep the notification delivery log this long. |
| `jobs.reminders.enabled` | `false` | Send due-date reminders daily (built-in replacement for a cron job running `rake redmine:send_reminders`). |
| `jobs.reminders.at` | `08:00` | Daily run time (`HH:MM`) in the server's local time zone (`TZ`). |
| `jobs.reminders.days` | `0` (= 7) | Remind about issues due within this many days. |
| `jobs.reminders.tracker` | `0` | Tracker id (0 = all). |
| `jobs.reminders.project` | `""` | Project id or identifier (empty = all). |
| `jobs.reminders.users` | `[]` | User/group ids to remind (empty = all assignees). |
| `jobs.reminders.version` | `""` | Target version name. |

The same can be run on demand: `buropher reminders -days 7 -tracker 1 -project foo -users 3,5 -version 1.0`.

### `[auth]`

| Key | Env | Default | Description |
|---|---|---|---|
| `auth.sudo_mode` | `BUROPHER_SUDO_MODE` (`1`/`true` enables, other values disable) | `true` | Require the password again before sensitive administrative actions (Redmine's `sudo_mode`; enabled by default since Redmine 7.0). |
| `auth.sudo_mode_timeout` | | `0` (= 15) | Minutes the sudo mode stays active. |

LDAP and OpenID Connect providers are configured in *Administration > Authentication modes*
(see [sso-entra-id.md](sso-entra-id.md)).

### `[discord]`

| Key | Env | Default | Description |
|---|---|---|---|
| `discord.enabled` | | `false` | Enable Discord DM notifications. The bot token, OAuth2 client and guild are configured in *Administration > Plugins* (see [discord.md](discord.md)). |
| `discord.api_base` | `BUROPHER_DISCORD_API_BASE` | `https://discord.com/api/v10` | REST API base URL (override for testing). |
| `discord.authorize_url` | | `https://discord.com/oauth2/authorize` | OAuth2 authorization URL. |

### `[pdf]`, `[pdf.fonts]`

| Key | Env | Default | Description |
|---|---|---|---|
| `pdf.font_dir` | `BUROPHER_PDF_FONT_DIR` | `""` | Directory with additional TrueType fonts. Only the embedded DejaVu fonts are used when empty. |
| `pdf.fonts.<locale>` | | | Font file per locale, e.g. `ja = "ipaexg.ttf"`; `"regular.ttf,bold.ttf"` sets a bold face too. |

See [pdf.md](pdf.md).

### `[scm]`

| Key | Env | Default | Description |
|---|---|---|---|
| `scm.git_command` | `BUROPHER_SCM_GIT_COMMAND` | `git` | Git executable (Redmine's `scm_git_command`). |
| `scm.fetch_interval` | `BUROPHER_SCM_FETCH_INTERVAL` | `""` | Fetch new changesets of all repositories at this interval (e.g. `15m`). Replaces the usual cron job running `Repository.fetch_changesets`. |

### Other environment variables

| Variable | Used by | Description |
|---|---|---|
| `BUROPHER_ADMIN_PASSWORD` | `buropher init` | Default for `-admin-password`. |
| `REDMINE_CIPHER_KEY` | `buropher redmine import` | Default for `--cipher-key`. |
| `TZ` | all | Server local time zone (reminder schedule, log timestamps). User-facing times use the user's or the default time zone setting. |

## Sub-path deployment

`server.relative_url_root` (env `BUROPHER_RELATIVE_URL_ROOT`) serves buropher under a sub-path,
e.g. `https://example.com/redmine/`, the same way Redmine does with `RAILS_RELATIVE_URL_ROOT`
(`config.relative_url_root` plus `map` in `config.ru`):

```toml
[server]
relative_url_root = "/redmine"
```

- buropher expects the **full** request path including the prefix (`/redmine/issues/1`). Configure
  the reverse proxy to pass the path through unchanged (do not strip the prefix). Requests outside the
  prefix get a plain `404 Not Found: <path>` like Rack::URLMap; the health check is
  `/redmine/healthz`.
- Every generated URL carries the prefix: links, form actions, redirects (`Location`), API
  `Location` headers and `content_url`, Atom feeds, asset URLs (`/redmine/assets/...`), and URLs
  embedded in JavaScript (autocomplete, context menu, preview, ...).
- The session cookie and the autologin cookie use the prefix as their `Path` (Redmine's
  `relative_url_root || '/'`).
- `back_url` values must start with the prefix (same validation as Redmine).
- Links in e-mails and the Discord redirect URI use *Administration > Settings > General* "Host name
  and path", exactly like Redmine's `Mailer.default_url_options`: include the sub-path there
  (e.g. `example.com/redmine`). The settings page shows the guessed value with the prefix.
- The prefix must not collide with a top-level Redmine route (`/projects`, `/issues`, `/admin`, ...).
- Links written by users in wiki text (e.g. `"text":/foo`) are left unchanged, as in Redmine.

See [install.md](install.md#sub-path-behind-a-reverse-proxy) for proxy examples.

## Application settings relevant to operators

These live in the database. Set them in the web UI or with `buropher setting set <name> <value>`
(e.g. `buropher setting set host_name tracker.example.com`; serialized settings take JSON, e.g.
`buropher setting set default_projects_modules '["issue_tracking","wiki"]'`). Restart `serve` after
changing settings from the command line while it is running.

| Setting | UI | Why it matters |
|---|---|---|
| `host_name`, `protocol` | Settings > General | Used for absolute links in e-mails, Atom feeds and the Discord OAuth2 redirect URI. Set them to the public host and `https`. |
| `mail_from`, `emails_footer`, `plain_text_mail` | Settings > Email notifications | Sender and format of notifications. |
| `rest_api_enabled`, `jsonp_enabled` | Settings > API | REST API (disabled by default, as in Redmine). |
| `sys_api_enabled`, `sys_api_key` | Settings > Repositories | Enables `/sys/fetch_changesets` and the repository management web service. |
| `autofetch_changesets` | Settings > Repositories | Fetch commits when a repository is browsed. |
| `attachment_max_size` | Settings > Files | Upload limit (KB). Also raise the limit in your reverse proxy. |
| `default_users_time_zone` | Settings > Users | Time zone of users that have not chosen one. |
| `login_required`, `self_registration`, `twofa` | Settings > Authentication | Access policy. |

`buropher setting get <name>` prints the current value as JSON.
