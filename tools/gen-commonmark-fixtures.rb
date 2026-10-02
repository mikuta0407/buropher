# frozen_string_literal: true

# Redmine の CommonMark 整形結果（正解データ）を生成するスクリプト。
#
# 使い方（Redmine 6.1.2 の作業ツリーで実行する。DB は参照しない）:
#
#   cd /path/to/redmine
#   SECRET_KEY_BASE=x RAILS_ENV=production bin/rails runner \
#     /path/to/buropher/tools/gen-commonmark-fixtures.rb \
#     $T/fixtures.json $T/corpus.txt $T/corpus_edge.txt $T/corpus_more.txt \
#     $T/corpus_spec.json $T/corpus_comrak.json $T/corpus_highlight.json $T/corpus_fuzz.json
#
#   （T=/path/to/buropher/internal/textformat/commonmark/testdata）
#
# corpus*.txt は手書きのコーパス、corpus_spec.json / corpus_comrak.json /
# corpus_highlight.json は tools/textformat/mkcorpus.py で外部のテスト資産
# （CommonMark 仕様の例、comrak のテスト、Rouge のデモ）から、corpus_fuzz.json は
# tools/textformat/mkfuzz.py で構文要素をランダムに組み合わせて生成したもの。
#
# コーパスは 2 形式に対応する:
#   *.json : {"name", "mode", "input", ...} の配列
#   *.txt  : "@@@ 名前 key=value key=\"JSON文字列\" ..." の見出し行で区切ったテキスト。
#            次の見出し行までがそのまま入力になる（末尾改行を含む）。
#            chomp=1 で末尾改行を 1 つ除去、esc=1 で \n \r \t \0 \uXXXX \\ を展開する。
#            最初の見出し行より前の行はコメント。
#
# mode ごとの動作:
#   commonmark     : Redmine::WikiFormatting::CommonMark::Formatter#to_html
#                    （"hardbreaks": false を指定すると hardbreaks 無効のパイプラインで整形）
#   sanitize       : Redmine::WikiFormatting::HtmlSanitizer.call
#   highlight      : Redmine::SyntaxHighlighting.highlight_by_language(input, lang)
#   highlight_file : Redmine::SyntaxHighlighting.highlight_by_filename(input, filename)
#   supported      : Redmine::SyntaxHighlighting.language_supported?(lang)
#   section_get    : Formatter#get_section(index) → [text, hash]
#   section_update : Formatter#update_section(index, replacement, hash)
#
# 出力は入力の各要素に "expected" を加えたもの。

require 'json'

out_path, *corpus_paths = ARGV
abort "usage: gen-commonmark-fixtures.rb fixtures.json corpus..." if corpus_paths.empty?

def unescape(str)
  str.gsub(/\\(u\h{4}|.)/m) do
    c = $1
    case c
    when 'n' then "\n"
    when 'r' then "\r"
    when 't' then "\t"
    when '0' then "\0"
    when '\\' then '\\'
    else
      c.start_with?('u') && c.size == 5 ? [c[1..].hex].pack('U') : "\\#{c}"
    end
  end
end

def parse_txt(path)
  entries = []
  cur = nil
  File.read(path, encoding: 'UTF-8').each_line do |line|
    if line.start_with?('@@@ ')
      entries << cur if cur
      name, rest = line[4..].chomp.split(' ', 2)
      cur = {'name' => name, 'input' => +''}
      (rest || '').scan(/(\w+)=("(?:[^"\\]|\\.)*"|\S+)/) do |k, v|
        v =
          if v.start_with?('"')
            JSON.parse(v)
          elsif v =~ /\A-?\d+\z/
            v.to_i
          elsif v == 'true' || v == 'false'
            v == 'true'
          else
            v
          end
        cur[k] = v
      end
    elsif cur
      cur['input'] << line
    end
  end
  entries << cur if cur
  entries.each do |e|
    e['input'] = unescape(e['input']) if e.delete('esc')
    e['input'] = e['input'].chomp if e.delete('chomp')
  end
  entries
end

cm = Redmine::WikiFormatting::CommonMark

# hardbreaks 無効版のパイプライン
no_hardbreaks_config = cm::PIPELINE_CONFIG.merge(
  commonmarker_render_options: cm::PIPELINE_CONFIG[:commonmarker_render_options].merge(hardbreaks: false)
)
no_hardbreaks_pipeline = HTML::Pipeline.new(cm::MarkdownPipeline.filters, no_hardbreaks_config)

I18n.locale = :en

entries = corpus_paths.flat_map do |path|
  path.end_with?('.json') ? JSON.parse(File.read(path)) : parse_txt(path)
end
names = entries.map {|e| e['name']}
dup = names.select {|n| names.count(n) > 1}.uniq
abort "duplicate names: #{dup.join(', ')}" unless dup.empty?

results = entries.map do |e|
  input = e['input']
  expected =
    case e['mode'] || 'commonmark'
    when 'commonmark'
      if e['hardbreaks'] == false
        no_hardbreaks_pipeline.call(input)[:output].to_s
      else
        cm::Formatter.new(input).to_html
      end
    when 'sanitize'
      Redmine::WikiFormatting::HtmlSanitizer.call(input)
    when 'highlight'
      Redmine::SyntaxHighlighting.highlight_by_language(input, e['lang'])
    when 'highlight_file'
      Redmine::SyntaxHighlighting.highlight_by_filename(input, e['filename'])
    when 'supported'
      Redmine::SyntaxHighlighting.language_supported?(e['lang'])
    when 'section_get'
      cm::Formatter.new(input).get_section(e['index'])
    when 'section_update'
      begin
        cm::Formatter.new(input).update_section(e['index'], e['replacement'], e['hash'])
      rescue Redmine::WikiFormatting::StaleSectionError
        {'error' => 'stale'}
      end
    else
      raise "unknown mode: #{e['mode']}"
    end
  e.merge('expected' => expected)
end

icon_path = ActionController::Base.helpers.asset_path('icons.svg')
File.write(out_path, JSON.pretty_generate({'icons_path' => icon_path, 'entries' => results}) + "\n")
STDERR.puts "wrote #{results.size} entries to #{out_path}"
