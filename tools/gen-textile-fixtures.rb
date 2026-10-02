# frozen_string_literal: true
#
# Redmine の Textile フォーマッタ (Redmine::WikiFormatting::Textile::Formatter)
# を実際に動かして、Go 版 (internal/textformat/textile) の期待値フィクスチャを生成する。
#
# 使い方 (Redmine の作業ツリーで rails runner 経由で実行する):
#   cd /path/to/redmine && SECRET_KEY_BASE=x RAILS_ENV=production \
#     bin/rails runner /path/to/buropher/tools/gen-textile-fixtures.rb \
#     /path/to/buropher/internal/textformat/textile/testdata
#
# 入力コーパス:
#   1. Redmine のテスト (textile_formatter_test.rb / application_helper_test.rb) の文字列リテラル
#   2. test/fixtures/*.yml の本文 (wiki / issue / journal / news / message など)
#   3. 本スクリプト内で手作りしたエッジケース
#
# シンタックスハイライトは Rouge に依存させないため、決定的なフェイクに差し替える
# (Go 側テストでも同じフェイクを使う)。

require 'json'
require 'prism'
require 'yaml'

out_dir = ARGV[0] or abort "usage: gen-textile-fixtures.rb OUTDIR"
redmine_root = Rails.root.to_s
test_root = ENV['REDMINE_TEST_ROOT'] || File.join(redmine_root, 'test')
test_root = '/home/mikuta0407/projects/buropher/_reference/redmine/test' unless File.directory?(test_root)

# ---- フェイクハイライタ ----
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

inputs = []

# ---- 1. テストファイルの文字列リテラル ----
def string_literals(path)
  res = []
  queue = [Prism.parse_file(path).value]
  until queue.empty?
    node = queue.shift
    next unless node

    case node
    when Prism::StringNode
      res << node.unescaped
      next
    when Prism::InterpolatedStringNode
      # <<~ ヒアドキュメントは行ごとの StringNode に分かれるので連結する
      if node.parts.all?(Prism::StringNode)
        res << node.parts.map(&:unescaped).join
        next
      end
    end
    queue.concat(node.compact_child_nodes)
  end
  res
end

%w(
  unit/lib/redmine/wiki_formatting/textile_formatter_test.rb
  helpers/application_helper_test.rb
  unit/lib/redmine/wiki_formatting/macros_test.rb
).each do |rel|
  path = File.join(test_root, rel)
  next unless File.exist?(path)

  string_literals(path).each {|s| inputs << ["test:#{rel}", s]}
end

# textile_formatter_test のヒアドキュメント入力 (Ripper 抽出で拾えないもの) は
# 手作りケースにも同等のものを入れてある。

# ---- 2. フィクスチャ YAML の本文 ----
{
  'wiki_contents.yml' => %w(text),
  'wiki_content_versions.yml' => %w(data),
  'issues.yml' => %w(description),
  'journals.yml' => %w(notes),
  'news.yml' => %w(description summary),
  'messages.yml' => %w(content),
  'documents.yml' => %w(description),
  'comments.yml' => %w(comments),
}.each do |file, fields|
  path = File.join(test_root, 'fixtures', file)
  next unless File.exist?(path)

  begin
    data = YAML.safe_load(ERB.new(File.read(path)).result, permitted_classes: [Date, Time, Symbol], aliases: true)
  rescue StandardError
    next
  end
  next unless data.is_a?(Hash)

  data.each do |key, row|
    next unless row.is_a?(Hash)

    fields.each do |f|
      v = row[f]
      inputs << ["fixture:#{file}:#{key}", v] if v.is_a?(String) && !v.empty?
    end
  end
end

# ---- 3. 手作りエッジケース ----
crafted = File.read(File.join(__dir__, 'textile-corpus.txt')).split(/^=====\n/).map {|s| s.chomp}
crafted.each_with_index {|s, i| inputs << ["crafted:#{i}", s]}
# ファイルに書きにくい制御文字・改行コードなど
[
  "CRLF line1\r\nCRLF line2\r\n\r\nCRLF para",
  "CR only\rsecond",
  "ctrl\u0001char and \u0007bell",
  " \f", " \v", "\f", "x\u0085y", "nbsp here", "zwj‍here",
  "trailing newline\n", "two trailing\n\n", "h1. Title\n\ntext\n",
  "* a\n* b\n", "|a|b|\n", "<pre>\nx\n</pre>\n",
  "line\t\ttabs\tinside", "\tleading tab\n\tsecond",
  "a\n\n\n\n\nb",
  "x　y　",
].each_with_index {|s, i| inputs << ["special:#{i}", s]}

# 重複除去
seen = {}
inputs = inputs.select do |name, s|
  next false if seen[s]

  seen[s] = true
end

cases = inputs.map do |name, s|
  html =
    begin
      Redmine::WikiFormatting::Textile::Formatter.new(s.dup).to_html
    rescue StandardError => e
      "!!ERROR #{e.class}: #{e.message}"
    end
  {'name' => name, 'input' => s, 'html' => html}
end

# ---- セクション ----
section_cases = []
inputs.each do |name, s|
  next unless /^h\d/.match?(s)

  n = s.scan(/^h\d/).size
  (0..n + 1).each do |i|
    f = Redmine::WikiFormatting::Textile::Formatter.new(s.dup)
    sec, hash = f.get_section(i)
    upd = Redmine::WikiFormatting::Textile::Formatter.new(s.dup).update_section(i, "UPDATED")
    section_cases << {'name' => name, 'input' => s, 'index' => i, 'section' => sec, 'hash' => hash, 'updated' => upd}
  end
end

FileUtils.mkdir_p(out_dir)
File.write(File.join(out_dir, 'fixtures.json'), JSON.pretty_generate(cases) + "\n")
File.write(File.join(out_dir, 'sections.json'), JSON.pretty_generate(section_cases) + "\n")
puts "cases: #{cases.size}, section cases: #{section_cases.size}"
