# frozen_string_literal: true

# internal/view/rails のゴールデンテスト用フィクスチャを、実際の Redmine 7.0.1（Rails 8.1）の
# ActionView ヘルパーで生成するスクリプト。
#
# 使い方（Redmine のルートで rails runner として実行。DB へは書き込まない）:
#   cd _reference/redmine7-migrated
#   SECRET_KEY_BASE=x RAILS_ENV=production bin/rails runner \
#     /path/to/buropher/tools/golden/rails_helpers.rb /path/to/buropher/internal/view/rails/testdata
#
# 入力: <testdata>/cases.json, <testdata>/models.json
# 出力: <testdata>/golden.json（[{id, output}]）
#
# 再現性のため以下をスタブする:
#   - CSRF: protect_against_forgery? = true, form_authenticity_token = "TOKEN"
#   - Redmine の form_tag_html が付与する name 属性の乱数部: SecureRandom.hex => "abcd1234"
#   - アセットパス: "/assets/<stem>-DIGEST<ext>"

require 'json'
require 'ostruct'
require 'securerandom'

dir = ARGV[0] or abort "usage: rails runner rails_helpers.rb <testdata dir>"
cases = JSON.parse(File.read(File.join(dir, 'cases.json')))
models = JSON.parse(File.read(File.join(dir, 'models.json')))

def SecureRandom.hex(*) = "abcd1234"

I18n.locale = :en

def make_view
  c = ApplicationController.new
  c.request = ActionDispatch::TestRequest.create
  c.response = ActionDispatch::TestResponse.new
  v = c.view_context
  def v.protect_against_forgery? = true
  def v.form_authenticity_token(*, **) = "TOKEN"
  def v.compute_asset_path(source, options = {})
    "/assets/" + source.sub(/(\.[^.\/]+)\z/, '-DIGEST\1')
  end
  def v.current_theme = nil
  v
end

def make_model(spec)
  attrs = spec['attrs']
  name = spec['name'].camelize
  persisted = spec['persisted']
  id = spec['id']
  klass = Class.new do
    include ActiveModel::Model
    attr_accessor(*attrs.keys)
    define_method(:persisted?) { persisted }
    define_method(:id) { id }
  end
  klass.define_singleton_method(:model_name) { ActiveModel::Name.new(klass, nil, name) }
  klass.define_singleton_method(:name) { name }
  obj = klass.new
  attrs.each { |k, val| obj.public_send("#{k}=", val) }
  spec['errors'].each { |k, msgs| msgs.each { |m| obj.errors.add(k.to_sym, m) } }
  obj
end

def conv(x, view, models)
  case x
  when Array
    x.map { |e| conv(e, view, models) }
  when Hash
    if x.key?('$safe')
      x['$safe'].html_safe
    elsif x.key?('$sym')
      x['$sym'].to_sym
    elsif x.key?('$obj')
      OpenStruct.new(x['$obj'])
    elsif x.key?('$model')
      make_model(models.fetch(x['$model']))
    elsif x.key?('$call')
      call = x['$call']
      view.public_send(call['fn'], *conv(call['args'], view, models))
    else
      x.each_with_object({}) { |(k, val), h| h[k.to_sym] = conv(val, view, models) }
    end
  else
    x
  end
end

def run_fn(view, fn, args)
  case fn
  when 'form_for', 'labelled_form_for'
    _name, model, opts = args
    view.public_send(fn, model, opts || {}) { |_f| "" }
  when 'field_set_tag'
    legend, opts, content = args
    view.field_set_tag(legend, opts) { content }
  else
    view.send(fn, *args)
  end
end

results = cases.map do |c|
  view = make_view
  out =
    begin
      if c['seq']
        c['seq'].filter_map do |s|
          o = run_fn(view, s['fn'], conv(s['args'], view, models)).to_s
          o unless s['discard']
        end.join("\n")
      elsif c['builder']
        b = c['builder']
        model = make_model(models.fetch(b['model']))
        klass = b['plain'] ? ActionView::Helpers::FormBuilder : Redmine::Views::LabelledFormBuilder
        builder = klass.new(b['name'], model, view, {skip_default_ids: false, allow_method_names_outside_object: false})
        builder.public_send(c['fn'], *conv(c['args'], view, models)).to_s
      else
        run_fn(view, c['fn'], conv(c['args'], view, models))
      end
    rescue => e
      warn "#{c['id']}: #{e.class}: #{e.message}"
      "!ERROR #{e.class}"
    end
  { 'id' => c['id'], 'output' => out.nil? ? nil : out.to_s, 'safe' => out.respond_to?(:html_safe?) && out.html_safe? }
end

File.write(File.join(dir, 'golden.json'), JSON.pretty_generate(results) + "\n")
puts "wrote #{results.size} cases"
