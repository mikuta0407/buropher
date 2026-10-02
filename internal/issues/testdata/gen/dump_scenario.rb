# Redmine 6.1.2 (公式フィクスチャ投入済み DB) でチケット操作のシナリオ (作成・更新・ワークフロー・
# 親子・関連と再スケジュール・コピー・移動・クローズ・削除) を実行し、結果の DB 状態と通知を JSON に書き出す。
#
#   internal/issues/testdata/gen/runner.sh <DB のコピー> internal/issues/testdata/gen/dump_scenario.rb <出力 JSON>
#
# runner.sh は時刻を 2026-01-15 12:00 UTC に固定する。シナリオは実際にコミットする
# (after_commit コールバックを動かすため) ので、必ず DB のコピーに対して実行すること。
# Go 側 (internal/issues/differential_test.go) は同じ操作を issues パッケージの API で再現して比較する。
require 'json'

out_path = File.expand_path(ARGV[0] || abort('usage: dump_scenario.rb OUTPUT.json'), ENV['ORIG_PWD'] || Dir.pwd)

# 通知 (deliver_later) を記録する
$deliveries = []
ActionMailer::MessageDelivery.prepend(Module.new do
  def deliver_later(*)
    user, obj = @args
    $deliveries << {'event' => @action.to_s, 'object' => obj.class.name.underscore, 'object_id' => obj.id, 'user' => user.id}
    nil
  end
end)

$log = []

def as(uid)
  User.current = User.find(uid)
end

def step(name)
  $step = name
  $deliveries << {'step' => name}
  yield
end

def record(name, value)
  $log << {'step' => $step, 'name' => name, 'value' => value}
end

def errors_of(obj)
  obj.errors.details.map {|attr, ds| ds.map {|d| [attr.to_s, d[:error].is_a?(Symbol) ? d[:error].to_s : d[:error].to_s]}}.flatten(1)
end

# IssuesController#create 相当
def create_issue(project_id, attrs)
  issue = Issue.new
  issue.project = Project.find(project_id)
  issue.author ||= User.current
  issue.start_date ||= User.current.today if Setting.default_issue_start_date_to_creation_date?
  issue.safe_attributes = attrs
  issue.tracker ||= issue.allowed_target_trackers.first
  ok = issue.save
  record('create', {'ok' => ok, 'id' => issue.id, 'errors' => errors_of(issue)})
  issue
end

# IssuesController#update 相当
def update_issue(id, attrs, notes = nil)
  issue = Issue.find(id)
  issue.init_journal(User.current)
  attrs = attrs.merge('notes' => notes) if notes
  issue.safe_attributes = attrs
  ok = issue.save
  record('update', {'ok' => ok, 'id' => id, 'errors' => errors_of(issue)})
  issue
end

def add_relation(from_id, type, to_id, delay = nil)
  rel = IssueRelation.new(:issue_from => Issue.find(from_id))
  attrs = {'relation_type' => type, 'issue_to_id' => to_id.to_s}
  attrs['delay'] = delay.to_s if delay
  rel.safe_attributes = attrs
  rel.init_journals(User.current)
  ok = rel.save
  record('relation', {'ok' => ok, 'id' => rel.id, 'errors' => errors_of(rel)})
  rel
end

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

def t(v)
  v && v.utc.strftime('%Y-%m-%d %H:%M:%S')
end

def dt(v)
  v && v.strftime('%Y-%m-%d')
end

def blank_nil(v)
  v.nil? || v == '' ? nil : v
end

issues = Issue.order(:id).map do |i|
  {'id' => i.id, 'project_id' => i.project_id, 'tracker_id' => i.tracker_id, 'status_id' => i.status_id,
   'priority_id' => i.priority_id, 'author_id' => i.author_id, 'assigned_to_id' => i.assigned_to_id,
   'category_id' => i.category_id, 'fixed_version_id' => i.fixed_version_id, 'parent_id' => i.parent_id,
   'root_id' => i.root_id, 'subject' => i.subject, 'description' => blank_nil(i.description),
   'start_date' => dt(i.start_date), 'due_date' => dt(i.due_date), 'done_ratio' => i.read_attribute(:done_ratio),
   'estimated_hours' => i.estimated_hours, 'is_private' => i.is_private, 'lock_version' => i.lock_version,
   'created_on' => t(i.created_on), 'updated_on' => t(i.updated_on), 'closed_on' => t(i.closed_on)}
end
tree_order = Issue.order(:root_id, :lft).pluck(:id)
journals = Journal.order(:id).map do |j|
  {'id' => j.id, 'issue_id' => j.journalized_id, 'user_id' => j.user_id, 'notes' => blank_nil(j.notes),
   'private_notes' => j.private_notes, 'created_on' => t(j.created_on),
   'details' => j.details.order(:id).map {|d| [d.property, d.prop_key, d.old_value, d.value]}}
end
relations = IssueRelation.order(:id).map {|r| [r.id, r.issue_from_id, r.issue_to_id, r.relation_type, r.delay]}
watchers = Watcher.where(:watchable_type => 'Issue').order(:watchable_id, :user_id).map {|w| [w.watchable_id, w.user_id]}
custom_values = CustomValue.where(:customized_type => 'Issue').order(:customized_id, :custom_field_id, :id).map {|v| [v.customized_id, v.custom_field_id, blank_nil(v.value)]}

File.write(out_path, JSON.pretty_generate({
  'log' => $log, 'deliveries' => $deliveries, 'issues' => issues, 'tree_order' => tree_order, 'journals' => journals,
  'relations' => relations, 'watchers' => watchers, 'custom_values' => custom_values
}))
puts "wrote #{out_path}"
