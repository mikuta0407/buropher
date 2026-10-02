# frozen_string_literal: true
#
# roadie（Redmine の Mailer が HTML メールに適用する CSS インライン化）の出力を期待値として生成する。
# usage (Redmine ツリーで): bundle exec ruby gen_roadie.rb <testdata dir>
require 'json'
require 'roadie'

dir = ARGV[0]
head = File.read(File.join(dir, 'layout_head.html'))
cases = JSON.parse(File.read(File.join(dir, 'roadie_inputs.json')))
out = cases.map do |c|
  html = head + "\n<body>\n" + c['body'] + "</body>\n</html>\n"
  doc = Roadie::Document.new(html)
  doc.url_options = {host: 'localhost', port: 3000, protocol: 'http'}
  {'name' => c['name'], 'body' => c['body'], 'expected' => doc.transform}
end
File.write(File.join(dir, 'roadie_golden.json'), JSON.pretty_generate(out) + "\n")
