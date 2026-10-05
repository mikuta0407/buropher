# Redmine 7.0 の WebhookPayload（app/models/webhook_payload.rb）を公式フィクスチャの DB で計算し、JSON で出力する。
# internal/server/testdata/webhook_payloads.json の生成に使う（webhook_payload_test.go が比較する）。
# 使い方: cd <redmine 7.0.x> && DATABASE_URL=sqlite3:<フィクスチャ DB のコピー> SECRET_KEY_BASE=x \
#   RAILS_ENV=production bin/rails runner <this file> > internal/server/testdata/webhook_payloads.json
# DB は変更しない（削除イベントも削除せずにペイロードだけ計算する。deleted の timestamp は Time.now のため除く）。
require 'json'

dlopper = User.find_by_login('dlopper')
admin = User.find_by_login('admin')
jsmith = User.find_by_login('jsmith')

cases = []
add = lambda do |name, event, obj, user, journal: nil|
  obj.instance_variable_set(:@current_journal, journal) if journal
  h = WebhookPayload.new(event, obj, user).to_h
  h = JSON.parse(h.to_json)
  h['timestamp'] = nil if event.end_with?('.deleted') || event == 'news.updated'
  cases << { name: name, event: event, id: obj.id, user: user.login, journal_id: journal&.id, payload: h }
end

%w(created updated deleted).each do |action|
  add.call("issue_1_#{action}_dlopper", "issue.#{action}", Issue.find(1), dlopper)
  add.call("news_1_#{action}_dlopper", "news.#{action}", News.find(1), dlopper)
  add.call("time_entry_1_#{action}_dlopper", "time_entry.#{action}", TimeEntry.find(1), dlopper)
  add.call("version_1_#{action}_dlopper", "version.#{action}", Version.find(1), dlopper)
  add.call("wiki_page_1_#{action}_dlopper", "wiki_page.#{action}", WikiPage.find(1), dlopper)
end
add.call('issue_1_updated_journal_1_dlopper', 'issue.updated', Issue.find(1), dlopper, journal: Journal.find(1))
add.call('issue_1_updated_journal_2_admin', 'issue.updated', Issue.find(1), admin, journal: Journal.find(2))
add.call('issue_6_updated_journal_4_jsmith', 'issue.updated', Issue.find(6), jsmith, journal: Journal.find(4))
add.call('issue_3_created_admin', 'issue.created', Issue.find(3), admin)
add.call('issue_14_created_jsmith', 'issue.created', Issue.find(14), jsmith)
add.call('version_2_created_admin', 'version.created', Version.find(2), admin)
add.call('wiki_page_2_updated_jsmith', 'wiki_page.updated', WikiPage.find(2), jsmith)
add.call('time_entry_2_created_admin', 'time_entry.created', TimeEntry.find(2), admin)
add.call('news_2_created_admin', 'news.created', News.find(2), admin)
puts JSON.pretty_generate(cases)
