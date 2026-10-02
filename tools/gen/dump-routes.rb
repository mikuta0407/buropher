# Redmine の全ルートを JSON で出力する。
# 使い方: cd <redmine> && SECRET_KEY_BASE=x RAILS_ENV=production bin/rails runner <this file>
require 'json'
routes = Rails.application.routes.routes.map do |r|
  next if r.app.respond_to?(:engine?) && r.app.engine?
  {
    name: r.name,
    verb: r.verb,
    path: r.path.spec.to_s,
    controller: r.defaults[:controller],
    action: r.defaults[:action],
    defaults: r.defaults.except(:controller, :action),
    requirements: r.requirements.except(:controller, :action).transform_values(&:to_s)
  }
end.compact
puts JSON.pretty_generate(routes)
