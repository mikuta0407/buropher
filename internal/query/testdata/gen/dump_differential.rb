# Redmine 7.0.1 (公式フィクスチャ投入済み DB) でクエリのフィルタ・演算子・値・ソート・グループ・合計の
# 組み合わせを評価し、結果 (チケット等の id の並び・件数・グループ別件数・合計) を JSON (gzip) に書き出す。
#
#   internal/query/testdata/gen/runner.sh <DB のコピー> internal/query/testdata/gen/dump_differential.rb <出力 .json.gz>
#
# runner.sh は時刻を 2026-01-15 12:00 UTC に固定する (フィクスチャの相対日時と「今日」をそろえるため)。
# 各シナリオはトランザクション内でデータを変更して評価し、最後にロールバックする。
# Go 側 (internal/query/differential_test.go) は同じフィクスチャを同じ基準時刻で投入し、
# シナリオを SQL で再現して同じケースを評価・比較する。
require 'json'
require 'zlib'

out_path = File.expand_path(ARGV[0] || abort('usage: dump_differential.rb OUTPUT.json.gz'), ENV['ORIG_PWD'] || Dir.pwd)

KLASSES = {
  'issue' => IssueQuery, 'time_entry' => TimeEntryQuery, 'project' => ProjectQuery,
  'project_admin' => ProjectAdminQuery, 'user' => UserQuery
}

NO_VALUE_OPS = %w(o c !* * nd t ld nw w lw l2w nm m lm y *o !o)

def norm_key(k)
  case k
  when nil then nil
  when ActiveRecord::Base then k.id.to_s
  when Date then k.iso8601
  when Time, ActiveSupport::TimeWithZone then k.to_date.iso8601
  when true, false then k.to_s
  when BigDecimal then (k == k.to_i ? k.to_i.to_s : k.to_s('F'))
  when Integer then k.to_s
  when Float then k.to_s
  else k.to_s
  end
end

def norm_num(v)
  v.nil? ? nil : v.to_f
end

def build_query(kind, user, project)
  User.current = user
  q = KLASSES[kind].new(:name => '_')
  q.project = project if project && %w(issue time_entry).include?(kind)
  q
end

def evaluate(kind, user, project, filters, opts = {})
  q = build_query(kind, user, project)
  q.filters = {} if filters
  (filters || []).each {|f, op, v| q.add_filter(f, op, v)}
  q.sort_criteria = opts[:sort] if opts[:sort]
  q.group_by = opts[:group_by] if opts[:group_by]
  q.totalable_names = opts[:totals] if opts[:totals]
  rec = {
    'kind' => kind, 'user' => user.id, 'anonymous' => user.anonymous?, 'project' => project&.id,
    'filters' => filters, 'sort' => opts[:sort], 'group_by' => opts[:group_by], 'totals' => opts[:totals],
    'applied' => q.filters.keys
  }
  begin
    rec['valid'] = q.valid?
    if kind == 'issue'
      rec['ids'] = q.issue_ids
      rec['count'] = q.issue_count
    else
      rec['ids'] = q.results_scope.pluck(:id)
      rec['count'] = q.base_scope.count
    end
    if opts[:group_by]
      rec['grouped'] = q.grouped?
      if q.grouped?
        rec['groups'] = q.result_count_by_group.map {|k, v| [norm_key(k), v]}.sort_by {|k, _| k.to_s}
      end
    end
    if opts[:totals]
      rec['total_values'] = {}
      rec['group_totals'] = {}
      q.totalable_columns.each do |c|
        rec['total_values'][c.name.to_s] = norm_num(q.total_for(c))
        if q.grouped?
          rec['group_totals'][c.name.to_s] = q.total_by_group_for(c).map {|k, v| [norm_key(k), norm_num(v)]}.sort_by {|k, _| k.to_s}
        end
      end
    end
  rescue => e
    rec['error'] = "#{e.class.name}: #{e.message[0, 300]}"
  end
  rec
end

TEXT_VALUES = ['issue', 'Issue', 'cannot print', '"cannot print"', 'recipe', 'a', '%', '_', 'e', 'search', 'Can', 'Some notes', 'note', 'error', 'x y', 'Ψ']
DATE_VALUES = [['2026-01-14'], ['2026-01-15'], ['2026-01-16'], ['2025-12-31'], ['2006-07-19'], ['2026-01-15T10:00:00Z'], ['2026-02-30'], ['abc']]
DAYS_VALUES = [['0'], ['1'], ['3'], ['10'], ['400'], ['x']]
INT_VALUES = [['1'], ['30'], ['1,2'], ['0'], ['-1'], ['12'], ['5'], ['abc'], ['']]
FLOAT_VALUES = [['2.1'], ['0'], ['-7.6'], ['11.65'], ['5'], ['0.5'], ['200'], ['1.'], ['abc']]
# Redmine 7.0 の :hour 型 (String#to_hours) 用の追加値
HOUR_VALUES = FLOAT_VALUES + [['0:30'], ['4:30'], ['1h30'], ['2h'], ['45m'], ['1:2:3']]
ID_VALUES = [['1'], ['2'], ['3'], ['7'], ['9'], ['10'], ['14'], ['1,2'], ['1 3'], ['999'], ['abc']]

# フィルタ型と演算子ごとの値の候補
def candidates(q, field, filter, op)
  return [['']] if NO_VALUE_OPS.include?(op)

  type = filter[:type]
  case type
  when :date, :date_past
    case op
    when '=', '>=', '<=' then DATE_VALUES
    when '><' then [['2026-01-01', '2026-01-14'], ['2026-01-14', '2026-01-16'], ['2006-01-01', '2006-12-31'], ['2026-01-14', '']]
    else DAYS_VALUES
    end
  when :string, :text, :search
    if %w(= !).include?(op)
      [['Cannot print recipes'], ['issue'], ['125'], ['MySQL'], ['01 42 50 00 00'], ['alpha']]
    else
      TEXT_VALUES.map {|v| [v]}
    end
  when :integer
    op == '><' ? [['0', '50'], ['1', '3'], ['-5', '5']] : INT_VALUES
  when :float
    op == '><' ? [['1', '10'], ['-10', '3'], ['0', '0']] : FLOAT_VALUES
  when :hour
    op == '><' ? [['1', '10'], ['-10', '3'], ['0', '0'], ['0:30', '4:30'], ['1h', ''], ['1h', 'x']] : HOUR_VALUES
  when :relation
    %w(=p =!p !p).include?(op) ? [['1'], ['2'], ['3'], ['5']] : ID_VALUES
  when :tree
    ID_VALUES
  else
    vals = (filter.values || []).map {|v| v.is_a?(Array) ? v[1].to_s : v.to_s}
    singles = vals.first(8).map {|v| [v]}
    extra = []
    extra << vals.first(2) if vals.size >= 2
    extra << vals.last(3) if vals.size >= 4
    extra << ['me'] if %w(assigned_to_id author_id user_id watcher_id updated_by last_updated_by).include?(field) || filter[:field]&.field_format == 'user'
    extra << ['mine'] << ['bookmarks'] if field == 'project_id' || (q.is_a?(ProjectQuery) && %w(id parent_id).include?(field))
    extra << ['999']
    extra << ['3'] << ['1', '5'] if %w(member_of_group author.group user.group is_member_of_group).include?(field)
    extra << ['10'] << ['11'] << ['10', '11'] << ['12'] if %w(member_of_group author.group user.group is_member_of_group).include?(field)
    singles + extra
  end
end

def filter_cases(kind, user, project, limit_ops = nil)
  q = build_query(kind, user, project)
  recs = []
  q.available_filters.each do |field, filter|
    ops = Query.operators_by_filter_type[filter[:type]] || []
    ops = ops & limit_ops if limit_ops
    ops.each do |op|
      candidates(q, field, filter, op).each do |vals|
        recs << evaluate(kind, user, project, [[field, op, vals]])
      end
    end
  end
  recs
end

def sort_group_cases(kind, user, project)
  q = build_query(kind, user, project)
  recs = []
  all_filter = kind == 'issue' ? [['status_id', '*', ['']]] : nil
  q.available_columns.each do |c|
    next unless c.sortable?
    %w(asc desc).each do |o|
      recs << evaluate(kind, user, project, all_filter, :sort => [[c.name.to_s, o]])
      recs << evaluate(kind, user, project, all_filter, :sort => [[c.name.to_s, o], ['id', 'asc']]) if kind == 'issue'
    end
  end
  totals = q.available_totalable_columns.map {|c| c.name.to_s}
  q.available_columns.each do |c|
    next unless c.groupable?
    recs << evaluate(kind, user, project, all_filter, :group_by => c.name.to_s, :totals => totals)
    recs << evaluate(kind, user, project, all_filter, :group_by => c.name.to_s, :sort => [['subject', 'desc']], :totals => totals) if kind == 'issue'
  end
  recs << evaluate(kind, user, project, all_filter, :totals => totals)
  recs
end

def multi_filter_cases(user, project)
  recs = []
  [
    [['status_id', 'o', ['']], ['tracker_id', '=', ['1']]],
    [['status_id', 'c', ['']], ['assigned_to_id', '!*', ['']]],
    [['status_id', '*', ['']], ['subject', '~', ['issue']], ['priority_id', '!', ['4']]],
    [['status_id', '*', ['']], ['cf_1', '=', ['MySQL']], ['cf_2', '=', ['125']]],
    [['status_id', '=', ['1', '2']], ['author_id', '=', ['me']]],
    [['status_id', '*', ['']], ['updated_on', '>t-', ['3']], ['done_ratio', '>=', ['0']]],
    [['status_id', 'o', ['']], ['subproject_id', '=', ['3']]],
    [['status_id', 'o', ['']], ['subproject_id', '!', ['3']]],
    [['status_id', 'o', ['']], ['subproject_id', '!*', ['']]],
    [['status_id', 'o', ['']], ['subproject_id', '*', ['']]],
    [['status_id', 'o', ['']], ['any_searchable', '~', ['issue']], ['project_id', '=', ['1']]],
    [['status_id', 'o', ['']], ['any_searchable', '*~', ['recipe cannot']], ['project_id', '=', ['mine']]],
    [['status_id', '*', ['']], ['estimated_hours', '><', ['abc', '1']]],
    [['status_id', '*', ['']], ['subject', '~', ['']]],
  ].each do |f|
    recs << evaluate('issue', user, project, f)
  end
  recs
end

ISSUE_COMBOS = [[1, nil], [2, nil], [2, 1], [3, 1], [:anon, nil], [8, nil], [1, 5], [2, 5]]
TE_COMBOS = [[1, nil], [2, nil], [2, 1], [:anon, nil], [3, 1]]
PROJECT_COMBOS = [[1, nil], [2, nil], [:anon, nil]]

def user_of(u)
  u == :anon ? User.anonymous : User.find(u)
end

def all_cases(scenario, full)
  recs = []
  ISSUE_COMBOS.each_with_index do |(u, p), i|
    user = user_of(u)
    project = p && Project.find(p)
    recs.concat(filter_cases('issue', user, project, (full || i < 3) ? nil : %w(= ! ~ * !* o c >= ><t- t w)))
    recs.concat(sort_group_cases('issue', user, project)) if full || i < 4
    recs.concat(multi_filter_cases(user, project))
  end
  TE_COMBOS.each do |u, p|
    user = user_of(u)
    project = p && Project.find(p)
    recs.concat(filter_cases('time_entry', user, project))
    recs.concat(sort_group_cases('time_entry', user, project))
  end
  PROJECT_COMBOS.each do |u, _|
    user = user_of(u)
    recs.concat(filter_cases('project', user, nil))
    recs.concat(sort_group_cases('project', user, nil))
  end
  recs.concat(filter_cases('project_admin', User.find(1), nil))
  recs.concat(sort_group_cases('project_admin', User.find(1), nil))
  recs.concat(filter_cases('user', User.find(1), nil))
  recs.concat(sort_group_cases('user', User.find(1), nil))
  recs.each {|r| r['scenario'] = scenario}
  recs
end

# ---------------------------------------------------------------- シナリオ

def sql(s)
  ActiveRecord::Base.connection.execute(s)
end

def yaml_list(a)
  a.to_yaml
end

# rich: チケットの親子・各種カスタムフィールド・非公開注記・グループのウォッチャー・タイムゾーン等を追加する。
# Go 側の applyRichScenario と同じ内容であること。
def apply_rich
  # 親子: 1 -> (2 -> 7), 3
  sql("UPDATE issues SET parent_id = NULL, root_id = 1, lft = 1, rgt = 8 WHERE id = 1")
  sql("UPDATE issues SET parent_id = 1, root_id = 1, lft = 2, rgt = 5 WHERE id = 2")
  sql("UPDATE issues SET parent_id = 2, root_id = 1, lft = 3, rgt = 4 WHERE id = 7")
  sql("UPDATE issues SET parent_id = 1, root_id = 1, lft = 6, rgt = 7 WHERE id = 3")
  sql("UPDATE issues SET assigned_to_id = 10 WHERE id = 5")
  sql("UPDATE issues SET assigned_to_id = 3, estimated_hours = 4.5, done_ratio = 50 WHERE id = 13")
  sql("UPDATE issues SET is_private = 1, assigned_to_id = 2 WHERE id = 9")
  sql("UPDATE issues SET description = 'Notes about printing' WHERE id = 6")
  cfs = [
    # id, type, name, format, possible_values, is_for_all, is_filter, visible, multiple, searchable, position
    [20, 'IssueCustomField', 'Int field', 'int', nil, 1, 1, 1, 0, 0, 6],
    [21, 'IssueCustomField', 'Milestone', 'version', nil, 1, 1, 1, 0, 0, 7],
    [22, 'IssueCustomField', 'Reviewer', 'user', nil, 1, 1, 1, 1, 0, 8],
    [23, 'VersionCustomField', 'Release date', 'date', nil, 0, 1, 1, 0, 0, 1],
    [24, 'UserCustomField', 'Team', 'string', nil, 0, 1, 1, 0, 0, 3],
    [25, 'IssueCustomField', 'Urgent', 'bool', nil, 1, 1, 0, 0, 0, 9],
    [26, 'IssueCustomField', 'Deadline', 'date', nil, 1, 1, 1, 0, 0, 10],
    [27, 'ProjectCustomField', 'Budget', 'int', nil, 0, 1, 1, 0, 0, 2],
    [28, 'IssueCustomField', 'Labels', 'list', %w(a b c), 1, 1, 1, 1, 1, 11],
    [29, 'TimeEntryCustomField', 'Note', 'string', nil, 0, 1, 1, 0, 0, 2],
  ]
  cfs.each do |id, type, name, format, pv, all, filter, visible, multiple, searchable, pos|
    pvs = pv ? ActiveRecord::Base.connection.quote(yaml_list(pv)) : 'NULL'
    sql("INSERT INTO custom_fields (id, type, name, field_format, possible_values, regexp, is_required, is_for_all, is_filter, position, searchable, default_value, editable, visible, multiple, format_store) " \
        "VALUES (#{id}, '#{type}', '#{name}', '#{format}', #{pvs}, '', 0, #{all}, #{filter}, #{pos}, #{searchable}, '', 1, #{visible}, #{multiple}, #{ActiveRecord::Base.connection.quote({}.to_yaml)})")
  end
  [20, 21, 22, 25, 28].each {|cf| [1, 2, 3].each {|t| sql("INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (#{cf}, #{t})")}}
  [1, 3].each {|t| sql("INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (26, #{t})")}
  sql("INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (25, 1)")
  values = [
    [20, 'Issue', 1, '5'], [20, 'Issue', 2, '12'], [20, 'Issue', 3, ''], [20, 'Issue', 7, '-3'], [20, 'Issue', 5, '5'],
    [21, 'Issue', 1, '2'], [21, 'Issue', 3, '3'], [21, 'Issue', 9, '6'],
    [22, 'Issue', 1, '2'], [22, 'Issue', 1, '3'], [22, 'Issue', 2, '3'], [22, 'Issue', 6, '2'],
    [23, 'Version', 2, '2026-01-20'], [23, 'Version', 3, '2026-01-15'], [23, 'Version', 6, '2025-12-01'],
    [24, 'Principal', 2, 'alpha'], [24, 'Principal', 3, 'beta'], [24, 'Principal', 4, 'Alpha team'],
    [25, 'Issue', 1, '1'], [25, 'Issue', 2, '0'], [25, 'Issue', 3, '1'], [25, 'Issue', 14, '1'],
    [26, 'Issue', 1, '2026-01-14'], [26, 'Issue', 3, '2026-01-16'], [26, 'Issue', 6, '2026-01-20'], [26, 'Issue', 7, '2025-12-31'], [26, 'Issue', 13, '2026-02-01'],
    [27, 'Project', 1, '1000'], [27, 'Project', 2, '50'], [27, 'Project', 5, '1000'],
    [28, 'Issue', 1, 'a'], [28, 'Issue', 1, 'b'], [28, 'Issue', 2, 'b'], [28, 'Issue', 3, 'c'], [28, 'Issue', 5, 'a'],
    [29, 'TimeEntry', 1, 'billable'], [29, 'TimeEntry', 2, 'internal'],
  ]
  values.each_with_index do |(cf, type, id, v), i|
    sql("INSERT INTO custom_values (id, customized_type, customized_id, custom_field_id, value) VALUES (#{100 + i}, '#{type}', #{id}, #{cf}, '#{v}')")
  end
  sql("INSERT INTO journals (id, journalized_id, journalized_type, user_id, notes, created_on, private_notes, updated_on) VALUES (6, 1, 'Issue', 3, 'secret note', '2026-01-14 10:00:00', 1, '2026-01-14 10:00:00')")
  sql("INSERT INTO journals (id, journalized_id, journalized_type, user_id, notes, created_on, private_notes, updated_on) VALUES (7, 3, 'Issue', 2, 'public reply', '2026-01-15 09:00:00', 0, '2026-01-15 09:00:00')")
  sql("INSERT INTO journals (id, journalized_id, journalized_type, user_id, notes, created_on, private_notes, updated_on) VALUES (8, 13, 'Issue', 4, '', '2026-01-13 09:00:00', 0, '2026-01-13 09:00:00')")
  sql("INSERT INTO journal_details (id, journal_id, property, prop_key, old_value, value) VALUES (7, 6, 'attr', 'status_id', '2', '1')")
  sql("INSERT INTO journal_details (id, journal_id, property, prop_key, old_value, value) VALUES (8, 7, 'attr', 'assigned_to_id', '2', '3')")
  sql("INSERT INTO journal_details (id, journal_id, property, prop_key, old_value, value) VALUES (9, 8, 'attr', 'priority_id', '6', '4')")
  sql("INSERT INTO watchers (watchable_type, watchable_id, user_id) VALUES ('Issue', 3, 10)")
  sql("INSERT INTO watchers (watchable_type, watchable_id, user_id) VALUES ('Issue', 5, 2)")
  sql("INSERT INTO watchers (watchable_type, watchable_id, user_id) VALUES ('Issue', 9, 8)")
  sql("INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type) VALUES (3, 1, 7, 'precedes')")
  sql("INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type) VALUES (4, 13, 14, 'duplicates')")
  sql("INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type) VALUES (5, 3, 8, 'blocks')")
  sql("INSERT INTO time_entries (id, project_id, user_id, author_id, issue_id, hours, comments, activity_id, spent_on, tyear, tmonth, tweek, created_on, updated_on) " \
      "VALUES (6, 1, 2, 2, 7, 3.5, 'work on child', 9, '2026-01-14', 2026, 1, 3, '2026-01-14 08:00:00', '2026-01-14 08:00:00')")
  sql("UPDATE user_preferences SET time_zone = 'Hawaii' WHERE user_id = 2")
  sql("UPDATE user_preferences SET time_zone = 'Nuku''alofa' WHERE user_id = 3")
  sql("UPDATE projects SET status = 5 WHERE id = 3")
  User.reset_column_information
  User.all.each {|u| u.pref.reload rescue nil}
end

all = []
ActiveRecord::Base.transaction do
  all.concat(all_cases('base', true))
  raise ActiveRecord::Rollback
end
ActiveRecord::Base.transaction do
  apply_rich
  User.current = nil
  all.concat(all_cases('rich', false))
  raise ActiveRecord::Rollback
end

Zlib::GzipWriter.open(out_path) {|gz| gz.write(JSON.generate({'cases' => all}))}
puts "#{all.size} cases written to #{out_path}"
