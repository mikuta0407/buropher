# frozen_string_literal: true
#
# internal/i18n のゴールデンテスト用フィクスチャを、実際の Redmine (Rails I18n) から生成する。
#
# 使い方（リポジトリルートから。DB は読み取りのみで変更しない）:
#   SECRET_KEY_BASE=x RAILS_ENV=production GOLDEN_OUT=$PWD/internal/i18n/testdata \
#     _reference/redmine-migrated/bin/rails runner internal/i18n/testdata/gen_golden.rb
#
# 出力:
#   golden.json         ... l() / format_* / strftime / distance_* / number_* / タイムゾーン等のケース
#   translations.json.gz ... 全ロケール × 全キーの I18n.t 解決結果（フォールバック込み）

require 'json'
require 'zlib'

OUT = ENV.fetch('GOLDEN_OUT')
ENV['TZ'] = 'Asia/Tokyo'

include Redmine::I18n
include ActionView::Helpers::DateHelper
include ActionView::Helpers::NumberHelper

# Setting を DB から読まずに差し替える（DB は変更しない）
$settings = {date_format: '', time_format: '', timespan_format: 'minutes', default_language: 'en'}
%i[date_format time_format timespan_format default_language].each do |k|
  Setting.define_singleton_method(k) { $settings[k] }
end

def jsonable(v)
  case v
  when Symbol then {'__sym' => v.to_s}
  when Array then v.map { |e| jsonable(e) }
  when Hash then v.to_h { |k, e| [k.to_s, jsonable(e)] }
  when Proc then '__proc'
  when ActiveSupport::SafeBuffer then v.to_str
  else v
  end
end

def safe
  yield
rescue Exception => e # rubocop:disable Lint/RescueException
  {'__error' => e.class.name}
end

LOCALES = %w[en ja de fr ru pl zh zh-TW ar cs lv pt-BR en-GB sr-YU uk lt sl ko]
cases = []
add = lambda do |fn, locale, args, &blk|
  I18n.with_locale(locale) do
    cases << {'fn' => fn, 'locale' => locale, 'args' => jsonable(args), 'want' => jsonable(safe(&blk))}
  end
end

# ---- l() ----
counts = [0, 1, 2, 3, 4, 5, 11, 12, 21, 22, 25, 100, 101, 1.0, 1.5]
LOCALES.each do |loc|
  %w[label_issue field_subject general_lang_name general_csv_separator general_first_day_of_week
     notice_successful_create direction date.day_names date.abbr_month_names date.order nonexistent_key
     foo.bar.baz date.formats.default time.am activerecord.errors.messages.blank errors.format
     number.human.storage_units.format label_date_from button_save
     number.format.separator number.format.precision support.array.sentence_connector
     helpers.select.prompt number.human.decimal_units.units.thousand doorkeeper.layouts.admin.nav.home].each do |k|
    add.call('l', loc, [k]) { l(k.to_sym) }
  end
  add.call('l', loc, ['label_updated_time', '3 days']) { l(:label_updated_time, '3 days') }
  add.call('l', loc, ['label_f_hour', {'value' => '1.50'}]) { l(:label_f_hour, value: '1.50') }
  add.call('l', loc, ['text_journal_changed', {'label' => 'Status', 'old' => 'New', 'new' => 'Closed'}]) do
    l(:text_journal_changed, label: 'Status', old: 'New', new: 'Closed')
  end
  add.call('l', loc, ['errors.format', {'attribute' => 'Subject', 'message' => 'cannot be blank'}]) do
    l('errors.format', attribute: 'Subject', message: 'cannot be blank')
  end
  add.call('l', loc, ['activerecord.errors.messages.too_long', {'count' => 255}]) do
    l('activerecord.errors.messages.too_long', count: 255)
  end
  add.call('l', loc, ['nonexistent_key', 'x']) { l(:nonexistent_key, 'x') }
  counts.each do |c|
    %w[label_x_issues label_x_projects label_x_comments label_x_open_issues_abbr
       datetime.distance_in_words.x_days datetime.distance_in_words.about_x_hours
       datetime.distance_in_words.less_than_x_minutes number.human.storage_units.units.byte
       reaction_text_x_other_users].each do |k|
      add.call('l', loc, [k, c]) { l(k, c) }
    end
    add.call('l', loc, ['label_attachment_summary', {'filename' => 'a.txt', 'count' => c}]) do
      l(:label_attachment_summary, filename: 'a.txt', count: c)
    end
  end
  # l_or_humanize
  [['assigned_to', 'field_'], ['foo_bar_id', ''], ['xyz_thing', 'field_'], ['ProjectName', ''], ['status', 'field_'],
   ['_leading_under', ''], ['ABC def_ghi', '']].each do |s, prefix|
    add.call('l_or_humanize', loc, [s, prefix]) { l_or_humanize(s, prefix: prefix) }
  end
  # ll / lu
  add.call('ll', loc, ['de', 'label_issue']) { ll('de', :label_issue) }
  add.call('ll', loc, ['zh-tw', 'label_issue']) { ll('zh-tw', :label_issue) }
  add.call('ll', loc, ['pt-br', 'label_updated_time', '5 min']) { ll('pt-br', :label_updated_time, '5 min') }
  # 時間
  %w[minutes decimal].each do |tf|
    $settings[:timespan_format] = tf
    [0, 0.5, 1, 1.25, 1.333, 1.999, 2, -1.5, 1234.567, 0.004, 10.125].each do |h|
      add.call('format_hours', loc, [tf, h]) { format_hours(h) }
      add.call('l_hours', loc, [tf, h]) { l_hours(h) }
      add.call('l_hours_short', loc, [tf, h]) { l_hours_short(h) }
    end
  end
  $settings[:timespan_format] = 'minutes'
  (0..7).each do |d|
    add.call('day_name', loc, [d]) { day_name(d) }
    add.call('abbr_day_name', loc, [d]) { abbr_day_name(d) }
    add.call('day_letter', loc, [d]) { day_letter(d) }
  end
  (1..12).each { |m| add.call('month_name', loc, [m]) { month_name(m) } }
end
# lu: 言語未設定ユーザーは Setting.default_language
%w[ja fr].each do |dl|
  $settings[:default_language] = dl
  add.call('lu', 'en', ['', dl, 'label_issue']) { lu(User.new, :label_issue) }
  u = User.new; u.language = 'de'
  add.call('lu', 'en', ['de', dl, 'label_issue']) { lu(u, :label_issue) }
end
$settings[:default_language] = 'en'

# ---- 日付・時刻 ----
dates = [Date.new(2024, 3, 5), Date.new(2023, 12, 31), Date.new(2025, 1, 1), Date.new(2024, 2, 29)]
times = [Time.utc(2024, 3, 5, 14, 7, 9), Time.utc(2023, 12, 31, 23, 59, 59), Time.utc(2025, 7, 1, 0, 0, 0),
         Time.utc(2024, 11, 3, 6, 30, 0)]
zones = [nil, 'Tokyo', 'Eastern Time (US & Canada)', 'London', 'Kathmandu', 'UTC']
LOCALES.each do |loc|
  ([''] + Setting::DATE_FORMATS).each do |df|
    $settings[:date_format] = df
    dates.each { |d| add.call('format_date', loc, [df, d.iso8601]) { format_date(d) } }
  end
  $settings[:date_format] = ''
  %i[default short long].each do |f|
    dates.each { |d| add.call('l_date', loc, [f.to_s, d.iso8601]) { I18n.l(d, format: f) } }
  end
  %i[default time short long].each do |f|
    times.each { |t| add.call('l_time', loc, [f.to_s, t.iso8601]) { I18n.l(t, format: f) } }
  end
  [['', ''], ['%Y-%m-%d', '%H:%M'], ['%d %b %Y', '%I:%M %p'], ['', '%I:%M %p']].each do |df, tf|
    $settings[:date_format] = df
    $settings[:time_format] = tf
    zones.each do |z|
      u = User.new
      u.pref.time_zone = z
      times.each do |t|
        [true, false].each do |inc|
          add.call('format_time', loc, [df, tf, z, t.iso8601, inc]) { format_time(t, inc, u) }
        end
      end
    end
  end
  $settings[:date_format] = ''
  $settings[:time_format] = ''
end

# ---- strftime（非ローカライズ）----
patterns = ['%Y-%m-%d %H:%M:%S', '%-d/%-m/%y', '%e|%j|%U|%W|%u|%w', '%a %A %b %B %h', '%p %P %I %l %k',
            '%Z %z %:z %::z', '%s %L %N %3N %%', '%C %G %V %g', '%D|%F|%T|%R|%r', '%c|%x|%X', '%10Y|%-H|%_m|%^a|%#b|%010d',
            '%^B %^p %#p %-I %_H %05e %-j %y', '%8z %-z %_z %Q %v %+', '100%% done %q %E %O']
ztimes = times.map { |t| t.in_time_zone('Tokyo') } + times.map { |t| t.in_time_zone('Eastern Time (US & Canada)') } +
         [Time.utc(2024, 1, 7, 12, 0, 0).in_time_zone('Kathmandu'), Time.utc(1999, 1, 1, 0, 0, 0.123456789r).in_time_zone('UTC')]
patterns.each do |p|
  ztimes.each do |t|
    cases << {'fn' => 'strftime', 'locale' => 'en', 'args' => [p, t.utc.iso8601(9), t.time_zone.name],
              'want' => jsonable(safe { t.strftime(p) })}
  end
end

# ---- distance_of_time_in_words ----
secs = [0, 1, 4, 5, 9, 10, 19, 20, 29, 30, 39, 40, 59, 60, 89, 90, 119, 120, 150, 2699, 2700, 5399, 5400, 86399, 86400,
        151199, 151200, 2591999, 2592000, 5183999, 5184000, 31535999, 31536000, 39420000, 47304000, 63072000,
        94608000, 315360000, 3153600000]
from = Time.utc(2020, 1, 15, 10, 0, 0)
%w[en ja de fr ru pl zh zh-TW ar cs].each do |loc|
  secs.each do |s|
    [false, true].each do |inc|
      add.call('distance_of_time_in_words', loc, [from.iso8601, (from + s).iso8601, inc]) do
        distance_of_time_in_words(from, from + s, include_seconds: inc)
      end
    end
  end
  add.call('distance_of_time_in_words', loc, [(from + 400).iso8601, from.iso8601, false]) do
    distance_of_time_in_words(from + 400, from)
  end
  [Time.utc(2019, 2, 27, 0, 0, 0), Time.utc(2016, 2, 28, 0, 0, 0)].each do |f|
    [Time.utc(2021, 3, 1), Time.utc(2024, 2, 29, 12, 0, 0), Time.utc(2017, 3, 1)].each do |t|
      add.call('distance_of_time_in_words', loc, [f.iso8601, t.iso8601, false]) { distance_of_time_in_words(f, t) }
    end
  end
  [0, 1, 2, 30, 60, 61, 89, 90, 365, 720, 721, 1000, 4000].each do |d|
    add.call('distance_of_date_in_words', loc, [Date.new(2020, 1, 15).iso8601, (Date.new(2020, 1, 15) + d).iso8601]) do
      distance_of_date_in_words(Date.new(2020, 1, 15), Date.new(2020, 1, 15) + d)
    end
  end
end

# ---- 数値 ----
sizes = [0, 1, 2, 100, 1023, 1024, 1025, 1536, 10_000, 1_048_575, 1_048_576, 1_500_000, 123_456_789, 5.kilobytes * 1000,
         1_073_741_824, 9_999_999_999, 1_099_511_627_776, 1_125_899_906_842_624, 1_152_921_504_606_846_976, -2048, 10.5]
LOCALES.each do |loc|
  sizes.each { |s| add.call('number_to_human_size', loc, [s]) { number_to_human_size(s) } }
  [0, 12, 1234, 1_234_567, -9_876_543, 1234.5].each do |n|
    add.call('number_with_delimiter', loc, [n, nil]) { number_with_delimiter(n, delimiter: nil) }
    add.call('number_with_delimiter', loc, [n, 'locale']) { number_with_delimiter(n, delimiter: I18n.t('number.format.delimiter')) }
    add.call('number_with_delimiter', loc, [n, 'default']) { number_with_delimiter(n) }
  end
  %w[1234.50 0.25 -1234567.00].each do |n|
    add.call('number_with_delimiter', loc, [n, nil]) { number_with_delimiter(n, delimiter: nil) }
    add.call('number_with_delimiter', loc, [n, 'locale']) { number_with_delimiter(n, delimiter: I18n.t('number.format.delimiter')) }
  end
  [[1234.5678, 2], [0.005, 2], [2.5, 0], [1.0 / 3, 3], [-0.004, 2], [100, 1]].each do |n, pr|
    add.call('number_with_precision', loc, [n, pr]) { number_with_precision(n, precision: pr) }
  end
end

# ---- 言語 ----
cases << {'fn' => 'valid_languages', 'locale' => 'en', 'args' => [], 'want' => valid_languages.map(&:to_s).sort}
cases << {'fn' => 'languages_options', 'locale' => 'en', 'args' => [], 'want' => languages_options(cache: false)}
%w[ja JA zh-tw zh-TW pt-br en-gb xx de-DE sr-yu].each do |s|
  cases << {'fn' => 'find_language', 'locale' => 'en', 'args' => [s], 'want' => find_language(s)&.to_s}
end

# ---- タイムゾーン ----
# 基準オフセットは評価時刻で変わる（Africa/Casablanca 等）ため、zones_gen.go（gen_timezones.rb）と同じく
# 互換テストの固定時刻で評価して並べ直す。
require 'active_support/testing/time_helpers'
Object.new.extend(ActiveSupport::Testing::TimeHelpers).instance_eval do
  travel_to(Time.parse(ENV['COMPAT_FROZEN_TIME'].presence || '2026-01-15 12:00:00 UTC')) do
    cases << {'fn' => 'time_zones', 'locale' => 'en', 'args' => [],
              'want' => ActiveSupport::TimeZone.all.sort.map { |z| [z.to_s, z.name, z.tzinfo.identifier] }}
  end
end

# 差分を見やすくするため 1 ケース 1 行で出力する
File.write(File.join(OUT, 'golden.json'),
           "{\"tz\": #{ENV['TZ'].to_json}, \"cases\": [\n" + cases.map { |c| JSON.generate(c) }.join(",\n") + "\n]}\n")

# ---- 全訳文ダンプ ----
I18n.backend.send(:init_translations)
all = I18n.backend.send(:translations)
def flatten_keys(h, prefix, out)
  h.each do |k, v|
    key = prefix.empty? ? k.to_s : "#{prefix}.#{k}"
    if v.is_a?(Hash)
      flatten_keys(v, key, out)
    else
      out << key
    end
  end
end
keys = []
all.each_value { |tree| flatten_keys(tree, '', keys) }
keys = keys.uniq.sort.reject { |k| k.start_with?('number.nth', 'i18n.') }
dump = {}
I18n.available_locales.sort.each do |loc|
  m = {}
  keys.each do |k|
    m[k] = jsonable(safe { I18n.t(k, locale: loc) })
  end
  dump[loc.to_s] = m
end
Zlib::GzipWriter.open(File.join(OUT, 'translations.json.gz')) { |gz| gz.write(JSON.generate(dump)) }
puts "cases=#{cases.size} keys=#{keys.size}"
