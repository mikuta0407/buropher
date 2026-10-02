# frozen_string_literal: true
#
# Textile フォーマッタの差分テスト用に、ランダムな Textile 断片を生成して
# 実際の Redmine の出力と組にして JSON で保存する。
#
#   cd /path/to/redmine && SECRET_KEY_BASE=x RAILS_ENV=production \
#     bin/rails runner /path/to/buropher/tools/gen-textile-fuzz.rb OUT.json [COUNT] [SEED]
#
# Go 側は TEXTILE_FUZZ_FILE=OUT.json go test ./internal/textformat/textile -run TestFuzzFile
# で比較する (フェイクハイライタは gen-textile-fixtures.rb と同じ)。

require 'json'
require 'timeout'

out = ARGV[0] or abort "usage: gen-textile-fuzz.rb OUT.json [COUNT] [SEED]"
count = (ARGV[1] || 5000).to_i
seed = (ARGV[2] || 1).to_i

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

TOKENS = [
  'foo', 'bar', 'Baz', 'ABC', 'NASA', 'x', '1', '42', '日本語', 'テスト', '中文', 'é', '😀',
  ' ', ' ', ' ', ' ', '  ', "\t", "\n", "\n", "\n\n", "\n\n", "\r\n", "\n ", "\n  ",
  '*', '**', '_', '__', '-', '--', '+', '^', '~', '%', '@', '==', '??', '!', '"', "'",
  '(', ')', '[', ']', '{', '}', '<', '>', '|', '#', '.', ':', '/', '\\', '&', ';', '?', '=', ',',
  'h1. ', 'h2(cls). ', 'h3{color:red}. ', 'p. ', 'p>. ', 'p(#id). ', 'bq. ', 'bq.:http://c.com ', 'fn1. ',
  "\n* ", "\n** ", "\n# ", "\n## ", "\n*(c) ", '* ', '# ',
  '|', '|_. ', '|\\2. ', '|/2. ', '|>. ', "|\n", 'table(t). ', "table.\n",
  '<pre>', '</pre>', '<code>', '</code>', '<code class="ruby">', "<code class='c'>", '<code class="foo">',
  '<notextile>', '</notextile>', '<kbd>', '</kbd>', '<b>', '</b>', '<br />', '<script>', '<!--', '-->',
  'http://ex.com/a', 'https://ex.com/p?q=1&r=2', 'www.ex.com', 'foo@bar.com', 'ftp://h/f',
  '!img.png!', '!>img.png!', '!(c)img.png(alt)!', '!img.png!:http://x.com',
  '"link":http://x.com', '"t (ti)":/p', '"a":http://x.com/(b)', '"j":javascript:x',
  '{color:red}', '(cls)', '(#id)', '[en]', '[1]', '[[Wiki]]', '[[W|t]]', '{{toc}}', '{{m(a)}}',
  'x%x%', '&amp;', '&#169;', '&', ':redsh#1:', '<redpre#0>', '> ', "\n> ", "\n>> ",
  '---', '***', 'version:"1.0"', 'user:a@b.com', '@a@b.com',
  '<PRE>', '<pre class="x">', '<code lang="r">', '<pre><code class="ruby">', '</code></pre>', "\f", "\v", "\u0001",
  "\u00a0", "\u3000", '²', 'Ⅻ', '_x_', '*y*', '-z-', '+w+', '%{color:blue}s%', '@c@', '@|ruby|c@',
  'GPL(General Public License)', 'AB(x "y")', 'a(b)', 'p{border:1px solid red}. ', 'p{invalid}. ', 'p<>(c#i)[fr]. ',
  "\n    ", "\n\n    code", '<a href="x">', '</a>', '<img src="a">', '<div>', '</div>', '<p>', '<span>', '<foo:bar>',
  '<!DOCTYPE x>', 'mailto:a@b.com', '"m":mailto:a@b.com', '!data:x!', '!http://h/i.png?a:b!', '[@a@b.com]',
  'attachment:i@2x.png', '[i@2x.png]', 'fn. ', 'fn12. ', 'h7. ', 'notextile. ', 'bc. ', '- - -',
].freeze

rng = Random.new(seed)
seen = {}
cases = []
while cases.size < count
  n = rng.rand(1..(ENV['FUZZ_MAXLEN'] || 25).to_i)
  s = Array.new(n) { TOKENS[rng.rand(TOKENS.size)] }.join
  next if seen[s]

  seen[s] = true
  html =
    begin
      Timeout.timeout(2) { Redmine::WikiFormatting::Textile::Formatter.new(s.dup).to_html }
    rescue Timeout::Error
      next
    rescue StandardError => e
      "!!ERROR #{e.class}"
    end
  cases << {'name' => "fuzz:#{cases.size}", 'input' => s, 'html' => html}
end

File.write(out, JSON.generate(cases))
puts "cases: #{cases.size}"
