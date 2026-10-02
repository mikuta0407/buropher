puts Date.today; puts Time.now
User.current=User.find(2)
q=IssueQuery.new(name:"_")
puts q.issue_ids.inspect
puts Setting.display_subprojects_issues?
puts q.available_filters.keys.inspect
puts q.available_columns.map(&:name).inspect
puts Setting.issue_list_default_columns.inspect
puts Setting.user_format.inspect
