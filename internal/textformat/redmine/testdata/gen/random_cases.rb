# textilizable 差分テスト用のランダムケース（固定シード）を生成する。
#
#   ruby internal/textformat/redmine/testdata/gen/random_cases.rb internal/textformat/redmine/testdata/gen/random_cases.json
#
# Redmine リンク・Wiki リンク・マクロ・書式の断片を無作為に連結し、正規表現の境界の差を探す。
require 'json'

out = ARGV[0] || abort('usage: random_cases.rb OUTPUT.json')
seed = (ARGV[1] || 20261003).to_i
count = (ARGV[2] || 400).to_i
prefix = ARGV[3] || 'random'
rng = Random.new(seed)

TOKENS = [
  '#1', '#2', '#3', '#4', '#6', '#14', '##1', '##3', '#1-1', '#3#note-2', '#note-1', 'r1', 'r2', 'r10', 'r99',
  'commit:691322a8eb01e11fd7', 'commit:bbf', 'source:/a/b.rb', 'source:a/b@2#L3', 'export:x.txt',
  'document#1', 'document:"Test document"', 'version#2', 'version:1.0', 'version:"2.0"', 'forum#1', 'forum:Help',
  'message#1', 'message#5', 'news#1', 'news:"eCookbook first release !"', 'project#1', 'project:onlinestore',
  'user#2', 'user:jsmith', '@jsmith', '@admin', '@dlopper', 'attachment:error281.txt', 'attachment:logo.gif',
  'ecookbook:#2', 'onlinestore:version:Alpha', 'ecookbook:document#1', '!#1', '!r1', '!@jsmith',
  '[[CookBook documentation]]', '[[Another page|text]]', '[[onlinestore:Start page]]', '[[#anchor]]', '[[New]]',
  '[[ecookbook:Child_1#x]]', '![[Wiki]]', '{{hello_world}}', '{{hello_world(a, b)}}', '{{issue(2)}}',
  '{{issue(3, subject=false)}}', '{{thumbnail(logo.gif)}}', '!{{hello_world}}', '{{macro_list}}', '{{unknown}}',
  'http://example.com/#1', 'www.redmine.org', 'jsmith@somenet.foo', '<b>x</b>', '&amp;', '&', '<', '"q"', "'s'",
  '*bold*', '_em_', '@code@', '`code`', '**strong**', '~~del~~', '"link":http://x.y/#3', '[md #1](http://x/)',
  '!logo.gif!', '![](logo.gif)', '<img src="logo.gif">', '(', ')', '[', ']', ',', '.', ';', ':', '-', '!', '?', '/',
  '<code>#1</code>', '<pre>r1</pre>', '%{color:red}#1%', 'é', '日本', 'ü#1', '#1é', '#1日',
  '<pre>', '</pre>', '<code>', '</code>', "{{collapse\n#1 [[Wiki]]\n}}", '{{include(Another page)}}', '{{child_pages}}',
  '{{recent_pages(days=9000, time=true)}}', "\nh2. Head #1\n\n", "\n## Head r1\n\n", '<a href="x">', '</a>',
  '&lt;', '&#35;1', '"', '!{{issue(1)}}', '[[', ']]', '{{', '}}', "```\n#1\n```", "<pre><code class=\"ruby\">\n#1\n</code></pre>"
].freeze
SEPS = [' ', ' ', ' ', '', "\n", "\n\n", ', ', ' - ', '(', ')', '[', ']', '>', '.'].freeze
HEADS = ['', '', '', "h1. Title\n\n", "# Title\n\n", "{{toc}}\n\nh2. A #1\n\n", "* ", "> ", "| ", "1. ", "    "].freeze
CTX = [
  ['anonymous', 'ecookbook', nil], ['jsmith', 'ecookbook', {'type' => 'issue', 'id' => 3}],
  ['admin', 'onlinestore', nil], ['dlopper', '', {'type' => 'journal', 'id' => 2}],
  ['rhill', 'ecookbook', {'type' => 'wiki_content', 'id' => 1}], ['admin', '', {'type' => 'news', 'id' => 1}],
].freeze

cases = []
count.times do |i|
  n = 2 + rng.rand(7)
  text = HEADS[rng.rand(HEADS.size)].dup
  n.times do |k|
    text << SEPS[rng.rand(SEPS.size)] if k > 0
    text << TOKENS[rng.rand(TOKENS.size)]
  end
  user, project, obj = CTX[rng.rand(CTX.size)]
  fmt = i.even? ? 'textile' : 'common_mark'
  c = {'id' => format("#{prefix}/%04d-%s", i, fmt == 'textile' ? 'tx' : 'md'), 'formatting' => fmt,
       'user' => user, 'project' => project}
  c['object'] = obj if obj
  c['text'] = text
  cases << c
end
File.write(out, JSON.pretty_generate(cases) + "\n")
puts "#{cases.size} cases"
