# Redmine の AccessControl 定義を JSON で出力する。
# 使い方: cd <redmine> && SECRET_KEY_BASE=x RAILS_ENV=production bin/rails runner <this file>
require 'json'
perms = Redmine::AccessControl.permissions.map do |p|
  {
    name: p.name.to_s,
    module: p.project_module&.to_s,
    actions: p.actions,
    public: p.public?,
    require: p.instance_variable_get(:@require)&.to_s,
    read: p.read?
  }
end
puts JSON.pretty_generate(perms)
