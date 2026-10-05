# frozen_string_literal: true
#
# Redmine::SyntaxHighlighting（Rouge）の出力を正解データとして生成し、
# internal/textformat/highlight のテスト（rouge_fixtures_test.go）で使う。
#
# 使い方（Redmine の作業ツリーで rails runner 経由で実行する。DB は参照しない）:
#   cd /path/to/redmine && SECRET_KEY_BASE=x RAILS_ENV=production \
#     bin/rails runner /path/to/buropher/tools/gen-rouge-fixtures.rb \
#     $H/testdata/rouge.json \
#     $B/internal/textformat/commonmark/testdata/corpus_highlight.json \
#     $H/testdata/corpus_extra.json \
#     --samples /path/to/rouge/spec/visual/samples
#
#   （B=/path/to/buropher、H=$B/internal/textformat/highlight）
#
# 入力:
#   *.json     : {"name", "mode", "lang" | "filename", "input"} の配列
#                （corpus_highlight.json は tools/textformat/mkcorpus.py で Rouge のデモから生成）
#   --samples  : Rouge のソースツリー（gem には含まれない）の spec/visual/samples。
#                Go に移植済みのレキサー（PORTED）の分だけ highlight を作る。
#
# 出力は {"rouge_version", "entries": [... "expected" を加えたもの]}。mode は
#   highlight      : highlight_by_language(input, lang)
#   highlight_file : highlight_by_filename(input, filename)
#   supported      : language_supported?(lang)（全レキサーのタグと別名、および例外的な名前）
#   filename       : filename_supported?(filename)

require 'json'

# Go に移植済みのレキサー（internal/textformat/highlight の registerRouge）。
# 移植済みのものはテストで完全一致を求める。
PORTED = %w[
  apache batchfile c conf console cpp csharp css diff docker erb go groovy html ini java
  javascript json jsx kotlin lua make markdown nginx objective_c pascal perl php plaintext
  powershell properties python r ruby rust scala scss shell sql swift toml tsx typescript vb
  xml yaml
].freeze

out_path = ARGV.shift or abort 'usage: gen-rouge-fixtures.rb OUT.json corpus.json... [--samples DIR]'
samples_dir = nil
corpus_paths = []
while (a = ARGV.shift)
  if a == '--samples'
    samples_dir = ARGV.shift
  else
    corpus_paths << a
  end
end

entries = []
corpus_paths.each { |p| entries.concat(JSON.parse(File.read(p))) }

if samples_dir
  PORTED.each do |tag|
    path = File.join(samples_dir, tag)
    next unless File.file?(path)

    src = File.read(path, mode: 'rb').force_encoding('UTF-8')
    next unless src.valid_encoding?

    # highlight_file（行ごとの分割）はデモで確かめるため、サンプルは highlight のみ
    entries << {'name' => "rouge-sample-#{tag}", 'mode' => 'highlight', 'lang' => tag, 'input' => src}
  end
end

names = Rouge::Lexer.all.flat_map { |l| [l.tag, *l.aliases] }.uniq.sort
names += %w[delphi cplusplus ecmascript ecma_script java_script xhtml text Ruby C++ foobar c-k&r mermaid plantuml]
names << ''
names.each do |n|
  entries << {'name' => "supported-#{n}", 'mode' => 'supported', 'lang' => n}
end

fnames = Rouge::Lexer.all.flat_map { |l| l.filenames.to_a }.uniq.sort.map { |g| g.tr('*?', 'xy').sub(/\[(.).*?\]/, '\1') }
fnames += %w[README foo.unknownext Makefile a.h .htaccess x.asm x.mml x.tm x.pdf]
fnames.uniq.each do |f|
  entries << {'name' => "filename-#{f}", 'mode' => 'filename', 'filename' => f}
end

hl = Redmine::SyntaxHighlighting
entries.each do |e|
  e['expected'] =
    case e['mode']
    when 'highlight' then hl.highlight_by_language(e['input'], e['lang'])
    when 'highlight_file' then hl.highlight_by_filename(e['input'], e['filename'])
    when 'supported' then hl.language_supported?(e['lang'])
    when 'filename' then hl.filename_supported?(e['filename'])
    else abort "unknown mode: #{e['mode']}"
    end
end

File.write(out_path, JSON.pretty_generate({'rouge_version' => Rouge.version, 'entries' => entries}) + "\n")
warn "wrote #{entries.size} entries (Rouge #{Rouge.version})"
