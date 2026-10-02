#!/usr/bin/env bash
# buropher 互換テスト用の参照 Redmine 6.1.2（公式 test/fixtures 投入済み）を管理するスクリプト。
#
# 使い方: tools/compat/redmine-ref.sh {setup|start|stop|restart|status|reset|logs} [--force]
#
#   setup   _reference/redmine-migrated（バンドル・プリコンパイル済みアセット・secret_token 込み）を
#           _reference/redmine-fixtures へコピーし、新規 sqlite DB を migrate、fixtures を固定時刻で投入する。
#           投入直後の DB を db/redmine.pristine.sqlite3 として保存する。--force で作り直し。
#   start   ポート 3998 で起動（未 setup なら setup を実行）。
#   stop    停止。
#   reset   停止 → pristine DB を書き戻し → 起動（シナリオ実行で DB が変化した場合に）。
#   status  起動状態を表示。
#   logs    サーバログを tail。
#
# 環境変数:
#   COMPAT_REF_DIR       配置先（既定: <repo>/_reference/redmine-fixtures。worktree からでも本体 repo の _reference を使う）
#   COMPAT_REF_SRC       コピー元（既定: <_reference>/redmine-migrated）
#   COMPAT_REF_PORT      ポート（既定: 3998）
#   COMPAT_FROZEN_TIME   fixtures の ERB 評価時刻かつサーバの固定時刻（既定: 2026-01-15 12:00:00 UTC）
#                        空文字にするとサーバ時刻は固定しない（fixtures 投入時は既定値を使用）。
#   COMPAT_REF_RELATIVE_URL_ROOT  サブパス配置で起動する（例: /redmine）。RAILS_RELATIVE_URL_ROOT を設定し、
#                        config.ru を map 付きに差し替える（Redmine wiki の推奨構成）。既定は空（ルート配置）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# _reference ディレクトリを探す（git worktree 内からでも本体の _reference を見つける）
find_reference() {
  local d="$SCRIPT_DIR"
  while [[ "$d" != "/" ]]; do
    if [[ -d "$d/_reference/redmine-migrated" ]]; then
      echo "$d/_reference"
      return 0
    fi
    d="$(dirname "$d")"
  done
  echo "error: _reference/redmine-migrated が見つかりません（COMPAT_REF_SRC / COMPAT_REF_DIR を指定してください）" >&2
  return 1
}

if [[ -z "${COMPAT_REF_SRC:-}" || -z "${COMPAT_REF_DIR:-}" ]]; then
  REF_ROOT="$(find_reference)"
fi
SRC="${COMPAT_REF_SRC:-$REF_ROOT/redmine-migrated}"
DIR="${COMPAT_REF_DIR:-$REF_ROOT/redmine-fixtures}"
PORT="${COMPAT_REF_PORT:-3998}"
FROZEN_DEFAULT="2026-01-15 12:00:00 UTC"
FROZEN="${COMPAT_FROZEN_TIME-$FROZEN_DEFAULT}"
BUNDLE="${BUNDLE:-bundle3.3}"
RELROOT="${COMPAT_REF_RELATIVE_URL_ROOT:-}"
RELROOT="${RELROOT%/}"
PIDFILE="$DIR/tmp/pids/compat-server.pid"
LOGFILE="$DIR/log/compat-server.out"

export RAILS_ENV=production
export TZ=UTC
export LANG=C.UTF-8
export RAILS_SERVE_STATIC_FILES=1
if [[ -n "$RELROOT" ]]; then
  export RAILS_RELATIVE_URL_ROOT="$RELROOT"
fi

# write_config_ru はサブパス配置なら config.ru を map 付きにする（ルート配置ならコピー元のまま）。
write_config_ru() {
  if [[ -n "$RELROOT" ]]; then
    cat > "$DIR/config.ru" <<'RUBY'
# compat: サブパス配置（COMPAT_REF_RELATIVE_URL_ROOT）
require_relative 'config/environment'
map ActionController::Base.config.relative_url_root || '/' do
  run Rails.application
end
RUBY
  else
    cp "$SRC/config.ru" "$DIR/config.ru"
  fi
}

log() { echo "[redmine-ref] $*" >&2; }

is_running() {
  [[ -f "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null
}

do_setup() {
  local force="${1:-}"
  if [[ -f "$DIR/db/redmine.pristine.sqlite3" && "$force" != "--force" ]]; then
    log "setup 済み: $DIR（作り直すには setup --force）"
    return 0
  fi
  if is_running; then
    do_stop
  fi
  log "コピー: $SRC -> $DIR"
  mkdir -p "$DIR"
  # 実行時データ（DB・ログ・キャッシュ・添付）はコピーしない
  rsync -a --delete \
    --exclude '/db/*.sqlite3*' --exclude '/db/schema.rb' \
    --exclude '/log/*' --exclude '/tmp/*' --exclude '/files/*' \
    "$SRC/" "$DIR/"
  mkdir -p "$DIR/tmp/pids" "$DIR/tmp/cache" "$DIR/log" "$DIR/files"

  cat > "$DIR/config/database.yml" <<'YAML'
production:
  adapter: sqlite3
  database: db/redmine.sqlite3
  timeout: 5000
YAML
  # 添付ファイルは fixtures 付属の test/fixtures/files を使う（テストの set_fixtures_attachments_directory 相当）
  cat > "$DIR/config/configuration.yml" <<YAML
production:
  attachments_storage_path: $DIR/test/fixtures/files
  email_delivery:
    delivery_method: :test
YAML
  cp "$SCRIPT_DIR/ruby/zz_compat_frozen_time.rb" "$DIR/config/initializers/zz_compat_frozen_time.rb"

  log "migrate"
  (cd "$DIR" && COMPAT_FROZEN_TIME= "$BUNDLE" exec rake db:migrate >"$DIR/log/setup.out" 2>&1) || {
    tail -30 "$DIR/log/setup.out" >&2; return 1; }
  log "fixtures 投入（基準時刻: ${FROZEN:-$FROZEN_DEFAULT}）"
  (cd "$DIR" && COMPAT_FROZEN_TIME="${FROZEN:-$FROZEN_DEFAULT}" "$BUNDLE" exec rails runner "$SCRIPT_DIR/ruby/load_fixtures.rb" 2>&1 | tee -a "$DIR/log/setup.out" >&2)
  # WAL をチェックポイントしてから pristine を保存
  (cd "$DIR" && "$BUNDLE" exec rails runner 'ActiveRecord::Base.connection.execute("PRAGMA wal_checkpoint(TRUNCATE)")' >/dev/null 2>&1 || true)
  cp "$DIR/db/redmine.sqlite3" "$DIR/db/redmine.pristine.sqlite3"
  log "setup 完了"
}

do_start() {
  if is_running; then
    log "既に起動中 (pid $(cat "$PIDFILE"), port $PORT)"
    return 0
  fi
  do_setup
  write_config_ru
  log "起動: http://127.0.0.1:$PORT$RELROOT (frozen time: ${FROZEN:-なし})"
  rm -f "$DIR/tmp/pids/server.pid"
  # bundle exec は exec で ruby に置き換わるので $! がそのままサーバの pid になる
  cd "$DIR"
  COMPAT_FROZEN_TIME="$FROZEN" nohup "$BUNDLE" exec rails server -e production \
    -b 127.0.0.1 -p "$PORT" -P "$DIR/tmp/pids/server.pid" </dev/null >"$LOGFILE" 2>&1 &
  echo $! >"$PIDFILE"
  cd - >/dev/null
  # 起動待ち
  local i
  for i in $(seq 1 120); do
    if curl -fsS -o /dev/null "http://127.0.0.1:$PORT$RELROOT/login" 2>/dev/null; then
      log "起動完了 (pid $(cat "$PIDFILE"))"
      return 0
    fi
    if ! is_running; then
      log "起動失敗:"; tail -40 "$LOGFILE" >&2; return 1
    fi
    sleep 1
  done
  log "起動タイムアウト"; tail -40 "$LOGFILE" >&2; return 1
}

do_stop() {
  if ! is_running; then
    log "停止済み"
    rm -f "$PIDFILE"
    return 0
  fi
  local pid; pid="$(cat "$PIDFILE")"
  log "停止 (pid $pid)"
  kill "$pid" 2>/dev/null || true
  local i
  for i in $(seq 1 30); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.5
  done
  kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
  rm -f "$PIDFILE" "$DIR/tmp/pids/server.pid"
}

do_reset() {
  do_stop
  if [[ ! -f "$DIR/db/redmine.pristine.sqlite3" ]]; then
    do_setup
  else
    log "pristine DB を復元"
    rm -f "$DIR/db/redmine.sqlite3-wal" "$DIR/db/redmine.sqlite3-shm"
    cp "$DIR/db/redmine.pristine.sqlite3" "$DIR/db/redmine.sqlite3"
    rm -rf "$DIR/tmp/cache/"*
    # 添付の保存先（test/fixtures/files）も書き戻す（削除系のシナリオがファイルを消すため）
    rsync -a --delete "$SRC/test/fixtures/files/" "$DIR/test/fixtures/files/"
  fi
  do_start
}

do_status() {
  if is_running; then
    echo "running pid=$(cat "$PIDFILE") url=http://127.0.0.1:$PORT dir=$DIR"
  else
    echo "stopped dir=$DIR"
    return 3
  fi
}

cmd="${1:-}"
shift || true
case "$cmd" in
  setup) do_setup "${1:-}" ;;
  start) do_start ;;
  stop) do_stop ;;
  restart) do_stop; do_start ;;
  reset) do_reset ;;
  status) do_status ;;
  logs) tail -f "$LOGFILE" ;;
  *) sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
