# frozen_string_literal: true

# internal/textformat/htmldom/testdata/html5.json の期待値（Loofah.html5_fragment(input).to_s）を
# 取り直すスクリプト。Redmine 7.0.1 の作業ツリーで実行する（DB は参照しない）:
#
#   cd /path/to/redmine
#   SECRET_KEY_BASE=x RAILS_ENV=production bin/rails runner \
#     /path/to/buropher/tools/gen-html5-fixtures.rb /path/to/buropher/internal/textformat/htmldom/testdata/html5.json
#
# 入力は既存ファイルの "input"（CommonMark・Textile の整形結果、サニタイズ対象の HTML、
# パーサの境界条件の手書きケース）。Nokogiri の上限（木の深さ 400・属性数 400）を超える入力は
# "error" に例外メッセージを記録する。

require 'json'

path = ARGV[0] or abort "usage: gen-html5-fixtures.rb html5.json"
inputs = JSON.parse(File.read(path)).map {|e| e['input']}
results = inputs.map do |input|
  {'input' => input, 'expected' => Loofah.html5_fragment(input).to_s}
rescue ArgumentError => e
  {'input' => input, 'error' => e.message}
end
File.write(path, JSON.generate(results))
STDERR.puts "wrote #{results.size} entries to #{path}"
