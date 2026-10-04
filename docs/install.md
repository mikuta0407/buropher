# Installation

buropher is a single static binary (no CGO, no Ruby). Templates, assets, locales, migrations and
the default PDF fonts are embedded. At runtime it needs:

- a database: SQLite (default, built in) or PostgreSQL (CI tests against PostgreSQL 17),
- a writable directory for attachments,
- `git` on `PATH` if you use repositories,
- optionally an SMTP server or `sendmail` for notifications.

To migrate an existing Redmine, install buropher first and then follow
[migration-from-redmine.md](migration-from-redmine.md) instead of running `buropher init`.

## 1. Binary

Download an archive for your platform from the GitHub releases page (linux/darwin amd64/arm64,
windows amd64) and verify it against `checksums.txt`:

```sh
sha256sum -c --ignore-missing checksums.txt
tar xzf buropher_<version>_linux_amd64.tar.gz
./buropher version
```

Or build from source (Go 1.26+):

```sh
make build            # -> bin/buropher, version information from git describe
```

First start:

```sh
cp config.example.toml config.toml        # optional; edit as needed
./buropher init -config config.toml -lang en -admin-login admin -admin-password 'change-me'
./buropher serve -config config.toml      # http://localhost:3000
```

`init` applies the migrations, loads the default data (roles, trackers, statuses, priorities in the
language given by `-lang`; skip with `-no-default-data`) and creates the administrator. Without
`-admin-password` (or `BUROPHER_ADMIN_PASSWORD`) a random password is generated and printed.

### Command reference

| Command | Purpose |
|---|---|
| `buropher serve [-config f]` | Run the web server, background job workers and schedulers. Refuses to start when migrations are pending or the database is not initialized. |
| `buropher migrate [-config f] [up\|down\|status]` | Apply (default), roll back one step, or list migrations. |
| `buropher init [-config f] [options]` | Initialize an empty database (see above). |
| `buropher setting [-config f] get <name>` / `set <name> <value>` | Read/write an application setting. |
| `buropher reminders [-config f] [-days N -tracker ID -project ID -users IDS -version NAME]` | Send due-date reminders now. |
| `buropher jobs [-config f] run` | Process pending background jobs once (for setups with `jobs.workers < 0`). |
| `buropher redmine export\|import\|verify ...` | Migration from Redmine. |
| `buropher version` | Version, commit, build date and Go version. |

### Upgrading

Stop the service, back up (see below), replace the binary, run `buropher migrate -config ...`,
start the service again. `serve` will not start while migrations are pending.

## 2. Docker

The repository contains a [`Dockerfile`](../Dockerfile) (Alpine based, includes `git`, runs as
UID 10001, data in `/data`, health check on `/healthz`) and an example
[`docker-compose.yml`](../docker-compose.yml).

```sh
docker compose build
docker compose run --rm buropher init -admin-password 'change-me'
docker compose up -d
```

Defaults inside the image: `BUROPHER_DB_DSN=/data/buropher.db`,
`BUROPHER_ATTACHMENTS_PATH=/data/files`, listening on `:3000`. Configure through `BUROPHER_*`
variables or mount a config file and run `serve -config /etc/buropher/config.toml`.
Set `BUROPHER_SECRET_KEY` (e.g. `openssl rand -hex 64`), otherwise a key is generated into
`/data/secret_key` — keep that file with your backups.

PostgreSQL: `docker compose --profile postgres up -d` starts the bundled `postgres` service;
uncomment the `BUROPHER_DB_*` lines and `depends_on` in `docker-compose.yml`.

Local Git repositories must be mounted into the container (read-only is enough) and referenced by
their path inside the container.

## 3. systemd

```sh
useradd --system --home-dir /var/lib/buropher --shell /usr/sbin/nologin buropher
install -m 0755 buropher /usr/local/bin/buropher
install -d -m 0750 /etc/buropher
install -m 0640 -g buropher config.example.toml /etc/buropher/config.toml
install -m 0644 packaging/systemd/buropher.service /etc/systemd/system/
systemctl daemon-reload
```

In `/etc/buropher/config.toml` use absolute paths, e.g.:

```toml
[server]
base_url = "https://tracker.example.com"
secret_key = "<openssl rand -hex 64>"

[database]
dsn = "/var/lib/buropher/buropher.db"

[storage]
attachments_path = "/var/lib/buropher/files"
```

Then:

```sh
sudo -u buropher /usr/local/bin/buropher init -config /etc/buropher/config.toml -admin-password 'change-me'
systemctl enable --now buropher
journalctl -u buropher -f
```

The unit uses `StateDirectory=buropher` (`/var/lib/buropher`) and `ProtectSystem=strict`; add
`ReadOnlyPaths=` for local Git repositories outside that directory. Set `TZ` (e.g.
`Environment=TZ=Asia/Tokyo`) if the scheduled reminder time should follow a local time zone.

## 4. Reverse proxy and HTTPS

Run buropher behind a reverse proxy that terminates TLS. buropher trusts `X-Forwarded-For`,
`X-Forwarded-Proto`, `X-Forwarded-Host` and `X-Forwarded-Port` only from loopback and private
address ranges (the same list as Rails' `TRUSTED_PROXIES`), so a proxy on the same host or private
network works without extra configuration.

After switching to HTTPS:

- set `server.base_url` to the public URL (needed for OIDC redirect URIs),
- set *Administration > Settings > General* "Host name and path" and "Protocol" (`https`), which are
  used for links in e-mails and the Discord redirect URI,
- raise the proxy's request body limit to at least the attachment size limit.

Hardening notes:

- Bind buropher to loopback when the proxy runs on the same host (`server.addr = "127.0.0.1:3000"`).
  Because forwarded headers are accepted from any private address, a client on the same private
  network that can reach buropher directly can otherwise choose its own logged IP address and the
  host name used in generated links (as with Rails). The same applies to a container whose port
  is published directly: Docker's NAT makes every client appear to come from the (private) bridge
  gateway, so put a reverse proxy in front instead of exposing port 3000 to the Internet.
- buropher sends the same security headers as Redmine (`X-Frame-Options: SAMEORIGIN`,
  `X-Content-Type-Options: nosniff`, `Referrer-Policy`, ...) but no `Strict-Transport-Security`;
  add HSTS in the TLS-terminating proxy (e.g. nginx
  `add_header Strict-Transport-Security "max-age=31536000" always;`).
- Session and autologin cookies get the `Secure` flag when the request arrived over HTTPS
  (directly or via `X-Forwarded-Proto: https` from a trusted proxy), so make sure the proxy sets it.
- `/healthz` is unauthenticated and returns only `ok` / `db: unavailable`; restrict it at the proxy
  if you do not want it public.
- For a PostgreSQL server on another host use `sslmode=verify-full` (pgx defaults to `prefer`, which
  does not verify the certificate), and pass the DSN through `BUROPHER_DB_DSN` in a root-owned
  `EnvironmentFile` (mode `0600`) or the `0640` config file rather than on the command line.
- New SQLite databases are created with mode `0600` and data directories with `0750`; the secret key
  file (`<data dir>/secret_key`) is `0600`. Attachments follow the process umask (`UMask=0027` in the
  systemd unit keeps them private to the service group).

To serve buropher under a sub-path instead of the root of a host name, see
[Sub-path behind a reverse proxy](#sub-path-behind-a-reverse-proxy).

### nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name tracker.example.com;
    ssl_certificate     /etc/letsencrypt/live/tracker.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/tracker.example.com/privkey.pem;

    client_max_body_size 50m;   # >= attachment_max_size

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 300s;   # large exports (CSV/PDF/Atom)
    }
}
server {
    listen 80;
    server_name tracker.example.com;
    return 301 https://$host$request_uri;
}
```

### Caddy

```caddy
tracker.example.com {
    request_body {
        max_size 50MB
    }
    reverse_proxy 127.0.0.1:3000
}
```

Caddy obtains certificates automatically and sets the `X-Forwarded-*` headers.

### Sub-path behind a reverse proxy

To serve buropher at `https://example.com/redmine/` (Redmine's `RAILS_RELATIVE_URL_ROOT`), set the
sub-path in the config (or `BUROPHER_RELATIVE_URL_ROOT=/redmine`):

```toml
[server]
relative_url_root = "/redmine"
base_url = "https://example.com"   # scheme and host only (for OIDC redirect URIs)
```

and set *Administration > Settings > General* "Host name and path" to `example.com/redmine` so
that e-mail links include the sub-path. buropher expects the proxy to forward the **full path,
including `/redmine`** (do not strip the prefix):

nginx (note: no trailing slash or URI part on `proxy_pass`, so the path is passed unchanged):

```nginx
location /redmine/ {
    proxy_pass http://127.0.0.1:3000;
    proxy_set_header Host              $host;
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 300s;
}
location = /redmine { return 301 /redmine/; }
```

Caddy (`handle`, not `handle_path`, which would strip the prefix):

```caddy
example.com {
    handle /redmine* {
        reverse_proxy 127.0.0.1:3000
    }
}
```

Apache httpd:

```apache
ProxyPass        /redmine http://127.0.0.1:3000/redmine
ProxyPassReverse /redmine http://127.0.0.1:3000/redmine
RequestHeader set X-Forwarded-Proto "https"
```

If your proxy can only forward with the prefix stripped, re-add it in the proxy (e.g. nginx
`proxy_pass http://127.0.0.1:3000/redmine/;` inside `location /redmine/`). The health check is
`http://127.0.0.1:3000/redmine/healthz`.

## 5. Backups

Back up three things together: the **database**, the **attachments directory** and the
**secret key** (`server.secret_key` or `<data dir>/secret_key`; without it, TOTP secrets and stored
LDAP/repository/OIDC/Discord secrets cannot be decrypted). The config file is also worth keeping.

### SQLite

The database runs in WAL mode; do not copy the `.db` file alone while the server runs. Use an online
backup instead:

```sh
sqlite3 /var/lib/buropher/buropher.db ".backup '/backup/buropher-$(date +%F).db'"
# or: sqlite3 /var/lib/buropher/buropher.db "VACUUM INTO '/backup/buropher-$(date +%F).db'"
```

Alternatively stop the service and copy `buropher.db` together with any `-wal`/`-shm` files.

### PostgreSQL

```sh
pg_dump -Fc -d buropher -f /backup/buropher-$(date +%F).dump
pg_restore -d buropher --clean --if-exists /backup/buropher-YYYY-MM-DD.dump
```

### Attachments

Files are written once and never modified, so incremental copies work well:

```sh
rsync -a /var/lib/buropher/files/ /backup/files/
```

Take the database backup first, then the files, so that every attachment row has its file.

### Restore

Stop the service, restore the database and the attachments directory, put back the same secret key,
run `buropher migrate` if the binary is newer than the backup, and start the service.
