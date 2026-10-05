# Redmine 7.0.1 (公式フィクスチャ投入済み DB, 時刻固定) で、保存クエリの可視性・編集可否、
# build_from_params、既定クエリの解決、IssueQuery#issues の preload 値、available_filters_as_json を
# 評価して JSON に書き出す。
#
#   internal/query/testdata/gen/runner.sh <DB のコピー> internal/query/testdata/gen/dump_misc.rb <出力 .json.gz>
#
# Go 側は internal/query/misc_differential_test.go。データ変更はトランザクション内で行いロールバックする。
require 'json'
require 'zlib'

out_path = File.expand_path(ARGV[0] || abort('usage: dump_misc.rb OUTPUT.json.gz'), ENV['ORIG_PWD'] || Dir.pwd)

USERS = [1, 2, 3, 4, 7, 8, :anon]
def user_of(u)
  u == :anon ? User.anonymous : User.find(u)
end
def uid(u)
  u == :anon ? 'anon' : u.to_s
end

def sql(s)
  ActiveRecord::Base.connection.execute(s)
end

result = {}

ActiveRecord::Base.transaction do
  # ロール限定クエリ・プロジェクト既定クエリ・個人設定の既定クエリを追加する (Go 側の applyMiscScenario と同じ)
  sql("INSERT INTO queries (id, project_id, name, filters, user_id, column_names, sort_criteria, group_by, type, visibility, options) " \
      "VALUES (20, 1, 'Roles query', #{ActiveRecord::Base.connection.quote({'status_id' => {:operator => 'o', :values => ['']}}.to_yaml)}, 2, NULL, NULL, NULL, 'IssueQuery', 1, #{ActiveRecord::Base.connection.quote({}.to_yaml)})")
  sql("INSERT INTO queries_roles (query_id, role_id) VALUES (20, 2)")
  sql("INSERT INTO queries (id, project_id, name, filters, user_id, column_names, sort_criteria, group_by, type, visibility, options) " \
      "VALUES (21, NULL, 'Global roles query', #{ActiveRecord::Base.connection.quote({'tracker_id' => {:operator => '=', :values => ['1']}}.to_yaml)}, 1, NULL, NULL, NULL, 'IssueQuery', 1, #{ActiveRecord::Base.connection.quote({}.to_yaml)})")
  sql("INSERT INTO queries_roles (query_id, role_id) VALUES (21, 1)")
  sql("INSERT INTO queries (id, project_id, name, filters, user_id, column_names, sort_criteria, group_by, type, visibility, options) " \
      "VALUES (22, 5, 'Private project query', #{ActiveRecord::Base.connection.quote({}.to_yaml)}, 1, NULL, NULL, NULL, 'IssueQuery', 2, #{ActiveRecord::Base.connection.quote({}.to_yaml)})")

  # 1. 可視性と編集可否
  vis = {}
  USERS.each do |u|
    user = user_of(u)
    User.current = user
    v = {}
    %w(IssueQuery TimeEntryQuery ProjectQuery ProjectAdminQuery UserQuery).each do |k|
      klass = k.constantize
      v[k] = klass.visible(user).pluck(:id).sort
    end
    v['visible?'] = Query.all.sort_by(&:id).map {|q| [q.id, q.visible?(user)]}
    v['editable_by?'] = Query.all.sort_by(&:id).map {|q| [q.id, q.editable_by?(user)]}
    v['global_or_on_project_1'] = IssueQuery.visible(user).global_or_on_project(Project.find(1)).sorted.pluck(:id)
    vis[uid(u)] = v
  end
  result['visibility'] = vis

  # 2. build_from_params
  params_cases = [
    {},
    {'set_filter' => '1'},
    {'set_filter' => '1', 'f' => ['status_id', 'tracker_id', ''], 'op' => {'status_id' => 'c', 'tracker_id' => '='}, 'v' => {'tracker_id' => ['1', '2']}},
    {'set_filter' => '1', 'fields' => ['status_id'], 'operators' => {'status_id' => '*'}, 'values' => {'status_id' => ['']}},
    {'set_filter' => '1', 'f' => ['assigned_to_id'], 'op' => {'assigned_to_id' => '='}, 'v' => {'assigned_to_id' => ['me']}},
    {'set_filter' => '1', 'f' => ['priority_id'], 'op' => {'priority_id' => '='}, 'v' => {'priority_id' => '4'}},
    {'set_filter' => '1', 'f' => ['subject'], 'op' => {}, 'v' => {'subject' => ['x']}},
    {'status_id' => 'o'},
    {'status_id' => 'c', 'tracker_id' => '1|2'},
    {'status_id' => '*', 'created_on' => '>=2026-01-10'},
    {'status_id' => '*', 'created_on' => '><2026-01-10|2026-01-13'},
    {'status_id' => '*', 'subject' => '~print'},
    {'status_id' => '*', 'subject' => 'Cannot print'},
    {'status_id' => '*', 'assigned_to_id' => '!*'},
    {'status_id' => '*', 'assigned_to_id' => 'me'},
    {'status_id' => '*', 'due_date' => '<t+5'},
    {'status_id' => '*', 'done_ratio' => '>=30'},
    {'status_id' => '*', 'estimated_hours' => '><0.5|1'},
    {'status_id' => '*', 'cf_1' => 'MySQL'},
    {'status_id' => '*', 'cf_2' => '!125'},
    {'status_id' => '*', 'issue_id' => '1,2,3'},
    {'status_id' => '*', 'parent_id' => '!*'},
    {'set_filter' => '1', 'c' => ['tracker', 'subject', 'cf_1', 'spent_hours', 'unknown'], 'sort' => 'priority:desc,tracker', 'group_by' => 'status', 't' => ['estimated_hours', '']},
    {'set_filter' => '1', 'c' => ['all_inline', 'description'], 'sort' => 'id,subject:desc,tracker,priority'},
    {'set_filter' => '1', 'c' => ['tracker', 'status', 'priority', 'subject', 'assigned_to', 'updated_on']},
    {'set_filter' => '1', 'query' => {'group_by' => 'tracker', 'column_names' => ['subject'], 'totalable_names' => ['spent_hours'], 'sort_criteria' => [['subject', 'desc']]}},
    {'set_filter' => '1', 'group_by' => '', 'query' => {'group_by' => 'tracker'}},
    {'set_filter' => '1', 'draw_relations' => '0', 'draw_progress_line' => '1'},
    {'set_filter' => '1', 'query' => {'draw_relations' => '1', 'draw_progress_line' => '0'}},
    {'set_filter' => '1', 'display_type' => 'board'},
  ]
  bfp = []
  [[2, nil], [2, 1], [:anon, nil]].each do |u, p|
    user = user_of(u)
    User.current = user
    project = p && Project.find(p)
    params_cases.each do |params|
      q = IssueQuery.new(:name => '_', :project => project)
      q.build_from_params(ActionController::Parameters.new(params))
      bfp << {
        'kind' => 'issue', 'user' => uid(u), 'project' => p, 'params' => params,
        'filters' => q.filters.map {|f, o| [f, o[:operator], o[:values]]},
        'column_names' => q.column_names&.map(&:to_s), 'columns' => q.columns.map {|c| c.name.to_s},
        'inline_columns' => q.inline_columns.map {|c| c.name.to_s}, 'block_columns' => q.block_columns.map {|c| c.name.to_s},
        'sort_criteria' => q.sort_criteria.to_a, 'group_by' => q.group_by, 'totalable_names' => q.totalable_names.map(&:to_s),
        'display_type' => q.display_type, 'draw_relations' => q.draw_relations, 'draw_progress_line' => q.draw_progress_line,
        'draw_selected_columns' => q.draw_selected_columns, 'valid' => q.valid?, 'ids' => q.issue_ids,
        'as_params_sort' => q.sort_criteria.to_param, 'css' => q.css_classes
      }
    end
  end
  te_params = [
    {}, {'set_filter' => '1'}, {'from' => '2007-03-20'}, {'to' => '2007-03-23'}, {'from' => '2007-03-01', 'to' => '2007-03-31'},
    {'spent_on' => '><2007-03-01|2007-04-30', 'c' => ['project', 'hours'], 't' => ['hours']},
    {'user_id' => 'me'}, {'activity_id' => '9'}, {'set_filter' => '1', 'sort' => 'hours:desc'},
  ]
  [[1, nil], [2, 1]].each do |u, p|
    user = user_of(u)
    User.current = user
    project = p && Project.find(p)
    te_params.each do |params|
      q = TimeEntryQuery.new(:name => '_', :project => project)
      q.build_from_params(ActionController::Parameters.new(params))
      bfp << {
        'kind' => 'time_entry', 'user' => uid(u), 'project' => p, 'params' => params,
        'filters' => q.filters.map {|f, o| [f, o[:operator], o[:values]]},
        'column_names' => q.column_names&.map(&:to_s), 'columns' => q.columns.map {|c| c.name.to_s},
        'sort_criteria' => q.sort_criteria.to_a, 'group_by' => q.group_by, 'totalable_names' => q.totalable_names.map(&:to_s),
        'valid' => q.valid?, 'ids' => q.results_scope.pluck(:id)
      }
    end
  end
  pq_params = [{}, {'set_filter' => '1'}, {'display_type' => 'list'}, {'status' => '*'}, {'id' => 'mine'}, {'set_filter' => '1', 'f' => ['id'], 'op' => {'id' => '='}, 'v' => {'id' => ['bookmarks']}}]
  [1, 2].each do |u|
    user = user_of(u)
    User.current = user
    pq_params.each do |params|
      q = ProjectQuery.new(:name => '_')
      q.build_from_params(ActionController::Parameters.new(params))
      bfp << {
        'kind' => 'project', 'user' => uid(u), 'project' => nil, 'params' => params,
        'filters' => q.filters.map {|f, o| [f, o[:operator], o[:values]]},
        'column_names' => q.column_names&.map(&:to_s), 'columns' => q.columns.map {|c| c.name.to_s},
        'sort_criteria' => q.sort_criteria.to_a, 'group_by' => q.group_by, 'display_type' => q.display_type,
        'valid' => q.valid?, 'ids' => q.results_scope.pluck(:id)
      }
    end
  end
  result['build_from_params'] = bfp

  # 3. 既定クエリの解決
  defaults = []
  scenarios = {
    'none' => -> {},
    'setting' => -> { Setting.default_issue_query = '4'; Setting.default_project_query = '11' },
    'setting_private' => -> { Setting.default_issue_query = '3' },
    'project' => -> { Project.find(1).update_column(:default_issue_query_id, 1); Setting.default_issue_query = '4' },
    'project_private' => -> { Project.find(1).update_column(:default_issue_query_id, 2) },
    'pref' => -> { p = User.find(2).pref; p[:default_issue_query] = '5'; p[:default_project_query] = '12'; p.save!; Setting.default_issue_query = '4' },
    'pref_invisible' => -> { p = User.find(2).pref; p[:default_issue_query] = '3'; p.save!; Setting.default_issue_query = '4' },
    'pref_project_query' => -> { p = User.find(3).pref; p[:default_issue_query] = '2'; p.save! },
  }
  scenarios.each do |name, mutate|
    ActiveRecord::Base.transaction(requires_new: true) do
      mutate.call
      [1, 2, 3, :anon].each do |u|
        user = user_of(u)
        User.current = user
        [nil, 1, 2].each do |p|
          project = p && Project.find(p)
          defaults << {'scenario' => name, 'kind' => 'issue', 'user' => uid(u), 'project' => p,
                       'id' => IssueQuery.default(:project => project, :user => user)&.id}
        end
        defaults << {'scenario' => name, 'kind' => 'project', 'user' => uid(u), 'project' => nil,
                     'id' => ProjectQuery.default(:user => user)&.id}
      end
      Setting.clear_cache
      raise ActiveRecord::Rollback
    end
    Setting.clear_cache
  end
  result['defaults'] = defaults

  # 4. issues の preload 値
  rows = []
  [[1, nil], [2, nil], [3, 1], [:anon, nil]].each do |u, p|
    user = user_of(u)
    User.current = user
    project = p && Project.find(p)
    q = IssueQuery.new(:name => '_', :project => project)
    q.filters = {'status_id' => {:operator => '*', :values => ['']}}
    q.column_names = [:subject, :spent_hours, :total_spent_hours, :last_updated_by, :relations, :last_notes, :cf_2, :cf_1, :watcher_users]
    q.issues.each do |i|
      rows << {
        'user' => uid(u), 'project' => p, 'id' => i.id,
        'spent_hours' => i.spent_hours.to_f, 'total_spent_hours' => i.total_spent_hours.to_f,
        'last_updated_by' => (i.last_updated_by.is_a?(Principal) ? i.last_updated_by.id : nil),
        'relations' => i.relations.map(&:id).sort, 'last_notes' => i.last_notes.to_s,
        'custom_values' => i.custom_values.map {|cv| [cv.custom_field_id, cv.value.to_s]}.sort,
        'watchers' => i.watcher_users.map(&:id).sort
      }
    end
  end
  result['issue_rows'] = rows

  # 5. available_filters_as_json (名前・型・remote・値)
  set_language_if_valid 'en' rescue nil
  ::I18n.locale = :en
  afj = []
  [[1, nil], [2, nil], [2, 1], [3, 1], [:anon, nil], [2, 5]].each do |u, p|
    user = user_of(u)
    User.current = user
    project = p && Project.find(p)
    q = IssueQuery.new(:name => '_', :project => project)
    q.filters = {'status_id' => {:operator => 'o', :values => ['']}, 'assigned_to_id' => {:operator => '=', :values => ['5']},
                 'project_id' => {:operator => '=', :values => ['1']}}
    afj << {'kind' => 'issue', 'user' => uid(u), 'project' => p, 'filters' => q.available_filters_as_json, 'keys' => q.available_filters.keys}
    q = TimeEntryQuery.new(:name => '_', :project => project)
    afj << {'kind' => 'time_entry', 'user' => uid(u), 'project' => p, 'filters' => q.available_filters_as_json, 'keys' => q.available_filters.keys}
  end
  [1, 2].each do |u|
    User.current = user_of(u)
    q = ProjectQuery.new(:name => '_')
    afj << {'kind' => 'project', 'user' => uid(u), 'project' => nil, 'filters' => q.available_filters_as_json, 'keys' => q.available_filters.keys}
  end
  User.current = User.find(1)
  q = UserQuery.new(:name => '_')
  afj << {'kind' => 'user', 'user' => '1', 'project' => nil, 'filters' => q.available_filters_as_json, 'keys' => q.available_filters.keys}
  result['filters_json'] = afj

  # 6. 演算子ラベル・列の見出し
  User.current = User.find(1)
  result['operators_labels'] = Query.operators_labels
  result['column_captions'] = IssueQuery.new(:name => '_').available_columns.map {|c| [c.name.to_s, c.caption.to_s, c.inline?, c.sortable?, c.groupable?.present?, c.totalable.present?, c.default_order]}
  result['te_column_captions'] = TimeEntryQuery.new(:name => '_').available_columns.map {|c| [c.name.to_s, c.caption.to_s, c.inline?, c.sortable?, c.groupable?.present?, c.totalable.present?, c.default_order]}

  # 7. journals / versions
  jv = []
  [[1, nil], [2, nil], [2, 1], [3, 1], [:anon, nil], [1, 5]].each do |u, p|
    user = user_of(u)
    User.current = user
    project = p && Project.find(p)
    [{'status_id' => {:operator => '*', :values => ['']}}, {'status_id' => {:operator => 'o', :values => ['']}},
     {'tracker_id' => {:operator => '=', :values => ['2']}}].each do |f|
      q = IssueQuery.new(:name => '_', :project => project)
      q.filters = f
      jv << {'user' => uid(u), 'project' => p, 'filters' => f.map {|k, o| [k, o[:operator], o[:values]]},
             'journals' => q.journals(:order => "#{Journal.table_name}.id DESC").map(&:id),
             'versions' => q.versions.map(&:id).sort}
    end
  end
  result['journals_versions'] = jv

  raise ActiveRecord::Rollback
end

Zlib::GzipWriter.open(out_path) {|gz| gz.write(JSON.generate(result))}
puts "written to #{out_path}"
