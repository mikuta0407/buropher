# 差分シナリオ 2: 複数値カスタムフィールド、ワークフローの必須・読み取り専用、メンション、子の進捗率の導出、
# 一括更新・一括コピー、再オープン、工数の付け替えを伴う削除、ノートの編集、ブロックによる遷移制限、
# 子を伴うプロジェクト移動、バージョン共有の変更。
#
#   internal/issues/testdata/gen/runner.sh <DB のコピー> internal/issues/testdata/gen/dump_scenario2.rb <出力 JSON>
#
# dump_scenario.rb の関数 (as / step / record / create_issue / update_issue / add_relation / ダンプ) を共有する。
load File.join(File.dirname(__FILE__), 'scenario_common.rb')

out_path = File.expand_path(ARGV[0] || abort('usage: dump_scenario2.rb OUTPUT.json'), ENV['ORIG_PWD'] || Dir.pwd)

g = h = i = j = k = l = m = nil
journal_id = nil

step('t1 setup') do
  cf = IssueCustomField.create!(:name => 'Multi', :field_format => 'list', :multiple => true,
                                :possible_values => %w(A B C), :is_for_all => true, :tracker_ids => Tracker.ids)
  record('cf', cf.id)
  WorkflowPermission.create!(:tracker_id => 1, :old_status_id => 1, :role_id => 2, :field_name => 'due_date', :rule => 'required')
  WorkflowPermission.create!(:tracker_id => 1, :old_status_id => 1, :role_id => 2, :field_name => 'priority_id', :rule => 'readonly')
end
cf_id = $log.last['value']

step('t2 create with workflow rules') do
  as 3
  create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff G missing due'})
  g = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff G', 'due_date' => '2026-02-20', 'priority_id' => '7',
                       'is_private' => '1', 'custom_field_values' => {cf_id.to_s => %w(A B)}})
end

step('t3 multi value change and mention') do
  as 3
  update_issue(g.id, {'custom_field_values' => {cf_id.to_s => %w(B C)}}, "@jsmith please check\n\n```\n@admin\n```")
  journal_id = Journal.where(:journalized_id => g.id).maximum(:id)
end

step('t4 children and derived done ratio') do
  as 2
  h = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff H', 'parent_issue_id' => g.id.to_s, 'estimated_hours' => '2',
                       'done_ratio' => '40'})
  i = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff I', 'parent_issue_id' => g.id.to_s, 'estimated_hours' => '6',
                       'status_id' => '5'})
  update_issue(i.id, {'status_id' => '5'}, 'closing child')
end

step('t5 bulk update') do
  as 1
  bulk_update([g.id, 2], {'assigned_to_id' => '3', 'fixed_version_id' => '3', 'start_date' => 'none'}, 'bulk', {})
end

step('t6 bulk copy with subtasks') do
  as 1
  bulk_update([g.id], {'project_id' => '1'}, 'bulk copy', {:copy => true, :copy_subtasks => true, :link => true})
end

step('t7 reopen child') do
  as 1
  update_issue(i.id, {'status_id' => '1'}, 'reopen')
end

step('t8 destroy with time entry reassign') do
  as 1
  TimeEntry.create!(:project_id => 1, :issue_id => h.id, :user_id => 2, :author_id => 2, :hours => 1.5, :activity_id => 9,
                    :spent_on => Date.today)
  destroy_issues([g.id], 'reassign', 1, 1)
end

step('t9 edit journal notes') do
  as 1
  edit_journal(1, 'Edited note')
  edit_journal(2, '')
end

step('t10 blocked issue cannot be closed') do
  as 1
  j = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff J'})
  k = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff K'})
  add_relation(j.id, 'blocks', k.id)
  kk = Issue.find(k.id)
  record('allowed', kk.new_statuses_allowed_to(User.current).map(&:id))
  update_issue(k.id, {'status_id' => '5'}, 'try close')
end

step('t11 move with subtasks') do
  as 1
  l = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff L', 'category_id' => '1', 'fixed_version_id' => '3'})
  m = create_issue(1, {'tracker_id' => '1', 'subject' => 'Diff M', 'parent_issue_id' => l.id.to_s, 'category_id' => '2'})
  update_issue(l.id, {'project_id' => '3'})
end

step('t12 version sharing change') do
  as 1
  v = Version.find(3)
  v.update!(:sharing => 'descendants')
  update_issue(m.id, {'fixed_version_id' => '3'})
  Version.find(3).update!(:sharing => 'none')
end

write_dump(out_path)
