#!/bin/bash
# 差分シナリオの正解データ (testdata/scenario*.json) を再生成する。
# usage: internal/issues/testdata/gen/regen.sh [作業ディレクトリ]
# 作業ディレクトリ (既定 _reference/issue-test) に公式フィクスチャ投入済み DB をコピーして実行する。
set -e
here="$(cd "$(dirname "$0")" && pwd)"
work="${1:-/home/mikuta0407/projects/buropher/_reference/issue-test}"
pristine="${REDMINE_PRISTINE_DB:-/home/mikuta0407/projects/buropher/_reference/redmine-fixtures/db/redmine.pristine.sqlite3}"
mkdir -p "$work"
for n in "" 2; do
  cp "$pristine" "$work/scenario$n.sqlite3"
  "$here/runner.sh" "$work/scenario$n.sqlite3" "$here/dump_scenario$n.rb" "$here/../scenario$n.json"
done
