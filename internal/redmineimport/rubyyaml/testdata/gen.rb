# rubyyaml のテストフィクスチャ(testdata/fixtures.json)を生成する。
# 期待値は Ruby(Psych 5 / Rails 7.2)で実際に YAML.unsafe_load した結果を型注釈付き JSON にしたもの。
#
# 使い方(DB は必ずコピーを指定する):
#   cd <redmine> && DATABASE_URL=sqlite3:<copy.sqlite3> SECRET_KEY_BASE=x RAILS_ENV=production \
#     bin/rails runner <this file> > fixtures.json
require 'json'
require 'set'
require 'bigdecimal'

def key_string(k)
  case k
  when nil then ''
  when String then k
  when Symbol then k.to_s
  when Date then k.strftime('%Y-%m-%d')
  when Integer, TrueClass, FalseClass then k.to_s
  when Float then k.to_s
  else k.to_s
  end
end

# Go 側 annotate() と同じ型注釈形式へ変換する
def conv(v)
  case v
  when nil, true, false then v
  when Symbol then { '$sym' => v.to_s }
  when Integer
    if v.bit_length >= 64 then v.to_s else v end
  when Float then { '$float' => v.nan? ? 'NaN' : (v.infinite? ? (v > 0 ? '+Inf' : '-Inf') : v.to_s.sub(/\.0\z/, '')) }
  when BigDecimal then { '$float' => v.to_f.to_s.sub(/\.0\z/, '') }
  when ActiveSupport::TimeWithZone then { '$time' => v.utc.strftime('%Y-%m-%dT%H:%M:%S.%N%:z') }
  when Time then { '$time' => v.strftime('%Y-%m-%dT%H:%M:%S.%N%:z') }
  when Date then { '$date' => v.strftime('%Y-%m-%d') }
  when String
    if v.encoding == Encoding::ASCII_8BIT && !v.dup.force_encoding('UTF-8').valid_encoding?
      v.dup.force_encoding('ISO-8859-1').encode('UTF-8')
    else
      v.dup.force_encoding('UTF-8')
    end
  when ActionController::Parameters then conv(v.to_unsafe_h)
  when Set then v.to_a.map { |e| conv(e) }
  when Hash then { '$hash' => v.map { |k, x| [key_string(k), conv(x)] } }
  when Array then v.map { |e| conv(e) }
  else
    { '$hash' => v.instance_variables.map { |iv| [iv.to_s.delete('@'), conv(v.instance_variable_get(iv))] } }
  end
end

fixtures = []
add = lambda do |name, yaml|
  fixtures << { 'name' => name, 'yaml' => yaml, 'expected' => conv(YAML.unsafe_load(yaml)) }
end

# 1. 実 DB の YAML 列
conn = ActiveRecord::Base.connection
[
  %w[settings value name],
  %w[user_preferences others id],
  %w[queries filters id], %w[queries column_names id], %w[queries sort_criteria id], %w[queries options id],
  %w[roles permissions id], %w[roles settings id],
  %w[custom_fields possible_values id], %w[custom_fields format_store id],
  %w[repositories extra_info id], %w[imports settings id]
].each do |table, col, key|
  conn.select_rows("SELECT #{key}, #{col} FROM #{table} WHERE #{col} LIKE '---%'").each do |k, val|
    add.call("db:#{table}.#{col}[#{k}]", val)
  end
end

# 2. Ruby で生成した値
t = Time.new(2026, 10, 3, 12, 34, 56.789r, '+09:00')
generated = {
  'symbol_hash' => { a: 1, 'b' => :c, d: [:e, 'f'], 'nil' => nil },
  'hwia' => ActiveSupport::HashWithIndifferentAccess.new(x: 1, 'y' => { z: [1, 2] }),
  'ac_parameters' => ActionController::Parameters.new('f' => { 'status_id' => { 'operator' => 'o', 'values' => [''] } }, 'c' => ['tracker']),
  'binary_utf8' => 'グループ'.b,
  'binary_latin1' => "caf\xE9".b,
  'quoted_specials' => ['yes', 'no', 'on', 'off', 'true', 'null', '~', '', ' lead', '1,000', '12:30', ':foo', '0755', '0x1F', '1e5', '1.5', '.5', '2026-10-03', '2026-10-03 12:00:00', '-', '- x', '#c', 'a: b', '@x', "multi\nline", "tab\tch"],
  'numbers' => [0, -1, 42, 2**62, 2**70, 1.0, -2.5, 1.0e20, 3.14159, Float::INFINITY, -Float::INFINITY],
  'time' => t,
  'time_utc' => Time.utc(2026, 1, 2, 3, 4, 5),
  'date' => Date.new(2026, 10, 3),
  'twz' => t.in_time_zone('Tokyo'),
  'set' => Set.new(%w[a b]),
  'bigdecimal' => BigDecimal('12.5'),
  'unicode' => { '日本語キー' => 'テスト 🎉', :ラベル => 'ユニコード' },
  'nested' => { queries: [{ filters: { 'cf_1' => { operator: '=', values: ['1'] } } }] },
  'shared_ref' => (s = %w[x y]; { 'a' => s, 'b' => s }),
  'empty' => { 'h' => {}, 'a' => [], 's' => '' },
  'bools' => [true, false, nil],
  'long_text' => 'a' * 200 + "\n" + 'b' * 10,
  'string_with_ivar' => ('str'.dup.tap { |x| x.instance_variable_set(:@foo, 1) }),
  'symbol_quoted' => :"foo bar",
  'symbol_colon' => :":x",
  'empty_symbol_str' => ':'
}
generated.each { |name, v| add.call("gen:#{name}", v.to_yaml) }

# 3. 手書き YAML(プレーンスカラーの解決規則・古い形式)
{
  'plain_bools' => "--- [yes, No, ON, off, TRUE, false, y, n, Yes]\n",
  'plain_nulls' => "--- [~, null, Null, NULL, '']\n",
  'plain_ints' => "--- [1_000, 1,000, 0x1f, 0b101, 0755, -42, +7, 0]\n",
  'plain_sexagesimal' => "--- [1:30, 1:30:15, 1:30.5]\n",
  'plain_floats' => "--- [1.5, .5, -1.25, 1.0e+5, 1_000.5, .inf, -.Inf, 1.]\n",
  'plain_dates' => "--- [2026-10-03, 2026-1-2, 2026-02-30]\n",
  'plain_times' => "--- [2026-10-03 12:00:00 +09:00, 2026-10-03T12:00:00Z, 2026-10-03 12:00:00.5, 2026-10-03t01:02:03-05]\n",
  'plain_symbols' => "---\n- :foo\n- ':notsym'\n- :\"quoted sym\"\n- :'single'\n- :a:b\n",
  'plain_strings' => "--- [hello world, abc, '123', \"456\", 1.2.3, 12abc, Yesterday, nothing, -foo, .foo]\n",
  'ruby_symbol_tags' => "--- [!ruby/symbol foo, !ruby/sym bar, !ruby/string baz, !str 123, !!str 456]\n",
  'legacy_hwia' => "--- !ruby/hash:ActiveSupport::HashWithIndifferentAccess\nurl_pattern: ''\nedit_tag_style: check_box\nuser_role:\n- ''\n- '3'\n",
  'legacy_ac_parameters' => "--- !ruby/hash-with-ivars:ActionController::Parameters\nelements:\n  status_id: !ruby/hash-with-ivars:ActionController::Parameters\n    elements:\n      operator: o\n      values:\n      - ''\n    ivars:\n      :@permitted: false\n  tracker_id:\n    operator: \"=\"\n    values:\n    - '1'\nivars:\n  :@permitted: false\n",
  'merge_keys' => "---\nbase: &b\n  a: 1\n  b: 2\nderived:\n  <<: *b\n  b: 3\n  c: 4\n",
  'binary_tag' => "--- !binary |-\n  44OG44K544OI\n",
  'binary_list' => "---\n- !binary |-\n  5pel5pys6Kqe\n- plain\n",
  'explicit_types' => "--- [!!int '12', !!float '1.5', !!null '', !!bool 'yes', !!binary 'YWJj']\n",
  'flow_mapping' => "--- {a: 1, b: [x, y], 'c': {d: e}}\n",
  'document_end' => "--- foo\n...\n",
  'scalar_only' => "--- 42\n",
  'int_keys' => "---\n1: one\n2: two\n",
  'dup_keys' => "---\na: 1\nb: 2\na: 3\n",
  'block_scalars' => "---\nlit: |\n  line1\n  yes\nfold: >\n  a\n  b\n",
  'set_tag' => "--- !!set\na: \nb: \n",
  'ruby_object_generic' => "--- !ruby/object:OpenStruct\ntable:\n  :name: x\n",
  'ruby_struct' => "--- !ruby/struct:Point\nx: 1\ny: 2\n",
  'unicode_plain' => "--- [日本語, テスト, ü, 12時]\n"
}.each do |name, y|
  begin
    add.call("hand:#{name}", y)
  rescue => e
    warn "skip #{name}: #{e.class} #{e.message}"
  end
end

puts JSON.pretty_generate(fixtures)
