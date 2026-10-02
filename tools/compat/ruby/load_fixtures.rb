# frozen_string_literal: true
#
# buropher 互換テスト用: Redmine 公式 test/fixtures を固定時刻で投入するランナー。
# 使い方: bin/rails runner -e production path/to/load_fixtures.rb
#   COMPAT_FROZEN_TIME  fixtures 内 ERB (例: 1.day.ago) を評価する基準時刻 (UTC)
#   FIXTURES_PATH       fixtures ディレクトリ (既定 test/fixtures)
require 'active_support/testing/time_helpers'
require 'active_record/fixtures'

frozen = ENV.fetch('COMPAT_FROZEN_TIME', '2026-01-15 12:00:00 UTC')
dir = File.expand_path(ENV.fetch('FIXTURES_PATH', 'test/fixtures'), Rails.root)

helper = Object.new.extend(ActiveSupport::Testing::TimeHelpers)
helper.travel_to(Time.zone.parse(frozen)) do
  names = Dir[File.join(dir, '*.yml')].map { |f| File.basename(f, '.yml') }.sort
  ActiveRecord::FixtureSet.reset_cache
  ActiveRecord::FixtureSet.create_fixtures(dir, names)
  puts "loaded #{names.size} fixture sets from #{dir} at #{Time.now.utc}"

  # REST API を有効化（JSON/XML を Basic 認証で取得するため。テストの with_settings 相当）
  Setting.rest_api_enabled = '1'
  Setting.clear_cache
end
