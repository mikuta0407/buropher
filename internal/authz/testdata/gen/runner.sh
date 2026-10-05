#!/bin/bash
# Redmine (公式テストフィクスチャ投入済み) の rails runner を指定 DB に対して実行する。
# usage: runner.sh <sqlite db path> <ruby file> [args...]
# REDMINE_FIXTURES_ROOT で Redmine ツリーを上書きできる。
set -e
export ORIG_PWD="$PWD"
db="$(realpath "$1")"; shift
script="$(realpath "$1")"; shift
cd "${REDMINE_FIXTURES_ROOT:-/home/mikuta0407/projects/buropher/_reference/redmine7-fixtures}"
SECRET_KEY_BASE=x RAILS_ENV=production TZ=UTC DATABASE_URL="sqlite3:$db" exec bundle3.3 exec rails runner "$script" "$@"
