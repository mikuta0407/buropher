#!/bin/bash
# Redmine (公式テストフィクスチャ投入済み) の rails runner を指定 DB に対して、
# 時刻を 2026-01-15 12:00 UTC に固定して実行する。
# usage: runner.sh <sqlite db path> <ruby file> [args...]
# REDMINE_FIXTURES_ROOT で Redmine ツリーを上書きできる。
set -e
export ORIG_PWD="$PWD"
db="$(realpath "$1")"; shift
script="$(realpath "$1")"; shift
cd "${REDMINE_FIXTURES_ROOT:-/home/mikuta0407/projects/buropher/_reference/redmine7-fixtures}"
COMPAT_FROZEN_TIME="${COMPAT_FROZEN_TIME:-2026-01-15 12:00:00 UTC}" SECRET_KEY_BASE=x RAILS_ENV=production TZ=UTC DATABASE_URL="sqlite3:$db" exec bin/rails runner "$script" "$@"
