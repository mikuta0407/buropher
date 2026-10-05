# frozen_string_literal: true
#
# 既存の Textile 差分テスト用データ ({"name", "input", "html"} の配列。例: internal/textformat/textile/testdata/fuzz.json)
# の入力はそのままに、期待値 "html" だけを現在の Redmine で取り直す。
#
#   cd /path/to/redmine && SECRET_KEY_BASE=x RAILS_ENV=production \
#     bin/rails runner /path/to/buropher/tools/textformat/resnapshot-textile.rb IN.json [OUT.json]
#
# フェイクハイライタは tools/gen-textile-fixtures.rb と同じ。

require 'json'
require 'timeout'

in_path = ARGV[0] or abort "usage: resnapshot-textile.rb IN.json [OUT.json]"
out_path = ARGV[1] || in_path

FAKE_LANGS = %w(ruby c python javascript sql bash xml html java go).freeze
module Redmine
  module SyntaxHighlighting
    class << self
      def language_supported?(language)
        FAKE_LANGS.include?(language.to_s)
      end

      def highlight_by_language(text, language)
        %(<span class="hl-#{language}">#{ERB::Util.h(text)}</span>)
      end
    end
  end
end

cases = JSON.parse(File.read(in_path)).filter_map do |c|
  html =
    begin
      Timeout.timeout(10) { Redmine::WikiFormatting::Textile::Formatter.new(c['input'].dup).to_html }
    rescue ArgumentError, Timeout::Error => e
      STDERR.puts "skip #{c['name']}: #{e.class}"
      nil
    end
  c.merge('html' => html) if html
end
File.write(out_path, JSON.pretty_generate(cases) + "\n")
STDERR.puts "wrote #{cases.size} cases to #{out_path}"
