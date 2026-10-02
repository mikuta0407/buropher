# Redmine 6.1.2 の textilizable で差分テスト用コーパスの期待値を生成する。
#
#   ruby internal/textformat/redmine/testdata/gen/cases.rb /tmp/x/cases.json
#   ruby internal/textformat/redmine/testdata/gen/random_cases.rb /tmp/x/random.json                 # → corpus_random.json
#   ruby internal/textformat/redmine/testdata/gen/random_cases.rb /tmp/x/random2.json 7 1000 random2 # → corpus_random2.json
#   cp _reference/redmine-fixtures/db/redmine.pristine.sqlite3 <copy>
#   COMPAT_FROZEN_TIME='2026-01-15 12:00:00' bash internal/authz/testdata/gen/runner.sh <copy> \
#     internal/textformat/redmine/testdata/gen/dump.rb /tmp/x/cases.json internal/textformat/redmine/testdata/corpus.json
#
# cases.json は [{id, formatting, user, project, object: {type, id}, options: {...}, text}] の配列。
# 出力は各ケースに html（エラー時は error）を加えたもの。
require 'json'

in_path = File.expand_path(ARGV[0], ENV['ORIG_PWD'] || Dir.pwd)
out_path = File.expand_path(ARGV[1], ENV['ORIG_PWD'] || Dir.pwd)
cases = JSON.parse(File.read(in_path))

# collapse マクロの id を決定的にする（Go 側も同じ連番を使う）
$rh = 0
module Redmine
  module Utils
    def self.random_hex(n)
      $rh += 1
      format("%0#{n * 2}x", $rh)
    end
  end
end

Rails.logger.level = :warn
::I18n.locale = :en

def find_object(spec)
  return nil if spec.nil? || spec['type'].to_s.empty?

  id = spec['id']
  case spec['type']
  when 'issue' then Issue.find(id)
  when 'journal' then Journal.find(id)
  when 'wiki_content' then WikiPage.find(id).content
  when 'news' then News.find(id)
  when 'message' then Message.find(id)
  when 'document' then Document.find(id)
  when 'version' then Version.find(id)
  when 'project' then Project.find(id)
  else raise "unknown object #{spec['type']}"
  end
end

def sym_opts(h)
  o = {}
  (h || {}).each do |k, v|
    case k
    when 'wiki_links' then o[:wiki_links] = v.to_sym
    when 'edit_section_links'
      o[:edit_section_links] = {:controller => 'wiki', :action => 'edit', :project_id => v['project_id'], :id => v['id']}
    when 'attachments'
      o[:attachments] = Attachment.where(:id => v).to_a
    else o[k.to_sym] = v
    end
  end
  o
end

results = []
current_fmt = :none
cases.each do |c|
  $rh = 0
  fmt = c.key?('formatting') ? c['formatting'].to_s : 'textile'
  if fmt != current_fmt
    Setting.text_formatting = fmt
    current_fmt = fmt
  end
  user = c['user'].to_s.empty? || c['user'] == 'anonymous' ? User.anonymous : User.find_by_login(c['user'])
  User.current = user
  ::I18n.locale = :en
  controller = ApplicationController.new
  controller.request = ActionDispatch::TestRequest.create
  controller.response = ActionDispatch::TestResponse.new
  view = controller.view_context
  view.instance_variable_set(:@project, c['project'].to_s.empty? ? nil : Project.find(c['project']))
  r = c.dup
  begin
    obj = find_object(c['object'])
    opts = sym_opts(c['options'])
    opts[:object] = obj if obj
    r['html'] = view.textilizable(c['text'], opts).to_s
  rescue => e
    r['error'] = "#{e.class}: #{e.message}"
    r['html'] = nil
  end
  results << r
end
File.write(out_path, JSON.pretty_generate(results) + "\n")
puts "wrote #{results.size} cases to #{out_path}"
