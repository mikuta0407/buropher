#!/bin/bash
# メール互換テストの期待値（../mails.json）を Redmine 7.0.1 で再生成する。
# 公式フィクスチャ投入済み DB（redmine.pristine.sqlite3）のコピーに対して、時刻を 2026-01-15 12:00 UTC に固定して実行する。
set -e
here="$(cd "$(dirname "$0")" && pwd)"
root="${REDMINE_FIXTURES_ROOT:-$(git -C "$(dirname "$0")" rev-parse --show-toplevel)/_reference/redmine7-fixtures}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/buropher-mail.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
cp "$root/db/redmine.pristine.sqlite3" "$tmp/db.sqlite3"
cd "$root"
COMPAT_FROZEN_TIME="${COMPAT_FROZEN_TIME:-2026-01-15 12:00:00 UTC}" SECRET_KEY_BASE=x RAILS_ENV=production TZ=UTC \
  DATABASE_URL="sqlite3:$tmp/db.sqlite3" bin/rails runner "$here/gen_mails.rb" "$here/../mails.json"
