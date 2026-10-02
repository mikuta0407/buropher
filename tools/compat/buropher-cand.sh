#!/usr/bin/env bash
# 互換テストの候補側: 参照 Redmine と同じフィクスチャ DB を export → import した buropher を起動する。
#   tools/compat/buropher-cand.sh start|stop|restart|reset|status  (既定ポート 3100)
# 参照側（tools/compat/redmine-ref.sh）と同じ固定時刻・TZ で動かす。
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
# _reference は main チェックアウトにあるので worktree からでも辿れるよう上位を探す
REF=""; d=$ROOT; while [ "$d" != "/" ]; do [ -d "$d/_reference" ] && { REF=$d/_reference; break; }; d=$(dirname "$d"); done
[ -n "$REF" ] || { echo "_reference not found" >&2; exit 1; }
PORT=${BUROPHER_CAND_PORT:-3100}
WORK=${BUROPHER_CAND_DIR:-$ROOT/data/compat-cand}
FROZEN=${COMPAT_FROZEN_TIME:-2026-01-15T12:00:00Z}
PIDFILE=$WORK/serve.pid
BIN=$WORK/buropher

build() { mkdir -p "$WORK"; (cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/buropher); }

prepare() {
  build
  rm -rf "$WORK/db" "$WORK/files"; mkdir -p "$WORK/db"
  cp "$REF/redmine-fixtures/db/redmine.pristine.sqlite3" "$WORK/db/src.sqlite3"
  "$BIN" redmine export --dsn "sqlite://$WORK/db/src.sqlite3" --source-timezone UTC \
    --files "$REF/redmine-fixtures/test/fixtures/files" -o "$WORK/db/dump.tar.zst" --overwrite --quiet
  env_run "$BIN" migrate >/dev/null
  env_run "$BIN" redmine import --quiet "$WORK/db/dump.tar.zst" >/dev/null
  cp "$WORK/db/buropher.db" "$WORK/db/pristine.db"
}

env_run() {
  BUROPHER_DB_DSN=$WORK/db/buropher.db BUROPHER_ATTACHMENTS_PATH=$WORK/files BUROPHER_SECRET_KEY=compat-secret \
  BUROPHER_ADDR=127.0.0.1:$PORT BUROPHER_FAKE_NOW=$FROZEN TZ=UTC "$@"
}

start() {
  [ -f "$WORK/db/pristine.db" ] || prepare
  build
  env_run nohup "$BIN" serve > "$WORK/serve.log" 2>&1 &
  echo $! > "$PIDFILE"
  for _ in $(seq 50); do curl -sf "http://127.0.0.1:$PORT/healthz" >/dev/null && { echo "buropher candidate on http://127.0.0.1:$PORT"; return; }; sleep 0.2; done
  echo "failed to start; see $WORK/serve.log" >&2; exit 1
}

# PIDFILE はシェル関数（env_run）を起動したサブシェルの pid のことがあるため、同じバイナリの serve も止める
stop() {
  [ -f "$PIDFILE" ] && kill "$(cat "$PIDFILE")" 2>/dev/null || true; rm -f "$PIDFILE"
  pkill -f "^$BIN serve\$" 2>/dev/null || true
  for _ in $(seq 25); do pgrep -f "^$BIN serve\$" >/dev/null || break; sleep 0.2; done
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  reset) stop; [ -f "$WORK/db/pristine.db" ] || prepare; cp "$WORK/db/pristine.db" "$WORK/db/buropher.db"; rm -f "$WORK/db/buropher.db-wal" "$WORK/db/buropher.db-shm"; start ;;
  prepare) stop; prepare ;;
  status) [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null && echo running || echo stopped ;;
  *) echo "usage: $0 start|stop|restart|reset|prepare|status" >&2; exit 2 ;;
esac
