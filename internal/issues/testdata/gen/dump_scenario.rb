# Redmine (公式フィクスチャ投入済み DB。現在の正解データは 7.0.1) でチケット操作のシナリオ (作成・更新・ワークフロー・
# 親子・関連と再スケジュール・コピー・移動・クローズ・削除) を実行し、結果の DB 状態と通知を JSON に書き出す。
#
#   internal/issues/testdata/gen/runner.sh <DB のコピー> internal/issues/testdata/gen/dump_scenario.rb <出力 JSON>
#
# runner.sh は時刻を 2026-01-15 12:00 UTC に固定する。シナリオは実際にコミットする
# (after_commit コールバックを動かすため) ので、必ず DB のコピーに対して実行すること。
# Go 側 (internal/issues/differential_test.go) は同じ操作を issues パッケージの API で再現して比較する。
out_path = File.expand_path(ARGV[0] || abort('usage: dump_scenario.rb OUTPUT.json'), ENV['ORIG_PWD'] || Dir.pwd)

load File.join(File.dirname(__FILE__), 'scenario_common.rb')

a = b = c = d = e = copy = nil

step('s1 create A') do
  as 2
  a = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff A', 'description' => "line1\nline2 @dlopper",
                       'priority_id' => '5', 'assigned_to_id' => '3', 'category_id' => '1', 'fixed_version_id' => '3',
                       'start_date' => '2026-01-19', 'due_date' => '2026-01-23', 'estimated_hours' => '2h30',
                       'custom_field_values' => {'2' => 'value1', '1' => 'PostgreSQL'}, 'watcher_user_ids' => ['3', '8']})
end

step('s2 create B') do
  as 2
  b = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff B', 'start_date' => '2026-01-20', 'due_date' => '2026-01-21'})
end

step('s3 create child C') do
  as 2
  c = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff C', 'parent_issue_id' => a.id.to_s, 'start_date' => '2026-01-26',
                       'due_date' => '2026-01-28', 'estimated_hours' => '4', 'done_ratio' => '50', 'priority_id' => '6'})
end

step('s4 relation A precedes B') do
  as 2
  add_relation(a.id, 'precedes', b.id, 2)
end

step('s5 workflow status change by assignee') do
  as 3
  update_issue(a.id, {'status_id' => '2'}, 'Status change note')
end

step('s6 update child C (reschedule B through parent)') do
  as 2
  update_issue(c.id, {'due_date' => '2026-01-30', 'done_ratio' => '80', 'custom_field_values' => {'2' => 'changed'}})
end

step('s7 private note with changes on B') do
  as 2
  update_issue(b.id, {'private_notes' => '1', 'done_ratio' => '20'}, 'private note')
end

step('s8 copy A with subtasks') do
  as 1
  copy = Issue.new
  copy.init_journal(User.current)
  src = Issue.visible.find(a.id)
  copy.copy_from(src, :attachments => true, :subtasks => true, :watchers => true, :link => true)
  copy.parent_issue_id = src.parent_id
  copy.project = Project.find(1)
  copy.author ||= User.current
  copy.safe_attributes = {'subject' => 'Diff A copy', 'status_id' => '1'}
  ok = copy.save
  record('copy', {'ok' => ok, 'id' => copy.id, 'errors' => errors_of(copy)})
end

step('s9 move B to project 2') do
  as 1
  update_issue(b.id, {'project_id' => '2'})
end

step('s10 close duplicated issue') do
  as 2
  e = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff E'})
  d = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff D (duplicate)'})
  add_relation(d.id, 'duplicates', e.id)
  as 1
  update_issue(e.id, {'status_id' => '5'}, 'closing E')
end

step('s11 remove parent of C') do
  as 2
  update_issue(c.id, {'parent_issue_id' => ''})
end

step('s12 invalid updates') do
  as 2
  update_issue(a.id, {'start_date' => '2026-02-10', 'due_date' => '2026-02-01'})
  update_issue(c.id, {'parent_issue_id' => c.id.to_s})
  add_relation(b.id, 'follows', a.id)
end

step('s13 destroy copy') do
  as 1
  Issue.where(:id => copy.id).each {|i| i.reload.destroy}
end

write_dump(out_path)
