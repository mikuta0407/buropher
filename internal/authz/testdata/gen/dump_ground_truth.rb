# Redmine 6.1.2 (公式テストフィクスチャ投入済み DB) から権限判定の正解データを JSON に書き出す。
#
#   internal/authz/testdata/gen/runner.sh <DB のコピー> internal/authz/testdata/gen/dump_ground_truth.rb <出力 JSON>
#
# 各シナリオはトランザクション内でデータを変更し、判定結果を記録してからロールバックする
# (DB は変更されないが、念のためコピーに対して実行すること)。
# Go 側 (internal/authz/differential_test.go) は同名のシナリオを repository の書き込み操作で再現し、
# 全 (ユーザ, プロジェクト, 権限) の組み合わせで結果を比較する。
require 'json'

out_path = File.expand_path(ARGV[0] || abort('usage: dump_ground_truth.rb OUTPUT.json'), ENV['ORIG_PWD'] || Dir.pwd)

PERMS = Redmine::AccessControl.permissions.map(&:name)
ACTIONS = [
  %w(issues index), %w(issues new), %w(issues show), %w(wiki show), %w(projects settings),
  %w(members create), %w(news index), %w(timelog index), %w(boards show), %w(projects show),
  %w(repositories show), %w(versions index)
]

def scenario_mutations
  {
    'base' => -> {},
    'closed_project1' => -> { Project.find(1).close },
    'archived_project5' => -> { raise 'archive failed' unless Project.find(5).archive },
    'archived_project1' => -> { raise 'archive failed' unless Project.find(1).archive },
    'reopen_after_close' => -> {
      Project.find(1).close
      Project.find(5).reopen
    },
    'builtin_group_overrides' => -> {
      Member.create!(:project => Project.find(1), :principal => Group.non_member, :role_ids => [1, 2])
      Member.create!(:project => Project.find(2), :principal => Group.anonymous, :role_ids => [2])
      Member.create!(:project => Project.find(3), :principal => Group.anonymous, :role_ids => [3])
      Member.create!(:project => Project.find(4), :principal => Group.non_member, :role_ids => [3])
    },
    'role_settings' => -> {
      r = Role.find(2)
      r.issues_visibility = 'own'
      r.set_permission_trackers :view_issues, [2]
      r.save!
      r = Role.find(1)
      r.users_visibility = 'members_of_visible_projects'
      r.set_permission_trackers :view_issues, []
      r.save!
      r = Role.find(4)
      r.users_visibility = 'members_of_visible_projects'
      r.issues_visibility = 'own'
      r.save!
      Role.find(5).remove_permission!(:view_issues)
      r = Role.find(3)
      r.issues_visibility = 'all'
      r.set_permission_trackers :view_issues, [1, 3]
      r.save!
    },
    'group_membership_changes' => -> {
      Group.find(10).users << User.find(7)
      Group.find(11).users.delete(User.find(8))
      Group.find(11).users << User.find(4)
      Member.create!(:project => Project.find(1), :principal => Group.find(11), :role_ids => [3])
    },
    'inherit_members' => -> {
      p = Project.find(3); p.inherit_members = true; p.save!
      p = Project.find(6); p.inherit_members = true; p.save!
      Member.create!(:project => Project.find(1), :principal => User.find(4), :role_ids => [2])
      Member.create!(:project => Project.find(5), :principal => User.find(7), :role_ids => [3])
    },
    'inherit_members_off' => -> {
      p = Project.find(3); p.inherit_members = true; p.save!
      p = Project.find(3); p.inherit_members = false; p.save!
      p = Project.find(4); p.inherit_members = true; p.save!
    },
    'move_project' => -> {
      p = Project.find(4); p.inherit_members = true; p.save!
      Project.find(4).set_parent!(2) or raise 'move failed'
      Project.find(3).set_parent!(nil) or raise 'move failed'
      p = Project.find(6); p.inherit_members = true; p.save!
      Project.find(6).set_parent!(4) or raise 'move failed'
    },
    'member_role_updates' => -> {
      m = Member.find(1); m.role_ids = [2, 3]; m.save!
      m = Member.find(6); m.role_ids = [2]; m.save!
      Member.find(9).destroy
      m = Member.find(7); m.role_ids = [3]; m.save!
    },
    'public_private' => -> {
      Project.find(1).update_attribute :is_public, false
      Project.find(2).update_attribute :is_public, true
    },
    'modules' => -> {
      p = Project.find(1); p.enabled_module_names = ['issue_tracking']; p.save!
      p = Project.find(2); p.enable_module!(:wiki)
      p = Project.find(5); p.disable_module!(:issue_tracking)
    },
    'create_projects' => -> {
      p = Project.new(:name => 'New child', :identifier => 'new-child', :is_public => false, :inherit_members => true)
      p.enabled_module_names = %w(issue_tracking wiki)
      p.parent = Project.find(5)
      p.save!
      q = Project.new(:name => 'Another', :identifier => 'another', :is_public => true)
      q.enabled_module_names = %w(issue_tracking)
      q.parent = Project.find(1)
      q.save!
      Member.create!(:project => q, :principal => User.find(7), :role_ids => [1])
    },
    'destroy_group' => -> { Group.find(10).destroy },
    'private_issues' => -> { private_issues! },
    'private_issues_own' => -> {
      private_issues!
      Role.find(1).update!(:issues_visibility => 'own')
      Role.find(2).update!(:issues_visibility => 'own')
      Role.find(4).update!(:issues_visibility => 'own')
    },
  }
end

# 非公開チケットとグループ担当のチケットを作る (Go 側は UPDATE 文で同じ変更を行う)
def private_issues!
  Issue.where(:id => 1).update_all(:is_private => true, :assigned_to_id => 10)
  Issue.where(:id => 4).update_all(:is_private => true, :assigned_to_id => 11)
  Issue.where(:id => 2).update_all(:is_private => true)
  Issue.where(:id => 6).update_all(:is_private => true, :author_id => 8)
  Issue.where(:id => 7).update_all(:assigned_to_id => 10)
end

def dump_state
  users = User.where(:type => %w(User AnonymousUser)).order(:id).to_a.map {|u| User.find(u.id)}
  projects = Project.order(:id).to_a
  issues = Issue.order(:id).to_a
  res = {}
  res['project_status'] = projects.to_h {|p| [p.id.to_s, p.status]}
  res['project_tree'] = Project.order(:lft).pluck(:id)
  members = []
  Member.includes(:member_roles).order(:id).each do |m|
    m.member_roles.each do |mr|
      src = nil
      if mr.inherited_from
        smr = MemberRole.find_by(:id => mr.inherited_from)
        src = smr ? [smr.member.project_id, smr.member.user_id, smr.role_id] : ['missing']
      end
      members << [m.project_id, m.user_id, mr.role_id, src]
    end
  end
  res['members'] = members.sort_by(&:to_s)
  res['users'] = {}
  users.each do |u0|
    u = User.find(u0.id)
    h = {}
    h['allowed'] = {}
    h['actions'] = {}
    h['roles_for_project'] = {}
    h['managed_roles'] = {}
    h['view_all_time_entries'] = {}
    projects.each do |p0|
      p = Project.find(p0.id)
      h['allowed'][p.id.to_s] = PERMS.map {|perm| u.allowed_to?(perm, p) ? '1' : '0'}.join
      h['actions'][p.id.to_s] = ACTIONS.map {|c, a| u.allowed_to?({:controller => c, :action => a}, p) ? '1' : '0'}.join
      h['roles_for_project'][p.id.to_s] = u.roles_for_project(p).map(&:id).sort
      h['managed_roles'][p.id.to_s] = u.managed_roles(p).map(&:id).sort
      h['view_all_time_entries'][p.id.to_s] = u.allowed_to_view_all_time_entries?(p)
    end
    h['allowed_all_projects'] = PERMS.map {|perm| u.allowed_to?(perm, Project.order(:id).to_a) ? '1' : '0'}.join
    h['global'] = PERMS.map {|perm| u.allowed_to?(perm, nil, :global => true) ? '1' : '0'}.join
    h['allowed_projects'] = PERMS.to_h {|perm| [perm.to_s, Project.allowed_to(u, perm).pluck(:id).sort]}
    h['allowed_projects_member'] = %w(view_issues add_issues view_project manage_members).to_h {|perm|
      [perm, Project.allowed_to(u, perm.to_sym, :member => true).pluck(:id).sort]
    }
    h['visible_issues'] = Issue.visible(u).pluck(:id).sort
    h['visible_issues_project1_sub'] = Issue.visible(u, :project => Project.find(1), :with_subprojects => true).pluck(:id).sort
    h['visible_issues_project1'] = Issue.visible(u, :project => Project.find(1)).pluck(:id).sort
    h['issue_visible'] = issues.map {|i| Issue.find(i.id).visible?(u) ? '1' : '0'}.join
    h['visible_principals'] = Principal.visible(u).pluck(:id).sort
    h['visible_project_ids'] = u.visible_project_ids.sort
    h['roles'] = u.roles.map(&:id).sort
    h['project_ids_by_role'] = u.project_ids_by_role.to_h {|r, ids| [r.id.to_s, ids.sort]}
    res['users'][u.id.to_s] = h
  end
  res
end

result = {
  'permissions' => PERMS.map(&:to_s),
  'actions' => ACTIONS.map {|c, a| "#{c}/#{a}"},
  'scenarios' => {}
}

scenario_mutations.each do |name, mutate|
  ActiveRecord::Base.transaction do
    mutate.call
    result['scenarios'][name] = dump_state
    raise ActiveRecord::Rollback
  end
  $stderr.puts "dumped #{name}"
end

File.write(out_path, JSON.generate(result) + "\n")
