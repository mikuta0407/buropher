#!/usr/bin/env bash
# E2E 用のサーバ（参照 Redmine と buropher 候補）をまとめて起動・停止する。
#   e2e/servers.sh start|stop|reset|status|destroy|install-browsers
# 参照は専用ディレクトリ（既定 _reference/ref-e2e、ポート 4035）、候補は data/cand（ポート 4135）。
# destroy は参照 Redmine を停止して専用ディレクトリを削除する。
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
REF=""; d=$ROOT; while [ "$d" != "/" ]; do [ -d "$d/_reference/redmine-migrated" ] && { REF=$d/_reference; break; }; d=$(dirname "$d"); done
[ -n "$REF" ] || { echo "_reference/redmine-migrated not found" >&2; exit 1; }
export COMPAT_REF_DIR=${COMPAT_REF_DIR:-$REF/ref-e2e}
export COMPAT_REF_PORT=${COMPAT_REF_PORT:-4035}
export BUROPHER_CAND_DIR=${BUROPHER_CAND_DIR:-$ROOT/data/cand}
export BUROPHER_CAND_PORT=${BUROPHER_CAND_PORT:-4135}
ref() { "$ROOT/tools/compat/redmine-ref.sh" "$@"; }
cand() { "$ROOT/tools/compat/buropher-cand.sh" "$@"; }
case "${1:-}" in
  start) ref start; cand start ;;
  stop) ref stop; cand stop ;;
  reset) ref reset; cand reset ;;
  status) ref status || true; cand status ;;
  destroy) ref stop; cand stop; rm -rf "$COMPAT_REF_DIR" ;;
  # ブラウザは /tmp（小さい tmpfs）ではなく _reference/playwright-browsers に置く（headless shell のみ）
  install-browsers) cd "$ROOT/e2e" && PLAYWRIGHT_BROWSERS_PATH=${PLAYWRIGHT_BROWSERS_PATH:-$REF/playwright-browsers} npx playwright install --only-shell chromium ;;
  *) echo "usage: $0 start|stop|reset|status|destroy|install-browsers" >&2; exit 2 ;;
esac
