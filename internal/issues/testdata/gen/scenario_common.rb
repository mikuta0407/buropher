# 差分シナリオ (dump_scenario*.rb) 共通の補助関数。
require 'json'

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


# IssuesController#bulk_update 相当
def bulk_update(ids, attrs, notes, opts)
  issues = Issue.where(:id => ids).to_a.sort
  attrs = attrs.reject {|k, v| v.blank?}
  attrs.each_key {|k| attrs[k] = '' if attrs[k] == 'none'}
  if opts[:copy] && opts[:copy_subtasks]
    issues.reject! {|issue| issues.detect {|other| issue.is_descendant_of?(other)}}
  end
  issues.each do |orig|
    orig.reload
    issue = if opts[:copy]
              orig.copy({}, :attachments => !!opts[:copy_attachments], :subtasks => !!opts[:copy_subtasks],
                        :watchers => !!opts[:copy_watchers], :link => !!opts[:link])
            else
              orig
            end
    issue.init_journal(User.current, notes)
    issue.safe_attributes = attrs
    ok = issue.save
    record('bulk', {'ok' => ok, 'id' => issue.id, 'errors' => errors_of(issue)})
  end
end

# IssuesController#destroy 相当
def destroy_issues(ids, todo, reassign_to_id, project_id)
  issues = Issue.where(:id => ids).to_a
  all_ids = Issue.self_and_descendants(issues).pluck(:id)
  time_entries = TimeEntry.where(:issue_id => all_ids)
  if time_entries.sum(:hours).to_f > 0
    case todo
    when 'nullify'
      time_entries.update_all(:issue_id => nil)
    when 'reassign'
      reassign_to = Project.find(project_id).issues.find_by_id(reassign_to_id)
      time_entries.update_all(:issue_id => reassign_to.id, :project_id => reassign_to.project_id)
    end
  end
  issues.each do |issue|
    begin
      issue.reload.destroy
    rescue ActiveRecord::RecordNotFound
    end
  end
  record('destroy', ids)
end

# JournalsController#update 相当
def edit_journal(id, notes)
  journal = Journal.find(id)
  journal.safe_attributes = {'notes' => notes, 'updated_by' => User.current}
  journal.save
  journal.destroy if journal.details.empty? && journal.notes.blank?
  record('edit_journal', {'id' => id, 'exists' => Journal.exists?(id)})
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

def write_dump(out_path)
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
     'private_notes' => j.private_notes, 'created_on' => t(j.created_on), 'updated_on' => t(j.updated_on), 'updated_by_id' => j.updated_by_id,
     'details' => j.details.order(:id).map {|d| [d.property, d.prop_key, d.old_value, d.value]}}
  end
  relations = IssueRelation.order(:id).map {|r| [r.id, r.issue_from_id, r.issue_to_id, r.relation_type, r.delay]}
  watchers = Watcher.where(:watchable_type => 'Issue').order(:watchable_id, :user_id).map {|w| [w.watchable_id, w.user_id]}
  time_entries = TimeEntry.order(:id).map {|t| [t.id, t.project_id, t.issue_id, t.hours.to_f]}
  custom_values = CustomValue.where(:customized_type => 'Issue').order(:customized_id, :custom_field_id, :id).map {|v| [v.customized_id, v.custom_field_id, blank_nil(v.value)]}

  File.write(out_path, JSON.pretty_generate({
    'log' => $log, 'deliveries' => $deliveries, 'issues' => issues, 'tree_order' => tree_order, 'journals' => journals,
    'relations' => relations, 'watchers' => watchers, 'custom_values' => custom_values, 'time_entries' => time_entries
  }))
  puts "wrote #{out_path}"
end
