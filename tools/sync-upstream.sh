#!/usr/bin/env bash
# Redmine の静的アセット・ロケールを web/ 配下へ同期する。
# 使い方: tools/sync-upstream.sh [redmine-src-dir] [gems-dir]
set -euo pipefail
cd "$(dirname "$0")/.."
SRC=${1:-_reference/redmine}
GEMS=${2:-_reference/vendor_bundle/ruby/3.3.0/gems}

sync() { rm -rf "$2"; mkdir -p "$(dirname "$2")"; cp -a "$1" "$2"; }

# app/assets (images, fonts, javascripts, stylesheets, themes)
for d in images fonts javascripts stylesheets themes; do
  sync "$SRC/app/assets/$d" "web/assets/$d"
done
# importmap 経由の JS（Stimulus コントローラ等）
sync "$SRC/app/javascript" "web/assets/javascript"
sync "$SRC/vendor/javascript" "web/assets/vendor"
# gem 同梱の JS
mkdir -p web/assets/javascripts
cp "$GEMS"/actionview-7.*/app/assets/javascripts/rails-ujs.js web/assets/javascripts/rails-ujs.js
for f in stimulus.min.js stimulus.min.js.map stimulus-loading.js; do
  [ -f "$GEMS"/stimulus-rails-1.*/app/assets/javascripts/$f ] && cp "$GEMS"/stimulus-rails-1.*/app/assets/javascripts/$f web/assets/vendor/$f
done
cp "$GEMS"/requestjs-rails-*/app/assets/javascripts/requestjs.js web/assets/vendor/requestjs.js
# ロケール
sync "$SRC/config/locales" "web/locales/redmine"
# Rails / doorkeeper-i18n gem 同梱ロケール（Redmine 実行時の I18n.load_path に先に積まれる分。MIT）
rm -rf web/locales/rails; mkdir -p web/locales/rails
for g in activesupport:active_support activemodel:active_model activerecord:active_record actionview:action_view; do
  cp "$GEMS"/${g%%:*}-7.*/lib/${g#*:}/locale/en.yml "web/locales/rails/${g%%:*}.en.yml"
done
# doorkeeper-i18n は available_locales（= Redmine のロケール）に含まれるものだけを読み込む
for f in "$GEMS"/doorkeeper-i18n-*/rails/locales/*.yml; do
  [ -f "web/locales/redmine/$(basename "$f")" ] && cp "$f" "web/locales/rails/doorkeeper.$(basename "$f")"
done
# doorkeeper gem 本体の en.yml（doorkeeper-i18n の後に読み込まれる）
cp "$GEMS"/doorkeeper-5.*/config/locales/en.yml web/locales/rails/doorkeeper-gem.en.yml
# 静的エラーページ
mkdir -p web/public
cp "$SRC/public/404.html" "$SRC/public/500.html" web/public/
# バージョン記録
grep -E "MAJOR|MINOR|TINY" "$SRC/lib/redmine/version.rb" | head -3 > web/UPSTREAM_VERSION
echo "synced from $SRC"
