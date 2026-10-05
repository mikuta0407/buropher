#!/usr/bin/env bash
# Redmine の静的アセット・ロケールを web/ 配下へ同期する。
# 使い方: tools/sync-upstream.sh [redmine-src-dir] [gems-dir]
set -euo pipefail
cd "$(dirname "$0")/.."
SRC=${1:-_reference/redmine}
GEMS=${2:-_reference/vendor_bundle7/ruby/3.3.0/gems}

LOCK=${LOCK:-_reference/redmine702-migrated/Gemfile.lock}

sync() { rm -rf "$2"; mkdir -p "$(dirname "$2")"; cp -a "$1" "$2"; }
# gemdir は gem のディレクトリを返す（GEMS に複数版があるときは LOCK の版、なければ最新版）。
gemdir() {
  local v=""
  [ -f "$LOCK" ] && v=$(grep -E "^    $1 \(" "$LOCK" | head -1 | sed -E 's/.*\((.*)\)/\1/')
  if [ -n "$v" ] && [ -d "$GEMS/$1-$v" ]; then echo "$GEMS/$1-$v"; else ls -d "$GEMS/$1"-[0-9]* | sort -V | tail -1; fi
}

# app/assets (images, fonts, javascripts, stylesheets, themes)
for d in images fonts javascripts stylesheets themes; do
  sync "$SRC/app/assets/$d" "web/assets/$d"
done
# importmap 経由の JS（Stimulus コントローラ等）
sync "$SRC/app/javascript" "web/assets/javascript"
sync "$SRC/vendor/javascript" "web/assets/vendor"
# gem 同梱の JS
mkdir -p web/assets/javascripts
cp "$(gemdir actionview)"/app/assets/javascripts/rails-ujs.js web/assets/javascripts/rails-ujs.js
for f in stimulus.min.js stimulus.min.js.map stimulus-loading.js; do
  [ -f "$(gemdir stimulus-rails)"/app/assets/javascripts/$f ] && cp "$(gemdir stimulus-rails)"/app/assets/javascripts/$f web/assets/vendor/$f
done
cp "$(gemdir requestjs-rails)"/app/assets/javascripts/requestjs.js web/assets/vendor/requestjs.js
# 同梱ライブラリ・アイコン・フォントのライセンス文（Redmine 7.0 から doc/licenses に集約。THIRD_PARTY_NOTICES.md）
sync "$SRC/doc/licenses" "docs/licenses"
# ロケール
sync "$SRC/config/locales" "web/locales/redmine"
# Rails / doorkeeper-i18n gem 同梱ロケール（Redmine 実行時の I18n.load_path に先に積まれる分。MIT）
rm -rf web/locales/rails; mkdir -p web/locales/rails
for g in activesupport:active_support activemodel:active_model activerecord:active_record actionview:action_view; do
  cp "$(gemdir ${g%%:*})"/lib/${g#*:}/locale/en.yml "web/locales/rails/${g%%:*}.en.yml"
done
# doorkeeper-i18n は available_locales（= Redmine のロケール）に含まれるものだけを読み込む
for f in "$(gemdir doorkeeper-i18n)"/rails/locales/*.yml; do
  [ -f "web/locales/redmine/$(basename "$f")" ] && cp "$f" "web/locales/rails/doorkeeper.$(basename "$f")"
done
# doorkeeper gem 本体の en.yml（doorkeeper-i18n の後に読み込まれる）
cp "$(gemdir doorkeeper)"/config/locales/en.yml web/locales/rails/doorkeeper-gem.en.yml
# 静的エラーページ
mkdir -p web/public
cp "$SRC/public/404.html" "$SRC/public/500.html" web/public/
# 製品名の置換（Buropher のブランディング。ロケールは internal/i18n が読み込み時に置換する）
sed -i 's/Redmine/Buropher/g' web/public/404.html web/public/500.html
# UPSTREAM_VERSION より新しい本家のセキュリティ修正（tools/upstream-patches/*.patch。例: 7.0.2 #44429）を当て直す
for p in tools/upstream-patches/*.patch; do
  [ -f "$p" ] || continue
  patch -p1 -N -r - --no-backup-if-mismatch < "$p" || echo "WARN: $p did not apply (already upstream?)" >&2
done
# バージョン記録
grep -E "MAJOR|MINOR|TINY" "$SRC/lib/redmine/version.rb" | head -3 > web/UPSTREAM_VERSION
echo "synced from $SRC"
